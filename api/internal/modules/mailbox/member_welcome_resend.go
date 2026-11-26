package mailbox

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/metrics"
	auditstarter "github.com/fastagi/zmail/api/internal/modules/audit"
)

const (
	// memberWelcomeResendInterval is the least time between two welcome mails of one mailbox.
	memberWelcomeResendInterval = time.Minute
	// memberWelcomeResendWindow and maxMemberWelcomeResends bound resends of one mailbox.
	memberWelcomeResendWindow = 24 * time.Hour
	maxMemberWelcomeResends   = 5
)

var (
	// ErrMemberWelcomeNotEligible means the mailbox is not one a welcome mail goes to.
	ErrMemberWelcomeNotEligible = errors.New("mailbox cannot be welcomed")
	// ErrMemberWelcomeDisabled means the organization turned welcome mails off.
	ErrMemberWelcomeDisabled = errors.New("member welcome mails are turned off")
	// ErrMemberWelcomeResendLimited means the mailbox's welcome mail was sent again too recently or
	// too often; it arrives wrapped in a RetryAfterError.
	ErrMemberWelcomeResendLimited = errors.New("member welcome mail resent too often")
)

// MemberWelcomeDeliveryStatus is what became of a mailbox's latest welcome mail.
type MemberWelcomeDeliveryStatus string

const (
	// MemberWelcomeDeliveryNotSent means no welcome mail was ever stored for the mailbox.
	MemberWelcomeDeliveryNotSent MemberWelcomeDeliveryStatus = "not_sent"
	// MemberWelcomeDeliveryQueued means the latest welcome mail waits to be sent.
	MemberWelcomeDeliveryQueued MemberWelcomeDeliveryStatus = "queued"
	// MemberWelcomeDeliverySent means the provider accepted the latest welcome mail.
	MemberWelcomeDeliverySent MemberWelcomeDeliveryStatus = "sent"
	// MemberWelcomeDeliveryFailed means sending the latest welcome mail failed for good.
	MemberWelcomeDeliveryFailed MemberWelcomeDeliveryStatus = "failed"
	// MemberWelcomeDeliverySuppressed means the recipient is on the suppression list.
	MemberWelcomeDeliverySuppressed MemberWelcomeDeliveryStatus = "suppressed"
	// MemberWelcomeDeliveryExpired means the latest welcome mail's send log entry is past retention.
	MemberWelcomeDeliveryExpired MemberWelcomeDeliveryStatus = "expired"
)

// MemberWelcomeMail is a mailbox that can be welcomed, with its latest welcome mail. It never
// carries the recipient address.
type MemberWelcomeMail struct {
	MailboxID         uint
	Address           string
	DisplayName       string
	OwnerUserID       *uint
	InvitationPending bool
	Status            MemberWelcomeDeliveryStatus
	FailureCode       string
	QueuedAt          *time.Time
	SentAt            *time.Time
	ResendCount       int
}

// memberWelcomeEligible reports whether a welcome mail goes to the mailbox's person: a Zmail
// personal mailbox on an organization domain, reserved for an invitee or owned by a current member
// other than the viewer, the same mailboxes a roster import or a manager's create welcomes.
func (s *service) memberWelcomeEligible(viewer domain.OrganizationContext, mailbox *domain.Mailbox) bool {
	if mailbox == nil || mailbox.Type != domain.MailboxTypePersonal || mailbox.Connected() || mailbox.OwnerLeftAt != nil ||
		s.isSharedDomain(addressDomain(mailbox.Address)) {
		return false
	}
	if mailbox.OwnerUserID == nil {
		return mailbox.PendingOwnerEmail != ""
	}
	return *mailbox.OwnerUserID != viewer.UserID
}

// memberWelcomeRecipient follows the rule of the first welcome: the invitation address while the
// mailbox is reserved for an invitee, the owner's account email once they joined.
func (s *service) memberWelcomeRecipient(ctx context.Context, mailbox *domain.Mailbox) (string, error) {
	if mailbox.OwnerUserID == nil {
		return mailbox.PendingOwnerEmail, nil
	}
	if s.accounts == nil {
		return "", domain.ErrServiceUnavailable
	}
	account, err := s.accounts.FindByID(ctx, *mailbox.OwnerUserID)
	if errors.Is(err, domain.ErrUserNotFound) || (err == nil && (account == nil || account.Email == "")) {
		return "", ErrMemberWelcomeNotEligible
	}
	if err != nil {
		return "", err
	}
	return account.Email, nil
}

// ListMemberWelcomeMails lists every mailbox that can be welcomed with its latest welcome mail;
// owners and admins only.
func (s *service) ListMemberWelcomeMails(ctx context.Context, viewer domain.OrganizationContext) ([]MemberWelcomeMail, error) {
	if !viewer.Role.CanManageOrganization() {
		return nil, domain.ErrPermissionDenied
	}
	if s.welcome == nil {
		return nil, domain.ErrServiceUnavailable
	}
	return s.memberWelcomeMails(ctx, viewer, 0)
}

// memberWelcomeMails reads the eligible mailboxes, or the one mailboxID names, with their latest
// welcome mail.
func (s *service) memberWelcomeMails(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint) ([]MemberWelcomeMail, error) {
	mailboxes, err := s.welcome.ListWelcomeCandidates(ctx, viewer.OrganizationID, mailboxID)
	if err != nil {
		return nil, err
	}
	deliveries, err := s.welcome.ListMemberWelcomeDeliveries(ctx, viewer.OrganizationID, mailboxID)
	if err != nil {
		return nil, err
	}
	byMailbox := make(map[uint]memberWelcomeDelivery, len(deliveries))
	for _, delivery := range deliveries {
		byMailbox[delivery.MailboxID] = delivery
	}
	mails := make([]MemberWelcomeMail, 0, len(mailboxes))
	for _, mailbox := range mailboxes {
		if !s.memberWelcomeEligible(viewer, mailbox) {
			continue
		}
		mail := MemberWelcomeMail{
			MailboxID: mailbox.ID, Address: mailbox.Address, DisplayName: mailbox.DisplayName,
			OwnerUserID: mailbox.OwnerUserID, InvitationPending: mailbox.OwnerUserID == nil,
			Status: MemberWelcomeDeliveryNotSent,
		}
		if delivery, ok := byMailbox[mailbox.ID]; ok {
			delivery.fill(&mail)
		}
		mails = append(mails, mail)
	}
	return mails, nil
}

func (d memberWelcomeDelivery) fill(mail *MemberWelcomeMail) {
	queuedAt := d.CreatedAt
	if d.LastSentAt != nil {
		queuedAt = *d.LastSentAt
	}
	mail.QueuedAt, mail.ResendCount = &queuedAt, d.ResendCount
	if d.MessageStatus == nil {
		mail.Status = MemberWelcomeDeliveryExpired
		return
	}
	switch domain.TransactionalStatus(*d.MessageStatus) {
	case domain.TransactionalStatusQueued:
		mail.Status = MemberWelcomeDeliveryQueued
	case domain.TransactionalStatusSent:
		mail.Status, mail.SentAt = MemberWelcomeDeliverySent, d.SentAt
	case domain.TransactionalStatusFailed:
		mail.Status = MemberWelcomeDeliveryFailed
		if d.FailureCode != nil {
			mail.FailureCode = *d.FailureCode
		}
	case domain.TransactionalStatusSuppressed:
		mail.Status = MemberWelcomeDeliverySuppressed
	default:
		mail.Status = MemberWelcomeDeliveryQueued
	}
}

// ResendMemberWelcome sends a mailbox's welcome mail again with the current copy; owners and
// admins only. It stores a new transactional message keyed member-welcome:<mailbox id>:<n> and
// points the mailbox's welcome record at it, at most once a minute and five times in 24 hours.
func (s *service) ResendMemberWelcome(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint) (MemberWelcomeMail, error) {
	if !viewer.Role.CanManageOrganization() {
		return MemberWelcomeMail{}, domain.ErrPermissionDenied
	}
	if s.welcome == nil {
		return MemberWelcomeMail{}, domain.ErrServiceUnavailable
	}
	mailbox, err := s.repo.FindMailbox(ctx, viewer.OrganizationID, mailboxID)
	if err != nil {
		return MemberWelcomeMail{}, err
	}
	if !s.memberWelcomeEligible(viewer, mailbox) {
		return MemberWelcomeMail{}, ErrMemberWelcomeNotEligible
	}
	setting, err := s.welcome.FindMemberWelcomeSetting(ctx, viewer.OrganizationID)
	if err != nil {
		return MemberWelcomeMail{}, err
	}
	if !setting.Enabled {
		return MemberWelcomeMail{}, ErrMemberWelcomeDisabled
	}
	if !s.memberWelcomeReady() {
		return MemberWelcomeMail{}, domain.ErrServiceUnavailable
	}
	recipient, err := s.memberWelcomeRecipient(ctx, mailbox)
	if err != nil {
		return MemberWelcomeMail{}, err
	}
	message, err := s.newMemberWelcomeMessage(ctx, viewer, setting, mailbox, recipient, mailbox.DisplayName)
	if err != nil {
		return MemberWelcomeMail{}, err
	}
	resendCount, err := s.storeMemberWelcomeResend(ctx, mailbox, message)
	if err != nil {
		return MemberWelcomeMail{}, err
	}
	slog.InfoContext(ctx, "member_welcome.resent",
		slog.Uint64("organization_id", uint64(viewer.OrganizationID)),
		slog.Uint64("mailbox_id", uint64(mailbox.ID)),
		slog.Uint64("message_id", uint64(message.ID)),
		slog.Int("resend_count", resendCount),
		slog.String("status", string(message.Status)))
	s.recordAudit(ctx, auditstarter.Change{
		Action:     "resend",
		Resource:   "member_welcome_mails",
		TargetType: "mailbox",
		TargetID:   strconv.FormatUint(uint64(mailbox.ID), 10),
		Result:     domain.AuditResultSuccess,
		Metadata: map[string]any{
			"mailbox_id": mailbox.ID, "resend_count": resendCount, "status": string(message.Status),
		},
	})
	if message.Status == domain.TransactionalStatusSuppressed {
		metrics.RecordTransactionalMessage(string(message.Mode), string(message.Status))
	} else {
		s.dispatchTransactionalSoon(ctx, message)
	}
	if mails, readErr := s.memberWelcomeMails(ctx, viewer, mailbox.ID); readErr == nil && len(mails) == 1 {
		return mails[0], nil
	}
	// The resend is stored; answer what is known without the send log state.
	slog.WarnContext(ctx, "member_welcome.resend_readback_failed",
		slog.Uint64("mailbox_id", uint64(mailbox.ID)))
	queuedAt := message.CreatedAt
	return MemberWelcomeMail{
		MailboxID: mailbox.ID, Address: mailbox.Address, DisplayName: mailbox.DisplayName,
		OwnerUserID: mailbox.OwnerUserID, InvitationPending: mailbox.OwnerUserID == nil,
		Status: MemberWelcomeDeliveryQueued, QueuedAt: &queuedAt, ResendCount: resendCount,
	}, nil
}

// storeMemberWelcomeResend checks the mailbox's resend limits under a lock on its welcome record,
// then stores the message under the next resend key and points the record at it, all in one
// transaction: a refused or failed resend stores nothing and counts toward no limit. It replaces
// *message with the stored row and returns the new resend count.
func (s *service) storeMemberWelcomeResend(ctx context.Context, mailbox *domain.Mailbox, message *domain.TransactionalMessage) (int, error) {
	var stored *domain.TransactionalMessage
	var resendCount int
	now := message.CreatedAt
	err := s.applyTransactional(ctx, func(tx context.Context) ([]TransactionalNotification, error) {
		stored, resendCount = nil, 0
		record, created, err := s.welcome.LockMemberWelcome(tx, message.OrganizationID, mailbox.ID, message.ToAddress, now)
		if err != nil {
			return nil, err
		}
		if limitErr := checkMemberWelcomeResend(&record, created, now); limitErr != nil {
			return nil, limitErr
		}
		record.ResendCount++
		key := memberWelcomeIdempotencyKey(mailbox.ID) + ":" + strconv.Itoa(record.ResendCount)
		candidate := *message
		candidate.IdempotencyKey = &key
		if candidate.CountsInLane() && candidate.Status == domain.TransactionalStatusQueued {
			reason, laneErr := s.transactionalLaneReason(tx, message.OrganizationID, now)
			if laneErr != nil {
				return nil, laneErr
			}
			candidate.QueuedReason = reason
		}
		inserted, err := s.transactional.CreateTransactional(tx, &candidate)
		if err != nil {
			return nil, err
		}
		if !inserted {
			return nil, ErrMailIdempotencyConflict
		}
		record.Recipient, record.TransactionalMessageID, record.LastSentAt = candidate.ToAddress, &candidate.ID, &now
		if err := s.welcome.SaveMemberWelcomeResend(tx, record); err != nil {
			return nil, err
		}
		stored, resendCount = &candidate, record.ResendCount
		if candidate.Status != domain.TransactionalStatusSuppressed {
			return nil, nil
		}
		return []TransactionalNotification{{
			Type: WebhookEmailSuppressed, Message: stored, Reason: "suppressed", OccurredAt: now,
		}}, nil
	})
	if err != nil {
		return 0, err
	}
	*message = *stored
	return resendCount, nil
}

// checkMemberWelcomeResend refuses a resend within a minute of the latest welcome or past the
// fifth in the current 24-hour window, and otherwise counts it in the window. A record created
// for this resend has no earlier welcome.
func checkMemberWelcomeResend(record *MemberWelcomeMailPO, created bool, now time.Time) error {
	if !created {
		latest := record.CreatedAt
		if record.LastSentAt != nil {
			latest = *record.LastSentAt
		}
		if wait := latest.Add(memberWelcomeResendInterval).Sub(now); wait > 0 {
			return &RetryAfterError{Err: ErrMemberWelcomeResendLimited, RetryAfter: wait}
		}
	}
	if record.ResendWindowStartedAt == nil || !now.Before(record.ResendWindowStartedAt.Add(memberWelcomeResendWindow)) {
		started := now
		record.ResendWindowStartedAt, record.ResendWindowCount = &started, 0
	}
	if record.ResendWindowCount >= maxMemberWelcomeResends {
		wait := record.ResendWindowStartedAt.Add(memberWelcomeResendWindow).Sub(now)
		return &RetryAfterError{Err: ErrMemberWelcomeResendLimited, RetryAfter: wait}
	}
	record.ResendWindowCount++
	return nil
}
