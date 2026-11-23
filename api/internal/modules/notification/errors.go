package notification

import "errors"

// Web push errors owned by the notification starter. Their codes stay inside the module, so the
// browser shells need no catalog change to receive them.
var (
	// ErrWebPushDisabled means the instance has no valid VAPID configuration.
	ErrWebPushDisabled = errors.New("web push is not configured on this instance")
	// ErrWebPushEndpointInvalid rejects an endpoint that is not https on a known browser push service.
	ErrWebPushEndpointInvalid = errors.New("web push endpoint is not an allowed push service")
	// ErrWebPushKeysInvalid rejects a p256dh that is not an uncompressed P-256 point or an auth
	// secret that is not 16 bytes.
	ErrWebPushKeysInvalid = errors.New("web push subscription keys are invalid")
	// ErrWebPushSubscriptionNotFound hides subscriptions of other users exactly like missing ones.
	ErrWebPushSubscriptionNotFound = errors.New("web push subscription not found")
)

// Public error codes of the web push boundary.
const (
	CodeWebPushDisabled             = "NOTIFICATION.WEB_PUSH_DISABLED"
	CodeWebPushEndpointInvalid      = "NOTIFICATION.WEB_PUSH_ENDPOINT_INVALID"
	CodeWebPushKeysInvalid          = "NOTIFICATION.WEB_PUSH_KEYS_INVALID"
	CodeWebPushSubscriptionNotFound = "NOTIFICATION.WEB_PUSH_SUBSCRIPTION_NOT_FOUND"
)
