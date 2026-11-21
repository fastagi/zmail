package mailbox

import (
	"context"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// syncOne delivers one message to B's connected mailbox through a real sync and returns it.
func (f *agentFixture) syncOne(t *testing.T, id, subject string) *domain.MailMessage {
	t.Helper()
	f.source.Deliver(externalTestToken, externalMessage(id, subject, f.service.now().Add(-time.Minute).UTC()))
	f.advance(externalMailSyncInterval)
	f.sync(t)
	for _, message := range f.storedExternal(t) {
		if message.Subject == subject {
			return message
		}
	}
	t.Fatalf("synced message %q not stored", subject)
	return nil
}

// agentRows counts every agent row of a mailbox: grants, events, drafts, and audit.
func (f *agentFixture) agentRows(t *testing.T, mailboxID uint) map[string]int64 {
	t.Helper()
	rows := map[string]int64{}
	for _, table := range []string{"mail_agent_grants", "mail_agent_events", "mail_agent_drafts", "mail_agent_audit"} {
		rows[table] = f.countRows(t, table, "mailbox_id = ?", mailboxID)
	}
	return rows
}

// seedConnectedAgent grants B's key read on the connected mailbox, syncs a message, reads it (an
// audit row), and stores a draft row as an agent of another capability once could have.
func (f *agentFixture) seedConnectedAgent(t *testing.T) (*domain.MailAgentGrant, *domain.MailMessage) {
	t.Helper()
	ctx := context.Background()
	grant := f.mustGrant(t, f.b, f.mailbox, "b", nil)
	message := f.syncOne(t, "g-agent", "Synced for the agent")
	if _, err := f.service.AgentMailMessage(ctx, f.b, f.keys["b"], message.ID); err != nil {
		t.Fatalf("agent read: %v", err)
	}
	draft := &domain.MailAgentDraft{
		OrganizationID: accessOrg, MailboxID: f.mailbox.ID, GrantID: grant.ID, MessageID: message.ID,
		APIKeyID: f.keys["b"], TextBody: "left behind?", UpdatedAt: f.service.now(),
	}
	if _, err := f.service.agents.SaveAgentDraft(ctx, draft); err != nil {
		t.Fatalf("store draft: %v", err)
	}
	rows := f.agentRows(t, f.mailbox.ID)
	if rows["mail_agent_grants"] != 1 || rows["mail_agent_events"] != 1 || rows["mail_agent_drafts"] != 1 || rows["mail_agent_audit"] != 1 {
		t.Fatalf("seeded agent rows = %v", rows)
	}
	return grant, message
}

// (1) A connected personal mailbox is like a personal one for issuing: its owner alone, hidden
// from organization managers and colleagues. It is read-only, so draft is refused for anyone.
func TestAgentGrantsOnConnectedMailbox(t *testing.T) {
	f := newAgentFixture(t)
	_, err := f.grant(f.b, f.mailbox, "b", func(in *IssueAgentGrantInput) { in.Capabilities = []string{"read", "draft"} })
	expectError(t, "draft on a connected mailbox", err, ErrMailAgentGrantInvalid)
	_, err = f.grant(f.admin, f.mailbox, "admin", nil)
	expectError(t, "admin on a member's connected mailbox", err, ErrMailboxNotFound)
	_, err = f.grant(f.c, f.mailbox, "c", nil)
	expectError(t, "colleague on a member's connected mailbox", err, ErrMailboxNotFound)
	grant := f.mustGrant(t, f.b, f.mailbox, "b", nil)
	if grant.CanDraft {
		t.Fatal("a connected mailbox grant drafts")
	}
	message := f.syncOne(t, "g-1", "Synced")
	expectMessages(t, "owner's agent feed", feedMessages(f.feed(t, f.b, "b", 0, 50)), message.ID)
}

// (2) Disconnecting the account, for any reason, ends its mailbox's grants and drafts in the same
// transaction; a revoked connection's mailbox writes no events even to a grant still on file.
func TestAgentGrantsEndWithTheConnection(t *testing.T) {
	t.Run("owner leaves", func(t *testing.T) {
		f := newAgentFixture(t)
		grant, message := f.seedConnectedAgent(t)
		f.memberships.absent[accessB] = true
		if err := f.service.MemberRemoved(context.Background(), domain.NewOrganizationMemberRemovedEvent(accessOrg, accessB, f.b.MembershipID, f.service.now())); err != nil {
			t.Fatalf("MemberRemoved: %v", err)
		}
		stored, _ := f.service.agents.FindAgentGrant(context.Background(), f.mailbox.ID, grant.ID, nil)
		if stored.RevokedAt == nil || stored.RevokeReason != domain.MailAgentRevokedOwnerLeft {
			t.Fatalf("grant after leaving = %+v", stored)
		}
		if n := f.countRows(t, "mail_agent_drafts", "mailbox_id = ?", f.mailbox.ID); n != 0 {
			t.Fatalf("%d drafts survived the departure", n)
		}
		f.expectCutOff(t, "after leaving", f.b, "b", message.ID)
	})
	t.Run("owner disconnects", func(t *testing.T) {
		f := newAgentFixture(t)
		grant, message := f.seedConnectedAgent(t)
		if err := f.service.RevokeExternalMailConnection(context.Background(), f.b, f.connection.ID); err != nil {
			t.Fatalf("RevokeExternalMailConnection: %v", err)
		}
		stored, _ := f.service.agents.FindAgentGrant(context.Background(), f.mailbox.ID, grant.ID, nil)
		if stored.RevokedAt == nil || stored.RevokeReason != domain.MailAgentRevokedConnectionRevoked {
			t.Fatalf("grant after disconnecting = %+v", stored)
		}
		if n := f.countRows(t, "mail_agent_drafts", "mailbox_id = ?", f.mailbox.ID); n != 0 {
			t.Fatalf("%d drafts survived the disconnect", n)
		}
		f.expectCutOff(t, "after disconnecting", f.b, "b", message.ID)
	})
	t.Run("revoked connection writes no events", func(t *testing.T) {
		f := newAgentFixture(t)
		grant := f.mustGrant(t, f.b, f.mailbox, "b", nil)
		db := f.repo.(*repository).db
		if err := db.Exec(`UPDATE external_mail_connections SET status = 'revoked' WHERE id = ?`, f.connection.ID).Error; err != nil {
			t.Fatalf("revoke connection row: %v", err)
		}
		message := &domain.MailMessage{
			MailboxID: f.mailbox.ID, Direction: domain.MailDirectionInbound, Status: domain.MailStatusReceived,
			FromAddress: "carol@sender.test", To: []string{f.mailbox.Address}, Subject: "Late copy", DedupeKey: "late",
		}
		err := f.repo.InTransaction(context.Background(), func(tx context.Context) error {
			if _, err := f.repo.CreateInboundMessage(tx, message); err != nil {
				return err
			}
			return f.service.appendAgentEvents(tx, f.mailbox, message)
		})
		if err != nil {
			t.Fatalf("store: %v", err)
		}
		if n := f.countRows(t, "mail_agent_events", "grant_id = ?", grant.ID); n != 0 {
			t.Fatalf("a revoked connection's mailbox wrote %d events", n)
		}
	})
}

// (3) Purging a revoked connection's copies and deleting a mailbox leave no agent grant, event,
// draft, or audit row of that mailbox behind, and neither is blocked by them.
func TestAgentRowsNeverOutliveTheMailbox(t *testing.T) {
	t.Run("purge of a revoked connection", func(t *testing.T) {
		f := newAgentFixture(t)
		f.seedConnectedAgent(t)
		ctx := context.Background()
		if err := f.service.RevokeExternalMailConnection(ctx, f.b, f.connection.ID); err != nil {
			t.Fatalf("RevokeExternalMailConnection: %v", err)
		}
		if _, err := f.service.PurgeExternalMailCopies(ctx, f.b, f.connection.ID); err != nil {
			t.Fatalf("PurgeExternalMailCopies: %v", err)
		}
		for table, count := range f.agentRows(t, f.mailbox.ID) {
			if count != 0 {
				t.Errorf("%s keeps %d rows of the purged mailbox", table, count)
			}
		}
	})
	t.Run("retention purge by the worker", func(t *testing.T) {
		f := newAgentFixture(t)
		f.seedConnectedAgent(t)
		ctx := context.Background()
		if err := f.service.RevokeExternalMailConnection(ctx, f.b, f.connection.ID); err != nil {
			t.Fatalf("RevokeExternalMailConnection: %v", err)
		}
		f.advance(f.service.settings.ExternalMail.Sync.Retention() + time.Hour)
		for i := 0; i < 5; i++ {
			if _, err := f.service.PurgeRevokedExternalMail(ctx, f.service.now(), 20); err != nil {
				t.Fatalf("PurgeRevokedExternalMail: %v", err)
			}
		}
		for table, count := range f.agentRows(t, f.mailbox.ID) {
			if count != 0 {
				t.Errorf("%s keeps %d rows after the retention purge", table, count)
			}
		}
	})
	t.Run("deleting a mailbox", func(t *testing.T) {
		f := newAgentFixture(t)
		ctx := context.Background()
		f.mustGrant(t, f.e, f.team, "e", func(in *IssueAgentGrantInput) { in.Capabilities = []string{"read", "draft"} })
		message := f.deliver(t, f.team, "a@client.test", "Team", "body", "del")
		if _, err := f.service.AgentMailMessage(ctx, f.e, f.keys["e"], message.ID); err != nil {
			t.Fatalf("read: %v", err)
		}
		if _, _, err := f.service.SaveAgentDraft(ctx, f.e, f.keys["e"], AgentDraftInput{MessageID: message.ID, Text: "Re"}); err != nil {
			t.Fatalf("draft: %v", err)
		}
		if err := f.service.DeleteMailbox(ctx, f.admin, f.team.ID); err != nil {
			t.Fatalf("DeleteMailbox: %v", err)
		}
		for table, count := range f.agentRows(t, f.team.ID) {
			if count != 0 {
				t.Errorf("%s keeps %d rows of the deleted mailbox", table, count)
			}
		}
	})
}
