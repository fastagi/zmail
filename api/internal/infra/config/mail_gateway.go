package config

import (
	"fmt"
	"net"
	"strings"
	"time"
)

// Defaults of MailGatewayConfig.
const (
	DefaultMailGatewayHealthAddr            = "127.0.0.1:18992"
	DefaultMailGatewayMaxConnections        = 1000
	DefaultMailGatewayMaxConnectionsPerUser = 20
	DefaultMailGatewayMaxConnectionsPerIP   = 100
	DefaultMailGatewayRevalidateInterval    = 20 * time.Second
	// MaxMailGatewayRevalidateInterval keeps a revoked password's sessions alive for at most a
	// minute: one interval plus the fail-closed window.
	MaxMailGatewayRevalidateInterval = 30 * time.Second
	maxMailGatewayConnections        = 100000
)

// MailGatewayConfig configures `fastbuild mail:gateway`, the process that serves mailboxes to
// mail apps over IMAP and lets them send over SMTP submission. Each listener with an empty address
// stays off; with all three empty the gateway is off.
type MailGatewayConfig struct {
	// IMAPAddr is the implicit-TLS IMAP listen address, such as ":993".
	IMAPAddr string
	// SMTPSAddr is the implicit-TLS SMTP submission listen address, such as ":465".
	SMTPSAddr string
	// SubmissionAddr is the STARTTLS SMTP submission listen address, such as ":587".
	SubmissionAddr string
	// TLSCertFile and TLSKeyFile are PEM files, re-read when they change on disk.
	TLSCertFile string
	TLSKeyFile  string
	// HealthAddr serves /health and /metrics; keep it private.
	HealthAddr            string
	MaxConnections        int
	MaxConnectionsPerUser int
	MaxConnectionsPerIP   int
	// RevalidateInterval is how often live sessions are checked against revocation and access.
	RevalidateInterval time.Duration
}

// Enabled reports whether the gateway has a listen address.
func (c MailGatewayConfig) Enabled() bool { return c.IMAPEnabled() || c.SMTPEnabled() }

// IMAPEnabled reports whether the gateway serves IMAP.
func (c MailGatewayConfig) IMAPEnabled() bool { return strings.TrimSpace(c.IMAPAddr) != "" }

// SMTPEnabled reports whether the gateway accepts SMTP submission on either port.
func (c MailGatewayConfig) SMTPEnabled() bool {
	return strings.TrimSpace(c.SMTPSAddr) != "" || strings.TrimSpace(c.SubmissionAddr) != ""
}

// mailGatewayAddrVars names the listen address variables in messages.
const mailGatewayAddrVars = "MAIL_GATEWAY_IMAP_ADDR, MAIL_GATEWAY_SMTPS_ADDR, or MAIL_GATEWAY_SUBMISSION_ADDR"

// Resolved trims the settings and fills the defaults of unset ones.
func (c MailGatewayConfig) Resolved() MailGatewayConfig {
	c.IMAPAddr = strings.TrimSpace(c.IMAPAddr)
	c.SMTPSAddr = strings.TrimSpace(c.SMTPSAddr)
	c.SubmissionAddr = strings.TrimSpace(c.SubmissionAddr)
	c.TLSCertFile = strings.TrimSpace(c.TLSCertFile)
	c.TLSKeyFile = strings.TrimSpace(c.TLSKeyFile)
	c.HealthAddr = strings.TrimSpace(c.HealthAddr)
	if c.HealthAddr == "" {
		c.HealthAddr = DefaultMailGatewayHealthAddr
	}
	if c.MaxConnections == 0 {
		c.MaxConnections = DefaultMailGatewayMaxConnections
	}
	if c.MaxConnectionsPerUser == 0 {
		c.MaxConnectionsPerUser = DefaultMailGatewayMaxConnectionsPerUser
	}
	if c.MaxConnectionsPerIP == 0 {
		c.MaxConnectionsPerIP = DefaultMailGatewayMaxConnectionsPerIP
	}
	if c.RevalidateInterval == 0 {
		c.RevalidateInterval = DefaultMailGatewayRevalidateInterval
	}
	return c
}

func validateMailGatewayConfig(gateway MailGatewayConfig, mailbox MailboxConfig, mailboxSelected bool) error {
	resolved := gateway.Resolved()
	if !resolved.Enabled() {
		if resolved.TLSCertFile != "" || resolved.TLSKeyFile != "" {
			return fmt.Errorf("%s must be set when MAIL_GATEWAY_TLS_CERT_FILE or MAIL_GATEWAY_TLS_KEY_FILE is", mailGatewayAddrVars)
		}
		return nil
	}
	if !mailboxSelected {
		return fmt.Errorf("%s requires the mailbox starter", mailGatewayAddrVars)
	}
	if strings.TrimSpace(mailbox.AppPasswordPepper) == "" {
		return fmt.Errorf("MAIL_APP_PASSWORD_PEPPER must be set when %s is", mailGatewayAddrVars)
	}
	if resolved.TLSCertFile == "" {
		return fmt.Errorf("MAIL_GATEWAY_TLS_CERT_FILE must be set when %s is", mailGatewayAddrVars)
	}
	if resolved.TLSKeyFile == "" {
		return fmt.Errorf("MAIL_GATEWAY_TLS_KEY_FILE must be set when %s is", mailGatewayAddrVars)
	}
	listeners := map[string]bool{}
	for _, listen := range []struct{ name, addr, example string }{
		{"MAIL_GATEWAY_IMAP_ADDR", resolved.IMAPAddr, ":993"},
		{"MAIL_GATEWAY_SMTPS_ADDR", resolved.SMTPSAddr, ":465"},
		{"MAIL_GATEWAY_SUBMISSION_ADDR", resolved.SubmissionAddr, ":587"},
	} {
		if listen.addr == "" {
			continue
		}
		if _, _, err := net.SplitHostPort(listen.addr); err != nil {
			return fmt.Errorf("%s must be host:port, such as %s", listen.name, listen.example)
		}
		if listeners[listen.addr] {
			return fmt.Errorf("%s repeats another mail gateway listen address", listen.name)
		}
		listeners[listen.addr] = true
	}
	if _, _, err := net.SplitHostPort(resolved.HealthAddr); err != nil {
		return fmt.Errorf("MAIL_GATEWAY_HEALTH_ADDR must be host:port, such as %s", DefaultMailGatewayHealthAddr)
	}
	if resolved.RevalidateInterval <= 0 || resolved.RevalidateInterval > MaxMailGatewayRevalidateInterval {
		return fmt.Errorf("MAIL_GATEWAY_REVALIDATE_INTERVAL must be greater than 0 and no more than %s", MaxMailGatewayRevalidateInterval)
	}
	if resolved.MaxConnections < 1 || resolved.MaxConnections > maxMailGatewayConnections {
		return fmt.Errorf("MAIL_GATEWAY_MAX_CONNECTIONS must be between 1 and %d", maxMailGatewayConnections)
	}
	if resolved.MaxConnectionsPerUser < 1 || resolved.MaxConnectionsPerUser > resolved.MaxConnections {
		return fmt.Errorf("MAIL_GATEWAY_MAX_CONNECTIONS_PER_USER must be between 1 and MAIL_GATEWAY_MAX_CONNECTIONS")
	}
	if resolved.MaxConnectionsPerIP < 1 || resolved.MaxConnectionsPerIP > resolved.MaxConnections {
		return fmt.Errorf("MAIL_GATEWAY_MAX_CONNECTIONS_PER_IP must be between 1 and MAIL_GATEWAY_MAX_CONNECTIONS")
	}
	return nil
}
