package mailbox

import (
	"io"
	"mime"
	"net/mail"
	"regexp"
	"strings"
)

var (
	// rfc2047EncodedWord matches one RFC 2047 encoded-word token: =?charset?encoding?encoded-text?=
	rfc2047EncodedWord = regexp.MustCompile(`=\?[^?\s]+\?[bBqQ]\?[^?\s]*\?=`)
	// fallbackFromParser keeps FromAddress extraction working when a header uses an unknown charset.
	fallbackFromParser = mail.AddressParser{
		WordDecoder: &mime.WordDecoder{
			CharsetReader: func(_ string, input io.Reader) (io.Reader, error) {
				return input, nil
			},
		},
	}
)

// decodeMIMEHeader decodes RFC 2047 encoded-words (UTF-8, ISO-8859-1, and other supported charsets)
// while falling back gracefully on unknown or malformed charsets without failing the whole header.
func decodeMIMEHeader(raw string) string {
	if !strings.Contains(raw, "=?") {
		return cleanText(raw)
	}
	if decoded, err := headerDecoder.DecodeHeader(raw); err == nil {
		return cleanText(decoded)
	}
	// If any single encoded-word fails (e.g. unknown charset), decode each valid token individually
	// and preserve unparseable tokens as-is.
	partial := rfc2047EncodedWord.ReplaceAllStringFunc(raw, func(token string) string {
		decoded, err := headerDecoder.Decode(token)
		if err != nil {
			return token
		}
		return decoded
	})
	return cleanText(partial)
}

// parseFromHeader extracts the lowercased email address and RFC 2047-decoded display name from a
// From header. Go's net/mail leaves encoded-words inside quoted-strings undecoded per strict
// RFC 2047, so the display name is always passed through decodeMIMEHeader.
func parseFromHeader(raw string) (address, name string) {
	if strings.TrimSpace(raw) == "" {
		return "", ""
	}
	parser := mail.AddressParser{WordDecoder: headerDecoder}
	from, err := parser.Parse(raw)
	if err != nil {
		from, err = fallbackFromParser.Parse(raw)
		if err != nil {
			return "", ""
		}
	}
	return strings.ToLower(from.Address), truncateText(strings.TrimSpace(decodeMIMEHeader(from.Name)), 255)
}
