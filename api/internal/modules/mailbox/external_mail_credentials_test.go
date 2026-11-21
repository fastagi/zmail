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
	"github.com/fastagi/zmail/api/internal/infra/ratelimit"
)

// newIMAPFixture is the external mail fixture with the IMAP connector pointed at a fake server.
func newIMAPFixture(t *testing.T) (*externalFixture, *fakeIMAPServer) {
	t.Helper()
	f := newExternalFixture(t, config.ExternalMailProviderIMAP)
	server := newFakeIMAPServer(t)
	f.service.externalConnectors = &externalMailRegistry{connectors: []ExternalMailConnector{{
		ID: ExternalMailProviderIMAP, AuthKind: ExternalMailAuthPassword,
		Capabilities: ExternalMailCapabilities{Read: true}, Source: server.source(),
	}}}
	return f, server
}

func imapInput(server *fakeIMAPServer, username string) ExternalMailCredentialsInput {
	return ExternalMailCredentialsInput{
		Provider: ExternalMailProviderIMAP, Host: "127.0.0.1", Port: server.port,
		Username: username, Password: fakeIMAPPassword,
	}
}

// TestIMAPCredentialBindingOutcomes: a new account is bound, binding it again restores the
// caller's connection, another member's account is refused, and refused credentials or an
// unreachable server bind nothing.
func TestIMAPCredentialBindingOutcomes(t *testing.T) {
	f, server := newIMAPFixture(t)
	ctx := context.Background()

	connection, created, err := f.service.BindExternalMailCredentials(ctx, f.c, imapInput(server, "Carol@imap.test"))
	if err != nil || !created || connection.Provider != ExternalMailProviderIMAP || connection.AccountAddress != "carol@imap.test" ||
		connection.Status != domain.ExternalMailStatusActive || connection.BackfillDays != domain.DefaultExternalMailBackfillDays {
		t.Fatalf("bind = %+v, created %v, err %v", connection, created, err)
	}
	mailbox, err := f.repo.FindMailbox(ctx, accessOrg, connection.MailboxID)
	if err != nil || mailbox.Type != domain.MailboxTypePersonal || !mailbox.Connected() || mailbox.OwnerUserID == nil || *mailbox.OwnerUserID != accessC {
		t.Fatalf("mailbox = %+v, err %v", mailbox, err)
	}
	stored, _ := f.service.externalMail.FindExternalMailConnection(ctx, accessOrg, connection.ID)
	if strings.Contains(stored.EncryptedRefreshToken, fakeIMAPPassword) {
		t.Fatal("the password is stored in the clear")
	}

	again, created, err := f.service.BindExternalMailCredentials(ctx, f.c, imapInput(server, "carol@imap.test"))
	if err != nil || created || again.ID != connection.ID {
		t.Fatalf("rebind = %+v, created %v, err %v; want the same connection", again, created, err)
	}
	if _, _, err = f.service.BindExternalMailCredentials(ctx, f.d, imapInput(server, "carol@imap.test")); !errors.Is(err, ErrExternalMailAccountTaken) {
		t.Fatalf("another member binding = %v, want taken", err)
	}
	wrong := imapInput(server, "dave@imap.test")
	wrong.Password = "wrong"
	if _, _, err = f.service.BindExternalMailCredentials(ctx, f.d, wrong); !errors.Is(err, ErrExternalMailAuthorizationFailed) {
		t.Fatalf("wrong password = %v, want authorization failed", err)
	}
	unreachable := imapInput(server, "dave@imap.test")
	unreachable.Port = closedPort(t)
	if _, _, err = f.service.BindExternalMailCredentials(ctx, f.d, unreachable); !errors.Is(err, ErrExternalMailServerUnreachable) {
		t.Fatalf("closed port = %v, want unreachable", err)
	}
	bare, created, err := f.service.BindExternalMailCredentials(ctx, f.d, imapInput(server, "Dave"))
	if err != nil || !created || bare.AccountAddress != "dave@127.0.0.1" {
		t.Fatalf("bare username = %+v, created %v, err %v; want dave@127.0.0.1", bare, created, err)
	}
	injected := imapInput(server, "dave@imap.test\r\nZ9 LOGOUT")
	if _, _, err := f.service.BindExternalMailCredentials(ctx, f.d, injected); !errors.Is(err, ErrExternalMailAuthorizationInvalid) {
		t.Fatalf("username with CRLF = %v, want invalid", err)
	}
	gmail := imapInput(server, "dave@imap.test")
	gmail.Provider = domain.ExternalMailProviderGmail
	if _, _, err := f.service.BindExternalMailCredentials(ctx, f.d, gmail); !errors.Is(err, ErrExternalMailProviderUnavailable) {
		t.Fatalf("an OAuth provider = %v, want unavailable", err)
	}
	long := imapInput(server, "erin@imap.test")
	long.BackfillDays = domain.MaxExternalMailBackfillDays + 1
	if _, _, err := f.service.BindExternalMailCredentials(ctx, f.d, long); !errors.Is(err, ErrExternalMailAuthorizationInvalid) {
		t.Fatalf("long backfill = %v, want invalid", err)
	}

	off := newExternalFixture(t)
	if _, _, err := off.service.BindExternalMailCredentials(ctx, off.c, imapInput(server, "carol@imap.test")); !errors.Is(err, ErrExternalMailProviderUnavailable) {
		t.Fatalf("binding with the feature off = %v, want unavailable", err)
	}
}

// TestIMAPCredentialBindingLimits: the per-member connection limit applies, and attempts are
// rate-limited per member.
func TestIMAPCredentialBindingLimits(t *testing.T) {
	f, server := newIMAPFixture(t)
	ctx := context.Background()
	f.service.externalCredentialLimits = externalCredentialLimits{}
	for i := range domain.MaxExternalMailConnectionsPerUser {
		if _, _, err := f.service.BindExternalMailCredentials(ctx, f.d, imapInput(server, fmt.Sprintf("d%d@imap.test", i))); err != nil {
			t.Fatalf("bind %d: %v", i, err)
		}
	}
	if _, _, err := f.service.BindExternalMailCredentials(ctx, f.d, imapInput(server, "one-more@imap.test")); !errors.Is(err, ErrExternalMailConnectionLimit) {
		t.Fatalf("past the limit = %v, want limit", err)
	}

	f.service.externalCredentialLimits = newExternalMailCredentialLimits(ratelimit.NewMemoryStores())
	wrong := imapInput(server, "carol@imap.test")
	wrong.Password = "guess"
	for range externalMailCredentialAttempts {
		if _, _, err := f.service.BindExternalMailCredentials(ctx, f.c, wrong); !errors.Is(err, ErrExternalMailAuthorizationFailed) {
			t.Fatalf("guess = %v", err)
		}
	}
	before := server.servedLogins()
	var retry *RetryAfterError
	if _, _, err := f.service.BindExternalMailCredentials(ctx, f.c, wrong); !errors.As(err, &retry) ||
		!errors.Is(err, ErrExternalMailCredentialsRateLimited) || retry.RetryAfter <= 0 {
		t.Fatalf("past the rate limit = %v, want a rate limit with Retry-After", err)
	}
	if after := server.servedLogins(); after != before {
		t.Fatal("a rate-limited attempt reached the server")
	}
	if _, created, err := f.service.BindExternalMailCredentials(ctx, f.b, imapInput(server, "b@imap.test")); err != nil || !created {
		t.Fatalf("another member is not limited: created %v, err %v", created, err)
	}

	// The organization's bound covers all members together: 10 + 10 + 10 attempts spend it.
	f.service.externalCredentialLimits = newExternalMailCredentialLimits(ratelimit.NewMemoryStores())
	for _, member := range []domain.OrganizationContext{f.b, f.c, f.d} {
		for range externalMailCredentialAttempts {
			if _, _, err := f.service.BindExternalMailCredentials(ctx, member, wrong); !errors.Is(err, ErrExternalMailAuthorizationFailed) {
				t.Fatalf("member %d guess = %v", member.UserID, err)
			}
		}
	}
	if _, _, err := f.service.BindExternalMailCredentials(ctx, f.admin, wrong); !errors.Is(err, ErrExternalMailCredentialsRateLimited) {
		t.Fatalf("a fresh member past the organization bound = %v, want rate limited", err)
	}
}

// TestIMAPRebindToAnotherHostRestartsBackfill: binding the same account again with a grant for
// another server starts the cursor over; the same server keeps it.
func TestIMAPRebindToAnotherHostRestartsBackfill(t *testing.T) {
	f, server := newIMAPFixture(t)
	ctx := context.Background()
	server.deliver("One", f.service.now())
	input := imapInput(server, "carol@imap.test")
	input.Host = "imap.test"
	connection, _, err := f.service.BindExternalMailCredentials(ctx, f.c, input)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	f.sync(t)
	synced, _ := f.service.externalMail.FindExternalMailConnection(ctx, accessOrg, connection.ID)
	if synced.SyncCursor == "" {
		t.Fatal("no cursor after the first sync")
	}
	if again, _, rebindErr := f.service.BindExternalMailCredentials(ctx, f.c, input); rebindErr != nil || again.SyncCursor != synced.SyncCursor {
		t.Fatalf("rebind to the same host = cursor %q, err %v; want it kept", again.SyncCursor, rebindErr)
	}
	input.Host = "other.imap.test"
	moved, created, err := f.service.BindExternalMailCredentials(ctx, f.c, input)
	if err != nil || created || moved.ID != connection.ID || moved.SyncCursor != "" {
		t.Fatalf("rebind to another host = %+v, created %v, err %v; want the cursor reset", moved, created, err)
	}
}

// TestIMAPUIDValidityChangeStoresNoSecondCopy: a mailbox rebuilt under a new UIDVALIDITY is read
// again from the backfill window, and messages already stored are recognized by their
// Message-ID.
func TestIMAPUIDValidityChangeStoresNoSecondCopy(t *testing.T) {
	f, server := newIMAPFixture(t)
	ctx := context.Background()
	server.deliver("First", f.service.now().Add(-time.Hour))
	server.deliver("Second", f.service.now().Add(-time.Minute))
	connection, _, err := f.service.BindExternalMailCredentials(ctx, f.c, imapInput(server, "carol@imap.test"))
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	f.sync(t)
	server.renumber()
	for range 3 {
		f.advance(externalMailSyncInterval)
		f.sync(t)
	}
	messages, _, err := f.service.ListMessages(ctx, f.c, connection.MailboxID, domain.MailMessageFilter{Page: 1, PerPage: 20})
	if err != nil || len(messages) != 2 {
		t.Fatalf("stored %d messages after the UIDVALIDITY change, err %v; want 2", len(messages), err)
	}
	reloaded, _ := f.service.externalMail.FindExternalMailConnection(ctx, accessOrg, connection.ID)
	if !strings.Contains(reloaded.SyncCursor, `"uidvalidity":7002`) {
		t.Fatalf("cursor = %q; want the new UIDVALIDITY", reloaded.SyncCursor)
	}
}

// servedLogins counts the LOGIN commands the server received.
func (f *fakeIMAPServer) servedLogins() int {
	commands, _, _ := f.served()
	logins := 0
	for _, command := range commands {
		if strings.Contains(command, " LOGIN ") {
			logins++
		}
	}
	return logins
}

// TestIMAPSyncStoresMessagesOnce: the sync worker reads a bound IMAP account through the adapter
// and stores each message once, with source imap.
func TestIMAPSyncStoresMessagesOnce(t *testing.T) {
	f, server := newIMAPFixture(t)
	ctx := context.Background()
	now := f.service.now()
	server.deliver("First", now.Add(-time.Hour))
	server.deliver("Second", now.Add(-time.Minute))
	connection, _, err := f.service.BindExternalMailCredentials(ctx, f.c, imapInput(server, "carol@imap.test"))
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	f.sync(t)
	f.advance(externalMailSyncInterval)
	f.sync(t)
	server.deliver("Third", now)
	f.advance(externalMailSyncInterval)
	f.sync(t)

	messages, _, err := f.service.ListMessages(ctx, f.c, connection.MailboxID, domain.MailMessageFilter{Page: 1, PerPage: 20})
	if err != nil || len(messages) != 3 {
		t.Fatalf("stored %d messages, err %v; want 3", len(messages), err)
	}
	subjects := map[string]bool{}
	for _, message := range messages {
		if message.Source != domain.MailSource("imap") || !strings.HasPrefix(message.ExternalID, "m:") ||
			message.Direction != domain.MailDirectionInbound {
			t.Fatalf("message = source %q, external id %q", message.Source, message.ExternalID)
		}
		subjects[message.Subject] = true
	}
	if !subjects["First"] || !subjects["Second"] || !subjects["Third"] {
		t.Fatalf("subjects = %v", subjects)
	}
	reloaded, _ := f.service.externalMail.FindExternalMailConnection(ctx, accessOrg, connection.ID)
	if reloaded.Status != domain.ExternalMailStatusActive || reloaded.LastErrorCode != "" ||
		reloaded.SyncCursor != `{"uidvalidity":7001,"last_uid":3}` {
		t.Fatalf("connection after sync = status %q, error %q, cursor %q", reloaded.Status, reloaded.LastErrorCode, reloaded.SyncCursor)
	}
}

// TestIMAPCredentialsHTTP: the endpoint answers 201, then 200 for a rebind, maps failures to
// their codes, and the password never appears in a response.
func TestIMAPCredentialsHTTP(t *testing.T) {
	f, server := newIMAPFixture(t)
	call := func(viewer domain.OrganizationContext, body string) (int, string, map[string]any) {
		router := accessRouter(f.service, viewer)
		router.POST("/external-mail-credentials", NewHandler(f.service).BindExternalMailCredentials)
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/external-mail-credentials", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		decoded := map[string]any{}
		_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
		if strings.Contains(recorder.Body.String(), fakeIMAPPassword) || strings.Contains(recorder.Body.String(), "guess-") {
			t.Fatalf("the response carries the password: %s", recorder.Body.String())
		}
		return recorder.Code, recorder.Body.String(), decoded
	}
	payload := func(username, password string, port int) string {
		return fmt.Sprintf(`{"provider":"imap","host":"127.0.0.1","port":%d,"username":%q,"password":%q,"backfill_days":14}`,
			port, username, password)
	}

	status, raw, body := call(f.c, payload("carol@imap.test", fakeIMAPPassword, server.port))
	data, _ := body["data"].(map[string]any)
	if status != http.StatusCreated || data["provider"] != "imap" || data["account_address"] != "carol@imap.test" || data["status"] != "active" {
		t.Fatalf("bind = %d %s", status, raw)
	}
	for _, secret := range []string{"password", "username", "host", "refresh_token", "encrypted_refresh_token", "sync_cursor"} {
		if _, leaked := data[secret]; leaked {
			t.Fatalf("the response carries %s", secret)
		}
	}
	if status, raw, _ = call(f.c, payload("carol@imap.test", fakeIMAPPassword, server.port)); status != http.StatusOK {
		t.Fatalf("rebind = %d %s", status, raw)
	}
	// A member cannot name the account: a claimed address is ignored.
	spoof := fmt.Sprintf(`{"provider":"imap","host":"127.0.0.1","port":%d,"username":"eve","password":%q,"account_address":"carol@corp.test"}`,
		server.port, fakeIMAPPassword)
	status, raw, body = call(f.d, spoof)
	data, _ = body["data"].(map[string]any)
	if status != http.StatusCreated || data["account_address"] != "eve@127.0.0.1" {
		t.Fatalf("claimed address = %d %s; want the derived eve@127.0.0.1", status, raw)
	}
	for name, want := range map[string]struct {
		viewer domain.OrganizationContext
		body   string
		status int
		code   string
	}{
		"taken":         {f.d, payload("carol@imap.test", fakeIMAPPassword, server.port), http.StatusConflict, domain.CodeExternalMailAccountTaken},
		"refused":       {f.d, payload("dave@imap.test", "guess-1", server.port), http.StatusUnprocessableEntity, domain.CodeExternalMailAuthorizationFailed},
		"unreachable":   {f.d, payload("dave@imap.test", "guess-2", closedPort(t)), http.StatusUnprocessableEntity, domain.CodeExternalMailServerUnreachable},
		"injection":     {f.d, payload("dave@imap.test\r\nZ9 LOGOUT", "guess-3", server.port), http.StatusUnprocessableEntity, domain.CodeExternalMailAuthorizationInvalid},
		"no password":   {f.d, `{"provider":"imap","host":"127.0.0.1","username":"dave@imap.test"}`, http.StatusUnprocessableEntity, domain.CodeValidationFailed},
		"long password": {f.d, payload("dave@imap.test", "guess-"+strings.Repeat("x", 400), server.port), http.StatusUnprocessableEntity, domain.CodeValidationFailed},
		"oauth":         {f.d, `{"provider":"gmail","host":"127.0.0.1","username":"d@x.test","password":"guess-4"}`, http.StatusNotFound, domain.CodeExternalMailProviderUnavailable},
	} {
		status, raw, body := call(want.viewer, want.body)
		if status != want.status || body["error_code"] != want.code {
			t.Fatalf("%s = %d %s; want %d %s", name, status, raw, want.status, want.code)
		}
	}
}
