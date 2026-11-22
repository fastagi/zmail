package mailbox

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
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
