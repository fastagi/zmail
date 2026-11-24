package triage

import (
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// MailTriageSettingResponse is the organization's AI triage switches.
type MailTriageSettingResponse struct {
	Available        bool       `json:"available"`
	Enabled          bool       `json:"enabled"`
	DailyMessageCap  int        `json:"daily_message_cap"`
	DraftEnabled     bool       `json:"draft_enabled"`
	InstanceDailyCap int        `json:"instance_daily_cap"`
	UpdatedAt        *time.Time `json:"updated_at"`
}

func toMailTriageSettingResponse(setting domain.MailTriageSettingView) MailTriageSettingResponse {
	res := MailTriageSettingResponse{
		Available:        setting.Available,
		Enabled:          setting.Enabled,
		DailyMessageCap:  setting.DailyMessageCap,
		DraftEnabled:     setting.DraftEnabled,
		InstanceDailyCap: setting.InstanceDailyCap,
	}
	if setting.Saved {
		ts := setting.UpdatedAt.UTC()
		res.UpdatedAt = &ts
	}
	return res
}

// UpdateMailTriageSettingRequest replaces the organization's AI triage switches.
type UpdateMailTriageSettingRequest struct {
	Enabled         *bool `json:"enabled" binding:"required"`
	DailyMessageCap *int  `json:"daily_message_cap" binding:"required"`
	DraftEnabled    *bool `json:"draft_enabled" binding:"required"`
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
	Source   string     `json:"source"`
}

func toMailTriageResponse(triage *domain.MailTriage) *MailTriageResponse {
	if !triage.Visible() {
		return nil
	}
	res := &MailTriageResponse{
		Category: string(triage.Category),
		Reason:   triage.Reason,
		Source:   string(triage.Source),
	}
	if triage.DueAt != nil {
		ts := triage.DueAt.UTC()
		res.DueAt = &ts
	}
	return res
}

// ReplyDraftBasisDTO names one thread message or template the draft relied on.
type ReplyDraftBasisDTO struct {
	Kind  string `json:"kind"`
	ID    uint   `json:"id"`
	Label string `json:"label"`
}

// ReplyDraftResponse is a generated reply draft; it is never stored.
type ReplyDraftResponse struct {
	Body  string               `json:"body"`
	Basis []ReplyDraftBasisDTO `json:"basis"`
}
