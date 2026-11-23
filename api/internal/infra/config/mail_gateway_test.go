package config

import (
	"strings"
	"testing"
	"time"
)

func TestMailGatewayConfigResolvedDefaults(t *testing.T) {
	resolved := MailGatewayConfig{IMAPAddr: " :993 "}.Resolved()
	if resolved.IMAPAddr != ":993" || resolved.HealthAddr != DefaultMailGatewayHealthAddr ||
		resolved.MaxConnections != DefaultMailGatewayMaxConnections || resolved.MaxConnectionsPerUser != DefaultMailGatewayMaxConnectionsPerUser ||
		resolved.MaxConnectionsPerIP != DefaultMailGatewayMaxConnectionsPerIP || resolved.RevalidateInterval != DefaultMailGatewayRevalidateInterval {
		t.Fatalf("Resolved() = %+v", resolved)
	}
	if !resolved.Enabled() || (MailGatewayConfig{}).Enabled() {
		t.Fatal("Enabled() must follow MAIL_GATEWAY_IMAP_ADDR")
	}
	for _, smtpOnly := range []MailGatewayConfig{{SMTPSAddr: ":465"}, {SubmissionAddr: " :587 "}} {
		if !smtpOnly.Enabled() || !smtpOnly.SMTPEnabled() || smtpOnly.IMAPEnabled() {
			t.Fatalf("an SMTP listener alone turns the gateway on: %+v", smtpOnly)
		}
	}
}

func TestValidateMailGatewayConfig(t *testing.T) {
	pepper := strings.Repeat("p", MinMailAppPasswordPepperLength)
	valid := func() MailGatewayConfig {
		return MailGatewayConfig{IMAPAddr: ":993", TLSCertFile: "/etc/zmail/imap.crt", TLSKeyFile: "/etc/zmail/imap.key"}
	}
	tests := []struct {
		name     string
		edit     func(*MailGatewayConfig)
		pepper   string
		selected bool
		wantErr  string
	}{
		{name: "valid", pepper: pepper, selected: true},
		{name: "disabled needs nothing", edit: func(c *MailGatewayConfig) { *c = MailGatewayConfig{} }},
		{name: "certificate without an address", edit: func(c *MailGatewayConfig) { c.IMAPAddr = "" }, wantErr: "MAIL_GATEWAY_IMAP_ADDR"},
		{name: "needs the mailbox starter", pepper: pepper, wantErr: "mailbox starter"},
		{name: "needs the pepper", selected: true, wantErr: "MAIL_APP_PASSWORD_PEPPER"},
		{name: "needs a certificate", pepper: pepper, selected: true, edit: func(c *MailGatewayConfig) { c.TLSCertFile = "" }, wantErr: "MAIL_GATEWAY_TLS_CERT_FILE"},
		{name: "needs a key", pepper: pepper, selected: true, edit: func(c *MailGatewayConfig) { c.TLSKeyFile = " " }, wantErr: "MAIL_GATEWAY_TLS_KEY_FILE"},
		{name: "address without a port", pepper: pepper, selected: true, edit: func(c *MailGatewayConfig) { c.IMAPAddr = "imap.example.com" }, wantErr: "MAIL_GATEWAY_IMAP_ADDR"},
		{name: "health address without a port", pepper: pepper, selected: true, edit: func(c *MailGatewayConfig) { c.HealthAddr = "localhost" }, wantErr: "MAIL_GATEWAY_HEALTH_ADDR"},
		{name: "sweep slower than 30s", pepper: pepper, selected: true, edit: func(c *MailGatewayConfig) { c.RevalidateInterval = 31 * time.Second }, wantErr: "MAIL_GATEWAY_REVALIDATE_INTERVAL"},
		{name: "negative sweep", pepper: pepper, selected: true, edit: func(c *MailGatewayConfig) { c.RevalidateInterval = -time.Second }, wantErr: "MAIL_GATEWAY_REVALIDATE_INTERVAL"},
		{name: "connection cap too high", pepper: pepper, selected: true, edit: func(c *MailGatewayConfig) { c.MaxConnections = 100001 }, wantErr: "MAIL_GATEWAY_MAX_CONNECTIONS"},
		{name: "per-user cap above the total", pepper: pepper, selected: true, edit: func(c *MailGatewayConfig) { c.MaxConnections, c.MaxConnectionsPerUser = 10, 11 }, wantErr: "MAIL_GATEWAY_MAX_CONNECTIONS_PER_USER"},
		{name: "SMTP only", pepper: pepper, selected: true, edit: func(c *MailGatewayConfig) { c.IMAPAddr, c.SMTPSAddr, c.SubmissionAddr = "", ":1465", ":1587" }},
		{name: "SMTP needs a certificate", pepper: pepper, selected: true, edit: func(c *MailGatewayConfig) { c.IMAPAddr, c.SubmissionAddr, c.TLSCertFile = "", ":1587", "" }, wantErr: "MAIL_GATEWAY_TLS_CERT_FILE"},
		{name: "SMTP needs the pepper", selected: true, edit: func(c *MailGatewayConfig) { c.IMAPAddr, c.SMTPSAddr = "", ":1465" }, wantErr: "MAIL_APP_PASSWORD_PEPPER"},
		{name: "SMTPS address without a port", pepper: pepper, selected: true, edit: func(c *MailGatewayConfig) { c.SMTPSAddr = "smtp.example.com" }, wantErr: "MAIL_GATEWAY_SMTPS_ADDR"},
		{name: "submission address without a port", pepper: pepper, selected: true, edit: func(c *MailGatewayConfig) { c.SubmissionAddr = "587" }, wantErr: "MAIL_GATEWAY_SUBMISSION_ADDR"},
		{name: "two listeners on one address", pepper: pepper, selected: true, edit: func(c *MailGatewayConfig) { c.SMTPSAddr = ":993" }, wantErr: "MAIL_GATEWAY_SMTPS_ADDR"},
		{name: "per-address cap negative", pepper: pepper, selected: true, edit: func(c *MailGatewayConfig) { c.MaxConnectionsPerIP = -1 }, wantErr: "MAIL_GATEWAY_MAX_CONNECTIONS_PER_IP"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gateway := valid()
			if test.edit != nil {
				test.edit(&gateway)
			}
			err := validateMailGatewayConfig(gateway, MailboxConfig{AppPasswordPepper: test.pepper}, test.selected)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("validate = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("validate = %v; want an error naming %s", err, test.wantErr)
			}
		})
	}
}

func TestLoadMailGatewayConfig(t *testing.T) {
	t.Setenv("MAIL_GATEWAY_IMAP_ADDR", ":1993")
	t.Setenv("MAIL_GATEWAY_SMTPS_ADDR", ":1465")
	t.Setenv("MAIL_GATEWAY_SUBMISSION_ADDR", ":1587")
	t.Setenv("MAIL_GATEWAY_TLS_CERT_FILE", "/tmp/c.pem")
	t.Setenv("MAIL_GATEWAY_TLS_KEY_FILE", "/tmp/k.pem")
	t.Setenv("MAIL_GATEWAY_HEALTH_ADDR", "127.0.0.1:19000")
	t.Setenv("MAIL_GATEWAY_MAX_CONNECTIONS", "50")
	t.Setenv("MAIL_GATEWAY_MAX_CONNECTIONS_PER_USER", "5")
	t.Setenv("MAIL_GATEWAY_MAX_CONNECTIONS_PER_IP", "7")
	t.Setenv("MAIL_GATEWAY_REVALIDATE_INTERVAL", "10s")
	got := loadMailGatewayConfig()
	want := MailGatewayConfig{IMAPAddr: ":1993", SMTPSAddr: ":1465", SubmissionAddr: ":1587", TLSCertFile: "/tmp/c.pem", TLSKeyFile: "/tmp/k.pem", HealthAddr: "127.0.0.1:19000",
		MaxConnections: 50, MaxConnectionsPerUser: 5, MaxConnectionsPerIP: 7, RevalidateInterval: 10 * time.Second}
	if got != want {
		t.Fatalf("loadMailGatewayConfig() = %+v", got)
	}
}
