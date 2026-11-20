package mailbox

import (
	"context"
	"fmt"
	"strings"

	"github.com/fastagi/zmail/api/internal/domain"
)

// MaxImportRows bounds one import request.
const MaxImportRows = 500

// The import types live in the domain so operator commands share them; these aliases keep the
// module's names.
type (
	MailboxImportRow       = domain.MailboxImportRow
	MailboxImportStatus    = domain.MailboxImportStatus
	MailboxImportRowResult = domain.MailboxImportRowResult
	MailboxImportResult    = domain.MailboxImportResult
)

const (
	MailboxImportValid   = domain.MailboxImportValid
	MailboxImportCreated = domain.MailboxImportCreated
	MailboxImportFailed  = domain.MailboxImportFailed
)

// importMember is one parsed segment of a members cell.
type importMember struct {
	email      string
	permission domain.MailboxPermission
}

// preparedImportRow is a row after the checks that load nothing.
type preparedImportRow struct {
	address     string
	localPart   string
	domainName  string
	displayName string
	members     []importMember
	err         error
}

// ImportMailboxes creates one shared mailbox per row, each row on its own: a failed row never
// undoes another. A dry run makes the same checks, with the mailbox limit counted in row order,
// and writes nothing. Only owners and admins may import.
func (s *service) ImportMailboxes(ctx context.Context, viewer domain.OrganizationContext, rows []MailboxImportRow, dryRun bool) (*MailboxImportResult, error) {
	if !viewer.Role.CanManageOrganization() {
		return nil, domain.ErrPermissionDenied
	}
	if len(rows) == 0 || len(rows) > MaxImportRows {
		return nil, fmt.Errorf("%w: an import has 1 to %d rows", domain.ErrInvalidInput, MaxImportRows)
	}
	prepared := prepareImportRows(rows)
	members, err := s.importMembers(ctx, viewer.OrganizationID, prepared)
	if err != nil {
		return nil, err
	}
	var existing int64
	if dryRun {
		if s.directory == nil {
			return nil, domain.ErrServiceUnavailable
		}
		if existing, err = s.directory.CountMailboxes(ctx, viewer.OrganizationID); err != nil {
			return nil, err
		}
	}

	result := &MailboxImportResult{DryRun: dryRun, Rows: make([]MailboxImportRowResult, 0, len(rows))}
	planned := int64(0)
	for index, row := range prepared {
		outcome := MailboxImportRowResult{Row: index + 1, Address: row.address, MissingEmails: []string{}}
		mailboxID, missing, rowErr := s.importRow(ctx, viewer, row, members, dryRun, existing+planned)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		switch {
		case rowErr != nil:
			outcome.Status = MailboxImportFailed
			outcome.ErrorCode = importErrorCode(rowErr)
			if missing != nil {
				outcome.MissingEmails = missing
			}
			result.Failed++
		case dryRun:
			outcome.Status = MailboxImportValid
			planned++
			result.Succeeded++
		default:
			outcome.Status = MailboxImportCreated
			outcome.MailboxID = mailboxID
			result.Succeeded++
		}
		result.Rows = append(result.Rows, outcome)
	}
	return result, nil
}

// importRow checks one prepared row and, outside a dry run, creates its mailbox. counted is the
// organization's mailboxes plus the earlier valid rows of a dry run.
func (s *service) importRow(
	ctx context.Context,
	viewer domain.OrganizationContext,
	row preparedImportRow,
	directory map[string]*domain.OrganizationMembership,
	dryRun bool,
	counted int64,
) (uint, []string, error) {
	if row.err != nil {
		return 0, nil, row.err
	}
	var missing []string
	resolved := make([]resolvedMember, 0, len(row.members))
	input := CreateMailboxInput{
		LocalPart: row.localPart, DisplayName: row.displayName, Domain: row.domainName, Type: domain.MailboxTypeShared,
	}
	for _, member := range row.members {
		membership, found := directory[member.email]
		if !found {
			missing = append(missing, member.email)
			continue
		}
		resolved = append(resolved, resolvedMember{userID: membership.UserID, membershipID: membership.ID, permission: member.permission})
		input.Members = append(input.Members, MailboxMemberInput{UserID: membership.UserID, Permission: member.permission})
	}
	if len(missing) > 0 {
		return 0, missing, fmt.Errorf("%w: %s", ErrMailboxImportMemberNotFound, strings.Join(missing, ", "))
	}
	if err := validateMailboxAccess(input); err != nil {
		return 0, nil, err
	}
	plan, err := s.planMailbox(ctx, viewer, input, resolved, nil)
	if err != nil {
		return 0, nil, err
	}
	if dryRun {
		if counted >= int64(s.MaxMailboxes()) {
			return 0, nil, ErrMailboxLimitReached
		}
		return 0, nil, nil
	}
	mailbox, err := s.commitMailbox(ctx, viewer, plan)
	if err != nil {
		return 0, nil, err
	}
	return mailbox.ID, nil, nil
}

// importMembers looks up every member email of the file in one call, keyed by lowercased email.
func (s *service) importMembers(ctx context.Context, organizationID uint, rows []preparedImportRow) (map[string]*domain.OrganizationMembership, error) {
	seen := map[string]struct{}{}
	emails := make([]string, 0, len(rows))
	for _, row := range rows {
		for _, member := range row.members {
			if _, duplicate := seen[member.email]; !duplicate {
				seen[member.email] = struct{}{}
				emails = append(emails, member.email)
			}
		}
	}
	found := map[string]*domain.OrganizationMembership{}
	if len(emails) == 0 {
		return found, nil
	}
	memberships, err := s.memberships.FindMembershipsByEmails(ctx, organizationID, emails)
	if err != nil {
		return nil, err
	}
	for _, membership := range memberships {
		if membership.User != nil {
			found[strings.ToLower(membership.User.Email)] = membership
		}
	}
	return found, nil
}

// prepareImportRows makes the checks that load nothing, in order: formula guard, address split,
// duplicate of an earlier row, display name, and members grammar.
func prepareImportRows(rows []MailboxImportRow) []preparedImportRow {
	prepared := make([]preparedImportRow, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, raw := range rows {
		row := preparedImportRow{address: strings.TrimSpace(stripFormulaGuard(strings.TrimSpace(raw.Address)))}
		key := strings.ToLower(row.address)
		_, duplicate := seen[key]
		seen[key] = struct{}{}
		at := strings.LastIndex(row.address, "@")
		switch {
		case at <= 0 || at == len(row.address)-1:
			row.err = ErrMailboxAddressInvalid
		case duplicate:
			row.err = ErrMailboxImportDuplicateRow
		}
		if row.err == nil {
			row.localPart, row.domainName = row.address[:at], row.address[at+1:]
			row.displayName, row.err = normalizeDisplayName(stripFormulaGuard(raw.DisplayName))
		}
		if row.err == nil {
			row.members, row.err = parseImportMembers(stripFormulaGuard(strings.TrimSpace(raw.Members)))
		}
		if row.err != nil {
			row.members = nil
		}
		prepared = append(prepared, row)
	}
	return prepared
}

// parseImportMembers reads a members cell: segments separated by semicolons, each an email with
// an optional :view, :send, or :manage (send by default). Blank segments are skipped; 1 to
// domain.MaxMailboxMembers members, each email once ignoring case.
func parseImportMembers(cell string) ([]importMember, error) {
	segments := strings.Split(cell, ";")
	members := make([]importMember, 0, len(segments))
	seen := make(map[string]struct{}, len(segments))
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		email, permission := segment, domain.MailboxPermissionSend
		if colon := strings.Index(segment, ":"); colon >= 0 {
			email = strings.TrimSpace(segment[:colon])
			permission = domain.MailboxPermission(strings.ToLower(strings.TrimSpace(segment[colon+1:])))
		}
		normalized, ok := normalizeRecipient(email)
		if !ok {
			return nil, fmt.Errorf("%w: %q is not an email", ErrMailboxAccessInvalid, email)
		}
		if !permission.Valid() {
			return nil, fmt.Errorf("%w: the permission of %s must be view, send, or manage", ErrMailboxAccessInvalid, normalized)
		}
		if _, duplicate := seen[normalized]; duplicate {
			return nil, fmt.Errorf("%w: %s is listed twice", ErrMailboxAccessInvalid, normalized)
		}
		seen[normalized] = struct{}{}
		members = append(members, importMember{email: normalized, permission: permission})
	}
	if len(members) == 0 || len(members) > domain.MaxMailboxMembers {
		return nil, fmt.Errorf("%w: a shared mailbox needs 1 to %d members", ErrMailboxAccessInvalid, domain.MaxMailboxMembers)
	}
	return members, nil
}

// stripFormulaGuard removes the one apostrophe an export puts before a cell starting with =, +,
// -, or @, so a file exported and imported again keeps its values.
func stripFormulaGuard(value string) string {
	if len(value) >= 2 && value[0] == '\'' && strings.ContainsRune("=+-@", rune(value[1])) {
		return value[1:]
	}
	return value
}
