package notification

import (
	"errors"

	"github.com/fastagi/zmail/api/internal/domain"
)

// Web push errors of the notification starter; their codes live in the shared domain catalog
// because the browser shells handle them.
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
	CodeWebPushDisabled             = domain.CodeNotificationWebPushDisabled
	CodeWebPushEndpointInvalid      = domain.CodeNotificationWebPushEndpointInvalid
	CodeWebPushKeysInvalid          = domain.CodeNotificationWebPushKeysInvalid
	CodeWebPushSubscriptionNotFound = domain.CodeNotificationWebPushSubscriptionNotFound
)
