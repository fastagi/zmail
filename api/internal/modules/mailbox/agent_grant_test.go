package mailbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/router"
	"github.com/fastagi/zmail/api/internal/modules/apikey"
	"github.com/fastagi/zmail/api/pkg/response"
)

// agentFixture is the external mail scenario (B owns b@ and an external Gmail mailbox, B sends
// from team@, C views it) plus E, a manage member of team@, and API keys stored like the apikey
// starter stores them.
type agentFixture struct {
	*externalFixture
	e domain.OrganizationContext
	// keys by name: the id of each stored key.
	keys map[string]uint
}

func newAgentFixture(t *testing.T) *agentFixture {
	t.Helper()
	ext := newExternalFixture(t, config.ExternalMailSourceFake)
	f := &agentFixture{externalFixture: ext, e: viewerOf(accessOrg, accessE, domain.OrganizationRoleMember), keys: map[string]uint{}}
	if _, err := f.service.SetMailboxMember(context.Background(), f.admin, f.team.ID, accessE, domain.MailboxPermissionManage); err != nil {
		t.Fatalf("add E as manager: %v", err)
	}
	f.addKey(t, "b", accessB, domain.APIKeyModeLive, ScopeMailAgent)
	f.addKey(t, "c", accessC, domain.APIKeyModeLive, ScopeMailAgent)
	f.addKey(t, "d", accessD, domain.APIKeyModeLive, ScopeMailAgent)
	f.addKey(t, "e", accessE, domain.APIKeyModeLive, ScopeMailAgent)
	f.addKey(t, "admin", accessAdmin, domain.APIKeyModeLive, ScopeMailAgent)
	f.addKey(t, "b-sender", accessB, domain.APIKeyModeLive, ScopeMailSend, ScopeMailRead)
	f.addKey(t, "b-test", accessB, domain.APIKeyModeTest, ScopeMailAgent)
	f.addKey(t, "b-all", accessB, domain.APIKeyModeLive, "*")
	return f
}

func (f *agentFixture) addKey(t *testing.T, name string, userID uint, mode domain.APIKeyMode, scopes ...string) {
	t.Helper()
	encoded, _ := json.Marshal(scopes)
	po := &apikey.APIKeyPO{
		UserID: userID, Name: name, KeyPrefix: "sk_" + name, KeyHash: "hash-" + name,
		Scopes: string(encoded), Mode: string(mode),
	}
	if err := f.repo.(*repository).db.Create(po).Error; err != nil {
		t.Fatalf("create key %s: %v", name, err)
	}
	f.keys[name] = po.ID
}

// grant issues a grant with defaults: read only, 30 days.
func (f *agentFixture) grant(viewer domain.OrganizationContext, mailbox *domain.Mailbox, key string, edit func(*IssueAgentGrantInput)) (*domain.MailAgentGrant, error) {
	input := IssueAgentGrantInput{
		APIKeyID: f.keys[key], Capabilities: []string{"read"}, ExpiresAt: f.service.now().Add(30 * 24 * time.Hour),
	}
	if edit != nil {
		edit(&input)
	}
	return f.service.IssueAgentGrant(context.Background(), viewer, mailbox.ID, input)
}

func (f *agentFixture) mustGrant(t *testing.T, viewer domain.OrganizationContext, mailbox *domain.Mailbox, key string, edit func(*IssueAgentGrantInput)) *domain.MailAgentGrant {
	t.Helper()
	grant, err := f.grant(viewer, mailbox, key, edit)
	if err != nil {
		t.Fatalf("IssueAgentGrant(%s on %s): %v", key, mailbox.Address, err)
	}
	return grant
}

// deliver stores an inbound message the way Zmail delivery does: the store and the post-store
// step in one transaction, nothing when the dedupe key was seen.
func (f *agentFixture) deliver(t *testing.T, mailbox *domain.Mailbox, from, subject, text, dedupe string) *domain.MailMessage {
	t.Helper()
	message := &domain.MailMessage{
		MailboxID: mailbox.ID, Direction: domain.MailDirectionInbound, Status: domain.MailStatusReceived,
		FromAddress: from, To: []string{mailbox.Address}, Subject: subject, TextBody: text, DedupeKey: dedupe,
	}
	err := f.service.apply(context.Background(), func(tx context.Context) ([]MailNotification, error) {
		created, err := f.repo.CreateInboundMessage(tx, message)
		if err != nil || !created {
			return nil, err
		}
		return f.service.afterInboundStored(tx, mailbox, message)
	})
	if err != nil {
		t.Fatalf("deliver %q: %v", subject, err)
	}
	return message
}

func (f *agentFixture) feed(t *testing.T, viewer domain.OrganizationContext, key string, after uint, limit int) *AgentMailFeed {
	t.Helper()
	feed, err := f.service.AgentMailEvents(context.Background(), viewer, f.keys[key], after, limit)
	if err != nil {
		t.Fatalf("AgentMailEvents(%s): %v", key, err)
	}
	return feed
}

func feedMessages(feed *AgentMailFeed) []uint {
	ids := make([]uint, 0, len(feed.Events))
	for _, event := range feed.Events {
		ids = append(ids, event.MessageID)
	}
	return ids
}

func expectMessages(t *testing.T, what string, got []uint, want ...uint) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("%s = %v; want %v", what, got, want)
	}
}

func (f *agentFixture) accessEventTypes(t *testing.T, mailbox *domain.Mailbox) []domain.MailboxAccessEventType {
	t.Helper()
	events, _, err := f.repo.ListMailboxAccessEvents(context.Background(), mailbox.ID, 1, 50)
	if err != nil {
		t.Fatalf("ListMailboxAccessEvents: %v", err)
	}
	types := make([]domain.MailboxAccessEventType, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}

func containsAccessType(types []domain.MailboxAccessEventType, want domain.MailboxAccessEventType) bool {
	for _, value := range types {
		if value == want {
			return true
		}
	}
	return false
}

// Who may issue, list, and revoke: the owner of a personal or connected mailbox and a shared
// mailbox's manage member; an organization admin only as such a member, never on someone's
// personal mailbox.
func TestAgentGrantIssuers(t *testing.T) {
	f := newAgentFixture(t)
	ctx := context.Background()

	personal := f.mustGrant(t, f.b, f.personal, "b", nil)
	if personal.KeyUserID != accessB || personal.GrantedBy != accessB || personal.CanDraft || personal.RevealSecrets {
		t.Fatalf("personal grant = %+v", personal)
	}
	f.mustGrant(t, f.b, f.mailbox, "b", nil) // the connected Gmail mailbox

	_, err := f.grant(f.c, f.personal, "c", nil)
	expectError(t, "colleague on B's personal mailbox", err, ErrMailboxNotFound)
	_, err = f.grant(f.admin, f.personal, "admin", nil)
	expectError(t, "admin on B's personal mailbox", err, domain.ErrPermissionDenied)
	_, err = f.grant(f.admin, f.mailbox, "admin", nil)
	expectError(t, "admin on B's connected mailbox, hidden from managers", err, ErrMailboxNotFound)
	_, _, err = f.service.ListAgentGrants(ctx, f.admin, f.personal.ID, 1, 15)
	expectError(t, "admin lists B's grants", err, domain.ErrPermissionDenied)
	err = f.service.RevokeAgentGrant(ctx, f.admin, f.personal.ID, personal.ID)
	expectError(t, "admin revokes B's grant", err, domain.ErrPermissionDenied)

	_, err = f.grant(f.b, f.team, "b", nil)
	expectError(t, "send member on team", err, domain.ErrPermissionDenied)
	_, err = f.grant(f.admin, f.team, "admin", nil)
	expectError(t, "admin who is not a manage member", err, domain.ErrPermissionDenied)
	_, err = f.grant(f.d, f.team, "d", nil)
	expectError(t, "non-member on team", err, ErrMailboxNotFound)

	// E manages team@ and grants a viewing member's key; a non-member's key is refused.
	teamC := f.mustGrant(t, f.e, f.team, "c", nil)
	_, err = f.grant(f.e, f.team, "d", nil)
	expectError(t, "key of a member who cannot read team", err, ErrMailAgentGrantInvalid)
	_, err = f.grant(f.e, f.team, "c", nil)
	expectError(t, "a second live grant of C's key", err, ErrMailAgentGrantExists)
	_, err = f.grant(f.e, f.team, "c", func(in *IssueAgentGrantInput) { in.Capabilities = []string{"read", "draft"} })
	expectError(t, "drafting key of a view member", err, ErrMailAgentGrantInvalid)
	_, err = f.grant(f.e, f.team, "e", func(in *IssueAgentGrantInput) { in.Capabilities = []string{"read", "draft"} })
	if err != nil {
		t.Fatalf("manager's own drafting key: %v", err)
	}

	// An admin who adds themselves as a manage member may issue; the history shows both steps.
	if _, err = f.service.SetMailboxMember(ctx, f.admin, f.team.ID, accessAdmin, domain.MailboxPermissionManage); err != nil {
		t.Fatalf("admin joins team: %v", err)
	}
	f.mustGrant(t, f.admin, f.team, "admin", nil)

	grants, total, err := f.service.ListAgentGrants(ctx, f.e, f.team.ID, 1, 15)
	if err != nil || total != 3 || len(grants) != 3 || grants[0].APIKeyName != "admin" || grants[2].APIKeyPrefix != "sk_c" {
		t.Fatalf("ListAgentGrants = %d %v, %v", total, grants, err)
	}
	_, _, err = f.service.ListAgentGrants(ctx, f.c, f.team.ID, 1, 15)
	expectError(t, "view member lists grants", err, domain.ErrPermissionDenied)
	err = f.service.RevokeAgentGrant(ctx, f.b, f.team.ID, teamC.ID)
	expectError(t, "send member revokes", err, domain.ErrPermissionDenied)
	err = f.service.RevokeAgentGrant(ctx, f.e, f.team.ID, personal.ID)
	expectError(t, "a grant of another mailbox", err, ErrMailAgentGrantNotFound)
	if err := f.service.RevokeAgentGrant(ctx, f.e, f.team.ID, teamC.ID); err != nil {
		t.Fatalf("manager revokes: %v", err)
	}
	if err := f.service.RevokeAgentGrant(ctx, f.e, f.team.ID, teamC.ID); err != nil {
		t.Fatalf("revoking again: %v", err)
	}
	types := f.accessEventTypes(t, f.team)
	if !containsAccessType(types, MailboxAccessAgentGranted) || !containsAccessType(types, MailboxAccessAgentRevoked) {
		t.Fatalf("team access history = %v; want agent_granted and agent_revoked", types)
	}
	revoked, _ := f.service.agents.FindAgentGrant(ctx, f.team.ID, teamC.ID, nil)
	if revoked.Status(f.service.now()) != domain.MailAgentGrantRevoked || revoked.RevokeReason != domain.MailAgentRevokedByPerson || *revoked.RevokedBy != accessE {
		t.Fatalf("revoked grant = %+v", revoked)
	}
}

// The key and the request are checked before anything is stored.
func TestAgentGrantValidation(t *testing.T) {
	f := newAgentFixture(t)
	now := f.service.now()
	for name, edit := range map[string]func(*IssueAgentGrantInput){
		"no mail:agent scope":      func(in *IssueAgentGrantInput) { in.APIKeyID = f.keys["b-sender"] },
		"test key":                 func(in *IssueAgentGrantInput) { in.APIKeyID = f.keys["b-test"] },
		"unknown key":              func(in *IssueAgentGrantInput) { in.APIKeyID = 999999 },
		"another person's key":     func(in *IssueAgentGrantInput) { in.APIKeyID = f.keys["c"] },
		"expired already":          func(in *IssueAgentGrantInput) { in.ExpiresAt = now.Add(-time.Minute) },
		"more than 90 days":        func(in *IssueAgentGrantInput) { in.ExpiresAt = now.Add(91 * 24 * time.Hour) },
		"draft without read":       func(in *IssueAgentGrantInput) { in.Capabilities = []string{"draft"} },
		"unknown capability":       func(in *IssueAgentGrantInput) { in.Capabilities = []string{"read", "send"} },
		"malformed sender":         func(in *IssueAgentGrantInput) { in.Senders = []string{"not a domain"} },
		"label of another mailbox": func(in *IssueAgentGrantInput) { in.LabelIDs = []uint{424242} },
	} {
		if _, err := f.grant(f.b, f.personal, "b", edit); !errors.Is(err, ErrMailAgentGrantInvalid) {
			t.Errorf("%s: error = %v; want %v", name, err, ErrMailAgentGrantInvalid)
		}
	}
	// A wildcard key holds mail:agent too.
	f.mustGrant(t, f.b, f.personal, "b-all", nil)

	// A revoked key cannot be granted.
	if err := f.repo.(*repository).db.Exec(`UPDATE api_keys SET revoked_at = now() WHERE id = ?`, f.keys["b"]).Error; err != nil {
		t.Fatalf("revoke key: %v", err)
	}
	if _, err := f.grant(f.b, f.personal, "b", nil); !errors.Is(err, ErrMailAgentGrantInvalid) {
		t.Fatalf("revoked key: %v", err)
	}

	// At most 10 live grants per mailbox; an ended grant frees its place.
	for i := 0; i < 9; i++ {
		f.addKey(t, fmt.Sprintf("b-%d", i), accessB, domain.APIKeyModeLive, ScopeMailAgent)
		f.mustGrant(t, f.b, f.personal, fmt.Sprintf("b-%d", i), nil)
	}
	f.addKey(t, "b-extra", accessB, domain.APIKeyModeLive, ScopeMailAgent)
	_, err := f.grant(f.b, f.personal, "b-extra", nil)
	expectError(t, "eleventh live grant", err, ErrMailAgentGrantLimit)
	grants, _, _ := f.service.ListAgentGrants(context.Background(), f.b, f.personal.ID, 1, 15)
	if err := f.service.RevokeAgentGrant(context.Background(), f.b, f.personal.ID, grants[0].ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	f.mustGrant(t, f.b, f.personal, "b-extra", nil)
}

// The feed holds only mail of granted mailboxes that passes the filters, synced mail included,
// in id order, exactly once per grant and message, and the same cursor replays the same page.
func TestAgentFeed(t *testing.T) {
	f := newAgentFixture(t)
	ctx := context.Background()
	f.mustGrant(t, f.b, f.personal, "b", nil)
	f.mustGrant(t, f.b, f.mailbox, "b", func(in *IssueAgentGrantInput) { in.Senders = []string{"carol@sender.test"} })
	f.mustGrant(t, f.e, f.team, "c", func(in *IssueAgentGrantInput) { in.Senders = []string{"@vip.example"} })

	first := f.deliver(t, f.personal, "alice@sender.test", "Hello", "body", "m1")
	f.deliver(t, f.team, "boss@other.example", "Team only", "body", "m2")
	vip := f.deliver(t, f.team, "ceo@eu.vip.example", "From a VIP subdomain", "body", "m3")
	// A delivery retry stores nothing and writes no second event.
	f.deliver(t, f.personal, "alice@sender.test", "Hello", "body", "m1")

	received := f.service.now().Add(-time.Minute).UTC()
	dave := externalMessage("g-2", "Filtered out", received)
	dave.FromAddress = "dave@sender.test"
	f.source.Deliver(externalTestToken, externalMessage("g-1", "Synced", received), dave)
	f.sync(t)
	synced := f.storedExternal(t)
	if len(synced) != 2 {
		t.Fatalf("synced %d messages; want 2", len(synced))
	}
	var carol uint
	for _, message := range synced {
		if message.FromAddress == "carol@sender.test" {
			carol = message.ID
		}
	}
	last := f.deliver(t, f.personal, "zed@sender.test", "Later", "body", "m4")

	feed := f.feed(t, f.b, "b", 0, 50)
	expectMessages(t, "B's feed", feedMessages(feed), first.ID, carol, last.ID)
	if feed.HasMore || feed.NextCursor != feed.Events[2].ID {
		t.Fatalf("feed cursor = %d more %v", feed.NextCursor, feed.HasMore)
	}
	for i := 1; i < len(feed.Events); i++ {
		if feed.Events[i].ID <= feed.Events[i-1].ID || feed.Events[i].Type != domain.MailAgentEventMessageReceived {
			t.Fatalf("events out of order or of the wrong type: %+v", feed.Events)
		}
	}
	expectMessages(t, "C's feed", feedMessages(f.feed(t, f.c, "c", 0, 50)), vip.ID)

	// Paging and replay.
	page := f.feed(t, f.b, "b", 0, 2)
	if !page.HasMore || page.NextCursor != page.Events[1].ID {
		t.Fatalf("first page = %+v", page)
	}
	again := f.feed(t, f.b, "b", 0, 2)
	expectMessages(t, "replayed page", feedMessages(again), feedMessages(page)...)
	rest := f.feed(t, f.b, "b", page.NextCursor, 2)
	expectMessages(t, "second page", feedMessages(rest), last.ID)
	end := f.feed(t, f.b, "b", rest.NextCursor, 2)
	if len(end.Events) != 0 || end.HasMore || end.NextCursor != rest.NextCursor {
		t.Fatalf("past the end = %+v", end)
	}

	// The other keys see nothing: D holds no grant, and a key never sees another key's events.
	if got := f.feed(t, f.d, "d", 0, 50); len(got.Events) != 0 {
		t.Fatalf("D's feed = %v", feedMessages(got))
	}
	if got := f.feed(t, f.b, "b-all", 0, 50); len(got.Events) != 0 {
		t.Fatalf("B's other key sees B's agent key's events: %v", feedMessages(got))
	}
	if _, err := f.service.AgentMailEvents(ctx, f.b, f.keys["b"], 0, 101); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("limit 101: %v", err)
	}
}

// A label filter matches the labels a message carries when its event is written.
func TestAgentFeedLabelFilter(t *testing.T) {
	f := newAgentFixture(t)
	ctx := context.Background()
	label := &domain.MailLabel{MailboxID: f.team.ID, Name: "Invoices", Color: "gray"}
	if err := f.repo.(*repository).CreateLabel(ctx, label, domain.MaxMailLabelsPerMailbox); err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	f.mustGrant(t, f.e, f.team, "c", func(in *IssueAgentGrantInput) { in.LabelIDs = []uint{label.ID} })

	store := func(subject string, labeled bool) *domain.MailMessage {
		message := &domain.MailMessage{
			MailboxID: f.team.ID, Direction: domain.MailDirectionInbound, Status: domain.MailStatusReceived,
			FromAddress: "billing@vendor.test", To: []string{f.team.Address}, Subject: subject, DedupeKey: subject,
		}
		err := f.repo.InTransaction(ctx, func(tx context.Context) error {
			if _, err := f.repo.CreateInboundMessage(tx, message); err != nil {
				return err
			}
			if labeled {
				if err := f.repo.(*repository).conn(tx).Create(&MailMessageLabelPO{MessageID: message.ID, LabelID: label.ID}).Error; err != nil {
					return err
				}
			}
			return f.service.appendAgentEvents(tx, f.team, message)
		})
		if err != nil {
			t.Fatalf("store %q: %v", subject, err)
		}
		return message
	}
	labeled := store("Invoice 42", true)
	store("Newsletter", false)
	expectMessages(t, "label-filtered feed", feedMessages(f.feed(t, f.c, "c", 0, 50)), labeled.ID)
}

const agentCodeText = "验证码 482913"

// Reads are masked unless the grant reveals secrets, audited either way, and never change
// anyone's read state; a message the grant did not receive is hidden.
func TestAgentMessageReads(t *testing.T) {
	f := newAgentFixture(t)
	ctx := context.Background()
	masked := f.mustGrant(t, f.b, f.personal, "b", nil)
	code := f.deliver(t, f.personal, "noreply@medium.com", "你的 Medium 验证码", agentCodeText, "code-1")

	read, err := f.service.AgentMailMessage(ctx, f.b, f.keys["b"], code.ID)
	if err != nil || read.Revealed {
		t.Fatalf("AgentMailMessage = %+v, %v", read, err)
	}
	body := toMailMessageResponse(read.Message)
	if !body.SecretsMasked || bytes.Contains([]byte(body.TextBody), []byte("482913")) {
		t.Fatalf("masked read shows the code: %q masked=%v", body.TextBody, body.SecretsMasked)
	}
	if states, _ := f.repo.ReadMessages(ctx, f.b.MembershipID, []uint{code.ID}); states[code.ID] {
		t.Fatal("an agent read marked the message read for its owner")
	}

	// Mail from before the grant and mail of other mailboxes are hidden.
	_, err = f.service.AgentMailMessage(ctx, f.b, f.keys["b"], f.personalMessage.ID)
	expectError(t, "mail older than the grant", err, ErrMailMessageNotFound)
	_, err = f.service.AgentMailMessage(ctx, f.b, f.keys["b"], f.teamMessage.ID)
	expectError(t, "mail of an ungranted mailbox", err, ErrMailMessageNotFound)

	if err = f.service.RevokeAgentGrant(ctx, f.b, f.personal.ID, masked.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	revealing := f.mustGrant(t, f.b, f.personal, "b", func(in *IssueAgentGrantInput) { in.RevealSecrets = true })
	second := f.deliver(t, f.personal, "noreply@medium.com", "你的 Medium 验证码", agentCodeText, "code-2")
	read, err = f.service.AgentMailMessage(ctx, f.b, f.keys["b"], second.ID)
	if err != nil || !read.Revealed || read.Message.TextBody != agentCodeText {
		t.Fatalf("revealed read = %+v, %v", read, err)
	}
	_, err = f.service.AgentMailMessage(ctx, f.b, f.keys["b"], code.ID)
	expectError(t, "a message of the revoked grant", err, ErrMailMessageNotFound)

	maskedActivity, err := f.service.AgentGrantActivity(ctx, f.b, f.personal.ID, masked.ID)
	if err != nil || len(maskedActivity) != 1 || maskedActivity[0].Action != domain.MailAgentActionRead || maskedActivity[0].MessageID != code.ID {
		t.Fatalf("masked grant activity = %+v, %v", maskedActivity, err)
	}
	activity, err := f.service.AgentGrantActivity(ctx, f.b, f.personal.ID, revealing.ID)
	if err != nil || len(activity) != 1 || activity[0].Action != domain.MailAgentActionReadRevealed || activity[0].APIKeyID != f.keys["b"] {
		t.Fatalf("revealing grant activity = %+v, %v", activity, err)
	}
	_, err = f.service.AgentGrantActivity(ctx, f.c, f.personal.ID, revealing.ID)
	expectError(t, "colleague reads activity", err, ErrMailboxNotFound)
}

// Drafts need the draft capability and the key owner's send; a second draft replaces the first;
// send holders list them.
func TestAgentDrafts(t *testing.T) {
	f := newAgentFixture(t)
	ctx := context.Background()
	f.mustGrant(t, f.e, f.team, "c", nil)
	f.mustGrant(t, f.e, f.team, "e", func(in *IssueAgentGrantInput) { in.Capabilities = []string{"read", "draft"} })
	message := f.deliver(t, f.team, "customer@client.test", "Question", "Can you help?", "q-1")

	_, _, err := f.service.SaveAgentDraft(ctx, f.c, f.keys["c"], AgentDraftInput{MessageID: message.ID, Text: "Sure"})
	expectError(t, "draft without the capability", err, domain.ErrPermissionDenied)
	_, _, err = f.service.SaveAgentDraft(ctx, f.e, f.keys["e"], AgentDraftInput{MessageID: message.ID, Text: "  "})
	expectError(t, "empty draft", err, ErrMailMessageInvalid)
	_, _, err = f.service.SaveAgentDraft(ctx, f.e, f.keys["e"], AgentDraftInput{MessageID: f.teamMessage.ID, Text: "Hi"})
	expectError(t, "draft of a message the grant did not receive", err, ErrMailMessageNotFound)

	draft, created, err := f.service.SaveAgentDraft(ctx, f.e, f.keys["e"], AgentDraftInput{MessageID: message.ID, Text: "Sure", HTML: "<p>Sure</p>"})
	if err != nil || !created || draft.Status != "pending" || draft.TextBody != "Sure" {
		t.Fatalf("first draft = %+v %v %v", draft, created, err)
	}
	replaced, created, err := f.service.SaveAgentDraft(ctx, f.e, f.keys["e"], AgentDraftInput{MessageID: message.ID, Text: "Sure, tomorrow"})
	if err != nil || created || replaced.ID != draft.ID || replaced.TextBody != "Sure, tomorrow" || replaced.HTMLBody != "" {
		t.Fatalf("replaced draft = %+v %v %v", replaced, created, err)
	}
	drafts, total, err := f.service.ListAgentDrafts(ctx, f.b, f.team.ID, 1, 15)
	if err != nil || total != 1 || drafts[0].ID != draft.ID {
		t.Fatalf("send member lists drafts = %d %v %v", total, drafts, err)
	}
	_, _, err = f.service.ListAgentDrafts(ctx, f.c, f.team.ID, 1, 15)
	expectError(t, "view member lists drafts", err, domain.ErrPermissionDenied)
	if f.outboundCount(t) != 0 || len(f.transport.sent) != 0 {
		t.Fatal("a draft sent mail")
	}

	// The manager loses send: drafting stops at once.
	if _, err = f.service.SetMailboxMember(ctx, f.admin, f.team.ID, accessE, domain.MailboxPermissionView); err != nil {
		t.Fatalf("downgrade E: %v", err)
	}
	_, _, err = f.service.SaveAgentDraft(ctx, f.e, f.keys["e"], AgentDraftInput{MessageID: message.ID, Text: "Again"})
	expectError(t, "draft after losing send", err, domain.ErrPermissionDenied)
}

// expectCutOff checks that a key's feed is empty and its message reads are hidden.
func (f *agentFixture) expectCutOff(t *testing.T, what string, viewer domain.OrganizationContext, key string, messageID uint) {
	t.Helper()
	if feed := f.feed(t, viewer, key, 0, 50); len(feed.Events) != 0 {
		t.Fatalf("%s: feed still has %v", what, feedMessages(feed))
	}
	if _, err := f.service.AgentMailMessage(context.Background(), viewer, f.keys[key], messageID); !errors.Is(err, ErrMailMessageNotFound) {
		t.Fatalf("%s: read error = %v; want %v", what, err, ErrMailMessageNotFound)
	}
}

func (f *agentFixture) expectLive(t *testing.T, what string, viewer domain.OrganizationContext, key string, messageID uint) {
	t.Helper()
	expectMessages(t, what, feedMessages(f.feed(t, viewer, key, 0, 50)), messageID)
}

// Every way a grant ends stops the feed and the reads at once.
func TestAgentRevocationPaths(t *testing.T) {
	t.Run("revoked by the issuer", func(t *testing.T) {
		f := newAgentFixture(t)
		grant := f.mustGrant(t, f.b, f.personal, "b", nil)
		message := f.deliver(t, f.personal, "alice@sender.test", "Hello", "body", "r1")
		f.expectLive(t, "before", f.b, "b", message.ID)
		if err := f.service.RevokeAgentGrant(context.Background(), f.b, f.personal.ID, grant.ID); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		f.expectCutOff(t, "after revoke", f.b, "b", message.ID)
		later := f.deliver(t, f.personal, "alice@sender.test", "Later", "body", "r2")
		f.expectCutOff(t, "mail after revoke", f.b, "b", later.ID)
	})
	t.Run("expired", func(t *testing.T) {
		f := newAgentFixture(t)
		f.mustGrant(t, f.b, f.personal, "b", func(in *IssueAgentGrantInput) { in.ExpiresAt = f.service.now().Add(time.Hour) })
		message := f.deliver(t, f.personal, "alice@sender.test", "Hello", "body", "x1")
		f.expectLive(t, "before", f.b, "b", message.ID)
		f.advance(time.Hour)
		f.expectCutOff(t, "at expiry", f.b, "b", message.ID)
		later := f.deliver(t, f.personal, "alice@sender.test", "Later", "body", "x2")
		var events int64
		f.repo.(*repository).db.Model(&MailAgentEventPO{}).Where("message_id = ?", later.ID).Count(&events)
		if events != 0 {
			t.Fatal("an expired grant received new mail")
		}
	})
	t.Run("key revoked", func(t *testing.T) {
		f := newAgentFixture(t)
		f.mustGrant(t, f.b, f.personal, "b", nil)
		message := f.deliver(t, f.personal, "alice@sender.test", "Hello", "body", "k1")
		router := f.agentRouter()
		if recorder, _ := f.call(router, http.MethodGet, "/v1/agent-mail-events", "b", ""); recorder.Code != http.StatusOK {
			t.Fatalf("feed before = %d", recorder.Code)
		}
		if err := f.repo.(*repository).db.Exec(`UPDATE api_keys SET revoked_at = now() WHERE id = ?`, f.keys["b"]).Error; err != nil {
			t.Fatalf("revoke key: %v", err)
		}
		for _, path := range []string{"/v1/agent-mail-events", fmt.Sprintf("/v1/agent-mail-messages/%d", message.ID)} {
			if recorder, _ := f.call(router, http.MethodGet, path, "b", ""); recorder.Code != http.StatusUnauthorized {
				t.Fatalf("%s with a revoked key = %d; want 401", path, recorder.Code)
			}
		}
	})
	t.Run("owner leaves the organization", func(t *testing.T) {
		f := newAgentFixture(t)
		f.mustGrant(t, f.b, f.personal, "b", nil)
		f.mustGrant(t, f.e, f.team, "b", nil)
		message := f.deliver(t, f.personal, "alice@sender.test", "Hello", "body", "l1")
		event := domain.NewOrganizationMemberRemovedEvent(accessOrg, accessB, f.b.MembershipID, f.service.now())
		f.memberships.absent[accessB] = true
		if err := f.service.MemberRemoved(context.Background(), event); err != nil {
			t.Fatalf("MemberRemoved: %v", err)
		}
		// The key's own requests stop at organization_context; even if one got through, every
		// grant of B's keys is revoked.
		live, _ := f.service.agents.ListLiveAgentGrantsOfKey(context.Background(), accessOrg, f.keys["b"], f.service.now())
		if len(live) != 0 {
			t.Fatalf("%d grants of the leaver are still live", len(live))
		}
		f.expectCutOff(t, "after leaving", f.b, "b", message.ID)
		if !containsAccessType(f.accessEventTypes(t, f.team), MailboxAccessAgentRevoked) {
			t.Fatal("team history misses the revocation")
		}
	})
	t.Run("owner loses view", func(t *testing.T) {
		f := newAgentFixture(t)
		f.mustGrant(t, f.e, f.team, "c", nil)
		message := f.deliver(t, f.team, "alice@sender.test", "Hello", "body", "v1")
		f.expectLive(t, "before", f.c, "c", message.ID)
		// The request-time check alone cuts the agent off, even before any revocation is written.
		if _, err := f.repo.DeleteMailboxMember(context.Background(), f.team.ID, accessC); err != nil {
			t.Fatalf("delete member row: %v", err)
		}
		f.expectCutOff(t, "without view", f.c, "c", message.ID)
		if _, err := f.service.SetMailboxMember(context.Background(), f.e, f.team.ID, accessC, domain.MailboxPermissionView); err != nil {
			t.Fatalf("re-add C: %v", err)
		}
		f.expectLive(t, "view again", f.c, "c", message.ID)
		// Removing the member through Zmail revokes the grant for good.
		if err := f.service.RemoveMailboxMember(context.Background(), f.e, f.team.ID, accessC); err != nil {
			t.Fatalf("RemoveMailboxMember: %v", err)
		}
		if _, err := f.service.SetMailboxMember(context.Background(), f.e, f.team.ID, accessC, domain.MailboxPermissionView); err != nil {
			t.Fatalf("re-add C: %v", err)
		}
		f.expectCutOff(t, "after removal and re-adding", f.c, "c", message.ID)
	})
	t.Run("handover", func(t *testing.T) {
		f := newAgentFixture(t)
		f.mustGrant(t, f.b, f.personal, "b", nil)
		message := f.deliver(t, f.personal, "alice@sender.test", "Hello", "body", "h1")
		if _, err := f.service.HandOverMailbox(context.Background(), f.admin, f.personal.ID, accessC); err != nil {
			t.Fatalf("HandOverMailbox: %v", err)
		}
		f.expectCutOff(t, "after handover", f.b, "b", message.ID)
		if !containsAccessType(f.accessEventTypes(t, f.personal), MailboxAccessAgentRevoked) {
			t.Fatal("history misses the revocation")
		}
	})
}

// agentRouter serves the starter's real routes. Its api_key middleware looks the key up like
// the apikey starter: a missing or revoked key is 401. organization_context acts as the key's
// owner, and refuses an owner who is not a member.
func (f *agentFixture) agentRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	handler := NewHandler(f.service)
	response.DefaultErrorMapper.Register(domain.ErrInvalidInput, http.StatusBadRequest, domain.CodeInvalidInput)
	response.DefaultErrorMapper.Register(domain.ErrPermissionDenied, http.StatusForbidden, domain.CodePermissionDenied)
	handler.RegisterErrorMappings(response.DefaultErrorMapper)
	engine := gin.New()
	routes := router.New(engine).Prefix("/v1")
	routes.MiddlewareGroup("api_key", func(c *gin.Context) {
		id, ok := f.keys[c.GetHeader("X-API-Key")]
		var key apikey.APIKeyPO
		if !ok || f.repo.(*repository).db.Where("id = ? AND revoked_at IS NULL", id).First(&key).Error != nil {
			response.AbortUnauthorized(c, "Invalid or missing API key")
			return
		}
		var scopes []string
		_ = json.Unmarshal([]byte(key.Scopes), &scopes)
		c.Set("apiKeyID", key.ID)
		c.Set("apiKeyScopes", scopes)
		c.Set("apiKeyMode", domain.APIKeyMode(key.Mode))
		c.Set("userID", key.UserID)
		c.Next()
	})
	routes.MiddlewareGroup("auth", func(c *gin.Context) {
		c.Set("userID", uint(accessB))
		c.Next()
	})
	routes.MiddlewareGroup("organization_context", func(c *gin.Context) {
		userID := c.GetUint("userID")
		if f.memberships.absent[userID] {
			response.AbortWithCode(c, http.StatusForbidden, domain.CodePermissionDenied, "not a member")
			return
		}
		viewer := viewerOf(accessOrg, userID, domain.OrganizationRoleMember)
		c.Request = c.Request.WithContext(domain.WithOrganizationContext(c.Request.Context(), viewer))
		c.Next()
	})
	handler.RegisterRoutes(routes)
	return engine
}

func (f *agentFixture) call(engine *gin.Engine, method, path, key, body string) (*httptest.ResponseRecorder, map[string]any) {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		request.Header.Set("X-API-Key", key)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	decoded := map[string]any{}
	_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
	return recorder, decoded
}

// The HTTP surface: mail:agent alone reaches only the agent routes, other keys never reach them,
// and the shapes match the contract.
func TestAgentRoutes(t *testing.T) {
	f := newAgentFixture(t)
	engine := f.agentRouter()

	// B issues a drafting, revealing grant through the session route.
	expires := f.service.now().Add(7 * 24 * time.Hour).UTC().Format(time.RFC3339)
	recorder, body := f.call(engine, http.MethodPost, fmt.Sprintf("/v1/mailboxes/%d/agent-grants", f.personal.ID), "",
		fmt.Sprintf(`{"api_key_id":%d,"capabilities":["read","draft"],"reveal_secrets":true,"filters":{"senders":["Sender.Test"]},"expires_at":%q}`, f.keys["b"], expires))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("issue = %d %s", recorder.Code, recorder.Body.String())
	}
	grant, _ := body["data"].(map[string]any)
	filters, _ := grant["filters"].(map[string]any)
	if grant["status"] != "active" || fmt.Sprint(grant["capabilities"]) != "[read draft]" || grant["reveal_secrets"] != true ||
		fmt.Sprint(filters["senders"]) != "[sender.test]" || grant["api_key_prefix"] != "sk_b" || grant["revoked_at"] != nil {
		t.Fatalf("grant = %v", grant)
	}
	if recorder, body = f.call(engine, http.MethodPost, fmt.Sprintf("/v1/mailboxes/%d/agent-grants", f.personal.ID), "",
		fmt.Sprintf(`{"api_key_id":%d,"capabilities":["read"],"expires_at":%q}`, f.keys["b"], expires)); recorder.Code != http.StatusConflict || body["error_code"] != domain.CodeMailAgentGrantExists {
		t.Fatalf("duplicate = %d %v", recorder.Code, body)
	}
	if recorder, body = f.call(engine, http.MethodPost, fmt.Sprintf("/v1/mailboxes/%d/agent-grants", f.personal.ID), "",
		fmt.Sprintf(`{"api_key_id":%d,"capabilities":["read"],"expires_at":%q}`, f.keys["b-sender"], expires)); recorder.Code != http.StatusUnprocessableEntity || body["error_code"] != domain.CodeMailAgentGrantInvalid {
		t.Fatalf("key without scope = %d %v", recorder.Code, body)
	}

	message := f.deliver(t, f.personal, "alice@sender.test", "你的 Medium 验证码", agentCodeText, "h-1")

	recorder, body = f.call(engine, http.MethodGet, "/v1/agent-mail-events?limit=10", "b", "")
	data, _ := body["data"].(map[string]any)
	events, _ := data["events"].([]any)
	if recorder.Code != http.StatusOK || len(events) != 1 || data["has_more"] != false || recorder.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("feed = %d %v", recorder.Code, body)
	}
	event, _ := events[0].(map[string]any)
	if event["type"] != "message.received" || event["message_id"] != float64(message.ID) || data["next_cursor"] != strconv.Itoa(int(event["id"].(float64))) {
		t.Fatalf("event = %v cursor %v", event, data["next_cursor"])
	}
	if recorder, body = f.call(engine, http.MethodGet, "/v1/agent-mail-events?after=abc", "b", ""); recorder.Code != http.StatusBadRequest || body["error_code"] != domain.CodeInvalidInput {
		t.Fatalf("bad cursor = %d %v", recorder.Code, body)
	}

	recorder, body = f.call(engine, http.MethodGet, fmt.Sprintf("/v1/agent-mail-messages/%d", message.ID), "b", "")
	data, _ = body["data"].(map[string]any)
	if recorder.Code != http.StatusOK || data["text_body"] != agentCodeText || data["secrets_masked"] == true {
		t.Fatalf("revealed read = %d %v", recorder.Code, body)
	}
	if recorder, body = f.call(engine, http.MethodGet, fmt.Sprintf("/v1/agent-mail-messages/%d", f.teamMessage.ID), "b", ""); recorder.Code != http.StatusNotFound || body["error_code"] != domain.CodeMailMessageNotFound {
		t.Fatalf("ungranted read = %d %v", recorder.Code, body)
	}

	recorder, body = f.call(engine, http.MethodPost, "/v1/agent-mail-drafts", "b", fmt.Sprintf(`{"message_id":%d,"text":"Thanks"}`, message.ID))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("draft = %d %v", recorder.Code, body)
	}
	if recorder, _ = f.call(engine, http.MethodPost, "/v1/agent-mail-drafts", "b", fmt.Sprintf(`{"message_id":%d,"text":"Thanks!"}`, message.ID)); recorder.Code != http.StatusOK {
		t.Fatalf("replaced draft = %d", recorder.Code)
	}
	recorder, body = f.call(engine, http.MethodGet, fmt.Sprintf("/v1/mailboxes/%d/agent-drafts", f.personal.ID), "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("draft list = %d %v", recorder.Code, body)
	}

	// mail:agent alone sends nothing and reads nothing else.
	if recorder, _ = f.call(engine, http.MethodPost, "/v1/outbound-mail-messages", "b",
		`{"from":"b@mail.example","to":["x@example.com"],"subject":"Hi","text":"Hi"}`); recorder.Code != http.StatusForbidden {
		t.Fatalf("agent key sends = %d", recorder.Code)
	}
	if recorder, _ = f.call(engine, http.MethodGet, fmt.Sprintf("/v1/outbound-mail-messages/%d", message.ID), "b", ""); recorder.Code != http.StatusForbidden {
		t.Fatalf("agent key reads outbound = %d", recorder.Code)
	}
	if f.outboundCount(t) != 0 {
		t.Fatal("an agent key stored outbound mail")
	}
	// Keys without mail:agent, and test keys, never reach the agent routes.
	for _, key := range []string{"b-sender", "b-test"} {
		for _, path := range []string{"/v1/agent-mail-events", fmt.Sprintf("/v1/agent-mail-messages/%d", message.ID)} {
			if recorder, _ = f.call(engine, http.MethodGet, path, key, ""); recorder.Code != http.StatusForbidden {
				t.Fatalf("%s on %s = %d; want 403", key, path, recorder.Code)
			}
		}
	}
	if recorder, _ = f.call(engine, http.MethodGet, "/v1/agent-mail-events", "", ""); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("no key = %d", recorder.Code)
	}

	// Activity lists the reads and drafts, newest first; revoking answers 204 twice.
	grantID := uint(grant["id"].(float64))
	recorder, body = f.call(engine, http.MethodGet, fmt.Sprintf("/v1/mailboxes/%d/agent-grants/%d/activity", f.personal.ID, grantID), "", "")
	activity, _ := body["data"].([]any)
	if recorder.Code != http.StatusOK || len(activity) != 3 || activity[0].(map[string]any)["action"] != "draft" || activity[2].(map[string]any)["action"] != "read_revealed" {
		t.Fatalf("activity = %d %v", recorder.Code, body)
	}
	for i := 0; i < 2; i++ {
		if recorder, _ = f.call(engine, http.MethodDelete, fmt.Sprintf("/v1/mailboxes/%d/agent-grants/%d", f.personal.ID, grantID), "", ""); recorder.Code != http.StatusNoContent {
			t.Fatalf("revoke %d = %d", i, recorder.Code)
		}
	}
	if recorder, body = f.call(engine, http.MethodDelete, fmt.Sprintf("/v1/mailboxes/%d/agent-grants/999999", f.personal.ID), "", ""); recorder.Code != http.StatusNotFound || body["error_code"] != domain.CodeMailAgentGrantNotFound {
		t.Fatalf("unknown grant = %d %v", recorder.Code, body)
	}
	recorder, body = f.call(engine, http.MethodGet, "/v1/agent-mail-events", "b", "")
	data, _ = body["data"].(map[string]any)
	if events, _ = data["events"].([]any); recorder.Code != http.StatusOK || len(events) != 0 {
		t.Fatalf("feed after revoke = %d %v", recorder.Code, body)
	}
}

// Pruning removes events past retention and nothing newer.
func TestAgentEventPrune(t *testing.T) {
	f := newAgentFixture(t)
	f.mustGrant(t, f.b, f.personal, "b", func(in *IssueAgentGrantInput) { in.ExpiresAt = f.service.now().Add(60 * 24 * time.Hour) })
	f.deliver(t, f.personal, "alice@sender.test", "Old", "body", "p1")
	f.advance(31 * 24 * time.Hour)
	fresh := f.deliver(t, f.personal, "alice@sender.test", "Fresh", "body", "p2")
	deleted, err := f.service.PruneAgentMailEvents(context.Background(), f.service.now().Add(-30*24*time.Hour))
	if err != nil || deleted != 1 {
		t.Fatalf("PruneAgentMailEvents = %d, %v", deleted, err)
	}
	expectMessages(t, "feed after pruning", feedMessages(f.feed(t, f.b, "b", 0, 50)), fresh.ID)
}
