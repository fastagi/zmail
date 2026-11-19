package mailbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	infradatabase "github.com/fastagi/zmail/api/internal/infra/database"
	"github.com/fastagi/zmail/api/pkg/response"
)

// Users of the access scenarios, all in organization accessOrg: A administers, B owns the
// personal mailbox b@ and sends from team@, C views team@, D holds nothing.
const (
	accessOrg   = 7
	accessAdmin = 1
	accessB     = 2
	accessC     = 3
	accessD     = 4
	// notMember is a user who does not belong to the organization.
	notMember = 404
)

type accessFixture struct {
	service     *service
	repo        domain.MailboxRepository
	transport   *fakeTransport
	memberships *fakeMemberships
	admin       domain.OrganizationContext
	b, c, d     domain.OrganizationContext
	// personal is b@, B's personal mailbox; team is team@, shared with B (send) and C (view).
	personal, team               *domain.Mailbox
	personalMessage, teamMessage *domain.MailMessage
}

func accessSettings() Settings {
	return Settings{
		Domain: "mail.example", InboundSecret: testInboundSecret, InboundTolerance: 5 * time.Minute,
		DailySendLimit: 5, PublicURL: "https://app.example",
	}
}

// newAccessFixture builds the scenario on the in-memory repository.
func newAccessFixture(t *testing.T) *accessFixture {
	t.Helper()
	repo := newFakeRepository()
	transport := &fakeTransport{}
	memberships := newAccessMemberships()
	svc := NewService(repo, memberships, repo, newFakeContacts(), &stubBroadcasts{}, nil,
		nil, nil, nil, transport, &fakeNotifier{}, accessSettings())
	return seedAccessFixture(t, svc, repo, transport, memberships)
}

// newPostgresAccessFixture builds the same scenario on PostgreSQL, with broadcasts.
func newPostgresAccessFixture(t *testing.T) *accessFixture {
	t.Helper()
	repo, db := openRepository(t)
	createOwner(t, db, accessOrg)
	transport := &fakeTransport{}
	memberships := newAccessMemberships()
	svc := NewService(repo, memberships, repo, repo, repo, repo,
		nil, nil, nil, transport, &fakeNotifier{}, accessSettings())
	return seedAccessFixture(t, svc, repo, transport, memberships)
}

func newAccessMemberships() *fakeMemberships {
	return &fakeMemberships{absent: map[uint]bool{notMember: true}, ids: map[uint]uint{}}
}

func seedAccessFixture(t *testing.T, svc *service, repo domain.MailboxRepository, transport *fakeTransport, memberships *fakeMemberships) *accessFixture {
	t.Helper()
	ctx := context.Background()
	f := &accessFixture{
		service: svc, repo: repo, transport: transport, memberships: memberships,
		admin: viewerOf(accessOrg, accessAdmin, domain.OrganizationRoleAdmin),
		b:     viewerOf(accessOrg, accessB, domain.OrganizationRoleMember),
		c:     viewerOf(accessOrg, accessC, domain.OrganizationRoleMember),
		d:     viewerOf(accessOrg, accessD, domain.OrganizationRoleMember),
	}
	var err error
	f.personal, err = svc.CreateMailbox(ctx, f.admin, CreateMailboxInput{
		LocalPart: "b", Type: domain.MailboxTypePersonal, OwnerUserID: accessB,
	})
	if err != nil {
		t.Fatalf("create personal mailbox: %v", err)
	}
	f.team, err = svc.CreateMailbox(ctx, f.admin, CreateMailboxInput{
		LocalPart: "team", Type: domain.MailboxTypeShared, Members: []MailboxMemberInput{
			{UserID: accessB, Permission: domain.MailboxPermissionSend},
			{UserID: accessC, Permission: domain.MailboxPermissionView},
		},
	})
	if err != nil {
		t.Fatalf("create shared mailbox: %v", err)
	}
	f.personalMessage = f.receive(t, f.personal, "Secret plan")
	f.teamMessage = f.receive(t, f.team, "Team update")
	return f
}

// receive stores one inbound message with an attachment, as the mail edge would.
func (f *accessFixture) receive(t *testing.T, mailbox *domain.Mailbox, subject string) *domain.MailMessage {
	t.Helper()
	message := &domain.MailMessage{
		MailboxID: mailbox.ID, Direction: domain.MailDirectionInbound, Status: domain.MailStatusReceived,
		FromAddress: "alice@sender.test", To: []string{mailbox.Address}, Subject: subject, TextBody: "body",
		DedupeKey:   subject,
		Attachments: []domain.MailAttachment{{Filename: "plan.txt", ContentType: "text/plain", Content: []byte("plan")}},
	}
	if _, err := f.repo.CreateInboundMessage(context.Background(), message); err != nil {
		t.Fatalf("CreateInboundMessage: %v", err)
	}
	return message
}

func (f *accessFixture) outboundCount(t *testing.T) int64 {
	t.Helper()
	count, err := f.repo.CountOutboundSince(context.Background(), accessOrg, domain.SendLanePersonal, time.Time{})
	if err != nil {
		t.Fatalf("CountOutboundSince: %v", err)
	}
	return count
}

func listedMailbox(t *testing.T, svc *service, viewer domain.OrganizationContext, id uint) *domain.Mailbox {
	t.Helper()
	mailboxes, err := svc.ListMailboxes(context.Background(), viewer)
	if err != nil {
		t.Fatalf("ListMailboxes: %v", err)
	}
	for _, mailbox := range mailboxes {
		if mailbox.ID == id {
			return mailbox
		}
	}
	return nil
}

func sendInput() SendInput {
	return SendInput{To: []string{"x@example.com"}, Subject: "Hello", Text: "Body"}
}

func expectError(t *testing.T, what string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s error = %v; want %v", what, err, want)
	}
}

// accessRouter serves the mailbox handlers as viewer, with the error mapping of a real server.
func accessRouter(svc *service, viewer domain.OrganizationContext) *gin.Engine {
	gin.SetMode(gin.TestMode)
	handler := NewHandler(svc)
	response.DefaultErrorMapper.Register(domain.ErrPermissionDenied, http.StatusForbidden, domain.CodePermissionDenied)
	response.DefaultErrorMapper.Register(domain.ErrInvalidInput, http.StatusBadRequest, domain.CodeInvalidInput)
	handler.RegisterErrorMappings(response.DefaultErrorMapper)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(domain.WithOrganizationContext(c.Request.Context(), viewer))
		c.Next()
	})
	router.GET("/mailboxes", handler.ListMailboxes)
	router.POST("/mailboxes", handler.CreateMailbox)
	router.GET("/mailboxes/:mailbox_id/messages", handler.ListMessages)
	router.GET("/mailboxes/:mailbox_id/threads/:thread_id", handler.GetThread)
	router.GET("/mailboxes/:mailbox_id/message-counts", handler.MessageCounts)
	router.POST("/mailboxes/:mailbox_id/messages", handler.SendMessage)
	router.GET("/mailboxes/:mailbox_id/messages/:message_id", handler.GetMessage)
	router.PUT("/mailboxes/:mailbox_id/messages/:message_id", handler.UpdateMessage)
	router.DELETE("/mailboxes/:mailbox_id/messages/:message_id", handler.DeleteMessage)
	router.POST("/mailboxes/:mailbox_id/message-updates", handler.UpdateMessages)
	router.POST("/mailboxes/:mailbox_id/trash-purges", handler.PurgeTrash)
	router.GET("/mailboxes/:mailbox_id/mail-folders", handler.ListFolders)
	router.POST("/mailboxes/:mailbox_id/mail-folders", handler.CreateFolder)
	router.PUT("/mailboxes/:mailbox_id/mail-folders/:folder_id", handler.RenameFolder)
	router.DELETE("/mailboxes/:mailbox_id/mail-folders/:folder_id", handler.DeleteFolder)
	router.GET("/mailboxes/:mailbox_id/mail-labels", handler.ListLabels)
	router.POST("/mailboxes/:mailbox_id/mail-labels", handler.CreateLabel)
	router.PUT("/mailboxes/:mailbox_id/mail-labels/:label_id", handler.UpdateLabel)
	router.DELETE("/mailboxes/:mailbox_id/mail-labels/:label_id", handler.DeleteLabel)
	router.GET("/mailboxes/:mailbox_id/members", handler.ListMailboxMembers)
	router.PUT("/mailboxes/:mailbox_id/members/:user_id", handler.SetMailboxMember)
	router.DELETE("/mailboxes/:mailbox_id/members/:user_id", handler.RemoveMailboxMember)
	router.GET("/mailboxes/:mailbox_id/access-events", handler.ListMailboxAccessEvents)
	router.POST("/mailboxes/:mailbox_id/handovers", handler.HandOverMailbox)
	router.DELETE("/mailboxes/:mailbox_id/sealed-messages", handler.DeleteMailboxSealedMessages)
	router.PUT("/mailboxes/:mailbox_id/webhook-setting", handler.SetMailboxWebhookSetting)
	router.POST("/mail-broadcasts/:broadcast_id/pauses", handler.PauseBroadcast)
	router.POST("/mail-broadcasts/:broadcast_id/cancellations", handler.CancelBroadcast)
	return router
}

func serve(router *gin.Engine, method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	decoded := map[string]any{}
	_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
	return recorder, decoded
}

// AC-1: a colleague finds nothing of another member's personal mailbox.
func TestPersonalMailboxIsInvisibleToColleagues(t *testing.T) {
	f := newAccessFixture(t)
	ctx := context.Background()
	box, message := f.personal.ID, f.personalMessage.ID

	if listedMailbox(t, f.service, f.c, box) != nil {
		t.Fatal("C's mailbox list shows B's personal mailbox")
	}
	_, _, err := f.service.ListMessages(ctx, f.c, box, domain.MailMessageFilter{})
	expectError(t, "ListMessages", err, ErrMailboxNotFound)
	_, err = f.service.GetMessage(ctx, f.c, box, message)
	expectError(t, "GetMessage", err, ErrMailboxNotFound)
	_, err = f.service.GetAttachment(ctx, f.c, box, message, 1)
	expectError(t, "GetAttachment", err, ErrMailboxNotFound)
	_, err = f.service.SetMessageRead(ctx, f.c, box, message, true)
	expectError(t, "SetMessageRead", err, ErrMailboxNotFound)
	_, _, err = f.service.SendMessage(ctx, f.c, box, sendInput())
	expectError(t, "SendMessage", err, ErrMailboxNotFound)
	err = f.service.DeleteMessage(ctx, f.c, box, message)
	expectError(t, "DeleteMessage", err, ErrMailboxNotFound)
	_, _, err = f.service.ListMailboxAccessEvents(ctx, f.c, box, 1, 15)
	expectError(t, "ListMailboxAccessEvents", err, ErrMailboxNotFound)
	_, err = f.service.ListMailboxMembers(ctx, f.c, box)
	expectError(t, "ListMailboxMembers", err, ErrMailboxNotFound)

	if len(f.transport.sent) != 0 || f.outboundCount(t) != 0 {
		t.Fatalf("a denied send reached the provider or stored a message")
	}
	if read, _ := f.repo.ReadMessages(ctx, f.c.MembershipID, []uint{message}); read[message] {
		t.Fatalf("a denied read marked the message read")
	}

	recorder, body := serve(accessRouter(f.service, f.c), http.MethodGet, fmt.Sprintf("/mailboxes/%d/messages/%d", box, message), "")
	if recorder.Code != http.StatusNotFound || body["error_code"] != domain.CodeMailboxNotFound || body["data"] != nil {
		t.Fatalf("C GET B's message = %d %v; want 404 %s and no data", recorder.Code, body, domain.CodeMailboxNotFound)
	}

	// The owner reads and sends.
	if _, err := f.service.GetMessage(ctx, f.b, box, message); err != nil {
		t.Fatalf("owner GetMessage: %v", err)
	}
	if _, _, err := f.service.SendMessage(ctx, f.b, box, sendInput()); err != nil {
		t.Fatalf("owner SendMessage: %v", err)
	}
}

// AC-2: an admin sees every mailbox's metadata but no mail without a member row.
func TestAdminSeesMetadataNotMail(t *testing.T) {
	f := newAccessFixture(t)
	ctx := context.Background()

	listed := listedMailbox(t, f.service, f.admin, f.personal.ID)
	if listed == nil {
		t.Fatal("the admin's list lacks the personal mailbox")
	}
	want := domain.MailboxAccess{Administer: true, SeeHistory: true}
	if listed.Access != want || listed.UnreadCount != 0 {
		t.Fatalf("admin sees personal mailbox with access %+v and unread %d; want %+v and 0", listed.Access, listed.UnreadCount, want)
	}
	if owner := listedMailbox(t, f.service, f.b, f.personal.ID); owner == nil || owner.UnreadCount != 1 || !owner.Access.View {
		t.Fatalf("owner sees %+v; want its unread count", owner)
	}
	if team := listedMailbox(t, f.service, f.admin, f.team.ID); team == nil || team.UnreadCount != 0 || team.Access.View || !team.Access.ManageMembers {
		t.Fatalf("admin sees team@ as %+v", team)
	}

	_, err := f.service.GetMessage(ctx, f.admin, f.personal.ID, f.personalMessage.ID)
	expectError(t, "admin GetMessage", err, domain.ErrPermissionDenied)
	_, _, err = f.service.ListMessages(ctx, f.admin, f.team.ID, domain.MailMessageFilter{})
	expectError(t, "admin ListMessages", err, domain.ErrPermissionDenied)
	_, _, err = f.service.SendMessage(ctx, f.admin, f.personal.ID, sendInput())
	expectError(t, "admin SendMessage", err, domain.ErrPermissionDenied)

	router := accessRouter(f.service, f.admin)
	recorder, body := serve(router, http.MethodGet, fmt.Sprintf("/mailboxes/%d/messages/%d", f.personal.ID, f.personalMessage.ID), "")
	if recorder.Code != http.StatusForbidden || body["error_code"] != domain.CodePermissionDenied {
		t.Fatalf("admin GET personal message = %d %v; want 403", recorder.Code, body)
	}
	recorder, body = serve(router, http.MethodGet, "/mailboxes", "")
	items, _ := body["data"].([]any)
	if recorder.Code != http.StatusOK || len(items) != 2 {
		t.Fatalf("admin list = %d %v", recorder.Code, body)
	}
	first, _ := items[0].(map[string]any)
	access, _ := first["access"].(map[string]any)
	if first["type"] != "personal" || first["owner_user_id"] != float64(accessB) || first["awaiting_handover"] != false ||
		access["view"] != false || access["administer"] != true || first["unread_count"] != float64(0) {
		t.Fatalf("admin list item = %v", first)
	}
}

// AC-3 and review focus 4: an admin who is a shared member searches the log and reads the
// statistics; nothing of a personal mailbox they cannot read shows, not even in totals.
func TestLogSkipsUnreadableMailboxes(t *testing.T) {
	f := newPostgresAccessFixture(t)
	ctx := context.Background()
	f.service.now = time.Now
	if _, err := f.service.SetMailboxMember(ctx, f.admin, f.team.ID, accessAdmin, domain.MailboxPermissionView); err != nil {
		t.Fatalf("admin joins team@: %v", err)
	}

	found, total, err := f.service.ListLog(ctx, f.admin, domain.MailLogFilter{Query: "Secret"})
	if err != nil || total != 0 || len(found) != 0 {
		t.Fatalf("admin searching the personal subject = %d rows, total %d, err %v; want nothing", len(found), total, err)
	}
	all, total, err := f.service.ListLog(ctx, f.admin, domain.MailLogFilter{})
	if err != nil || total != 1 || len(all) != 1 || all[0].ID != f.teamMessage.ID {
		t.Fatalf("admin log = %+v, total %d, err %v; want only team@'s message", all, total, err)
	}
	filtered, total, err := f.service.ListLog(ctx, f.admin, domain.MailLogFilter{MailboxID: f.personal.ID})
	if err != nil || total != 0 || len(filtered) != 0 {
		t.Fatalf("admin filtering by the personal mailbox = %d rows, total %d, err %v", len(filtered), total, err)
	}
	if owned, total, err := f.service.ListLog(ctx, f.b, domain.MailLogFilter{Query: "Secret"}); err != nil || total != 1 || len(owned) != 1 {
		t.Fatalf("owner search = %d rows, total %d, err %v", len(owned), total, err)
	}
	if none, total, err := f.service.ListLog(ctx, f.d, domain.MailLogFilter{}); err != nil || total != 0 || len(none) != 0 {
		t.Fatalf("D's log = %d rows, total %d, err %v", len(none), total, err)
	}

	received := func(viewer domain.OrganizationContext) int {
		t.Helper()
		stats, err := f.service.Stats(ctx, viewer, 1)
		if err != nil || len(stats) != 1 {
			t.Fatalf("Stats = %+v, %v", stats, err)
		}
		return stats[0].Received
	}
	if got := received(f.admin); got != 1 {
		t.Fatalf("admin statistics count %d received; want only team@'s 1", got)
	}
	if got := received(f.b); got != 2 {
		t.Fatalf("B's statistics count %d received; want 2", got)
	}
	if got := received(f.d); got != 0 {
		t.Fatalf("D's statistics count %d received; want 0", got)
	}
}

// AC-4: a view member reads but cannot send; a denied send stores nothing and uses no quota.
func TestViewMemberCannotSend(t *testing.T) {
	f := newAccessFixture(t)
	ctx := context.Background()

	if _, err := f.service.GetMessage(ctx, f.c, f.team.ID, f.teamMessage.ID); err != nil {
		t.Fatalf("view member GetMessage: %v", err)
	}
	if _, err := f.service.SetMessageRead(ctx, f.c, f.team.ID, f.teamMessage.ID, true); err != nil {
		t.Fatalf("view member marks read: %v", err)
	}
	_, _, err := f.service.SendMessage(ctx, f.c, f.team.ID, sendInput())
	expectError(t, "view member SendMessage", err, domain.ErrPermissionDenied)
	err = f.service.DeleteMessage(ctx, f.c, f.team.ID, f.teamMessage.ID)
	expectError(t, "view member DeleteMessage", err, domain.ErrPermissionDenied)

	recorder, body := serve(accessRouter(f.service, f.c), http.MethodPost, fmt.Sprintf("/mailboxes/%d/messages", f.team.ID),
		`{"to":["x@example.com"],"subject":"s","text":"b"}`)
	if recorder.Code != http.StatusForbidden || body["error_code"] != domain.CodePermissionDenied {
		t.Fatalf("view member POST send = %d %v; want 403", recorder.Code, body)
	}
	if len(f.transport.sent) != 0 || f.outboundCount(t) != 0 {
		t.Fatalf("a denied send reached the provider or counts against the limit")
	}
	if _, err := f.repo.FindMessage(ctx, f.team.ID, f.teamMessage.ID); err != nil {
		t.Fatalf("a denied delete removed the message: %v", err)
	}
}

// AC-6: an API key acts with its owner's access.
func TestApiKeyFollowsOwnerAccess(t *testing.T) {
	f := newAccessFixture(t)
	ctx := context.Background()

	_, _, err := f.service.SendFromAddress(ctx, f.c, f.team.Address, sendInput())
	expectError(t, "C's key from team@", err, domain.ErrPermissionDenied)
	_, _, err = f.service.SendFromAddress(ctx, f.c, f.personal.Address, sendInput())
	expectError(t, "C's key from b@", err, ErrMailboxNotFound)
	if len(f.transport.sent) != 0 || f.outboundCount(t) != 0 {
		t.Fatal("a denied key send stored a message or reached the provider")
	}

	teamSent, _, err := f.service.SendFromAddress(ctx, f.b, f.team.Address, sendInput())
	if err != nil {
		t.Fatalf("B's key from team@: %v", err)
	}
	personalSent, _, err := f.service.SendFromAddress(ctx, f.b, f.personal.Address, sendInput())
	if err != nil {
		t.Fatalf("B's key from b@: %v", err)
	}
	if _, err = f.service.GetOutboundMessage(ctx, f.c, teamSent.ID); err != nil {
		t.Fatalf("C's key reads team@'s sent message: %v", err)
	}
	_, err = f.service.GetOutboundMessage(ctx, f.c, personalSent.ID)
	expectError(t, "C's key reads b@'s sent message", err, ErrMailMessageNotFound)
	_, err = f.service.GetOutboundMessage(ctx, f.d, teamSent.ID)
	expectError(t, "D's key reads team@'s sent message", err, ErrMailMessageNotFound)
	_, err = f.service.GetOutboundMessage(ctx, f.admin, personalSent.ID)
	expectError(t, "admin's key reads b@'s sent message", err, domain.ErrPermissionDenied)
}

func accessEvents(t *testing.T, f *accessFixture, viewer domain.OrganizationContext, mailboxID uint) []*domain.MailboxAccessEvent {
	t.Helper()
	events, _, err := f.service.ListMailboxAccessEvents(context.Background(), viewer, mailboxID, 1, 100)
	if err != nil {
		t.Fatalf("ListMailboxAccessEvents: %v", err)
	}
	return events
}

func sameID(id *uint, want uint) bool {
	return id != nil && *id == want
}

// AC-7: an admin may grant themselves access, and the history records it.
func TestAdminSelfGrantIsRecorded(t *testing.T) {
	f := newAccessFixture(t)
	ctx := context.Background()

	_, err := f.service.GetMessage(ctx, f.admin, f.team.ID, f.teamMessage.ID)
	expectError(t, "admin reads before joining", err, domain.ErrPermissionDenied)
	member, err := f.service.SetMailboxMember(ctx, f.admin, f.team.ID, accessAdmin, domain.MailboxPermissionView)
	if err != nil || member.GrantedBy != accessAdmin || member.Permission != domain.MailboxPermissionView {
		t.Fatalf("admin self-grant = %+v, %v", member, err)
	}
	if _, err := f.service.GetMessage(ctx, f.admin, f.team.ID, f.teamMessage.ID); err != nil {
		t.Fatalf("admin reads after joining: %v", err)
	}

	latest := accessEvents(t, f, f.admin, f.team.ID)[0]
	if latest.Type != domain.MailboxAccessMemberSet || !sameID(latest.ActorUserID, accessAdmin) ||
		!sameID(latest.SubjectUserID, accessAdmin) || latest.Detail["permission"] != "view" ||
		latest.Detail["membership_id"] != f.admin.MembershipID || latest.CreatedAt.IsZero() {
		t.Fatalf("latest access event = %+v", latest)
	}
}

// AC-8: changing members needs ManageMembers; a manager's changes are recorded.
func TestManageRequiredToChangeMembers(t *testing.T) {
	f := newAccessFixture(t)
	ctx := context.Background()

	_, err := f.service.SetMailboxMember(ctx, f.b, f.team.ID, accessD, domain.MailboxPermissionView)
	expectError(t, "send member adds a member", err, domain.ErrPermissionDenied)
	err = f.service.RemoveMailboxMember(ctx, f.b, f.team.ID, accessC)
	expectError(t, "send member removes a member", err, domain.ErrPermissionDenied)
	_, err = f.service.ListMailboxMembers(ctx, f.b, f.team.ID)
	expectError(t, "send member lists members", err, domain.ErrPermissionDenied)
	_, _, err = f.service.ListMailboxAccessEvents(ctx, f.c, f.team.ID, 1, 15)
	expectError(t, "view member reads the history", err, domain.ErrPermissionDenied)

	if _, err = f.service.SetMailboxMember(ctx, f.admin, f.team.ID, accessB, domain.MailboxPermissionManage); err != nil {
		t.Fatalf("admin grants B manage: %v", err)
	}
	if _, err = f.service.SetMailboxMember(ctx, f.b, f.team.ID, accessD, domain.MailboxPermissionView); err != nil {
		t.Fatalf("manager adds D: %v", err)
	}
	if _, err = f.service.GetMessage(ctx, f.d, f.team.ID, f.teamMessage.ID); err != nil {
		t.Fatalf("D reads after being added: %v", err)
	}
	members, err := f.service.ListMailboxMembers(ctx, f.b, f.team.ID)
	if err != nil || len(members) != 3 || members[0].UserID != accessB || members[2].UserID != accessD || members[2].GrantedBy != accessB {
		t.Fatalf("members = %+v, %v; want B, C, D ordered by user id", members, err)
	}
	_, err = f.service.SetMailboxMember(ctx, f.b, f.team.ID, notMember, domain.MailboxPermissionView)
	expectError(t, "adding a non-member", err, ErrMailboxAccessInvalid)
	_, err = f.service.SetMailboxMember(ctx, f.b, f.team.ID, accessD, "owner")
	expectError(t, "an unknown permission", err, ErrMailboxAccessInvalid)

	if err = f.service.RemoveMailboxMember(ctx, f.b, f.team.ID, accessD); err != nil {
		t.Fatalf("manager removes D: %v", err)
	}
	_, err = f.service.GetMessage(ctx, f.d, f.team.ID, f.teamMessage.ID)
	expectError(t, "removed member's next request", err, ErrMailboxNotFound)
	err = f.service.RemoveMailboxMember(ctx, f.b, f.team.ID, accessD)
	expectError(t, "removing a missing member", err, ErrMailboxNotFound)

	events := accessEvents(t, f, f.b, f.team.ID)
	removed, added := events[0], events[1]
	if removed.Type != domain.MailboxAccessMemberRemoved || !sameID(removed.ActorUserID, accessB) || !sameID(removed.SubjectUserID, accessD) ||
		removed.Detail["membership_id"] != f.d.MembershipID {
		t.Fatalf("removal event = %+v", removed)
	}
	if added.Type != domain.MailboxAccessMemberSet || !sameID(added.ActorUserID, accessB) || !sameID(added.SubjectUserID, accessD) {
		t.Fatalf("addition event = %+v", added)
	}

	router := accessRouter(f.service, f.c)
	recorder, body := serve(router, http.MethodPut, fmt.Sprintf("/mailboxes/%d/members/%d", f.team.ID, accessD), `{"permission":"view"}`)
	if recorder.Code != http.StatusForbidden || body["error_code"] != domain.CodePermissionDenied {
		t.Fatalf("view member PUT member = %d %v; want 403", recorder.Code, body)
	}
	recorder, body = serve(accessRouter(f.service, f.b), http.MethodPut, fmt.Sprintf("/mailboxes/%d/members/%d", f.team.ID, accessD), `{"permission":"send"}`)
	if data, _ := body["data"].(map[string]any); recorder.Code != http.StatusOK || data["permission"] != "send" || data["user_id"] != float64(accessD) {
		t.Fatalf("manager PUT member = %d %v", recorder.Code, body)
	}
	recorder, _ = serve(accessRouter(f.service, f.b), http.MethodDelete, fmt.Sprintf("/mailboxes/%d/members/%d", f.team.ID, accessD), "")
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("manager DELETE member = %d", recorder.Code)
	}
	recorder, body = serve(accessRouter(f.service, f.b), http.MethodGet, fmt.Sprintf("/mailboxes/%d/access-events", f.team.ID), "")
	if items, _ := body["data"].([]any); recorder.Code != http.StatusOK || len(items) == 0 {
		t.Fatalf("manager GET access-events = %d %v", recorder.Code, body)
	}
}

// An admin who is also a member gets the union of what the role and the row grant.
func TestAccessIsUnionOfRules(t *testing.T) {
	f := newAccessFixture(t)
	ctx := context.Background()
	if _, err := f.service.SetMailboxMember(ctx, f.admin, f.team.ID, accessAdmin, domain.MailboxPermissionView); err != nil {
		t.Fatalf("admin joins with view: %v", err)
	}
	team := listedMailbox(t, f.service, f.admin, f.team.ID)
	want := domain.MailboxAccess{View: true, ManageMembers: true, Administer: true, SeeHistory: true}
	if team == nil || team.Access != want || team.UnreadCount != 1 {
		t.Fatalf("admin with a view row sees %+v; want access %+v and the unread count", team, want)
	}
	_, _, err := f.service.SendMessage(ctx, f.admin, f.team.ID, sendInput())
	expectError(t, "admin with view sends", err, domain.ErrPermissionDenied)
	// Managing members comes from the role, although the row only grants view.
	if _, err := f.service.SetMailboxMember(ctx, f.admin, f.team.ID, accessD, domain.MailboxPermissionView); err != nil {
		t.Fatalf("admin with a view row adds a member: %v", err)
	}
}

// Personal mailboxes have no members: every member route answers 409 to anyone who may know
// the mailbox, and 404 to anyone else.
func TestPersonalMailboxMembersRouteIs409(t *testing.T) {
	f := newAccessFixture(t)
	ctx := context.Background()
	for _, viewer := range []domain.OrganizationContext{f.admin, f.b} {
		_, err := f.service.ListMailboxMembers(ctx, viewer, f.personal.ID)
		expectError(t, "list members of a personal mailbox", err, ErrMailboxAccessConflict)
		_, err = f.service.SetMailboxMember(ctx, viewer, f.personal.ID, accessC, domain.MailboxPermissionView)
		expectError(t, "add a member to a personal mailbox", err, ErrMailboxAccessConflict)
		err = f.service.RemoveMailboxMember(ctx, viewer, f.personal.ID, accessB)
		expectError(t, "remove a member of a personal mailbox", err, ErrMailboxAccessConflict)
	}
	_, err := f.service.SetMailboxMember(ctx, f.c, f.personal.ID, accessC, domain.MailboxPermissionView)
	expectError(t, "a colleague adds themselves to a personal mailbox", err, ErrMailboxNotFound)

	for _, request := range [][3]string{
		{http.MethodGet, "/members", ""},
		{http.MethodPut, "/members/3", `{"permission":"view"}`},
		{http.MethodDelete, "/members/2", ""},
	} {
		recorder, body := serve(accessRouter(f.service, f.admin), request[0], fmt.Sprintf("/mailboxes/%d%s", f.personal.ID, request[1]), request[2])
		if recorder.Code != http.StatusConflict || body["error_code"] != domain.CodeMailboxAccessConflict {
			t.Fatalf("admin %s %s on a personal mailbox = %d %v; want 409", request[0], request[1], recorder.Code, body)
		}
	}
	// The owner and the admin still read the history of a personal mailbox.
	if events := accessEvents(t, f, f.b, f.personal.ID); len(events) != 1 || events[0].Type != domain.MailboxAccessCreated ||
		!sameID(events[0].SubjectUserID, accessB) || events[0].Detail["type"] != "personal" {
		t.Fatalf("personal mailbox history = %+v", events)
	}
}

// AC-14: only admins create mailboxes, and every mailbox needs an owner or members.
func TestCreateValidation(t *testing.T) {
	f := newAccessFixture(t)
	ctx := context.Background()
	member := func(userID uint, permission domain.MailboxPermission) MailboxMemberInput {
		return MailboxMemberInput{UserID: userID, Permission: permission}
	}
	tooMany := make([]MailboxMemberInput, 0, domain.MaxMailboxMembers+1)
	for i := uint(0); i <= domain.MaxMailboxMembers; i++ {
		tooMany = append(tooMany, member(1000+i, domain.MailboxPermissionView))
	}
	before := listedMailboxCount(t, f)

	_, err := f.service.CreateMailbox(ctx, f.b, CreateMailboxInput{LocalPart: "mine", Type: domain.MailboxTypePersonal, OwnerUserID: accessB})
	expectError(t, "member creates a mailbox", err, domain.ErrPermissionDenied)
	for name, input := range map[string]CreateMailboxInput{
		"personal without owner":     {LocalPart: "x", Type: domain.MailboxTypePersonal},
		"personal with members":      {LocalPart: "x", Type: domain.MailboxTypePersonal, OwnerUserID: accessB, Members: []MailboxMemberInput{member(accessC, "view")}},
		"shared without members":     {LocalPart: "x", Type: domain.MailboxTypeShared},
		"shared with owner":          {LocalPart: "x", Type: domain.MailboxTypeShared, OwnerUserID: accessB, Members: []MailboxMemberInput{member(accessC, "view")}},
		"shared with too many":       {LocalPart: "x", Type: domain.MailboxTypeShared, Members: tooMany},
		"shared with a duplicate":    {LocalPart: "x", Type: domain.MailboxTypeShared, Members: []MailboxMemberInput{member(accessC, "view"), member(accessC, "send")}},
		"shared with bad permission": {LocalPart: "x", Type: domain.MailboxTypeShared, Members: []MailboxMemberInput{member(accessC, "admin")}},
		"no type":                    {LocalPart: "x", OwnerUserID: accessB},
		"unknown type":               {LocalPart: "x", Type: "team", OwnerUserID: accessB},
		"owner outside":              {LocalPart: "x", Type: domain.MailboxTypePersonal, OwnerUserID: notMember},
		"member outside":             {LocalPart: "x", Type: domain.MailboxTypeShared, Members: []MailboxMemberInput{member(notMember, "view")}},
	} {
		_, err := f.service.CreateMailbox(ctx, f.admin, input)
		expectError(t, name, err, ErrMailboxAccessInvalid)
	}
	if after := listedMailboxCount(t, f); after != before {
		t.Fatalf("rejected creations stored %d mailboxes", after-before)
	}

	router := accessRouter(f.service, f.admin)
	recorder, body := serve(router, http.MethodPost, "/mailboxes", `{"local_part":"x","type":"personal"}`)
	if recorder.Code != http.StatusUnprocessableEntity || body["error_code"] != domain.CodeMailboxAccessInvalid {
		t.Fatalf("POST personal without owner = %d %v; want 422", recorder.Code, body)
	}
	recorder, body = serve(router, http.MethodPost, "/mailboxes", `{"local_part":"x","type":"shared","members":[]}`)
	if recorder.Code != http.StatusUnprocessableEntity || body["error_code"] != domain.CodeMailboxAccessInvalid {
		t.Fatalf("POST shared without members = %d %v; want 422", recorder.Code, body)
	}
	recorder, body = serve(router, http.MethodPost, "/mailboxes", `{"local_part":"sales","type":"shared","members":[{"user_id":3,"permission":"send"}]}`)
	data, _ := body["data"].(map[string]any)
	access, _ := data["access"].(map[string]any)
	if recorder.Code != http.StatusCreated || data["type"] != "shared" || access["administer"] != true || access["view"] != false {
		t.Fatalf("POST shared = %d %v", recorder.Code, body)
	}

	// A shared mailbox's history starts with created, then one member_set per first member.
	events := accessEvents(t, f, f.admin, f.team.ID)
	if len(events) != 3 || events[2].Type != domain.MailboxAccessCreated || events[2].Detail["members"] != 2 ||
		events[0].Type != domain.MailboxAccessMemberSet || events[0].Detail["membership_id"] != f.c.MembershipID {
		t.Fatalf("team@ history = %+v", events)
	}
}

func listedMailboxCount(t *testing.T, f *accessFixture) int {
	t.Helper()
	mailboxes, err := f.service.ListMailboxes(context.Background(), f.admin)
	if err != nil {
		t.Fatalf("ListMailboxes: %v", err)
	}
	return len(mailboxes)
}

// broadcastScene adds a template and a list with one subscriber to a PostgreSQL access fixture.
func broadcastScene(t *testing.T, f *accessFixture) (templateID, listID uint) {
	t.Helper()
	ctx := context.Background()
	template, err := f.service.CreateTemplate(ctx, accessOrg, accessB, TemplateInput{
		Name: "News", Subject: "News", HTML: "<p>Hi</p>", Text: "Hi",
	})
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	list, err := f.service.CreateContactList(ctx, accessOrg, "Newsletter")
	if err != nil {
		t.Fatalf("CreateContactList: %v", err)
	}
	if _, err := f.service.CreateContact(ctx, accessOrg, ContactInput{Email: "ann@example.com", ListIDs: []uint{list.ID}}); err != nil {
		t.Fatalf("CreateContact: %v", err)
	}
	return template.ID, list.ID
}

// AC-13: starting and resuming need Send on the sender at that call; pausing and canceling need
// no access and then reveal only the id and status.
func TestBroadcastNeedsSendOnSender(t *testing.T) {
	f := newPostgresAccessFixture(t)
	ctx := context.Background()
	f.service.now = time.Now
	templateID, listID := broadcastScene(t, f)
	input := BroadcastInput{Name: "Team news", MailboxID: f.team.ID, TemplateID: templateID, ListID: listID}

	_, err := f.service.CreateBroadcast(ctx, f.c, input)
	expectError(t, "view member drafts", err, domain.ErrPermissionDenied)
	draft, err := f.service.CreateBroadcast(ctx, f.b, input)
	if err != nil {
		t.Fatalf("send member drafts: %v", err)
	}

	_, err = f.service.SendBroadcast(ctx, f.admin, draft.ID, nil)
	expectError(t, "admin without a member row starts", err, domain.ErrPermissionDenied)
	if _, err = f.service.SetMailboxMember(ctx, f.admin, f.team.ID, accessAdmin, domain.MailboxPermissionSend); err != nil {
		t.Fatalf("admin joins with send: %v", err)
	}
	started, err := f.service.SendBroadcast(ctx, f.admin, draft.ID, nil)
	if err != nil || started.Status != domain.MailBroadcastSending {
		t.Fatalf("admin with send starts = %+v, %v", started, err)
	}
	if err = f.service.RemoveMailboxMember(ctx, f.admin, f.team.ID, accessAdmin); err != nil {
		t.Fatalf("admin leaves team@: %v", err)
	}

	router := accessRouter(f.service, f.admin)
	recorder, _ := serve(router, http.MethodPost, fmt.Sprintf("/mail-broadcasts/%d/pauses", draft.ID), "")
	if recorder.Code != http.StatusNoContent || recorder.Body.Len() != 0 {
		t.Fatalf("admin without access pauses = %d %q; want 204 with no body", recorder.Code, recorder.Body.String())
	}
	if paused, _ := f.service.GetBroadcast(ctx, f.b, draft.ID); paused == nil || paused.Status != domain.MailBroadcastPaused {
		t.Fatalf("broadcast after the pause = %+v; want paused", paused)
	}
	_, err = f.service.ResumeBroadcast(ctx, f.admin, draft.ID)
	expectError(t, "admin without access resumes", err, domain.ErrPermissionDenied)
	recorder, _ = serve(router, http.MethodPost, fmt.Sprintf("/mail-broadcasts/%d/cancellations", draft.ID), "")
	if recorder.Code != http.StatusNoContent || recorder.Body.Len() != 0 {
		t.Fatalf("admin without access cancels = %d %q; want 204 with no body", recorder.Code, recorder.Body.String())
	}

	// An admin who may read the sender gets the full broadcast back.
	second, err := f.service.CreateBroadcast(ctx, f.b, input)
	if err != nil {
		t.Fatalf("second draft: %v", err)
	}
	if _, err := f.service.SetMailboxMember(ctx, f.admin, f.team.ID, accessAdmin, domain.MailboxPermissionSend); err != nil {
		t.Fatalf("admin rejoins: %v", err)
	}
	if _, err := f.service.SendBroadcast(ctx, f.admin, second.ID, nil); err != nil {
		t.Fatalf("start second: %v", err)
	}
	if paused, visible, err := f.service.PauseBroadcast(ctx, f.admin, second.ID); err != nil || !visible || paused.Name != "Team news" {
		t.Fatalf("admin with access pauses = %+v, visible %v, %v", paused, visible, err)
	}
}

func TestBroadcastDeleteAndUpdateNeedSend(t *testing.T) {
	f := newPostgresAccessFixture(t)
	ctx := context.Background()
	templateID, listID := broadcastScene(t, f)
	input := BroadcastInput{Name: "Team news", MailboxID: f.team.ID, TemplateID: templateID, ListID: listID}
	draft, err := f.service.CreateBroadcast(ctx, f.b, input)
	if err != nil {
		t.Fatalf("CreateBroadcast: %v", err)
	}
	viewOnly, err := f.service.CreateMailbox(ctx, f.admin, CreateMailboxInput{
		LocalPart: "news", Type: domain.MailboxTypeShared,
		Members: []MailboxMemberInput{{UserID: accessB, Permission: domain.MailboxPermissionView}},
	})
	if err != nil {
		t.Fatalf("create view-only mailbox: %v", err)
	}

	_, err = f.service.UpdateBroadcast(ctx, f.c, draft.ID, input)
	expectError(t, "view member updates", err, domain.ErrPermissionDenied)
	err = f.service.DeleteBroadcast(ctx, f.c, draft.ID)
	expectError(t, "view member deletes", err, domain.ErrPermissionDenied)
	_, err = f.service.UpdateBroadcast(ctx, f.d, draft.ID, input)
	expectError(t, "outsider updates", err, ErrMailBroadcastNotFound)

	moved := input
	moved.MailboxID = viewOnly.ID
	_, err = f.service.UpdateBroadcast(ctx, f.b, draft.ID, moved)
	expectError(t, "moving to a mailbox the editor only views", err, domain.ErrPermissionDenied)
	moved.MailboxID = f.personal.ID
	if updated, updateErr := f.service.UpdateBroadcast(ctx, f.b, draft.ID, moved); updateErr != nil || updated.MailboxID != f.personal.ID {
		t.Fatalf("moving to the editor's personal mailbox = %+v, %v", updated, updateErr)
	}
	// Now on b@, the draft is out of team@ members' reach.
	err = f.service.DeleteBroadcast(ctx, f.c, draft.ID)
	expectError(t, "team member deletes b@'s draft", err, ErrMailBroadcastNotFound)
	if err := f.service.DeleteBroadcast(ctx, f.b, draft.ID); err != nil {
		t.Fatalf("owner deletes: %v", err)
	}
}

// AC-15: broadcasts of a mailbox the viewer may not read are not listed and answer as missing.
func TestBroadcastsHiddenWithoutView(t *testing.T) {
	f := newPostgresAccessFixture(t)
	ctx := context.Background()
	templateID, listID := broadcastScene(t, f)
	personal, err := f.service.CreateBroadcast(ctx, f.b, BroadcastInput{
		Name: "From b@", MailboxID: f.personal.ID, TemplateID: templateID, ListID: listID,
	})
	if err != nil {
		t.Fatalf("CreateBroadcast: %v", err)
	}
	team, err := f.service.CreateBroadcast(ctx, f.b, BroadcastInput{
		Name: "From team@", MailboxID: f.team.ID, TemplateID: templateID, ListID: listID,
	})
	if err != nil {
		t.Fatalf("CreateBroadcast: %v", err)
	}

	listed, total, err := f.service.ListBroadcasts(ctx, f.c, 1, 15)
	if err != nil || total != 1 || len(listed) != 1 || listed[0].ID != team.ID {
		t.Fatalf("C's broadcasts = %+v, total %d, %v; want only team@'s", listed, total, err)
	}
	_, err = f.service.GetBroadcast(ctx, f.c, personal.ID)
	expectError(t, "C opens b@'s broadcast", err, ErrMailBroadcastNotFound)
	_, _, err = f.service.ListBroadcastRecipients(ctx, f.c, personal.ID, "", 1, 15)
	expectError(t, "C lists b@'s broadcast recipients", err, ErrMailBroadcastNotFound)

	_, err = f.service.GetBroadcast(ctx, f.admin, personal.ID)
	expectError(t, "admin opens b@'s broadcast", err, domain.ErrPermissionDenied)
	if listed, total, err := f.service.ListBroadcasts(ctx, f.admin, 1, 15); err != nil || total != 0 || len(listed) != 0 {
		t.Fatalf("admin's broadcasts = %+v, total %d, %v; want none", listed, total, err)
	}
	if listed, total, err := f.service.ListBroadcasts(ctx, f.b, 1, 15); err != nil || total != 2 || len(listed) != 2 {
		t.Fatalf("B's broadcasts = %d, total %d, %v; want both", len(listed), total, err)
	}
}

// Concurrent additions never pass the member limit: the mailbox row lock counts them one at a time.
func TestMemberLimitUnderConcurrency(t *testing.T) {
	repo, db := openRepository(t)
	createOwner(t, db, 1)
	mailbox := createMailbox(t, repo, "crowded@mail.example")
	ctx := context.Background()
	// createMailbox added as(1); fill the mailbox to one below the limit.
	for user := uint(1); user < domain.MaxMailboxMembers-1; user++ {
		addMember(t, repo, mailbox.ID, viewerOf(1, user, domain.OrganizationRoleMember), domain.MailboxPermissionView)
	}
	add := func(ctx context.Context, user uint) error {
		_, err := repo.UpsertMailboxMember(ctx, &domain.MailboxMember{
			MailboxID: mailbox.ID, MembershipID: membershipIDOf(1, user), UserID: user,
			Permission: domain.MailboxPermissionView, GrantedBy: 1,
		}, domain.MaxMailboxMembers)
		return err
	}

	// The first addition holds its transaction open while a second one runs; the second must
	// wait for it and then find the mailbox full, instead of counting 99 and inserting too.
	first := db.Begin()
	if err := add(infradatabase.ContextWithTransaction(ctx, first), 5000); err != nil {
		first.Rollback()
		t.Fatalf("first addition: %v", err)
	}
	second := make(chan error, 1)
	go func() { second <- add(ctx, 5001) }()
	select {
	case err := <-second:
		first.Rollback()
		t.Fatalf("the second addition finished (%v) while the first was uncommitted; it must wait on the mailbox lock", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := first.Commit().Error; err != nil {
		t.Fatalf("commit first: %v", err)
	}
	if err := <-second; !errors.Is(err, ErrMailboxAccessInvalid) {
		t.Fatalf("second addition = %v; want the member limit", err)
	}

	// Free contenders racing for one remaining place: exactly one gets it.
	if _, err := repo.DeleteMailboxMember(ctx, mailbox.ID, 5000); err != nil {
		t.Fatalf("DeleteMailboxMember: %v", err)
	}
	const contenders = 8
	var wg sync.WaitGroup
	results := make([]error, contenders)
	for i := range contenders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = add(ctx, uint(6000+i))
		}()
	}
	wg.Wait()
	succeeded := 0
	for _, err := range results {
		switch {
		case err == nil:
			succeeded++
		case !errors.Is(err, ErrMailboxAccessInvalid):
			t.Fatalf("unexpected error: %v", err)
		}
	}
	members, err := repo.ListMailboxMembers(ctx, mailbox.ID)
	if succeeded != 1 || err != nil || len(members) != domain.MaxMailboxMembers {
		t.Fatalf("%d additions succeeded and the mailbox has %d members (%v); want exactly 1 and %d",
			succeeded, len(members), err, domain.MaxMailboxMembers)
	}
	// Changing an existing member's permission at the limit still works.
	if created, err := repo.UpsertMailboxMember(ctx, &domain.MailboxMember{
		MailboxID: mailbox.ID, MembershipID: as(1).MembershipID, UserID: testCreator, Permission: domain.MailboxPermissionSend, GrantedBy: 1,
	}, domain.MaxMailboxMembers); err != nil || created {
		t.Fatalf("update at the limit = created %v, %v", created, err)
	}
}

// TestDeletedMailboxesDoNotCrowdOutLiveOnes proves a member's rows on deleted mailboxes never push
// a live mailbox out of what they see: the membership listing is capped at the mailbox limit.
func TestDeletedMailboxesDoNotCrowdOutLiveOnes(t *testing.T) {
	f := newPostgresAccessFixture(t)
	ctx := context.Background()
	for i := 0; i < domain.MaxMailboxesPerOrganization; i++ {
		gone, err := f.service.CreateMailbox(ctx, f.admin, CreateMailboxInput{
			LocalPart: fmt.Sprintf("gone%d", i), Type: domain.MailboxTypeShared,
			Members: []MailboxMemberInput{{UserID: accessC, Permission: domain.MailboxPermissionView}},
		})
		if err != nil {
			t.Fatalf("create mailbox %d: %v", i, err)
		}
		if err := f.service.DeleteMailbox(ctx, f.admin, gone.ID); err != nil {
			t.Fatalf("delete mailbox %d: %v", i, err)
		}
	}
	live, err := f.service.CreateMailbox(ctx, f.admin, CreateMailboxInput{
		LocalPart: "live", Type: domain.MailboxTypeShared,
		Members: []MailboxMemberInput{{UserID: accessC, Permission: domain.MailboxPermissionView}},
	})
	if err != nil {
		t.Fatalf("create live mailbox: %v", err)
	}
	if got := listedMailbox(t, f.service, f.c, live.ID); got == nil || !got.Access.View {
		t.Fatalf("C's list lacks the live mailbox after 50 deleted ones: %+v", got)
	}
}

// TestSettingTheSamePermissionRecordsNothing keeps the access history to real changes.
func TestSettingTheSamePermissionRecordsNothing(t *testing.T) {
	f := newAccessFixture(t)
	before := len(accessEvents(t, f, f.admin, f.team.ID))
	if _, err := f.service.SetMailboxMember(context.Background(), f.admin, f.team.ID, accessC, domain.MailboxPermissionView); err != nil {
		t.Fatalf("SetMailboxMember: %v", err)
	}
	if after := len(accessEvents(t, f, f.admin, f.team.ID)); after != before {
		t.Fatalf("access events went from %d to %d for an unchanged permission", before, after)
	}
}
