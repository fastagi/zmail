package mailbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/smtp"
	nettextproto "net/textproto"
	"sort"
	"strings"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
)

const (
	// smtpImplicitTLSPort is SMTP submission over implicit TLS, the default.
	smtpImplicitTLSPort = 465
	// smtpSubmissionPort is SMTP submission upgraded with a required STARTTLS.
	smtpSubmissionPort = 587
)

// Stable failure codes of a send through an account's own SMTP server.
const (
	failureSMTPAuth        = "smtp_auth_failed"
	failureSMTPRejected    = "smtp_rejected"
	failureSMTPUnavailable = "smtp_unavailable"
	failureSMTPNotBound    = "smtp_not_connected"
)

// ExternalMailSender is implemented by sources that can deliver mail written in Zmail through the
// account's own server, with the grant stored at binding.
type ExternalMailSender interface {
	SendMail(ctx context.Context, token ExternalMailToken, message mailtransport.Message) (mailtransport.Result, error)
}

var _ ExternalMailSender = (*imapSource)(nil)

// validateSMTP refuses an outgoing server that cannot be dialed safely.
// The port is checked when dialing, as for IMAP.
func (c imapCredentials) validateSMTP() error {
	probe := c
	probe.Host, probe.Port = c.SMTPHost, c.SMTPPort
	return probe.validate()
}

// tlsConfig verifies the server's certificate for host.
func (s *imapSource) tlsConfig(host string) *tls.Config {
	return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12, RootCAs: s.policy.RootCAs}
}

// openSMTP connects to the account's SMTP server, secures the session (implicit TLS on 465, a
// required STARTTLS on 587), and signs in with the stored login.
func (s *imapSource) openSMTP(ctx context.Context, credentials imapCredentials) (*smtp.Client, error) {
	port := credentials.SMTPPort
	if port != smtpImplicitTLSPort && port != smtpSubmissionPort && !s.policy.AllowAnyPort {
		return nil, fmt.Errorf("%w: SMTP port %d is not allowed", ErrExternalMailServerUnreachable, port)
	}
	implicitTLS := port != smtpSubmissionPort
	conn, err := s.dialServer(ctx, credentials.SMTPHost, port, implicitTLS)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(s.overallTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if deadlineErr := conn.SetDeadline(deadline); deadlineErr != nil {
		closeQuietly(conn)
		return nil, fmt.Errorf("%w: %v", ErrExternalMailServerUnreachable, deadlineErr)
	}
	client, err := smtp.NewClient(conn, credentials.SMTPHost)
	if err != nil {
		closeQuietly(conn)
		return nil, fmt.Errorf("%w: %v", ErrExternalMailServerUnreachable, err)
	}
	if !implicitTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			_ = client.Close()
			return nil, fmt.Errorf("%w: STARTTLS is not offered", ErrExternalMailServerUnreachable)
		}
		if err := client.StartTLS(s.tlsConfig(credentials.SMTPHost)); err != nil {
			_ = client.Close()
			return nil, fmt.Errorf("%w: %v", ErrExternalMailServerUnreachable, err)
		}
	}
	_, mechanisms := client.Extension("AUTH")
	var auth smtp.Auth = &smtpLoginAuth{username: credentials.Username, password: credentials.Password}
	if strings.Contains(" "+strings.ToUpper(mechanisms)+" ", " PLAIN ") {
		auth = smtp.PlainAuth("", credentials.Username, credentials.Password, credentials.SMTPHost)
	}
	if err := client.Auth(auth); err != nil {
		_ = client.Close()
		var reply *nettextproto.Error
		if errors.As(err, &reply) && reply.Code >= 500 {
			return nil, fmt.Errorf("%w: SMTP login refused", ErrExternalMailAuth)
		}
		return nil, fmt.Errorf("%w: %v", ErrExternalMailServerUnreachable, err)
	}
	return client, nil
}

// verifySMTP signs in to the outgoing server once and leaves.
func (s *imapSource) verifySMTP(ctx context.Context, credentials imapCredentials) error {
	client, err := s.openSMTP(ctx, credentials)
	if err != nil {
		return err
	}
	quitSMTP(client)
	return nil
}

// quitSMTP ends the session politely, or just closes it.
func quitSMTP(client *smtp.Client) {
	if err := client.Quit(); err != nil {
		_ = client.Close()
	}
}

// SendMail delivers one message through the SMTP server stored with the grant. A refusal is a
// mailtransport.Error with a stable code, so the message records why it failed.
func (s *imapSource) SendMail(ctx context.Context, token ExternalMailToken, message mailtransport.Message) (mailtransport.Result, error) {
	var credentials imapCredentials
	if json.Unmarshal([]byte(token.RefreshToken), &credentials) != nil || credentials.SMTPHost == "" {
		return mailtransport.Result{}, &mailtransport.Error{Code: failureSMTPNotBound, Permanent: true, Responded: true}
	}
	ctx, cancel := context.WithTimeout(ctx, s.overallTimeout)
	defer cancel()
	messageID := message.MessageID
	if messageID == "" {
		messageID = newSMTPMessageID(message.FromAddress)
	}
	raw, err := buildMIMEMessage(message, messageID, time.Now())
	if err != nil {
		return mailtransport.Result{}, &mailtransport.Error{Code: failureSMTPRejected, Permanent: true, Responded: true}
	}
	client, err := s.openSMTP(ctx, credentials)
	if err != nil {
		if errors.Is(err, ErrExternalMailAuth) {
			return mailtransport.Result{}, &mailtransport.Error{Code: failureSMTPAuth, Permanent: true, Responded: true}
		}
		return mailtransport.Result{}, &mailtransport.Error{Code: failureSMTPUnavailable, Responded: true}
	}
	defer func() { _ = client.Close() }()
	recipients := append(append([]string{}, message.To...), message.Cc...)
	if err := smtpTransaction(client, message.FromAddress, recipients, raw); err != nil {
		return mailtransport.Result{}, err
	}
	quitSMTP(client)
	return mailtransport.Result{RFCMessageID: messageID, MessageID: messageID}, nil
}

// smtpTransaction runs MAIL, RCPT, and DATA. Only a reply the server sent before DATA ended counts
// as answered; a lost connection after the body may have delivered it.
func smtpTransaction(client *smtp.Client, from string, recipients []string, raw []byte) error {
	refused := func(err error, afterBody bool) error {
		var reply *nettextproto.Error
		if errors.As(err, &reply) {
			return &mailtransport.Error{Code: failureSMTPRejected, Permanent: reply.Code >= 500, Responded: true}
		}
		return &mailtransport.Error{Code: failureSMTPUnavailable, Responded: !afterBody}
	}
	if err := client.Mail(from); err != nil {
		return refused(err, false)
	}
	for _, recipient := range recipients {
		if err := client.Rcpt(recipient); err != nil {
			return refused(err, false)
		}
	}
	writer, err := client.Data()
	if err != nil {
		return refused(err, false)
	}
	if _, err := writer.Write(raw); err != nil {
		return refused(err, true)
	}
	if err := writer.Close(); err != nil {
		return refused(err, true)
	}
	return nil
}

// sendThroughAccount delivers a message written in a connected mailbox through its account's SMTP
// server. The connection must be active and its grant must name the server.
func (s *service) sendThroughAccount(ctx context.Context, mailbox *domain.Mailbox, message mailtransport.Message) (mailtransport.Result, error) {
	notBound := &mailtransport.Error{Code: failureSMTPNotBound, Permanent: true, Responded: true}
	if s.externalMail == nil || s.encryptor == nil {
		return mailtransport.Result{}, notBound
	}
	connection, err := s.externalMail.FindExternalMailConnectionByMailbox(ctx, mailbox.OrganizationID, mailbox.ID)
	if errors.Is(err, ErrExternalMailConnectionNotFound) {
		return mailtransport.Result{}, notBound
	}
	if err != nil {
		return mailtransport.Result{}, err
	}
	if connection.Status != domain.ExternalMailStatusActive || connection.EncryptedRefreshToken == "" {
		return mailtransport.Result{}, notBound
	}
	connector, registered := s.externalConnectors.lookup(connection.Provider)
	sender, sends := connector.Source.(ExternalMailSender)
	if !registered || !sends || !connector.Capabilities.Send {
		return mailtransport.Result{}, notBound
	}
	grant, err := s.encryptor.DecryptString(connection.EncryptedRefreshToken)
	if err != nil {
		return mailtransport.Result{}, notBound
	}
	return sender.SendMail(ctx, ExternalMailToken{RefreshToken: grant}, message)
}

// smtpLoginAuth is the LOGIN mechanism, for servers that do not offer PLAIN. net/smtp only runs it
// after TLS is established.
type smtpLoginAuth struct {
	username, password string
}

func (a *smtpLoginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS {
		return "", nil, errors.New("refusing LOGIN without TLS")
	}
	return "LOGIN", nil, nil
}

func (a *smtpLoginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(fromServer))) {
	case "username:", "user name", "username":
		return []byte(a.username), nil
	case "password:", "password":
		return []byte(a.password), nil
	}
	return nil, errors.New("unexpected LOGIN challenge")
}

// newSMTPMessageID makes a Message-ID at the sender's domain.
func newSMTPMessageID(from string) string {
	var random [16]byte
	_, _ = rand.Read(random[:])
	_, host, found := strings.Cut(from, "@")
	if !found || host == "" {
		host = "zmail.local"
	}
	return hex.EncodeToString(random[:]) + "@" + host
}

// buildMIMEMessage writes an RFC 5322 message: text and HTML alternatives, attachments, and the
// threading headers.
func buildMIMEMessage(message mailtransport.Message, messageID string, now time.Time) ([]byte, error) {
	var out bytes.Buffer
	header := func(name, value string) {
		if value != "" {
			out.WriteString(name + ": " + value + "\r\n")
		}
	}
	for _, value := range append(append([]string{message.FromAddress, message.Subject, messageID}, message.To...), message.Cc...) {
		if strings.ContainsAny(value, "\r\n") {
			return nil, errors.New("header value carries a line break")
		}
	}
	header("From", (&mail.Address{Name: message.FromName, Address: message.FromAddress}).String())
	header("To", addressList(message.To))
	header("Cc", addressList(message.Cc))
	header("Subject", mime.QEncoding.Encode("utf-8", message.Subject))
	header("Date", now.UTC().Format(time.RFC1123Z))
	header("Message-ID", "<"+messageID+">")
	if message.InReplyTo != "" {
		header("In-Reply-To", "<"+message.InReplyTo+">")
	}
	if len(message.References) > 0 {
		header("References", "<"+strings.Join(message.References, "> <")+">")
	}
	names := make([]string, 0, len(message.Headers))
	for name := range message.Headers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value := message.Headers[name]
		if strings.ContainsAny(name, ":\r\n ") || strings.ContainsAny(value, "\r\n") {
			return nil, errors.New("extra header carries a line break")
		}
		header(nettextproto.CanonicalMIMEHeaderKey(name), value)
	}
	header("MIME-Version", "1.0")

	body := &bytes.Buffer{}
	contentType, err := writeMIMEBody(body, message)
	if err != nil {
		return nil, err
	}
	header("Content-Type", contentType)
	if !strings.HasPrefix(contentType, "multipart/") {
		header("Content-Transfer-Encoding", "quoted-printable")
	}
	out.WriteString("\r\n")
	out.Write(body.Bytes())
	return out.Bytes(), nil
}

// writeMIMEBody writes the body and returns its Content-Type.
func writeMIMEBody(body *bytes.Buffer, message mailtransport.Message) (string, error) {
	text, html := message.TextBody, message.HTMLBody
	if len(message.Attachments) == 0 && (text == "" || html == "") {
		if html != "" {
			return "text/html; charset=utf-8", writeQuotedPrintable(body, html)
		}
		return "text/plain; charset=utf-8", writeQuotedPrintable(body, text)
	}
	// The first part of a mixed message is the readable content, as alternatives.
	mixed := multipart.NewWriter(body)
	alternative := &bytes.Buffer{}
	alt := multipart.NewWriter(alternative)
	for _, part := range []struct{ kind, value string }{{"text/plain", text}, {"text/html", html}} {
		if part.value == "" {
			continue
		}
		w, err := alt.CreatePart(nettextproto.MIMEHeader{
			"Content-Type": {part.kind + "; charset=utf-8"}, "Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return "", err
		}
		if err := writeQuotedPrintable(w, part.value); err != nil {
			return "", err
		}
	}
	if err := alt.Close(); err != nil {
		return "", err
	}
	alternativeType := "multipart/alternative; boundary=" + alt.Boundary()
	if len(message.Attachments) == 0 {
		body.Reset()
		body.Write(alternative.Bytes())
		return alternativeType, nil
	}
	w, err := mixed.CreatePart(nettextproto.MIMEHeader{"Content-Type": {alternativeType}})
	if err != nil {
		return "", err
	}
	if _, err := w.Write(alternative.Bytes()); err != nil {
		return "", err
	}
	for _, file := range message.Attachments {
		kind := file.ContentType
		if kind == "" {
			kind = "application/octet-stream"
		}
		name := mime.QEncoding.Encode("utf-8", file.Filename)
		part, err := mixed.CreatePart(nettextproto.MIMEHeader{
			"Content-Type":              {mime.FormatMediaType(kind, nil) + "; name=\"" + name + "\""},
			"Content-Disposition":       {"attachment; filename=\"" + name + "\""},
			"Content-Transfer-Encoding": {"base64"},
		})
		if err != nil {
			return "", err
		}
		encoded := base64.StdEncoding.EncodeToString(file.Content)
		var wrapped strings.Builder
		for len(encoded) > 76 {
			wrapped.WriteString(encoded[:76] + "\r\n")
			encoded = encoded[76:]
		}
		wrapped.WriteString(encoded + "\r\n")
		if _, err := part.Write([]byte(wrapped.String())); err != nil {
			return "", err
		}
	}
	if err := mixed.Close(); err != nil {
		return "", err
	}
	return "multipart/mixed; boundary=" + mixed.Boundary(), nil
}

func writeQuotedPrintable(w interface{ Write([]byte) (int, error) }, value string) error {
	encoder := quotedprintable.NewWriter(w)
	if _, err := encoder.Write([]byte(strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\n", "\r\n"))); err != nil {
		return err
	}
	return encoder.Close()
}

func addressList(addresses []string) string {
	formatted := make([]string, 0, len(addresses))
	for _, address := range addresses {
		formatted = append(formatted, (&mail.Address{Address: address}).String())
	}
	return strings.Join(formatted, ", ")
}
