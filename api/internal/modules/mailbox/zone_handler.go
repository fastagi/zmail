package mailbox

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	handler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/response"
)

// MailDomainPreviewRequest names a domain to read before connecting it.
type MailDomainPreviewRequest struct {
	Name string `json:"name" binding:"required,max=253"`
}

// MailDomainPreviewResponse is what connecting the domain would find.
type MailDomainPreviewResponse struct {
	Name                string   `json:"name"`
	Apex                string   `json:"apex"`
	ExistingMX          []string `json:"existing_mx"`
	SuggestedCapability string   `json:"suggested_capability"`
}

// MailZoneResponse is the public shape of a managed zone. The provider's zone ID stays private.
type MailZoneResponse struct {
	ID                  uint       `json:"id"`
	Name                string     `json:"name"`
	Status              string     `json:"status"`
	NameServers         []string   `json:"name_servers"`
	OriginalNameServers []string   `json:"original_name_servers"`
	ActivatedAt         *time.Time `json:"activated_at"`
	LastCheckedAt       *time.Time `json:"last_checked_at"`
	CreatedAt           time.Time  `json:"created_at"`
}

func toMailZoneResponse(zone *domain.MailZone) *MailZoneResponse {
	if zone == nil {
		return nil
	}
	nameServers := zone.NameServers
	if nameServers == nil {
		nameServers = []string{}
	}
	originalNameServers := zone.OriginalNameServers
	if originalNameServers == nil {
		originalNameServers = []string{}
	}
	return &MailZoneResponse{
		ID:                  zone.ID,
		Name:                zone.Name,
		Status:              string(zone.Status),
		NameServers:         nameServers,
		OriginalNameServers: originalNameServers,
		ActivatedAt:         zone.ActivatedAt,
		LastCheckedAt:       zone.LastCheckedAt,
		CreatedAt:           zone.CreatedAt,
	}
}

// PreviewMailDomain reads a domain's apex and public MX records before it is connected. It
// changes nothing, so any member may.
func (h *Handler) PreviewMailDomain(c *gin.Context) {
	if _, ok := currentOrganizationID(c); !ok {
		return
	}
	var req MailDomainPreviewRequest
	if !handler.BindJSON(c, &req) {
		return
	}
	preview, err := h.domains.PreviewMailDomain(c.Request.Context(), req.Name)
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
	zones, err := h.domains.ListMailZones(c.Request.Context(), organizationID)
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
	id, ok := handler.ParseID(c, "zone_id")
	if !ok {
		return
	}
	zone, err := h.domains.GetMailZone(c.Request.Context(), organizationID, id)
	if err != nil {
		response.HandleError(c, "Failed to get mail zone", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailZoneResponse(zone))
}

// DeleteMailZone removes a managed zone from Zmail and the provider account. Only owners and
// admins may.
func (h *Handler) DeleteMailZone(c *gin.Context) {
	organization, ok := managerScope(c)
	if !ok {
		return
	}
	id, ok := handler.ParseID(c, "zone_id")
	if !ok {
		return
	}
	if err := h.domains.DeleteMailZone(c.Request.Context(), organization.OrganizationID, id); err != nil {
		response.HandleError(c, "Failed to delete mail zone", err)
		return
	}
	response.NoContent(c)
}

// EnableMailDomainReceiving makes a send-only domain receive through Zmail. Only owners and
// admins may.
func (h *Handler) EnableMailDomainReceiving(c *gin.Context) {
	h.runDomainAction(c, "Failed to enable receiving on mail domain", h.domains.EnableMailDomainReceiving)
}
