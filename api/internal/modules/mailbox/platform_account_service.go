package mailbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/fastagi/zmail/api/internal/domain"
	auditstarter "github.com/fastagi/zmail/api/internal/modules/audit"
)

// PlatformAccountService keeps the platform account ledger: which mailbox registered an account
// on an outside platform, its mail role, its owner, and its status, with a history of handovers
// and status changes. Owners and admins write; members only read the accounts registered with a
// mailbox they may read or that they own. The ledger grants no mailbox access.
type PlatformAccountService interface {
	ListPlatformAccounts(ctx context.Context, viewer domain.OrganizationContext, filter domain.PlatformAccountFilter) ([]*domain.PlatformAccount, int64, error)
	CreatePlatformAccount(ctx context.Context, viewer domain.OrganizationContext, input PlatformAccountInput) (*domain.PlatformAccount, error)
	UpdatePlatformAccount(ctx context.Context, viewer domain.OrganizationContext, id uint, input PlatformAccountInput) (*domain.PlatformAccount, error)
	DeletePlatformAccount(ctx context.Context, viewer domain.OrganizationContext, id uint) error
	ListPlatformAccountEvents(ctx context.Context, viewer domain.OrganizationContext, id uint, page, perPage int) ([]*domain.PlatformAccountEvent, int64, error)
	// HandOverPlatformAccounts moves every account owned by fromUserID to toUserID.
	HandOverPlatformAccounts(ctx context.Context, viewer domain.OrganizationContext, fromUserID, toUserID uint) (*domain.PlatformAccountHandover, error)
}

// platformAccountFields are the fields of an account a write sets, in audit order.
var platformAccountFields = []string{"platform", "handle", "profile_url", "role_id", "mailbox_id", "owner_user_id", "status", "notes"}

// PlatformAccountInput is the whole of an account a create or replace sets. The service trims
// text; an empty status is active and nil references are none.
type PlatformAccountInput struct {
	Platform    string
	Handle      string
	ProfileURL  string
	RoleID      *uint
	MailboxID   *uint
	OwnerUserID *uint
	Status      domain.PlatformAccountStatus
	Notes       string
}

// ListPlatformAccounts lists one page of the accounts the viewer may see. Owners and admins see
// every account; members only those registered with a mailbox they may read or that they own, and
// the search text matches only the addresses of mailboxes they can see.
func (s *service) ListPlatformAccounts(ctx context.Context, viewer domain.OrganizationContext, filter domain.PlatformAccountFilter) ([]*domain.PlatformAccount, int64, error) {
	if s.ledger == nil {
		return nil, 0, domain.ErrServiceUnavailable
	}
	filter.Query = strings.TrimSpace(filter.Query)
	if utf8.RuneCountInString(filter.Query) > domain.MaxPlatformAccountQueryLength {
		return nil, 0, fmt.Errorf("%w: q must be at most %d characters", domain.ErrInvalidInput, domain.MaxPlatformAccountQueryLength)
	}
	if filter.Status != "" && !filter.Status.Valid() {
		return nil, 0, fmt.Errorf("%w: unknown status", domain.ErrInvalidInput)
	}
	filter.Visible, filter.VisibleOwner, filter.AddressMailboxIDs = nil, 0, nil
	var seen map[uint]bool
	if !viewer.Role.CanManageOrganization() {
		readable, visible, err := s.ledgerMailboxes(ctx, viewer)
		if err != nil {
			return nil, 0, err
		}
		filter.Visible, filter.VisibleOwner = readable, viewer.UserID
		filter.AddressMailboxIDs = make([]uint, 0, len(visible))
		seen = make(map[uint]bool, len(visible))
		for id := range visible {
			filter.AddressMailboxIDs = append(filter.AddressMailboxIDs, id)
			seen[id] = true
		}
	}
	accounts, total, err := s.ledger.ListPlatformAccounts(ctx, viewer.OrganizationID, filter)
	if err != nil {
		return nil, 0, err
	}
	if seen != nil {
		for _, account := range accounts {
			hideUnseenMailbox(account, seen)
		}
	}
	return accounts, total, nil
}

// CreatePlatformAccount adds an account to the ledger with its created event. Only owners and
// admins may.
func (s *service) CreatePlatformAccount(ctx context.Context, viewer domain.OrganizationContext, input PlatformAccountInput) (*domain.PlatformAccount, error) {
	if s.ledger == nil {
		return nil, domain.ErrServiceUnavailable
	}
	if !viewer.Role.CanManageOrganization() {
		return nil, domain.ErrPermissionDenied
	}
	account, err := s.validPlatformAccount(ctx, viewer.OrganizationID, input)
	if err != nil {
		return nil, err
	}
	if err := s.ledger.CreatePlatformAccount(ctx, account, viewer.UserID); err != nil {
		return nil, err
	}
	s.auditPlatformAccount(ctx, "create", account.ID, platformAccountFields)
	slog.InfoContext(ctx, "mail.platform_account.created",
		slog.Uint64("organization_id", uint64(viewer.OrganizationID)), slog.Uint64("account_id", uint64(account.ID)))
	return account, nil
}

// UpdatePlatformAccount replaces every field of an account; the last write wins. Each change of
// status, owner, or registration mailbox records an event. Only owners and admins may.
func (s *service) UpdatePlatformAccount(ctx context.Context, viewer domain.OrganizationContext, id uint, input PlatformAccountInput) (*domain.PlatformAccount, error) {
	if s.ledger == nil {
		return nil, domain.ErrServiceUnavailable
	}
	if !viewer.Role.CanManageOrganization() {
		return nil, domain.ErrPermissionDenied
	}
	before, err := s.ledger.FindPlatformAccount(ctx, viewer.OrganizationID, id)
	if err != nil {
		return nil, err
	}
	account, err := s.validPlatformAccount(ctx, viewer.OrganizationID, input)
	if err != nil {
		return nil, err
	}
	account.ID = id
	if err := s.ledger.UpdatePlatformAccount(ctx, account, viewer.UserID); err != nil {
		return nil, err
	}
	s.auditPlatformAccount(ctx, "update", id, changedPlatformAccountFields(before, account))
	slog.InfoContext(ctx, "mail.platform_account.updated",
		slog.Uint64("organization_id", uint64(viewer.OrganizationID)), slog.Uint64("account_id", uint64(id)))
	return account, nil
}

// DeletePlatformAccount records the deleted event and removes the account. Only owners and
// admins may.
func (s *service) DeletePlatformAccount(ctx context.Context, viewer domain.OrganizationContext, id uint) error {
	if s.ledger == nil {
		return domain.ErrServiceUnavailable
	}
	if !viewer.Role.CanManageOrganization() {
		return domain.ErrPermissionDenied
	}
	if err := s.ledger.DeletePlatformAccount(ctx, viewer.OrganizationID, id, viewer.UserID); err != nil {
		return err
	}
	s.auditPlatformAccount(ctx, "delete", id, platformAccountFields)
	slog.InfoContext(ctx, "mail.platform_account.deleted",
		slog.Uint64("organization_id", uint64(viewer.OrganizationID)), slog.Uint64("account_id", uint64(id)))
	return nil
}

// ListPlatformAccountEvents lists one page of an account's history, newest first. An account the
// viewer may not see is not found.
func (s *service) ListPlatformAccountEvents(ctx context.Context, viewer domain.OrganizationContext, id uint, page, perPage int) ([]*domain.PlatformAccountEvent, int64, error) {
	if s.ledger == nil {
		return nil, 0, domain.ErrServiceUnavailable
	}
	if !viewer.Role.CanManageOrganization() {
		account, err := s.ledger.FindPlatformAccount(ctx, viewer.OrganizationID, id)
		if err != nil {
			return nil, 0, err
		}
		if account.OwnerUserID == nil || *account.OwnerUserID != viewer.UserID {
			readable, _, err := s.ledgerMailboxes(ctx, viewer)
			if err != nil {
				return nil, 0, err
			}
			if account.MailboxID == nil || !containsID(readable, *account.MailboxID) {
				return nil, 0, ErrPlatformAccountNotFound
			}
		}
	}
	return s.ledger.ListPlatformAccountEvents(ctx, viewer.OrganizationID, id, page, perPage)
}

// HandOverPlatformAccounts moves every account owned by fromUserID to toUserID, with one
// owner_changed event each. Only owners and admins may. The successor must be a current member;
// the previous owner may have left the organization, which is the usual reason to hand over.
func (s *service) HandOverPlatformAccounts(ctx context.Context, viewer domain.OrganizationContext, fromUserID, toUserID uint) (*domain.PlatformAccountHandover, error) {
	if s.ledger == nil {
		return nil, domain.ErrServiceUnavailable
	}
	if !viewer.Role.CanManageOrganization() {
		return nil, domain.ErrPermissionDenied
	}
	invalid := &ValidationError{}
	if fromUserID == 0 {
		invalid.add("from_user_id", "must be a positive user id")
	}
	switch toUserID {
	case 0:
		invalid.add("to_user_id", "must be a current member of the organization")
	case fromUserID:
		invalid.add("to_user_id", "must differ from from_user_id")
	default:
		if _, err := s.currentMembership(ctx, viewer.OrganizationID, toUserID); err != nil {
			if !errors.Is(err, ErrMailboxAccessInvalid) {
				return nil, err
			}
			invalid.add("to_user_id", "must be a current member of the organization")
		}
	}
	if err := invalid.orNil(); err != nil {
		return nil, err
	}
	moved, err := s.ledger.HandOverPlatformAccounts(ctx, viewer.OrganizationID, fromUserID, toUserID, viewer.UserID)
	if err != nil {
		return nil, err
	}
	s.auditChange(ctx, auditstarter.Change{
		Action: "create", Resource: "platform_account_handovers", TargetType: "user", TargetID: uintText(fromUserID),
		Result: domain.AuditResultSuccess,
		Changes: map[string]domain.AuditValueChange{
			"owner_user_id": {Before: fromUserID, After: toUserID},
		},
		Metadata: map[string]any{"account_count": len(moved)},
	})
	slog.InfoContext(ctx, "mail.platform_account.handed_over",
		slog.Uint64("organization_id", uint64(viewer.OrganizationID)), slog.Uint64("from_user_id", uint64(fromUserID)),
		slog.Uint64("to_user_id", uint64(toUserID)), slog.Int("account_count", len(moved)))
	return &domain.PlatformAccountHandover{FromUserID: fromUserID, ToUserID: toUserID, AccountIDs: moved}, nil
}

// ledgerMailboxes returns the mailboxes whose mail the viewer may read and, as a set, every
// mailbox the viewer can see at all.
func (s *service) ledgerMailboxes(ctx context.Context, viewer domain.OrganizationContext) ([]uint, map[uint]bool, error) {
	mailboxes, err := s.accessRows(ctx, viewer)
	if err != nil {
		return nil, nil, err
	}
	readable := make([]uint, 0, len(mailboxes))
	visible := make(map[uint]bool, len(mailboxes))
	for _, mailbox := range mailboxes {
		if mailbox.Access.Any() {
			visible[mailbox.ID] = true
		}
		if mailbox.Access.View {
			readable = append(readable, mailbox.ID)
		}
	}
	return readable, visible, nil
}

// hideUnseenMailbox clears the registration address of an account whose mailbox the viewer
// cannot see, so the ledger never reveals another member's private address.
func hideUnseenMailbox(account *domain.PlatformAccount, seen map[uint]bool) {
	if account.MailboxID != nil && !seen[*account.MailboxID] {
		account.MailboxAddress = ""
	}
}

func containsID(ids []uint, id uint) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

// validPlatformAccount trims and checks every field at once. The role and mailbox must belong to
// the organization (a deleted mailbox does not), and the owner must be a current member.
func (s *service) validPlatformAccount(ctx context.Context, organizationID uint, input PlatformAccountInput) (*domain.PlatformAccount, error) {
	invalid := &ValidationError{}
	platform := checkLedgerText(invalid, "platform", input.Platform, 1, domain.MaxPlatformNameLength)
	handle := checkLedgerText(invalid, "handle", input.Handle, 1, domain.MaxPlatformHandleLength)
	notes := strings.TrimSpace(input.Notes)
	if utf8.RuneCountInString(notes) > domain.MaxPlatformAccountNotesLength {
		invalid.add("notes", "must be at most "+strconv.Itoa(domain.MaxPlatformAccountNotesLength)+" characters after trimming")
	}
	profileURL := strings.TrimSpace(input.ProfileURL)
	if profileURL != "" && !validProfileURL(profileURL) {
		invalid.add("profile_url", "must be an absolute http or https URL of at most "+strconv.Itoa(domain.MaxPlatformProfileURLLength)+" bytes")
	}
	status := input.Status
	if status == "" {
		status = domain.PlatformAccountStatusActive
	}
	if !status.Valid() {
		invalid.add("status", "must be one of: invited, awaiting_confirmation, active, timed_out, needs_review, retired")
	}
	if input.RoleID != nil {
		if *input.RoleID == 0 || s.roles == nil {
			invalid.add("role_id", "must be a mail role of the organization or null")
		} else if _, err := s.roles.FindMailRole(ctx, organizationID, *input.RoleID); err != nil {
			if !errors.Is(err, ErrMailRoleNotFound) {
				return nil, err
			}
			invalid.add("role_id", "must be a mail role of the organization or null")
		}
	}
	if input.MailboxID != nil {
		if *input.MailboxID == 0 {
			invalid.add("mailbox_id", "must be a mailbox of the organization or null")
		} else if _, err := s.repo.FindMailbox(ctx, organizationID, *input.MailboxID); err != nil {
			if !errors.Is(err, ErrMailboxNotFound) {
				return nil, err
			}
			invalid.add("mailbox_id", "must be a mailbox of the organization or null")
		}
	}
	if input.OwnerUserID != nil {
		if *input.OwnerUserID == 0 {
			invalid.add("owner_user_id", "must be a current member of the organization or null")
		} else if _, err := s.currentMembership(ctx, organizationID, *input.OwnerUserID); err != nil {
			if !errors.Is(err, ErrMailboxAccessInvalid) {
				return nil, err
			}
			invalid.add("owner_user_id", "must be a current member of the organization or null")
		}
	}
	if err := invalid.orNil(); err != nil {
		return nil, err
	}
	return &domain.PlatformAccount{
		OrganizationID: organizationID, Platform: platform, Handle: handle, ProfileURL: profileURL,
		RoleID: input.RoleID, MailboxID: input.MailboxID, OwnerUserID: input.OwnerUserID, Status: status, Notes: notes,
	}, nil
}

// checkLedgerText trims value and checks its length in characters and that it has no control
// characters.
func checkLedgerText(invalid *ValidationError, field, value string, minLength, maxLength int) string {
	value = strings.TrimSpace(value)
	if length := utf8.RuneCountInString(value); length < minLength || length > maxLength {
		invalid.add(field, "must be "+strconv.Itoa(minLength)+" to "+strconv.Itoa(maxLength)+" characters after trimming")
	} else if strings.ContainsFunc(value, unicode.IsControl) {
		invalid.add(field, "must not contain control characters")
	}
	return value
}

// validProfileURL accepts an absolute http or https URL with a host and no credentials.
func validProfileURL(raw string) bool {
	if len(raw) > domain.MaxPlatformProfileURLLength || strings.ContainsFunc(raw, unicode.IsControl) {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.Host == "" {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

// changedPlatformAccountFields names the fields a replace changed, in audit order.
func changedPlatformAccountFields(before, after *domain.PlatformAccount) []string {
	changed := []string{}
	if before.Platform != after.Platform {
		changed = append(changed, "platform")
	}
	if before.Handle != after.Handle {
		changed = append(changed, "handle")
	}
	if before.ProfileURL != after.ProfileURL {
		changed = append(changed, "profile_url")
	}
	if auditID(before.RoleID) != auditID(after.RoleID) {
		changed = append(changed, "role_id")
	}
	if auditID(before.MailboxID) != auditID(after.MailboxID) {
		changed = append(changed, "mailbox_id")
	}
	if auditID(before.OwnerUserID) != auditID(after.OwnerUserID) {
		changed = append(changed, "owner_user_id")
	}
	if before.Status != after.Status {
		changed = append(changed, "status")
	}
	if before.Notes != after.Notes {
		changed = append(changed, "notes")
	}
	return changed
}

// auditPlatformAccount enriches the request's audit entry with the account id and the names of
// the fields written; handles and notes never reach the audit.
func (s *service) auditPlatformAccount(ctx context.Context, action string, id uint, fields []string) {
	s.auditChange(ctx, auditstarter.Change{
		Action: action, Resource: "platform_accounts", TargetType: "platform_account", TargetID: uintText(id),
		Result: domain.AuditResultSuccess, Metadata: map[string]any{"changed_fields": append([]string(nil), fields...)},
	})
}
