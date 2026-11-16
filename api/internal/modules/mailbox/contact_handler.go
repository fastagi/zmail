package mailbox

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/pagination"
	"github.com/fastagi/zmail/api/pkg/response"
)

// MailContactRequest creates or replaces a contact. Email is used only when creating.
type MailContactRequest struct {
	Email      string            `json:"email" binding:"max=320"`
	FirstName  string            `json:"first_name" binding:"max=400"`
	LastName   string            `json:"last_name" binding:"max=400"`
	Properties map[string]string `json:"properties" binding:"omitempty,max=50"`
	Status     string            `json:"status" binding:"omitempty,oneof=subscribed unsubscribed suppressed"`
	ListIDs    []uint            `json:"list_ids" binding:"omitempty,max=100"`
}

func (r MailContactRequest) input() ContactInput {
	return ContactInput{
		Email:      r.Email,
		FirstName:  r.FirstName,
		LastName:   r.LastName,
		Properties: r.Properties,
		Status:     domain.MailContactStatus(r.Status),
		ListIDs:    r.ListIDs,
	}
}

// MailContactImportRequest imports CSV text with a header row.
type MailContactImportRequest struct {
	CSV    string `json:"csv" binding:"required"`
	ListID uint   `json:"list_id"`
}

// MailContactListRequest creates or renames a list.
type MailContactListRequest struct {
	Name string `json:"name" binding:"required,max=400"`
}

// MailContactResponse is the public shape of a contact.
type MailContactResponse struct {
	ID           uint              `json:"id"`
	Email        string            `json:"email"`
	FirstName    string            `json:"first_name"`
	LastName     string            `json:"last_name"`
	Properties   map[string]string `json:"properties"`
	Status       string            `json:"status"`
	StatusReason string            `json:"status_reason"`
	ListIDs      []uint            `json:"list_ids"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

// MailContactListResponse is the public shape of a list.
type MailContactListResponse struct {
	ID           uint      `json:"id"`
	Name         string    `json:"name"`
	ContactCount int       `json:"contact_count"`
	CreatedAt    time.Time `json:"created_at"`
}

// MailContactImportSkipResponse is one row an import did not store.
type MailContactImportSkipResponse struct {
	Line   int    `json:"line"`
	Reason string `json:"reason"`
}

// MailContactImportResponse says what an import did.
type MailContactImportResponse struct {
	Rows           int                             `json:"rows"`
	Created        int                             `json:"created"`
	Updated        int                             `json:"updated"`
	SkippedCount   int                             `json:"skipped_count"`
	Skipped        []MailContactImportSkipResponse `json:"skipped"`
	IgnoredColumns []string                        `json:"ignored_columns"`
}

func toMailContactResponse(contact *domain.MailContact) MailContactResponse {
	properties := contact.Properties
	if properties == nil {
		properties = map[string]string{}
	}
	listIDs := contact.ListIDs
	if listIDs == nil {
		listIDs = []uint{}
	}
	return MailContactResponse{
		ID:           contact.ID,
		Email:        contact.Email,
		FirstName:    contact.FirstName,
		LastName:     contact.LastName,
		Properties:   properties,
		Status:       string(contact.Status),
		StatusReason: contact.StatusReason,
		ListIDs:      listIDs,
		CreatedAt:    contact.CreatedAt,
		UpdatedAt:    contact.UpdatedAt,
	}
}

func toMailContactListResponse(list *domain.MailContactList) MailContactListResponse {
	return MailContactListResponse{
		ID: list.ID, Name: list.Name, ContactCount: list.ContactCount, CreatedAt: list.CreatedAt,
	}
}

func (h *Handler) ListContacts(c *gin.Context) {
	organizationID, ok := currentOrganizationID(c)
	if !ok {
		return
	}
	var listID uint
	if raw := c.Query("list_id"); raw != "" {
		parsed, valid := parseQueryID(raw)
		if !valid {
			response.HandleError(c, "Invalid list filter", domain.ErrInvalidInput)
			return
		}
		listID = parsed
	}
	page := pagination.FromContext(c)
	contacts, total, err := h.service.ListContacts(c.Request.Context(), organizationID, domain.MailContactFilter{
		Query:   c.Query("q"),
		ListID:  listID,
		Status:  domain.MailContactStatus(c.Query("status")),
		Page:    page.GetPage(),
		PerPage: page.GetPerPage(),
	})
	if err != nil {
		response.HandleError(c, "Failed to list mail contacts", err)
		return
	}
	items := make([]MailContactResponse, 0, len(contacts))
	for _, contact := range contacts {
		items = append(items, toMailContactResponse(contact))
	}
	paginator := pagination.NewPaginator(items, total, page.GetPage(), page.GetPerPage())
	paginator.SetPath(c.Request.URL.Path)
	privateResponse(c)
	response.Success(c, paginator)
}

func (h *Handler) GetContact(c *gin.Context) {
	organizationID, contactID, ok := idScope(c, "contact_id")
	if !ok {
		return
	}
	contact, err := h.service.GetContact(c.Request.Context(), organizationID, contactID)
	if err != nil {
		response.HandleError(c, "Failed to get mail contact", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailContactResponse(contact))
}

func (h *Handler) CreateContact(c *gin.Context) {
	organizationID, ok := currentOrganizationID(c)
	if !ok {
		return
	}
	var req MailContactRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	contact, err := h.service.CreateContact(c.Request.Context(), organizationID, req.input())
	if err != nil {
		response.HandleError(c, "Failed to create mail contact", err)
		return
	}
	privateResponse(c)
	response.Created(c, toMailContactResponse(contact))
}

func (h *Handler) UpdateContact(c *gin.Context) {
	organizationID, contactID, ok := idScope(c, "contact_id")
	if !ok {
		return
	}
	var req MailContactRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	contact, err := h.service.UpdateContact(c.Request.Context(), organizationID, contactID, req.input())
	if err != nil {
		response.HandleError(c, "Failed to update mail contact", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailContactResponse(contact))
}

func (h *Handler) DeleteContact(c *gin.Context) {
	organizationID, contactID, ok := idScope(c, "contact_id")
	if !ok {
		return
	}
	if err := h.service.DeleteContact(c.Request.Context(), organizationID, contactID); err != nil {
		response.HandleError(c, "Failed to delete mail contact", err)
		return
	}
	privateResponse(c)
	response.NoContent(c)
}

func (h *Handler) ImportContacts(c *gin.Context) {
	organizationID, ok := currentOrganizationID(c)
	if !ok {
		return
	}
	var req MailContactImportRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	result, err := h.service.ImportContacts(c.Request.Context(), organizationID, req.CSV, req.ListID)
	if err != nil {
		response.HandleError(c, "Failed to import mail contacts", err)
		return
	}
	skipped := make([]MailContactImportSkipResponse, 0, len(result.Skipped))
	for _, skip := range result.Skipped {
		skipped = append(skipped, MailContactImportSkipResponse(skip))
	}
	privateResponse(c)
	response.Success(c, MailContactImportResponse{
		Rows:           result.Rows,
		Created:        result.Created,
		Updated:        result.Updated,
		SkippedCount:   result.SkippedCount,
		Skipped:        skipped,
		IgnoredColumns: result.IgnoredColumns,
	})
}

// ListContactLists returns every list of the organization with its contact count.
// fastbuild:bounded-list max=100 reason=an organization keeps at most MaxMailContactListsPerOrganization lists
func (h *Handler) ListContactLists(c *gin.Context) {
	organizationID, ok := currentOrganizationID(c)
	if !ok {
		return
	}
	lists, err := h.service.ListContactLists(c.Request.Context(), organizationID)
	if err != nil {
		response.HandleError(c, "Failed to list mail contact lists", err)
		return
	}
	items := make([]MailContactListResponse, 0, len(lists))
	for _, list := range lists {
		items = append(items, toMailContactListResponse(list))
	}
	privateResponse(c)
	response.Success(c, items)
}

func (h *Handler) CreateContactList(c *gin.Context) {
	organizationID, ok := currentOrganizationID(c)
	if !ok {
		return
	}
	var req MailContactListRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	list, err := h.service.CreateContactList(c.Request.Context(), organizationID, req.Name)
	if err != nil {
		response.HandleError(c, "Failed to create mail contact list", err)
		return
	}
	privateResponse(c)
	response.Created(c, toMailContactListResponse(list))
}

func (h *Handler) RenameContactList(c *gin.Context) {
	organizationID, listID, ok := idScope(c, "list_id")
	if !ok {
		return
	}
	var req MailContactListRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	list, err := h.service.RenameContactList(c.Request.Context(), organizationID, listID, req.Name)
	if err != nil {
		response.HandleError(c, "Failed to rename mail contact list", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailContactListResponse(list))
}

func (h *Handler) DeleteContactList(c *gin.Context) {
	organizationID, listID, ok := idScope(c, "list_id")
	if !ok {
		return
	}
	if err := h.service.DeleteContactList(c.Request.Context(), organizationID, listID); err != nil {
		response.HandleError(c, "Failed to delete mail contact list", err)
		return
	}
	privateResponse(c)
	response.NoContent(c)
}

func idScope(c *gin.Context, param string) (organizationID, id uint, ok bool) {
	organizationID, ok = currentOrganizationID(c)
	if !ok {
		return 0, 0, false
	}
	id, ok = httphandler.ParseID(c, param)
	return organizationID, id, ok
}

func parseQueryID(raw string) (uint, bool) {
	parsed, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || parsed == 0 {
		return 0, false
	}
	return uint(parsed), true
}
