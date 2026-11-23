package mailbox

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fastagi/zmail/api/internal/domain"
)

// MaxMessageIDs bounds the messages one batch update names, and the labels each label field of an
// update names.
const MaxMessageIDs = 100

func canOrganize(access domain.MailboxAccess) bool { return access.Organize }

// MessageUpdate is one change to one or several messages. A nil field changes nothing; a non-nil
// empty AddLabelIDs or RemoveLabelIDs is present and invalid. Read and Starred are the caller's
// own and need view; the rest is shared and needs send.
type MessageUpdate struct {
	Read, Starred  *bool
	Placement      *domain.MailPlacement
	FolderID       *uint
	AddLabelIDs    []uint
	RemoveLabelIDs []uint
	SetLabelIDs    *[]uint
}

func (u MessageUpdate) empty() bool {
	return u.Read == nil && u.Starred == nil && u.Placement == nil && u.FolderID == nil &&
		u.AddLabelIDs == nil && u.RemoveLabelIDs == nil && u.SetLabelIDs == nil
}

// shared reports whether the update changes state everyone with access sees.
func (u MessageUpdate) shared() bool {
	return u.Placement != nil || u.FolderID != nil || u.AddLabelIDs != nil || u.RemoveLabelIDs != nil || u.SetLabelIDs != nil
}

// need is the highest access any field of the update needs.
func (u MessageUpdate) need() func(domain.MailboxAccess) bool {
	if u.shared() {
		return canSend
	}
	return canView
}

// labelIDs lists every positive label id the update names, once each.
func (u MessageUpdate) labelIDs() []uint {
	ids := slices.Concat(u.AddLabelIDs, u.RemoveLabelIDs)
	if u.SetLabelIDs != nil {
		ids = append(ids, *u.SetLabelIDs...)
	}
	return positiveDistinct(ids)
}

// ValidationError is a request body that breaks the contract's rules, keyed by field name.
type ValidationError struct {
	Fields map[string][]string
}

func (e *ValidationError) Error() string {
	keys := make([]string, 0, len(e.Fields))
	for key := range e.Fields {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return "validation failed: " + strings.Join(keys, ", ")
}

func (e *ValidationError) add(field, message string) {
	if e.Fields == nil {
		e.Fields = map[string][]string{}
	}
	e.Fields[field] = append(e.Fields[field], message)
}

func (e *ValidationError) orNil() error {
	if len(e.Fields) == 0 {
		return nil
	}
	return e
}

func asValidationError(err error) (*ValidationError, bool) {
	var validation *ValidationError
	ok := errors.As(err, &validation)
	return validation, ok
}

// UpdateMessages applies one change to every named message in one transaction. The access it
// needs is the highest any field needs, checked once before anything is looked up, so a view
// member asking for a shared change is refused with nothing changed. Then every message, the
// folder, and every label must belong to the mailbox, and only then is the body validated.
func (s *service) UpdateMessages(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, ids []uint, update MessageUpdate) (int, error) {
	if err := s.updateMessages(ctx, viewer, mailboxID, ids, update, true); err != nil {
		return 0, err
	}
	return len(ids), nil
}

// UpdateMessage applies a change to one message and returns it as the caller sees it.
func (s *service) UpdateMessage(ctx context.Context, viewer domain.OrganizationContext, mailboxID, messageID uint, update MessageUpdate) (*domain.MailMessage, error) {
	if err := s.updateMessages(ctx, viewer, mailboxID, []uint{messageID}, update, false); err != nil {
		return nil, err
	}
	message, err := s.repo.FindMessage(ctx, mailboxID, messageID)
	if err != nil {
		return nil, err
	}
	if err := s.fillViewerState(ctx, viewer, message); err != nil {
		return nil, err
	}
	return s.withEvents(ctx, message)
}

func (s *service) updateMessages(
	ctx context.Context,
	viewer domain.OrganizationContext,
	mailboxID uint,
	ids []uint,
	update MessageUpdate,
	batch bool,
) error {
	if _, err := s.authorizeMailbox(ctx, viewer, mailboxID, update.need()); err != nil {
		return err
	}
	if s.organize == nil {
		return domain.ErrServiceUnavailable
	}
	// An over-long list is refused before anything is looked up or locked.
	if batch && len(ids) > MaxMessageIDs {
		invalid := &ValidationError{}
		validateIDList(invalid, "message_ids", ids, 1)
		return invalid
	}
	return s.repo.InTransaction(ctx, func(tx context.Context) error {
		if err := s.lockReferences(tx, mailboxID, ids, update); err != nil {
			return err
		}
		if err := validateMessageUpdate(ids, update, batch); err != nil {
			return err
		}
		return s.organize.ApplyMessageChange(tx, positiveDistinct(ids), viewer.MembershipID,
			domain.MailMessageChange(update), s.now().UTC())
	})
}

// lockReferences checks that every message, the folder, and every label the update names belong
// to the mailbox, and holds them until the transaction ends. Ids that are not positive are left
// to validation.
//
// The folder and the labels are locked before the messages, the same order DeleteFolder and
// DeleteLabel take (folder or label row, then the messages or label rows under it), so the two
// never wait on each other in a cycle. Every lookup runs before any is reported, so a missing
// message still answers before a missing folder or label, as the contract orders them.
func (s *service) lockReferences(ctx context.Context, mailboxID uint, ids []uint, update MessageUpdate) error {
	var folderErr error
	if update.FolderID != nil && *update.FolderID != 0 {
		if _, folderErr = s.organize.LockFolder(ctx, mailboxID, *update.FolderID); folderErr != nil &&
			!errors.Is(folderErr, ErrMailFolderNotFound) {
			return folderErr
		}
	}
	labelIDs := update.labelIDs()
	labels, err := s.organize.LockLabels(ctx, mailboxID, labelIDs)
	if err != nil {
		return err
	}
	wanted := positiveDistinct(ids)
	messages, err := s.organize.LockMessages(ctx, mailboxID, wanted)
	if err != nil {
		return err
	}
	switch {
	case len(messages) != len(wanted):
		return ErrMailMessageNotFound
	case folderErr != nil:
		return folderErr
	case len(labels) != len(labelIDs):
		return ErrMailLabelNotFound
	}
	return nil
}

// validateMessageUpdate applies the contract's body rules, after access and lookups.
func validateMessageUpdate(ids []uint, update MessageUpdate, batch bool) error {
	invalid := &ValidationError{}
	if batch {
		validateIDList(invalid, "message_ids", ids, 1)
	}
	if update.empty() {
		invalid.add("change", "at least one change is required")
	}
	if update.Placement != nil && !update.Placement.Valid() {
		invalid.add("placement", "must be one of: default, archive, folder, trash")
	}
	inFolder := update.Placement != nil && *update.Placement == domain.MailPlacementFolder
	switch {
	case inFolder && update.FolderID == nil:
		invalid.add("folder_id", "is required with placement folder")
	case !inFolder && update.FolderID != nil:
		invalid.add("folder_id", "is allowed only with placement folder")
	case update.FolderID != nil && *update.FolderID == 0:
		invalid.add("folder_id", "must be a positive id")
	}
	if update.AddLabelIDs != nil {
		validateIDList(invalid, "add_label_ids", update.AddLabelIDs, 1)
	}
	if update.RemoveLabelIDs != nil {
		validateIDList(invalid, "remove_label_ids", update.RemoveLabelIDs, 1)
	}
	if update.SetLabelIDs != nil {
		validateIDList(invalid, "label_ids", *update.SetLabelIDs, 0)
	}
	for _, id := range update.AddLabelIDs {
		if slices.Contains(update.RemoveLabelIDs, id) {
			invalid.add("add_label_ids", "a label may not be both added and removed")
			break
		}
	}
	return invalid.orNil()
}

// validateIDList checks a list of minItems to MaxMessageIDs different positive ids.
func validateIDList(invalid *ValidationError, field string, ids []uint, minItems int) {
	if len(ids) < minItems || len(ids) > MaxMessageIDs {
		if minItems > 0 {
			invalid.add(field, "must hold 1 to "+strconv.Itoa(MaxMessageIDs)+" ids")
		} else {
			invalid.add(field, "must hold at most "+strconv.Itoa(MaxMessageIDs)+" ids")
		}
	}
	if len(positiveDistinct(ids)) != len(ids) {
		invalid.add(field, "must hold different positive ids")
	}
}

// positiveDistinct drops zero and repeated ids, keeping the first occurrence of each.
func positiveDistinct(ids []uint) []uint {
	seen := make(map[uint]struct{}, len(ids))
	distinct := make([]uint, 0, len(ids))
	for _, id := range ids {
		if _, repeated := seen[id]; id == 0 || repeated {
			continue
		}
		seen[id] = struct{}{}
		distinct = append(distinct, id)
	}
	return distinct
}

// DeleteMessage moves a message outside the trash to the trash, which needs send, and deletes a
// message already in the trash for good, which needs organize. A queued message that waits for
// the send limit is never sent once it is in the trash.
func (s *service) DeleteMessage(ctx context.Context, viewer domain.OrganizationContext, mailboxID, messageID uint) error {
	mailbox, err := s.authorizeMailbox(ctx, viewer, mailboxID, canSend)
	if err != nil {
		return err
	}
	if s.organize == nil {
		return domain.ErrServiceUnavailable
	}
	return s.repo.InTransaction(ctx, func(tx context.Context) error {
		messages, err := s.organize.LockMessages(tx, mailboxID, []uint{messageID})
		if err != nil {
			return err
		}
		if len(messages) != 1 {
			return ErrMailMessageNotFound
		}
		if messages[0].Placement == domain.MailPlacementTrash {
			if !canOrganize(mailbox.Access) {
				return domain.ErrPermissionDenied
			}
			deleted, err := s.organize.DeleteTrashedMessages(tx, mailboxID, []uint{messageID})
			if err == nil && deleted != 1 {
				err = ErrMailMessageNotFound
			}
			return err
		}
		trash := domain.MailPlacementTrash
		return s.organize.ApplyMessageChange(tx, []uint{messageID}, viewer.MembershipID,
			domain.MailMessageChange{Placement: &trash}, s.now().UTC())
	})
}

// PurgeTrash deletes every message in the mailbox's trash for good. It needs organize.
func (s *service) PurgeTrash(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint) (int, error) {
	if _, err := s.authorizeMailbox(ctx, viewer, mailboxID, canOrganize); err != nil {
		return 0, err
	}
	if s.organize == nil {
		return 0, domain.ErrServiceUnavailable
	}
	return s.organize.DeleteTrashedMessages(ctx, mailboxID, nil)
}

// PurgeExpiredTrash deletes for good up to limit messages that moved to the trash at least
// domain.MailTrashRetention before now, oldest first. The mail worker runs it on every tick.
func (s *service) PurgeExpiredTrash(ctx context.Context, now time.Time, limit int) (int, error) {
	if s.organize == nil {
		return 0, domain.ErrServiceUnavailable
	}
	purged, err := s.organize.PurgeExpiredTrash(ctx, now.Add(-domain.MailTrashRetention), limit)
	if purged > 0 {
		slog.InfoContext(ctx, "mail_trash.purged", slog.Int("count", purged))
	}
	return purged, err
}

// fillViewerState sets what each message looks like to the viewer: their own read state and star,
// and the mailbox's labels. Blind copies stay only on the messages the viewer sent.
func (s *service) fillViewerState(ctx context.Context, viewer domain.OrganizationContext, messages ...*domain.MailMessage) error {
	hideBlindCopies(viewer.MembershipID, messages...)
	if err := s.fillRead(ctx, viewer, messages...); err != nil {
		return err
	}
	if len(messages) == 0 {
		return nil
	}
	ids := make([]uint, 0, len(messages))
	for _, message := range messages {
		ids = append(ids, message.ID)
	}
	starred, err := s.repo.StarredMessages(ctx, viewer.MembershipID, ids)
	if err != nil {
		return err
	}
	for _, message := range messages {
		message.Starred = starred[message.ID]
	}
	return s.fillLabels(ctx, messages...)
}

// hideBlindCopies clears the blind copies of every message that membershipID did not send; zero,
// an API key, is nobody's.
func hideBlindCopies(membershipID uint, messages ...*domain.MailMessage) {
	for _, message := range messages {
		if message != nil && (membershipID == 0 || message.SentByMembershipID != membershipID) {
			message.Bcc = nil
		}
	}
}

// fillLabels sets the label ids of each message. Labels are shared, so every reader sees them.
func (s *service) fillLabels(ctx context.Context, messages ...*domain.MailMessage) error {
	if len(messages) == 0 {
		return nil
	}
	ids := make([]uint, 0, len(messages))
	for _, message := range messages {
		ids = append(ids, message.ID)
	}
	labels, err := s.repo.MessageLabelIDs(ctx, ids)
	if err != nil {
		return err
	}
	for _, message := range messages {
		message.LabelIDs = labels[message.ID]
	}
	return nil
}

// ListFolders lists the mailbox's folders by name ignoring case. It needs view.
func (s *service) ListFolders(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint) ([]*domain.MailFolder, error) {
	if _, err := s.authorizeMailbox(ctx, viewer, mailboxID, canView); err != nil {
		return nil, err
	}
	if s.organize == nil {
		return nil, domain.ErrServiceUnavailable
	}
	return s.organize.ListFolders(ctx, mailboxID)
}

// CreateFolder creates a folder. It needs organize.
func (s *service) CreateFolder(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, name string) (*domain.MailFolder, error) {
	if err := s.authorizeOrganize(ctx, viewer, mailboxID); err != nil {
		return nil, err
	}
	name, err := validOrganizeName(name)
	if err != nil {
		return nil, err
	}
	folder := &domain.MailFolder{MailboxID: mailboxID, Name: name}
	if err := s.organize.CreateFolder(ctx, folder, domain.MaxMailFoldersPerMailbox); err != nil {
		return nil, err
	}
	return folder, nil
}

// RenameFolder renames a folder of the mailbox. It needs organize.
func (s *service) RenameFolder(ctx context.Context, viewer domain.OrganizationContext, mailboxID, folderID uint, name string) (*domain.MailFolder, error) {
	if err := s.authorizeOrganize(ctx, viewer, mailboxID); err != nil {
		return nil, err
	}
	if _, err := s.organize.LockFolder(ctx, mailboxID, folderID); err != nil {
		return nil, err
	}
	name, err := validOrganizeName(name)
	if err != nil {
		return nil, err
	}
	return s.organize.RenameFolder(ctx, mailboxID, folderID, name)
}

// DeleteFolder deletes a folder; its messages go back to default. It needs organize.
func (s *service) DeleteFolder(ctx context.Context, viewer domain.OrganizationContext, mailboxID, folderID uint) error {
	if err := s.authorizeOrganize(ctx, viewer, mailboxID); err != nil {
		return err
	}
	return s.organize.DeleteFolder(ctx, mailboxID, folderID)
}

// ListLabels lists the mailbox's labels by name ignoring case. It needs view.
func (s *service) ListLabels(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint) ([]*domain.MailLabel, error) {
	if _, err := s.authorizeMailbox(ctx, viewer, mailboxID, canView); err != nil {
		return nil, err
	}
	if s.organize == nil {
		return nil, domain.ErrServiceUnavailable
	}
	return s.organize.ListLabels(ctx, mailboxID)
}

// CreateLabel creates a label. It needs organize.
func (s *service) CreateLabel(
	ctx context.Context,
	viewer domain.OrganizationContext,
	mailboxID uint,
	name string,
	color domain.MailLabelColor,
) (*domain.MailLabel, error) {
	if err := s.authorizeOrganize(ctx, viewer, mailboxID); err != nil {
		return nil, err
	}
	name, err := validLabel(name, color)
	if err != nil {
		return nil, err
	}
	label := &domain.MailLabel{MailboxID: mailboxID, Name: name, Color: color}
	if err := s.organize.CreateLabel(ctx, label, domain.MaxMailLabelsPerMailbox); err != nil {
		return nil, err
	}
	return label, nil
}

// UpdateLabel replaces the name and color of a label. It needs organize.
func (s *service) UpdateLabel(
	ctx context.Context,
	viewer domain.OrganizationContext,
	mailboxID, labelID uint,
	name string,
	color domain.MailLabelColor,
) (*domain.MailLabel, error) {
	if err := s.authorizeOrganize(ctx, viewer, mailboxID); err != nil {
		return nil, err
	}
	found, err := s.organize.LockLabels(ctx, mailboxID, []uint{labelID})
	if err != nil {
		return nil, err
	}
	if len(found) != 1 {
		return nil, ErrMailLabelNotFound
	}
	name, err = validLabel(name, color)
	if err != nil {
		return nil, err
	}
	label := &domain.MailLabel{ID: labelID, MailboxID: mailboxID, Name: name, Color: color}
	if err := s.organize.UpdateLabel(ctx, label); err != nil {
		return nil, err
	}
	return label, nil
}

// DeleteLabel deletes a label and removes it from every message. It needs organize.
func (s *service) DeleteLabel(ctx context.Context, viewer domain.OrganizationContext, mailboxID, labelID uint) error {
	if err := s.authorizeOrganize(ctx, viewer, mailboxID); err != nil {
		return err
	}
	return s.organize.DeleteLabel(ctx, mailboxID, labelID)
}

func (s *service) authorizeOrganize(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint) error {
	if _, err := s.authorizeMailbox(ctx, viewer, mailboxID, canOrganize); err != nil {
		return err
	}
	if s.organize == nil {
		return domain.ErrServiceUnavailable
	}
	return nil
}

// validOrganizeName trims a folder name and checks it is 1 to 64 characters.
func validOrganizeName(name string) (string, error) {
	invalid := &ValidationError{}
	name = checkOrganizeName(invalid, name, domain.MaxMailFolderNameLength)
	return name, invalid.orNil()
}

func validLabel(name string, color domain.MailLabelColor) (string, error) {
	invalid := &ValidationError{}
	name = checkOrganizeName(invalid, name, domain.MaxMailLabelNameLength)
	if !color.Valid() {
		invalid.add("color", "must be one of: gray, red, orange, yellow, green, teal, blue, purple, pink")
	}
	return name, invalid.orNil()
}

func checkOrganizeName(invalid *ValidationError, name string, maxLength int) string {
	name = strings.TrimSpace(name)
	if length := utf8.RuneCountInString(name); length < 1 || length > maxLength {
		invalid.add("name", "must be 1 to "+strconv.Itoa(maxLength)+" characters after trimming")
	}
	return name
}
