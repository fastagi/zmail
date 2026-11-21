package mailbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
)

func (f *agentFixture) countRows(t *testing.T, table, where string, args ...any) int64 {
	t.Helper()
	var count int64
	if err := f.repo.(*repository).db.Table(table).Where(where, args...).Count(&count).Error; err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

// A handover seals the mailbox's history: its agent drafts, which quote that mail, are deleted in
// the same transaction, so the successor never reads them.
func TestAgentDraftsEndWithHandover(t *testing.T) {
	f := newAgentFixture(t)
	ctx := context.Background()
	f.mustGrant(t, f.b, f.personal, "b", func(in *IssueAgentGrantInput) { in.Capabilities = []string{"read", "draft"} })
	message := f.deliver(t, f.personal, "alice@sender.test", "Private", "Quote me", "d1")
	if _, _, err := f.service.SaveAgentDraft(ctx, f.b, f.keys["b"], AgentDraftInput{MessageID: message.ID, Text: "Re: Quote me"}); err != nil {
		t.Fatalf("SaveAgentDraft: %v", err)
	}
	f.advance(time.Minute)
	if _, err := f.service.HandOverMailbox(ctx, f.admin, f.personal.ID, accessC); err != nil {
		t.Fatalf("HandOverMailbox: %v", err)
	}
	if left := f.countRows(t, "mail_agent_drafts", "mailbox_id = ?", f.personal.ID); left != 0 {
		t.Fatalf("%d drafts survived the handover", left)
	}
	drafts, total, err := f.service.ListAgentDrafts(ctx, f.c, f.personal.ID, 1, 15)
	if err != nil || total != 0 || len(drafts) != 0 {
		t.Fatalf("successor lists drafts = %d %v %v", total, drafts, err)
	}
}

// A departing owner's mailbox loses its drafts too: nobody may read it until a handover.
func TestAgentDraftsEndWhenOwnerLeaves(t *testing.T) {
	f := newAgentFixture(t)
	ctx := context.Background()
	f.mustGrant(t, f.b, f.personal, "b", func(in *IssueAgentGrantInput) { in.Capabilities = []string{"read", "draft"} })
	message := f.deliver(t, f.personal, "alice@sender.test", "Private", "Quote me", "d2")
	if _, _, err := f.service.SaveAgentDraft(ctx, f.b, f.keys["b"], AgentDraftInput{MessageID: message.ID, Text: "Re"}); err != nil {
		t.Fatalf("SaveAgentDraft: %v", err)
	}
	f.memberships.absent[accessB] = true
	if err := f.service.MemberRemoved(ctx, domain.NewOrganizationMemberRemovedEvent(accessOrg, accessB, f.b.MembershipID, f.service.now())); err != nil {
		t.Fatalf("MemberRemoved: %v", err)
	}
	if left := f.countRows(t, "mail_agent_drafts", "mailbox_id = ?", f.personal.ID); left != 0 {
		t.Fatalf("%d drafts survived the owner's departure", left)
	}
}

// Defense in depth: a draft whose message is sealed or deleted is never listed, even if a row
// were left behind.
func TestAgentDraftListHidesSealedAndDeletedMail(t *testing.T) {
	f := newAgentFixture(t)
	ctx := context.Background()
	f.mustGrant(t, f.e, f.team, "e", func(in *IssueAgentGrantInput) { in.Capabilities = []string{"read", "draft"} })
	sealed := f.deliver(t, f.team, "a@client.test", "Old", "old", "s1")
	deleted := f.deliver(t, f.team, "b@client.test", "Gone", "gone", "s2")
	for _, message := range []*domain.MailMessage{sealed, deleted} {
		if _, _, err := f.service.SaveAgentDraft(ctx, f.e, f.keys["e"], AgentDraftInput{MessageID: message.ID, Text: "Re"}); err != nil {
			t.Fatalf("SaveAgentDraft: %v", err)
		}
	}
	f.advance(time.Minute)
	fresh := f.deliver(t, f.team, "c@client.test", "New", "new", "s3")
	if _, _, err := f.service.SaveAgentDraft(ctx, f.e, f.keys["e"], AgentDraftInput{MessageID: fresh.ID, Text: "Re"}); err != nil {
		t.Fatalf("SaveAgentDraft: %v", err)
	}
	db := f.repo.(*repository).db
	if err := db.Exec(`UPDATE mailboxes SET sealed_before = ? WHERE id = ?`, fresh.CreatedAt, f.team.ID).Error; err != nil {
		t.Fatalf("seal: %v", err)
	}
	if err := db.Exec(`UPDATE mail_messages SET created_at = ? WHERE id = ?`, fresh.CreatedAt, deleted.ID).Error; err != nil {
		t.Fatalf("move deleted message past the seal: %v", err)
	}
	if err := db.Exec(`DELETE FROM mail_messages WHERE id = ?`, deleted.ID).Error; err != nil {
		t.Fatalf("delete message: %v", err)
	}
	drafts, total, err := f.service.ListAgentDrafts(ctx, f.b, f.team.ID, 1, 15)
	if err != nil || total != 1 || len(drafts) != 1 || drafts[0].MessageID != fresh.ID {
		t.Fatalf("drafts = %d %+v %v; want only the draft of the unsealed message", total, drafts, err)
	}
}

// A key of anyone but the issuer gets one answer, whatever is wrong with it, so issuing is no
// oracle for other organizations' keys or colleagues' key settings; the issuer's own keys get
// reasons.
func TestAgentGrantKeyRefusalsAreUndifferentiated(t *testing.T) {
	f := newAgentFixture(t)
	f.addKey(t, "c-test", accessC, domain.APIKeyModeTest, ScopeMailAgent)
	f.addKey(t, "c-sender", accessC, domain.APIKeyModeLive, ScopeMailSend)
	f.addKey(t, "outsider", notMember, domain.APIKeyModeLive, ScopeMailAgent)
	f.addKey(t, "c-revoked", accessC, domain.APIKeyModeLive, ScopeMailAgent)
	if err := f.repo.(*repository).db.Exec(`UPDATE api_keys SET revoked_at = now() WHERE id = ?`, f.keys["c-revoked"]).Error; err != nil {
		t.Fatalf("revoke: %v", err)
	}
	f.keys["unknown"] = 987654
	for _, key := range []string{"unknown", "outsider", "c", "c-test", "c-sender", "c-revoked"} {
		_, err := f.grant(f.b, f.personal, key, nil)
		if err == nil || err.Error() != errForeignAgentKey.Error() {
			t.Errorf("key %s: error = %v; want the undifferentiated %v", key, err, errForeignAgentKey)
		}
	}
	_, err := f.grant(f.b, f.personal, "b-sender", nil)
	if !errors.Is(err, ErrMailAgentGrantInvalid) || err.Error() == errForeignAgentKey.Error() {
		t.Fatalf("own key without scope = %v; want its own reason", err)
	}
}

// Grants and audit rows from before a mailbox's seal boundary are hidden like sealed mail.
func TestAgentGrantMetadataHidesSealedEra(t *testing.T) {
	f := newAgentFixture(t)
	ctx := context.Background()
	old := f.mustGrant(t, f.b, f.personal, "b", nil)
	message := f.deliver(t, f.personal, "alice@sender.test", "Old", "body", "e1")
	if _, err := f.service.AgentMailMessage(ctx, f.b, f.keys["b"], message.ID); err != nil {
		t.Fatalf("read: %v", err)
	}
	f.advance(time.Minute)
	if _, err := f.service.HandOverMailbox(ctx, f.admin, f.personal.ID, accessC); err != nil {
		t.Fatalf("HandOverMailbox: %v", err)
	}
	f.advance(time.Minute)
	if _, err := f.service.SetMailboxMember(ctx, f.admin, f.personal.ID, accessAdmin, domain.MailboxPermissionManage); err != nil {
		t.Fatalf("admin joins: %v", err)
	}
	fresh := f.mustGrant(t, f.admin, f.personal, "admin", nil)
	grants, total, err := f.service.ListAgentGrants(ctx, f.admin, f.personal.ID, 1, 15)
	if err != nil || total != 1 || len(grants) != 1 || grants[0].ID != fresh.ID {
		t.Fatalf("grants after the seal = %d %+v %v; want only the new one", total, grants, err)
	}
	_, err = f.service.AgentGrantActivity(ctx, f.admin, f.personal.ID, old.ID)
	expectError(t, "activity of a sealed-era grant", err, ErrMailAgentGrantNotFound)
	err = f.service.RevokeAgentGrant(ctx, f.admin, f.personal.ID, old.ID)
	expectError(t, "revoking a sealed-era grant", err, ErrMailAgentGrantNotFound)
	audit, err := f.service.agents.ListAgentAudit(ctx, old.ID, &fresh.CreatedAt, 100)
	if err != nil || len(audit) != 0 {
		t.Fatalf("sealed-era audit = %+v %v", audit, err)
	}
}

// A grant whose key stopped working, or whose key owner lost the mailbox, writes no events and
// holds no live slot, without anything revoking it.
func TestAgentGrantStopsAtWriteTime(t *testing.T) {
	f := newAgentFixture(t)
	ctx := context.Background()
	db := f.repo.(*repository).db
	byB := f.mustGrant(t, f.b, f.personal, "b", nil)
	byC := f.mustGrant(t, f.e, f.team, "c", nil)

	for name, stop := range map[string]string{
		"revoked key": `UPDATE api_keys SET revoked_at = now() WHERE id = ?`,
		"deleted key": `UPDATE api_keys SET deleted_at = now() WHERE id = ?`,
		"expired key": `UPDATE api_keys SET expires_at = now() - interval '1 minute' WHERE id = ?`,
	} {
		key := f.addKey2(t, name, accessB)
		grant := f.mustGrant(t, f.b, f.mailbox, name, nil)
		if err := db.Exec(stop, key).Error; err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		live, err := f.service.agents.ListLiveAgentGrantsOfMailbox(ctx, f.mailbox.ID, f.service.now())
		if err != nil {
			t.Fatalf("ListLiveAgentGrantsOfMailbox: %v", err)
		}
		for _, other := range live {
			if other.ID == grant.ID {
				t.Fatalf("%s: grant still receives mail", name)
			}
		}
	}
	// C loses team@ without any revocation being written.
	if _, err := f.repo.DeleteMailboxMember(ctx, f.team.ID, accessC); err != nil {
		t.Fatalf("delete member row: %v", err)
	}
	team := f.deliver(t, f.team, "a@client.test", "Team", "body", "w1")
	personal := f.deliver(t, f.personal, "a@client.test", "Mine", "body", "w2")
	if n := f.countRows(t, "mail_agent_events", "grant_id = ?", byC.ID); n != 0 {
		t.Fatalf("a grant whose owner lost the mailbox received %d events", n)
	}
	if n := f.countRows(t, "mail_agent_events", "grant_id = ? AND message_id = ?", byB.ID, personal.ID); n != 1 {
		t.Fatalf("a working grant received %d events", n)
	}
	_ = team

	// Ten grants fill the slots; one whose key is revoked frees its slot.
	keys := make([]uint, 0, 10)
	for i := 1; i < 10; i++ {
		name := fmt.Sprintf("slot-%d", i)
		keys = append(keys, f.addKey2(t, name, accessB))
		f.mustGrant(t, f.b, f.personal, name, nil)
	}
	f.addKey2(t, "slot-extra", accessB)
	_, err := f.grant(f.b, f.personal, "slot-extra", nil)
	expectError(t, "eleventh grant", err, ErrMailAgentGrantLimit)
	if err := db.Exec(`UPDATE api_keys SET revoked_at = now() WHERE id = ?`, keys[0]).Error; err != nil {
		t.Fatalf("revoke: %v", err)
	}
	f.mustGrant(t, f.b, f.personal, "slot-extra", nil)
}

// addKey2 stores a live mail:agent key of a user and returns its id.
func (f *agentFixture) addKey2(t *testing.T, name string, userID uint) uint {
	t.Helper()
	f.addKey(t, name, userID, domain.APIKeyModeLive, ScopeMailAgent)
	return f.keys[name]
}

// Events past their retention and audit rows past theirs go in bounded batches.
func TestPruneExpiredAgentMail(t *testing.T) {
	f := newAgentFixture(t)
	ctx := context.Background()
	f.mustGrant(t, f.b, f.personal, "b", func(in *IssueAgentGrantInput) { in.ExpiresAt = f.service.now().Add(89 * 24 * time.Hour) })
	for i := 0; i < 3; i++ {
		message := f.deliver(t, f.personal, "alice@sender.test", fmt.Sprintf("Old %d", i), "body", fmt.Sprintf("o%d", i))
		if _, err := f.service.AgentMailMessage(ctx, f.b, f.keys["b"], message.ID); err != nil {
			t.Fatalf("read: %v", err)
		}
	}
	f.advance(31 * 24 * time.Hour)
	recent := f.deliver(t, f.personal, "alice@sender.test", "Recent", "body", "r")

	pruned, err := f.service.PruneExpiredAgentMail(ctx, f.service.now(), 2)
	if err != nil || pruned != 2 {
		t.Fatalf("first batch = %d %v; want 2", pruned, err)
	}
	pruned, err = f.service.PruneExpiredAgentMail(ctx, f.service.now(), 10)
	if err != nil || pruned != 1 {
		t.Fatalf("second batch = %d %v; want the last old event", pruned, err)
	}
	expectMessages(t, "events left", feedMessages(f.feed(t, f.b, "b", 0, 50)), recent.ID)
	if n := f.countRows(t, "mail_agent_audit", "1 = 1"); n != 3 {
		t.Fatalf("audit rows = %d; want the 3 reads kept for 90 days", n)
	}
	f.advance(60 * 24 * time.Hour)
	if pruned, err = f.service.PruneExpiredAgentMail(ctx, f.service.now(), 10); err != nil || pruned < 3 {
		t.Fatalf("after 91 days = %d %v; want the old audit rows gone", pruned, err)
	}
	if n := f.countRows(t, "mail_agent_audit", "1 = 1"); n != 0 {
		t.Fatalf("audit rows after retention = %d", n)
	}
}

// Each key gets a bounded number of agent reads per minute; past it the routes answer 429 with
// Retry-After.
func TestAgentReadsAreRateLimitedPerKey(t *testing.T) {
	f := newAgentFixture(t)
	f.service.agentReads = newAgentReadLimit(nil, Settings{MailAgent: configMailAgent(2)})
	f.mustGrant(t, f.b, f.personal, "b", nil)
	f.mustGrant(t, f.e, f.team, "c", nil)
	engine := f.agentRouter()
	for i := 0; i < 2; i++ {
		if recorder, _ := f.call(engine, http.MethodGet, "/v1/agent-mail-events", "b", ""); recorder.Code != http.StatusOK {
			t.Fatalf("read %d = %d", i, recorder.Code)
		}
	}
	recorder, body := f.call(engine, http.MethodGet, "/v1/agent-mail-events", "b", "")
	if recorder.Code != http.StatusTooManyRequests || body["error_code"] != domain.CodeRateLimited || recorder.Header().Get("Retry-After") == "" {
		t.Fatalf("third read = %d %v Retry-After %q", recorder.Code, body, recorder.Header().Get("Retry-After"))
	}
	if recorder, _ = f.call(engine, http.MethodGet, "/v1/agent-mail-messages/1", "b", ""); recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("message read past the limit = %d", recorder.Code)
	}
	if recorder, _ = f.call(engine, http.MethodGet, "/v1/agent-mail-events", "c", ""); recorder.Code != http.StatusOK {
		t.Fatalf("another key = %d; limits are per key", recorder.Code)
	}
}

func configMailAgent(readsPerMinute int) config.MailAgentConfig {
	return config.MailAgentConfig{ReadsPerMinute: readsPerMinute}
}
