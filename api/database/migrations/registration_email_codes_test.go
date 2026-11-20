package migrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// TestRegistrationEmailCodesMigrationUpDown rolls the code table back over stored codes and adds
// it again: the result is the golden schema.
func TestRegistrationEmailCodesMigrationUpDown(t *testing.T) {
	db := testplatform.OpenPostgres(t, nil)
	migrateAll(t, db)
	migrated := dumpSchema(t, db)

	require.NoError(t, db.Exec(`INSERT INTO registration_email_codes (email, code_hash, expires_at, created_at, updated_at)
		VALUES ('new@example.test', repeat('a', 64), now() + interval '10 minutes', now(), now())`).Error)

	require.NoError(t, (&createRegistrationEmailCodes{}).Down(db))
	assert.NotContains(t, dumpSchema(t, db), "TABLE registration_email_codes")

	require.NoError(t, (&createRegistrationEmailCodes{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db), "up after down must leave the golden schema")
}

// TestRegistrationEmailCodeConstraints keeps the row's rules in the schema.
func TestRegistrationEmailCodeConstraints(t *testing.T) {
	db := testplatform.OpenPostgres(t, nil)
	require.NoError(t, (&createRegistrationEmailCodes{}).Up(db))

	for name, statement := range map[string]string{
		"short hash":        `INSERT INTO registration_email_codes (email, code_hash, expires_at, created_at, updated_at) VALUES ('a@example.test', 'abc', now(), now(), now())`,
		"uppercase address": `INSERT INTO registration_email_codes (email, code_hash, expires_at, created_at, updated_at) VALUES ('A@example.test', repeat('a', 64), now(), now(), now())`,
		"negative attempts": `INSERT INTO registration_email_codes (email, code_hash, expires_at, attempts, created_at, updated_at) VALUES ('a@example.test', repeat('a', 64), now(), -1, now(), now())`,
	} {
		assert.Error(t, db.Exec(statement).Error, name)
	}
}
