package mailbox

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"

	"github.com/fastagi/fastmail/api/internal/domain"
)

func crlf(lines ...string) []byte {
	return []byte(strings.Join(lines, "\r\n"))
}

func TestParseMailPlainText(t *testing.T) {
	parsed, err := parseMail(crlf(
		"From: Alice Example <Alice@Sender.Test>",
		"To: hello@mail.example, Other <other@mail.example>",
		"Cc: copy@mail.example",
		"Subject: Plain   subject",
		"Message-ID: <m1@sender.test>",
		"In-Reply-To: <m0@mail.example> <ignored@x.y>",
		"",
		"Hello there.",
	))
	if err != nil {
		t.Fatalf("parseMail: %v", err)
	}
	if parsed.FromAddress != "alice@sender.test" || parsed.FromName != "Alice Example" {
		t.Fatalf("from = %q %q", parsed.FromAddress, parsed.FromName)
	}
	if len(parsed.To) != 2 || parsed.To[1] != "other@mail.example" || len(parsed.Cc) != 1 {
		t.Fatalf("recipients = %v %v", parsed.To, parsed.Cc)
	}
	if parsed.Subject != "Plain subject" || parsed.MessageID != "m1@sender.test" || parsed.InReplyTo != "m0@mail.example" {
		t.Fatalf("headers = %+v", parsed)
	}
	if parsed.TextBody != "Hello there." || parsed.HTMLBody != "" {
		t.Fatalf("bodies = %q %q", parsed.TextBody, parsed.HTMLBody)
	}
}

func TestParseMailMultipartSkipsAttachmentsAndDecodesParts(t *testing.T) {
	htmlBody := base64.StdEncoding.EncodeToString([]byte("<p>Hi <b>there</b></p>"))
	parsed, err := parseMail(crlf(
		"From: alice@sender.test",
		"To: hello@mail.example",
		"Subject: =?UTF-8?B?5L2g5aW9?=",
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="outer"`,
		"",
		"--outer",
		`Content-Type: multipart/alternative; boundary="inner"`,
		"",
		"--inner",
		"Content-Type: text/plain; charset=utf-8",
		"Content-Transfer-Encoding: quoted-printable",
		"",
		"caf=C3=A9 costs 5=E2=82=AC",
		"--inner",
		"Content-Type: text/html; charset=utf-8",
		"Content-Transfer-Encoding: base64",
		"",
		htmlBody[:10],
		htmlBody[10:],
		"--inner--",
		"--outer",
		"Content-Type: text/plain",
		`Content-Disposition: attachment; filename="notes.txt"`,
		"",
		"attachment text must not become the body",
		"--outer--",
		"",
	))
	if err != nil {
		t.Fatalf("parseMail: %v", err)
	}
	if parsed.Subject != "你好" {
		t.Fatalf("subject = %q", parsed.Subject)
	}
	if parsed.TextBody != "café costs 5€" {
		t.Fatalf("text = %q", parsed.TextBody)
	}
	if parsed.HTMLBody != "<p>Hi <b>there</b></p>" {
		t.Fatalf("html = %q", parsed.HTMLBody)
	}
}

func TestParseMailConvertsLegacyCharsets(t *testing.T) {
	gbk, err := simplifiedchinese.GBK.NewEncoder().String("你好，世界")
	if err != nil {
		t.Fatalf("encode GBK: %v", err)
	}
	raw := append(crlf(
		"From: alice@sender.test",
		"To: hello@mail.example",
		"Subject: =?GBK?B?"+base64.StdEncoding.EncodeToString([]byte(gbk))+"?=",
		"Content-Type: text/plain; charset=gbk",
		"",
		"",
	), []byte(gbk)...)

	parsed, err := parseMail(raw)
	if err != nil {
		t.Fatalf("parseMail: %v", err)
	}
	if parsed.Subject != "你好，世界" || parsed.TextBody != "你好，世界" {
		t.Fatalf("subject = %q, text = %q", parsed.Subject, parsed.TextBody)
	}
}

func TestParseMailProducesStorableText(t *testing.T) {
	body := strings.Repeat("好", domain.MaxMailBodyBytes) // three bytes per rune, over the limit
	parsed, err := parseMail(append(crlf(
		"From: alice@sender.test",
		"Subject: bad\x00bytes \xff here",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"",
	), []byte(body+"\x00\xff")...))
	if err != nil {
		t.Fatalf("parseMail: %v", err)
	}
	if len(parsed.TextBody) > domain.MaxMailBodyBytes || !utf8.ValidString(parsed.TextBody) {
		t.Fatalf("text body is %d bytes, valid UTF-8 = %v", len(parsed.TextBody), utf8.ValidString(parsed.TextBody))
	}
	if strings.ContainsRune(parsed.TextBody, 0) || strings.ContainsRune(parsed.Subject, 0) || !utf8.ValidString(parsed.Subject) {
		t.Fatalf("NUL or invalid UTF-8 survived: subject %q", parsed.Subject)
	}
}

func TestParseMailRejectsGarbage(t *testing.T) {
	if _, err := parseMail([]byte("no header separator at all")); !errors.Is(err, ErrMailInboundUnparseable) {
		t.Fatalf("error = %v; want ErrMailInboundUnparseable", err)
	}
}
