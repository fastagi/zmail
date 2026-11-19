// Package mailtransport provides the outbound transports of the mailbox starter.
package mailtransport

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fastagi/zmail/api/internal/infra/config"
)

const (
	defaultCloudflareBaseURL = "https://api.cloudflare.com/client/v4"
	maxProviderResponseBytes = 64 * 1024
	maxFailureCodeLength     = 120

	failureTransport        = "transport.unreachable"
	failureInvalidResponse  = "transport.invalid_response"
	failureResponseTooLarge = "transport.response_too_large"
	failurePermanentBounce  = "transport.permanent_bounce"
	failureSuppressed       = "transport.suppressed"
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
	// Attachments carry standard base64 content.
	Attachments []cloudflareAttachment `json:"attachments,omitempty"`
}

type cloudflareAttachment struct {
	Content     string `json:"content"`
	Filename    string `json:"filename"`
	Type        string `json:"type"`
	Disposition string `json:"disposition"`
}

type cloudflareSendResponse struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result *struct {
		MessageID            string   `json:"message_id"`
		Delivered            []string `json:"delivered"`
		PermanentBounces     []string `json:"permanent_bounces"`
		Queued               []string `json:"queued"`
		SuppressedRecipients []string `json:"suppressed_recipients"`
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

// Send delivers one message and reports the provider's message identifier and what it said
// about each recipient.
func (c *Cloudflare) Send(ctx context.Context, mail Message) (Result, error) {
	if c == nil || c.token == "" || c.client == nil {
		return Result{}, ErrNotConfigured
	}
	body := cloudflareSendRequest{
		From:    cloudflareAddress{Address: mail.FromAddress, Name: mail.FromName},
		To:      mail.To,
		Cc:      mail.Cc,
		Subject: mail.Subject,
		Text:    mail.TextBody,
		HTML:    mail.HTMLBody,
	}
	for _, attachment := range mail.Attachments {
		body.Attachments = append(body.Attachments, cloudflareAttachment{
			Content:     base64.StdEncoding.EncodeToString(attachment.Content),
			Filename:    attachment.Filename,
			Type:        attachment.ContentType,
			Disposition: "attachment",
		})
	}
	body.Headers = sendHeaders(mail)
	payload, err := json.Marshal(body)
	if err != nil {
		return Result{}, fmt.Errorf("encode mail request: %w", err)
	}

	requestContext, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return Result{}, fmt.Errorf("build mail request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.token)

	response, err := c.client.Do(request)
	if err != nil {
		// The provider may or may not have accepted the message; report it as retryable.
		return Result{}, &Error{Code: failureTransport}
	}
	defer response.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(response.Body, maxProviderResponseBytes+1))
	if err != nil {
		return Result{}, &Error{Code: failureTransport}
	}
	if len(raw) > maxProviderResponseBytes {
		return Result{}, &Error{Code: failureResponseTooLarge}
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
		return Result{}, &Error{Code: code, Permanent: permanent}
	}
	if decodeErr != nil || parsed.Result == nil {
		return Result{}, &Error{Code: failureInvalidResponse}
	}
	result := Result{
		MessageID:  normalizeProviderMessageID(parsed.Result.MessageID),
		Delivered:  parsed.Result.Delivered,
		Queued:     parsed.Result.Queued,
		Bounced:    parsed.Result.PermanentBounces,
		Suppressed: parsed.Result.SuppressedRecipients,
	}
	if len(result.Delivered) == 0 && len(result.Queued) == 0 {
		// Nobody was accepted. The result still tells the caller who was refused and why.
		switch {
		case len(result.Bounced) > 0:
			return result, &Error{Code: failurePermanentBounce, Permanent: true}
		case len(result.Suppressed) > 0:
			return result, &Error{Code: failureSuppressed, Permanent: true}
		}
	}
	return result, nil
}

// sendHeaders lists the extra headers of a message. The threading headers always come from the
// message's own fields, never from a caller header of the same name.
func sendHeaders(mail Message) map[string]string {
	headers := make(map[string]string, len(mail.Headers)+3)
	for name, value := range mail.Headers {
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "message-id", "in-reply-to", "references":
			continue
		}
		headers[name] = value
	}
	if mail.MessageID != "" {
		headers["Message-ID"] = "<" + mail.MessageID + ">"
	}
	references := mail.References
	if mail.InReplyTo != "" {
		headers["In-Reply-To"] = "<" + mail.InReplyTo + ">"
		if len(references) == 0 {
			references = []string{mail.InReplyTo}
		}
	}
	if len(references) > 0 {
		bracketed := make([]string, 0, len(references))
		for _, id := range references {
			bracketed = append(bracketed, "<"+id+">")
		}
		headers["References"] = strings.Join(bracketed, " ")
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}

// normalizeProviderMessageID strips angle brackets and rejects anything that is not a short
// single token, so a provider value is safe to store and to match later events against.
func normalizeProviderMessageID(raw string) string {
	id := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(raw), "<"), ">")
	if id == "" || len(id) > 900 || strings.ContainsAny(id, " \t\r\n<>") {
		return ""
	}
	return id
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
