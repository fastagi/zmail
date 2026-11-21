package config

import (
	"testing"
	"time"
)

func TestMailAgentConfigDefaultsAndBounds(t *testing.T) {
	resolved := MailAgentConfig{}.Resolved()
	if resolved.EventRetention != 30*24*time.Hour || resolved.AuditRetention != 90*24*time.Hour || resolved.ReadsPerMinute != 600 {
		t.Fatalf("defaults = %+v", resolved)
	}
	if err := validateMailAgentConfig(MailAgentConfig{}); err != nil {
		t.Fatalf("defaults invalid: %v", err)
	}
	for name, bad := range map[string]MailAgentConfig{
		"events kept an hour":   {EventRetention: time.Hour},
		"events kept two years": {EventRetention: 2 * 365 * 24 * time.Hour},
		"audit kept an hour":    {AuditRetention: time.Hour},
		"negative read limit":   {ReadsPerMinute: -1},
	} {
		if validateMailAgentConfig(bad) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
