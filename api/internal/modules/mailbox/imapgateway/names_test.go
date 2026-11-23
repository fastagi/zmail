package imapgateway

import (
	"testing"

	"github.com/emersion/go-imap/v2"

	"github.com/fastagi/zmail/api/internal/domain"
)

func TestFolderNamesEscapeAndDeduplicate(t *testing.T) {
	folders := []domain.MailClientFolder{
		{MailboxID: 1, Key: "inbox", MailboxAddress: "alice@mail.example"},
		{MailboxID: 1, Key: "sent", MailboxAddress: "alice@mail.example"},
		{MailboxID: 1, Key: "archive", MailboxAddress: "alice@mail.example"},
		{MailboxID: 1, Key: "trash", MailboxAddress: "alice@mail.example"},
		{MailboxID: 1, Key: "folder:7", Name: "Invoices/2026", MailboxAddress: "alice@mail.example"},
		{MailboxID: 1, Key: "folder:8", Name: "inbox", MailboxAddress: "alice@mail.example"},
		{MailboxID: 1, Key: "folder:9", Name: "Sent", MailboxAddress: "alice@mail.example"},
		{MailboxID: 1, Key: "folder:10", Name: "Shared", MailboxAddress: "alice@mail.example"},
		{MailboxID: 1, Key: "folder:11", Name: "Sent (1)", MailboxAddress: "alice@mail.example"},
		{MailboxID: 2, Key: "inbox", Shared: true, MailboxAddress: "helpdesk@mail.example"},
		{MailboxID: 2, Key: "trash", Shared: true, MailboxAddress: "helpdesk@mail.example"},
		{MailboxID: 2, Key: "folder:12", Name: "季度/Q4", Shared: true, MailboxAddress: "helpdesk@mail.example"},
	}
	tree := newFolderTree(folders)
	want := map[string]string{
		"INBOX":                              "1/inbox",
		"Sent":                               "1/sent",
		"Archive":                            "1/archive",
		"Trash":                              "1/trash",
		"Invoices∕2026":                      "1/folder:7",
		"inbox (1)":                          "1/folder:8",
		"Sent (1)":                           "1/folder:9",
		"Shared (1)":                         "1/folder:10",
		"Sent (1) (1)":                       "1/folder:11",
		"Shared/helpdesk@mail.example/INBOX": "2/inbox",
		"Shared/helpdesk@mail.example/Trash": "2/trash",
		"Shared/helpdesk@mail.example/季度∕Q4": "2/folder:12",
	}
	for name, id := range want {
		entry, ok := tree.lookup(name)
		if !ok || entry.id() != id {
			t.Fatalf("lookup(%q) = %+v, %v; want %s", name, entry, ok, id)
		}
	}
	if entry, ok := tree.lookup("inbox"); !ok || entry.id() != "1/inbox" {
		t.Fatalf("INBOX is case-insensitive: %+v %v", entry, ok)
	}
	if len(tree.selectable()) != len(want) {
		t.Fatalf("selectable = %d entries; want %d", len(tree.selectable()), len(want))
	}
	for _, parent := range []string{"Shared", "Shared/helpdesk@mail.example"} {
		entry, ok := tree.lookup(parent)
		if !ok || entry.selectable() {
			t.Fatalf("%s must be a non-selectable parent: %+v %v", parent, entry, ok)
		}
	}
	if sent, _ := tree.lookup("Sent"); sent.specialUse != imap.MailboxAttrSent {
		t.Fatalf("Sent special use = %q", sent.specialUse)
	}
	if trash, _ := tree.lookup("Shared/helpdesk@mail.example/Trash"); trash.specialUse != imap.MailboxAttrTrash {
		t.Fatalf("shared Trash special use = %q", trash.specialUse)
	}
	if _, ok := tree.lookup("Shared/other@mail.example/INBOX"); ok {
		t.Fatal("an unknown shared mailbox resolved")
	}
}
