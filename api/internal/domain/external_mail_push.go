package domain

import (
	"context"
	"time"
)

// ExternalMailSyncTrigger is why a sync ran: the poll schedule, or a provider notification.
type ExternalMailSyncTrigger string

const (
	ExternalMailSyncTriggerPoll ExternalMailSyncTrigger = "poll"
	ExternalMailSyncTriggerPush ExternalMailSyncTrigger = "push"
)

// ExternalMailSubscriptionStatus is the state of a connection's push subscription.
type ExternalMailSubscriptionStatus string

const (
	// ExternalMailSubscriptionPending has no live subscription at the provider yet: every attempt
	// so far failed, and the next one waits its backoff.
	ExternalMailSubscriptionPending ExternalMailSubscriptionStatus = "pending"
	// ExternalMailSubscriptionActive has a subscription at the provider until ExpiresAt.
	ExternalMailSubscriptionActive ExternalMailSubscriptionStatus = "active"
	// ExternalMailSubscriptionStopped ended with its connection; notifications are ignored.
	ExternalMailSubscriptionStopped ExternalMailSubscriptionStatus = "stopped"
)

// Lifecycle events a provider reports about a subscription (Microsoft Graph's names).
const (
	ExternalMailPushReauthorize = "reauthorizationRequired"
	ExternalMailPushRemoved     = "subscriptionRemoved"
	ExternalMailPushMissed      = "missed"
)

// ExternalMailSubscription is the push subscription of one connection. A notification it matches
// only marks the connection due; it never carries mail.
type ExternalMailSubscription struct {
	ID           uint
	ConnectionID uint
	Provider     ExternalMailProvider
	// LookupKey finds the row from a notification: the lowercased Gmail address, or the Graph
	// subscription id.
	LookupKey string
	// ProviderSubscriptionID is the provider's id of the subscription; empty for Gmail.
	ProviderSubscriptionID string
	// SecretHash is the hex SHA-256 of the secret a notification must echo (the Graph clientState);
	// empty for Gmail, whose notifications are authenticated by a signed token.
	SecretHash string
	Status     ExternalMailSubscriptionStatus
	// ExpiresAt is when the provider ends the subscription; nil before the first success.
	ExpiresAt *time.Time
	// RenewAfter is when the next subscribe or renewal is due.
	RenewAfter time.Time
	// PendingAt is the first notification not yet answered by a sync; nil when none waits.
	PendingAt      *time.Time
	LastNotifiedAt *time.Time
	// LastPushSyncAt is when the latest push-triggered sync started; it spaces the next one.
	LastPushSyncAt *time.Time
	// ScheduledSyncAt is the next_sync_at the latest save of the connection wrote; a notification
	// lowers next_sync_at only while it is still that value, so a claim lease is never shortened.
	ScheduledSyncAt     *time.Time
	ConsecutiveFailures int
	LastErrorCode       string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// Live reports whether the subscription is active and unexpired at now.
func (s *ExternalMailSubscription) Live(now time.Time) bool {
	return s != nil && s.Status == ExternalMailSubscriptionActive && s.ExpiresAt != nil && s.ExpiresAt.After(now)
}

// ExternalMailPushHit is a notification matched to subscription rows of active connections.
type ExternalMailPushHit struct {
	SubscriptionID uint
	ConnectionID   uint
	SecretHash     string
}

// ExternalMailPushMark records one authenticated notification on the rows it names.
type ExternalMailPushMark struct {
	SubscriptionIDs []uint
	Now             time.Time
	// Window is the least time between two push-triggered syncs of one connection.
	Window time.Duration
	// Lifecycle, when set, is the provider's lifecycle event: reauthorization and removal make the
	// renewal due now, and removal also ends the subscription's life.
	Lifecycle string
}

// ExternalMailSubscriptionSave records the outcome of one subscribe or renewal attempt, in the
// transaction that saves the connection's sync.
type ExternalMailSubscriptionSave struct {
	ConnectionID uint
	Provider     ExternalMailProvider
	// Succeeded attempts replace the provider fields; failed ones only record the failure.
	Succeeded              bool
	LookupKey              string
	ProviderSubscriptionID string
	// SecretHash replaces the stored hash when not empty (a newly created Graph subscription).
	SecretHash          string
	ExpiresAt           time.Time
	RenewAfter          time.Time
	ConsecutiveFailures int
	LastErrorCode       string
	Now                 time.Time
}

// ExternalMailSubscriptionSynced records a saved sync on the connection's subscription.
type ExternalMailSubscriptionSynced struct {
	ConnectionID uint
	// FetchStartedAt answers every notification received before it.
	FetchStartedAt time.Time
	// Pushed marks a sync that answered a notification.
	Pushed bool
	Now    time.Time
}

// ExternalMailPushRepository keeps push subscriptions.
type ExternalMailPushRepository interface {
	// FindExternalMailSubscription returns the connection's subscription, or nil.
	FindExternalMailSubscription(ctx context.Context, connectionID uint) (*ExternalMailSubscription, error)
	// ListExternalMailSubscriptions returns the subscriptions of the connections, keyed by connection.
	ListExternalMailSubscriptions(ctx context.Context, connectionIDs []uint) (map[uint]*ExternalMailSubscription, error)
	// MatchExternalMailSubscriptions returns at most limit rows of the provider and key that are
	// not stopped and whose connection is active.
	MatchExternalMailSubscriptions(ctx context.Context, provider ExternalMailProvider, lookupKey string, limit int) ([]ExternalMailPushHit, error)
	// MarkExternalMailPush records a notification on the rows and marks their connections due as
	// the mark allows; it returns how many connections it made due sooner.
	MarkExternalMailPush(ctx context.Context, mark ExternalMailPushMark) (int, error)
	// PromotePendingExternalMailPush marks due up to limit connections whose notification arrived
	// while they were being fetched.
	PromotePendingExternalMailPush(ctx context.Context, now time.Time, window time.Duration, limit int) (int, error)
	// SaveExternalMailSubscription records a subscribe or renewal attempt.
	SaveExternalMailSubscription(ctx context.Context, save ExternalMailSubscriptionSave) error
	// RecordExternalMailPushSync records a saved sync on the subscription, when there is one: it
	// answers the notifications received before the fetch and remembers the next_sync_at the save
	// wrote. Call it in the transaction that saved the sync.
	RecordExternalMailPushSync(ctx context.Context, synced ExternalMailSubscriptionSynced) error
	// StopExternalMailSubscription stops the connection's subscription and returns it as it was;
	// nil when it had none or it was already stopped.
	StopExternalMailSubscription(ctx context.Context, connectionID uint, now time.Time) (*ExternalMailSubscription, error)
	// StopOrphanedExternalMailSubscriptions stops up to limit subscriptions whose connection is not
	// active and returns how many.
	StopOrphanedExternalMailSubscriptions(ctx context.Context, now time.Time, limit int) (int, error)
	// CountLiveExternalMailWatchers counts subscriptions of the provider and key other than the
	// connection's that are not stopped.
	CountLiveExternalMailWatchers(ctx context.Context, provider ExternalMailProvider, lookupKey string, exceptConnectionID uint) (int, error)
}
