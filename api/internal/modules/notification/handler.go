package notification

import (
	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/starter/assembly"
	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/pagination"
	"github.com/fastagi/zmail/api/pkg/response"
)

// Handler owns the authenticated notification-center HTTP boundary.
type Handler struct {
	service Service
}

var (
	_ assembly.Module      = (*Handler)(nil)
	_ assembly.RouteModule = (*Handler)(nil)
)

// NewHandler creates the notification handler.
func NewHandler(service Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Name() string { return config.StarterNotification }

func (h *Handler) List(c *gin.Context) {
	userID, ok := httphandler.GetUserID(c)
	if !ok {
		return
	}
	status := c.DefaultQuery("status", notificationFilterAll)
	request := pagination.FromContext(c)
	items, total, err := h.service.ListForUser(
		c.Request.Context(),
		userID,
		status,
		request.GetPage(),
		request.GetPerPage(),
	)
	if err != nil {
		response.HandleError(c, "Failed to list notifications", err)
		return
	}

	responses := make([]*NotificationResponse, len(items))
	for index := range items {
		responses[index] = toNotificationResponse(items[index])
	}
	paginator := pagination.NewPaginator(responses, total, request.GetPage(), request.GetPerPage())
	paginator.SetPath(c.Request.URL.Path)
	paginator.Append("status", status)
	response.Success(c, paginator)
}

func (h *Handler) Status(c *gin.Context) {
	userID, ok := httphandler.GetUserID(c)
	if !ok {
		return
	}
	count, err := h.service.UnreadCount(c.Request.Context(), userID)
	if err != nil {
		response.HandleError(c, "Failed to read notification status", err)
		return
	}
	response.Success(c, &NotificationStatusResponse{UnreadCount: count})
}

func (h *Handler) ReplaceReadState(c *gin.Context) {
	userID, ok := httphandler.GetUserID(c)
	if !ok {
		return
	}
	notificationID, ok := httphandler.ParseID(c, "id")
	if !ok {
		return
	}
	var request replaceNotificationReadStateRequest
	if !httphandler.BindJSON(c, &request) {
		return
	}
	notification, err := h.service.ReplaceReadState(
		c.Request.Context(),
		userID,
		notificationID,
		*request.IsRead,
	)
	if err != nil {
		response.HandleError(c, "Failed to update notification read state", err)
		return
	}
	response.Success(c, toNotificationResponse(notification))
}

func (h *Handler) MarkReadThrough(c *gin.Context) {
	userID, ok := httphandler.GetUserID(c)
	if !ok {
		return
	}
	var request replaceNotificationReadStateThroughRequest
	if !httphandler.BindJSON(c, &request) {
		return
	}
	updated, unread, err := h.service.MarkReadThrough(
		c.Request.Context(),
		userID,
		request.ThroughID,
	)
	if err != nil {
		response.HandleError(c, "Failed to update notification read state", err)
		return
	}
	response.Success(c, &NotificationReadStateResponse{
		UpdatedCount: updated,
		UnreadCount:  unread,
	})
}

func (h *Handler) GetPreference(c *gin.Context) {
	userID, ok := httphandler.GetUserID(c)
	if !ok {
		return
	}
	preference, err := h.service.Preference(c.Request.Context(), userID)
	if err != nil {
		response.HandleError(c, "Failed to read notification preferences", err)
		return
	}
	response.Success(c, toNotificationPreferenceResponse(preference))
}

func (h *Handler) ReplacePreference(c *gin.Context) {
	userID, ok := httphandler.GetUserID(c)
	if !ok {
		return
	}
	var request replaceNotificationPreferenceRequest
	if !httphandler.BindJSON(c, &request) {
		return
	}
	preference, err := h.service.ReplacePreference(
		c.Request.Context(),
		userID,
		*request.InAppEnabled,
		*request.EmailEnabled,
		request.WebPushEnabled,
	)
	if err != nil {
		response.HandleError(c, "Failed to update notification preferences", err)
		return
	}
	response.Success(c, toNotificationPreferenceResponse(preference))
}

// GetWebPushConfig tells the browser whether push is available and with which VAPID public key.
func (h *Handler) GetWebPushConfig(c *gin.Context) {
	if _, ok := httphandler.GetUserID(c); !ok {
		return
	}
	cfg := h.service.WebPushConfig()
	response.Success(c, &WebPushConfigResponse{Enabled: cfg.Enabled, PublicKey: cfg.PublicKey})
}

// PutWebPushSubscription registers the caller's browser subscription idempotently by endpoint.
func (h *Handler) PutWebPushSubscription(c *gin.Context) {
	userID, ok := httphandler.GetUserID(c)
	if !ok {
		return
	}
	var request putWebPushSubscriptionRequest
	if !httphandler.BindJSON(c, &request) {
		return
	}
	subscription, err := h.service.RegisterWebPushSubscription(c.Request.Context(), userID, WebPushSubscriptionInput{
		Endpoint:    request.Endpoint,
		P256dh:      request.Keys.P256dh,
		Auth:        request.Keys.Auth,
		DeviceLabel: request.DeviceLabel,
	})
	if err != nil {
		response.HandleError(c, "Failed to register web push subscription", err)
		return
	}
	response.Success(c, toWebPushSubscriptionResponse(subscription))
}

// ListWebPushSubscriptions lists the caller's registered devices without endpoints or keys.
// fastbuild:bounded-list max=20 reason=a user keeps at most maxWebPushSubscriptionsPerUser subscriptions
func (h *Handler) ListWebPushSubscriptions(c *gin.Context) {
	userID, ok := httphandler.GetUserID(c)
	if !ok {
		return
	}
	subscriptions, err := h.service.ListWebPushSubscriptions(c.Request.Context(), userID)
	if err != nil {
		response.HandleError(c, "Failed to list web push subscriptions", err)
		return
	}
	items := make([]*WebPushSubscriptionResponse, len(subscriptions))
	for index := range subscriptions {
		items[index] = toWebPushSubscriptionResponse(subscriptions[index])
	}
	response.Success(c, items)
}

// DeleteWebPushSubscription removes one of the caller's devices.
func (h *Handler) DeleteWebPushSubscription(c *gin.Context) {
	userID, ok := httphandler.GetUserID(c)
	if !ok {
		return
	}
	id, ok := httphandler.ParseID(c, "id")
	if !ok {
		return
	}
	if err := h.service.DeleteWebPushSubscription(c.Request.Context(), userID, id); err != nil {
		response.HandleError(c, "Failed to delete web push subscription", err)
		return
	}
	response.NoContent(c)
}
