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

	"github.com/fastagi/fastmail/api/internal/infra/config"
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
		_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":{"delivered":["bob@example.com"],"permanent_bounces":[],"queued":[]}}`))
	})

	err := transport.Send(context.Background(), Message{
		FromAddress: "hello@mail.example", FromName: "Hello",
		To: []string{"bob@example.com"}, Cc: []string{"amy@example.org"},
		Subject: "Hi", TextBody: "text", HTMLBody: "<p>html</p>", InReplyTo: "prev@example.com",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
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
			err := transport.Send(context.Background(), Message{FromAddress: "a@mail.example", To: []string{"b@example.com"}, Subject: "s", TextBody: "t"})
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

func TestCloudflareAcceptsQueuedDelivery(t *testing.T) {
	transport := cloudflareAgainst(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"result":{"delivered":[],"permanent_bounces":[],"queued":["b@example.com"]}}`))
	})
	if err := transport.Send(context.Background(), Message{FromAddress: "a@mail.example", To: []string{"b@example.com"}, Subject: "s", TextBody: "t"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
}

func TestCloudflareReportsAnUnreachableProvider(t *testing.T) {
	transport := cloudflareAgainst(t, func(http.ResponseWriter, *http.Request) {})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := transport.Send(ctx, Message{FromAddress: "a@mail.example", To: []string{"b@example.com"}, Subject: "s", TextBody: "t"})
	var transportErr *Error
	if !errors.As(err, &transportErr) || transportErr.Code != failureTransport || transportErr.Permanent {
		t.Fatalf("error = %v; want a retryable %q", err, failureTransport)
	}
}

func TestNewSelectsTheConfiguredTransport(t *testing.T) {
	if err := New(nil).Send(context.Background(), Message{}); !errors.Is(err, ErrNotConfigured) {
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
	if err := New(unknown).Send(context.Background(), Message{}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("unknown provider error = %v", err)
	}
}
