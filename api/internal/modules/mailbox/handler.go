package mailbox

import (
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/fastmail/api/internal/domain"
	"github.com/fastagi/fastmail/api/internal/infra/config"
	"github.com/fastagi/fastmail/api/internal/modules/user"
	"github.com/fastagi/fastmail/api/internal/starter/assembly"
	httphandler "github.com/fastagi/fastmail/api/pkg/handler"
	"github.com/fastagi/fastmail/api/pkg/pagination"
	"github.com/fastagi/fastmail/api/pkg/response"
)

// Handler handles HTTP requests for the mailbox starter.
type Handler struct {
	service        Service
	cleaner        user.AccountDeletionCleaner
	deletionPolicy *user.AccountDeletionPolicy
}

var (
	_ assembly.Module           = (*Handler)(nil)
	_ assembly.RouteModule      = (*Handler)(nil)
	_ assembly.ErrorModule      = (*Handler)(nil)
	_ assembly.ActivationModule = (*Handler)(nil)
)

// NewHandler creates a new handler.
func NewHandler(service *service, deletionPolicy *user.AccountDeletionPolicy) *Handler {
	return &Handler{service: service, cleaner: service, deletionPolicy: deletionPolicy}
}

// Activate installs account-deletion cleanup only when the mailbox starter is selected.
func (h *Handler) Activate() error {
	return h.deletionPolicy.RegisterCleaner(h.cleaner)
}

// Name returns the starter name.
func (h *Handler) Name() string {
	return config.StarterMailbox
}

// ListDomains returns the domains a user may create addresses on.
// fastbuild:bounded-list max=1 reason=one configured mailbox domain per deployment
func (h *Handler) ListDomains(c *gin.Context) {
	if _, ok := httphandler.GetUserID(c); !ok {
		return
	}
	privateResponse(c)
	response.Success(c, []MailboxDomainResponse{{
		Domain:       h.service.Domain(),
		MaxMailboxes: domain.MaxMailboxesPerUser,
	}})
}

// ListMailboxes returns the caller's mailboxes.
// fastbuild:bounded-list max=10 reason=a user owns at most domain.MaxMailboxesPerUser mailboxes
func (h *Handler) ListMailboxes(c *gin.Context) {
	userID, ok := httphandler.GetUserID(c)
	if !ok {
		return
	}
	mailboxes, err := h.service.ListMailboxes(c.Request.Context(), userID)
	if err != nil {
		response.HandleError(c, "Failed to list mailboxes", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailboxResponses(mailboxes))
}

func (h *Handler) CreateMailbox(c *gin.Context) {
	userID, ok := httphandler.GetUserID(c)
	if !ok {
		return
	}
	var req CreateMailboxRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	mailbox, err := h.service.CreateMailbox(c.Request.Context(), userID, req.LocalPart, req.DisplayName)
	if err != nil {
		response.HandleError(c, "Failed to create mailbox", err)
		return
	}
	privateResponse(c)
	response.Created(c, toMailboxResponse(mailbox))
}

func (h *Handler) DeleteMailbox(c *gin.Context) {
	userID, mailboxID, ok := mailboxScope(c)
	if !ok {
		return
	}
	if err := h.service.DeleteMailbox(c.Request.Context(), userID, mailboxID); err != nil {
		response.HandleError(c, "Failed to delete mailbox", err)
		return
	}
	response.NoContent(c)
}

func (h *Handler) ListMessages(c *gin.Context) {
	userID, mailboxID, ok := mailboxScope(c)
	if !ok {
		return
	}
	page := pagination.FromContext(c)
	messages, total, err := h.service.ListMessages(c.Request.Context(), userID, mailboxID, domain.MailMessageFilter{
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
	userID, mailboxID, messageID, ok := messageScope(c)
	if !ok {
		return
	}
	message, err := h.service.GetMessage(c.Request.Context(), userID, mailboxID, messageID)
	if err != nil {
		response.HandleError(c, "Failed to get mail message", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailMessageResponse(message))
}

func (h *Handler) SendMessage(c *gin.Context) {
	userID, mailboxID, ok := mailboxScope(c)
	if !ok {
		return
	}
	var req SendMailMessageRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	message, err := h.service.SendMessage(c.Request.Context(), userID, mailboxID, SendInput(req))
	if err != nil {
		response.HandleError(c, "Failed to send mail message", err)
		return
	}
	privateResponse(c)
	response.Created(c, toMailMessageResponse(message))
}

func (h *Handler) UpdateMessage(c *gin.Context) {
	userID, mailboxID, messageID, ok := messageScope(c)
	if !ok {
		return
	}
	var req UpdateMailMessageRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	message, err := h.service.SetMessageRead(c.Request.Context(), userID, mailboxID, messageID, *req.Read)
	if err != nil {
		response.HandleError(c, "Failed to update mail message", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailMessageResponse(message))
}

func (h *Handler) DeleteMessage(c *gin.Context) {
	userID, mailboxID, messageID, ok := messageScope(c)
	if !ok {
		return
	}
	if err := h.service.DeleteMessage(c.Request.Context(), userID, mailboxID, messageID); err != nil {
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

func mailboxScope(c *gin.Context) (userID, mailboxID uint, ok bool) {
	userID, ok = httphandler.GetUserID(c)
	if !ok {
		return 0, 0, false
	}
	mailboxID, ok = httphandler.ParseID(c, "mailbox_id")
	return userID, mailboxID, ok
}

func messageScope(c *gin.Context) (userID, mailboxID, messageID uint, ok bool) {
	userID, mailboxID, ok = mailboxScope(c)
	if !ok {
		return 0, 0, 0, false
	}
	messageID, ok = httphandler.ParseID(c, "message_id")
	return userID, mailboxID, messageID, ok
}

// privateResponse keeps mail out of shared and browser caches.
func privateResponse(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
}
