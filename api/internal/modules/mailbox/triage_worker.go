package mailbox

import (
	"context"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/modules/mailbox/triage"
)

var _ domain.MailTriageWorker = (*service)(nil)

// DispatchTriage runs one worker pass by delegating to the isolated triage subpackage worker.
func (s *service) DispatchTriage(ctx context.Context, limit int) (int, error) {
	worker := triage.NewWorker(
		s.triage,
		s.repo.FindMessage,
		ErrMailMessageNotFound,
		s.triageModel,
		s.settings.Triage,
		s.now,
		mailboxSecretMasker{},
	)
	return worker.DispatchTriage(ctx, limit)
}
