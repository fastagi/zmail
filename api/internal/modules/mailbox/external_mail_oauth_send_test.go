package mailbox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
)

func TestGmailSendMailSuccessAndBcc(t *testing.T) {
	var capturedAuth string
	var capturedRaw string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"gmail-access-123","expires_in":3600}`))
		case "/users/me/messages/send":
			capturedAuth = r.Header.Get("Authorization")
			var body struct {
				Raw string `json:"raw"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode body: %v", err)
			}
			rawBytes, err := base64.RawURLEncoding.DecodeString(body.Raw)
			if err != nil {
				t.Errorf("decode raw base64url: %v", err)
			}
			capturedRaw = string(rawBytes)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"gmail-msg-42"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	src := &gmailMailSource{
		clientID:      "cid",
		clientSecret:  "csec",
		tokenEndpoint: srv.URL + "/token",
		apiBase:       srv.URL,
		httpClient:    srv.Client(),
	}

	res, err := src.SendMail(context.Background(), ExternalMailToken{RefreshToken: "ref-1"}, mailtransport.Message{
		FromAddress: "alice@gmail.com",
		To:          []string{"bob@example.com"},
		Bcc:         []string{"secret@example.com"},
		Subject:     "Hello via Gmail OAuth",
		TextBody:    "Body text",
	})
	if err != nil {
		t.Fatalf("SendMail failed: %v", err)
	}
	if res.MessageID != "gmail-msg-42" {
		t.Fatalf("MessageID = %q, want gmail-msg-42", res.MessageID)
	}
	if capturedAuth != "Bearer gmail-access-123" {
		t.Fatalf("Authorization = %q", capturedAuth)
	}
	if !strings.Contains(capturedRaw, "Bcc: secret@example.com\r\n") {
		t.Fatalf("expected Bcc header in raw MIME, got:\n%s", capturedRaw)
	}
	if !strings.Contains(capturedRaw, "Subject: Hello via Gmail OAuth") {
		t.Fatalf("expected Subject in raw MIME")
	}
}

func TestGmailSendMailAuthFailureAndTransientRetry(t *testing.T) {
	status := http.StatusUnauthorized
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	defer srv.Close()

	src := &gmailMailSource{apiBase: srv.URL, httpClient: srv.Client()}

	_, err := src.SendMail(context.Background(), ExternalMailToken{AccessToken: "tok"}, mailtransport.Message{
		FromAddress: "alice@gmail.com", To: []string{"bob@example.com"}, Subject: "Test", TextBody: "Hi",
	})
	var transportErr *mailtransport.Error
	if !errors.As(err, &transportErr) || transportErr.Code != failureOAuthAuth || !transportErr.Permanent {
		t.Fatalf("want permanent %s, got %v", failureOAuthAuth, err)
	}

	status = http.StatusServiceUnavailable
	_, err = src.SendMail(context.Background(), ExternalMailToken{AccessToken: "tok"}, mailtransport.Message{
		FromAddress: "alice@gmail.com", To: []string{"bob@example.com"}, Subject: "Test", TextBody: "Hi",
	})
	if !errors.As(err, &transportErr) || transportErr.Code != failureOAuthUnavailable || transportErr.Permanent {
		t.Fatalf("want transient %s, got %v", failureOAuthUnavailable, err)
	}
}

func TestOutlookSendMailSuccess(t *testing.T) {
	var capturedCT, capturedAuth, capturedRaw string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/oauth2/v2.0/token"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"ms-access-99","expires_in":3600}`))
		case r.URL.Path == "/me/sendMail":
			capturedCT = r.Header.Get("Content-Type")
			capturedAuth = r.Header.Get("Authorization")
			bodyBytes, _ := io.ReadAll(r.Body)
			decoded, err := base64.StdEncoding.DecodeString(string(bodyBytes))
			if err != nil {
				t.Errorf("decode base64 MIME: %v", err)
			}
			capturedRaw = string(decoded)
			w.WriteHeader(http.StatusAccepted)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	src := &outlookMailSource{
		clientID:     "cid",
		clientSecret: "csec",
		tenant:       "common",
		loginBaseURL: srv.URL,
		graphBaseURL: srv.URL,
		client:       srv.Client(),
	}

	res, err := src.SendMail(context.Background(), ExternalMailToken{RefreshToken: "ms-ref-1"}, mailtransport.Message{
		FromAddress: "carol@outlook.com",
		To:          []string{"dave@example.com"},
		Subject:     "Hello via Outlook Graph",
		TextBody:    "Graph MIME body",
	})
	if err != nil {
		t.Fatalf("Outlook SendMail failed: %v", err)
	}
	if res.RFCMessageID == "" {
		t.Fatalf("expected non-empty RFCMessageID")
	}
	if capturedCT != "text/plain" {
		t.Fatalf("Content-Type = %q, want text/plain", capturedCT)
	}
	if capturedAuth != "Bearer ms-access-99" {
		t.Fatalf("Authorization = %q", capturedAuth)
	}
	if !strings.Contains(capturedRaw, "Subject: Hello via Outlook Graph") {
		t.Fatalf("expected Subject in MIME body")
	}
}
