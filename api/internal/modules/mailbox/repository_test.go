package mailbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/fastmail/api/internal/domain"
	infradatabase "github.com/fastagi/fastmail/api/internal/infra/database"
	testplatform "github.com/fastagi/fastmail/api/internal/infra/testing"
)

// mailboxOwnerPO stands in for the users table so the foreign key target exists without
// importing another module.
type mailboxOwnerPO struct {
	ID uint `gorm:"primaryKey"`
}

func (mailboxOwnerPO) TableName() string { return "users" }

func openRepository(t *testing.T) (*repository, *gorm.DB) {
	t.Helper()
	db := testplatform.OpenPostgres(t, &gorm.Config{TranslateError: true}, &mailboxOwnerPO{}, &MailboxPO{}, &MailMessagePO{})
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
	mailbox := &domain.Mailbox{UserID: 1, Address: address, DisplayName: "Owner"}
	if err := repo.CreateMailbox(context.Background(), mailbox, domain.MaxMailboxesPerUser); err != nil {
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
	duplicate := &domain.Mailbox{UserID: 2, Address: "hello@mail.example"}
	if err := repo.CreateMailbox(ctx, duplicate, domain.MaxMailboxesPerUser); !errors.Is(err, ErrMailboxAddressTaken) {
		t.Fatalf("duplicate address error = %v", err)
	}

	if err := repo.DeleteMailbox(ctx, 1, mailbox.ID); err != nil {
		t.Fatalf("DeleteMailbox: %v", err)
	}
	if _, err := repo.FindMailboxByAddress(ctx, "hello@mail.example"); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("deleted mailbox lookup error = %v", err)
	}
	// The next owner must not inherit mail addressed to the previous one.
	if err := repo.CreateMailbox(ctx, duplicate, domain.MaxMailboxesPerUser); !errors.Is(err, ErrMailboxAddressTaken) {
		t.Fatalf("released address error = %v; want ErrMailboxAddressTaken", err)
	}
}

func TestRepositoryEnforcesTheMailboxLimitPerUser(t *testing.T) {
	repo, db := openRepository(t)
	ctx := context.Background()
	createOwner(t, db, 1)
	createOwner(t, db, 2)

	for _, address := range []string{"a@mail.example", "b@mail.example"} {
		if err := repo.CreateMailbox(ctx, &domain.Mailbox{UserID: 1, Address: address}, 2); err != nil {
			t.Fatalf("CreateMailbox(%q): %v", address, err)
		}
	}
	if err := repo.CreateMailbox(ctx, &domain.Mailbox{UserID: 1, Address: "c@mail.example"}, 2); !errors.Is(err, ErrMailboxLimitReached) {
		t.Fatalf("limit error = %v", err)
	}
	if err := repo.CreateMailbox(ctx, &domain.Mailbox{UserID: 2, Address: "c@mail.example"}, 2); err != nil {
		t.Fatalf("another user is limited by the first user's mailboxes: %v", err)
	}

	owned, err := repo.ListMailboxes(ctx, 1)
	if err != nil || len(owned) != 2 || owned[0].Address != "a@mail.example" {
		t.Fatalf("ListMailboxes = %v, %v", owned, err)
	}
}

func TestRepositoryScopesMailboxesToTheirOwner(t *testing.T) {
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

func TestRepositoryCleansUpADeletedAccountInsideItsTransaction(t *testing.T) {
	repo, db := openRepository(t)
	ctx := context.Background()
	createOwner(t, db, 1)
	createOwner(t, db, 2)
	leaving := createMailbox(t, repo, "leaving@mail.example")
	staying := &domain.Mailbox{UserID: 2, Address: "staying@mail.example"}
	if err := repo.CreateMailbox(ctx, staying, domain.MaxMailboxesPerUser); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	for _, mailboxID := range []uint{leaving.ID, staying.ID} {
		if _, err := repo.CreateMessage(ctx, &domain.MailMessage{
			MailboxID: mailboxID, Direction: domain.MailDirectionInbound, Status: domain.MailStatusReceived,
			FromAddress: "alice@sender.test", Subject: "Hi",
		}); err != nil {
			t.Fatalf("CreateMessage: %v", err)
		}
	}
	countMessages := func(mailboxID uint) int64 {
		var count int64
		if err := db.Model(&MailMessagePO{}).Where("mailbox_id = ?", mailboxID).Count(&count).Error; err != nil {
			t.Fatalf("count messages: %v", err)
		}
		return count
	}

	// A rolled-back account deletion must leave the mail untouched.
	rollback := errors.New("account deletion failed later")
	err := db.Transaction(func(tx *gorm.DB) error {
		if cleanErr := repo.DeleteMailboxesForUser(infradatabase.ContextWithTransaction(ctx, tx), 1); cleanErr != nil {
			return cleanErr
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("transaction error = %v", err)
	}
	if _, findErr := repo.FindMailbox(ctx, 1, leaving.ID); findErr != nil || countMessages(leaving.ID) != 1 {
		t.Fatalf("cleanup escaped the rolled-back transaction: %v, %d messages", findErr, countMessages(leaving.ID))
	}

	if err := repo.DeleteMailboxesForUser(ctx, 1); err != nil {
		t.Fatalf("DeleteMailboxesForUser: %v", err)
	}
	if _, err := repo.FindMailbox(ctx, 1, leaving.ID); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("mailbox of the deleted account error = %v", err)
	}
	if countMessages(leaving.ID) != 0 || countMessages(staying.ID) != 1 {
		t.Fatalf("messages = %d and %d; want 0 and 1", countMessages(leaving.ID), countMessages(staying.ID))
	}
	reclaim := &domain.Mailbox{UserID: 2, Address: "leaving@mail.example"}
	if err := repo.CreateMailbox(ctx, reclaim, domain.MaxMailboxesPerUser); !errors.Is(err, ErrMailboxAddressTaken) {
		t.Fatalf("address of a deleted account error = %v; want ErrMailboxAddressTaken", err)
	}
}

func TestRepositoryStoresOneOutboundMessagePerIdempotencyKey(t *testing.T) {
	repo, db := openRepository(t)
	ctx := context.Background()
	createOwner(t, db, 1)
	createOwner(t, db, 2)
	mailbox := createMailbox(t, repo, "hello@mail.example")
	foreign := &domain.Mailbox{UserID: 2, Address: "other@mail.example"}
	if err := repo.CreateMailbox(ctx, foreign, domain.MaxMailboxesPerUser); err != nil {
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

	if owned, findErr := repo.FindOutboundMessageForUser(ctx, 1, first.ID); findErr != nil || owned.ID != first.ID {
		t.Fatalf("FindOutboundMessageForUser = %+v, %v", owned, findErr)
	}
	if _, err = repo.FindOutboundMessageForUser(ctx, 2, first.ID); !errors.Is(err, ErrMailMessageNotFound) {
		t.Fatalf("another user's lookup error = %v", err)
	}
	inbound := &domain.MailMessage{
		MailboxID: mailbox.ID, Direction: domain.MailDirectionInbound, Status: domain.MailStatusReceived,
		FromAddress: "alice@sender.test", Subject: "In",
	}
	if _, err = repo.CreateMessage(ctx, inbound); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	if _, err = repo.FindOutboundMessageForUser(ctx, 1, inbound.ID); !errors.Is(err, ErrMailMessageNotFound) {
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

func TestRepositoryReportsADisabledDatabase(t *testing.T) {
	repo := NewRepository(nil)
	if _, err := repo.ListMailboxes(context.Background(), 1); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("error = %v; want ErrServiceUnavailable", err)
	}
}
