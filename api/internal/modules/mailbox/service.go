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

// InboundDelivery is one signed delivery from the mail edge.
type InboundDelivery struct {
	ConnectionID string
	Recipient    string
	Signature    string
	Raw          []byte
}

// DeliveryEvent is one signed provider event forwarded by the mail edge.
type DeliveryEvent struct {
	Signature string
	Raw       []byte
}

// SendInput is a validated request to send one message.
type SendInput struct {
	To []string
	Cc []string
	// Bcc are blind copies: delivered, counted in the recipient limit with To and Cc, stored apart
	// from them, and never written into a header.
	Bcc       []string
	Subject   string
	Text      string
	HTML      string
	InReplyTo string
	// SentByMembershipID records the member who sends; SendMessage and SendFromAddress set it from
	// the viewer.
	SentByMembershipID uint
	// allowBlankBody lets a message without text or HTML through: a mail app may send only files.
	// Only the SMTP submission path sets it.
	allowBlankBody bool
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
	// ScheduledAt, when set, queues the message for future delivery instead of sending now.
	ScheduledAt *time.Time
}

// Service composes the segregated subdomain interfaces of the mailbox starter.
// Callers and handlers should depend on the narrow subdomain interface they need
// rather than this full facade.
type Service interface {
	CoreService
	DomainService
	BroadcastService
	TransactionalService
	ClientAccessService
	RosterService
	ExternalMailAllService
	TriageService
	AgentGrantService
	PublicMailboxService
}

type service struct {
	repo domain.MailboxRepository
	// memberships validates owners and members against the organization's current members.
	memberships membershipDirectory
	// inviter sends roster import invitations through the organization starter; nil when not wired.
	inviter domain.OrganizationRosterInviter
	// roster reserves and claims roster import mailboxes; nil when repo does not implement it.
	roster    domain.MailboxRosterRepository
	templates domain.MailTemplateRepository
	contacts  domain.MailContactRepository
	// broadcasts is the broadcast store; nil until NewService wires it.
	broadcasts domain.MailBroadcastRepository
	// organize stores folders, labels, placements, and stars; nil when a test does not need them.
	organize domain.MailOrganizeRepository
	// inbox reads the unified inbox; nil when repo does not implement it.
	inbox domain.MailInboxRepository
	// snooze wakes due snoozed inbox messages; nil when repo does not implement it.
	snooze domain.MailSnoozeRepository
	// roles stores mail roles; nil when repo does not implement it.
	roles domain.MailRoleRepository
	// directory reads the administration directory; nil when repo does not implement it.
	directory domain.MailboxDirectoryRepository
	// ledger stores the platform account ledger; nil when repo does not implement it.
	ledger domain.PlatformAccountRepository
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
	// notices tells the broadcast starter how it ended or stopped; nil sends no notices.
	notices broadcastNoticeSender
	// notifications publishes mail.received to the notification starter; nil publishes nothing.
	notifications domain.NotificationPublisher
	// mailEvents publishes domain mail events to downstream subscribers; nil publishes nothing.
	mailEvents domain.MailEventPublisher
	settings   Settings
	now           func() time.Time
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
	// triage stores AI triage rows and settings; nil when repo does not implement it.
	triage domain.MailTriageRepository
	// signatures stores members' signatures; nil when repo does not implement it.
	signatures domain.MailSignatureRepository
	// drafts stores members' drafts; nil when repo does not implement it.
	drafts domain.MailDraftRepository
	// assignments stores thread assignments; nil when repo does not implement it.
	assignments domain.MailThreadAssignmentRepository
	// notes stores internal thread notes; nil when repo does not implement it.
	notes domain.MailThreadNoteRepository
	// triageModel classifies and drafts; settings.Triage selects it.
	triageModel TriageModel
	// externalMail stores external mail connections; nil when repo does not implement it.
	externalMail domain.ExternalMailRepository
	// externalConnectors are the providers outside mailboxes can be bound with; settings.ExternalMail
	// selects them.
	externalConnectors *externalMailRegistry
	// externalCredentialLimits bound password bindings per member and per organization.
	externalCredentialLimits externalCredentialLimits
	// agents stores agent grants, their feed, audit, and drafts; nil when repo does not implement it.
	agents domain.MailAgentRepository
	// agentReads limits the agent route requests of each API key per minute.
	agentReads ratelimit.Store
	// pushStore keeps push subscriptions; nil when repo does not implement it.
	pushStore domain.ExternalMailPushRepository
	// pushLimits bounds each provider's authenticated notification requests per minute.
	pushLimits ratelimit.Store
	// pushRefusals bounds, per IP and in this process, notification requests that authenticate nothing;
	// pushHandshakes bounds validation handshakes apart from them.
	pushRefusals   ratelimit.Store
	pushHandshakes ratelimit.Store
	// releases keeps the provider calls revocations leave to do; nil when repo does not implement it.
	releases domain.ExternalMailReleaseRepository
	// afterStoredHook, when set, runs after each copy a sync stores, inside its save; tests set it.
	afterStoredHook func()
	// pushReconcile spaces the stop of subscriptions of ended connections.
	pushReconcile pushReconcileClock
	// pushRefusalLog samples the log lines of refused notification requests.
	pushRefusalLog *pushRefusalLog
	// appPasswords stores mail client passwords and the organization switch; nil when repo does not
	// implement it.
	appPasswords appPasswordStore
	// reverifier confirms the account credentials before a mail client password is created; nil
	// refuses creating one.
	reverifier domain.CredentialReverifier
	// appPasswordAttempts bounds the credential checks of creating mail client passwords per user.
	appPasswordAttempts ratelimit.Store
	// welcome stores the welcome mail setting and the record of welcomed mailboxes; nil when repo
	// does not implement it, which sends no welcome mails.
	welcome memberWelcomeStore
	// publicMailboxes tracks per-user global public-domain mailbox claims; nil when repo does not
	// implement it.
	publicMailboxes publicMailboxStore
	// publicLimits bounds public mailbox availability checks, claims, and shared-domain send bursts.
	publicLimits publicMailboxLimits
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
	notices broadcastNoticeSender,
) *service {
	var inbox domain.MailInboxRepository
	if seam, ok := repo.(domain.MailInboxRepository); ok {
		inbox = seam
	}
	var snooze domain.MailSnoozeRepository
	if seam, ok := repo.(domain.MailSnoozeRepository); ok {
		snooze = seam
	}
	var roles domain.MailRoleRepository
	if seam, ok := repo.(domain.MailRoleRepository); ok {
		roles = seam
	}
	var directory domain.MailboxDirectoryRepository
	if seam, ok := repo.(domain.MailboxDirectoryRepository); ok {
		directory = seam
	}
	var ledger domain.PlatformAccountRepository
	if seam, ok := repo.(domain.PlatformAccountRepository); ok {
		ledger = seam
	}
	var triage domain.MailTriageRepository
	if seam, ok := repo.(domain.MailTriageRepository); ok {
		triage = seam
	}
	var signatures domain.MailSignatureRepository
	if seam, ok := repo.(domain.MailSignatureRepository); ok {
		signatures = seam
	}
	var drafts domain.MailDraftRepository
	if seam, ok := repo.(domain.MailDraftRepository); ok {
		drafts = seam
	}
	var assignments domain.MailThreadAssignmentRepository
	if seam, ok := repo.(domain.MailThreadAssignmentRepository); ok {
		assignments = seam
	}
	var notes domain.MailThreadNoteRepository
	if seam, ok := repo.(domain.MailThreadNoteRepository); ok {
		notes = seam
	}
	var externalMail domain.ExternalMailRepository
	if seam, ok := repo.(domain.ExternalMailRepository); ok {
		externalMail = seam
	}
	var agents domain.MailAgentRepository
	if seam, ok := repo.(domain.MailAgentRepository); ok {
		agents = seam
	}
	var releases domain.ExternalMailReleaseRepository
	if seam, ok := repo.(domain.ExternalMailReleaseRepository); ok {
		releases = seam
	}
	var pushStore domain.ExternalMailPushRepository
	if seam, ok := repo.(domain.ExternalMailPushRepository); ok {
		pushStore = seam
	}
	var roster domain.MailboxRosterRepository
	if seam, ok := repo.(domain.MailboxRosterRepository); ok {
		roster = seam
	}
	var appPasswords appPasswordStore
	if seam, ok := repo.(appPasswordStore); ok {
		appPasswords = seam
	}
	var welcome memberWelcomeStore
	if seam, ok := repo.(memberWelcomeStore); ok {
		welcome = seam
	}
	var publicMailboxes publicMailboxStore
	if seam, ok := repo.(publicMailboxStore); ok {
		publicMailboxes = seam
	}
	var encryptor *crypto.AESEncryptor
	var fingerprintKey []byte
	if settings.SecretEncryptionKey != "" {
		encryptor = crypto.NewAESEncryptorFromString(settings.SecretEncryptionKey)
		fingerprintKey = transactionalFingerprintKey(settings.SecretEncryptionKey)
	}
	return &service{
		notices:       notices,
		transactional: transactional, accounts: accounts, transactionalLimits: newTransactionalLimits(limits, settings),
		encryptor: encryptor, fingerprintKey: fingerprintKey,
		repo: repo, inbox: inbox, snooze: snooze, roles: roles, directory: directory, ledger: ledger, memberships: memberships, templates: templates, contacts: contacts, broadcasts: broadcasts,
		organize: organize, domains: domains, zones: zones,
		domainProvider: domainProvider, transport: transport, notifier: notifier, settings: settings, now: time.Now,
		background: func(run func()) { go run() },
		mxResolver: net.DefaultResolver, nsResolver: net.DefaultResolver,
		recordAudit: auditstarter.RecordChange,
		triage:      triage, triageModel: newTriageModel(settings.Triage), signatures: signatures, drafts: drafts,
		assignments:  assignments,
		notes:        notes,
		externalMail: externalMail, externalConnectors: newExternalMailRegistry(settings.ExternalMail),
		externalCredentialLimits: newExternalMailCredentialLimits(limits),
		agents:                   agents, agentReads: newAgentReadLimit(limits, settings),
		pushStore:    pushStore,
		pushLimits:   newExternalMailPushLimit(limits, settings.ExternalMail.Push.RequestsPerMinute()),
		pushRefusals: newExternalMailPushRefusals(), pushHandshakes: newExternalMailPushHandshakes(),
		pushRefusalLog: &pushRefusalLog{}, releases: releases,
		roster:          roster,
		appPasswords:    appPasswords, appPasswordAttempts: newAppPasswordAttempts(limits),
		welcome:         welcome,
		publicMailboxes: publicMailboxes,
		publicLimits:    newPublicMailboxLimits(limits),
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

func (s *service) Domain() string {
	return s.settings.Domain
}

func (s *service) Provider() string {
	return s.settings.Provider
}

// MaxMailboxes is the configured per-organization mailbox limit.
func (s *service) MaxMailboxes() int {
	if s.settings.MaxMailboxes > 0 {
		return s.settings.MaxMailboxes
	}
	return config.DefaultMailboxMaxPerOrganization
}

// Now reads the service clock.
func (s *service) Now() time.Time { return s.now() }

func (s *service) DailySendLimit() int {
	return s.settings.laneLimit(domain.SendLanePersonal)
}

func (s *service) DailyBroadcastLimit() int {
	return s.settings.laneLimit(domain.SendLaneBroadcast)
}

func (s *service) DailyTransactionalLimit() int {
	return s.settings.laneLimit(domain.SendLaneTransactional)
}

// DailyLimits holds an organization's limit in each send lane.
type DailyLimits struct {
	Personal      int
	Broadcast     int
	Transactional int
}

func (s *service) OrganizationDailyLimits(ctx context.Context, organizationID uint) (DailyLimits, error) {
	var limits DailyLimits
	for lane, target := range map[domain.SendLane]*int{
		domain.SendLanePersonal:      &limits.Personal,
		domain.SendLaneBroadcast:     &limits.Broadcast,
		domain.SendLaneTransactional: &limits.Transactional,
	} {
		limit, err := s.organizationLaneLimit(ctx, organizationID, lane)
		if err != nil {
			return DailyLimits{}, err
		}
		*target = limit
	}
	return limits, nil
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

// organizationLaneLimit is laneLimit for one organization: a suspended organization has no room,
// an operator's limit replaces the instance limits, and while the organization is younger than the
// new-organization period no lane allows more than the new-organization cap.
func (s *service) organizationLaneLimit(ctx context.Context, organizationID uint, lane domain.SendLane) (int, error) {
	policy, err := s.repo.OrganizationSendPolicy(ctx, organizationID)
	if err != nil {
		return 0, err
	}
	switch {
	case policy.Suspended:
		return 0, nil
	case policy.DailySendLimit != nil:
		return *policy.DailySendLimit, nil
	}
	limit := s.settings.laneLimit(lane)
	capped := s.settings.NewOrganizationDailySendLimit
	if capped <= 0 || limit <= capped {
		return limit, nil
	}
	if !policy.CreatedAt.IsZero() && s.now().Sub(policy.CreatedAt) < s.settings.NewOrganizationPeriod {
		return capped, nil
	}
	return limit, nil
}

// checkSendingHealth returns ErrMailSendingPaused while the organization's personal sends of the
// rolling window bounced or drew complaints at the rates a broadcast pauses at. The pause lifts by
// itself once those sends leave the window.
func (s *service) checkSendingHealth(ctx context.Context, organizationID uint) error {
	attempted, bounced, complained, err := s.repo.CountPersonalOutcomesSince(ctx, organizationID, s.now().Add(-sendLimitWindow))
	if err != nil || attempted < minHealthSample {
		return err
	}
	if bounced*1000 < attempted*maxBouncePerMille && complained*1000 < attempted*maxComplaintPerMille {
		return nil
	}
	slog.WarnContext(ctx, "mailbox personal sending paused by its health check",
		slog.Uint64("organization_id", uint64(organizationID)), slog.Int64("attempted", attempted),
		slog.Int64("bounced", bounced), slog.Int64("complained", complained))
	return ErrMailSendingPaused
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

// MailboxListScope selects which mailboxes ListMailboxes returns.
type MailboxListScope string

const (
	// MailboxListScopeAll lists every mailbox the viewer has any access to.
	MailboxListScopeAll MailboxListScope = ""
	// MailboxListScopeUsable lists the mailboxes the viewer can read or send from, plus, for owners
	// and admins, personal mailboxes awaiting handover.
	MailboxListScopeUsable MailboxListScope = "usable"
)

func (s *service) ListMailboxes(ctx context.Context, viewer domain.OrganizationContext, scope MailboxListScope) ([]*domain.Mailbox, error) {
	var mailboxes []*domain.Mailbox
	var err error
	switch scope {
	case MailboxListScopeAll:
		mailboxes, err = s.visibleMailboxes(ctx, viewer)
	case MailboxListScopeUsable:
		mailboxes, err = s.usableMailboxes(ctx, viewer)
	default:
		return nil, fmt.Errorf("%w: unknown mailbox list scope", domain.ErrInvalidInput)
	}
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
	if mailbox.TenantAssigned() && mailbox.AwaitingHandover() {
		// A departed member's mailbox at an organization address awaits its handover or the member's
		// return; deleting it would lose their copies.
		return fmt.Errorf("%w: the mailbox awaits its handover", ErrMailboxAccessConflict)
	}
	if mailbox.Connected() {
		// No stored grant outlives its mailbox: the connection is revoked and its token erased in the
		// transaction that deletes the mailbox, and handed back once it committed.
		return s.deleteConnectedMailbox(ctx, viewer, mailbox)
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
	input.SentByMembershipID = viewer.MembershipID
	return s.send(ctx, mailbox, input, true)
}

func (s *service) SendFromAddress(ctx context.Context, viewer domain.OrganizationContext, from string, input SendInput) (*domain.MailMessage, bool, error) {
	address, ok := normalizeRecipient(from)
	if !ok {
		return nil, false, fmt.Errorf("%w: from", ErrMailMessageInvalid)
	}
	if err := requireAPIKeySendingDomain(ctx, address); err != nil {
		return nil, false, err
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
	if s.enforcePublicDomainGuardrails(mailbox) {
		return nil, false, fmt.Errorf("%w: programmatic sending requires a verified custom domain", ErrMailDomainNotVerified)
	}
	input.SentByMembershipID = viewer.MembershipID
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
	// An API key acts for no viewing person: it sees the shared labels but never a star or a
	// blind copy.
	hideBlindCopies(0, message)
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
	// A connected mailbox sends only through its account's own SMTP server, verified at binding.
	if mailbox.Connected() && (!mailbox.ConnectedSend || mailbox.TenantAssigned()) {
		return nil, false, domain.ErrPermissionDenied
	}
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

	if queueAtLimit {
		if healthErr := s.checkSendingHealth(ctx, mailbox.OrganizationID); healthErr != nil {
			return nil, false, healthErr
		}
	}
	if message.ScheduledAt != nil {
		return s.queueForSchedule(ctx, mailbox, message)
	}
	sent, err := s.repo.CountOutboundSince(ctx, mailbox.OrganizationID, message.SendLane, s.now().Add(-sendLimitWindow))
	if err != nil {
		return nil, false, err
	}
	laneLimit, err := s.organizationLaneLimit(ctx, mailbox.OrganizationID, message.SendLane)
	if err != nil {
		return nil, false, err
	}
	laneLimit = s.effectiveSendLimit(mailbox, laneLimit)
	if sent >= int64(laneLimit) {
		if !queueAtLimit || s.enforcePublicDomainGuardrails(mailbox) {
			return nil, false, ErrMailSendLimitReached
		}
		return s.queueForSendLimit(ctx, mailbox, message)
	}
	if burstErr := s.checkPublicSendBurst(ctx, mailbox); burstErr != nil {
		return nil, false, burstErr
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
			ContentID:   message.Attachments[i].ContentID,
			Inline:      message.Attachments[i].Inline,
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
	send := s.sendWithConnection
	if mailbox.Connected() {
		send = func(ctx context.Context, _ uint, mail mailtransport.Message) (mailtransport.Result, error) {
			return s.sendThroughAccount(ctx, mailbox, mail)
		}
	}
	outcome, sendErr := send(ctx, mailbox.OrganizationID, mailtransport.Message{
		FromAddress: message.FromAddress,
		FromName:    message.FromName,
		To:          message.To,
		Cc:          message.Cc,
		Bcc:         message.Bcc,
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
	if outcome.RFCMessageID != "" {
		message.RFCMessageID = outcome.RFCMessageID
	}
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
		if updateErr := s.repo.UpdateMessageDelivery(tx, message.ID, message.Status, message.MessageID, message.RFCMessageID, message.FailureCode); updateErr != nil {
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
	laneLimit, err := s.organizationLaneLimit(ctx, mailbox.OrganizationID, domain.SendLanePersonal)
	if err != nil {
		return nil, false, err
	}
	if waiting >= int64(laneLimit) {
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
	if !ok {
		return nil, fmt.Errorf("%w: recipients", ErrMailMessageInvalid)
	}
	// A blind copy that is also in to or cc is already delivered there.
	bcc, ok := normalizeRecipients(input.Bcc, seen)
	if !ok || len(to)+len(cc)+len(bcc) > s.effectiveMaxRecipients(mailbox) {
		return nil, fmt.Errorf("%w: recipients", ErrMailMessageInvalid)
	}

	subject := strings.TrimSpace(input.Subject)
	if subject == "" || utf8.RuneCountInString(subject) > domain.MaxMailSubjectLength || strings.ContainsAny(subject, "\r\n\x00") {
		return nil, fmt.Errorf("%w: subject", ErrMailMessageInvalid)
	}
	text, html := cleanText(input.Text), cleanText(input.HTML)
	if strings.TrimSpace(text) == "" && strings.TrimSpace(html) == "" && !input.allowBlankBody {
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
	scheduledAt, err := validateScheduledAt(s.now().UTC(), input.ScheduledAt)
	if err != nil {
		return nil, err
	}

	idempotencyKey := strings.TrimSpace(input.IdempotencyKey)
	if !validIdempotencyKey(idempotencyKey) {
		return nil, ErrMailIdempotencyKeyInvalid
	}
	fingerprint := ""
	if idempotencyKey != "" {
		fingerprint = sendFingerprintWith(to, cc, bcc, cleanText(subject), text, html, inReplyTo, attachmentDigest(attachments))
		if scheduledAt != nil {
			fingerprint = sendFingerprintWith(to, cc, bcc, cleanText(subject), text, html, inReplyTo,
				attachmentDigest(attachments)+"|scheduled_at:"+scheduledAt.Format(time.RFC3339Nano))
		}
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
		ScheduledAt:            scheduledAt,
		FromAddress:            mailbox.Address,
		FromName:               mailbox.DisplayName,
		To:                     to,
		Cc:                     cc,
		Bcc:                    bcc,
		SentByMembershipID:     input.SentByMembershipID,
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
	secret := s.settings.InboundSecret
	var conn *CloudflareConnection
	if delivery.ConnectionID != "" {
		var err error
		conn, err = s.connection(ctx, 0, delivery.ConnectionID)
		if err != nil || conn == nil {
			return nil, false, ErrMailInboundSignature
		}
		secret, err = s.encryptor.DecryptString(conn.SigningCipher)
		if err != nil {
			return nil, false, ErrMailInboundSignature
		}
	}
	if secret == "" {
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
	if !verifyInbound(secret, delivery.Signature, recipient, delivery.Raw, s.now(), s.settings.InboundTolerance) {
		return nil, false, ErrMailInboundSignature
	}

	mailbox, err := s.repo.FindMailboxByAddress(ctx, recipient)
	if err != nil {
		if errors.Is(err, ErrMailboxNotFound) {
			return nil, false, ErrMailRecipientNotFound
		}
		return nil, false, err
	}
	if s.domains != nil {
		_, name, _ := strings.Cut(recipient, "@")
		d, lookupErr := s.domains.FindDomainByName(ctx, name)
		if lookupErr != nil && !errors.Is(lookupErr, ErrMailDomainNotFound) {
			return nil, false, lookupErr
		}
		if conn != nil && (d == nil || d.CloudflareConnectionID == nil || *d.CloudflareConnectionID != conn.ID || !receives(d) || mailbox.OrganizationID != conn.OrganizationID || d.OrganizationID != conn.OrganizationID) {
			return nil, false, ErrMailInboundSignature
		}
		if conn == nil && d != nil && d.CloudflareConnectionID != nil {
			return nil, false, ErrMailInboundSignature
		}
	} else if conn != nil {
		return nil, false, ErrMailInboundSignature
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
		RawSource:    delivery.Raw,
	}
	created := false
	err = s.apply(ctx, func(tx context.Context) ([]MailNotification, error) {
		var createErr error
		if created, createErr = s.repo.CreateInboundMessage(tx, message); createErr != nil || !created {
			return nil, createErr
		}
		return s.afterInboundStored(tx, mailbox, message)
	})
	if err != nil {
		return nil, false, err
	}
	if created {
		s.publishMailReceived(ctx, mailbox, message)
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
	return sendFingerprintWith(to, cc, nil, subject, text, html, inReplyTo, attachments)
}

// sendFingerprintWith is sendFingerprint with blind copies. Without them the digest is exactly the
// one sendFingerprint gave before blind copies existed, so stored idempotency keys still replay.
func sendFingerprintWith(to, cc, bcc []string, subject, text, html, inReplyTo, attachments string) string {
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
	if len(bcc) > 0 {
		write("bcc:" + strings.Join(bcc, ","))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// afterInboundStored is the one post-store step of every newly stored inbound message, whatever
// its source: Zmail delivery or a synced outside account. It runs in the storing transaction, so
// a message is queued for triage and announced exactly when it is stored. Consumers of new mail
// (triage, webhooks, and agent grants) attach here and nowhere else.
func (s *service) afterInboundStored(tx context.Context, mailbox *domain.Mailbox, message *domain.MailMessage) ([]MailNotification, error) {
	if err := s.enqueueTriage(tx, message); err != nil {
		return nil, err
	}
	if err := s.appendAgentEvents(tx, mailbox, message); err != nil {
		return nil, err
	}
	notification := mailNotificationFor(mailbox, message)
	notification.Type = WebhookMailReceived
	notification.OccurredAt = s.now().UTC()
	return []MailNotification{notification}, nil
}
