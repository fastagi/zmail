package mailbox

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// accessE is a user who manages team@, and so may organize it.
const accessE = 5

// organizeFixture is the PostgreSQL access scene plus E, a manage member of team@.
type organizeFixture struct {
	*accessFixture
	e domain.OrganizationContext
}

func newOrganizeFixture(t *testing.T) *organizeFixture {
	t.Helper()
	f := &organizeFixture{accessFixture: newPostgresAccessFixture(t)}
	f.e = viewerOf(accessOrg, accessE, domain.OrganizationRoleMember)
	if _, err := f.service.SetMailboxMember(context.Background(), f.admin, f.team.ID, accessE, domain.MailboxPermissionManage); err != nil {
		t.Fatalf("add E as a manage member: %v", err)
	}
	return f
}

func (f *accessFixture) stored(t *testing.T, id uint) MailMessagePO {
	t.Helper()
	var po MailMessagePO
	if err := f.repo.(*repository).db.First(&po, id).Error; err != nil {
		t.Fatalf("load message %d: %v", id, err)
	}
	return po
}

func (f *accessFixture) messageExists(t *testing.T, id uint) bool {
	t.Helper()
	var count int64
	if err := f.repo.(*repository).db.Model(&MailMessagePO{}).Where("id = ?", id).Count(&count).Error; err != nil {
		t.Fatalf("count message %d: %v", id, err)
	}
	return count == 1
}

func (f *accessFixture) folder(t *testing.T, viewer domain.OrganizationContext, mailboxID uint, name string) *domain.MailFolder {
	t.Helper()
	folder, err := f.service.CreateFolder(context.Background(), viewer, mailboxID, name)
	if err != nil {
		t.Fatalf("CreateFolder(%q): %v", name, err)
	}
	return folder
}

func (f *accessFixture) label(t *testing.T, viewer domain.OrganizationContext, mailboxID uint, name string) *domain.MailLabel {
	t.Helper()
	label, err := f.service.CreateLabel(context.Background(), viewer, mailboxID, name, domain.MailLabelColorBlue)
	if err != nil {
		t.Fatalf("CreateLabel(%q): %v", name, err)
	}
	return label
}

func boolPtr(value bool) *bool { return &value }

func placementPtr(placement domain.MailPlacement) *domain.MailPlacement { return &placement }

func uintPtr(value uint) *uint { return &value }

func TestOrganizeAccess(t *testing.T) {
	f := newOrganizeFixture(t)
	cases := []struct {
		name    string
		viewer  domain.OrganizationContext
		mailbox *domain.Mailbox
		want    bool
	}{
		{"personal owner", f.b, f.personal, true},
		{"shared manage member", f.e, f.team, true},
		{"shared send member", f.b, f.team, false},
		{"shared view member", f.c, f.team, false},
		{"admin who is not a member of a shared mailbox", f.admin, f.team, false},
		{"admin of a personal mailbox", f.admin, f.personal, false},
	}
	for _, tc := range cases {
		listed := listedMailbox(t, f.service, tc.viewer, tc.mailbox.ID)
		if listed == nil {
			t.Fatalf("%s: mailbox not listed", tc.name)
		}
		if got := canOrganize(listed.Access); got != tc.want || listed.Access.Organize != tc.want {
			t.Fatalf("%s: organize = %v (flag %v); want %v", tc.name, got, listed.Access.Organize, tc.want)
		}
	}

	// An owner who is a send member does not organize: the role grants no mail rights.
	owner := viewerOf(accessOrg, accessB, domain.OrganizationRoleOwner)
	if listed := listedMailbox(t, f.service, owner, f.team.ID); listed == nil || listed.Access.Organize {
		t.Fatalf("an owner with send on team@ organizes it: %+v", listed)
	}

	recorder, body := serve(accessRouter(f.service, f.e), http.MethodGet, "/mailboxes", "")
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"organize":true`) {
		t.Fatalf("GET /mailboxes as E = %d %v; want access.organize true", recorder.Code, body)
	}
}

// Review focus 3: a view member asking for a personal and a shared change at once is refused
// before anything changes.
func TestBatchUpdateNeedsHighestAccess(t *testing.T) {
	f := newOrganizeFixture(t)
	ctx := context.Background()
	message := f.teamMessage.ID

	_, err := f.service.UpdateMessages(ctx, f.c, f.team.ID, []uint{message}, MessageUpdate{
		Starred: boolPtr(true), Placement: placementPtr(domain.MailPlacementArchive),
	})
	expectError(t, "view member stars and archives", err, domain.ErrPermissionDenied)

	path := fmt.Sprintf("/mailboxes/%d/message-updates", f.team.ID)
	recorder, body := serve(accessRouter(f.service, f.c), http.MethodPost, path,
		fmt.Sprintf(`{"message_ids":[%d],"starred":true,"placement":"archive"}`, message))
	if recorder.Code != http.StatusForbidden || body["error_code"] != domain.CodePermissionDenied {
		t.Fatalf("view member POST message-updates = %d %v; want 403", recorder.Code, body)
	}
	// A view member's invalid shared change is refused for permission, not validation.
	recorder, body = serve(accessRouter(f.service, f.c), http.MethodPost, path,
		fmt.Sprintf(`{"message_ids":[%d],"placement":"folder"}`, message))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("view member invalid move = %d %v; want 403", recorder.Code, body)
	}

	if po := f.stored(t, message); po.Placement != string(domain.MailPlacementDefault) {
		t.Fatalf("placement = %q after a refused batch; want default", po.Placement)
	}
	read, err := f.service.GetMessage(ctx, f.c, f.team.ID, message)
	if err != nil || read.Starred {
		t.Fatalf("C's message after a refused batch = %+v, %v; want not starred", read, err)
	}

	// The same personal change alone is fine for a view member.
	updated, err := f.service.UpdateMessages(ctx, f.c, f.team.ID, []uint{message}, MessageUpdate{Starred: boolPtr(true), Read: boolPtr(true)})
	if err != nil || updated != 1 {
		t.Fatalf("view member stars = %d, %v; want 1", updated, err)
	}
}

func TestStarIsPerPerson(t *testing.T) {
	f := newOrganizeFixture(t)
	ctx := context.Background()
	box, message := f.team.ID, f.teamMessage.ID

	starred, err := f.service.UpdateMessage(ctx, f.b, box, message, MessageUpdate{Starred: boolPtr(true)})
	if err != nil || !starred.Starred {
		t.Fatalf("B stars = %+v, %v; want starred", starred, err)
	}
	if seen, _ := f.service.GetMessage(ctx, f.c, box, message); seen.Starred {
		t.Fatal("C sees B's star")
	}
	if _, err = f.service.UpdateMessage(ctx, f.c, box, message, MessageUpdate{Starred: boolPtr(true)}); err != nil {
		t.Fatalf("C stars: %v", err)
	}
	if _, err = f.service.UpdateMessage(ctx, f.b, box, message, MessageUpdate{Starred: boolPtr(false)}); err != nil {
		t.Fatalf("B unstars: %v", err)
	}
	if seen, _ := f.service.GetMessage(ctx, f.c, box, message); !seen.Starred {
		t.Fatal("B's unstar removed C's star")
	}
	if seen, _ := f.service.GetMessage(ctx, f.b, box, message); seen.Starred {
		t.Fatal("B still sees a star after unstarring")
	}
	listed, _, err := f.service.ListMessages(ctx, f.c, box, domain.MailMessageFilter{})
	if err != nil || len(listed) != 1 || !listed[0].Starred {
		t.Fatalf("C's listing = %+v, %v; want the message starred", listed, err)
	}

	recorder, body := serve(accessRouter(f.service, f.c), http.MethodPut, fmt.Sprintf("/mailboxes/%d/messages/%d", box, message), `{"starred":true}`)
	data, _ := body["data"].(map[string]any)
	if recorder.Code != http.StatusOK || data["starred"] != true || data["placement"] != "default" {
		t.Fatalf("PUT starred = %d %v; want 200 starred in default", recorder.Code, body)
	}
	if ids, ok := data["label_ids"].([]any); !ok || len(ids) != 0 {
		t.Fatalf("label_ids = %v; want an empty list", data["label_ids"])
	}
}

func TestDeleteMovesToTrashThenPermanent(t *testing.T) {
	f := newOrganizeFixture(t)
	ctx := context.Background()
	box, message := f.team.ID, f.teamMessage.ID

	err := f.service.DeleteMessage(ctx, f.c, box, message)
	expectError(t, "view member delete", err, domain.ErrPermissionDenied)
	err = f.service.DeleteMessage(ctx, f.b, box, 999_999)
	expectError(t, "delete a missing message", err, ErrMailMessageNotFound)

	if err = f.service.DeleteMessage(ctx, f.b, box, message); err != nil {
		t.Fatalf("B moves to the trash: %v", err)
	}
	po := f.stored(t, message)
	if po.Placement != string(domain.MailPlacementTrash) || po.TrashedAt == nil {
		t.Fatalf("after delete placement = %q trashed_at = %v; want trash with a time", po.Placement, po.TrashedAt)
	}
	trashedAt := *po.TrashedAt

	err = f.service.DeleteMessage(ctx, f.b, box, message)
	expectError(t, "send member deletes from the trash", err, domain.ErrPermissionDenied)
	recorder, body := serve(accessRouter(f.service, f.b), http.MethodDelete, fmt.Sprintf("/mailboxes/%d/messages/%d", box, message), "")
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("B DELETE a trashed message = %d %v; want 403", recorder.Code, body)
	}
	if !f.messageExists(t, message) {
		t.Fatal("a refused permanent delete removed the message")
	}

	// Moving it to the trash again keeps the first time.
	if _, err := f.service.UpdateMessage(ctx, f.b, box, message, MessageUpdate{Placement: placementPtr(domain.MailPlacementTrash)}); err != nil {
		t.Fatalf("re-trash: %v", err)
	}
	if again := f.stored(t, message); again.TrashedAt == nil || !again.TrashedAt.Equal(trashedAt) {
		t.Fatalf("re-trash moved trashed_at from %v to %v", trashedAt, again.TrashedAt)
	}

	recorder, body = serve(accessRouter(f.service, f.e), http.MethodDelete, fmt.Sprintf("/mailboxes/%d/messages/%d", box, message), "")
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("E DELETE a trashed message = %d %v; want 204", recorder.Code, body)
	}
	if f.messageExists(t, message) {
		t.Fatal("the permanently deleted message is still stored")
	}
	var files int64
	f.repo.(*repository).db.Model(&MailAttachmentPO{}).Where("message_id = ?", message).Count(&files)
	if files != 0 {
		t.Fatalf("%d attachments outlived their message", files)
	}
}

// Review focus 1: a waiting message moved to the trash is never sent.
func TestTrashingQueuedMessageCancelsSend(t *testing.T) {
	f := newQueueFixture(t)
	start := f.clock
	for i := 0; i < 5; i++ {
		f.mustSend(t, "fill", false)
	}
	waiting := f.mustSend(t, "w1", true)
	batched := f.mustSend(t, "w2", true)
	ctx := context.Background()
	if _, err := f.service.UpdateMessage(ctx, f.b, f.team.ID, waiting.ID, MessageUpdate{Placement: placementPtr(domain.MailPlacementTrash)}); err != nil {
		t.Fatalf("trash w1: %v", err)
	}
	if _, err := f.service.UpdateMessages(ctx, f.b, f.team.ID, []uint{batched.ID}, MessageUpdate{Placement: placementPtr(domain.MailPlacementTrash)}); err != nil {
		t.Fatalf("trash w2: %v", err)
	}
	for _, id := range []uint{waiting.ID, batched.ID} {
		po := f.stored(t, id)
		if po.Status != string(domain.MailStatusFailed) || po.FailureCode != failureSendCanceled || po.QueuedReason != nil {
			t.Fatalf("trashed waiting message = status %q failure %q queued %v; want failed send.canceled", po.Status, po.FailureCode, po.QueuedReason)
		}
	}
	f.clock = start.Add(25 * time.Hour)
	before := len(f.transport.sent)
	if got := f.dispatch(t, 25); got != 0 || len(f.transport.sent) != before {
		t.Fatalf("dispatch processed %d and sent %d; want nothing", got, len(f.transport.sent)-before)
	}
}

// Review focus 1: restoring a canceled message does not resurrect its send.
func TestRestoringCanceledMessageDoesNotSend(t *testing.T) {
	f := newQueueFixture(t)
	start := f.clock
	for i := 0; i < 5; i++ {
		f.mustSend(t, "fill", false)
	}
	waiting := f.mustSend(t, "w1", true)
	ctx := context.Background()
	if err := f.service.DeleteMessage(ctx, f.b, f.team.ID, waiting.ID); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	restored, err := f.service.UpdateMessage(ctx, f.b, f.team.ID, waiting.ID, MessageUpdate{Placement: placementPtr(domain.MailPlacementDefault)})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if restored.Placement != domain.MailPlacementDefault || restored.TrashedAt != nil || restored.Status != domain.MailStatusFailed || restored.QueuedReason != "" {
		t.Fatalf("restored = placement %q trashed %v status %q queued %q; want default, failed, not queued",
			restored.Placement, restored.TrashedAt, restored.Status, restored.QueuedReason)
	}
	f.clock = start.Add(25 * time.Hour)
	before := len(f.transport.sent)
	if got := f.dispatch(t, 25); got != 0 || len(f.transport.sent) != before {
		t.Fatalf("dispatch after restore processed %d and sent %d; want nothing", got, len(f.transport.sent)-before)
	}
}

func TestPurgeExpiredTrashAfter30Days(t *testing.T) {
	f := newOrganizeFixture(t)
	ctx := context.Background()
	db := f.repo.(*repository).db
	now := time.Now().UTC()
	old := f.receive(t, f.team, "Old")
	recent := f.receive(t, f.team, "Recent")
	older := f.receive(t, f.personal, "Older")
	for _, id := range []uint{old.ID, recent.ID, older.ID, f.teamMessage.ID} {
		mailbox := f.team.ID
		if id == older.ID {
			mailbox = f.personal.ID
		}
		if err := f.service.DeleteMessage(ctx, f.b, mailbox, id); err != nil {
			t.Fatalf("trash %d: %v", id, err)
		}
	}
	setTrashedAt := func(id uint, at time.Time) {
		if err := db.Model(&MailMessagePO{}).Where("id = ?", id).Update("trashed_at", at).Error; err != nil {
			t.Fatalf("set trashed_at: %v", err)
		}
	}
	setTrashedAt(old.ID, now.Add(-31*24*time.Hour))
	setTrashedAt(older.ID, now.Add(-40*24*time.Hour))
	setTrashedAt(recent.ID, now.Add(-29*24*time.Hour))
	// The message still in default was never trashed, however old it is.
	if _, err := f.service.UpdateMessage(ctx, f.b, f.team.ID, f.teamMessage.ID, MessageUpdate{Placement: placementPtr(domain.MailPlacementDefault)}); err != nil {
		t.Fatalf("restore: %v", err)
	}

	purged, err := f.service.PurgeExpiredTrash(ctx, now, 1)
	if err != nil || purged != 1 {
		t.Fatalf("PurgeExpiredTrash(limit 1) = %d, %v; want 1", purged, err)
	}
	if f.messageExists(t, older.ID) {
		t.Fatal("the oldest expired message was not purged first")
	}
	purged, err = f.service.PurgeExpiredTrash(ctx, now, 10)
	if err != nil || purged != 1 {
		t.Fatalf("PurgeExpiredTrash = %d, %v; want the other expired message", purged, err)
	}
	if f.messageExists(t, old.ID) || !f.messageExists(t, recent.ID) || !f.messageExists(t, f.teamMessage.ID) {
		t.Fatal("purge removed the wrong messages")
	}
	var files int64
	db.Model(&MailAttachmentPO{}).Where("message_id IN ?", []uint{old.ID, older.ID}).Count(&files)
	if files != 0 {
		t.Fatalf("%d attachments outlived their purged messages", files)
	}
}

func TestPurgeTrashNeedsOrganize(t *testing.T) {
	f := newOrganizeFixture(t)
	ctx := context.Background()
	kept := f.receive(t, f.team, "Kept")
	if err := f.service.DeleteMessage(ctx, f.b, f.team.ID, f.teamMessage.ID); err != nil {
		t.Fatalf("trash: %v", err)
	}
	_, err := f.service.PurgeTrash(ctx, f.b, f.team.ID)
	expectError(t, "send member empties the trash", err, domain.ErrPermissionDenied)
	_, err = f.service.PurgeTrash(ctx, f.admin, f.team.ID)
	expectError(t, "admin empties the trash", err, domain.ErrPermissionDenied)
	_, err = f.service.PurgeTrash(ctx, f.d, f.team.ID)
	expectError(t, "stranger empties the trash", err, ErrMailboxNotFound)

	recorder, body := serve(accessRouter(f.service, f.e), http.MethodPost, fmt.Sprintf("/mailboxes/%d/trash-purges", f.team.ID), "")
	data, _ := body["data"].(map[string]any)
	if recorder.Code != http.StatusOK || data["deleted"] != float64(1) {
		t.Fatalf("E POST trash-purges = %d %v; want 200 deleted 1", recorder.Code, body)
	}
	if f.messageExists(t, f.teamMessage.ID) || !f.messageExists(t, kept.ID) {
		t.Fatal("emptying the trash removed the wrong messages")
	}
}

func TestFolderLimit100(t *testing.T) {
	f := newOrganizeFixture(t)
	ctx := context.Background()
	for i := 0; i < domain.MaxMailFoldersPerMailbox; i++ {
		f.folder(t, f.e, f.team.ID, fmt.Sprintf("Folder %d", i))
	}
	_, err := f.service.CreateFolder(ctx, f.e, f.team.ID, "One more")
	expectError(t, "101st folder", err, ErrMailFolderLimitReached)
	recorder, body := serve(accessRouter(f.service, f.e), http.MethodPost, fmt.Sprintf("/mailboxes/%d/mail-folders", f.team.ID), `{"name":"One more"}`)
	if recorder.Code != http.StatusUnprocessableEntity || body["error_code"] != domain.CodeMailFolderLimitReached {
		t.Fatalf("POST 101st folder = %d %v; want 422 %s", recorder.Code, body, domain.CodeMailFolderLimitReached)
	}
	folders, err := f.service.ListFolders(ctx, f.c, f.team.ID)
	if err != nil || len(folders) != domain.MaxMailFoldersPerMailbox {
		t.Fatalf("view member lists %d folders, %v; want 100", len(folders), err)
	}
}

func TestLabelColorMustBeInPalette(t *testing.T) {
	f := newOrganizeFixture(t)
	router := accessRouter(f.service, f.e)
	path := fmt.Sprintf("/mailboxes/%d/mail-labels", f.team.ID)
	for _, body := range []string{`{"name":"Urgent","color":"black"}`, `{"name":"Urgent"}`, `{"name":"Urgent","color":"RED"}`} {
		recorder, decoded := serve(router, http.MethodPost, path, body)
		errs, _ := decoded["errors"].(map[string]any)
		if recorder.Code != http.StatusUnprocessableEntity || decoded["error_code"] != domain.CodeValidationFailed || errs["color"] == nil {
			t.Fatalf("POST %s = %d %v; want 422 with a color field error", body, recorder.Code, decoded)
		}
	}
	recorder, decoded := serve(router, http.MethodPost, path, `{"name":"  Urgent  ","color":"red"}`)
	data, _ := decoded["data"].(map[string]any)
	if recorder.Code != http.StatusCreated || data["name"] != "Urgent" || data["color"] != "red" {
		t.Fatalf("POST a red label = %d %v; want 201 Urgent red", recorder.Code, decoded)
	}
	id := uint(data["id"].(float64))
	recorder, decoded = serve(router, http.MethodPut, fmt.Sprintf("%s/%d", path, id), `{"name":"Urgent","color":"purple2"}`)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("PUT an off-palette color = %d %v; want 422", recorder.Code, decoded)
	}
	recorder, decoded = serve(router, http.MethodPut, fmt.Sprintf("%s/%d", path, id), `{"name":"Later","color":"teal"}`)
	data, _ = decoded["data"].(map[string]any)
	if recorder.Code != http.StatusOK || data["name"] != "Later" || data["color"] != "teal" {
		t.Fatalf("PUT a label = %d %v; want 200 Later teal", recorder.Code, decoded)
	}
}

func TestFolderAndLabelRoutesNeedOrganize(t *testing.T) {
	f := newOrganizeFixture(t)
	ctx := context.Background()
	folder := f.folder(t, f.e, f.team.ID, "Invoices")
	label := f.label(t, f.e, f.team.ID, "Urgent")

	_, err := f.service.CreateFolder(ctx, f.b, f.team.ID, "Bills")
	expectError(t, "send member creates a folder", err, domain.ErrPermissionDenied)
	_, err = f.service.CreateLabel(ctx, f.admin, f.team.ID, "Bills", domain.MailLabelColorRed)
	expectError(t, "admin creates a label", err, domain.ErrPermissionDenied)
	_, err = f.service.RenameFolder(ctx, f.b, f.team.ID, folder.ID, "Bills")
	expectError(t, "send member renames a folder", err, domain.ErrPermissionDenied)
	err = f.service.DeleteLabel(ctx, f.c, f.team.ID, label.ID)
	expectError(t, "view member deletes a label", err, domain.ErrPermissionDenied)
	_, err = f.service.ListLabels(ctx, f.d, f.team.ID)
	expectError(t, "stranger lists labels", err, ErrMailboxNotFound)

	// The personal owner organizes their own mailbox.
	if _, err = f.service.CreateFolder(ctx, f.b, f.personal.ID, "Mine"); err != nil {
		t.Fatalf("owner creates a folder: %v", err)
	}
	// A folder of another mailbox is not found, and a taken name conflicts.
	_, err = f.service.RenameFolder(ctx, f.b, f.personal.ID, folder.ID, "Bills")
	expectError(t, "rename another mailbox's folder", err, ErrMailFolderNotFound)
	_, err = f.service.CreateFolder(ctx, f.e, f.team.ID, "INVOICES")
	expectError(t, "taken folder name", err, ErrMailFolderNameTaken)
	_, err = f.service.CreateFolder(ctx, f.e, f.team.ID, "   ")
	expectValidation(t, err, "name")
	_, err = f.service.CreateFolder(ctx, f.e, f.team.ID, strings.Repeat("界", 65))
	expectValidation(t, err, "name")
	if _, err := f.service.CreateFolder(ctx, f.e, f.team.ID, strings.Repeat("界", 64)); err != nil {
		t.Fatalf("a 64-character name: %v", err)
	}

	router := accessRouter(f.service, f.e)
	recorder, body := serve(router, http.MethodPut, fmt.Sprintf("/mailboxes/%d/mail-folders/%d", f.team.ID, folder.ID), `{"name":"Bills"}`)
	data, _ := body["data"].(map[string]any)
	if recorder.Code != http.StatusOK || data["name"] != "Bills" {
		t.Fatalf("PUT folder = %d %v", recorder.Code, body)
	}
	recorder, body = serve(router, http.MethodGet, fmt.Sprintf("/mailboxes/%d/mail-folders", f.team.ID), "")
	if recorder.Code != http.StatusOK || len(body["data"].([]any)) != 2 {
		t.Fatalf("GET folders = %d %v", recorder.Code, body)
	}
	recorder, body = serve(router, http.MethodDelete, fmt.Sprintf("/mailboxes/%d/mail-folders/%d", f.team.ID, folder.ID), "")
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("DELETE folder = %d %v", recorder.Code, body)
	}
	recorder, body = serve(router, http.MethodDelete, fmt.Sprintf("/mailboxes/%d/mail-folders/%d", f.team.ID, folder.ID), "")
	if recorder.Code != http.StatusNotFound || body["error_code"] != domain.CodeMailFolderNotFound {
		t.Fatalf("DELETE a deleted folder = %d %v; want 404 %s", recorder.Code, body, domain.CodeMailFolderNotFound)
	}
	recorder, body = serve(router, http.MethodGet, fmt.Sprintf("/mailboxes/%d/mail-labels", f.team.ID), "")
	if recorder.Code != http.StatusOK || len(body["data"].([]any)) != 1 {
		t.Fatalf("GET labels = %d %v", recorder.Code, body)
	}
	recorder, body = serve(router, http.MethodDelete, fmt.Sprintf("/mailboxes/%d/mail-labels/%d", f.team.ID, label.ID), "")
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("DELETE label = %d %v", recorder.Code, body)
	}
}

func expectValidation(t *testing.T, err error, field string) {
	t.Helper()
	validation, ok := asValidationError(err)
	if !ok || validation.Fields[field] == nil {
		t.Fatalf("error = %v; want a validation error under %q", err, field)
	}
}

func TestMovingMessagesBetweenPlacements(t *testing.T) {
	f := newOrganizeFixture(t)
	ctx := context.Background()
	box, message := f.team.ID, f.teamMessage.ID
	folder := f.folder(t, f.e, box, "Invoices")
	otherFolder := f.folder(t, f.b, f.personal.ID, "Mine")

	moved, err := f.service.UpdateMessage(ctx, f.b, box, message, MessageUpdate{
		Placement: placementPtr(domain.MailPlacementFolder), FolderID: uintPtr(folder.ID),
	})
	if err != nil || moved.Placement != domain.MailPlacementFolder || moved.FolderID == nil || *moved.FolderID != folder.ID {
		t.Fatalf("move to folder = %+v, %v", moved, err)
	}
	// A folder of another mailbox is not found, and nothing moves.
	_, err = f.service.UpdateMessage(ctx, f.b, box, message, MessageUpdate{
		Placement: placementPtr(domain.MailPlacementFolder), FolderID: uintPtr(otherFolder.ID),
	})
	expectError(t, "move to another mailbox's folder", err, ErrMailFolderNotFound)

	for _, placement := range []domain.MailPlacement{domain.MailPlacementArchive, domain.MailPlacementTrash, domain.MailPlacementDefault} {
		if _, err := f.service.UpdateMessage(ctx, f.b, box, message, MessageUpdate{
			Placement: placementPtr(domain.MailPlacementFolder), FolderID: uintPtr(folder.ID),
		}); err != nil {
			t.Fatalf("back to the folder: %v", err)
		}
		moved, err := f.service.UpdateMessage(ctx, f.b, box, message, MessageUpdate{Placement: placementPtr(placement)})
		if err != nil {
			t.Fatalf("move to %s: %v", placement, err)
		}
		po := f.stored(t, message)
		if moved.Placement != placement || moved.FolderID != nil || po.FolderID != nil {
			t.Fatalf("move out of the folder to %s left folder_id %v (stored %v)", placement, moved.FolderID, po.FolderID)
		}
		if (placement == domain.MailPlacementTrash) != (po.TrashedAt != nil) {
			t.Fatalf("placement %s has trashed_at %v", placement, po.TrashedAt)
		}
	}
}

func TestLabelsSetAddAndRemove(t *testing.T) {
	f := newOrganizeFixture(t)
	ctx := context.Background()
	box := f.team.ID
	second := f.receive(t, f.team, "Second")
	urgent := f.label(t, f.e, box, "Urgent")
	later := f.label(t, f.e, box, "Later")
	foreign := f.label(t, f.b, f.personal.ID, "Mine")
	both := []uint{f.teamMessage.ID, second.ID}

	if _, err := f.service.UpdateMessages(ctx, f.b, box, both, MessageUpdate{AddLabelIDs: []uint{later.ID, urgent.ID}}); err != nil {
		t.Fatalf("add labels: %v", err)
	}
	got, _ := f.service.GetMessage(ctx, f.c, box, second.ID)
	if fmt.Sprint(got.LabelIDs) != fmt.Sprint([]uint{urgent.ID, later.ID}) {
		t.Fatalf("labels = %v; want %v ascending", got.LabelIDs, []uint{urgent.ID, later.ID})
	}
	if _, err := f.service.UpdateMessages(ctx, f.b, box, both, MessageUpdate{RemoveLabelIDs: []uint{urgent.ID}}); err != nil {
		t.Fatalf("remove label: %v", err)
	}
	set := []uint{urgent.ID}
	updated, err := f.service.UpdateMessage(ctx, f.b, box, second.ID, MessageUpdate{SetLabelIDs: &set})
	if err != nil || fmt.Sprint(updated.LabelIDs) != fmt.Sprint(set) {
		t.Fatalf("set labels = %v, %v; want %v", updated, err, set)
	}
	first, _ := f.service.GetMessage(ctx, f.b, box, f.teamMessage.ID)
	if fmt.Sprint(first.LabelIDs) != fmt.Sprint([]uint{later.ID}) {
		t.Fatalf("first message labels = %v; want [later]", first.LabelIDs)
	}

	// A label of another mailbox is not found, and nothing changes for any named message.
	_, err = f.service.UpdateMessages(ctx, f.b, box, both, MessageUpdate{AddLabelIDs: []uint{urgent.ID, foreign.ID}})
	expectError(t, "add a foreign label", err, ErrMailLabelNotFound)
	_, err = f.service.UpdateMessages(ctx, f.b, box, []uint{f.teamMessage.ID, 999_999}, MessageUpdate{
		AddLabelIDs: []uint{urgent.ID}, Placement: placementPtr(domain.MailPlacementArchive),
	})
	expectError(t, "batch with a missing message", err, ErrMailMessageNotFound)
	first, _ = f.service.GetMessage(ctx, f.b, box, f.teamMessage.ID)
	if fmt.Sprint(first.LabelIDs) != fmt.Sprint([]uint{later.ID}) || first.Placement != domain.MailPlacementDefault {
		t.Fatalf("a failed batch changed the message: labels %v placement %s", first.LabelIDs, first.Placement)
	}
	// Another mailbox's message is not found either.
	_, err = f.service.UpdateMessages(ctx, f.b, box, []uint{f.personalMessage.ID}, MessageUpdate{Read: boolPtr(true)})
	expectError(t, "batch naming another mailbox's message", err, ErrMailMessageNotFound)
}

func TestUpdateValidationKeys(t *testing.T) {
	f := newOrganizeFixture(t)
	router := accessRouter(f.service, f.b)
	box, message := f.team.ID, f.teamMessage.ID
	folder := f.folder(t, f.e, box, "Invoices")
	label := f.label(t, f.e, box, "Urgent")
	single := fmt.Sprintf("/mailboxes/%d/messages/%d", box, message)
	batch := fmt.Sprintf("/mailboxes/%d/message-updates", box)

	cases := []struct {
		method, path, body, key string
	}{
		{http.MethodPut, single, `{}`, "change"},
		{http.MethodPost, batch, fmt.Sprintf(`{"message_ids":[%d]}`, message), "change"},
		{http.MethodPut, single, `{"placement":"folder"}`, "folder_id"},
		{http.MethodPut, single, fmt.Sprintf(`{"folder_id":%d}`, folder.ID), "folder_id"},
		{http.MethodPut, single, fmt.Sprintf(`{"placement":"archive","folder_id":%d}`, folder.ID), "folder_id"},
		{http.MethodPost, batch, fmt.Sprintf(`{"message_ids":[%d],"add_label_ids":[%d],"remove_label_ids":[%d]}`, message, label.ID, label.ID), "add_label_ids"},
		{http.MethodPut, single, `{"placement":"spam"}`, "placement"},
		{http.MethodPost, batch, `{"message_ids":[],"read":true}`, "message_ids"},
		{http.MethodPost, batch, fmt.Sprintf(`{"message_ids":[%d,%d],"read":true}`, message, message), "message_ids"},
		{http.MethodPost, batch, fmt.Sprintf(`{"message_ids":[%d],"add_label_ids":[]}`, message), "add_label_ids"},
	}
	for _, tc := range cases {
		recorder, body := serve(router, tc.method, tc.path, tc.body)
		errs, _ := body["errors"].(map[string]any)
		if recorder.Code != http.StatusUnprocessableEntity || body["error_code"] != domain.CodeValidationFailed || errs[tc.key] == nil {
			t.Fatalf("%s %s %s = %d %v; want 422 under %q", tc.method, tc.path, tc.body, recorder.Code, body, tc.key)
		}
	}

	// A referenced folder or label that is not found answers 404 before validation.
	notFound := []struct{ body, code string }{
		{`{"placement":"archive","folder_id":999999}`, domain.CodeMailFolderNotFound},
		{`{"label_ids":[999999]}`, domain.CodeMailLabelNotFound},
	}
	for _, tc := range notFound {
		recorder, body := serve(router, http.MethodPut, single, tc.body)
		if recorder.Code != http.StatusNotFound || body["error_code"] != tc.code {
			t.Fatalf("PUT %s = %d %v; want 404 %s", tc.body, recorder.Code, body, tc.code)
		}
	}
	recorder, body := serve(router, http.MethodPost, batch, `{"message_ids":[999999],"read":true}`)
	if recorder.Code != http.StatusNotFound || body["error_code"] != domain.CodeMailMessageNotFound {
		t.Fatalf("batch missing message = %d %v; want 404", recorder.Code, body)
	}
	recorder, body = serve(router, http.MethodPost, batch, `{"message_ids":`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("malformed JSON = %d %v; want 400", recorder.Code, body)
	}
	recorder, body = serve(router, http.MethodPost, batch, fmt.Sprintf(`{"message_ids":[%d],"placement":"archive","add_label_ids":[%d]}`, message, label.ID))
	data, _ := body["data"].(map[string]any)
	if recorder.Code != http.StatusOK || data["updated"] != float64(1) {
		t.Fatalf("batch archive = %d %v; want 200 updated 1", recorder.Code, body)
	}
}

// Sealed handover history is never purged by the trash worker, however long it sat in the trash:
// only the sealed-history delete removes it.
func TestPurgeExpiredTrashKeepsSealedMessages(t *testing.T) {
	f := newOrganizeFixture(t)
	ctx := context.Background()
	db := f.repo.(*repository).db
	sealed := f.personalMessage.ID
	f.leave(t, f.b, tick())
	f.handOver(t, f.personal.ID)
	expired := time.Now().UTC().Add(-40 * 24 * time.Hour)
	if err := db.Model(&MailMessagePO{}).Where("id = ?", sealed).
		Updates(map[string]any{"placement": string(domain.MailPlacementTrash), "trashed_at": expired}).Error; err != nil {
		t.Fatalf("trash the sealed message: %v", err)
	}
	purged, err := f.service.PurgeExpiredTrash(ctx, time.Now().UTC(), 10)
	if err != nil || purged != 0 {
		t.Fatalf("PurgeExpiredTrash = %d, %v; want nothing purged", purged, err)
	}
	if !f.messageExists(t, sealed) {
		t.Fatal("the purge deleted sealed handover history")
	}
}

// lockSpy counts the message locks a service takes.
type lockSpy struct {
	domain.MailOrganizeRepository
	messageLocks int
}

func (s *lockSpy) LockMessages(ctx context.Context, mailboxID uint, ids []uint) ([]*domain.MailMessage, error) {
	s.messageLocks++
	return s.MailOrganizeRepository.LockMessages(ctx, mailboxID, ids)
}

// Over 100 ids is refused before any message is looked up or locked.
func TestBatchOverLimitIsRefusedBeforeLocking(t *testing.T) {
	f := newOrganizeFixture(t)
	spy := &lockSpy{MailOrganizeRepository: f.service.organize}
	f.service.organize = spy
	ids := make([]uint, MaxMessageIDs+1)
	for i := range ids {
		ids[i] = uint(1_000_000 + i)
	}
	_, err := f.service.UpdateMessages(context.Background(), f.b, f.team.ID, ids, MessageUpdate{Read: boolPtr(true)})
	expectValidation(t, err, "message_ids")
	if spy.messageLocks != 0 {
		t.Fatalf("%d message locks taken for an over-limit batch; want none", spy.messageLocks)
	}
	// Access still comes first.
	_, err = f.service.UpdateMessages(context.Background(), f.c, f.team.ID, ids, MessageUpdate{Placement: placementPtr(domain.MailPlacementArchive)})
	expectError(t, "view member over-limit move", err, domain.ErrPermissionDenied)
}

// A batch that moves messages into a folder while the folder is being deleted waits for the
// delete instead of deadlocking with it: both lock the folder before its messages.
func TestBatchMoveAndFolderDeleteDoNotDeadlock(t *testing.T) {
	f := newOrganizeFixture(t)
	ctx := context.Background()
	db := f.repo.(*repository).db
	box, message := f.team.ID, f.teamMessage.ID
	folder := f.folder(t, f.e, box, "Invoices")
	into := MessageUpdate{Placement: placementPtr(domain.MailPlacementFolder), FolderID: uintPtr(folder.ID)}
	if _, err := f.service.UpdateMessages(ctx, f.b, box, []uint{message}, into); err != nil {
		t.Fatalf("move into the folder: %v", err)
	}

	// The deleting transaction, the batch, and the polling below each need a connection.
	pool, err := db.DB()
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	pool.SetMaxOpenConns(4)

	// The deleting transaction locks the folder first, as DeleteFolder does.
	deleting := db.Begin()
	defer deleting.Rollback()
	var pid int
	if err := deleting.Raw(`SELECT pg_backend_pid()`).Scan(&pid).Error; err != nil {
		t.Fatalf("backend pid: %v", err)
	}
	if err := deleting.Exec(`SELECT id FROM mail_folders WHERE id = ? FOR UPDATE`, folder.ID).Error; err != nil {
		t.Fatalf("lock the folder: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := f.service.UpdateMessages(ctx, f.b, box, []uint{message}, into)
		done <- err
	}()
	// Wait until the batch is blocked behind the deleting transaction.
	deadline := time.Now().Add(5 * time.Second)
	for {
		var blocked int
		poll, cancel := context.WithTimeout(ctx, time.Second)
		err := db.WithContext(poll).Raw(`SELECT count(*) FROM pg_stat_activity WHERE ? = ANY(pg_blocking_pids(pid))`, pid).Scan(&blocked).Error
		cancel()
		if err != nil {
			t.Fatalf("blocked sessions: %v", err)
		}
		if blocked > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the batch never waited for the folder lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The rest of DeleteFolder: move the folder's messages out, then delete it.
	if err := deleting.Exec(`UPDATE mail_messages SET placement = 'default', folder_id = NULL
		WHERE folder_id = ? AND placement = 'folder'`, folder.ID).Error; err != nil {
		t.Fatalf("delete moves the folder's messages: %v", err)
	}
	if err := deleting.Exec(`DELETE FROM mail_folders WHERE id = ?`, folder.ID).Error; err != nil {
		t.Fatalf("delete the folder: %v", err)
	}
	if err := deleting.Commit().Error; err != nil {
		t.Fatalf("commit the delete: %v", err)
	}
	select {
	case err := <-done:
		expectError(t, "batch into a folder deleted meanwhile", err, ErrMailFolderNotFound)
	case <-time.After(10 * time.Second):
		t.Fatal("the batch never finished")
	}
	if po := f.stored(t, message); po.Placement != string(domain.MailPlacementDefault) || po.FolderID != nil {
		t.Fatalf("message after the delete = placement %q folder %v; want default without folder", po.Placement, po.FolderID)
	}
}

// Locking the folder and labels first does not change which 404 answers: a missing message wins.
func TestMissingMessageAnswersBeforeMissingFolderAndLabel(t *testing.T) {
	f := newOrganizeFixture(t)
	_, err := f.service.UpdateMessages(context.Background(), f.b, f.team.ID, []uint{999_999}, MessageUpdate{
		Placement: placementPtr(domain.MailPlacementFolder), FolderID: uintPtr(999_998), AddLabelIDs: []uint{999_997},
	})
	expectError(t, "missing message, folder, and label", err, ErrMailMessageNotFound)
	_, err = f.service.UpdateMessages(context.Background(), f.b, f.team.ID, []uint{f.teamMessage.ID}, MessageUpdate{
		Placement: placementPtr(domain.MailPlacementFolder), FolderID: uintPtr(999_998), AddLabelIDs: []uint{999_997},
	})
	expectError(t, "missing folder and label", err, ErrMailFolderNotFound)
}
