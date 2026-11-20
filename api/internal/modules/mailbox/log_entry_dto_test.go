package mailbox

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
)

// The cross-mailbox log answers MailLogEntry, which forbids additional properties: the
// organize fields of the single-mailbox list (placement, folders, labels, stars, threads,
// trash) are not part of it, and a browser that validates the contract rejects the page.
func TestMailLogEntryCarriesOnlyContractFields(t *testing.T) {
	entries := toMailLogEntries([]*domain.MailMessage{{ID: 7, MailboxID: 2, MailboxAddress: "ava@mail.example.com"}})
	raw, err := json.Marshal(entries[0])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(fields))
	for key := range fields {
		got = append(got, key)
	}
	sort.Strings(got)
	want := []string{
		"attachment_count", "cc", "created_at", "direction", "failure_code", "from", "id",
		"last_event", "mailbox_address", "mailbox_id", "queued_reason", "read", "source",
		"status", "subject", "template_id", "to",
	}
	if len(got) != len(want) {
		t.Fatalf("log entry fields = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("log entry fields = %v, want %v", got, want)
		}
	}
}
