package mailbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/metrics"
	auditstarter "github.com/fastagi/zmail/api/internal/modules/audit"
)

// MemberWelcomeStatus is what became of one member's welcome mail.
type MemberWelcomeStatus string

const (
	// MemberWelcomeQueued stored the welcome mail in the transactional send log to be sent.
	MemberWelcomeQueued MemberWelcomeStatus = "queued"
	// MemberWelcomeSuppressed stored it as suppressed: the recipient is on the suppression list.
	MemberWelcomeSuppressed MemberWelcomeStatus = "suppressed"
	// MemberWelcomeAlreadySent means the mailbox was welcomed before; nothing new was sent.
	MemberWelcomeAlreadySent MemberWelcomeStatus = "already_sent"
	// MemberWelcomeDisabled means the organization turned welcome mails off.
	MemberWelcomeDisabled MemberWelcomeStatus = "disabled"
	// MemberWelcomeNotConfigured means this instance cannot send welcome mails.
	MemberWelcomeNotConfigured MemberWelcomeStatus = "not_configured"
	// MemberWelcomeFailed means no welcome mail was stored; ErrorCode says why.
	MemberWelcomeFailed MemberWelcomeStatus = "failed"
)

// memberWelcomeOutcome is the result of welcoming one mailbox's member.
type memberWelcomeOutcome struct {
	Status    MemberWelcomeStatus
	ErrorCode string
	MessageID uint
}

// errMemberWelcomeAlreadySent ends a welcome transaction that found the mailbox welcomed.
var errMemberWelcomeAlreadySent = errors.New("member welcome mail already sent")

// memberWelcomeIdempotencyKey names a mailbox's welcome mail in the send log.
func memberWelcomeIdempotencyKey(mailboxID uint) string {
	return memberWelcomeTemplateKey + ":" + strconv.FormatUint(uint64(mailboxID), 10)
}

// MemberWelcomeSetting reads the organization's welcome mail setting; owners and admins only.
func (s *service) MemberWelcomeSetting(ctx context.Context, viewer domain.OrganizationContext) (MemberWelcomeSetting, error) {
	if !viewer.Role.CanManageOrganization() {
		return MemberWelcomeSetting{}, domain.ErrPermissionDenied
	}
	if s.welcome == nil {
		return MemberWelcomeSetting{}, domain.ErrServiceUnavailable
	}
	return s.welcome.FindMemberWelcomeSetting(ctx, viewer.OrganizationID)
}

// UpdateMemberWelcomeSetting replaces the organization's welcome mail setting; owners and admins
// only. Empty subject or body saves the standard text of the language.
func (s *service) UpdateMemberWelcomeSetting(ctx context.Context, viewer domain.OrganizationContext, input MemberWelcomeInput) (MemberWelcomeSetting, error) {
	if !viewer.Role.CanManageOrganization() {
		return MemberWelcomeSetting{}, domain.ErrPermissionDenied
	}
	if s.welcome == nil {
		return MemberWelcomeSetting{}, domain.ErrServiceUnavailable
	}
	input.Subject = strings.TrimSpace(input.Subject)
	input.Body = strings.TrimSpace(strings.ReplaceAll(input.Body, "\r\n", "\n"))
	if err := validateMemberWelcomeCopy(input.Language, input.Subject, input.Body); err != nil {
		return MemberWelcomeSetting{}, err
	}
	// Copy equal to the standard text is stored as the standard text, so it follows the language.
	standard := MemberWelcomeDefault(input.Language)
	if input.Subject == standard.Subject {
		input.Subject = ""
	}
	if input.Body == standard.Body {
		input.Body = ""
	}
	setting, err := s.welcome.SaveMemberWelcomeSetting(ctx, viewer.OrganizationID, input, viewer.UserID, s.now().UTC())
	if err != nil {
		return MemberWelcomeSetting{}, err
	}
	s.recordAudit(ctx, auditstarter.Change{
		Action:     "update",
		Resource:   "member_welcome_settings",
		TargetType: "member_welcome_setting",
		TargetID:   strconv.FormatUint(uint64(viewer.OrganizationID), 10),
		Result:     domain.AuditResultSuccess,
		Metadata: map[string]any{
			"enabled": setting.Enabled, "language": string(setting.Language), "customized": setting.Customized(),
		},
	})
	return setting, nil
}

// PreviewMemberWelcome renders copy with sample values, exactly as a member would receive it;
// owners and admins only. Nothing is stored or sent.
func (s *service) PreviewMemberWelcome(ctx context.Context, viewer domain.OrganizationContext, input MemberWelcomePreviewInput) (MemberWelcomePreview, error) {
	if !viewer.Role.CanManageOrganization() {
		return MemberWelcomePreview{}, domain.ErrPermissionDenied
	}
	input.Subject = strings.TrimSpace(input.Subject)
	input.Body = strings.TrimSpace(strings.ReplaceAll(input.Body, "\r\n", "\n"))
	if err := validateMemberWelcomeCopy(input.Language, input.Subject, input.Body); err != nil {
		return MemberWelcomePreview{}, err
	}
	setting := MemberWelcomeSetting{Language: input.Language, Subject: input.Subject, Body: input.Body}
	values := memberWelcomeValues{
		MemberName: "Alex Chen", MailboxAddress: "alex@example.com",
		OrganizationName: viewer.OrganizationName, SignInURL: s.memberSignInURL(),
	}
	if input.Language == MemberWelcomeChinese {
		values.MemberName, values.MailboxAddress = "\u9648\u6653\u660e", "xiaoming@example.com"
	}
	rendered, err := renderMemberWelcome(setting.Effective(), input.Language, values, s.memberWelcomeClientSetup())
	if err != nil {
		return MemberWelcomePreview{}, err
	}
	return MemberWelcomePreview{Subject: rendered.Subject, Text: rendered.Text, HTML: rendered.HTML}, nil
}

// memberSignInURL is the console sign-in page; it falls back to the instance domain for a
// preview on an instance without MAILBOX_PUBLIC_URL, which never sends.
func (s *service) memberSignInURL() string {
	if s.settings.PublicURL != "" {
		return strings.TrimSuffix(s.settings.PublicURL, "/") + "/login"
	}
	return "https://" + s.settings.Domain + "/login"
}

func (s *service) memberWelcomeClientSetup() memberWelcomeClientSetup {
	origin := strings.TrimSuffix(s.settings.PublicURL, "/")
	if origin == "" {
		origin = "https://" + s.settings.Domain
	}
	return memberWelcomeClientSetup{
		SettingsURL: origin + "/console/settings?tab=mail-clients",
		IMAPHost:    s.settings.ClientIMAPHost, IMAPPort: s.settings.ClientIMAPPort,
		SMTPHost: s.settings.ClientSMTPHost, SMTPPort: s.settings.ClientSMTPPort,
		SubmissionPort: s.settings.ClientSubmissionPort,
	}
}

// memberWelcomeReady reports whether this instance can store and send welcome mails: it needs the
// transactional store, its encryption key, and the console origin the links point at. The
// transactional API switch does not apply: welcome mail is Zmail's own, not an API key's.
func (s *service) memberWelcomeReady() bool {
	return s.welcome != nil && s.transactional != nil && s.encryptor != nil && s.settings.PublicURL != ""
}

// welcomeMember queues the one welcome mail of a newly provisioned personal mailbox, addressed to
// recipient: the address the person reads before they first sign in. It never fails the caller;
// the outcome says what happened. A mailbox welcomed before is not welcomed again.
func (s *service) welcomeMember(
	ctx context.Context,
	viewer domain.OrganizationContext,
	mailbox *domain.Mailbox,
	recipient string,
	memberName string,
) memberWelcomeOutcome {
	if s.welcome == nil || mailbox == nil || mailbox.ID == 0 {
		return memberWelcomeOutcome{Status: MemberWelcomeNotConfigured}
	}
	setting, err := s.welcome.FindMemberWelcomeSetting(ctx, viewer.OrganizationID)
	if err != nil {
		return s.memberWelcomeFailed(ctx, viewer, mailbox, err)
	}
	if !setting.Enabled {
		return memberWelcomeOutcome{Status: MemberWelcomeDisabled}
	}
	if !s.memberWelcomeReady() {
		return memberWelcomeOutcome{Status: MemberWelcomeNotConfigured}
	}
	message, err := s.newMemberWelcomeMessage(ctx, viewer, setting, mailbox, recipient, memberName)
	if err != nil {
		return s.memberWelcomeFailed(ctx, viewer, mailbox, err)
	}
	if err = s.storeMemberWelcome(ctx, mailbox, message); errors.Is(err, errMemberWelcomeAlreadySent) {
		return memberWelcomeOutcome{Status: MemberWelcomeAlreadySent}
	}
	if err != nil {
		return s.memberWelcomeFailed(ctx, viewer, mailbox, err)
	}
	slog.InfoContext(ctx, "member_welcome.queued",
		slog.Uint64("organization_id", uint64(viewer.OrganizationID)),
		slog.Uint64("mailbox_id", uint64(mailbox.ID)),
		slog.Uint64("message_id", uint64(message.ID)),
		slog.String("status", string(message.Status)))
	if message.Status == domain.TransactionalStatusSuppressed {
		metrics.RecordTransactionalMessage(string(message.Mode), string(message.Status))
		return memberWelcomeOutcome{Status: MemberWelcomeSuppressed, MessageID: message.ID}
	}
	s.dispatchTransactionalSoon(ctx, message)
	return memberWelcomeOutcome{Status: MemberWelcomeQueued, MessageID: message.ID}
}

func (s *service) memberWelcomeFailed(ctx context.Context, viewer domain.OrganizationContext, mailbox *domain.Mailbox, err error) memberWelcomeOutcome {
	code := importErrorCode(err)
	if errors.Is(err, domain.ErrServiceUnavailable) {
		code = domain.CodeServiceUnavailable
	}
	slog.WarnContext(ctx, "member_welcome.failed",
		slog.Uint64("organization_id", uint64(viewer.OrganizationID)),
		slog.Uint64("mailbox_id", uint64(mailbox.ID)),
		slog.String("error_code", code),
		slog.String("error_type", fmt.Sprintf("%T", err)))
	return memberWelcomeOutcome{Status: MemberWelcomeFailed, ErrorCode: code}
}

// newMemberWelcomeMessage renders the welcome mail into a queued live transactional message sent
// from no-reply at the mailbox's own domain, which must be verified, in the organization's name.
func (s *service) newMemberWelcomeMessage(
	ctx context.Context,
	viewer domain.OrganizationContext,
	setting MemberWelcomeSetting,
	mailbox *domain.Mailbox,
	recipient string,
	memberName string,
) (*domain.TransactionalMessage, error) {
	to, ok := normalizeRecipient(recipient)
	if !ok || len(to) < 3 {
		return nil, ErrMailMessageInvalid
	}
	_, simulated, ok := simulatorOutcome(to, s.settings.simulatorDomain())
	if !ok {
		return nil, ErrMailMessageInvalid
	}
	from := "no-reply@" + addressDomain(mailbox.Address)
	if err := s.requireTemplateSender(ctx, viewer.OrganizationID, from); err != nil {
		return nil, err
	}
	values := memberWelcomeValues{
		MemberName: strings.TrimSpace(memberName), MailboxAddress: mailbox.Address,
		OrganizationName: viewer.OrganizationName, SignInURL: s.memberSignInURL(),
	}
	if values.MemberName == "" {
		values.MemberName = mailbox.DisplayName
	}
	rendered, err := renderMemberWelcome(setting.Effective(), setting.Language, values, s.memberWelcomeClientSetup())
	if err != nil {
		return nil, err
	}
	if utf8.RuneCountInString(rendered.Subject) > domain.MaxMailSubjectLength {
		return nil, ErrMailMessageInvalid
	}
	suppressed := false
	if !simulated {
		if suppressed, err = s.transactional.IsSuppressed(ctx, viewer.OrganizationID, to); err != nil {
			return nil, err
		}
	}
	variables := values.variables()
	key := memberWelcomeIdempotencyKey(mailbox.ID)
	now := s.now().UTC()
	message := &domain.TransactionalMessage{
		OrganizationID:         viewer.OrganizationID,
		Mode:                   domain.APIKeyModeLive,
		Simulated:              simulated,
		CreatedBy:              viewer.UserID,
		TemplateKey:            memberWelcomeTemplateKey,
		FromAddress:            from,
		FromName:               truncateRunes(viewer.OrganizationName, maxMemberWelcomeFromName),
		ToAddress:              to,
		SubjectMasked:          rendered.Subject,
		VariablesMasked:        variables,
		IdempotencyKey:         &key,
		IdempotencyFingerprint: transactionalFingerprint(s.fingerprintKey, memberWelcomeTemplateKey, to, variables),
		Status:                 domain.TransactionalStatusQueued,
		RFCMessageID:           newRFCMessageID(addressDomain(from)),
		CreatedAt:              now,
		UpdatedAt:              now,
	}
	if suppressed {
		message.Status = domain.TransactionalStatusSuppressed
		return message, nil
	}
	payload, err := json.Marshal(transactionalPayload{Subject: rendered.Subject, HTML: rendered.HTML, Text: rendered.Text})
	if err != nil {
		return nil, err
	}
	if message.PayloadCiphertext, err = s.encryptor.Encrypt(payload); err != nil {
		return nil, err
	}
	message.NextAttemptAt = &now
	return message, nil
}

// storeMemberWelcome claims the mailbox's welcome and stores its message in one transaction, so a
// mailbox is never claimed without a message nor welcomed twice. It replaces *message with the
// stored row.
func (s *service) storeMemberWelcome(ctx context.Context, mailbox *domain.Mailbox, message *domain.TransactionalMessage) error {
	var stored *domain.TransactionalMessage
	err := s.applyTransactional(ctx, func(tx context.Context) ([]TransactionalNotification, error) {
		stored = nil
		claimed, err := s.welcome.ClaimMemberWelcome(tx, message.OrganizationID, mailbox.ID, message.ToAddress, message.CreatedAt)
		if err != nil {
			return nil, err
		}
		if !claimed {
			return nil, errMemberWelcomeAlreadySent
		}
		candidate := *message
		if candidate.CountsInLane() && candidate.Status == domain.TransactionalStatusQueued {
			reason, err := s.transactionalLaneReason(tx, message.OrganizationID, message.CreatedAt)
			if err != nil {
				return nil, err
			}
			candidate.QueuedReason = reason
		}
		inserted, err := s.transactional.CreateTransactional(tx, &candidate)
		if err != nil {
			return nil, err
		}
		if !inserted {
			// Another sender holds the key within its 24 hours; the welcome is not claimed.
			return nil, ErrMailIdempotencyConflict
		}
		if err := s.welcome.LinkMemberWelcomeMessage(tx, mailbox.ID, candidate.ID); err != nil {
			return nil, err
		}
		stored = &candidate
		if candidate.Status != domain.TransactionalStatusSuppressed {
			return nil, nil
		}
		return []TransactionalNotification{{
			Type: WebhookEmailSuppressed, Message: stored, Reason: "suppressed", OccurredAt: candidate.CreatedAt,
		}}, nil
	})
	if err != nil {
		return err
	}
	*message = *stored
	return nil
}

// dispatchTransactionalSoon hands a message that may go now to the provider off the request
// path; a message waiting for the lane or a retry is left to the worker.
func (s *service) dispatchTransactionalSoon(ctx context.Context, message *domain.TransactionalMessage) {
	if message.Status != domain.TransactionalStatusQueued || message.QueuedReason != nil {
		return
	}
	id := message.ID
	dispatchCtx := context.WithoutCancel(ctx)
	s.background(func() {
		if dispatchErr := s.DispatchTransactionalMessage(dispatchCtx, id); dispatchErr != nil {
			slog.WarnContext(dispatchCtx, "transactional_message.dispatch_deferred",
				slog.Uint64("message_id", uint64(id)))
		}
	})
}
