package mailbox

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	handler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/response"
)

// MailDomainRequest connects a domain.
type MailDomainRequest struct {
	Name string `json:"name" binding:"required,max=253"`
}

// MailDomainRecordResponse is one DNS record a domain needs, with what the last check found.
type MailDomainRecordResponse struct {
	Purpose  string `json:"purpose"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	Priority int    `json:"priority,omitempty"`
	Status   string `json:"status"`
	Found    string `json:"found,omitempty"`
}

// MailDomainResponse is the public shape of a connected domain.
type MailDomainResponse struct {
	ID         uint                       `json:"id"`
	Name       string                     `json:"name"`
	ZoneName   string                     `json:"zone_name"`
	Status     string                     `json:"status"`
	Records    []MailDomainRecordResponse `json:"records"`
	LastError  string                     `json:"last_error,omitempty"`
	SettingUp  bool                       `json:"setting_up"`
	CheckedAt  *time.Time                 `json:"checked_at"`
	VerifiedAt *time.Time                 `json:"verified_at"`
	CreatedAt  time.Time                  `json:"created_at"`
}

func toMailDomainResponse(mailDomain *domain.MailDomain) MailDomainResponse {
	records := make([]MailDomainRecordResponse, 0, len(mailDomain.Records))
	for _, record := range mailDomain.Records {
		records = append(records, MailDomainRecordResponse{
			Purpose:  string(record.Purpose),
			Type:     record.Type,
			Name:     record.Name,
			Content:  record.Content,
			Priority: record.Priority,
			Status:   string(record.Status),
			Found:    record.Found,
		})
	}
	return MailDomainResponse{
		ID:         mailDomain.ID,
		Name:       mailDomain.Name,
		ZoneName:   mailDomain.ZoneName,
		Status:     string(mailDomain.Status),
		Records:    records,
		LastError:  mailDomain.LastError,
		SettingUp:  mailDomain.SettingUp,
		CheckedAt:  mailDomain.CheckedAt,
		VerifiedAt: mailDomain.VerifiedAt,
		CreatedAt:  mailDomain.CreatedAt,
	}
}

// ListMailDomains returns the organization's connected domains.
// fastbuild:bounded-list max=10 reason=an organization connects at most domain.MaxMailDomainsPerOrganization domains
func (h *Handler) ListMailDomains(c *gin.Context) {
	organizationID, ok := currentOrganizationID(c)
	if !ok {
		return
	}
	domains, err := h.service.ListMailDomains(c.Request.Context(), organizationID)
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
	id, ok := handler.ParseID(c, "domain_id")
	if !ok {
		return
	}
	mailDomain, err := h.service.GetMailDomain(c.Request.Context(), organizationID, id)
	if err != nil {
		response.HandleError(c, "Failed to get mail domain", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailDomainResponse(mailDomain))
}

// ConnectMailDomain claims a domain and sets it up. Only owners and admins may.
func (h *Handler) ConnectMailDomain(c *gin.Context) {
	organization, ok := managerScope(c)
	if !ok {
		return
	}
	var req MailDomainRequest
	if !handler.BindJSON(c, &req) {
		return
	}
	mailDomain, err := h.service.ConnectMailDomain(c.Request.Context(), organization.OrganizationID, organization.UserID, req.Name)
	if err != nil {
		response.HandleError(c, "Failed to connect mail domain", err)
		return
	}
	privateResponse(c)
	response.Created(c, toMailDomainResponse(mailDomain))
}

// SetUpMailDomain runs the domain setup again. Only owners and admins may.
func (h *Handler) SetUpMailDomain(c *gin.Context) {
	h.runDomainAction(c, "Failed to set up mail domain", h.service.SetUpMailDomain)
}

// CheckMailDomain re-reads the domain's DNS records. Only owners and admins may.
func (h *Handler) CheckMailDomain(c *gin.Context) {
	h.runDomainAction(c, "Failed to check mail domain", h.service.CheckMailDomain)
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
	id, ok := handler.ParseID(c, "domain_id")
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

// DeleteMailDomain forgets a domain no mailbox uses; Cloudflare is left as it is.
func (h *Handler) DeleteMailDomain(c *gin.Context) {
	organization, ok := managerScope(c)
	if !ok {
		return
	}
	id, ok := handler.ParseID(c, "domain_id")
	if !ok {
		return
	}
	if err := h.service.DeleteMailDomain(c.Request.Context(), organization.OrganizationID, id); err != nil {
		response.HandleError(c, "Failed to delete mail domain", err)
		return
	}
	response.NoContent(c)
}
