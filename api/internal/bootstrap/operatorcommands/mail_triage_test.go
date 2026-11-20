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

type countingTriageWorker struct {
	limits []int
}

func (w *countingTriageWorker) DispatchTriage(_ context.Context, limit int) (int, error) {
	w.limits = append(w.limits, limit)
	return 0, nil
}

func TestMailTriageWorkCommandRegistered(t *testing.T) {
	application := console.New("fastbuild", "test")
	infracommands.RegisterManifest(application, Manifest())
	assert.True(t, application.HasCommand("mail:triage:work"))
}

func TestMailTriageWorkerDefaultsAndOnce(t *testing.T) {
	cfg, err := parseMailTriageWorkerArgs(nil)
	require.NoError(t, err)
	assert.Equal(t, 20, cfg.Batch)
	assert.Equal(t, 5*time.Second, cfg.Poll)

	cfg, err = parseMailTriageWorkerArgs([]string{"--once"})
	require.NoError(t, err)
	worker := &countingTriageWorker{}
	_, err = runWebhookWorker(context.Background(), mailTriageWorker{worker: worker}, cfg)
	require.NoError(t, err)
	assert.Equal(t, []int{20}, worker.limits)
}
