package mailbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
)

// storeMessage inserts an inbound message of the mailbox in the given placement.
func storeMessage(t *testing.T, db *gorm.DB, mailboxID uint, placement domain.MailPlacement, folderID *uint) uint {
	t.Helper()
	po := &MailMessagePO{
		MailboxID: mailboxID, Direction: string(domain.MailDirectionInbound), Status: string(domain.MailStatusReceived),
		FromAddress: "alice@sender.test", Placement: string(placement), FolderID: folderID,
	}
	if placement == domain.MailPlacementTrash {
		trashedAt := time.Now().UTC()
		po.TrashedAt = &trashedAt
	}
	if err := db.Create(po).Error; err != nil {
		t.Fatalf("store message: %v", err)
	}
	return po.ID
}

func placementOf(t *testing.T, db *gorm.DB, id uint) MailMessagePO {
	t.Helper()
	var po MailMessagePO
	if err := db.First(&po, id).Error; err != nil {
		t.Fatalf("load message %d: %v", id, err)
	}
	return po
}

func TestFolderNameUniqueIgnoringCase(t *testing.T) {
	repo, db := openRepository(t)
	ctx := context.Background()
	createOwner(t, db, 1)
	mailbox := createMailbox(t, repo, "team@mail.example")
	other := createMailbox(t, repo, "other@mail.example")

	contracts := &domain.MailFolder{MailboxID: mailbox.ID, Name: "Contracts"}
	if err := repo.CreateFolder(ctx, contracts, domain.MaxMailFoldersPerMailbox); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	if contracts.ID == 0 || contracts.CreatedAt.IsZero() {
		t.Fatalf("created folder = %+v; want an id and a creation time", contracts)
	}
	if err := repo.CreateFolder(ctx, &domain.MailFolder{MailboxID: mailbox.ID, Name: "contracts"}, domain.MaxMailFoldersPerMailbox); !errors.Is(err, ErrMailFolderNameTaken) {
		t.Fatalf("second folder error = %v; want ErrMailFolderNameTaken", err)
	}
	// The same name in another mailbox, and a label of that name, are fine.
	if err := repo.CreateFolder(ctx, &domain.MailFolder{MailboxID: other.ID, Name: "contracts"}, domain.MaxMailFoldersPerMailbox); err != nil {
		t.Fatalf("folder in another mailbox: %v", err)
	}
	if err := repo.CreateLabel(ctx, &domain.MailLabel{MailboxID: mailbox.ID, Name: "Contracts", Color: domain.MailLabelColorBlue}, domain.MaxMailLabelsPerMailbox); err != nil {
		t.Fatalf("label named like a folder: %v", err)
	}
	if err := repo.CreateLabel(ctx, &domain.MailLabel{MailboxID: mailbox.ID, Name: "CONTRACTS", Color: domain.MailLabelColorRed}, domain.MaxMailLabelsPerMailbox); !errors.Is(err, ErrMailLabelNameTaken) {
		t.Fatalf("second label error = %v; want ErrMailLabelNameTaken", err)
	}

	invoices := &domain.MailFolder{MailboxID: mailbox.ID, Name: "Invoices"}
	if err := repo.CreateFolder(ctx, invoices, domain.MaxMailFoldersPerMailbox); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	if _, err := repo.RenameFolder(ctx, mailbox.ID, invoices.ID, "CONTRACTS"); !errors.Is(err, ErrMailFolderNameTaken) {
		t.Fatalf("rename onto a taken name error = %v", err)
	}
	renamed, err := repo.RenameFolder(ctx, mailbox.ID, contracts.ID, "contracts")
	if err != nil || renamed.Name != "contracts" {
		t.Fatalf("renaming a folder's own case = %+v, %v", renamed, err)
	}
	if _, missing := repo.RenameFolder(ctx, other.ID, invoices.ID, "Bills"); !errors.Is(missing, ErrMailFolderNotFound) {
		t.Fatalf("rename through another mailbox error = %v", missing)
	}

	folders, err := repo.ListFolders(ctx, mailbox.ID)
	if err != nil || len(folders) != 2 || folders[0].Name != "contracts" || folders[1].Name != "Invoices" {
		t.Fatalf("ListFolders = %+v, %v; want contracts, Invoices", folders, err)
	}
	if count, err := repo.CountFolders(ctx, mailbox.ID); err != nil || count != 2 {
		t.Fatalf("CountFolders = %d, %v", count, err)
	}
	if count, err := repo.CountLabels(ctx, mailbox.ID); err != nil || count != 1 {
		t.Fatalf("CountLabels = %d, %v", count, err)
	}
	if err := repo.CreateFolder(ctx, &domain.MailFolder{MailboxID: mailbox.ID, Name: "Third"}, 2); !errors.Is(err, ErrMailFolderLimitReached) {
		t.Fatalf("folder past the limit error = %v", err)
	}
}

// Review Focus 4: deleting a folder moves its messages to default, but a message of that folder
// already in the trash stays in the trash.
func TestDeleteFolderMovesMessagesToDefaultButKeepsTrash(t *testing.T) {
	repo, db := openRepository(t)
	ctx := context.Background()
	createOwner(t, db, 1)
	mailbox := createMailbox(t, repo, "team@mail.example")
	other := createMailbox(t, repo, "other@mail.example")
	folder := &domain.MailFolder{MailboxID: mailbox.ID, Name: "Invoices"}
	keep := &domain.MailFolder{MailboxID: mailbox.ID, Name: "Keep"}
	for _, f := range []*domain.MailFolder{folder, keep} {
		if err := repo.CreateFolder(ctx, f, domain.MaxMailFoldersPerMailbox); err != nil {
			t.Fatalf("CreateFolder: %v", err)
		}
	}
	filed := storeMessage(t, db, mailbox.ID, domain.MailPlacementFolder, &folder.ID)
	trashed := storeMessage(t, db, mailbox.ID, domain.MailPlacementTrash, &folder.ID)
	elsewhere := storeMessage(t, db, mailbox.ID, domain.MailPlacementFolder, &keep.ID)
	archived := storeMessage(t, db, mailbox.ID, domain.MailPlacementArchive, nil)

	if err := repo.DeleteFolder(ctx, other.ID, folder.ID); !errors.Is(err, ErrMailFolderNotFound) {
		t.Fatalf("delete through another mailbox error = %v", err)
	}
	if err := repo.DeleteFolder(ctx, mailbox.ID, folder.ID); err != nil {
		t.Fatalf("DeleteFolder: %v", err)
	}
	if err := repo.DeleteFolder(ctx, mailbox.ID, folder.ID); !errors.Is(err, ErrMailFolderNotFound) {
		t.Fatalf("second delete error = %v", err)
	}

	if got := placementOf(t, db, filed); got.Placement != "default" || got.FolderID != nil {
		t.Fatalf("filed message = %s/%v; want default without a folder", got.Placement, got.FolderID)
	}
	got := placementOf(t, db, trashed)
	if got.Placement != "trash" || got.TrashedAt == nil || got.FolderID != nil {
		t.Fatalf("trashed message = %s/%v/%v; want still in the trash, without the deleted folder", got.Placement, got.TrashedAt, got.FolderID)
	}
	if got := placementOf(t, db, elsewhere); got.Placement != "folder" || got.FolderID == nil || *got.FolderID != keep.ID {
		t.Fatalf("message of another folder = %s/%v", got.Placement, got.FolderID)
	}
	if got := placementOf(t, db, archived); got.Placement != "archive" {
		t.Fatalf("archived message = %s", got.Placement)
	}
	folders, err := repo.ListFolders(ctx, mailbox.ID)
	if err != nil || len(folders) != 1 || folders[0].ID != keep.ID {
		t.Fatalf("ListFolders = %+v, %v; want only Keep", folders, err)
	}
}

func TestDeleteLabelDetachesFromMessages(t *testing.T) {
	repo, db := openRepository(t)
	ctx := context.Background()
	createOwner(t, db, 1)
	mailbox := createMailbox(t, repo, "team@mail.example")
	other := createMailbox(t, repo, "other@mail.example")
	urgent := &domain.MailLabel{MailboxID: mailbox.ID, Name: "Urgent", Color: domain.MailLabelColorRed}
	later := &domain.MailLabel{MailboxID: mailbox.ID, Name: "Later", Color: domain.MailLabelColorGray}
	for _, label := range []*domain.MailLabel{urgent, later} {
		if err := repo.CreateLabel(ctx, label, domain.MaxMailLabelsPerMailbox); err != nil {
			t.Fatalf("CreateLabel: %v", err)
		}
	}
	first := storeMessage(t, db, mailbox.ID, domain.MailPlacementDefault, nil)
	second := storeMessage(t, db, mailbox.ID, domain.MailPlacementArchive, nil)
	for _, row := range []MailMessageLabelPO{{MessageID: first, LabelID: urgent.ID}, {MessageID: second, LabelID: urgent.ID}, {MessageID: first, LabelID: later.ID}} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("label message: %v", err)
		}
	}

	updated := &domain.MailLabel{ID: later.ID, MailboxID: mailbox.ID, Name: "Someday", Color: domain.MailLabelColorTeal}
	if err := repo.UpdateLabel(ctx, updated); err != nil || updated.Name != "Someday" || updated.Color != domain.MailLabelColorTeal || updated.CreatedAt.IsZero() {
		t.Fatalf("UpdateLabel = %+v, %v", updated, err)
	}
	if err := repo.UpdateLabel(ctx, &domain.MailLabel{ID: later.ID, MailboxID: other.ID, Name: "X", Color: domain.MailLabelColorGray}); !errors.Is(err, ErrMailLabelNotFound) {
		t.Fatalf("update through another mailbox error = %v", err)
	}

	if err := repo.DeleteLabel(ctx, other.ID, urgent.ID); !errors.Is(err, ErrMailLabelNotFound) {
		t.Fatalf("delete through another mailbox error = %v", err)
	}
	if err := repo.DeleteLabel(ctx, mailbox.ID, urgent.ID); err != nil {
		t.Fatalf("DeleteLabel: %v", err)
	}
	var rows []MailMessageLabelPO
	if err := db.Order("message_id").Find(&rows).Error; err != nil {
		t.Fatalf("load message labels: %v", err)
	}
	if len(rows) != 1 || rows[0].MessageID != first || rows[0].LabelID != later.ID {
		t.Fatalf("message labels after delete = %+v; want only the other label on the first message", rows)
	}
	labels, err := repo.ListLabels(ctx, mailbox.ID)
	if err != nil || len(labels) != 1 || labels[0].Name != "Someday" {
		t.Fatalf("ListLabels = %+v, %v", labels, err)
	}
	if err := repo.DeleteLabel(ctx, mailbox.ID, urgent.ID); !errors.Is(err, ErrMailLabelNotFound) {
		t.Fatalf("second delete error = %v", err)
	}
}

// Stars and labels leave with their message, like read rows.
func TestDeletingAMessageRemovesItsStarsAndLabels(t *testing.T) {
	repo, db := openRepository(t)
	ctx := context.Background()
	createOwner(t, db, 1)
	mailbox := createMailbox(t, repo, "team@mail.example")
	label := &domain.MailLabel{MailboxID: mailbox.ID, Name: "Urgent", Color: domain.MailLabelColorRed}
	if err := repo.CreateLabel(ctx, label, domain.MaxMailLabelsPerMailbox); err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	id := storeMessage(t, db, mailbox.ID, domain.MailPlacementDefault, nil)
	if err := db.Create(&MailMessageLabelPO{MessageID: id, LabelID: label.ID}).Error; err != nil {
		t.Fatalf("label: %v", err)
	}
	if err := db.Create(&MailMessageStarPO{MembershipID: as(1).MembershipID, MessageID: id, StarredAt: time.Now()}).Error; err != nil {
		t.Fatalf("star: %v", err)
	}
	if err := repo.DeleteMessage(ctx, mailbox.ID, id); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	for _, model := range []any{&MailMessageLabelPO{}, &MailMessageStarPO{}} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("%T rows left = %d, %v", model, count, err)
		}
	}
}
