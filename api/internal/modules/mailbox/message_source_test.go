package mailbox

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

func countSources(t *testing.T, f *accessFixture) int64 {
	t.Helper()
	repo, ok := f.repo.(*repository)
	if !ok {
		t.Fatal("the fixture needs the PostgreSQL repository")
	}
	var count int64
	if err := repo.db.Model(&MailMessageSourcePO{}).Count(&count).Error; err != nil {
		t.Fatalf("count sources: %v", err)
	}
	return count
}

func deliverTo(t *testing.T, f *accessFixture, mailbox *domain.Mailbox, raw []byte) (*domain.MailMessage, bool) {
	t.Helper()
	now := time.Now()
	message, created, err := f.service.ReceiveInbound(context.Background(), InboundDelivery{
		Recipient: mailbox.Address, Signature: signedHeader(now, mailbox.Address, raw), Raw: raw,
	})
	if err != nil {
		t.Fatalf("ReceiveInbound: %v", err)
	}
	return message, created
}

// TestReceiveInboundStoresRawSource keeps what the mail edge delivered byte for byte.
func TestReceiveInboundStoresRawSource(t *testing.T) {
	f := newPostgresAccessFixture(t)
	raw := attachmentMail()
	message, created := deliverTo(t, f, f.team, raw)
	if !created {
		t.Fatal("the delivery must be stored")
	}
	repo := f.repo.(*repository)
	stored, err := repo.FindMessageSource(context.Background(), message.ID)
	if err != nil || !bytes.Equal(stored, raw) {
		t.Fatalf("FindMessageSource = %d bytes, %v; want the delivered %d bytes", len(stored), err, len(raw))
	}
	var po MailMessageSourcePO
	if err := repo.db.First(&po, "message_id = ?", message.ID).Error; err != nil {
		t.Fatalf("read source row: %v", err)
	}
	if po.Origin != messageSourceInbound || po.Size != len(raw) || len(po.SHA256) != 64 {
		t.Fatalf("source row = origin %q size %d sha %q", po.Origin, po.Size, po.SHA256)
	}
	if _, err := repo.FindMessageSource(context.Background(), f.teamMessage.ID); !errors.Is(err, ErrMailMessageSourceNotFound) {
		t.Fatalf("a message stored without a source = %v; want ErrMailMessageSourceNotFound", err)
	}
}

// TestReceiveInboundDuplicateKeepsOneSource stores the source once for a repeated delivery.
func TestReceiveInboundDuplicateKeepsOneSource(t *testing.T) {
	f := newPostgresAccessFixture(t)
	raw := attachmentMail()
	deliverTo(t, f, f.team, raw)
	if _, created := deliverTo(t, f, f.team, raw); created {
		t.Fatal("the repeat must be a duplicate")
	}
	if count := countSources(t, f); count != 1 {
		t.Fatalf("sources = %d; want 1", count)
	}
}

// TestPurgeTrashDeletesSource deletes a message's source with the message.
func TestPurgeTrashDeletesSource(t *testing.T) {
	f := newPostgresAccessFixture(t)
	message, _ := deliverTo(t, f, f.personal, attachmentMail())
	ctx := context.Background()
	if err := f.service.DeleteMessage(ctx, f.b, f.personal.ID, message.ID); err != nil {
		t.Fatalf("move to trash: %v", err)
	}
	if _, err := f.service.PurgeTrash(ctx, f.b, f.personal.ID); err != nil {
		t.Fatalf("PurgeTrash: %v", err)
	}
	if count := countSources(t, f); count != 0 {
		t.Fatalf("sources after purge = %d; want 0", count)
	}
}

// TestComposeMessageSourceRoundTrips rebuilds a message without a stored source so that parsing
// it again yields the stored fields.
func TestComposeMessageSourceRoundTrips(t *testing.T) {
	created := time.Date(2026, 12, 1, 9, 30, 0, 0, time.UTC)
	message := &domain.MailMessage{
		ID: 7, Direction: domain.MailDirectionInbound, FromAddress: "alice@sender.test", FromName: "爱丽丝",
		To: []string{"team@mail.example"}, Cc: []string{"carol@example.com"}, Subject: "季度报告 Q4",
		TextBody: "Plain body\nsecond line", HTMLBody: "<p>HTML body</p>", MessageID: "abc@sender.test",
		InReplyTo: "parent@sender.test", References: []string{"root@sender.test", "parent@sender.test"},
		CreatedAt: created,
	}
	attachments := []domain.MailAttachment{{Filename: "报告.pdf", ContentType: "application/pdf", Content: []byte("%PDF-1.4\x00\x01")}}
	raw, err := composeMessageSource(message, attachments)
	if err != nil {
		t.Fatalf("composeMessageSource: %v", err)
	}
	parsed, err := parseMail(raw)
	if err != nil {
		t.Fatalf("parseMail: %v", err)
	}
	if parsed.FromAddress != message.FromAddress || parsed.FromName != message.FromName ||
		strings.Join(parsed.To, ",") != "team@mail.example" || strings.Join(parsed.Cc, ",") != "carol@example.com" ||
		parsed.Subject != message.Subject || parsed.MessageID != message.MessageID || parsed.InReplyTo != message.InReplyTo ||
		strings.Join(parsed.References, " ") != "root@sender.test parent@sender.test" {
		t.Fatalf("headers did not survive: %+v", parsed)
	}
	// Quoted-printable carries line breaks as CRLF.
	if strings.ReplaceAll(strings.TrimRight(parsed.TextBody, "\r\n"), "\r\n", "\n") != "Plain body\nsecond line" || strings.TrimSpace(parsed.HTMLBody) != "<p>HTML body</p>" {
		t.Fatalf("bodies = %q / %q", parsed.TextBody, parsed.HTMLBody)
	}
	if len(parsed.Attachments) != 1 || parsed.Attachments[0].Filename != "报告.pdf" || !bytes.Equal(parsed.Attachments[0].Content, attachments[0].Content) {
		t.Fatalf("attachments = %+v", parsed.Attachments)
	}
	head := string(raw[:bytes.Index(raw, []byte("\r\n\r\n"))])
	if !strings.Contains(head, "\r\nX-Zmail-Reconstructed: 1") || !strings.Contains(head, "Date: Tue, 01 Dec 2026 09:30:00 +0000") {
		t.Fatalf("header block = %q", head)
	}
}

// TestMessageSourceFallsBackToComposed serves the stored source when there is one and a rebuilt
// message otherwise.
func TestMessageSourceFallsBackToComposed(t *testing.T) {
	f := newPostgresAccessFixture(t)
	raw := attachmentMail()
	delivered, _ := deliverTo(t, f, f.team, raw)
	ctx := context.Background()
	source, err := f.service.messageSource(ctx, delivered)
	if err != nil || !bytes.Equal(source, raw) {
		t.Fatalf("stored source = %d bytes, %v", len(source), err)
	}
	older, err := f.repo.FindMessage(ctx, f.team.ID, f.teamMessage.ID)
	if err != nil {
		t.Fatalf("FindMessage: %v", err)
	}
	source, err = f.service.messageSource(ctx, older)
	if err != nil || !bytes.Contains(source, []byte("X-Zmail-Reconstructed: 1")) {
		t.Fatalf("rebuilt source = %q, %v", source, err)
	}
	parsed, err := parseMail(source)
	if err != nil || parsed.Subject != "Team update" || len(parsed.Attachments) != 1 || string(parsed.Attachments[0].Content) != "plan" {
		t.Fatalf("rebuilt message = %+v, %v", parsed, err)
	}
}
