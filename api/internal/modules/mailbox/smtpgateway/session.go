package smtpgateway

import (
	"errors"
	"io"
	"net/mail"
	"strings"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"github.com/fastagi/zmail/api/internal/domain"
)

// backend starts a go-smtp session at each EHLO or HELO, again after STARTTLS.
type backend struct {
	server *Server
}

func (b backend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	tracked := trackedConn(c)
	if tracked == nil {
		return nil, errors.New("connection is not tracked")
	}
	return &session{server: b.server, conn: tracked}, nil
}

// session is one mail app between greetings: whom it signed in as and the message it is sending.
type session struct {
	server *Server
	conn   *conn

	principal *domain.MailClientPrincipal

	from         string
	recipients   []string
	seen         map[string]bool
	rcptCommands int
}

var _ smtp.AuthSession = (*session)(nil)

const (
	mechanismPlain = "PLAIN"
	mechanismLogin = "LOGIN"
)

func (s *session) AuthMechanisms() []string { return []string{mechanismPlain, mechanismLogin} }

func (s *session) Auth(mech string) (sasl.Server, error) {
	switch strings.ToUpper(mech) {
	case mechanismPlain:
		return sasl.NewPlainServer(func(identity, username, password string) error {
			if identity != "" && identity != username {
				return s.refuseSignIn(username)
			}
			return s.signIn(username, password)
		}), nil
	case mechanismLogin:
		return &loginServer{authenticate: s.signIn}, nil
	}
	return nil, &smtp.SMTPError{Code: 504, EnhancedCode: smtp.EnhancedCode{5, 5, 4}, Message: "Unrecognized authentication mechanism"}
}

// signIn checks a mail client password with the smtp scope. Every refusal looks the same and waits
// the same fixed delay; a store failure is temporary so it never counts as a wrong password.
func (s *session) signIn(username, password string) error {
	server := s.server
	ctx, cancel := server.storeContext()
	defer cancel()
	limiter := server.cfg.Limiter
	if !limiter.Allow(ctx, username, s.conn.ip) {
		recordAuth("locked")
		server.wait(server.cfg.FailureDelay)
		return replyAuthLocked
	}
	principal, err := server.store.AuthenticateClient(ctx, username, password, domain.MailClientProtocolSMTP, s.conn.ip)
	if errors.Is(err, domain.ErrMailClientAuthFailed) {
		limiter.Failed(ctx, username, s.conn.ip)
		return s.refuseSignIn(username)
	}
	if err != nil {
		recordAuth("error")
		server.logger.Warn("smtp_gateway.auth_error", "error_kind", errorKind(err))
		return replyAuthUnavailable
	}
	if err := server.registry.signIn(s.conn, principal.Viewer.MembershipID); err != nil {
		recordAuth("limited")
		return &smtp.SMTPError{Code: 454, EnhancedCode: smtp.EnhancedCode{4, 7, 0}, Message: capitalize(err.Error())}
	}
	limiter.Succeeded(ctx, username, s.conn.ip)
	recordAuth("success")
	s.principal = principal
	server.logger.Info("smtp_gateway.signed_in", "password_id", principal.PasswordID)
	return nil
}

func (s *session) refuseSignIn(string) error {
	recordAuth("failure")
	s.server.wait(s.server.cfg.FailureDelay)
	return replyAuthFailed
}

// stillValid checks again that the password may sign in, before each message and its data. A
// revoked password ends the connection after the reply.
func (s *session) stillValid() error {
	ctx, cancel := s.server.storeContext()
	defer cancel()
	valid, err := s.server.store.RevalidateClients(ctx, []*domain.MailClientPrincipal{s.principal})
	if err != nil {
		s.server.logger.Warn("smtp_gateway.revalidate_error", "error_kind", errorKind(err))
		return replyTemporary
	}
	if !valid[s.principal.PasswordID] {
		s.principal = nil
		s.conn.expire()
		recordAuth("revoked")
		return replyRevoked
	}
	return nil
}

func (s *session) Mail(from string, opts *smtp.MailOptions) error {
	if s.principal == nil {
		return replyAuthRequired
	}
	if opts != nil && opts.Size > domain.MailClientMaxSubmissionBytes {
		return replyTooLarge("The message exceeds 8 MiB")
	}
	if err := s.stillValid(); err != nil {
		return err
	}
	address, ok := normalizeAddress(from)
	if !ok {
		return replySenderNotAllowed
	}
	ctx, cancel := s.server.storeContext()
	defer cancel()
	allowed, err := s.server.store.ClientSenderAllowed(ctx, s.principal, address)
	if err != nil {
		s.server.logger.Warn("smtp_gateway.sender_check_error", "error_kind", errorKind(err))
		return replyTemporary
	}
	if !allowed {
		recordSubmission(string(domain.MailClientSubmitSenderNotAllowed))
		return replySenderNotAllowed
	}
	s.from = address
	return nil
}

func (s *session) Rcpt(to string, _ *smtp.RcptOptions) error {
	if s.principal == nil {
		return replyAuthRequired
	}
	if s.rcptCommands++; s.rcptCommands > maxRcptCommands {
		return replyTooManyRecipients
	}
	address, ok := normalizeAddress(to)
	if !ok {
		return replyBadRecipient
	}
	if s.seen[address] {
		return nil
	}
	if len(s.recipients) >= domain.MaxMailRecipients {
		return replyTooManyRecipients
	}
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	s.seen[address] = true
	s.recipients = append(s.recipients, address)
	return nil
}

func (s *session) Data(r io.Reader) error {
	s.conn.busy.Store(true)
	defer s.conn.busy.Store(false)
	if s.principal == nil {
		return replyAuthRequired
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		var smtpErr *smtp.SMTPError
		if errors.As(err, &smtpErr) {
			recordSubmission(string(domain.MailClientSubmitTooLarge))
			return smtpErr
		}
		return replyTemporary
	}
	if validErr := s.stillValid(); validErr != nil {
		return validErr
	}
	ctx, cancel := s.server.storeContext()
	defer cancel()
	start := time.Now()
	result, err := s.server.store.SubmitClientMessage(ctx, s.principal, domain.MailClientSubmission{
		EnvelopeFrom: s.from, Recipients: append([]string(nil), s.recipients...), Raw: raw,
	})
	observeSubmission(start)
	if err != nil {
		reply, outcome := replyFor(err)
		recordSubmission(outcome)
		if outcome == outcomeError {
			s.server.logger.Warn("smtp_gateway.submit_error", "error_kind", errorKind(err))
		} else {
			s.server.logger.Info("smtp_gateway.submit_refused", "password_id", s.principal.PasswordID, "outcome", outcome)
		}
		return reply
	}
	recordSubmission(string(result.Status))
	s.server.logger.Info("smtp_gateway.submitted", "password_id", s.principal.PasswordID,
		"message_id", result.MessageID, "status", string(result.Status), "recipients", len(s.recipients), "bytes", len(raw))
	return replyAccepted(result)
}

func (s *session) Reset() {
	s.from, s.recipients, s.seen, s.rcptCommands = "", nil, nil, 0
}

func (s *session) Logout() error { return nil }

// normalizeAddress lowers a bare envelope address and checks its form; the store checks it again.
func normalizeAddress(raw string) (string, bool) {
	address := strings.TrimSpace(raw)
	if address == "" || len(address) > 320 || strings.ContainsAny(address, "<>\" \t\r\n") {
		return "", false
	}
	parsed, err := mail.ParseAddress(address)
	if err != nil || parsed.Name != "" {
		return "", false
	}
	at := strings.LastIndex(parsed.Address, "@")
	if at <= 0 || at == len(parsed.Address)-1 || !strings.Contains(parsed.Address[at+1:], ".") {
		return "", false
	}
	return strings.ToLower(parsed.Address), true
}

// wait sleeps for d unless the server stops first.
func (s *Server) wait(d time.Duration) {
	if d <= 0 {
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-s.ctx.Done():
	}
}
