package mailbox

import (
	"time"

	"github.com/fastagi/zmail/api/internal/modules/mailbox/transactional"
)

type (
	TransactionalMessagePO      = transactional.TransactionalMessagePO
	TransactionalMessageEventPO = transactional.TransactionalMessageEventPO
	MailSuppressionPO           = transactional.MailSuppressionPO
)

func pgTime(t time.Time) time.Time {
	return transactional.PGTime(t)
}

func pgTimePointer(t *time.Time) *time.Time {
	return transactional.PGTimePointer(t)
}

func normalizeEmail(email string) string {
	return transactional.NormalizeEmail(email)
}
