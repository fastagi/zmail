package mailbox

import (
	"context"
	"crypto/rand"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/middleware"
	"github.com/fastagi/zmail/api/internal/infra/router"
	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/response"
)

// Development only: a running stack has no way to put mail into an account of the fake connector,
// whose mail lives in the API process's memory. The route below exists only while `fake` is the
// active external mail source, which configuration refuses in production, and is never part of the
// public contract (see api/docs/EXTERNAL_MAIL_DEVELOPMENT.md).

// maxFakeExternalMailDeliveryBodyBytes bounds the JSON body: two parts of at most 100 KB each.
const maxFakeExternalMailDeliveryBodyBytes = 2*domain.MaxMailAgentDraftBytes + 4096

// fakeExternalMailDelivery is implemented by the service; the handler registers the development
// route only when it reports the fake source active.
type fakeExternalMailDelivery interface {
	fakeExternalMailSource() *FakeExternalMailSource
	DeliverFakeExternalMail(ctx context.Context, viewer domain.OrganizationContext, id uint, input FakeExternalMailDeliveryInput) (string, error)
}

// FakeExternalMailDeliveryInput is one message for an account of the fake connector.
type FakeExternalMailDeliveryInput struct {
	From    string
	Subject string
	Text    string
	HTML    string
	// MessageID is the provider's message id; empty generates one. Delivering the same id again
	// stores no second copy, as a real provider's repeated message would not.
	MessageID string
}

// fakeExternalMailSource returns the in-memory source while `fake` is the active external mail
// source, otherwise nil.
func (s *service) fakeExternalMailSource() *FakeExternalMailSource {
	if !s.externalMailAvailable() || s.settings.ExternalMail.Source() != config.ExternalMailSourceFake {
		return nil
	}
	for _, connector := range s.externalConnectors.list() {
		if fake, ok := connector.Source.(*FakeExternalMailSource); ok {
			return fake
		}
	}
	return nil
}

// DeliverFakeExternalMail appends a message to the account of the caller's own live connection in
// the fake source and syncs that connection at once in this process: the mail:external:sync
// worker runs in another process with its own, empty fake source, so it never sees the message.
// The sync is the worker's own, so the message is stored once, with the connection's provider as
// its source. It answers the provider message id.
func (s *service) DeliverFakeExternalMail(ctx context.Context, viewer domain.OrganizationContext, id uint, input FakeExternalMailDeliveryInput) (string, error) {
	fake := s.fakeExternalMailSource()
	if fake == nil {
		return "", ErrExternalMailConnectionNotFound
	}
	connection, err := s.ownExternalMailConnection(ctx, viewer, id, true)
	if err != nil {
		return "", err
	}
	if connection.Status == domain.ExternalMailStatusRevoked {
		return "", ErrExternalMailConnectionNotFound
	}
	token, err := s.encryptor.DecryptString(connection.EncryptedRefreshToken)
	if err != nil {
		return "", err
	}
	messageID := strings.Trim(strings.TrimSpace(input.MessageID), "<>")
	if messageID == "" {
		messageID = "dev-" + strings.ToLower(rand.Text()) + "@external.invalid"
	}
	now := s.now().UTC()
	fake.Deliver(token, ExternalMessage{
		ExternalID: messageID, FromAddress: strings.TrimSpace(input.From), To: []string{connection.AccountAddress},
		Subject: input.Subject, TextBody: input.Text, HTMLBody: input.HTML, RFCMessageID: messageID, ReceivedAt: now,
	})
	if err := s.syncExternalConnection(ctx, connection, now); err != nil {
		return "", err
	}
	return messageID, nil
}

// FakeExternalMailDeliveryRequest is the body of the development delivery route.
type FakeExternalMailDeliveryRequest struct {
	From      string `json:"from" binding:"required,email,max=320"`
	Subject   string `json:"subject" binding:"max=998"`
	Text      string `json:"text" binding:"required,max=102400"`
	HTML      string `json:"html" binding:"max=102400"`
	MessageID string `json:"message_id" binding:"max=255"`
}

// registerFakeExternalMailDelivery adds the development delivery route while the fake source is
// active; with real connectors or external mail off the route does not exist.
func (h *Handler) registerFakeExternalMailDelivery(auth *router.Router) {
	dev, ok := h.service.(fakeExternalMailDelivery)
	if !ok || dev.fakeExternalMailSource() == nil {
		return
	}
	auth.POST("/external-mail-connections/:connection_id/fake-deliveries", h.deliverFakeExternalMail(dev)).
		Name("external_mail_connections.fake_deliveries.store").WhereNumber("connection_id").
		Middleware(middleware.BodyLimit(maxFakeExternalMailDeliveryBodyBytes))
}

func (h *Handler) deliverFakeExternalMail(dev fakeExternalMailDelivery) gin.HandlerFunc {
	return func(c *gin.Context) {
		viewer, connectionID, ok := viewerScope(c, "connection_id")
		if !ok {
			return
		}
		var req FakeExternalMailDeliveryRequest
		if !httphandler.BindJSON(c, &req) {
			return
		}
		messageID, err := dev.DeliverFakeExternalMail(c.Request.Context(), viewer, connectionID, FakeExternalMailDeliveryInput(req))
		if err != nil {
			response.HandleError(c, "Failed to deliver fake external mail", err)
			return
		}
		privateResponse(c)
		response.Created(c, gin.H{"message_id": messageID})
	}
}
