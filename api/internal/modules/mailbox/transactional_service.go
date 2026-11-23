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
	"sort"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/fastagi/zmail/api/internal/capabilities/crypto"
	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/metrics"
	"github.com/fastagi/zmail/api/internal/infra/ratelimit"
)

const (
	// transactionalIdempotencyWindow is how long an Idempotency-Key names its first message.
	transactionalIdempotencyWindow = 24 * time.Hour
	// maxTransactionalEvents bounds the events a read returns.
	maxTransactionalEvents = 50
	// transactionalTestSendsPerMinute bounds console test sends of one user.
	transactionalTestSendsPerMinute = 5

	ruleTransactionalKey       = "mail.transactional.key"
	ruleTransactionalRecipient = "mail.transactional.recipient"
	ruleTransactionalTestSend  = "mail.transactional.test_send"
)

// Transactional errors.
var (
	// ErrTransactionalDisabled means the instance does not accept transactional messages.
	ErrTransactionalDisabled = errors.New("transactional mail is not enabled")
	// ErrMailRecipientRateLimited means one template went to one recipient too often.
	ErrMailRecipientRateLimited = errors.New("too many transactional messages to this recipient")
	// ErrTransactionalRateLimited means an API key or a user sent too many messages in a window.
	ErrTransactionalRateLimited = errors.New("too many transactional messages")
)

// RetryAfterError is a refusal that may be retried after RetryAfter.
type RetryAfterError struct {
	Err        error
	RetryAfter time.Duration
}

func (e *RetryAfterError) Error() string { return e.Err.Error() }

func (e *RetryAfterError) Unwrap() error { return e.Err }

// FieldsError is a refusal with a reason per request field, answered with the code of Err.
type FieldsError struct {
	Err    error
	Fields map[string][]string
}

func (e *FieldsError) Error() string {
	names := make([]string, 0, len(e.Fields))
	for name := range e.Fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return fmt.Sprintf("%s: %v", e.Err.Error(), names)
}

func (e *FieldsError) Unwrap() error { return e.Err }

// accountDirectory reads the account a member signed in with, for console test sends.
type accountDirectory interface {
	FindByID(ctx context.Context, id uint) (*domain.User, error)
}

// TransactionalActor is who sends: an API key of a member, or a member in the console.
type TransactionalActor struct {
	OrganizationID uint
	UserID         uint
	// APIKeyID is nil for a console test send.
	APIKeyID *uint
	Mode     domain.APIKeyMode
}

// TransactionalSendInput is one send request of an API key.
type TransactionalSendInput struct {
	Template       string
	To             string
	IdempotencyKey string
	Variables      map[string]string
}

// TransactionalTestInput is one console test send to the caller's own address.
type TransactionalTestInput struct {
	Template  string
	Mode      domain.APIKeyMode
	Variables map[string]string
}

// TransactionalNotification is a webhook about a transactional message.
type TransactionalNotification struct {
	Type       string
	Message    *domain.TransactionalMessage
	BounceType string
	Reason     string
	// Key distinguishes several notifications of one type for one message; empty when once.
	Key        string
	OccurredAt time.Time
}

// TransactionalPolicy is the instance's transactional mail configuration as members see it.
type TransactionalPolicy struct {
	Enabled            bool
	RecipientPerMinute int
	KeyPerSecond       int
	SimulatorDomain    string
}

// transactionalLimits are the fixed-window limits of transactional sends.
type transactionalLimits struct {
	key       ratelimit.Store
	recipient ratelimit.Store
	testSend  ratelimit.Store
}

func newTransactionalLimits(stores *ratelimit.Stores, settings Settings) transactionalLimits {
	if stores == nil {
		stores = ratelimit.NewMemoryStores()
	}
	return transactionalLimits{
		key: stores.New(ratelimit.Rule{
			Name: ruleTransactionalKey, Max: settings.transactionalKeyPerSecond(), Window: time.Second,
		}),
		recipient: stores.New(ratelimit.Rule{
			Name: ruleTransactionalRecipient, Max: settings.transactionalRecipientPerMinute(), Window: time.Minute,
		}),
		testSend: stores.New(ratelimit.Rule{
			Name: ruleTransactionalTestSend, Max: transactionalTestSendsPerMinute, Window: time.Minute,
		}),
	}
}

func (settings Settings) transactionalKeyPerSecond() int {
	if settings.TransactionalKeyPerSecond > 0 {
		return settings.TransactionalKeyPerSecond
	}
	return config.DefaultMailboxTransactionalKeyPerSecond
}

func (settings Settings) transactionalRecipientPerMinute() int {
	if settings.TransactionalRecipientPerMinute > 0 {
		return settings.TransactionalRecipientPerMinute
	}
	return config.DefaultMailboxTransactionalRecipientPerMinute
}

func (settings Settings) simulatorDomain() string {
	if settings.SimulatorDomain != "" {
		return settings.SimulatorDomain
	}
	return config.DefaultMailboxSimulatorDomain
}

// TransactionalPolicy reports the transactional switch, rate limits, and simulator domain.
func (s *service) TransactionalPolicy() TransactionalPolicy {
	return TransactionalPolicy{
		Enabled:            s.transactionalEnabled(),
		RecipientPerMinute: s.settings.transactionalRecipientPerMinute(),
		KeyPerSecond:       s.settings.transactionalKeyPerSecond(),
		SimulatorDomain:    s.settings.simulatorDomain(),
	}
}

// transactionalEnabled is the instance switch; it also needs the encryption key and storage.
func (s *service) transactionalEnabled() bool {
	return s.settings.TransactionalEnabled && s.settings.SecretEncryptionKey != "" && s.transactional != nil
}

// take consumes one unit of the limit of a rule, or counts the refusal and refuses with the time
// until its window has room.
func take(ctx context.Context, store ratelimit.Store, rule, bucket string, refusal error) error {
	allowed, _, resetAt := store.Take(ctx, bucket)
	if allowed {
		return nil
	}
	metrics.RecordTransactionalRateLimited(rule)
	return &RetryAfterError{Err: refusal, RetryAfter: time.Until(resetAt)}
}

// transactionalSend is a validated send waiting for its remaining checks.
type transactionalSend struct {
	actor          TransactionalActor
	template       *domain.MailTemplate
	parsed         *parsedTemplate
	to             string
	values         map[string]string
	simulated      bool
	idempotencyKey string
	fingerprint    []byte
	// expired is the message holding the key whose 24 hours passed; its key is released.
	expired *domain.TransactionalMessage
}

// SendTransactional sends one message for an API key, in the order of the contract: switch,
// key, per-key limit, template and variables, idempotent replay, per-recipient limit, sender,
// required values, suppression, and lane. It reports created=false for a replay.
func (s *service) SendTransactional(ctx context.Context, actor TransactionalActor, input TransactionalSendInput) (*domain.TransactionalMessage, bool, error) {
	if !s.transactionalEnabled() {
		return nil, false, ErrTransactionalDisabled
	}
	if input.IdempotencyKey == "" || !validIdempotencyKey(input.IdempotencyKey) {
		return nil, false, ErrMailIdempotencyKeyInvalid
	}
	if !actor.Mode.Valid() {
		return nil, false, domain.ErrPermissionDenied
	}
	if err := validateTransactionalRequest(input.Template, input.Variables); err != nil {
		return nil, false, err
	}
	if actor.APIKeyID != nil {
		bucket := "key:" + strconv.FormatUint(uint64(*actor.APIKeyID), 10)
		if err := take(ctx, s.transactionalLimits.key, ruleTransactionalKey, bucket, ErrTransactionalRateLimited); err != nil {
			return nil, false, err
		}
	}
	template, parsed, err := s.loadTransactionalTemplate(ctx, actor.OrganizationID, input.Template)
	if err != nil {
		return nil, false, err
	}
	send, err := s.newTransactionalSend(actor, template, parsed, input.To, input.Variables, input.IdempotencyKey)
	if err != nil {
		return nil, false, err
	}
	existing, err := s.transactional.FindTransactionalByIdempotencyKey(ctx, actor.OrganizationID, actor.Mode, input.IdempotencyKey)
	switch {
	case errors.Is(err, ErrTransactionalMessageNotFound):
	case err != nil:
		return nil, false, err
	case !existing.CreatedAt.Before(s.now().Add(-transactionalIdempotencyWindow)):
		replay, replayErr := s.replayTransactional(ctx, existing, send.fingerprint)
		return replay, false, replayErr
	default:
		send.expired = existing
	}
	return s.sendTransactional(ctx, send)
}

// SendTransactionalTest sends a transactional template to the caller's own account address. The
// recipient cannot be chosen, so the console cannot mail arbitrary addresses.
func (s *service) SendTransactionalTest(ctx context.Context, viewer domain.OrganizationContext, input TransactionalTestInput) (*domain.TransactionalMessage, error) {
	if !s.transactionalEnabled() {
		return nil, ErrTransactionalDisabled
	}
	if !input.Mode.Valid() {
		return nil, &ValidationError{Fields: map[string][]string{"mode": {"must be live or test"}}}
	}
	if err := validateTransactionalRequest(input.Template, input.Variables); err != nil {
		return nil, err
	}
	bucket := "user:" + strconv.FormatUint(uint64(viewer.UserID), 10)
	if err := take(ctx, s.transactionalLimits.testSend, ruleTransactionalTestSend, bucket, ErrTransactionalRateLimited); err != nil {
		return nil, err
	}
	if s.accounts == nil {
		return nil, domain.ErrServiceUnavailable
	}
	account, err := s.accounts.FindByID(ctx, viewer.UserID)
	if err != nil {
		return nil, err
	}
	template, parsed, err := s.loadTransactionalTemplate(ctx, viewer.OrganizationID, input.Template)
	if err != nil {
		return nil, err
	}
	declared := make(map[string]string, len(template.Variables))
	for _, variable := range template.Variables {
		if sample, ok := template.SampleVariables[variable.Name]; ok {
			declared[variable.Name] = sample
		}
	}
	var random [16]byte
	// crypto/rand.Read always fills the buffer and never returns an error.
	_, _ = rand.Read(random[:])
	key := "test-send:" + strconv.FormatUint(uint64(viewer.UserID), 10) + ":" + hex.EncodeToString(random[:])
	actor := TransactionalActor{OrganizationID: viewer.OrganizationID, UserID: viewer.UserID, Mode: input.Mode}
	send, err := s.newTransactionalSend(actor, template, parsed, account.Email, mergeValues(declared, input.Variables), key)
	if err != nil {
		return nil, err
	}
	message, _, err := s.sendTransactional(ctx, send)
	return message, err
}

// GetTransactional reads a message of an organization and mode with its first events. A message
// of the other mode is not found, so a test key never learns about live mail.
func (s *service) GetTransactional(ctx context.Context, organizationID uint, mode domain.APIKeyMode, id uint) (*domain.TransactionalMessage, error) {
	if s.transactional == nil {
		return nil, ErrTransactionalMessageNotFound
	}
	message, err := s.transactional.FindTransactional(ctx, organizationID, id)
	if err != nil {
		return nil, err
	}
	if message.Mode != mode {
		return nil, ErrTransactionalMessageNotFound
	}
	return s.withTransactionalEvents(ctx, message)
}

func (s *service) withTransactionalEvents(ctx context.Context, message *domain.TransactionalMessage) (*domain.TransactionalMessage, error) {
	events, err := s.transactional.ListTransactionalEvents(ctx, message.ID, maxTransactionalEvents)
	if err != nil {
		return nil, err
	}
	message.Events = events
	return message, nil
}

// validateTransactionalRequest checks the request shape: the template key's form and the number
// and size of the values.
func validateTransactionalRequest(templateKey string, variables map[string]string) error {
	fields := &ValidationError{}
	if !templateKeyPattern.MatchString(templateKey) {
		fields.add("template", "must be a template key")
	}
	if len(variables) > maxTemplateVariables {
		fields.add("variables", "has more than 100 values")
	}
	for name, value := range variables {
		if utf8.RuneCountInString(value) > maxTemplateVariableValue {
			fields.add("variables."+name, "is longer than 2000 characters")
		}
	}
	if len(fields.Fields) > 0 {
		return fields
	}
	return nil
}

// loadTransactionalTemplate finds the organization's template by key; it must be transactional.
func (s *service) loadTransactionalTemplate(ctx context.Context, organizationID uint, key string) (*domain.MailTemplate, *parsedTemplate, error) {
	template, err := s.templates.FindTemplateByKey(ctx, organizationID, key)
	if err != nil {
		return nil, nil, err
	}
	if template.Kind != domain.MailTemplateTransactional {
		return nil, nil, ErrMailTemplateKindMismatch
	}
	parsed, err := parseStoredTemplate(template)
	if err != nil {
		return nil, nil, err
	}
	return template, parsed, nil
}

// newTransactionalSend normalizes the recipient, checks that every value names a declared
// variable and that a simulator address names an outcome, and computes the fingerprint.
func (s *service) newTransactionalSend(
	actor TransactionalActor,
	template *domain.MailTemplate,
	parsed *parsedTemplate,
	rawTo string,
	variables map[string]string,
	idempotencyKey string,
) (*transactionalSend, error) {
	to, ok := normalizeRecipient(rawTo)
	if !ok || len(to) < 3 {
		return nil, &FieldsError{Err: ErrMailMessageInvalid, Fields: map[string][]string{"to": {"must be one bare address"}}}
	}
	values, ok := cleanTemplateValues(variables)
	if !ok {
		// Only an invalid name gets here; it cannot name a declared variable.
		values = variables
	}
	declared := make(map[string]struct{}, len(template.Variables))
	for _, variable := range template.Variables {
		declared[variable.Name] = struct{}{}
	}
	undeclared := map[string][]string{}
	for name := range values {
		if _, known := declared[name]; !known {
			undeclared["variables."+name] = []string{"is not declared by the template"}
		}
	}
	if len(undeclared) > 0 {
		return nil, &FieldsError{Err: ErrMailMessageInvalid, Fields: undeclared}
	}
	_, simulatedAddress, ok := simulatorOutcome(to, s.settings.simulatorDomain())
	if !ok {
		return nil, &FieldsError{Err: ErrMailMessageInvalid, Fields: map[string][]string{"to": {"names no simulator outcome"}}}
	}
	return &transactionalSend{
		actor:          actor,
		template:       template,
		parsed:         parsed,
		to:             to,
		values:         values,
		simulated:      simulatedAddress || actor.Mode == domain.APIKeyModeTest,
		idempotencyKey: idempotencyKey,
		fingerprint:    transactionalFingerprint(s.fingerprintKey, template.Key, to, values),
	}, nil
}

// replayTransactional answers a repeat of a key: the stored message when the content matches.
func (s *service) replayTransactional(ctx context.Context, existing *domain.TransactionalMessage, fingerprint []byte) (*domain.TransactionalMessage, error) {
	if !hmac.Equal(existing.IdempotencyFingerprint, fingerprint) {
		return nil, ErrMailIdempotencyConflict
	}
	return s.withTransactionalEvents(ctx, existing)
}

// transactionalPayload is the rendered message, stored encrypted while it is queued.
type transactionalPayload struct {
	Subject string `json:"subject"`
	HTML    string `json:"html"`
	Text    string `json:"text"`
}

// sendTransactional runs the checks after the replay and stores the message.
func (s *service) sendTransactional(ctx context.Context, send *transactionalSend) (*domain.TransactionalMessage, bool, error) {
	actor := send.actor
	bucket := crypto.SHA256Hex(fmt.Sprintf("%d:%s:%d:%s", actor.OrganizationID, actor.Mode, send.template.ID, send.to))
	if err := take(ctx, s.transactionalLimits.recipient, ruleTransactionalRecipient, bucket, ErrMailRecipientRateLimited); err != nil {
		return nil, false, err
	}
	if err := s.requireTemplateSender(ctx, actor.OrganizationID, send.template.FromAddress); err != nil {
		return nil, false, err
	}
	rendered := send.parsed.render(send.values)
	if len(rendered.Missing) > 0 {
		return nil, false, &MissingVariablesError{Names: rendered.Missing}
	}
	masked := make(map[string]string, len(send.values))
	for name, value := range send.values {
		masked[name] = value
	}
	for _, variable := range send.template.Variables {
		if value, ok := masked[variable.Name]; ok && variable.Secret {
			masked[variable.Name] = maskSecret(value)
		}
	}
	subjectMasked := send.parsed.render(masked).Subject
	if utf8.RuneCountInString(rendered.Subject) > domain.MaxMailSubjectLength || utf8.RuneCountInString(subjectMasked) > domain.MaxMailSubjectLength {
		return nil, false, &FieldsError{Err: ErrMailMessageInvalid, Fields: map[string][]string{"variables": {"make the subject too long"}}}
	}
	suppressed := false
	if !send.simulated {
		var err error
		if suppressed, err = s.transactional.IsSuppressed(ctx, actor.OrganizationID, send.to); err != nil {
			return nil, false, err
		}
	}
	now := s.now().UTC()
	templateID := send.template.ID
	key := send.idempotencyKey
	message := &domain.TransactionalMessage{
		OrganizationID:         actor.OrganizationID,
		Mode:                   actor.Mode,
		Simulated:              send.simulated,
		APIKeyID:               actor.APIKeyID,
		CreatedBy:              actor.UserID,
		TemplateID:             &templateID,
		TemplateKey:            send.template.Key,
		FromAddress:            send.template.FromAddress,
		FromName:               send.template.FromName,
		ToAddress:              send.to,
		SubjectMasked:          subjectMasked,
		VariablesMasked:        masked,
		IdempotencyKey:         &key,
		IdempotencyFingerprint: send.fingerprint,
		Status:                 domain.TransactionalStatusQueued,
		RFCMessageID:           newRFCMessageID(addressDomain(send.template.FromAddress)),
		CreatedAt:              now,
		UpdatedAt:              now,
	}
	if suppressed {
		message.Status = domain.TransactionalStatusSuppressed
	} else {
		payload, err := json.Marshal(transactionalPayload{Subject: rendered.Subject, HTML: rendered.HTML, Text: rendered.Text})
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
		if send.expired != nil {
			if err := s.transactional.ReleaseIdempotencyKey(tx, send.expired.ID); err != nil {
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
			if !hmac.Equal(winner.IdempotencyFingerprint, send.fingerprint) {
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
		slog.Uint64("template_id", uint64(templateID)),
		slog.String("mode", string(message.Mode)),
		slog.String("status", string(message.Status)))
	message.Events = []domain.MailEvent{}
	if message.Status == domain.TransactionalStatusSuppressed {
		// Final at acceptance: counted here, once; a replay returns before this point.
		metrics.RecordTransactionalMessage(string(message.Mode), string(message.Status))
	}
	s.dispatchTransactionalSoon(ctx, message)
	return message, true, nil
}

// transactionalLaneReason decides whether a live message waits for the lane: nil when the lane
// has room, send_limit when it is full, and ErrMailSendLimitReached when as many messages already
// wait as the lane allows.
func (s *service) transactionalLaneReason(ctx context.Context, organizationID uint, now time.Time) (*domain.TransactionalQueuedReason, error) {
	laneLimit, err := s.organizationLaneLimit(ctx, organizationID, domain.SendLaneTransactional)
	if err != nil {
		return nil, err
	}
	limit := int64(laneLimit)
	used, err := s.transactional.CountTransactionalAttemptsSince(ctx, organizationID, now.Add(-sendLimitWindow))
	if err != nil {
		return nil, err
	}
	if used < limit {
		return nil, nil
	}
	waiting, err := s.transactional.CountTransactionalWaiting(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	if waiting >= limit {
		return nil, ErrMailSendLimitReached
	}
	reason := domain.TransactionalQueuedSendLimit
	return &reason, nil
}

// applyTransactional runs a state change and publishes its notifications in one transaction,
// like apply: a webhook that cannot be queued never undoes the record of the message.
func (s *service) applyTransactional(ctx context.Context, state func(ctx context.Context) ([]TransactionalNotification, error)) error {
	var publishErr error
	err := s.repo.InTransaction(ctx, func(tx context.Context) error {
		notifications, err := state(tx)
		if err != nil {
			return err
		}
		for _, notification := range notifications {
			if publishErr = s.notifier.NotifyTransactional(tx, notification); publishErr != nil {
				return publishErr
			}
		}
		return nil
	})
	if err == nil || publishErr == nil {
		return err
	}
	slog.ErrorContext(ctx, "transactional webhook publication failed; recording the message without it")
	return s.repo.InTransaction(ctx, func(tx context.Context) error {
		_, stateErr := state(tx)
		return stateErr
	})
}
