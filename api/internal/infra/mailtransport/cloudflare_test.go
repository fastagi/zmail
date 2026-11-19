package mailtransport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/infra/config"
)

func cloudflareAgainst(t *testing.T, handler http.HandlerFunc) *Cloudflare {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return newCloudflare(config.MailboxConfig{
		CloudflareAccountID: "acct123",
		CloudflareAPIToken:  "token-value",
		RequestTimeout:      2 * time.Second,
	}, server.Client(), server.URL)
}

func TestCloudflareSendsTheDocumentedRequest(t *testing.T) {
	var (
		path, authorization string
		body                map[string]any
	)
	transport := cloudflareAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		path, authorization = r.URL.Path, r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":{"message_id":"<abc123@mail.example>","delivered":["bob@example.com"],"permanent_bounces":[],"queued":["amy@example.org"],"suppressed_recipients":[]}}`))
	})

	result, err := transport.Send(context.Background(), Message{
		FromAddress: "hello@mail.example", FromName: "Hello",
		To: []string{"bob@example.com"}, Cc: []string{"amy@example.org"},
		Subject: "Hi", TextBody: "text", HTMLBody: "<p>html</p>", InReplyTo: "prev@example.com",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result.MessageID != "abc123@mail.example" || len(result.Delivered) != 1 || len(result.Queued) != 1 {
		t.Fatalf("result = %+v; want the provider's id without brackets and its recipient lists", result)
	}
	if path != "/accounts/acct123/email/sending/send" || authorization != "Bearer token-value" {
		t.Fatalf("request = %s, %q", path, authorization)
	}
	from, _ := body["from"].(map[string]any)
	if from["address"] != "hello@mail.example" || from["name"] != "Hello" {
		t.Fatalf("from = %v", body["from"])
	}
	headers, _ := body["headers"].(map[string]any)
	if headers["In-Reply-To"] != "<prev@example.com>" || headers["References"] != "<prev@example.com>" {
		t.Fatalf("headers = %v", body["headers"])
	}
	if body["subject"] != "Hi" || body["text"] != "text" || body["html"] != "<p>html</p>" {
		t.Fatalf("body = %v", body)
	}
	if to, _ := body["to"].([]any); len(to) != 1 || to[0] != "bob@example.com" {
		t.Fatalf("to = %v", body["to"])
	}
	if _, present := body["message_id"]; present {
		t.Fatalf("body carries a provider-controlled field: %v", body)
	}
}

func TestCloudflareMapsProviderFailures(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		body      string
		code      string
		permanent bool
	}{
		{"documented error", http.StatusForbidden, `{"success":false,"errors":[{"code":10203,"message":"email.sending.error.email.sending_disabled"}],"result":null}`, "email.sending.error.email.sending_disabled", true},
		{"throttled is retryable", http.StatusTooManyRequests, `{"success":false,"errors":[{"code":10004,"message":"email.sending.error.throttled"}]}`, "email.sending.error.throttled", false},
		{"free-form reason is not stored", http.StatusBadRequest, `{"success":false,"errors":[{"code":1,"message":"Recipient bob@example.com rejected!"}]}`, "provider.http_400", true},
		{"server error", http.StatusInternalServerError, `oops`, "provider.http_500", false},
		{"success flag false on 200", http.StatusOK, `{"success":false,"errors":[{"code":2,"message":"email.sending.error.internal"}]}`, "email.sending.error.internal", false},
		{"unreadable success body", http.StatusOK, `not json`, failureInvalidResponse, false},
		{"missing result", http.StatusOK, `{"success":true}`, failureInvalidResponse, false},
		{"every recipient bounced", http.StatusOK, `{"success":true,"result":{"delivered":[],"permanent_bounces":["bob@example.com"],"queued":[]}}`, failurePermanentBounce, true},
		{"oversized body", http.StatusOK, strings.Repeat("x", maxProviderResponseBytes+1), failureResponseTooLarge, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			transport := cloudflareAgainst(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			})
			_, err := transport.Send(context.Background(), Message{FromAddress: "a@mail.example", To: []string{"b@example.com"}, Subject: "s", TextBody: "t"})
			var transportErr *Error
			if !errors.As(err, &transportErr) {
				t.Fatalf("error = %v; want *Error", err)
			}
			if transportErr.Code != test.code || transportErr.Permanent != test.permanent {
				t.Fatalf("error = %+v; want code %q permanent %v", transportErr, test.code, test.permanent)
			}
		})
	}
}

func TestCloudflareReportsWhoWasRefusedWhenNobodyIsAccepted(t *testing.T) {
	transport := cloudflareAgainst(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"result":{"message_id":"m1@mail.example","delivered":[],"permanent_bounces":[],"queued":[],"suppressed_recipients":["b@example.com"]}}`))
	})
	result, err := transport.Send(context.Background(), Message{FromAddress: "a@mail.example", To: []string{"b@example.com"}, Subject: "s", TextBody: "t"})
	var transportErr *Error
	if !errors.As(err, &transportErr) || transportErr.Code != failureSuppressed || !transportErr.Permanent {
		t.Fatalf("error = %v; want a permanent %q", err, failureSuppressed)
	}
	if len(result.Suppressed) != 1 || result.MessageID != "m1@mail.example" {
		t.Fatalf("result = %+v; the refusal must still name the recipient", result)
	}
}

func TestCloudflareIgnoresAnUnusableMessageID(t *testing.T) {
	for _, id := range []string{"", "has space@x", "<a@b> <c@d>"} {
		if got := normalizeProviderMessageID(id); got != "" {
			t.Fatalf("normalizeProviderMessageID(%q) = %q; want empty", id, got)
		}
	}
}

func TestCloudflareAcceptsQueuedDelivery(t *testing.T) {
	transport := cloudflareAgainst(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"result":{"delivered":[],"permanent_bounces":[],"queued":["b@example.com"]}}`))
	})
	if _, err := transport.Send(context.Background(), Message{FromAddress: "a@mail.example", To: []string{"b@example.com"}, Subject: "s", TextBody: "t"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
}

func TestCloudflareReportsAnUnreachableProvider(t *testing.T) {
	transport := cloudflareAgainst(t, func(http.ResponseWriter, *http.Request) {})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := transport.Send(ctx, Message{FromAddress: "a@mail.example", To: []string{"b@example.com"}, Subject: "s", TextBody: "t"})
	var transportErr *Error
	if !errors.As(err, &transportErr) || transportErr.Code != failureTransport || transportErr.Permanent {
		t.Fatalf("error = %v; want a retryable %q", err, failureTransport)
	}
}

func TestNewSelectsTheConfiguredTransport(t *testing.T) {
	if _, err := New(nil).Send(context.Background(), Message{}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("nil config error = %v", err)
	}

	development := &config.Config{}
	if _, ok := New(development).(*Log); !ok {
		t.Fatalf("development default = %T; want *Log", New(development))
	}

	cloudflare := &config.Config{Mailbox: config.MailboxConfig{
		Provider: " Cloudflare ", CloudflareAccountID: "acct", CloudflareAPIToken: "token",
	}}
	if _, ok := New(cloudflare).(*Cloudflare); !ok {
		t.Fatalf("cloudflare provider = %T; want *Cloudflare", New(cloudflare))
	}

	unknown := &config.Config{Mailbox: config.MailboxConfig{Provider: "smtp"}}
	if _, err := New(unknown).Send(context.Background(), Message{}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("unknown provider error = %v", err)
	}
}

func TestCloudflareSendsAttachmentsAsBase64(t *testing.T) {
	var body map[string]any
	transport := cloudflareAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":{"message_id":"<a@mail.example>","delivered":[],"permanent_bounces":[],"queued":["bob@example.com"],"suppressed_recipients":[]}}`))
	})
	_, err := transport.Send(context.Background(), Message{
		FromAddress: "hello@mail.example", To: []string{"bob@example.com"}, Subject: "Hi", TextBody: "text",
		Attachments: []Attachment{{Filename: "a.pdf", ContentType: "application/pdf", Content: []byte("%PDF-1.4")}},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	attachments, _ := body["attachments"].([]any)
	if len(attachments) != 1 {
		t.Fatalf("attachments = %v", body["attachments"])
	}
	first, _ := attachments[0].(map[string]any)
	if first["content"] != "JVBERi0xLjQ=" || first["filename"] != "a.pdf" || first["type"] != "application/pdf" ||
		first["disposition"] != "attachment" {
		t.Fatalf("attachment = %v", first)
	}
}

func TestCloudflareSendsMessageIDAndReferences(t *testing.T) {
	var body map[string]any
	transport := cloudflareAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":{"message_id":"<provider@cf.example>","delivered":["bob@example.com"]}}`))
	})

	_, err := transport.Send(context.Background(), Message{
		FromAddress: "hello@mail.example", To: []string{"bob@example.com"}, Subject: "Re: Hi", TextBody: "text",
		MessageID:  "0123456789abcdef0123456789abcdef@mail.example",
		InReplyTo:  "parent@example.com",
		References: []string{"root@example.com", "middle@example.com", "parent@example.com"},
		// A caller header never replaces the threading headers.
		Headers: map[string]string{"Message-ID": "<forged@evil.test>", "List-Unsubscribe": "<https://u.example>"},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	headers, _ := body["headers"].(map[string]any)
	if headers["Message-ID"] != "<0123456789abcdef0123456789abcdef@mail.example>" {
		t.Fatalf("Message-ID = %v; want our own id in angle brackets", headers["Message-ID"])
	}
	if headers["In-Reply-To"] != "<parent@example.com>" {
		t.Fatalf("In-Reply-To = %v", headers["In-Reply-To"])
	}
	if headers["References"] != "<root@example.com> <middle@example.com> <parent@example.com>" {
		t.Fatalf("References = %v; want the whole chain, oldest first", headers["References"])
	}
	if headers["List-Unsubscribe"] != "<https://u.example>" {
		t.Fatalf("headers = %v; want caller headers kept", headers)
	}

	// Without an id or a reply, no threading header is sent.
	body = nil
	if _, err := transport.Send(context.Background(), Message{
		FromAddress: "hello@mail.example", To: []string{"bob@example.com"}, Subject: "Hi", TextBody: "text",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, present := body["headers"]; present {
		t.Fatalf("headers = %v; want none", body["headers"])
	}
}
