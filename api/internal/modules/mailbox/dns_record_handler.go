package mailbox

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	handler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/response"
)

// dnsRecordIDPattern matches a Cloudflare record ID.
var dnsRecordIDPattern = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)

// CreateMailDNSRecordRequest is a new record. Name may be "@", a single label, or a full name in
// the zone. TTL 1 or an omitted TTL is automatic.
type CreateMailDNSRecordRequest struct {
	Type     string `json:"type" binding:"required,max=10"`
	Name     string `json:"name" binding:"required,max=253"`
	Content  string `json:"content" binding:"required,max=4096"`
	TTL      *int   `json:"ttl"`
	Priority *int   `json:"priority"`
	Proxied  bool   `json:"proxied"`
}

// UpdateMailDNSRecordRequest changes a record; an omitted field keeps its value.
type UpdateMailDNSRecordRequest struct {
	Type     *string `json:"type" binding:"omitempty,max=10"`
	Name     *string `json:"name" binding:"omitempty,max=253"`
	Content  *string `json:"content" binding:"omitempty,max=4096"`
	TTL      *int    `json:"ttl"`
	Priority *int    `json:"priority"`
	Proxied  *bool   `json:"proxied"`
}

// MailDNSRecordResponse is one record of a managed zone as Cloudflare has it.
type MailDNSRecordResponse struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	TTL      int    `json:"ttl"`
	Priority *int   `json:"priority,omitempty"`
	Proxied  bool   `json:"proxied"`
	Locked   bool   `json:"locked"`
	// LockedReason names what Zmail mail uses the record for; omitted when it is not locked.
	LockedReason string `json:"locked_reason,omitempty"`
}

func toMailDNSRecordResponse(record MailDNSRecord) MailDNSRecordResponse {
	item := MailDNSRecordResponse{
		ID: record.ID, Type: record.Type, Name: record.Name, Content: record.Content, TTL: record.TTL,
		Proxied: record.Proxied, Locked: record.LockedReason != "", LockedReason: record.LockedReason,
	}
	if strings.EqualFold(record.Type, "MX") {
		priority := record.Priority
		item.Priority = &priority
	}
	return item
}

// ListMailDNSRecords lists a managed zone's records. Any member may.
// fastbuild:bounded-list max=3500 reason=Cloudflare keeps at most 3500 records in one zone
func (h *Handler) ListMailDNSRecords(c *gin.Context) {
	organizationID, ok := currentOrganizationID(c)
	if !ok {
		return
	}
	zoneID, ok := handler.ParseID(c, "zone_id")
	if !ok {
		return
	}
	records, err := h.domains.ListMailDNSRecords(c.Request.Context(), organizationID, zoneID)
	if err != nil {
		response.HandleError(c, "Failed to list DNS records", err)
		return
	}
	items := make([]MailDNSRecordResponse, 0, len(records))
	for _, record := range records {
		items = append(items, toMailDNSRecordResponse(record))
	}
	privateResponse(c)
	response.Success(c, items)
}

// CreateMailDNSRecord adds a record to a managed zone. Only owners and admins may.
func (h *Handler) CreateMailDNSRecord(c *gin.Context) {
	organization, ok := managerScope(c)
	if !ok {
		return
	}
	zoneID, ok := handler.ParseID(c, "zone_id")
	if !ok {
		return
	}
	var req CreateMailDNSRecordRequest
	if !handler.BindJSON(c, &req) {
		return
	}
	record, err := h.domains.CreateMailDNSRecord(c.Request.Context(), organization.OrganizationID, zoneID, MailDNSRecordInput(req))
	if err != nil {
		respondDNSRecordError(c, "Failed to create DNS record", err)
		return
	}
	privateResponse(c)
	response.Created(c, toMailDNSRecordResponse(*record))
}

// UpdateMailDNSRecord changes a record of a managed zone. Only owners and admins may.
func (h *Handler) UpdateMailDNSRecord(c *gin.Context) {
	organization, ok := managerScope(c)
	if !ok {
		return
	}
	zoneID, recordID, ok := dnsRecordScope(c)
	if !ok {
		return
	}
	var req UpdateMailDNSRecordRequest
	if !handler.BindJSON(c, &req) {
		return
	}
	record, err := h.domains.UpdateMailDNSRecord(c.Request.Context(), organization.OrganizationID, zoneID, recordID, MailDNSRecordPatch(req))
	if err != nil {
		respondDNSRecordError(c, "Failed to update DNS record", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailDNSRecordResponse(*record))
}

// DeleteMailDNSRecord removes a record from a managed zone. Only owners and admins may.
func (h *Handler) DeleteMailDNSRecord(c *gin.Context) {
	organization, ok := managerScope(c)
	if !ok {
		return
	}
	zoneID, recordID, ok := dnsRecordScope(c)
	if !ok {
		return
	}
	if err := h.domains.DeleteMailDNSRecord(c.Request.Context(), organization.OrganizationID, zoneID, recordID); err != nil {
		respondDNSRecordError(c, "Failed to delete DNS record", err)
		return
	}
	response.NoContent(c)
}

func dnsRecordScope(c *gin.Context) (zoneID uint, recordID string, ok bool) {
	zoneID, ok = handler.ParseID(c, "zone_id")
	if !ok {
		return 0, "", false
	}
	recordID = c.Param("record_id")
	if !dnsRecordIDPattern.MatchString(recordID) {
		response.BadRequest(c, "Invalid DNS record ID")
		return 0, "", false
	}
	return zoneID, recordID, true
}

// respondDNSRecordError answers an invalid record with a field error under the request field
// that failed, and any other error through the error mapping.
func respondDNSRecordError(c *gin.Context, message string, err error) {
	var invalid *DNSRecordInvalidError
	if errors.As(err, &invalid) {
		c.JSON(http.StatusUnprocessableEntity, response.ValidationErrorResponse{
			Code:      http.StatusUnprocessableEntity,
			ErrorCode: domain.CodeMailDNSRecordInvalid,
			Message:   "DNS record is invalid",
			Errors:    map[string][]string{invalid.Field: {invalid.Reason}},
			RequestID: c.GetString("request_id"),
		})
		return
	}
	response.HandleError(c, message, err)
}
