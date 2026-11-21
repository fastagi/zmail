package mailbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
)

// runs lists the fixture connection's sync runs as its owner, newest first.
func (f *externalFixture) runs(t *testing.T) []*domain.ExternalMailSyncRun {
	t.Helper()
	runs, err := f.service.ListExternalMailSyncRuns(context.Background(), f.b, f.connection.ID)
	if err != nil {
		t.Fatalf("ListExternalMailSyncRuns: %v", err)
	}
	return runs
}

func (f *externalFixture) count(t *testing.T, query string, args ...any) int64 {
	t.Helper()
	var count int64
	if err := f.repo.(*repository).db.Raw(query, args...).Scan(&count).Error; err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return count
}

// addConnection binds another outside account of B's to its own external mailbox, due now.
func (f *externalFixture) addConnection(t *testing.T, provider domain.ExternalMailProvider, token string) *domain.ExternalMailConnection {
	t.Helper()
	ctx := context.Background()
	owner := uint(accessB)
	mailbox := &domain.Mailbox{
		OrganizationID: accessOrg, CreatedBy: accessB, Address: token + "@" + string(provider) + ".example",
		Type: domain.MailboxTypeExternal, OwnerUserID: &owner,
	}
	if err := f.repo.CreateMailbox(ctx, mailbox, domain.MailboxesPerOrganizationCeiling); err != nil {
		t.Fatalf("create external mailbox: %v", err)
	}
	sealed, err := f.service.sealRefreshToken(token)
	if err != nil {
		t.Fatalf("seal token: %v", err)
	}
	connection := &domain.ExternalMailConnection{
		OrganizationID: accessOrg, MailboxID: mailbox.ID, Provider: provider, AccountAddress: mailbox.Address,
		OwnerUserID: &owner, EncryptedRefreshToken: sealed, NextSyncAt: f.service.now().UTC(),
	}
	if err := f.service.externalMail.CreateExternalMailConnection(ctx, connection); err != nil {
		t.Fatalf("create connection: %v", err)
	}
	return connection
}

// TestExternalMailSyncRecordsARunPerOutcome: every fetch leaves one run with its outcome, counts,
// and error code, newest first.
func TestExternalMailSyncRecordsARunPerOutcome(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	f.source.Deliver(externalTestToken, externalMessage("g-1", "One", time.Time{}))
	f.sync(t) // ok

	f.source.ExpireCursor(externalTestToken)
	f.advance(externalMailSyncInterval)
	f.sync(t) // cursor_expired

	f.service.externalConnectors = fakeExternalMailRegistry(failingSource{f.source})
	f.advance(externalMailSyncInterval)
	f.sync(t) // fetch_failed

	f.service.externalConnectors = &externalMailRegistry{}
	f.advance(externalMailMaxBackoff)
	f.sync(t) // provider_unavailable

	f.service.externalConnectors = fakeExternalMailRegistry(f.source)
	f.source.FailAuth(externalTestToken)
	f.advance(externalMailMaxBackoff)
	f.sync(t) // auth_failed

	runs := f.runs(t)
	want := []struct {
		outcome   domain.ExternalMailSyncOutcome
		errorCode string
		fetched   int
		stored    int
	}{
		{domain.ExternalMailSyncAuthFailed, domain.ExternalMailErrorAuthFailed, 0, 0},
		{domain.ExternalMailSyncProviderUnavailable, domain.ExternalMailErrorProviderUnavailable, 0, 0},
		{domain.ExternalMailSyncFetchFailed, domain.ExternalMailErrorFetchFailed, 0, 0},
		{domain.ExternalMailSyncCursorExpired, "", 0, 0},
		{domain.ExternalMailSyncOK, "", 1, 1},
	}
	if len(runs) != len(want) {
		t.Fatalf("recorded %d runs, want %d", len(runs), len(want))
	}
	for i, run := range runs {
		if run.Outcome != want[i].outcome || run.ErrorCode != want[i].errorCode || run.Fetched != want[i].fetched ||
			run.Stored != want[i].stored || run.ConnectionID != f.connection.ID || run.FinishedAt.Before(run.StartedAt) {
			t.Fatalf("run %d = %+v, want %+v", i, run, want[i])
		}
		if i > 0 && run.ID >= runs[i-1].ID {
			t.Fatalf("runs are not newest first: %d after %d", run.ID, runs[i-1].ID)
		}
	}

	// A revoke that lands mid-fetch drops the page and says so.
	dropped := newExternalFixture(t, config.ExternalMailSourceFake)
	dropped.source.Deliver(externalTestToken, externalMessage("g-1", "One", time.Time{}))
	dropped.service.externalConnectors = fakeExternalMailRegistry(revokingSource{FakeExternalMailSource: dropped.source, revoke: func() {
		if err := dropped.service.RevokeExternalMailConnection(context.Background(), dropped.b, dropped.connection.ID); err != nil {
			t.Errorf("revoke during fetch: %v", err)
		}
	}})
	dropped.sync(t)
	if runs := dropped.runs(t); len(runs) != 1 || runs[0].Outcome != domain.ExternalMailSyncDropped || runs[0].Fetched != 1 || runs[0].Stored != 0 {
		t.Fatalf("runs after a revoke mid-fetch = %+v, want one dropped run that fetched 1 and stored 0", runs)
	}
}

// TestExternalMailSyncRunsArePruned: a connection keeps its latest 50 runs, and a pass deletes runs
// older than 14 days.
func TestExternalMailSyncRunsArePruned(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	db := f.repo.(*repository).db
	now := f.service.now().UTC()
	for i := range domain.MaxExternalMailSyncRuns + 7 {
		run := &domain.ExternalMailSyncRun{
			ConnectionID: f.connection.ID, StartedAt: now.Add(time.Duration(i) * time.Second),
			FinishedAt: now.Add(time.Duration(i) * time.Second), Outcome: domain.ExternalMailSyncOK,
		}
		if err := recordExternalMailSyncRun(db, run); err != nil {
			t.Fatalf("record run %d: %v", i, err)
		}
	}
	runs := f.runs(t)
	if len(runs) != domain.MaxExternalMailSyncRuns || f.count(t, `SELECT count(*) FROM external_mail_sync_runs`) != domain.MaxExternalMailSyncRuns {
		t.Fatalf("kept %d runs, want %d", len(runs), domain.MaxExternalMailSyncRuns)
	}
	if !runs[0].StartedAt.Equal(now.Add(time.Duration(domain.MaxExternalMailSyncRuns+6) * time.Second)) {
		t.Fatalf("newest kept run started at %v, want the last one recorded", runs[0].StartedAt)
	}

	// Fifteen days later a pass prunes them all; the fetch of that pass leaves one fresh run.
	f.advance(15 * 24 * time.Hour)
	f.sync(t)
	if runs := f.runs(t); len(runs) != 1 || !runs[0].StartedAt.Equal(f.service.now().UTC()) {
		t.Fatalf("after the retention: %d runs, want only the fresh one", len(runs))
	}
}

// TestExternalMailBackoffSchedule: each failed fetch doubles the wait from the sync interval up to
// an hour, less at most a fifth of jitter, and a success resets it.
func TestExternalMailBackoffSchedule(t *testing.T) {
	none := func(time.Duration) time.Duration { return 0 }
	for failures, want := range map[int]time.Duration{
		1: 2 * time.Minute, 2: 4 * time.Minute, 3: 8 * time.Minute, 5: 32 * time.Minute, 6: time.Hour, 40: time.Hour,
	} {
		if got := externalMailBackoff(failures, none); got != want {
			t.Fatalf("backoff after %d failures = %v, want %v", failures, got, want)
		}
	}
	for range 100 {
		if got := externalMailBackoff(3, externalMailJitter); got <= 8*time.Minute*4/5 || got > 8*time.Minute {
			t.Fatalf("jittered backoff = %v, want within (6m24s, 8m]", got)
		}
	}

	f := newExternalFixture(t, config.ExternalMailSourceFake)
	f.service.externalConnectors = fakeExternalMailRegistry(failingSource{f.source})
	for failures, base := range []time.Duration{2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute, 32 * time.Minute, time.Hour, time.Hour} {
		if handled := f.sync(t); handled != 1 {
			t.Fatalf("failure %d: the connection was not due (%d handled)", failures+1, handled)
		}
		connection := f.reload(t)
		wait := connection.NextSyncAt.Sub(f.service.now().UTC())
		if connection.ConsecutiveFailures != failures+1 || wait > base || wait <= base*4/5 {
			t.Fatalf("after failure %d: failures %d, next in %v; want %d and within (%v, %v]",
				failures+1, connection.ConsecutiveFailures, wait, failures+1, base*4/5, base)
		}
		if handled := f.sync(t); handled != 0 {
			t.Fatal("a backing-off connection was fetched before its time")
		}
		f.advance(wait)
	}

	f.service.externalConnectors = fakeExternalMailRegistry(f.source)
	f.sync(t)
	connection := f.reload(t)
	if connection.ConsecutiveFailures != 0 || connection.LastErrorCode != "" ||
		!connection.NextSyncAt.Equal(f.service.now().UTC().Add(externalMailSyncInterval)) {
		t.Fatalf("after a success: failures %d, error %q, next %v; want reset to the interval",
			connection.ConsecutiveFailures, connection.LastErrorCode, connection.NextSyncAt)
	}
}

// TestExternalMailBudgetIsFairAcrossProviders: one provider with many due connections takes no more
// than its share of a pass, and the pass serves every provider round-robin.
func TestExternalMailBudgetIsFairAcrossProviders(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	gmail := []string{externalTestToken}
	for i := range 5 {
		token := fmt.Sprintf("gmail-%d", i)
		f.addConnection(t, domain.ExternalMailProviderGmail, token)
		gmail = append(gmail, token)
	}
	f.addConnection(t, domain.ExternalMailProviderOutlook, "outlook-0")
	fetched := func(tokens ...string) int {
		total := 0
		for _, token := range tokens {
			total += f.source.Fetches(token)
		}
		return total
	}

	// A pass smaller than the backlog takes the first of each provider before a second of any.
	if handled, err := f.service.SyncExternalMail(context.Background(), 2); err != nil || handled != 2 {
		t.Fatalf("SyncExternalMail(2) = %d, %v", handled, err)
	}
	if fetched(gmail...) != 1 || fetched("outlook-0") != 1 {
		t.Fatalf("a pass of 2 fetched gmail %d and outlook %d, want 1 each", fetched(gmail...), fetched("outlook-0"))
	}

	// The per-provider cap holds Gmail back even when the batch has room.
	f.service.settings.ExternalMail.Sync.ProviderFetchesPerPass = 2
	f.advance(externalMailSyncInterval)
	if handled := f.sync(t); handled != 3 {
		t.Fatalf("a capped pass handled %d connections, want 2 gmail and 1 outlook", handled)
	}
	if fetched(gmail...) != 3 || fetched("outlook-0") != 2 {
		t.Fatalf("after the capped pass: gmail %d, outlook %d; want 3 and 2", fetched(gmail...), fetched("outlook-0"))
	}
	// The connections left behind are still due and go first next pass.
	if handled := f.sync(t); handled != 2 {
		t.Fatalf("the next pass handled %d connections, want the 2 gmail left behind", handled)
	}
}

// TestExternalMailRetentionPurgesRevokedCopies: copies of a revoked connection stay until the
// retention ends, then the worker deletes them, the mailbox, and the connection, records the purge,
// and leaves every other mailbox alone.
func TestExternalMailRetentionPurgesRevokedCopies(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	f.source.Deliver(externalTestToken, externalMessage("g-1", "One", time.Time{}), externalMessage("g-2", "Two", time.Time{}))
	other := f.addConnection(t, domain.ExternalMailProviderGmail, "other-account")
	f.source.Deliver("other-account", externalMessage("o-1", "Kept", time.Time{}))
	f.sync(t)
	retention := 48 * time.Hour
	f.service.settings.ExternalMail.Sync.RevokedRetention = retention

	ctx := context.Background()
	if err := f.service.RevokeExternalMailConnection(ctx, f.admin, f.connection.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	revokedAt := f.reload(t).RevokedAt
	if revokedAt == nil || !revokedAt.Equal(f.service.now().UTC()) {
		t.Fatalf("revoked_at = %v, want the revoke time", revokedAt)
	}
	// A second revoke does not move the deadline.
	f.advance(time.Hour)
	if err := f.service.externalMail.RevokeExternalMailConnection(ctx, accessOrg, f.connection.ID, f.service.now().UTC()); err != nil {
		t.Fatalf("revoke again: %v", err)
	}
	if again := f.reload(t).RevokedAt; again == nil || !again.Equal(*revokedAt) {
		t.Fatalf("revoked_at moved to %v", again)
	}

	f.advance(retention - time.Hour - time.Minute)
	f.sync(t)
	if stored := len(f.storedExternal(t)); stored != 2 {
		t.Fatalf("before the deadline the mailbox holds %d copies, want 2", stored)
	}

	f.advance(2 * time.Minute)
	f.sync(t)
	if left := f.count(t, `SELECT count(*) FROM mail_messages WHERE mailbox_id = ?`, f.mailbox.ID); left != 0 {
		t.Fatalf("%d copies left after the deadline", left)
	}
	if _, err := f.repo.FindMailbox(ctx, accessOrg, f.mailbox.ID); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("the purged mailbox is still found: %v", err)
	}
	if _, err := f.service.externalMail.FindExternalMailConnection(ctx, accessOrg, f.connection.ID); !errors.Is(err, ErrExternalMailConnectionNotFound) {
		t.Fatalf("the purged connection is still found: %v", err)
	}
	var detail string
	if err := f.repo.(*repository).db.Raw(`SELECT detail::text FROM mailbox_access_events WHERE mailbox_id = ? AND type = ? AND actor_user_id IS NULL AND subject_user_id = ?`,
		f.mailbox.ID, string(domain.MailboxAccessCopiesPurged), accessB).Scan(&detail).Error; err != nil || detail == "" {
		t.Fatalf("copies_purged event = %q, %v", detail, err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(detail), &decoded); err != nil || decoded["reason"] != "retention" || decoded["connection_id"] != float64(f.connection.ID) {
		t.Fatalf("copies_purged detail = %s", detail)
	}

	// Other mailboxes keep their mail: B's personal mailbox, the team mailbox, B's other account.
	for name, mailboxID := range map[string]uint{"personal": f.personal.ID, "team": f.team.ID, "other external": other.MailboxID} {
		if f.count(t, `SELECT count(*) FROM mail_messages WHERE mailbox_id = ?`, mailboxID) == 0 {
			t.Fatalf("the %s mailbox lost its mail", name)
		}
	}
	if _, err := f.service.externalMail.FindExternalMailConnection(ctx, accessOrg, other.ID); err != nil {
		t.Fatalf("the other connection: %v", err)
	}
}

// TestExternalMailLeaverRevokeStartsRetention: a member who leaves has every connection revoked
// with revoked_at set, so the copies age out like any other revoke.
func TestExternalMailLeaverRevokeStartsRetention(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	f.sync(t)
	if err := f.service.revokeConnectionsOfLeaver(context.Background(), accessOrg, accessB); err != nil {
		t.Fatalf("revokeConnectionsOfLeaver: %v", err)
	}
	if connection := f.reload(t); connection.Status != domain.ExternalMailStatusRevoked || connection.RevokedAt == nil {
		t.Fatalf("after leaving: status %q, revoked_at %v", connection.Status, connection.RevokedAt)
	}
}

// TestExternalMailRebindCancelsThePurge: binding the account again before the deadline clears
// revoked_at, and nothing is deleted.
func TestExternalMailRebindCancelsThePurge(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	f.source.Deliver(externalTestToken, externalMessage("g-1", "One", time.Time{}))
	f.sync(t)
	ctx := context.Background()
	if err := f.service.RevokeExternalMailConnection(ctx, f.b, f.connection.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	f.advance(time.Hour)
	sealed, err := f.service.sealRefreshToken("refresh-b-again")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	rebound, err := f.service.externalMail.RebindExternalMailConnection(ctx, domain.ExternalMailRebind{
		ConnectionID: f.connection.ID, EncryptedRefreshToken: sealed, ResetCursor: true, Now: f.service.now().UTC(),
	})
	if err != nil || rebound.RevokedAt != nil || rebound.Status != domain.ExternalMailStatusActive {
		t.Fatalf("rebind = %+v, %v; want active without revoked_at", rebound, err)
	}
	f.advance(config.DefaultExternalMailRevokedRetention + time.Hour)
	f.sync(t)
	if stored := len(f.storedExternal(t)); stored != 1 {
		t.Fatalf("after the old deadline the rebound mailbox holds %d copies, want 1", stored)
	}
	if purge, err := f.service.PurgeExternalMailCopies(ctx, f.b, f.connection.ID); !errors.Is(err, ErrExternalMailConnectionNotRevoked) {
		t.Fatalf("purging a rebound connection = %+v, %v; want not revoked", purge, err)
	}
}

func (f *externalFixture) opsCall(t *testing.T, viewer domain.OrganizationContext, method, path string) (int, map[string]any) {
	t.Helper()
	router := accessRouter(f.service, viewer)
	handler := NewHandler(f.service)
	router.GET("/external-mail-connections/:connection_id/sync-runs", handler.ListExternalMailSyncRuns)
	router.POST("/external-mail-connections/:connection_id/purges", handler.PurgeExternalMailCopies)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(method, path, nil))
	decoded := map[string]any{}
	_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
	return recorder.Code, decoded
}

// TestExternalMailImmediatePurge: only the member who bound the account purges its copies at once,
// and only after revoking it; a large mailbox is finished by the worker.
func TestExternalMailImmediatePurge(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	for i := range 3 {
		f.source.Deliver(externalTestToken, externalMessage(fmt.Sprintf("g-%d", i), "Mail", time.Time{}))
	}
	f.sync(t)
	path := fmt.Sprintf("/external-mail-connections/%d/purges", f.connection.ID)

	status, body := f.opsCall(t, f.b, http.MethodPost, path)
	if status != http.StatusConflict || body["error_code"] != domain.CodeExternalMailConnectionNotRevoked {
		t.Fatalf("purging a live connection = %d %v", status, body)
	}
	if err := f.service.RevokeExternalMailConnection(context.Background(), f.b, f.connection.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	for name, viewer := range map[string]domain.OrganizationContext{"colleague": f.c, "admin": f.admin} {
		status, body := f.opsCall(t, viewer, http.MethodPost, path)
		if status != http.StatusNotFound || body["error_code"] != domain.CodeExternalMailConnectionNotFound {
			t.Fatalf("%s purging = %d %v", name, status, body)
		}
	}
	if left := f.count(t, `SELECT count(*) FROM mail_messages WHERE mailbox_id = ?`, f.mailbox.ID); left != 3 {
		t.Fatalf("refused purges deleted copies: %d left", left)
	}

	status, body = f.opsCall(t, f.b, http.MethodPost, path)
	data, _ := body["data"].(map[string]any)
	if status != http.StatusOK || data["deleted_messages"] != float64(3) || data["completed"] != true {
		t.Fatalf("owner purging = %d %v", status, body)
	}
	if _, err := f.repo.FindMailbox(context.Background(), accessOrg, f.mailbox.ID); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("the purged mailbox is still found: %v", err)
	}
	var reason string
	f.repo.(*repository).db.Raw(`SELECT detail->>'reason' FROM mailbox_access_events WHERE mailbox_id = ? AND type = ? AND actor_user_id = ?`,
		f.mailbox.ID, string(domain.MailboxAccessCopiesPurged), accessB).Scan(&reason)
	if reason != "owner" {
		t.Fatalf("copies_purged reason = %q, want owner", reason)
	}
	if status, body = f.opsCall(t, f.b, http.MethodPost, path); status != http.StatusNotFound {
		t.Fatalf("purging again = %d %v, want 404", status, body)
	}
}

// TestExternalMailImmediatePurgeHandsTheRestToTheWorker: more copies than one request deletes are
// made due at once and finished by the next worker pass.
func TestExternalMailImmediatePurgeHandsTheRestToTheWorker(t *testing.T) {
	previous := externalMailPurgeBatch
	externalMailPurgeBatch = 1
	t.Cleanup(func() { externalMailPurgeBatch = previous })

	f := newExternalFixture(t, config.ExternalMailSourceFake)
	for i := range externalMailImmediatePurgeSteps + 2 {
		f.source.Deliver(externalTestToken, externalMessage(fmt.Sprintf("g-%d", i), "Mail", time.Time{}))
	}
	f.sync(t)
	if err := f.service.RevokeExternalMailConnection(context.Background(), f.b, f.connection.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	status, body := f.opsCall(t, f.b, http.MethodPost, fmt.Sprintf("/external-mail-connections/%d/purges", f.connection.ID))
	data, _ := body["data"].(map[string]any)
	if status != http.StatusAccepted || data["deleted_messages"] != float64(externalMailImmediatePurgeSteps) || data["completed"] != false {
		t.Fatalf("owner purging a large mailbox = %d %v", status, body)
	}
	f.sync(t)
	if left := f.count(t, `SELECT count(*) FROM mail_messages WHERE mailbox_id = ?`, f.mailbox.ID); left != 0 {
		t.Fatalf("the worker left %d copies", left)
	}
	if _, err := f.service.externalMail.FindExternalMailConnection(context.Background(), accessOrg, f.connection.ID); !errors.Is(err, ErrExternalMailConnectionNotFound) {
		t.Fatalf("the connection survived the purge: %v", err)
	}
}

// TestExternalMailSyncRunsHTTP: the owner reads the runs; anyone else, managers included, gets 404.
func TestExternalMailSyncRunsHTTP(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	f.source.Deliver(externalTestToken, externalMessage("g-1", "One", time.Time{}))
	f.sync(t)
	path := fmt.Sprintf("/external-mail-connections/%d/sync-runs", f.connection.ID)

	status, body := f.opsCall(t, f.b, http.MethodGet, path)
	items, _ := body["data"].([]any)
	if status != http.StatusOK || len(items) != 1 {
		t.Fatalf("owner listing runs = %d %v", status, body)
	}
	item := items[0].(map[string]any)
	for key, want := range map[string]any{"outcome": "ok", "fetched_count": float64(1), "stored_count": float64(1), "more": false, "error_code": nil} {
		if item[key] != want {
			t.Fatalf("run[%s] = %v, want %v", key, item[key], want)
		}
	}
	for _, key := range []string{"id", "started_at", "finished_at"} {
		if item[key] == nil {
			t.Fatalf("run misses %s", key)
		}
	}
	for name, viewer := range map[string]domain.OrganizationContext{"colleague": f.c, "admin": f.admin} {
		if status, body := f.opsCall(t, viewer, http.MethodGet, path); status != http.StatusNotFound ||
			body["error_code"] != domain.CodeExternalMailConnectionNotFound {
			t.Fatalf("%s listing runs = %d %v", name, status, body)
		}
	}
}
