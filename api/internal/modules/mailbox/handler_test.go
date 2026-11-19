package mailbox

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/pkg/response"
)

// handlerFixture serves the handlers directly, with the organization context a real request
// would get from the organization_context middleware.
type handlerFixture struct {
	router  *gin.Engine
	service *service
	repo    *fakeRepository
}

func newHandlerFixture(t *testing.T, organization *domain.OrganizationContext) *handlerFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	repo := newFakeRepository()
	svc := NewService(repo, &fakeMemberships{}, repo, newFakeContacts(), &stubBroadcasts{}, nil, nil, &fakeTransport{}, &fakeNotifier{}, Settings{
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
	router.POST("/mail-broadcasts/:broadcast_id/sends", handler.SendBroadcast)
	router.POST("/mail-broadcasts/:broadcast_id/cancellations", handler.CancelBroadcast)
	router.POST("/mail-broadcasts/:broadcast_id/pauses", handler.PauseBroadcast)
	router.POST("/mail-broadcasts/:broadcast_id/resumptions", handler.ResumeBroadcast)
	return &handlerFixture{router: router, service: svc, repo: repo}
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

// sharedWithHandlerUser is a shared mailbox whose one member is the handler user.
func sharedWithHandlerUser(localPart string, permission domain.MailboxPermission) CreateMailboxInput {
	return CreateMailboxInput{
		LocalPart: localPart, DisplayName: "Helpdesk", Type: domain.MailboxTypeShared,
		Members: []MailboxMemberInput{{UserID: handlerUser, Permission: permission}},
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
	if _, err := fixture.service.CreateMailbox(t.Context(), as(7), sharedWithHandlerUser("helpdesk", domain.MailboxPermissionSend)); err != nil {
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

func TestMailboxRoutesRequireAnOrganizationContext(t *testing.T) {
	fixture := newHandlerFixture(t, nil)
	for _, request := range [][2]string{
		{http.MethodGet, "/mailbox-domains"}, {http.MethodGet, "/mailboxes"},
		{http.MethodPost, "/mailboxes"}, {http.MethodDelete, "/mailboxes/1"},
		{http.MethodGet, "/mailboxes/1/messages"},
		{http.MethodPost, "/mailboxes/1/messages"}, {http.MethodGet, "/mail-messages"},
	} {
		recorder, body := fixture.do(request[0], request[1], `{}`)
		if recorder.Code != http.StatusBadRequest || body["error_code"] != domain.CodeOrganizationContextRequired {
			t.Fatalf("%s %s = %d %v; want 400 %s", request[0], request[1], recorder.Code, body, domain.CodeOrganizationContextRequired)
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
