package mailtransport

import (
	"context"
	"errors"

	"github.com/fastagi/zmail/api/internal/infra/config"
)

// ErrNotConfigured means no outbound provider is configured for this process.
var ErrNotConfigured = errors.New("mail transport is not configured")

// Message is one outbound message handed to a provider.
type Message struct {
	FromAddress string
	FromName    string
	To          []string
	Cc          []string
	Subject     string
	TextBody    string
	HTMLBody    string
	// InReplyTo is an RFC 5322 message identifier without angle brackets.
	InReplyTo string
	// Attachments are files sent with the message.
	Attachments []Attachment
	// Headers are extra headers such as List-Unsubscribe.
	Headers map[string]string
}

// Attachment is one file of an outbound message.
type Attachment struct {
	Filename    string
	ContentType string
	Content     []byte
}

// Error reports a provider rejection with a stable, non-sensitive code.
type Error struct {
	Code string
	// Permanent tells that retrying the same message cannot succeed.
	Permanent bool
}

func (e *Error) Error() string {
	if e == nil || e.Code == "" {
		return "mail transport failed"
	}
	return "mail transport failed: " + e.Code
}

// Result is what the provider reported when it accepted a message. A recipient appears in at
// most one list; a provider that reports nothing per recipient leaves them all empty.
type Result struct {
	// MessageID is the provider's identifier for the message, without angle brackets. Later
	// delivery events name the message by it.
	MessageID string
	// Delivered recipients were handed to their mail server during the send call.
	Delivered []string
	// Queued recipients were accepted and will be attempted later.
	Queued []string
	// Bounced recipients were permanently refused.
	Bounced []string
	// Suppressed recipients were not attempted because the provider's suppression list has them.
	Suppressed []string
}

// Transport sends one outbound message through a provider. An error means no recipient was
// accepted; the Result may still say why.
type Transport interface {
	Send(ctx context.Context, mail Message) (Result, error)
}

// New selects the outbound transport from typed configuration. An unknown or unset provider
// yields a transport that refuses to send, so a misconfigured process fails closed.
func New(cfg *config.Config) Transport {
	if cfg == nil {
		return unconfigured{}
	}
	mailbox := cfg.Mailbox.Resolved(cfg.IsProduction())
	switch mailbox.Provider {
	case config.MailboxProviderCloudflare:
		return NewCloudflare(mailbox)
	case config.MailboxProviderLog:
		return NewLog(nil)
	default:
		return unconfigured{}
	}
}

type unconfigured struct{}

func (unconfigured) Send(_ context.Context, _ Message) (Result, error) {
	return Result{}, ErrNotConfigured
}
