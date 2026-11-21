package mailbox

import (
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

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
	transactional := h.service.TransactionalPolicy()
	for _, mailboxDomain := range domains {
		kind := "organization"
		if mailboxDomain.Shared {
			kind = "shared"
		}
		items = append(items, MailboxDomainResponse{
			Domain:                  mailboxDomain.Name,
			Kind:                    kind,
			MaxMailboxes:            h.service.MaxMailboxes(),
			Provider:                h.service.Provider(),
			InboundEnabled:          h.service.InboundEnabled(),
			DailySendLimit:          h.service.DailySendLimit(),
			DailyBroadcastLimit:     h.service.DailyBroadcastLimit(),
			DailyTransactionalLimit: h.service.DailyTransactionalLimit(),
			BroadcastsEnabled:       h.service.BroadcastsEnabled(),
			CustomDomainsEnabled:    h.service.CustomDomainsEnabled(),

			TransactionalEnabled:                 transactional.Enabled,
			TransactionalRecipientLimitPerMinute: transactional.RecipientPerMinute,
			TransactionalKeyLimitPerSecond:       transactional.KeyPerSecond,
			SimulatorDomain:                      transactional.SimulatorDomain,
		})
	}
	privateResponse(c)
	response.Success(c, items)
}

// ListMailboxes returns the organization's mailboxes the caller has any access to, each with that
// access. Admins see every mailbox's metadata; mail stays with its owner and members. The usable
// scope narrows it to the mailboxes the caller works in.
// fastbuild:bounded-list max=5000 reason=an organization owns at most domain.MailboxesPerOrganizationCeiling mailboxes
func (h *Handler) ListMailboxes(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	var request ListMailboxesRequest
	if !bindValidatedQuery(c, &request) {
		return
	}
	mailboxes, err := h.service.ListMailboxes(c.Request.Context(), viewer, MailboxListScope(request.Scope))
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
	mailbox, err := h.service.CreateMailbox(c.Request.Context(), organization, req.input())
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
	if err := h.service.DeleteMailbox(c.Request.Context(), organization, mailboxID); err != nil {
		response.HandleError(c, "Failed to delete mailbox", err)
		return
	}
	response.NoContent(c)
}

func (h *Handler) ListLog(c *gin.Context) {
	viewer, ok := organizationScope(c)
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
	messages, total, err := h.service.ListLog(c.Request.Context(), viewer, domain.MailLogFilter{
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
	viewer, ok := organizationScope(c)
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
	stats, err := h.service.Stats(c.Request.Context(), viewer, days)
	if err != nil {
		response.HandleError(c, "Failed to load mail statistics", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailDayStats(stats))
}

func (h *Handler) ListMessages(c *gin.Context) {
	viewer, mailboxID, ok := mailboxScope(c)
	if !ok {
		return
	}
	page := pagination.FromContext(c)
	filter := messageFilterFromQuery(c)
	filter.Page, filter.PerPage = page.GetPage(), page.GetPerPage()
	messages, total, err := h.service.ListMessages(c.Request.Context(), viewer, mailboxID, filter)
	if err != nil {
		response.HandleError(c, "Failed to list mail messages", err)
		return
	}
	paginator := pagination.NewPaginator(toMailMessageSummaries(messages), total, page.GetPage(), page.GetPerPage())
	paginator.SetPath(c.Request.URL.Path)
	privateResponse(c)
	response.Success(c, paginator)
}

// messageFilterFromQuery reads the listing's query. A value it cannot read is named in Malformed
// rather than refused here, so the service still answers access and lookups first.
func messageFilterFromQuery(c *gin.Context) domain.MailMessageFilter {
	filter := domain.MailMessageFilter{
		Direction: domain.MailDirection(c.Query("direction")),
		Placement: domain.MailPlacement(c.Query("placement")),
	}
	malformed := func(name string) { filter.Malformed = append(filter.Malformed, name) }
	// A filter given twice is ambiguous, so it is refused rather than read from its first value.
	for _, name := range []string{"direction", "placement", "folder_id", "label_id", "starred", "unread", "has_attachments", "q", "group"} {
		if len(c.QueryArray(name)) > 1 {
			malformed(name)
		}
	}
	readID := func(name string) uint {
		raw, present := c.GetQuery(name)
		if !present {
			return 0
		}
		id, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || id == 0 {
			malformed(name)
			return 0
		}
		return uint(id)
	}
	readBool := func(name string) *bool {
		raw, present := c.GetQuery(name)
		switch {
		case !present:
			return nil
		case raw == "true":
			return new(true)
		case raw == "false":
			return new(false)
		}
		malformed(name)
		return nil
	}
	filter.FolderID = readID("folder_id")
	filter.LabelID = readID("label_id")
	filter.Starred = readBool("starred")
	filter.Unread = readBool("unread")
	filter.HasAttachments = readBool("has_attachments")
	if raw, present := c.GetQuery("q"); present {
		filter.Query = strings.TrimSpace(raw)
		if filter.Query == "" || utf8.RuneCountInString(raw) > domain.MaxMailSearchLength {
			malformed("q")
		}
	}
	if raw, present := c.GetQuery("group"); present {
		if raw == "thread" {
			filter.GroupThreads = true
		} else {
			malformed("group")
		}
	}
	return filter
}

// GetThread returns the messages of one thread outside the trash, oldest first.
// fastbuild:bounded-list max=100 reason=a thread returns at most domain.MaxMailThreadMessageIDs messages
func (h *Handler) GetThread(c *gin.Context) {
	viewer, mailboxID, ok := mailboxScope(c)
	if !ok {
		return
	}
	threadID, ok := httphandler.ParseID(c, "thread_id")
	if !ok {
		return
	}
	messages, err := h.service.GetThread(c.Request.Context(), viewer, mailboxID, threadID)
	if err != nil {
		response.HandleError(c, "Failed to get mail thread", err)
		return
	}
	items := make([]*MailMessageResponse, 0, len(messages))
	for _, message := range messages {
		items = append(items, toMailMessageResponse(message))
	}
	privateResponse(c)
	response.Success(c, items)
}

// MessageCounts returns the caller's unread and starred counts for a mailbox's navigation.
func (h *Handler) MessageCounts(c *gin.Context) {
	viewer, mailboxID, ok := mailboxScope(c)
	if !ok {
		return
	}
	counts, err := h.service.MessageCounts(c.Request.Context(), viewer, mailboxID)
	if err != nil {
		response.HandleError(c, "Failed to count mail messages", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailMessageCountsResponse(counts))
}

func (h *Handler) GetMessage(c *gin.Context) {
	viewer, mailboxID, messageID, ok := messageScope(c)
	if !ok {
		return
	}
	message, err := h.service.GetMessage(c.Request.Context(), viewer, mailboxID, messageID)
	if err != nil {
		response.HandleError(c, "Failed to get mail message", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailMessageResponse(message))
}

// RevealMessage returns an inbound message with its one-time codes and sign-in links unmasked,
// to anyone who may read it, and logs that it did. It is a POST so nothing caches or prefetches it.
func (h *Handler) RevealMessage(c *gin.Context) {
	viewer, mailboxID, messageID, ok := messageScope(c)
	if !ok {
		return
	}
	message, err := h.service.GetMessage(c.Request.Context(), viewer, mailboxID, messageID)
	if err != nil {
		response.HandleError(c, "Failed to reveal mail message", err)
		return
	}
	slog.InfoContext(c.Request.Context(), "mail.secret_revealed",
		slog.Uint64("organization_id", uint64(viewer.OrganizationID)), slog.Uint64("user_id", uint64(viewer.UserID)),
		slog.Uint64("mailbox_id", uint64(mailboxID)), slog.Uint64("message_id", uint64(messageID)))
	privateResponse(c)
	response.Success(c, toRevealedMailMessageResponse(message))
}

// DownloadAttachment returns the bytes of one attached file. The declared media type is never
// served: every file is an opaque download, so a browser cannot render or run it on this origin.
func (h *Handler) DownloadAttachment(c *gin.Context) {
	viewer, mailboxID, messageID, ok := messageScope(c)
	if !ok {
		return
	}
	attachmentID, ok := httphandler.ParseID(c, "attachment_id")
	if !ok {
		return
	}
	attachment, err := h.service.GetAttachment(c.Request.Context(), viewer, mailboxID, messageID, attachmentID)
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
	viewer, mailboxID, ok := mailboxScope(c)
	if !ok {
		return
	}
	var req SendMailMessageRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	message, created, err := h.service.SendMessage(
		c.Request.Context(), viewer, mailboxID, req.input(c.GetHeader(IdempotencyKeyHeader)))
	respondSent(c, message, created, err)
}

// SendOutbound sends one message with an API key that holds the mail:send scope.
func (h *Handler) SendOutbound(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	var req SendOutboundMailRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	message, created, err := h.service.SendFromAddress(
		c.Request.Context(), viewer, req.From, req.input(c.GetHeader(IdempotencyKeyHeader)))
	respondSent(c, message, created, err)
}

// GetOutbound returns one message the API key owner sent, so a sender can check its outcome.
func (h *Handler) GetOutbound(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	messageID, ok := httphandler.ParseID(c, "message_id")
	if !ok {
		return
	}
	message, err := h.service.GetOutboundMessage(c.Request.Context(), viewer, messageID)
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
	if message.QueuedReason != "" {
		// Stored and waiting for room in the send limit: accepted, not yet sent.
		response.Accepted(c, toMailMessageResponse(message))
		return
	}
	if created {
		response.Created(c, toMailMessageResponse(message))
		return
	}
	response.Success(c, toMailMessageResponse(message))
}

func (h *Handler) UpdateMessage(c *gin.Context) {
	viewer, mailboxID, messageID, ok := messageScope(c)
	if !ok {
		return
	}
	var req UpdateMailMessageRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	message, err := h.service.UpdateMessage(c.Request.Context(), viewer, mailboxID, messageID, req.update())
	if err != nil {
		respondOrganizeError(c, "Failed to update mail message", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailMessageResponse(message))
}

func (h *Handler) DeleteMessage(c *gin.Context) {
	viewer, mailboxID, messageID, ok := messageScope(c)
	if !ok {
		return
	}
	if err := h.service.DeleteMessage(c.Request.Context(), viewer, mailboxID, messageID); err != nil {
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
		ConnectionID: c.Param("connection_id"),
		Recipient:    c.GetHeader(InboundRecipientHeader),
		Signature:    c.GetHeader(InboundSignatureHeader),
		Raw:          raw,
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
	result, err := h.service.RecordDeliveryEvent(c.Request.Context(), DeliveryEvent{
		Signature: c.GetHeader(InboundSignatureHeader),
		Raw:       raw,
	})
	if err != nil {
		response.HandleError(c, "Failed to record delivery event", err)
		return
	}
	body := DeliveryEventResponse{
		MessageID: result.MessageID, MessageKind: result.Kind, Type: string(result.Event.Type), Duplicate: !result.Recorded,
	}
	if result.Recorded {
		response.Created(c, body)
		return
	}
	response.Success(c, body)
}

func mailboxScope(c *gin.Context) (viewer domain.OrganizationContext, mailboxID uint, ok bool) {
	return viewerScope(c, "mailbox_id")
}

func messageScope(c *gin.Context) (viewer domain.OrganizationContext, mailboxID, messageID uint, ok bool) {
	viewer, mailboxID, ok = mailboxScope(c)
	if !ok {
		return domain.OrganizationContext{}, 0, 0, false
	}
	messageID, ok = httphandler.ParseID(c, "message_id")
	return viewer, mailboxID, messageID, ok
}

// viewerScope returns the verified membership the request acts with and one numeric path id.
func viewerScope(c *gin.Context, param string) (viewer domain.OrganizationContext, id uint, ok bool) {
	viewer, ok = organizationScope(c)
	if !ok {
		return domain.OrganizationContext{}, 0, false
	}
	id, ok = httphandler.ParseID(c, param)
	return viewer, id, ok
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

// bindValidatedQuery binds query parameters whose values the contract validates: a value that fails
// its validation tag is 422 COMMON.VALIDATION_FAILED, anything unparseable 400 COMMON.INVALID_INPUT.
func bindValidatedQuery(c *gin.Context, obj any) bool {
	if err := c.ShouldBindQuery(obj); err != nil {
		httphandler.WriteBodyError(c, err)
		return false
	}
	return true
}
