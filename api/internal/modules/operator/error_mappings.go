package operator

import (
	"net/http"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/pkg/response"
)

// RegisterErrorMappings maps this starter's domain errors for callers outside its own handlers.
func (h *Handler) RegisterErrorMappings(mapper *response.ErrorMapper) {
	mapper.Register(domain.ErrOperatorForbidden, http.StatusForbidden, domain.CodeOperatorForbidden)
	mapper.Register(domain.ErrOperatorCapability, http.StatusForbidden, domain.CodeOperatorCapability)
	mapper.Register(domain.ErrOperatorContentNotEligible, http.StatusForbidden, domain.CodeOperatorContentNotEligible)
	mapper.Register(domain.ErrOperatorTargetProtected, http.StatusConflict, domain.CodeOperatorTargetProtected)
}
