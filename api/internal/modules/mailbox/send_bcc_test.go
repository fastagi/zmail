package mailbox

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
)

// TestSendWithBccKeepsItOutOfCc: blind copies reach the provider apart from to and cc, are stored
// apart from cc with the sending member, and count against the recipient cap.
func TestSendWithBccKeepsItOutOfCc(t *testing.T) {
	fixture := newServiceFixture(t)
	mailbox := fixture.mailbox(t, 1, "hello")
	viewer := as(1)

	message, _, err := fixture.service.SendMessage(context.Background(), viewer, mailbox.ID, SendInput{
		To: []string{"bob@example.com"}, Cc: []string{"amy@example.org"},
		Bcc:     []string{"Carol@Example.net", "bob@example.com", "carol@example.net"},
		Subject: "s", Text: "b",
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	sent := fixture.transport.sent[0]
	if fmt.Sprint(sent.To, sent.Cc, sent.Bcc) != "[bob@example.com] [amy@example.org] [carol@example.net]" {
		t.Fatalf("to/cc/bcc = %v %v %v; a bcc also in to collapses into to", sent.To, sent.Cc, sent.Bcc)
	}
	stored := fixture.repo.messages[message.ID]
	if fmt.Sprint(stored.Cc, stored.Bcc) != "[amy@example.org] [carol@example.net]" || stored.SentByMembershipID != viewer.MembershipID || viewer.MembershipID == 0 {
		t.Fatalf("stored cc %v bcc %v by %d", stored.Cc, stored.Bcc, stored.SentByMembershipID)
	}

	many := make([]string, 0, 49)
	for i := range 49 {
		many = append(many, fmt.Sprintf("r%d@example.com", i))
	}
	_, _, err = fixture.service.SendMessage(context.Background(), viewer, mailbox.ID, SendInput{
		To: []string{"bob@example.com"}, Bcc: append(many, "one-more@example.com"), Subject: "s", Text: "b",
	})
	if !errors.Is(err, ErrMailMessageInvalid) {
		t.Fatalf("51 recipients with bcc = %v; want ErrMailMessageInvalid", err)
	}
	if _, _, err := fixture.service.SendMessage(context.Background(), viewer, mailbox.ID, SendInput{
		To: []string{"bob@example.com"}, Bcc: []string{"not an address"}, Subject: "s", Text: "b",
	}); !errors.Is(err, ErrMailMessageInvalid) {
		t.Fatalf("an invalid bcc = %v", err)
	}
}

// TestSendFingerprintUnchangedWithoutBcc keeps the idempotency fingerprint of a request without
// blind copies or inline parts what it was, so stored keys still replay.
func TestSendFingerprintUnchangedWithoutBcc(t *testing.T) {
	before := sendFingerprint([]string{"a@x.test"}, nil, "s", "t", "", "", "")
	if got := sendFingerprintWith([]string{"a@x.test"}, nil, nil, "s", "t", "", "", ""); got != before {
		t.Fatalf("fingerprint changed without bcc: %s != %s", got, before)
	}
	if sendFingerprintWith([]string{"a@x.test"}, nil, []string{"b@x.test"}, "s", "t", "", "", "") == before {
		t.Fatal("a bcc must change the fingerprint")
	}
	plain := []domain.MailAttachment{{Filename: "a.png", ContentType: "image/png", Content: []byte("x")}}
	inline := []domain.MailAttachment{{Filename: "a.png", ContentType: "image/png", Content: []byte("x"), ContentID: "a@x", Inline: true}}
	if attachmentDigest(plain) == attachmentDigest(inline) {
		t.Fatal("an inline Content-ID must change the attachment digest")
	}
}

// TestQueuedSendKeepsBccAndInline: a message that waited for the send limit goes out later with the
// same blind copies and inline parts.
func TestQueuedSendKeepsBccAndInline(t *testing.T) {
	f := newQueueFixture(t)
	for i := range 5 {
		f.mustSend(t, fmt.Sprintf("fill %d", i), false)
	}
	message, _, err := f.service.SendMessage(context.Background(), f.b, f.team.ID, SendInput{
		To: []string{"bob@example.com"}, Bcc: []string{"carol@example.com"}, Subject: "queued", HTML: `<img src="cid:logo@x">`,
		Attachments: []AttachmentInput{{
			Filename: "logo.png", ContentType: "image/png", Content: base64.StdEncoding.EncodeToString([]byte("png")),
			ContentID: "logo@x", Inline: true,
		}},
	})
	if err != nil || message.QueuedReason != domain.MailQueuedForSendLimit {
		t.Fatalf("queued send = %+v, %v", message, err)
	}
	if po := f.stored(t, message.ID); po.BccAddresses != `["carol@example.com"]` || po.SentByMembershipID == nil || *po.SentByMembershipID != f.b.MembershipID {
		t.Fatalf("stored bcc %q by %v", po.BccAddresses, po.SentByMembershipID)
	}
	f.clock = f.clock.Add(25 * time.Hour)
	if processed := f.dispatch(t, 10); processed != 1 {
		t.Fatalf("dispatched %d", processed)
	}
	sent := f.transport.sent[len(f.transport.sent)-1]
	if sent.Subject != "queued" || fmt.Sprint(sent.Bcc) != "[carol@example.com]" || len(sent.Cc) != 0 {
		t.Fatalf("worker sent %q cc %v bcc %v", sent.Subject, sent.Cc, sent.Bcc)
	}
	if len(sent.Attachments) != 1 || sent.Attachments[0].ContentID != "logo@x" || !sent.Attachments[0].Inline {
		t.Fatalf("worker attachments = %+v", sent.Attachments)
	}
	detail, err := f.service.GetMessage(context.Background(), f.b, f.team.ID, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(detail.Bcc) != "[carol@example.com]" {
		t.Fatalf("detail bcc = %v", detail.Bcc)
	}
	// Everyone else with access to the mailbox never sees them.
	for name, viewer := range map[string]domain.OrganizationContext{"member with view": f.c} {
		other, getErr := f.service.GetMessage(context.Background(), viewer, f.team.ID, message.ID)
		if getErr != nil {
			t.Fatalf("%s: %v", name, getErr)
		}
		if len(other.Bcc) != 0 {
			t.Fatalf("%s sees bcc %v", name, other.Bcc)
		}
	}
	listed, _, err := f.service.ListMessages(context.Background(), f.c, f.team.ID, domain.MailMessageFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range listed {
		if len(row.Bcc) != 0 {
			t.Fatalf("a listing carries bcc %v", row.Bcc)
		}
	}
}

// TestBuildMIMEMessageNeverWritesBcc: neither a connected account's SMTP send nor a source rebuilt
// for a mail app names a blind copy, and an inline part keeps its Content-ID.
func TestBuildMIMEMessageNeverWritesBcc(t *testing.T) {
	raw, err := buildMIMEMessage(mailtransport.Message{
		FromAddress: "a@mail.example", To: []string{"b@example.com"}, Bcc: []string{"secret@example.com"},
		Subject: "s", HTMLBody: `<img src="cid:logo@x">`, TextBody: "t",
		Headers: map[string]string{"Bcc": "secret@example.com"},
		Attachments: []mailtransport.Attachment{
			{Filename: "logo.png", ContentType: "image/png", Content: []byte("png"), ContentID: "logo@x", Inline: true},
			{Filename: "a.pdf", ContentType: "application/pdf", Content: []byte("pdf")},
		},
	}, "id@mail.example", time.Unix(1_800_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(bytes.ToLower(raw), []byte("secret@example.com")) || bytes.Contains(bytes.ToLower(raw), []byte("\nbcc:")) {
		t.Fatalf("a blind copy appears in the message:\n%s", raw)
	}
	parsed, err := parseMail(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Attachments) != 2 || parsed.Attachments[0].ContentID != "logo@x" || !parsed.Attachments[0].Inline || parsed.Attachments[1].Inline {
		t.Fatalf("attachments = %+v", parsed.Attachments)
	}
	if !strings.Contains(string(raw), "Content-ID: <logo@x>") {
		t.Fatalf("no Content-ID header:\n%s", raw)
	}
}

// TestMessageResponseCarriesBccOnlyWhenKept: the detail names blind copies the service kept for
// the sender and leaves the field out otherwise.
func TestMessageResponseCarriesBccOnlyWhenKept(t *testing.T) {
	message := &domain.MailMessage{ID: 1, MailboxID: 1, Direction: domain.MailDirectionOutbound, Status: domain.MailStatusSent,
		To: []string{"a@example.com"}, Bcc: []string{"b@example.com"}, SentByMembershipID: 5}
	kept, err := json.Marshal(toMailMessageResponse(message))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(kept), `"bcc":["b@example.com"]`) {
		t.Fatalf("sender's detail = %s", kept)
	}
	hideBlindCopies(6, message)
	hidden, err := json.Marshal(toMailMessageResponse(message))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(hidden), "bcc") || strings.Contains(string(hidden), "b@example.com") {
		t.Fatalf("another member's detail = %s", hidden)
	}
}
