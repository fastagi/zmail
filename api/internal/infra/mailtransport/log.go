package mailtransport

import (
	"context"
	"log/slog"
)

// Log records outbound mail instead of delivering it. It exists for local development and
// tests; production configuration rejects it.
type Log struct {
	logger *slog.Logger
}

var _ Transport = (*Log)(nil)

// NewLog builds the development transport.
func NewLog(logger *slog.Logger) *Log {
	if logger == nil {
		logger = slog.Default()
	}
	return &Log{logger: logger}
}

// Send logs the envelope only. Subjects and bodies stay out of the log.
func (l *Log) Send(ctx context.Context, mail Message) (Result, error) {
	l.logger.InfoContext(ctx, "mailbox outbound mail recorded by the log transport",
		slog.String("from", mail.FromAddress),
		slog.Int("to_count", len(mail.To)),
		slog.Int("cc_count", len(mail.Cc)),
		slog.Int("text_bytes", len(mail.TextBody)),
		slog.Int("html_bytes", len(mail.HTMLBody)),
	)
	// Nothing is delivered, so nothing is reported per recipient.
	return Result{}, nil
}
