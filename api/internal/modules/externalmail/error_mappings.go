package externalmail

import (
	"net/http"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/pkg/response"
)

// RegisterErrorMappings maps externalmail domain errors to public HTTP status and error codes.
func (h *Handler) RegisterErrorMappings(mapper *response.ErrorMapper) {
	mapper.Register(domain.ErrExternalMailConnectionNotFound, http.StatusNotFound, domain.CodeExternalMailConnectionNotFound)
	mapper.Register(domain.ErrExternalMailAuthorizationNotFound, http.StatusNotFound, domain.CodeExternalMailAuthorizationNotFound)
	mapper.Register(domain.ErrExternalMailAuthorizationFailed, http.StatusUnprocessableEntity, domain.CodeExternalMailAuthorizationFailed)
	mapper.Register(domain.ErrExternalMailAccountMismatch, http.StatusConflict, domain.CodeExternalMailAccountMismatch)
	mapper.Register(domain.ErrExternalMailAccountTaken, http.StatusConflict, domain.CodeExternalMailAccountTaken)
	mapper.Register(domain.ErrExternalMailConnectionLimit, http.StatusConflict, domain.CodeExternalMailConnectionLimit)
	mapper.Register(domain.ErrExternalMailAuthorizationInvalid, http.StatusUnprocessableEntity, domain.CodeExternalMailAuthorizationInvalid)
	mapper.Register(domain.ErrExternalMailProviderUnavailable, http.StatusNotFound, domain.CodeExternalMailProviderUnavailable)
}
