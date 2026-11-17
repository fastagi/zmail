package mailbox

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/starter/assembly"
	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/pagination"
	"github.com/fastagi/zmail/api/pkg/response"
)

// IdempotencyKeyHeader names a send so a retry returns the first attempt instead of sending again.
const IdempotencyKeyHeader = "Idempotency-Key"

// API key scopes of the programmatic mail routes.
const (
	ScopeMailSend = "mail:send"
	ScopeMailRead = "mail:read"
)

// Handler handles HTTP requests for the mailbox starter.
type Handler struct {
	service Service
}

var (
	_ assembly.Module      = (*Handler)(nil)
	_ assembly.RouteModule = (*Handler)(nil)
	_ assembly.ErrorModule = (*Handler)(nil)
)

// NewHandler creates a new handler.
func NewHandler(service Service) *Handler {
	return &Handler{service: service}
}

// Name returns the starter name.
func (h *Handler) Name() string {
	return config.StarterMailbox
}

// ListDomains returns the domains the organization may create addresses on: the shared domain
// and its verified domains. The capability flags describe the instance and repeat on every item.
// fastbuild:bounded-list max=11 reason=the shared domain plus at most domain.MaxMailDomainsPerOrganization organization domains
func (h *Handler) ListDomains(c *gin.Context) {
	organizationID, ok := currentOrganizationID(c)
	if !ok {
		return
	}
	domains, err := h.service.MailboxDomains(c.Request.Context(), organizationID)
	if err != nil {
		response.HandleError(c, "Failed to list mailbox domains", err)
		return
	}
	items := make([]MailboxDomainResponse, 0, len(domains))
	for _, mailboxDomain := range domains {
		kind := "organization"
		if mailboxDomain.Shared {
			kind = "shared"
		}
		items = append(items, MailboxDomainResponse{
			Domain:               mailboxDomain.Name,
			Kind:                 kind,
			MaxMailboxes:         domain.MaxMailboxesPerOrganization,
			Provider:             h.service.Provider(),
			InboundEnabled:       h.service.InboundEnabled(),
			DailySendLimit:       h.service.DailySendLimit(),
			BroadcastsEnabled:    h.service.BroadcastsEnabled(),
			CustomDomainsEnabled: h.service.CustomDomainsEnabled(),
		})
	}
	privateResponse(c)
	response.Success(c, items)
}

// ListMailboxes returns the organization's mailboxes.
// fastbuild:bounded-list max=50 reason=an organization owns at most domain.MaxMailboxesPerOrganization mailboxes
func (h *Handler) ListMailboxes(c *gin.Context) {
	organizationID, ok := currentOrganizationID(c)
	if !ok {
		return
	}
	mailboxes, err := h.service.ListMailboxes(c.Request.Context(), organizationID)
	if err != nil {
		response.HandleError(c, "Failed to list mailboxes", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailboxResponses(mailboxes))
}

// CreateMailbox claims an address for the organization. Only owners and admins may.
func (h *Handler) CreateMailbox(c *gin.Context) {
	organization, ok := managerScope(c)
	if !ok {
		return
	}
	var req CreateMailboxRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	mailbox, err := h.service.CreateMailbox(
		c.Request.Context(), organization.OrganizationID, organization.UserID, req.LocalPart, req.DisplayName, req.Domain)
	if err != nil {
		response.HandleError(c, "Failed to create mailbox", err)
		return
	}
	privateResponse(c)
	response.Created(c, toMailboxResponse(mailbox))
}

// DeleteMailbox removes a mailbox and its messages. Only owners and admins may.
func (h *Handler) DeleteMailbox(c *gin.Context) {
	organization, ok := managerScope(c)
	if !ok {
		return
	}
	mailboxID, ok := httphandler.ParseID(c, "mailbox_id")
	if !ok {
		return
	}
	if err := h.service.DeleteMailbox(c.Request.Context(), organization.OrganizationID, mailboxID); err != nil {
		response.HandleError(c, "Failed to delete mailbox", err)
		return
	}
	response.NoContent(c)
}

func (h *Handler) ListLog(c *gin.Context) {
	organizationID, ok := currentOrganizationID(c)
	if !ok {
		return
	}
	var mailboxID uint
	if raw := c.Query("mailbox_id"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 32)
		if err != nil || parsed == 0 {
			response.HandleError(c, "Invalid mailbox filter", domain.ErrInvalidInput)
			return
		}
		mailboxID = uint(parsed)
	}
	page := pagination.FromContext(c)
	messages, total, err := h.service.ListLog(c.Request.Context(), organizationID, domain.MailLogFilter{
		Direction: domain.MailDirection(c.Query("direction")),
		Status:    domain.MailStatus(c.Query("status")),
		MailboxID: mailboxID,
		Query:     c.Query("q"),
		Page:      page.GetPage(),
		PerPage:   page.GetPerPage(),
	})
	if err != nil {
		response.HandleError(c, "Failed to list mail messages", err)
		return
	}
	paginator := pagination.NewPaginator(toMailLogEntries(messages), total, page.GetPage(), page.GetPerPage())
	paginator.SetPath(c.Request.URL.Path)
	privateResponse(c)
	response.Success(c, paginator)
}

// ListStats returns per-day counts for the caller's mail.
// fastbuild:bounded-list max=90 reason=one entry per day of a window capped at MaxStatsDays
func (h *Handler) ListStats(c *gin.Context) {
	organizationID, ok := currentOrganizationID(c)
	if !ok {
		return
	}
	days := 7
	if raw := c.Query("days"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			response.HandleError(c, "Invalid statistics window", domain.ErrInvalidInput)
			return
		}
		days = parsed
	}
	stats, err := h.service.Stats(c.Request.Context(), organizationID, days)
	if err != nil {
		response.HandleError(c, "Failed to load mail statistics", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailDayStats(stats))
}

func (h *Handler) ListMessages(c *gin.Context) {
	organizationID, mailboxID, ok := mailboxScope(c)
	if !ok {
		return
	}
	page := pagination.FromContext(c)
	messages, total, err := h.service.ListMessages(c.Request.Context(), organizationID, mailboxID, domain.MailMessageFilter{
		Direction: domain.MailDirection(c.Query("direction")),
		Page:      page.GetPage(),
		PerPage:   page.GetPerPage(),
	})
	if err != nil {
		response.HandleError(c, "Failed to list mail messages", err)
		return
	}
	paginator := pagination.NewPaginator(toMailMessageSummaries(messages), total, page.GetPage(), page.GetPerPage())
	paginator.SetPath(c.Request.URL.Path)
	privateResponse(c)
	response.Success(c, paginator)
}

func (h *Handler) GetMessage(c *gin.Context) {
	organizationID, mailboxID, messageID, ok := messageScope(c)
	if !ok {
		return
	}
	message, err := h.service.GetMessage(c.Request.Context(), organizationID, mailboxID, messageID)
	if err != nil {
		response.HandleError(c, "Failed to get mail message", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailMessageResponse(message))
}

// DownloadAttachment returns the bytes of one attached file. The declared media type is never
// served: every file is an opaque download, so a browser cannot render or run it on this origin.
func (h *Handler) DownloadAttachment(c *gin.Context) {
	organizationID, mailboxID, messageID, ok := messageScope(c)
	if !ok {
		return
	}
	attachmentID, ok := httphandler.ParseID(c, "attachment_id")
	if !ok {
		return
	}
	attachment, err := h.service.GetAttachment(c.Request.Context(), organizationID, mailboxID, messageID, attachmentID)
	if err != nil {
		response.HandleError(c, "Failed to get mail attachment", err)
		return
	}
	privateResponse(c)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "default-src 'none'; sandbox")
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": attachment.Filename}))
	c.Data(http.StatusOK, "application/octet-stream", attachment.Content)
}

func (h *Handler) SendMessage(c *gin.Context) {
	organizationID, mailboxID, ok := mailboxScope(c)
	if !ok {
		return
	}
	var req SendMailMessageRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	message, created, err := h.service.SendMessage(
		c.Request.Context(), organizationID, mailboxID, req.input(c.GetHeader(IdempotencyKeyHeader)))
	respondSent(c, message, created, err)
}

// SendOutbound sends one message with an API key that holds the mail:send scope.
func (h *Handler) SendOutbound(c *gin.Context) {
	organizationID, ok := currentOrganizationID(c)
	if !ok {
		return
	}
	var req SendOutboundMailRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	message, created, err := h.service.SendFromAddress(
		c.Request.Context(), organizationID, req.From, req.input(c.GetHeader(IdempotencyKeyHeader)))
	respondSent(c, message, created, err)
}

// GetOutbound returns one message the API key owner sent, so a sender can check its outcome.
func (h *Handler) GetOutbound(c *gin.Context) {
	organizationID, ok := currentOrganizationID(c)
	if !ok {
		return
	}
	messageID, ok := httphandler.ParseID(c, "message_id")
	if !ok {
		return
	}
	message, err := h.service.GetOutboundMessage(c.Request.Context(), organizationID, messageID)
	if err != nil {
		response.HandleError(c, "Failed to get mail message", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailMessageResponse(message))
}

// respondSent answers 201 for a new send and 200 when an idempotency key replayed an earlier one.
func respondSent(c *gin.Context, message *domain.MailMessage, created bool, err error) {
	if err != nil && respondMissingVariables(c, err) {
		return
	}
	if err != nil {
		response.HandleError(c, "Failed to send mail message", err)
		return
	}
	privateResponse(c)
	if created {
		response.Created(c, toMailMessageResponse(message))
		return
	}
	response.Success(c, toMailMessageResponse(message))
}

func (h *Handler) UpdateMessage(c *gin.Context) {
	organizationID, mailboxID, messageID, ok := messageScope(c)
	if !ok {
		return
	}
	var req UpdateMailMessageRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	message, err := h.service.SetMessageRead(c.Request.Context(), organizationID, mailboxID, messageID, *req.Read)
	if err != nil {
		response.HandleError(c, "Failed to update mail message", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailMessageResponse(message))
}

func (h *Handler) DeleteMessage(c *gin.Context) {
	organizationID, mailboxID, messageID, ok := messageScope(c)
	if !ok {
		return
	}
	if err := h.service.DeleteMessage(c.Request.Context(), organizationID, mailboxID, messageID); err != nil {
		response.HandleError(c, "Failed to delete mail message", err)
		return
	}
	response.NoContent(c)
}

// ReceiveInbound accepts one raw RFC 5322 message from the mail edge. The request is
// authenticated by its HMAC signature, not by a user session.
func (h *Handler) ReceiveInbound(c *gin.Context) {
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, MaxInboundBytes+1))
	var bodyLimit *http.MaxBytesError
	if len(raw) > MaxInboundBytes || errors.As(err, &bodyLimit) {
		response.HandleError(c, "Inbound mail is too large", ErrMailInboundTooLarge)
		return
	}
	if err != nil {
		response.HandleError(c, "Failed to read inbound mail", ErrMailInboundUnparseable)
		return
	}
	message, created, err := h.service.ReceiveInbound(c.Request.Context(), InboundDelivery{
		Recipient: c.GetHeader(InboundRecipientHeader),
		Signature: c.GetHeader(InboundSignatureHeader),
		Raw:       raw,
	})
	if err != nil {
		response.HandleError(c, "Failed to accept inbound mail", err)
		return
	}
	body := InboundMailResponse{ID: message.ID, Duplicate: !created}
	if created {
		response.Created(c, body)
		return
	}
	response.Success(c, body)
}

// ReceiveDeliveryEvent accepts one provider delivery event from the mail edge. Like inbound
// mail it is authenticated by the edge's HMAC signature, not by a user session.
func (h *Handler) ReceiveDeliveryEvent(c *gin.Context) {
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, MaxDeliveryEventBytes+1))
	if err != nil || len(raw) > MaxDeliveryEventBytes {
		response.HandleError(c, "Delivery event is invalid", ErrMailDeliveryEventInvalid)
		return
	}
	message, event, recorded, err := h.service.RecordDeliveryEvent(c.Request.Context(), DeliveryEvent{
		Signature: c.GetHeader(InboundSignatureHeader),
		Raw:       raw,
	})
	if err != nil {
		response.HandleError(c, "Failed to record delivery event", err)
		return
	}
	body := DeliveryEventResponse{MessageID: message.ID, Type: string(event.Type), Duplicate: !recorded}
	if recorded {
		response.Created(c, body)
		return
	}
	response.Success(c, body)
}

func mailboxScope(c *gin.Context) (organizationID, mailboxID uint, ok bool) {
	organizationID, ok = currentOrganizationID(c)
	if !ok {
		return 0, 0, false
	}
	mailboxID, ok = httphandler.ParseID(c, "mailbox_id")
	return organizationID, mailboxID, ok
}

func messageScope(c *gin.Context) (organizationID, mailboxID, messageID uint, ok bool) {
	organizationID, mailboxID, ok = mailboxScope(c)
	if !ok {
		return 0, 0, 0, false
	}
	messageID, ok = httphandler.ParseID(c, "message_id")
	return organizationID, mailboxID, messageID, ok
}

// organizationScope returns the organization the request acts in, resolved and verified by the
// organization_context middleware. The raw Organization-Id header is never trusted here.
func organizationScope(c *gin.Context) (domain.OrganizationContext, bool) {
	organization, ok := domain.OrganizationContextFromContext(c.Request.Context())
	if !ok {
		response.HandleError(c, "Organization context required", domain.ErrOrganizationContextRequired)
		return domain.OrganizationContext{}, false
	}
	return organization, true
}

func currentOrganizationID(c *gin.Context) (uint, bool) {
	organization, ok := organizationScope(c)
	return organization.OrganizationID, ok
}

// managerScope additionally requires the owner or admin role.
func managerScope(c *gin.Context) (domain.OrganizationContext, bool) {
	organization, ok := organizationScope(c)
	if !ok {
		return domain.OrganizationContext{}, false
	}
	if !organization.Role.CanManageOrganization() {
		response.HandleError(c, "Mailbox management forbidden", domain.ErrPermissionDenied)
		return domain.OrganizationContext{}, false
	}
	return organization, true
}

// privateResponse keeps mail out of shared and browser caches and tells caches the answer
// depends on the caller and the selected organization.
func privateResponse(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	c.Header("Vary", "Authorization, Organization-Id")
}
