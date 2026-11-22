package mailbox

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/fastagi/zmail/api/internal/domain"
)

// MailDraftInput is what a member writes into a draft.
type MailDraftInput struct {
	MailboxID uint
	To        string
	Cc        string
	Subject   string
	Body      string
	InReplyTo string
}

func (s *service) draftStore() (domain.MailDraftRepository, error) {
	if s.drafts == nil {
		return nil, domain.ErrServiceUnavailable
	}
	return s.drafts, nil
}

// MailDrafts lists the viewer's own drafts in the organization, the latest change first.
func (s *service) MailDrafts(ctx context.Context, viewer domain.OrganizationContext) ([]domain.MailDraft, error) {
	store, err := s.draftStore()
	if err != nil {
		return nil, err
	}
	return store.ListMailDrafts(ctx, viewer.OrganizationID, viewer.UserID, domain.MailDraftsPerMember)
}

// CreateMailDraft saves a new draft from a mailbox the viewer may send from.
func (s *service) CreateMailDraft(ctx context.Context, viewer domain.OrganizationContext, input MailDraftInput) (domain.MailDraft, error) {
	store, err := s.draftStore()
	if err != nil {
		return domain.MailDraft{}, err
	}
	draft, err := s.checkedDraft(ctx, viewer, input)
	if err != nil {
		return domain.MailDraft{}, err
	}
	count, err := store.CountMailDrafts(ctx, viewer.OrganizationID, viewer.UserID)
	if err != nil {
		return domain.MailDraft{}, err
	}
	if count >= domain.MailDraftsPerMember {
		return domain.MailDraft{}, ErrMailDraftLimitReached
	}
	draft.CreatedAt = draft.UpdatedAt
	return store.CreateMailDraft(ctx, draft)
}

// UpdateMailDraft replaces what one of the viewer's drafts holds.
func (s *service) UpdateMailDraft(ctx context.Context, viewer domain.OrganizationContext, id uint, input MailDraftInput) (domain.MailDraft, error) {
	store, err := s.draftStore()
	if err != nil {
		return domain.MailDraft{}, err
	}
	if _, err = store.FindMailDraft(ctx, viewer.OrganizationID, viewer.UserID, id); err != nil {
		return domain.MailDraft{}, err
	}
	draft, err := s.checkedDraft(ctx, viewer, input)
	if err != nil {
		return domain.MailDraft{}, err
	}
	draft.ID = id
	return store.UpdateMailDraft(ctx, draft)
}

// DeleteMailDraft removes one of the viewer's drafts.
func (s *service) DeleteMailDraft(ctx context.Context, viewer domain.OrganizationContext, id uint) error {
	store, err := s.draftStore()
	if err != nil {
		return err
	}
	return store.DeleteMailDraft(ctx, viewer.OrganizationID, viewer.UserID, id)
}

// checkedDraft validates what is written and that the viewer may send from its mailbox.
func (s *service) checkedDraft(ctx context.Context, viewer domain.OrganizationContext, input MailDraftInput) (domain.MailDraft, error) {
	draft := domain.MailDraft{
		OrganizationID: viewer.OrganizationID, UserID: viewer.UserID,
		To: normalizeDraftLine(input.To), Cc: normalizeDraftLine(input.Cc),
		Subject: input.Subject, Body: strings.ReplaceAll(strings.ReplaceAll(input.Body, "\r\n", "\n"), "\r", "\n"),
		InReplyTo: strings.TrimSpace(input.InReplyTo), UpdatedAt: s.now().UTC(),
	}
	invalid := &ValidationError{}
	checkDraftField(invalid, "to", draft.To, domain.MailDraftRecipientsMaxLength, false)
	checkDraftField(invalid, "cc", draft.Cc, domain.MailDraftRecipientsMaxLength, false)
	checkDraftField(invalid, "subject", draft.Subject, domain.MailDraftSubjectMaxLength, false)
	checkDraftField(invalid, "body", draft.Body, domain.MailDraftBodyMaxLength, true)
	checkDraftField(invalid, "in_reply_to", draft.InReplyTo, domain.MailDraftInReplyToMaxLength, false)
	if err := invalid.orNil(); err != nil {
		return domain.MailDraft{}, err
	}
	mailbox, err := s.authorizeMailbox(ctx, viewer, input.MailboxID, needSend)
	if err != nil {
		return domain.MailDraft{}, err
	}
	draft.MailboxID = mailbox.ID
	return draft, nil
}

// normalizeDraftLine keeps a one-line field on one line, as the composer writes it.
func normalizeDraftLine(value string) string {
	return strings.Join(strings.Fields(strings.NewReplacer("\r", " ", "\n", " ").Replace(value)), " ")
}

func checkDraftField(invalid *ValidationError, field, value string, maxLength int, multiline bool) {
	if utf8.RuneCountInString(value) > maxLength {
		invalid.add(field, "is too long")
		return
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return unicode.IsControl(r) && r != '\t' && (!multiline || r != '\n')
	}) >= 0 {
		invalid.add(field, "must not contain control characters")
	}
}
