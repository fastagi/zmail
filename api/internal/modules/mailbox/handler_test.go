package mailbox

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
	"github.com/fastagi/zmail/api/pkg/response"
)

// handlerFixture serves the handlers directly, with the organization context a real request
// would get from the organization_context middleware.
type handlerFixture struct {
	router  *gin.Engine
	service *service
	repo    *fakeRepository
	// inbox is repo with the unified inbox seam, the repository the service runs with.
	inbox *inboxRecorder
}

func newHandlerFixture(t *testing.T, organization *domain.OrganizationContext) *handlerFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	inbox := newInboxRecorder()
	repo := inbox.fakeRepository
	svc := NewService(inbox, &fakeMemberships{}, repo, newFakeContacts(), &stubBroadcasts{}, nil, nil, nil, nil, &fakeTransport{}, &fakeNotifier{}, Settings{
		Domain: "mail.example", InboundTolerance: 5 * time.Minute, DailySendLimit: 5,
	})
	handler := NewHandler(svc)
	response.DefaultErrorMapper.Register(domain.ErrInvalidInput, http.StatusBadRequest, domain.CodeInvalidInput)
	response.DefaultErrorMapper.Register(domain.ErrPermissionDenied, http.StatusForbidden, domain.CodePermissionDenied)
	response.DefaultErrorMapper.Register(
		domain.ErrOrganizationContextRequired, http.StatusBadRequest, domain.CodeOrganizationContextRequired)
	handler.RegisterErrorMappings(response.DefaultErrorMapper)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		if organization != nil {
			c.Request = c.Request.WithContext(domain.WithOrganizationContext(c.Request.Context(), *organization))
		}
		c.Next()
	})
	router.GET("/mailbox-domains", handler.ListDomains)
	router.GET("/mailboxes", handler.ListMailboxes)
	router.POST("/mailboxes", handler.CreateMailbox)
	router.DELETE("/mailboxes/:mailbox_id", handler.DeleteMailbox)
	router.GET("/mailboxes/:mailbox_id/messages", handler.ListMessages)
	router.POST("/mailboxes/:mailbox_id/messages", handler.SendMessage)
	router.GET("/mail-messages", handler.ListLog)
	router.GET("/inbox-messages", handler.ListInboxMessages)
	router.GET("/inbox-message-counts", handler.InboxMessageCounts)
	router.GET("/mail-roles", handler.ListMailRoles)
	router.POST("/mail-roles", handler.CreateMailRole)
	router.PUT("/mail-roles/:role_id", handler.UpdateMailRole)
	router.DELETE("/mail-roles/:role_id", handler.DeleteMailRole)
	router.PUT("/mailboxes/:mailbox_id/role-assignment", handler.AssignMailboxRole)
	router.POST("/mail-broadcasts/:broadcast_id/sends", handler.SendBroadcast)
	router.POST("/mail-broadcasts/:broadcast_id/cancellations", handler.CancelBroadcast)
	router.POST("/mail-broadcasts/:broadcast_id/pauses", handler.PauseBroadcast)
	router.POST("/mail-broadcasts/:broadcast_id/resumptions", handler.ResumeBroadcast)
	return &handlerFixture{router: router, service: svc, repo: repo, inbox: inbox}
}

func (f *handlerFixture) do(method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	f.router.ServeHTTP(recorder, request)
	decoded := map[string]any{}
	_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
	return recorder, decoded
}

// handlerUser is the user handler requests act as, in organization 7.
const handlerUser = 700

func membership(role domain.OrganizationRole) *domain.OrganizationContext {
	viewer := viewerOf(7, handlerUser, role)
	return &viewer
}

// sharedWithHandlerUser is a shared mailbox whose one member is the handler user, with send.
func sharedWithHandlerUser(localPart string) CreateMailboxInput {
	return CreateMailboxInput{
		LocalPart: localPart, DisplayName: "Helpdesk", Type: domain.MailboxTypeShared,
		Members: []MailboxMemberInput{{UserID: handlerUser, Permission: domain.MailboxPermissionSend}},
	}
}

func TestOnlyManagersCreateAndDeleteMailboxes(t *testing.T) {
	for _, role := range []domain.OrganizationRole{domain.OrganizationRoleOwner, domain.OrganizationRoleAdmin} {
		fixture := newHandlerFixture(t, membership(role))
		created, body := fixture.do(http.MethodPost, "/mailboxes",
			`{"local_part":"helpdesk","type":"shared","members":[{"user_id":700,"permission":"manage"}]}`)
		if created.Code != http.StatusCreated {
			t.Fatalf("%s create = %d %s", role, created.Code, created.Body.String())
		}
		stored := fixture.repo.mailboxes[1]
		if stored == nil || stored.OrganizationID != 7 || stored.CreatedBy != 700 {
			t.Fatalf("%s stored mailbox = %+v; want organization 7 created by user 700", role, stored)
		}
		if data, _ := body["data"].(map[string]any); data["address"] != "helpdesk@mail.example" {
			t.Fatalf("%s response = %v", role, body)
		}
		if deleted, _ := fixture.do(http.MethodDelete, "/mailboxes/1", ""); deleted.Code != http.StatusNoContent {
			t.Fatalf("%s delete = %d", role, deleted.Code)
		}
	}

	fixture := newHandlerFixture(t, membership(domain.OrganizationRoleMember))
	// A manager of the same organization created this one.
	if _, err := fixture.service.CreateMailbox(t.Context(), as(7), sharedInput("helpdesk", "", "")); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	created, body := fixture.do(http.MethodPost, "/mailboxes",
		`{"local_part":"sales","type":"shared","members":[{"user_id":700,"permission":"manage"}]}`)
	if created.Code != http.StatusForbidden || body["error_code"] != domain.CodePermissionDenied {
		t.Fatalf("member create = %d %v; want 403 %s", created.Code, body, domain.CodePermissionDenied)
	}
	deleted, body := fixture.do(http.MethodDelete, "/mailboxes/1", "")
	if deleted.Code != http.StatusForbidden || body["error_code"] != domain.CodePermissionDenied {
		t.Fatalf("member delete = %d %v; want 403", deleted.Code, body)
	}
	if len(fixture.repo.mailboxes) != 1 {
		t.Fatalf("mailboxes = %d; a member's requests must change nothing", len(fixture.repo.mailboxes))
	}
}

func TestMembersReadAndSendTheirMailboxes(t *testing.T) {
	fixture := newHandlerFixture(t, membership(domain.OrganizationRoleMember))
	if _, err := fixture.service.CreateMailbox(t.Context(), as(7), sharedWithHandlerUser("helpdesk")); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	if _, err := fixture.service.CreateMailbox(t.Context(), as(8), sharedInput("elsewhere", "", "")); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}

	listed, body := fixture.do(http.MethodGet, "/mailboxes", "")
	mailboxes, _ := body["data"].([]any)
	if listed.Code != http.StatusOK || len(mailboxes) != 1 {
		t.Fatalf("member list = %d %v; want only the organization's mailbox", listed.Code, body)
	}
	if listed.Header().Get("Vary") != "Authorization, Organization-Id" || listed.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("cache headers = %q / %q", listed.Header().Get("Vary"), listed.Header().Get("Cache-Control"))
	}

	sent, body := fixture.do(http.MethodPost, "/mailboxes/1/messages", `{"to":["bob@example.com"],"subject":"Hi","text":"Body"}`)
	if sent.Code != http.StatusCreated {
		t.Fatalf("member send = %d %v", sent.Code, body)
	}
	// Another organization's mailbox is reported as missing, not forbidden.
	other, body := fixture.do(http.MethodPost, "/mailboxes/2/messages", `{"to":["bob@example.com"],"subject":"Hi","text":"Body"}`)
	if other.Code != http.StatusNotFound || body["error_code"] != domain.CodeMailboxNotFound {
		t.Fatalf("send from another organization's mailbox = %d %v; want 404", other.Code, body)
	}

	log, body := fixture.do(http.MethodGet, "/mail-messages", "")
	if entries, _ := body["data"].([]any); log.Code != http.StatusOK || len(entries) != 1 {
		t.Fatalf("log = %d %v; want the one message of this organization", log.Code, body)
	}
}

func TestSendAtTheLimitAnswers202Queued(t *testing.T) {
	fixture := newHandlerFixture(t, membership(domain.OrganizationRoleMember))
	if _, err := fixture.service.CreateMailbox(t.Context(), as(7), sharedWithHandlerUser("helpdesk")); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	send := `{"to":["bob@example.com"],"subject":"Hi","text":"Body"}`
	for i := 0; i < 5; i++ {
		if sent, body := fixture.do(http.MethodPost, "/mailboxes/1/messages", send); sent.Code != http.StatusCreated {
			t.Fatalf("send %d = %d %v", i+1, sent.Code, body)
		}
	}
	queued, body := fixture.do(http.MethodPost, "/mailboxes/1/messages", send)
	data, _ := body["data"].(map[string]any)
	if queued.Code != http.StatusAccepted || data["status"] != "queued" || data["queued_reason"] != "send_limit" {
		t.Fatalf("send at the limit = %d %v; want 202 with a message queued for the send limit", queued.Code, body)
	}
	for _, key := range []string{"daily_send_limit", "sent", "remaining"} {
		if _, leaked := data[key]; leaked {
			t.Fatalf("queued answer exposes %q: %v", key, data)
		}
	}
}

func TestMailboxRoutesRequireAnOrganizationContext(t *testing.T) {
	fixture := newHandlerFixture(t, nil)
	for _, request := range [][2]string{
		{http.MethodGet, "/mailbox-domains"}, {http.MethodGet, "/mailboxes"},
		{http.MethodPost, "/mailboxes"}, {http.MethodDelete, "/mailboxes/1"},
		{http.MethodGet, "/mailboxes/1/messages"},
		{http.MethodPost, "/mailboxes/1/messages"}, {http.MethodGet, "/mail-messages"},
		{http.MethodGet, "/inbox-messages"}, {http.MethodGet, "/mail-roles"}, {http.MethodPost, "/mail-roles"},
		{http.MethodPut, "/mail-roles/1"}, {http.MethodDelete, "/mail-roles/1"},
		{http.MethodPut, "/mailboxes/1/role-assignment"},
	} {
		recorder, body := fixture.do(request[0], request[1], `{}`)
		if recorder.Code != http.StatusBadRequest || body["error_code"] != domain.CodeOrganizationContextRequired {
			t.Fatalf("%s %s = %d %v; want 400 %s", request[0], request[1], recorder.Code, body, domain.CodeOrganizationContextRequired)
		}
	}
}

func TestMailboxListCarriesRoleID(t *testing.T) {
	fixture := newHandlerFixture(t, membership(domain.OrganizationRoleMember))
	for _, localPart := range []string{"grouped", "loose"} {
		if _, err := fixture.service.CreateMailbox(t.Context(), as(7), sharedWithHandlerUser(localPart)); err != nil {
			t.Fatalf("CreateMailbox(%s): %v", localPart, err)
		}
	}
	fixture.repo.mailboxes[1].RoleID = new(uint(3))

	listed, body := fixture.do(http.MethodGet, "/mailboxes", "")
	mailboxes, _ := body["data"].([]any)
	if listed.Code != http.StatusOK || len(mailboxes) != 2 {
		t.Fatalf("list = %d %v", listed.Code, body)
	}
	for _, item := range mailboxes {
		mailbox, _ := item.(map[string]any)
		roleID, present := mailbox["role_id"]
		want := any(nil)
		if mailbox["id"] == float64(1) {
			want = float64(3)
		}
		if !present || roleID != want {
			t.Fatalf("mailbox %v role_id = %v (present %v); want %v", mailbox["id"], roleID, present, want)
		}
	}
}

func TestMailRoleErrorCodes(t *testing.T) {
	mapper := &response.ErrorMapper{}
	NewHandler(nil).RegisterErrorMappings(mapper)
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{ErrMailRoleNotFound, http.StatusNotFound, "MAILBOX.ROLE_NOT_FOUND"},
		{ErrMailRoleNameTaken, http.StatusConflict, "MAILBOX.ROLE_NAME_TAKEN"},
		{ErrMailRoleLimitReached, http.StatusUnprocessableEntity, "MAILBOX.ROLE_LIMIT_REACHED"},
	}
	for _, tc := range cases {
		got := mapper.Resolve(tc.err)
		if got.StatusCode != tc.status || got.ErrorCode != tc.code {
			t.Fatalf("%v maps to %d %s; want %d %s", tc.err, got.StatusCode, got.ErrorCode, tc.status, tc.code)
		}
	}
}

func TestListMessagesFiltersBySearchAndUnread(t *testing.T) {
	fixture := newHandlerFixture(t, membership(domain.OrganizationRoleMember))
	fixture.service.settings.InboundSecret = testInboundSecret
	fixture.service.now = func() time.Time { return time.Unix(1_800_000_000, 0).UTC() }

	viewer := *membership(domain.OrganizationRoleMember)
	// The handler user sends; a second member reads, with read state of their own.
	mine, err := fixture.service.CreateMailbox(t.Context(), as(7), CreateMailboxInput{
		LocalPart: "helpdesk", DisplayName: "Helpdesk", Type: domain.MailboxTypeShared,
		Members: []MailboxMemberInput{
			{UserID: handlerUser, Permission: domain.MailboxPermissionSend},
			{UserID: testCreator, Permission: domain.MailboxPermissionView},
		},
	})
	if err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	foreign, err := fixture.service.CreateMailbox(t.Context(), as(8), sharedInput("elsewhere", "", ""))
	if err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}

	deliver := func(messageID, from, subject string) *domain.MailMessage {
		t.Helper()
		raw := crlf(
			"From: "+from,
			"To: helpdesk@mail.example",
			"Subject: "+subject,
			"Message-ID: <"+messageID+">",
			"",
			"Body text.",
		)
		stored, _, deliverErr := fixture.service.ReceiveInbound(t.Context(), InboundDelivery{
			Recipient: "helpdesk@mail.example",
			Signature: signedHeader(fixture.service.now(), "helpdesk@mail.example", raw),
			Raw:       raw,
		})
		if deliverErr != nil {
			t.Fatalf("ReceiveInbound: %v", deliverErr)
		}
		return stored
	}

	invoice := deliver("m-1@sender.test", "Alice <alice@sender.test>", "Invoice 100% due")
	welcome := deliver("m-2@sender.test", "Carol <carol@sender.test>", "Welcome aboard")
	if _, err := fixture.service.SetMessageRead(t.Context(), viewer, mine.ID, welcome.ID, true); err != nil {
		t.Fatalf("SetMessageRead: %v", err)
	}
	// Read state is per person: another member reading the invoice leaves it unread here.
	if _, err := fixture.service.SetMessageRead(t.Context(), as(7), mine.ID, invoice.ID, true); err != nil {
		t.Fatalf("SetMessageRead(other member): %v", err)
	}
	if _, _, err := fixture.service.SendMessage(t.Context(), viewer, mine.ID, SendInput{
		To: []string{"bob@example.com"}, Cc: []string{"dan@example.org"}, Subject: "Receipt", Text: "Paid",
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	subjects := func(path string) []string {
		t.Helper()
		recorder, body := fixture.do(http.MethodGet, path, "")
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s = %d %v", path, recorder.Code, body)
		}
		items, _ := body["data"].([]any)
		out := make([]string, 0, len(items))
		for _, item := range items {
			row, _ := item.(map[string]any)
			subject, _ := row["subject"].(string)
			out = append(out, subject)
		}
		return out
	}

	if got := subjects("/mailboxes/1/messages?unread=true"); len(got) != 1 || got[0] != "Invoice 100% due" {
		t.Fatalf("unread=true = %v; want [Invoice 100%% due]", got)
	}
	if got := subjects("/mailboxes/1/messages?unread=false"); len(got) != 2 || got[0] != "Receipt" || got[1] != "Welcome aboard" {
		t.Fatalf("unread=false = %v; want [Receipt Welcome aboard]", got)
	}
	if got := subjects("/mailboxes/1/messages?q=%20%20INVOICE%20%20"); len(got) != 1 || got[0] != "Invoice 100% due" {
		t.Fatalf("q=INVOICE = %v; want [Invoice 100%% due]", got)
	}
	if got := subjects("/mailboxes/1/messages?direction=outbound&q=dan@example.org"); len(got) != 1 || got[0] != "Receipt" {
		t.Fatalf("outbound cc search = %v; want [Receipt]", got)
	}
	if got := subjects("/mailboxes/1/messages?direction=inbound&unread=true&q=welcome"); len(got) != 0 {
		t.Fatalf("combined non-matching filter = %v; want empty", got)
	}

	for _, query := range []string{
		"direction=sideways",
		"unread=maybe",
		"unread=1",
		"unread=true&unread=false",
		"q=" + strings.Repeat("q", 201),
		"q=bad%00char",
		"q=a&q=b",
	} {
		recorder, body := fixture.do(http.MethodGet, "/mailboxes/1/messages?"+query, "")
		if recorder.Code != http.StatusBadRequest || body["error_code"] != domain.CodeInvalidInput {
			t.Fatalf("GET /mailboxes/1/messages?%s = %d %v; want 400 %s", query, recorder.Code, body, domain.CodeInvalidInput)
		}
	}

	other, body := fixture.do(http.MethodGet, "/mailboxes/"+string(rune('0'+foreign.ID))+"/messages?unread=true&q=invoice", "")
	if other.Code != http.StatusNotFound || body["error_code"] != domain.CodeMailboxNotFound {
		t.Fatalf("other organization mailbox list = %d %v; want 404 %s", other.Code, body, domain.CodeMailboxNotFound)
	}
}

func TestInboxRequestValidation(t *testing.T) {
	fixture := newHandlerFixture(t, membership(domain.OrganizationRoleMember))
	encode := func(raw string) string { return base64.RawURLEncoding.EncodeToString([]byte(raw)) }
	for _, query := range []string{
		"view=bogus",
		"view=",
		"view=all&view=sent",
		"page=1",
		"view=today&q=x",
		"view=sent&q=x",
		"group=thread&unread=true",
		"group=thread&q=x",
		"group=thread&view=today",
		"group=thread&view=pending",
		"group=threads",
		"q=" + strings.Repeat("q", 201),
		"q=%20%20",
		"q=bad%00char",
		"tz=Mars/Base",
		"tz=Local",
		"tz=",
		"tz=" + strings.Repeat("A", 65),
		"tz=../../etc/passwd",
		"tz=UTC&tz=UTC",
		"per_page=abc",
		"per_page=",
		"per_page=1.5",
		"role_id=0",
		"role_id=-1",
		"role_id=abc",
		"role_id=",
		"mailbox_id=0",
		"mailbox_id=x",
		"unread=1",
		"cursor=" + encode("x.y"),
		"cursor=" + strings.Repeat("A", 129),
		"cursor=",
		"cursor=%%%",
	} {
		recorder, body := fixture.do(http.MethodGet, "/inbox-messages?"+query, "")
		if recorder.Code != http.StatusBadRequest || body["error_code"] != domain.CodeInvalidInput {
			t.Fatalf("GET /inbox-messages?%s = %d %v; want 400 %s", query, recorder.Code, body, domain.CodeInvalidInput)
		}
	}
	if len(fixture.inbox.queries) != 0 {
		t.Fatalf("inbox queries = %+v; a refused request must read nothing", fixture.inbox.queries)
	}

	for query, want := range map[string]float64{
		"per_page=0": 1, "per_page=-3": 1, "per_page=101": 100, "per_page=99999999999999999999": 100, "per_page=7": 7, "": 15,
		"role_id=none": 15, "view=sent&group=thread": 15, "tz=Asia/Shanghai": 15,
	} {
		recorder, body := fixture.do(http.MethodGet, "/inbox-messages?"+query, "")
		meta, _ := body["meta"].(map[string]any)
		if recorder.Code != http.StatusOK || meta["per_page"] != want {
			t.Fatalf("GET /inbox-messages?%s = %d %v; want 200 with per_page %v", query, recorder.Code, body, want)
		}
	}
}

func TestInboxItemShape(t *testing.T) {
	fixture := newHandlerFixture(t, membership(domain.OrganizationRoleMember))
	viewer := *membership(domain.OrganizationRoleMember)
	mailbox, err := fixture.service.CreateMailbox(t.Context(), as(7), sharedWithHandlerUser("helpdesk"))
	if err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	received := &domain.MailMessage{
		MailboxID: mailbox.ID, Direction: domain.MailDirectionInbound, Status: domain.MailStatusReceived,
		FromAddress: "ann@example.com", To: []string{mailbox.Address}, Subject: "Seats",
		TextBody: "Hi,\n\n  more   seats?", DedupeKey: "seats", CreatedAt: time.Unix(1_800_000_000, 0).UTC(),
	}
	if _, err = fixture.repo.CreateInboundMessage(t.Context(), received); err != nil {
		t.Fatalf("CreateInboundMessage: %v", err)
	}
	if _, err = fixture.service.SetMessageRead(t.Context(), viewer, mailbox.ID, received.ID, true); err != nil {
		t.Fatalf("SetMessageRead: %v", err)
	}
	if _, err = fixture.repo.CreateMessage(t.Context(), &domain.MailMessage{
		MailboxID: mailbox.ID, Direction: domain.MailDirectionOutbound, Status: domain.MailStatusSent,
		FromAddress: mailbox.Address, To: []string{"ann@example.com"}, Subject: "Re: Seats",
		CreatedAt: time.Unix(1_800_000_100, 0).UTC(),
	}); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	only := func(path string) (*httptest.ResponseRecorder, map[string]any, map[string]any) {
		t.Helper()
		recorder, body := fixture.do(http.MethodGet, path, "")
		items, _ := body["data"].([]any)
		if recorder.Code != http.StatusOK || len(items) != 1 {
			t.Fatalf("GET %s = %d %v; want one item", path, recorder.Code, body)
		}
		item, _ := items[0].(map[string]any)
		return recorder, body, item
	}

	recorder, body, item := only("/inbox-messages")
	if recorder.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("Cache-Control = %q; want private, no-store", recorder.Header().Get("Cache-Control"))
	}
	meta, _ := body["meta"].(map[string]any)
	if nextCursor, present := meta["next_cursor"]; !present || nextCursor != nil || meta["has_more"] != false {
		t.Fatalf("meta = %v; the last page has has_more false and next_cursor null", meta)
	}
	roleID, hasRole := item["role_id"]
	if item["mailbox_address"] != "helpdesk@mail.example" || !hasRole || roleID != nil ||
		item["snippet"] != "Hi, more seats?" || item["read"] != true || item["starred"] != false ||
		item["subject"] != "Seats" || item["direction"] != "inbound" {
		t.Fatalf("item = %v; want the inbound summary with mailbox_address, role_id null, snippet, own state", item)
	}
	if _, grouped := item["thread"]; grouped {
		t.Fatalf("item = %v; thread only appears with group=thread", item)
	}
	for _, field := range []string{"text_body", "html_body", "message_id", "in_reply_to"} {
		if _, present := item[field]; present {
			t.Fatalf("item = %v; it must not carry %s", item, field)
		}
	}

	_, _, sent := only("/inbox-messages?view=sent")
	if sent["direction"] != "outbound" || sent["read"] != true || sent["snippet"] != "" {
		t.Fatalf("sent item = %v; outbound mail is always read", sent)
	}

	_, _, grouped := only("/inbox-messages?group=thread")
	if thread, _ := grouped["thread"].(map[string]any); thread["message_count"] != float64(1) {
		t.Fatalf("grouped item = %v; want its thread", grouped)
	}

	// A page that is not the last carries the cursor of its last item.
	fixture.inbox.messages[received.ID+100] = &domain.MailMessage{
		ID: received.ID + 100, MailboxID: mailbox.ID, Direction: domain.MailDirectionInbound,
		Status: domain.MailStatusReceived, Subject: "Later", CreatedAt: received.CreatedAt.Add(time.Minute),
	}
	_, body, _ = only("/inbox-messages?per_page=1")
	meta, _ = body["meta"].(map[string]any)
	next, _ := meta["next_cursor"].(string)
	cursor, err := decodeInboxCursor(next)
	if meta["has_more"] != true || err != nil || cursor.ID != received.ID+100 {
		t.Fatalf("meta = %v (%v); want the cursor of the newest message", meta, err)
	}
	_, _, last := only("/inbox-messages?per_page=1&cursor=" + next)
	if last["id"] != float64(received.ID) {
		t.Fatalf("second page = %v; want the older message", last)
	}
}

func TestInboxCountsRejectsOtherParameters(t *testing.T) {
	fixture := newHandlerFixture(t, membership(domain.OrganizationRoleMember))
	if _, err := fixture.service.CreateMailbox(t.Context(), as(7), sharedWithHandlerUser("helpdesk")); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	for _, query := range []string{
		"view=all", "tz=UTC&tz=UTC", "tz=UTC&per_page=5", "cursor=x", "page=1",
		"tz=", "tz=Local", "tz=Mars/Base", "tz=" + strings.Repeat("A", 65), "tz=%zz",
	} {
		recorder, body := fixture.do(http.MethodGet, "/inbox-message-counts?"+query, "")
		expectStatus(t, "GET /inbox-message-counts?"+query, recorder.Code, body, http.StatusBadRequest, domain.CodeInvalidInput)
	}
	if len(fixture.inbox.unreadCalls) != 0 || len(fixture.inbox.viewCalls) != 0 {
		t.Fatalf("count calls = %v, %v; a refused request must count nothing", fixture.inbox.unreadCalls, fixture.inbox.viewCalls)
	}

	recorder, body := fixture.do(http.MethodGet, "/inbox-message-counts?tz=Asia/Shanghai", "")
	expectStatus(t, "GET /inbox-message-counts?tz=Asia/Shanghai", recorder.Code, body, http.StatusOK, "")
	if recorder.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("Cache-Control = %q; want private, no-store", recorder.Header().Get("Cache-Control"))
	}
	data, _ := body["data"].(map[string]any)
	views, _ := data["views"].(map[string]any)
	for _, field := range []string{"all_unread", "today", "today_unread", "pending", "sent_waiting"} {
		if _, present := views[field]; !present {
			t.Fatalf("views = %v; want %s", views, field)
		}
	}
	roles, rolesOK := data["roles"].([]any)
	mailboxes, _ := data["mailboxes"].([]any)
	if !rolesOK || len(roles) != 0 || len(mailboxes) != 1 || data["ungrouped_unread"] != float64(0) {
		t.Fatalf("data = %v; want no roles and the one readable mailbox", data)
	}
	if first, _ := mailboxes[0].(map[string]any); first["mailbox_id"] == nil || first["unread"] != float64(0) {
		t.Fatalf("mailbox entry = %v; want mailbox_id and unread", mailboxes[0])
	}
	for _, field := range []string{"today_starts_at", "today_ends_at"} {
		value, _ := data[field].(string)
		if at, err := time.Parse(time.RFC3339, value); err != nil || !strings.HasSuffix(value, "Z") || at.IsZero() {
			t.Fatalf("%s = %q; want an RFC 3339 UTC time", field, value)
		}
	}
	if len(data) != 6 {
		t.Fatalf("data = %v; want exactly views, roles, ungrouped_unread, mailboxes, today_starts_at, today_ends_at", data)
	}

	recorder, body = fixture.do(http.MethodGet, "/inbox-message-counts", "")
	expectStatus(t, "GET /inbox-message-counts", recorder.Code, body, http.StatusOK, "")
	data, _ = body["data"].(map[string]any)
	if data["today_starts_at"] == nil {
		t.Fatalf("data = %v; tz defaults to UTC", data)
	}
}

func TestInboxSearchTooBroadIs422(t *testing.T) {
	fixture := newHandlerFixture(t, membership(domain.OrganizationRoleMember))
	if _, err := fixture.service.CreateMailbox(t.Context(), as(7), sharedWithHandlerUser("helpdesk")); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	fixture.inbox.listErr = fmt.Errorf("list inbox: %w", ErrMailSearchTooBroad)
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	recorder, body := fixture.do(http.MethodGet, "/inbox-messages?q=%20invoice%20", "")
	expectStatus(t, "GET a search that is too broad", recorder.Code, body, http.StatusUnprocessableEntity, domain.CodeMailSearchTooBroad)

	var event map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(logs.String()), "\n") {
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) == nil && entry["msg"] == "mail.inbox.search_too_broad" {
			event = entry
		}
	}
	if event == nil || event["organization_id"] != float64(7) || event["query_length"] != float64(len("invoice")) ||
		event["readable_mailboxes"] != float64(1) {
		t.Fatalf("logs = %s; want mail.inbox.search_too_broad with organization 7, query length 7, 1 readable mailbox", logs.String())
	}
	if _, present := event["request_id"]; !present {
		t.Fatalf("event = %v; want request_id", event)
	}
	if strings.Contains(logs.String(), "invoice") {
		t.Fatalf("logs = %s; the query never reaches the log", logs.String())
	}
}

func TestOnlyOwnersAndAdminsControlBroadcasts(t *testing.T) {
	fixture := newHandlerFixture(t, membership(domain.OrganizationRoleMember))
	for _, path := range []string{
		"/mail-broadcasts/3/sends", "/mail-broadcasts/3/cancellations",
		"/mail-broadcasts/3/pauses", "/mail-broadcasts/3/resumptions",
	} {
		recorder, body := fixture.do(http.MethodPost, path, "{}")
		if recorder.Code != http.StatusForbidden || body["error_code"] != domain.CodePermissionDenied {
			t.Fatalf("member POST %s = %d %v; want 403", path, recorder.Code, body)
		}
	}
}

// TestOrganizeErrorCodes maps every organize error to the status and code the contract names.
func TestOrganizeErrorCodes(t *testing.T) {
	mapper := &response.ErrorMapper{}
	NewHandler(nil).RegisterErrorMappings(mapper)
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{ErrMailFolderNotFound, http.StatusNotFound, domain.CodeMailFolderNotFound},
		{ErrMailLabelNotFound, http.StatusNotFound, domain.CodeMailLabelNotFound},
		{ErrMailFolderNameTaken, http.StatusConflict, domain.CodeMailFolderNameTaken},
		{ErrMailLabelNameTaken, http.StatusConflict, domain.CodeMailLabelNameTaken},
		{ErrMailFolderLimitReached, http.StatusUnprocessableEntity, domain.CodeMailFolderLimitReached},
		{ErrMailLabelLimitReached, http.StatusUnprocessableEntity, domain.CodeMailLabelLimitReached},
	}
	for _, tc := range cases {
		got := mapper.Resolve(tc.err)
		if got.StatusCode != tc.status || got.ErrorCode != tc.code {
			t.Fatalf("%v maps to %d %s; want %d %s", tc.err, got.StatusCode, got.ErrorCode, tc.status, tc.code)
		}
	}
	if domain.CodeMailSearchTooBroad != "MAILBOX.SEARCH_TOO_BROAD" {
		t.Fatalf("search code = %q", domain.CodeMailSearchTooBroad)
	}
}

// fakeResolver answers public DNS lookups from fixed tables, so no test reaches real DNS.
type fakeResolver struct {
	mx      map[string][]*net.MX
	ns      map[string][]*net.NS
	mxErr   error
	nsErr   error
	nsCalls []string
}

func (r *fakeResolver) LookupMX(_ context.Context, name string) ([]*net.MX, error) {
	if r.mxErr != nil {
		return nil, r.mxErr
	}
	return r.mx[name], nil
}

func (r *fakeResolver) LookupNS(_ context.Context, name string) ([]*net.NS, error) {
	r.nsCalls = append(r.nsCalls, name)
	if r.nsErr != nil {
		return nil, r.nsErr
	}
	return r.ns[name], nil
}

// zoneHandlerFixture serves the domain and zone handlers over the in-memory domain fakes.
type zoneHandlerFixture struct {
	*domainFixture
	router   *gin.Engine
	resolver *fakeResolver
}

func newZoneHandlerFixture(t *testing.T, organization *domain.OrganizationContext) *zoneHandlerFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	domains := newDomainFixture(t)
	resolver := &fakeResolver{mx: map[string][]*net.MX{}, ns: map[string][]*net.NS{}}
	domains.service.mxResolver = resolver
	domains.service.nsResolver = resolver
	handler := NewHandler(domains.service)
	response.DefaultErrorMapper.Register(domain.ErrPermissionDenied, http.StatusForbidden, domain.CodePermissionDenied)
	handler.RegisterErrorMappings(response.DefaultErrorMapper)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(domain.WithOrganizationContext(c.Request.Context(), *organization))
		c.Next()
	})
	router.POST("/mail-domains", handler.ConnectMailDomain)
	router.POST("/mail-domains/:domain_id/receiving", handler.EnableMailDomainReceiving)
	router.POST("/mail-domain-previews", handler.PreviewMailDomain)
	router.GET("/mail-zones", handler.ListMailZones)
	router.GET("/mail-zones/:zone_id", handler.GetMailZone)
	router.DELETE("/mail-zones/:zone_id", handler.DeleteMailZone)
	return &zoneHandlerFixture{domainFixture: domains, router: router, resolver: resolver}
}

func (f *zoneHandlerFixture) do(method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	f.router.ServeHTTP(recorder, request)
	decoded := map[string]any{}
	_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
	return recorder, decoded
}

// activeZone stores an active managed zone of organization 7 that the provider serves.
func (f *zoneHandlerFixture) activeZone(name, providerID string) *domain.MailZone {
	f.zones.zones[name] = providerID
	f.zones.zoneStatus[providerID] = "active"
	return f.mailZones.store(domain.MailZone{
		OrganizationID: 7, Name: name, CloudflareZoneID: providerID, Status: domain.MailZoneStatusActive,
		NameServers: fakeNameServers, OriginalNameServers: []string{"ns1.registrar.test", "ns2.registrar.test"},
	})
}

func TestPreviewSuggestsSendOnlyForForeignMX(t *testing.T) {
	fixture := newZoneHandlerFixture(t, membership(domain.OrganizationRoleAdmin))
	fixture.resolver.mx["mail.example.org"] = []*net.MX{{Host: "MXBIZ1.qq.com.", Pref: 5}, {Host: "route1.mx.cloudflare.net.", Pref: 10}}
	fixture.resolver.mx["news.example.org"] = []*net.MX{{Host: "route1.mx.cloudflare.net.", Pref: 10}}

	recorder, body := fixture.do(http.MethodPost, "/mail-domain-previews", `{"name":" Mail.Example.org. "}`)
	data, _ := body["data"].(map[string]any)
	if recorder.Code != http.StatusOK || data["name"] != "mail.example.org" || data["apex"] != "example.org" ||
		data["suggested_capability"] != "send_only" {
		t.Fatalf("preview = %d %v", recorder.Code, body)
	}
	if mx, _ := data["existing_mx"].([]any); len(mx) != 2 || mx[0] != "mxbiz1.qq.com" || mx[1] != "route1.mx.cloudflare.net" {
		t.Fatalf("existing_mx = %v", data["existing_mx"])
	}

	// Mail that already reaches Cloudflare keeps the default.
	_, body = fixture.do(http.MethodPost, "/mail-domain-previews", `{"name":"news.example.org"}`)
	if data, _ := body["data"].(map[string]any); data["suggested_capability"] != "send_receive" {
		t.Fatalf("cloudflare MX preview = %v", body)
	}
	invalid, body := fixture.do(http.MethodPost, "/mail-domain-previews", `{"name":"com"}`)
	if invalid.Code != http.StatusUnprocessableEntity || body["error_code"] != domain.CodeMailDomainInvalid {
		t.Fatalf("invalid preview = %d %v", invalid.Code, body)
	}
	if len(fixture.domains.domains) != 0 || len(fixture.zones.createdZones) != 0 {
		t.Fatalf("a preview changed state: domains %v, zones %v", fixture.domains.domains, fixture.zones.createdZones)
	}
}

func TestPreviewLookupFailureSuggestsSendReceive(t *testing.T) {
	fixture := newZoneHandlerFixture(t, membership(domain.OrganizationRoleMember))
	fixture.resolver.mxErr = &net.DNSError{Err: "no such host", Name: "example.org", IsNotFound: true}

	recorder, body := fixture.do(http.MethodPost, "/mail-domain-previews", `{"name":"example.org"}`)
	data, _ := body["data"].(map[string]any)
	mx, isList := data["existing_mx"].([]any)
	if recorder.Code != http.StatusOK || !isList || len(mx) != 0 || data["suggested_capability"] != "send_receive" {
		t.Fatalf("preview = %d %v", recorder.Code, body)
	}
}

func TestListZonesScopedToOrganization(t *testing.T) {
	fixture := newZoneHandlerFixture(t, membership(domain.OrganizationRoleMember))
	own := fixture.activeZone("example.org", "cf-own")
	other := fixture.mailZones.store(domain.MailZone{
		OrganizationID: 8, Name: "other.test", CloudflareZoneID: "cf-other", Status: domain.MailZoneStatusPending,
	})

	listed, body := fixture.do(http.MethodGet, "/mail-zones", "")
	items, _ := body["data"].([]any)
	if listed.Code != http.StatusOK || len(items) != 1 {
		t.Fatalf("list = %d %v", listed.Code, body)
	}
	if listed.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("cache = %q", listed.Header().Get("Cache-Control"))
	}
	item, _ := items[0].(map[string]any)
	servers, _ := item["name_servers"].([]any)
	original, _ := item["original_name_servers"].([]any)
	if item["id"] != float64(own.ID) || item["name"] != "example.org" || item["status"] != "active" ||
		len(servers) != 2 || len(original) != 2 {
		t.Fatalf("zone = %v", item)
	}
	if _, present := item["cloudflare_zone_id"]; present {
		t.Fatalf("the provider zone ID leaked: %v", item)
	}

	shown, body := fixture.do(http.MethodGet, fmt.Sprintf("/mail-zones/%d", own.ID), "")
	if data, _ := body["data"].(map[string]any); shown.Code != http.StatusOK || data["name"] != "example.org" {
		t.Fatalf("show = %d %v", shown.Code, body)
	}
	foreign, body := fixture.do(http.MethodGet, fmt.Sprintf("/mail-zones/%d", other.ID), "")
	if foreign.Code != http.StatusNotFound || body["error_code"] != domain.CodeMailZoneNotFound {
		t.Fatalf("another organization's zone = %d %v", foreign.Code, body)
	}
}

func TestDeleteZoneInUse(t *testing.T) {
	fixture := newZoneHandlerFixture(t, membership(domain.OrganizationRoleOwner))
	zone := fixture.activeZone("example.org", "cf-own")
	fixture.connect(t, "mail.example.org")

	recorder, body := fixture.do(http.MethodDelete, fmt.Sprintf("/mail-zones/%d", zone.ID), "")
	if recorder.Code != http.StatusConflict || body["error_code"] != domain.CodeMailZoneInUse {
		t.Fatalf("delete = %d %v", recorder.Code, body)
	}
	if len(fixture.resolver.nsCalls) != 0 || len(fixture.zones.deletedZones) != 0 || fixture.mailZones.zones[zone.ID] == nil {
		t.Fatalf("ns lookups %v, deleted %v", fixture.resolver.nsCalls, fixture.zones.deletedZones)
	}
}

func TestDeleteZoneStillDelegated(t *testing.T) {
	fixture := newZoneHandlerFixture(t, membership(domain.OrganizationRoleAdmin))
	zone := fixture.activeZone("example.org", "cf-own")
	fixture.resolver.ns["example.org"] = []*net.NS{{Host: "ns1.registrar.test."}, {Host: "ADA.ns.cloudflare.com."}}
	path := fmt.Sprintf("/mail-zones/%d", zone.ID)

	recorder, body := fixture.do(http.MethodDelete, path, "")
	if recorder.Code != http.StatusConflict || body["error_code"] != domain.CodeMailZoneStillDelegated {
		t.Fatalf("delete while delegated = %d %v", recorder.Code, body)
	}
	if len(fixture.zones.deletedZones) != 0 || fixture.mailZones.zones[zone.ID] == nil {
		t.Fatalf("a delegated zone was removed: deleted %v", fixture.zones.deletedZones)
	}

	// Once the registrar points elsewhere the zone leaves Zmail and the provider account.
	fixture.resolver.ns["example.org"] = []*net.NS{{Host: "ns1.registrar.test."}}
	recorder, body = fixture.do(http.MethodDelete, path, "")
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %v", recorder.Code, body)
	}
	if !slices.Equal(fixture.zones.deletedZones, []string{"cf-own"}) || fixture.mailZones.zones[zone.ID] != nil {
		t.Fatalf("deleted %v, stored %v", fixture.zones.deletedZones, fixture.mailZones.zones)
	}
	gone, body := fixture.do(http.MethodDelete, path, "")
	if gone.Code != http.StatusNotFound || body["error_code"] != domain.CodeMailZoneNotFound {
		t.Fatalf("delete again = %d %v", gone.Code, body)
	}
}

func TestDeleteZoneRefusesOnLookupError(t *testing.T) {
	fixture := newZoneHandlerFixture(t, membership(domain.OrganizationRoleAdmin))
	zone := fixture.activeZone("example.org", "cf-own")
	fixture.resolver.nsErr = &net.DNSError{Err: "server misbehaving", Name: "example.org", IsTemporary: true}

	recorder, body := fixture.do(http.MethodDelete, fmt.Sprintf("/mail-zones/%d", zone.ID), "")
	if recorder.Code != http.StatusConflict || body["error_code"] != domain.CodeMailZoneStillDelegated {
		t.Fatalf("delete = %d %v", recorder.Code, body)
	}
	if len(fixture.zones.deletedZones) != 0 || fixture.mailZones.zones[zone.ID] == nil {
		t.Fatalf("zone removed without knowing its delegation: deleted %v", fixture.zones.deletedZones)
	}
}

func TestDeleteZoneToleratesAZoneGoneAtTheProvider(t *testing.T) {
	fixture := newZoneHandlerFixture(t, membership(domain.OrganizationRoleAdmin))
	zone := fixture.activeZone("example.org", "cf-own")
	fixture.zones.deleteZoneErr = &mailtransport.DomainError{Operation: "delete zone", Status: http.StatusNotFound}
	if recorder, body := fixture.do(http.MethodDelete, fmt.Sprintf("/mail-zones/%d", zone.ID), ""); recorder.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %v", recorder.Code, body)
	}

	// Any other provider refusal keeps the zone so the removal can be tried again.
	kept := fixture.activeZone("example.net", "cf-net")
	fixture.zones.deleteZoneErr = &mailtransport.DomainError{Operation: "delete zone", Status: http.StatusForbidden}
	recorder, body := fixture.do(http.MethodDelete, fmt.Sprintf("/mail-zones/%d", kept.ID), "")
	if recorder.Code != http.StatusBadGateway || body["error_code"] != domain.CodeMailDomainProviderFailed {
		t.Fatalf("provider refusal = %d %v", recorder.Code, body)
	}
	if fixture.mailZones.zones[kept.ID] == nil {
		t.Fatal("zone row removed although the provider kept the zone")
	}
}

// A pending zone is removed only once the registrar does not point at Cloudflare either: a
// registrar may already delegate to a zone the provider has not activated yet. A name that does
// not exist at all cannot be delegated, so NXDOMAIN lets a pending zone go.
func TestDeletePendingZoneRequiresUndelegation(t *testing.T) {
	fixture := newZoneHandlerFixture(t, membership(domain.OrganizationRoleOwner))
	zone := fixture.mailZones.store(domain.MailZone{
		OrganizationID: 7, Name: "example.org", CloudflareZoneID: "cf-pending", Status: domain.MailZoneStatusPending,
	})
	fixture.zones.zones["example.org"] = "cf-pending"
	path := fmt.Sprintf("/mail-zones/%d", zone.ID)

	fixture.resolver.ns["example.org"] = []*net.NS{{Host: "ada.ns.cloudflare.com."}}
	recorder, body := fixture.do(http.MethodDelete, path, "")
	if recorder.Code != http.StatusConflict || body["error_code"] != domain.CodeMailZoneStillDelegated {
		t.Fatalf("delete while delegated = %d %v", recorder.Code, body)
	}
	fixture.resolver.nsErr = &net.DNSError{Err: "server misbehaving", Name: "example.org", IsTemporary: true}
	recorder, body = fixture.do(http.MethodDelete, path, "")
	if recorder.Code != http.StatusConflict || body["error_code"] != domain.CodeMailZoneStillDelegated {
		t.Fatalf("delete on a lookup error = %d %v", recorder.Code, body)
	}
	if len(fixture.zones.deletedZones) != 0 || fixture.mailZones.zones[zone.ID] == nil {
		t.Fatalf("a possibly delegated pending zone was removed: deleted %v", fixture.zones.deletedZones)
	}

	fixture.resolver.nsErr = &net.DNSError{Err: "no such host", Name: "example.org", IsNotFound: true}
	recorder, body = fixture.do(http.MethodDelete, path, "")
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete of an unregistered name = %d %v", recorder.Code, body)
	}
	if !slices.Equal(fixture.zones.deletedZones, []string{"cf-pending"}) || fixture.mailZones.zones[zone.ID] != nil {
		t.Fatalf("deleted %v, stored %v", fixture.zones.deletedZones, fixture.mailZones.zones)
	}
}

// NXDOMAIN lets only a pending zone go; an active zone serves its name, so the answer is wrong.
func TestDeleteActiveZoneRefusesOnNXDOMAIN(t *testing.T) {
	fixture := newZoneHandlerFixture(t, membership(domain.OrganizationRoleAdmin))
	zone := fixture.activeZone("example.org", "cf-own")
	fixture.resolver.nsErr = &net.DNSError{Err: "no such host", Name: "example.org", IsNotFound: true}
	recorder, body := fixture.do(http.MethodDelete, fmt.Sprintf("/mail-zones/%d", zone.ID), "")
	if recorder.Code != http.StatusConflict || body["error_code"] != domain.CodeMailZoneStillDelegated {
		t.Fatalf("delete = %d %v", recorder.Code, body)
	}
	if len(fixture.zones.deletedZones) != 0 || fixture.mailZones.zones[zone.ID] == nil {
		t.Fatalf("active zone removed: deleted %v", fixture.zones.deletedZones)
	}
}

func TestReceivingOnPendingZone409(t *testing.T) {
	fixture := newZoneHandlerFixture(t, membership(domain.OrganizationRoleAdmin))
	pending, err := fixture.service.ConnectMailDomain(context.Background(), 7, testCreator, "mail.example.org", domain.MailDomainSendOnly)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	recorder, body := fixture.do(http.MethodPost, fmt.Sprintf("/mail-domains/%d/receiving", pending.ID), "")
	if recorder.Code != http.StatusConflict || body["error_code"] != domain.CodeMailZonePending {
		t.Fatalf("receiving = %d %v", recorder.Code, body)
	}
	if stored := fixture.domains.domains[pending.ID]; stored.Capability != domain.MailDomainSendOnly {
		t.Fatalf("capability = %s", stored.Capability)
	}
}

func TestReceivingUpgradesSendOnly(t *testing.T) {
	fixture := newZoneHandlerFixture(t, membership(domain.OrganizationRoleOwner))
	ctx := context.Background()
	sendOnly, err := fixture.service.ConnectMailDomain(ctx, 7, testCreator, "news.example.com", domain.MailDomainSendOnly)
	if err != nil || sendOnly.Status != domain.MailDomainStatusVerified {
		t.Fatalf("send-only domain = %+v, %v", sendOnly, err)
	}
	mailbox, err := fixture.service.CreateMailbox(ctx, as(7), sharedInput("hello", "", "news.example.com"))
	if err != nil || mailbox.RoutingRuleID != "" {
		t.Fatalf("mailbox = %+v, %v", mailbox, err)
	}
	removed, err := fixture.service.CreateMailbox(ctx, as(7), sharedInput("gone", "", "news.example.com"))
	if err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	if err := fixture.service.DeleteMailbox(ctx, as(7), removed.ID); err != nil {
		t.Fatalf("DeleteMailbox: %v", err)
	}
	path := fmt.Sprintf("/mail-domains/%d/receiving", sendOnly.ID)

	recorder, body := fixture.do(http.MethodPost, path, "")
	data, _ := body["data"].(map[string]any)
	if recorder.Code != http.StatusOK || data["capability"] != "send_receive" || data["status"] != "verified" {
		t.Fatalf("receiving = %d %v", recorder.Code, body)
	}
	if !slices.Equal(fixture.zones.routingFor, []string{"news.example.com"}) {
		t.Fatalf("routing enabled for %v", fixture.zones.routingFor)
	}
	if stored := fixture.domains.domains[sendOnly.ID]; stored.Capability != domain.MailDomainSendReceive {
		t.Fatalf("stored capability = %s", stored.Capability)
	}
	// The mailbox created while the domain only sent now receives through its own rule.
	routed := fixture.repo.mailboxes[mailbox.ID]
	if routed.RoutingRuleID == "" || fixture.zones.rules[routed.RoutingRuleID] != "hello@news.example.com" || len(fixture.zones.rules) != 1 {
		t.Fatalf("mailbox rule = %q, rules = %v", routed.RoutingRuleID, fixture.zones.rules)
	}

	// Asking again changes nothing.
	again, body := fixture.do(http.MethodPost, path, "")
	if data, _ := body["data"].(map[string]any); again.Code != http.StatusOK || data["capability"] != "send_receive" {
		t.Fatalf("receiving again = %d %v", again.Code, body)
	}
	if len(fixture.zones.routingFor) != 1 || len(fixture.zones.rules) != 1 {
		t.Fatalf("routing %v, rules %v", fixture.zones.routingFor, fixture.zones.rules)
	}
	// A later setup does not add a second rule for the same mailbox.
	if _, err := fixture.service.SetUpMailDomain(ctx, 7, sendOnly.ID); err != nil {
		t.Fatalf("SetUpMailDomain: %v", err)
	}
	if len(fixture.zones.rules) != 1 {
		t.Fatalf("rules after a second setup = %v", fixture.zones.rules)
	}
}

func TestConnectAcceptsCapability(t *testing.T) {
	fixture := newZoneHandlerFixture(t, membership(domain.OrganizationRoleAdmin))

	recorder, body := fixture.do(http.MethodPost, "/mail-domains", `{"name":"news.example.com","capability":"send_only"}`)
	data, _ := body["data"].(map[string]any)
	if recorder.Code != http.StatusCreated || data["capability"] != "send_only" || data["zone"] != nil ||
		data["zone_check_limited"] != false {
		t.Fatalf("connect = %d %v", recorder.Code, body)
	}
	if len(fixture.zones.routingFor) != 0 {
		t.Fatalf("routing enabled for a send-only domain: %v", fixture.zones.routingFor)
	}

	recorder, body = fixture.do(http.MethodPost, "/mail-domains", `{"name":"mail.example.org"}`)
	data, _ = body["data"].(map[string]any)
	zone, _ := data["zone"].(map[string]any)
	servers, _ := zone["name_servers"].([]any)
	if recorder.Code != http.StatusCreated || data["capability"] != "send_receive" || zone["name"] != "example.org" ||
		zone["status"] != "pending" || len(servers) != 2 {
		t.Fatalf("connect into a new zone = %d %v", recorder.Code, body)
	}

	invalid, body := fixture.do(http.MethodPost, "/mail-domains", `{"name":"other.example.com","capability":"receive_only"}`)
	if invalid.Code != http.StatusUnprocessableEntity || body["error_code"] != "COMMON.VALIDATION_FAILED" {
		t.Fatalf("unknown capability = %d %v", invalid.Code, body)
	}
}

func TestZoneRoutesDisabledWithoutFlag(t *testing.T) {
	fixture := newZoneHandlerFixture(t, membership(domain.OrganizationRoleOwner))
	fixture.service.settings.CustomDomainsEnabled = false
	for _, request := range [][3]string{
		{http.MethodGet, "/mail-zones", ""},
		{http.MethodGet, "/mail-zones/1", ""},
		{http.MethodDelete, "/mail-zones/1", ""},
		{http.MethodPost, "/mail-domain-previews", `{"name":"example.org"}`},
		{http.MethodPost, "/mail-domains/1/receiving", ""},
	} {
		recorder, body := fixture.do(request[0], request[1], request[2])
		if recorder.Code != http.StatusNotFound || body["error_code"] != domain.CodeMailDomainsDisabled {
			t.Fatalf("%s %s = %d %v; want 404 %s", request[0], request[1], recorder.Code, body, domain.CodeMailDomainsDisabled)
		}
	}
}

func TestZoneWriteRequiresAdmin(t *testing.T) {
	fixture := newZoneHandlerFixture(t, membership(domain.OrganizationRoleMember))
	zone := fixture.activeZone("example.org", "cf-own")
	mailDomain, err := fixture.service.ConnectMailDomain(context.Background(), 7, testCreator, "news.example.com", domain.MailDomainSendOnly)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	for _, request := range [][2]string{
		{http.MethodDelete, fmt.Sprintf("/mail-zones/%d", zone.ID)},
		{http.MethodPost, fmt.Sprintf("/mail-domains/%d/receiving", mailDomain.ID)},
	} {
		recorder, body := fixture.do(request[0], request[1], "")
		if recorder.Code != http.StatusForbidden || body["error_code"] != domain.CodePermissionDenied {
			t.Fatalf("member %s %s = %d %v; want 403", request[0], request[1], recorder.Code, body)
		}
	}
	if fixture.mailZones.zones[zone.ID] == nil || fixture.domains.domains[mailDomain.ID].Capability != domain.MailDomainSendOnly {
		t.Fatal("a member's request changed state")
	}
	// Members read zones and preview domains.
	if listed, body := fixture.do(http.MethodGet, "/mail-zones", ""); listed.Code != http.StatusOK {
		t.Fatalf("member list = %d %v", listed.Code, body)
	}
}

func TestReceivingDuringARunningSetupRunsTheSetupAgain(t *testing.T) {
	fixture := newZoneHandlerFixture(t, membership(domain.OrganizationRoleOwner))
	ctx := context.Background()
	sendOnly, err := fixture.service.ConnectMailDomain(ctx, 7, testCreator, "news.example.com", domain.MailDomainSendOnly)
	if err != nil || sendOnly.Status != domain.MailDomainStatusVerified {
		t.Fatalf("send-only domain = %+v, %v", sendOnly, err)
	}
	mailbox, err := fixture.service.CreateMailbox(ctx, as(7), sharedInput("hello", "", "news.example.com"))
	if err != nil || mailbox.RoutingRuleID != "" {
		t.Fatalf("mailbox = %+v, %v", mailbox, err)
	}

	// Setups run in the background again, and the next one stops inside the provider until released.
	fixture.service.background = func(run func()) { go run() }
	entered, release := make(chan struct{}), make(chan struct{})
	var held bool
	fixture.zones.ensureSendingHook = func() {
		if !held {
			held = true
			close(entered)
			<-release
		}
	}
	if _, err := fixture.service.SetUpMailDomain(ctx, 7, sendOnly.ID); err != nil {
		t.Fatalf("SetUpMailDomain: %v", err)
	}
	<-entered

	// The running setup read the domain while it only sent.
	recorder, body := fixture.do(http.MethodPost, fmt.Sprintf("/mail-domains/%d/receiving", sendOnly.ID), "")
	if data, _ := body["data"].(map[string]any); recorder.Code != http.StatusOK || data["setting_up"] != true {
		t.Fatalf("receiving = %d %v", recorder.Code, body)
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for fixture.service.setupRunning(sendOnly.ID) {
		if time.Now().After(deadline) {
			t.Fatal("the setup did not finish")
		}
		time.Sleep(time.Millisecond)
	}

	if !slices.Equal(fixture.zones.routingFor, []string{"news.example.com"}) {
		t.Fatalf("routing enabled for %v; want the domain once the setup ran again", fixture.zones.routingFor)
	}
	routed := fixture.repo.mailboxes[mailbox.ID]
	if routed.RoutingRuleID == "" || fixture.zones.rules[routed.RoutingRuleID] != "hello@news.example.com" {
		t.Fatalf("mailbox rule = %q, rules = %v", routed.RoutingRuleID, fixture.zones.rules)
	}
	stored := fixture.domains.domains[sendOnly.ID]
	if stored.Capability != domain.MailDomainSendReceive || stored.Status != domain.MailDomainStatusVerified ||
		recordStatus(stored, domain.MailDomainRecordReceiving, "MX", "news.example.com") != domain.MailDomainRecordOK {
		t.Fatalf("stored domain = %+v; want verified with its receiving records", stored)
	}
}
