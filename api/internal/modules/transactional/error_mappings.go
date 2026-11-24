package transactional

import (
	"net/http"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/pkg/response"
)

// RegisterErrorMappings maps transactional domain errors to public HTTP status and error codes.
func (h *Handler) RegisterErrorMappings(mapper *response.ErrorMapper) {
	mapper.Register(domain.ErrTransactionalMessageNotFound, http.StatusNotFound, domain.CodeMailTransactionalMessageNotFound)
	mapper.Register(domain.ErrTransactionalDisabled, http.StatusServiceUnavailable, domain.CodeMailTransactionalDisabled)
	mapper.Register(domain.ErrMailRecipientRateLimited, http.StatusTooManyRequests, domain.CodeMailRecipientRateLimited)
	mapper.Register(domain.ErrTransactionalRateLimited, http.StatusTooManyRequests, domain.CodeRateLimited)
	mapper.Register(domain.ErrMailMessageInvalid, http.StatusUnprocessableEntity, domain.CodeMailMessageInvalid)
}
