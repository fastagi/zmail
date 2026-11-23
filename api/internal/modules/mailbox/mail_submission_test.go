package mailbox

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
)

func submitErrorKind(err error) domain.MailClientSubmitErrorKind {
	var refused *domain.MailClientSubmitError
	if errors.As(err, &refused) {
		return refused.Kind
	}
	return ""
}

// submission builds a message as a mail app sends it.
func submission(from, to, cc, subject, messageID, body string) []byte {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\nTo: " + to + "\r\n")
	if cc != "" {
		b.WriteString("Cc: " + cc + "\r\n")
	}
	if messageID != "" {
		b.WriteString("Message-ID: <" + messageID + ">\r\n")
	}
	b.WriteString("Subject: " + subject + "\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + body + "\r\n")
	return []byte(b.String())
}

func relatedSubmission(parts int, partBytes int) []byte {
	var b strings.Builder
	b.WriteString("From: bob@mail.example\r\nTo: carol@example.com\r\nSubject: logo\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/related; boundary=REL\r\n\r\n--REL\r\nContent-Type: text/html\r\n\r\n<img src=\"cid:logo@x\">\r\n")
	for i := range parts {
		fmt.Fprintf(&b, "--REL\r\nContent-Type: image/png\r\nContent-ID: <logo%d@x>\r\nContent-Transfer-Encoding: base64\r\n\r\n%s\r\n",
			i, base64.StdEncoding.EncodeToString([]byte(strings.Repeat("p", partBytes))))
	}
	b.WriteString("--REL--\r\n")
	return []byte(b.String())
}

// TestParseSubmissionKeepsInlineContentID: an image of a multipart/related body is inline under
// its Content-ID, and a part with an explicit attachment disposition is not.
func TestParseSubmissionKeepsInlineContentID(t *testing.T) {
	raw := []byte("From: bob@mail.example\r\nTo: carol@example.com\r\nSubject: logo\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=MIX\r\n\r\n--MIX\r\n" +
		"Content-Type: multipart/related; boundary=REL\r\n\r\n--REL\r\nContent-Type: text/html\r\n\r\n<img src=\"cid:logo@x\">\r\n" +
		"--REL\r\nContent-Type: image/png\r\nContent-ID: <logo@x>\r\nContent-Transfer-Encoding: base64\r\n\r\ncG5n\r\n--REL--\r\n" +
		"--MIX\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=a.pdf\r\nContent-ID: <pdf@x>\r\n\r\npdf\r\n--MIX--\r\n")
	parsed, err := parseSubmission(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Attachments) != 2 {
		t.Fatalf("attachments = %+v", parsed.Attachments)
	}
	logo, pdf := parsed.Attachments[0], parsed.Attachments[1]
	if logo.ContentID != "logo@x" || !logo.Inline || string(logo.Content) != "png" {
		t.Fatalf("inline image = %+v", logo)
	}
	if pdf.Inline || pdf.Filename != "a.pdf" {
		t.Fatalf("attachment = %+v", pdf)
	}
	if !strings.Contains(parsed.HTMLBody, "cid:logo@x") {
		t.Fatalf("html = %q", parsed.HTMLBody)
	}
}

// TestParseSubmissionLimits refuses what parseMail would cut short, naming the limit.
func TestParseSubmissionLimits(t *testing.T) {
	for name, tc := range map[string]struct {
		raw    []byte
		kind   domain.MailClientSubmitErrorKind
		reason string
	}{
		"over 8 MiB":       {append(submission("bob@mail.example", "c@example.com", "", "s", "", ""), make([]byte, 8<<20)...), domain.MailClientSubmitTooLarge, "The message exceeds 8 MiB"},
		"11 attachments":   {relatedSubmission(11, 10), domain.MailClientSubmitTooLarge, "More than 10 attachments"},
		"over 4 MiB files": {relatedSubmission(3, 1500*1024), domain.MailClientSubmitTooLarge, "Attachments exceed 4 MiB in total"},
		"long subject":     {submission("bob@mail.example", "c@example.com", "", strings.Repeat("s", 999), "", "b"), domain.MailClientSubmitTooLarge, "The subject exceeds 998 characters"},
		"long body":        {submission("bob@mail.example", "c@example.com", "", "s", "", strings.Repeat("b", 257*1024)), domain.MailClientSubmitTooLarge, "The text body exceeds 256 KiB"},
		"unreadable":       {[]byte("no headers here"), domain.MailClientSubmitInvalid, "The message could not be read"},
	} {
		_, err := parseSubmission(tc.raw)
		var refused *domain.MailClientSubmitError
		if !errors.As(err, &refused) || refused.Kind != tc.kind || refused.Reason != tc.reason {
			t.Errorf("%s: %v; want %s %q", name, err, tc.kind, tc.reason)
		}
	}
	if parsed, err := parseSubmission(relatedSubmission(10, 10)); err != nil || len(parsed.Attachments) != 10 {
		t.Fatalf("10 attachments = %v", err)
	}
	// parseMail still keeps what fits for inbound mail.
	if parsed, err := parseMail(relatedSubmission(11, 10)); err != nil || len(parsed.Attachments) != 10 {
		t.Fatalf("parseMail of 11 attachments = %v", err)
	}
}

func TestSubmissionIdempotencyKey(t *testing.T) {
	key := submissionIdempotencyKey(7, "abc@x", []byte("one"))
	if key != submissionIdempotencyKey(7, "abc@x", []byte("two")) {
		t.Fatal("the Message-ID names the message whatever its bytes")
	}
	if key == submissionIdempotencyKey(8, "abc@x", []byte("one")) {
		t.Fatal("another mailbox has its own keys")
	}
	raw := submissionIdempotencyKey(7, "", []byte("one"))
	if raw == key || raw != submissionIdempotencyKey(7, "", []byte("one")) || raw == submissionIdempotencyKey(7, "", []byte("two")) {
		t.Fatal("without a Message-ID the bytes name the message")
	}
	if !strings.HasPrefix(key, "smtp:") || !validIdempotencyKey(key) || strings.Contains(key, "abc") {
		t.Fatalf("key %q", key)
	}
}

func TestSplitSubmissionRecipients(t *testing.T) {
	to, cc, bcc := splitSubmissionRecipients(
		[]string{"Hidden@example.com", "a@example.com", "c@example.com"},
		[]string{"a@example.com", "left-out@example.com"}, []string{"c@example.com", "a@example.com"})
	if fmt.Sprint(to, cc, bcc) != "[a@example.com] [c@example.com] [hidden@example.com]" {
		t.Fatalf("to %v cc %v bcc %v", to, cc, bcc)
	}
}

func TestClassifySubmitError(t *testing.T) {
	for err, want := range map[error]domain.MailClientSubmitErrorKind{
		domain.ErrPermissionDenied:                                           domain.MailClientSubmitSenderNotAllowed,
		ErrMailboxNotFound:                                                   domain.MailClientSubmitSenderNotAllowed,
		fmt.Errorf("%w: attachments", ErrMailMessageInvalid):                 domain.MailClientSubmitInvalid,
		ErrMailIdempotencyConflict:                                           domain.MailClientSubmitConflict,
		ErrMailSendLimitReached:                                              domain.MailClientSubmitLimitReached,
		ErrMailSendingPaused:                                                 domain.MailClientSubmitPaused,
		ErrMailSenderNotConfigured:                                           domain.MailClientSubmitNotConfigured,
		fmt.Errorf("%w: provider_rejected", ErrMailDeliveryFailed):           domain.MailClientSubmitDeliveryFailed,
		ErrMailSendInProgress:                                                domain.MailClientSubmitInProgress,
		&domain.MailClientSubmitError{Kind: domain.MailClientSubmitTooLarge}: domain.MailClientSubmitTooLarge,
	} {
		if got := submitErrorKind(classifySubmitError(err)); got != want {
			t.Errorf("%v = %s; want %s", err, got, want)
		}
	}
	if got := classifySubmitError(fmt.Errorf("%w: attachments", ErrMailMessageInvalid)).Error(); !strings.Contains(got, "An attachment is not valid") {
		t.Errorf("reason = %s", got)
	}
	database := errors.New("connection refused")
	if got := classifySubmitError(database); got != database { //nolint:errorlint // the same value is passed through
		t.Errorf("an unknown error = %v; want it as it is", got)
	}
}

// TestClientSenderAllowed: a member sends from the Zmail mailboxes they may send from in their
// organization, and from nothing else.
func TestClientSenderAllowed(t *testing.T) {
	f := newMailClientFixture(t)
	ctx := context.Background()
	alice, bob := f.Login(t, "alice@mail.example", f.AliceSecret), f.Login(t, "bob@mail.example", f.BobSecret)
	for _, tc := range []struct {
		principal *domain.MailClientPrincipal
		address   string
		want      bool
	}{
		{alice, "alice@mail.example", true},
		{alice, "ALICE@mail.example", true},
		{alice, "helpdesk@mail.example", false}, // view only
		{alice, "deals@mail.example", false},
		{alice, "connected@outside.test", false},
		{alice, "nobody@mail.example", false},
		{alice, "not an address", false},
		{bob, "helpdesk@mail.example", true},
		{bob, "deals@mail.example", true},
		{bob, "alice@mail.example", false},
	} {
		got, err := f.Service.ClientSenderAllowed(ctx, tc.principal, tc.address)
		if err != nil || got != tc.want {
			t.Errorf("%d may send from %s = %t, %v; want %t", tc.principal.Viewer.UserID, tc.address, got, err, tc.want)
		}
	}
}

// TestSubmitClientMessage sends once under the mailbox's name, keeps blind copies out of the
// headers, and answers a retry of the same message with the first outcome.
func TestSubmitClientMessage(t *testing.T) {
	f := newMailClientFixture(t)
	ctx := context.Background()
	f.Exec(t, `UPDATE mailboxes SET display_name = 'Help Desk' WHERE id = ?`, f.Support.ID)
	bob := f.Login(t, "bob@mail.example", f.BobSecret)
	raw := submission(`"Bob Spoofed" <helpdesk@mail.example>`, "Carol <carol@example.com>, left-out@example.com", "dan@example.com", "Hello", "m1@bob-laptop", "Hi")
	input := domain.MailClientSubmission{
		EnvelopeFrom: "bob@mail.example",
		Recipients:   []string{"carol@example.com", "dan@example.com", "erin@example.com"},
		Raw:          raw,
	}

	result, err := f.Service.SubmitClientMessage(ctx, bob, input)
	if err != nil || result.Status != domain.MailClientSubmissionSent || result.MessageID == 0 {
		t.Fatalf("submit = %+v, %v", result, err)
	}
	if len(f.Transport.sent) != 1 {
		t.Fatalf("sent %d", len(f.Transport.sent))
	}
	sent := f.Transport.sent[0]
	if sent.FromAddress != "helpdesk@mail.example" || sent.FromName != "Help Desk" {
		t.Fatalf("from %q %q; want the mailbox's own name", sent.FromName, sent.FromAddress)
	}
	if fmt.Sprint(sent.To, sent.Cc, sent.Bcc) != "[carol@example.com] [dan@example.com] [erin@example.com]" {
		t.Fatalf("to %v cc %v bcc %v", sent.To, sent.Cc, sent.Bcc)
	}
	stored, err := f.Service.GetMessage(ctx, f.Bob, f.Support.ID, result.MessageID)
	if err != nil || fmt.Sprint(stored.Bcc) != "[erin@example.com]" || stored.Subject != "Hello" {
		t.Fatalf("stored = %+v, %v", stored, err)
	}

	again, err := f.Service.SubmitClientMessage(ctx, bob, input)
	if err != nil || again.MessageID != result.MessageID || len(f.Transport.sent) != 1 {
		t.Fatalf("retry = %+v, %v; sent %d", again, err, len(f.Transport.sent))
	}
	changed := input
	changed.Raw = submission("helpdesk@mail.example", "carol@example.com", "", "Hello", "m1@bob-laptop", "Something else")
	if _, err := f.Service.SubmitClientMessage(ctx, bob, changed); submitErrorKind(err) != domain.MailClientSubmitConflict {
		t.Fatalf("another message under the same Message-ID = %v", err)
	}

	for name, tc := range map[string]struct {
		principal *domain.MailClientPrincipal
		submit    domain.MailClientSubmission
		kind      domain.MailClientSubmitErrorKind
	}{
		"From of another member": {bob, domain.MailClientSubmission{EnvelopeFrom: "bob@mail.example", Recipients: []string{"c@example.com"},
			Raw: submission("alice@mail.example", "c@example.com", "", "s", "", "b")}, domain.MailClientSubmitSenderNotAllowed},
		"MAIL FROM of another member": {bob, domain.MailClientSubmission{EnvelopeFrom: "alice@mail.example", Recipients: []string{"c@example.com"},
			Raw: submission("bob@mail.example", "c@example.com", "", "s", "", "b")}, domain.MailClientSubmitSenderNotAllowed},
		"view only mailbox": {f.Login(t, "alice@mail.example", f.AliceSecret), domain.MailClientSubmission{EnvelopeFrom: "alice@mail.example", Recipients: []string{"c@example.com"},
			Raw: submission("helpdesk@mail.example", "c@example.com", "", "s", "", "b")}, domain.MailClientSubmitSenderNotAllowed},
		"two From addresses": {bob, domain.MailClientSubmission{EnvelopeFrom: "bob@mail.example", Recipients: []string{"c@example.com"},
			Raw: submission("bob@mail.example, deals@mail.example", "c@example.com", "", "s", "", "b")}, domain.MailClientSubmitSenderNotAllowed},
		"only blind copies": {bob, domain.MailClientSubmission{EnvelopeFrom: "bob@mail.example", Recipients: []string{"hidden@example.com"},
			Raw: submission("bob@mail.example", "undisclosed-recipients:;", "", "s", "", "b")}, domain.MailClientSubmitInvalid},
	} {
		if _, err := f.Service.SubmitClientMessage(ctx, tc.principal, tc.submit); submitErrorKind(err) != tc.kind {
			t.Errorf("%s = %v; want %s", name, err, tc.kind)
		}
	}
	if len(f.Transport.sent) != 1 {
		t.Fatalf("a refused submission was sent: %d", len(f.Transport.sent))
	}
}

// TestSubmitClientMessageRetriesAfterFailure: a mail app retries a message the provider refused,
// and the retry sends it again instead of replaying the failure.
func TestSubmitClientMessageRetriesAfterFailure(t *testing.T) {
	f := newMailClientFixture(t)
	ctx := context.Background()
	bob := f.Login(t, "bob@mail.example", f.BobSecret)
	input := domain.MailClientSubmission{EnvelopeFrom: "bob@mail.example", Recipients: []string{"carol@example.com"},
		Raw: submission("bob@mail.example", "carol@example.com", "", "Hello", "m2@bob-laptop", "Hi")}

	f.Transport.err = &mailtransport.Error{Code: "provider.http_503", Responded: true}
	if _, err := f.Service.SubmitClientMessage(ctx, bob, input); submitErrorKind(err) != domain.MailClientSubmitDeliveryFailed {
		t.Fatalf("failed send = %v", err)
	}
	f.Transport.err = nil
	result, err := f.Service.SubmitClientMessage(ctx, bob, input)
	if err != nil || result.Status != domain.MailClientSubmissionSent || len(f.Transport.sent) != 2 {
		t.Fatalf("retry = %+v, %v; sent %d", result, err, len(f.Transport.sent))
	}
	again, err := f.Service.SubmitClientMessage(ctx, bob, input)
	if err != nil || again.MessageID != result.MessageID || len(f.Transport.sent) != 2 {
		t.Fatalf("retry after success = %+v, %v", again, err)
	}
}

// TestSubmitClientMessageQueuesAtLimit: past the daily limit a submission waits like a console
// send, and an inline image keeps its Content-ID on the way.
func TestSubmitClientMessageQueuesAtLimit(t *testing.T) {
	f := newMailClientFixture(t)
	ctx := context.Background()
	bob := f.Login(t, "bob@mail.example", f.BobSecret)
	for i := range 5 {
		input := domain.MailClientSubmission{EnvelopeFrom: "bob@mail.example", Recipients: []string{"carol@example.com"},
			Raw: submission("bob@mail.example", "carol@example.com", "", "s", fmt.Sprintf("fill%d@x", i), "b")}
		if result, err := f.Service.SubmitClientMessage(ctx, bob, input); err != nil || result.Status != domain.MailClientSubmissionSent {
			t.Fatalf("fill %d = %+v, %v", i, result, err)
		}
	}
	raw := relatedSubmission(1, 3)
	result, err := f.Service.SubmitClientMessage(ctx, bob, domain.MailClientSubmission{
		EnvelopeFrom: "bob@mail.example", Recipients: []string{"carol@example.com"}, Raw: raw,
	})
	if err != nil || result.Status != domain.MailClientSubmissionQueued {
		t.Fatalf("over the limit = %+v, %v", result, err)
	}
	var stored struct {
		ContentID string
		Inline    bool
	}
	if err := f.DB.Raw(`SELECT content_id, inline FROM mail_attachments WHERE message_id = ?`, result.MessageID).Scan(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ContentID != "logo0@x" || !stored.Inline {
		t.Fatalf("stored attachment = %+v", stored)
	}
	var subject string
	if err := f.DB.Raw(`SELECT subject FROM mail_messages WHERE id = ?`, result.MessageID).Scan(&subject).Error; err != nil || subject != "logo" {
		t.Fatalf("subject %q, %v", subject, err)
	}
}

// TestSubmitClientMessageFillsSubjectAndBody: a message without a subject or a body still sends.
func TestSubmitClientMessageFillsSubjectAndBody(t *testing.T) {
	f := newMailClientFixture(t)
	bob := f.Login(t, "bob@mail.example", f.BobSecret)
	raw := []byte("From: bob@mail.example\r\nTo: carol@example.com\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=MIX\r\n\r\n--MIX\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=a.pdf\r\n\r\npdf\r\n--MIX--\r\n")
	result, err := f.Service.SubmitClientMessage(context.Background(), bob, domain.MailClientSubmission{
		EnvelopeFrom: "bob@mail.example", Recipients: []string{"carol@example.com"}, Raw: raw,
	})
	if err != nil || result.Status != domain.MailClientSubmissionSent {
		t.Fatalf("submit = %+v, %v", result, err)
	}
	sent := f.Transport.sent[0]
	if sent.Subject != submissionNoSubject || len(sent.Attachments) != 1 {
		t.Fatalf("subject %q attachments %d", sent.Subject, len(sent.Attachments))
	}
}
