package migrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// TestAddAPIKeyRestrictionsUpDown keeps existing keys unrestricted, refuses domains without an
// organization, revokes restricted keys on rollback, and rolls back and forward to the golden schema.
func TestAddAPIKeyRestrictionsUpDown(t *testing.T) {
	db := testplatform.OpenPostgres(t, nil)
	migrateAll(t, db)
	migrated := dumpSchema(t, db)
	require.NoError(t, (&addAPIKeyRestrictions{}).Down(db))
	require.NoError(t, db.Exec(`INSERT INTO api_keys (user_id, name, key_prefix, key_hash, scopes, created_at, updated_at)
		VALUES (7, 'old', 'zmail_ab12cd34', 'hash-old', '["mail:send"]', now(), now())`).Error)

	require.NoError(t, (&addAPIKeyRestrictions{}).Up(db))

	var old struct {
		OrganizationID *uint
		SendingDomains *string
	}
	require.NoError(t, db.Raw(`SELECT organization_id, sending_domains FROM api_keys WHERE key_hash = 'hash-old'`).Scan(&old).Error)
	assert.Nil(t, old.OrganizationID, "an existing key must stay unrestricted")
	assert.Nil(t, old.SendingDomains, "an existing key must stay unrestricted")
	require.NoError(t, db.Exec(`INSERT INTO api_keys (user_id, name, key_prefix, key_hash, scopes, organization_id, sending_domains, created_at, updated_at)
		VALUES (7, 'bound', 'zmail_cd34ef56', 'hash-bound', '["mail:send"]', 5, '["acme.example"]', now(), now())`).Error)
	require.NoError(t, db.Exec(`INSERT INTO api_keys (user_id, name, key_prefix, key_hash, scopes, organization_id, created_at, updated_at)
		VALUES (7, 'org', 'zmail_ef56ab12', 'hash-org', '["mail:send"]', 5, now(), now())`).Error)
	assert.Error(t, db.Exec(`INSERT INTO api_keys (user_id, name, key_prefix, key_hash, scopes, sending_domains, created_at, updated_at)
		VALUES (7, 'bad', 'zmail_zz', 'hash-bad', '[]', '["acme.example"]', now(), now())`).Error,
		"domains without an organization")

	require.NoError(t, (&addAPIKeyRestrictions{}).Down(db))
	var revoked struct{ Old, Bound, Org bool }
	require.NoError(t, db.Raw(`SELECT
		(SELECT revoked_at IS NOT NULL FROM api_keys WHERE key_hash = 'hash-old') AS old,
		(SELECT revoked_at IS NOT NULL FROM api_keys WHERE key_hash = 'hash-bound') AS bound,
		(SELECT revoked_at IS NOT NULL FROM api_keys WHERE key_hash = 'hash-org') AS org`).Scan(&revoked).Error)
	assert.False(t, revoked.Old, "the rollback revoked an unrestricted key")
	assert.True(t, revoked.Bound, "a domain-restricted key survived the rollback unrevoked")
	assert.True(t, revoked.Org, "an organization-bound key survived the rollback unrevoked")
	schema := dumpSchema(t, db)
	for _, column := range []string{"sending_domains", "chk_api_keys_sending_domains_organization"} {
		assert.NotContains(t, schema, column)
	}

	require.NoError(t, (&addAPIKeyRestrictions{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db), "up after down must leave the golden schema")
}
