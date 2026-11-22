package mailbox

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/response"
)

const (
	// maxExternalMailTenantBodyBytes bounds an authorization: a service account key is about 2.4 KiB.
	maxExternalMailTenantBodyBytes = 32 << 10
	// maxExternalMailAssignmentsBodyBytes bounds a batch of 100 rows with up to 100 members each.
	maxExternalMailAssignmentsBodyBytes = 1 << 20
	// maxTenantDirectoryCursorLength bounds the opaque directory cursor, a provider page token or link.
	maxTenantDirectoryCursorLength = 4096
)

// authorizeExternalMailTenantRequest authorizes a tenant with a credential the organization owns.
// Google takes service_account_key (the JSON key file, as an object or as a string) and
// admin_subject; Microsoft takes tenant_id, client_id, and client_secret of the organization's own
// application.
type authorizeExternalMailTenantRequest struct {
	Provider          string          `json:"provider" binding:"required,max=16"`
	ServiceAccountKey json.RawMessage `json:"service_account_key"`
	AdminSubject      string          `json:"admin_subject" binding:"max=320"`
	TenantID          string          `json:"tenant_id" binding:"max=255"`
	ClientID          string          `json:"client_id" binding:"max=128"`
	ClientSecret      string          `json:"client_secret" binding:"max=1024"`
}

// serviceAccountKey reads the key as the file's JSON: an object as it is, or a string holding it.
func (r authorizeExternalMailTenantRequest) serviceAccountKey() (string, error) {
	raw := bytes.TrimSpace(r.ServiceAccountKey)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", nil
	}
	if raw[0] == '"' {
		var key string
		if err := json.Unmarshal(raw, &key); err != nil {
			return "", ErrExternalMailAuthorizationInvalid
		}
		return key, nil
	}
	if raw[0] != '{' {
		return "", ErrExternalMailAuthorizationInvalid
	}
	return string(raw), nil
}

// ExternalMailTenantResponse is one authorized tenant. It never carries the credential.
type ExternalMailTenantResponse struct {
	ID                 uint       `json:"id"`
	Provider           string     `json:"provider"`
	TenantIdentifier   string     `json:"tenant_identifier"`
	DisplayName        string     `json:"display_name"`
	Status             string     `json:"status"`
	LastErrorCode      *string    `json:"last_error_code"`
	Assignments        int        `json:"assignments"`
	AuthorizedByUserID *uint      `json:"authorized_by_user_id"`
	AuthorizedAt       time.Time  `json:"authorized_at"`
	RevokedAt          *time.Time `json:"revoked_at"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

func toExternalMailTenantResponse(view ExternalMailTenantView) ExternalMailTenantResponse {
	tenant := view.Tenant
	item := ExternalMailTenantResponse{
		ID: tenant.ID, Provider: string(tenant.Provider), TenantIdentifier: tenant.TenantIdentifier, DisplayName: tenant.DisplayName,
		Status: string(tenant.Status), LastErrorCode: optionalText(tenant.LastErrorCode), Assignments: view.Assignments,
		AuthorizedByUserID: tenant.AuthorizedByUserID, AuthorizedAt: tenant.AuthorizedAt.UTC(),
		CreatedAt: tenant.CreatedAt.UTC(), UpdatedAt: tenant.UpdatedAt.UTC(),
	}
	if tenant.RevokedAt != nil {
		item.RevokedAt = new(tenant.RevokedAt.UTC())
	}
	return item
}

// writeTenantAuthorization answers an authorization: 201 with a new tenant, or 200 with a
// re-authorized one.
func writeTenantAuthorization(c *gin.Context, result ExternalMailTenantAuthorizationResult) {
	privateResponse(c)
	if result.Created {
		response.Created(c, toExternalMailTenantResponse(*result.Tenant))
		return
	}
	response.Success(c, toExternalMailTenantResponse(*result.Tenant))
}

// AuthorizeExternalMailTenant authorizes a whole tenant for the organization (owners only).
func (h *Handler) AuthorizeExternalMailTenant(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	var req authorizeExternalMailTenantRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	key, err := req.serviceAccountKey()
	if err != nil {
		response.HandleError(c, "Failed to authorize external mail tenant", err)
		return
	}
	result, err := h.service.AuthorizeExternalMailTenant(c.Request.Context(), viewer, ExternalMailTenantInput{
		Provider: domain.ExternalMailProvider(req.Provider),
		Grant: ExternalMailTenantGrant{
			ServiceAccountKey: key, AdminSubject: req.AdminSubject, TenantID: req.TenantID,
			ClientID: req.ClientID, ClientSecret: req.ClientSecret,
		},
	})
	if err != nil {
		response.HandleError(c, "Failed to authorize external mail tenant", err)
		return
	}
	writeTenantAuthorization(c, result)
}

// ListExternalMailTenants lists the organization's tenants for owners and admins.
// fastbuild:bounded-list max=50 reason=one organization lists at most MaxExternalMailTenantsPerOrganization tenants
func (h *Handler) ListExternalMailTenants(c *gin.Context) {
	viewer, ok := managerScope(c)
	if !ok {
		return
	}
	views, err := h.service.ListExternalMailTenants(c.Request.Context(), viewer)
	if err != nil {
		response.HandleError(c, "Failed to list external mail tenants", err)
		return
	}
	items := make([]ExternalMailTenantResponse, 0, len(views))
	for _, view := range views {
		items = append(items, toExternalMailTenantResponse(view))
	}
	privateResponse(c)
	response.Success(c, items)
}

// RevokeExternalMailTenant revokes a tenant and every connection of it (owners only).
func (h *Handler) RevokeExternalMailTenant(c *gin.Context) {
	viewer, tenantID, ok := viewerScope(c, "tenant_id")
	if !ok {
		return
	}
	if err := h.service.RevokeExternalMailTenant(c.Request.Context(), viewer, tenantID); err != nil {
		response.HandleError(c, "Failed to revoke external mail tenant", err)
		return
	}
	response.NoContent(c)
}

// externalMailTenantDirectoryRequest is the query of GET /v1/external-mail-tenants/{id}/directory.
type externalMailTenantDirectoryRequest struct {
	Cursor  string `form:"cursor" binding:"max=4096"`
	PerPage int    `form:"per_page" binding:"omitempty,min=1,max=100"`
}

// ExternalMailTenantDirectoryEntryResponse is one directory mailbox and its assignment.
type ExternalMailTenantDirectoryEntryResponse struct {
	Address     string `json:"address"`
	DisplayName string `json:"display_name"`
	DirectoryID string `json:"directory_id"`
	// Assigned marks a live connection of this tenant.
	Assigned     bool    `json:"assigned"`
	Assignment   *string `json:"assignment"`
	ConnectionID *uint   `json:"connection_id"`
	MailboxID    *uint   `json:"mailbox_id"`
	// Available is false while any live connection of the organization holds the address.
	Available bool `json:"available"`
}

// ListExternalMailTenantDirectory returns one page of a tenant's directory for owners and admins.
func (h *Handler) ListExternalMailTenantDirectory(c *gin.Context) {
	viewer, tenantID, ok := viewerScope(c, "tenant_id")
	if !ok {
		return
	}
	var req externalMailTenantDirectoryRequest
	if !bindValidatedQuery(c, &req) {
		return
	}
	perPage := req.PerPage
	if perPage == 0 {
		perPage = DefaultTenantDirectoryPageSize
	}
	cursor := ""
	if req.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(req.Cursor)
		if err != nil || len(req.Cursor) > maxTenantDirectoryCursorLength {
			response.HandleError(c, "Invalid tenant directory cursor", fmt.Errorf("%w: malformed cursor", domain.ErrInvalidInput))
			return
		}
		cursor = string(raw)
	}
	page, err := h.service.ExternalMailTenantDirectory(c.Request.Context(), viewer, tenantID, cursor, perPage)
	if err != nil {
		response.HandleError(c, "Failed to read the tenant directory", err)
		return
	}
	items := make([]ExternalMailTenantDirectoryEntryResponse, 0, len(page.Entries))
	for _, entry := range page.Entries {
		item := ExternalMailTenantDirectoryEntryResponse{
			Address: entry.Entry.Address, DisplayName: entry.Entry.DisplayName, DirectoryID: entry.Entry.DirectoryID,
			Available: entry.Available,
		}
		if connection := entry.Connection; connection != nil {
			assignment := TenantAssignPersonal
			if connection.Shared {
				assignment = TenantAssignShared
			}
			item.Assigned, item.Assignment = true, &assignment
			item.ConnectionID, item.MailboxID = new(connection.ID), new(connection.MailboxID)
		}
		items = append(items, item)
	}
	next := ""
	if page.Next != "" {
		next = base64.RawURLEncoding.EncodeToString([]byte(page.Next))
		if len(next) > maxTenantDirectoryCursorLength {
			// A provider cursor too long to send back ends the listing rather than breaking it.
			next = ""
		}
	}
	privateResponse(c)
	response.SuccessCursorPage(c, items, perPage, next)
}

// assignExternalMailTenantMailboxesRequest is a batch of assignments.
type assignExternalMailTenantMailboxesRequest struct {
	Assignments []externalMailTenantAssignmentRequest `json:"assignments" binding:"required,min=1,max=100,dive"`
}

// externalMailTenantAssignmentRequest is one row: {address, assign: personal, user_id} or {address,
// assign: shared, display_name, members}. The service checks the combination per row.
type externalMailTenantAssignmentRequest struct {
	Address     string                 `json:"address" binding:"required,max=320"`
	Assign      string                 `json:"assign" binding:"required,oneof=personal shared"`
	UserID      uint                   `json:"user_id"`
	DisplayName string                 `json:"display_name" binding:"max=255"`
	Members     []MailboxMemberRequest `json:"members" binding:"omitempty,max=100,dive"`
}

// ExternalMailTenantAssignmentResponse is the outcome of one row.
type ExternalMailTenantAssignmentResponse struct {
	Address      string  `json:"address"`
	Status       string  `json:"status"`
	ConnectionID *uint   `json:"connection_id"`
	MailboxID    *uint   `json:"mailbox_id"`
	ErrorCode    *string `json:"error_code"`
}

// AssignExternalMailTenantMailboxes assigns tenant mailboxes row by row for owners and admins; the
// answer is 200 with one result per row, failed rows included.
func (h *Handler) AssignExternalMailTenantMailboxes(c *gin.Context) {
	viewer, tenantID, ok := viewerScope(c, "tenant_id")
	if !ok {
		return
	}
	var req assignExternalMailTenantMailboxesRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	rows := make([]ExternalMailTenantAssignmentInput, 0, len(req.Assignments))
	for _, row := range req.Assignments {
		input := ExternalMailTenantAssignmentInput{Address: row.Address, Assign: row.Assign, UserID: row.UserID}
		if row.Assign == TenantAssignShared || len(row.Members) > 0 {
			sharing := externalMailSharingRequest{DisplayName: row.DisplayName, Members: row.Members}
			input.Sharing = sharing.input()
		}
		rows = append(rows, input)
	}
	results, err := h.service.AssignExternalMailTenantMailboxes(c.Request.Context(), viewer, tenantID, rows)
	if err != nil {
		response.HandleError(c, "Failed to assign tenant mailboxes", err)
		return
	}
	items := make([]ExternalMailTenantAssignmentResponse, 0, len(results))
	for _, result := range results {
		item := ExternalMailTenantAssignmentResponse{Address: result.Address, Status: result.Status, ErrorCode: optionalText(result.ErrorCode)}
		if result.Connection != nil {
			item.ConnectionID, item.MailboxID = new(result.Connection.ID), new(result.Connection.MailboxID)
		}
		items = append(items, item)
	}
	privateResponse(c)
	response.Success(c, items)
}
