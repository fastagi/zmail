package operatorcommands

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/fastagi/zmail/api/internal/bootstrap"
	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/wiring"
)

var errOrganizationConsoleUnavailable = errors.New("organization console runtime is unavailable")

// organizationConsoleRuntime is what the organization console commands use. They act on any
// organization without being a member of it, so they only run on the server.
type organizationConsoleRuntime struct {
	organizations domain.OrganizationAdministrator
	users         domain.UserAdministrator
	// mailboxes is nil when the mailbox starter is not selected.
	mailboxes domain.MailboxOperatorConsole
	audit     domain.AuditLogRecorder
}

// loadOrganizationConsoleRuntime builds the application; requireMailbox fails when the mailbox
// starter is not selected.
func loadOrganizationConsoleRuntime(requireMailbox bool) (*organizationConsoleRuntime, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if !cfg.Starters.Selected(config.StarterOrganization) {
		return nil, fmt.Errorf("organization starter is not selected in OPTIONAL_STARTERS")
	}
	mailboxSelected := cfg.Starters.Selected(config.StarterMailbox)
	if requireMailbox && !mailboxSelected {
		return nil, fmt.Errorf("mailbox starter is not selected in OPTIONAL_STARTERS")
	}
	if loggerErr := bootstrap.InitLogger(cfg); loggerErr != nil {
		return nil, loggerErr
	}
	application, err := wiring.InitApplicationWithConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("initialize organization console: %w", err)
	}
	if application.OrganizationAdministration == nil || application.UserAdministration == nil || application.AuditRecorder == nil {
		return nil, errOrganizationConsoleUnavailable
	}
	runtime := &organizationConsoleRuntime{
		organizations: application.OrganizationAdministration,
		users:         application.UserAdministration,
		audit:         application.AuditRecorder,
	}
	if mailboxSelected {
		runtime.mailboxes = application.MailboxOperator
	}
	if requireMailbox && runtime.mailboxes == nil {
		return nil, errOrganizationConsoleUnavailable
	}
	return runtime, nil
}

// newConsoleFlagSet parses --name=value and --name value flags without printing usage.
func newConsoleFlagSet(command string) *flag.FlagSet {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	return flags
}

func parseConsoleFlags(flags *flag.FlagSet, args []string) error {
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("%s: %w", flags.Name(), err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("%s does not accept positional arguments: %s", flags.Name(), strconv.Quote(strings.Join(flags.Args(), " ")))
	}
	return nil
}

// recordConsoleAudit records an organization console change with the system as actor: the
// operator who ran the command has no account session. Emails are never stored.
func recordConsoleAudit(ctx context.Context, recorder domain.AuditLogRecorder, command string, entry *domain.AuditLog) error {
	if recorder == nil {
		return errOrganizationConsoleUnavailable
	}
	entry.ActorType = domain.AuditActorSystem
	entry.Result = domain.AuditResultSuccess
	entry.Method = "CLI"
	entry.Path = command
	entry.RouteName = "console." + strings.NewReplacer(":", ".", "-", "_").Replace(command)
	entry.StatusCode = 200
	return recorder.Record(ctx, entry)
}

func formatID(id uint) string { return strconv.FormatUint(uint64(id), 10) }

func managerEmail(membership *domain.OrganizationMembership) string {
	if membership.User == nil {
		return ""
	}
	return membership.User.Email
}

// ---------------------------------------------------------------------------
// organization:show
// ---------------------------------------------------------------------------

// OrganizationShowCommand prints an organization's managers, mailbox count, limit, and domains.
type OrganizationShowCommand struct{}

func NewOrganizationShowCommand() *OrganizationShowCommand { return &OrganizationShowCommand{} }

func (c *OrganizationShowCommand) Name() string { return "organization:show" }
func (c *OrganizationShowCommand) Description() string {
	return "Show an organization by ID or connected domain: owners and admins, mailboxes and their limit, and domains"
}
func (c *OrganizationShowCommand) Usage() string {
	return "organization:show (--domain=<name> | --organization=<id>)"
}

type organizationShowArguments struct {
	organizationID uint
	domain         string
}

func parseOrganizationShowArgs(args []string) (organizationShowArguments, error) {
	flags := newConsoleFlagSet("organization:show")
	organizationID := flags.Uint("organization", 0, "organization ID")
	domainName := flags.String("domain", "", "connected domain")
	if err := parseConsoleFlags(flags, args); err != nil {
		return organizationShowArguments{}, err
	}
	parsed := organizationShowArguments{organizationID: *organizationID, domain: strings.TrimSpace(*domainName)}
	set := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { set[f.Name] = true })
	switch {
	case set["organization"] == set["domain"]:
		return organizationShowArguments{}, errors.New("organization:show needs exactly one of --domain or --organization")
	case set["organization"] && parsed.organizationID == 0:
		return organizationShowArguments{}, errors.New("--organization must be a positive ID")
	case set["domain"] && parsed.domain == "":
		return organizationShowArguments{}, errors.New("--domain must not be empty")
	}
	return parsed, nil
}

func (c *OrganizationShowCommand) Run(args []string) error {
	parsed, err := parseOrganizationShowArgs(args)
	if err != nil {
		return err
	}
	runtime, err := loadOrganizationConsoleRuntime(false)
	if err != nil {
		return err
	}
	ctx, stop := operatorCommandContext()
	defer stop()
	return runOrganizationShow(ctx, runtime, parsed, os.Stdout)
}

func runOrganizationShow(ctx context.Context, runtime *organizationConsoleRuntime, args organizationShowArguments, out io.Writer) error {
	organizationID := args.organizationID
	var lookedUp *domain.MailDomain
	if args.domain != "" {
		if runtime.mailboxes == nil {
			return errors.New("--domain needs the mailbox starter; pass --organization instead")
		}
		found, err := runtime.mailboxes.FindConnectedDomain(ctx, args.domain)
		if err != nil {
			return fmt.Errorf("find domain %s: %w", args.domain, err)
		}
		if found == nil {
			return fmt.Errorf("no organization has connected %s", args.domain)
		}
		lookedUp, organizationID = found, found.OrganizationID
	}
	organization, managers, err := runtime.organizations.FindOrganizationManagers(ctx, organizationID)
	if err != nil {
		return fmt.Errorf("load organization %d: %w", organizationID, err)
	}
	var summary *domain.OrganizationMailboxSummary
	if runtime.mailboxes != nil {
		if summary, err = runtime.mailboxes.OrganizationMailboxSummary(ctx, organization.ID); err != nil {
			return fmt.Errorf("summarize mailboxes of organization %d: %w", organization.ID, err)
		}
	}

	fmt.Fprintf(out, "Organization: %d\n", organization.ID)
	fmt.Fprintf(out, "Name: %s (%s)\n", organization.Name, organization.Slug)
	fmt.Fprintln(out, "Owners and admins:")
	table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "USER ID\tEMAIL\tROLE")
	for _, manager := range managers {
		fmt.Fprintf(table, "%d\t%s\t%s\n", manager.UserID, managerEmail(manager), manager.Role)
	}
	if err = table.Flush(); err != nil {
		return err
	}
	if summary == nil {
		fmt.Fprintln(out, "Mailboxes: unavailable (the mailbox starter is not selected)")
		return nil
	}
	fmt.Fprintf(out, "Mailboxes: %d of %d\n", summary.Mailboxes, summary.MaxMailboxes)
	if len(summary.Domains) == 0 {
		fmt.Fprintln(out, "Domains: none connected")
	} else {
		fmt.Fprintln(out, "Domains:")
		table = tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(table, "NAME\tSTATUS\tCAPABILITY")
		for _, mailDomain := range summary.Domains {
			fmt.Fprintf(table, "%s\t%s\t%s\n", mailDomain.Name, mailDomain.Status, mailDomain.Capability)
		}
		if err = table.Flush(); err != nil {
			return err
		}
	}
	if lookedUp != nil {
		state := "verified"
		if lookedUp.Status != domain.MailDomainStatusVerified {
			state = string(lookedUp.Status) + ", not verified: mailboxes cannot use it yet"
		}
		fmt.Fprintf(out, "Domain %s: connected to this organization, %s\n", lookedUp.Name, state)
	}
	return nil
}

// ---------------------------------------------------------------------------
// organization:grant-admin
// ---------------------------------------------------------------------------

// OrganizationGrantAdminCommand makes an existing account an admin of an organization.
type OrganizationGrantAdminCommand struct{}

func NewOrganizationGrantAdminCommand() *OrganizationGrantAdminCommand {
	return &OrganizationGrantAdminCommand{}
}

func (c *OrganizationGrantAdminCommand) Name() string { return "organization:grant-admin" }
func (c *OrganizationGrantAdminCommand) Description() string {
	return "Make an existing account an admin of an organization; a dry run unless --commit"
}
func (c *OrganizationGrantAdminCommand) Usage() string {
	return "organization:grant-admin --organization=<id> --email=<address> [--commit]"
}

type organizationGrantAdminArguments struct {
	organizationID uint
	email          string
	commit         bool
}

func parseOrganizationGrantAdminArgs(args []string) (organizationGrantAdminArguments, error) {
	flags := newConsoleFlagSet("organization:grant-admin")
	organizationID := flags.Uint("organization", 0, "organization ID")
	email := flags.String("email", "", "account email")
	commit := flags.Bool("commit", false, "write the membership")
	if err := parseConsoleFlags(flags, args); err != nil {
		return organizationGrantAdminArguments{}, err
	}
	parsed := organizationGrantAdminArguments{organizationID: *organizationID, email: strings.TrimSpace(*email), commit: *commit}
	if parsed.organizationID == 0 {
		return organizationGrantAdminArguments{}, errors.New("--organization must be a positive ID")
	}
	if at := strings.LastIndex(parsed.email, "@"); at <= 0 || at == len(parsed.email)-1 {
		return organizationGrantAdminArguments{}, errors.New("--email must be an email address")
	}
	return parsed, nil
}

func (c *OrganizationGrantAdminCommand) Run(args []string) error {
	parsed, err := parseOrganizationGrantAdminArgs(args)
	if err != nil {
		return err
	}
	runtime, err := loadOrganizationConsoleRuntime(false)
	if err != nil {
		return err
	}
	ctx, stop := operatorCommandContext()
	defer stop()
	return runOrganizationGrantAdmin(ctx, runtime, parsed, os.Stdout)
}

func runOrganizationGrantAdmin(ctx context.Context, runtime *organizationConsoleRuntime, args organizationGrantAdminArguments, out io.Writer) error {
	organization, _, err := runtime.organizations.FindOrganizationManagers(ctx, args.organizationID)
	if err != nil {
		return fmt.Errorf("load organization %d: %w", args.organizationID, err)
	}
	account, err := runtime.users.FindUserByEmail(ctx, args.email)
	if errors.Is(err, domain.ErrUserNotFound) {
		return fmt.Errorf("no account uses %s; the person must register first", args.email)
	}
	if err != nil {
		return fmt.Errorf("find account: %w", err)
	}
	current, err := runtime.organizations.FindMembership(ctx, organization.ID, account.ID)
	if err != nil {
		return fmt.Errorf("load membership: %w", err)
	}
	if !args.commit {
		fmt.Fprintln(out, "Dry run: nothing is written")
	}
	fmt.Fprintf(out, "Organization: %d (%s)\n", organization.ID, organization.Name)
	fmt.Fprintf(out, "User: %d\n", account.ID)
	switch {
	case current != nil && current.Role != domain.OrganizationRoleMember:
		fmt.Fprintf(out, "Membership: already %s, nothing to do\n", current.Role)
		return nil
	case !args.commit && current == nil:
		fmt.Fprintln(out, "Membership: none, would add an admin membership")
		fmt.Fprintln(out, "Run again with --commit to write it")
		return nil
	case !args.commit:
		fmt.Fprintln(out, "Membership: member, would raise member to admin")
		fmt.Fprintln(out, "Run again with --commit to write it")
		return nil
	}

	grant, err := runtime.organizations.GrantAdmin(ctx, organization.ID, account.ID)
	if err != nil {
		return err
	}
	membership := grant.Membership
	if !grant.Changed() {
		fmt.Fprintf(out, "Membership: already %s, nothing to do\n", membership.Role)
		return nil
	}
	entry := &domain.AuditLog{
		Resource:   "organization_members",
		TargetType: "organization_membership",
		TargetID:   formatID(membership.ID),
		Metadata: map[string]any{
			"operation":       "grant_admin",
			"organization_id": organization.ID,
			"user_id":         account.ID,
		},
	}
	if grant.Created() {
		entry.Action = "add_member"
		entry.Metadata["role"] = membership.Role
		fmt.Fprintln(out, "Membership: added an admin membership")
	} else {
		entry.Action = "change_role"
		entry.Changes = map[string]domain.AuditValueChange{"role": {Before: grant.BeforeRole, After: membership.Role}}
		fmt.Fprintf(out, "Membership: raised %s to %s\n", grant.BeforeRole, membership.Role)
	}
	if auditErr := recordConsoleAudit(ctx, runtime.audit, "organization:grant-admin", entry); auditErr != nil {
		fmt.Fprintln(out, "Warning: the membership was written, but audit persistence failed")
	}
	return nil
}
