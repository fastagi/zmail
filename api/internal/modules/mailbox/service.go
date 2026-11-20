package mailbox

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/fastagi/zmail/api/internal/capabilities/crypto"
	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
	"github.com/fastagi/zmail/api/internal/infra/metrics"
	"github.com/fastagi/zmail/api/internal/infra/ratelimit"
	auditstarter "github.com/fastagi/zmail/api/internal/modules/audit"
)

const (
	// MaxInboundBytes bounds one raw inbound message accepted from the mail edge.
	MaxInboundBytes = 5 * 1024 * 1024

	sendLimitWindow       = 24 * time.Hour
	deliveryRecordTimeout = 5 * time.Second
	failureNotConfigured  = "transport.not_configured"
	failureUnknown        = "transport.failed"
	// failureSendCanceled marks a waiting message that will never be sent: its mailbox was
	// deleted or a handover sealed it.
	failureSendCanceled = "send.canceled"
)

// Settings is the resolved policy the service runs with.
type Settings struct {
	Domain string
	// Provider names the outbound transport, for display only.
	Provider         string
	InboundSecret    string
	InboundTolerance time.Duration
	// DailySendLimit is the personal lane limit: console sends, replies, API outbound messages, and
	// template tests one organization may attempt per rolling 24 hours.
	DailySendLimit int
	// DailyBroadcastLimit is the broadcast lane limit; 0 takes DailySendLimit.
	DailyBroadcastLimit int
	// DailyTransactionalLimit is the transactional lane limit; 0 takes DailySendLimit.
	DailyTransactionalLimit int
	// PublicURL is the public Web origin unsubscribe links point at; empty disables broadcasts.
	PublicURL string
	// CustomDomainsEnabled lets organizations connect their own domains.
	CustomDomainsEnabled bool
	// EmailWorker names the Email Worker routing rules of organization domains deliver to.
	EmailWorker string
	// MaxZonesPerOrganization bounds the managed zones one organization may have Zmail create.
	MaxZonesPerOrganization int
	// TransactionalEnabled accepts transactional messages; SecretEncryptionKey must be set too.
	TransactionalEnabled bool
	// SecretEncryptionKey encrypts queued transactional content and keys idempotency fingerprints.
	SecretEncryptionKey string
	// SimulatorDomain is the domain whose addresses transactional mail simulates; empty takes
	// the default.
	SimulatorDomain string
	// TransactionalRecipientPerMinute and TransactionalKeyPerSecond are the transactional rate
	// limits; 0 takes the defaults.
	TransactionalRecipientPerMinute int
	TransactionalKeyPerSecond       int
}

// InboundDelivery is one signed delivery from the mail edge.
type InboundDelivery struct {
	Recipient string
	Signature string
	Raw       []byte
}

// DeliveryEvent is one signed provider event forwarded by the mail edge.
type DeliveryEvent struct {
	Signature string
	Raw       []byte
}

// SendInput is a validated request to send one message.
type SendInput struct {
	To        []string
	Cc        []string
	Subject   string
	Text      string
	HTML      string
	InReplyTo string
	// Attachments are optional files, bounded in number and total size.
	Attachments []AttachmentInput
	// Template, when set, supplies the subject and bodies; Subject, Text, and HTML must be empty.
	Template *TemplateSendInput
	// Headers are extra message headers set by Zmail itself, such as List-Unsubscribe. Requests
	// from members and API keys never set them.
	Headers map[string]string
	// IdempotencyKey is optional. A repeated send with the same key and the same content in the
	// same mailbox returns the first attempt's outcome instead of sending again.
	IdempotencyKey string
}

// Service defines the business interface of the mailbox starter.
type Service interface {
	Domain() string
	// CreateMailbox creates a personal or shared mailbox on the input's domain, the shared domain
	// when it is empty. Only owners and admins may.
	CreateMailbox(ctx context.Context, viewer domain.OrganizationContext, input CreateMailboxInput) (*domain.Mailbox, error)
	CustomDomainsEnabled() bool
	// MailboxDomains lists the shared domain and the organization's verified domains.
	MailboxDomains(ctx context.Context, organizationID uint) ([]MailboxDomain, error)
	ListMailDomains(ctx context.Context, organizationID uint) ([]*domain.MailDomain, error)
	GetMailDomain(ctx context.Context, organizationID, id uint) (*domain.MailDomain, error)
	ConnectMailDomain(ctx context.Context, organizationID, createdBy uint, name string, capability domain.MailDomainCapability) (*domain.MailDomain, error)
	SetUpMailDomain(ctx context.Context, organizationID, id uint) (*domain.MailDomain, error)
	CheckMailDomain(ctx context.Context, organizationID, id uint) (*domain.MailDomain, error)
	DeleteMailDomain(ctx context.Context, organizationID, id uint) error
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
	// ListMailboxes lists the mailboxes the viewer has any access to, with that access.
	ListMailboxes(ctx context.Context, viewer domain.OrganizationContext) ([]*domain.Mailbox, error)
	DeleteMailbox(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint) error
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
	// ListTemplates lists templates of one kind, or every template when kind is empty.
	ListTemplates(ctx context.Context, organizationID uint, kind domain.MailTemplateKind) ([]*domain.MailTemplate, error)
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
	// ListBroadcasts lists the broadcasts of the mailboxes the viewer may read.
	ListBroadcasts(ctx context.Context, viewer domain.OrganizationContext, page, perPage int) ([]*domain.MailBroadcast, int64, error)
	GetBroadcast(ctx context.Context, viewer domain.OrganizationContext, id uint) (*domain.MailBroadcast, error)
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
	// ListLog lists messages across the mailboxes the viewer may read.
	ListLog(ctx context.Context, viewer domain.OrganizationContext, filter domain.MailLogFilter) ([]*domain.MailMessage, int64, error)
	// Stats returns one entry per UTC day for the last days days, oldest first, including empty
	// days, counting only the mailboxes the viewer may read.
	Stats(ctx context.Context, viewer domain.OrganizationContext, days int) ([]domain.MailDayStats, error)
	Provider() string
	InboundEnabled() bool
	DailySendLimit() int
	DailyBroadcastLimit() int
	DailyTransactionalLimit() int
	// TransactionalPolicy reports the instance's transactional switch, rate limits, and simulator domain.
	TransactionalPolicy() TransactionalPolicy
	// SendTransactional sends one transactional message for an API key; created is false for an
	// idempotent replay.
	SendTransactional(ctx context.Context, actor TransactionalActor, input TransactionalSendInput) (message *domain.TransactionalMessage, created bool, err error)
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
	ReceiveInbound(ctx context.Context, delivery InboundDelivery) (message *domain.MailMessage, created bool, err error)
}

type service struct {
	repo domain.MailboxRepository
	// memberships validates owners and members against the organization's current members.
	memberships membershipDirectory
	templates   domain.MailTemplateRepository
	contacts    domain.MailContactRepository
	// broadcasts is the broadcast store; nil until NewService wires it.
	broadcasts domain.MailBroadcastRepository
	// organize stores folders, labels, placements, and stars; nil when a test does not need them.
	organize domain.MailOrganizeRepository
	// inbox reads the unified inbox; nil when repo does not implement it.
	inbox domain.MailInboxRepository
	// roles stores mail roles; nil when repo does not implement it.
	roles domain.MailRoleRepository
	// domains, zones, and domainProvider serve organization domains; nil when a test does not
	// need them.
	domains        domain.MailDomainRepository
	zones          domain.MailZoneRepository
	domainProvider mailtransport.DomainProvider
	// mxResolver and nsResolver read public DNS for previews and zone removal; tests fake them.
	mxResolver mxResolver
	nsResolver nsResolver
	// setupMu guards domainSetups, the setups running in the background by domain ID.
	setupMu      sync.Mutex
	domainSetups map[uint]*domainSetupRun
	// setups counts the background setups that have not ended, for WaitForSetups.
	setups sync.WaitGroup
	// background runs domain setups off the request path; tests make it synchronous.
	background func(func())
	// setupClaimHook, when set, runs right after a request found a running setup of its domain;
	// tests use it to end that setup at the worst moment.
	setupClaimHook func()
	transport      mailtransport.Transport
	notifier       mailNotifier
	settings       Settings
	now            func() time.Time
	// recordAudit enriches the request's audit entry; tests capture it.
	recordAudit func(context.Context, auditstarter.Change)
	// transactional stores transactional messages and suppressions; nil when a test does not
	// need them, which keeps transactional mail disabled.
	transactional domain.TransactionalMailRepository
	// accounts reads the caller's account address for console test sends.
	accounts            accountDirectory
	transactionalLimits transactionalLimits
	// encryptor seals queued transactional content; nil without SecretEncryptionKey.
	encryptor      *crypto.AESEncryptor
	fingerprintKey []byte
}

var _ Service = (*service)(nil)

// NewService creates the mailbox service.
func NewService(
	repo domain.MailboxRepository,
	memberships membershipDirectory,
	templates domain.MailTemplateRepository,
	contacts domain.MailContactRepository,
	broadcasts domain.MailBroadcastRepository,
	organize domain.MailOrganizeRepository,
	domains domain.MailDomainRepository,
	zones domain.MailZoneRepository,
	domainProvider mailtransport.DomainProvider,
	transport mailtransport.Transport,
	notifier mailNotifier,
	settings Settings,
	transactional domain.TransactionalMailRepository,
	limits *ratelimit.Stores,
	accounts accountDirectory,
) *service {
	var inbox domain.MailInboxRepository
	if seam, ok := repo.(domain.MailInboxRepository); ok {
		inbox = seam
	}
	var roles domain.MailRoleRepository
	if seam, ok := repo.(domain.MailRoleRepository); ok {
		roles = seam
	}
	var encryptor *crypto.AESEncryptor
	var fingerprintKey []byte
	if settings.SecretEncryptionKey != "" {
		encryptor = crypto.NewAESEncryptorFromString(settings.SecretEncryptionKey)
		fingerprintKey = transactionalFingerprintKey(settings.SecretEncryptionKey)
	}
	return &service{
		transactional: transactional, accounts: accounts, transactionalLimits: newTransactionalLimits(limits, settings),
		encryptor: encryptor, fingerprintKey: fingerprintKey,
		repo: repo, inbox: inbox, roles: roles, memberships: memberships, templates: templates, contacts: contacts, broadcasts: broadcasts,
		organize: organize, domains: domains, zones: zones,
		domainProvider: domainProvider, transport: transport, notifier: notifier, settings: settings, now: time.Now,
		background: func(run func()) { go run() },
		mxResolver: net.DefaultResolver, nsResolver: net.DefaultResolver,
		recordAudit: auditstarter.RecordChange,
	}
}

// apply runs a state change and publishes the notifications it returns in one transaction, so
// a webhook is queued exactly when its state change commits. If publishing fails, the state
// change is applied on its own: a webhook that cannot be queued must never undo the record of
// mail that was really sent or received.
func (s *service) apply(ctx context.Context, state func(ctx context.Context) ([]MailNotification, error)) error {
	var publishErr error
	err := s.repo.InTransaction(ctx, func(tx context.Context) error {
		notifications, err := state(tx)
		if err != nil {
			return err
		}
		for _, notification := range notifications {
			if publishErr = s.notifier.Notify(tx, notification); publishErr != nil {
				return publishErr
			}
		}
		return nil
	})
	if err == nil || publishErr == nil {
		return err
	}
	slog.ErrorContext(ctx, "mailbox webhook publication failed; recording the mail event without it",
		slog.String("error", publishErr.Error()))
	return s.repo.InTransaction(ctx, func(tx context.Context) error {
		_, stateErr := state(tx)
		return stateErr
	})
}

// SettingsFromConfig resolves the service policy from typed configuration.
func SettingsFromConfig(cfg *config.Config) Settings {
	settings := Settings{
		Domain:           config.DefaultMailboxDomain,
		Provider:         config.MailboxProviderLog,
		InboundTolerance: config.DefaultMailboxInboundTolerance,
		DailySendLimit:   config.DefaultMailboxDailySendLimit,

		MaxZonesPerOrganization: config.DefaultMailboxMaxZonesPerOrganization,
	}
	if cfg == nil {
		return settings
	}
	resolved := cfg.Mailbox.Resolved(cfg.IsProduction())
	settings.Domain = resolved.Domain
	settings.Provider = resolved.Provider
	settings.InboundSecret = resolved.InboundSecret
	settings.PublicURL = resolved.PublicURL
	settings.CustomDomainsEnabled = resolved.CustomDomainsEnabled
	settings.EmailWorker = resolved.CloudflareEmailWorker
	if resolved.DailySendLimit > 0 {
		settings.DailySendLimit = resolved.DailySendLimit
	}
	settings.DailyBroadcastLimit = resolved.DailyBroadcastLimit
	settings.DailyTransactionalLimit = resolved.DailyTransactionalLimit
	if resolved.MaxZonesPerOrganization > 0 {
		settings.MaxZonesPerOrganization = resolved.MaxZonesPerOrganization
	}
	settings.TransactionalEnabled = resolved.TransactionalEnabled
	settings.SecretEncryptionKey = resolved.SecretEncryptionKey
	settings.SimulatorDomain = resolved.SimulatorDomain
	settings.TransactionalRecipientPerMinute = resolved.TransactionalRecipientPerMinute
	settings.TransactionalKeyPerSecond = resolved.TransactionalKeyPerSecond
	return settings
}

func (s *service) Domain() string {
	return s.settings.Domain
}

func (s *service) Provider() string {
	return s.settings.Provider
}

func (s *service) DailySendLimit() int {
	return s.settings.laneLimit(domain.SendLanePersonal)
}

func (s *service) DailyBroadcastLimit() int {
	return s.settings.laneLimit(domain.SendLaneBroadcast)
}

func (s *service) DailyTransactionalLimit() int {
	return s.settings.laneLimit(domain.SendLaneTransactional)
}

// laneLimit is the rolling 24-hour limit of one lane. A lane without its own limit takes the
// personal limit, so no lane has less room than the shared limit gave before lanes existed.
func (settings Settings) laneLimit(lane domain.SendLane) int {
	switch {
	case lane == domain.SendLaneBroadcast && settings.DailyBroadcastLimit > 0:
		return settings.DailyBroadcastLimit
	case lane == domain.SendLaneTransactional && settings.DailyTransactionalLimit > 0:
		return settings.DailyTransactionalLimit
	}
	return settings.DailySendLimit
}

func (s *service) InboundEnabled() bool {
	return s.settings.InboundSecret != ""
}

const (
	maxLogQueryLength = 200
	// MaxStatsDays bounds the statistics window.
	MaxStatsDays = 90
)

func cleanSearchQuery(raw string) (string, bool) {
	query := strings.TrimSpace(raw)
	if !utf8.ValidString(query) || utf8.RuneCountInString(query) > maxLogQueryLength || strings.IndexFunc(query, unicode.IsControl) >= 0 {
		return "", false
	}
	return query, true
}

func (s *service) ListLog(ctx context.Context, viewer domain.OrganizationContext, filter domain.MailLogFilter) ([]*domain.MailMessage, int64, error) {
	if filter.Direction != "" && !filter.Direction.Valid() {
		return nil, 0, domain.ErrInvalidInput
	}
	if filter.Status != "" && !filter.Status.Valid() {
		return nil, 0, domain.ErrInvalidInput
	}
	query, ok := cleanSearchQuery(filter.Query)
	if !ok {
		return nil, 0, domain.ErrInvalidInput
	}
	filter.Query = query
	readable, err := s.readableMailboxIDs(ctx, viewer)
	if err != nil {
		return nil, 0, err
	}
	if filter.MailboxID != 0 {
		// A mailbox filter the viewer may not read lists nothing, like a mailbox without mail.
		readable = slices.DeleteFunc(readable, func(id uint) bool { return id != filter.MailboxID })
	}
	messages, total, err := s.repo.ListMessagesForOrganization(ctx, viewer.OrganizationID, readable, filter)
	if err != nil {
		return nil, 0, err
	}
	if err := s.fillViewerState(ctx, viewer, messages...); err != nil {
		return nil, 0, err
	}
	return messages, total, nil
}

// fillRead sets each inbound message's read state to the viewer's own. Outbound mail is always
// read. Filling it never changes read state.
func (s *service) fillRead(ctx context.Context, viewer domain.OrganizationContext, messages ...*domain.MailMessage) error {
	ids := make([]uint, 0, len(messages))
	for _, message := range messages {
		if message.Direction == domain.MailDirectionInbound {
			ids = append(ids, message.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	read, err := s.repo.ReadMessages(ctx, viewer.MembershipID, ids)
	if err != nil {
		return err
	}
	for _, message := range messages {
		if message.Direction == domain.MailDirectionInbound {
			message.Read = read[message.ID]
		}
	}
	return nil
}

func (s *service) Stats(ctx context.Context, viewer domain.OrganizationContext, days int) ([]domain.MailDayStats, error) {
	if days < 1 || days > MaxStatsDays {
		return nil, domain.ErrInvalidInput
	}
	now := s.now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	first := today.AddDate(0, 0, -(days - 1))
	readable, err := s.readableMailboxIDs(ctx, viewer)
	if err != nil {
		return nil, err
	}
	counted, err := s.repo.StatsForOrganization(ctx, viewer.OrganizationID, readable, first)
	if err != nil {
		return nil, err
	}
	byDay := make(map[time.Time]domain.MailDayStats, len(counted))
	for _, day := range counted {
		byDay[day.Day] = day
	}
	// Every day of the window is present, so a chart needs no gap filling.
	stats := make([]domain.MailDayStats, 0, days)
	for day := first; !day.After(today); day = day.AddDate(0, 0, 1) {
		entry := byDay[day]
		entry.Day = day
		stats = append(stats, entry)
	}
	return stats, nil
}

func (s *service) ListMailboxes(ctx context.Context, viewer domain.OrganizationContext) ([]*domain.Mailbox, error) {
	mailboxes, err := s.visibleMailboxes(ctx, viewer)
	if err != nil {
		return nil, err
	}
	for _, mailbox := range mailboxes {
		// Unread counts describe mail; a viewer who may not read it learns nothing about it.
		if !mailbox.Access.View {
			mailbox.UnreadCount = 0
		}
	}
	return mailboxes, nil
}

func (s *service) DeleteMailbox(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint) error {
	mailbox, err := s.authorizeMailbox(ctx, viewer, mailboxID, canAdminister)
	if err != nil {
		return err
	}
	if err := s.repo.DeleteMailbox(ctx, viewer.OrganizationID, mailboxID); err != nil {
		return err
	}
	s.removeRoutingRule(ctx, mailbox)
	return nil
}

func (s *service) GetMessage(ctx context.Context, viewer domain.OrganizationContext, mailboxID, messageID uint) (*domain.MailMessage, error) {
	if _, err := s.authorizeMailbox(ctx, viewer, mailboxID, canView); err != nil {
		return nil, err
	}
	message, err := s.repo.FindMessage(ctx, mailboxID, messageID)
	if err != nil {
		return nil, err
	}
	if err := s.fillViewerState(ctx, viewer, message); err != nil {
		return nil, err
	}
	return s.withEvents(ctx, message)
}

func (s *service) GetAttachment(ctx context.Context, viewer domain.OrganizationContext, mailboxID, messageID, attachmentID uint) (*domain.MailAttachment, error) {
	if _, err := s.authorizeMailbox(ctx, viewer, mailboxID, canView); err != nil {
		return nil, err
	}
	if _, err := s.repo.FindMessage(ctx, mailboxID, messageID); err != nil {
		return nil, err
	}
	return s.repo.FindMessageAttachment(ctx, messageID, attachmentID)
}

// withEvents fills in what only the detail of a message shows: its delivery history and the
// list of its attachments.
func (s *service) withEvents(ctx context.Context, message *domain.MailMessage) (*domain.MailMessage, error) {
	events, err := s.repo.ListMessageEvents(ctx, message.ID)
	if err != nil {
		return nil, err
	}
	message.Events = events
	message.Attachments = nil
	if message.AttachmentCount > 0 {
		if message.Attachments, err = s.repo.ListMessageAttachments(ctx, message.ID); err != nil {
			return nil, err
		}
	}
	return message, nil
}

// SetMessageRead changes the viewer's own read state of a message; nobody else's changes. It
// needs View. Marking an outbound message changes nothing: it is always read.
func (s *service) SetMessageRead(ctx context.Context, viewer domain.OrganizationContext, mailboxID, messageID uint, read bool) (*domain.MailMessage, error) {
	if _, err := s.authorizeMailbox(ctx, viewer, mailboxID, canView); err != nil {
		return nil, err
	}
	err := s.repo.InTransaction(ctx, func(tx context.Context) error {
		return s.repo.SetMessageRead(tx, mailboxID, messageID, viewer.MembershipID, read, s.now().UTC())
	})
	if err != nil {
		return nil, err
	}
	message, err := s.repo.FindMessage(ctx, mailboxID, messageID)
	if err != nil {
		return nil, err
	}
	if err := s.fillViewerState(ctx, viewer, message); err != nil {
		return nil, err
	}
	return message, nil
}

// SendMessage stores the message as queued, calls the provider, and records the real outcome.
// A provider failure keeps the message with status "failed" and returns ErrMailDeliveryFailed
// together with the stored message, so a caller never sees a failed send reported as sent.
// Access is checked before anything is validated, stored, or counted against the send limit.
func (s *service) SendMessage(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, input SendInput) (*domain.MailMessage, bool, error) {
	mailbox, err := s.authorizeMailbox(ctx, viewer, mailboxID, canSend)
	if err != nil {
		return nil, false, err
	}
	return s.send(ctx, mailbox, input, true)
}

func (s *service) SendFromAddress(ctx context.Context, viewer domain.OrganizationContext, from string, input SendInput) (*domain.MailMessage, bool, error) {
	address, ok := normalizeRecipient(from)
	if !ok {
		return nil, false, fmt.Errorf("%w: from", ErrMailMessageInvalid)
	}
	mailbox, err := s.repo.FindMailboxByAddress(ctx, address)
	if err != nil {
		return nil, false, err
	}
	// Another organization's address answers exactly like an address that is not a mailbox, and
	// so does a mailbox the key's owner has no access to.
	if mailbox.OrganizationID != viewer.OrganizationID {
		return nil, false, ErrMailboxNotFound
	}
	if err := s.checkAccess(ctx, viewer, mailbox, canSend, ErrMailboxNotFound); err != nil {
		return nil, false, err
	}
	return s.send(ctx, mailbox, input, true)
}

// GetOutboundMessage needs View on the message's mailbox; a message of a mailbox the viewer has
// no access to answers like a message that does not exist.
func (s *service) GetOutboundMessage(ctx context.Context, viewer domain.OrganizationContext, messageID uint) (*domain.MailMessage, error) {
	message, err := s.repo.FindOutboundMessageForOrganization(ctx, viewer.OrganizationID, messageID)
	if err != nil {
		return nil, err
	}
	mailbox, err := s.repo.FindMailbox(ctx, viewer.OrganizationID, message.MailboxID)
	if errors.Is(err, ErrMailboxNotFound) {
		return nil, ErrMailMessageNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := s.checkAccess(ctx, viewer, mailbox, canView, ErrMailMessageNotFound); err != nil {
		return nil, err
	}
	// An API key acts for no viewing person: it sees the shared labels but never a star.
	if err := s.fillLabels(ctx, message); err != nil {
		return nil, err
	}
	return s.withEvents(ctx, message)
}

// send stores and sends one outbound message. A direct send (queueAtLimit) counts in the personal
// lane and, when that lane is full, is stored as a waiting message instead, for the worker to send
// when the window has room. A broadcast recipient (not queueAtLimit) counts in the broadcast lane
// and gets ErrMailSendLimitReached there, which pauses the broadcast.
func (s *service) send(ctx context.Context, mailbox *domain.Mailbox, input SendInput, queueAtLimit bool) (*domain.MailMessage, bool, error) {
	var templateID uint
	if input.Template != nil {
		var err error
		if input, templateID, err = s.applyTemplate(ctx, mailbox.OrganizationID, input); err != nil {
			return nil, false, err
		}
	}
	message, err := s.buildOutbound(mailbox, input)
	if err != nil {
		return nil, false, err
	}
	message.TemplateID = templateID
	message.SendLane = domain.SendLanePersonal
	if !queueAtLimit {
		message.SendLane = domain.SendLaneBroadcast
	}
	if message.IdempotencyKey != "" {
		existing, findErr := s.repo.FindMessageByIdempotencyKey(ctx, mailbox.ID, message.IdempotencyKey)
		if findErr == nil {
			replayed, replayErr := replayOutbound(mailbox, existing, message.IdempotencyFingerprint)
			return replayed, false, replayErr
		}
		if !errors.Is(findErr, ErrMailMessageNotFound) {
			return nil, false, findErr
		}
	}

	if message.InReplyTo != "" {
		parent, findErr := s.repo.FindMessageByRFCMessageID(ctx, mailbox.ID, message.InReplyTo)
		switch {
		case findErr == nil:
			message.References = replyReferences(parent)
		case !errors.Is(findErr, ErrMailMessageNotFound):
			return nil, false, findErr
		}
	}

	sent, err := s.repo.CountOutboundSince(ctx, mailbox.OrganizationID, message.SendLane, s.now().Add(-sendLimitWindow))
	if err != nil {
		return nil, false, err
	}
	if sent >= int64(s.settings.laneLimit(message.SendLane)) {
		if !queueAtLimit {
			return nil, false, ErrMailSendLimitReached
		}
		return s.queueForSendLimit(ctx, mailbox, message)
	}
	if queueAtLimit {
		// Older messages still wait: a new one queues behind them instead of overtaking them.
		waiting, countErr := s.repo.CountWaitingSends(ctx, mailbox.OrganizationID)
		if countErr != nil {
			return nil, false, countErr
		}
		if waiting > 0 {
			return s.queueForSendLimit(ctx, mailbox, message)
		}
	}

	attemptedAt := s.now().UTC()
	message.SendAttemptedAt = &attemptedAt
	fingerprint := message.IdempotencyFingerprint
	created, err := s.repo.CreateMessage(ctx, message)
	if err != nil {
		return nil, false, err
	}
	if !created {
		// A concurrent request with the same key stored its message first.
		replayed, replayErr := replayOutbound(mailbox, message, fingerprint)
		return replayed, false, replayErr
	}

	files := make([]mailtransport.Attachment, 0, len(message.Attachments))
	for i := range message.Attachments {
		files = append(files, mailtransport.Attachment{
			Filename:    message.Attachments[i].Filename,
			ContentType: message.Attachments[i].ContentType,
			Content:     message.Attachments[i].Content,
		})
		// The stored copy is the record; the response carries metadata only.
		message.Attachments[i].Content = nil
	}
	return s.deliver(ctx, mailbox, message, files, input.Headers)
}

// deliver calls the provider for a stored message whose attempt has started and records the
// outcome, its delivery events, and the webhooks they publish. A direct send and the worker that
// sends waiting messages share it, so both record a send the same way.
func (s *service) deliver(
	ctx context.Context,
	mailbox *domain.Mailbox,
	message *domain.MailMessage,
	files []mailtransport.Attachment,
	headers map[string]string,
) (*domain.MailMessage, bool, error) {
	metrics.RecordSendLaneAttempt(string(message.SendLane))
	outcome, sendErr := s.transport.Send(ctx, mailtransport.Message{
		FromAddress: message.FromAddress,
		FromName:    message.FromName,
		To:          message.To,
		Cc:          message.Cc,
		Subject:     message.Subject,
		TextBody:    message.TextBody,
		HTMLBody:    message.HTMLBody,
		MessageID:   message.RFCMessageID,
		InReplyTo:   message.InReplyTo,
		References:  message.References,
		Attachments: files,
		Headers:     headers,
	})

	message.Status = domain.MailStatusSent
	message.MessageID = outcome.MessageID
	var result error
	if sendErr != nil {
		message.Status = domain.MailStatusFailed
		message.FailureCode, result = classifySendError(sendErr)
	}

	// Record the outcome even when the caller has gone away: a canceled request must not
	// leave a delivered message looking queued.
	recordContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), deliveryRecordTimeout)
	defer cancel()
	occurredAt := s.now().UTC()
	events := sendEvents(outcome, sendErr == nil, occurredAt)
	err := s.apply(recordContext, func(tx context.Context) ([]MailNotification, error) {
		if updateErr := s.repo.UpdateMessageDelivery(tx, message.ID, message.Status, message.MessageID, message.FailureCode); updateErr != nil {
			return nil, updateErr
		}
		if len(events) > 0 {
			_, summary, addErr := s.repo.AddMessageEvents(tx, message.ID, events)
			if addErr != nil {
				return nil, addErr
			}
			message.LastEvent = summary
			message.Events = events
		}
		return sendNotifications(mailbox, message, events, occurredAt), nil
	})
	if err != nil {
		return nil, false, err
	}
	return message, true, result
}

// queueForSendLimit stores a direct send as a waiting message. At most one day's personal lane
// limit of messages may wait per organization; one more is rejected and nothing is stored.
func (s *service) queueForSendLimit(ctx context.Context, mailbox *domain.Mailbox, message *domain.MailMessage) (*domain.MailMessage, bool, error) {
	waiting, err := s.repo.CountWaitingSends(ctx, mailbox.OrganizationID)
	if err != nil {
		return nil, false, err
	}
	if waiting >= int64(s.settings.laneLimit(domain.SendLanePersonal)) {
		return nil, false, ErrMailSendLimitReached
	}
	message.Status = domain.MailStatusQueued
	message.QueuedReason = domain.MailQueuedForSendLimit
	fingerprint := message.IdempotencyFingerprint
	created, err := s.repo.CreateMessage(ctx, message)
	if err != nil {
		return nil, false, err
	}
	if !created {
		replayed, replayErr := replayOutbound(mailbox, message, fingerprint)
		return replayed, false, replayErr
	}
	// The stored copy keeps the attachments for the worker; the response carries metadata only.
	for i := range message.Attachments {
		message.Attachments[i].Content = nil
	}
	return message, true, nil
}

// sendNotifications lists what a finished send tells webhook endpoints: whether the provider
// took the message, and anything it already said about a recipient.
func sendNotifications(mailbox *domain.Mailbox, message *domain.MailMessage, events []domain.MailEvent, at time.Time) []MailNotification {
	base := mailNotificationFor(mailbox, message)
	base.OccurredAt = at
	outcome := base
	outcome.Type = WebhookMailSent
	if message.Status == domain.MailStatusFailed {
		outcome.Type = WebhookMailFailed
		outcome.Reason = message.FailureCode
	}
	notifications := []MailNotification{outcome}
	for _, event := range events {
		eventType, publish := webhookTypeForEvent(event.Type)
		if !publish {
			continue
		}
		notification := base
		notification.Type = eventType
		notification.Recipient = event.Recipient
		notification.Reason = event.Detail
		notification.Key = "send:" + event.Recipient
		notifications = append(notifications, notification)
	}
	return notifications
}

// mailNotificationFor starts a notification about a message of the mailbox, carrying what the
// webhook guard needs to know about the mailbox.
func mailNotificationFor(mailbox *domain.Mailbox, message *domain.MailMessage) MailNotification {
	return MailNotification{
		OrganizationID:  mailbox.OrganizationID,
		MailboxAddress:  mailbox.Address,
		MailboxType:     mailbox.Type,
		WebhooksEnabled: mailbox.WebhooksEnabled,
		SealedBefore:    mailbox.SealedBefore,
		Message:         message,
	}
}

// sealedFor reports whether a message or broadcast created at createdAt is sealed in the mailbox.
func sealedFor(mailbox *domain.Mailbox, createdAt time.Time) bool {
	return mailbox.SealedBefore != nil && createdAt.Before(*mailbox.SealedBefore)
}

// sendEvents turns what the provider said during the send call into delivery events.
func sendEvents(outcome mailtransport.Result, accepted bool, at time.Time) []domain.MailEvent {
	var events []domain.MailEvent
	if accepted {
		events = append(events, domain.MailEvent{Type: domain.MailEventAccepted, OccurredAt: at})
	}
	add := func(recipients []string, eventType domain.MailEventType, detail string) {
		for _, recipient := range recipients {
			address, ok := normalizeRecipient(recipient)
			if !ok {
				continue
			}
			events = append(events, domain.MailEvent{Type: eventType, Recipient: address, Detail: detail, OccurredAt: at})
		}
	}
	add(outcome.Delivered, domain.MailEventDelivered, "")
	add(outcome.Bounced, domain.MailEventBounced, "permanent_failure")
	add(outcome.Suppressed, domain.MailEventRejected, "suppressed")
	return events
}

// replayOutbound answers a send whose idempotency key already names a message. The provider is
// not called again: the caller gets the first attempt's outcome, whatever it was. A sealed
// message is a conflict that reveals nothing of it.
func replayOutbound(mailbox *domain.Mailbox, existing *domain.MailMessage, fingerprint string) (*domain.MailMessage, error) {
	if sealedFor(mailbox, existing.CreatedAt) || existing.IdempotencyFingerprint != fingerprint {
		return nil, ErrMailIdempotencyConflict
	}
	if existing.QueuedReason != "" {
		// Still waiting for the send limit: the caller gets the same queued answer again.
		return existing, nil
	}
	switch existing.Status {
	case domain.MailStatusSent:
		return existing, nil
	case domain.MailStatusFailed:
		code := existing.FailureCode
		if code == failureNotConfigured {
			return existing, ErrMailSenderNotConfigured
		}
		return existing, fmt.Errorf("%w: %s", ErrMailDeliveryFailed, code)
	default:
		return nil, ErrMailSendInProgress
	}
}

func classifySendError(err error) (string, error) {
	if errors.Is(err, mailtransport.ErrNotConfigured) {
		return failureNotConfigured, ErrMailSenderNotConfigured
	}
	var transportErr *mailtransport.Error
	if errors.As(err, &transportErr) && transportErr.Code != "" {
		return transportErr.Code, fmt.Errorf("%w: %s", ErrMailDeliveryFailed, transportErr.Code)
	}
	return failureUnknown, ErrMailDeliveryFailed
}

func (s *service) buildOutbound(mailbox *domain.Mailbox, input SendInput) (*domain.MailMessage, error) {
	seen := map[string]struct{}{}
	to, ok := normalizeRecipients(input.To, seen)
	if !ok || len(to) == 0 {
		return nil, fmt.Errorf("%w: recipients", ErrMailMessageInvalid)
	}
	cc, ok := normalizeRecipients(input.Cc, seen)
	if !ok || len(to)+len(cc) > domain.MaxMailRecipients {
		return nil, fmt.Errorf("%w: recipients", ErrMailMessageInvalid)
	}

	subject := strings.TrimSpace(input.Subject)
	if subject == "" || len(subject) > domain.MaxMailSubjectLength || strings.ContainsAny(subject, "\r\n\x00") {
		return nil, fmt.Errorf("%w: subject", ErrMailMessageInvalid)
	}
	text, html := cleanText(input.Text), cleanText(input.HTML)
	if strings.TrimSpace(text) == "" && strings.TrimSpace(html) == "" {
		return nil, fmt.Errorf("%w: body", ErrMailMessageInvalid)
	}
	if len(text) > domain.MaxMailBodyBytes || len(html) > domain.MaxMailBodyBytes {
		return nil, fmt.Errorf("%w: body", ErrMailMessageInvalid)
	}
	inReplyTo, ok := normalizeMessageID(input.InReplyTo)
	if !ok {
		return nil, fmt.Errorf("%w: in_reply_to", ErrMailMessageInvalid)
	}

	attachments, err := buildAttachments(input.Attachments)
	if err != nil {
		return nil, err
	}

	idempotencyKey := strings.TrimSpace(input.IdempotencyKey)
	if !validIdempotencyKey(idempotencyKey) {
		return nil, ErrMailIdempotencyKeyInvalid
	}
	fingerprint := ""
	if idempotencyKey != "" {
		fingerprint = sendFingerprint(to, cc, cleanText(subject), text, html, inReplyTo, attachmentDigest(attachments))
	}

	// The id is chosen before the message is stored, so a replay or a queued send reuses it.
	rfcMessageID := newRFCMessageID(addressDomain(mailbox.Address))
	// A reply references at least its parent; send extends this with the parent's own chain.
	var references []string
	if inReplyTo != "" {
		references = []string{inReplyTo}
	}

	return &domain.MailMessage{
		IdempotencyKey:         idempotencyKey,
		IdempotencyFingerprint: fingerprint,
		MailboxID:              mailbox.ID,
		Direction:              domain.MailDirectionOutbound,
		Status:                 domain.MailStatusQueued,
		FromAddress:            mailbox.Address,
		FromName:               mailbox.DisplayName,
		To:                     to,
		Cc:                     cc,
		Subject:                cleanText(subject),
		TextBody:               text,
		HTMLBody:               html,
		InReplyTo:              inReplyTo,
		References:             references,
		RFCMessageID:           rfcMessageID,
		Attachments:            attachments,
		AttachmentCount:        len(attachments),
		// Outbound mail is read for everyone.
		Read: true,
	}, nil
}

// newRFCMessageID returns a new RFC 5322 Message-ID at the sending domain, without angle
// brackets: 32 lowercase hex characters from a cryptographic random source.
func newRFCMessageID(domainName string) string {
	var random [16]byte
	// crypto/rand.Read always fills the buffer and never returns an error.
	_, _ = rand.Read(random[:])
	return hex.EncodeToString(random[:]) + "@" + domainName
}

// addressDomain returns the domain part of a mail address.
func addressDomain(address string) string {
	return address[strings.LastIndex(address, "@")+1:]
}

// replyReferences is the References chain of a reply to parent: the parent's References followed
// by the parent's own Message-ID, keeping the newest maxMailReferences ids.
func replyReferences(parent *domain.MailMessage) []string {
	chain := make([]string, 0, len(parent.References)+1)
	for _, id := range parent.References {
		if id != parent.RFCMessageID {
			chain = append(chain, id)
		}
	}
	chain = append(chain, parent.RFCMessageID)
	if len(chain) > maxMailReferences {
		chain = chain[len(chain)-maxMailReferences:]
	}
	return chain
}

// ReceiveInbound verifies and stores one delivery from the mail edge. A repeated delivery of
// the same message to the same mailbox returns the stored message with created=false.
func (s *service) ReceiveInbound(ctx context.Context, delivery InboundDelivery) (*domain.MailMessage, bool, error) {
	if s.settings.InboundSecret == "" {
		return nil, false, ErrMailInboundDisabled
	}
	if len(delivery.Raw) == 0 {
		return nil, false, ErrMailInboundUnparseable
	}
	if len(delivery.Raw) > MaxInboundBytes {
		return nil, false, ErrMailInboundTooLarge
	}
	recipient := strings.ToLower(strings.TrimSpace(delivery.Recipient))
	// Verify before any lookup, so an unsigned caller cannot probe which addresses exist.
	if !verifyInbound(s.settings.InboundSecret, delivery.Signature, recipient, delivery.Raw, s.now(), s.settings.InboundTolerance) {
		return nil, false, ErrMailInboundSignature
	}

	mailbox, err := s.repo.FindMailboxByAddress(ctx, recipient)
	if err != nil {
		if errors.Is(err, ErrMailboxNotFound) {
			return nil, false, ErrMailRecipientNotFound
		}
		return nil, false, err
	}
	parsed, err := parseMail(delivery.Raw)
	if err != nil {
		return nil, false, err
	}
	if parsed.FromAddress == "" {
		return nil, false, ErrMailInboundUnparseable
	}

	to := parsed.To
	if len(to) == 0 {
		to = []string{recipient}
	}
	message := &domain.MailMessage{
		MailboxID:   mailbox.ID,
		Direction:   domain.MailDirectionInbound,
		Status:      domain.MailStatusReceived,
		FromAddress: parsed.FromAddress,
		FromName:    parsed.FromName,
		To:          to,
		Cc:          parsed.Cc,
		Subject:     parsed.Subject,
		TextBody:    parsed.TextBody,
		HTMLBody:    parsed.HTMLBody,
		MessageID:   parsed.MessageID,
		InReplyTo:   parsed.InReplyTo,
		References:  parsed.References,
		// Threading matches replies against the sender's Message-ID.
		RFCMessageID: parsed.MessageID,
		DedupeKey:    inboundDedupeKey(parsed.MessageID, delivery.Raw),
		Attachments:  parsed.Attachments,
	}
	created := false
	err = s.apply(ctx, func(tx context.Context) ([]MailNotification, error) {
		var createErr error
		if created, createErr = s.repo.CreateInboundMessage(tx, message); createErr != nil || !created {
			return nil, createErr
		}
		notification := mailNotificationFor(mailbox, message)
		notification.Type = WebhookMailReceived
		notification.OccurredAt = s.now().UTC()
		return []MailNotification{notification}, nil
	})
	if err != nil {
		return nil, false, err
	}
	return message, created, nil
}

// inboundDedupeKey identifies a message by its Message-ID, or by its content when the sender
// set none, so an edge retry never stores the same message twice.
func inboundDedupeKey(messageID string, raw []byte) string {
	var sum [sha256.Size]byte
	if messageID != "" {
		sum = sha256.Sum256([]byte("id:" + messageID))
	} else {
		sum = sha256.Sum256(raw)
	}
	return hex.EncodeToString(sum[:])
}

// validIdempotencyKey accepts an empty key or 1 to 255 printable ASCII characters.
func validIdempotencyKey(key string) bool {
	if len(key) > 255 {
		return false
	}
	for _, char := range key {
		if char < 0x21 || char > 0x7e {
			return false
		}
	}
	return true
}

// sendFingerprint hashes the normalized request with length prefixes, so different field
// boundaries can never produce the same digest.
func sendFingerprint(to, cc []string, subject, text, html, inReplyTo, attachments string) string {
	hash := sha256.New()
	write := func(value string) {
		fmt.Fprintf(hash, "%d:", len(value))
		hash.Write([]byte(value))
	}
	write(strings.Join(to, ","))
	write(strings.Join(cc, ","))
	write(subject)
	write(text)
	write(html)
	write(inReplyTo)
	// A request without attachments keeps the digest it had before attachments existed.
	if attachments != "" {
		write(attachments)
	}
	return hex.EncodeToString(hash.Sum(nil))
}
