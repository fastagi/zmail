package mailtransport

import (
	"context"
	"errors"

	"github.com/fastagi/fastmail/api/internal/infra/config"
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

// Transport sends one outbound message through a provider.
type Transport interface {
	Send(ctx context.Context, mail Message) error
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

func (unconfigured) Send(_ context.Context, _ Message) error {
	return ErrNotConfigured
}
