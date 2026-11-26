package config

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMailboxConfigPublicResolvedDefaultsAndNormalization(t *testing.T) {
	dev := (MailboxConfig{}).Resolved(false)
	if !slices.Equal(dev.PublicDomains, []string{DefaultMailboxDomain}) {
		t.Fatalf("dev PublicDomains = %v; want [%s]", dev.PublicDomains, DefaultMailboxDomain)
	}
	if dev.PublicLimitPerUser != DefaultMailboxPublicLimitPerUser ||
		dev.PublicMinLocalLen != DefaultMailboxPublicMinLocalLen ||
		dev.PublicDailySendLimitNew != DefaultMailboxPublicDailySendLimitNew ||
		dev.PublicDailySendLimit != DefaultMailboxPublicDailySendLimit ||
		dev.PublicMaxRecipientsNew != DefaultMailboxPublicMaxRecipientsNew ||
		dev.PublicMaxRecipients != DefaultMailboxPublicMaxRecipients {
		t.Fatalf("dev public defaults = %+v", dev)
	}

	prod := (MailboxConfig{
		Domain:        "zmail.im",
		PublicDomains: []string{" ZMail.IM. ", "zmail.ai", "", "zmail.im"},
	}).Resolved(true)
	want := []string{"zmail.im", "zmail.ai"}
	if !slices.Equal(prod.PublicDomains, want) {
		t.Fatalf("prod PublicDomains = %v; want %v", prod.PublicDomains, want)
	}

	fallback := (MailboxConfig{Domain: "zmail.im"}).Resolved(true)
	if !slices.Equal(fallback.PublicDomains, []string{"zmail.im"}) {
		t.Fatalf("fallback PublicDomains = %v; want [zmail.im]", fallback.PublicDomains)
	}
}

func TestLoadMailboxPublicConfigFromEnv(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("FASTBUILD_ENV_FILE", "")
	t.Setenv("DB_ENABLED", "false")
	t.Setenv("OPTIONAL_STARTERS", "organization,mailbox")
	t.Setenv("MAILBOX_DOMAIN", "zmail.im")
	t.Setenv("MAILBOX_PUBLIC_DOMAINS", "zmail.im, zmail.ai")
	t.Setenv("MAILBOX_PUBLIC_LIMIT_PER_USER", "2")
	t.Setenv("MAILBOX_PUBLIC_MIN_LOCAL_LEN", "5")
	t.Setenv("MAILBOX_PUBLIC_DAILY_SEND_NEW", "12")
	t.Setenv("MAILBOX_PUBLIC_DAILY_SEND", "40")
	t.Setenv("MAILBOX_PUBLIC_MAX_RECIPIENTS_NEW", "2")
	t.Setenv("MAILBOX_PUBLIC_MAX_RECIPIENTS", "8")
	t.Cleanup(func() { LoadFresh() })

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	resolved := cfg.Mailbox.Resolved(false)
	if !slices.Equal(resolved.PublicDomains, []string{"zmail.im", "zmail.ai"}) {
		t.Fatalf("PublicDomains = %v", resolved.PublicDomains)
	}
	if resolved.PublicLimitPerUser != 2 || resolved.PublicMinLocalLen != 5 ||
		resolved.PublicDailySendLimitNew != 12 || resolved.PublicDailySendLimit != 40 ||
		resolved.PublicMaxRecipientsNew != 2 || resolved.PublicMaxRecipients != 8 {
		t.Fatalf("unexpected public config: %+v", resolved)
	}
}

func TestValidateMailboxPublicConfigRejectsInvalidValues(t *testing.T) {
	validBase := func() MailboxConfig {
		return MailboxConfig{
			Domain:             "zmail.im",
			Provider:           MailboxProviderLog,
			RequestTimeout:     15 * time.Second,
			DailySendLimit:     200,
			MaxPerOrganization: 50,
		}
	}
	cases := []struct {
		name   string
		mutate func(*MailboxConfig)
		want   string
	}{
		{"invalid public domain", func(c *MailboxConfig) { c.PublicDomains = []string{"not a domain"} }, "MAILBOX_PUBLIC_DOMAINS"},
		{"limit too high", func(c *MailboxConfig) { c.PublicLimitPerUser = 101 }, "MAILBOX_PUBLIC_LIMIT_PER_USER"},
		{"limit negative", func(c *MailboxConfig) { c.PublicLimitPerUser = -1 }, "MAILBOX_PUBLIC_LIMIT_PER_USER"},
		{"min len too high", func(c *MailboxConfig) { c.PublicMinLocalLen = 65 }, "MAILBOX_PUBLIC_MIN_LOCAL_LEN"},
		{"daily new negative", func(c *MailboxConfig) { c.PublicDailySendLimitNew = -1 }, "MAILBOX_PUBLIC_DAILY_SEND_NEW"},
		{"daily send too high", func(c *MailboxConfig) { c.PublicDailySendLimit = 200000 }, "MAILBOX_PUBLIC_DAILY_SEND"},
		{"max recipients new too high", func(c *MailboxConfig) { c.PublicMaxRecipientsNew = 51 }, "MAILBOX_PUBLIC_MAX_RECIPIENTS_NEW"},
		{"max recipients too high", func(c *MailboxConfig) { c.PublicMaxRecipients = 51 }, "MAILBOX_PUBLIC_MAX_RECIPIENTS"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validBase()
			tc.mutate(&cfg)
			err := validateMailboxConfig(cfg, true, false)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validateMailboxConfig error = %v; want %s", err, tc.want)
			}
		})
	}
}
