package domain

import (
	"context"
	"strings"
	"time"
)

const (
	// MaxLiveMailAgentGrantsPerMailbox bounds the live grants of one mailbox.
	MaxLiveMailAgentGrantsPerMailbox = 10
	// MaxMailAgentGrantLifetime is how far ahead a grant may expire.
	MaxMailAgentGrantLifetime = 90 * 24 * time.Hour
	// MaxMailAgentGrantFilters bounds each filter list of a grant.
	MaxMailAgentGrantFilters = 20
	// MaxMailAgentFeedPage is the most events one feed read returns.
	MaxMailAgentFeedPage = 100
	// DefaultMailAgentFeedPage is the page size of a feed read without limit.
	DefaultMailAgentFeedPage = 50
	// MaxMailAgentActivity is the most audit rows the activity of a grant shows.
	MaxMailAgentActivity = 100
	// MaxMailAgentDraftBytes bounds the text and the HTML of an agent draft.
	MaxMailAgentDraftBytes = 100 * 1024
)

// MailAgentEventType names one kind of agent feed event.
type MailAgentEventType string

// MailAgentEventMessageReceived announces a newly stored inbound message.
const MailAgentEventMessageReceived MailAgentEventType = "message.received"

// MailAgentAction is what an agent did, as its audit records it.
type MailAgentAction string

const (
	MailAgentActionRead         MailAgentAction = "read"
	MailAgentActionReadRevealed MailAgentAction = "read_revealed"
	MailAgentActionDraft        MailAgentAction = "draft"
)

// MailAgentRevokeReason says why a grant ended before it expired.
type MailAgentRevokeReason string

const (
	// MailAgentRevokedByPerson is a revocation by someone who may issue grants of the mailbox.
	MailAgentRevokedByPerson MailAgentRevokeReason = "revoked"
	// MailAgentRevokedOwnerLeft ends the grants of a key whose owner left the organization.
	MailAgentRevokedOwnerLeft MailAgentRevokeReason = "left_organization"
	// MailAgentRevokedHandedOver ends the grants of a personal mailbox that was handed over.
	MailAgentRevokedHandedOver MailAgentRevokeReason = "handed_over"
	// MailAgentRevokedMemberRemoved ends the grants of a key whose owner lost a shared mailbox.
	MailAgentRevokedMemberRemoved MailAgentRevokeReason = "member_removed"
)

// MailAgentGrantStatus is a grant's state as responses show it.
type MailAgentGrantStatus string

const (
	MailAgentGrantActive  MailAgentGrantStatus = "active"
	MailAgentGrantExpired MailAgentGrantStatus = "expired"
	MailAgentGrantRevoked MailAgentGrantStatus = "revoked"
)

// MailAgentGrant lets one API key receive one mailbox's new mail. The key acts with its owner's
// current access, so a grant never reaches further than that person can.
type MailAgentGrant struct {
	ID             uint
	OrganizationID uint
	MailboxID      uint
	APIKeyID       uint
	// KeyUserID is the key's owner when the grant was issued.
	KeyUserID uint
	GrantedBy uint
	// CanDraft allows reply drafts; reading is always allowed.
	CanDraft bool
	// RevealSecrets returns one-time codes and sign-in links unmasked.
	RevealSecrets bool
	// SenderFilters are lowercase addresses (with @) or domains; empty matches every sender.
	SenderFilters []string
	// LabelIDs are labels of the mailbox; empty matches every message.
	LabelIDs     []uint
	ExpiresAt    time.Time
	RevokedAt    *time.Time
	RevokedBy    *uint
	RevokeReason MailAgentRevokeReason
	CreatedAt    time.Time
	UpdatedAt    time.Time
	// APIKeyName and APIKeyPrefix describe the key; only listings fill them.
	APIKeyName   string
	APIKeyPrefix string
}

// Live reports whether the grant still receives mail: not revoked and not expired.
func (g *MailAgentGrant) Live(now time.Time) bool {
	return g != nil && g.RevokedAt == nil && now.Before(g.ExpiresAt)
}

// Status derives the grant's state at now.
func (g *MailAgentGrant) Status(now time.Time) MailAgentGrantStatus {
	switch {
	case g.RevokedAt != nil:
		return MailAgentGrantRevoked
	case !now.Before(g.ExpiresAt):
		return MailAgentGrantExpired
	default:
		return MailAgentGrantActive
	}
}

// Capabilities lists what the grant allows, read first.
func (g *MailAgentGrant) Capabilities() []string {
	if g.CanDraft {
		return []string{"read", "draft"}
	}
	return []string{"read"}
}

// MatchesSender reports whether a sender address passes the sender filters. An entry with @
// matches the whole address; any other entry matches the sender's domain or a subdomain of it.
func (g *MailAgentGrant) MatchesSender(address string) bool {
	if len(g.SenderFilters) == 0 {
		return true
	}
	address = strings.ToLower(strings.TrimSpace(address))
	_, senderDomain, _ := strings.Cut(address, "@")
	for _, filter := range g.SenderFilters {
		if strings.Contains(filter, "@") {
			if filter == address {
				return true
			}
			continue
		}
		if senderDomain == filter || strings.HasSuffix(senderDomain, "."+filter) {
			return true
		}
	}
	return false
}

// MatchesLabels reports whether a message carrying labelIDs passes the label filters.
func (g *MailAgentGrant) MatchesLabels(labelIDs []uint) bool {
	if len(g.LabelIDs) == 0 {
		return true
	}
	for _, want := range g.LabelIDs {
		for _, have := range labelIDs {
			if want == have {
				return true
			}
		}
	}
	return false
}

// MailAgentEvent is one entry of a grant's feed. Its id is the feed cursor.
type MailAgentEvent struct {
	ID             uint
	GrantID        uint
	OrganizationID uint
	MailboxID      uint
	MessageID      uint
	Type           MailAgentEventType
	CreatedAt      time.Time
}

// MailAgentAuditEntry records one agent read or draft by ids only, never content.
type MailAgentAuditEntry struct {
	ID             uint
	OrganizationID uint
	MailboxID      uint
	GrantID        uint
	APIKeyID       uint
	MessageID      uint
	Action         MailAgentAction
	CreatedAt      time.Time
}

// MailAgentDraft is a reply draft an agent stored for a person to review. Sending stays a
// person's action.
type MailAgentDraft struct {
	ID             uint
	OrganizationID uint
	MailboxID      uint
	GrantID        uint
	MessageID      uint
	APIKeyID       uint
	TextBody       string
	HTMLBody       string
	// Status is "pending"; nothing else exists yet.
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// MailAgentGrantRevocation selects live grants to revoke. OrganizationID is required, and at least
// one of the other fields narrows it.
type MailAgentGrantRevocation struct {
	OrganizationID uint
	MailboxID      uint
	GrantID        uint
	KeyUserID      uint
	Reason         MailAgentRevokeReason
	// RevokedBy is the person who revoked; zero when Zmail did.
	RevokedBy uint
	At        time.Time
}

// MailAgentRepository stores agent grants, their feed, audit, and drafts.
type MailAgentRepository interface {
	// FindAgentKey reads an API key by id, deleted keys excluded; nil when there is none.
	FindAgentKey(ctx context.Context, keyID uint) (*APIKey, error)
	// CreateAgentGrant stores a grant under the mailbox's row lock. It refuses a second effective
	// grant of the same key and mailbox, and a grant past maxLive effective grants of the mailbox.
	CreateAgentGrant(ctx context.Context, grant *MailAgentGrant, maxLive int) error
	// FindAgentGrant reads one grant of a mailbox; a grant created before sealedBefore is hidden.
	FindAgentGrant(ctx context.Context, mailboxID, grantID uint, sealedBefore *time.Time) (*MailAgentGrant, error)
	// ListAgentGrants pages a mailbox's grants created at or after sealedBefore, newest first,
	// with their keys' names.
	ListAgentGrants(ctx context.Context, mailboxID uint, sealedBefore *time.Time, page, pageSize int) ([]*MailAgentGrant, int64, error)
	// ListLiveAgentGrantsOfMailbox lists the effective grants of a mailbox at now: live, their key
	// usable, and their key's owner still able to read the mailbox.
	ListLiveAgentGrantsOfMailbox(ctx context.Context, mailboxID uint, now time.Time) ([]*MailAgentGrant, error)
	// ListLiveAgentGrantsOfKey lists the live grants of a key in an organization at now.
	ListLiveAgentGrantsOfKey(ctx context.Context, organizationID, keyID uint, now time.Time) ([]*MailAgentGrant, error)
	// RevokeAgentGrants revokes the live grants the revocation selects and returns them.
	RevokeAgentGrants(ctx context.Context, revocation MailAgentGrantRevocation) ([]*MailAgentGrant, error)
	// AppendAgentEvents writes feed events of one organization, at most one per grant and
	// message, in commit order of the organization.
	AppendAgentEvents(ctx context.Context, organizationID uint, events []*MailAgentEvent) error
	// ListAgentEvents reads the events of grants after a cursor, ascending, at most limit.
	ListAgentEvents(ctx context.Context, grantIDs []uint, after uint, limit int) ([]*MailAgentEvent, error)
	// ListAgentMessageEvents reads the events of grants for one message.
	ListAgentMessageEvents(ctx context.Context, grantIDs []uint, messageID uint) ([]*MailAgentEvent, error)
	// AddAgentAudit records one agent read or draft.
	AddAgentAudit(ctx context.Context, entry *MailAgentAuditEntry) error
	// ListAgentAudit reads a grant's newest audit rows created at or after sealedBefore.
	ListAgentAudit(ctx context.Context, grantID uint, sealedBefore *time.Time, limit int) ([]*MailAgentAuditEntry, error)
	// SaveAgentDraft stores the pending draft of a grant and message, replacing its body when one
	// exists; created reports a new draft.
	SaveAgentDraft(ctx context.Context, draft *MailAgentDraft) (created bool, err error)
	// ListAgentDrafts pages a mailbox's drafts of visible, unsealed messages, newest first.
	ListAgentDrafts(ctx context.Context, mailboxID uint, page, pageSize int) ([]*MailAgentDraft, int64, error)
	// DeleteAgentDrafts deletes every draft of a mailbox.
	DeleteAgentDrafts(ctx context.Context, mailboxID uint) error
	// PruneAgentEvents deletes up to limit events created before a time and returns how many.
	PruneAgentEvents(ctx context.Context, before time.Time, limit int) (int64, error)
	// PruneAgentAudit deletes up to limit audit rows created before a time and returns how many.
	PruneAgentAudit(ctx context.Context, before time.Time, limit int) (int64, error)
}

// MailAgentEventPruner deletes agent feed events and audit rows past retention.
type MailAgentEventPruner interface {
	// PruneAgentMailEvents deletes every feed event created before a time; the operator command runs it.
	PruneAgentMailEvents(ctx context.Context, before time.Time) (int64, error)
	// PruneExpiredAgentMail deletes up to limit events and audit rows past their configured
	// retention; the mail worker runs it every pass.
	PruneExpiredAgentMail(ctx context.Context, now time.Time, limit int) (int, error)
}
