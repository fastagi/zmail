package mailbox

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"

	"github.com/fastagi/zmail/api/internal/domain"
)

// MaxMemberImportRows bounds one roster import request, like a mailbox import.
const MaxMemberImportRows = MaxImportRows

// maxInvitationEmailLength is the longest email an organization invitation stores.
const maxInvitationEmailLength = 100

// MemberImportStatus is the outcome of one roster row.
type MemberImportStatus string

const (
	// MemberImportValid is a dry-run row that a real run would invite or create.
	MemberImportValid MemberImportStatus = "valid"
	// MemberImportInvited sent an invitation and reserved the mailbox, or completed either half.
	MemberImportInvited MemberImportStatus = "invited"
	// MemberImportCreated gave an existing member without a personal mailbox their mailbox.
	MemberImportCreated MemberImportStatus = "created"
	// MemberImportSkipped had nothing to do; SkipReason says why.
	MemberImportSkipped MemberImportStatus = "skipped"
	// MemberImportFailed was refused; ErrorCode says why.
	MemberImportFailed MemberImportStatus = "failed"
)

// MemberImportSkipReason says why a roster row needed no change.
type MemberImportSkipReason string

const (
	// SkipAlreadyMember is a current member who already owns a personal mailbox.
	SkipAlreadyMember MemberImportSkipReason = "already_member"
	// SkipAlreadyInvited has a pending invitation and the mailbox reserved for it already.
	SkipAlreadyInvited MemberImportSkipReason = "already_invited"
	// SkipMailboxTaken names an address another person or a shared mailbox holds, or held.
	SkipMailboxTaken MemberImportSkipReason = "mailbox_taken"
)

// MemberImportRow is one roster row as uploaded.
type MemberImportRow struct {
	Email   string
	Name    string
	Role    string
	Mailbox string
}

// MemberImportInput is one roster import: the rows, the verified domain their mailboxes default
// to, and the grants every invited member starts with.
type MemberImportInput struct {
	DryRun   bool
	Domain   string
	Features domain.MemberFeatures
	Rows     []MemberImportRow
}

// MemberImportRowResult is the outcome of one row, in request order.
type MemberImportRowResult struct {
	Row        int
	Email      string
	Address    string
	Status     MemberImportStatus
	SkipReason MemberImportSkipReason
	ErrorCode  string
	MailboxID  uint
	// InvitationID is the pending invitation the row created or found; zero otherwise.
	InvitationID uint
	// EmailSendStatus is the invitation email outcome when the row sent one.
	EmailSendStatus string
	// WelcomeStatus is the welcome mail outcome of a row whose mailbox is provisioned; empty for a
	// dry run and for rows that provisioned nothing. WelcomeErrorCode says why one failed.
	WelcomeStatus    MemberWelcomeStatus
	WelcomeErrorCode string
}

// MemberImportResult counts the rows by outcome.
type MemberImportResult struct {
	DryRun  bool
	Total   int
	Valid   int
	Invited int
	Created int
	Skipped int
	Failed  int
	// WelcomeQueued counts rows whose welcome mail was queued now; WelcomeFailed counts rows whose
	// welcome mail failed or was suppressed. Neither changes a row's status.
	WelcomeQueued int
	WelcomeFailed int
	Rows          []MemberImportRowResult
}

// preparedMemberRow is a roster row after the checks that load nothing.
type preparedMemberRow struct {
	email       string
	displayName string
	role        domain.OrganizationRole
	localPart   string
	domainName  string
	err         error
}

func (r preparedMemberRow) address() string {
	if r.localPart == "" || r.domainName == "" {
		return ""
	}
	return r.localPart + "@" + r.domainName
}

// ImportMembers invites every roster row, reserves a personal mailbox on a verified organization
// domain for each, and gives each invitation the requested grants. Each row reconciles toward
// that end state on its own, so sending the same file again changes nothing and a row that
// failed halfway completes on retry. A dry run makes the same checks and writes nothing. Only
// owners and admins may import.
func (s *service) ImportMembers(ctx context.Context, viewer domain.OrganizationContext, input MemberImportInput) (*MemberImportResult, error) {
	if !viewer.Role.CanManageOrganization() {
		return nil, domain.ErrPermissionDenied
	}
	if len(input.Rows) == 0 || len(input.Rows) > MaxMemberImportRows {
		return nil, fmt.Errorf("%w: an import has 1 to %d rows", domain.ErrInvalidInput, MaxMemberImportRows)
	}
	if s.inviter == nil || s.roster == nil || s.directory == nil {
		return nil, domain.ErrServiceUnavailable
	}
	defaultDomain := normalizeImportDomain(input.Domain)
	prepared := prepareMemberRows(input.Rows, defaultDomain)
	members, err := s.rosterMembers(ctx, viewer.OrganizationID, prepared)
	if err != nil {
		return nil, err
	}
	var counted int64
	if input.DryRun {
		if counted, err = s.directory.CountMailboxes(ctx, viewer.OrganizationID); err != nil {
			return nil, err
		}
	}

	result := &MemberImportResult{DryRun: input.DryRun, Total: len(prepared), Rows: make([]MemberImportRowResult, 0, len(prepared))}
	for index, row := range prepared {
		outcome := s.importMemberRow(ctx, viewer, input, row, members[row.email], &counted)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		outcome.Row = index + 1
		outcome.Email = row.email
		if outcome.Address == "" {
			outcome.Address = row.address()
		}
		if !input.DryRun {
			s.welcomeImportedMember(ctx, viewer, row, &outcome)
			switch outcome.WelcomeStatus {
			case MemberWelcomeQueued:
				result.WelcomeQueued++
			case MemberWelcomeFailed, MemberWelcomeSuppressed:
				result.WelcomeFailed++
			case MemberWelcomeAlreadySent, MemberWelcomeDisabled, MemberWelcomeNotConfigured, "":
			}
		}
		switch outcome.Status {
		case MemberImportValid:
			result.Valid++
		case MemberImportInvited:
			result.Invited++
		case MemberImportCreated:
			result.Created++
		case MemberImportSkipped:
			result.Skipped++
		default:
			result.Failed++
		}
		result.Rows = append(result.Rows, outcome)
	}
	return result, nil
}

// welcomeImportedMember sends the welcome mail of a row whose mailbox is provisioned: invited,
// created, or already invited by an earlier upload, which completes a welcome that failed then.
// The mail goes to the row's email, the address the person reads until they first sign in. A
// welcome failure is reported on the row and never changes its status.
func (s *service) welcomeImportedMember(ctx context.Context, viewer domain.OrganizationContext, row preparedMemberRow, outcome *MemberImportRowResult) {
	provisioned := outcome.Status == MemberImportInvited || outcome.Status == MemberImportCreated ||
		(outcome.Status == MemberImportSkipped && outcome.SkipReason == SkipAlreadyInvited)
	if !provisioned || outcome.MailboxID == 0 {
		return
	}
	mailbox := &domain.Mailbox{ID: outcome.MailboxID, OrganizationID: viewer.OrganizationID, Address: outcome.Address, DisplayName: row.displayName}
	welcome := s.welcomeMember(ctx, viewer, mailbox, outcome.Email, row.displayName)
	outcome.WelcomeStatus, outcome.WelcomeErrorCode = welcome.Status, welcome.ErrorCode
}

// importMemberRow reconciles one row. counted carries the organization's mailboxes plus the
// mailboxes earlier rows of a dry run would create, for the limit check.
func (s *service) importMemberRow(
	ctx context.Context,
	viewer domain.OrganizationContext,
	input MemberImportInput,
	row preparedMemberRow,
	membership *domain.OrganizationMembership,
	counted *int64,
) MemberImportRowResult {
	if row.err != nil {
		return failedMemberRow(row.err)
	}
	if membership != nil {
		return s.importExistingMember(ctx, viewer, input.DryRun, row, membership, counted)
	}
	return s.importInvitee(ctx, viewer, input, row, counted)
}

// importExistingMember gives a current member a personal mailbox when they own none. Their role
// and grants are left as they are: an import never changes what a member already has.
func (s *service) importExistingMember(
	ctx context.Context,
	viewer domain.OrganizationContext,
	dryRun bool,
	row preparedMemberRow,
	membership *domain.OrganizationMembership,
	counted *int64,
) MemberImportRowResult {
	owns, err := s.roster.OwnsPersonalMailbox(ctx, viewer.OrganizationID, membership.UserID)
	if err != nil {
		return failedMemberRow(err)
	}
	if owns {
		return skippedMemberRow(SkipAlreadyMember)
	}
	reserved, err := s.reservationAt(ctx, viewer.OrganizationID, row)
	if err != nil {
		return skippedOrFailed(err)
	}
	if reserved != nil {
		// The person was invited earlier and joined some other way: hand over the reservation.
		if dryRun {
			return MemberImportRowResult{Status: MemberImportValid, MailboxID: reserved.ID}
		}
		claimed, claimErr := s.claimReserved(ctx, viewer.OrganizationID, membership.UserID, row.email)
		if claimErr != nil {
			return failedMemberRow(claimErr)
		}
		return MemberImportRowResult{Status: MemberImportCreated, MailboxID: firstMailboxID(claimed, reserved.ID)}
	}
	input := CreateMailboxInput{
		LocalPart: row.localPart, DisplayName: row.displayName, Domain: row.domainName, Type: domain.MailboxTypePersonal,
		OwnerUserID: membership.UserID,
	}
	mailbox, err := s.createRosterMailbox(ctx, viewer, input, membership, "", dryRun, counted)
	if err != nil {
		return skippedOrFailed(err)
	}
	if dryRun {
		return MemberImportRowResult{Status: MemberImportValid}
	}
	return MemberImportRowResult{Status: MemberImportCreated, MailboxID: mailbox.ID}
}

// importInvitee reserves the mailbox first, then invites, so a refused address never sends an
// invitation. Either half that already exists is reused.
func (s *service) importInvitee(
	ctx context.Context,
	viewer domain.OrganizationContext,
	input MemberImportInput,
	row preparedMemberRow,
	counted *int64,
) MemberImportRowResult {
	pending, err := s.inviter.FindPendingInvitationByEmail(ctx, viewer.OrganizationID, row.email)
	if err != nil {
		return failedMemberRow(err)
	}
	reserved, err := s.reservationAt(ctx, viewer.OrganizationID, row)
	if err != nil {
		return skippedOrFailed(err)
	}
	if pending != nil && reserved != nil {
		return MemberImportRowResult{
			Status: MemberImportSkipped, SkipReason: SkipAlreadyInvited, MailboxID: reserved.ID, InvitationID: pending.ID,
		}
	}

	var mailboxID uint
	if reserved != nil {
		mailboxID = reserved.ID
	} else {
		create := CreateMailboxInput{
			LocalPart: row.localPart, DisplayName: row.displayName, Domain: row.domainName, Type: domain.MailboxTypePersonal,
		}
		mailbox, createErr := s.createRosterMailbox(ctx, viewer, create, nil, row.email, input.DryRun, counted)
		if createErr != nil {
			return skippedOrFailed(createErr)
		}
		if mailbox != nil {
			mailboxID = mailbox.ID
		}
	}
	if input.DryRun {
		return MemberImportRowResult{Status: MemberImportValid, MailboxID: mailboxID}
	}

	outcome := MemberImportRowResult{Status: MemberImportInvited, MailboxID: mailboxID}
	if pending != nil {
		outcome.InvitationID = pending.ID
		return outcome
	}
	invitation, emailStatus, err := s.inviter.InviteRosterMember(ctx, viewer.UserID, viewer.OrganizationID, row.email, row.role, input.Features)
	switch {
	case errors.Is(err, domain.ErrOrganizationMemberAlreadyExists):
		// The person joined since the lookup; the join claimed or will claim the mailbox.
		return MemberImportRowResult{Status: MemberImportSkipped, SkipReason: SkipAlreadyMember, MailboxID: mailboxID}
	case errors.Is(err, domain.ErrOrganizationInvitationAlreadyPending):
		return MemberImportRowResult{Status: MemberImportSkipped, SkipReason: SkipAlreadyInvited, MailboxID: mailboxID}
	case err != nil:
		// The reservation stays: sending the row again reuses it and retries the invitation.
		failed := failedMemberRow(err)
		failed.MailboxID = mailboxID
		return failed
	}
	outcome.InvitationID = invitation.ID
	outcome.EmailSendStatus = emailStatus
	return outcome
}

// reservationAt returns the live personal mailbox at the row's address reserved for the row's
// email, nil when the address is free, and ErrMailboxAddressTaken when anything else holds it.
func (s *service) reservationAt(ctx context.Context, organizationID uint, row preparedMemberRow) (*domain.Mailbox, error) {
	address := row.address()
	mailbox, err := s.repo.FindMailboxByAddress(ctx, address)
	if errors.Is(err, ErrMailboxNotFound) {
		// A deleted mailbox keeps its address, and the planned create checks for it.
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if mailbox.OrganizationID == organizationID && mailbox.Type == domain.MailboxTypePersonal &&
		mailbox.OwnerUserID == nil && mailbox.PendingOwnerEmail == row.email {
		return mailbox, nil
	}
	return nil, ErrMailboxAddressTaken
}

// createRosterMailbox plans a personal mailbox for an owner or, with pendingEmail, reserved for an
// invitee, and stores it outside a dry run. A dry run checks the limit against counted instead and
// returns a nil mailbox.
func (s *service) createRosterMailbox(
	ctx context.Context,
	viewer domain.OrganizationContext,
	input CreateMailboxInput,
	owner *domain.OrganizationMembership,
	pendingEmail string,
	dryRun bool,
	counted *int64,
) (*domain.Mailbox, error) {
	plan, err := s.planMailbox(ctx, viewer, input, nil, owner)
	if err != nil {
		return nil, err
	}
	if plan.connected == nil || strings.HasSuffix(plan.mailbox.Address, "@"+s.settings.Domain) {
		return nil, ErrMemberImportDomainRequired
	}
	if dryRun {
		if *counted >= int64(s.MaxMailboxes()) {
			return nil, ErrMailboxLimitReached
		}
		*counted++
		return nil, nil
	}
	plan.mailbox.PendingOwnerEmail = pendingEmail
	return s.commitMailbox(ctx, viewer, plan)
}

// claimReserved makes the user the owner of the mailboxes reserved for email and records each
// change in the mailbox's access history.
func (s *service) claimReserved(ctx context.Context, organizationID, userID uint, email string) ([]*domain.Mailbox, error) {
	var claimed []*domain.Mailbox
	err := s.repo.InTransaction(ctx, func(tx context.Context) error {
		var claimErr error
		if claimed, claimErr = s.roster.ClaimReservedMailboxes(tx, organizationID, userID, email); claimErr != nil {
			return claimErr
		}
		for _, mailbox := range claimed {
			if err := s.recordAccess(tx, mailbox, domain.MailboxAccessOwnerAssigned, 0, userID, map[string]any{}); err != nil {
				return err
			}
		}
		return nil
	})
	return claimed, err
}

// claimOnJoin gives a joining member the mailboxes a roster import reserved for their account
// email. It runs inside the join handler's transaction.
func (s *service) claimOnJoin(ctx context.Context, organizationID, userID uint) error {
	if s.roster == nil || s.accounts == nil {
		return nil
	}
	account, err := s.accounts.FindByID(ctx, userID)
	if err != nil {
		return err
	}
	if account == nil || account.Email == "" {
		return nil
	}
	claimed, err := s.roster.ClaimReservedMailboxes(ctx, organizationID, userID, strings.ToLower(strings.TrimSpace(account.Email)))
	if err != nil {
		return err
	}
	for _, mailbox := range claimed {
		if err := s.recordAccess(ctx, mailbox, domain.MailboxAccessOwnerAssigned, 0, userID, map[string]any{}); err != nil {
			return err
		}
	}
	return nil
}

// rosterMembers looks up every valid row email among current members in one call.
func (s *service) rosterMembers(ctx context.Context, organizationID uint, rows []preparedMemberRow) (map[string]*domain.OrganizationMembership, error) {
	emails := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.err == nil {
			emails = append(emails, row.email)
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

// prepareMemberRows makes the checks that load nothing, in order: formula guard, email, role,
// mailbox address, display name, and a duplicate email or address of an earlier row.
func prepareMemberRows(rows []MemberImportRow, defaultDomain string) []preparedMemberRow {
	prepared := make([]preparedMemberRow, 0, len(rows))
	seenEmails := make(map[string]struct{}, len(rows))
	seenAddresses := make(map[string]struct{}, len(rows))
	for _, raw := range rows {
		row := prepareMemberRow(raw, defaultDomain)
		if row.err == nil {
			_, emailSeen := seenEmails[row.email]
			_, addressSeen := seenAddresses[row.address()]
			if emailSeen || addressSeen {
				row.err = ErrMailboxImportDuplicateRow
			}
		}
		if row.email != "" {
			seenEmails[row.email] = struct{}{}
		}
		if address := row.address(); address != "" {
			seenAddresses[address] = struct{}{}
		}
		prepared = append(prepared, row)
	}
	return prepared
}

func prepareMemberRow(raw MemberImportRow, defaultDomain string) preparedMemberRow {
	row := preparedMemberRow{email: strings.ToLower(strings.TrimSpace(stripFormulaGuard(strings.TrimSpace(raw.Email))))}
	if !validRosterEmail(row.email) {
		row.err = ErrMemberImportEmailInvalid
		return row
	}
	switch strings.ToLower(strings.TrimSpace(raw.Role)) {
	case "", string(domain.OrganizationRoleMember):
		row.role = domain.OrganizationRoleMember
	case string(domain.OrganizationRoleAdmin):
		row.role = domain.OrganizationRoleAdmin
	default:
		row.err = ErrMemberImportRoleInvalid
		return row
	}
	mailbox := strings.ToLower(strings.TrimSpace(stripFormulaGuard(strings.TrimSpace(raw.Mailbox))))
	localPart, domainName := mailbox, defaultDomain
	if at := strings.LastIndex(mailbox, "@"); at >= 0 {
		localPart, domainName = mailbox[:at], normalizeImportDomain(mailbox[at+1:])
	}
	if localPart == "" {
		localPart = defaultLocalPart(row.email)
	}
	normalized, err := normalizeLocalPart(localPart)
	if err != nil {
		row.err = err
		return row
	}
	if domainName == "" {
		row.err = ErrMemberImportDomainRequired
		return row
	}
	row.localPart, row.domainName = normalized, domainName
	row.displayName, row.err = normalizeDisplayName(stripFormulaGuard(raw.Name))
	return row
}

// defaultLocalPart takes the email's local part without a +tag: alice+hr@gmail.com gives alice.
func defaultLocalPart(email string) string {
	local, _, _ := strings.Cut(email, "@")
	local, _, _ = strings.Cut(local, "+")
	return local
}

func validRosterEmail(email string) bool {
	if email == "" || len(email) > maxInvitationEmailLength {
		return false
	}
	address, err := mail.ParseAddress(email)
	return err == nil && address.Address == email
}

func normalizeImportDomain(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}

func failedMemberRow(err error) MemberImportRowResult {
	return MemberImportRowResult{Status: MemberImportFailed, ErrorCode: importErrorCode(err)}
}

func skippedMemberRow(reason MemberImportSkipReason) MemberImportRowResult {
	return MemberImportRowResult{Status: MemberImportSkipped, SkipReason: reason}
}

// skippedOrFailed reports a taken address as a skip, which needs a new address rather than a
// retry, and anything else as a failure.
func skippedOrFailed(err error) MemberImportRowResult {
	if errors.Is(err, ErrMailboxAddressTaken) {
		return skippedMemberRow(SkipMailboxTaken)
	}
	return failedMemberRow(err)
}

func firstMailboxID(mailboxes []*domain.Mailbox, fallback uint) uint {
	if len(mailboxes) > 0 {
		return mailboxes[0].ID
	}
	return fallback
}
