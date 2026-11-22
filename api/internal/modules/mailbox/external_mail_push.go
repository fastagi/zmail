package mailbox

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/metrics"
	"github.com/fastagi/zmail/api/internal/infra/ratelimit"
)

// Push subscriptions only ever mark a connection due: a notification never carries mail, and the
// regular sync reads it with the connection's own grant, cursor, floor, backoff, and budget.

// Bounds of the notification routes and of the subscription upkeep.
const (
	// maxExternalMailPushHints bounds the notifications one request is read for; the rest are
	// dropped, and the poll picks up whatever they announced.
	maxExternalMailPushHints = 20
	// maxExternalMailPushMatches bounds the connections one notification may name: one Gmail account
	// bound by several connections.
	maxExternalMailPushMatches = 10
	// maxExternalMailPushBodyBytes bounds a notification request body.
	maxExternalMailPushBodyBytes = 64 << 10
	// externalMailPushReconcileBatch bounds the subscriptions of ended connections stopped per pass,
	// externalMailPushPromoteBatch the notifications promoted per pass.
	externalMailPushReconcileBatch = 100
	externalMailPushPromoteBatch   = 200
	// externalMailPushCallTimeout bounds one subscribe, renewal, or stop at the provider.
	externalMailPushCallTimeout = 20 * time.Second
	// minExternalMailPushLife is the least life a provider must grant a new subscription.
	minExternalMailPushLife = 5 * time.Minute
	// ruleExternalMailPush names the per-provider notification rate limit.
	ruleExternalMailPush = "external_mail_push"
)

var (
	// ErrExternalMailPushUnauthenticated means a notification did not prove it came from the
	// provider; it is answered 401 and changes nothing.
	ErrExternalMailPushUnauthenticated = errors.New("external mail notification is not authenticated")
	// ErrExternalMailPushRateLimited means the provider's notification budget is used up.
	ErrExternalMailPushRateLimited = errors.New("external mail notifications are rate limited")
)

// ExternalMailPushRequest is what a source needs to start, renew, or stop one subscription.
type ExternalMailPushRequest struct {
	Token          ExternalMailToken
	AccountAddress string
	// SubscriptionID is the stored provider id of the subscription; empty creates one.
	SubscriptionID string
	// Secret is a fresh random secret a newly created subscription carries (Graph's clientState).
	Secret string
	Now    time.Time
}

// ExternalMailPushSubscription is a subscription the provider accepted.
type ExternalMailPushSubscription struct {
	// LookupKey is what the provider's notifications will name.
	LookupKey      string
	SubscriptionID string
	// Created means a new subscription carrying the request's secret exists; false renewed the stored one.
	Created   bool
	ExpiresAt time.Time
	// RefreshToken is set when the provider rotated the grant during the call.
	RefreshToken string
}

// ExternalMailPushDelivery is one notification request as it reached Zmail.
type ExternalMailPushDelivery struct {
	Header http.Header
	Query  url.Values
	Body   []byte
	Now    time.Time
}

// ExternalMailPushHint is one notification: which subscription it names and how it proves itself.
type ExternalMailPushHint struct {
	Key string
	// Secret is what the notification echoes (Graph's clientState); it must hash to the stored one
	// unless Authenticated.
	Secret string
	// Authenticated means the source proved the request itself (a verified Google-signed token).
	Authenticated bool
	// Lifecycle is the provider's lifecycle event, if any.
	Lifecycle string
}

// ExternalMailPushParsed is what a notification request said: hints, or a validation handshake
// whose token is echoed.
type ExternalMailPushParsed struct {
	Hints      []ExternalMailPushHint
	Validation string
}

// ExternalMailPushSource is the optional push capability of a connector's source.
type ExternalMailPushSource interface {
	// Subscribe creates the subscription, or renews the stored one, and reports when it ends.
	Subscribe(ctx context.Context, request ExternalMailPushRequest) (ExternalMailPushSubscription, error)
	// Unsubscribe stops the subscription at the provider; one the provider no longer knows is stopped.
	Unsubscribe(ctx context.Context, request ExternalMailPushRequest) error
	// ParseNotification authenticates and reads one request. A request that fails the provider's
	// proof is ErrExternalMailPushUnauthenticated; a malformed one wraps domain.ErrInvalidInput.
	ParseNotification(ctx context.Context, delivery ExternalMailPushDelivery) (ExternalMailPushParsed, error)
}

// newExternalMailPushLimit is one fixed window per provider for notification requests.
func newExternalMailPushLimit(stores *ratelimit.Stores, perMinute int) ratelimit.Store {
	if stores == nil {
		stores = ratelimit.NewMemoryStores()
	}
	return stores.New(ratelimit.Rule{Name: ruleExternalMailPush, Max: perMinute, Window: time.Minute, MaxBuckets: 16})
}

// pushSource returns the connector's push capability while push is on; nil otherwise.
func (s *service) pushSource(connector ExternalMailConnector) ExternalMailPushSource {
	if !s.pushOn() {
		return nil
	}
	return connector.Push
}

// pushOn reports whether push subscriptions are on. While off, nothing reads or writes them: the
// routes are inert, no subscription is created, and the rows left behind are ignored until push
// is on again, when the worker stops those of ended connections.
func (s *service) pushOn() bool {
	return s.pushStore != nil && s.settings.ExternalMail.Push.Enabled
}

// pushHash is the stored form of a notification secret.
func pushHash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// newPushSecret is a random notification secret.
func newPushSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// ExternalMailPushReply is how a notification route answers.
type ExternalMailPushReply struct {
	// Validation is the handshake token to echo; empty for a notification.
	Validation string
}

// ReceiveExternalMailPush handles one notification request of the provider. It answers the same for
// a known and an unknown subscription, so nothing reveals one, and it does a bounded amount of work.
func (s *service) ReceiveExternalMailPush(ctx context.Context, provider domain.ExternalMailProvider, delivery ExternalMailPushDelivery) (ExternalMailPushReply, error) {
	connector, registered := s.externalConnectors.lookup(provider)
	source := s.pushSource(connector)
	if !registered || source == nil || !s.externalMailAvailable() {
		return ExternalMailPushReply{}, ErrExternalMailProviderUnavailable
	}
	if s.pushLimits != nil {
		if allowed, _, resetAt := s.pushLimits.Take(ctx, "provider:"+string(provider)); !allowed {
			metrics.RecordExternalMailPushNotification(string(provider), "rate_limited")
			return ExternalMailPushReply{}, &RetryAfterError{Err: ErrExternalMailPushRateLimited, RetryAfter: time.Until(resetAt)}
		}
	}
	now := s.now().UTC()
	delivery.Now = now
	parsed, err := source.ParseNotification(ctx, delivery)
	if err != nil {
		result := "malformed"
		if errors.Is(err, ErrExternalMailPushUnauthenticated) {
			result = "unauthenticated"
		}
		metrics.RecordExternalMailPushNotification(string(provider), result)
		slog.WarnContext(ctx, "external_mail.push_refused", slog.String("provider", string(provider)), slog.String("result", result))
		return ExternalMailPushReply{}, err
	}
	if parsed.Validation != "" {
		metrics.RecordExternalMailPushNotification(string(provider), "validation")
		return ExternalMailPushReply{Validation: parsed.Validation}, nil
	}
	window := s.settings.ExternalMail.Push.Coalesce()
	accepted, promoted := 0, 0
	for i, hint := range parsed.Hints {
		if i >= maxExternalMailPushHints {
			break
		}
		ids, matchErr := s.matchPushHint(ctx, provider, hint)
		if matchErr != nil {
			return ExternalMailPushReply{}, matchErr
		}
		if len(ids) == 0 {
			continue
		}
		accepted += len(ids)
		made, markErr := s.pushStore.MarkExternalMailPush(ctx, domain.ExternalMailPushMark{
			SubscriptionIDs: ids, Now: now, Window: window, Lifecycle: hint.Lifecycle,
		})
		if markErr != nil {
			return ExternalMailPushReply{}, markErr
		}
		promoted += made
	}
	result := "ignored"
	if accepted > 0 {
		result = "accepted"
	}
	metrics.RecordExternalMailPushNotification(string(provider), result)
	slog.InfoContext(ctx, "external_mail.push_received", slog.String("provider", string(provider)),
		slog.Int("hints", min(len(parsed.Hints), maxExternalMailPushHints)), slog.Int("matched", accepted), slog.Int("made_due", promoted))
	return ExternalMailPushReply{}, nil
}

// matchPushHint returns the subscriptions a notification may mark: those of its provider and key
// whose connection is active and, unless the source authenticated the request itself, whose stored
// secret hash the notification's secret matches in constant time.
func (s *service) matchPushHint(ctx context.Context, provider domain.ExternalMailProvider, hint ExternalMailPushHint) ([]uint, error) {
	hits, err := s.pushStore.MatchExternalMailSubscriptions(ctx, provider, hint.Key, maxExternalMailPushMatches)
	if err != nil {
		return nil, err
	}
	presented := pushHash(hint.Secret)
	ids := make([]uint, 0, len(hits))
	for _, hit := range hits {
		if hint.Authenticated {
			ids = append(ids, hit.SubscriptionID)
			continue
		}
		if hit.SecretHash != "" && hint.Secret != "" && subtle.ConstantTimeCompare([]byte(presented), []byte(hit.SecretHash)) == 1 {
			ids = append(ids, hit.SubscriptionID)
		}
	}
	return ids, nil
}

// maintainExternalMailPush runs at the start of each sync pass: it stops the subscriptions of
// connections that are no longer active and marks due the connections whose notification arrived
// while they were being fetched. A failure is logged; the pass goes on.
func (s *service) maintainExternalMailPush(ctx context.Context, now time.Time) {
	if !s.pushOn() {
		return
	}
	if stopped, err := s.pushStore.StopOrphanedExternalMailSubscriptions(ctx, now, externalMailPushReconcileBatch); err != nil {
		slog.WarnContext(ctx, "external_mail.push_reconcile_failed", slog.String("error", err.Error()))
	} else if stopped > 0 {
		slog.InfoContext(ctx, "external_mail.push_stopped", slog.Int("subscriptions", stopped), slog.String("reason", "connection_inactive"))
	}
	if _, err := s.pushStore.PromotePendingExternalMailPush(ctx, now, s.settings.ExternalMail.Push.Coalesce(), externalMailPushPromoteBatch); err != nil {
		slog.WarnContext(ctx, "external_mail.push_promote_failed", slog.String("error", err.Error()))
	}
}

// pushUpkeep is what one sync did to the connection's subscription.
type pushUpkeep struct {
	save *domain.ExternalMailSubscriptionSave
	// live and renewAt describe the subscription after the sync, for the next poll.
	live      bool
	expiresAt time.Time
	renewAt   time.Time
}

// subscriptionDue reports whether the connection needs a subscribe or renewal now.
func subscriptionDue(subscription *domain.ExternalMailSubscription, now time.Time) bool {
	return subscription == nil || subscription.Status == domain.ExternalMailSubscriptionStopped || !subscription.RenewAfter.After(now)
}

// upkeepPush subscribes or renews the connection's subscription when due, with the token the fetch
// used, while the caller holds the connection's lease. A rotated grant is returned for the sync to
// save. A failure backs off and never touches the connection's status.
func (s *service) upkeepPush(ctx context.Context, source ExternalMailPushSource, connection *domain.ExternalMailConnection,
	subscription *domain.ExternalMailSubscription, token ExternalMailToken, now time.Time) (pushUpkeep, string) {
	upkeep := pushUpkeep{}
	if subscription.Live(now) {
		upkeep.live, upkeep.expiresAt, upkeep.renewAt = true, *subscription.ExpiresAt, subscription.RenewAfter
	}
	if !subscriptionDue(subscription, now) {
		return upkeep, ""
	}
	request := ExternalMailPushRequest{Token: token, AccountAddress: connection.AccountAddress, Now: now}
	if subscription != nil && subscription.Status != domain.ExternalMailSubscriptionStopped {
		request.SubscriptionID = subscription.ProviderSubscriptionID
	}
	secret, err := newPushSecret()
	var got ExternalMailPushSubscription
	if err == nil {
		request.Secret = secret
		callCtx, cancel := context.WithTimeout(ctx, externalMailPushCallTimeout)
		got, err = source.Subscribe(callCtx, request)
		cancel()
	}
	if err == nil && (got.LookupKey == "" || got.ExpiresAt.Before(now.Add(minExternalMailPushLife))) {
		err = errors.New("external mail push: the provider granted no usable subscription")
	}
	if err == nil {
		save := &domain.ExternalMailSubscriptionSave{
			ConnectionID: connection.ID, Provider: connection.Provider, Succeeded: true,
			LookupKey: got.LookupKey, ProviderSubscriptionID: got.SubscriptionID, ExpiresAt: got.ExpiresAt,
			RenewAfter: now.Add(got.ExpiresAt.Sub(now) / 2), Now: now,
		}
		if got.Created && got.SubscriptionID != "" {
			save.SecretHash = pushHash(secret)
		}
		upkeep.save, upkeep.live, upkeep.expiresAt, upkeep.renewAt = save, true, got.ExpiresAt, save.RenewAfter
		slog.InfoContext(ctx, "external_mail.push_subscribed", slog.Uint64("connection_id", uint64(connection.ID)),
			slog.String("provider", string(connection.Provider)), slog.Bool("created", got.Created), slog.Time("expires_at", got.ExpiresAt))
		return upkeep, got.RefreshToken
	}
	failures := 1
	if subscription != nil {
		failures = min(subscription.ConsecutiveFailures+1, maxConsecutiveFailures)
	}
	code := domain.ExternalMailErrorFetchFailed
	if errors.Is(err, ErrExternalMailAuth) {
		code = domain.ExternalMailErrorAuthFailed
	}
	retry := now.Add(externalMailBackoff(failures, externalMailJitter))
	upkeep.save = &domain.ExternalMailSubscriptionSave{
		ConnectionID: connection.ID, Provider: connection.Provider, RenewAfter: retry,
		ConsecutiveFailures: failures, LastErrorCode: code, Now: now,
	}
	upkeep.renewAt = retry
	// The error names the operation and the provider's status only; it never carries a token.
	slog.WarnContext(ctx, "external_mail.push_subscribe_failed", slog.Uint64("connection_id", uint64(connection.ID)),
		slog.String("provider", string(connection.Provider)), slog.String("error_code", code),
		slog.Int("consecutive_failures", failures), slog.Time("retry_at", retry))
	return upkeep, got.RefreshToken
}

// pushPollAt is when a connection that synced fully at now is polled next: after the push poll
// interval while its subscription is live, never past its renewal or expiry; otherwise at the
// regular interval.
func (s *service) pushPollAt(upkeep pushUpkeep, now time.Time) time.Time {
	regular := now.Add(externalMailSyncInterval)
	if !upkeep.live || !upkeep.expiresAt.After(regular) {
		return regular
	}
	next := now.Add(s.settings.ExternalMail.Push.Poll())
	next = minTime(next, upkeep.expiresAt)
	if !upkeep.renewAt.IsZero() {
		next = minTime(next, maxTime(upkeep.renewAt, regular))
	}
	return maxTime(next, regular)
}

// minTime returns the earlier of two times.
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// stopPushAtProvider stops a connection's subscription at its provider with the grant still at hand,
// before the grant is handed back. It is best effort: a provider that cannot be reached leaves the
// subscription to lapse at its expiry, and the row is stopped in the revoke transaction either way.
// A Gmail watch other live connections of the same account share is left running.
func (s *service) stopPushAtProvider(ctx context.Context, connection *domain.ExternalMailConnection) {
	if !s.pushOn() || s.encryptor == nil {
		return
	}
	subscription, err := s.pushStore.FindExternalMailSubscription(ctx, connection.ID)
	if err != nil || subscription == nil || subscription.Status != domain.ExternalMailSubscriptionActive {
		return
	}
	connector, registered := s.externalConnectors.lookup(connection.Provider)
	if !registered || connector.Push == nil {
		return
	}
	if others, countErr := s.pushStore.CountLiveExternalMailWatchers(ctx, connection.Provider, subscription.LookupKey, connection.ID); countErr != nil || others > 0 {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, externalMailPushCallTimeout)
	defer cancel()
	token, _, err := s.connectionToken(callCtx, connection, connector)
	if err == nil {
		err = connector.Push.Unsubscribe(callCtx, ExternalMailPushRequest{
			Token: token, AccountAddress: connection.AccountAddress, SubscriptionID: subscription.ProviderSubscriptionID, Now: s.now().UTC(),
		})
	}
	if err != nil {
		slog.WarnContext(ctx, "external_mail.push_unsubscribe_failed", slog.Uint64("connection_id", uint64(connection.ID)),
			slog.String("provider", string(connection.Provider)))
		return
	}
	slog.InfoContext(ctx, "external_mail.push_unsubscribed", slog.Uint64("connection_id", uint64(connection.ID)),
		slog.String("provider", string(connection.Provider)))
}

// stopPushRow stops the connection's subscription row in the caller's transaction.
func (s *service) stopPushRow(tx context.Context, connectionID uint) error {
	if !s.pushOn() {
		return nil
	}
	_, err := s.pushStore.StopExternalMailSubscription(tx, connectionID, s.now().UTC())
	return err
}

// attachPushState fills in the push subscription of each listed connection while push is on.
func (s *service) attachPushState(ctx context.Context, connections []*domain.ExternalMailConnection) error {
	if !s.pushOn() || len(connections) == 0 {
		return nil
	}
	ids := make([]uint, 0, len(connections))
	for _, connection := range connections {
		ids = append(ids, connection.ID)
	}
	subscriptions, err := s.pushStore.ListExternalMailSubscriptions(ctx, ids)
	if err != nil {
		return fmt.Errorf("list push subscriptions: %w", err)
	}
	for _, connection := range connections {
		connection.Push = subscriptions[connection.ID]
	}
	return nil
}

// normalizedPushKey is a Gmail notification's address as it is stored: trimmed and lowercased.
func normalizedPushKey(address string) string {
	return strings.ToLower(strings.TrimSpace(address))
}

// savePushUpkeep records, in the sync's transaction, what the sync did to the connection's
// subscription: the subscribe or renewal attempt, and for a successful sync the notifications it
// answered and the next_sync_at it scheduled. Nothing is recorded for a connection that stopped
// being active meanwhile.
func (s *service) savePushUpkeep(tx context.Context, connectionID uint, applied, succeeded bool, upkeep pushUpkeep,
	trigger domain.ExternalMailSyncTrigger, startedAt time.Time) error {
	if !s.pushOn() || !applied {
		return nil
	}
	if upkeep.save != nil {
		if err := s.pushStore.SaveExternalMailSubscription(tx, *upkeep.save); err != nil {
			return err
		}
	}
	if !succeeded {
		return nil
	}
	return s.pushStore.RecordExternalMailPushSync(tx, domain.ExternalMailSubscriptionSynced{
		ConnectionID: connectionID, FetchStartedAt: startedAt, Pushed: trigger == domain.ExternalMailSyncTriggerPush, Now: s.now().UTC(),
	})
}
