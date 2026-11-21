package operatorcommands

import (
	"context"
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
	before, err := mailAgentPruneBefore(nil, now, 30*24*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, now.Add(-30*24*time.Hour), before, "the default keeps 30 days")

	before, err = mailAgentPruneBefore([]string{"--before=2026-12-01T00:00:00Z"}, now, time.Hour)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), before)

	_, err = mailAgentPruneBefore([]string{"--before=yesterday"}, now, time.Hour)
	assert.Error(t, err)
}

type recordingAgentPruner struct {
	limit  int
	pruned int
}

func (p *recordingAgentPruner) PruneAgentMailEvents(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func (p *recordingAgentPruner) PruneExpiredAgentMail(_ context.Context, _ time.Time, limit int) (int, error) {
	p.limit = limit
	return min(p.pruned, limit), nil
}

// The mail worker prunes agent feed events and audit rows last, with what the batch has left.
func TestMailWorkerPrunesAgentMailLast(t *testing.T) {
	dispatcher := &recordingMailDispatcher{queued: 3, sent: 4, purged: 2}
	pruner := &recordingAgentPruner{pruned: 100}
	processed, err := mailBroadcastWorker{dispatcher: dispatcher, agents: pruner}.DispatchWebhooks(context.Background(), 25)
	require.NoError(t, err)
	assert.Equal(t, 16, pruner.limit, "the rest of the batch after 3 queued, 4 broadcast, and 2 purged")
	assert.Equal(t, 25, processed)
}
