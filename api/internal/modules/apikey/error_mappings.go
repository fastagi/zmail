package apikey

import (
	"net/http"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/pkg/response"
)

// RegisterErrorMappings maps this starter's domain errors to public status codes and error codes.
func (h *Handler) RegisterErrorMappings(mapper *response.ErrorMapper) {
	mapper.Register(domain.ErrAPIKeyNotFound, http.StatusNotFound, domain.CodeAPIKeyNotFound)
	mapper.Register(domain.ErrAPIKeyInvalid, http.StatusUnauthorized, domain.CodeAPIKeyInvalid)
	mapper.Register(domain.ErrAPIKeyExpired, http.StatusUnauthorized, domain.CodeAPIKeyExpired)
	mapper.Register(domain.ErrAPIKeyRevoked, http.StatusUnauthorized, domain.CodeAPIKeyRevoked)
	mapper.Register(domain.ErrAPIKeyOrganizationMismatch, http.StatusForbidden, domain.CodeAPIKeyOrganizationMismatch)
	mapper.Register(domain.ErrAPIKeyDomainNotAllowed, http.StatusForbidden, domain.CodeAPIKeyDomainNotAllowed)
}
