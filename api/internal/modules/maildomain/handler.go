package maildomain

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/starter/assembly"
	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/response"
)

// Handler serves the HTTP endpoints for the top-level maildomain module.
type Handler struct {
	service domain.MailDomainPort
}

var (
	_ assembly.Module      = (*Handler)(nil)
	_ assembly.RouteModule = (*Handler)(nil)
	_ assembly.ErrorModule = (*Handler)(nil)
)

// NewHandler builds a maildomain HTTP handler backed by domain.MailDomainPort.
func NewHandler(service domain.MailDomainPort) *Handler {
	return &Handler{service: service}
}

// Name identifies this module inside the starter registry.
func (h *Handler) Name() string {
	return "maildomain"
}

// ListMailDomains returns the organization's connected domains.
// fastbuild:bounded-list max=10 reason=an organization connects at most domain.MaxMailDomainsPerOrganization domains
func (h *Handler) ListMailDomains(c *gin.Context) {
	organizationID, ok := currentOrganizationID(c)
	if !ok {
		return
	}
	domains, err := h.service.ListMailDomainsPort(c.Request.Context(), organizationID)
	if err != nil {
		response.HandleError(c, "Failed to list mail domains", err)
		return
	}
	items := make([]MailDomainResponse, 0, len(domains))
	for _, mailDomain := range domains {
		items = append(items, toMailDomainResponse(mailDomain))
	}
	privateResponse(c)
	response.Success(c, items)
}

func (h *Handler) GetMailDomain(c *gin.Context) {
	organizationID, ok := currentOrganizationID(c)
	if !ok {
		return
	}
	id, ok := httphandler.ParseID(c, "domain_id")
	if !ok {
		return
	}
	mailDomain, err := h.service.GetMailDomainPort(c.Request.Context(), organizationID, id)
	if err != nil {
		response.HandleError(c, "Failed to get mail domain", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailDomainResponse(mailDomain))
}

func (h *Handler) ConnectMailDomain(c *gin.Context) {
	organization, ok := managerScope(c)
	if !ok {
		return
	}
	var req MailDomainRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	mailDomain, err := h.service.ConnectMailDomainPort(c.Request.Context(), organization.OrganizationID, organization.UserID,
		req.Name, domain.MailDomainCapability(req.Capability))
	if err != nil {
		response.HandleError(c, "Failed to connect mail domain", err)
		return
	}
	privateResponse(c)
	response.Created(c, toMailDomainResponse(mailDomain))
}

func (h *Handler) SetUpMailDomain(c *gin.Context) {
	h.runDomainAction(c, "Failed to set up mail domain", h.service.SetUpMailDomainPort)
}

func (h *Handler) CheckMailDomain(c *gin.Context) {
	h.runDomainAction(c, "Failed to check mail domain", h.service.CheckMailDomainPort)
}

func (h *Handler) EnableMailDomainReceiving(c *gin.Context) {
	h.runDomainAction(c, "Failed to enable receiving on mail domain", h.service.EnableMailDomainReceivingPort)
}

func (h *Handler) SetDefaultMailDomain(c *gin.Context) {
	h.runDomainAction(c, "Failed to set the default mail domain", h.service.SetDefaultMailDomainPort)
}

func (h *Handler) runDomainAction(
	c *gin.Context,
	failure string,
	action func(ctx context.Context, organizationID, id uint) (*domain.MailDomain, error),
) {
	organization, ok := managerScope(c)
	if !ok {
		return
	}
	id, ok := httphandler.ParseID(c, "domain_id")
	if !ok {
		return
	}
	mailDomain, err := action(c.Request.Context(), organization.OrganizationID, id)
	if err != nil {
		response.HandleError(c, failure, err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailDomainResponse(mailDomain))
}

func (h *Handler) DeleteMailDomain(c *gin.Context) {
	organization, ok := managerScope(c)
	if !ok {
		return
	}
	id, ok := httphandler.ParseID(c, "domain_id")
	if !ok {
		return
	}
	if err := h.service.DeleteMailDomainPort(c.Request.Context(), organization.OrganizationID, id); err != nil {
		response.HandleError(c, "Failed to delete mail domain", err)
		return
	}
	response.NoContent(c)
}

func (h *Handler) PreviewMailDomain(c *gin.Context) {
	if _, ok := currentOrganizationID(c); !ok {
		return
	}
	var req MailDomainPreviewRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	preview, err := h.service.PreviewMailDomainPort(c.Request.Context(), req.Name)
	if err != nil {
		response.HandleError(c, "Failed to preview mail domain", err)
		return
	}
	privateResponse(c)
	response.Success(c, MailDomainPreviewResponse{
		Name:                preview.Name,
		Apex:                preview.Apex,
		ExistingMX:          preview.ExistingMX,
		SuggestedCapability: string(preview.SuggestedCapability),
	})
}

// ListMailZones returns the organization's managed zones.
// fastbuild:bounded-list max=20 reason=an organization holds at most domain.MaxMailZonesPerOrganization managed zones
func (h *Handler) ListMailZones(c *gin.Context) {
	organizationID, ok := currentOrganizationID(c)
	if !ok {
		return
	}
	zones, err := h.service.ListMailZonesPort(c.Request.Context(), organizationID)
	if err != nil {
		response.HandleError(c, "Failed to list mail zones", err)
		return
	}
	items := make([]*MailZoneResponse, 0, len(zones))
	for _, zone := range zones {
		items = append(items, toMailZoneResponse(zone))
	}
	privateResponse(c)
	response.Success(c, items)
}

func (h *Handler) GetMailZone(c *gin.Context) {
	organizationID, ok := currentOrganizationID(c)
	if !ok {
		return
	}
	id, ok := httphandler.ParseID(c, "zone_id")
	if !ok {
		return
	}
	zone, err := h.service.GetMailZonePort(c.Request.Context(), organizationID, id)
	if err != nil {
		response.HandleError(c, "Failed to get mail zone", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailZoneResponse(zone))
}

func (h *Handler) DeleteMailZone(c *gin.Context) {
	organization, ok := managerScope(c)
	if !ok {
		return
	}
	id, ok := httphandler.ParseID(c, "zone_id")
	if !ok {
		return
	}
	if err := h.service.DeleteMailZonePort(c.Request.Context(), organization.OrganizationID, id); err != nil {
		response.HandleError(c, "Failed to delete mail zone", err)
		return
	}
	response.NoContent(c)
}

func organizationScope(c *gin.Context) (domain.OrganizationContext, bool) {
	viewer, ok := domain.OrganizationContextFromContext(c.Request.Context())
	if !ok {
		response.AbortWithCode(c, http.StatusBadRequest, domain.CodeOrganizationContextRequired,
			"Organization context required")
		return domain.OrganizationContext{}, false
	}
	return viewer, true
}

func currentOrganizationID(c *gin.Context) (uint, bool) {
	org, ok := organizationScope(c)
	return org.OrganizationID, ok
}

func managerScope(c *gin.Context) (domain.OrganizationContext, bool) {
	org, ok := organizationScope(c)
	if !ok {
		return domain.OrganizationContext{}, false
	}
	if !org.Role.CanManageOrganization() {
		response.HandleError(c, "Mail domain management forbidden", domain.ErrPermissionDenied)
		return domain.OrganizationContext{}, false
	}
	return org, true
}

func privateResponse(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	c.Header("Vary", "Authorization, Organization-Id")
}
