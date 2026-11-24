package broadcast

import (
	"net/http"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/pkg/response"
)

// RegisterErrorMappings maps broadcast domain errors to public HTTP status and error codes.
func (h *Handler) RegisterErrorMappings(mapper *response.ErrorMapper) {
	mapper.Register(domain.ErrMailBroadcastNotFound, http.StatusNotFound, domain.CodeMailBroadcastNotFound)
	mapper.Register(domain.ErrMailBroadcastInvalid, http.StatusUnprocessableEntity, domain.CodeMailBroadcastInvalid)
	mapper.Register(domain.ErrMailBroadcastNotEditable, http.StatusConflict, domain.CodeMailBroadcastNotEditable)
	mapper.Register(domain.ErrMailBroadcastNoRecipients, http.StatusConflict, domain.CodeMailBroadcastNoRecipients)
	mapper.Register(domain.ErrMailBroadcastDisabled, http.StatusServiceUnavailable, domain.CodeMailBroadcastDisabled)
	mapper.Register(domain.ErrMailUnsubscribeInvalid, http.StatusUnprocessableEntity, domain.CodeMailUnsubscribeInvalid)
}
