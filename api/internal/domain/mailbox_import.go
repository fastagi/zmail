package domain

import "context"

// MailboxImportRow is one row of an import file: a shared mailbox's full address, its display
// name, and its members as email[:permission] segments separated by semicolons.
type MailboxImportRow struct {
	Address     string
	DisplayName string
	Members     string
}

// MailboxImportStatus is the outcome of one import row.
type MailboxImportStatus string

const (
	// MailboxImportValid is a dry-run row that would be created.
	MailboxImportValid MailboxImportStatus = "valid"
	// MailboxImportCreated is a row whose mailbox was created.
	MailboxImportCreated MailboxImportStatus = "created"
	// MailboxImportFailed is a row that was not, or would not be, created.
	MailboxImportFailed MailboxImportStatus = "failed"
)

// MailboxImportRowResult is the outcome of one row; Row counts from 1 in request order.
type MailboxImportRowResult struct {
	Row       int
	Address   string
	Status    MailboxImportStatus
	MailboxID uint
	// ErrorCode is the public error code of a failed row, empty otherwise.
	ErrorCode string
	// MissingEmails are the member emails of no organization member, never nil.
	MissingEmails []string
}

// MailboxImportResult is the outcome of an import, one result per row in request order.
type MailboxImportResult struct {
	DryRun    bool
	Succeeded int
	Failed    int
	Rows      []MailboxImportRowResult
}

// OrganizationMailboxSummary is what an operator needs to know about an organization's mailboxes:
// how many it has, the configured per-organization limit, and its connected domains.
type OrganizationMailboxSummary struct {
	Mailboxes    int64
	MaxMailboxes int
	Domains      []*MailDomain
}

// MailboxOperatorConsole is the mailbox starter's seam for operator console commands. It runs the
// same import as POST /v1/mailbox-imports for a resolved owner or admin, and reads what an
// operator needs to pick the organization and check its limits.
type MailboxOperatorConsole interface {
	// ImportMailboxes checks, or outside a dry run creates, one shared mailbox per row as the
	// viewer. Only owners and admins may import.
	ImportMailboxes(ctx context.Context, viewer OrganizationContext, rows []MailboxImportRow, dryRun bool) (*MailboxImportResult, error)
	// OrganizationMailboxSummary counts the organization's mailboxes and lists its domains.
	OrganizationMailboxSummary(ctx context.Context, organizationID uint) (*OrganizationMailboxSummary, error)
	// FindConnectedDomain loads a connected domain by name on the whole instance; nil when no
	// organization connected it.
	FindConnectedDomain(ctx context.Context, name string) (*MailDomain, error)
}
