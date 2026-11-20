package operatorcommands

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fastagi/zmail/api/internal/domain"
)

func TestParseOperatorGrantArguments(t *testing.T) {
	email, scope, err := parseOperatorGrantArguments([]string{"ops@example.test"})
	require.NoError(t, err)
	assert.Equal(t, "ops@example.test", email)
	assert.Equal(t, domain.OperatorScopeFull, scope, "the default scope keeps existing scripts' meaning")

	email, scope, err = parseOperatorGrantArguments([]string{"--scope=mail-metrics", "viewer@example.test"})
	require.NoError(t, err)
	assert.Equal(t, "viewer@example.test", email)
	assert.Equal(t, domain.OperatorScopeMailMetrics, scope)

	_, _, err = parseOperatorGrantArguments([]string{"ops@example.test", "--scope=admin"})
	require.Error(t, err)
	_, _, err = parseOperatorGrantArguments([]string{"--scope=full"})
	require.Error(t, err)
	_, _, err = parseOperatorGrantArguments([]string{"a@example.test", "b@example.test"})
	require.Error(t, err)
}
