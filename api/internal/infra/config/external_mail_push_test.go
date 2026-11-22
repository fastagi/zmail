package config

import (
	"strings"
	"testing"
	"time"
)

// TestExternalMailPushGmailNeedsItsServiceAccount: Gmail push is refused at startup in every
// environment unless the push subscription's service account is named, since otherwise any
// Google-signed token for the audience would be accepted; with it, Gmail push is configured.
func TestExternalMailPushGmailNeedsItsServiceAccount(t *testing.T) {
	gmail := ExternalMailPushConfig{
		Enabled: true, GmailTopic: "projects/zmail-project/topics/gmail",
		GmailAudience: "https://zmail.example/v1/external-mail-notifications/gmail",
	}
	for _, production := range []bool{false, true} {
		err := validateExternalMailPushConfig(gmail, production)
		if err == nil || !strings.Contains(err.Error(), "EXTERNAL_MAIL_GMAIL_PUSH_SERVICE_ACCOUNT") {
			t.Fatalf("production %v: Gmail push without a service account = %v", production, err)
		}
	}
	if gmail.GmailConfigured() {
		t.Fatal("Gmail push counts as configured without a service account")
	}
	gmail.GmailServiceAccount = "gmail-push@zmail-project.iam.gserviceaccount.com"
	if err := validateExternalMailPushConfig(gmail, true); err != nil || !gmail.GmailConfigured() {
		t.Fatalf("Gmail push with a service account = %v, configured %v", err, gmail.GmailConfigured())
	}
}

// TestExternalMailPushConfigBounds: the defaults, and values outside their bounds refused.
func TestExternalMailPushConfigBounds(t *testing.T) {
	var defaults ExternalMailPushConfig
	if defaults.Poll() != 15*time.Minute || defaults.Coalesce() != 10*time.Second || defaults.Quiet() != 24*time.Hour ||
		defaults.RequestsPerMinute() != 6000 || defaults.JWKSURL() != DefaultExternalMailGmailPushJWKSURL {
		t.Fatalf("defaults = %v %v %v %v %v", defaults.Poll(), defaults.Coalesce(), defaults.Quiet(), defaults.RequestsPerMinute(), defaults.JWKSURL())
	}
	for name, raw := range map[string]ExternalMailPushSettings{
		"poll too short":    {PollInterval: "1m"},
		"quiet too short":   {QuietLimit: "10m"},
		"window too long":   {CoalesceWindow: "10m"},
		"rate not a number": {MaxPerMinute: "many"},
		"topic malformed":   {GmailTopic: "zmail", GmailAudience: "a", GmailServiceAccount: "a@b.example"},
		"audience alone":    {GmailAudience: "a"},
		"outlook over http": {OutlookNotificationURL: "http://zmail.example/hook"},
	} {
		if err := validateExternalMailPushConfig(parseExternalMailPushConfig(raw), true); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
