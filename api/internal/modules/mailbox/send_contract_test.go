package mailbox

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
)

// addresses returns n distinct quoted recipient addresses joined for a JSON array.
func addresses(prefix string, n int) string {
	quoted := make([]string, 0, n)
	for i := range n {
		quoted = append(quoted, fmt.Sprintf("%q", fmt.Sprintf("%s%d@example.com", prefix, i)))
	}
	return strings.Join(quoted, ",")
}

// Issue 76: every recipient rule of a send answers MAILBOX.MESSAGE_INVALID, as the contract says,
// not the binding layer's COMMON.VALIDATION_FAILED.
func TestSendRecipientLimitsAnswerMessageInvalid(t *testing.T) {
	fixture := newHandlerFixture(t, membership(domain.OrganizationRoleMember))
	if _, err := fixture.service.CreateMailbox(t.Context(), as(7), sharedWithHandlerUser("helpdesk")); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	long := strings.Repeat("a", 310) + "@example.com"
	for name, recipients := range map[string]string{
		"51 to":           `"to":[` + addresses("to", 51) + `]`,
		"51 cc":           `"to":["bob@example.com"],"cc":[` + addresses("cc", 51) + `]`,
		"to and cc 51":    `"to":[` + addresses("to", 26) + `],"cc":[` + addresses("cc", 25) + `]`,
		"long address":    `"to":["` + long + `"]`,
		"empty to":        `"to":[]`,
		"missing to":      `"cc":["bob@example.com"]`,
		"empty address":   `"to":[""]`,
		"display name":    `"to":["Bob <bob@example.com>"]`,
		"long cc address": `"to":["bob@example.com"],"cc":["` + long + `"]`,
	} {
		sent, body := fixture.do(http.MethodPost, "/mailboxes/1/messages", `{`+recipients+`,"subject":"Hi","text":"Body"}`)
		if sent.Code != http.StatusUnprocessableEntity || body["error_code"] != domain.CodeMailMessageInvalid {
			t.Errorf("%s = %d %v; want 422 %s", name, sent.Code, body["error_code"], domain.CodeMailMessageInvalid)
		}
	}
	// 50 recipients after duplicates are removed is allowed.
	sent, body := fixture.do(http.MethodPost, "/mailboxes/1/messages",
		`{"to":[`+addresses("to", 25)+`],"cc":[`+addresses("cc", 25)+`,"to0@example.com"],"subject":"Hi","text":"Body"}`)
	if sent.Code != http.StatusCreated {
		t.Fatalf("50 recipients = %d %v; want 201", sent.Code, body)
	}
}

// Issue 77: the detail's message_id is the RFC Message-ID the message was sent with, even when the
// provider reports no id of its own (the log transport), so a client can reply to it.
func TestOutboundDetailCarriesTheRFCMessageID(t *testing.T) {
	fixture := newHandlerFixture(t, membership(domain.OrganizationRoleMember))
	if _, err := fixture.service.CreateMailbox(t.Context(), as(7), sharedWithHandlerUser("helpdesk")); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	sent, body := fixture.do(http.MethodPost, "/mailboxes/1/messages", `{"to":["bob@example.com"],"subject":"Hi","text":"Body"}`)
	if sent.Code != http.StatusCreated {
		t.Fatalf("send = %d %v", sent.Code, body)
	}
	data, _ := body["data"].(map[string]any)
	messageID, _ := data["message_id"].(string)
	if !strings.HasSuffix(messageID, "@mail.example") {
		t.Fatalf("message_id = %q; want the generated RFC Message-ID on the mailbox domain", messageID)
	}

	// A provider id never stands in for the RFC Message-ID.
	outbound := &domain.MailMessage{
		Direction: domain.MailDirectionOutbound, MessageID: "0100018c-ses-id", RFCMessageID: "abc@mail.example",
	}
	if got := toMailMessageResponse(outbound).MessageID; got != "abc@mail.example" {
		t.Fatalf("outbound message_id = %q; want the RFC Message-ID", got)
	}
	// An outbound message sent before Zmail generated ids has no known RFC Message-ID.
	legacy := &domain.MailMessage{Direction: domain.MailDirectionOutbound, MessageID: "0100018c-ses-id"}
	if got := toMailMessageResponse(legacy).MessageID; got != "" {
		t.Fatalf("legacy outbound message_id = %q; want empty", got)
	}
	// Inbound mail keeps the sender's Message-ID.
	inbound := &domain.MailMessage{Direction: domain.MailDirectionInbound, MessageID: "abc1@example.org"}
	if got := toMailMessageResponse(inbound).MessageID; got != "abc1@example.org" {
		t.Fatalf("inbound message_id = %q; want the sender's Message-ID", got)
	}
}
