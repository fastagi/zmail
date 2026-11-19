package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"

	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// TestTransactionalTemplatesMigrationUpDown keeps existing templates standard, ties the
// transactional fields to the kind, keeps keys unique per organization, and rolls back by
// deleting transactional templates before the columns go.
func TestTransactionalTemplatesMigrationUpDown(t *testing.T) {
	db := testplatform.OpenPostgres(t, nil)
	migrateAll(t, db)
	require.NoError(t, (&addTransactionalTemplates{}).Down(db))
	exec := func(statement string, arguments ...any) error {
		t.Helper()
		return db.Exec(statement, arguments...).Error
	}
	require.NoError(t, exec(`INSERT INTO users (id, username, email, password, created_at, updated_at) VALUES (901, 'tpl-owner', 'tpl-owner@example.com', 'x', now(), now())`))
	for _, id := range []int{10, 11} {
		require.NoError(t, exec(`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at) VALUES (?, ?, ?, 901, now(), now())`,
			id, "Org", "org-"+string(rune('a'+id-10))))
	}
	require.NoError(t, exec(`INSERT INTO mail_templates (id, organization_id, name, subject, text_body, created_by, created_at, updated_at)
		VALUES (1, 10, 'Welcome', 'Hi', 'Hello', 901, now(), now())`))

	require.NoError(t, (&addTransactionalTemplates{}).Up(db))

	var existing struct {
		Kind                string
		TemplateKey         *string
		FromAddress         *string
		FromName            string
		VariableDefinitions string
	}
	require.NoError(t, db.Raw(`SELECT kind, template_key, from_address, from_name, variable_definitions::text AS variable_definitions
		FROM mail_templates WHERE id = 1`).Scan(&existing).Error)
	require.Equal(t, "standard", existing.Kind)
	require.Nil(t, existing.TemplateKey)
	require.Nil(t, existing.FromAddress)
	require.Equal(t, "", existing.FromName)
	require.Equal(t, "[]", existing.VariableDefinitions)

	insert := func(id, organization int, kind, key, from, definitions string) error {
		var keyValue, fromValue any
		if key != "" {
			keyValue = key
		}
		if from != "" {
			fromValue = from
		}
		return exec(`INSERT INTO mail_templates (id, organization_id, name, subject, text_body, created_by, created_at, updated_at,
			kind, template_key, from_address, variable_definitions)
			VALUES (?, ?, ?, 'Code', 'Code {{code}}', 901, now(), now(), ?, ?, ?, ?::jsonb)`,
			id, organization, "Template "+string(rune('A'+id)), kind, keyValue, fromValue, definitions)
	}
	const codeVariable = `[{"name":"code","secret":true,"required":true}]`
	require.NoError(t, insert(2, 10, "transactional", "signup-code", "no-reply@acme.test", codeVariable))
	// The kind is one of two values.
	require.Error(t, insert(3, 10, "marketing", "", "", `[]`))
	// A transactional template needs its key and sender.
	require.Error(t, insert(4, 10, "transactional", "", "no-reply@acme.test", codeVariable))
	require.Error(t, insert(5, 10, "transactional", "reset", "", codeVariable))
	// A standard template carries none of the transactional fields.
	require.Error(t, insert(6, 10, "standard", "welcome", "", `[]`))
	require.Error(t, insert(7, 10, "standard", "", "no-reply@acme.test", `[]`))
	require.Error(t, insert(8, 10, "standard", "", "", codeVariable))
	// Keys are unique per organization; another organization may reuse one.
	require.Error(t, insert(9, 10, "transactional", "signup-code", "no-reply@acme.test", codeVariable))
	require.NoError(t, insert(10, 11, "transactional", "signup-code", "no-reply@other.test", codeVariable))

	var indexes []string
	require.NoError(t, db.Raw(`SELECT indexname FROM pg_indexes WHERE schemaname = current_schema()
		AND tablename = 'mail_templates' AND indexname = 'mail_templates_key_idx'`).Scan(&indexes).Error)
	require.Equal(t, []string{"mail_templates_key_idx"}, indexes)

	require.NoError(t, (&addTransactionalTemplates{}).Down(db))
	var ids []int
	require.NoError(t, db.Raw(`SELECT id FROM mail_templates ORDER BY id`).Scan(&ids).Error)
	require.Equal(t, []int{1}, ids, "Down must delete transactional templates and keep standard ones")
	var columns int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM information_schema.columns WHERE table_schema = current_schema()
		AND table_name = 'mail_templates' AND column_name IN ('kind', 'template_key', 'from_address', 'from_name', 'variable_definitions')`).
		Scan(&columns).Error)
	require.Zero(t, columns, "Down must drop the transactional columns")

	require.NoError(t, (&addTransactionalTemplates{}).Up(db))
	require.NoError(t, insert(2, 10, "transactional", "signup-code", "no-reply@acme.test", codeVariable))
}
