package mailbox

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/fastagi/zmail/api/internal/capabilities/crypto"
	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/metrics"
	"github.com/fastagi/zmail/api/internal/modules/mailbox/transactional"
)

// TransactionalDirectSendInput is one direct (template-free) email send request via POST /v1/emails.
type TransactionalDirectSendInput struct {
	From           string
	To             string
	Subject        string
	HTML           string
	Text           string
	IdempotencyKey string
}

// SendDirectTransactional sends one direct HTML/text email through the transactional delivery
// pipeline without requiring a mailbox or template. IdempotencyKey is optional; when omitted, a
// unique key is generated.
func (s *service) SendDirectTransactional(ctx context.Context, actor TransactionalActor, input TransactionalDirectSendInput) (*domain.TransactionalMessage, bool, error) {
	if !s.transactionalEnabled() {
		return nil, false, ErrTransactionalDisabled
	}
	if !actor.Mode.Valid() {
		return nil, false, domain.ErrPermissionDenied
	}
	idempotencyKey := strings.TrimSpace(input.IdempotencyKey)
	if idempotencyKey == "" {
		idempotencyKey = generateDirectIdempotencyKey()
	} else if !validIdempotencyKey(idempotencyKey) {
		return nil, false, ErrMailIdempotencyKeyInvalid
	}

	fromAddress, fromName, to, subject, err := s.validateDirectSendInput(input)
	if err != nil {
		return nil, false, err
	}

	if actor.APIKeyID != nil {
		bucket := "key:" + strconv.FormatUint(uint64(*actor.APIKeyID), 10)
		if err := take(ctx, s.transactionalLimits.key, ruleTransactionalKey, bucket, ErrTransactionalRateLimited); err != nil {
			return nil, false, err
		}
	}

	if err := s.authorizeDirectSender(ctx, actor, fromAddress); err != nil {
		return nil, false, err
	}

	_, simulatedAddress, ok := simulatorOutcome(to, s.settings.simulatorDomain())
	if !ok {
		return nil, false, &FieldsError{Err: ErrMailMessageInvalid, Fields: map[string][]string{"to": {"names no simulator outcome"}}}
	}
	simulated := simulatedAddress || actor.Mode == domain.APIKeyModeTest
	fingerprint := transactional.DirectFingerprint(s.fingerprintKey, fromAddress, fromName, to, subject, input.HTML, input.Text)

	var expired *domain.TransactionalMessage
	existing, err := s.transactional.FindTransactionalByIdempotencyKey(ctx, actor.OrganizationID, actor.Mode, idempotencyKey)
	switch {
	case errors.Is(err, ErrTransactionalMessageNotFound):
	case err != nil:
		return nil, false, err
	case !existing.CreatedAt.Before(s.now().Add(-transactionalIdempotencyWindow)):
		replay, replayErr := s.replayTransactional(ctx, existing, fingerprint)
		return replay, false, replayErr
	default:
		expired = existing
	}

	recipientBucket := crypto.SHA256Hex(fmt.Sprintf("%d:%s:direct:%s", actor.OrganizationID, actor.Mode, to))
	if err := take(ctx, s.transactionalLimits.recipient, ruleTransactionalRecipient, recipientBucket, ErrMailRecipientRateLimited); err != nil {
		return nil, false, err
	}

	return s.persistDirectTransactional(ctx, directSendRecord{
		actor:          actor,
		fromAddress:    fromAddress,
		fromName:       fromName,
		to:             to,
		subject:        subject,
		html:           input.HTML,
		text:           input.Text,
		simulated:      simulated,
		idempotencyKey: idempotencyKey,
		fingerprint:    fingerprint,
		expired:        expired,
	})
}

type directSendRecord struct {
	actor          TransactionalActor
	fromAddress    string
	fromName       string
	to             string
	subject        string
	html           string
	text           string
	simulated      bool
	idempotencyKey string
	fingerprint    []byte
	expired        *domain.TransactionalMessage
}

func (s *service) persistDirectTransactional(ctx context.Context, rec directSendRecord) (*domain.TransactionalMessage, bool, error) {
	actor := rec.actor
	suppressed := false
	if !rec.simulated {
		var err error
		if suppressed, err = s.transactional.IsSuppressed(ctx, actor.OrganizationID, rec.to); err != nil {
			return nil, false, err
		}
	}
	now := s.now().UTC()
	key := rec.idempotencyKey
	message := &domain.TransactionalMessage{
		OrganizationID:         actor.OrganizationID,
		Mode:                   actor.Mode,
		Simulated:              rec.simulated,
		APIKeyID:               actor.APIKeyID,
		CreatedBy:              actor.UserID,
		TemplateID:             nil,
		TemplateKey:            "",
		FromAddress:            rec.fromAddress,
		FromName:               rec.fromName,
		ToAddress:              rec.to,
		SubjectMasked:          rec.subject,
		VariablesMasked:        map[string]string{},
		IdempotencyKey:         &key,
		IdempotencyFingerprint: rec.fingerprint,
		Status:                 domain.TransactionalStatusQueued,
		RFCMessageID:           newRFCMessageID(addressDomain(rec.fromAddress)),
		CreatedAt:              now,
		UpdatedAt:              now,
	}
	if suppressed {
		message.Status = domain.TransactionalStatusSuppressed
	} else {
		payload, err := json.Marshal(transactionalPayload{Subject: rec.subject, HTML: rec.html, Text: rec.text})
		if err != nil {
			return nil, false, err
		}
		if message.PayloadCiphertext, err = s.encryptor.Encrypt(payload); err != nil {
			return nil, false, err
		}
		message.NextAttemptAt = &now
	}

	var replay, created *domain.TransactionalMessage
	err := s.applyTransactional(ctx, func(tx context.Context) ([]TransactionalNotification, error) {
		replay, created = nil, nil
		if rec.expired != nil {
			if err := s.transactional.ReleaseIdempotencyKey(tx, rec.expired.ID); err != nil {
				return nil, err
			}
		}
		stored := *message
		if message.CountsInLane() && !suppressed {
			reason, err := s.transactionalLaneReason(tx, actor.OrganizationID, now)
			if err != nil {
				return nil, err
			}
			stored.QueuedReason = reason
		}
		inserted, err := s.transactional.CreateTransactional(tx, &stored)
		if err != nil {
			return nil, err
		}
		if !inserted {
			winner, err := s.transactional.FindTransactionalByIdempotencyKey(tx, actor.OrganizationID, actor.Mode, key)
			if err != nil {
				return nil, err
			}
			if !hmac.Equal(winner.IdempotencyFingerprint, rec.fingerprint) {
				return nil, ErrMailIdempotencyConflict
			}
			replay = winner
			return nil, nil
		}
		created = &stored
		if !suppressed {
			return nil, nil
		}
		return []TransactionalNotification{{
			Type: WebhookEmailSuppressed, Message: created, Reason: "suppressed", OccurredAt: now,
		}}, nil
	})
	if err != nil {
		return nil, false, err
	}
	if replay != nil {
		replay, err = s.withTransactionalEvents(ctx, replay)
		return replay, false, err
	}
	message = created
	slog.InfoContext(ctx, "transactional_message.accepted",
		slog.Uint64("organization_id", uint64(message.OrganizationID)),
		slog.Uint64("message_id", uint64(message.ID)),
		slog.Uint64("template_id", 0),
		slog.String("mode", string(message.Mode)),
		slog.String("status", string(message.Status)))
	message.Events = []domain.MailEvent{}
	if message.Status == domain.TransactionalStatusSuppressed {
		metrics.RecordTransactionalMessage(string(message.Mode), string(message.Status))
	}
	s.dispatchTransactionalSoon(ctx, message)
	return message, true, nil
}

func (s *service) authorizeDirectSender(ctx context.Context, actor TransactionalActor, fromAddress string) error {
	if err := s.requireDeveloperSender(ctx, actor.Viewer, fromAddress); err != nil {
		return err
	}
	if err := requireAPIKeySendingDomain(ctx, fromAddress); err != nil {
		return err
	}
	restriction := domain.APIKeyRestrictionFromContext(ctx)
	if (restriction.OrganizationID > 0 || restriction.SendingDomains != nil) &&
		s.isSharedDomain(addressDomain(fromAddress)) {
		return domain.ErrAPIKeyDomainNotAllowed
	}
	return s.requireTemplateSender(ctx, actor.OrganizationID, fromAddress)
}

func (s *service) validateDirectSendInput(input TransactionalDirectSendInput) (fromAddress, fromName, to, subject string, err error) {
	fields := &ValidationError{}
	var ok bool
	fromAddress, fromName, ok = parseDirectSender(input.From)
	if !ok {
		fields.add("from", "must be a valid sender address")
	}
	subject = strings.TrimSpace(input.Subject)
	if subject == "" {
		fields.add("subject", "is required")
	} else if utf8.RuneCountInString(subject) > domain.MaxMailSubjectLength || strings.ContainsAny(subject, "\r\n") {
		fields.add("subject", "is invalid or too long")
	}
	if strings.TrimSpace(input.HTML) == "" && strings.TrimSpace(input.Text) == "" {
		fields.add("html", "html or text is required")
	}
	if len(fields.Fields) > 0 {
		return "", "", "", "", fields
	}
	to, ok = normalizeRecipient(input.To)
	if !ok || len(to) < 3 {
		return "", "", "", "", &FieldsError{Err: ErrMailMessageInvalid, Fields: map[string][]string{"to": {"must be one bare address"}}}
	}
	return fromAddress, fromName, to, subject, nil
}

// parseDirectSender parses "Display Name <user@domain>" or "user@domain" into a normalized address
// and cleaned display name.
func parseDirectSender(raw string) (address, name string, ok bool) {
	candidate := strings.TrimSpace(raw)
	if candidate == "" || len(candidate) > 320 || strings.ContainsAny(candidate, "\r\n") {
		return "", "", false
	}
	parsed, err := mail.ParseAddress(candidate)
	if err != nil {
		return "", "", false
	}
	address, ok = normalizeRecipient(parsed.Address)
	if !ok {
		return "", "", false
	}
	name = strings.Join(strings.Fields(parsed.Name), " ")
	if utf8.RuneCountInString(name) > maxTemplateFromName || strings.ContainsAny(name, "<>\"") ||
		strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", "", false
	}
	return address, name, true
}

func generateDirectIdempotencyKey() string {
	var random [16]byte
	_, _ = rand.Read(random[:])
	return "email:" + hex.EncodeToString(random[:])
}
