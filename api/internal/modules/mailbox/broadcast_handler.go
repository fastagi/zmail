package mailbox

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/pagination"
	"github.com/fastagi/zmail/api/pkg/response"
)

// MailBroadcastRequest creates or replaces a draft broadcast.
type MailBroadcastRequest struct {
	Name             string `json:"name" binding:"required,max=400"`
	MailboxID        uint   `json:"mailbox_id" binding:"required,min=1"`
	TemplateID       uint   `json:"template_id" binding:"required,min=1"`
	ListID           uint   `json:"list_id" binding:"required,min=1"`
	UnsubscribeLabel string `json:"unsubscribe_label" binding:"max=200"`
	// TestPercent sends to this share of the list first, 0 to 50; 0 sends to everyone at once.
	TestPercent int `json:"test_percent" binding:"min=0,max=50"`
	// TestWaitMinutes is the wait after the test wave, 5 to 1440; 0 means 30.
	TestWaitMinutes int `json:"test_wait_minutes" binding:"min=0,max=1440"`
	// Variables are template values shared by every recipient.
	Variables map[string]string `json:"variables"`
}

func (r MailBroadcastRequest) input() BroadcastInput {
	return BroadcastInput(r)
}

// MailBroadcastSendRequest starts a draft now, or at scheduled_at when it is in the future.
type MailBroadcastSendRequest struct {
	ScheduledAt *time.Time `json:"scheduled_at"`
}

// MailBroadcastCountsResponse counts the recipients of a broadcast by status.
type MailBroadcastCountsResponse struct {
	Total    int `json:"total"`
	Pending  int `json:"pending"`
	Sent     int `json:"sent"`
	Failed   int `json:"failed"`
	Skipped  int `json:"skipped"`
	Canceled int `json:"canceled"`
	// Bounced and Complained are finished recipients whose message bounced or was reported as spam.
	Bounced    int `json:"bounced"`
	Complained int `json:"complained"`
}

// MailBroadcastResponse is the public shape of a broadcast.
type MailBroadcastResponse struct {
	ID               uint                        `json:"id"`
	Name             string                      `json:"name"`
	MailboxID        uint                        `json:"mailbox_id"`
	TemplateID       uint                        `json:"template_id"`
	ListID           uint                        `json:"list_id"`
	UnsubscribeLabel string                      `json:"unsubscribe_label"`
	Variables        map[string]string           `json:"variables"`
	Status           string                      `json:"status"`
	ScheduledAt      *time.Time                  `json:"scheduled_at"`
	StartedAt        *time.Time                  `json:"started_at"`
	FinishedAt       *time.Time                  `json:"finished_at"`
	StopReason       string                      `json:"stop_reason"`
	TestPercent      int                         `json:"test_percent"`
	TestWaitMinutes  int                         `json:"test_wait_minutes"`
	CurrentWave      int                         `json:"current_wave"`
	ResumeAt         *time.Time                  `json:"resume_at"`
	SafetyOverride   bool                        `json:"safety_override"`
	Counts           MailBroadcastCountsResponse `json:"counts"`
	CreatedAt        time.Time                   `json:"created_at"`
	UpdatedAt        time.Time                   `json:"updated_at"`
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

func toMailBroadcastResponse(broadcast *domain.MailBroadcast) MailBroadcastResponse {
	return MailBroadcastResponse{
		ID:               broadcast.ID,
		Name:             broadcast.Name,
		MailboxID:        broadcast.MailboxID,
		TemplateID:       broadcast.TemplateID,
		ListID:           broadcast.ListID,
		UnsubscribeLabel: broadcast.UnsubscribeLabel,
		Variables:        broadcastVariables(broadcast.Variables),
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

func (h *Handler) ListBroadcasts(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	page := pagination.FromContext(c)
	broadcasts, total, err := h.service.ListBroadcasts(c.Request.Context(), viewer, page.GetPage(), page.GetPerPage())
	if err != nil {
		response.HandleError(c, "Failed to list mail broadcasts", err)
		return
	}
	items := make([]MailBroadcastResponse, 0, len(broadcasts))
	for _, broadcast := range broadcasts {
		items = append(items, toMailBroadcastResponse(broadcast))
	}
	paginator := pagination.NewPaginator(items, total, page.GetPage(), page.GetPerPage())
	paginator.SetPath(c.Request.URL.Path)
	privateResponse(c)
	response.Success(c, paginator)
}

func (h *Handler) GetBroadcast(c *gin.Context) {
	viewer, id, ok := viewerScope(c, "broadcast_id")
	if !ok {
		return
	}
	broadcast, err := h.service.GetBroadcast(c.Request.Context(), viewer, id)
	respondBroadcast(c, broadcast, err, false)
}

func (h *Handler) CreateBroadcast(c *gin.Context) {
	organization, ok := organizationScope(c)
	if !ok {
		return
	}
	var req MailBroadcastRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	broadcast, err := h.service.CreateBroadcast(c.Request.Context(), organization, req.input())
	respondBroadcast(c, broadcast, err, true)
}

func (h *Handler) UpdateBroadcast(c *gin.Context) {
	viewer, id, ok := viewerScope(c, "broadcast_id")
	if !ok {
		return
	}
	var req MailBroadcastRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	broadcast, err := h.service.UpdateBroadcast(c.Request.Context(), viewer, id, req.input())
	respondBroadcast(c, broadcast, err, false)
}

func (h *Handler) DeleteBroadcast(c *gin.Context) {
	viewer, id, ok := viewerScope(c, "broadcast_id")
	if !ok {
		return
	}
	if err := h.service.DeleteBroadcast(c.Request.Context(), viewer, id); err != nil {
		response.HandleError(c, "Failed to delete mail broadcast", err)
		return
	}
	privateResponse(c)
	response.NoContent(c)
}

// managedBroadcast resolves a broadcast route that only owners and admins may use: starting,
// stopping, pausing, or resuming mail to a whole list.
func managedBroadcast(c *gin.Context) (viewer domain.OrganizationContext, id uint, ok bool) {
	viewer, ok = managerScope(c)
	if !ok {
		return domain.OrganizationContext{}, 0, false
	}
	id, ok = httphandler.ParseID(c, "broadcast_id")
	return viewer, id, ok
}

// SendBroadcast starts or schedules a draft. Only owners and admins may.
func (h *Handler) SendBroadcast(c *gin.Context) {
	viewer, id, ok := managedBroadcast(c)
	if !ok {
		return
	}
	var req MailBroadcastSendRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	broadcast, err := h.service.SendBroadcast(c.Request.Context(), viewer, id, req.ScheduledAt)
	if err != nil && respondMissingVariablesAt(c, err, "variables.") {
		return
	}
	respondBroadcast(c, broadcast, err, false)
}

// CancelBroadcast stops a broadcast for good. Only owners and admins may; they need no access
// to the sender mailbox, and without View they get only the id and the new status.
func (h *Handler) CancelBroadcast(c *gin.Context) {
	viewer, id, ok := managedBroadcast(c)
	if !ok {
		return
	}
	broadcast, visible, err := h.service.CancelBroadcast(c.Request.Context(), viewer, id)
	respondStoppedBroadcast(c, broadcast, visible, err)
}

// PauseBroadcast holds a sending broadcast. Only owners and admins may; like cancel, it needs no
// access to the sender mailbox.
func (h *Handler) PauseBroadcast(c *gin.Context) {
	viewer, id, ok := managedBroadcast(c)
	if !ok {
		return
	}
	broadcast, visible, err := h.service.PauseBroadcast(c.Request.Context(), viewer, id)
	respondStoppedBroadcast(c, broadcast, visible, err)
}

// respondStoppedBroadcast answers a pause or cancellation. A caller who may not view the sender
// mailbox learns nothing about the broadcast, so the answer has no body: 204.
func respondStoppedBroadcast(c *gin.Context, broadcast *domain.MailBroadcast, visible bool, err error) {
	if err != nil || visible {
		respondBroadcast(c, broadcast, err, false)
		return
	}
	privateResponse(c)
	response.NoContent(c)
}

// ResumeBroadcast continues a paused broadcast. Only owners and admins may.
func (h *Handler) ResumeBroadcast(c *gin.Context) {
	viewer, id, ok := managedBroadcast(c)
	if !ok {
		return
	}
	broadcast, err := h.service.ResumeBroadcast(c.Request.Context(), viewer, id)
	respondBroadcast(c, broadcast, err, false)
}

func (h *Handler) ListBroadcastRecipients(c *gin.Context) {
	viewer, id, ok := viewerScope(c, "broadcast_id")
	if !ok {
		return
	}
	page := pagination.FromContext(c)
	recipients, total, err := h.service.ListBroadcastRecipients(c.Request.Context(), viewer, id,
		domain.MailRecipientStatus(c.Query("status")), page.GetPage(), page.GetPerPage())
	if err != nil {
		response.HandleError(c, "Failed to list mail broadcast recipients", err)
		return
	}
	items := make([]MailBroadcastRecipientResponse, 0, len(recipients))
	for _, recipient := range recipients {
		item := MailBroadcastRecipientResponse{
			ID:          recipient.ID,
			ContactID:   recipient.ContactID,
			Email:       recipient.Email,
			Status:      string(recipient.Status),
			FailureCode: recipient.FailureCode,
			LastEvent:   string(recipient.LastEvent),
			UpdatedAt:   recipient.UpdatedAt,
		}
		if recipient.ContactStatus != "" {
			contactStatus := string(recipient.ContactStatus)
			item.ContactStatus = &contactStatus
		}
		if recipient.MessageID != 0 {
			messageID := recipient.MessageID
			item.MessageID = &messageID
		}
		items = append(items, item)
	}
	paginator := pagination.NewPaginator(items, total, page.GetPage(), page.GetPerPage())
	paginator.SetPath(c.Request.URL.Path)
	privateResponse(c)
	response.Success(c, paginator)
}

// Unsubscribe applies a signed unsubscribe link. It needs no session: the token names one
// contact. Mail clients call it for one-click unsubscribe with any body, which is ignored.
func (h *Handler) Unsubscribe(c *gin.Context) {
	if err := h.service.Unsubscribe(c.Request.Context(), c.Param("token")); err != nil {
		response.HandleError(c, "Failed to unsubscribe", err)
		return
	}
	privateResponse(c)
	response.NoContent(c)
}

func respondBroadcast(c *gin.Context, broadcast *domain.MailBroadcast, err error, created bool) {
	if err != nil {
		response.HandleError(c, "Failed to handle mail broadcast", err)
		return
	}
	privateResponse(c)
	if created {
		response.Created(c, toMailBroadcastResponse(broadcast))
		return
	}
	response.Success(c, toMailBroadcastResponse(broadcast))
}

// broadcastVariables returns an object, never null, for a broadcast without shared values.
func broadcastVariables(variables map[string]string) map[string]string {
	if variables == nil {
		return map[string]string{}
	}
	return variables
}
