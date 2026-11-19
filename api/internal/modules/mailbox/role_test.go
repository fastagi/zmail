package mailbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	auditstarter "github.com/fastagi/zmail/api/internal/modules/audit"
)

// roleFixture is the Postgres access scenario with two mail roles: "Design desk" holds b@ (B's
// personal mailbox) and "Official" holds team@ (B send, C view).
type roleFixture struct {
	*accessFixture
	design, official *domain.MailRole
	audited          []auditstarter.Change
}

func newRoleFixture(t *testing.T) *roleFixture {
	t.Helper()
	f := &roleFixture{accessFixture: newPostgresAccessFixture(t)}
	f.service.recordAudit = func(_ context.Context, change auditstarter.Change) {
		f.audited = append(f.audited, change)
	}
	ctx := context.Background()
	var err error
	if f.design, err = f.service.CreateMailRole(ctx, f.admin, MailRoleInput{Name: "Design desk", Color: domain.MailLabelColorOrange}); err != nil {
		t.Fatalf("create Design desk: %v", err)
	}
	if f.official, err = f.service.CreateMailRole(ctx, f.admin, MailRoleInput{Name: "Official"}); err != nil {
		t.Fatalf("create Official: %v", err)
	}
	f.assign(t, f.personal.ID, &f.design.ID)
	f.assign(t, f.team.ID, &f.official.ID)
	f.audited = nil
	return f
}

func (f *roleFixture) assign(t *testing.T, mailboxID uint, roleID *uint) {
	t.Helper()
	if _, err := f.service.AssignMailboxRole(context.Background(), f.admin, mailboxID, roleID); err != nil {
		t.Fatalf("assign mailbox %d to %v: %v", mailboxID, roleID, err)
	}
}

// roleRouter serves the access routes plus the mail role routes as viewer.
func (f *roleFixture) roleRouter(viewer domain.OrganizationContext) *gin.Engine {
	router := accessRouter(f.service, viewer)
	handler := NewHandler(f.service)
	router.GET("/mail-roles", handler.ListMailRoles)
	router.POST("/mail-roles", handler.CreateMailRole)
	router.PUT("/mail-roles/:role_id", handler.UpdateMailRole)
	router.DELETE("/mail-roles/:role_id", handler.DeleteMailRole)
	router.PUT("/mailboxes/:mailbox_id/role-assignment", handler.AssignMailboxRole)
	router.GET("/inbox-messages", handler.ListInboxMessages)
	router.GET("/inbox-message-counts", handler.InboxMessageCounts)
	return router
}

// call sends one request as viewer and decodes the envelope.
func (f *roleFixture) call(t *testing.T, viewer domain.OrganizationContext, method, path, body string) (int, map[string]any) {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	f.roleRouter(viewer).ServeHTTP(recorder, request)
	decoded := map[string]any{}
	_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
	return recorder.Code, decoded
}

func (f *roleFixture) roleCount(t *testing.T) int64 {
	t.Helper()
	var count int64
	if err := f.inboxRepo().db.Model(&MailRolePO{}).Count(&count).Error; err != nil {
		t.Fatalf("count roles: %v", err)
	}
	return count
}

func (f *roleFixture) storedRole(t *testing.T, id uint) *MailRolePO {
	t.Helper()
	var rows []MailRolePO
	if err := f.inboxRepo().db.Where("id = ?", id).Find(&rows).Error; err != nil {
		t.Fatalf("load role %d: %v", id, err)
	}
	if len(rows) == 0 {
		return nil
	}
	return &rows[0]
}

func expectRoleStatus(t *testing.T, what string, status int, body map[string]any, wantStatus int, wantCode string) {
	t.Helper()
	if status != wantStatus || (wantCode != "" && body["error_code"] != wantCode) {
		t.Fatalf("%s = %d %v; want %d %s", what, status, body, wantStatus, wantCode)
	}
}

// expectFieldError checks a 422 COMMON.VALIDATION_FAILED keyed by field.
func expectFieldError(t *testing.T, what string, status int, body map[string]any, field string) {
	t.Helper()
	expectRoleStatus(t, what, status, body, http.StatusUnprocessableEntity, domain.CodeValidationFailed)
	fields, _ := body["errors"].(map[string]any)
	if _, ok := fields[field]; !ok {
		t.Fatalf("%s errors = %v; want key %q", what, body["errors"], field)
	}
}

// listedRoles lists the roles as viewer through the service, by name.
func (f *roleFixture) listedRoles(t *testing.T, viewer domain.OrganizationContext) map[string]*domain.MailRole {
	t.Helper()
	roles, err := f.service.ListMailRoles(context.Background(), viewer)
	if err != nil {
		t.Fatalf("ListMailRoles: %v", err)
	}
	byName := map[string]*domain.MailRole{}
	for _, role := range roles {
		byName[role.Name] = role
	}
	return byName
}

func TestMailRoleWriteNeedsOwnerOrAdmin(t *testing.T) {
	f := newRoleFixture(t)
	before := f.roleCount(t)

	status, body := f.call(t, f.b, http.MethodPost, "/mail-roles", `{"name":"Sales"}`)
	expectRoleStatus(t, "member B creates a role", status, body, http.StatusForbidden, domain.CodePermissionDenied)
	if f.roleCount(t) != before {
		t.Fatal("a refused create stored a role")
	}
	status, body = f.call(t, f.b, http.MethodPut, fmt.Sprintf("/mail-roles/%d", f.design.ID), `{"name":"Renamed"}`)
	expectRoleStatus(t, "member B updates a role", status, body, http.StatusForbidden, domain.CodePermissionDenied)
	status, body = f.call(t, f.b, http.MethodDelete, fmt.Sprintf("/mail-roles/%d", f.design.ID), "")
	expectRoleStatus(t, "member B deletes a role", status, body, http.StatusForbidden, domain.CodePermissionDenied)
	if stored := f.storedRole(t, f.design.ID); stored == nil || stored.Name != "Design desk" {
		t.Fatalf("Design desk after refused writes = %+v", stored)
	}

	owner := viewerOf(accessOrg, 9, domain.OrganizationRoleOwner)
	status, body = f.call(t, owner, http.MethodPost, "/mail-roles", `{"name":"  Sales  ","color":"teal","description":" Deals ","owner_user_id":3}`)
	expectRoleStatus(t, "owner creates a role", status, body, http.StatusCreated, "")
	data, _ := body["data"].(map[string]any)
	if data["name"] != "Sales" || data["color"] != "teal" || data["description"] != "Deals" ||
		data["owner_user_id"] != float64(accessC) {
		t.Fatalf("created role = %v", data)
	}
	if ids, ok := data["mailbox_ids"].([]any); !ok || len(ids) != 0 {
		t.Fatalf("created mailbox_ids = %v; want []", data["mailbox_ids"])
	}
}

func TestMailRoleVisibility(t *testing.T) {
	f := newRoleFixture(t)
	empty, err := f.service.CreateMailRole(context.Background(), f.admin, MailRoleInput{Name: "Empty"})
	if err != nil {
		t.Fatalf("create Empty: %v", err)
	}

	c := f.listedRoles(t, f.c)
	if _, ok := c["Design desk"]; ok || len(c) != 1 || !slices.Equal(c["Official"].MailboxIDs, []uint{f.team.ID}) {
		t.Fatalf("C sees %v; want only Official with team@", roleNames(mapValues(c)))
	}
	b := f.listedRoles(t, f.b)
	if len(b) != 2 || !slices.Equal(b["Design desk"].MailboxIDs, []uint{f.personal.ID}) ||
		!slices.Equal(b["Official"].MailboxIDs, []uint{f.team.ID}) {
		t.Fatalf("B sees %+v", b)
	}
	if d := f.listedRoles(t, f.d); len(d) != 0 {
		t.Fatalf("D, with no mailbox, sees %v", roleNames(mapValues(d)))
	}
	admin := f.listedRoles(t, f.admin)
	if len(admin) != 3 || admin["Empty"].ID != empty.ID || len(admin["Empty"].MailboxIDs) != 0 ||
		!slices.Equal(admin["Design desk"].MailboxIDs, []uint{f.personal.ID}) {
		t.Fatalf("admin sees %+v", admin)
	}

	status, body := f.call(t, f.c, http.MethodGet, "/mail-roles", "")
	listed, _ := body["data"].([]any)
	if status != http.StatusOK || len(listed) != 1 {
		t.Fatalf("C GET /mail-roles = %d %v", status, body)
	}
	status, body = f.call(t, f.admin, http.MethodGet, "/mail-roles", "")
	listed, _ = body["data"].([]any)
	if status != http.StatusOK || len(listed) != 3 {
		t.Fatalf("admin GET /mail-roles = %d %v", status, body)
	}
	// By name ignoring case: Design desk, Empty, Official.
	if first, _ := listed[0].(map[string]any); first["name"] != "Design desk" {
		t.Fatalf("admin roles order = %v", listed)
	}
	if second, _ := listed[1].(map[string]any); fmt.Sprint(second["mailbox_ids"]) != "[]" {
		t.Fatalf("Empty mailbox_ids = %v; want []", second["mailbox_ids"])
	}
}

func mapValues(roles map[string]*domain.MailRole) []*domain.MailRole {
	values := make([]*domain.MailRole, 0, len(roles))
	for _, role := range roles {
		values = append(values, role)
	}
	return values
}

func TestMailRoleValidation(t *testing.T) {
	f := newRoleFixture(t)
	cases := []struct {
		name, body, field string
	}{
		{"whitespace name", `{"name":"   "}`, "name"},
		{"missing name", `{"color":"red"}`, "name"},
		{"65-rune name", `{"name":"` + strings.Repeat("名", 65) + `"}`, "name"},
		{"281-rune description", `{"name":"Long","description":"` + strings.Repeat("d", 281) + `"}`, "description"},
		{"control character", `{"name":"Ctl","description":"a\u0007b"}`, "description"},
		{"color black", `{"name":"Dark","color":"black"}`, "color"},
		{"owner zero", `{"name":"Zero","owner_user_id":0}`, "owner_user_id"},
	}
	before := f.roleCount(t)
	for _, tc := range cases {
		status, body := f.call(t, f.admin, http.MethodPost, "/mail-roles", tc.body)
		expectFieldError(t, tc.name, status, body, tc.field)
	}
	if f.roleCount(t) != before {
		t.Fatal("an invalid create stored a role")
	}

	// 64 runes and 280 runes after trimming are accepted; a missing color is gray.
	status, body := f.call(t, f.admin, http.MethodPost, "/mail-roles",
		`{"name":"  `+strings.Repeat("名", 64)+`  ","description":"`+strings.Repeat("d", 280)+`"}`)
	expectRoleStatus(t, "longest valid role", status, body, http.StatusCreated, "")
	if data, _ := body["data"].(map[string]any); data["color"] != "gray" || data["owner_user_id"] != nil {
		t.Fatalf("defaults = %v; want gray and no owner", data)
	}

	for _, malformed := range []string{`[]`, `null`, `{"name":`, `{"name":5}`, `{"name":"x"} {}`} {
		status, body = f.call(t, f.admin, http.MethodPost, "/mail-roles", malformed)
		expectRoleStatus(t, "malformed body "+malformed, status, body, http.StatusBadRequest, domain.CodeInvalidInput)
	}
	huge := `{"name":"Big","description":"` + strings.Repeat("x", 5000) + `"}`
	status, body = f.call(t, f.admin, http.MethodPost, "/mail-roles", huge)
	expectRoleStatus(t, "body over 4 KiB", status, body, http.StatusBadRequest, domain.CodeInvalidInput)

	status, body = f.call(t, f.admin, http.MethodPost, "/mail-roles", `{"name":"design DESK"}`)
	expectRoleStatus(t, "name taken ignoring case", status, body, http.StatusConflict, domain.CodeMailRoleNameTaken)
}

func TestMailRoleLimit(t *testing.T) {
	f := newRoleFixture(t)
	for i := f.roleCount(t); i < domain.MaxMailRolesPerOrganization; i++ {
		if _, err := f.service.CreateMailRole(context.Background(), f.admin, MailRoleInput{Name: fmt.Sprintf("Role %d", i)}); err != nil {
			t.Fatalf("create role %d: %v", i, err)
		}
	}
	status, body := f.call(t, f.admin, http.MethodPost, "/mail-roles", `{"name":"One more"}`)
	expectRoleStatus(t, "51st role", status, body, http.StatusUnprocessableEntity, domain.CodeMailRoleLimitReached)
	// A field error answers before the limit.
	status, body = f.call(t, f.admin, http.MethodPost, "/mail-roles", `{"name":""}`)
	expectFieldError(t, "invalid 51st role", status, body, "name")
}

func TestMailRoleOwnerMustBeMember(t *testing.T) {
	f := newRoleFixture(t)
	status, body := f.call(t, f.admin, http.MethodPost, "/mail-roles", fmt.Sprintf(`{"name":"Ghost","owner_user_id":%d}`, notMember))
	expectFieldError(t, "owner who is not a member", status, body, "owner_user_id")
	status, body = f.call(t, f.admin, http.MethodPut, fmt.Sprintf("/mail-roles/%d", f.design.ID),
		fmt.Sprintf(`{"name":"Design desk","owner_user_id":%d}`, notMember))
	expectFieldError(t, "update to an owner who is not a member", status, body, "owner_user_id")
	if stored := f.storedRole(t, f.design.ID); stored.OwnerUserID != nil {
		t.Fatalf("Design desk owner = %v after a refused update", *stored.OwnerUserID)
	}

	status, body = f.call(t, f.admin, http.MethodPut, fmt.Sprintf("/mail-roles/%d", f.design.ID),
		fmt.Sprintf(`{"name":"Design desk","color":"pink","owner_user_id":%d}`, accessB))
	expectRoleStatus(t, "update to a member owner", status, body, http.StatusOK, "")
	data, _ := body["data"].(map[string]any)
	if data["owner_user_id"] != float64(accessB) || data["color"] != "pink" || fmt.Sprint(data["mailbox_ids"]) != fmt.Sprint([]any{float64(f.personal.ID)}) {
		t.Fatalf("updated role = %v", data)
	}
	// PUT replaces every field: an omitted owner and color return to their defaults.
	status, body = f.call(t, f.admin, http.MethodPut, fmt.Sprintf("/mail-roles/%d", f.design.ID), `{"name":"Design desk"}`)
	expectRoleStatus(t, "update with defaults", status, body, http.StatusOK, "")
	if data, _ = body["data"].(map[string]any); data["owner_user_id"] != nil || data["color"] != "gray" {
		t.Fatalf("replaced role = %v; want defaults", data)
	}
}

func TestMailRoleOwnerHiddenAfterLeaving(t *testing.T) {
	f := newRoleFixture(t)
	owner := uint(accessC)
	if _, err := f.service.UpdateMailRole(context.Background(), f.admin, f.official.ID, MailRoleInput{Name: "Official", OwnerUserID: &owner}); err != nil {
		t.Fatalf("set owner: %v", err)
	}
	if got := f.listedRoles(t, f.admin)["Official"].OwnerUserID; got == nil || *got != accessC {
		t.Fatalf("owner while a member = %v", got)
	}
	f.memberships.absent[accessC] = true
	if got := f.listedRoles(t, f.admin)["Official"].OwnerUserID; got != nil {
		t.Fatalf("owner after leaving = %d; want none", *got)
	}
	if stored := f.storedRole(t, f.official.ID); stored.OwnerUserID == nil {
		t.Fatal("reading must not clear the stored owner")
	}
}

func TestMailRoleOwnerClearedWhenMemberRemoved(t *testing.T) {
	f := newRoleFixture(t)
	owner := uint(accessC)
	if _, err := f.service.UpdateMailRole(context.Background(), f.admin, f.official.ID, MailRoleInput{Name: "Official", OwnerUserID: &owner}); err != nil {
		t.Fatalf("set owner: %v", err)
	}
	event := domain.NewOrganizationMemberRemovedEvent(accessOrg, accessC, f.c.MembershipID, time.Now())
	if err := f.service.MemberRemoved(context.Background(), event); err != nil {
		t.Fatalf("MemberRemoved: %v", err)
	}
	if stored := f.storedRole(t, f.official.ID); stored.OwnerUserID != nil {
		t.Fatalf("Official owner = %d after the owner left", *stored.OwnerUserID)
	}
	// The role grants and revokes nothing: C's mailbox access is unchanged by the role.
	if mailbox := listedMailbox(t, f.service, f.c, f.team.ID); mailbox == nil || !mailbox.Access.View {
		t.Fatalf("C's team@ access after leaving = %+v; roles must not change access", mailbox)
	}
}

func TestMailRoleCheckOrder(t *testing.T) {
	f := newRoleFixture(t)
	status, body := f.call(t, f.b, http.MethodPost, "/mail-roles", `{"name":"","color":"black"}`)
	expectRoleStatus(t, "member with a bad body", status, body, http.StatusForbidden, domain.CodePermissionDenied)
	status, body = f.call(t, f.b, http.MethodPut, "/mail-roles/999999", `{"name":""}`)
	expectRoleStatus(t, "member updates a missing role with a bad body", status, body, http.StatusForbidden, domain.CodePermissionDenied)
	status, body = f.call(t, f.admin, http.MethodPut, "/mail-roles/999999", `{"name":""}`)
	expectRoleStatus(t, "admin updates a missing role with a bad body", status, body, http.StatusNotFound, domain.CodeMailRoleNotFound)
	status, body = f.call(t, f.admin, http.MethodPut, fmt.Sprintf("/mail-roles/%d", f.design.ID), `{"name":"official"}`)
	expectRoleStatus(t, "rename to a taken name", status, body, http.StatusConflict, domain.CodeMailRoleNameTaken)
	status, body = f.call(t, f.admin, http.MethodPut, fmt.Sprintf("/mail-roles/%d", f.design.ID), `{"name":"OFFICIAL","color":"black"}`)
	expectFieldError(t, "field error before a taken name", status, body, "color")
	status, body = f.call(t, f.admin, http.MethodPut, "/mail-roles/abc", `{"name":"x"}`)
	expectRoleStatus(t, "non-numeric role id", status, body, http.StatusBadRequest, domain.CodeInvalidInput)

	status, body = f.call(t, f.admin, http.MethodDelete, fmt.Sprintf("/mail-roles/%d", f.design.ID), "")
	expectRoleStatus(t, "delete", status, body, http.StatusNoContent, "")
	status, body = f.call(t, f.admin, http.MethodDelete, fmt.Sprintf("/mail-roles/%d", f.design.ID), "")
	expectRoleStatus(t, "delete again", status, body, http.StatusNotFound, domain.CodeMailRoleNotFound)
	if mailbox := listedMailbox(t, f.service, f.admin, f.personal.ID); mailbox.RoleID != nil {
		t.Fatalf("b@ role after delete = %d; want ungrouped", *mailbox.RoleID)
	}
}

func TestMailRoleAssignAccess(t *testing.T) {
	f := newRoleFixture(t)
	path := func(mailboxID uint) string { return fmt.Sprintf("/mailboxes/%d/role-assignment", mailboxID) }

	status, body := f.call(t, f.b, http.MethodPut, path(f.team.ID), fmt.Sprintf(`{"role_id":%d}`, f.design.ID))
	expectRoleStatus(t, "B assigns team@", status, body, http.StatusForbidden, domain.CodePermissionDenied)
	status, body = f.call(t, f.c, http.MethodPut, path(f.personal.ID), fmt.Sprintf(`{"role_id":%d}`, f.official.ID))
	expectRoleStatus(t, "C assigns b@", status, body, http.StatusNotFound, domain.CodeMailboxNotFound)
	status, body = f.call(t, f.c, http.MethodPut, path(f.personal.ID), `{}`)
	expectRoleStatus(t, "C assigns b@ without role_id", status, body, http.StatusNotFound, domain.CodeMailboxNotFound)

	for _, bad := range []string{`{}`, `{"role_id":0}`, `{"role_id":-1}`, `{"role_id":"3"}`, `{"role_id":1.5}`, `{"role_id":9007199254740992}`} {
		status, body = f.call(t, f.admin, http.MethodPut, path(f.team.ID), bad)
		expectFieldError(t, "admin assigns "+bad, status, body, "role_id")
	}
	for _, malformed := range []string{`[]`, `null`, `{"role_id":`} {
		status, body = f.call(t, f.admin, http.MethodPut, path(f.team.ID), malformed)
		expectRoleStatus(t, "malformed assignment "+malformed, status, body, http.StatusBadRequest, domain.CodeInvalidInput)
	}
	status, body = f.call(t, f.admin, http.MethodPut, path(f.team.ID), `{"role_id":999999}`)
	expectRoleStatus(t, "assign a missing role", status, body, http.StatusNotFound, domain.CodeMailRoleNotFound)
	status, body = f.call(t, f.admin, http.MethodPut, path(999999), `{"role_id":999999}`)
	expectRoleStatus(t, "assign a missing mailbox", status, body, http.StatusNotFound, domain.CodeMailboxNotFound)

	for range 2 {
		status, body = f.call(t, f.admin, http.MethodPut, path(f.team.ID), fmt.Sprintf(`{"role_id":%d}`, f.design.ID))
		expectRoleStatus(t, "assign the same role", status, body, http.StatusOK, "")
	}
	data, _ := body["data"].(map[string]any)
	access, _ := data["access"].(map[string]any)
	if data["role_id"] != float64(f.design.ID) || access["administer"] != true || data["unread_count"] != float64(0) {
		t.Fatalf("assigned mailbox = %v", data)
	}
	status, body = f.call(t, f.admin, http.MethodPut, path(f.team.ID), `{"role_id":null}`)
	expectRoleStatus(t, "ungroup", status, body, http.StatusOK, "")
	if data, _ = body["data"].(map[string]any); data["role_id"] != nil {
		t.Fatalf("ungrouped mailbox = %v", data)
	}
	// Roles grant nothing: B keeps send on team@ and C still cannot see b@.
	if mailbox := listedMailbox(t, f.service, f.b, f.team.ID); mailbox == nil || !mailbox.Access.Send {
		t.Fatalf("B's team@ access = %+v", mailbox)
	}
	if listedMailbox(t, f.service, f.c, f.personal.ID) != nil {
		t.Fatal("C sees b@ after role changes")
	}
}

func TestMailRoleAssignCountsUnreadForReaders(t *testing.T) {
	f := newRoleFixture(t)
	// B owns b@ but is not an admin, so assigns nothing; an admin who is a team@ member reads it.
	if _, err := f.service.SetMailboxMember(context.Background(), f.admin, f.team.ID, accessAdmin, domain.MailboxPermissionView); err != nil {
		t.Fatalf("admin joins team@: %v", err)
	}
	mailbox, err := f.service.AssignMailboxRole(context.Background(), f.admin, f.team.ID, nil)
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if !mailbox.Access.View || mailbox.UnreadCount != 1 || mailbox.RoleID != nil {
		t.Fatalf("assigned team@ = %+v; want readable with 1 unread and no role", mailbox)
	}
	mailbox, err = f.service.AssignMailboxRole(context.Background(), f.admin, f.personal.ID, &f.official.ID)
	if err != nil {
		t.Fatalf("assign b@: %v", err)
	}
	if mailbox.Access.View || mailbox.UnreadCount != 0 || mailbox.RoleID == nil || *mailbox.RoleID != f.official.ID {
		t.Fatalf("assigned b@ = %+v; an admin never reads b@", mailbox)
	}
}

func TestMailRoleAuditRecordsIdsNotNames(t *testing.T) {
	f := newRoleFixture(t)
	status, body := f.call(t, f.admin, http.MethodPost, "/mail-roles", `{"name":"Secret brand","description":"Private notes"}`)
	expectRoleStatus(t, "create", status, body, http.StatusCreated, "")
	created, _ := body["data"].(map[string]any)
	id := fmt.Sprint(uint(created["id"].(float64)))
	status, body = f.call(t, f.admin, http.MethodPut, "/mail-roles/"+id, `{"name":"Secret brand","color":"red","description":"Private notes"}`)
	expectRoleStatus(t, "update", status, body, http.StatusOK, "")
	status, body = f.call(t, f.admin, http.MethodPut, fmt.Sprintf("/mailboxes/%d/role-assignment", f.team.ID), `{"role_id":`+id+`}`)
	expectRoleStatus(t, "assign", status, body, http.StatusOK, "")
	status, body = f.call(t, f.admin, http.MethodDelete, "/mail-roles/"+id, "")
	expectRoleStatus(t, "delete", status, body, http.StatusNoContent, "")

	if len(f.audited) != 4 {
		t.Fatalf("audited %d changes: %+v", len(f.audited), f.audited)
	}
	want := []struct{ action, resource, targetType, targetID string }{
		{"create", "mail_roles", "mail_role", id},
		{"update", "mail_roles", "mail_role", id},
		{"update", "mailboxes.role", "mailbox", fmt.Sprint(f.team.ID)},
		{"delete", "mail_roles", "mail_role", id},
	}
	for i, change := range f.audited {
		if change.Action != want[i].action || change.Resource != want[i].resource ||
			change.TargetType != want[i].targetType || change.TargetID != want[i].targetID ||
			change.Result != domain.AuditResultSuccess {
			t.Fatalf("audit %d = %+v; want %+v", i, change, want[i])
		}
		encoded, _ := json.Marshal(change)
		if strings.Contains(string(encoded), "Secret") || strings.Contains(string(encoded), "Private") {
			t.Fatalf("audit %d carries a name or description: %s", i, encoded)
		}
	}
	if fields := f.audited[1].Metadata["changed_fields"]; fmt.Sprint(fields) != "[color]" {
		t.Fatalf("update changed_fields = %v; want [color]", fields)
	}
	if fields := f.audited[0].Metadata["changed_fields"]; fmt.Sprint(fields) != "[name color description owner_user_id]" {
		t.Fatalf("create changed_fields = %v", fields)
	}
	roleChange := f.audited[2].Changes["role_id"]
	if roleChange.Before != f.official.ID || fmt.Sprint(roleChange.After) != id {
		t.Fatalf("assignment change = %+v; want %d → %s", roleChange, f.official.ID, id)
	}
}

func TestMailRoleServiceUnavailableWithoutSeam(t *testing.T) {
	svc := newInboxRecorderService(newInboxRecorder())
	admin := viewerOf(accessOrg, accessAdmin, domain.OrganizationRoleAdmin)
	if _, err := svc.ListMailRoles(context.Background(), admin); err != domain.ErrServiceUnavailable {
		t.Fatalf("ListMailRoles without the seam = %v", err)
	}
	if _, err := svc.CreateMailRole(context.Background(), admin, MailRoleInput{Name: "x"}); err != domain.ErrServiceUnavailable {
		t.Fatalf("CreateMailRole without the seam = %v", err)
	}
}
