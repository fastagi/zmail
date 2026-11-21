package mailbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
)

// signIn starts a sign-in as viewer and returns the state the provider would hand back.
func (f *externalFixture) signIn(t *testing.T, viewer domain.OrganizationContext, input ExternalMailAuthorizationInput) string {
	t.Helper()
	started, err := f.service.StartExternalMailAuthorization(context.Background(), viewer, input)
	if err != nil {
		t.Fatalf("StartExternalMailAuthorization: %v", err)
	}
	parsed, err := url.Parse(started.AuthorizationURL)
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	query := parsed.Query()
	if query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") == "" {
		t.Fatalf("authorization URL %q carries no S256 PKCE challenge", started.AuthorizationURL)
	}
	return query.Get("state")
}

// bind signs viewer in to the fake account "<code>@external.invalid".
func (f *externalFixture) bind(t *testing.T, viewer domain.OrganizationContext, provider domain.ExternalMailProvider, code string) *domain.ExternalMailConnection {
	t.Helper()
	state := f.signIn(t, viewer, ExternalMailAuthorizationInput{Provider: provider})
	connection, created, err := f.service.CompleteExternalMailAuthorization(context.Background(), viewer, state, code)
	if err != nil || !created {
		t.Fatalf("CompleteExternalMailAuthorization = created %v, err %v; want a new connection", created, err)
	}
	return connection
}

// TestExternalMailBindCreatesPrivateConnection: completing a sign-in creates a read-only external
// mailbox that only the binder reads, and a connection due for its first sync at once, which
// then stores the account's mail.
func TestExternalMailBindCreatesPrivateConnection(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	connection := f.bind(t, f.c, domain.ExternalMailProviderOutlook, "carol")

	if connection.Status != domain.ExternalMailStatusActive || connection.AccountAddress != "carol@external.invalid" ||
		connection.Provider != domain.ExternalMailProviderOutlook || connection.BackfillDays != domain.DefaultExternalMailBackfillDays {
		t.Fatalf("connection = %+v", connection)
	}
	mailbox, err := f.repo.FindMailbox(context.Background(), accessOrg, connection.MailboxID)
	if err != nil || mailbox.Type != domain.MailboxTypeExternal || mailbox.OwnerUserID == nil || *mailbox.OwnerUserID != accessC {
		t.Fatalf("mailbox = %+v, err %v; want carol's external mailbox", mailbox, err)
	}
	if _, _, listErr := f.service.ListMessages(context.Background(), f.b, connection.MailboxID, domain.MailMessageFilter{Page: 1, PerPage: 10}); listErr == nil {
		t.Fatal("a colleague listed the new external mailbox")
	}

	f.source.Deliver("fake-carol", externalMessage("o-1", "Invoice", time.Time{}))
	f.sync(t)
	messages, _, err := f.service.ListMessages(context.Background(), f.c, connection.MailboxID, domain.MailMessageFilter{Page: 1, PerPage: 10})
	if err != nil || len(messages) != 1 || messages[0].Source != domain.MailSource("outlook") {
		t.Fatalf("owner's listing = %d messages, err %v; want the synced outlook copy", len(messages), err)
	}
}

// TestExternalMailAuthorizationIsSingleUseAndBound: a state works once, only for the member and
// organization that started it, and only before it expires.
func TestExternalMailAuthorizationIsSingleUseAndBound(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	ctx := context.Background()

	state := f.signIn(t, f.c, ExternalMailAuthorizationInput{Provider: domain.ExternalMailProviderGmail})
	if _, _, err := f.service.CompleteExternalMailAuthorization(ctx, f.d, state, "dave"); !errors.Is(err, ErrExternalMailAuthorizationNotFound) {
		t.Fatalf("another member completing = %v, want not found", err)
	}
	// The failed attempt used the state up.
	if _, _, err := f.service.CompleteExternalMailAuthorization(ctx, f.c, state, "carol"); !errors.Is(err, ErrExternalMailAuthorizationNotFound) {
		t.Fatalf("reusing the state = %v, want not found", err)
	}

	state = f.signIn(t, f.c, ExternalMailAuthorizationInput{Provider: domain.ExternalMailProviderGmail})
	f.advance(domain.ExternalMailAuthorizationTTL + time.Second)
	if _, _, err := f.service.CompleteExternalMailAuthorization(ctx, f.c, state, "carol"); !errors.Is(err, ErrExternalMailAuthorizationNotFound) {
		t.Fatalf("expired state = %v, want not found", err)
	}

	state = f.signIn(t, f.c, ExternalMailAuthorizationInput{Provider: domain.ExternalMailProviderGmail})
	if _, _, err := f.service.CompleteExternalMailAuthorization(ctx, f.c, state, "refuse"); !errors.Is(err, ErrExternalMailAuthorizationFailed) {
		t.Fatalf("refused code = %v, want authorization failed", err)
	}
	if _, _, err := f.service.CompleteExternalMailAuthorization(ctx, f.c, "made-up", "carol"); !errors.Is(err, ErrExternalMailAuthorizationNotFound) {
		t.Fatalf("unknown state = %v, want not found", err)
	}
}

// TestExternalMailReconnectRestoresWithoutDuplicates: a connection that lost its grant is restored
// by signing in to the same account again; it keeps its mailbox and cursor, so mail already
// synced is not stored twice. A reconnect that signs in to another account changes nothing.
func TestExternalMailReconnectRestoresWithoutDuplicates(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	ctx := context.Background()
	connection := f.bind(t, f.c, domain.ExternalMailProviderGmail, "carol")
	f.source.Deliver("fake-carol", externalMessage("g-1", "One", time.Time{}))
	f.sync(t)

	f.source.FailAuth("fake-carol")
	f.advance(externalMailSyncInterval)
	f.sync(t)
	lost, _ := f.service.externalMail.FindExternalMailConnection(ctx, accessOrg, connection.ID)
	if lost.Status != domain.ExternalMailStatusNeedsReconnect {
		t.Fatalf("status = %q, want needs_reconnect", lost.Status)
	}

	reconnect := ExternalMailAuthorizationInput{ConnectionID: &connection.ID}
	state := f.signIn(t, f.c, reconnect)
	if _, _, err := f.service.CompleteExternalMailAuthorization(ctx, f.c, state, "someone-else"); !errors.Is(err, ErrExternalMailAccountMismatch) {
		t.Fatalf("reconnect to another account = %v, want mismatch", err)
	}

	state = f.signIn(t, f.c, reconnect)
	restored, created, err := f.service.CompleteExternalMailAuthorization(ctx, f.c, state, "carol")
	if err != nil || created || restored.ID != connection.ID || restored.Status != domain.ExternalMailStatusActive ||
		restored.LastErrorCode != "" || restored.SyncCursor != lost.SyncCursor {
		t.Fatalf("reconnect = %+v, created %v, err %v; want the same active connection with its cursor", restored, created, err)
	}

	f.source.Deliver("fake-carol", externalMessage("g-2", "Two", time.Time{}))
	f.sync(t)
	messages, _, _ := f.service.ListMessages(ctx, f.c, connection.MailboxID, domain.MailMessageFilter{Page: 1, PerPage: 10})
	if len(messages) != 2 {
		t.Fatalf("stored %d messages after the reconnect, want 2", len(messages))
	}
}

// TestExternalMailRebindAfterRevokeReusesMailbox: binding an account again after unbinding it
// restores the same connection and mailbox and backfills from the start without copies.
func TestExternalMailRebindAfterRevokeReusesMailbox(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	ctx := context.Background()
	connection := f.bind(t, f.c, domain.ExternalMailProviderGmail, "carol")
	f.source.Deliver("fake-carol", externalMessage("g-1", "One", time.Time{}))
	f.sync(t)
	if err := f.service.RevokeExternalMailConnection(ctx, f.c, connection.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	state := f.signIn(t, f.c, ExternalMailAuthorizationInput{Provider: domain.ExternalMailProviderGmail})
	rebound, created, err := f.service.CompleteExternalMailAuthorization(ctx, f.c, state, "carol")
	if err != nil || created || rebound.ID != connection.ID || rebound.MailboxID != connection.MailboxID ||
		rebound.Status != domain.ExternalMailStatusActive || rebound.SyncCursor != "" {
		t.Fatalf("rebind = %+v, created %v, err %v; want the same connection, active, cursor reset", rebound, created, err)
	}
	f.advance(time.Second)
	f.sync(t)
	messages, _, _ := f.service.ListMessages(ctx, f.c, connection.MailboxID, domain.MailMessageFilter{Page: 1, PerPage: 10})
	if len(messages) != 1 {
		t.Fatalf("stored %d messages after the backfill, want the 1 already there", len(messages))
	}
}

// TestExternalMailAccountBelongsToOneMember: a colleague cannot bind or take over an account
// another member bound, and a member binds at most MaxExternalMailConnectionsPerUser accounts.
func TestExternalMailAccountBelongsToOneMember(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	ctx := context.Background()
	f.bind(t, f.c, domain.ExternalMailProviderGmail, "carol")

	state := f.signIn(t, f.d, ExternalMailAuthorizationInput{Provider: domain.ExternalMailProviderGmail})
	if _, _, err := f.service.CompleteExternalMailAuthorization(ctx, f.d, state, "carol"); !errors.Is(err, ErrExternalMailAccountTaken) {
		t.Fatalf("a colleague binding the account = %v, want taken", err)
	}
	if !f.source.Revoked("fake-carol") {
		t.Fatal("the refused grant was not handed back to the provider")
	}
	// The same address at another provider is another account.
	f.bind(t, f.d, domain.ExternalMailProviderOutlook, "carol")

	for i := 1; i < domain.MaxExternalMailConnectionsPerUser; i++ {
		f.bind(t, f.c, domain.ExternalMailProviderOutlook, "carol"+string(rune('a'+i)))
	}
	state = f.signIn(t, f.c, ExternalMailAuthorizationInput{Provider: domain.ExternalMailProviderOutlook})
	if _, _, err := f.service.CompleteExternalMailAuthorization(ctx, f.c, state, "one-too-many"); !errors.Is(err, ErrExternalMailConnectionLimit) {
		t.Fatalf("binding past the limit = %v, want limit", err)
	}

	// Unbinding frees a place: revoked connections are kept for a rebind but do not count.
	owned, _ := f.service.ListExternalMailConnections(ctx, f.c)
	if err := f.service.RevokeExternalMailConnection(ctx, f.c, owned[0].ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	f.bind(t, f.c, domain.ExternalMailProviderOutlook, "after-unbinding")
	owned, _ = f.service.ListExternalMailConnections(ctx, f.c)
	for _, connection := range owned {
		if connection.Status == domain.ExternalMailStatusRevoked {
			t.Fatalf("the listing of %d shows a revoked connection before a live one is cut off", len(owned))
		}
	}
}

// TestExternalMailStartRules: a reconnect is only for the binder's own connection, an unknown
// connector is unavailable, and the backfill is bounded.
func TestExternalMailStartRules(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	ctx := context.Background()
	for name, tc := range map[string]struct {
		viewer domain.OrganizationContext
		input  ExternalMailAuthorizationInput
		want   error
	}{
		"another member's connection": {f.c, ExternalMailAuthorizationInput{ConnectionID: &f.connection.ID}, ErrExternalMailConnectionNotFound},
		"an admin's reconnect":        {f.admin, ExternalMailAuthorizationInput{ConnectionID: &f.connection.ID}, ErrExternalMailConnectionNotFound},
		"another provider":            {f.b, ExternalMailAuthorizationInput{ConnectionID: &f.connection.ID, Provider: domain.ExternalMailProviderOutlook}, ErrExternalMailAuthorizationInvalid},
		"an unknown connector":        {f.b, ExternalMailAuthorizationInput{Provider: "imap"}, ErrExternalMailProviderUnavailable},
		"too long a backfill":         {f.b, ExternalMailAuthorizationInput{Provider: domain.ExternalMailProviderGmail, BackfillDays: 91}, ErrExternalMailAuthorizationInvalid},
	} {
		if _, err := f.service.StartExternalMailAuthorization(ctx, tc.viewer, tc.input); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
	if got := f.service.ListExternalMailConnectors(ctx); len(got) != 2 || got[0].ID != domain.ExternalMailProviderGmail {
		t.Fatalf("connectors = %+v, want gmail and outlook", got)
	}
}

// TestExternalMailBindingOffWithoutProviders: with no connector enabled nothing can be started
// and no connector is listed.
func TestExternalMailBindingOffWithoutProviders(t *testing.T) {
	f := newExternalFixture(t)
	ctx := context.Background()
	if got := f.service.ListExternalMailConnectors(ctx); len(got) != 0 {
		t.Fatalf("connectors = %+v, want none", got)
	}
	if _, err := f.service.StartExternalMailAuthorization(ctx, f.b, ExternalMailAuthorizationInput{Provider: domain.ExternalMailProviderGmail}); !errors.Is(err, ErrExternalMailProviderUnavailable) {
		t.Fatalf("start = %v, want unavailable", err)
	}
}

// TestExternalMailUnregisteredConnectorPauses: a connection whose connector the instance stopped
// running keeps its grant and state and records why it is not syncing.
func TestExternalMailUnregisteredConnectorPauses(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	f.service.externalConnectors = &externalMailRegistry{}
	f.sync(t)
	connection := f.reload(t)
	if connection.Status != domain.ExternalMailStatusActive || connection.LastErrorCode != domain.ExternalMailErrorProviderUnavailable ||
		connection.EncryptedRefreshToken == "" {
		t.Fatalf("connection = %+v, want active with provider_unavailable and its grant", connection)
	}
}

// TestInboundDeliveryNeverReachesExternalMailbox: an external mailbox carries an outside address,
// which is not Zmail's to deliver to, and a Zmail mailbox may use the same address.
func TestInboundDeliveryNeverReachesExternalMailbox(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	if _, err := f.repo.FindMailboxByAddress(context.Background(), f.mailbox.Address); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("lookup of the external address = %v, want not found", err)
	}
	owner := uint(accessC)
	twin := &domain.Mailbox{OrganizationID: accessOrg, CreatedBy: accessC, Address: f.mailbox.Address,
		Type: domain.MailboxTypeExternal, OwnerUserID: &owner}
	if err := f.repo.CreateMailbox(context.Background(), twin, domain.MailboxesPerOrganizationCeiling); err != nil {
		t.Fatalf("a second external mailbox with the same outside address: %v", err)
	}
}

// TestSyncedMailIsQueuedForTriage: synced mail runs the same post-store step as Zmail's own
// inbound mail, so it reaches AI triage once its owner switched triage on, and only then.
func TestSyncedMailIsQueuedForTriage(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	db := f.repo.(*repository).db
	if err := db.AutoMigrate(&MailTriagePO{}, &MailTriageSettingsPO{}); err != nil {
		t.Fatalf("migrate triage tables: %v", err)
	}
	f.service.settings.Triage = config.AITriageConfig{Provider: config.AITriageProviderFake, DailyCap: 100}
	ctx := context.Background()

	// An organization admin cannot consent for a member's external mailbox; its owner can.
	f.switchTriage(t, true)
	if _, err := f.service.SetMailboxTriage(ctx, f.admin, f.mailbox.ID, true); err == nil {
		t.Fatal("an admin switched triage on for a member's external mailbox")
	}
	f.source.Deliver(externalTestToken, externalMessage("g-1", "Before consent", time.Time{}))
	f.sync(t)
	if _, err := f.service.SetMailboxTriage(ctx, f.b, f.mailbox.ID, true); err != nil {
		t.Fatalf("owner switching triage on: %v", err)
	}
	f.source.Deliver(externalTestToken, externalMessage("g-2", "After consent", time.Time{}))
	f.advance(externalMailSyncInterval)
	f.sync(t)

	stored := f.storedExternal(t)
	queued := map[string]bool{}
	for _, message := range stored {
		queued[message.Subject] = f.triageOf(t, message.ID) != nil
	}
	if queued["Before consent"] || !queued["After consent"] {
		t.Fatalf("queued = %v, want only the message synced after consent", queued)
	}
}

// TestExternalMailBindingHTTP: the connector list, the start, and both completion outcomes over
// HTTP, with no grant material in any response.
func TestExternalMailBindingHTTP(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)

	status, body := f.call(t, f.c, http.MethodGet, "/external-mail-connectors")
	connectors, _ := body["data"].([]any)
	if status != http.StatusOK || len(connectors) != 2 || connectors[0].(map[string]any)["auth_kind"] != "oauth2" {
		t.Fatalf("connectors = %d %v", status, body)
	}

	start := func(payload string) (string, int, map[string]any) {
		code, answer := f.callWith(t, f.c, http.MethodPost, "/external-mail-authorizations", payload)
		data, _ := answer["data"].(map[string]any)
		authorizationURL, _ := data["authorization_url"].(string)
		parsed, _ := url.Parse(authorizationURL)
		if parsed == nil {
			return "", code, answer
		}
		return parsed.Query().Get("state"), code, answer
	}
	if _, code, answer := start(`{}`); code != http.StatusUnprocessableEntity || answer["error_code"] != domain.CodeExternalMailAuthorizationInvalid {
		t.Fatalf("start without provider = %d %v", code, answer)
	}
	if _, code, answer := start(`{"provider":"gmail","backfill_days":91}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("start with a long backfill = %d %v", code, answer)
	}
	state, status, body := start(`{"provider":"gmail","backfill_days":30}`)
	if status != http.StatusCreated || state == "" {
		t.Fatalf("start = %d %v", status, body)
	}
	complete := fmt.Sprintf(`{"state":%q,"code":"carol"}`, state)
	status, body = f.callWith(t, f.c, http.MethodPost, "/external-mail-authorizations/completions", complete)
	data, _ := body["data"].(map[string]any)
	if status != http.StatusCreated || data["provider"] != "gmail" || data["account_address"] != "carol@external.invalid" {
		t.Fatalf("complete = %d %v", status, body)
	}
	for _, secret := range []string{"refresh_token", "encrypted_refresh_token", "sync_cursor", "state"} {
		if _, leaked := data[secret]; leaked {
			t.Fatalf("the completion carries %s", secret)
		}
	}
	status, body = f.callWith(t, f.c, http.MethodPost, "/external-mail-authorizations/completions", complete)
	if status != http.StatusNotFound || body["error_code"] != domain.CodeExternalMailAuthorizationNotFound {
		t.Fatalf("replayed completion = %d %v", status, body)
	}

	state, _, _ = start(fmt.Sprintf(`{"connection_id":%v}`, data["id"]))
	status, body = f.callWith(t, f.c, http.MethodPost, "/external-mail-authorizations/completions",
		fmt.Sprintf(`{"state":%q,"code":"carol"}`, state))
	if status != http.StatusOK {
		t.Fatalf("reconnect = %d %v", status, body)
	}
}

// TestLeavingTheOrganizationRevokesConnections: a member who leaves stops being synced at once.
// Their outside accounts are their own, so nothing is handed over: the grant is handed back, the
// token erased, and the copies stay unreadable. Rejoining and binding again restores the mailbox.
func TestLeavingTheOrganizationRevokesConnections(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	f.source.Deliver(externalTestToken, externalMessage("g-1", "Before leaving", time.Time{}))
	f.sync(t)

	f.leave(t, f.b, f.service.now())
	connection := f.reload(t)
	if connection.Status != domain.ExternalMailStatusRevoked || connection.EncryptedRefreshToken != "" {
		t.Fatalf("after leaving: status %q, token kept %v; want revoked and erased",
			connection.Status, connection.EncryptedRefreshToken != "")
	}
	if !f.source.Revoked(externalTestToken) {
		t.Fatal("the grant was not handed back to the provider")
	}
	fetches := f.source.Fetches(externalTestToken)
	f.source.Deliver(externalTestToken, externalMessage("g-2", "After leaving", time.Time{}))
	f.advance(externalMailSyncInterval)
	f.sync(t)
	if f.source.Fetches(externalTestToken) != fetches {
		t.Fatal("the account of a member who left was fetched again")
	}
	// The mailbox is not awaiting a handover: it is not the organization's to hand over.
	mailbox, err := f.repo.FindMailbox(context.Background(), accessOrg, f.mailbox.ID)
	if err != nil || mailbox.OwnerLeftAt != nil {
		t.Fatalf("external mailbox = %+v, err %v; want no handover state", mailbox, err)
	}
}

// TestExternalMailBackfillProceedsPageByPage: a source that returns pages is fetched again at once
// while more is ready, each page saved with its cursor, and then at the normal interval.
func TestExternalMailBackfillProceedsPageByPage(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	f.source.PageSize = 2
	for i := 1; i <= 5; i++ {
		f.source.Deliver(externalTestToken, externalMessage(fmt.Sprintf("g-%d", i), fmt.Sprintf("Mail %d", i), time.Time{}))
	}
	for pass, want := range []int{2, 4, 5} {
		if pass > 0 {
			f.advance(config.DefaultExternalMailMinFetchSpacing)
		}
		if handled := f.sync(t); handled != 1 {
			t.Fatalf("pass %d handled %d connections, want 1 without waiting for the interval", pass, handled)
		}
		if stored := len(f.storedExternal(t)); stored != want {
			t.Fatalf("pass %d stored %d messages, want %d", pass, stored, want)
		}
	}
	if handled := f.sync(t); handled != 0 {
		t.Fatalf("a finished backfill was fetched again before the interval (%d)", handled)
	}
	request := f.source.LastRequest(externalTestToken)
	if request.BackfillDays != domain.DefaultExternalMailBackfillDays || request.AccountAddress != f.mailbox.Address || request.Now.IsZero() {
		t.Fatalf("fetch request = %+v, want the connection's backfill window, address, and sync time", request)
	}
}

// TestExternalMailSavesRotatedToken: a grant the provider rotates during a fetch is saved with that
// page, so the next fetch uses the new one and the account is not lost.
func TestExternalMailSavesRotatedToken(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	f.source.Deliver(externalTestToken, externalMessage("g-1", "One", time.Time{}))
	f.source.RotateOnNextFetch(externalTestToken, "refresh-b-2")
	f.sync(t)

	stored, err := f.service.encryptor.DecryptString(f.reload(t).EncryptedRefreshToken)
	if err != nil || stored != "refresh-b-2" {
		t.Fatalf("stored token = %q, err %v; want the rotated one", stored, err)
	}
	f.source.Deliver(externalTestToken, externalMessage("g-2", "Two", time.Time{}))
	f.advance(externalMailSyncInterval)
	f.sync(t)
	if connection := f.reload(t); connection.Status != domain.ExternalMailStatusActive || len(f.storedExternal(t)) != 2 {
		t.Fatalf("after rotation: status %q, %d messages; want active and 2", connection.Status, len(f.storedExternal(t)))
	}
}

// TestExternalMailCursorExpiryResyncs: when the provider no longer honours the cursor, the sync
// starts over at once from an empty cursor and stores no second copy.
func TestExternalMailCursorExpiryResyncs(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	f.source.Deliver(externalTestToken, externalMessage("g-1", "One", time.Time{}))
	f.sync(t)
	f.source.ExpireCursor(externalTestToken)
	f.source.Deliver(externalTestToken, externalMessage("g-2", "Two", time.Time{}))
	f.advance(externalMailSyncInterval)
	f.sync(t)
	if connection := f.reload(t); connection.SyncCursor != "" || connection.Status != domain.ExternalMailStatusActive || connection.LastErrorCode != "" {
		t.Fatalf("after expiry: %+v, want an empty cursor, active, no error", connection)
	}
	f.advance(config.DefaultExternalMailMinFetchSpacing)
	if handled := f.sync(t); handled != 1 {
		t.Fatalf("the resync waited for the interval (%d handled)", handled)
	}
	if stored := len(f.storedExternal(t)); stored != 2 {
		t.Fatalf("stored %d messages after the resync, want 2 without copies", stored)
	}
}

// rotatingFailure rotates the grant and then fails the fetch, as a provider that redeems the
// refresh token before a throttled or expired read does.
type rotatingFailure struct {
	*FakeExternalMailSource
	next string
	fail error
}

func (s rotatingFailure) Fetch(context.Context, ExternalMailFetchRequest) (ExternalMailFetch, error) {
	return ExternalMailFetch{RefreshToken: s.next}, s.fail
}

// TestExternalMailKeepsRotatedTokenOfFailedFetch: a grant rotated before a fetch failed is saved,
// whether the failure is retried later or restarts the sync.
func TestExternalMailKeepsRotatedTokenOfFailedFetch(t *testing.T) {
	for name, failure := range map[string]error{
		"a retryable failure": errors.New("throttled"),
		"an expired cursor":   ErrExternalMailCursorExpired,
	} {
		f := newExternalFixture(t, config.ExternalMailSourceFake)
		f.service.externalConnectors = fakeExternalMailRegistry(rotatingFailure{f.source, "refresh-b-2", failure})
		f.sync(t)
		connection := f.reload(t)
		stored, err := f.service.encryptor.DecryptString(connection.EncryptedRefreshToken)
		if err != nil || stored != "refresh-b-2" || connection.Status != domain.ExternalMailStatusActive {
			t.Fatalf("%s: stored token %q, status %q, err %v; want the rotated token on an active connection",
				name, stored, connection.Status, err)
		}
	}
}
