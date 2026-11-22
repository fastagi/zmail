package operatorcommands

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/console"
	infracommands "github.com/fastagi/zmail/api/internal/infra/console/commands"
)

type countingExternalMailWorker struct {
	limits []int
}

func (w *countingExternalMailWorker) SyncExternalMail(_ context.Context, limit int) (int, error) {
	w.limits = append(w.limits, limit)
	return 0, nil
}

func TestMailExternalSyncCommandRegistered(t *testing.T) {
	application := console.New("fastbuild", "test")
	infracommands.RegisterManifest(application, Manifest())
	assert.True(t, application.HasCommand("mail:external:sync"))
}

func TestMailExternalSyncWorkerDefaultsAndOnce(t *testing.T) {
	cfg, err := parseExternalMailWorkerArgs(nil)
	require.NoError(t, err)
	assert.Equal(t, 20, cfg.Batch)
	assert.Equal(t, 30*time.Second, cfg.Poll)

	cfg, err = parseExternalMailWorkerArgs([]string{"--once", "--batch=5"})
	require.NoError(t, err)
	worker := &countingExternalMailWorker{}
	_, err = runWebhookWorker(context.Background(), externalMailWorker{worker: worker}, cfg)
	require.NoError(t, err)
	assert.Equal(t, []int{5}, worker.limits)
}

// TestMailExternalSyncPollsQuicklyForPush: with push on for a connector that can receive it and no
// --poll, the worker waits two seconds between idle passes; an explicit --poll wins; without push, or
// with push on but no connector configured for it, the default stays 30 seconds.
func TestMailExternalSyncPollsQuicklyForPush(t *testing.T) {
	gmail := config.ExternalMailConfig{Providers: []string{config.ExternalMailProviderGmail}, Push: config.ExternalMailPushConfig{
		Enabled: true, GmailTopic: "projects/zmail-project/topics/gmail", GmailAudience: "https://zmail.example/push",
		GmailServiceAccount: "push@zmail-project.iam.gserviceaccount.com",
	}}
	assert.Equal(t, 2*time.Second, externalMailWorkerPoll(nil, 30*time.Second, gmail))
	assert.Equal(t, 10*time.Second, externalMailWorkerPoll([]string{"--poll=10s"}, 10*time.Second, gmail))
	assert.Equal(t, 30*time.Second, externalMailWorkerPoll(nil, 30*time.Second, config.ExternalMailConfig{}))
	unconfigured := config.ExternalMailConfig{Providers: []string{config.ExternalMailProviderOutlook},
		Push: config.ExternalMailPushConfig{Enabled: true}}
	assert.Equal(t, 30*time.Second, externalMailWorkerPoll(nil, 30*time.Second, unconfigured))
}

// TestMailExternalSyncDoesNotStartWhenOff: with EXTERNAL_MAIL_PROVIDERS empty the command exits
// cleanly before it initializes the application or opens the database.
func TestMailExternalSyncDoesNotStartWhenOff(t *testing.T) {
	command := NewMailExternalSyncCommand()
	command.loadConfig = func() (*config.Config, error) { return &config.Config{}, nil }
	require.NoError(t, command.Run([]string{"--once"}))
}
