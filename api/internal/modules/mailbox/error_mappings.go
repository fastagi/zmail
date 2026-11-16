package mailbox

import (
	"net/http"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/pkg/response"
)

// RegisterErrorMappings maps this starter's own domain errors to public status and error codes.
func (h *Handler) RegisterErrorMappings(mapper *response.ErrorMapper) {
	mapper.Register(ErrMailboxNotFound, http.StatusNotFound, domain.CodeMailboxNotFound)
	mapper.Register(ErrMailboxAddressTaken, http.StatusConflict, domain.CodeMailboxAddressTaken)
	mapper.Register(ErrMailboxAddressInvalid, http.StatusUnprocessableEntity, domain.CodeMailboxAddressInvalid)
	mapper.Register(ErrMailboxAddressReserved, http.StatusUnprocessableEntity, domain.CodeMailboxAddressReserved)
	mapper.Register(ErrMailboxLimitReached, http.StatusConflict, domain.CodeMailboxLimitReached)
	mapper.Register(ErrMailMessageNotFound, http.StatusNotFound, domain.CodeMailMessageNotFound)
	mapper.Register(ErrMailMessageInvalid, http.StatusUnprocessableEntity, domain.CodeMailMessageInvalid)
	mapper.Register(ErrMailSendLimitReached, http.StatusTooManyRequests, domain.CodeMailSendLimitReached)
	mapper.Register(ErrMailDeliveryFailed, http.StatusBadGateway, domain.CodeMailDeliveryFailed)
	mapper.Register(ErrMailIdempotencyConflict, http.StatusConflict, domain.CodeMailIdempotencyConflict)
	mapper.Register(ErrMailIdempotencyKeyInvalid, http.StatusUnprocessableEntity, domain.CodeMailIdempotencyKeyInvalid)
	mapper.Register(ErrMailSendInProgress, http.StatusConflict, domain.CodeMailSendInProgress)
	mapper.Register(ErrMailSenderNotConfigured, http.StatusServiceUnavailable, domain.CodeMailSenderNotConfigured)
	mapper.Register(ErrMailInboundDisabled, http.StatusServiceUnavailable, domain.CodeMailInboundDisabled)
	mapper.Register(ErrMailInboundSignature, http.StatusUnauthorized, domain.CodeMailInboundSignature)
	mapper.Register(ErrMailInboundUnparseable, http.StatusUnprocessableEntity, domain.CodeMailInboundUnparseable)
	mapper.Register(ErrMailInboundTooLarge, http.StatusRequestEntityTooLarge, domain.CodeMailInboundTooLarge)
	mapper.Register(ErrMailDeliveryEventInvalid, http.StatusUnprocessableEntity, domain.CodeMailDeliveryEventInvalid)
	mapper.Register(ErrMailRecipientNotFound, http.StatusNotFound, domain.CodeMailRecipientNotFound)
}
