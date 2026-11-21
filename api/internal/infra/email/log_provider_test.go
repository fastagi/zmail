package email

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/infra/config"
)

type failingDoer struct{ calls int }

func (d *failingDoer) Do(*http.Request) (*http.Response, error) {
	d.calls++
	return nil, http.ErrHandlerTimeout
}

func TestLogProviderRecordsSystemMailWithoutNetworkDelivery(t *testing.T) {
	var buffer bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buffer, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	client := &failingDoer{}
	service := newService(config.EmailConfig{
		Provider:       config.EmailProviderLog,
		From:           "Zmail <noreply@mail.localhost>",
		RequestTimeout: time.Second,
	}, client, "")
	if !service.IsConfigured() {
		t.Fatal("log provider with a sender must be configured")
	}

	if err := service.SendEmail(
		context.Background(),
		[]string{"new.user@example.com"},
		"Your verification code",
		"<p>Your code is</p><p><strong>482913</strong></p>",
	); err != nil {
		t.Fatalf("SendEmail() error = %v", err)
	}
	if client.calls != 0 {
		t.Fatalf("log provider made %d network calls", client.calls)
	}
	output := buffer.String()
	for _, want := range []string{"email.log_delivery", "new.user@example.com", "Your verification code", "Your code is 482913"} {
		if !strings.Contains(output, want) {
			t.Fatalf("log output %q does not contain %q", output, want)
		}
	}

	unconfigured := newService(config.EmailConfig{Provider: config.EmailProviderLog}, client, "")
	if unconfigured.IsConfigured() {
		t.Fatal("log provider without a sender must stay off")
	}
}
