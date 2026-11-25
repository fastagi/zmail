package mailbox

import (
	"context"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// CoreService owns the high-frequency mailbox core: mailbox lifecycle, messages, threads,
// attachments, send/receive, organization (folders/labels/trash), unified inbox, mail roles,
// access/handover, signatures, drafts, thread collaboration, logs, and stats.
type CoreService interface {
	Domain() string
	// CreateMailbox creates a personal or shared mailbox on the input's domain, the shared domain
	// when it is empty. Only owners and admins may.
	CreateMailbox(ctx context.Context, viewer domain.OrganizationContext, input CreateMailboxInput) (*domain.Mailbox, error)
	// ListMailboxes lists the mailboxes the viewer has any access to, with that access; the usable
	// scope keeps only those the viewer can read or send from, plus, for owners and admins,
	// personal mailboxes awaiting handover.
	ListMailboxes(ctx context.Context, viewer domain.OrganizationContext, scope MailboxListScope) ([]*domain.Mailbox, error)
	// MaxMailboxes is the configured per-organization mailbox limit.
	MaxMailboxes() int
	DeleteMailbox(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint) error
	// RenameMailbox changes a mailbox's display name; only owners and admins may.
	RenameMailbox(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, displayName string) (*domain.Mailbox, error)
	// ListMessages lists a mailbox's messages, or with filter.GroupThreads one row per thread, each
	// row the newest message of its thread in the view with its Thread filled.
	ListMessages(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, filter domain.MailMessageFilter) ([]*domain.MailMessage, int64, error)
	// GetThread returns a thread's messages outside the trash, oldest first, as message details.
	GetThread(ctx context.Context, viewer domain.OrganizationContext, mailboxID, threadID uint) ([]*domain.MailMessage, error)
	// MessageCounts returns the viewer's unread and starred counts for the mailbox's navigation.
	MessageCounts(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint) (domain.MailMessageCounts, error)
	GetMessage(ctx context.Context, viewer domain.OrganizationContext, mailboxID, messageID uint) (*domain.MailMessage, error)
	// SendMessage reports created=false when an idempotency key replayed an earlier attempt.
	SendMessage(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, input SendInput) (message *domain.MailMessage, created bool, err error)
	// SendFromAddress sends from the mailbox with this address, which the viewer must be allowed to send from.
	SendFromAddress(ctx context.Context, viewer domain.OrganizationContext, from string, input SendInput) (message *domain.MailMessage, created bool, err error)
	ListMailboxMembers(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint) ([]*domain.MailboxMember, error)
	SetMailboxMember(ctx context.Context, viewer domain.OrganizationContext, mailboxID, userID uint, permission domain.MailboxPermission) (*domain.MailboxMember, error)
	SetPendingMailboxMember(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, email string, permission domain.MailboxPermission) (*domain.MailboxPendingMember, error)
	RemoveMailboxMember(ctx context.Context, viewer domain.OrganizationContext, mailboxID, userID uint) error
	ListMailboxAccessEvents(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, page, size int) ([]*domain.MailboxAccessEvent, int64, error)
	// HandOverMailbox makes a personal mailbox shared with a successor and seals its history.
	HandOverMailbox(ctx context.Context, viewer domain.OrganizationContext, mailboxID, successorUserID uint) (*domain.Mailbox, error)
	// SetMailboxWebhooks turns mail webhooks of a shared mailbox on or off.
	SetMailboxWebhooks(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, enabled bool) (*domain.Mailbox, error)
	// DeleteSealedHistory deletes the messages and broadcasts before the seal boundary.
	DeleteSealedHistory(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, confirmAddress string) (*SealedDeletion, error)
	// MemberRemoved and MemberJoined follow the organization starter's membership events.
	MemberRemoved(ctx context.Context, event domain.OrganizationMemberRemovedEvent) error
	MemberJoined(ctx context.Context, event domain.OrganizationMemberJoinedEvent) error
	// GetAttachment returns one attachment of a message with its content.
	GetAttachment(ctx context.Context, viewer domain.OrganizationContext, mailboxID, messageID, attachmentID uint) (*domain.MailAttachment, error)
	GetOutboundMessage(ctx context.Context, viewer domain.OrganizationContext, messageID uint) (*domain.MailMessage, error)
	SetMessageRead(ctx context.Context, viewer domain.OrganizationContext, mailboxID, messageID uint, read bool) (*domain.MailMessage, error)
	// DeleteMessage moves a message to the trash, or deletes it for good when it is already there.
	DeleteMessage(ctx context.Context, viewer domain.OrganizationContext, mailboxID, messageID uint) error
	// UpdateMessage and UpdateMessages mark, star, move, or label messages; see MessageUpdate.
	UpdateMessage(ctx context.Context, viewer domain.OrganizationContext, mailboxID, messageID uint, update MessageUpdate) (*domain.MailMessage, error)
	UpdateMessages(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, ids []uint, update MessageUpdate) (int, error)
	// PurgeTrash empties the trash of a mailbox and returns how many messages it deleted.
	PurgeTrash(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint) (int, error)
	ListFolders(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint) ([]*domain.MailFolder, error)
	CreateFolder(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, name string) (*domain.MailFolder, error)
	RenameFolder(ctx context.Context, viewer domain.OrganizationContext, mailboxID, folderID uint, name string) (*domain.MailFolder, error)
	DeleteFolder(ctx context.Context, viewer domain.OrganizationContext, mailboxID, folderID uint) error
	ListLabels(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint) ([]*domain.MailLabel, error)
	CreateLabel(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, name string, color domain.MailLabelColor) (*domain.MailLabel, error)
	UpdateLabel(ctx context.Context, viewer domain.OrganizationContext, mailboxID, labelID uint, name string, color domain.MailLabelColor) (*domain.MailLabel, error)
	DeleteLabel(ctx context.Context, viewer domain.OrganizationContext, mailboxID, labelID uint) error
	// ListInbox lists one page of the unified inbox: the viewer's mail across every mailbox they
	// may read.
	ListInbox(ctx context.Context, viewer domain.OrganizationContext, request InboxRequest) (InboxPage, error)
	// InboxCounts counts the viewer's unified inbox views, mail roles, and mailboxes, with today
	// as the viewer's day in location.
	InboxCounts(ctx context.Context, viewer domain.OrganizationContext, location *time.Location) (InboxCounts, error)
	// ListMailRoles lists the organization's mail roles with the mailboxes of each the viewer can
	// see; members get only the roles holding such a mailbox.
	ListMailRoles(ctx context.Context, viewer domain.OrganizationContext) ([]*domain.MailRole, error)
	// CreateMailRole, UpdateMailRole, and DeleteMailRole write mail roles; only owners and admins
	// may. Mail roles group mailboxes for display and grant nothing.
	CreateMailRole(ctx context.Context, viewer domain.OrganizationContext, input MailRoleInput) (*domain.MailRole, error)
	UpdateMailRole(ctx context.Context, viewer domain.OrganizationContext, roleID uint, input MailRoleInput) (*domain.MailRole, error)
	DeleteMailRole(ctx context.Context, viewer domain.OrganizationContext, roleID uint) error
	// AssignMailboxRole puts a mailbox in a mail role, or takes it out when roleID is nil. It needs
	// administer on the mailbox.
	AssignMailboxRole(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, roleID *uint) (*domain.Mailbox, error)
	// MailSignature reads the viewer's own signature for a mailbox they may send from.
	MailSignature(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint) (domain.MailSignature, error)
	// SaveMailSignature replaces the viewer's own signature for a mailbox they may send from; an
	// empty body removes it.
	SaveMailSignature(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, body string) (domain.MailSignature, error)
	// MailDrafts lists the viewer's own drafts in the organization, the latest change first.
	MailDrafts(ctx context.Context, viewer domain.OrganizationContext) ([]domain.MailDraft, error)
	// MailDraft reads one of the viewer's own drafts.
	MailDraft(ctx context.Context, viewer domain.OrganizationContext, id uint) (domain.MailDraft, error)
	CreateMailDraft(ctx context.Context, viewer domain.OrganizationContext, input MailDraftInput) (domain.MailDraft, error)
	// UpdateMailDraft replaces what one of the viewer's drafts holds.
	UpdateMailDraft(ctx context.Context, viewer domain.OrganizationContext, id uint, input MailDraftInput) (domain.MailDraft, error)
	// DeleteMailDraft removes one of the viewer's drafts.
	DeleteMailDraft(ctx context.Context, viewer domain.OrganizationContext, id uint) error
	// MailThreadAssignment reads who works a shared mailbox's thread.
	MailThreadAssignment(ctx context.Context, viewer domain.OrganizationContext, mailboxID, threadID uint) (MailThreadAssignmentView, error)
	// SaveMailThreadAssignment assigns a shared mailbox's thread and sets its status.
	SaveMailThreadAssignment(ctx context.Context, viewer domain.OrganizationContext, mailboxID, threadID uint, input MailThreadAssignmentInput) (MailThreadAssignmentView, error)
	// MailThreadNotes reads the internal notes on a shared mailbox's thread.
	MailThreadNotes(ctx context.Context, viewer domain.OrganizationContext, mailboxID, threadID uint) ([]domain.MailThreadNote, error)
	// AddMailThreadNote leaves an internal note on a shared mailbox's thread.
	AddMailThreadNote(ctx context.Context, viewer domain.OrganizationContext, mailboxID, threadID uint, body string) (domain.MailThreadNote, error)
	// DeleteMailThreadNote removes one of the viewer's own notes.
	DeleteMailThreadNote(ctx context.Context, viewer domain.OrganizationContext, mailboxID, threadID, noteID uint) error
	// ListLog lists messages across the mailboxes the viewer may read.
	ListLog(ctx context.Context, viewer domain.OrganizationContext, filter domain.MailLogFilter) ([]*domain.MailMessage, int64, error)
	// Stats returns one entry per UTC day for the last days days, oldest first, including empty
	// days, counting only the mailboxes the viewer may read.
	Stats(ctx context.Context, viewer domain.OrganizationContext, days int) ([]domain.MailDayStats, error)
	Provider() string
	InboundEnabled() bool
	DailySendLimit() int
	// Now is the service clock, which names export files.
	Now() time.Time
	// OrganizationDailyLimits returns the lane limits in effect for one organization.
	OrganizationDailyLimits(ctx context.Context, organizationID uint) (DailyLimits, error)
	ReceiveInbound(ctx context.Context, delivery InboundDelivery) (message *domain.MailMessage, created bool, err error)
}

// DomainService owns custom mail domains, managed DNS zones, DNS records, and Cloudflare setup.
type DomainService interface {
	CloudflareConnectionService
	CustomDomainsEnabled() bool
	// MailboxDomains lists the shared domain and the organization's verified domains.
	MailboxDomains(ctx context.Context, organizationID uint) ([]MailboxDomain, error)
	ListMailDomains(ctx context.Context, organizationID uint) ([]*domain.MailDomain, error)
	GetMailDomain(ctx context.Context, organizationID, id uint) (*domain.MailDomain, error)
	ConnectMailDomain(ctx context.Context, organizationID, createdBy uint, name string, capability domain.MailDomainCapability) (*domain.MailDomain, error)
	SetUpMailDomain(ctx context.Context, organizationID, id uint) (*domain.MailDomain, error)
	CheckMailDomain(ctx context.Context, organizationID, id uint) (*domain.MailDomain, error)
	DeleteMailDomain(ctx context.Context, organizationID, id uint) error
	// SetDefaultMailDomain makes a verified domain the one new mailboxes use unless another is
	// picked. Only owners and admins may.
	SetDefaultMailDomain(ctx context.Context, organizationID, id uint) (*domain.MailDomain, error)
	// EnableMailDomainReceiving turns a send-only domain into one that also receives and sets it
	// up again.
	EnableMailDomainReceiving(ctx context.Context, organizationID, id uint) (*domain.MailDomain, error)
	// PreviewMailDomain reads what a domain would be connected as, without changing anything.
	PreviewMailDomain(ctx context.Context, name string) (*MailDomainPreview, error)
	ListMailZones(ctx context.Context, organizationID uint) ([]*domain.MailZone, error)
	GetMailZone(ctx context.Context, organizationID, id uint) (*domain.MailZone, error)
	// DeleteMailZone removes a managed zone no domain uses, once the registrar no longer
	// delegates it, from Zmail and from the provider account.
	DeleteMailZone(ctx context.Context, organizationID, id uint) error
	// ListMailDNSRecords reads a managed zone's records from Cloudflare, marking the ones Zmail
	// mail relies on.
	ListMailDNSRecords(ctx context.Context, organizationID, zoneID uint) ([]MailDNSRecord, error)
	CreateMailDNSRecord(ctx context.Context, organizationID, zoneID uint, input MailDNSRecordInput) (*MailDNSRecord, error)
	UpdateMailDNSRecord(ctx context.Context, organizationID, zoneID uint, recordID string, patch MailDNSRecordPatch) (*MailDNSRecord, error)
	DeleteMailDNSRecord(ctx context.Context, organizationID, zoneID uint, recordID string) error
}

// BroadcastService owns templates, contacts, contact lists, broadcasts, and unsubscribes.
type BroadcastService interface {
	// ListTemplates lists templates of one kind, or every template when kind is empty.
	ListTemplates(ctx context.Context, organizationID uint, kind domain.MailTemplateKind) ([]*domain.MailTemplate, error)
	TemplatesOutsideSendingScope(ctx context.Context, viewer domain.OrganizationContext, templates []*domain.MailTemplate) map[uint]bool
	GetTemplate(ctx context.Context, organizationID, id uint) (*domain.MailTemplate, error)
	// CreateTemplate, UpdateTemplate, and DeleteTemplate let every member write standard
	// templates and only owners and admins write transactional ones.
	CreateTemplate(ctx context.Context, viewer domain.OrganizationContext, input TemplateInput) (*domain.MailTemplate, error)
	UpdateTemplate(ctx context.Context, viewer domain.OrganizationContext, id uint, input TemplateInput) (*domain.MailTemplate, error)
	DeleteTemplate(ctx context.Context, viewer domain.OrganizationContext, id uint) error
	PreviewTemplate(ctx context.Context, organizationID uint, input PreviewInput) (*TemplatePreview, error)
	SendTemplateTest(ctx context.Context, viewer domain.OrganizationContext, templateID, mailboxID uint, to []string, values map[string]string) (*domain.MailMessage, error)
	ListContacts(ctx context.Context, organizationID uint, filter domain.MailContactFilter) ([]*domain.MailContact, int64, error)
	GetContact(ctx context.Context, organizationID, id uint) (*domain.MailContact, error)
	CreateContact(ctx context.Context, organizationID uint, input ContactInput) (*domain.MailContact, error)
	UpdateContact(ctx context.Context, organizationID, id uint, input ContactInput) (*domain.MailContact, error)
	DeleteContact(ctx context.Context, organizationID, id uint) error
	ImportContacts(ctx context.Context, organizationID uint, text string, listID uint) (*ContactImportResult, error)
	ListContactLists(ctx context.Context, organizationID uint) ([]*domain.MailContactList, error)
	CreateContactList(ctx context.Context, organizationID uint, name string) (*domain.MailContactList, error)
	RenameContactList(ctx context.Context, organizationID, id uint, name string) (*domain.MailContactList, error)
	DeleteContactList(ctx context.Context, organizationID, id uint) error
	BroadcastsEnabled() bool
	DailyBroadcastLimit() int
	// ListBroadcasts lists the broadcasts of the mailboxes the viewer may read.
	ListBroadcasts(ctx context.Context, viewer domain.OrganizationContext, page, perPage int) ([]*domain.MailBroadcast, int64, error)
	GetBroadcast(ctx context.Context, viewer domain.OrganizationContext, id uint) (*domain.MailBroadcast, error)
	// BroadcastProgress estimates the send rate and finish of a broadcast the caller already read.
	BroadcastProgress(ctx context.Context, broadcast *domain.MailBroadcast) (*domain.MailBroadcastProgress, error)
	CreateBroadcast(ctx context.Context, viewer domain.OrganizationContext, input BroadcastInput) (*domain.MailBroadcast, error)
	UpdateBroadcast(ctx context.Context, viewer domain.OrganizationContext, id uint, input BroadcastInput) (*domain.MailBroadcast, error)
	DeleteBroadcast(ctx context.Context, viewer domain.OrganizationContext, id uint) error
	SendBroadcast(ctx context.Context, viewer domain.OrganizationContext, id uint, scheduledAt *time.Time) (*domain.MailBroadcast, error)
	// CancelBroadcast and PauseBroadcast need no access to the sender mailbox; visible reports
	// whether the viewer may read it, and so see more than the broadcast's id and status.
	CancelBroadcast(ctx context.Context, viewer domain.OrganizationContext, id uint) (broadcast *domain.MailBroadcast, visible bool, err error)
	PauseBroadcast(ctx context.Context, viewer domain.OrganizationContext, id uint) (broadcast *domain.MailBroadcast, visible bool, err error)
	ResumeBroadcast(ctx context.Context, viewer domain.OrganizationContext, id uint) (*domain.MailBroadcast, error)
	ListBroadcastRecipients(ctx context.Context, viewer domain.OrganizationContext, id uint, status domain.MailRecipientStatus, page, perPage int) ([]*domain.MailBroadcastRecipient, int64, error)
	Unsubscribe(ctx context.Context, token string) error
}

// TransactionalService owns transactional mail delivery, logs, send lanes, suppressions, and
// provider delivery event ingestion.
type TransactionalService interface {
	DailyTransactionalLimit() int
	// TransactionalPolicy reports the instance's transactional switch, rate limits, and simulator domain.
	TransactionalPolicy() TransactionalPolicy
	// SendTransactional sends one transactional message for an API key; created is false for an
	// idempotent replay.
	SendTransactional(ctx context.Context, actor TransactionalActor, input TransactionalSendInput) (message *domain.TransactionalMessage, created bool, err error)
	// SendDirectTransactional sends one direct (template-free) transactional message for an API key;
	// created is false for an idempotent replay.
	SendDirectTransactional(ctx context.Context, actor TransactionalActor, input TransactionalDirectSendInput) (message *domain.TransactionalMessage, created bool, err error)
	// GetTransactional reads a transactional message of the organization and mode.
	GetTransactional(ctx context.Context, organizationID uint, mode domain.APIKeyMode, id uint) (*domain.TransactionalMessage, error)
	// SendTransactionalTest sends a transactional template to the caller's own account address.
	SendTransactionalTest(ctx context.Context, viewer domain.OrganizationContext, input TransactionalTestInput) (*domain.TransactionalMessage, error)
	// ListTransactionalLog lists the organization's transactional messages for the console.
	ListTransactionalLog(ctx context.Context, organizationID uint, filter domain.TransactionalLogFilter) (TransactionalLogPage, error)
	// GetTransactionalLogEntry reads one transactional message of the organization, of either mode.
	GetTransactionalLogEntry(ctx context.Context, organizationID, id uint) (*domain.TransactionalMessage, error)
	// ListSendLanes counts each lane's usage over the rolling window ending at computedAt; only
	// owners and admins may.
	ListSendLanes(ctx context.Context, viewer domain.OrganizationContext) (lanes []SendLaneUsage, computedAt time.Time, err error)
	// ListSuppressions lists one page of the suppression list and the next page's cursor; only
	// owners and admins may.
	ListSuppressions(ctx context.Context, viewer domain.OrganizationContext, filter domain.MailSuppressionFilter) ([]domain.MailSuppression, *uint, error)
	// RemoveSuppression takes an entry off the suppression list, succeeding when there is none.
	RemoveSuppression(ctx context.Context, viewer domain.OrganizationContext, id uint) error
	// RecordDeliveryEvent stores one signed provider event about an outbound mailbox message or a
	// transactional message. Its Recorded is false when the event was already stored.
	RecordDeliveryEvent(ctx context.Context, delivery DeliveryEvent) (DeliveryEventResult, error)
}

// ClientAccessService owns IMAP/SMTP app passwords and organization mail client settings.
type ClientAccessService interface {
	// ListAppPasswords lists the viewer's own mail client passwords with access and server details.
	ListAppPasswords(ctx context.Context, viewer domain.OrganizationContext) (*AppPasswordList, error)
	// CreateAppPassword creates a mail client password and returns its secret, this once.
	CreateAppPassword(ctx context.Context, viewer domain.OrganizationContext, input CreateAppPasswordInput) (*domain.MailAppPassword, string, error)
	// RevokeAppPassword revokes one of the viewer's own mail client passwords.
	RevokeAppPassword(ctx context.Context, viewer domain.OrganizationContext, id uint) error
	// AppPasswordDirectory pages the organization's mail client passwords for owners and admins.
	AppPasswordDirectory(ctx context.Context, viewer domain.OrganizationContext, page, perPage int) ([]*domain.MailAppPassword, int64, error)
	// ForceRevokeAppPassword revokes any member's mail client password; owners and admins only.
	ForceRevokeAppPassword(ctx context.Context, viewer domain.OrganizationContext, id uint) error
	// MailClientSettings reads the organization's mail app sign-in switch.
	MailClientSettings(ctx context.Context, viewer domain.OrganizationContext) (MailClientSettings, error)
	// UpdateMailClientSettings sets the organization's mail app sign-in switch; owners and admins only.
	UpdateMailClientSettings(ctx context.Context, viewer domain.OrganizationContext, enabled bool) (MailClientSettings, error)
}

// RosterService owns mailbox directory administration, CSV import/export, member welcome mails,
// and the platform account ledger.
type RosterService interface {
	PlatformAccountService
	// MailboxDirectory lists one page of the organization's mailboxes with member counts and send
	// attempts; only owners and admins may.
	MailboxDirectory(ctx context.Context, viewer domain.OrganizationContext, filter domain.MailboxDirectoryFilter) (*MailboxDirectoryPage, error)
	// ImportMailboxes checks or creates shared mailboxes row by row; only owners and admins may.
	ImportMailboxes(ctx context.Context, viewer domain.OrganizationContext, rows []MailboxImportRow, dryRun bool) (*MailboxImportResult, error)
	// ImportMembers invites roster rows, reserves each a personal mailbox, and sets their grants;
	// only owners and admins may.
	ImportMembers(ctx context.Context, viewer domain.OrganizationContext, input MemberImportInput) (*MemberImportResult, error)
	// ExportMailboxes reads every mailbox of one type with members' emails; only owners and admins may.
	ExportMailboxes(ctx context.Context, viewer domain.OrganizationContext, mailboxType domain.MailboxType) ([]MailboxExportRow, error)
	// MemberWelcomeSetting reads the organization's welcome mail setting; owners and admins only.
	MemberWelcomeSetting(ctx context.Context, viewer domain.OrganizationContext) (MemberWelcomeSetting, error)
	// UpdateMemberWelcomeSetting replaces the welcome mail setting; owners and admins only.
	UpdateMemberWelcomeSetting(ctx context.Context, viewer domain.OrganizationContext, input MemberWelcomeInput) (MemberWelcomeSetting, error)
	// PreviewMemberWelcome renders welcome copy with sample values; owners and admins only.
	PreviewMemberWelcome(ctx context.Context, viewer domain.OrganizationContext, input MemberWelcomePreviewInput) (MemberWelcomePreview, error)
	// ListMemberWelcomeMails lists the mailboxes that can be welcomed with their latest welcome
	// mail; owners and admins only.
	ListMemberWelcomeMails(ctx context.Context, viewer domain.OrganizationContext) ([]MemberWelcomeMail, error)
	// ResendMemberWelcome sends a mailbox's welcome mail again; owners and admins only.
	ResendMemberWelcome(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint) (MemberWelcomeMail, error)
}

// ExternalMailAllService composes the read-only external mail, push, binding, ops, directory,
// and tenant authorization surfaces.
type ExternalMailAllService interface {
	ExternalMailService
	ExternalMailPushService
	ExternalMailBindingService
	ExternalMailOpsService
	ExternalMailDirectoryService
	ExternalMailTenantService
}
