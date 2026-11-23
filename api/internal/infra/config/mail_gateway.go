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
// mail apps over IMAP. An empty IMAPAddr leaves the gateway off.
type MailGatewayConfig struct {
	// IMAPAddr is the implicit-TLS IMAP listen address, such as ":993".
	IMAPAddr string
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
func (c MailGatewayConfig) Enabled() bool { return strings.TrimSpace(c.IMAPAddr) != "" }

// Resolved trims the settings and fills the defaults of unset ones.
func (c MailGatewayConfig) Resolved() MailGatewayConfig {
	c.IMAPAddr = strings.TrimSpace(c.IMAPAddr)
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
			return fmt.Errorf("MAIL_GATEWAY_IMAP_ADDR must be set when MAIL_GATEWAY_TLS_CERT_FILE or MAIL_GATEWAY_TLS_KEY_FILE is")
		}
		return nil
	}
	if !mailboxSelected {
		return fmt.Errorf("MAIL_GATEWAY_IMAP_ADDR requires the mailbox starter")
	}
	if strings.TrimSpace(mailbox.AppPasswordPepper) == "" {
		return fmt.Errorf("MAIL_APP_PASSWORD_PEPPER must be set when MAIL_GATEWAY_IMAP_ADDR is")
	}
	if resolved.TLSCertFile == "" {
		return fmt.Errorf("MAIL_GATEWAY_TLS_CERT_FILE must be set when MAIL_GATEWAY_IMAP_ADDR is")
	}
	if resolved.TLSKeyFile == "" {
		return fmt.Errorf("MAIL_GATEWAY_TLS_KEY_FILE must be set when MAIL_GATEWAY_IMAP_ADDR is")
	}
	if _, _, err := net.SplitHostPort(resolved.IMAPAddr); err != nil {
		return fmt.Errorf("MAIL_GATEWAY_IMAP_ADDR must be host:port, such as :993")
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
