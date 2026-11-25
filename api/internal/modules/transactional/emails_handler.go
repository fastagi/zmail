package transactional

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/modules/apikey"
	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/response"
)

// maxDirectEmailBodyBytes bounds a direct email send request on POST /v1/emails.
const maxDirectEmailBodyBytes = 512 << 10

// StringOrSlice unmarshals either a single JSON string or a JSON array of strings.
type StringOrSlice []string

// UnmarshalJSON accepts either `"user@example.com"` or `["user@example.com"]`.
func (s *StringOrSlice) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "null" {
		*s = nil
		return nil
	}
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*s = []string{single}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err == nil {
		*s = many
		return nil
	}
	return errors.New("must be a string or an array of strings")
}

// SendEmailRequest is the Resend-style body of POST /v1/emails.
// When Template is empty, direct mode uses From, To, Subject, and HTML/Text.
// When Template is non-empty, template mode delegates to the transactional template pipeline.
type SendEmailRequest struct {
	From      string            `json:"from"`
	To        StringOrSlice     `json:"to"`
	Subject   string            `json:"subject"`
	HTML      string            `json:"html"`
	Text      string            `json:"text"`
	Template  string            `json:"template"`
	Variables map[string]string `json:"variables"`
}

// SendEmail handles POST /v1/emails for an API key with mail:transactional.
func (h *Handler) SendEmail(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	if !h.service.TransactionalEnabledPort() {
		response.HandleError(c, "Transactional mail is disabled", domain.ErrTransactionalDisabled)
		return
	}
	var req SendEmailRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	if len(req.To) == 0 {
		respondFields(c, domain.CodeValidationFailed, "Validation failed", map[string][]string{"to": {"is required"}})
		return
	}
	if len(req.To) > 1 {
		respondFields(c, domain.CodeValidationFailed, "Validation failed", map[string][]string{"to": {"must contain exactly one recipient"}})
		return
	}
	to := req.To[0]

	actor := domain.MailTransactionalActor{
		OrganizationID: viewer.OrganizationID,
		UserID:         viewer.UserID,
		APIKeyID:       apiKeyID(c),
		Mode:           apikey.APIKeyModeFromContext(c),
		Viewer:         viewer,
	}
	idempotencyKey := strings.TrimSpace(c.GetHeader(IdempotencyKeyHeader))

	var (
		message *domain.TransactionalMessage
		created bool
		err     error
	)
	if templateKey := strings.TrimSpace(req.Template); templateKey != "" {
		if idempotencyKey == "" {
			idempotencyKey = generateEmailIdempotencyKey()
		}
		message, created, err = h.service.SendTransactionalPort(c.Request.Context(), actor, domain.MailTransactionalSendInput{
			Template:       templateKey,
			To:             to,
			IdempotencyKey: idempotencyKey,
			Variables:      req.Variables,
		})
	} else {
		message, created, err = h.service.SendDirectTransactionalPort(c.Request.Context(), actor, domain.MailDirectSendInput{
			From:           req.From,
			To:             to,
			Subject:        req.Subject,
			HTML:           req.HTML,
			Text:           req.Text,
			IdempotencyKey: idempotencyKey,
		})
	}
	if err != nil {
		respondTransactionalError(c, "Failed to send email", err)
		return
	}
	privateResponse(c)
	c.Header("Location", "/v1/emails/"+strconv.FormatUint(uint64(message.ID), 10))
	if created {
		response.Accepted(c, toTransactionalMessageResponse(message))
		return
	}
	response.Success(c, toTransactionalMessageResponse(message))
}

func generateEmailIdempotencyKey() string {
	var random [16]byte
	_, _ = rand.Read(random[:])
	return "email:" + hex.EncodeToString(random[:])
}
