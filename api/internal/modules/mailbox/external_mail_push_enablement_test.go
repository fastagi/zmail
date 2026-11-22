package mailbox

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/ratelimit"
)

// Tests for the second review of the push subscriptions, which blocks enabling push.

// newAddressPushFixture is the push fixture whose Gmail connector authenticates notifications by a
// "Bearer good" header and names the account by its address, as Gmail does.
func newAddressPushFixture(t *testing.T) *externalFixture {
	t.Helper()
	f := newPushFixture(t)
	registry := fakeExternalMailRegistry(f.source)
	registry.connectors[0].Push = addressPushSource{f.source}
	f.service.externalConnectors = registry
	return f
}

// notifyAddress sends an authenticated notification for the fixture account.
func (f *externalFixture) notifyAddress(t *testing.T) {
	t.Helper()
	if _, err := f.service.ReceiveExternalMailPush(context.Background(), domain.ExternalMailProviderGmail, ExternalMailPushDelivery{
		Header: http.Header{"Authorization": {"Bearer good"}}, Body: []byte(f.connection.AccountAddress), ClientIP: "66.102.0.1",
	}); err != nil {
		t.Fatalf("notify: %v", err)
	}
}

// TestExternalMailPushHandshakesSurviveJunk: a sender that used up its junk budget still completes
// Graph's validation handshake, which has a budget of its own; junk past its budget is answered 429
// without a log line, and the refusals within it log at most one line a minute.
func TestExternalMailPushHandshakesSurviveJunk(t *testing.T) {
	f := newPushFixture(t)
	f.service.pushRefusals = ratelimit.NewMemoryStore(3, time.Minute)
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	ctx := context.Background()
	limited := 0
	for range 40 {
		_, err := f.service.ReceiveExternalMailPush(ctx, domain.ExternalMailProviderOutlook, ExternalMailPushDelivery{
			Header: http.Header{}, Query: url.Values{}, Body: fakeNotificationBody("fake-sub-404", "guess", ""), ClientIP: "10.1.2.3",
		})
		if errors.Is(err, ErrExternalMailPushRateLimited) {
			limited++
		}
	}
	if limited != 37 {
		t.Fatalf("junk past its budget answered 429 %d times, want 37", limited)
	}
	if lines := strings.Count(logs.String(), "external_mail.push_refused"); lines != 1 {
		t.Fatalf("40 junk requests logged %d lines, want 1", lines)
	}
	reply, err := f.service.ReceiveExternalMailPush(ctx, domain.ExternalMailProviderOutlook, ExternalMailPushDelivery{
		Header: http.Header{}, Query: url.Values{"validationToken": {"handshake-1"}}, ClientIP: "10.1.2.3",
	})
	if err != nil || reply.Validation != "handshake-1" {
		t.Fatalf("a handshake after the junk = %+v, %v", reply, err)
	}
}

// TestExternalMailPushIgnoresMailItAlreadyHas: a poll that reads again a message already stored, as
// Graph delta does for a message the member read, flagged, or categorized, and as a restart after an
// expired cursor does, never marks a healthy subscription degraded. Only mail the save inserts
// counts.
func TestExternalMailPushIgnoresMailItAlreadyHas(t *testing.T) {
	for name, again := range map[string]func(f *externalFixture, message ExternalMessage){
		"provider reports an updated message": func(f *externalFixture, message ExternalMessage) {
			f.source.Deliver(externalTestToken, message)
		},
		"restart after an expired cursor": func(f *externalFixture, _ ExternalMessage) {
			f.source.ExpireCursor(externalTestToken)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newAddressPushFixture(t)
			start := f.service.now().UTC()
			f.sync(t)
			message := externalMessage("g-read-later", "Read ten minutes later", start.Add(8*time.Minute))
			f.source.Deliver(externalTestToken, message)
			f.advance(8 * time.Minute)
			f.notifyAddress(t)
			f.advance(config.DefaultExternalMailPushCoalesceWindow)
			f.sync(t)
			if runs := f.runs(t); runs[0].Trigger != domain.ExternalMailSyncTriggerPush || runs[0].Stored != 1 {
				t.Fatalf("the announced message: %+v", runs[0])
			}
			again(f, message)
			for range 3 {
				f.advance(config.DefaultExternalMailPushPollInterval)
				f.sync(t)
			}
			if subscription := f.subscription(t, f.connection.ID); subscription.DegradedAt != nil {
				t.Fatalf("a message already stored marked the subscription degraded: %+v", subscription)
			}
			if len(f.storedExternal(t)) != 1 {
				t.Fatal("the message was stored twice")
			}
		})
	}
}
