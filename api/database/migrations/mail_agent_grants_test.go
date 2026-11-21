package migrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// agentGrantFixture is the external mail fixture plus a live API key of user 901 and one grant of
// it on mailbox 1.
func agentGrantFixture(t *testing.T) (*gorm.DB, func(string, ...any)) {
	t.Helper()
	db, exec := externalMailFixture(t)
	exec(`INSERT INTO api_keys (id, user_id, name, key_prefix, key_hash, scopes, created_at, updated_at)
		VALUES (5, 901, 'agent', 'sk_agent', 'hash-agent', '["mail:agent"]', now(), now())`)
	exec(`INSERT INTO mail_agent_grants (id, organization_id, mailbox_id, api_key_id, key_user_id, granted_by, expires_at, created_at, updated_at)
		VALUES (1, 10, 1, 5, 901, 901, now() + interval '30 days', now(), now())`)
	return db, exec
}

// TestMailAgentGrantsUpDown rolls the migration back over a grant with an event, an audit row, and
// a draft, and adds it again: the result is the golden schema.
func TestMailAgentGrantsUpDown(t *testing.T) {
	db, exec := agentGrantFixture(t)
	migrated := dumpSchema(t, db)
	exec(`INSERT INTO mail_agent_events (grant_id, organization_id, mailbox_id, message_id, type, created_at)
		VALUES (1, 10, 1, 11, 'message.received', now())`)
	exec(`INSERT INTO mail_agent_audit (organization_id, mailbox_id, grant_id, api_key_id, message_id, action, created_at)
		VALUES (10, 1, 1, 5, 11, 'read', now())`)
	exec(`INSERT INTO mail_agent_drafts (organization_id, mailbox_id, grant_id, message_id, api_key_id, text_body, created_at, updated_at)
		VALUES (10, 1, 1, 11, 5, 'Thanks', now(), now())`)

	require.NoError(t, (&addMailAgentGrants{}).Down(db))
	rolledBack := dumpSchema(t, db)
	for _, table := range []string{"mail_agent_grants", "mail_agent_events", "mail_agent_audit", "mail_agent_drafts"} {
		assert.NotContains(t, rolledBack, "TABLE "+table+"\n")
	}
	require.NoError(t, (&addMailAgentGrants{}).Up(db))
	assert.Equal(t, migrated, dumpSchema(t, db), "up after down must leave the golden schema")
}

// TestMailAgentGrantsConstraints keeps the feed's rules in the schema: one event per grant and
// message, known event types, actions, and reasons, and a revocation always has its reason.
func TestMailAgentGrantsConstraints(t *testing.T) {
	db, exec := agentGrantFixture(t)
	event := `INSERT INTO mail_agent_events (grant_id, organization_id, mailbox_id, message_id, type, created_at)
		VALUES (1, 10, 1, 11, ?, now())`
	exec(event, "message.received")
	for name, statement := range map[string]string{
		"second event of the message": `INSERT INTO mail_agent_events (grant_id, organization_id, mailbox_id, message_id, type, created_at)
			VALUES (1, 10, 1, 11, 'message.received', now())`,
		"unknown event type": `INSERT INTO mail_agent_events (grant_id, organization_id, mailbox_id, message_id, type, created_at)
			VALUES (1, 10, 1, 11, 'message.sent', now())`,
		"unknown audit action": `INSERT INTO mail_agent_audit (organization_id, mailbox_id, grant_id, api_key_id, message_id, action, created_at)
			VALUES (10, 1, 1, 5, 11, 'send', now())`,
		"revoked without a reason":    `UPDATE mail_agent_grants SET revoked_at = now() WHERE id = 1`,
		"reason without a revocation": `UPDATE mail_agent_grants SET revoke_reason = 'revoked' WHERE id = 1`,
		"unknown reason":              `UPDATE mail_agent_grants SET revoked_at = now(), revoke_reason = 'bored' WHERE id = 1`,
		"grant without expiry": `INSERT INTO mail_agent_grants (organization_id, mailbox_id, api_key_id, key_user_id, granted_by, created_at, updated_at)
			VALUES (10, 1, 5, 901, 901, now(), now())`,
		"draft of another status": `INSERT INTO mail_agent_drafts (organization_id, mailbox_id, grant_id, message_id, api_key_id, text_body, status, created_at, updated_at)
			VALUES (10, 1, 1, 11, 5, 'Thanks', 'sent', now(), now())`,
	} {
		assert.Error(t, db.Exec(statement).Error, name)
	}
	exec(`UPDATE mail_agent_grants SET revoked_at = now(), revoke_reason = 'revoked' WHERE id = 1`)

	// Deleting the message removes its events; deleting the key removes its grants.
	exec(`DELETE FROM mail_messages WHERE id = 11`)
	var events int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM mail_agent_events`).Scan(&events).Error)
	assert.Zero(t, events)
	exec(`DELETE FROM api_keys WHERE id = 5`)
	var grants int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM mail_agent_grants`).Scan(&grants).Error)
	assert.Zero(t, grants)
}
