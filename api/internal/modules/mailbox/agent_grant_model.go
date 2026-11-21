package mailbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

var (
	// ErrMailAgentGrantNotFound answers a grant id that is not a grant of the mailbox.
	ErrMailAgentGrantNotFound = errors.New("mail agent grant not found")
	// ErrMailAgentGrantInvalid rejects a grant request that cannot be applied: its key, its
	// capabilities, its filters, or its expiry.
	ErrMailAgentGrantInvalid = errors.New("mail agent grant is invalid")
	// ErrMailAgentGrantLimit rejects a grant past the mailbox's live grants.
	ErrMailAgentGrantLimit = fmt.Errorf("a mailbox has at most %d live agent grants", domain.MaxLiveMailAgentGrantsPerMailbox)
	// ErrMailAgentGrantExists rejects a second live grant of one key on one mailbox.
	ErrMailAgentGrantExists = errors.New("the key already holds a live grant of this mailbox")
)

// ScopeMailAgent lets an API key use the agent routes: the feed, message reads, and drafts. It
// grants nothing by itself; every mailbox needs its own live grant.
const ScopeMailAgent = "mail:agent"

// MailAgentGrantPO is the persistent object of an agent grant.
type MailAgentGrantPO struct {
	ID             uint      `gorm:"primaryKey"`
	OrganizationID uint      `gorm:"not null"`
	MailboxID      uint      `gorm:"not null;index"`
	APIKeyID       uint      `gorm:"column:api_key_id;not null;index"`
	KeyUserID      uint      `gorm:"not null"`
	GrantedBy      uint      `gorm:"not null"`
	CanDraft       bool      `gorm:"not null;default:false"`
	RevealSecrets  bool      `gorm:"not null;default:false"`
	SenderFilters  string    `gorm:"type:jsonb;not null;default:'[]'"`
	LabelIDs       string    `gorm:"column:label_ids;type:jsonb;not null;default:'[]'"`
	ExpiresAt      time.Time `gorm:"not null"`
	RevokedAt      *time.Time
	RevokedBy      *uint
	RevokeReason   string `gorm:"size:32;not null;default:''"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (MailAgentGrantPO) TableName() string { return "mail_agent_grants" }

// mailAgentGrantRow is a grant with its key's name, as listings read it.
type mailAgentGrantRow struct {
	MailAgentGrantPO
	APIKeyName   string
	APIKeyPrefix string
}

func (po *MailAgentGrantPO) toDomain() *domain.MailAgentGrant {
	grant := &domain.MailAgentGrant{
		ID: po.ID, OrganizationID: po.OrganizationID, MailboxID: po.MailboxID, APIKeyID: po.APIKeyID,
		KeyUserID: po.KeyUserID, GrantedBy: po.GrantedBy, CanDraft: po.CanDraft, RevealSecrets: po.RevealSecrets,
		ExpiresAt: po.ExpiresAt.UTC(), RevokedAt: agentUTCTime(po.RevokedAt), RevokedBy: po.RevokedBy,
		RevokeReason: domain.MailAgentRevokeReason(po.RevokeReason), CreatedAt: po.CreatedAt.UTC(), UpdatedAt: po.UpdatedAt.UTC(),
		SenderFilters: []string{}, LabelIDs: []uint{},
	}
	// Both columns are jsonb this module writes; an unreadable value reads as no filter, which
	// cannot happen through the service.
	_ = json.Unmarshal([]byte(po.SenderFilters), &grant.SenderFilters)
	_ = json.Unmarshal([]byte(po.LabelIDs), &grant.LabelIDs)
	return grant
}

func newMailAgentGrantPO(grant *domain.MailAgentGrant) (*MailAgentGrantPO, error) {
	senders, err := json.Marshal(agentStrings(grant.SenderFilters))
	if err != nil {
		return nil, err
	}
	labels := grant.LabelIDs
	if labels == nil {
		labels = []uint{}
	}
	labelJSON, err := json.Marshal(labels)
	if err != nil {
		return nil, err
	}
	return &MailAgentGrantPO{
		ID: grant.ID, OrganizationID: grant.OrganizationID, MailboxID: grant.MailboxID, APIKeyID: grant.APIKeyID,
		KeyUserID: grant.KeyUserID, GrantedBy: grant.GrantedBy, CanDraft: grant.CanDraft, RevealSecrets: grant.RevealSecrets,
		SenderFilters: string(senders), LabelIDs: string(labelJSON), ExpiresAt: grant.ExpiresAt.UTC(),
		RevokedAt: grant.RevokedAt, RevokedBy: grant.RevokedBy, RevokeReason: string(grant.RevokeReason),
		CreatedAt: grant.CreatedAt, UpdatedAt: grant.UpdatedAt,
	}, nil
}

func agentStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func agentUTCTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	utc := value.UTC()
	return &utc
}

// MailAgentEventPO is one feed event.
type MailAgentEventPO struct {
	ID             uint      `gorm:"primaryKey"`
	GrantID        uint      `gorm:"not null;uniqueIndex:idx_mail_agent_events_grant_message,priority:1"`
	OrganizationID uint      `gorm:"not null"`
	MailboxID      uint      `gorm:"not null"`
	MessageID      uint      `gorm:"not null;uniqueIndex:idx_mail_agent_events_grant_message,priority:2"`
	Type           string    `gorm:"size:32;not null"`
	CreatedAt      time.Time `gorm:"not null"`
}

func (MailAgentEventPO) TableName() string { return "mail_agent_events" }

func (po *MailAgentEventPO) toDomain() *domain.MailAgentEvent {
	return &domain.MailAgentEvent{
		ID: po.ID, GrantID: po.GrantID, OrganizationID: po.OrganizationID, MailboxID: po.MailboxID,
		MessageID: po.MessageID, Type: domain.MailAgentEventType(po.Type), CreatedAt: po.CreatedAt.UTC(),
	}
}

// MailAgentAuditPO is one audited agent read or draft.
type MailAgentAuditPO struct {
	ID             uint      `gorm:"primaryKey"`
	OrganizationID uint      `gorm:"not null"`
	MailboxID      uint      `gorm:"not null"`
	GrantID        uint      `gorm:"not null;index"`
	APIKeyID       uint      `gorm:"column:api_key_id;not null"`
	MessageID      uint      `gorm:"not null"`
	Action         string    `gorm:"size:16;not null"`
	CreatedAt      time.Time `gorm:"not null"`
}

func (MailAgentAuditPO) TableName() string { return "mail_agent_audit" }

func (po *MailAgentAuditPO) toDomain() *domain.MailAgentAuditEntry {
	return &domain.MailAgentAuditEntry{
		ID: po.ID, OrganizationID: po.OrganizationID, MailboxID: po.MailboxID, GrantID: po.GrantID,
		APIKeyID: po.APIKeyID, MessageID: po.MessageID, Action: domain.MailAgentAction(po.Action), CreatedAt: po.CreatedAt.UTC(),
	}
}

// MailAgentDraftPO is a pending reply draft of an agent.
type MailAgentDraftPO struct {
	ID             uint   `gorm:"primaryKey"`
	OrganizationID uint   `gorm:"not null"`
	MailboxID      uint   `gorm:"not null;index"`
	GrantID        uint   `gorm:"not null;uniqueIndex:idx_mail_agent_drafts_grant_message,priority:1"`
	MessageID      uint   `gorm:"not null;uniqueIndex:idx_mail_agent_drafts_grant_message,priority:2"`
	APIKeyID       uint   `gorm:"column:api_key_id;not null"`
	TextBody       string `gorm:"type:text;not null"`
	HTMLBody       string `gorm:"column:html_body;type:text;not null;default:''"`
	Status         string `gorm:"size:16;not null;default:'pending'"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (MailAgentDraftPO) TableName() string { return "mail_agent_drafts" }

func (po *MailAgentDraftPO) toDomain() *domain.MailAgentDraft {
	return &domain.MailAgentDraft{
		ID: po.ID, OrganizationID: po.OrganizationID, MailboxID: po.MailboxID, GrantID: po.GrantID,
		MessageID: po.MessageID, APIKeyID: po.APIKeyID, TextBody: po.TextBody, HTMLBody: po.HTMLBody,
		Status: po.Status, CreatedAt: po.CreatedAt.UTC(), UpdatedAt: po.UpdatedAt.UTC(),
	}
}
