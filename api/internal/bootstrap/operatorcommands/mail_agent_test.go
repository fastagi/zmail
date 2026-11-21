package operatorcommands

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fastagi/zmail/api/internal/infra/console"
	infracommands "github.com/fastagi/zmail/api/internal/infra/console/commands"
)

func TestMailAgentEventsPruneCommandRegistered(t *testing.T) {
	application := console.New("fastbuild", "test")
	infracommands.RegisterManifest(application, Manifest())
	assert.True(t, application.HasCommand("mail:agent-events:prune"))
}

func TestMailAgentEventsPruneBefore(t *testing.T) {
	now := time.Date(2026, 12, 31, 12, 0, 0, 0, time.UTC)
	before, err := mailAgentPruneBefore(nil, now)
	require.NoError(t, err)
	assert.Equal(t, now.Add(-30*24*time.Hour), before, "the default keeps 30 days")

	before, err = mailAgentPruneBefore([]string{"--before=2026-12-01T00:00:00Z"}, now)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), before)

	_, err = mailAgentPruneBefore([]string{"--before=yesterday"}, now)
	assert.Error(t, err)
}
