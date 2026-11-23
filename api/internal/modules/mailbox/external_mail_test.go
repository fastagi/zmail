package mailbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
)

const (
	externalTestKey   = "0123456789abcdef0123456789abcdef"
	externalTestToken = "refresh-b"
)

// externalFixture is the PostgreSQL access scenario plus B's external Gmail mailbox, bound to a
// fake source and due for its first sync.
type externalFixture struct {
	*accessFixture
	source     *FakeExternalMailSource
	mailbox    *domain.Mailbox
	connection *domain.ExternalMailConnection
	advance    func(time.Duration) time.Time
}

func newExternalFixture(t *testing.T, providers ...string) *externalFixture {
	t.Helper()
	repo, db := openRepository(t)
	if err := db.AutoMigrate(&ExternalMailConnectionPO{}, &ExternalMailProviderReleasePO{}, &ExternalMailAuthorizationPO{}, &ExternalMailSyncRunPO{}, &MailTriagePO{}, &MailTriageSettingsPO{},
		&ExternalMailTenantPO{}, &ExternalMailAddressRecordPO{}); err != nil {
		t.Fatalf("migrate external mail connections: %v", err)
	}
	createOwner(t, db, accessOrg)
	transport := &fakeTransport{}
	memberships := newAccessMemberships()
	settings := accessSettings()
	settings.SecretEncryptionKey = externalTestKey
	settings.ExternalMail = config.ExternalMailConfig{Providers: providers}
	svc := NewService(repo, memberships, repo, repo, repo, repo,
		nil, nil, nil, transport, &fakeNotifier{}, settings, nil, nil, nil, nil)
	source := NewFakeExternalMailSource()
	svc.externalConnectors = fakeExternalMailRegistry(source)
	f := &externalFixture{accessFixture: seedAccessFixture(t, svc, repo, transport, memberships), source: source}
	f.advance = f.fixClock()

	ctx := context.Background()
	owner := uint(accessB)
	f.mailbox = &domain.Mailbox{
		OrganizationID: accessOrg, CreatedBy: accessB, Address: "b.personal@gmail.example",
		Type: domain.MailboxTypePersonal, Origin: domain.MailboxOriginConnected, OwnerUserID: &owner,
	}
	if err := repo.CreateMailbox(ctx, f.mailbox, domain.MailboxesPerOrganizationCeiling); err != nil {
		t.Fatalf("create external mailbox: %v", err)
	}
	sealed, err := svc.sealRefreshToken(externalTestToken)
	if err != nil {
		t.Fatalf("seal token: %v", err)
	}
	f.connection = &domain.ExternalMailConnection{
		OrganizationID: accessOrg, MailboxID: f.mailbox.ID, Provider: domain.ExternalMailProviderGmail,
		AccountAddress: f.mailbox.Address, OwnerUserID: &owner, EncryptedRefreshToken: sealed,
		NextSyncAt: svc.now().UTC(),
	}
	if err := repo.CreateExternalMailConnection(ctx, f.connection); err != nil {
		t.Fatalf("create connection: %v", err)
	}
	return f
}

func (f *externalFixture) sync(t *testing.T) int {
	t.Helper()
	handled, err := f.service.SyncExternalMail(context.Background(), 20)
	if err != nil {
		t.Fatalf("SyncExternalMail: %v", err)
	}
	return handled
}

func (f *externalFixture) reload(t *testing.T) *domain.ExternalMailConnection {
	t.Helper()
	connection, err := f.service.externalMail.FindExternalMailConnection(context.Background(), accessOrg, f.connection.ID)
	if err != nil {
		t.Fatalf("FindExternalMailConnection: %v", err)
	}
	return connection
}

// storedExternal lists the external mailbox as its owner.
func (f *externalFixture) storedExternal(t *testing.T) []*domain.MailMessage {
	t.Helper()
	messages, _, err := f.service.ListMessages(context.Background(), f.b, f.mailbox.ID, domain.MailMessageFilter{Page: 1, PerPage: 50})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	return messages
}

func externalMessage(id, subject string, receivedAt time.Time) ExternalMessage {
	return ExternalMessage{
		ExternalID: id, FromAddress: "carol@sender.test", FromName: "Carol", To: []string{"b.personal@gmail.example"},
		Subject: subject, TextBody: "Body of " + subject, RFCMessageID: id + "@sender.test", ReceivedAt: receivedAt,
	}
}

// TestExternalMailSyncIsIdempotent: fetching the same mail again, as after a reconnect or a
// repeated notification, stores no second copy.
func TestExternalMailSyncIsIdempotent(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	received := f.service.now().Add(-time.Hour).UTC()
	f.source.Deliver(externalTestToken, externalMessage("g-1", "Invoice", received), externalMessage("g-2", "Lunch", received))

	if handled := f.sync(t); handled != 1 {
		t.Fatalf("first pass handled %d connections, want 1", handled)
	}
	first := f.storedExternal(t)
	if len(first) != 2 {
		t.Fatalf("stored %d messages, want 2", len(first))
	}
	for _, message := range first {
		if message.Source != domain.MailSourceGmail || message.Direction != domain.MailDirectionInbound {
			t.Fatalf("message %d: source %q direction %q", message.ID, message.Source, message.Direction)
		}
		if !message.CreatedAt.Equal(received) {
			t.Fatalf("message %d created at %v, want the provider's receive time %v", message.ID, message.CreatedAt, received)
		}
	}

	// A reconnect starts over from an empty cursor and fetches the same messages again.
	if err := f.repo.(*repository).db.Exec(`UPDATE external_mail_connections SET sync_cursor = '' WHERE id = ?`, f.connection.ID).Error; err != nil {
		t.Fatalf("reset cursor: %v", err)
	}
	f.advance(externalMailSyncInterval)
	if handled := f.sync(t); handled != 1 {
		t.Fatalf("second pass handled %d connections, want 1", handled)
	}
	if again := f.storedExternal(t); len(again) != 2 {
		t.Fatalf("after a repeated fetch the mailbox holds %d messages, want 2", len(again))
	}
	if fetches := f.source.Fetches(externalTestToken); fetches != 2 {
		t.Fatalf("fetches = %d, want 2", fetches)
	}
}

// TestExternalMailSyncAdvancesCursor: each successful fetch stores the provider's new cursor and
// time, the next fetch starts from it, and a connection is not polled again within the interval.
func TestExternalMailSyncAdvancesCursor(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	f.source.Deliver(externalTestToken, externalMessage("g-1", "One", time.Time{}))
	f.sync(t)

	connection := f.reload(t)
	if connection.SyncCursor != "1" || connection.LastSyncedAt == nil || connection.LastErrorCode != "" {
		t.Fatalf("after the first sync: cursor %q, synced %v, error %q", connection.SyncCursor, connection.LastSyncedAt, connection.LastErrorCode)
	}
	if !connection.NextSyncAt.Equal(f.service.now().UTC().Add(externalMailSyncInterval)) {
		t.Fatalf("next sync at %v, want one interval later", connection.NextSyncAt)
	}

	f.source.Deliver(externalTestToken, externalMessage("g-2", "Two", time.Time{}))
	if handled := f.sync(t); handled != 0 {
		t.Fatalf("a pass within the interval handled %d connections, want 0", handled)
	}
	f.advance(externalMailSyncInterval)
	f.sync(t)
	if connection = f.reload(t); connection.SyncCursor != "2" {
		t.Fatalf("cursor = %q, want 2", connection.SyncCursor)
	}
	if stored := f.storedExternal(t); len(stored) != 2 {
		t.Fatalf("stored %d messages, want 2", len(stored))
	}
}

// TestExternalMailSyncAuthFailureNeedsReconnect: a refused token moves the connection to
// needs_reconnect and polling stops.
func TestExternalMailSyncAuthFailureNeedsReconnect(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	f.source.FailAuth(externalTestToken)
	f.sync(t)

	connection := f.reload(t)
	if connection.Status != domain.ExternalMailStatusNeedsReconnect || connection.LastErrorCode != domain.ExternalMailErrorAuthFailed {
		t.Fatalf("status %q error %q, want needs_reconnect with %q", connection.Status, connection.LastErrorCode, domain.ExternalMailErrorAuthFailed)
	}
	f.advance(10 * externalMailSyncInterval)
	if handled := f.sync(t); handled != 0 {
		t.Fatalf("a connection that needs reconnecting was polled (%d)", handled)
	}
	if fetches := f.source.Fetches(externalTestToken); fetches != 1 {
		t.Fatalf("fetches = %d, want 1", fetches)
	}
}

// failingSource fails every fetch with a provider error that is not an authorization failure.
type failingSource struct{ *FakeExternalMailSource }

func (failingSource) Fetch(context.Context, ExternalMailFetchRequest) (ExternalMailFetch, error) {
	return ExternalMailFetch{}, errors.New("provider unavailable")
}

// TestExternalMailSyncFetchFailureRetries: another provider failure keeps the connection active
// with its cursor, records the error, and retries next interval.
func TestExternalMailSyncFetchFailureRetries(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	f.source.Deliver(externalTestToken, externalMessage("g-1", "One", time.Time{}))
	f.sync(t)
	f.service.externalConnectors = fakeExternalMailRegistry(failingSource{f.source})
	f.advance(externalMailSyncInterval)
	f.sync(t)

	connection := f.reload(t)
	if connection.Status != domain.ExternalMailStatusActive || connection.LastErrorCode != domain.ExternalMailErrorFetchFailed || connection.SyncCursor != "1" {
		t.Fatalf("status %q error %q cursor %q, want active, fetch_failed, 1", connection.Status, connection.LastErrorCode, connection.SyncCursor)
	}
	f.service.externalConnectors = fakeExternalMailRegistry(f.source)
	f.advance(externalMailSyncInterval)
	f.sync(t)
	if connection = f.reload(t); connection.LastErrorCode != "" {
		t.Fatalf("a successful sync keeps error %q", connection.LastErrorCode)
	}
}

// TestExternalMailRevokeStopsFetching: unbinding revokes the grant at the provider, erases the
// token, keeps the synced mail, and the connection is never fetched again.
func TestExternalMailRevokeStopsFetching(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	f.source.Deliver(externalTestToken, externalMessage("g-1", "One", time.Time{}))
	f.sync(t)

	ctx := context.Background()
	if err := f.service.RevokeExternalMailConnection(ctx, f.c, f.connection.ID); !errors.Is(err, ErrExternalMailConnectionNotFound) {
		t.Fatalf("another member revoking: err = %v, want not found", err)
	}
	if err := f.service.RevokeExternalMailConnection(ctx, f.b, f.connection.ID); err != nil {
		t.Fatalf("owner revoking: %v", err)
	}
	connection := f.reload(t)
	if connection.Status != domain.ExternalMailStatusRevoked || connection.EncryptedRefreshToken != "" || connection.SyncCursor != "" {
		t.Fatalf("after revoke: status %q, token kept %v, cursor %q", connection.Status, connection.EncryptedRefreshToken != "", connection.SyncCursor)
	}
	if !f.source.Revoked(externalTestToken) {
		t.Fatal("the provider grant was not revoked")
	}

	f.source.Deliver(externalTestToken, externalMessage("g-2", "Two", time.Time{}))
	f.advance(10 * externalMailSyncInterval)
	if handled := f.sync(t); handled != 0 {
		t.Fatalf("a revoked connection was polled (%d)", handled)
	}
	if fetches := f.source.Fetches(externalTestToken); fetches != 1 {
		t.Fatalf("fetches = %d, want 1", fetches)
	}
	if stored := f.storedExternal(t); len(stored) != 1 {
		t.Fatalf("synced mail after revoke: %d messages, want the 1 already synced", len(stored))
	}
	if err := f.service.RevokeExternalMailConnection(ctx, f.admin, f.connection.ID); err != nil {
		t.Fatalf("revoking twice: %v", err)
	}
}

// revokingSource revokes the connection while its fetch is running.
type revokingSource struct {
	*FakeExternalMailSource
	revoke func()
}

func (s revokingSource) Fetch(ctx context.Context, request ExternalMailFetchRequest) (ExternalMailFetch, error) {
	fetched, err := s.FakeExternalMailSource.Fetch(ctx, request)
	s.revoke()
	return fetched, err
}

// TestExternalMailRevokeDuringFetchDropsTheBatch: mail fetched before a revoke that lands mid-fetch
// is not stored, and the connection stays revoked.
func TestExternalMailRevokeDuringFetchDropsTheBatch(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	f.source.Deliver(externalTestToken, externalMessage("g-1", "One", time.Time{}))
	f.service.externalConnectors = fakeExternalMailRegistry(revokingSource{FakeExternalMailSource: f.source, revoke: func() {
		if err := f.service.RevokeExternalMailConnection(context.Background(), f.admin, f.connection.ID); err != nil {
			t.Errorf("revoke during fetch: %v", err)
		}
	}})
	f.sync(t)

	if stored := f.storedExternal(t); len(stored) != 0 {
		t.Fatalf("stored %d messages fetched before the revoke, want 0", len(stored))
	}
	if connection := f.reload(t); connection.Status != domain.ExternalMailStatusRevoked {
		t.Fatalf("status = %q, want revoked", connection.Status)
	}
}

// TestExternalMailboxIsPrivateAndReadOnly: only the binder reads the external mailbox, in its own
// listing and in the unified inbox; colleagues and admins do not, and nobody sends from it.
func TestExternalMailboxIsPrivateAndReadOnly(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	f.source.Deliver(externalTestToken, externalMessage("g-1", "Private", time.Time{}))
	f.sync(t)
	synced := f.storedExternal(t)[0]

	owner := listInbox(t, f.service, f.b, InboxRequest{})
	var found *InboxItem
	for i := range owner.Items {
		if owner.Items[i].Message.ID == synced.ID {
			found = &owner.Items[i]
		}
	}
	if found == nil || found.Message.Source != domain.MailSourceGmail {
		t.Fatalf("the binder's unified inbox misses the synced copy or its source: %+v", found)
	}
	for name, viewer := range map[string]domain.OrganizationContext{"colleague": f.c, "admin": f.admin} {
		for _, item := range listInbox(t, f.service, viewer, InboxRequest{}).Items {
			if item.Message.ID == synced.ID {
				t.Fatalf("%s sees the binder's external mail in the unified inbox", name)
			}
		}
	}

	ctx := context.Background()
	if _, _, err := f.service.ListMessages(ctx, f.c, f.mailbox.ID, domain.MailMessageFilter{Page: 1, PerPage: 50}); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("colleague listing the external mailbox: err = %v, want not found", err)
	}
	// An admin cannot even tell the member's own account exists.
	if _, err := f.service.GetMessage(ctx, f.admin, f.mailbox.ID, synced.ID); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("admin reading connected mail: err = %v, want not found", err)
	}
	if _, _, err := f.service.SendMessage(ctx, f.b, f.mailbox.ID, SendInput{To: []string{"x@example.com"}, Subject: "Hi", Text: "Hi"}); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("binder sending from the external mailbox: err = %v, want permission denied", err)
	}
	if _, _, err := f.service.send(ctx, f.mailbox, SendInput{To: []string{"x@example.com"}, Subject: "Hi", Text: "Hi"}, true); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("send() from an external mailbox: err = %v, want permission denied", err)
	}
	if len(f.transport.sent) != 0 {
		t.Fatalf("the transport sent %d messages", len(f.transport.sent))
	}

	connections, err := f.service.ListExternalMailConnections(ctx, f.b)
	if err != nil || len(connections) != 1 || connections[0].ID != f.connection.ID {
		t.Fatalf("binder's connections = %v, %v", connections, err)
	}
	if others, err := f.service.ListExternalMailConnections(ctx, f.c); err != nil || len(others) != 0 {
		t.Fatalf("colleague's connections = %v, %v; want none", others, err)
	}
}

// TestExternalMailOffWithoutProviders: with EXTERNAL_MAIL_PROVIDERS empty nothing is polled and
// the connection endpoints answer not found.
func TestExternalMailOffWithoutProviders(t *testing.T) {
	f := newExternalFixture(t)
	f.source.Deliver(externalTestToken, externalMessage("g-1", "One", time.Time{}))
	if handled := f.sync(t); handled != 0 || f.source.Fetches(externalTestToken) != 0 {
		t.Fatalf("sync with the feature off handled %d and fetched %d", handled, f.source.Fetches(externalTestToken))
	}
	ctx := context.Background()
	if _, err := f.service.ListExternalMailConnections(ctx, f.b); !errors.Is(err, ErrExternalMailConnectionNotFound) {
		t.Fatalf("list with the feature off: err = %v", err)
	}
	if err := f.service.RevokeExternalMailConnection(ctx, f.b, f.connection.ID); !errors.Is(err, ErrExternalMailConnectionNotFound) {
		t.Fatalf("revoke with the feature off: err = %v", err)
	}
}

func (f *externalFixture) call(t *testing.T, viewer domain.OrganizationContext, method, path string) (int, map[string]any) {
	t.Helper()
	return f.callWith(t, viewer, method, path, "")
}

func (f *externalFixture) callWith(t *testing.T, viewer domain.OrganizationContext, method, path, body string) (int, map[string]any) {
	t.Helper()
	router := accessRouter(f.service, viewer)
	handler := NewHandler(f.service)
	router.GET("/external-mail-connections", handler.ListExternalMailConnections)
	router.DELETE("/external-mail-connections/:connection_id", handler.RevokeExternalMailConnection)
	router.GET("/external-mail-connection-directory", handler.ListExternalMailConnectionDirectory)
	router.DELETE("/external-mail-connection-directory/:connection_id", handler.ForceDisconnectExternalMailConnection)
	router.GET("/external-mail-connectors", handler.ListExternalMailConnectors)
	router.POST("/external-mail-authorizations", handler.StartExternalMailAuthorization)
	router.POST("/external-mail-authorizations/completions", handler.CompleteExternalMailAuthorization)
	router.POST("/external-mail-credentials", handler.BindExternalMailCredentials)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	router.ServeHTTP(recorder, request)
	decoded := map[string]any{}
	_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
	return recorder.Code, decoded
}

func TestExternalMailConnectionHTTP(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	f.sync(t)

	status, body := f.call(t, f.b, http.MethodGet, "/external-mail-connections")
	items, _ := body["data"].([]any)
	if status != http.StatusOK || len(items) != 1 {
		t.Fatalf("binder listing = %d %v", status, body)
	}
	item := items[0].(map[string]any)
	for key, want := range map[string]any{
		"id": float64(f.connection.ID), "provider": "gmail", "account_address": f.mailbox.Address,
		"mailbox_id": float64(f.mailbox.ID), "status": "active", "last_error_code": nil,
	} {
		if item[key] != want {
			t.Fatalf("item[%s] = %v; want %v", key, item[key], want)
		}
	}
	if item["last_synced_at"] == nil {
		t.Fatal("last_synced_at is missing after a sync")
	}
	for _, secret := range []string{"refresh_token", "encrypted_refresh_token", "sync_cursor"} {
		if _, leaked := item[secret]; leaked {
			t.Fatalf("the response carries %s", secret)
		}
	}

	path := fmt.Sprintf("/external-mail-connections/%d", f.connection.ID)
	status, body = f.call(t, f.c, http.MethodDelete, path)
	if status != http.StatusNotFound || body["error_code"] != domain.CodeExternalMailConnectionNotFound {
		t.Fatalf("colleague revoking = %d %v", status, body)
	}
	if status, body = f.call(t, f.b, http.MethodDelete, path); status != http.StatusNoContent {
		t.Fatalf("binder revoking = %d %v", status, body)
	}

	off := newExternalFixture(t)
	status, body = off.call(t, off.b, http.MethodGet, "/external-mail-connections")
	if status != http.StatusNotFound || body["error_code"] != domain.CodeExternalMailConnectionNotFound {
		t.Fatalf("listing with the feature off = %d %v", status, body)
	}
}
