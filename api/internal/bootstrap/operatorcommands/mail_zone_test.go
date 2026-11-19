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

func TestWorkerArgumentErrorsNameTheWorker(t *testing.T) {
	_, err := parseMailZoneWorkerArgs([]string{"--fast"})
	require.EqualError(t, err, `unknown mail zone worker argument "--fast"`)
	_, err = parseWebhookWorkerArgs([]string{"--fast"})
	require.EqualError(t, err, `unknown webhook worker argument "--fast"`)
	_, err = parseMailZoneWorkerArgs([]string{"--poll=2h"})
	require.EqualError(t, err, "--poll must be between 100ms and 1h")
	_, err = parseWebhookWorkerArgs([]string{"--poll=2m"})
	require.EqualError(t, err, "--poll must be between 100ms and 1m")
}

func TestFormatPollLimit(t *testing.T) {
	for limit, want := range map[time.Duration]string{
		time.Hour:               "1h",
		2 * time.Hour:           "2h",
		10 * time.Minute:        "10m",
		90 * time.Minute:        "90m",
		time.Minute:             "1m",
		30 * time.Second:        "30s",
		1500 * time.Millisecond: "1.5s",
	} {
		assert.Equal(t, want, formatPollLimit(limit), limit.String())
	}
}
