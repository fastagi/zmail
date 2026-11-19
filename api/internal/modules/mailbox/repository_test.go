package mailbox

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// mailboxOwnerPO stands in for the organizations table so the owner rows exist without
// importing another module.
type mailboxOwnerPO struct {
	ID uint `gorm:"primaryKey"`
}

func (mailboxOwnerPO) TableName() string { return "organizations" }

func openRepository(t *testing.T) (*repository, *gorm.DB) {
	t.Helper()
	db := testplatform.OpenPostgres(
		t, &gorm.Config{TranslateError: true}, &mailboxOwnerPO{}, &MailboxPO{}, &MailboxMemberPO{}, &MailboxAccessEventPO{}, &MailFolderPO{}, &MailLabelPO{}, &MailMessagePO{}, &MailMessageReadPO{}, &MailMessageStarPO{}, &MailMessageLabelPO{}, &MailMessageEventPO{}, &MailAttachmentPO{}, &MailTemplatePO{}, &MailContactPO{}, &MailContactListPO{}, &MailContactListMemberPO{}, &MailBroadcastPO{}, &MailBroadcastRecipientPO{})
	return NewRepository(db), db
}

func createOwner(t *testing.T, db *gorm.DB, id uint) {
	t.Helper()
	if err := db.Create(&mailboxOwnerPO{ID: id}).Error; err != nil {
		t.Fatalf("create owner: %v", err)
	}
}

func createMailbox(t *testing.T, repo *repository, address string) *domain.Mailbox {
	t.Helper()
	mailbox := &domain.Mailbox{OrganizationID: 1, Address: address, DisplayName: "Owner", Type: domain.MailboxTypeShared}
	if err := repo.CreateMailbox(context.Background(), mailbox, domain.MaxMailboxesPerOrganization); err != nil {
		t.Fatalf("CreateMailbox(%q): %v", address, err)
	}
	// as(1) works the mailbox, like the creator a service-created mailbox gets as its manager.
	addMember(t, repo, mailbox.ID, as(1), domain.MailboxPermissionManage)
	return mailbox
}

// addMember gives viewer's membership a permission on a mailbox.
func addMember(t *testing.T, repo *repository, mailboxID uint, viewer domain.OrganizationContext, permission domain.MailboxPermission) {
	t.Helper()
	member := &domain.MailboxMember{
		MailboxID: mailboxID, MembershipID: viewer.MembershipID, UserID: viewer.UserID, Permission: permission, GrantedBy: viewer.UserID,
	}
	if _, err := repo.UpsertMailboxMember(context.Background(), member, domain.MaxMailboxMembers); err != nil {
		t.Fatalf("UpsertMailboxMember: %v", err)
	}
}

func TestRepositoryKeepsAReleasedAddressReserved(t *testing.T) {
	repo, db := openRepository(t)
	ctx := context.Background()
	createOwner(t, db, 1)
	createOwner(t, db, 2)

	mailbox := createMailbox(t, repo, "hello@mail.example")
	duplicate := &domain.Mailbox{OrganizationID: 2, Address: "hello@mail.example"}
	if err := repo.CreateMailbox(ctx, duplicate, domain.MaxMailboxesPerOrganization); !errors.Is(err, ErrMailboxAddressTaken) {
		t.Fatalf("duplicate address error = %v", err)
	}

	if err := repo.DeleteMailbox(ctx, 1, mailbox.ID); err != nil {
		t.Fatalf("DeleteMailbox: %v", err)
	}
	if _, err := repo.FindMailboxByAddress(ctx, "hello@mail.example"); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("deleted mailbox lookup error = %v", err)
	}
	// The next owner must not inherit mail addressed to the previous one.
	if err := repo.CreateMailbox(ctx, duplicate, domain.MaxMailboxesPerOrganization); !errors.Is(err, ErrMailboxAddressTaken) {
		t.Fatalf("released address error = %v; want ErrMailboxAddressTaken", err)
	}
}

func TestRepositoryEnforcesTheMailboxLimitPerOrganization(t *testing.T) {
	repo, db := openRepository(t)
	ctx := context.Background()
	createOwner(t, db, 1)
	createOwner(t, db, 2)

	for _, address := range []string{"a@mail.example", "b@mail.example"} {
		if err := repo.CreateMailbox(ctx, &domain.Mailbox{OrganizationID: 1, Address: address}, 2); err != nil {
			t.Fatalf("CreateMailbox(%q): %v", address, err)
		}
	}
	if err := repo.CreateMailbox(ctx, &domain.Mailbox{OrganizationID: 1, Address: "c@mail.example"}, 2); !errors.Is(err, ErrMailboxLimitReached) {
		t.Fatalf("limit error = %v", err)
	}
	if err := repo.CreateMailbox(ctx, &domain.Mailbox{OrganizationID: 2, Address: "c@mail.example"}, 2); err != nil {
		t.Fatalf("another organization is limited by the first one's mailboxes: %v", err)
	}

	owned, err := repo.ListMailboxes(ctx, 1, 0)
	if err != nil || len(owned) != 2 || owned[0].Address != "a@mail.example" {
		t.Fatalf("ListMailboxes = %v, %v", owned, err)
	}
}

func TestRepositoryScopesMailboxesToTheirOrganization(t *testing.T) {
	repo, db := openRepository(t)
	ctx := context.Background()
	createOwner(t, db, 1)
	createOwner(t, db, 2)
	mailbox := createMailbox(t, repo, "hello@mail.example")

	if _, err := repo.FindMailbox(ctx, 2, mailbox.ID); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("FindMailbox as another user error = %v", err)
	}
	if err := repo.DeleteMailbox(ctx, 2, mailbox.ID); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("DeleteMailbox as another user error = %v", err)
	}
	if found, err := repo.FindMailbox(ctx, 1, mailbox.ID); err != nil || found.Address != "hello@mail.example" {
		t.Fatalf("FindMailbox = %+v, %v", found, err)
	}
}

func TestRepositoryStoresAnInboundMessageOncePerMailbox(t *testing.T) {
	repo, db := openRepository(t)
	ctx := context.Background()
	createOwner(t, db, 1)
	first := createMailbox(t, repo, "one@mail.example")
	second := createMailbox(t, repo, "two@mail.example")

	inbound := func(mailboxID uint) *domain.MailMessage {
		return &domain.MailMessage{
			MailboxID: mailboxID, Direction: domain.MailDirectionInbound, Status: domain.MailStatusReceived,
			FromAddress: "alice@sender.test", To: []string{"one@mail.example"}, Cc: []string{"cc@sender.test"},
			Subject: "Hi", TextBody: "text", HTMLBody: "<p>html</p>", MessageID: "m@sender.test", DedupeKey: "key-1",
		}
	}

	message := inbound(first.ID)
	created, err := repo.CreateInboundMessage(ctx, message)
	if err != nil || !created || message.ID == 0 {
		t.Fatalf("first delivery = created %v, id %d, err %v", created, message.ID, err)
	}
	retry := inbound(first.ID)
	created, err = repo.CreateInboundMessage(ctx, retry)
	if err != nil || created || retry.ID != message.ID {
		t.Fatalf("retry = created %v, id %d, err %v; want the stored message %d", created, retry.ID, err, message.ID)
	}
	other := inbound(second.ID)
	created, err = repo.CreateInboundMessage(ctx, other)
	if err != nil || !created {
		t.Fatalf("same message to another mailbox = created %v, err %v", created, err)
	}

	// Outbound messages carry no dedupe key and never collide with each other.
	for i := 0; i < 2; i++ {
		outbound := &domain.MailMessage{
			MailboxID: first.ID, Direction: domain.MailDirectionOutbound, Status: domain.MailStatusQueued,
			FromAddress: "one@mail.example", To: []string{"bob@example.com"}, Subject: "Out",
		}
		if _, createErr := repo.CreateMessage(ctx, outbound); createErr != nil {
			t.Fatalf("CreateMessage %d: %v", i, createErr)
		}
	}

	stored, err := repo.FindMessage(ctx, first.ID, message.ID)
	if err != nil {
		t.Fatalf("FindMessage: %v", err)
	}
	if stored.TextBody != "text" || stored.HTMLBody != "<p>html</p>" || len(stored.Cc) != 1 || stored.Cc[0] != "cc@sender.test" {
		t.Fatalf("stored = %+v", stored)
	}
	if _, err := repo.FindMessage(ctx, second.ID, message.ID); !errors.Is(err, ErrMailMessageNotFound) {
		t.Fatalf("FindMessage through another mailbox error = %v", err)
	}
}

func TestRepositoryListsMessagesWithoutBodies(t *testing.T) {
	repo, db := openRepository(t)
	ctx := context.Background()
	createOwner(t, db, 1)
	mailbox := createMailbox(t, repo, "hello@mail.example")

	for i, direction := range []domain.MailDirection{domain.MailDirectionInbound, domain.MailDirectionOutbound, domain.MailDirectionInbound} {
		message := &domain.MailMessage{
			MailboxID: mailbox.ID, Direction: direction, Status: domain.MailStatusReceived,
			FromAddress: "alice@sender.test", To: []string{"hello@mail.example"},
			Subject: "Message", TextBody: "secret body", HTMLBody: "<p>secret</p>",
		}
		if direction == domain.MailDirectionOutbound {
			message.Status = domain.MailStatusQueued
		}
		if _, err := repo.CreateMessage(ctx, message); err != nil {
			t.Fatalf("CreateMessage %d: %v", i, err)
		}
	}

	inbox, total, err := repo.ListMessages(ctx, mailbox.ID, domain.MailMessageFilter{Direction: domain.MailDirectionInbound, Page: 1, PerPage: 1})
	if err != nil || total != 2 || len(inbox) != 1 {
		t.Fatalf("inbox page = %d rows, total %d, err %v", len(inbox), total, err)
	}
	if inbox[0].TextBody != "" || inbox[0].HTMLBody != "" {
		t.Fatalf("listing returned a body: %+v", inbox[0])
	}
	if inbox[0].To[0] != "hello@mail.example" {
		t.Fatalf("listing lost recipients: %+v", inbox[0])
	}
	second, _, err := repo.ListMessages(ctx, mailbox.ID, domain.MailMessageFilter{Direction: domain.MailDirectionInbound, Page: 2, PerPage: 1})
	if err != nil || len(second) != 1 || second[0].ID >= inbox[0].ID {
		t.Fatalf("second page = %+v, err %v; want the older message", second, err)
	}
	all, total, err := repo.ListMessages(ctx, mailbox.ID, domain.MailMessageFilter{})
	if err != nil || total != 3 || len(all) != 3 {
		t.Fatalf("unfiltered = %d rows, total %d, err %v", len(all), total, err)
	}
}

func TestRepositoryRecordsDeliveryReadStateAndDeletion(t *testing.T) {
	repo, db := openRepository(t)
	ctx := context.Background()
	createOwner(t, db, 1)
	mailbox := createMailbox(t, repo, "hello@mail.example")
	attemptedAt := time.Now().UTC()
	message := &domain.MailMessage{
		MailboxID: mailbox.ID, Direction: domain.MailDirectionOutbound, Status: domain.MailStatusQueued,
		FromAddress: "hello@mail.example", To: []string{"bob@example.com"}, Subject: "Out",
		SendAttemptedAt: &attemptedAt,
	}
	if _, err := repo.CreateMessage(ctx, message); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	if err := repo.UpdateMessageDelivery(ctx, message.ID, domain.MailStatusFailed, "", "provider.http_500"); err != nil {
		t.Fatalf("UpdateMessageDelivery: %v", err)
	}
	readAt := time.Now().UTC().Truncate(time.Microsecond)
	reader := as(1).MembershipID
	// Outbound mail is always read and stores no read row.
	if err := repo.SetMessageRead(ctx, mailbox.ID, message.ID, reader, false, readAt); err != nil {
		t.Fatalf("SetMessageRead: %v", err)
	}
	stored, err := repo.FindMessage(ctx, mailbox.ID, message.ID)
	if err != nil || stored.Status != domain.MailStatusFailed || stored.FailureCode != "provider.http_500" || !stored.Read {
		t.Fatalf("stored = %+v, err %v", stored, err)
	}
	if read, _ := repo.ReadMessages(ctx, reader, []uint{message.ID}); read[message.ID] {
		t.Fatal("an outbound message stored a read row")
	}

	count, err := repo.CountOutboundSince(ctx, 1, time.Now().Add(-time.Hour))
	if err != nil || count != 1 {
		t.Fatalf("CountOutboundSince = %d, %v", count, err)
	}
	if count, _ := repo.CountOutboundSince(ctx, 1, time.Now().Add(time.Hour)); count != 0 {
		t.Fatalf("CountOutboundSince in the future = %d", count)
	}

	if err := repo.DeleteMessage(ctx, mailbox.ID, message.ID); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	if err := repo.DeleteMessage(ctx, mailbox.ID, message.ID); !errors.Is(err, ErrMailMessageNotFound) {
		t.Fatalf("second delete error = %v", err)
	}
	if err := repo.SetMessageRead(ctx, mailbox.ID, message.ID, reader, true, readAt); !errors.Is(err, ErrMailMessageNotFound) {
		t.Fatalf("read on a deleted message error = %v", err)
	}
}

func TestRepositoryDeletesMessagesWithTheirMailbox(t *testing.T) {
	repo, db := openRepository(t)
	ctx := context.Background()
	createOwner(t, db, 1)
	mailbox := createMailbox(t, repo, "hello@mail.example")
	if _, err := repo.CreateMessage(ctx, &domain.MailMessage{
		MailboxID: mailbox.ID, Direction: domain.MailDirectionInbound, Status: domain.MailStatusReceived,
		FromAddress: "alice@sender.test", Subject: "Hi",
	}); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	if err := repo.DeleteMailbox(ctx, 1, mailbox.ID); err != nil {
		t.Fatalf("DeleteMailbox: %v", err)
	}
	var remaining int64
	if err := db.Model(&MailMessagePO{}).Where("mailbox_id = ?", mailbox.ID).Count(&remaining).Error; err != nil || remaining != 0 {
		t.Fatalf("messages left after mailbox deletion = %d, err %v", remaining, err)
	}
}

func TestRepositoryStoresOneOutboundMessagePerIdempotencyKey(t *testing.T) {
	repo, db := openRepository(t)
	ctx := context.Background()
	createOwner(t, db, 1)
	createOwner(t, db, 2)
	mailbox := createMailbox(t, repo, "hello@mail.example")
	foreign := &domain.Mailbox{OrganizationID: 2, Address: "other@mail.example"}
	if err := repo.CreateMailbox(ctx, foreign, domain.MaxMailboxesPerOrganization); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	outbound := func(mailboxID uint, key string) *domain.MailMessage {
		return &domain.MailMessage{
			MailboxID: mailboxID, Direction: domain.MailDirectionOutbound, Status: domain.MailStatusQueued,
			FromAddress: "hello@mail.example", To: []string{"bob@example.com"}, Subject: "Out",
			IdempotencyKey: key, IdempotencyFingerprint: "fp-" + key,
		}
	}

	first := outbound(mailbox.ID, "k1")
	created, err := repo.CreateMessage(ctx, first)
	if err != nil || !created {
		t.Fatalf("first = created %v, err %v", created, err)
	}
	duplicate := outbound(mailbox.ID, "k1")
	duplicate.Subject = "a different request"
	created, err = repo.CreateMessage(ctx, duplicate)
	if err != nil || created || duplicate.ID != first.ID || duplicate.Subject != "Out" {
		t.Fatalf("duplicate = created %v, id %d, subject %q, err %v; want the stored message", created, duplicate.ID, duplicate.Subject, err)
	}
	if created, err = repo.CreateMessage(ctx, outbound(foreign.ID, "k1")); err != nil || !created {
		t.Fatalf("same key in another mailbox = created %v, err %v", created, err)
	}
	for i := 0; i < 2; i++ {
		if created, err = repo.CreateMessage(ctx, outbound(mailbox.ID, "")); err != nil || !created {
			t.Fatalf("message without a key %d = created %v, err %v", i, created, err)
		}
	}

	found, err := repo.FindMessageByIdempotencyKey(ctx, mailbox.ID, "k1")
	if err != nil || found.ID != first.ID || found.IdempotencyFingerprint != "fp-k1" {
		t.Fatalf("FindMessageByIdempotencyKey = %+v, %v", found, err)
	}
	if _, err = repo.FindMessageByIdempotencyKey(ctx, mailbox.ID, "missing"); !errors.Is(err, ErrMailMessageNotFound) {
		t.Fatalf("missing key error = %v", err)
	}

	if owned, findErr := repo.FindOutboundMessageForOrganization(ctx, 1, first.ID); findErr != nil || owned.ID != first.ID {
		t.Fatalf("FindOutboundMessageForOrganization = %+v, %v", owned, findErr)
	}
	if _, err = repo.FindOutboundMessageForOrganization(ctx, 2, first.ID); !errors.Is(err, ErrMailMessageNotFound) {
		t.Fatalf("another user's lookup error = %v", err)
	}
	inbound := &domain.MailMessage{
		MailboxID: mailbox.ID, Direction: domain.MailDirectionInbound, Status: domain.MailStatusReceived,
		FromAddress: "alice@sender.test", Subject: "In",
	}
	if _, err = repo.CreateMessage(ctx, inbound); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	if _, err = repo.FindOutboundMessageForOrganization(ctx, 1, inbound.ID); !errors.Is(err, ErrMailMessageNotFound) {
		t.Fatalf("inbound message through the outbound lookup error = %v", err)
	}
}

func TestRepositoryCountsUnreadInboundMessagesPerMailbox(t *testing.T) {
	repo, db := openRepository(t)
	ctx := context.Background()
	createOwner(t, db, 1)
	busy := createMailbox(t, repo, "busy@mail.example")
	quiet := createMailbox(t, repo, "quiet@mail.example")
	readAt := time.Now().UTC()
	reader, other := as(1).MembershipID, viewerOf(1, 2, domain.OrganizationRoleMember).MembershipID
	add := func(mailboxID uint, direction domain.MailDirection, read bool) {
		message := &domain.MailMessage{
			MailboxID: mailboxID, Direction: direction, Status: domain.MailStatusReceived,
			FromAddress: "alice@sender.test", Subject: "Hi",
		}
		if direction == domain.MailDirectionOutbound {
			message.Status = domain.MailStatusSent
		}
		if _, err := repo.CreateMessage(ctx, message); err != nil {
			t.Fatalf("CreateMessage: %v", err)
		}
		if read {
			if err := repo.SetMessageRead(ctx, mailboxID, message.ID, reader, true, readAt); err != nil {
				t.Fatalf("SetMessageRead: %v", err)
			}
		}
	}
	add(busy.ID, domain.MailDirectionInbound, false)
	add(busy.ID, domain.MailDirectionInbound, false)
	add(busy.ID, domain.MailDirectionInbound, true)
	// An outbound message is never unread mail, even without a read time.
	add(busy.ID, domain.MailDirectionOutbound, false)

	owned, err := repo.ListMailboxes(ctx, 1, reader)
	if err != nil || len(owned) != 2 {
		t.Fatalf("ListMailboxes = %v, %v", owned, err)
	}
	if owned[0].ID != busy.ID || owned[0].UnreadCount != 2 {
		t.Fatalf("busy mailbox unread = %d; want 2", owned[0].UnreadCount)
	}
	// Read state is the reader's own: someone who read nothing has everything unread.
	if others, _ := repo.ListMailboxes(ctx, 1, other); others[0].UnreadCount != 3 {
		t.Fatalf("busy mailbox unread for another reader = %d; want 3", others[0].UnreadCount)
	}
	if owned[1].ID != quiet.ID || owned[1].UnreadCount != 0 {
		t.Fatalf("quiet mailbox unread = %d; want 0", owned[1].UnreadCount)
	}
}

func TestRepositoryListsTheLogAcrossAnOrganizationsMailboxes(t *testing.T) {
	repo, db := openRepository(t)
	ctx := context.Background()
	createOwner(t, db, 1)
	createOwner(t, db, 2)
	first := createMailbox(t, repo, "first@mail.example")
	second := createMailbox(t, repo, "second@mail.example")
	foreign := &domain.Mailbox{OrganizationID: 2, Address: "foreign@mail.example"}
	if err := repo.CreateMailbox(ctx, foreign, domain.MaxMailboxesPerOrganization); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	add := func(mailboxID uint, direction domain.MailDirection, status domain.MailStatus, from, to, subject string) {
		message := &domain.MailMessage{
			MailboxID: mailboxID, Direction: direction, Status: status,
			FromAddress: from, To: []string{to}, Subject: subject, TextBody: "secret body",
		}
		if _, err := repo.CreateMessage(ctx, message); err != nil {
			t.Fatalf("CreateMessage: %v", err)
		}
	}
	add(first.ID, domain.MailDirectionInbound, domain.MailStatusReceived, "alice@sender.test", "first@mail.example", "Invoice 100% due")
	add(first.ID, domain.MailDirectionOutbound, domain.MailStatusSent, "first@mail.example", "bob@example.com", "Receipt")
	add(second.ID, domain.MailDirectionOutbound, domain.MailStatusFailed, "second@mail.example", "carol@example.com", "Reminder")
	add(foreign.ID, domain.MailDirectionInbound, domain.MailStatusReceived, "alice@sender.test", "foreign@mail.example", "Invoice for someone else")

	list := func(filter domain.MailLogFilter) []*domain.MailMessage {
		t.Helper()
		messages, total, err := repo.ListMessagesForOrganization(ctx, 1, []uint{first.ID, second.ID, foreign.ID}, filter)
		if err != nil || int(total) != len(messages) {
			t.Fatalf("ListMessagesForOrganization(%+v) = %d rows, total %d, err %v", filter, len(messages), total, err)
		}
		return messages
	}

	all := list(domain.MailLogFilter{})
	if len(all) != 3 || all[0].Subject != "Reminder" || all[0].MailboxAddress != "second@mail.example" {
		t.Fatalf("log = %d rows, first %+v; want 3 newest first with the mailbox address", len(all), all[0])
	}
	if all[0].TextBody != "" {
		t.Fatalf("the log returned a body")
	}
	if got := list(domain.MailLogFilter{Direction: domain.MailDirectionOutbound}); len(got) != 2 {
		t.Fatalf("outbound = %d; want 2", len(got))
	}
	if got := list(domain.MailLogFilter{Status: domain.MailStatusFailed}); len(got) != 1 || got[0].Subject != "Reminder" {
		t.Fatalf("failed = %+v", got)
	}
	if got := list(domain.MailLogFilter{MailboxID: first.ID}); len(got) != 2 {
		t.Fatalf("first mailbox = %d; want 2", len(got))
	}
	// Another user's mailbox id filters to nothing instead of leaking their mail.
	if got := list(domain.MailLogFilter{MailboxID: foreign.ID}); len(got) != 0 {
		t.Fatalf("foreign mailbox filter returned %d rows", len(got))
	}
	for query, want := range map[string]string{"INVOICE": "Invoice 100% due", "bob@example": "Receipt", "ALICE@sender": "Invoice 100% due"} {
		if got := list(domain.MailLogFilter{Query: query}); len(got) != 1 || got[0].Subject != want {
			t.Fatalf("query %q = %+v; want %q", query, got, want)
		}
	}
	// LIKE wildcards in the query match literally.
	if got := list(domain.MailLogFilter{Query: "100%"}); len(got) != 1 {
		t.Fatalf("literal %% query = %d rows; want 1", len(got))
	}
	if got := list(domain.MailLogFilter{Query: "%"}); len(got) != 1 {
		t.Fatalf("a lone %% matched %d rows; want only the subject that contains it", len(got))
	}
	if got := list(domain.MailLogFilter{Query: "_"}); len(got) != 0 {
		t.Fatalf("a lone _ matched %d rows; want 0", len(got))
	}

	page, total, err := repo.ListMessagesForOrganization(ctx, 1, []uint{first.ID, second.ID}, domain.MailLogFilter{Page: 2, PerPage: 2})
	if err != nil || total != 3 || len(page) != 1 || page[0].Subject != "Invoice 100% due" {
		t.Fatalf("page 2 = %+v, total %d, err %v", page, total, err)
	}

	// Only the given mailboxes are listed and counted; none lists nothing.
	readable, total, err := repo.ListMessagesForOrganization(ctx, 1, []uint{second.ID}, domain.MailLogFilter{Query: "Invoice"})
	if err != nil || total != 0 || len(readable) != 0 {
		t.Fatalf("log of an unreadable mailbox's subject = %+v, total %d, err %v", readable, total, err)
	}
	none, total, err := repo.ListMessagesForOrganization(ctx, 1, nil, domain.MailLogFilter{})
	if err != nil || total != 0 || len(none) != 0 {
		t.Fatalf("log without mailboxes = %+v, total %d, err %v", none, total, err)
	}
}

func TestRepositoryCountsMailPerDay(t *testing.T) {
	repo, db := openRepository(t)
	ctx := context.Background()
	createOwner(t, db, 1)
	createOwner(t, db, 2)
	mailbox := createMailbox(t, repo, "hello@mail.example")
	foreign := &domain.Mailbox{OrganizationID: 2, Address: "foreign@mail.example"}
	if err := repo.CreateMailbox(ctx, foreign, domain.MaxMailboxesPerOrganization); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	add := func(mailboxID uint, direction domain.MailDirection, status domain.MailStatus, at time.Time) {
		message := &domain.MailMessage{MailboxID: mailboxID, Direction: direction, Status: status, FromAddress: "a@sender.test", Subject: "s"}
		if _, err := repo.CreateMessage(ctx, message); err != nil {
			t.Fatalf("CreateMessage: %v", err)
		}
		if err := db.Model(&MailMessagePO{}).Where("id = ?", message.ID).UpdateColumn("created_at", at).Error; err != nil {
			t.Fatalf("set created_at: %v", err)
		}
	}
	day1 := time.Date(2026, 11, 14, 23, 30, 0, 0, time.UTC)
	day2 := time.Date(2026, 11, 15, 0, 30, 0, 0, time.UTC)
	add(mailbox.ID, domain.MailDirectionOutbound, domain.MailStatusSent, day1)
	add(mailbox.ID, domain.MailDirectionOutbound, domain.MailStatusSent, day2)
	add(mailbox.ID, domain.MailDirectionOutbound, domain.MailStatusFailed, day2)
	add(mailbox.ID, domain.MailDirectionOutbound, domain.MailStatusQueued, day2)
	add(mailbox.ID, domain.MailDirectionInbound, domain.MailStatusReceived, day2)
	add(mailbox.ID, domain.MailDirectionInbound, domain.MailStatusReceived, time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC))
	add(foreign.ID, domain.MailDirectionInbound, domain.MailStatusReceived, day2)

	if empty, err := repo.StatsForOrganization(ctx, 1, nil, time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)); err != nil || len(empty) != 0 {
		t.Fatalf("stats without mailboxes = %+v, %v", empty, err)
	}
	stats, err := repo.StatsForOrganization(ctx, 1, []uint{mailbox.ID, foreign.ID}, time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC))
	if err != nil || len(stats) != 2 {
		t.Fatalf("StatsForOrganization = %+v, %v; want 2 days", stats, err)
	}
	if !stats[0].Day.Equal(time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)) || stats[0].Sent != 1 || stats[0].Failed != 0 || stats[0].Received != 0 {
		t.Fatalf("first day = %+v", stats[0])
	}
	if !stats[1].Day.Equal(time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)) || stats[1].Sent != 1 || stats[1].Failed != 1 || stats[1].Received != 1 {
		t.Fatalf("second day = %+v; a queued message counts as neither sent nor failed", stats[1])
	}
}

func TestRepositoryStoresDeliveryEventsOnceAndSummarizesThem(t *testing.T) {
	repo, db := openRepository(t)
	ctx := context.Background()
	createOwner(t, db, 1)
	mailbox := createMailbox(t, repo, "hello@mail.example")
	message := &domain.MailMessage{
		MailboxID: mailbox.ID, Direction: domain.MailDirectionOutbound, Status: domain.MailStatusSent,
		FromAddress: "hello@mail.example", To: []string{"bob@example.com", "amy@example.org"},
		Subject: "Out", MessageID: "prov-9@mail.example",
	}
	if _, err := repo.CreateMessage(ctx, message); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	at := func(second int) time.Time { return time.Date(2027, 1, 1, 0, 0, second, 0, time.UTC) }

	added, summary, err := repo.AddMessageEvents(ctx, message.ID, []domain.MailEvent{
		{Type: domain.MailEventAccepted, OccurredAt: at(0)},
		{Type: domain.MailEventDeferred, Recipient: "bob@example.com", Detail: "4.2.2", ProviderEventID: "evt-a", OccurredAt: at(1)},
	})
	if err != nil || added != 2 || summary != domain.MailEventDeferred {
		t.Fatalf("first batch = added %d, summary %q, err %v", added, summary, err)
	}
	// A provider redelivery of evt-a is skipped; the new event still lands.
	added, summary, err = repo.AddMessageEvents(ctx, message.ID, []domain.MailEvent{
		{Type: domain.MailEventDeferred, Recipient: "bob@example.com", Detail: "4.2.2", ProviderEventID: "evt-a", OccurredAt: at(1)},
		{Type: domain.MailEventDelivered, Recipient: "bob@example.com", Detail: "2.0.0", ProviderEventID: "evt-b", OccurredAt: at(5)},
	})
	if err != nil || added != 1 || summary != domain.MailEventDelivered {
		t.Fatalf("second batch = added %d, summary %q, err %v; want 1 new event and a delivered summary", added, summary, err)
	}
	added, summary, err = repo.AddMessageEvents(ctx, message.ID, []domain.MailEvent{
		{Type: domain.MailEventBounced, Recipient: "amy@example.org", Detail: "5.1.1 permanent_failure", ProviderEventID: "evt-c", OccurredAt: at(6)},
	})
	if err != nil || added != 1 || summary != domain.MailEventBounced {
		t.Fatalf("third batch = added %d, summary %q, err %v", added, summary, err)
	}

	events, err := repo.ListMessageEvents(ctx, message.ID)
	if err != nil || len(events) != 4 {
		t.Fatalf("ListMessageEvents = %d events, err %v; want 4", len(events), err)
	}
	if events[0].Type != domain.MailEventAccepted || events[3].Type != domain.MailEventBounced || events[3].Detail != "5.1.1 permanent_failure" {
		t.Fatalf("events are not oldest first: %+v", events)
	}
	stored, err := repo.FindMessage(ctx, mailbox.ID, message.ID)
	if err != nil || stored.LastEvent != domain.MailEventBounced {
		t.Fatalf("stored summary = %q, err %v", stored.LastEvent, err)
	}
	listed, _, err := repo.ListMessagesForOrganization(ctx, 1, []uint{mailbox.ID}, domain.MailLogFilter{})
	if err != nil || len(listed) != 1 || listed[0].LastEvent != domain.MailEventBounced {
		t.Fatalf("the log does not carry the summary: %+v, %v", listed, err)
	}

	found, foundMailbox, err := repo.FindOutboundMessageByProviderID(ctx, "prov-9@mail.example")
	if err != nil || found.ID != message.ID || found.MailboxAddress != mailbox.Address || found.OrganizationID != 1 {
		t.Fatalf("FindOutboundMessageByProviderID = %+v, %v", found, err)
	}
	if foundMailbox.ID != mailbox.ID || foundMailbox.OrganizationID != 1 || foundMailbox.Type != domain.MailboxTypeShared ||
		foundMailbox.WebhooksEnabled || foundMailbox.SealedBefore != nil {
		t.Fatalf("provider-id lookup mailbox = %+v", foundMailbox)
	}
	for _, missing := range []string{"", "unknown@mail.example"} {
		if _, _, err := repo.FindOutboundMessageByProviderID(ctx, missing); !errors.Is(err, ErrMailMessageNotFound) {
			t.Fatalf("provider id %q error = %v", missing, err)
		}
	}
	// An inbound message that happens to carry the same Message-ID is not an outbound send.
	inbound := &domain.MailMessage{
		MailboxID: mailbox.ID, Direction: domain.MailDirectionInbound, Status: domain.MailStatusReceived,
		FromAddress: "a@sender.test", Subject: "In", MessageID: "only-inbound@sender.test",
	}
	if _, err := repo.CreateMessage(ctx, inbound); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	if _, _, err := repo.FindOutboundMessageByProviderID(ctx, "only-inbound@sender.test"); !errors.Is(err, ErrMailMessageNotFound) {
		t.Fatalf("an inbound message matched a provider event: %v", err)
	}
	if _, _, err := repo.AddMessageEvents(ctx, 999999, []domain.MailEvent{{Type: domain.MailEventAccepted, OccurredAt: at(0)}}); !errors.Is(err, ErrMailMessageNotFound) {
		t.Fatalf("events for a missing message error = %v", err)
	}

	if err := repo.DeleteMessage(ctx, mailbox.ID, message.ID); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
}

func TestRepositoryReportsADisabledDatabase(t *testing.T) {
	repo := NewRepository(nil)
	if _, err := repo.ListMailboxes(context.Background(), 1, 0); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("error = %v; want ErrServiceUnavailable", err)
	}
}

func TestRepositoryTransactionCommitsOrRollsBackTogether(t *testing.T) {
	repo, db := openRepository(t)
	ctx := context.Background()
	createOwner(t, db, 1)
	mailbox := createMailbox(t, repo, "tx@mail.example")
	message := &domain.MailMessage{
		MailboxID: mailbox.ID, Direction: domain.MailDirectionOutbound, Status: domain.MailStatusQueued,
		FromAddress: mailbox.Address, To: []string{"bob@example.com"}, Subject: "Tx",
	}
	if _, err := repo.CreateMessage(ctx, message); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	change := func(tx context.Context) error {
		if err := repo.UpdateMessageDelivery(tx, message.ID, domain.MailStatusSent, "prov-tx@mail.example", ""); err != nil {
			return err
		}
		_, _, err := repo.AddMessageEvents(tx, message.ID, []domain.MailEvent{{Type: domain.MailEventAccepted, OccurredAt: time.Now().UTC()}})
		return err
	}

	failure := errors.New("publication failed")
	err := repo.InTransaction(ctx, func(tx context.Context) error {
		if err := change(tx); err != nil {
			return err
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("InTransaction error = %v; want the callback's error", err)
	}
	stored, err := repo.FindMessage(ctx, mailbox.ID, message.ID)
	if err != nil || stored.Status != domain.MailStatusQueued || stored.LastEvent != "" {
		t.Fatalf("after rollback message = %+v, %v; want it unchanged", stored, err)
	}
	if events, _ := repo.ListMessageEvents(ctx, message.ID); len(events) != 0 {
		t.Fatalf("after rollback events = %+v; want none", events)
	}

	// A nested call joins the outer transaction instead of opening its own.
	err = repo.InTransaction(ctx, func(tx context.Context) error {
		return repo.InTransaction(tx, change)
	})
	if err != nil {
		t.Fatalf("InTransaction: %v", err)
	}
	stored, err = repo.FindMessage(ctx, mailbox.ID, message.ID)
	if err != nil || stored.Status != domain.MailStatusSent || stored.LastEvent != domain.MailEventAccepted {
		t.Fatalf("after commit message = %+v, %v", stored, err)
	}
}

func TestRepositoryStoresAttachmentsWithTheirMessage(t *testing.T) {
	repo, db := openRepository(t)
	ctx := context.Background()
	createOwner(t, db, 1)
	mailbox := createMailbox(t, repo, "files@mail.example")
	message := &domain.MailMessage{
		MailboxID: mailbox.ID, Direction: domain.MailDirectionOutbound, Status: domain.MailStatusQueued,
		FromAddress: mailbox.Address, To: []string{"bob@example.com"}, Subject: "Files",
		Attachments: []domain.MailAttachment{
			{Filename: "a.pdf", ContentType: "application/pdf", Content: []byte("%PDF-1.4")},
			{Filename: "b.bin", ContentType: "application/octet-stream", Content: []byte{0, 1, 2, 0xff}},
		},
	}
	if created, err := repo.CreateMessage(ctx, message); err != nil || !created {
		t.Fatalf("CreateMessage = %v, %v", created, err)
	}
	if message.AttachmentCount != 2 || message.Attachments[0].ID == 0 {
		t.Fatalf("message = %+v", message)
	}

	stored, err := repo.FindMessage(ctx, mailbox.ID, message.ID)
	if err != nil || stored.AttachmentCount != 2 {
		t.Fatalf("FindMessage = %+v, %v", stored, err)
	}
	listed, _, err := repo.ListMessagesForOrganization(ctx, 1, []uint{mailbox.ID}, domain.MailLogFilter{})
	if err != nil || len(listed) != 1 || listed[0].AttachmentCount != 2 {
		t.Fatalf("log = %+v, %v", listed, err)
	}
	attachments, err := repo.ListMessageAttachments(ctx, message.ID)
	if err != nil || len(attachments) != 2 || attachments[0].Filename != "a.pdf" || attachments[0].Size != 8 ||
		attachments[0].Content != nil {
		t.Fatalf("ListMessageAttachments = %+v, %v; want metadata without content", attachments, err)
	}
	file, err := repo.FindMessageAttachment(ctx, message.ID, attachments[1].ID)
	if err != nil || string(file.Content) != string([]byte{0, 1, 2, 0xff}) {
		t.Fatalf("FindMessageAttachment = %+v, %v", file, err)
	}
	if _, err := repo.FindMessageAttachment(ctx, message.ID+1, attachments[1].ID); !errors.Is(err, ErrMailMessageNotFound) {
		t.Fatalf("attachment under another message error = %v", err)
	}

	// A repeated inbound delivery stores neither the message nor its files again.
	inbound := func() *domain.MailMessage {
		return &domain.MailMessage{
			MailboxID: mailbox.ID, Direction: domain.MailDirectionInbound, Status: domain.MailStatusReceived,
			FromAddress: "a@sender.test", Subject: "In", DedupeKey: "dedupe-files",
			Attachments: []domain.MailAttachment{{Filename: "in.txt", ContentType: "text/plain", Content: []byte("in")}},
		}
	}
	first := inbound()
	if created, err := repo.CreateInboundMessage(ctx, first); err != nil || !created {
		t.Fatalf("CreateInboundMessage = %v, %v", created, err)
	}
	if created, err := repo.CreateInboundMessage(ctx, inbound()); err != nil || created {
		t.Fatalf("repeated CreateInboundMessage = %v, %v", created, err)
	}
	var count int64
	if err := db.Model(&MailAttachmentPO{}).Where("message_id = ?", first.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("inbound attachments = %d, %v; want 1", count, err)
	}

	if err := repo.DeleteMessage(ctx, mailbox.ID, message.ID); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	if err := db.Model(&MailAttachmentPO{}).Where("message_id = ?", message.ID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("attachments after deleting the message = %d, %v; want 0", count, err)
	}
}

func TestRepositoryKeepsTemplatesPerOrganization(t *testing.T) {
	repo, db := openRepository(t)
	ctx := context.Background()
	createOwner(t, db, 1)
	createOwner(t, db, 2)

	template := &domain.MailTemplate{
		OrganizationID: 1, Name: "Welcome", Subject: "Hi {{name}}", HTMLBody: "<p>{{name}}</p>",
		SampleVariables: map[string]string{"name": "Ann"}, CreatedBy: 9,
		Design: `{"version":1,"blocks":[]}`,
	}
	if err := repo.CreateTemplate(ctx, template, 2); err != nil || template.ID == 0 {
		t.Fatalf("CreateTemplate = %v, id %d", err, template.ID)
	}
	if err := repo.CreateTemplate(ctx, &domain.MailTemplate{OrganizationID: 1, Name: "Welcome", Subject: "x", TextBody: "x"}, 2); !errors.Is(err, ErrMailTemplateNameTaken) {
		t.Fatalf("duplicate name error = %v", err)
	}
	if err := repo.CreateTemplate(ctx, &domain.MailTemplate{OrganizationID: 2, Name: "Welcome", Subject: "x", TextBody: "x"}, 2); err != nil {
		t.Fatalf("same name in another organization: %v", err)
	}
	if err := repo.CreateTemplate(ctx, &domain.MailTemplate{OrganizationID: 1, Name: "Second", Subject: "x", TextBody: "x"}, 2); err != nil {
		t.Fatalf("second template: %v", err)
	}
	if err := repo.CreateTemplate(ctx, &domain.MailTemplate{OrganizationID: 1, Name: "Third", Subject: "x", TextBody: "x"}, 2); !errors.Is(err, ErrMailTemplateLimitReached) {
		t.Fatalf("limit error = %v", err)
	}

	found, err := repo.FindTemplate(ctx, 1, template.ID)
	if err != nil || found.SampleVariables["name"] != "Ann" || found.CreatedBy != 9 || !sameJSON(found.Design, `{"version":1,"blocks":[]}`) {
		t.Fatalf("FindTemplate = %+v, %v", found, err)
	}
	if _, err = repo.FindTemplate(ctx, 2, template.ID); !errors.Is(err, ErrMailTemplateNotFound) {
		t.Fatalf("another organization's template error = %v", err)
	}
	listed, err := repo.ListTemplates(ctx, 1)
	if err != nil || len(listed) != 2 || listed[0].Name != "Second" {
		t.Fatalf("ListTemplates = %+v, %v; want two, by name", listed, err)
	}

	found.Name, found.Subject = "Second", "changed"
	if err := repo.UpdateTemplate(ctx, found); !errors.Is(err, ErrMailTemplateNameTaken) {
		t.Fatalf("rename onto another template error = %v", err)
	}
	found.Name, found.Design = "Welcome back", ""
	if err := repo.UpdateTemplate(ctx, found); err != nil || found.Subject != "changed" || found.CreatedBy != 9 {
		t.Fatalf("UpdateTemplate = %+v, %v", found, err)
	}
	// Switching to HTML clears the design.
	if reloaded, err := repo.FindTemplate(ctx, 1, template.ID); err != nil || reloaded.Design != "" {
		t.Fatalf("design after switching to HTML = %q, %v", reloaded.Design, err)
	}
	found.OrganizationID = 2
	if err := repo.UpdateTemplate(ctx, found); !errors.Is(err, ErrMailTemplateNotFound) {
		t.Fatalf("update from another organization error = %v", err)
	}
	if err := repo.DeleteTemplate(ctx, 2, template.ID); !errors.Is(err, ErrMailTemplateNotFound) {
		t.Fatalf("delete from another organization error = %v", err)
	}
	if err := repo.DeleteTemplate(ctx, 1, template.ID); err != nil {
		t.Fatalf("DeleteTemplate: %v", err)
	}
}

func TestRepositoryManagesContactsAndLists(t *testing.T) {
	repo, db := openRepository(t)
	ctx := context.Background()
	createOwner(t, db, 1)
	createOwner(t, db, 2)

	vip := &domain.MailContactList{OrganizationID: 1, Name: "VIP"}
	news := &domain.MailContactList{OrganizationID: 1, Name: "News"}
	for _, list := range []*domain.MailContactList{vip, news} {
		if err := repo.CreateContactList(ctx, list, 2); err != nil {
			t.Fatalf("CreateContactList: %v", err)
		}
	}
	if err := repo.CreateContactList(ctx, &domain.MailContactList{OrganizationID: 1, Name: "Third"}, 2); !errors.Is(err, ErrMailContactListLimit) {
		t.Fatalf("list limit error = %v", err)
	}
	foreign := &domain.MailContactList{OrganizationID: 2, Name: "VIP"}
	if err := repo.CreateContactList(ctx, foreign, 2); err != nil {
		t.Fatalf("same list name in another organization: %v", err)
	}

	ann := &domain.MailContact{
		OrganizationID: 1, Email: "ann@example.com", FirstName: "Ann", Properties: map[string]string{"plan": "pro"},
		Status: domain.MailContactSubscribed, ListIDs: []uint{vip.ID, news.ID},
	}
	if err := repo.CreateContact(ctx, ann, 2); err != nil || ann.ID == 0 || len(ann.ListIDs) != 2 {
		t.Fatalf("CreateContact = %+v, %v", ann, err)
	}
	if err := repo.CreateContact(ctx, &domain.MailContact{OrganizationID: 1, Email: "ann@example.com", Status: "subscribed"}, 2); !errors.Is(err, ErrMailContactExists) {
		t.Fatalf("duplicate error = %v", err)
	}
	if err := repo.CreateContact(ctx, &domain.MailContact{OrganizationID: 1, Email: "x@example.com", Status: "subscribed", ListIDs: []uint{foreign.ID}}, 5); !errors.Is(err, ErrMailContactListNotFound) {
		t.Fatalf("another organization's list error = %v", err)
	}
	bob := &domain.MailContact{OrganizationID: 1, Email: "bob@example.com", LastName: "Smith", Status: domain.MailContactUnsubscribed, StatusReason: "manual"}
	if err := repo.CreateContact(ctx, bob, 2); err != nil {
		t.Fatalf("CreateContact bob: %v", err)
	}
	if err := repo.CreateContact(ctx, &domain.MailContact{OrganizationID: 1, Email: "c@example.com", Status: "subscribed"}, 2); !errors.Is(err, ErrMailContactLimitReached) {
		t.Fatalf("contact limit error = %v", err)
	}

	page := func(filter domain.MailContactFilter) []string {
		filter.Page, filter.PerPage = 1, 10
		contacts, total, err := repo.ListContacts(ctx, 1, filter)
		if err != nil || int(total) != len(contacts) {
			t.Fatalf("ListContacts(%+v) = %d of %d, %v", filter, len(contacts), total, err)
		}
		emails := make([]string, 0, len(contacts))
		for _, contact := range contacts {
			emails = append(emails, contact.Email)
		}
		return emails
	}
	if got := page(domain.MailContactFilter{}); len(got) != 2 || got[0] != "bob@example.com" {
		t.Fatalf("all contacts = %v; want newest first", got)
	}
	if got := page(domain.MailContactFilter{ListID: vip.ID}); len(got) != 1 || got[0] != "ann@example.com" {
		t.Fatalf("VIP contacts = %v", got)
	}
	if got := page(domain.MailContactFilter{Query: "SMI"}); len(got) != 1 || got[0] != "bob@example.com" {
		t.Fatalf("search = %v", got)
	}
	if got := page(domain.MailContactFilter{Query: "%"}); len(got) != 0 {
		t.Fatalf("a literal percent sign matched %v", got)
	}
	if got := page(domain.MailContactFilter{Status: domain.MailContactUnsubscribed}); len(got) != 1 || got[0] != "bob@example.com" {
		t.Fatalf("unsubscribed = %v", got)
	}

	ann.FirstName, ann.ListIDs, ann.Properties = "Annie", []uint{news.ID}, map[string]string{"city": "Oslo"}
	if err := repo.UpdateContact(ctx, ann); err != nil || ann.FirstName != "Annie" || len(ann.ListIDs) != 1 || ann.Properties["plan"] != "" {
		t.Fatalf("UpdateContact = %+v, %v", ann, err)
	}
	ann.OrganizationID = 2
	if err := repo.UpdateContact(ctx, ann); !errors.Is(err, ErrMailContactNotFound) {
		t.Fatalf("update from another organization error = %v", err)
	}
	ann.OrganizationID = 1

	lists, err := repo.ListContactLists(ctx, 1)
	if err != nil || len(lists) != 2 || lists[0].Name != "News" || lists[0].ContactCount != 1 || lists[1].ContactCount != 0 {
		t.Fatalf("ListContactLists = %+v, %v", lists, err)
	}
	if _, err = repo.RenameContactList(ctx, 1, vip.ID, "News"); !errors.Is(err, ErrMailContactListNameTaken) {
		t.Fatalf("rename onto another list error = %v", err)
	}
	renamed, err := repo.RenameContactList(ctx, 1, vip.ID, "Gold")
	if err != nil || renamed.Name != "Gold" {
		t.Fatalf("RenameContactList = %+v, %v", renamed, err)
	}
	if err = repo.DeleteContactList(ctx, 2, news.ID); !errors.Is(err, ErrMailContactListNotFound) {
		t.Fatalf("delete from another organization error = %v", err)
	}
	if err = repo.DeleteContactList(ctx, 1, news.ID); err != nil {
		t.Fatalf("DeleteContactList: %v", err)
	}
	stored, err := repo.FindContact(ctx, 1, ann.ID)
	if err != nil || len(stored.ListIDs) != 0 {
		t.Fatalf("contact after deleting its list = %+v, %v; want kept without the list", stored, err)
	}
	if err := repo.DeleteContact(ctx, 1, bob.ID); err != nil {
		t.Fatalf("DeleteContact: %v", err)
	}
	if _, err := repo.FindContact(ctx, 1, bob.ID); !errors.Is(err, ErrMailContactNotFound) {
		t.Fatalf("deleted contact error = %v", err)
	}
}

func TestRepositoryImportsContacts(t *testing.T) {
	repo, db := openRepository(t)
	ctx := context.Background()
	createOwner(t, db, 1)
	list := &domain.MailContactList{OrganizationID: 1, Name: "Imported"}
	if err := repo.CreateContactList(ctx, list, 10); err != nil {
		t.Fatalf("CreateContactList: %v", err)
	}
	existing := &domain.MailContact{
		OrganizationID: 1, Email: "ann@example.com", FirstName: "Ann", LastName: "Lee",
		Properties: map[string]string{"plan": "free", "city": "Oslo"},
		Status:     domain.MailContactUnsubscribed, StatusReason: "manual",
	}
	if err := repo.CreateContact(ctx, existing, 10); err != nil {
		t.Fatalf("CreateContact: %v", err)
	}

	rows := []domain.MailContactImportRow{
		{Email: "ann@example.com", FirstName: "", LastName: "Smith", Properties: map[string]string{"plan": "pro"}},
		{Email: "bob@example.com", FirstName: "Bob", Properties: map[string]string{"plan": "free"}},
	}
	counts, err := repo.ImportContacts(ctx, 1, rows, list.ID, 10)
	if err != nil || counts.Created != 1 || counts.Updated != 1 {
		t.Fatalf("ImportContacts = %+v, %v", counts, err)
	}
	ann, err := repo.FindContact(ctx, 1, existing.ID)
	if err != nil {
		t.Fatalf("FindContact: %v", err)
	}
	// An empty cell keeps the stored value, properties merge, and the status is untouched.
	if ann.FirstName != "Ann" || ann.LastName != "Smith" || ann.Properties["plan"] != "pro" || ann.Properties["city"] != "Oslo" ||
		ann.Status != domain.MailContactUnsubscribed || len(ann.ListIDs) != 1 {
		t.Fatalf("updated contact = %+v", ann)
	}
	contacts, total, err := repo.ListContacts(ctx, 1, domain.MailContactFilter{ListID: list.ID, Page: 1, PerPage: 10})
	if err != nil || total != 2 || contacts[0].Status != domain.MailContactSubscribed {
		t.Fatalf("imported list = %+v (%d), %v", contacts, total, err)
	}

	// Importing again is idempotent for list membership.
	if _, err = repo.ImportContacts(ctx, 1, rows, list.ID, 10); err != nil {
		t.Fatalf("second import: %v", err)
	}
	lists, err := repo.ListContactLists(ctx, 1)
	if err != nil || lists[0].ContactCount != 2 {
		t.Fatalf("lists after a second import = %+v, %v", lists, err)
	}

	// The limit counts only new addresses, and a refused import stores nothing.
	more := []domain.MailContactImportRow{{Email: "c@example.com"}, {Email: "d@example.com"}}
	if _, err := repo.ImportContacts(ctx, 1, more, 0, 3); !errors.Is(err, ErrMailContactLimitReached) {
		t.Fatalf("limit error = %v", err)
	}
	if _, total, _ := repo.ListContacts(ctx, 1, domain.MailContactFilter{Page: 1, PerPage: 10}); total != 2 {
		t.Fatalf("contacts after a refused import = %d; want 2", total)
	}
	if _, err := repo.ImportContacts(ctx, 1, more, 999, 10); !errors.Is(err, ErrMailContactListNotFound) {
		t.Fatalf("missing list error = %v", err)
	}
}

// sameJSON compares two JSON documents by content; jsonb does not keep key order or spacing.
func sameJSON(a, b string) bool {
	var left, right any
	if json.Unmarshal([]byte(a), &left) != nil || json.Unmarshal([]byte(b), &right) != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
}

// A message deleted between SetMessageRead's lookup and its insert hits the read row's foreign
// key; that answers not found, as the lookup would have.
func TestRepositorySetMessageReadOnAMessageDeletedAtInsert(t *testing.T) {
	repo, db := openRepository(t)
	ctx := context.Background()
	createOwner(t, db, 1)
	// The schema migration's foreign key; AutoMigrate does not create it.
	if err := db.Exec(`ALTER TABLE mail_message_reads ADD CONSTRAINT mail_message_reads_message_fk
		FOREIGN KEY (message_id) REFERENCES mail_messages(id) ON DELETE CASCADE`).Error; err != nil {
		t.Fatalf("add foreign key: %v", err)
	}
	mailbox := createMailbox(t, repo, "hello@mail.example")
	message := &domain.MailMessage{
		MailboxID: mailbox.ID, Direction: domain.MailDirectionInbound, Status: domain.MailStatusReceived,
		FromAddress: "alice@sender.test", To: []string{mailbox.Address}, Subject: "In", DedupeKey: "in",
	}
	if _, err := repo.CreateInboundMessage(ctx, message); err != nil {
		t.Fatalf("CreateInboundMessage: %v", err)
	}
	// Another request deletes the message right before the read row is inserted.
	deleted := false
	if err := db.Callback().Create().Before("gorm:create").Register("test:delete_message_first", func(tx *gorm.DB) {
		if tx.Statement.Table != "mail_message_reads" || deleted {
			return
		}
		deleted = true
		if err := db.Exec(`DELETE FROM mail_messages WHERE id = ?`, message.ID).Error; err != nil {
			t.Errorf("concurrent delete: %v", err)
		}
	}); err != nil {
		t.Fatalf("register callback: %v", err)
	}

	err := repo.SetMessageRead(ctx, mailbox.ID, message.ID, as(1).MembershipID, true, time.Now().UTC())
	if !deleted {
		t.Fatal("the read row was never inserted")
	}
	if !errors.Is(err, ErrMailMessageNotFound) {
		t.Fatalf("SetMessageRead on a message deleted at insert = %v; want ErrMailMessageNotFound", err)
	}
}

// Read state belongs to a membership: the repository refuses a read without one.
func TestRepositorySetMessageReadNeedsAMembership(t *testing.T) {
	repo, db := openRepository(t)
	ctx := context.Background()
	createOwner(t, db, 1)
	mailbox := createMailbox(t, repo, "hello@mail.example")
	message := &domain.MailMessage{
		MailboxID: mailbox.ID, Direction: domain.MailDirectionInbound, Status: domain.MailStatusReceived,
		FromAddress: "alice@sender.test", To: []string{mailbox.Address}, Subject: "In", DedupeKey: "in",
	}
	if _, err := repo.CreateInboundMessage(ctx, message); err != nil {
		t.Fatalf("CreateInboundMessage: %v", err)
	}
	for _, read := range []bool{true, false} {
		if err := repo.SetMessageRead(ctx, mailbox.ID, message.ID, 0, read, time.Now().UTC()); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("SetMessageRead(read=%v) without a membership = %v; want ErrInvalidInput", read, err)
		}
	}
	var rows int64
	db.Model(&MailMessageReadPO{}).Count(&rows)
	if rows != 0 {
		t.Fatalf("stored %d read rows without a membership", rows)
	}
}

func TestCountOutboundSinceUsesAttemptTime(t *testing.T) {
	repo, db := openRepository(t)
	ctx := context.Background()
	createOwner(t, db, 1)
	mailbox := createMailbox(t, repo, "hello@mail.example")
	now := time.Now().UTC()
	store := func(subject string, attemptedAt *time.Time, queuedReason string) *domain.MailMessage {
		t.Helper()
		message := &domain.MailMessage{
			MailboxID: mailbox.ID, Direction: domain.MailDirectionOutbound, Status: domain.MailStatusQueued,
			FromAddress: "hello@mail.example", To: []string{"bob@example.com"}, Subject: subject,
			SendAttemptedAt: attemptedAt, QueuedReason: queuedReason,
		}
		if _, err := repo.CreateMessage(ctx, message); err != nil {
			t.Fatalf("CreateMessage(%s): %v", subject, err)
		}
		return message
	}
	// Created two days ago but only attempted now: it counts in today's window.
	late := store("late", &now, "")
	if err := db.Exec("UPDATE mail_messages SET created_at = ? WHERE id = ?", now.Add(-48*time.Hour), late.ID).Error; err != nil {
		t.Fatalf("age message: %v", err)
	}
	old := now.Add(-48 * time.Hour)
	store("old attempt", &old, "")
	waiting := store("waiting", nil, domain.MailQueuedForSendLimit)

	count, err := repo.CountOutboundSince(ctx, 1, now.Add(-24*time.Hour))
	if err != nil || count != 1 {
		t.Fatalf("CountOutboundSince = %d, %v; want 1, the late attempt only", count, err)
	}
	waitingCount, err := repo.CountWaitingSends(ctx, 1)
	if err != nil || waitingCount != 1 {
		t.Fatalf("CountWaitingSends = %d, %v; want 1", waitingCount, err)
	}
	stored, err := repo.FindMessage(ctx, mailbox.ID, waiting.ID)
	if err != nil || stored.QueuedReason != domain.MailQueuedForSendLimit || stored.SendAttemptedAt != nil {
		t.Fatalf("stored waiting = %+v, %v", stored, err)
	}
}
