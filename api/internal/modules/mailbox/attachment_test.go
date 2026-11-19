package mailbox

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
)

func b64(content string) string {
	return base64.StdEncoding.EncodeToString([]byte(content))
}

func TestSendStoresAttachmentsAndHandsThemToTheProvider(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	mailbox := fixture.mailbox(t, 1, "hello")

	message, _, err := fixture.service.SendMessage(ctx, as(1), mailbox.ID, SendInput{
		To: []string{"bob@example.com"}, Subject: "Files", Text: "See attached.",
		Attachments: []AttachmentInput{
			{Filename: "../../etc/report.pdf", ContentType: "application/pdf; name=x", Content: b64("%PDF-1.4")},
			{Filename: "notes.txt", ContentType: "not a type", Content: b64("hello")},
		},
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if message.AttachmentCount != 2 || len(message.Attachments) != 2 {
		t.Fatalf("message attachments = %d, %+v", message.AttachmentCount, message.Attachments)
	}
	for _, attachment := range message.Attachments {
		if attachment.ID == 0 || attachment.Content != nil {
			t.Fatalf("returned attachment = %+v; want an id and no content", attachment)
		}
	}
	sent := fixture.transport.sent[0].Attachments
	if len(sent) != 2 || sent[0].Filename != "report.pdf" || sent[0].ContentType != "application/pdf" ||
		string(sent[0].Content) != "%PDF-1.4" || sent[1].ContentType != defaultAttachmentType {
		t.Fatalf("provider attachments = %+v", sent)
	}

	detail, err := fixture.service.GetMessage(ctx, as(1), mailbox.ID, message.ID)
	if err != nil || len(detail.Attachments) != 2 || detail.Attachments[0].Size != 8 || detail.Attachments[0].Content != nil {
		t.Fatalf("detail attachments = %+v, %v", detail.Attachments, err)
	}
	file, err := fixture.service.GetAttachment(ctx, as(1), mailbox.ID, message.ID, detail.Attachments[1].ID)
	if err != nil || string(file.Content) != "hello" || file.Filename != "notes.txt" {
		t.Fatalf("GetAttachment = %+v, %v", file, err)
	}
}

func TestAttachmentsAreInvisibleOutsideTheirMessageAndOrganization(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	mailbox := fixture.mailbox(t, 1, "hello")
	other := fixture.mailbox(t, 1, "other")
	input := SendInput{
		To: []string{"bob@example.com"}, Subject: "s", Text: "b",
		Attachments: []AttachmentInput{{Filename: "a.txt", Content: b64("a")}},
	}
	first, _, err := fixture.service.SendMessage(ctx, as(1), mailbox.ID, input)
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	second, _, err := fixture.service.SendMessage(ctx, as(1), other.ID, input)
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	attachmentID := first.Attachments[0].ID

	if _, err := fixture.service.GetAttachment(ctx, as(2), mailbox.ID, first.ID, attachmentID); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("another organization error = %v", err)
	}
	if _, err := fixture.service.GetAttachment(ctx, as(1), other.ID, first.ID, attachmentID); !errors.Is(err, ErrMailMessageNotFound) {
		t.Fatalf("wrong mailbox error = %v", err)
	}
	if _, err := fixture.service.GetAttachment(ctx, as(1), other.ID, second.ID, attachmentID); !errors.Is(err, ErrMailMessageNotFound) {
		t.Fatalf("another message's attachment error = %v", err)
	}
}

func TestSendRejectsUnusableAttachments(t *testing.T) {
	fixture := newServiceFixture(t)
	mailbox := fixture.mailbox(t, 1, "hello")
	tooMany := make([]AttachmentInput, domain.MaxMailAttachments+1)
	for i := range tooMany {
		tooMany[i] = AttachmentInput{Filename: "a.txt", Content: b64("a")}
	}
	half := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'x'}, domain.MaxMailAttachmentBytes/2+1))
	for name, attachments := range map[string][]AttachmentInput{
		"too many":      tooMany,
		"not base64":    {{Filename: "a.txt", Content: "***"}},
		"empty":         {{Filename: "a.txt", Content: ""}},
		"no name":       {{Filename: " / ", Content: b64("a")}},
		"control name":  {{Filename: "\x00\n", Content: b64("a")}},
		"total too big": {{Filename: "a.bin", Content: half}, {Filename: "b.bin", Content: half}},
	} {
		_, _, err := fixture.service.SendMessage(context.Background(), as(1), mailbox.ID, SendInput{
			To: []string{"x@example.com"}, Subject: "s", Text: "b", Attachments: attachments,
		})
		if !errors.Is(err, ErrMailMessageInvalid) {
			t.Errorf("%s: error = %v; want an invalid message", name, err)
		}
	}
	if len(fixture.transport.sent) != 0 || len(fixture.repo.messages) != 0 {
		t.Fatalf("a rejected message was stored or sent")
	}
}

func TestIdempotencyKeyCoversAttachments(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	mailbox := fixture.mailbox(t, 1, "hello")
	input := SendInput{
		To: []string{"x@example.com"}, Subject: "s", Text: "b", IdempotencyKey: "key-1",
		Attachments: []AttachmentInput{{Filename: "a.txt", Content: b64("first")}},
	}
	if _, _, err := fixture.service.SendMessage(ctx, as(1), mailbox.ID, input); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if _, created, err := fixture.service.SendMessage(ctx, as(1), mailbox.ID, input); err != nil || created {
		t.Fatalf("replay = created %v, %v", created, err)
	}
	input.Attachments = []AttachmentInput{{Filename: "a.txt", Content: b64("second")}}
	if _, _, err := fixture.service.SendMessage(ctx, as(1), mailbox.ID, input); !errors.Is(err, ErrMailIdempotencyConflict) {
		t.Fatalf("changed attachment error = %v; want a conflict", err)
	}
	if len(fixture.transport.sent) != 1 {
		t.Fatalf("provider calls = %d; want 1", len(fixture.transport.sent))
	}
}

func attachmentMail() []byte {
	return crlf(
		"From: Alice <alice@sender.test>",
		"To: hello@mail.example",
		"Subject: With files",
		"Message-ID: <files-1@sender.test>",
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="outer"`,
		"",
		"--outer",
		`Content-Type: multipart/alternative; boundary="inner"`,
		"",
		"--inner",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"Plain body.",
		"--inner",
		"Content-Type: text/html; charset=utf-8",
		"",
		"<p>HTML body.</p>",
		"--inner--",
		"--outer",
		`Content-Type: application/pdf; name="=?UTF-8?B?5oql5ZGKLnBkZg==?="`,
		"Content-Transfer-Encoding: base64",
		`Content-Disposition: attachment; filename="=?UTF-8?B?5oql5ZGKLnBkZg==?="`,
		"",
		"JVBERi0x",
		"LjQ=",
		"--outer",
		"Content-Type: text/plain; charset=utf-8",
		`Content-Disposition: attachment; filename="../notes.txt"`,
		"",
		"attached text",
		"--outer",
		"Content-Type: image/png",
		"Content-Transfer-Encoding: base64",
		"Content-ID: <logo>",
		"",
		"iVBORw==",
		"--outer--",
	)
}

func TestParseMailCollectsAttachments(t *testing.T) {
	parsed, err := parseMail(attachmentMail())
	if err != nil {
		t.Fatalf("parseMail: %v", err)
	}
	if parsed.TextBody != "Plain body." || !strings.Contains(parsed.HTMLBody, "HTML body.") {
		t.Fatalf("bodies = %q, %q", parsed.TextBody, parsed.HTMLBody)
	}
	if len(parsed.Attachments) != 3 {
		t.Fatalf("attachments = %+v; want 3", parsed.Attachments)
	}
	pdf, text, image := parsed.Attachments[0], parsed.Attachments[1], parsed.Attachments[2]
	if pdf.Filename != "报告.pdf" || pdf.ContentType != "application/pdf" || string(pdf.Content) != "%PDF-1.4" {
		t.Fatalf("pdf = %+v", pdf)
	}
	if text.Filename != "notes.txt" || strings.TrimSpace(string(text.Content)) != "attached text" {
		t.Fatalf("text attachment = %+v", text)
	}
	if image.Filename != "attachment-3" || image.ContentType != "image/png" || image.Size != 4 {
		t.Fatalf("unnamed inline part = %+v", image)
	}
}

func TestParseMailBoundsAttachmentCount(t *testing.T) {
	lines := []string{
		"From: a@sender.test", "To: hello@mail.example", "Subject: Many", "MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="b"`, "",
	}
	for range domain.MaxMailAttachments + 5 {
		lines = append(lines, "--b", "Content-Type: application/octet-stream",
			`Content-Disposition: attachment; filename="f.bin"`, "", "data")
	}
	lines = append(lines, "--b--")
	parsed, err := parseMail(crlf(lines...))
	if err != nil || len(parsed.Attachments) != domain.MaxMailAttachments {
		t.Fatalf("attachments = %d, %v; want %d", len(parsed.Attachments), err, domain.MaxMailAttachments)
	}
}

func TestInboundStoresAttachments(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	mailbox := fixture.mailbox(t, 1, "hello")
	message, created, err := fixture.service.ReceiveInbound(ctx, fixture.delivery("hello@mail.example", attachmentMail()))
	if err != nil || !created || message.AttachmentCount != 3 {
		t.Fatalf("ReceiveInbound = %+v, created %v, %v", message, created, err)
	}
	detail, err := fixture.service.GetMessage(ctx, as(1), mailbox.ID, message.ID)
	if err != nil || len(detail.Attachments) != 3 {
		t.Fatalf("detail = %+v, %v", detail, err)
	}
	file, err := fixture.service.GetAttachment(ctx, as(1), mailbox.ID, message.ID, detail.Attachments[0].ID)
	if err != nil || string(file.Content) != "%PDF-1.4" {
		t.Fatalf("GetAttachment = %+v, %v", file, err)
	}
}

func TestDownloadAttachmentIsAnOpaquePrivateDownload(t *testing.T) {
	fixture := newHandlerFixture(t, membership(domain.OrganizationRoleMember))
	fixture.router.GET("/mailboxes/:mailbox_id/messages/:message_id/attachments/:attachment_id",
		NewHandler(fixture.service).DownloadAttachment)
	mailbox, err := fixture.service.CreateMailbox(context.Background(), as(7), sharedWithHandlerUser("hello", domain.MailboxPermissionSend))
	if err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	recorder, body := fixture.do(http.MethodPost, "/mailboxes/1/messages",
		`{"to":["bob@example.com"],"subject":"s","text":"b","attachments":[{"filename":"页面.html","content_type":"text/html","content":"`+b64("<script>alert(1)</script>")+`"}]}`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("send = %d %s", recorder.Code, recorder.Body.String())
	}
	data, _ := body["data"].(map[string]any)
	attachments, _ := data["attachments"].([]any)
	if data["attachment_count"] != float64(1) || len(attachments) != 1 {
		t.Fatalf("send response = %v", data)
	}
	first, _ := attachments[0].(map[string]any)
	if first["filename"] != "页面.html" || first["size"] != float64(25) || first["content"] != nil {
		t.Fatalf("attachment = %v", first)
	}
	_ = mailbox

	request := httptest.NewRequest(http.MethodGet, "/mailboxes/1/messages/2/attachments/3", nil)
	download := httptest.NewRecorder()
	fixture.router.ServeHTTP(download, request)
	if download.Code != http.StatusOK || download.Body.String() != "<script>alert(1)</script>" {
		t.Fatalf("download = %d %q", download.Code, download.Body.String())
	}
	header := download.Header()
	if header.Get("Content-Type") != "application/octet-stream" || header.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.HasPrefix(header.Get("Content-Disposition"), "attachment;") ||
		!strings.Contains(header.Get("Content-Disposition"), "filename*=utf-8''") ||
		header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("download headers = %v", header)
	}

	missing := httptest.NewRecorder()
	fixture.router.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/mailboxes/1/messages/2/attachments/999", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing attachment = %d", missing.Code)
	}
}
