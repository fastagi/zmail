package mailbox

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/pkg/response"
)

// MailRoleRequest is the whole of a mail role a create or replace sets. Unknown fields are
// ignored; the service trims and checks every field.
type MailRoleRequest struct {
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
	// OwnerUserID is the member responsible for the role, for display only; null for none.
	OwnerUserID *uint `json:"owner_user_id"`
}

func (r MailRoleRequest) input() MailRoleInput {
	return MailRoleInput{
		Name: r.Name, Color: domain.MailLabelColor(r.Color), Description: r.Description, OwnerUserID: r.OwnerUserID,
	}
}

// MailRoleResponse is one mail role with the mailboxes in it the caller can see, ascending.
type MailRoleResponse struct {
	ID          uint      `json:"id"`
	Name        string    `json:"name"`
	Color       string    `json:"color"`
	Description string    `json:"description"`
	OwnerUserID *uint     `json:"owner_user_id"`
	MailboxIDs  []uint    `json:"mailbox_ids"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func toMailRoleResponse(role *domain.MailRole) MailRoleResponse {
	mailboxIDs := role.MailboxIDs
	if mailboxIDs == nil {
		mailboxIDs = []uint{}
	}
	return MailRoleResponse{
		ID: role.ID, Name: role.Name, Color: string(role.Color), Description: role.Description,
		OwnerUserID: role.OwnerUserID, MailboxIDs: mailboxIDs, CreatedAt: role.CreatedAt, UpdatedAt: role.UpdatedAt,
	}
}

func toMailRoleResponses(roles []*domain.MailRole) []MailRoleResponse {
	responses := make([]MailRoleResponse, 0, len(roles))
	for _, role := range roles {
		responses = append(responses, toMailRoleResponse(role))
	}
	return responses
}

const (
	// maxMailRoleBodyBytes caps a mail role or role assignment body.
	maxMailRoleBodyBytes = 4 << 10
	// maxPublicID is the largest id the contract's PositiveID allows.
	maxPublicID = 1<<53 - 1
)

// ListMailRoles lists the organization's mail roles the caller may see.
// fastbuild:bounded-list max=50 reason=MaxMailRolesPerOrganization
func (h *Handler) ListMailRoles(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	roles, err := h.service.ListMailRoles(c.Request.Context(), viewer)
	if err != nil {
		response.HandleError(c, "Failed to list mail roles", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailRoleResponses(roles))
}

// CreateMailRole adds a mail role. Only owners and admins may.
func (h *Handler) CreateMailRole(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	var req MailRoleRequest
	if !bindMailRoleBody(c, &req) {
		return
	}
	role, err := h.service.CreateMailRole(c.Request.Context(), viewer, req.input())
	if err != nil {
		respondOrganizeError(c, "Failed to create mail role", err)
		return
	}
	privateResponse(c)
	response.Created(c, toMailRoleResponse(role))
}

// UpdateMailRole replaces every field of a mail role. Only owners and admins may.
func (h *Handler) UpdateMailRole(c *gin.Context) {
	viewer, roleID, ok := viewerScope(c, "role_id")
	if !ok {
		return
	}
	var req MailRoleRequest
	if !bindMailRoleBody(c, &req) {
		return
	}
	role, err := h.service.UpdateMailRole(c.Request.Context(), viewer, roleID, req.input())
	if err != nil {
		respondOrganizeError(c, "Failed to update mail role", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailRoleResponse(role))
}

// DeleteMailRole deletes a mail role and ungroups its mailboxes. Only owners and admins may.
func (h *Handler) DeleteMailRole(c *gin.Context) {
	viewer, roleID, ok := viewerScope(c, "role_id")
	if !ok {
		return
	}
	if err := h.service.DeleteMailRole(c.Request.Context(), viewer, roleID); err != nil {
		response.HandleError(c, "Failed to delete mail role", err)
		return
	}
	response.NoContent(c)
}

// AssignMailboxRole puts a mailbox in a mail role or takes it out of its role. A role_id that is
// missing or not a positive integer or null reaches the service as zero, which answers the field
// error after the mailbox access checks.
func (h *Handler) AssignMailboxRole(c *gin.Context) {
	viewer, mailboxID, ok := mailboxScope(c)
	if !ok {
		return
	}
	var fields map[string]json.RawMessage
	if !bindMailRoleBody(c, &fields) {
		return
	}
	mailbox, err := h.service.AssignMailboxRole(c.Request.Context(), viewer, mailboxID, assignedRoleID(fields["role_id"]))
	if err != nil {
		respondOrganizeError(c, "Failed to set the mailbox's mail role", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailboxResponse(mailbox))
}

// assignedRoleID reads role_id: nil for null, the id for a positive integer, and zero for
// anything else, including a missing role_id.
func assignedRoleID(raw json.RawMessage) *uint {
	value := string(bytes.TrimSpace(raw))
	if value == "null" {
		return nil
	}
	id, err := strconv.ParseUint(value, 10, 64)
	if err != nil || id > maxPublicID {
		id = 0
	}
	return new(uint(id))
}

// bindMailRoleBody decodes a body of at most maxMailRoleBodyBytes that is exactly one JSON
// object into target. Anything else answers 400 COMMON.INVALID_INPUT.
func bindMailRoleBody(c *gin.Context, target any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxMailRoleBodyBytes)
	body, err := io.ReadAll(c.Request.Body)
	trimmed := bytes.TrimSpace(body)
	if err != nil || len(trimmed) == 0 || trimmed[0] != '{' || json.Unmarshal(trimmed, target) != nil {
		// Neither the body nor the decoder's message reaches the response.
		response.HandleError(c, "Invalid request body",
			fmt.Errorf("%w: the body must be one JSON object of at most %d bytes", domain.ErrInvalidInput, maxMailRoleBodyBytes))
		return false
	}
	return true
}
