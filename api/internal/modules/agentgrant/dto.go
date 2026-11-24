package agentgrant

import (
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// CreateAgentGrantRequest issues an agent grant.
type CreateAgentGrantRequest struct {
	APIKeyID      uint                      `json:"api_key_id" binding:"required"`
	Capabilities  []string                  `json:"capabilities" binding:"required,min=1,max=2"`
	RevealSecrets bool                      `json:"reveal_secrets"`
	Filters       *AgentGrantFiltersRequest `json:"filters"`
	ExpiresAt     time.Time                 `json:"expires_at" binding:"required"`
}

// AgentGrantFiltersRequest narrows a grant to specific senders or labels.
type AgentGrantFiltersRequest struct {
	Senders  []string `json:"senders" binding:"max=20"`
	LabelIDs []uint   `json:"label_ids" binding:"max=20"`
}

func (r CreateAgentGrantRequest) input() domain.IssueMailAgentGrantInput {
	input := domain.IssueMailAgentGrantInput{
		APIKeyID:      r.APIKeyID,
		Capabilities:  r.Capabilities,
		RevealSecrets: r.RevealSecrets,
		ExpiresAt:     r.ExpiresAt,
	}
	if r.Filters != nil {
		input.Senders = r.Filters.Senders
		input.LabelIDs = r.Filters.LabelIDs
	}
	return input
}

// AgentGrantFiltersResponse is a grant's filters; empty lists match everything.
type AgentGrantFiltersResponse struct {
	Senders  []string `json:"senders"`
	LabelIDs []uint   `json:"label_ids"`
}

// AgentGrantResponse is one agent grant.
type AgentGrantResponse struct {
	ID            uint                      `json:"id"`
	MailboxID     uint                      `json:"mailbox_id"`
	APIKeyID      uint                      `json:"api_key_id"`
	APIKeyName    string                    `json:"api_key_name"`
	APIKeyPrefix  string                    `json:"api_key_prefix"`
	KeyUserID     uint                      `json:"key_user_id"`
	GrantedBy     uint                      `json:"granted_by"`
	Capabilities  []string                  `json:"capabilities"`
	RevealSecrets bool                      `json:"reveal_secrets"`
	Filters       AgentGrantFiltersResponse `json:"filters"`
	Status        string                    `json:"status"`
	ExpiresAt     time.Time                 `json:"expires_at"`
	RevokedAt     *time.Time                `json:"revoked_at"`
	RevokeReason  *string                   `json:"revoke_reason"`
	CreatedAt     time.Time                 `json:"created_at"`
}

func toAgentGrantResponse(grant *domain.MailAgentGrant, now time.Time) *AgentGrantResponse {
	senders := grant.SenderFilters
	if senders == nil {
		senders = []string{}
	}
	labelIDs := grant.LabelIDs
	if labelIDs == nil {
		labelIDs = []uint{}
	}
	res := &AgentGrantResponse{
		ID:            grant.ID,
		MailboxID:     grant.MailboxID,
		APIKeyID:      grant.APIKeyID,
		APIKeyName:    grant.APIKeyName,
		APIKeyPrefix:  grant.APIKeyPrefix,
		KeyUserID:     grant.KeyUserID,
		GrantedBy:     grant.GrantedBy,
		Capabilities:  grant.Capabilities(),
		RevealSecrets: grant.RevealSecrets,
		Filters:       AgentGrantFiltersResponse{Senders: senders, LabelIDs: labelIDs},
		Status:        string(grant.Status(now)),
		ExpiresAt:     grant.ExpiresAt,
		RevokedAt:     grant.RevokedAt,
		CreatedAt:     grant.CreatedAt,
	}
	if grant.RevokedAt != nil {
		reason := string(grant.RevokeReason)
		res.RevokeReason = &reason
	}
	return res
}

// AgentAuditResponse is one audited agent read or draft.
type AgentAuditResponse struct {
	ID        uint      `json:"id"`
	GrantID   uint      `json:"grant_id"`
	APIKeyID  uint      `json:"api_key_id"`
	MessageID uint      `json:"message_id"`
	Action    string    `json:"action"`
	CreatedAt time.Time `json:"created_at"`
}

// AgentDraftResponse is a reply draft awaiting a person.
type AgentDraftResponse struct {
	ID        uint      `json:"id"`
	MailboxID uint      `json:"mailbox_id"`
	GrantID   uint      `json:"grant_id"`
	MessageID uint      `json:"message_id"`
	APIKeyID  uint      `json:"api_key_id"`
	Text      string    `json:"text"`
	HTML      string    `json:"html"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func toAgentDraftResponse(draft *domain.MailAgentDraft) *AgentDraftResponse {
	return &AgentDraftResponse{
		ID:        draft.ID,
		MailboxID: draft.MailboxID,
		GrantID:   draft.GrantID,
		MessageID: draft.MessageID,
		APIKeyID:  draft.APIKeyID,
		Text:      draft.TextBody,
		HTML:      draft.HTMLBody,
		Status:    draft.Status,
		CreatedAt: draft.CreatedAt,
		UpdatedAt: draft.UpdatedAt,
	}
}

// AgentMailEventResponse is one feed event.
type AgentMailEventResponse struct {
	ID        uint      `json:"id"`
	Type      string    `json:"type"`
	GrantID   uint      `json:"grant_id"`
	MailboxID uint      `json:"mailbox_id"`
	MessageID uint      `json:"message_id"`
	CreatedAt time.Time `json:"created_at"`
}

// AgentMailFeedResponse is one page of the agent feed.
type AgentMailFeedResponse struct {
	Events     []AgentMailEventResponse `json:"events"`
	NextCursor string                   `json:"next_cursor"`
	HasMore    bool                     `json:"has_more"`
}

// CreateAgentDraftRequest stores an agent's reply draft.
type CreateAgentDraftRequest struct {
	MessageID uint   `json:"message_id" binding:"required"`
	Text      string `json:"text"`
	HTML      string `json:"html"`
}
