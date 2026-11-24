package roster

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/starter/assembly"
	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/pagination"
	"github.com/fastagi/zmail/api/pkg/response"
)

// Handler serves the HTTP endpoints for the roster module (contacts, contact lists, CSV imports).
type Handler struct {
	service Service
}

var (
	_ assembly.Module      = (*Handler)(nil)
	_ assembly.RouteModule = (*Handler)(nil)
	_ assembly.ErrorModule = (*Handler)(nil)
)

// NewHandler builds a roster HTTP handler backed by the narrow roster.Service interface.
func NewHandler(service Service) *Handler {
	return &Handler{service: service}
}

// Name identifies this module inside the starter registry.
func (h *Handler) Name() string {
	return "roster"
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

func currentOrganizationID(c *gin.Context) (uint, bool) {
	organization, ok := domain.OrganizationContextFromContext(c.Request.Context())
	if !ok {
		response.AbortWithCode(c, http.StatusBadRequest, domain.CodeOrganizationContextRequired,
			"Organization context required")
		return 0, false
	}
	return organization.OrganizationID, true
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

func privateResponse(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	c.Header("Vary", "Authorization, Organization-Id")
}

func requireFeature(features ...domain.MemberFeature) gin.HandlerFunc {
	if len(features) == 0 {
		panic("roster: requireFeature needs at least one feature")
	}
	return func(c *gin.Context) {
		viewer, ok := domain.OrganizationContextFromContext(c.Request.Context())
		if !ok {
			response.AbortWithCode(c, http.StatusBadRequest, domain.CodeOrganizationContextRequired,
				"Organization context required")
			return
		}
		for _, feature := range features {
			if viewer.CanUse(feature) {
				c.Next()
				return
			}
		}
		response.AbortWithCode(c, http.StatusForbidden, domain.CodeOrganizationFeatureNotGranted,
			"An organization owner or admin has not granted you this feature")
	}
}
