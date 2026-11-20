package mailbox

import (
	"context"
	"fmt"
	"sort"

	"github.com/fastagi/zmail/api/internal/domain"
)

// MailboxExportMember is one member of an exported mailbox. A personal mailbox's owner has no
// permission.
type MailboxExportMember struct {
	Email      string
	Permission domain.MailboxPermission
}

// MailboxExportRow is one exported mailbox with its counts and members ordered by email.
type MailboxExportRow struct {
	Entry   *domain.MailboxDirectoryEntry
	Members []MailboxExportMember
}

// ExportMailboxes reads every mailbox of one type, newest first and at most
// domain.MailboxesPerOrganizationCeiling, with the account emails of its members (of its owner for
// a personal mailbox). It is the one read that carries member emails; only owners and admins may.
func (s *service) ExportMailboxes(ctx context.Context, viewer domain.OrganizationContext, mailboxType domain.MailboxType) ([]MailboxExportRow, error) {
	if !viewer.Role.CanManageOrganization() {
		return nil, domain.ErrPermissionDenied
	}
	if mailboxType != domain.MailboxTypeShared && mailboxType != domain.MailboxTypePersonal {
		return nil, fmt.Errorf("%w: type must be shared or personal", domain.ErrInvalidInput)
	}
	if s.directory == nil {
		return nil, domain.ErrServiceUnavailable
	}
	entries, err := s.directory.ListMailboxDirectory(ctx, viewer.OrganizationID, domain.MailboxDirectoryFilter{
		Type: mailboxType, Limit: domain.MailboxesPerOrganizationCeiling,
	}, s.now().UTC().Add(-sendLimitWindow))
	if err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.Mailbox.ID)
	}
	members := map[uint][]*domain.MailboxMember{}
	userIDs := []uint{}
	if mailboxType == domain.MailboxTypeShared && len(ids) > 0 {
		listed, listErr := s.directory.ListMembersOfMailboxes(ctx, ids)
		if listErr != nil {
			return nil, listErr
		}
		for _, member := range listed {
			members[member.MailboxID] = append(members[member.MailboxID], member)
			userIDs = append(userIDs, member.UserID)
		}
	} else {
		for _, entry := range entries {
			if entry.Mailbox.OwnerUserID != nil {
				userIDs = append(userIDs, *entry.Mailbox.OwnerUserID)
			}
		}
	}
	emails, err := s.memberEmails(ctx, viewer.OrganizationID, userIDs)
	if err != nil {
		return nil, err
	}

	rows := make([]MailboxExportRow, 0, len(entries))
	for _, entry := range entries {
		row := MailboxExportRow{Entry: entry}
		if entry.Mailbox.OwnerUserID != nil {
			if email, ok := emails[*entry.Mailbox.OwnerUserID]; ok {
				row.Members = append(row.Members, MailboxExportMember{Email: email})
			}
		}
		for _, member := range members[entry.Mailbox.ID] {
			// A member who left the organization has no email to export.
			if email, ok := emails[member.UserID]; ok {
				row.Members = append(row.Members, MailboxExportMember{Email: email, Permission: member.Permission})
			}
		}
		sort.Slice(row.Members, func(i, j int) bool { return row.Members[i].Email < row.Members[j].Email })
		rows = append(rows, row)
	}
	return rows, nil
}

// memberEmails maps user ids of current organization members to their lowercased account emails.
func (s *service) memberEmails(ctx context.Context, organizationID uint, userIDs []uint) (map[uint]string, error) {
	emails := map[uint]string{}
	if len(userIDs) == 0 {
		return emails, nil
	}
	memberships, err := s.memberships.ListMembershipsByUserIDs(ctx, organizationID, userIDs)
	if err != nil {
		return nil, err
	}
	for _, membership := range memberships {
		if membership.User != nil && membership.User.Email != "" {
			emails[membership.UserID] = normalizedEmail(membership.User.Email)
		}
	}
	return emails, nil
}

// normalizedEmail lowercases an account email the way import matching compares it.
func normalizedEmail(email string) string {
	if normalized, ok := normalizeRecipient(email); ok {
		return normalized
	}
	return email
}
