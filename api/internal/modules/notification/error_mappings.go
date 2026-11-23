package notification

import (
	"net/http"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/pkg/response"
)

// RegisterErrorMappings maps this starter's domain errors to public status codes and error codes.
func (h *Handler) RegisterErrorMappings(mapper *response.ErrorMapper) {
	mapper.Register(domain.ErrNotificationNotFound, http.StatusNotFound, domain.CodeNotificationNotFound)
	mapper.Register(domain.ErrNotificationIdempotencyConflict, http.StatusConflict, domain.CodeNotificationIdempotencyConflict)
	mapper.Register(domain.ErrNotificationInvalidChannel, http.StatusUnprocessableEntity, domain.CodeNotificationInvalidChannel)
	mapper.Register(ErrWebPushDisabled, http.StatusServiceUnavailable, CodeWebPushDisabled)
	mapper.Register(ErrWebPushEndpointInvalid, http.StatusUnprocessableEntity, CodeWebPushEndpointInvalid)
	mapper.Register(ErrWebPushKeysInvalid, http.StatusUnprocessableEntity, CodeWebPushKeysInvalid)
	mapper.Register(ErrWebPushSubscriptionNotFound, http.StatusNotFound, CodeWebPushSubscriptionNotFound)
}
