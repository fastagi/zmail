package mailbox

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/htmlindex"

	"github.com/fastagi/zmail/api/internal/domain"
)

const (
	maxMIMEDepth = 6
	maxMIMEParts = 100
	// maxMIMEPartBytes bounds how much of one part is decoded before truncation to the stored limit.
	maxMIMEPartBytes = 4 * domain.MaxMailBodyBytes
)

// parsedMail is the subset of an RFC 5322 message the mailbox stores.
type parsedMail struct {
	FromAddress string
	FromName    string
	To          []string
	Cc          []string
	Subject     string
	MessageID   string
	InReplyTo   string
	TextBody    string
	HTMLBody    string
}

var headerDecoder = &mime.WordDecoder{CharsetReader: charsetReader}

// parseMail reads the headers and the first text and HTML bodies of a raw message.
// Attachments are skipped. Every returned string is valid UTF-8 without NUL bytes.
func parseMail(raw []byte) (*parsedMail, error) {
	message, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, ErrMailInboundUnparseable
	}
	parsed := &parsedMail{
		Subject: truncateText(decodeHeader(message.Header.Get("Subject")), domain.MaxMailSubjectLength),
		To:      headerAddresses(message.Header, "To"),
		Cc:      headerAddresses(message.Header, "Cc"),
	}
	parsed.Subject = strings.Join(strings.Fields(parsed.Subject), " ")
	if id, ok := normalizeMessageID(message.Header.Get("Message-Id")); ok {
		parsed.MessageID = id
	}
	if id, ok := normalizeMessageID(firstField(message.Header.Get("In-Reply-To"))); ok {
		parsed.InReplyTo = id
	}

	parser := mail.AddressParser{WordDecoder: headerDecoder}
	if from, err := parser.Parse(message.Header.Get("From")); err == nil {
		parsed.FromAddress = strings.ToLower(from.Address)
		parsed.FromName = truncateText(cleanText(from.Name), 255)
	}

	walker := &mimeWalker{}
	walker.walk(textproto(message.Header), message.Body, 0)
	parsed.TextBody = truncateText(walker.text, domain.MaxMailBodyBytes)
	parsed.HTMLBody = truncateText(walker.html, domain.MaxMailBodyBytes)
	return parsed, nil
}

type partHeader interface {
	Get(key string) string
}

type mailHeader mail.Header

func (h mailHeader) Get(key string) string { return mail.Header(h).Get(key) }

func textproto(header mail.Header) partHeader { return mailHeader(header) }

type mimeWalker struct {
	text  string
	html  string
	parts int
}

func (w *mimeWalker) walk(header partHeader, body io.Reader, depth int) {
	if depth > maxMIMEDepth || w.parts >= maxMIMEParts {
		return
	}
	w.parts++

	mediaType, params, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil {
		mediaType, params = "text/plain", map[string]string{}
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return
		}
		reader := multipart.NewReader(body, boundary)
		for w.parts < maxMIMEParts {
			// NextRawPart leaves Content-Transfer-Encoding for decodeBody to handle.
			part, err := reader.NextRawPart()
			if err != nil {
				return
			}
			w.walk(part.Header, part, depth+1)
		}
		return
	}

	if disposition, _, err := mime.ParseMediaType(header.Get("Content-Disposition")); err == nil && disposition == "attachment" {
		return
	}
	switch mediaType {
	case "text/plain":
		if w.text == "" {
			w.text = decodeBody(header, params, body)
		}
	case "text/html":
		if w.html == "" {
			w.html = decodeBody(header, params, body)
		}
	}
}

func decodeBody(header partHeader, params map[string]string, body io.Reader) string {
	var decoded io.Reader
	switch strings.ToLower(strings.TrimSpace(header.Get("Content-Transfer-Encoding"))) {
	case "base64":
		decoded = base64.NewDecoder(base64.StdEncoding, newlineStripper{body})
	case "quoted-printable":
		decoded = quotedprintable.NewReader(body)
	default:
		decoded = body
	}
	if label := strings.TrimSpace(params["charset"]); label != "" {
		if converted, err := charsetReader(label, decoded); err == nil {
			decoded = converted
		}
	}
	// A part that breaks off mid-stream still yields the text decoded so far.
	raw, err := io.ReadAll(io.LimitReader(decoded, maxMIMEPartBytes))
	if err != nil && len(raw) == 0 {
		return ""
	}
	return cleanText(string(raw))
}

// newlineStripper removes line breaks that the base64 decoder would reject mid-stream.
type newlineStripper struct {
	source io.Reader
}

func (s newlineStripper) Read(buffer []byte) (int, error) {
	for {
		read, err := s.source.Read(buffer)
		kept := 0
		for _, char := range buffer[:read] {
			if char != '\r' && char != '\n' && char != ' ' && char != '\t' {
				buffer[kept] = char
				kept++
			}
		}
		if kept > 0 || err != nil {
			return kept, err
		}
	}
}

func charsetReader(label string, input io.Reader) (io.Reader, error) {
	label = strings.ToLower(strings.TrimSpace(label))
	if label == "" || label == "utf-8" || label == "utf8" || label == "us-ascii" || label == "ascii" {
		return input, nil
	}
	encoding, err := htmlindex.Get(label)
	if err != nil {
		return nil, err
	}
	return encoding.NewDecoder().Reader(input), nil
}

func decodeHeader(raw string) string {
	decoded, err := headerDecoder.DecodeHeader(raw)
	if err != nil {
		decoded = raw
	}
	return cleanText(decoded)
}

func headerAddresses(header mail.Header, key string) []string {
	addresses := []string{}
	raw := header.Get(key)
	if raw == "" {
		return addresses
	}
	parser := mail.AddressParser{WordDecoder: headerDecoder}
	list, err := parser.ParseList(raw)
	if err != nil {
		return addresses
	}
	for _, entry := range list {
		if len(addresses) >= domain.MaxMailRecipients {
			break
		}
		addresses = append(addresses, strings.ToLower(entry.Address))
	}
	return addresses
}

func firstField(raw string) string {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// cleanText makes a string safe for PostgreSQL text columns.
func cleanText(value string) string {
	value = strings.ToValidUTF8(value, "�")
	return strings.ReplaceAll(value, "\x00", "")
}

// truncateText cuts at a rune boundary so the result stays valid UTF-8.
func truncateText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}
