package operatorcommands

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/console"
	infracommands "github.com/fastagi/zmail/api/internal/infra/console/commands"
)

const consoleOrganization uint = 42

// fakeOrganizations holds one organization, its managers, and its other memberships.
type fakeOrganizations struct {
	organization *domain.Organization
	memberships  []*domain.OrganizationMembership
	grants       []uint
}

func newFakeOrganizations() *fakeOrganizations {
	joined := time.Date(2026, time.November, 1, 0, 0, 0, 0, time.UTC)
	member := func(id, userID uint, role domain.OrganizationRole, email string, age int) *domain.OrganizationMembership {
		return &domain.OrganizationMembership{
			ID: id, OrganizationID: consoleOrganization, UserID: userID, Role: role,
			User: &domain.User{ID: userID, Email: email}, CreatedAt: joined.Add(time.Duration(age) * time.Hour),
		}
	}
	return &fakeOrganizations{
		organization: &domain.Organization{ID: consoleOrganization, Name: "ZGI", Slug: "zgi"},
		memberships: []*domain.OrganizationMembership{
			member(1, 10, domain.OrganizationRoleOwner, "owner@zgi.app", 0),
			member(2, 11, domain.OrganizationRoleOwner, "late-owner@zgi.app", 5),
			member(3, 12, domain.OrganizationRoleAdmin, "admin@zgi.app", 1),
			member(4, 13, domain.OrganizationRoleMember, "member@zgi.app", 2),
		},
	}
}

func (f *fakeOrganizations) FindOrganizationManagers(_ context.Context, organizationID uint) (*domain.Organization, []*domain.OrganizationMembership, error) {
	if organizationID != consoleOrganization {
		return nil, nil, domain.ErrOrganizationNotFound
	}
	var managers []*domain.OrganizationMembership
	for _, role := range []domain.OrganizationRole{domain.OrganizationRoleOwner, domain.OrganizationRoleAdmin} {
		for _, membership := range f.memberships {
			if membership.Role == role {
				managers = append(managers, membership)
			}
		}
	}
	return f.organization, managers, nil
}

func (f *fakeOrganizations) FindMembership(_ context.Context, organizationID, userID uint) (*domain.OrganizationMembership, error) {
	for _, membership := range f.memberships {
		if membership.OrganizationID == organizationID && membership.UserID == userID {
			return membership, nil
		}
	}
	return nil, nil
}

func (f *fakeOrganizations) GrantAdmin(ctx context.Context, organizationID, userID uint) (*domain.OrganizationAdminGrant, error) {
	f.grants = append(f.grants, userID)
	existing, _ := f.FindMembership(ctx, organizationID, userID)
	if existing == nil {
		created := &domain.OrganizationMembership{ID: 99, OrganizationID: organizationID, UserID: userID, Role: domain.OrganizationRoleAdmin}
		f.memberships = append(f.memberships, created)
		return &domain.OrganizationAdminGrant{Membership: created}, nil
	}
	before := existing.Role
	if before == domain.OrganizationRoleMember {
		existing.Role = domain.OrganizationRoleAdmin
	}
	return &domain.OrganizationAdminGrant{Membership: existing, BeforeRole: before}, nil
}

// fakeAccounts finds accounts by email ignoring case; it implements only what the commands use.
type fakeAccounts struct {
	domain.UserAdministrator
	users []*domain.User
}

func (f *fakeAccounts) FindUserByEmail(_ context.Context, email string) (*domain.User, error) {
	for _, user := range f.users {
		if strings.EqualFold(user.Email, strings.TrimSpace(email)) {
			return user, nil
		}
	}
	return nil, domain.ErrUserNotFound
}

// fakeMailboxConsole records import calls and answers each row from outcomes by address.
type fakeMailboxConsole struct {
	viewer   domain.OrganizationContext
	rows     []domain.MailboxImportRow
	dryRun   bool
	calls    int
	failures map[string]domain.MailboxImportRowResult
	domains  map[string]*domain.MailDomain
	summary  *domain.OrganizationMailboxSummary
}

func (f *fakeMailboxConsole) ImportMailboxes(_ context.Context, viewer domain.OrganizationContext, rows []domain.MailboxImportRow, dryRun bool) (*domain.MailboxImportResult, error) {
	f.calls++
	f.viewer, f.rows, f.dryRun = viewer, rows, dryRun
	result := &domain.MailboxImportResult{DryRun: dryRun}
	for index, row := range rows {
		outcome, failed := f.failures[row.Address]
		if failed {
			outcome.Row, outcome.Address, outcome.Status = index+1, row.Address, domain.MailboxImportFailed
			result.Failed++
		} else {
			outcome = domain.MailboxImportRowResult{Row: index + 1, Address: row.Address, Status: domain.MailboxImportValid, MissingEmails: []string{}}
			if !dryRun {
				outcome.Status, outcome.MailboxID = domain.MailboxImportCreated, uint(100+index)
			}
			result.Succeeded++
		}
		result.Rows = append(result.Rows, outcome)
	}
	return result, nil
}

func (f *fakeMailboxConsole) OrganizationMailboxSummary(_ context.Context, organizationID uint) (*domain.OrganizationMailboxSummary, error) {
	if f.summary == nil {
		return &domain.OrganizationMailboxSummary{MaxMailboxes: 50, Domains: []*domain.MailDomain{}}, nil
	}
	return f.summary, nil
}

func (f *fakeMailboxConsole) FindConnectedDomain(_ context.Context, name string) (*domain.MailDomain, error) {
	if found, ok := f.domains[strings.ToLower(strings.TrimSpace(name))]; ok {
		return found, nil
	}
	return nil, nil
}

type recordingAudit struct {
	entries []*domain.AuditLog
}

func (r *recordingAudit) Record(_ context.Context, entry *domain.AuditLog) error {
	r.entries = append(r.entries, entry)
	return nil
}

func newConsoleRuntime() (*organizationConsoleRuntime, *fakeOrganizations, *fakeMailboxConsole, *recordingAudit) {
	organizations := newFakeOrganizations()
	mailboxes := &fakeMailboxConsole{failures: map[string]domain.MailboxImportRowResult{}, domains: map[string]*domain.MailDomain{}}
	audit := &recordingAudit{}
	accounts := &fakeAccounts{users: []*domain.User{
		{ID: 13, Email: "member@zgi.app"},
		{ID: 12, Email: "admin@zgi.app"},
		{ID: 10, Email: "owner@zgi.app"},
		{ID: 20, Email: "New.Admin@Example.com"},
	}}
	return &organizationConsoleRuntime{organizations: organizations, users: accounts, mailboxes: mailboxes, audit: audit},
		organizations, mailboxes, audit
}

func TestOrganizationConsoleCommandsRegistered(t *testing.T) {
	application := console.New("fastbuild", "test")
	infracommands.RegisterManifest(application, Manifest())
	assert.True(t, application.HasCommand("mailbox:import"))
	assert.True(t, application.HasCommand("organization:show"))
	assert.True(t, application.HasCommand("organization:grant-admin"))
}

// ---------------------------------------------------------------------------
// mailbox:import
// ---------------------------------------------------------------------------

func TestParseMailboxImportArguments(t *testing.T) {
	parsed, err := parseMailboxImportArgs([]string{"--organization=42", "--file=/tmp/a.csv"})
	require.NoError(t, err)
	assert.Equal(t, mailboxImportArguments{organizationID: 42, file: "/tmp/a.csv"}, parsed)

	parsed, err = parseMailboxImportArgs([]string{"--organization", "42", "--file", "a.csv", "--actor=12", "--commit"})
	require.NoError(t, err)
	assert.Equal(t, mailboxImportArguments{organizationID: 42, file: "a.csv", actorID: 12, commit: true}, parsed)

	for _, args := range [][]string{
		{"--file=a.csv"},
		{"--organization=42"},
		{"--organization=0", "--file=a.csv"},
		{"--organization=42", "--file=a.csv", "extra"},
		{"--organization=42", "--file=a.csv", "--dry-run"},
	} {
		_, err = parseMailboxImportArgs(args)
		assert.Error(t, err, "%v", args)
	}
}

func TestReadMailboxImportFileMatchesTheWebImport(t *testing.T) {
	file := "\ufeffAddress, Display_Name ,members,member_count\r\n" +
		"sales@zgi.app,Sales,a@zgi.app:view;b@zgi.app,2\r\n" +
		"\r\n" +
		"\"ops@zgi.app\",\"Ops, Team\",,0\r\n"
	rows, err := readMailboxImportFile(strings.NewReader(file))
	require.NoError(t, err)
	assert.Equal(t, []domain.MailboxImportRow{
		{Address: "sales@zgi.app", DisplayName: "Sales", Members: "a@zgi.app:view;b@zgi.app"},
		{Address: "ops@zgi.app", DisplayName: "Ops, Team", Members: ""},
	}, rows)

	rows, err = readMailboxImportFile(strings.NewReader("address\nsupport@zgi.app\n"))
	require.NoError(t, err)
	assert.Equal(t, []domain.MailboxImportRow{{Address: "support@zgi.app"}}, rows, "display_name and members columns are optional")

	_, err = readMailboxImportFile(strings.NewReader(""))
	assert.ErrorContains(t, err, "empty")
	_, err = readMailboxImportFile(strings.NewReader("address,members\n"))
	assert.ErrorContains(t, err, "no rows")
	_, err = readMailboxImportFile(strings.NewReader("display_name,members\nSales,a@zgi.app\n"))
	assert.ErrorContains(t, err, "address")

	var tooMany strings.Builder
	tooMany.WriteString("address\n")
	for index := range 501 {
		fmt.Fprintf(&tooMany, "m%d@zgi.app\n", index)
	}
	_, err = readMailboxImportFile(strings.NewReader(tooMany.String()))
	assert.ErrorContains(t, err, "500")
}

func TestMailboxImportDefaultsToADryRunAsTheEarliestOwner(t *testing.T) {
	runtime, _, mailboxes, audit := newConsoleRuntime()
	mailboxes.failures["taken@zgi.app"] = domain.MailboxImportRowResult{ErrorCode: "MAILBOX.ADDRESS_TAKEN", MissingEmails: []string{}}
	var out bytes.Buffer

	err := runMailboxImport(context.Background(), runtime, mailboxImportArguments{organizationID: consoleOrganization}, []domain.MailboxImportRow{
		{Address: "sales@zgi.app", DisplayName: "Sales", Members: "member@zgi.app:send"},
		{Address: "ops@zgi.app"},
		{Address: "taken@zgi.app", Members: "  "},
	}, &out)

	require.ErrorIs(t, err, errMailboxImportRowsFailed)
	require.Equal(t, 1, mailboxes.calls)
	assert.True(t, mailboxes.dryRun)
	assert.Equal(t, domain.OrganizationContext{
		OrganizationID: consoleOrganization, OrganizationName: "ZGI", OrganizationSlug: "zgi",
		MembershipID: 1, UserID: 10, Role: domain.OrganizationRoleOwner,
	}, mailboxes.viewer)
	assert.Equal(t, []domain.MailboxImportRow{
		{Address: "sales@zgi.app", DisplayName: "Sales", Members: "member@zgi.app:send"},
		{Address: "ops@zgi.app", Members: "owner@zgi.app:manage"},
		{Address: "taken@zgi.app", Members: "owner@zgi.app:manage"},
	}, mailboxes.rows)
	assert.Empty(t, audit.entries, "a dry run writes no audit entry")

	printed := out.String()
	assert.Contains(t, printed, "Dry run")
	assert.Regexp(t, `(?m)^1\s+sales@zgi\.app\s+valid\s+-\s+-$`, printed)
	assert.Regexp(t, `(?m)^3\s+taken@zgi\.app\s+failed\s+MAILBOX\.ADDRESS_TAKEN\s+-$`, printed)
	assert.Contains(t, printed, "2 valid, 1 failed")
	assert.Contains(t, printed, "--commit")
}

func TestMailboxImportCommitCreatesAndAudits(t *testing.T) {
	runtime, _, mailboxes, audit := newConsoleRuntime()
	var out bytes.Buffer

	err := runMailboxImport(context.Background(), runtime,
		mailboxImportArguments{organizationID: consoleOrganization, actorID: 12, commit: true},
		[]domain.MailboxImportRow{{Address: "sales@zgi.app"}}, &out)

	require.NoError(t, err)
	assert.False(t, mailboxes.dryRun)
	assert.Equal(t, uint(12), mailboxes.viewer.UserID)
	assert.Equal(t, domain.OrganizationRoleAdmin, mailboxes.viewer.Role)
	assert.Equal(t, "admin@zgi.app:manage", mailboxes.rows[0].Members)
	assert.Regexp(t, `(?m)^1\s+sales@zgi\.app\s+created\s+-\s+-$`, out.String())
	assert.Contains(t, out.String(), "1 created, 0 failed")

	require.Len(t, audit.entries, 1)
	entry := audit.entries[0]
	assert.Equal(t, domain.AuditActorSystem, entry.ActorType)
	assert.Equal(t, "import", entry.Action)
	assert.Equal(t, "mailboxes", entry.Resource)
	assert.Equal(t, "organization", entry.TargetType)
	assert.Equal(t, "42", entry.TargetID)
	assert.Equal(t, "CLI", entry.Method)
	assert.Equal(t, "mailbox:import", entry.Path)
	assert.Equal(t, map[string]any{"operation": "import", "actor_user_id": uint(12), "created": 1, "failed": 0}, entry.Metadata)
}

func TestMailboxImportPrintsMissingMembers(t *testing.T) {
	runtime, _, mailboxes, _ := newConsoleRuntime()
	mailboxes.failures["sales@zgi.app"] = domain.MailboxImportRowResult{
		ErrorCode: "MAILBOX.IMPORT_MEMBER_NOT_FOUND", MissingEmails: []string{"x@zgi.app", "y@zgi.app"},
	}
	var out bytes.Buffer
	err := runMailboxImport(context.Background(), runtime, mailboxImportArguments{organizationID: consoleOrganization},
		[]domain.MailboxImportRow{{Address: "sales@zgi.app", Members: "x@zgi.app;y@zgi.app"}}, &out)
	require.ErrorIs(t, err, errMailboxImportRowsFailed)
	assert.Regexp(t, `(?m)^1\s+sales@zgi\.app\s+failed\s+MAILBOX\.IMPORT_MEMBER_NOT_FOUND\s+x@zgi\.app;y@zgi\.app$`, out.String())
}

func TestMailboxImportActorMustManageTheOrganization(t *testing.T) {
	for name, args := range map[string]mailboxImportArguments{
		"member":       {organizationID: consoleOrganization, actorID: 13},
		"not a member": {organizationID: consoleOrganization, actorID: 77},
	} {
		t.Run(name, func(t *testing.T) {
			runtime, _, mailboxes, _ := newConsoleRuntime()
			err := runMailboxImport(context.Background(), runtime, args, []domain.MailboxImportRow{{Address: "a@zgi.app"}}, &bytes.Buffer{})
			require.ErrorContains(t, err, "is not an owner or admin of organization 42")
			assert.Zero(t, mailboxes.calls)
		})
	}

	runtime, organizations, mailboxes, _ := newConsoleRuntime()
	err := runMailboxImport(context.Background(), runtime, mailboxImportArguments{organizationID: 7}, []domain.MailboxImportRow{{Address: "a@zgi.app"}}, &bytes.Buffer{})
	require.ErrorIs(t, err, domain.ErrOrganizationNotFound)

	organizations.memberships = organizations.memberships[2:3]
	err = runMailboxImport(context.Background(), runtime, mailboxImportArguments{organizationID: consoleOrganization}, []domain.MailboxImportRow{{Address: "a@zgi.app"}}, &bytes.Buffer{})
	require.ErrorContains(t, err, "has no owner")
	assert.Zero(t, mailboxes.calls)
}

// ---------------------------------------------------------------------------
// organization:show
// ---------------------------------------------------------------------------

func TestParseOrganizationShowArguments(t *testing.T) {
	parsed, err := parseOrganizationShowArgs([]string{"--domain=zgi.app"})
	require.NoError(t, err)
	assert.Equal(t, organizationShowArguments{domain: "zgi.app"}, parsed)
	parsed, err = parseOrganizationShowArgs([]string{"--organization=42"})
	require.NoError(t, err)
	assert.Equal(t, organizationShowArguments{organizationID: 42}, parsed)

	for _, args := range [][]string{nil, {"--domain=zgi.app", "--organization=42"}, {"--organization=0"}, {"--domain= "}, {"zgi.app"}} {
		_, err = parseOrganizationShowArgs(args)
		assert.Error(t, err, "%v", args)
	}
}

func TestOrganizationShowByDomain(t *testing.T) {
	runtime, _, mailboxes, _ := newConsoleRuntime()
	verified := &domain.MailDomain{OrganizationID: consoleOrganization, Name: "zgi.app", Status: domain.MailDomainStatusVerified, Capability: domain.MailDomainSendReceive}
	mailboxes.domains["zgi.app"] = verified
	mailboxes.summary = &domain.OrganizationMailboxSummary{Mailboxes: 3, MaxMailboxes: 50, Domains: []*domain.MailDomain{
		verified,
		{OrganizationID: consoleOrganization, Name: "news.zgi.app", Status: domain.MailDomainStatusPending, Capability: domain.MailDomainSendOnly},
	}}
	var out bytes.Buffer

	require.NoError(t, runOrganizationShow(context.Background(), runtime, organizationShowArguments{domain: "ZGI.app"}, &out))

	printed := out.String()
	assert.Contains(t, printed, "Organization: 42")
	assert.Contains(t, printed, "Name: ZGI (zgi)")
	assert.Contains(t, printed, "Mailboxes: 3 of 50")
	assert.Regexp(t, `(?m)^10\s+owner@zgi\.app\s+owner$`, printed)
	assert.Regexp(t, `(?m)^11\s+late-owner@zgi\.app\s+owner$`, printed)
	assert.Regexp(t, `(?m)^12\s+admin@zgi\.app\s+admin$`, printed)
	assert.NotContains(t, printed, "member@zgi.app")
	assert.Regexp(t, `(?m)^zgi\.app\s+verified\s+send_receive$`, printed)
	assert.Regexp(t, `(?m)^news\.zgi\.app\s+pending\s+send_only$`, printed)
	assert.Contains(t, printed, "Domain zgi.app: connected to this organization, verified")

	err := runOrganizationShow(context.Background(), runtime, organizationShowArguments{domain: "unknown.example"}, &bytes.Buffer{})
	require.ErrorContains(t, err, "no organization has connected unknown.example")
}

func TestOrganizationShowByIDWithoutTheMailboxStarter(t *testing.T) {
	runtime, _, _, _ := newConsoleRuntime()
	runtime.mailboxes = nil
	var out bytes.Buffer
	require.NoError(t, runOrganizationShow(context.Background(), runtime, organizationShowArguments{organizationID: consoleOrganization}, &out))
	assert.Contains(t, out.String(), "Organization: 42")
	assert.Contains(t, out.String(), "Mailboxes: unavailable")

	err := runOrganizationShow(context.Background(), runtime, organizationShowArguments{domain: "zgi.app"}, &bytes.Buffer{})
	require.ErrorContains(t, err, "mailbox starter")
}

// ---------------------------------------------------------------------------
// organization:grant-admin
// ---------------------------------------------------------------------------

func TestParseOrganizationGrantAdminArguments(t *testing.T) {
	parsed, err := parseOrganizationGrantAdminArgs([]string{"--organization=42", "--email= New.Admin@Example.com "})
	require.NoError(t, err)
	assert.Equal(t, organizationGrantAdminArguments{organizationID: 42, email: "New.Admin@Example.com"}, parsed)
	parsed, err = parseOrganizationGrantAdminArgs([]string{"--organization=42", "--email=a@b.c", "--commit"})
	require.NoError(t, err)
	assert.True(t, parsed.commit)

	for _, args := range [][]string{{"--email=a@b.c"}, {"--organization=42"}, {"--organization=42", "--email=nope"}, {"--organization=42", "--email=a@b.c", "--role=owner"}} {
		_, err = parseOrganizationGrantAdminArgs(args)
		assert.Error(t, err, "%v", args)
	}
}

func TestOrganizationGrantAdminDryRunWritesNothing(t *testing.T) {
	runtime, organizations, _, audit := newConsoleRuntime()
	var out bytes.Buffer
	require.NoError(t, runOrganizationGrantAdmin(context.Background(), runtime,
		organizationGrantAdminArguments{organizationID: consoleOrganization, email: "new.admin@example.com"}, &out))

	assert.Empty(t, organizations.grants)
	assert.Empty(t, audit.entries)
	assert.Contains(t, out.String(), "Dry run")
	assert.Contains(t, out.String(), "User: 20")
	assert.Contains(t, out.String(), "would add an admin membership")
	assert.Contains(t, out.String(), "--commit")
}

func TestOrganizationGrantAdminCommitAddsAndAudits(t *testing.T) {
	runtime, organizations, _, audit := newConsoleRuntime()
	var out bytes.Buffer
	require.NoError(t, runOrganizationGrantAdmin(context.Background(), runtime,
		organizationGrantAdminArguments{organizationID: consoleOrganization, email: "new.admin@example.com", commit: true}, &out))

	assert.Equal(t, []uint{20}, organizations.grants)
	assert.Contains(t, out.String(), "added an admin membership")
	require.Len(t, audit.entries, 1)
	entry := audit.entries[0]
	assert.Equal(t, domain.AuditActorSystem, entry.ActorType)
	assert.Equal(t, "add_member", entry.Action)
	assert.Equal(t, "organization_members", entry.Resource)
	assert.Equal(t, "organization_membership", entry.TargetType)
	assert.Equal(t, "99", entry.TargetID)
	assert.Equal(t, "organization:grant-admin", entry.Path)
	assert.Equal(t, map[string]any{
		"operation": "grant_admin", "organization_id": consoleOrganization, "user_id": uint(20), "role": domain.OrganizationRoleAdmin,
	}, entry.Metadata)
	assert.NotContains(t, fmt.Sprint(entry), "example.com", "the audit entry never stores the email")
}

func TestOrganizationGrantAdminRaisesAMember(t *testing.T) {
	runtime, organizations, _, audit := newConsoleRuntime()
	var out bytes.Buffer
	require.NoError(t, runOrganizationGrantAdmin(context.Background(), runtime,
		organizationGrantAdminArguments{organizationID: consoleOrganization, email: "member@zgi.app", commit: true}, &out))

	assert.Equal(t, []uint{13}, organizations.grants)
	assert.Contains(t, out.String(), "raised member to admin")
	require.Len(t, audit.entries, 1)
	entry := audit.entries[0]
	assert.Equal(t, "change_role", entry.Action)
	assert.Equal(t, "4", entry.TargetID)
	assert.Equal(t, map[string]domain.AuditValueChange{
		"role": {Before: domain.OrganizationRoleMember, After: domain.OrganizationRoleAdmin},
	}, entry.Changes)
}

func TestOrganizationGrantAdminLeavesManagersAlone(t *testing.T) {
	for _, email := range []string{"admin@zgi.app", "OWNER@zgi.app"} {
		runtime, organizations, _, audit := newConsoleRuntime()
		var out bytes.Buffer
		require.NoError(t, runOrganizationGrantAdmin(context.Background(), runtime,
			organizationGrantAdminArguments{organizationID: consoleOrganization, email: email, commit: true}, &out))
		assert.Empty(t, organizations.grants, email)
		assert.Empty(t, audit.entries, email)
		assert.Contains(t, out.String(), "nothing to do", email)
	}
}

func TestOrganizationGrantAdminRequiresAnExistingAccount(t *testing.T) {
	runtime, organizations, _, _ := newConsoleRuntime()
	err := runOrganizationGrantAdmin(context.Background(), runtime,
		organizationGrantAdminArguments{organizationID: consoleOrganization, email: "nobody@example.com", commit: true}, &bytes.Buffer{})
	require.ErrorContains(t, err, "no account uses nobody@example.com")
	assert.Empty(t, organizations.grants)

	err = runOrganizationGrantAdmin(context.Background(), runtime,
		organizationGrantAdminArguments{organizationID: 7, email: "member@zgi.app", commit: true}, &bytes.Buffer{})
	require.ErrorIs(t, err, domain.ErrOrganizationNotFound)
	assert.Empty(t, organizations.grants)
}
