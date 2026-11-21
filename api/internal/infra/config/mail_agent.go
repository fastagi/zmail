package config

import (
	"fmt"
	"time"
)

const (
	// DefaultMailAgentEventRetention keeps agent feed events 30 days.
	DefaultMailAgentEventRetention = 30 * 24 * time.Hour
	// DefaultMailAgentAuditRetention keeps the audit of agent reads and drafts 90 days.
	DefaultMailAgentAuditRetention = 90 * 24 * time.Hour
	// DefaultMailAgentReadsPerMinute bounds the agent route requests of one API key per minute.
	DefaultMailAgentReadsPerMinute = 600
)

// MailAgentConfig bounds what mail agent grants keep and how fast one agent may read.
type MailAgentConfig struct {
	// EventRetention is how long feed events are kept, between 1 and 365 days.
	EventRetention time.Duration
	// AuditRetention is how long agent audit rows are kept, between 1 and 730 days.
	AuditRetention time.Duration
	// ReadsPerMinute bounds the agent route requests of one API key, between 1 and 100000.
	ReadsPerMinute int
}

// Resolved fills unset values with the defaults.
func (c MailAgentConfig) Resolved() MailAgentConfig {
	if c.EventRetention == 0 {
		c.EventRetention = DefaultMailAgentEventRetention
	}
	if c.AuditRetention == 0 {
		c.AuditRetention = DefaultMailAgentAuditRetention
	}
	if c.ReadsPerMinute == 0 {
		c.ReadsPerMinute = DefaultMailAgentReadsPerMinute
	}
	return c
}

func validateMailAgentConfig(c MailAgentConfig) error {
	resolved := c.Resolved()
	const day = 24 * time.Hour
	if resolved.EventRetention < day || resolved.EventRetention > 365*day {
		return fmt.Errorf("MAIL_AGENT_EVENT_RETENTION must be between 24h and 8760h")
	}
	if resolved.AuditRetention < day || resolved.AuditRetention > 730*day {
		return fmt.Errorf("MAIL_AGENT_AUDIT_RETENTION must be between 24h and 17520h")
	}
	if resolved.ReadsPerMinute < 1 || resolved.ReadsPerMinute > 100000 {
		return fmt.Errorf("MAIL_AGENT_READS_PER_MINUTE must be between 1 and 100000")
	}
	return nil
}
