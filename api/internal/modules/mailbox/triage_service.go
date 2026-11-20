package mailbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	auditstarter "github.com/fastagi/zmail/api/internal/modules/audit"
)

// triageClaimLease is how long a claimed row stays with its worker before another may take it.
const triageClaimLease = 2 * time.Minute

// triageAvailable reports whether a model is configured and the triage store exists.
func (s *service) triageAvailable() bool {
	return s.triage != nil && s.settings.Triage.Available()
}

// enqueueTriage queues a new inbound message for AI triage in the delivery's transaction when a
// model is configured; the repository checks both switches in the same statement.
func (s *service) enqueueTriage(ctx context.Context, message *domain.MailMessage) error {
	if !s.triageAvailable() || message.Direction != domain.MailDirectionInbound {
		return nil
	}
	_, err := s.triage.EnqueueTriage(ctx, message.ID, message.MailboxID, s.now())
	return err
}

// TriageService is AI triage as members use it: the organization and mailbox switches, a
// person's override, and reply drafts.
type TriageService interface {
	// TriageSetting reads the organization's switches; any member may.
	TriageSetting(ctx context.Context, viewer domain.OrganizationContext) (TriageSetting, error)
	// SaveTriageSetting replaces the organization's switches; only owners and admins may, and
	// only turning everything off works without a configured model.
	SaveTriageSetting(ctx context.Context, viewer domain.OrganizationContext, input TriageSettingInput) (TriageSetting, error)
	// SetMailboxTriage turns a mailbox's switch on or off: a personal mailbox's owner alone, or a
	// shared mailbox's member managers and the organization's owners and admins.
	SetMailboxTriage(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, enabled bool) (*domain.Mailbox, error)
	// OverrideTriage records a reader's decision on an inbound message; it needs view.
	OverrideTriage(ctx context.Context, viewer domain.OrganizationContext, mailboxID, messageID uint, input TriageOverrideInput) (*domain.MailTriage, error)
	// DraftReply drafts a reply to an inbound message; it needs send and stores nothing.
	DraftReply(ctx context.Context, viewer domain.OrganizationContext, mailboxID, messageID uint) (DraftOutput, error)
}

// TriageSetting is the organization's switches with what the instance offers.
type TriageSetting struct {
	domain.MailTriageSettings
	// Available is false when no model is configured; the feature is then hidden.
	Available bool
	// InstanceDailyCap is AI_TRIAGE_DAILY_CAP, the ceiling of DailyMessageCap's effect.
	InstanceDailyCap int
}

// TriageSettingInput replaces the organization's switches.
type TriageSettingInput struct {
	Enabled         bool
	DailyMessageCap int
	DraftEnabled    bool
}

// TriageOverrideInput is a person's decision; DueAt only with deadline.
type TriageOverrideInput struct {
	Category domain.MailTriageCategory
	DueAt    *time.Time
}

// triageStore returns the triage store, or ErrServiceUnavailable without one.
func (s *service) triageStore() (domain.MailTriageRepository, error) {
	if s.triage == nil {
		return nil, domain.ErrServiceUnavailable
	}
	return s.triage, nil
}

func (s *service) TriageSetting(ctx context.Context, viewer domain.OrganizationContext) (TriageSetting, error) {
	store, err := s.triageStore()
	if err != nil {
		return TriageSetting{}, err
	}
	settings, err := store.FindTriageSettings(ctx, viewer.OrganizationID)
	if err != nil {
		return TriageSetting{}, err
	}
	return s.triageSettingOf(settings), nil
}

func (s *service) triageSettingOf(settings domain.MailTriageSettings) TriageSetting {
	return TriageSetting{MailTriageSettings: settings, Available: s.triageAvailable(), InstanceDailyCap: s.settings.Triage.DailyCap}
}

func (s *service) SaveTriageSetting(ctx context.Context, viewer domain.OrganizationContext, input TriageSettingInput) (TriageSetting, error) {
	store, err := s.triageStore()
	if err != nil {
		return TriageSetting{}, err
	}
	if !viewer.Role.CanManageOrganization() {
		return TriageSetting{}, domain.ErrPermissionDenied
	}
	invalid := &ValidationError{}
	if input.DailyMessageCap < 1 || input.DailyMessageCap > config.MaxAITriageDailyCap {
		invalid.add("daily_message_cap", fmt.Sprintf("must be between 1 and %d", config.MaxAITriageDailyCap))
	}
	if err := invalid.orNil(); err != nil {
		return TriageSetting{}, err
	}
	if (input.Enabled || input.DraftEnabled) && !s.triageAvailable() {
		return TriageSetting{}, fmt.Errorf("%w: no AI triage model is configured", ErrTriageUnavailable)
	}
	before, err := store.FindTriageSettings(ctx, viewer.OrganizationID)
	if err != nil {
		return TriageSetting{}, err
	}
	saved, err := store.SaveTriageSettings(ctx, domain.MailTriageSettings{
		OrganizationID: viewer.OrganizationID, Enabled: input.Enabled, DailyMessageCap: input.DailyMessageCap,
		DraftEnabled: input.DraftEnabled, UpdatedBy: new(viewer.UserID),
	}, s.now())
	if err != nil {
		return TriageSetting{}, err
	}
	s.auditChange(ctx, auditstarter.Change{
		Action: "update", Resource: "mail_triage_setting", TargetType: "organization", TargetID: uintText(viewer.OrganizationID),
		Result: domain.AuditResultSuccess,
		Changes: map[string]domain.AuditValueChange{
			"enabled":           {Before: before.Enabled, After: saved.Enabled},
			"daily_message_cap": {Before: before.DailyMessageCap, After: saved.DailyMessageCap},
			"draft_enabled":     {Before: before.DraftEnabled, After: saved.DraftEnabled},
		},
	})
	return s.triageSettingOf(saved), nil
}

// canSwitchTriage is the owner of a personal mailbox, or ManageMembers on a shared one.
func canSwitchTriage(mailbox *domain.Mailbox, viewer domain.OrganizationContext) bool {
	if mailbox.Type == domain.MailboxTypePersonal {
		return mailbox.OwnerUserID != nil && *mailbox.OwnerUserID == viewer.UserID && mailbox.OwnerLeftAt == nil
	}
	return mailbox.Access.ManageMembers
}

func (s *service) SetMailboxTriage(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, enabled bool) (*domain.Mailbox, error) {
	store, err := s.triageStore()
	if err != nil {
		return nil, err
	}
	mailbox, err := s.authorizeMailbox(ctx, viewer, mailboxID, nil)
	if err != nil {
		return nil, err
	}
	if !canSwitchTriage(mailbox, viewer) {
		return nil, domain.ErrPermissionDenied
	}
	if enabled && !s.triageAvailable() {
		return nil, fmt.Errorf("%w: no AI triage model is configured", ErrTriageUnavailable)
	}
	if mailbox.AITriageEnabled != enabled {
		if err := store.SetMailboxTriage(ctx, mailbox.ID, enabled); err != nil {
			return nil, err
		}
		s.auditChange(ctx, auditstarter.Change{
			Action: "update", Resource: "mailboxes.ai_triage", TargetType: "mailbox", TargetID: uintText(mailbox.ID),
			Result:  domain.AuditResultSuccess,
			Changes: map[string]domain.AuditValueChange{"ai_triage_enabled": {Before: mailbox.AITriageEnabled, After: enabled}},
		})
	}
	return s.authorizeMailbox(ctx, viewer, mailbox.ID, nil)
}

// activeTriageSettings returns the organization's settings when a model is configured and the
// organization switch is on, else ErrTriageUnavailable.
func (s *service) activeTriageSettings(ctx context.Context, organizationID uint) (domain.MailTriageSettings, error) {
	store, err := s.triageStore()
	if err != nil {
		return domain.MailTriageSettings{}, err
	}
	settings, err := store.FindTriageSettings(ctx, organizationID)
	if err != nil {
		return settings, err
	}
	if !s.triageAvailable() || !settings.Enabled {
		return settings, fmt.Errorf("%w: AI triage is off for the organization", ErrTriageUnavailable)
	}
	return settings, nil
}

// inboundMessage loads an inbound message of a mailbox the viewer may use with need; an outbound
// message is a validation error of field.
func (s *service) inboundMessage(
	ctx context.Context,
	viewer domain.OrganizationContext,
	mailboxID, messageID uint,
	need func(domain.MailboxAccess) bool,
) (*domain.Mailbox, *domain.MailMessage, error) {
	mailbox, err := s.authorizeMailbox(ctx, viewer, mailboxID, need)
	if err != nil {
		return nil, nil, err
	}
	message, err := s.repo.FindMessage(ctx, mailbox.ID, messageID)
	if err != nil {
		return nil, nil, err
	}
	if message.Direction != domain.MailDirectionInbound {
		invalid := &ValidationError{}
		invalid.add("message_id", "must be an inbound message")
		return nil, nil, invalid
	}
	return mailbox, message, nil
}

func (s *service) OverrideTriage(
	ctx context.Context,
	viewer domain.OrganizationContext,
	mailboxID, messageID uint,
	input TriageOverrideInput,
) (*domain.MailTriage, error) {
	if _, err := s.activeTriageSettings(ctx, viewer.OrganizationID); err != nil {
		return nil, err
	}
	now := s.now().UTC()
	invalid := &ValidationError{}
	switch input.Category {
	case domain.MailTriageReply, domain.MailTriageDeadline, domain.MailTriageFYI:
	default:
		invalid.add("category", "must be reply, deadline, or fyi")
	}
	if input.DueAt != nil {
		if input.Category != domain.MailTriageDeadline {
			invalid.add("due_at", "is only allowed with deadline")
		} else if due, ok := parseTriageDue(input.DueAt.UTC().Format(time.RFC3339), now); !ok {
			invalid.add("due_at", "must be within a year before and five years after now")
		} else {
			input.DueAt = &due
		}
	}
	if err := invalid.orNil(); err != nil {
		return nil, err
	}
	mailbox, message, err := s.inboundMessage(ctx, viewer, mailboxID, messageID, canView)
	if err != nil {
		return nil, err
	}
	triage, err := s.triage.OverrideTriage(ctx, domain.MailTriageOverride{
		MessageID: message.ID, MailboxID: mailbox.ID, Category: input.Category, DueAt: input.DueAt,
		DecidedBy: viewer.UserID, At: now,
	})
	if err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "mail_triage.overridden",
		slog.Uint64("message_id", uint64(message.ID)),
		slog.Uint64("mailbox_id", uint64(mailbox.ID)),
		slog.String("category", string(triage.Category)),
	)
	return triage, nil
}

func (s *service) DraftReply(ctx context.Context, viewer domain.OrganizationContext, mailboxID, messageID uint) (DraftOutput, error) {
	settings, err := s.activeTriageSettings(ctx, viewer.OrganizationID)
	if err != nil {
		return DraftOutput{}, err
	}
	if !settings.DraftEnabled {
		return DraftOutput{}, fmt.Errorf("%w: reply drafts are off for the organization", ErrTriageUnavailable)
	}
	mailbox, message, err := s.inboundMessage(ctx, viewer, mailboxID, messageID, canSend)
	if err != nil {
		return DraftOutput{}, err
	}
	thread := []*domain.MailMessage{message}
	if s.organize != nil && message.ThreadID != 0 {
		messages, threadErr := s.organize.ThreadMessages(ctx, mailbox.ID, message.ThreadID, viewer.MembershipID)
		if threadErr != nil {
			return DraftOutput{}, threadErr
		}
		// The thread up to the message being answered, oldest first.
		thread = thread[:0]
		for _, item := range messages {
			if item.CreatedAt.After(message.CreatedAt) {
				break
			}
			thread = append(thread, item)
		}
		if len(thread) == 0 || thread[len(thread)-1].ID != message.ID {
			thread = append(thread, message)
		}
	}
	var templates []*domain.MailTemplate
	if s.templates != nil {
		if templates, err = s.templates.ListTemplates(ctx, viewer.OrganizationID, domain.MailTemplateStandard); err != nil {
			return DraftOutput{}, err
		}
	}
	roleName := ""
	if mailbox.RoleID != nil && s.roles != nil {
		if role, roleErr := s.roles.FindMailRole(ctx, viewer.OrganizationID, *mailbox.RoleID); roleErr == nil {
			roleName = role.Name
		}
	}
	input := buildDraftInput(thread, templates, roleName)
	callContext, cancel := context.WithTimeout(ctx, triageCallTimeout)
	defer cancel()
	output, err := s.triageModel.Draft(callContext, input)
	switch {
	case errors.Is(err, ErrTriageUnavailable):
		return DraftOutput{}, err
	case err != nil:
		slog.WarnContext(ctx, "mail_triage.draft_failed", slog.Uint64("message_id", uint64(message.ID)))
		return DraftOutput{}, fmt.Errorf("%w: draft", ErrTriageModelFailed)
	}
	draft, err := validateDraftOutput(output, input)
	if err != nil {
		return DraftOutput{}, err
	}
	slog.InfoContext(ctx, "mail_triage.drafted",
		slog.Uint64("message_id", uint64(message.ID)),
		slog.Uint64("mailbox_id", uint64(mailbox.ID)),
		slog.Int("basis", len(draft.Basis)),
	)
	return draft, nil
}

// triageOn reports whether the organization's readers see AI triage: a model is configured and
// the organization switch is on.
func (s *service) triageOn(ctx context.Context, organizationID uint) (bool, error) {
	if !s.triageAvailable() {
		return false, nil
	}
	settings, err := s.triage.FindTriageSettings(ctx, organizationID)
	if err != nil {
		return false, err
	}
	return settings.Enabled, nil
}

// triageCounts counts the viewer's unread Inbox mail of the readable mailboxes per category; nil
// when the organization's readers see no AI triage.
func (s *service) triageCounts(ctx context.Context, viewer domain.OrganizationContext, mailboxIDs []uint) (*domain.MailTriageCounts, error) {
	on, err := s.triageOn(ctx, viewer.OrganizationID)
	if err != nil || !on {
		return nil, err
	}
	counts, err := s.triage.CountTriage(ctx, viewer.MembershipID, mailboxIDs)
	if err != nil {
		return nil, err
	}
	return &counts, nil
}
