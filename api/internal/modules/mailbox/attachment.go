package mailbox

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"mime"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/fastagi/zmail/api/internal/domain"
)

// defaultAttachmentType is used when a sender declares no usable media type.
const defaultAttachmentType = "application/octet-stream"

// AttachmentInput is one file a sender attaches. Content is standard base64.
type AttachmentInput struct {
	Filename    string
	ContentType string
	Content     string
	// ContentID and Inline keep an image an HTML body shows by cid:; only mail app submissions set
	// them. ContentID carries no angle brackets.
	ContentID string
	Inline    bool
}

// buildAttachments validates and decodes the files of an outbound message.
func buildAttachments(inputs []AttachmentInput) ([]domain.MailAttachment, error) {
	invalid := fmt.Errorf("%w: attachments", ErrMailMessageInvalid)
	if len(inputs) > domain.MaxMailAttachments {
		return nil, invalid
	}
	attachments := make([]domain.MailAttachment, 0, len(inputs))
	total := 0
	for _, input := range inputs {
		filename, ok := cleanAttachmentName(input.Filename)
		if !ok {
			return nil, invalid
		}
		// Refuse before decoding what could not fit anyway.
		if base64.StdEncoding.DecodedLen(len(input.Content)) > domain.MaxMailAttachmentBytes+3 {
			return nil, invalid
		}
		content, err := base64.StdEncoding.DecodeString(input.Content)
		if err != nil || len(content) == 0 {
			return nil, invalid
		}
		total += len(content)
		if total > domain.MaxMailAttachmentBytes {
			return nil, invalid
		}
		contentID, ok := cleanContentID(input.ContentID)
		if !ok {
			return nil, invalid
		}
		attachments = append(attachments, domain.MailAttachment{
			Filename:    filename,
			ContentType: cleanAttachmentType(input.ContentType),
			Size:        len(content),
			Content:     content,
			ContentID:   contentID,
			// Only a part an HTML body can name is placed inline.
			Inline: input.Inline && contentID != "",
		})
	}
	return attachments, nil
}

// maxContentIDLength bounds a stored Content-ID (mail_attachments.content_id).
const maxContentIDLength = 250

// cleanContentID strips the angle brackets of a Content-ID and accepts only a bounded token of
// printable ASCII without spaces, so it is safe to store and to put back in a header. An empty
// one is no Content-ID.
func cleanContentID(raw string) (string, bool) {
	id := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(raw), "<"), ">")
	if id == "" {
		return "", true
	}
	if len(id) > maxContentIDLength {
		return "", false
	}
	for _, char := range id {
		if char <= 0x20 || char >= 0x7f || char == '<' || char == '>' {
			return "", false
		}
	}
	return id, true
}

// cleanAttachmentName reduces a declared file name to a bare name that is safe to store and to
// put in a Content-Disposition header: no path, no control characters, bounded length.
func cleanAttachmentName(raw string) (string, bool) {
	if !utf8.ValidString(raw) {
		return "", false
	}
	name := strings.ReplaceAll(raw, "\\", "/")
	name = path.Base(name)
	name = strings.Map(func(char rune) rune {
		if unicode.IsControl(char) || char == utf8.RuneError {
			return -1
		}
		return char
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || name == "/" {
		return "", false
	}
	for len(name) > domain.MaxMailAttachmentNameLength {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	return name, true
}

// cleanAttachmentType keeps a declared media type only when it is a plain type/subtype.
func cleanAttachmentType(raw string) string {
	mediaType, _, err := mime.ParseMediaType(raw)
	if err != nil || len(mediaType) > 127 || !strings.Contains(mediaType, "/") {
		return defaultAttachmentType
	}
	return mediaType
}

// attachmentDigest identifies the files of a request for the idempotency fingerprint.
func attachmentDigest(attachments []domain.MailAttachment) string {
	if len(attachments) == 0 {
		return ""
	}
	hash := sha256.New()
	for _, attachment := range attachments {
		fmt.Fprintf(hash, "%d:%s%d:%s%d:", len(attachment.Filename), attachment.Filename,
			len(attachment.ContentType), attachment.ContentType, len(attachment.Content))
		hash.Write(attachment.Content)
		// A file without a Content-ID keeps the digest it had before inline parts existed.
		if attachment.ContentID != "" {
			fmt.Fprintf(hash, "cid:%d:%s:%t:", len(attachment.ContentID), attachment.ContentID, attachment.Inline)
		}
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}
