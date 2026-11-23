package imapgateway

import (
	"strconv"
	"strings"

	"github.com/emersion/go-imap/v2"

	"github.com/fastagi/zmail/api/internal/domain"
)

const (
	// delimiter separates the levels of a folder name.
	delimiter = '/'
	// sharedPrefix is the namespace of shared mailboxes: Shared/<address>/<folder>.
	sharedPrefix = "Shared"
	// slashReplacement stands for a "/" inside a folder's own name (U+2215 DIVISION SLASH).
	slashReplacement = "∕"
)

// standardNames are the names of a mailbox's standard folders, by key.
var standardNames = map[string]string{
	domain.MailClientFolderInbox:   "INBOX",
	domain.MailClientFolderSent:    "Sent",
	domain.MailClientFolderArchive: "Archive",
	domain.MailClientFolderTrash:   "Trash",
}

// specialUses are the SPECIAL-USE attributes of the standard folders, by key.
var specialUses = map[string]imap.MailboxAttr{
	domain.MailClientFolderSent:    imap.MailboxAttrSent,
	domain.MailClientFolderArchive: imap.MailboxAttrArchive,
	domain.MailClientFolderTrash:   imap.MailboxAttrTrash,
}

// folderEntry is one name a mail app sees: a folder, or a parent that only holds folders.
type folderEntry struct {
	name       string
	folder     *domain.MailClientFolder
	specialUse imap.MailboxAttr
}

func (e folderEntry) selectable() bool { return e.folder != nil }

// id names the folder for tests and logs: <mailbox id>/<key>.
func (e folderEntry) id() string {
	if e.folder == nil {
		return ""
	}
	return strconv.FormatUint(uint64(e.folder.MailboxID), 10) + "/" + e.folder.Key
}

// folderTree maps the folders a member sees to IMAP names. It is rebuilt from the store on every
// LIST and SELECT, with the same rules, so a name always resolves to the folder it was listed for.
type folderTree struct {
	entries []folderEntry
	byName  map[string]int
}

// newFolderTree names the folders: the root mailbox's at the top level, every shared mailbox's
// under Shared/<address>/. A "/" inside a folder's own name becomes U+2215, and a user folder
// whose name is taken (a standard folder, "Shared" at the top level, or an earlier folder) gets
// " (1)", " (2)", and so on.
func newFolderTree(folders []domain.MailClientFolder) *folderTree {
	tree := &folderTree{byName: map[string]int{}}
	// Standard folders claim their names before any user folder.
	for i := range folders {
		folder := &folders[i]
		if name, ok := standardNames[folder.Key]; ok {
			tree.add(folderEntry{name: prefixOf(folder) + name, folder: folder, specialUse: specialUses[folder.Key]})
		}
	}
	tree.reserve(sharedPrefix)
	for i := range folders {
		folder := &folders[i]
		if _, ok := standardNames[folder.Key]; ok {
			continue
		}
		base := prefixOf(folder) + strings.ReplaceAll(folder.Name, string(delimiter), slashReplacement)
		name := base
		for n := 1; tree.taken(name); n++ {
			name = base + " (" + strconv.Itoa(n) + ")"
		}
		tree.add(folderEntry{name: name, folder: folder})
	}
	return tree
}

func prefixOf(folder *domain.MailClientFolder) string {
	if !folder.Shared {
		return ""
	}
	return sharedPrefix + string(delimiter) + folder.MailboxAddress + string(delimiter)
}

// nameKey folds a name for comparison. INBOX is case-insensitive by RFC 3501; other names are
// compared ignoring case too, as Zmail folder names are unique ignoring case.
func nameKey(name string) string { return strings.ToLower(name) }

func (t *folderTree) taken(name string) bool {
	_, ok := t.byName[nameKey(name)]
	return ok
}

// reserve claims a name without a folder.
func (t *folderTree) reserve(name string) {
	if !t.taken(name) {
		t.byName[nameKey(name)] = -1
	}
}

func (t *folderTree) add(entry folderEntry) {
	if entry.folder != nil && entry.folder.Shared {
		parent := sharedPrefix + string(delimiter) + entry.folder.MailboxAddress
		if index, ok := t.byName[nameKey(parent)]; !ok || index == -1 {
			t.entries = append(t.entries, folderEntry{name: parent})
			t.byName[nameKey(parent)] = len(t.entries) - 1
		}
		if index, ok := t.byName[nameKey(sharedPrefix)]; !ok || index == -1 {
			t.entries = append(t.entries, folderEntry{name: sharedPrefix})
			t.byName[nameKey(sharedPrefix)] = len(t.entries) - 1
		}
	}
	t.entries = append(t.entries, entry)
	t.byName[nameKey(entry.name)] = len(t.entries) - 1
}

// lookup finds a listed name, parents included.
func (t *folderTree) lookup(name string) (folderEntry, bool) {
	index, ok := t.byName[nameKey(name)]
	if !ok || index < 0 {
		return folderEntry{}, false
	}
	return t.entries[index], true
}

// selectable lists the folders, without parents, in listing order.
func (t *folderTree) selectable() []folderEntry {
	entries := make([]folderEntry, 0, len(t.entries))
	for _, entry := range t.entries {
		if entry.selectable() {
			entries = append(entries, entry)
		}
	}
	return entries
}

// all lists every entry, parents included, in listing order.
func (t *folderTree) all() []folderEntry { return t.entries }
