package migrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMailDomainDefaultConstraints: domains start unmarked, one organization marks at most one,
// and another organization marks its own.
func TestMailDomainDefaultConstraints(t *testing.T) {
	db, exec := mailClientFixture(t)
	exec(`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at) VALUES (11, 'Beta', 'beta', 901, now(), now())`)
	insert := `INSERT INTO mail_domains (id, organization_id, name, zone_id, zone_name, status, created_by, created_at, updated_at)
		VALUES (?, ?, ?, 'z1', 'example.test', 'verified', 901, now(), now())`
	exec(insert, 1, 10, "a.example.test")
	exec(insert, 2, 10, "b.example.test")
	exec(insert, 3, 11, "c.example.test")

	var marked int
	require.NoError(t, db.Raw(`SELECT count(*) FROM mail_domains WHERE is_default`).Scan(&marked).Error)
	assert.Zero(t, marked, "existing domains start unmarked")

	exec(`UPDATE mail_domains SET is_default = true WHERE id = 1`)
	assert.Error(t, db.Exec(`UPDATE mail_domains SET is_default = true WHERE id = 2`).Error,
		"a second default in one organization")
	exec(`UPDATE mail_domains SET is_default = true WHERE id = 3`)
	exec(`UPDATE mail_domains SET is_default = false WHERE id = 1`)
	exec(`UPDATE mail_domains SET is_default = true WHERE id = 2`)
}

// TestMailDomainDefaultUpDown rolls the mark back and applies it again.
func TestMailDomainDefaultUpDown(t *testing.T) {
	db, _ := mailClientFixture(t)
	migrated := dumpSchema(t, db)
	require.NoError(t, (&addMailDomainDefault{}).Down(db))
	rolledBack := dumpSchema(t, db)
	assert.NotContains(t, rolledBack, "is_default")
	assert.NotContains(t, rolledBack, "idx_mail_domains_default")
	require.NoError(t, (&addMailDomainDefault{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db), "up after down must leave the golden schema")
}
