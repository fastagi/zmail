package operatorcommands

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fastagi/zmail/api/internal/domain"
)

func TestParseOperatorGrantArguments(t *testing.T) {
	email, access, err := parseOperatorGrantArguments([]string{"ops@example.test"})
	require.NoError(t, err)
	assert.Equal(t, "ops@example.test", email)
	assert.Equal(t, domain.OperatorAccess{Scope: domain.OperatorScopeFull}, access,
		"the default scope keeps existing scripts' meaning and never includes content")

	email, access, err = parseOperatorGrantArguments([]string{"--scope=mail-metrics", "viewer@example.test"})
	require.NoError(t, err)
	assert.Equal(t, "viewer@example.test", email)
	assert.Equal(t, domain.OperatorAccess{Scope: domain.OperatorScopeMailMetrics}, access)

	_, access, err = parseOperatorGrantArguments([]string{"--scope=mail-metrics,mail-content", "viewer@example.test"})
	require.NoError(t, err)
	assert.Equal(t, domain.OperatorAccess{Scope: domain.OperatorScopeMailMetrics, MailContent: true}, access)

	_, access, err = parseOperatorGrantArguments([]string{"ops@example.test", "--scope=full, mail-content"})
	require.NoError(t, err)
	assert.Equal(t, domain.OperatorAccess{Scope: domain.OperatorScopeFull, MailContent: true}, access,
		"a full operator reads bodies only with the explicit content add-on")

	for _, invalid := range [][]string{
		{"ops@example.test", "--scope=admin"},
		{"ops@example.test", "--scope=mail-content"},
		{"ops@example.test", "--scope=full,mail-metrics"},
		{"ops@example.test", "--scope=full,mail-content,mail-content"},
		{"ops@example.test", "--scope="},
		{"--scope=full"},
		{"a@example.test", "b@example.test"},
	} {
		_, _, err = parseOperatorGrantArguments(invalid)
		require.Error(t, err, "%v", invalid)
	}
}

func TestOperatorAuditMetadata(t *testing.T) {
	assert.Equal(t, map[string]any{"operation": "grant", "scope": "mail_metrics", "mail_content": true},
		operatorAuditMetadata("grant", &domain.OperatorAccess{Scope: domain.OperatorScopeMailMetrics, MailContent: true}))
	assert.Equal(t, map[string]any{"operation": "revoke"}, operatorAuditMetadata("revoke", nil))
}
