package mailbox

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"

	"github.com/fastagi/zmail/api/internal/domain"
)

var _ domain.MailSubmissionStore = (*service)(nil)

// submissionNoSubject stands in for the subject of a submitted message that has none, which the
// send path requires.
const submissionNoSubject = "(no subject)"

// submissionRepository releases the idempotency key of a failed submission, so a mail app that
// retries the same message sends it again instead of replaying the failure forever.
type submissionRepository interface {
	// ReleaseFailedIdempotencyKey clears the key of the message when its send failed.
	ReleaseFailedIdempotencyKey(ctx context.Context, messageID uint) error
}

var _ submissionRepository = (*repository)(nil)

func (r *repository) ReleaseFailedIdempotencyKey(ctx context.Context, messageID uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Model(&MailMessagePO{}).
		Where("id = ? AND status = ? AND queued_reason IS NULL", messageID, domain.MailStatusFailed).
		Update("idempotency_key", nil).Error
}

// clientSender finds the Zmail mailbox at the address that the principal may send from. Every
// refusal is ErrPermissionDenied: another organization's address, a mailbox the member may not
// send from, and a connected mailbox, which sends only through its own account.
func (s *service) clientSender(ctx context.Context, p *domain.MailClientPrincipal, address string) (*domain.Mailbox, error) {
	normalized, ok := normalizeRecipient(address)
	if !ok || p == nil {
		return nil, domain.ErrPermissionDenied
	}
	mailbox, err := s.repo.FindMailboxByAddress(ctx, normalized)
	if errors.Is(err, ErrMailboxNotFound) {
		return nil, domain.ErrPermissionDenied
	}
	if err != nil {
		return nil, err
	}
	if mailbox.OrganizationID != p.Viewer.OrganizationID || mailbox.Connected() {
		return nil, domain.ErrPermissionDenied
	}
	if err := s.checkAccess(ctx, p.Viewer, mailbox, canSend, domain.ErrPermissionDenied); err != nil {
		if errors.Is(err, ErrMailboxNotFound) {
			return nil, domain.ErrPermissionDenied
		}
		return nil, err
	}
	return mailbox, nil
}

// ClientSenderAllowed reports whether the principal may send from the address.
func (s *service) ClientSenderAllowed(ctx context.Context, p *domain.MailClientPrincipal, address string) (bool, error) {
	_, err := s.clientSender(ctx, p, address)
	if errors.Is(err, domain.ErrPermissionDenied) {
		return false, nil
	}
	return err == nil, err
}

// SubmitClientMessage sends a message a mail app submitted. The From header picks the mailbox,
// which the MAIL FROM must also be allowed to send from; the envelope is who receives it: To and
// Cc keep the header recipients the envelope names and every other envelope recipient is a blind
// copy. The mailbox's display name, a fresh Message-ID, and the send limit apply as for the
// console, and a retry of the same message replays the first outcome.
func (s *service) SubmitClientMessage(ctx context.Context, p *domain.MailClientPrincipal, submission domain.MailClientSubmission) (*domain.MailClientSubmissionResult, error) {
	senderRefused := &domain.MailClientSubmitError{Kind: domain.MailClientSubmitSenderNotAllowed, Reason: "You may not send from this address"}
	if p == nil || !p.Viewer.IsValid() {
		return nil, senderRefused
	}
	parsed, err := parseSubmission(submission.Raw)
	if err != nil {
		return nil, err
	}
	if parsed.FromAddress == "" {
		return nil, senderRefused
	}
	if _, senderErr := s.clientSender(ctx, p, submission.EnvelopeFrom); senderErr != nil {
		return nil, classifySubmitError(senderErr)
	}
	mailbox, err := s.clientSender(ctx, p, parsed.FromAddress)
	if err != nil {
		return nil, classifySubmitError(err)
	}

	to, cc, bcc := splitSubmissionRecipients(submission.Recipients, parsed.To, parsed.Cc)
	if len(to)+len(cc)+len(bcc) == 0 {
		return nil, &domain.MailClientSubmitError{Kind: domain.MailClientSubmitInvalid, Reason: "The message has no recipients"}
	}
	if len(to) == 0 {
		return nil, &domain.MailClientSubmitError{Kind: domain.MailClientSubmitInvalid, Reason: "Add at least one recipient in To"}
	}
	if len(to)+len(cc)+len(bcc) > domain.MaxMailRecipients {
		return nil, &domain.MailClientSubmitError{Kind: domain.MailClientSubmitTooLarge, Reason: "More than 50 recipients"}
	}

	input := SendInput{
		To: to, Cc: cc, Bcc: bcc,
		Subject:            parsed.Subject,
		Text:               parsed.TextBody,
		HTML:               parsed.HTMLBody,
		InReplyTo:          parsed.InReplyTo,
		SentByMembershipID: p.Viewer.MembershipID,
		allowBlankBody:     true,
		IdempotencyKey:     submissionIdempotencyKey(mailbox.ID, parsed.MessageID, submission.Raw),
	}
	if input.Subject == "" {
		input.Subject = submissionNoSubject
	}
	if strings.TrimSpace(input.Text) == "" && strings.TrimSpace(input.HTML) == "" {
		// The provider needs some body; a message of only files gets a blank one.
		input.Text = " "
	}
	for _, attachment := range parsed.Attachments {
		input.Attachments = append(input.Attachments, AttachmentInput{
			Filename:    attachment.Filename,
			ContentType: attachment.ContentType,
			Content:     base64.StdEncoding.EncodeToString(attachment.Content),
			ContentID:   attachment.ContentID,
			Inline:      attachment.Inline,
		})
	}
	if releaseErr := s.releaseFailedSubmission(ctx, mailbox, input); releaseErr != nil {
		return nil, releaseErr
	}

	message, _, err := s.send(ctx, mailbox, input, true)
	if err != nil {
		return nil, classifySubmitError(err)
	}
	result := &domain.MailClientSubmissionResult{Status: domain.MailClientSubmissionSent, MessageID: message.ID}
	if message.QueuedReason != "" {
		result.Status = domain.MailClientSubmissionQueued
	}
	return result, nil
}

// releaseFailedSubmission lets a retry of a message whose earlier attempt failed send again: the
// failed copy stays in Sent without its key. A different message under the same key is left for
// send to refuse as a conflict.
func (s *service) releaseFailedSubmission(ctx context.Context, mailbox *domain.Mailbox, input SendInput) error {
	releaser, ok := s.repo.(submissionRepository)
	if !ok {
		return nil
	}
	existing, err := s.repo.FindMessageByIdempotencyKey(ctx, mailbox.ID, input.IdempotencyKey)
	if errors.Is(err, ErrMailMessageNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if existing.Status != domain.MailStatusFailed || existing.QueuedReason != "" {
		return nil
	}
	// A message send would refuse, or a different one under the same key, keeps the key: send
	// answers it the same way.
	if built, buildErr := s.buildOutbound(mailbox, input); buildErr != nil || built.IdempotencyFingerprint != existing.IdempotencyFingerprint {
		return nil //nolint:nilerr // send reports the refusal itself
	}
	return releaser.ReleaseFailedIdempotencyKey(ctx, existing.ID)
}

// splitSubmissionRecipients keeps the To and Cc header recipients that the envelope names, in
// header order, and makes every other envelope recipient a blind copy. A header recipient the
// envelope leaves out receives nothing.
func splitSubmissionRecipients(envelope, headerTo, headerCc []string) (to, cc, bcc []string) {
	remaining := make(map[string]bool, len(envelope))
	for _, address := range envelope {
		remaining[strings.ToLower(address)] = true
	}
	take := func(header []string) []string {
		kept := []string{}
		for _, address := range header {
			if remaining[address] {
				kept = append(kept, address)
				delete(remaining, address)
			}
		}
		return kept
	}
	to, cc = take(headerTo), take(headerCc)
	bcc = []string{}
	for _, address := range envelope {
		address = strings.ToLower(address)
		if remaining[address] {
			bcc = append(bcc, address)
			delete(remaining, address)
		}
	}
	return to, cc, bcc
}

// submissionIdempotencyKey names a submitted message within its mailbox: by its Message-ID, which
// a mail app keeps when it retries, or by its exact bytes when it has none.
func submissionIdempotencyKey(mailboxID uint, messageID string, raw []byte) string {
	name := messageID
	if name == "" {
		digest := sha256.Sum256(raw)
		name = "raw:" + hex.EncodeToString(digest[:])
	}
	sum := sha256.Sum256([]byte(strconv.FormatUint(uint64(mailboxID), 10) + ":" + name))
	return "smtp:" + hex.EncodeToString(sum[:])
}

// classifySubmitError turns an error of the send path into the refusal a mail app reads. An error
// it does not know, such as a failing database, is returned as it is.
func classifySubmitError(err error) error {
	var refused *domain.MailClientSubmitError
	switch {
	case errors.As(err, &refused):
		return refused
	case errors.Is(err, domain.ErrPermissionDenied), errors.Is(err, ErrMailboxNotFound):
		return &domain.MailClientSubmitError{Kind: domain.MailClientSubmitSenderNotAllowed, Reason: "You may not send from this address"}
	case errors.Is(err, ErrMailMessageInvalid):
		return &domain.MailClientSubmitError{Kind: domain.MailClientSubmitInvalid, Reason: invalidSubmissionReason(err)}
	case errors.Is(err, ErrMailIdempotencyConflict):
		return &domain.MailClientSubmitError{Kind: domain.MailClientSubmitConflict, Reason: "A different message was already sent with this Message-ID"}
	case errors.Is(err, ErrMailSendLimitReached):
		return &domain.MailClientSubmitError{Kind: domain.MailClientSubmitLimitReached, Reason: "Daily send limit reached, try later"}
	case errors.Is(err, ErrMailSendingPaused):
		return &domain.MailClientSubmitError{Kind: domain.MailClientSubmitPaused, Reason: "Sending paused for this organization"}
	case errors.Is(err, ErrMailSenderNotConfigured):
		return &domain.MailClientSubmitError{Kind: domain.MailClientSubmitNotConfigured, Reason: "Sending is not set up on this server"}
	case errors.Is(err, ErrMailDeliveryFailed):
		return &domain.MailClientSubmitError{Kind: domain.MailClientSubmitDeliveryFailed, Reason: "The mail provider did not accept the message, try later"}
	case errors.Is(err, ErrMailSendInProgress):
		return &domain.MailClientSubmitError{Kind: domain.MailClientSubmitInProgress, Reason: "An earlier attempt of this message is still being sent"}
	}
	return err
}

// invalidSubmissionReason names the part of the message ErrMailMessageInvalid refused.
func invalidSubmissionReason(err error) string {
	message := err.Error()
	switch {
	case strings.HasSuffix(message, ": recipients"):
		return "A recipient address is not valid"
	case strings.HasSuffix(message, ": subject"):
		return "The subject is not valid"
	case strings.HasSuffix(message, ": body"):
		return "The message body is not valid"
	case strings.HasSuffix(message, ": attachments"):
		return "An attachment is not valid"
	case strings.HasSuffix(message, ": in_reply_to"):
		return "The In-Reply-To header is not valid"
	}
	return "The message cannot be sent as it is"
}
