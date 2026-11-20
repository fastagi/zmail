package mailbox

import (
	"context"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// triageClaimLease is how long a claimed row stays with its worker before another may take it.
const triageClaimLease = 2 * time.Minute

// triageAvailable reports whether a model is configured and the triage store exists.
func (s *service) triageAvailable() bool {
	return s.triage != nil && s.settings.Triage.Available()
}

// enqueueTriage queues a new inbound message for AI triage in the delivery's transaction when a
// model is configured; the repository checks both switches in the same statement.
func (s *service) enqueueTriage(ctx context.Context, message *domain.MailMessage) error {
	if !s.triageAvailable() || message.Direction != domain.MailDirectionInbound {
		return nil
	}
	_, err := s.triage.EnqueueTriage(ctx, message.ID, message.MailboxID, s.now())
	return err
}
