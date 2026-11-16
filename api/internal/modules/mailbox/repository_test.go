package mailbox

import (
	"context"
	"errors"
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
		t, &gorm.Config{TranslateError: true}, &mailboxOwnerPO{}, &MailboxPO{}, &MailMessagePO{}, &MailMessageEventPO{}, &MailAttachmentPO{})
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
	mailbox := &domain.Mailbox{OrganizationID: 1, Address: address, DisplayName: "Owner"}
	if err := repo.CreateMailbox(context.Background(), mailbox, domain.MaxMailboxesPerOrganization); err != nil {
		t.Fatalf("CreateMailbox(%q): %v", address, err)
	}
	return mailbox
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

	owned, err := repo.ListMailboxes(ctx, 1)
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
	message := &domain.MailMessage{
		MailboxID: mailbox.ID, Direction: domain.MailDirectionOutbound, Status: domain.MailStatusQueued,
		FromAddress: "hello@mail.example", To: []string{"bob@example.com"}, Subject: "Out",
	}
	if _, err := repo.CreateMessage(ctx, message); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	if err := repo.UpdateMessageDelivery(ctx, message.ID, domain.MailStatusFailed, "", "provider.http_500"); err != nil {
		t.Fatalf("UpdateMessageDelivery: %v", err)
	}
	readAt := time.Now().UTC().Truncate(time.Microsecond)
	if err := repo.SetMessageRead(ctx, mailbox.ID, message.ID, &readAt); err != nil {
		t.Fatalf("SetMessageRead: %v", err)
	}
	stored, err := repo.FindMessage(ctx, mailbox.ID, message.ID)
	if err != nil || stored.Status != domain.MailStatusFailed || stored.FailureCode != "provider.http_500" ||
		stored.ReadAt == nil || !stored.ReadAt.Equal(readAt) {
		t.Fatalf("stored = %+v, err %v", stored, err)
	}
	if clearErr := repo.SetMessageRead(ctx, mailbox.ID, message.ID, nil); clearErr != nil {
		t.Fatalf("clear read: %v", clearErr)
	}
	if stored, _ := repo.FindMessage(ctx, mailbox.ID, message.ID); stored.ReadAt != nil {
		t.Fatalf("read_at = %v; want cleared", stored.ReadAt)
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
	if err := repo.SetMessageRead(ctx, mailbox.ID, message.ID, &readAt); !errors.Is(err, ErrMailMessageNotFound) {
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
	add := func(mailboxID uint, direction domain.MailDirection, read bool) {
		message := &domain.MailMessage{
			MailboxID: mailboxID, Direction: direction, Status: domain.MailStatusReceived,
			FromAddress: "alice@sender.test", Subject: "Hi",
		}
		if direction == domain.MailDirectionOutbound {
			message.Status = domain.MailStatusSent
		}
		if read {
			message.ReadAt = &readAt
		}
		if _, err := repo.CreateMessage(ctx, message); err != nil {
			t.Fatalf("CreateMessage: %v", err)
		}
	}
	add(busy.ID, domain.MailDirectionInbound, false)
	add(busy.ID, domain.MailDirectionInbound, false)
	add(busy.ID, domain.MailDirectionInbound, true)
	// An outbound message is never unread mail, even without a read time.
	add(busy.ID, domain.MailDirectionOutbound, false)

	owned, err := repo.ListMailboxes(ctx, 1)
	if err != nil || len(owned) != 2 {
		t.Fatalf("ListMailboxes = %v, %v", owned, err)
	}
	if owned[0].ID != busy.ID || owned[0].UnreadCount != 2 {
		t.Fatalf("busy mailbox unread = %d; want 2", owned[0].UnreadCount)
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
		messages, total, err := repo.ListMessagesForOrganization(ctx, 1, filter)
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

	page, total, err := repo.ListMessagesForOrganization(ctx, 1, domain.MailLogFilter{Page: 2, PerPage: 2})
	if err != nil || total != 3 || len(page) != 1 || page[0].Subject != "Invoice 100% due" {
		t.Fatalf("page 2 = %+v, total %d, err %v", page, total, err)
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

	stats, err := repo.StatsForOrganization(ctx, 1, time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC))
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
	listed, _, err := repo.ListMessagesForOrganization(ctx, 1, domain.MailLogFilter{})
	if err != nil || len(listed) != 1 || listed[0].LastEvent != domain.MailEventBounced {
		t.Fatalf("the log does not carry the summary: %+v, %v", listed, err)
	}

	found, err := repo.FindOutboundMessageByProviderID(ctx, "prov-9@mail.example")
	if err != nil || found.ID != message.ID || found.MailboxAddress != mailbox.Address || found.OrganizationID != 1 {
		t.Fatalf("FindOutboundMessageByProviderID = %+v, %v", found, err)
	}
	for _, missing := range []string{"", "unknown@mail.example"} {
		if _, err := repo.FindOutboundMessageByProviderID(ctx, missing); !errors.Is(err, ErrMailMessageNotFound) {
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
	if _, err := repo.FindOutboundMessageByProviderID(ctx, "only-inbound@sender.test"); !errors.Is(err, ErrMailMessageNotFound) {
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
	if _, err := repo.ListMailboxes(context.Background(), 1); !errors.Is(err, domain.ErrServiceUnavailable) {
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
	listed, _, err := repo.ListMessagesForOrganization(ctx, 1, domain.MailLogFilter{})
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
