package triage

import (
	"net/http"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/pkg/response"
)

// RegisterErrorMappings maps triage domain errors to public HTTP status and error codes.
func (h *Handler) RegisterErrorMappings(mapper *response.ErrorMapper) {
	mapper.Register(domain.ErrMailTriageUnavailable, http.StatusConflict, domain.CodeMailTriageUnavailable)
	mapper.Register(domain.ErrMailTriageModelFailed, http.StatusBadGateway, domain.CodeMailTriageModelFailed)
}
