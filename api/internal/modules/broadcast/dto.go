package broadcast

import (
	"math"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// MailBroadcastRequest creates or replaces a draft broadcast.
type MailBroadcastRequest struct {
	Name             string            `json:"name" binding:"required,max=400"`
	MailboxID        uint              `json:"mailbox_id" binding:"required,min=1"`
	TemplateID       uint              `json:"template_id" binding:"required,min=1"`
	ListID           uint              `json:"list_id" binding:"required,min=1"`
	UnsubscribeLabel string            `json:"unsubscribe_label" binding:"max=200"`
	TestPercent      int               `json:"test_percent" binding:"min=0,max=50"`
	TestWaitMinutes  int               `json:"test_wait_minutes" binding:"min=0,max=1440"`
	Variables        map[string]string `json:"variables"`
}

func (r MailBroadcastRequest) input() domain.MailBroadcastInput {
	return domain.MailBroadcastInput(r)
}

// MailBroadcastSendRequest starts a draft now, or at scheduled_at when in the future.
type MailBroadcastSendRequest struct {
	ScheduledAt *time.Time `json:"scheduled_at"`
}

// MailBroadcastCountsResponse counts the recipients of a broadcast by status.
type MailBroadcastCountsResponse struct {
	Total      int `json:"total"`
	Pending    int `json:"pending"`
	Sent       int `json:"sent"`
	Failed     int `json:"failed"`
	Skipped    int `json:"skipped"`
	Canceled   int `json:"canceled"`
	Bounced    int `json:"bounced"`
	Complained int `json:"complained"`
}

// MailBroadcastProgressResponse is the send rate and estimated finish of a running broadcast.
type MailBroadcastProgressResponse struct {
	RatePerMinute      *float64   `json:"rate_per_minute"`
	RateWindowMinutes  int        `json:"rate_window_minutes"`
	EstimatedFinishAt  *time.Time `json:"estimated_finish_at"`
	SendLimitPauses    bool       `json:"send_limit_pauses"`
	SendLimitResumesAt *time.Time `json:"send_limit_resumes_at"`
	ComputedAt         time.Time  `json:"computed_at"`
}

// MailBroadcastResponse is the public shape of a broadcast.
type MailBroadcastResponse struct {
	ID               uint                           `json:"id"`
	Name             string                         `json:"name"`
	MailboxID        uint                           `json:"mailbox_id"`
	TemplateID       uint                           `json:"template_id"`
	ListID           uint                           `json:"list_id"`
	UnsubscribeLabel string                         `json:"unsubscribe_label"`
	Variables        map[string]string              `json:"variables"`
	Status           string                         `json:"status"`
	ScheduledAt      *time.Time                     `json:"scheduled_at"`
	StartedAt        *time.Time                     `json:"started_at"`
	FinishedAt       *time.Time                     `json:"finished_at"`
	StopReason       string                         `json:"stop_reason"`
	TestPercent      int                            `json:"test_percent"`
	TestWaitMinutes  int                            `json:"test_wait_minutes"`
	CurrentWave      int                            `json:"current_wave"`
	ResumeAt         *time.Time                     `json:"resume_at"`
	SafetyOverride   bool                           `json:"safety_override"`
	Counts           MailBroadcastCountsResponse    `json:"counts"`
	Progress         *MailBroadcastProgressResponse `json:"progress"`
	CreatedAt        time.Time                      `json:"created_at"`
	UpdatedAt        time.Time                      `json:"updated_at"`
}

// MailBroadcastRecipientResponse is one recipient of a broadcast.
type MailBroadcastRecipientResponse struct {
	ID            uint      `json:"id"`
	ContactID     uint      `json:"contact_id"`
	Email         string    `json:"email"`
	Status        string    `json:"status"`
	MessageID     *uint     `json:"message_id"`
	FailureCode   string    `json:"failure_code"`
	LastEvent     string    `json:"last_event"`
	ContactStatus *string   `json:"contact_status"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func utcTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	at := value.UTC().Truncate(time.Second)
	return &at
}

func toMailBroadcastProgressResponse(progress *domain.MailBroadcastProgress) *MailBroadcastProgressResponse {
	if progress == nil {
		return nil
	}
	res := &MailBroadcastProgressResponse{
		RateWindowMinutes:  int(progress.RateWindow / time.Minute),
		EstimatedFinishAt:  utcTime(progress.EstimatedFinishAt),
		SendLimitPauses:    progress.SendLimitPauses,
		SendLimitResumesAt: utcTime(progress.SendLimitResumesAt),
		ComputedAt:         progress.ComputedAt.UTC(),
	}
	if progress.RatePerMinute != nil {
		rounded := math.Round(*progress.RatePerMinute*100) / 100
		res.RatePerMinute = &rounded
	}
	return res
}

func toMailBroadcastResponse(broadcast *domain.MailBroadcast) MailBroadcastResponse {
	vars := broadcast.Variables
	if vars == nil {
		vars = map[string]string{}
	}
	return MailBroadcastResponse{
		ID:               broadcast.ID,
		Name:             broadcast.Name,
		MailboxID:        broadcast.MailboxID,
		TemplateID:       broadcast.TemplateID,
		ListID:           broadcast.ListID,
		UnsubscribeLabel: broadcast.UnsubscribeLabel,
		Variables:        vars,
		Status:           string(broadcast.Status),
		ScheduledAt:      broadcast.ScheduledAt,
		StartedAt:        broadcast.StartedAt,
		FinishedAt:       broadcast.FinishedAt,
		StopReason:       broadcast.StopReason,
		TestPercent:      broadcast.TestPercent,
		TestWaitMinutes:  broadcast.TestWaitMinutes,
		CurrentWave:      broadcast.CurrentWave,
		ResumeAt:         broadcast.ResumeAt,
		SafetyOverride:   broadcast.SafetyOverride,
		Counts:           MailBroadcastCountsResponse(broadcast.Counts),
		CreatedAt:        broadcast.CreatedAt,
		UpdatedAt:        broadcast.UpdatedAt,
	}
}
