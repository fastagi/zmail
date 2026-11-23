package mailbox

import (
	"context"
	"slices"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
)

// ClaimReservedMailboxes and OwnsPersonalMailbox make the fake repository a roster store.
func (r *fakeRepository) ClaimReservedMailboxes(_ context.Context, organizationID, userID uint, email string) ([]*domain.Mailbox, error) {
	var claimed []*domain.Mailbox
	for _, mailbox := range r.mailboxes {
		if mailbox.OrganizationID == organizationID && mailbox.Type == domain.MailboxTypePersonal &&
			mailbox.OwnerUserID == nil && mailbox.PendingOwnerEmail == email {
			owner := userID
			mailbox.OwnerUserID = &owner
			mailbox.PendingOwnerEmail = ""
			copied := *mailbox
			claimed = append(claimed, &copied)
		}
	}
	return claimed, nil
}

func (r *fakeRepository) OwnsPersonalMailbox(_ context.Context, organizationID, userID uint) (bool, error) {
	for _, mailbox := range r.mailboxes {
		if mailbox.OrganizationID == organizationID && mailbox.Type == domain.MailboxTypePersonal &&
			mailbox.OwnerUserID != nil && *mailbox.OwnerUserID == userID {
			return true, nil
		}
	}
	return false, nil
}

// fakeInviter is the organization starter's roster inviter: pending invitations by email.
type fakeInviter struct {
	pending map[string]*domain.OrganizationInvitation
	sent    []*domain.OrganizationInvitation
	err     error
}

func (f *fakeInviter) FindPendingInvitationByEmail(_ context.Context, _ uint, email string) (*domain.OrganizationInvitation, error) {
	return f.pending[email], nil
}

func (f *fakeInviter) InviteRosterMember(_ context.Context, actorUserID, organizationID uint, email string, role domain.OrganizationRole, features domain.MemberFeatures) (*domain.OrganizationInvitation, string, error) {
	if f.err != nil {
		return nil, "", f.err
	}
	invitation := &domain.OrganizationInvitation{
		ID: uint(len(f.sent) + 500), OrganizationID: organizationID, InvitedBy: actorUserID,
		Email: email, Role: role, Features: features,
	}
	f.sent = append(f.sent, invitation)
	f.pending[email] = invitation
	return invitation, "accepted_by_provider", nil
}

type rosterAccounts map[uint]string

func (f rosterAccounts) FindByID(_ context.Context, id uint) (*domain.User, error) {
	return &domain.User{ID: id, Email: f[id]}, nil
}

type rosterFixture struct {
	*domainFixture
	inviter *fakeInviter
}

func newRosterFixture(t *testing.T) *rosterFixture {
	t.Helper()
	base := newDomainFixture(t)
	base.connect(t, "news.example.com")
	inviter := &fakeInviter{pending: map[string]*domain.OrganizationInvitation{}}
	base.service.inviter = inviter
	return &rosterFixture{domainFixture: base, inviter: inviter}
}

func (f *rosterFixture) run(t *testing.T, dryRun bool, rows ...MemberImportRow) *MemberImportResult {
	t.Helper()
	result, err := f.service.ImportMembers(context.Background(), as(7), MemberImportInput{
		DryRun: dryRun, Domain: "news.example.com", Features: domain.MemberFeatures{Broadcasts: true}, Rows: rows,
	})
	if err != nil {
		t.Fatalf("ImportMembers: %v", err)
	}
	return result
}

// rosterOutcomes renders each row as status, status:reason, or status:code.
func rosterOutcomes(result *MemberImportResult) []string {
	rendered := make([]string, 0, len(result.Rows))
	for _, row := range result.Rows {
		switch {
		case row.SkipReason != "":
			rendered = append(rendered, string(row.Status)+":"+string(row.SkipReason))
		case row.ErrorCode != "":
			rendered = append(rendered, string(row.Status)+":"+row.ErrorCode)
		default:
			rendered = append(rendered, string(row.Status))
		}
	}
	return rendered
}

func (f *rosterFixture) mailboxAt(address string) *domain.Mailbox {
	for _, mailbox := range f.repo.mailboxes {
		if mailbox.Address == address {
			return mailbox
		}
	}
	return nil
}

func TestRosterDryRunChecksEveryRowAndWritesNothing(t *testing.T) {
	f := newRosterFixture(t)
	result := f.run(t, true,
		MemberImportRow{Email: "Alice@Gmail.com", Name: "Alice", Mailbox: "alice"},
		MemberImportRow{Email: "bob@outlook.com", Role: "owner"},
		MemberImportRow{Email: "not-an-email"},
		MemberImportRow{Email: "alice@gmail.com"},
		MemberImportRow{Email: "carol@gmail.com", Mailbox: "carol@elsewhere.test"},
		MemberImportRow{Email: "dan+hr@gmail.com"},
	)
	want := []string{
		"valid", "failed:MAILBOX.IMPORT_ROLE_INVALID", "failed:MAILBOX.IMPORT_EMAIL_INVALID",
		"failed:MAILBOX.IMPORT_DUPLICATE_ROW", "failed:MAILBOX.DOMAIN_NOT_FOUND", "valid",
	}
	if got := rosterOutcomes(result); !slices.Equal(got, want) {
		t.Fatalf("outcomes = %v; want %v", got, want)
	}
	if result.Rows[0].Address != "alice@news.example.com" || result.Rows[5].Address != "dan@news.example.com" {
		t.Fatalf("addresses = %q, %q", result.Rows[0].Address, result.Rows[5].Address)
	}
	if result.Valid != 2 || result.Failed != 4 || result.Total != 6 {
		t.Fatalf("counts = %+v", result)
	}
	if len(f.repo.mailboxes) != 0 || len(f.inviter.sent) != 0 {
		t.Fatalf("a dry run wrote %d mailboxes and %d invitations", len(f.repo.mailboxes), len(f.inviter.sent))
	}
}

func TestRosterInvitesReservesAMailboxAndCarriesTheGrants(t *testing.T) {
	f := newRosterFixture(t)
	result := f.run(t, false,
		MemberImportRow{Email: "alice@gmail.com", Name: "Alice Wang", Mailbox: "alice"},
		MemberImportRow{Email: "bob@outlook.com", Role: "admin"},
	)
	if got := rosterOutcomes(result); !slices.Equal(got, []string{"invited", "invited"}) {
		t.Fatalf("outcomes = %v", got)
	}
	reserved := f.mailboxAt("alice@news.example.com")
	if reserved == nil || reserved.Type != domain.MailboxTypePersonal || reserved.OwnerUserID != nil ||
		reserved.PendingOwnerEmail != "alice@gmail.com" || reserved.DisplayName != "Alice Wang" {
		t.Fatalf("reserved mailbox = %+v", reserved)
	}
	if len(f.zones.rules) != 2 {
		t.Fatalf("routing rules = %d; want one per reserved mailbox", len(f.zones.rules))
	}
	if len(f.inviter.sent) != 2 || f.inviter.sent[1].Role != domain.OrganizationRoleAdmin ||
		f.inviter.sent[0].Features != (domain.MemberFeatures{Broadcasts: true}) {
		t.Fatalf("invitations = %+v", f.inviter.sent)
	}
	if result.Rows[0].InvitationID == 0 || result.Rows[0].MailboxID != reserved.ID || result.Rows[0].EmailSendStatus != "accepted_by_provider" {
		t.Fatalf("row = %+v", result.Rows[0])
	}
}

// TestRosterDeveloperRowsInviteDevelopers: a developer=yes row carries developer access for a
// member; an admin row never needs it.
func TestRosterDeveloperRowsInviteDevelopers(t *testing.T) {
	f := newRosterFixture(t)
	f.run(t, false,
		MemberImportRow{Email: "dev@gmail.com", Developer: true},
		MemberImportRow{Email: "plain@gmail.com"},
		MemberImportRow{Email: "boss@gmail.com", Role: "admin", Developer: true},
	)
	if len(f.inviter.sent) != 3 {
		t.Fatalf("invitations = %d", len(f.inviter.sent))
	}
	if !f.inviter.sent[0].Features.Developer || f.inviter.sent[1].Features.Developer || f.inviter.sent[2].Features.Developer {
		t.Fatalf("developer grants = %v %v %v", f.inviter.sent[0].Features.Developer,
			f.inviter.sent[1].Features.Developer, f.inviter.sent[2].Features.Developer)
	}
}

func TestRosterReuploadIsIdempotent(t *testing.T) {
	f := newRosterFixture(t)
	rows := []MemberImportRow{{Email: "alice@gmail.com"}, {Email: "bob@outlook.com"}}
	f.run(t, false, rows...)
	again := f.run(t, false, rows...)
	if got := rosterOutcomes(again); !slices.Equal(got, []string{"skipped:already_invited", "skipped:already_invited"}) {
		t.Fatalf("outcomes = %v", got)
	}
	if len(f.inviter.sent) != 2 || len(f.repo.mailboxes) != 2 {
		t.Fatalf("re-upload sent %d invitations and kept %d mailboxes", len(f.inviter.sent), len(f.repo.mailboxes))
	}
}

func TestRosterRetryCompletesAHalfDoneRow(t *testing.T) {
	f := newRosterFixture(t)
	f.inviter.err = domain.ErrServiceUnavailable
	first := f.run(t, false, MemberImportRow{Email: "alice@gmail.com"})
	if first.Rows[0].Status != MemberImportFailed || first.Rows[0].ErrorCode == "" || first.Rows[0].MailboxID == 0 {
		t.Fatalf("first = %+v", first.Rows[0])
	}
	if f.mailboxAt("alice@news.example.com") == nil {
		t.Fatalf("the reservation should stay for the retry")
	}
	f.inviter.err = nil
	retry := f.run(t, false, MemberImportRow{Email: "alice@gmail.com"})
	if got := rosterOutcomes(retry); !slices.Equal(got, []string{"invited"}) {
		t.Fatalf("retry = %v", got)
	}
	if len(f.repo.mailboxes) != 1 || len(f.inviter.sent) != 1 {
		t.Fatalf("retry made %d mailboxes and %d invitations", len(f.repo.mailboxes), len(f.inviter.sent))
	}
}

func TestRosterSkipsATakenAddressWithoutInviting(t *testing.T) {
	f := newRosterFixture(t)
	if _, err := f.service.CreateMailbox(context.Background(), as(7), sharedInput("sales", "", "news.example.com")); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	result := f.run(t, false, MemberImportRow{Email: "sam@gmail.com", Mailbox: "sales"})
	if got := rosterOutcomes(result); !slices.Equal(got, []string{"skipped:mailbox_taken"}) {
		t.Fatalf("outcomes = %v", got)
	}
	if len(f.inviter.sent) != 0 {
		t.Fatalf("a taken address must not send an invitation")
	}
}

func TestRosterGivesAnExistingMemberTheirMailboxOnce(t *testing.T) {
	f := newRosterFixture(t)
	// u3@people.test is user 3, a current member of every organization in the fake directory.
	first := f.run(t, false, MemberImportRow{Email: "u3@people.test", Mailbox: "three"})
	if got := rosterOutcomes(first); !slices.Equal(got, []string{"created"}) {
		t.Fatalf("first = %v", got)
	}
	created := f.mailboxAt("three@news.example.com")
	if created == nil || created.OwnerUserID == nil || *created.OwnerUserID != 3 || created.PendingOwnerEmail != "" {
		t.Fatalf("mailbox = %+v", created)
	}
	again := f.run(t, false, MemberImportRow{Email: "u3@people.test", Mailbox: "three"})
	if got := rosterOutcomes(again); !slices.Equal(got, []string{"skipped:already_member"}) {
		t.Fatalf("again = %v", got)
	}
	if len(f.inviter.sent) != 0 {
		t.Fatalf("a member is never invited")
	}
}

func TestRosterRefusesTheSharedDomain(t *testing.T) {
	f := newRosterFixture(t)
	result, err := f.service.ImportMembers(context.Background(), as(7), MemberImportInput{
		Domain: "mail.example", Rows: []MemberImportRow{{Email: "alice@gmail.com"}},
	})
	if err != nil {
		t.Fatalf("ImportMembers: %v", err)
	}
	if got := rosterOutcomes(result); !slices.Equal(got, []string{"failed:MAILBOX.IMPORT_DOMAIN_REQUIRED"}) {
		t.Fatalf("outcomes = %v", got)
	}
}

func TestRosterNeedsAManager(t *testing.T) {
	f := newRosterFixture(t)
	member := viewerOf(7, 3, domain.OrganizationRoleMember)
	_, err := f.service.ImportMembers(context.Background(), member, MemberImportInput{
		Domain: "news.example.com", Rows: []MemberImportRow{{Email: "alice@gmail.com"}},
	})
	if err != domain.ErrPermissionDenied {
		t.Fatalf("error = %v; want permission denied", err)
	}
}

func TestJoiningClaimsTheReservedMailboxAndOnlyTheOwnerReadsIt(t *testing.T) {
	f := newRosterFixture(t)
	f.run(t, false, MemberImportRow{Email: "alice@gmail.com", Mailbox: "alice"})
	reserved := f.mailboxAt("alice@news.example.com")
	const alice = 41

	// Before joining nobody reads the reserved mailbox: managers administer it only.
	admin := viewerOf(7, testCreator, domain.OrganizationRoleOwner)
	if access := domain.MailboxAccessFor(reserved, admin, nil); access.View || access.Send || !access.Administer {
		t.Fatalf("manager access before joining = %+v", access)
	}

	f.service.accounts = rosterAccounts{alice: "Alice@Gmail.com"}
	joined := domain.NewOrganizationMemberJoinedEvent(7, alice, 4100, f.now)
	if err := f.service.MemberJoined(context.Background(), joined); err != nil {
		t.Fatalf("MemberJoined: %v", err)
	}
	claimed := f.mailboxAt("alice@news.example.com")
	if claimed.OwnerUserID == nil || *claimed.OwnerUserID != alice || claimed.PendingOwnerEmail != "" {
		t.Fatalf("claimed mailbox = %+v", claimed)
	}
	owner := viewerOf(7, alice, domain.OrganizationRoleMember)
	if access := domain.MailboxAccessFor(claimed, owner, nil); !access.View || !access.Send {
		t.Fatalf("owner access = %+v", access)
	}
	other := viewerOf(7, 3, domain.OrganizationRoleMember)
	if access := domain.MailboxAccessFor(claimed, other, nil); access.Any() {
		t.Fatalf("another member sees the mailbox: %+v", access)
	}
	if access := domain.MailboxAccessFor(claimed, admin, nil); access.View {
		t.Fatalf("a manager reads the member's mailbox: %+v", access)
	}
	last := f.repo.accessEvents[len(f.repo.accessEvents)-1]
	if last.Type != domain.MailboxAccessOwnerAssigned || last.SubjectUserID == nil || *last.SubjectUserID != alice {
		t.Fatalf("last access event = %+v", last)
	}
}

func TestJoiningWithAnotherEmailClaimsNothing(t *testing.T) {
	f := newRosterFixture(t)
	f.run(t, false, MemberImportRow{Email: "alice@gmail.com"})
	f.service.accounts = rosterAccounts{42: "mallory@gmail.com"}
	if err := f.service.MemberJoined(context.Background(), domain.NewOrganizationMemberJoinedEvent(7, 42, 4200, f.now)); err != nil {
		t.Fatalf("MemberJoined: %v", err)
	}
	if mailbox := f.mailboxAt("alice@news.example.com"); mailbox.OwnerUserID != nil {
		t.Fatalf("a different account claimed the mailbox: %+v", mailbox)
	}
}
