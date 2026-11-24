package mailbox

import (
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/response"
)

// MailTriageSettingResponse is the organization's AI triage switches.
type MailTriageSettingResponse struct {
	// Available is false when the instance has no AI triage model; the feature is then hidden.
	Available       bool `json:"available"`
	Enabled         bool `json:"enabled"`
	DailyMessageCap int  `json:"daily_message_cap"`
	DraftEnabled    bool `json:"draft_enabled"`
	// InstanceDailyCap is the instance ceiling; the effective cap is the lower of the two.
	InstanceDailyCap int `json:"instance_daily_cap"`
	// UpdatedAt is null until the organization saves its settings.
	UpdatedAt *time.Time `json:"updated_at"`
}

func toMailTriageSettingResponse(setting TriageSetting) MailTriageSettingResponse {
	response := MailTriageSettingResponse{
		Available: setting.Available, Enabled: setting.Enabled, DailyMessageCap: setting.DailyMessageCap,
		DraftEnabled: setting.DraftEnabled, InstanceDailyCap: setting.InstanceDailyCap,
	}
	if setting.Saved {
		response.UpdatedAt = new(setting.UpdatedAt.UTC())
	}
	return response
}

// UpdateMailTriageSettingRequest replaces the organization's AI triage switches.
type UpdateMailTriageSettingRequest struct {
	Enabled         *bool `json:"enabled" binding:"required"`
	DailyMessageCap *int  `json:"daily_message_cap" binding:"required"`
	DraftEnabled    *bool `json:"draft_enabled" binding:"required"`
}

// SetMailboxTriageSettingRequest turns a mailbox's AI triage on or off.
type SetMailboxTriageSettingRequest struct {
	Enabled *bool `json:"enabled" binding:"required"`
}

// UpdateMailTriageRequest is a reader's decision on one inbound message.
type UpdateMailTriageRequest struct {
	Category string     `json:"category" binding:"required"`
	DueAt    *time.Time `json:"due_at"`
}

// MailTriageResponse is a message's triage result as readers see it.
type MailTriageResponse struct {
	Category string     `json:"category"`
	Reason   string     `json:"reason"`
	DueAt    *time.Time `json:"due_at"`
	// Source is "ai" or "manual".
	Source string `json:"source"`
}

func toMailTriageResponse(triage *domain.MailTriage) *MailTriageResponse {
	if !triage.Visible() {
		return nil
	}
	response := &MailTriageResponse{Category: string(triage.Category), Reason: triage.Reason, Source: string(triage.Source)}
	if triage.DueAt != nil {
		response.DueAt = new(triage.DueAt.UTC())
	}
	return response
}

// ReplyDraftResponse is a generated reply draft; it is never stored.
type ReplyDraftResponse struct {
	Body  string               `json:"body"`
	Basis []ReplyDraftBasisDTO `json:"basis"`
}

// ReplyDraftBasisDTO names one thread message or template the draft relied on.
type ReplyDraftBasisDTO struct {
	// Kind is "message" or "template".
	Kind  string `json:"kind"`
	ID    uint   `json:"id"`
	Label string `json:"label"`
}

// GetTriageSetting returns the organization's AI triage switches to any member.
func (h *Handler) GetTriageSetting(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	setting, err := h.triage.TriageSetting(c.Request.Context(), viewer)
	if err != nil {
		response.HandleError(c, "Failed to read the AI triage setting", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailTriageSettingResponse(setting))
}

// UpdateTriageSetting replaces the organization's AI triage switches; owners and admins only.
func (h *Handler) UpdateTriageSetting(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	var req UpdateMailTriageSettingRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	setting, err := h.triage.SaveTriageSetting(c.Request.Context(), viewer, TriageSettingInput{
		Enabled: *req.Enabled, DailyMessageCap: *req.DailyMessageCap, DraftEnabled: *req.DraftEnabled,
	})
	if err != nil {
		respondOrganizeError(c, "Failed to change the AI triage setting", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailTriageSettingResponse(setting))
}

// SetMailboxTriageSetting turns a mailbox's AI triage on or off.
func (h *Handler) SetMailboxTriageSetting(c *gin.Context) {
	viewer, mailboxID, ok := mailboxScope(c)
	if !ok {
		return
	}
	var req SetMailboxTriageSettingRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	mailbox, err := h.triage.SetMailboxTriage(c.Request.Context(), viewer, mailboxID, *req.Enabled)
	if err != nil {
		response.HandleError(c, "Failed to change the mailbox AI triage switch", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailboxResponse(mailbox))
}

// UpdateMessageTriage records a reader's decision on an inbound message.
func (h *Handler) UpdateMessageTriage(c *gin.Context) {
	viewer, mailboxID, messageID, ok := messageScope(c)
	if !ok {
		return
	}
	var req UpdateMailTriageRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	triage, err := h.triage.OverrideTriage(c.Request.Context(), viewer, mailboxID, messageID, TriageOverrideInput{
		Category: domain.MailTriageCategory(req.Category), DueAt: req.DueAt,
	})
	if err != nil {
		respondOrganizeError(c, "Failed to change the message triage", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailTriageResponse(triage))
}

// CreateReplyDraft drafts a reply to an inbound message. The draft is returned once and never
// stored.
func (h *Handler) CreateReplyDraft(c *gin.Context) {
	viewer, mailboxID, messageID, ok := messageScope(c)
	if !ok {
		return
	}
	draft, err := h.triage.DraftReply(c.Request.Context(), viewer, mailboxID, messageID)
	if err != nil {
		respondOrganizeError(c, "Failed to draft a reply", err)
		return
	}
	basis := make([]ReplyDraftBasisDTO, 0, len(draft.Basis))
	for _, item := range draft.Basis {
		basis = append(basis, ReplyDraftBasisDTO(item))
	}
	privateResponse(c)
	response.Success(c, ReplyDraftResponse{Body: draft.Body, Basis: basis})
}

func toInboxTriageCountsResponse(counts *domain.MailTriageCounts) *InboxTriageCountsResponse {
	if counts == nil {
		return nil
	}
	return &InboxTriageCountsResponse{Reply: counts.Reply, Deadline: counts.Deadline, FYI: counts.FYI, Review: counts.Review}
}

// parseTriageCategories reads a comma-separated list of distinct categories.
func parseTriageCategories(raw string) ([]domain.MailTriageCategory, bool) {
	parts := strings.Split(raw, ",")
	if len(parts) > len(domain.MailTriageCategories) {
		return nil, false
	}
	categories := make([]domain.MailTriageCategory, 0, len(parts))
	for _, part := range parts {
		category := domain.MailTriageCategory(part)
		if !category.Valid() || slices.Contains(categories, category) {
			return nil, false
		}
		categories = append(categories, category)
	}
	return categories, true
}
