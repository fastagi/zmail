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
	// waits records each WaitForSetups call and whether its context still had time left.
	waits []bool
}

func (c *countingZoneChecker) WaitForSetups(ctx context.Context) error {
	_, bounded := ctx.Deadline()
	c.waits = append(c.waits, bounded && ctx.Err() == nil)
	return nil
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

// The worker waits, with a bound, for the setups an activation started before it exits, also when
// it was stopped: --once and a deploy would otherwise end them halfway.
func TestMailZoneWorkerWaitsForSetupsBeforeExiting(t *testing.T) {
	checker := &countingZoneChecker{}
	require.NoError(t, runMailZoneWorker(context.Background(), checker, webhookWorkerConfig{Batch: 5, Poll: time.Hour, Once: true}))
	assert.Equal(t, 1, checker.calls)
	assert.Equal(t, []bool{true}, checker.waits)

	stopped, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, runMailZoneWorker(stopped, checker, webhookWorkerConfig{Batch: 5, Poll: time.Hour}))
	assert.Equal(t, []bool{true, true}, checker.waits, "a stopped worker still waits for its setups")
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

type recordingZoneReleaser struct {
	zone  string
	force bool
	err   error
}

func (r *recordingZoneReleaser) ReleasePendingZone(_ context.Context, zone string, force bool) error {
	r.zone, r.force = zone, force
	return r.err
}

func TestMailZoneReleaseCommandRegistered(t *testing.T) {
	application := console.New("fastbuild", "test")
	infracommands.RegisterManifest(application, Manifest())
	assert.True(t, application.HasCommand("mail:zone:release"))
}

func TestMailZoneReleaseArguments(t *testing.T) {
	zone, force, err := parseMailZoneReleaseArgs([]string{"example.org"})
	require.NoError(t, err)
	assert.Equal(t, "example.org", zone)
	assert.False(t, force)

	zone, force, err = parseMailZoneReleaseArgs([]string{"--force", "example.org"})
	require.NoError(t, err)
	assert.Equal(t, "example.org", zone)
	assert.True(t, force)

	for _, args := range [][]string{nil, {"--force"}, {"a.org", "b.org"}, {"--fast", "a.org"}} {
		_, _, err = parseMailZoneReleaseArgs(args)
		assert.Error(t, err, "%v", args)
	}
}

func TestMailZoneReleaseRunsTheRelease(t *testing.T) {
	releaser := &recordingZoneReleaser{}
	require.NoError(t, runMailZoneRelease(context.Background(), releaser, "example.org", true))
	assert.Equal(t, "example.org", releaser.zone)
	assert.True(t, releaser.force)

	releaser.err = assert.AnError
	require.ErrorIs(t, runMailZoneRelease(context.Background(), releaser, "example.org", false), assert.AnError)
}
