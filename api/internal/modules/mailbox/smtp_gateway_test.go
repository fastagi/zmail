package mailbox

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	nettextproto "net/textproto"
	"strings"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/ratelimit"
	"github.com/fastagi/zmail/api/internal/modules/mailbox/smtpgateway"
)

// The SMTP tests drive the gateway with Go's own SMTP client over TLS, against the mail client
// fixture on the real migrations: what a mail app submits must come out of the send path.

type smtpGateway struct {
	Server             *smtpgateway.Server
	Implicit, StartTLS string
	Logs               *syncBuffer
}

func startSMTPGateway(t *testing.T, f *mailClientFixture) *smtpGateway {
	t.Helper()
	logs := &syncBuffer{}
	server := smtpgateway.NewServer(f.Service, selfSignedTLS(t), smtpgateway.Config{
		Domain: "smtp.test", FailureDelay: -1, Limits: ratelimit.NewMemoryStores(),
	}, slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	implicit, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	starttls, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.ServeImplicitTLS(implicit) }()
	go func() { _ = server.ServeSubmission(starttls) }()
	t.Cleanup(func() { _ = server.Close() })
	return &smtpGateway{Server: server, Implicit: implicit.Addr().String(), StartTLS: starttls.Addr().String(), Logs: logs}
}

// smtpPassword creates a mail client password with the smtp scope only.
func (f *mailClientFixture) smtpPassword(t *testing.T, viewer domain.OrganizationContext) string {
	t.Helper()
	_, secret, err := f.Service.CreateAppPassword(context.Background(), viewer, CreateAppPasswordInput{
		Name: "Laptop", Scopes: []domain.MailClientProtocol{domain.MailClientProtocolSMTP}, CurrentPassword: "right",
	})
	if err != nil {
		t.Fatalf("CreateAppPassword: %v", err)
	}
	return secret
}

func dialSMTPS(t *testing.T, addr string) *smtp.Client {
	t.Helper()
	conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // the test's own certificate
	if err != nil {
		t.Fatal(err)
	}
	client, err := smtp.NewClient(conn, "smtp.test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// loginAuth is AUTH LOGIN for net/smtp, which ships only PLAIN and CRAM-MD5.
type loginAuth struct{ username, password string }

func (a loginAuth) Start(*smtp.ServerInfo) (string, []byte, error) { return "LOGIN", nil, nil }

func (a loginAuth) Next(challenge []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch string(challenge) {
	case "Username:":
		return []byte(a.username), nil
	case "Password:":
		return []byte(a.password), nil
	}
	return nil, fmt.Errorf("unexpected challenge %q", challenge)
}

// sendOver submits raw from envelopeFrom to recipients on an authenticated client and returns the
// DATA reply, or the first refusal.
func sendOver(t *testing.T, client *smtp.Client, envelopeFrom string, recipients []string, raw string) error {
	t.Helper()
	if err := client.Mail(envelopeFrom); err != nil {
		return err
	}
	for _, recipient := range recipients {
		if err := client.Rcpt(recipient); err != nil {
			return err
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(raw)); err != nil {
		return err
	}
	return w.Close()
}

func smtpCode(err error) int {
	var protocolErr *nettextproto.Error
	if errors.As(err, &protocolErr) {
		return protocolErr.Code
	}
	return 0
}

func TestSMTPGatewaySendsFromASharedMailbox(t *testing.T) {
	f := newMailClientFixture(t)
	f.Exec(t, `UPDATE mailboxes SET display_name = 'Help Desk' WHERE id = ?`, f.Support.ID)
	secret := f.smtpPassword(t, f.Bob)
	g := startSMTPGateway(t, f)

	client := dialSMTPS(t, g.Implicit)
	if err := client.Auth(smtp.PlainAuth("", "bob@mail.example", secret, "smtp.test")); err != nil {
		t.Fatalf("auth: %v", err)
	}
	raw := "From: Bob <helpdesk@mail.example>\r\nTo: Carol <carol@example.com>\r\nSubject: Ticket 42\r\n" +
		"Message-ID: <t42@bob-laptop>\r\nMIME-Version: 1.0\r\nContent-Type: text/plain\r\n\r\nFixed.\r\n"
	recipients := []string{"carol@example.com", "erin@example.com"}
	if err := sendOver(t, client, "bob@mail.example", recipients, raw); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(f.Transport.sent) != 1 {
		t.Fatalf("sent %d", len(f.Transport.sent))
	}
	sent := f.Transport.sent[0]
	if sent.FromAddress != "helpdesk@mail.example" || sent.FromName != "Help Desk" || fmt.Sprint(sent.To, sent.Bcc) != "[carol@example.com] [erin@example.com]" {
		t.Fatalf("sent from %q <%s> to %v bcc %v", sent.FromName, sent.FromAddress, sent.To, sent.Bcc)
	}
	// A mail app that lost the reply sends the same message again: nothing goes out twice.
	if err := sendOver(t, client, "bob@mail.example", recipients, raw); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if len(f.Transport.sent) != 1 {
		t.Fatalf("a retry sent again: %d", len(f.Transport.sent))
	}
	// Alice's mailbox is not Bob's to send from.
	foreign := strings.Replace(raw, "helpdesk@mail.example", "alice@mail.example", 1)
	if err := sendOver(t, client, "bob@mail.example", recipients, foreign); smtpCode(err) != 550 {
		t.Fatalf("a foreign From = %v; want 550", err)
	}
	if err := client.Reset(); err != nil {
		t.Fatal(err)
	}
	if err := sendOver(t, client, "alice@mail.example", recipients, raw); smtpCode(err) != 550 {
		t.Fatalf("a foreign MAIL FROM = %v; want 550", err)
	}
	if err := client.Quit(); err != nil {
		t.Fatal(err)
	}
	for _, secretText := range []string{secret, "bob@mail.example", "helpdesk@mail.example", "carol@example.com", "erin@example.com", "Ticket 42", "Fixed."} {
		if strings.Contains(g.Logs.String(), secretText) {
			t.Fatalf("the gateway logged %q:\n%s", secretText, g.Logs.String())
		}
	}
}

func TestSMTPGatewayStartTLSLoginQueueAndLimits(t *testing.T) {
	f := newMailClientFixture(t)
	secret := f.smtpPassword(t, f.Bob)
	g := startSMTPGateway(t, f)

	client, err := smtp.Dial(g.StartTLS)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if ok, _ := client.Extension("AUTH"); ok {
		t.Fatal("AUTH is offered before STARTTLS")
	}
	if err := client.StartTLS(&tls.Config{InsecureSkipVerify: true}); err != nil { //nolint:gosec // the test's own certificate
		t.Fatal(err)
	}
	if err := client.Auth(loginAuth{"bob@mail.example", secret}); err != nil {
		t.Fatalf("AUTH LOGIN: %v", err)
	}
	for i := range 5 {
		raw := fmt.Sprintf("From: bob@mail.example\r\nTo: carol@example.com\r\nSubject: s\r\nMessage-ID: <fill%d@x>\r\n\r\nb\r\n", i)
		if err := sendOver(t, client, "bob@mail.example", []string{"carol@example.com"}, raw); err != nil {
			t.Fatalf("fill %d: %v", i, err)
		}
	}
	// Past the daily limit the message waits, and the mail app hears it was accepted.
	if err := sendOver(t, client, "bob@mail.example", []string{"carol@example.com"},
		"From: bob@mail.example\r\nTo: carol@example.com\r\nSubject: later\r\n\r\nb\r\n"); err != nil {
		t.Fatalf("over the limit: %v", err)
	}
	if len(f.Transport.sent) != 5 {
		t.Fatalf("sent %d; the sixth waits", len(f.Transport.sent))
	}
	var queued int64
	if err := f.DB.Raw(`SELECT count(*) FROM mail_messages WHERE queued_reason IS NOT NULL`).Scan(&queued).Error; err != nil || queued != 1 {
		t.Fatalf("queued %d, %v", queued, err)
	}
	big := "From: bob@mail.example\r\nTo: carol@example.com\r\nSubject: big\r\n\r\n" + strings.Repeat("0123456789abcdef\r\n", 9<<20/18) + "\r\n"
	if err := sendOver(t, client, "bob@mail.example", []string{"carol@example.com"}, big); smtpCode(err) != 552 {
		t.Fatalf("a message over 8 MiB = %v; want 552", err)
	}
}

func TestSMTPGatewayRefusesAnIMAPOnlyPassword(t *testing.T) {
	f := newMailClientFixture(t)
	g := startSMTPGateway(t, f)
	client := dialSMTPS(t, g.Implicit)
	err := client.Auth(smtp.PlainAuth("", "alice@mail.example", f.AliceSecret, "smtp.test"))
	if smtpCode(err) != 535 {
		t.Fatalf("an imap-only password = %v; want 535", err)
	}
}
