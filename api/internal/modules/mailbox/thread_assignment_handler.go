package mailbox

import (
	"bytes"
	"encoding/json"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/response"
)

// UpdateMailThreadAssignmentRequest replaces a thread's assignment; a null assignee unassigns it.
type UpdateMailThreadAssignmentRequest struct {
	AssigneeUserID requiredNullableID `json:"assignee_user_id"`
	Status         string             `json:"status" binding:"required"`
}

// requiredNullableID tells an explicit null from an omitted field, so a partial body cannot unassign.
type requiredNullableID struct {
	Set   bool
	Value *uint
}

func (id *requiredNullableID) UnmarshalJSON(raw []byte) error {
	id.Set = true
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		id.Value = nil
		return nil
	}
	var value uint
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	id.Value = &value
	return nil
}

// MailThreadAssignmentResponse is who works a thread, and who it may go to.
type MailThreadAssignmentResponse struct {
	ThreadID          uint       `json:"thread_id"`
	AssigneeUserID    *uint      `json:"assignee_user_id"`
	Status            string     `json:"status"`
	UpdatedByUserID   *uint      `json:"updated_by_user_id"`
	UpdatedAt         *time.Time `json:"updated_at"`
	AssignableUserIDs []uint     `json:"assignable_user_ids"`
}

func toMailThreadAssignmentResponse(view MailThreadAssignmentView) MailThreadAssignmentResponse {
	return MailThreadAssignmentResponse{
		ThreadID: view.ThreadID, AssigneeUserID: view.AssigneeUserID, Status: string(view.Status),
		UpdatedByUserID: view.UpdatedBy, UpdatedAt: view.UpdatedAt, AssignableUserIDs: view.AssignableUserIDs,
	}
}

// threadScope reads the mailbox and thread ids of a thread route.
func threadScope(c *gin.Context) (domain.OrganizationContext, uint, uint, bool) {
	viewer, mailboxID, ok := mailboxScope(c)
	if !ok {
		return viewer, 0, 0, false
	}
	threadID, err := strconv.ParseUint(c.Param("thread_id"), 10, 64)
	if err != nil || threadID == 0 {
		response.BadRequest(c, "Invalid thread id")
		return viewer, 0, 0, false
	}
	return viewer, mailboxID, uint(threadID), true
}

// GetMailThreadAssignment reads who works a thread of a shared mailbox.
func (h *Handler) GetMailThreadAssignment(c *gin.Context) {
	viewer, mailboxID, threadID, ok := threadScope(c)
	if !ok {
		return
	}
	view, err := h.service.MailThreadAssignment(c.Request.Context(), viewer, mailboxID, threadID)
	if err != nil {
		response.HandleError(c, "Failed to read the assignment", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailThreadAssignmentResponse(view))
}

// UpdateMailThreadAssignment assigns a thread of a shared mailbox and sets its status.
func (h *Handler) UpdateMailThreadAssignment(c *gin.Context) {
	viewer, mailboxID, threadID, ok := threadScope(c)
	if !ok {
		return
	}
	var req UpdateMailThreadAssignmentRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	if !req.AssigneeUserID.Set {
		response.ValidationFailed(c, map[string][]string{"assignee_user_id": {"is required; send null to unassign"}})
		return
	}
	view, err := h.service.SaveMailThreadAssignment(c.Request.Context(), viewer, mailboxID, threadID, MailThreadAssignmentInput{
		AssigneeUserID: req.AssigneeUserID.Value, Status: domain.MailThreadStatus(req.Status),
	})
	if err != nil {
		respondOrganizeError(c, "Failed to save the assignment", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailThreadAssignmentResponse(view))
}
