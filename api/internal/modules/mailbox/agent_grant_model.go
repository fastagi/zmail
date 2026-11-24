package mailbox

import "github.com/fastagi/zmail/api/internal/modules/mailbox/agentgrant"

var (
	// ErrMailAgentGrantNotFound answers a grant id that is not a grant of the mailbox.
	ErrMailAgentGrantNotFound = agentgrant.ErrNotFound
	// ErrMailAgentGrantInvalid rejects a grant request that cannot be applied.
	ErrMailAgentGrantInvalid = agentgrant.ErrInvalid
	// ErrMailAgentGrantLimit rejects a grant past the mailbox's live grants.
	ErrMailAgentGrantLimit = agentgrant.ErrLimit
	// ErrMailAgentGrantExists rejects a second live grant of one key on one mailbox.
	ErrMailAgentGrantExists = agentgrant.ErrExists
)

// ScopeMailAgent lets an API key use the agent routes: the feed, message reads, and drafts.
const ScopeMailAgent = agentgrant.ScopeMailAgent

type (
	MailAgentGrantPO = agentgrant.MailAgentGrantPO
	MailAgentEventPO = agentgrant.MailAgentEventPO
	MailAgentAuditPO = agentgrant.MailAgentAuditPO
	MailAgentDraftPO = agentgrant.MailAgentDraftPO
)

func agentStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
