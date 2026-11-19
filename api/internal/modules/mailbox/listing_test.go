package mailbox

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// delivery is one inbound message of the listing tests.
type delivery struct {
	subject, body, fromName string
	cc                      []string
	rfcID, inReplyTo        string
	files                   bool
}

// deliver stores an inbound message in mailbox as the mail edge would, threaded by its ids.
func (f *accessFixture) deliver(t *testing.T, mailbox *domain.Mailbox, in delivery) *domain.MailMessage {
	t.Helper()
	body := in.body
	if body == "" {
		body = "body"
	}
	message := &domain.MailMessage{
		MailboxID: mailbox.ID, Direction: domain.MailDirectionInbound, Status: domain.MailStatusReceived,
		FromAddress: "alice@sender.test", FromName: in.fromName, To: []string{mailbox.Address}, Cc: in.cc,
		Subject: in.subject, TextBody: body, MessageID: in.rfcID, RFCMessageID: in.rfcID, InReplyTo: in.inReplyTo,
		DedupeKey: fmt.Sprintf("%s-%d", in.subject, time.Now().UnixNano()),
	}
	if in.files {
		message.Attachments = []domain.MailAttachment{{Filename: "quote.pdf", ContentType: "application/pdf", Content: []byte("pdf")}}
	}
	if _, err := f.repo.CreateInboundMessage(context.Background(), message); err != nil {
		t.Fatalf("CreateInboundMessage(%q): %v", in.subject, err)
	}
	return message
}

// change applies an update as viewer and fails the test on error.
func (f *accessFixture) change(t *testing.T, viewer domain.OrganizationContext, mailboxID uint, update MessageUpdate, ids ...uint) {
	t.Helper()
	if _, err := f.service.UpdateMessages(context.Background(), viewer, mailboxID, ids, update); err != nil {
		t.Fatalf("UpdateMessages(%v): %v", ids, err)
	}
}

func (f *organizeFixture) move(t *testing.T, placement domain.MailPlacement, folderID uint, ids ...uint) {
	t.Helper()
	update := MessageUpdate{Placement: placementPtr(placement)}
	if folderID != 0 {
		update.FolderID = uintPtr(folderID)
	}
	f.change(t, f.e, f.team.ID, update, ids...)
}

func (f *accessFixture) list(t *testing.T, viewer domain.OrganizationContext, mailboxID uint, filter domain.MailMessageFilter) ([]*domain.MailMessage, int64) {
	t.Helper()
	messages, total, err := f.service.ListMessages(context.Background(), viewer, mailboxID, filter)
	if err != nil {
		t.Fatalf("ListMessages(%+v): %v", filter, err)
	}
	return messages, total
}

func idsOf(messages []*domain.MailMessage) []uint {
	ids := make([]uint, 0, len(messages))
	for _, message := range messages {
		ids = append(ids, message.ID)
	}
	return ids
}

func expectIDs(t *testing.T, what string, got []uint, want ...uint) {
	t.Helper()
	if want == nil {
		want = []uint{}
	}
	if got == nil {
		got = []uint{}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("%s = %v; want %v", what, got, want)
	}
}

// listPath is the listing route of a mailbox with a query.
func listPath(mailboxID uint, query string) string {
	path := fmt.Sprintf("/mailboxes/%d/messages", mailboxID)
	if query != "" {
		path += "?" + query
	}
	return path
}

func dataItems(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	raw, ok := body["data"].([]any)
	if !ok {
		t.Fatalf("data is not a list: %v", body)
	}
	items := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		items = append(items, item.(map[string]any))
	}
	return items
}

func expectStatus(t *testing.T, what string, code int, body map[string]any, wantCode int, wantError string) {
	t.Helper()
	if code != wantCode || (wantError != "" && body["error_code"] != wantError) {
		t.Fatalf("%s = %d %v; want %d %s", what, code, body, wantCode, wantError)
	}
}

func TestListWithoutPlacementExcludesTrash(t *testing.T) {
	f := newOrganizeFixture(t)
	box := f.team.ID
	first := f.deliver(t, f.team, delivery{subject: "First"})
	second := f.deliver(t, f.team, delivery{subject: "Second"})
	// The newer message goes to the trash first, so trashed_at and id order disagree.
	f.move(t, domain.MailPlacementTrash, 0, second.ID)
	f.move(t, domain.MailPlacementTrash, 0, first.ID)

	listed, total := f.list(t, f.c, box, domain.MailMessageFilter{})
	expectIDs(t, "list without placement", idsOf(listed), f.teamMessage.ID)
	if total != 1 {
		t.Fatalf("total = %d; want 1", total)
	}
	inbound, _ := f.list(t, f.c, box, domain.MailMessageFilter{Direction: domain.MailDirectionInbound})
	expectIDs(t, "inbound without placement", idsOf(inbound), f.teamMessage.ID)

	trash, total := f.list(t, f.c, box, domain.MailMessageFilter{Placement: domain.MailPlacementTrash})
	expectIDs(t, "trash, newest trashed first", idsOf(trash), first.ID, second.ID)
	if total != 2 {
		t.Fatalf("trash total = %d; want 2", total)
	}

	recorder, body := serve(accessRouter(f.service, f.c), http.MethodGet, listPath(box, "placement=trash"), "")
	items := dataItems(t, body)
	if recorder.Code != http.StatusOK || len(items) != 2 || items[0]["trashed_at"] == nil || items[0]["placement"] != "trash" {
		t.Fatalf("GET placement=trash = %d %v; want two trashed messages", recorder.Code, body)
	}
	recorder, body = serve(accessRouter(f.service, f.c), http.MethodGet, listPath(box, ""), "")
	if items = dataItems(t, body); recorder.Code != http.StatusOK || len(items) != 1 {
		t.Fatalf("GET without placement = %d %v; want only the message outside the trash", recorder.Code, body)
	}
}

func TestThreadRowShowsLatestAndCount(t *testing.T) {
	f := newOrganizeFixture(t)
	ctx := context.Background()
	box := f.team.ID
	root := f.deliver(t, f.team, delivery{subject: "Quote", rfcID: "root@sender.test"})
	other := f.deliver(t, f.team, delivery{subject: "Other", rfcID: "other@sender.test"})
	reply := f.deliver(t, f.team, delivery{subject: "Re: Quote", rfcID: "r1@sender.test", inReplyTo: "root@sender.test"})
	archived := f.deliver(t, f.team, delivery{subject: "Re: Re: Quote", rfcID: "r2@sender.test", inReplyTo: "r1@sender.test"})
	if reply.ThreadID != root.ID || archived.ThreadID != root.ID {
		t.Fatalf("thread ids = %d, %d; want %d", reply.ThreadID, archived.ThreadID, root.ID)
	}
	f.move(t, domain.MailPlacementArchive, 0, archived.ID)
	urgent := f.label(t, f.e, box, "Urgent")
	f.change(t, f.e, box, MessageUpdate{AddLabelIDs: []uint{urgent.ID}}, root.ID)
	f.change(t, f.c, box, MessageUpdate{Read: boolPtr(true)}, reply.ID)
	f.change(t, f.c, box, MessageUpdate{Starred: boolPtr(true)}, root.ID)

	inbox := domain.MailMessageFilter{Placement: domain.MailPlacementDefault, Direction: domain.MailDirectionInbound, GroupThreads: true}
	rows, total := f.list(t, f.c, box, inbox)
	expectIDs(t, "thread rows", idsOf(rows), reply.ID, other.ID, f.teamMessage.ID)
	if total != 3 {
		t.Fatalf("thread total = %d; want 3", total)
	}
	thread := rows[0].Thread
	if thread == nil || thread.MessageCount != 2 || !thread.Unread || !thread.Starred {
		t.Fatalf("root thread = %+v; want 2 messages, unread, starred", thread)
	}
	expectIDs(t, "root thread message ids", thread.MessageIDs, root.ID, reply.ID)
	expectIDs(t, "root thread labels", thread.LabelIDs, urgent.ID)
	single := rows[1].Thread
	if single == nil || single.MessageCount != 1 || !single.Unread || single.Starred || len(single.LabelIDs) != 0 {
		t.Fatalf("single thread = %+v; want one unread message without star or label", single)
	}
	expectIDs(t, "single thread message ids", single.MessageIDs, other.ID)

	// B has read nothing and starred nothing: the flags are the caller's own.
	rows, _ = f.list(t, f.b, box, inbox)
	if rows[0].Thread.Starred || !rows[0].Thread.Unread {
		t.Fatalf("B's root thread = %+v; want unread and not starred", rows[0].Thread)
	}
	f.change(t, f.c, box, MessageUpdate{Read: boolPtr(true)}, root.ID)
	rows, _ = f.list(t, f.c, box, inbox)
	if rows[0].Thread.Unread {
		t.Fatalf("root thread after reading both = %+v; want read", rows[0].Thread)
	}

	// The archive is a view of its own.
	rows, total = f.list(t, f.c, box, domain.MailMessageFilter{Placement: domain.MailPlacementArchive, GroupThreads: true})
	if len(rows) != 1 || rows[0].ID != archived.ID || rows[0].Thread.MessageCount != 1 || total != 1 {
		t.Fatalf("archive threads = %v (total %d); want the archived reply alone", idsOf(rows), total)
	}

	// Page two holds the rest, and meta counts threads.
	paged, total := f.list(t, f.c, box, domain.MailMessageFilter{Placement: domain.MailPlacementDefault, Direction: domain.MailDirectionInbound, GroupThreads: true, Page: 2, PerPage: 2})
	expectIDs(t, "second page of threads", idsOf(paged), f.teamMessage.ID)
	if total != 3 {
		t.Fatalf("paged thread total = %d; want 3", total)
	}

	router := accessRouter(f.service, f.c)
	recorder, body := serve(router, http.MethodGet, listPath(box, "placement=default&direction=inbound&group=thread"), "")
	items := dataItems(t, body)
	meta, _ := body["meta"].(map[string]any)
	if recorder.Code != http.StatusOK || len(items) != 3 || meta["total"] != float64(3) {
		t.Fatalf("GET group=thread = %d %v; want three thread rows", recorder.Code, body)
	}
	row, _ := items[0]["thread"].(map[string]any)
	if row == nil || row["message_count"] != float64(2) || row["unread"] != false || row["starred"] != true ||
		fmt.Sprint(row["message_ids"]) != fmt.Sprintf("[%d %d]", root.ID, reply.ID) || fmt.Sprint(row["label_ids"]) != fmt.Sprintf("[%d]", urgent.ID) {
		t.Fatalf("thread of the first row = %v", items[0]["thread"])
	}
	recorder, body = serve(router, http.MethodGet, listPath(box, "placement=default&direction=inbound"), "")
	if items = dataItems(t, body); recorder.Code != http.StatusOK || len(items) != 4 {
		t.Fatalf("GET inbox = %d %v; want the four inbox messages", recorder.Code, body)
	}
	if _, grouped := items[0]["thread"]; grouped {
		t.Fatalf("a single-message row carries thread: %v", items[0])
	}
	recorder, body = serve(router, http.MethodGet, listPath(box, "placement=default&group=thread"), "")
	expectStatus(t, "group=thread on default without direction", recorder.Code, body, http.StatusBadRequest, domain.CodeInvalidInput)
	if _, _, err := f.service.ListMessages(ctx, f.c, box, domain.MailMessageFilter{Placement: domain.MailPlacementDefault, GroupThreads: true}); err == nil {
		t.Fatal("grouping default without direction succeeded")
	}
}

func TestThreadViewIncludesSentReplies(t *testing.T) {
	f := newOrganizeFixture(t)
	ctx := context.Background()
	box := f.team.ID
	root := f.deliver(t, f.team, delivery{subject: "Question", body: "Can you quote?", rfcID: "ask@sender.test"})
	sent, _, err := f.service.SendMessage(ctx, f.b, box, SendInput{
		To: []string{"alice@sender.test"}, Subject: "Re: Question", Text: "Here it is", InReplyTo: "ask@sender.test",
	})
	if err != nil {
		t.Fatalf("send the reply: %v", err)
	}
	if sent.ThreadID != root.ID {
		t.Fatalf("sent reply thread = %d; want %d", sent.ThreadID, root.ID)
	}
	trashed := f.deliver(t, f.team, delivery{subject: "Re: Re: Question", rfcID: "late@sender.test", inReplyTo: "ask@sender.test"})
	lone := f.deliver(t, f.team, delivery{subject: "Lone", rfcID: "lone@sender.test"})
	f.move(t, domain.MailPlacementTrash, 0, trashed.ID, lone.ID)
	f.move(t, domain.MailPlacementArchive, 0, root.ID)
	f.change(t, f.c, box, MessageUpdate{Starred: boolPtr(true)}, sent.ID)

	messages, err := f.service.GetThread(ctx, f.c, box, root.ID)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	expectIDs(t, "thread messages, oldest first", idsOf(messages), root.ID, sent.ID)
	if messages[0].TextBody != "Can you quote?" || messages[1].Direction != domain.MailDirectionOutbound || !messages[1].Starred || !messages[1].Read {
		t.Fatalf("thread messages = %+v, %+v", messages[0], messages[1])
	}

	_, err = f.service.GetThread(ctx, f.c, box, 999_999)
	expectError(t, "a thread that does not exist", err, ErrMailMessageNotFound)
	_, err = f.service.GetThread(ctx, f.b, box, f.personalMessage.ThreadID)
	expectError(t, "another mailbox's thread", err, ErrMailMessageNotFound)
	_, err = f.service.GetThread(ctx, f.c, box, lone.ThreadID)
	expectError(t, "a thread whose only message is trashed", err, ErrMailMessageNotFound)
	_, err = f.service.GetThread(ctx, f.d, box, root.ID)
	expectError(t, "a stranger", err, ErrMailboxNotFound)

	path := fmt.Sprintf("/mailboxes/%d/threads/%d", box, root.ID)
	recorder, body := serve(accessRouter(f.service, f.c), http.MethodGet, path, "")
	items := dataItems(t, body)
	if recorder.Code != http.StatusOK || len(items) != 2 || items[1]["direction"] != "outbound" || items[0]["text_body"] != "Can you quote?" {
		t.Fatalf("GET thread = %d %v", recorder.Code, body)
	}
	if _, ok := items[0]["events"].([]any); !ok {
		t.Fatalf("thread message without events: %v", items[0])
	}
	recorder, body = serve(accessRouter(f.service, f.d), http.MethodGet, path, "")
	expectStatus(t, "stranger GET thread", recorder.Code, body, http.StatusNotFound, domain.CodeMailboxNotFound)
	recorder, body = serve(accessRouter(f.service, f.c), http.MethodGet, fmt.Sprintf("/mailboxes/%d/threads/999999", box), "")
	expectStatus(t, "GET missing thread", recorder.Code, body, http.StatusNotFound, domain.CodeMailMessageNotFound)
}

func TestStarredViewIsNotGrouped(t *testing.T) {
	f := newOrganizeFixture(t)
	box := f.team.ID
	root := f.deliver(t, f.team, delivery{subject: "Plan", rfcID: "plan@sender.test"})
	reply := f.deliver(t, f.team, delivery{subject: "Re: Plan", rfcID: "plan2@sender.test", inReplyTo: "plan@sender.test"})
	f.change(t, f.c, box, MessageUpdate{Starred: boolPtr(true)}, root.ID, reply.ID)

	starred, total := f.list(t, f.c, box, domain.MailMessageFilter{Starred: boolPtr(true)})
	expectIDs(t, "starred messages", idsOf(starred), reply.ID, root.ID)
	if total != 2 || starred[0].Thread != nil {
		t.Fatalf("starred listing total %d, thread %+v; want two single messages", total, starred[0].Thread)
	}
	notStarred, _ := f.list(t, f.c, box, domain.MailMessageFilter{Starred: boolPtr(false)})
	expectIDs(t, "messages without a star", idsOf(notStarred), f.teamMessage.ID)
	// B starred nothing.
	byB, _ := f.list(t, f.b, box, domain.MailMessageFilter{Starred: boolPtr(true)})
	expectIDs(t, "B's starred messages", idsOf(byB))

	router := accessRouter(f.service, f.c)
	recorder, body := serve(router, http.MethodGet, listPath(box, "starred=true"), "")
	items := dataItems(t, body)
	if recorder.Code != http.StatusOK || len(items) != 2 {
		t.Fatalf("GET starred=true = %d %v", recorder.Code, body)
	}
	if _, grouped := items[0]["thread"]; grouped {
		t.Fatalf("starred row carries thread: %v", items[0])
	}
	for _, query := range []string{
		"placement=default&direction=inbound&group=thread&starred=true",
		"placement=default&direction=inbound&group=thread&unread=true",
		"placement=default&direction=inbound&group=thread&has_attachments=true",
		"placement=default&direction=inbound&group=thread&q=plan",
		fmt.Sprintf("placement=default&direction=inbound&group=thread&label_id=%d", f.label(t, f.e, box, "Later").ID),
		"placement=trash&group=thread",
		"group=thread",
		"direction=inbound&group=thread",
		"placement=archive&group=messages",
	} {
		recorder, body = serve(router, http.MethodGet, listPath(box, query), "")
		expectStatus(t, "GET "+query, recorder.Code, body, http.StatusBadRequest, domain.CodeInvalidInput)
	}
}

func TestSearchFindsChineseSubstring(t *testing.T) {
	f := newOrganizeFixture(t)
	box := f.team.ID
	quote := f.deliver(t, f.team, delivery{subject: "报价单确认"})
	f.deliver(t, f.team, delivery{subject: "报价"})
	archived := f.deliver(t, f.team, delivery{subject: "Monthly", body: "附件是新的报价单。"})
	trashed := f.deliver(t, f.team, delivery{subject: "报价单草稿"})
	byName := f.deliver(t, f.team, delivery{subject: "Hello", fromName: "张报价单"})
	byCc := f.deliver(t, f.team, delivery{subject: "Invoice", cc: []string{"finance@corp.test"}})
	f.move(t, domain.MailPlacementArchive, 0, archived.ID)
	f.move(t, domain.MailPlacementTrash, 0, trashed.ID)

	found, total := f.list(t, f.c, box, domain.MailMessageFilter{Query: "报价单"})
	expectIDs(t, "search 报价单", idsOf(found), byName.ID, archived.ID, quote.ID)
	if total != 3 || found[0].Thread != nil {
		t.Fatalf("search total = %d; want 3 single messages", total)
	}
	cc, _ := f.list(t, f.c, box, domain.MailMessageFilter{Query: "FINANCE@corp"})
	expectIDs(t, "search by cc ignoring case", idsOf(cc), byCc.ID)
	narrowed, _ := f.list(t, f.c, box, domain.MailMessageFilter{Query: "报价单", Placement: domain.MailPlacementArchive})
	expectIDs(t, "search narrowed to the archive", idsOf(narrowed), archived.ID)

	router := accessRouter(f.service, f.c)
	recorder, body := serve(router, http.MethodGet, listPath(box, "q="+url.QueryEscape("  报价单 ")), "")
	if items := dataItems(t, body); recorder.Code != http.StatusOK || len(items) != 3 {
		t.Fatalf("GET q=报价单 = %d %v; want three results", recorder.Code, body)
	}
	recorder, body = serve(router, http.MethodGet, listPath(box, "q="+url.QueryEscape(strings.Repeat("报", 200))), "")
	if items := dataItems(t, body); recorder.Code != http.StatusOK || len(items) != 0 {
		t.Fatalf("GET q of 200 characters = %d %v; want an empty page", recorder.Code, body)
	}
	for _, query := range []string{
		"q=" + url.QueryEscape(strings.Repeat("a", 201)),
		"q=" + url.QueryEscape("   "),
		"q=",
		"q=report&placement=trash",
	} {
		recorder, body = serve(router, http.MethodGet, listPath(box, query), "")
		expectStatus(t, "GET "+query, recorder.Code, body, http.StatusBadRequest, domain.CodeInvalidInput)
	}
}

// Review focus 5: %, _, and \ in a search match themselves.
func TestSearchEscapesWildcards(t *testing.T) {
	f := newOrganizeFixture(t)
	box := f.team.ID
	percent := f.deliver(t, f.team, delivery{subject: "50% off"})
	f.deliver(t, f.team, delivery{subject: "500 units"})
	underscore := f.deliver(t, f.team, delivery{subject: "a_b report"})
	f.deliver(t, f.team, delivery{subject: "axb report"})
	backslash := f.deliver(t, f.team, delivery{subject: `C:\temp`})
	f.deliver(t, f.team, delivery{subject: "C:temp"})

	for _, tc := range []struct {
		query string
		want  uint
	}{
		{"50%", percent.ID},
		{"%", percent.ID},
		{"a_b", underscore.ID},
		{`:\t`, backslash.ID},
		{`\`, backslash.ID},
	} {
		found, _ := f.list(t, f.c, box, domain.MailMessageFilter{Query: tc.query})
		expectIDs(t, "search "+tc.query, idsOf(found), tc.want)
	}
	recorder, body := serve(accessRouter(f.service, f.c), http.MethodGet, listPath(box, "q="+url.QueryEscape("50%")), "")
	if items := dataItems(t, body); recorder.Code != http.StatusOK || len(items) != 1 {
		t.Fatalf("GET q=50%% = %d %v; want one result", recorder.Code, body)
	}
}

// A search the database cannot finish in time answers SEARCH_TOO_BROAD. The table is held by
// another transaction, so the search waits until its statement timeout cancels it.
func TestSearchTimeoutMapsToTooBroad(t *testing.T) {
	f := newOrganizeFixture(t)
	repo := f.repo.(*repository)
	widenPool(t, repo.db)
	repo.searchTimeout = 100 * time.Millisecond

	holder := repo.db.Begin()
	// A failing assertion must still release the table, or dropping the test schema waits forever.
	released := false
	release := func() {
		if !released {
			released = true
			if err := holder.Rollback().Error; err != nil {
				t.Errorf("release mail_messages: %v", err)
			}
		}
	}
	t.Cleanup(release)
	if err := holder.Exec(`LOCK TABLE mail_messages IN ACCESS EXCLUSIVE MODE`).Error; err != nil {
		t.Fatalf("lock mail_messages: %v", err)
	}
	_, _, err := f.service.ListMessages(context.Background(), f.c, f.team.ID, domain.MailMessageFilter{Query: "Team"})
	expectError(t, "a search that times out", err, ErrMailSearchTooBroad)
	recorder, body := serve(accessRouter(f.service, f.c), http.MethodGet, listPath(f.team.ID, "q=Team"), "")
	expectStatus(t, "GET a search that times out", recorder.Code, body, http.StatusUnprocessableEntity, domain.CodeMailSearchTooBroad)
	release()

	found, _ := f.list(t, f.c, f.team.ID, domain.MailMessageFilter{Query: "Team"})
	expectIDs(t, "the same search once the table is free", idsOf(found), f.teamMessage.ID)
}

func TestUnreadCountOnlyInbox(t *testing.T) {
	f := newOrganizeFixture(t)
	ctx := context.Background()
	box := f.team.ID
	folder := f.folder(t, f.e, box, "Invoices")
	archived := f.deliver(t, f.team, delivery{subject: "Archived"})
	filed := f.deliver(t, f.team, delivery{subject: "Filed"})
	trashed := f.deliver(t, f.team, delivery{subject: "Trashed"})
	f.move(t, domain.MailPlacementArchive, 0, archived.ID)
	f.move(t, domain.MailPlacementFolder, folder.ID, filed.ID)
	f.move(t, domain.MailPlacementTrash, 0, trashed.ID)
	sent, _, err := f.service.SendMessage(ctx, f.b, box, sendInput())
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	if listed := listedMailbox(t, f.service, f.c, box); listed.UnreadCount != 1 {
		t.Fatalf("unread_count = %d; want 1, the Inbox message only", listed.UnreadCount)
	}
	unread, _ := f.list(t, f.c, box, domain.MailMessageFilter{Unread: boolPtr(true)})
	expectIDs(t, "unread=true", idsOf(unread), filed.ID, archived.ID, f.teamMessage.ID)
	read, _ := f.list(t, f.c, box, domain.MailMessageFilter{Unread: boolPtr(false)})
	expectIDs(t, "unread=false", idsOf(read), sent.ID)
	outboundUnread, _ := f.list(t, f.c, box, domain.MailMessageFilter{Unread: boolPtr(true), Direction: domain.MailDirectionOutbound})
	expectIDs(t, "unread outbound", idsOf(outboundUnread))

	f.change(t, f.c, box, MessageUpdate{Read: boolPtr(true)}, f.teamMessage.ID)
	if listed := listedMailbox(t, f.service, f.c, box); listed.UnreadCount != 0 {
		t.Fatalf("unread_count after reading the Inbox = %d; want 0", listed.UnreadCount)
	}
	if listed := listedMailbox(t, f.service, f.b, box); listed.UnreadCount != 1 {
		t.Fatalf("B's unread_count = %d; want 1", listed.UnreadCount)
	}
}

func TestMessageCountsPerFolder(t *testing.T) {
	f := newOrganizeFixture(t)
	ctx := context.Background()
	box := f.team.ID
	invoices := f.folder(t, f.e, box, "Invoices")
	empty := f.folder(t, f.e, box, "Archive 2025")
	one := f.deliver(t, f.team, delivery{subject: "Invoice 1"})
	two := f.deliver(t, f.team, delivery{subject: "Invoice 2"})
	archived := f.deliver(t, f.team, delivery{subject: "Archived"})
	trashed := f.deliver(t, f.team, delivery{subject: "Trashed"})
	f.move(t, domain.MailPlacementFolder, invoices.ID, one.ID, two.ID)
	f.move(t, domain.MailPlacementArchive, 0, archived.ID)
	f.move(t, domain.MailPlacementTrash, 0, trashed.ID)
	f.change(t, f.c, box, MessageUpdate{Read: boolPtr(true)}, one.ID)
	f.change(t, f.c, box, MessageUpdate{Starred: boolPtr(true)}, one.ID, f.teamMessage.ID, trashed.ID)

	counts, err := f.service.MessageCounts(ctx, f.c, box)
	if err != nil {
		t.Fatalf("MessageCounts: %v", err)
	}
	want := domain.MailMessageCounts{
		DefaultInboundUnread: 1, ArchiveUnread: 1, Starred: 2,
		Folders: []domain.MailFolderCount{{FolderID: empty.ID, Unread: 0}, {FolderID: invoices.ID, Unread: 1}},
	}
	if fmt.Sprintf("%+v", counts) != fmt.Sprintf("%+v", want) {
		t.Fatalf("C's counts = %+v; want %+v", counts, want)
	}
	byB, err := f.service.MessageCounts(ctx, f.b, box)
	if err != nil || byB.Folders[1].Unread != 2 || byB.Starred != 0 || byB.DefaultInboundUnread != 1 {
		t.Fatalf("B's counts = %+v, %v; want 2 unread in Invoices and nothing starred", byB, err)
	}
	if listed := listedMailbox(t, f.service, f.c, box); listed.UnreadCount != counts.DefaultInboundUnread {
		t.Fatalf("unread_count %d differs from default_inbound_unread %d", listed.UnreadCount, counts.DefaultInboundUnread)
	}

	path := fmt.Sprintf("/mailboxes/%d/message-counts", box)
	recorder, body := serve(accessRouter(f.service, f.c), http.MethodGet, path, "")
	data, _ := body["data"].(map[string]any)
	placements, _ := data["placements"].(map[string]any)
	folders, _ := data["folders"].([]any)
	if recorder.Code != http.StatusOK || placements["default_inbound_unread"] != float64(1) || placements["archive_unread"] != float64(1) ||
		len(folders) != 2 || data["starred"] != float64(2) {
		t.Fatalf("GET message-counts = %d %v", recorder.Code, body)
	}
	recorder, body = serve(accessRouter(f.service, f.d), http.MethodGet, path, "")
	expectStatus(t, "stranger GET message-counts", recorder.Code, body, http.StatusNotFound, domain.CodeMailboxNotFound)
	recorder, body = serve(accessRouter(f.service, f.admin), http.MethodGet, path, "")
	expectStatus(t, "admin without view GET message-counts", recorder.Code, body, http.StatusForbidden, domain.CodePermissionDenied)
}

func TestListFilters(t *testing.T) {
	f := newOrganizeFixture(t)
	box := f.team.ID
	folder := f.folder(t, f.e, box, "Clients")
	label := f.label(t, f.e, box, "Follow up")
	foreignFolder := f.folder(t, f.b, f.personal.ID, "Mine")
	foreignLabel := f.label(t, f.b, f.personal.ID, "Mine")
	plain := f.deliver(t, f.team, delivery{subject: "Plain"})
	filed := f.deliver(t, f.team, delivery{subject: "Filed", files: true})
	f.move(t, domain.MailPlacementFolder, folder.ID, filed.ID)
	f.change(t, f.e, box, MessageUpdate{AddLabelIDs: []uint{label.ID}}, plain.ID, filed.ID)

	inFolder, _ := f.list(t, f.c, box, domain.MailMessageFilter{Placement: domain.MailPlacementFolder, FolderID: folder.ID})
	expectIDs(t, "folder", idsOf(inFolder), filed.ID)
	labeled, _ := f.list(t, f.c, box, domain.MailMessageFilter{LabelID: label.ID})
	expectIDs(t, "label", idsOf(labeled), filed.ID, plain.ID)
	withFiles, _ := f.list(t, f.c, box, domain.MailMessageFilter{HasAttachments: boolPtr(true)})
	expectIDs(t, "with files", idsOf(withFiles), filed.ID, f.teamMessage.ID)
	withoutFiles, _ := f.list(t, f.c, box, domain.MailMessageFilter{HasAttachments: boolPtr(false), LabelID: label.ID})
	expectIDs(t, "labeled without files", idsOf(withoutFiles), plain.ID)
	threads, _ := f.list(t, f.c, box, domain.MailMessageFilter{Placement: domain.MailPlacementFolder, FolderID: folder.ID, GroupThreads: true})
	expectIDs(t, "folder threads", idsOf(threads), filed.ID)

	c := accessRouter(f.service, f.c)
	for _, tc := range []struct {
		viewer    domain.OrganizationContext
		query     string
		status    int
		errorCode string
	}{
		{f.c, fmt.Sprintf("placement=folder&folder_id=%d", foreignFolder.ID), http.StatusNotFound, domain.CodeMailFolderNotFound},
		{f.c, fmt.Sprintf("label_id=%d", foreignLabel.ID), http.StatusNotFound, domain.CodeMailLabelNotFound},
		{f.c, fmt.Sprintf("folder_id=%d", folder.ID), http.StatusBadRequest, domain.CodeInvalidInput},
		{f.c, "placement=folder", http.StatusBadRequest, domain.CodeInvalidInput},
		{f.c, "placement=spam", http.StatusBadRequest, domain.CodeInvalidInput},
		{f.c, "direction=sideways", http.StatusBadRequest, domain.CodeInvalidInput},
		{f.c, "starred=yes", http.StatusBadRequest, domain.CodeInvalidInput},
		{f.c, "unread=1", http.StatusBadRequest, domain.CodeInvalidInput},
		{f.c, "has_attachments=maybe", http.StatusBadRequest, domain.CodeInvalidInput},
		{f.c, "label_id=0", http.StatusBadRequest, domain.CodeInvalidInput},
		{f.c, "placement=folder&folder_id=abc", http.StatusBadRequest, domain.CodeInvalidInput},
		// A referenced folder or label answers before validation, access before both.
		{f.c, fmt.Sprintf("label_id=%d&starred=yes", foreignLabel.ID), http.StatusNotFound, domain.CodeMailLabelNotFound},
		{f.c, fmt.Sprintf("folder_id=%d", foreignFolder.ID), http.StatusNotFound, domain.CodeMailFolderNotFound},
		{f.admin, fmt.Sprintf("label_id=%d&starred=yes", foreignLabel.ID), http.StatusForbidden, domain.CodePermissionDenied},
		{f.d, "placement=spam&starred=yes", http.StatusNotFound, domain.CodeMailboxNotFound},
	} {
		router := c
		if tc.viewer.UserID != f.c.UserID {
			router = accessRouter(f.service, tc.viewer)
		}
		recorder, body := serve(router, http.MethodGet, listPath(box, tc.query), "")
		expectStatus(t, fmt.Sprintf("GET %s as user %d", tc.query, tc.viewer.UserID), recorder.Code, body, tc.status, tc.errorCode)
	}
	recorder, body := serve(c, http.MethodGet, listPath(box, fmt.Sprintf("placement=folder&folder_id=%d&label_id=%d&has_attachments=true", folder.ID, label.ID)), "")
	if items := dataItems(t, body); recorder.Code != http.StatusOK || len(items) != 1 {
		t.Fatalf("GET combined filters = %d %v; want the filed message", recorder.Code, body)
	}
}
