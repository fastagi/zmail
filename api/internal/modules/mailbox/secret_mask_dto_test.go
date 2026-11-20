package mailbox

import (
	"strings"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
)

func codeMessage(direction domain.MailDirection) *domain.MailMessage {
	return &domain.MailMessage{
		ID: 7, MailboxID: 3, Direction: direction,
		Subject:  "Your verification code is 482913",
		TextBody: "Use code 482913 to sign in.",
		HTMLBody: "<p>Use code <b>482913</b> to sign in.</p>",
	}
}

func TestInboundResponsesMaskSecrets(t *testing.T) {
	message := codeMessage(domain.MailDirectionInbound)

	summary := toMailMessageSummary(message)
	if summary.Subject != "Your verification code is ••••••" {
		t.Fatalf("summary subject %q", summary.Subject)
	}
	detail := toMailMessageResponse(message)
	if strings.Contains(detail.Subject+detail.TextBody+detail.HTMLBody, "482913") || !detail.SecretsMasked {
		t.Fatalf("detail leaked or not flagged: %+v", detail)
	}
	if detail.HTMLBody != "<p>Use code <b>••••••</b> to sign in.</p>" {
		t.Fatalf("html %q", detail.HTMLBody)
	}
	logEntries := toMailLogEntries([]*domain.MailMessage{message})
	if strings.Contains(logEntries[0].Subject, "482913") {
		t.Fatalf("log subject leaked: %q", logEntries[0].Subject)
	}
	items := toInboxItemResponses([]InboxItem{{Message: message, Snippet: "x"}})
	if strings.Contains(items[0].Subject, "482913") {
		t.Fatalf("inbox subject leaked: %q", items[0].Subject)
	}
	if message.Subject != "Your verification code is 482913" {
		t.Fatalf("masking changed the stored message: %q", message.Subject)
	}
}

func TestRevealedResponseKeepsOriginal(t *testing.T) {
	message := codeMessage(domain.MailDirectionInbound)
	revealed := toRevealedMailMessageResponse(message)
	if revealed.Subject != message.Subject || revealed.TextBody != message.TextBody ||
		revealed.HTMLBody != message.HTMLBody || revealed.SecretsMasked {
		t.Fatalf("revealed response changed: %+v", revealed)
	}
}

func TestOutboundResponsesAreNotMasked(t *testing.T) {
	message := codeMessage(domain.MailDirectionOutbound)
	detail := toMailMessageResponse(message)
	if detail.Subject != message.Subject || detail.TextBody != message.TextBody || detail.SecretsMasked {
		t.Fatalf("outbound masked: %+v", detail)
	}
}

func TestPlainInboundDetailIsNotFlagged(t *testing.T) {
	message := &domain.MailMessage{Direction: domain.MailDirectionInbound, Subject: "Lunch?", TextBody: "Order 48291377"}
	if detail := toMailMessageResponse(message); detail.SecretsMasked || detail.TextBody != "Order 48291377" {
		t.Fatalf("plain message changed: %+v", detail)
	}
}

func TestInboxItemSnippetMasksInboundSource(t *testing.T) {
	inbound := &domain.MailMessage{Direction: domain.MailDirectionInbound}
	if got := inboxItemSnippet(inbound, "验证码 482913\n有效"); got != "验证码 •••••• 有效" {
		t.Fatalf("inbound snippet %q", got)
	}
	outbound := &domain.MailMessage{Direction: domain.MailDirectionOutbound}
	if got := inboxItemSnippet(outbound, "code 482913"); got != "code 482913" {
		t.Fatalf("outbound snippet %q", got)
	}
	cut := strings.Repeat(" ", inboxSnippetSource-7) + "code 48"
	if got := inboxItemSnippet(inbound, cut); got != "code" {
		t.Fatalf("cut tail kept: %q", got)
	}
}
