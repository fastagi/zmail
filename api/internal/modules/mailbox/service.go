package mailbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fastagi/fastmail/api/internal/domain"
	"github.com/fastagi/fastmail/api/internal/infra/config"
	"github.com/fastagi/fastmail/api/internal/infra/mailtransport"
)

const (
	// MaxInboundBytes bounds one raw inbound message accepted from the mail edge.
	MaxInboundBytes = 5 * 1024 * 1024

	sendLimitWindow       = 24 * time.Hour
	deliveryRecordTimeout = 5 * time.Second
	failureNotConfigured  = "transport.not_configured"
	failureUnknown        = "transport.failed"
)

// Settings is the resolved policy the service runs with.
type Settings struct {
	Domain           string
	InboundSecret    string
	InboundTolerance time.Duration
	DailySendLimit   int
}

// InboundDelivery is one signed delivery from the mail edge.
type InboundDelivery struct {
	Recipient string
	Signature string
	Raw       []byte
}

// SendInput is a validated request to send one message.
type SendInput struct {
	To        []string
	Cc        []string
	Subject   string
	Text      string
	HTML      string
	InReplyTo string
}

// Service defines the business interface of the mailbox starter.
type Service interface {
	Domain() string
	CreateMailbox(ctx context.Context, userID uint, localPart, displayName string) (*domain.Mailbox, error)
	ListMailboxes(ctx context.Context, userID uint) ([]*domain.Mailbox, error)
	DeleteMailbox(ctx context.Context, userID, mailboxID uint) error
	ListMessages(ctx context.Context, userID, mailboxID uint, filter domain.MailMessageFilter) ([]*domain.MailMessage, int64, error)
	GetMessage(ctx context.Context, userID, mailboxID, messageID uint) (*domain.MailMessage, error)
	SendMessage(ctx context.Context, userID, mailboxID uint, input SendInput) (*domain.MailMessage, error)
	SetMessageRead(ctx context.Context, userID, mailboxID, messageID uint, read bool) (*domain.MailMessage, error)
	DeleteMessage(ctx context.Context, userID, mailboxID, messageID uint) error
	ReceiveInbound(ctx context.Context, delivery InboundDelivery) (message *domain.MailMessage, created bool, err error)
}

type service struct {
	repo      domain.MailboxRepository
	transport mailtransport.Transport
	settings  Settings
	now       func() time.Time
}

var _ Service = (*service)(nil)

// NewService creates the mailbox service.
func NewService(repo domain.MailboxRepository, transport mailtransport.Transport, settings Settings) *service {
	return &service{repo: repo, transport: transport, settings: settings, now: time.Now}
}

// SettingsFromConfig resolves the service policy from typed configuration.
func SettingsFromConfig(cfg *config.Config) Settings {
	settings := Settings{
		Domain:           config.DefaultMailboxDomain,
		InboundTolerance: config.DefaultMailboxInboundTolerance,
		DailySendLimit:   config.DefaultMailboxDailySendLimit,
	}
	if cfg == nil {
		return settings
	}
	resolved := cfg.Mailbox.Resolved(cfg.IsProduction())
	settings.Domain = resolved.Domain
	settings.InboundSecret = resolved.InboundSecret
	if resolved.DailySendLimit > 0 {
		settings.DailySendLimit = resolved.DailySendLimit
	}
	return settings
}

func (s *service) Domain() string {
	return s.settings.Domain
}

func (s *service) CreateMailbox(ctx context.Context, userID uint, localPart, displayName string) (*domain.Mailbox, error) {
	normalized, err := normalizeLocalPart(localPart)
	if err != nil {
		return nil, err
	}
	displayName = strings.Join(strings.Fields(displayName), " ")
	if len(displayName) > 120 || strings.ContainsAny(displayName, "<>\"") {
		return nil, fmt.Errorf("%w: display name", ErrMailboxAddressInvalid)
	}
	mailbox := &domain.Mailbox{
		UserID:      userID,
		Address:     normalized + "@" + s.settings.Domain,
		DisplayName: displayName,
	}
	if err := s.repo.CreateMailbox(ctx, mailbox, domain.MaxMailboxesPerUser); err != nil {
		return nil, err
	}
	return mailbox, nil
}

func (s *service) ListMailboxes(ctx context.Context, userID uint) ([]*domain.Mailbox, error) {
	return s.repo.ListMailboxes(ctx, userID)
}

func (s *service) DeleteMailbox(ctx context.Context, userID, mailboxID uint) error {
	return s.repo.DeleteMailbox(ctx, userID, mailboxID)
}

// AccountDeletionCleanerName identifies this starter in the account deletion policy.
func (s *service) AccountDeletionCleanerName() string {
	return config.StarterMailbox
}

// CleanAccountDeletion removes a deleted account's mailboxes and messages inside the account
// transaction. The addresses stay reserved, and later mail for them is bounced as unknown.
func (s *service) CleanAccountDeletion(ctx context.Context, userID uint) error {
	return s.repo.DeleteMailboxesForUser(ctx, userID)
}

func (s *service) ListMessages(
	ctx context.Context,
	userID, mailboxID uint,
	filter domain.MailMessageFilter,
) ([]*domain.MailMessage, int64, error) {
	if filter.Direction != "" && !filter.Direction.Valid() {
		return nil, 0, domain.ErrInvalidInput
	}
	if _, err := s.repo.FindMailbox(ctx, userID, mailboxID); err != nil {
		return nil, 0, err
	}
	return s.repo.ListMessages(ctx, mailboxID, filter)
}

func (s *service) GetMessage(ctx context.Context, userID, mailboxID, messageID uint) (*domain.MailMessage, error) {
	if _, err := s.repo.FindMailbox(ctx, userID, mailboxID); err != nil {
		return nil, err
	}
	return s.repo.FindMessage(ctx, mailboxID, messageID)
}

func (s *service) SetMessageRead(ctx context.Context, userID, mailboxID, messageID uint, read bool) (*domain.MailMessage, error) {
	if _, err := s.repo.FindMailbox(ctx, userID, mailboxID); err != nil {
		return nil, err
	}
	var readAt *time.Time
	if read {
		now := s.now().UTC()
		readAt = &now
	}
	if err := s.repo.SetMessageRead(ctx, mailboxID, messageID, readAt); err != nil {
		return nil, err
	}
	return s.repo.FindMessage(ctx, mailboxID, messageID)
}

func (s *service) DeleteMessage(ctx context.Context, userID, mailboxID, messageID uint) error {
	if _, err := s.repo.FindMailbox(ctx, userID, mailboxID); err != nil {
		return err
	}
	return s.repo.DeleteMessage(ctx, mailboxID, messageID)
}

// SendMessage stores the message as queued, calls the provider, and records the real outcome.
// A provider failure keeps the message with status "failed" and returns ErrMailDeliveryFailed
// together with the stored message, so a caller never sees a failed send reported as sent.
func (s *service) SendMessage(ctx context.Context, userID, mailboxID uint, input SendInput) (*domain.MailMessage, error) {
	mailbox, err := s.repo.FindMailbox(ctx, userID, mailboxID)
	if err != nil {
		return nil, err
	}
	message, err := s.buildOutbound(mailbox, input)
	if err != nil {
		return nil, err
	}

	sent, err := s.repo.CountOutboundSince(ctx, userID, s.now().Add(-sendLimitWindow))
	if err != nil {
		return nil, err
	}
	if sent >= int64(s.settings.DailySendLimit) {
		return nil, ErrMailSendLimitReached
	}

	if err := s.repo.CreateMessage(ctx, message); err != nil {
		return nil, err
	}

	sendErr := s.transport.Send(ctx, mailtransport.Message{
		FromAddress: message.FromAddress,
		FromName:    message.FromName,
		To:          message.To,
		Cc:          message.Cc,
		Subject:     message.Subject,
		TextBody:    message.TextBody,
		HTMLBody:    message.HTMLBody,
		InReplyTo:   message.InReplyTo,
	})

	message.Status = domain.MailStatusSent
	var result error
	if sendErr != nil {
		message.Status = domain.MailStatusFailed
		message.FailureCode, result = classifySendError(sendErr)
	}

	// Record the outcome even when the caller has gone away: a canceled request must not
	// leave a delivered message looking queued.
	recordContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), deliveryRecordTimeout)
	defer cancel()
	if err := s.repo.UpdateMessageDelivery(recordContext, message.ID, message.Status, message.MessageID, message.FailureCode); err != nil {
		return nil, err
	}
	return message, result
}

func classifySendError(err error) (string, error) {
	if errors.Is(err, mailtransport.ErrNotConfigured) {
		return failureNotConfigured, ErrMailSenderNotConfigured
	}
	var transportErr *mailtransport.Error
	if errors.As(err, &transportErr) && transportErr.Code != "" {
		return transportErr.Code, fmt.Errorf("%w: %s", ErrMailDeliveryFailed, transportErr.Code)
	}
	return failureUnknown, ErrMailDeliveryFailed
}

func (s *service) buildOutbound(mailbox *domain.Mailbox, input SendInput) (*domain.MailMessage, error) {
	seen := map[string]struct{}{}
	to, ok := normalizeRecipients(input.To, seen)
	if !ok || len(to) == 0 {
		return nil, fmt.Errorf("%w: recipients", ErrMailMessageInvalid)
	}
	cc, ok := normalizeRecipients(input.Cc, seen)
	if !ok || len(to)+len(cc) > domain.MaxMailRecipients {
		return nil, fmt.Errorf("%w: recipients", ErrMailMessageInvalid)
	}

	subject := strings.TrimSpace(input.Subject)
	if subject == "" || len(subject) > domain.MaxMailSubjectLength || strings.ContainsAny(subject, "\r\n\x00") {
		return nil, fmt.Errorf("%w: subject", ErrMailMessageInvalid)
	}
	text, html := cleanText(input.Text), cleanText(input.HTML)
	if strings.TrimSpace(text) == "" && strings.TrimSpace(html) == "" {
		return nil, fmt.Errorf("%w: body", ErrMailMessageInvalid)
	}
	if len(text) > domain.MaxMailBodyBytes || len(html) > domain.MaxMailBodyBytes {
		return nil, fmt.Errorf("%w: body", ErrMailMessageInvalid)
	}
	inReplyTo, ok := normalizeMessageID(input.InReplyTo)
	if !ok {
		return nil, fmt.Errorf("%w: in_reply_to", ErrMailMessageInvalid)
	}

	now := s.now().UTC()
	return &domain.MailMessage{
		MailboxID:   mailbox.ID,
		Direction:   domain.MailDirectionOutbound,
		Status:      domain.MailStatusQueued,
		FromAddress: mailbox.Address,
		FromName:    mailbox.DisplayName,
		To:          to,
		Cc:          cc,
		Subject:     cleanText(subject),
		TextBody:    text,
		HTMLBody:    html,
		InReplyTo:   inReplyTo,
		// The owner wrote it, so it starts out read.
		ReadAt: &now,
	}, nil
}

// ReceiveInbound verifies and stores one delivery from the mail edge. A repeated delivery of
// the same message to the same mailbox returns the stored message with created=false.
func (s *service) ReceiveInbound(ctx context.Context, delivery InboundDelivery) (*domain.MailMessage, bool, error) {
	if s.settings.InboundSecret == "" {
		return nil, false, ErrMailInboundDisabled
	}
	if len(delivery.Raw) == 0 {
		return nil, false, ErrMailInboundUnparseable
	}
	if len(delivery.Raw) > MaxInboundBytes {
		return nil, false, ErrMailInboundTooLarge
	}
	recipient := strings.ToLower(strings.TrimSpace(delivery.Recipient))
	// Verify before any lookup, so an unsigned caller cannot probe which addresses exist.
	if !verifyInbound(s.settings.InboundSecret, delivery.Signature, recipient, delivery.Raw, s.now(), s.settings.InboundTolerance) {
		return nil, false, ErrMailInboundSignature
	}

	mailbox, err := s.repo.FindMailboxByAddress(ctx, recipient)
	if err != nil {
		if errors.Is(err, ErrMailboxNotFound) {
			return nil, false, ErrMailRecipientNotFound
		}
		return nil, false, err
	}
	parsed, err := parseMail(delivery.Raw)
	if err != nil {
		return nil, false, err
	}
	if parsed.FromAddress == "" {
		return nil, false, ErrMailInboundUnparseable
	}

	to := parsed.To
	if len(to) == 0 {
		to = []string{recipient}
	}
	message := &domain.MailMessage{
		MailboxID:   mailbox.ID,
		Direction:   domain.MailDirectionInbound,
		Status:      domain.MailStatusReceived,
		FromAddress: parsed.FromAddress,
		FromName:    parsed.FromName,
		To:          to,
		Cc:          parsed.Cc,
		Subject:     parsed.Subject,
		TextBody:    parsed.TextBody,
		HTMLBody:    parsed.HTMLBody,
		MessageID:   parsed.MessageID,
		InReplyTo:   parsed.InReplyTo,
		DedupeKey:   inboundDedupeKey(parsed.MessageID, delivery.Raw),
	}
	created, err := s.repo.CreateInboundMessage(ctx, message)
	if err != nil {
		return nil, false, err
	}
	return message, created, nil
}

// inboundDedupeKey identifies a message by its Message-ID, or by its content when the sender
// set none, so an edge retry never stores the same message twice.
func inboundDedupeKey(messageID string, raw []byte) string {
	var sum [sha256.Size]byte
	if messageID != "" {
		sum = sha256.Sum256([]byte("id:" + messageID))
	} else {
		sum = sha256.Sum256(raw)
	}
	return hex.EncodeToString(sum[:])
}
