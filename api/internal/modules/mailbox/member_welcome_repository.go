package mailbox

import (
	"context"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/modules/mailbox/roster"
)

type (
	MemberWelcomeSettingPO = roster.MemberWelcomeSettingPO
	MemberWelcomeMailPO    = roster.MemberWelcomeMailPO
	memberWelcomeDelivery  roster.MemberWelcomeDelivery
)

// memberWelcomeStore stores the welcome mail setting and the record of each mailbox's welcome.
type memberWelcomeStore interface {
	FindMemberWelcomeSetting(ctx context.Context, organizationID uint) (MemberWelcomeSetting, error)
	SaveMemberWelcomeSetting(ctx context.Context, organizationID uint, input MemberWelcomeInput, updatedBy uint, at time.Time) (MemberWelcomeSetting, error)
	ClaimMemberWelcome(ctx context.Context, organizationID, mailboxID uint, recipient string, at time.Time) (bool, error)
	LinkMemberWelcomeMessage(ctx context.Context, mailboxID, messageID uint) error
	LockMemberWelcome(ctx context.Context, organizationID, mailboxID uint, recipient string, at time.Time) (MemberWelcomeMailPO, bool, error)
	SaveMemberWelcomeResend(ctx context.Context, record MemberWelcomeMailPO) error
	ListWelcomeCandidates(ctx context.Context, organizationID, mailboxID uint) ([]*domain.Mailbox, error)
	ListMemberWelcomeDeliveries(ctx context.Context, organizationID, mailboxID uint) ([]memberWelcomeDelivery, error)
}

var _ memberWelcomeStore = (*repository)(nil)

func (r *repository) FindMemberWelcomeSetting(ctx context.Context, organizationID uint) (MemberWelcomeSetting, error) {
	setting := defaultMemberWelcomeSetting()
	po, found, err := r.rosterRepo().FindMemberWelcomeSetting(ctx, organizationID)
	if err != nil || !found {
		return setting, err
	}
	return welcomeSettingFromPO(po), nil
}

func (r *repository) SaveMemberWelcomeSetting(
	ctx context.Context,
	organizationID uint,
	input MemberWelcomeInput,
	updatedBy uint,
	at time.Time,
) (MemberWelcomeSetting, error) {
	po, err := r.rosterRepo().SaveMemberWelcomeSetting(ctx, organizationID, input.Enabled, string(input.Language), input.Subject, input.Body, updatedBy, at)
	if err != nil {
		return MemberWelcomeSetting{}, err
	}
	return welcomeSettingFromPO(po), nil
}

func (r *repository) ClaimMemberWelcome(ctx context.Context, organizationID, mailboxID uint, recipient string, at time.Time) (bool, error) {
	return r.rosterRepo().ClaimMemberWelcome(ctx, organizationID, mailboxID, recipient, at)
}

func (r *repository) LinkMemberWelcomeMessage(ctx context.Context, mailboxID, messageID uint) error {
	return r.rosterRepo().LinkMemberWelcomeMessage(ctx, mailboxID, messageID)
}

func (r *repository) LockMemberWelcome(
	ctx context.Context,
	organizationID, mailboxID uint,
	recipient string,
	at time.Time,
) (MemberWelcomeMailPO, bool, error) {
	return r.rosterRepo().LockMemberWelcome(ctx, organizationID, mailboxID, recipient, at)
}

func (r *repository) SaveMemberWelcomeResend(ctx context.Context, record MemberWelcomeMailPO) error {
	return r.rosterRepo().SaveMemberWelcomeResend(ctx, record)
}

func (r *repository) ListWelcomeCandidates(ctx context.Context, organizationID, mailboxID uint) ([]*domain.Mailbox, error) {
	return r.rosterRepo().ListWelcomeCandidates(ctx, organizationID, mailboxID)
}

func (r *repository) ListMemberWelcomeDeliveries(ctx context.Context, organizationID, mailboxID uint) ([]memberWelcomeDelivery, error) {
	rows, err := r.rosterRepo().ListMemberWelcomeDeliveries(ctx, organizationID, mailboxID)
	if err != nil {
		return nil, err
	}
	deliveries := make([]memberWelcomeDelivery, len(rows))
	for i := range rows {
		deliveries[i] = memberWelcomeDelivery(rows[i])
	}
	return deliveries, nil
}

func welcomeSettingFromPO(po MemberWelcomeSettingPO) MemberWelcomeSetting {
	updatedAt := po.UpdatedAt
	return MemberWelcomeSetting{
		Enabled: po.Enabled, Language: MemberWelcomeLanguage(po.Language), Subject: po.Subject, Body: po.Body,
		UpdatedAt: &updatedAt,
	}
}
