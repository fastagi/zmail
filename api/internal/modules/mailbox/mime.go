package mailbox

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strconv"
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
	// maxMailReferences bounds the References ids stored for a message and sent with a reply.
	maxMailReferences = 20
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
	// References are the parsed References header ids, oldest first.
	References []string
	TextBody   string
	HTMLBody   string
	// Attachments are the files of the message, at most domain.MaxMailAttachments of them.
	Attachments []domain.MailAttachment
}

var headerDecoder = &mime.WordDecoder{CharsetReader: charsetReader}

// parseMail reads the headers, the first text and HTML bodies, and the attached files of a raw
// message. Every returned string is valid UTF-8 without NUL bytes.
func parseMail(raw []byte) (*parsedMail, error) {
	return readMail(raw, false)
}

// parseSubmission reads a message a mail app submits. Unlike parseMail it cuts nothing short: a
// subject, body, or set of attachments over its limit is a *domain.MailClientSubmitError of kind
// too_large naming the limit, and a message that cannot be read one of kind invalid.
func parseSubmission(raw []byte) (*parsedMail, error) {
	if len(raw) > domain.MailClientMaxSubmissionBytes {
		return nil, &domain.MailClientSubmitError{Kind: domain.MailClientSubmitTooLarge, Reason: "The message exceeds 8 MiB"}
	}
	return readMail(raw, true)
}

func readMail(raw []byte, strict bool) (*parsedMail, error) {
	message, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		if strict {
			return nil, &domain.MailClientSubmitError{Kind: domain.MailClientSubmitInvalid, Reason: "The message could not be read"}
		}
		return nil, ErrMailInboundUnparseable
	}
	subject := strings.Join(strings.Fields(decodeHeader(message.Header.Get("Subject"))), " ")
	if strict && utf8.RuneCountInString(subject) > domain.MaxMailSubjectLength {
		return nil, &domain.MailClientSubmitError{Kind: domain.MailClientSubmitTooLarge, Reason: "The subject exceeds 998 characters"}
	}
	parsed := &parsedMail{
		Subject: strings.TrimSpace(truncateRunes(subject, domain.MaxMailSubjectLength)),
		To:      headerAddresses(message.Header, "To"),
		Cc:      headerAddresses(message.Header, "Cc"),
	}
	if id, ok := normalizeMessageID(message.Header.Get("Message-Id")); ok {
		parsed.MessageID = id
	}
	if id, ok := normalizeMessageID(firstField(message.Header.Get("In-Reply-To"))); ok {
		parsed.InReplyTo = id
	}
	parsed.References = parseReferences(message.Header.Get("References"))

	parser := mail.AddressParser{WordDecoder: headerDecoder}
	if from, err := parser.Parse(message.Header.Get("From")); err == nil {
		parsed.FromAddress = strings.ToLower(from.Address)
		parsed.FromName = truncateText(cleanText(from.Name), 255)
	}

	walker := &mimeWalker{strict: strict}
	walker.walk(textproto(message.Header), message.Body, 0, false)
	if walker.violation != nil {
		return nil, walker.violation
	}
	parsed.TextBody = truncateText(walker.text, domain.MaxMailBodyBytes)
	parsed.HTMLBody = truncateText(walker.html, domain.MaxMailBodyBytes)
	parsed.Attachments = walker.attachments
	return parsed, nil
}

type partHeader interface {
	Get(key string) string
}

type mailHeader mail.Header

func (h mailHeader) Get(key string) string { return mail.Header(h).Get(key) }

func textproto(header mail.Header) partHeader { return mailHeader(header) }

type mimeWalker struct {
	text            string
	html            string
	parts           int
	attachments     []domain.MailAttachment
	attachmentBytes int
	// strict refuses what the lenient walk drops or cuts short: a mail app's submission must be
	// sent whole or not at all. The first limit met is kept in violation.
	strict    bool
	violation *domain.MailClientSubmitError
}

// refuse records the first limit a strict walk meets.
func (w *mimeWalker) refuse(reason string) {
	if w.violation == nil {
		w.violation = &domain.MailClientSubmitError{Kind: domain.MailClientSubmitTooLarge, Reason: reason}
	}
}

// walk reads one part; related tells that it sits in a multipart/related, where a part without a
// disposition is shown inline by the HTML body.
func (w *mimeWalker) walk(header partHeader, body io.Reader, depth int, related bool) {
	if depth > maxMIMEDepth || w.parts >= maxMIMEParts {
		if w.strict && w.violation == nil {
			w.violation = &domain.MailClientSubmitError{Kind: domain.MailClientSubmitInvalid, Reason: "The message nests or splits into too many parts"}
		}
		return
	}
	if w.violation != nil {
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
			part, partErr := reader.NextRawPart()
			if partErr != nil {
				return
			}
			w.walk(part.Header, part, depth+1, mediaType == "multipart/related")
		}
		return
	}

	disposition, dispositionParams, err := mime.ParseMediaType(header.Get("Content-Disposition"))
	if err != nil {
		disposition, dispositionParams = "", map[string]string{}
	}
	filename := dispositionParams["filename"]
	if filename == "" {
		filename = params["name"]
	}
	// A text part is a body unless the sender marked it as a file.
	if disposition != "attachment" && filename == "" {
		switch mediaType {
		case "text/plain":
			if w.text == "" {
				w.text = decodeBody(header, params, body)
				if w.strict && len(w.text) > domain.MaxMailBodyBytes {
					w.refuse("The text body exceeds 256 KiB")
				}
				return
			}
		case "text/html":
			if w.html == "" {
				w.html = decodeBody(header, params, body)
				if w.strict && len(w.html) > domain.MaxMailBodyBytes {
					w.refuse("The HTML body exceeds 256 KiB")
				}
				return
			}
		}
	}
	inline := disposition == "inline" || (disposition == "" && related)
	w.attach(header, mediaType, filename, inline, body)
}

// attach stores one leaf part as a file. Parts beyond the count or size limits are dropped.
func (w *mimeWalker) attach(header partHeader, mediaType, filename string, inline bool, body io.Reader) {
	if len(w.attachments) >= domain.MaxMailAttachments {
		if w.strict {
			w.refuse("More than 10 attachments")
		}
		return
	}
	remaining := domain.MaxMailAttachmentBytes - w.attachmentBytes
	content, err := io.ReadAll(io.LimitReader(transferDecoder(header, body), int64(remaining)+1))
	if w.strict && len(content) > remaining {
		w.refuse("Attachments exceed 4 MiB in total")
		return
	}
	if (err != nil && len(content) == 0) || len(content) == 0 || len(content) > remaining {
		return
	}
	name, ok := cleanAttachmentName(decodeHeader(filename))
	if !ok {
		name = "attachment-" + strconv.Itoa(len(w.attachments)+1)
	}
	w.attachmentBytes += len(content)
	// A Content-ID that cannot be stored is dropped, never the file.
	contentID, ok := cleanContentID(header.Get("Content-Id"))
	if !ok {
		contentID = ""
	}
	w.attachments = append(w.attachments, domain.MailAttachment{
		Filename:    name,
		ContentType: cleanAttachmentType(mediaType),
		Size:        len(content),
		Content:     content,
		ContentID:   contentID,
		Inline:      inline && contentID != "",
	})
}

// transferDecoder undoes the Content-Transfer-Encoding of a part.
func transferDecoder(header partHeader, body io.Reader) io.Reader {
	switch strings.ToLower(strings.TrimSpace(header.Get("Content-Transfer-Encoding"))) {
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, newlineStripper{body})
	case "quoted-printable":
		return quotedprintable.NewReader(body)
	default:
		return body
	}
}

func decodeBody(header partHeader, params map[string]string, body io.Reader) string {
	decoded := transferDecoder(header, body)
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

// parseReferences reads the message identifiers of a References header, oldest first. Invalid
// and repeated ids are dropped, and only the newest maxMailReferences are kept. Ids without
// angle brackets, separated by spaces or commas, are accepted from senders that write them so.
func parseReferences(raw string) []string {
	var tokens []string
	if strings.Contains(raw, "<") {
		for rest := raw; ; {
			start := strings.Index(rest, "<")
			if start < 0 {
				break
			}
			end := strings.Index(rest[start:], ">")
			if end < 0 {
				break
			}
			tokens = append(tokens, rest[start:start+end+1])
			rest = rest[start+end+1:]
		}
	} else {
		tokens = strings.FieldsFunc(raw, func(char rune) bool {
			return char == ',' || char == ' ' || char == '\t' || char == '\r' || char == '\n'
		})
	}
	var ids []string
	seen := map[string]struct{}{}
	for _, token := range tokens {
		id, ok := normalizeMessageID(token)
		if !ok || id == "" {
			continue
		}
		if _, repeated := seen[id]; repeated {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) > maxMailReferences {
		ids = ids[len(ids)-maxMailReferences:]
	}
	return ids
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
