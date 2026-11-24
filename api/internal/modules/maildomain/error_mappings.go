package maildomain

import (
	"net/http"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/pkg/response"
)

// RegisterErrorMappings maps maildomain and zone domain errors to public HTTP status and error codes.
func (h *Handler) RegisterErrorMappings(mapper *response.ErrorMapper) {
	mapper.Register(domain.ErrMailDomainNotFound, http.StatusNotFound, domain.CodeMailDomainNotFound)
	mapper.Register(domain.ErrMailDomainInvalid, http.StatusUnprocessableEntity, domain.CodeMailDomainInvalid)
	mapper.Register(domain.ErrMailDomainTaken, http.StatusConflict, domain.CodeMailDomainTaken)
	mapper.Register(domain.ErrMailDomainLimitReached, http.StatusConflict, domain.CodeMailDomainLimitReached)
	mapper.Register(domain.ErrMailDomainZoneNotFound, http.StatusUnprocessableEntity, domain.CodeMailDomainZoneNotFound)
	mapper.Register(domain.ErrMailDomainInUse, http.StatusConflict, domain.CodeMailDomainInUse)
	mapper.Register(domain.ErrMailDomainNotVerified, http.StatusConflict, domain.CodeMailDomainNotVerified)
	mapper.Register(domain.ErrMailDomainsDisabled, http.StatusServiceUnavailable, domain.CodeMailDomainsDisabled)
	mapper.Register(domain.ErrMailDomainProviderFailed, http.StatusBadGateway, domain.CodeMailDomainProviderFailed)
	mapper.Register(domain.ErrMailZoneNotFound, http.StatusNotFound, domain.CodeMailZoneNotFound)
	mapper.Register(domain.ErrMailZoneLimitReached, http.StatusConflict, domain.CodeMailZoneLimitReached)
	mapper.Register(domain.ErrMailZoneInUse, http.StatusConflict, domain.CodeMailZoneInUse)
	mapper.Register(domain.ErrMailZoneStillDelegated, http.StatusConflict, domain.CodeMailZoneStillDelegated)
	mapper.Register(domain.ErrMailZonePending, http.StatusConflict, domain.CodeMailZonePending)
}
