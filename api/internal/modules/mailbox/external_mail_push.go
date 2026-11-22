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
	"sync"
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
	// maxExternalMailPushRefusalsPerMinute is how many requests that authenticate nothing one IP may
	// send per minute; the budget is kept in this process only, so junk never writes to the database.
	maxExternalMailPushRefusalsPerMinute = 120
	maxExternalMailPushRefusalIPs        = 10000
	// maxExternalMailPushHandshakesPerMinute is a sender's own budget of Graph validation handshakes,
	// apart from its junk, so a flood of junk never stops a new subscription from being created.
	maxExternalMailPushHandshakesPerMinute = 30
	// externalMailPushRefusalLogEvery is how often a refused-request line is logged per provider and
	// reason at most; the metric counts every one.
	externalMailPushRefusalLogEvery = time.Minute
	// externalMailPushUnsupportedRecheck is how long a connection whose provider cannot push for it
	// waits before it is asked again.
	externalMailPushUnsupportedRecheck = 24 * time.Hour
	// externalMailPushDegradeGrace is how long a notification may take: a poll only counts mail
	// received earlier than this before it, and later than this after the subscription went live.
	externalMailPushDegradeGrace = 5 * time.Minute
	// externalMailReleaseParallelism bounds the provider calls made at once after a revocation.
	externalMailReleaseParallelism = 4
)

var (
	// ErrExternalMailPushUnauthenticated means a notification did not prove it came from the
	// provider; it is answered 401 and changes nothing.
	ErrExternalMailPushUnauthenticated = errors.New("external mail notification is not authenticated")
	// ErrExternalMailPushRateLimited means the provider's notification budget, or the sender's, is used up.
	ErrExternalMailPushRateLimited = errors.New("external mail notifications are rate limited")
	// ErrExternalMailPushUnsupported means the provider cannot deliver push for this connection.
	ErrExternalMailPushUnsupported = errors.New("external mail push is not supported for this connection")
)

// ExternalMailPushRequest is what a source needs to start, renew, or stop one subscription.
type ExternalMailPushRequest struct {
	Token          ExternalMailToken
	AccountAddress string
	// SubscriptionID is the stored provider id of the subscription; empty creates one.
	SubscriptionID string
	// Secret is a fresh random secret a newly created subscription carries (Graph's clientState).
	Secret string
	// TenantMailbox marks a mailbox assigned from a tenant, read with the tenant's token.
	TenantMailbox bool
	Now           time.Time
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
	// ClientIP is the sender's address as the trusted proxy settings resolve it.
	ClientIP string
	Now      time.Time
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
	// Authenticated means the source proved the request itself, even when it names no hint.
	Authenticated bool
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

// newExternalMailPushHandshakes is the per-IP budget of validation handshakes, process-local too.
func newExternalMailPushHandshakes() ratelimit.Store {
	return ratelimit.NewMemoryStore(maxExternalMailPushHandshakesPerMinute, time.Minute, ratelimit.WithMaxBuckets(maxExternalMailPushRefusalIPs))
}

// pushRefusalLog lets through at most one refused-request log line per provider and reason a minute.
type pushRefusalLog struct {
	mu   sync.Mutex
	last map[string]time.Time
}

// allow reports whether a line for the key may be logged at now, and records it.
func (l *pushRefusalLog) allow(key string, now time.Time) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.last == nil {
		l.last = map[string]time.Time{}
	}
	if last, seen := l.last[key]; seen && now.Sub(last) < externalMailPushRefusalLogEvery {
		return false
	}
	l.last[key] = now
	return true
}

// newExternalMailPushRefusals is the per-IP budget of requests that authenticate nothing. It is
// always process-local, whatever RATE_LIMIT_STORE says, so junk never writes to the database.
func newExternalMailPushRefusals() ratelimit.Store {
	return ratelimit.NewMemoryStore(maxExternalMailPushRefusalsPerMinute, time.Minute, ratelimit.WithMaxBuckets(maxExternalMailPushRefusalIPs))
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
// a known and an unknown subscription, so nothing reveals one, and it does a bounded amount of work:
// at most one key lookup for the whole request. Only an authenticated notification spends the
// provider's budget; a request that proves nothing spends the per-IP budget of its sender, which is
// kept in this process only, so junk never writes to the database and never starves the provider.
func (s *service) ReceiveExternalMailPush(ctx context.Context, provider domain.ExternalMailProvider, delivery ExternalMailPushDelivery) (ExternalMailPushReply, error) {
	connector, registered := s.externalConnectors.lookup(provider)
	source := s.pushSource(connector)
	if !registered || source == nil || !s.externalMailAvailable() {
		return ExternalMailPushReply{}, ErrExternalMailProviderUnavailable
	}
	now := s.now().UTC()
	delivery.Now = now
	parsed, err := source.ParseNotification(ctx, delivery)
	if err != nil {
		result := "malformed"
		if errors.Is(err, ErrExternalMailPushUnauthenticated) {
			result = "unauthenticated"
		}
		return ExternalMailPushReply{}, s.refusePush(ctx, provider, delivery.ClientIP, result, err)
	}
	if parsed.Validation != "" {
		// A handshake proves nothing about its sender; it is cheap, but it spends the sender's budget.
		if refusal := s.refusePush(ctx, provider, delivery.ClientIP, "validation", nil); refusal != nil {
			return ExternalMailPushReply{}, refusal
		}
		return ExternalMailPushReply{Validation: parsed.Validation}, nil
	}
	hints := parsed.Hints
	if len(hints) > maxExternalMailPushHints {
		hints = hints[:maxExternalMailPushHints]
	}
	matched, err := s.matchPushHints(ctx, provider, hints)
	if err != nil {
		return ExternalMailPushReply{}, err
	}
	if len(matched) == 0 {
		slog.InfoContext(ctx, "external_mail.push_received", slog.String("provider", string(provider)),
			slog.Int("hints", len(hints)), slog.Int("matched", 0))
		if parsed.Authenticated {
			// The provider itself spoke (a verified token) about an account Zmail no longer follows.
			metrics.RecordExternalMailPushNotification(string(provider), "ignored")
			return ExternalMailPushReply{}, nil
		}
		return ExternalMailPushReply{}, s.refusePush(ctx, provider, delivery.ClientIP, "ignored", nil)
	}
	if s.pushLimits != nil {
		if allowed, _, resetAt := s.pushLimits.Take(ctx, "provider:"+string(provider)); !allowed {
			metrics.RecordExternalMailPushNotification(string(provider), "rate_limited")
			return ExternalMailPushReply{}, &RetryAfterError{Err: ErrExternalMailPushRateLimited, RetryAfter: time.Until(resetAt)}
		}
	}
	push := s.settings.ExternalMail.Push
	accepted, promoted := 0, 0
	for _, group := range matched {
		made, markErr := s.pushStore.MarkExternalMailPush(ctx, domain.ExternalMailPushMark{
			SubscriptionIDs: group.ids, Now: now, Window: push.Coalesce(), Spacing: s.settings.ExternalMail.Sync.Spacing(),
			Lifecycle: group.lifecycle,
		})
		if markErr != nil {
			return ExternalMailPushReply{}, markErr
		}
		accepted += len(group.ids)
		promoted += made
	}
	metrics.RecordExternalMailPushNotification(string(provider), "accepted")
	slog.InfoContext(ctx, "external_mail.push_received", slog.String("provider", string(provider)),
		slog.Int("hints", len(hints)), slog.Int("matched", accepted), slog.Int("made_due", promoted))
	return ExternalMailPushReply{}, nil
}

// refusePush spends one unit of the sender's per-IP budget for a request that authenticated nothing:
// a validation handshake its own small budget, anything else the junk budget. Within the budget it
// answers err (nil for an answer like any other); past it, 429, without a log line. Refusals are
// logged at most once a minute per provider and reason; the metric counts each.
func (s *service) refusePush(ctx context.Context, provider domain.ExternalMailProvider, clientIP, result string, err error) error {
	budget := s.pushRefusals
	if result == "validation" {
		budget = s.pushHandshakes
	}
	if budget != nil {
		if allowed, _, resetAt := budget.Take(ctx, "ip:"+clientIP); !allowed {
			metrics.RecordExternalMailPushNotification(string(provider), "rate_limited")
			return &RetryAfterError{Err: ErrExternalMailPushRateLimited, RetryAfter: time.Until(resetAt)}
		}
	}
	metrics.RecordExternalMailPushNotification(string(provider), result)
	if result != "validation" && s.pushRefusalLog.allow(string(provider)+":"+result, s.now()) {
		slog.WarnContext(ctx, "external_mail.push_refused", slog.String("provider", string(provider)), slog.String("result", result),
			slog.String("note", "logged at most once a minute; see zmail_external_mail_push_notifications_total"))
	}
	return err
}

// pushMatch is the subscriptions one notification may mark, with its lifecycle event.
type pushMatch struct {
	ids       []uint
	lifecycle string
}

// matchPushHints looks up every hint's key in one query and keeps, per hint, the subscriptions it
// may mark: all of its key's when the source authenticated the request itself, otherwise those
// whose stored secret hash the hint's secret matches, compared in constant time.
func (s *service) matchPushHints(ctx context.Context, provider domain.ExternalMailProvider, hints []ExternalMailPushHint) ([]pushMatch, error) {
	keys := make([]string, 0, len(hints))
	for _, hint := range hints {
		keys = append(keys, hint.Key)
	}
	hits, err := s.pushStore.MatchExternalMailSubscriptions(ctx, provider, keys, maxExternalMailPushHints*maxExternalMailPushMatches)
	if err != nil {
		return nil, err
	}
	byKey := map[string][]domain.ExternalMailPushHit{}
	for _, hit := range hits {
		if len(byKey[hit.LookupKey]) < maxExternalMailPushMatches {
			byKey[hit.LookupKey] = append(byKey[hit.LookupKey], hit)
		}
	}
	var matched []pushMatch
	for _, hint := range hints {
		presented := pushHash(hint.Secret)
		var ids []uint
		for _, hit := range byKey[hint.Key] {
			if hint.Authenticated ||
				(hit.SecretHash != "" && hint.Secret != "" && subtle.ConstantTimeCompare([]byte(presented), []byte(hit.SecretHash)) == 1) {
				ids = append(ids, hit.SubscriptionID)
			}
		}
		if len(ids) > 0 {
			matched = append(matched, pushMatch{ids: ids, lifecycle: hint.Lifecycle})
		}
	}
	return matched, nil
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
	if _, err := s.pushStore.PromotePendingExternalMailPush(ctx, now, s.settings.ExternalMail.Push.Coalesce(),
		s.settings.ExternalMail.Sync.Spacing(), externalMailPushPromoteBatch); err != nil {
		slog.WarnContext(ctx, "external_mail.push_promote_failed", slog.String("error", err.Error()))
	}
}

// pushUpkeep is what one sync did to the connection's subscription.
type pushUpkeep struct {
	save *domain.ExternalMailSubscriptionSave
	// after is the subscription as the sync leaves it, for the next poll; nil when it has none.
	after *domain.ExternalMailSubscription
	// subscribed is the subscription the call created or renewed, to stop again when the save
	// finds the connection revoked meanwhile.
	subscribed *ExternalMailPushRequest
}

// subscriptionDue reports whether the connection needs a subscribe or renewal now.
func subscriptionDue(subscription *domain.ExternalMailSubscription, now time.Time) bool {
	return subscription == nil || subscription.Status == domain.ExternalMailSubscriptionStopped || !subscription.RenewAfter.After(now)
}

// upkeepPush subscribes or renews the connection's subscription when due, with the token the fetch
// used, while the caller holds the connection's lease. A rotated grant is returned for the sync to
// save. A failure backs off and never touches the connection's status; a provider that cannot push
// for this connection is recorded as unsupported and asked again a day later, without a warning.
func (s *service) upkeepPush(ctx context.Context, source ExternalMailPushSource, connection *domain.ExternalMailConnection,
	subscription *domain.ExternalMailSubscription, token ExternalMailToken, now time.Time) (pushUpkeep, string) {
	upkeep := pushUpkeep{after: subscription}
	if !subscriptionDue(subscription, now) {
		return upkeep, ""
	}
	request := ExternalMailPushRequest{Token: token, AccountAddress: connection.AccountAddress, TenantMailbox: connection.TenantID != nil, Now: now}
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
	if errors.Is(err, ErrExternalMailPushUnsupported) {
		upkeep.save = &domain.ExternalMailSubscriptionSave{
			ConnectionID: connection.ID, Provider: connection.Provider, Unsupported: true,
			RenewAfter: now.Add(externalMailPushUnsupportedRecheck), Now: now,
		}
		upkeep.after = &domain.ExternalMailSubscription{Status: domain.ExternalMailSubscriptionUnsupported, RenewAfter: upkeep.save.RenewAfter}
		return upkeep, got.RefreshToken
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
		after := &domain.ExternalMailSubscription{Status: domain.ExternalMailSubscriptionActive, ExpiresAt: &got.ExpiresAt, RenewAfter: save.RenewAfter, LiveSince: &now}
		if subscription.Live(now) {
			// A renewal keeps what the subscription has shown so far.
			after.LiveSince, after.LastNotifiedAt, after.DegradedAt = subscription.LiveSince, subscription.LastNotifiedAt, subscription.DegradedAt
		} else if subscription != nil {
			after.DegradedAt = subscription.DegradedAt
		}
		upkeep.save, upkeep.after = save, after
		cleanup := ExternalMailPushRequest{Token: token, AccountAddress: connection.AccountAddress, SubscriptionID: got.SubscriptionID, Now: now}
		if got.RefreshToken != "" {
			cleanup.Token.RefreshToken = got.RefreshToken
		}
		upkeep.subscribed = &cleanup
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
	if subscription != nil {
		after := *subscription
		after.RenewAfter = retry
		upkeep.after = &after
	}
	// The error names the operation and the provider's status only; it never carries a token.
	slog.WarnContext(ctx, "external_mail.push_subscribe_failed", slog.Uint64("connection_id", uint64(connection.ID)),
		slog.String("provider", string(connection.Provider)), slog.String("error_code", code),
		slog.Int("consecutive_failures", failures), slog.Time("retry_at", retry))
	return upkeep, got.RefreshToken
}

// pushMissedMail reports whether a poll stored mail its live, proven subscription never announced:
// a newly stored copy received after the subscription went live and earlier than the grace before
// the fetch.
// Mail received within the grace may still have its notification on the way.
func pushMissedMail(subscription *domain.ExternalMailSubscription, copies []*domain.MailMessage, startedAt time.Time, quiet time.Duration) bool {
	if !subscription.Proven(startedAt, quiet) {
		return false
	}
	var since time.Time
	if subscription.LiveSince != nil {
		since = *subscription.LiveSince
	}
	for _, message := range copies {
		received := message.CreatedAt
		if received.After(since.Add(externalMailPushDegradeGrace)) && received.Before(startedAt.Add(-externalMailPushDegradeGrace)) {
			return true
		}
	}
	return false
}

// pushPollAt is when a connection that synced fully at now is polled next: after the push poll
// interval while its subscription is shown to work, never past its renewal or expiry; otherwise at
// the regular interval.
func (s *service) pushPollAt(upkeep pushUpkeep, now time.Time) time.Time {
	regular := now.Add(externalMailSyncInterval)
	after := upkeep.after
	if !after.Proven(now, s.settings.ExternalMail.Push.Quiet()) || !after.ExpiresAt.After(regular) {
		return regular
	}
	next := minTime(now.Add(s.settings.ExternalMail.Push.Poll()), *after.ExpiresAt)
	if !after.RenewAfter.IsZero() {
		next = minTime(next, maxTime(after.RenewAfter, regular))
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

// Provider releases: what is left to do at the provider once a connection's revocation committed.
const (
	// externalMailReleaseGrace is how long a fresh release waits for the call made right after its
	// commit before the worker may take it over.
	externalMailReleaseGrace = 2 * time.Minute
	// externalMailReleaseDeadline is how long a release, and the sealed grant it holds, is kept: a
	// release still undone then is deleted and the grant lapses at the provider.
	externalMailReleaseDeadline = 24 * time.Hour
	// externalMailReleaseLease holds a claimed release from other workers while it runs.
	externalMailReleaseLease = 5 * time.Minute
	// externalMailReleaseBatch bounds the releases one worker pass performs, and the expired ones it
	// deletes.
	externalMailReleaseBatch = 20
)

// providerRelease is a provider call to make once a revocation committed.
type providerRelease func(ctx context.Context)

// deferredReleasesKey carries the releases of revocations inside an enclosing transaction.
type deferredReleasesKey struct{}

// deferredReleases collects provider releases until the enclosing transaction has committed.
type deferredReleases struct {
	mu       sync.Mutex
	releases []providerRelease
}

// withDeferredReleases makes revocations under ctx collect their provider calls instead of making
// them; the caller runs them once its transaction committed.
func withDeferredReleases(ctx context.Context) (context.Context, *deferredReleases) {
	collected := &deferredReleases{}
	return context.WithValue(ctx, deferredReleasesKey{}, collected), collected
}

// run makes the collected provider calls, at most externalMailReleaseParallelism at once, and
// waits for them; each is bounded by its own deadlines.
func (d *deferredReleases) run(ctx context.Context) {
	d.mu.Lock()
	releases := d.releases
	d.releases = nil
	d.mu.Unlock()
	slots := make(chan struct{}, externalMailReleaseParallelism)
	var wait sync.WaitGroup
	for _, release := range releases {
		wait.Add(1)
		slots <- struct{}{}
		go func(release providerRelease) {
			defer func() { <-slots; wait.Done() }()
			release(context.WithoutCancel(ctx))
		}(release)
	}
	wait.Wait()
}

// releaseAfterCommit runs the release now when ctx is outside any collecting transaction, or
// queues it for the transaction that collects.
func releaseAfterCommit(ctx context.Context, release providerRelease) {
	if collected, ok := ctx.Value(deferredReleasesKey{}).(*deferredReleases); ok {
		collected.mu.Lock()
		collected.releases = append(collected.releases, release)
		collected.mu.Unlock()
		return
	}
	release(ctx)
}

// planProviderRelease reads, without calling the provider, what the revocation of the connection
// leaves to do there: stop its live push subscription (unless another live connection of the same
// account shares a Gmail watch) and hand back the sealed grant it holds now, before the revocation
// erases it. Nil when there is nothing to do.
func (s *service) planProviderRelease(ctx context.Context, connection *domain.ExternalMailConnection) *domain.ExternalMailProviderRelease {
	now := s.now().UTC()
	release := &domain.ExternalMailProviderRelease{
		ConnectionID: connection.ID, OrganizationID: connection.OrganizationID, Provider: connection.Provider,
		EncryptedGrant: connection.EncryptedRefreshToken, NextAttemptAt: now.Add(externalMailReleaseGrace),
		ExpiresAt: now.Add(externalMailReleaseDeadline), CreatedAt: now,
	}
	if s.pushOn() {
		found, err := s.pushStore.FindExternalMailSubscription(ctx, connection.ID)
		if err == nil && found != nil && found.Status == domain.ExternalMailSubscriptionActive {
			others, countErr := s.pushStore.CountLiveExternalMailWatchers(ctx, connection.Provider, found.LookupKey, connection.ID)
			if countErr == nil && others == 0 {
				release.StopPush, release.StopSubscriptionID = true, found.ProviderSubscriptionID
			}
		}
	}
	if !release.StopPush && release.EncryptedGrant == "" {
		return nil
	}
	return release
}

// recordProviderRelease stores the release in the revocation's transaction, so a process that stops
// right after the commit leaves it to the worker.
func (s *service) recordProviderRelease(tx context.Context, release *domain.ExternalMailProviderRelease) error {
	if release == nil || s.releases == nil {
		return nil
	}
	return s.releases.CreateExternalMailProviderRelease(tx, release)
}

// performProviderRelease makes the release's provider calls for the connection: stop the push
// subscription, then hand the grant back. A stored release is deleted once both succeeded, and
// retried with backoff otherwise until its deadline. Nothing secret is logged.
func (s *service) performProviderRelease(ctx context.Context, connection *domain.ExternalMailConnection, release *domain.ExternalMailProviderRelease) {
	held := *connection
	held.EncryptedRefreshToken = release.EncryptedGrant
	var failed error
	if release.StopPush {
		failed = s.stopPushAtProvider(ctx, &held, release.StopSubscriptionID)
	}
	if err := s.returnExternalGrant(ctx, &held); err != nil && failed == nil {
		failed = err
	}
	if errors.Is(failed, ErrExternalMailAuth) {
		// The provider no longer honors the grant: nothing is left to stop or hand back.
		failed = nil
	}
	if s.releases == nil || release.ID == 0 {
		return
	}
	if failed == nil {
		if err := s.releases.DeleteExternalMailProviderRelease(ctx, release.ID); err != nil {
			slog.WarnContext(ctx, "external_mail.provider_release_failed", slog.Uint64("connection_id", uint64(connection.ID)),
				slog.String("provider", string(connection.Provider)), slog.String("stage", "delete"))
		}
		return
	}
	attempts := release.Attempts + 1
	retry := s.now().UTC().Add(externalMailBackoff(attempts, externalMailJitter))
	if err := s.releases.DeferExternalMailProviderRelease(ctx, release.ID, attempts, retry); err != nil {
		slog.WarnContext(ctx, "external_mail.provider_release_failed", slog.Uint64("connection_id", uint64(connection.ID)),
			slog.String("provider", string(connection.Provider)), slog.String("stage", "defer"))
	}
}

// processProviderReleases runs in each sync pass: it deletes releases past their deadline, with
// the sealed grants they hold, and performs a bounded batch of the due ones. A failure is logged;
// the pass goes on.
func (s *service) processProviderReleases(ctx context.Context, now time.Time) {
	if s.releases == nil {
		return
	}
	if expired, err := s.releases.DeleteExpiredExternalMailProviderReleases(ctx, now, externalMailReleaseBatch); err != nil {
		slog.WarnContext(ctx, "external_mail.provider_release_failed", slog.String("stage", "expire"))
	} else if expired > 0 {
		slog.WarnContext(ctx, "external_mail.provider_release_expired", slog.Int("releases", expired))
	}
	due, err := s.releases.ClaimExternalMailProviderReleases(ctx, now, now.Add(externalMailReleaseLease), externalMailReleaseBatch)
	if err != nil {
		slog.WarnContext(ctx, "external_mail.provider_release_failed", slog.String("stage", "claim"))
		return
	}
	for _, release := range due {
		if ctx.Err() != nil {
			return
		}
		connection, findErr := s.externalMail.FindExternalMailConnection(ctx, release.OrganizationID, release.ConnectionID)
		if findErr != nil {
			if errors.Is(findErr, ErrExternalMailConnectionNotFound) {
				if err := s.releases.DeleteExternalMailProviderRelease(ctx, release.ID); err != nil {
					slog.WarnContext(ctx, "external_mail.provider_release_failed", slog.String("stage", "delete"))
				}
			}
			continue
		}
		s.performProviderRelease(ctx, connection, release)
	}
}

// stopPushAtProvider stops a subscription at its provider with the connection's grant (or its
// tenant's token). A provider that cannot be reached is retried by the release; the row is already
// stopped.
func (s *service) stopPushAtProvider(ctx context.Context, connection *domain.ExternalMailConnection, subscriptionID string) error {
	connector, registered := s.externalConnectors.lookup(connection.Provider)
	if !registered || connector.Push == nil || s.encryptor == nil {
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx, externalMailPushCallTimeout)
	defer cancel()
	token, _, err := s.connectionToken(callCtx, connection, connector)
	if err == nil {
		err = connector.Push.Unsubscribe(callCtx, ExternalMailPushRequest{
			Token: token, AccountAddress: connection.AccountAddress, SubscriptionID: subscriptionID,
			TenantMailbox: connection.TenantID != nil, Now: s.now().UTC(),
		})
	}
	if err != nil {
		slog.WarnContext(ctx, "external_mail.push_unsubscribe_failed", slog.Uint64("connection_id", uint64(connection.ID)),
			slog.String("provider", string(connection.Provider)))
		return err
	}
	slog.InfoContext(ctx, "external_mail.push_unsubscribed", slog.Uint64("connection_id", uint64(connection.ID)),
		slog.String("provider", string(connection.Provider)))
	return nil
}

// unsubscribeOrphan stops at the provider a subscription a sync created or renewed for a connection
// that was revoked while the sync ran, so no watch outlives its connection. Best effort.
func (s *service) unsubscribeOrphan(ctx context.Context, source ExternalMailPushSource, connection *domain.ExternalMailConnection, request ExternalMailPushRequest) {
	if others, err := s.pushStore.CountLiveExternalMailWatchers(ctx, connection.Provider, normalizedPushKey(connection.AccountAddress), connection.ID); err != nil || others > 0 {
		return
	}
	callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), externalMailPushCallTimeout)
	defer cancel()
	request.TenantMailbox = connection.TenantID != nil
	if err := source.Unsubscribe(callCtx, request); err != nil {
		slog.WarnContext(ctx, "external_mail.push_unsubscribe_failed", slog.Uint64("connection_id", uint64(connection.ID)),
			slog.String("provider", string(connection.Provider)), slog.String("reason", "connection_revoked_during_sync"))
		return
	}
	slog.InfoContext(ctx, "external_mail.push_unsubscribed", slog.Uint64("connection_id", uint64(connection.ID)),
		slog.String("provider", string(connection.Provider)), slog.String("reason", "connection_revoked_during_sync"))
}

// stopPushRow stops the connection's subscription row in the caller's transaction.
func (s *service) stopPushRow(tx context.Context, connectionID uint) error {
	if !s.pushOn() {
		return nil
	}
	_, err := s.pushStore.StopExternalMailSubscription(tx, connectionID, s.now().UTC())
	return err
}

// attachPushState fills in the push subscription of each listed connection while push is on, with
// the status owners see: active only while push is shown to work.
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
	now := s.now().UTC()
	for _, connection := range connections {
		connection.Push = subscriptions[connection.ID]
		connection.PushStatus = pushStatus(connection.Push, now, s.settings.ExternalMail.Push.Quiet())
	}
	return nil
}

// Public push statuses beyond the stored ones.
const (
	// pushStatusQuiet is a live subscription without a notification within the quiet limit.
	pushStatusQuiet domain.ExternalMailSubscriptionStatus = "quiet"
	// pushStatusDegraded is a live subscription a poll caught missing new mail.
	pushStatusDegraded domain.ExternalMailSubscriptionStatus = "degraded"
)

// pushStatus is the status owners see for a subscription at now.
func pushStatus(subscription *domain.ExternalMailSubscription, now time.Time, quiet time.Duration) domain.ExternalMailSubscriptionStatus {
	switch {
	case subscription == nil:
		return ""
	case subscription.Status != domain.ExternalMailSubscriptionActive:
		return subscription.Status
	case !subscription.Live(now):
		return domain.ExternalMailSubscriptionPending
	case subscription.DegradedAt != nil:
		return pushStatusDegraded
	case !subscription.Proven(now, quiet):
		return pushStatusQuiet
	}
	return domain.ExternalMailSubscriptionActive
}

// normalizedPushKey is a Gmail notification's address as it is stored: trimmed and lowercased.
func normalizedPushKey(address string) string {
	return strings.ToLower(strings.TrimSpace(address))
}

// savePushUpkeep records, in the sync's transaction, what the sync did to the connection's
// subscription: the subscribe or renewal attempt, a poll that caught it missing mail, and for a
// successful sync the notifications it answered and the next_sync_at it scheduled. Nothing is
// recorded for a connection that stopped being active meanwhile.
func (s *service) savePushUpkeep(tx context.Context, connection *domain.ExternalMailConnection, applied, succeeded bool, upkeep pushUpkeep,
	degraded bool, trigger domain.ExternalMailSyncTrigger, startedAt time.Time) error {
	if !s.pushOn() || !applied {
		return nil
	}
	if upkeep.save != nil {
		if err := s.pushStore.SaveExternalMailSubscription(tx, *upkeep.save); err != nil {
			return err
		}
	}
	if degraded {
		// The connection is polled at the regular interval from now on, whatever the save scheduled.
		now := s.now().UTC()
		marked, err := s.pushStore.MarkExternalMailPushDegraded(tx, connection.ID, now, now.Add(externalMailSyncInterval))
		if err != nil {
			return err
		}
		if marked {
			metrics.RecordExternalMailPushDegraded(string(connection.Provider))
			slog.WarnContext(tx, "external_mail.push_degraded", slog.Uint64("connection_id", uint64(connection.ID)),
				slog.String("provider", string(connection.Provider)))
		}
	}
	if !succeeded {
		return nil
	}
	return s.pushStore.RecordExternalMailPushSync(tx, domain.ExternalMailSubscriptionSynced{
		ConnectionID: connection.ID, FetchStartedAt: startedAt, Pushed: trigger == domain.ExternalMailSyncTriggerPush, Now: s.now().UTC(),
	})
}
