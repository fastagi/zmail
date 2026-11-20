package operatorcommands

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/fastagi/zmail/api/internal/domain"
)

// maxMailboxImportRows is the import service's row limit.
const maxMailboxImportRows = 500

// errMailboxImportRowsFailed makes the command exit non-zero when any row failed.
var errMailboxImportRowsFailed = errors.New("some rows failed")

// mailboxImportHeaders are the import columns, each with its Simplified Chinese name, as the web
// import accepts them.
var mailboxImportHeaders = map[string][]string{
	"address":      {"address", "\u90ae\u7bb1\u5730\u5740"},
	"display_name": {"display_name", "\u540d\u79f0"},
	"members":      {"members", "\u6210\u5458"},
}

// MailboxImportCommand creates shared mailboxes from a CSV file as an owner or admin of the
// organization, through the same service as POST /v1/mailbox-imports.
type MailboxImportCommand struct{}

func NewMailboxImportCommand() *MailboxImportCommand { return &MailboxImportCommand{} }

func (c *MailboxImportCommand) Name() string { return "mailbox:import" }
func (c *MailboxImportCommand) Description() string {
	return "Check, or with --commit create, an organization's shared mailboxes from a CSV file of address,display_name,members"
}
func (c *MailboxImportCommand) Usage() string {
	return "mailbox:import --organization=<id> --file=<path.csv> [--actor=<user id>] [--commit]"
}

type mailboxImportArguments struct {
	organizationID uint
	file           string
	actorID        uint
	commit         bool
}

func parseMailboxImportArgs(args []string) (mailboxImportArguments, error) {
	flags := newConsoleFlagSet("mailbox:import")
	organizationID := flags.Uint("organization", 0, "organization ID")
	file := flags.String("file", "", "CSV file")
	actorID := flags.Uint("actor", 0, "acting owner or admin user ID")
	commit := flags.Bool("commit", false, "create the mailboxes")
	if err := parseConsoleFlags(flags, args); err != nil {
		return mailboxImportArguments{}, err
	}
	parsed := mailboxImportArguments{organizationID: *organizationID, file: strings.TrimSpace(*file), actorID: *actorID, commit: *commit}
	if parsed.organizationID == 0 {
		return mailboxImportArguments{}, errors.New("--organization must be a positive ID")
	}
	if parsed.file == "" {
		return mailboxImportArguments{}, errors.New("--file is required")
	}
	return parsed, nil
}

// readMailboxImportFile reads the rows of an import file as the web import does: a UTF-8 BOM is
// dropped, headers match trimmed and ignoring case, and other columns are ignored, so an export
// imports again. Unlike the web import, display_name and members are both optional.
func readMailboxImportFile(source io.Reader) ([]domain.MailboxImportRow, error) {
	reader := csv.NewReader(source)
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read CSV: %w", err)
	}
	if len(records) == 0 {
		return nil, errors.New("the file is empty")
	}
	header := records[0]
	if len(header) > 0 {
		header[0] = strings.TrimPrefix(header[0], "\ufeff")
	}
	columnOf := func(key string) int {
		return slices.IndexFunc(header, func(cell string) bool {
			return slices.Contains(mailboxImportHeaders[key], strings.ToLower(strings.TrimSpace(cell)))
		})
	}
	address, displayName, members := columnOf("address"), columnOf("display_name"), columnOf("members")
	if address < 0 {
		return nil, errors.New("the header has no address column")
	}
	data := records[1:]
	if len(data) == 0 {
		return nil, errors.New("the file has no rows")
	}
	if len(data) > maxMailboxImportRows {
		return nil, fmt.Errorf("the file has %d rows; an import has at most %d", len(data), maxMailboxImportRows)
	}
	cell := func(record []string, column int) string {
		if column < 0 || column >= len(record) {
			return ""
		}
		return record[column]
	}
	rows := make([]domain.MailboxImportRow, 0, len(data))
	for _, record := range data {
		rows = append(rows, domain.MailboxImportRow{
			Address: cell(record, address), DisplayName: cell(record, displayName), Members: cell(record, members),
		})
	}
	return rows, nil
}

func (c *MailboxImportCommand) Run(args []string) error {
	parsed, err := parseMailboxImportArgs(args)
	if err != nil {
		return err
	}
	file, err := os.Open(parsed.file)
	if err != nil {
		return fmt.Errorf("open import file: %w", err)
	}
	rows, err := readMailboxImportFile(file)
	_ = file.Close()
	if err != nil {
		return fmt.Errorf("%s: %w", parsed.file, err)
	}
	runtime, err := loadOrganizationConsoleRuntime(true)
	if err != nil {
		return err
	}
	ctx, stop := operatorCommandContext()
	defer stop()
	return runMailboxImport(ctx, runtime, parsed, rows, os.Stdout)
}

// resolveImportActor picks the acting membership: the given user when it is an owner or admin, or
// else the earliest owner.
func resolveImportActor(ctx context.Context, organizations domain.OrganizationAdministrator, organizationID, actorID uint) (domain.OrganizationContext, string, error) {
	organization, managers, err := organizations.FindOrganizationManagers(ctx, organizationID)
	if err != nil {
		return domain.OrganizationContext{}, "", fmt.Errorf("load organization %d: %w", organizationID, err)
	}
	var actor *domain.OrganizationMembership
	for _, manager := range managers {
		if (actorID == 0 && manager.Role == domain.OrganizationRoleOwner) || (actorID != 0 && manager.UserID == actorID) {
			actor = manager
			break
		}
	}
	switch {
	case actor == nil && actorID == 0:
		return domain.OrganizationContext{}, "", fmt.Errorf("organization %d has no owner; pass --actor with an admin's user ID", organizationID)
	case actor == nil:
		return domain.OrganizationContext{}, "", fmt.Errorf("user %d is not an owner or admin of organization %d", actorID, organizationID)
	case managerEmail(actor) == "":
		return domain.OrganizationContext{}, "", fmt.Errorf("acting user %d has no account email", actor.UserID)
	}
	viewer := domain.OrganizationContext{
		OrganizationID: organization.ID, OrganizationName: organization.Name, OrganizationSlug: organization.Slug,
		MembershipID: actor.ID, UserID: actor.UserID, Role: actor.Role,
	}
	return viewer, managerEmail(actor), nil
}

// runMailboxImport imports the rows as the acting manager, who becomes the one member with manage
// of each row without members. It prints one line per row and the totals, and fails when any row
// failed.
func runMailboxImport(ctx context.Context, runtime *organizationConsoleRuntime, args mailboxImportArguments, rows []domain.MailboxImportRow, out io.Writer) error {
	viewer, actorEmail, err := resolveImportActor(ctx, runtime.organizations, args.organizationID, args.actorID)
	if err != nil {
		return err
	}
	prepared := make([]domain.MailboxImportRow, len(rows))
	for index, row := range rows {
		if strings.TrimSpace(row.Members) == "" {
			row.Members = actorEmail + ":" + string(domain.MailboxPermissionManage)
		}
		prepared[index] = row
	}
	result, err := runtime.mailboxes.ImportMailboxes(ctx, viewer, prepared, !args.commit)
	if err != nil {
		return fmt.Errorf("import mailboxes: %w", err)
	}

	if !args.commit {
		fmt.Fprintln(out, "Dry run: nothing is created")
	}
	fmt.Fprintf(out, "Organization: %d (%s), acting as user %d (%s)\n", viewer.OrganizationID, viewer.OrganizationName, viewer.UserID, viewer.Role)
	table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "ROW\tADDRESS\tSTATUS\tERROR\tMISSING")
	for _, row := range result.Rows {
		code, missing := "-", "-"
		if row.ErrorCode != "" {
			code = row.ErrorCode
		}
		if len(row.MissingEmails) > 0 {
			missing = strings.Join(row.MissingEmails, ";")
		}
		fmt.Fprintf(table, "%d\t%s\t%s\t%s\t%s\n", row.Row, row.Address, row.Status, code, missing)
	}
	if err = table.Flush(); err != nil {
		return err
	}
	if args.commit {
		fmt.Fprintf(out, "Total: %d created, %d failed\n", result.Succeeded, result.Failed)
		entry := &domain.AuditLog{
			Action:     "import",
			Resource:   "mailboxes",
			TargetType: "organization",
			TargetID:   formatID(viewer.OrganizationID),
			Metadata: map[string]any{
				"operation": "import", "actor_user_id": viewer.UserID, "created": result.Succeeded, "failed": result.Failed,
			},
		}
		if auditErr := recordConsoleAudit(ctx, runtime.audit, "mailbox:import", entry); auditErr != nil {
			fmt.Fprintln(out, "Warning: the import ran, but audit persistence failed")
		}
	} else {
		fmt.Fprintf(out, "Total: %d valid, %d failed\n", result.Succeeded, result.Failed)
		fmt.Fprintln(out, "Run again with --commit to create the valid rows")
	}
	if result.Failed > 0 {
		return fmt.Errorf("%d of %d rows: %w", result.Failed, len(result.Rows), errMailboxImportRowsFailed)
	}
	return nil
}
