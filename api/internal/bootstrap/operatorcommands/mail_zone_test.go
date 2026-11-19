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

type countingZoneChecker struct {
	calls  int
	limits []int
}

func (c *countingZoneChecker) CheckPendingZones(_ context.Context, limit int) (int, error) {
	c.calls++
	c.limits = append(c.limits, limit)
	return limit, nil
}

func TestMailZoneWorkCommandRegistered(t *testing.T) {
	application := console.New("fastbuild", "test")
	infracommands.RegisterManifest(application, Manifest())
	assert.True(t, application.HasCommand("mail:zone:work"))
}

func TestMailZoneWorkerDefaults(t *testing.T) {
	cfg, err := parseMailZoneWorkerArgs(nil)
	require.NoError(t, err)
	assert.Equal(t, 100, cfg.Batch)
	assert.Equal(t, 10*time.Minute, cfg.Poll)
	assert.False(t, cfg.Once)

	cfg, err = parseMailZoneWorkerArgs([]string{"--batch=20", "--poll=30m", "--once"})
	require.NoError(t, err)
	assert.Equal(t, 20, cfg.Batch)
	assert.Equal(t, 30*time.Minute, cfg.Poll)
	assert.True(t, cfg.Once)

	_, err = parseMailZoneWorkerArgs([]string{"--poll=50ms"})
	assert.Error(t, err)
	_, err = parseMailZoneWorkerArgs([]string{"--poll=2h"})
	assert.Error(t, err)
}

func TestMailZoneWorkerAlwaysWaitsForThePoll(t *testing.T) {
	checker := &countingZoneChecker{}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// A full batch would make the shared loop run again at once; the zone worker waits instead.
	_, err := runWebhookWorker(ctx, mailZoneWorker{checker: checker}, webhookWorkerConfig{Batch: 5, Poll: time.Hour})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, 1, checker.calls)
	assert.Equal(t, []int{5}, checker.limits)
}
