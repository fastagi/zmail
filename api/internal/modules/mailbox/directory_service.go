package mailbox

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/fastagi/zmail/api/internal/domain"
)

const (
	// DefaultDirectoryPageSize and MaxDirectoryPageSize bound one directory page.
	DefaultDirectoryPageSize = 15
	MaxDirectoryPageSize     = 100
	// MaxDirectoryQueryLength bounds a trimmed directory search.
	MaxDirectoryQueryLength = 120
)

// MailboxDirectoryPage is one directory page with the organization-wide figures shown above it.
type MailboxDirectoryPage struct {
	Entries []*domain.MailboxDirectoryEntry
	// NextBeforeID is the id the next, older page starts below; 0 on the last page.
	NextBeforeID uint
	// MailboxCount counts the organization's live mailboxes of every type.
	MailboxCount int64
	// SentLast24h counts the organization's personal-lane send attempts in the rolling window,
	// the figure the personal send limit is checked against.
	SentLast24h int64
}

// MailboxDirectory lists one page of the organization's mailboxes for owners and admins, newest
// first, with member counts and send attempts. It never reads mail.
func (s *service) MailboxDirectory(ctx context.Context, viewer domain.OrganizationContext, filter domain.MailboxDirectoryFilter) (*MailboxDirectoryPage, error) {
	if !viewer.Role.CanManageOrganization() {
		return nil, domain.ErrPermissionDenied
	}
	if s.directory == nil {
		return nil, domain.ErrServiceUnavailable
	}
	if filter.Type != "" && filter.Type != domain.MailboxTypeShared && filter.Type != domain.MailboxTypePersonal {
		return nil, fmt.Errorf("%w: type must be shared or personal", domain.ErrInvalidInput)
	}
	filter.Query = strings.TrimSpace(filter.Query)
	if !utf8.ValidString(filter.Query) || utf8.RuneCountInString(filter.Query) > MaxDirectoryQueryLength ||
		strings.IndexFunc(filter.Query, unicode.IsControl) >= 0 {
		return nil, fmt.Errorf("%w: q must be at most %d characters", domain.ErrInvalidInput, MaxDirectoryQueryLength)
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = DefaultDirectoryPageSize
	}
	limit = min(limit, MaxDirectoryPageSize)
	filter.Limit = limit + 1

	since := s.now().UTC().Add(-sendLimitWindow)
	entries, err := s.directory.ListMailboxDirectory(ctx, viewer.OrganizationID, filter, since)
	if err != nil {
		return nil, err
	}
	page := &MailboxDirectoryPage{Entries: entries}
	if len(entries) > limit {
		page.Entries = entries[:limit]
		page.NextBeforeID = page.Entries[limit-1].Mailbox.ID
	}
	if page.MailboxCount, err = s.directory.CountMailboxes(ctx, viewer.OrganizationID); err != nil {
		return nil, err
	}
	if page.SentLast24h, err = s.repo.CountOutboundSince(ctx, viewer.OrganizationID, domain.SendLanePersonal, since); err != nil {
		return nil, err
	}
	return page, nil
}

// normalizeDisplayName collapses whitespace and applies the display-name rules of a create.
func normalizeDisplayName(raw string) (string, error) {
	displayName := strings.Join(strings.Fields(raw), " ")
	if len(displayName) > 120 || strings.ContainsAny(displayName, "<>\"") {
		return "", fmt.Errorf("%w: display name", ErrMailboxAddressInvalid)
	}
	return displayName, nil
}

// RenameMailbox changes a mailbox's display name and records it in the access history, in one
// transaction. Only owners and admins may; an unchanged name writes nothing. Renaming grants the
// viewer no access to the mail.
func (s *service) RenameMailbox(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, displayName string) (*domain.Mailbox, error) {
	if !viewer.Role.CanManageOrganization() {
		return nil, domain.ErrPermissionDenied
	}
	normalized, err := normalizeDisplayName(displayName)
	if err != nil {
		return nil, err
	}
	mailbox, err := s.repo.FindMailbox(ctx, viewer.OrganizationID, mailboxID)
	if err != nil {
		return nil, err
	}
	if mailbox.DisplayName != normalized {
		previous := mailbox.DisplayName
		err = s.repo.InTransaction(ctx, func(tx context.Context) error {
			if updateErr := s.repo.UpdateMailboxDisplayName(tx, viewer.OrganizationID, mailbox.ID, normalized); updateErr != nil {
				return updateErr
			}
			return s.recordAccess(tx, mailbox, domain.MailboxAccessRenamed, viewer.UserID, 0, map[string]any{
				"from": previous,
				"to":   normalized,
			})
		})
		if err != nil {
			return nil, err
		}
		mailbox.DisplayName = normalized
	}
	if mailbox.Access, err = s.accessFor(ctx, viewer, mailbox); err != nil {
		return nil, err
	}
	return mailbox, nil
}
