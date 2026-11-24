package agentgrant

import (
	"net/http"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/pkg/response"
)

// RegisterErrorMappings maps agentgrant domain errors to public HTTP status and error codes.
func (h *Handler) RegisterErrorMappings(mapper *response.ErrorMapper) {
	mapper.Register(domain.ErrMailAgentGrantNotFound, http.StatusNotFound, domain.CodeMailAgentGrantNotFound)
	mapper.Register(domain.ErrMailAgentGrantInvalid, http.StatusUnprocessableEntity, domain.CodeMailAgentGrantInvalid)
	mapper.Register(domain.ErrMailAgentGrantLimit, http.StatusConflict, domain.CodeMailAgentGrantLimit)
	mapper.Register(domain.ErrMailAgentGrantExists, http.StatusConflict, domain.CodeMailAgentGrantExists)
	mapper.Register(domain.ErrMailAgentRateLimited, http.StatusTooManyRequests, domain.CodeRateLimited)
}
