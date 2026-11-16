// Package mailtransport provides the outbound transports of the mailbox starter.
package mailtransport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fastagi/fastmail/api/internal/infra/config"
)

const (
	defaultCloudflareBaseURL = "https://api.cloudflare.com/client/v4"
	maxProviderResponseBytes = 64 * 1024
	maxFailureCodeLength     = 120

	failureTransport        = "transport.unreachable"
	failureInvalidResponse  = "transport.invalid_response"
	failureResponseTooLarge = "transport.response_too_large"
	failurePermanentBounce  = "transport.permanent_bounce"
)

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Cloudflare sends mail through the Cloudflare Email Sending REST API.
type Cloudflare struct {
	endpoint string
	token    string
	timeout  time.Duration
	client   httpDoer
}

var _ Transport = (*Cloudflare)(nil)

type cloudflareAddress struct {
	Address string `json:"address"`
	Name    string `json:"name,omitempty"`
}

type cloudflareSendRequest struct {
	From    cloudflareAddress `json:"from"`
	To      []string          `json:"to"`
	Cc      []string          `json:"cc,omitempty"`
	Subject string            `json:"subject"`
	Text    string            `json:"text,omitempty"`
	HTML    string            `json:"html,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type cloudflareSendResponse struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result *struct {
		Delivered        []string `json:"delivered"`
		PermanentBounces []string `json:"permanent_bounces"`
		Queued           []string `json:"queued"`
	} `json:"result"`
}

// NewCloudflare builds the transport from resolved mailbox configuration.
func NewCloudflare(cfg config.MailboxConfig) *Cloudflare {
	return newCloudflare(cfg, nil, defaultCloudflareBaseURL)
}

func newCloudflare(cfg config.MailboxConfig, client httpDoer, baseURL string) *Cloudflare {
	timeout := cfg.RequestTimeout
	if timeout <= 0 {
		timeout = config.DefaultMailboxRequestTimeout
	}
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/accounts/" +
		url.PathEscape(strings.TrimSpace(cfg.CloudflareAccountID)) + "/email/sending/send"
	return &Cloudflare{
		endpoint: endpoint,
		token:    strings.TrimSpace(cfg.CloudflareAPIToken),
		timeout:  timeout,
		client:   client,
	}
}

// Send delivers one message. The REST API does not report a message identifier.
func (c *Cloudflare) Send(ctx context.Context, mail Message) error {
	if c == nil || c.token == "" || c.client == nil {
		return ErrNotConfigured
	}
	body := cloudflareSendRequest{
		From:    cloudflareAddress{Address: mail.FromAddress, Name: mail.FromName},
		To:      mail.To,
		Cc:      mail.Cc,
		Subject: mail.Subject,
		Text:    mail.TextBody,
		HTML:    mail.HTMLBody,
	}
	if mail.InReplyTo != "" {
		reference := "<" + mail.InReplyTo + ">"
		body.Headers = map[string]string{"In-Reply-To": reference, "References": reference}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode mail request: %w", err)
	}

	requestContext, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build mail request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.token)

	response, err := c.client.Do(request)
	if err != nil {
		// The provider may or may not have accepted the message; report it as retryable.
		return &Error{Code: failureTransport}
	}
	defer response.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(response.Body, maxProviderResponseBytes+1))
	if err != nil {
		return &Error{Code: failureTransport}
	}
	if len(raw) > maxProviderResponseBytes {
		return &Error{Code: failureResponseTooLarge}
	}

	var parsed cloudflareSendResponse
	decodeErr := json.Unmarshal(raw, &parsed)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices || (decodeErr == nil && !parsed.Success) {
		code := fmt.Sprintf("provider.http_%d", response.StatusCode)
		if decodeErr == nil && len(parsed.Errors) > 0 {
			if reason := sanitizeFailureCode(parsed.Errors[0].Message); reason != "" {
				code = reason
			}
		}
		permanent := response.StatusCode >= 400 && response.StatusCode < 500 &&
			response.StatusCode != http.StatusTooManyRequests && response.StatusCode != http.StatusRequestTimeout
		return &Error{Code: code, Permanent: permanent}
	}
	if decodeErr != nil || parsed.Result == nil {
		return &Error{Code: failureInvalidResponse}
	}
	if len(parsed.Result.Delivered) == 0 && len(parsed.Result.Queued) == 0 && len(parsed.Result.PermanentBounces) > 0 {
		return &Error{Code: failurePermanentBounce, Permanent: true}
	}
	return nil
}

// sanitizeFailureCode keeps a provider reason only when it is a short dotted identifier, so
// free-form provider text never reaches storage or clients.
func sanitizeFailureCode(reason string) string {
	reason = strings.ToLower(strings.TrimSpace(reason))
	if reason == "" || len(reason) > maxFailureCodeLength {
		return ""
	}
	for _, char := range reason {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9', char == '.', char == '_':
		default:
			return ""
		}
	}
	return reason
}
