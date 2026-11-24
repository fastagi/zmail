package roster

import (
	"net/http"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/pkg/response"
)

// RegisterErrorMappings maps roster domain errors to their public HTTP status and error codes.
func (h *Handler) RegisterErrorMappings(mapper *response.ErrorMapper) {
	mapper.Register(domain.ErrMailContactNotFound, http.StatusNotFound, domain.CodeMailContactNotFound)
	mapper.Register(domain.ErrMailContactExists, http.StatusConflict, domain.CodeMailContactExists)
	mapper.Register(domain.ErrMailContactInvalid, http.StatusUnprocessableEntity, domain.CodeMailContactInvalid)
	mapper.Register(domain.ErrMailContactLimitReached, http.StatusConflict, domain.CodeMailContactLimitReached)
	mapper.Register(domain.ErrMailContactListNotFound, http.StatusNotFound, domain.CodeMailContactListNotFound)
	mapper.Register(domain.ErrMailContactListNameTaken, http.StatusConflict, domain.CodeMailContactListNameTaken)
	mapper.Register(domain.ErrMailContactListLimit, http.StatusConflict, domain.CodeMailContactListLimitReached)
	mapper.Register(domain.ErrMailContactImportInvalid, http.StatusUnprocessableEntity, domain.CodeMailContactImportInvalid)
}
