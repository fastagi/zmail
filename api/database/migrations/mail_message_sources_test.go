package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// mailClientFixture migrates everything and seeds one organization with an owner, a member, and
// one mailbox.
func mailClientFixture(t *testing.T) (*gorm.DB, func(string, ...any)) {
	t.Helper()
	db := testplatform.OpenPostgres(t, nil)
	migrateAll(t, db)
	exec := func(statement string, arguments ...any) {
		t.Helper()
		require.NoError(t, db.Exec(statement, arguments...).Error)
	}
	exec(`INSERT INTO users (id, username, email, password, created_at, updated_at) VALUES
		(901, 'client-owner', 'client-owner@example.com', 'x', now(), now()),
		(902, 'client-member', 'client-member@example.com', 'x', now(), now())`)
	exec(`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at) VALUES (10, 'Acme', 'acme', 901, now(), now())`)
	exec(`INSERT INTO organization_memberships (id, organization_id, user_id, role, created_at, updated_at) VALUES
		(100, 10, 901, 'owner', now(), now()), (101, 10, 902, 'member', now(), now())`)
	exec(`INSERT INTO mailboxes (id, organization_id, created_by, address, created_at, updated_at) VALUES (1, 10, 901, 'team@mail.example', now(), now())`)
	return db, exec
}

// TestMailMessageSourcesUpDown: the source table enforces its size, digest, and origin rules,
// follows its message on delete, and rolls back to the golden schema.
func TestMailMessageSourcesUpDown(t *testing.T) {
	db, exec := mailClientFixture(t)
	migrated := dumpSchema(t, db)
	exec(`INSERT INTO mail_messages (id, mailbox_id, direction, status, from_address, created_at, updated_at)
		VALUES (1, 1, 'inbound', 'received', 'a@example.com', now(), now()), (2, 1, 'inbound', 'received', 'b@example.com', now(), now())`)
	digest := strings.Repeat("a", 64)
	insert := `INSERT INTO mail_message_sources (message_id, raw, size, sha256, origin, created_at) VALUES (?, ?, ?, ?, ?, now())`
	for name, arguments := range map[string][]any{
		"size disagrees with raw": {1, []byte("hello"), 4, digest, "inbound"},
		"empty source":            {1, []byte{}, 0, digest, "inbound"},
		"digest not hex":          {1, []byte("hello"), 5, "xyz", "inbound"},
		"unknown origin":          {1, []byte("hello"), 5, digest, "import"},
		"unknown message":         {99, []byte("hello"), 5, digest, "inbound"},
		"over five MiB":           {1, make([]byte, 5*1024*1024+1), 5*1024*1024 + 1, digest, "inbound"},
	} {
		assert.Error(t, db.Exec(insert, arguments...).Error, name)
	}
	exec(insert, 1, []byte("hello"), 5, digest, "inbound")
	exec(insert, 2, []byte("world"), 5, digest, "smtp")
	exec(`DELETE FROM mail_messages WHERE id = 1`)
	var remaining []int
	require.NoError(t, db.Raw(`SELECT message_id FROM mail_message_sources ORDER BY message_id`).Scan(&remaining).Error)
	assert.Equal(t, []int{2}, remaining, "deleting a message deletes its source")

	require.NoError(t, (&addMailMessageSources{}).Down(db))
	assert.NotContains(t, dumpSchema(t, db), "TABLE mail_message_sources")
	require.NoError(t, (&addMailMessageSources{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db), "up after down must leave the golden schema")
}
