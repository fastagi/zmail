package domain

import (
	"context"
	"time"
)

// Mailbox limits shared by the service, persistence, and the HTTP contract.
const (
	// MaxMailboxesPerOrganization bounds how many addresses one organization may own.
	MaxMailboxesPerOrganization = 50
	// MaxMailRecipients matches the provider limit across to and cc.
	MaxMailRecipients = 50
	// MaxMailSubjectLength is the RFC 5322 line limit for a subject.
	MaxMailSubjectLength = 998
	// MaxMailBodyBytes bounds each stored body representation.
	MaxMailBodyBytes = 256 * 1024
	// MaxMailAttachments bounds the attachments of one message.
	MaxMailAttachments = 10
	// MaxMailAttachmentBytes bounds the decoded size of all attachments of one message. It is
	// larger than what a maximum-size inbound message can carry, so inbound attachments are
	// never cut by size.
	MaxMailAttachmentBytes = 4 * 1024 * 1024
	// MaxMailAttachmentNameLength bounds a stored file name.
	MaxMailAttachmentNameLength = 255
	// MaxMailTemplatesPerOrganization bounds how many templates one organization keeps.
	MaxMailTemplatesPerOrganization = 200
	// MaxMailContactsPerOrganization bounds how many contacts one organization keeps.
	MaxMailContactsPerOrganization = 100_000
	// MaxMailContactListsPerOrganization bounds how many lists one organization keeps.
	MaxMailContactListsPerOrganization = 100
	// MaxMailContactProperties bounds the custom properties of one contact.
	MaxMailContactProperties = 50
)

// MailDirection tells whether a message arrived at or left a mailbox.
type MailDirection string

const (
	MailDirectionInbound  MailDirection = "inbound"
	MailDirectionOutbound MailDirection = "outbound"
)

// Valid reports whether the direction is a known value.
func (d MailDirection) Valid() bool {
	return d == MailDirectionInbound || d == MailDirectionOutbound
}

// MailStatus is the delivery state of one message.
type MailStatus string

const (
	// MailStatusReceived marks an inbound message accepted from the mail edge.
	MailStatusReceived MailStatus = "received"
	// MailStatusQueued marks an outbound message whose provider call has not finished.
	MailStatusQueued MailStatus = "queued"
	// MailStatusSent marks an outbound message the provider accepted.
	MailStatusSent MailStatus = "sent"
	// MailStatusFailed marks an outbound message the provider rejected or could not take.
	MailStatusFailed MailStatus = "failed"
)

// Valid reports whether the status is a known value.
func (s MailStatus) Valid() bool {
	switch s {
	case MailStatusReceived, MailStatusQueued, MailStatusSent, MailStatusFailed:
		return true
	default:
		return false
	}
}

// MailEventType is one step in the delivery of an outbound message, as the provider reported it.
type MailEventType string

const (
	// MailEventAccepted: the provider took the message for delivery.
	MailEventAccepted MailEventType = "accepted"
	// MailEventDelivered: the recipient's mail server accepted the message.
	MailEventDelivered MailEventType = "delivered"
	// MailEventDeferred: a temporary failure; the provider is still retrying.
	MailEventDeferred MailEventType = "deferred"
	// MailEventBounced: the recipient's mail server refused the message for good.
	MailEventBounced MailEventType = "bounced"
	// MailEventFailed: the provider gave up without a bounce.
	MailEventFailed MailEventType = "failed"
	// MailEventRejected: the provider did not attempt the recipient, for example a suppressed one.
	MailEventRejected MailEventType = "rejected"
	// MailEventComplained: the recipient reported the message as spam.
	MailEventComplained MailEventType = "complained"
)

// severity orders event types by how much attention they need; the summary of a message is
// the most severe current state among its recipients.
func (t MailEventType) severity() int {
	switch t {
	case MailEventComplained:
		return 6
	case MailEventBounced:
		return 5
	case MailEventFailed:
		return 4
	case MailEventRejected:
		return 3
	case MailEventDeferred:
		return 2
	case MailEventDelivered:
		return 1
	default:
		return 0
	}
}

// Valid reports whether the type is a known value.
func (t MailEventType) Valid() bool {
	return t == MailEventAccepted || t.severity() > 0
}

// MailEvent is one recorded delivery step of a message.
type MailEvent struct {
	ID        uint
	MessageID uint
	Type      MailEventType
	// Recipient is the address the event is about; empty for a message-level event.
	Recipient string
	// Detail is a short machine-readable reason such as "5.1.1 permanent_failure"; never free text.
	Detail string
	// ProviderEventID makes a provider event idempotent. Empty for events recorded at send time.
	ProviderEventID string
	OccurredAt      time.Time
}

// SummarizeMailEvents returns the state a listing shows for a message: the most severe current
// state among its recipients, where a recipient's current state is its latest event. With no
// recipient events it is "accepted" when the provider took the message, and empty otherwise.
func SummarizeMailEvents(events []MailEvent) MailEventType {
	latest := map[string]MailEvent{}
	accepted := false
	for _, event := range events {
		if event.Recipient == "" {
			accepted = accepted || event.Type == MailEventAccepted
			continue
		}
		current, seen := latest[event.Recipient]
		if !seen || event.OccurredAt.After(current.OccurredAt) ||
			(event.OccurredAt.Equal(current.OccurredAt) && event.ID >= current.ID) {
			latest[event.Recipient] = event
		}
	}
	var summary MailEventType
	for _, event := range latest {
		if summary == "" || event.Type.severity() > summary.severity() {
			summary = event.Type
		}
	}
	if summary == "" && accepted {
		return MailEventAccepted
	}
	return summary
}

// Mailbox is one address an organization sends from and receives at. Who may read and send its
// mail follows from its type: a personal mailbox's owner, or a shared mailbox's members. See
// MailboxAccessFor.
type Mailbox struct {
	ID             uint
	OrganizationID uint
	// CreatedBy is the user who created the mailbox. It grants nothing.
	CreatedBy   uint
	Address     string
	DisplayName string
	Type        MailboxType
	// OwnerUserID is the owner of a personal mailbox; nil for a shared one, or when the owner's
	// account was deleted.
	OwnerUserID *uint
	// OwnerLeftAt is when the owner of a personal mailbox left the organization; nil while the
	// owner is a member. The mailbox then awaits handover.
	OwnerLeftAt *time.Time
	// SealedBefore hides every message created before it, on every surface; nil when never sealed.
	SealedBefore *time.Time
	// WebhooksEnabled publishes the mailbox's mail to organization webhooks; only shared mailboxes.
	WebhooksEnabled bool
	// Access is what the requesting viewer may do; only filled for a viewer.
	Access MailboxAccess
	// RoutingRuleID names the provider rule that delivers mail for an address on an organization
	// domain. It is empty on the shared domain, whose routing the operator manages.
	RoutingRuleID string
	// UnreadCount is the number of unread inbound messages. Only listings fill it, and only for a
	// viewer who may read the mailbox.
	UnreadCount int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// MailMessage is one stored inbound or outbound message.
type MailMessage struct {
	ID          uint
	MailboxID   uint
	Direction   MailDirection
	Status      MailStatus
	FromAddress string
	FromName    string
	To          []string
	Cc          []string
	Subject     string
	TextBody    string
	HTMLBody    string
	// MessageID is the RFC 5322 Message-ID without angle brackets. Inbound messages carry the
	// sender's value; outbound messages get one only when the provider reports it.
	MessageID string
	InReplyTo string
	// DedupeKey makes inbound delivery idempotent for one mailbox. Empty for outbound.
	DedupeKey   string
	FailureCode string
	// IdempotencyKey names an outbound message chosen by the sender; a later send with the same
	// key in the same mailbox finds this message instead of sending again. Empty when unused.
	IdempotencyKey string
	// IdempotencyFingerprint is a hash of the send request, so a reused key with a different
	// request is refused instead of answered with an unrelated message.
	IdempotencyFingerprint string
	// Read is whether the viewer has read the message. Read state is per person; outbound
	// messages are always read.
	Read bool
	// MailboxAddress is the address of the owning mailbox. Only the cross-mailbox log and the
	// provider-id lookup fill it.
	MailboxAddress string
	// OrganizationID is the owner of the mailbox; zero for an unowned mailbox. Only the
	// provider-id lookup fills it.
	OrganizationID uint
	// LastEvent summarizes the delivery events of an outbound message; see SummarizeMailEvents.
	LastEvent MailEventType
	// Events is the delivery history, oldest first. Only the detail lookup fills it.
	Events []MailEvent
	// TemplateID names the template the message was rendered from; zero when none was used.
	TemplateID uint
	// AttachmentCount is how many attachments the message has.
	AttachmentCount int
	// Attachments carries content when a message is being stored and metadata only when the
	// detail lookup fills it.
	Attachments []MailAttachment
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// MailAttachment is one file attached to a message.
type MailAttachment struct {
	ID        uint
	MessageID uint
	// Filename is a bare file name without any path.
	Filename string
	// ContentType is the media type the sender declared; it is never trusted for rendering.
	ContentType string
	Size        int
	// Content is empty when only metadata was loaded.
	Content []byte
}

// MailTemplate is reusable message content with variables, owned by an organization.
type MailTemplate struct {
	ID             uint
	OrganizationID uint
	Name           string
	Subject        string
	HTMLBody       string
	TextBody       string
	// SampleVariables are example values used by previews and test sends.
	SampleVariables map[string]string
	// Design is the visual editor's JSON document the bodies were rendered from; empty when the
	// template is written in HTML. The API stores it as given and never renders it.
	Design    string
	CreatedBy uint
	CreatedAt time.Time
	UpdatedAt time.Time
}

// MailContactStatus says whether a contact may receive broadcasts.
type MailContactStatus string

const (
	// MailContactSubscribed contacts receive broadcasts.
	MailContactSubscribed MailContactStatus = "subscribed"
	// MailContactUnsubscribed contacts asked not to receive broadcasts.
	MailContactUnsubscribed MailContactStatus = "unsubscribed"
	// MailContactSuppressed contacts bounced permanently or complained; Zmail set the status.
	MailContactSuppressed MailContactStatus = "suppressed"
)

// Valid reports whether the status is a known value.
func (s MailContactStatus) Valid() bool {
	return s == MailContactSubscribed || s == MailContactUnsubscribed || s == MailContactSuppressed
}

// MailContact is one address an organization sends broadcasts to.
type MailContact struct {
	ID             uint
	OrganizationID uint
	Email          string
	FirstName      string
	LastName       string
	// Properties are custom string fields; their names follow the template variable rules.
	Properties map[string]string
	Status     MailContactStatus
	// StatusReason is a short machine-readable reason for unsubscribed or suppressed.
	StatusReason string
	// ListIDs are the lists the contact belongs to, ascending.
	ListIDs   []uint
	CreatedAt time.Time
	UpdatedAt time.Time
}

// MailContactList groups contacts of one organization.
type MailContactList struct {
	ID             uint
	OrganizationID uint
	Name           string
	ContactCount   int
	CreatedAt      time.Time
}

// MailContactFilter narrows a contact listing.
type MailContactFilter struct {
	Query   string
	ListID  uint
	Status  MailContactStatus
	Page    int
	PerPage int
}

// MailContactImportRow is one valid row of an import.
type MailContactImportRow struct {
	Email      string
	FirstName  string
	LastName   string
	Properties map[string]string
}

// MailContactImportCounts says what an import changed.
type MailContactImportCounts struct {
	Created int
	Updated int
}

// MailContactRepository stores contacts and lists. Every call is scoped to an organization.
type MailContactRepository interface {
	ListContacts(ctx context.Context, organizationID uint, filter MailContactFilter) ([]*MailContact, int64, error)
	FindContact(ctx context.Context, organizationID, id uint) (*MailContact, error)
	// CreateContact stores a contact with its list memberships unless the organization has limit contacts.
	CreateContact(ctx context.Context, contact *MailContact, limit int) error
	// UpdateContact replaces the names, properties, status, and list memberships of a contact.
	UpdateContact(ctx context.Context, contact *MailContact) error
	DeleteContact(ctx context.Context, organizationID, id uint) error
	// ImportContacts creates or updates contacts by address in one transaction and adds them to
	// listID when it is not zero. It never changes the status of an existing contact.
	ImportContacts(ctx context.Context, organizationID uint, rows []MailContactImportRow, listID uint, limit int) (MailContactImportCounts, error)

	ListContactLists(ctx context.Context, organizationID uint) ([]*MailContactList, error)
	CreateContactList(ctx context.Context, list *MailContactList, limit int) error
	RenameContactList(ctx context.Context, organizationID, id uint, name string) (*MailContactList, error)
	DeleteContactList(ctx context.Context, organizationID, id uint) error
}

// MailBroadcastStatus is the state of a broadcast.
type MailBroadcastStatus string

const (
	// MailBroadcastDraft broadcasts can be edited and have no recipients yet.
	MailBroadcastDraft MailBroadcastStatus = "draft"
	// MailBroadcastScheduled broadcasts have their recipients and wait for scheduled_at.
	MailBroadcastScheduled MailBroadcastStatus = "scheduled"
	// MailBroadcastSending broadcasts are being sent by the worker.
	MailBroadcastSending MailBroadcastStatus = "sending"
	// MailBroadcastPaused broadcasts keep their pending recipients but send nothing: the test wave
	// is waiting, the health check stopped them, or an owner paused them. StopReason says which.
	MailBroadcastPaused MailBroadcastStatus = "paused"
	// MailBroadcastSent broadcasts have no pending recipient left.
	MailBroadcastSent MailBroadcastStatus = "sent"
	// MailBroadcastCanceled broadcasts were stopped; recipients not yet sent were not sent.
	MailBroadcastCanceled MailBroadcastStatus = "canceled"
)

// MailRecipientStatus is the state of one recipient of a broadcast.
type MailRecipientStatus string

const (
	MailRecipientPending  MailRecipientStatus = "pending"
	MailRecipientSent     MailRecipientStatus = "sent"
	MailRecipientFailed   MailRecipientStatus = "failed"
	MailRecipientSkipped  MailRecipientStatus = "skipped"
	MailRecipientCanceled MailRecipientStatus = "canceled"
)

// MailBroadcastCounts counts the recipients of a broadcast by status.
type MailBroadcastCounts struct {
	Total    int
	Pending  int
	Sent     int
	Failed   int
	Skipped  int
	Canceled int
	// Bounced and Complained count sent recipients whose message later bounced or was reported
	// as spam; they drive the automatic pause.
	Bounced    int
	Complained int
}

// MailBroadcast sends one template from one mailbox to the subscribed contacts of one list.
type MailBroadcast struct {
	ID             uint
	OrganizationID uint
	Name           string
	MailboxID      uint
	TemplateID     uint
	ListID         uint
	// UnsubscribeLabel is the text of the unsubscribe link added when the template has none.
	UnsubscribeLabel string
	// Variables are template values shared by every recipient. A contact's properties and its
	// email, first_name, and last_name override them.
	Variables map[string]string
	Status    MailBroadcastStatus
	// Subject, HTMLBody, and TextBody are the template as it was when sending started.
	Subject     string
	HTMLBody    string
	TextBody    string
	ScheduledAt *time.Time
	StartedAt   *time.Time
	FinishedAt  *time.Time
	// StopReason is a short code when the worker paused or stopped, such as send_limit_reached.
	StopReason string
	// TestPercent of the recipients, picked at random, form a test wave sent first; 0 sends to
	// everyone at once. TestWaitMinutes is how long the rest waits after the test wave.
	TestPercent     int
	TestWaitMinutes int
	// CurrentWave is the last wave the worker may send: 1 during the test wave, then 2.
	CurrentWave int
	// ResumeAt is when a paused test wave is checked and the rest continues.
	ResumeAt *time.Time
	// SafetyOverride is set when an owner resumed a broadcast the health check paused; it is not
	// paused automatically again.
	SafetyOverride bool
	Counts         MailBroadcastCounts
	CreatedBy      uint
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// MailBroadcastRecipient is one contact of a broadcast, fixed when sending starts.
type MailBroadcastRecipient struct {
	ID          uint
	BroadcastID uint
	ContactID   uint
	Email       string
	Status      MailRecipientStatus
	// MessageID is the stored message once it was sent or failed.
	MessageID   uint
	FailureCode string
	UpdatedAt   time.Time
}

// MailBroadcastRepository stores broadcasts and their recipients. Every member call is scoped
// to an organization; the worker calls are not.
type MailBroadcastRepository interface {
	// ListBroadcasts lists the broadcasts sent from the given mailboxes. No mailbox ids list nothing.
	ListBroadcasts(ctx context.Context, organizationID uint, mailboxIDs []uint, page, perPage int) ([]*MailBroadcast, int64, error)
	FindBroadcast(ctx context.Context, organizationID, id uint) (*MailBroadcast, error)
	CreateBroadcast(ctx context.Context, broadcast *MailBroadcast) error
	// UpdateDraft replaces the editable fields of a draft broadcast.
	UpdateDraft(ctx context.Context, broadcast *MailBroadcast) error
	DeleteBroadcast(ctx context.Context, organizationID, id uint) error
	// StartBroadcast moves a draft to scheduled or sending, stores the template snapshot, and adds
	// one recipient per subscribed contact of the list. It returns the number of recipients.
	StartBroadcast(ctx context.Context, broadcast *MailBroadcast) (int, error)
	// CancelBroadcast stops a scheduled or sending broadcast; pending recipients become canceled.
	CancelBroadcast(ctx context.Context, organizationID, id uint) error
	ListRecipients(ctx context.Context, organizationID, broadcastID uint, status MailRecipientStatus, page, perPage int) ([]*MailBroadcastRecipient, int64, error)

	// PromoteScheduled moves scheduled broadcasts whose time has come to sending.
	PromoteScheduled(ctx context.Context, now time.Time) error
	// LeasePending claims up to limit pending recipients of sending broadcasts until leaseUntil.
	LeasePending(ctx context.Context, now, leaseUntil time.Time, limit int) ([]*MailBroadcastRecipient, error)
	// FindBroadcastForWorker loads a broadcast without an organization scope.
	FindBroadcastForWorker(ctx context.Context, id uint) (*MailBroadcast, error)
	// FinishRecipient records the outcome of one recipient.
	FinishRecipient(ctx context.Context, id uint, status MailRecipientStatus, messageID uint, failureCode string) error
	// ReleaseRecipient returns a leased recipient to pending.
	ReleaseRecipient(ctx context.Context, id uint) error
	// SetStopReason records why the worker paused a broadcast; empty clears it.
	SetStopReason(ctx context.Context, id uint, reason string) error
	// PauseBroadcast moves a sending broadcast, or a scheduled one when organizationID is not 0,
	// to paused with the reason. organizationID 0 is the worker.
	PauseBroadcast(ctx context.Context, organizationID, id uint, reason string) error
	// ResumeBroadcast moves a paused broadcast back to sending: it opens wave 2 when the test wave
	// was waiting, and sets the safety override when the health check had paused it.
	ResumeBroadcast(ctx context.Context, organizationID, id uint, override bool) error
	// CompleteTestWaves pauses sending broadcasts whose test wave is done until its wait is over.
	CompleteTestWaves(ctx context.Context, now time.Time) error
	// ListActiveBroadcasts lists sending broadcasts and paused ones waiting after their test wave,
	// with counts, for the health check.
	ListActiveBroadcasts(ctx context.Context, limit int) ([]*MailBroadcast, error)
	// FinishBroadcasts marks sending broadcasts without pending recipients as sent.
	FinishBroadcasts(ctx context.Context, now time.Time) (int, error)

	// FindContactForBroadcast loads a contact by id within an organization, without its lists.
	FindContactForBroadcast(ctx context.Context, organizationID, id uint) (*MailContact, error)
	// SetContactStatus changes the status of a contact; reason is stored with it.
	SetContactStatus(ctx context.Context, organizationID, id uint, status MailContactStatus, reason string) error
	// SuppressContactForMessage marks the contact a broadcast message went to as suppressed,
	// unless it already is. It does nothing for a message that was not part of a broadcast.
	SuppressContactForMessage(ctx context.Context, messageID uint, reason string) error
}

// MailBroadcastDispatcher sends due broadcast recipients outside request paths.
type MailBroadcastDispatcher interface {
	DispatchBroadcasts(ctx context.Context, limit int) (int, error)
}

// MailTemplateRepository stores templates. Every lookup is scoped to an organization.
type MailTemplateRepository interface {
	// CreateTemplate stores a template unless the organization already has limit templates.
	CreateTemplate(ctx context.Context, template *MailTemplate, limit int) error
	ListTemplates(ctx context.Context, organizationID uint) ([]*MailTemplate, error)
	FindTemplate(ctx context.Context, organizationID, id uint) (*MailTemplate, error)
	UpdateTemplate(ctx context.Context, template *MailTemplate) error
	DeleteTemplate(ctx context.Context, organizationID, id uint) error
}

// MailMessageFilter narrows a mailbox message listing.
type MailMessageFilter struct {
	Direction MailDirection
	// Query matches the subject, the sender address, or a recipient address, ignoring case.
	Query string
	// Unread keeps unread messages when true and read messages when false; nil keeps both. Read
	// state is ReaderMembershipID's own: outbound mail is always read, and inbound mail is read
	// once that member opened it.
	Unread             *bool
	ReaderMembershipID uint
	Page               int
	PerPage            int
}

// MailLogFilter narrows the log of the messages in the mailboxes a viewer may read.
type MailLogFilter struct {
	Direction MailDirection
	Status    MailStatus
	MailboxID uint
	// Query matches the subject, the sender address, or a recipient address, ignoring case.
	Query   string
	Page    int
	PerPage int
}

// MailDayStats counts one UTC day of an organization's mail.
type MailDayStats struct {
	Day      time.Time
	Sent     int
	Failed   int
	Received int
}

// MailboxRepository defines persistence for mailboxes and their messages.
type MailboxRepository interface {
	CreateMailbox(ctx context.Context, mailbox *Mailbox, limit int) error
	// ListMailboxes lists the organization's mailboxes. UnreadCount is what readerMembershipID has
	// not read of each mailbox's inbound mail after its seal boundary.
	ListMailboxes(ctx context.Context, organizationID, readerMembershipID uint) ([]*Mailbox, error)
	FindMailbox(ctx context.Context, organizationID, id uint) (*Mailbox, error)
	FindMailboxByAddress(ctx context.Context, address string) (*Mailbox, error)
	DeleteMailbox(ctx context.Context, organizationID, id uint) error

	// FindMailboxMember returns the member row of one membership on a mailbox, or nil without error
	// when the membership has none.
	FindMailboxMember(ctx context.Context, mailboxID, membershipID uint) (*MailboxMember, error)
	// ListMailboxMembers lists a mailbox's members ordered by user id, at most MaxMailboxMembers.
	ListMailboxMembers(ctx context.Context, mailboxID uint) ([]*MailboxMember, error)
	// ListMembershipMailboxMembers lists the member rows one membership holds on the live mailboxes
	// of its organization.
	ListMembershipMailboxMembers(ctx context.Context, organizationID, membershipID uint) ([]*MailboxMember, error)
	// UpsertMailboxMember sets the permission of a membership on a mailbox. It locks the mailbox
	// row and refuses a new member once the mailbox has limit members. created reports an insert.
	UpsertMailboxMember(ctx context.Context, member *MailboxMember, limit int) (created bool, err error)
	// DeleteMailboxMember removes the member row of a user and returns it.
	DeleteMailboxMember(ctx context.Context, mailboxID, userID uint) (*MailboxMember, error)
	AddMailboxAccessEvent(ctx context.Context, event *MailboxAccessEvent) error
	// ListMailboxAccessEvents lists a mailbox's access history, newest first.
	ListMailboxAccessEvents(ctx context.Context, mailboxID uint, page, size int) ([]*MailboxAccessEvent, int64, error)

	// CreateMessage stores a message with its attachments. With an idempotency key that the mailbox already used it
	// reports created=false and fills message with the stored row.
	CreateMessage(ctx context.Context, message *MailMessage) (created bool, err error)
	FindMessageByIdempotencyKey(ctx context.Context, mailboxID uint, key string) (*MailMessage, error)
	// FindOutboundMessageForOrganization finds an outbound message in any mailbox of the organization.
	FindOutboundMessageForOrganization(ctx context.Context, organizationID, id uint) (*MailMessage, error)
	// CreateInboundMessage stores an inbound message with its attachments once per mailbox and dedupe key. It
	// reports created=false and fills message with the stored row on a repeated delivery.
	CreateInboundMessage(ctx context.Context, message *MailMessage) (created bool, err error)
	UpdateMessageDelivery(ctx context.Context, id uint, status MailStatus, messageID, failureCode string) error
	ListMessages(ctx context.Context, mailboxID uint, filter MailMessageFilter) ([]*MailMessage, int64, error)
	FindMessage(ctx context.Context, mailboxID, id uint) (*MailMessage, error)
	// SetMessageRead stores or deletes one membership's read row of a message of the mailbox. A
	// sealed or missing message is ErrMailMessageNotFound; an outbound message is always read and
	// stores nothing.
	SetMessageRead(ctx context.Context, mailboxID, id, membershipID uint, read bool, at time.Time) error
	// ReadMessages reports which of the given messages the membership has read.
	ReadMessages(ctx context.Context, membershipID uint, messageIDs []uint) (map[uint]bool, error)
	// DeleteMailboxReads removes a membership's read rows of every message of a mailbox; membership
	// id 0 removes everyone's.
	DeleteMailboxReads(ctx context.Context, mailboxID, membershipID uint) error
	DeleteMessage(ctx context.Context, mailboxID, id uint) error
	CountOutboundSince(ctx context.Context, organizationID uint, since time.Time) (int64, error)
	// InTransaction runs fn in one database transaction. Repository calls made with the context
	// fn receives, and cooperating repositories of other modules, join it.
	InTransaction(ctx context.Context, fn func(ctx context.Context) error) error
	// AddMessageEvents records delivery events and refreshes the message's summary in one
	// transaction. An event whose ProviderEventID is already stored is skipped; the count of
	// newly stored events is returned.
	AddMessageEvents(ctx context.Context, messageID uint, events []MailEvent) (added int, summary MailEventType, err error)
	ListMessageEvents(ctx context.Context, messageID uint) ([]MailEvent, error)
	// ListMessageAttachments lists the attachments of a message without their content.
	ListMessageAttachments(ctx context.Context, messageID uint) ([]MailAttachment, error)
	// FindMessageAttachment loads one attachment of a message with its content.
	FindMessageAttachment(ctx context.Context, messageID, id uint) (*MailAttachment, error)
	// FindOutboundMessageByProviderID finds the outbound message a provider event names, sealed or
	// not, with its mailbox's id, organization, address, type, webhook switch, and seal boundary.
	FindOutboundMessageByProviderID(ctx context.Context, providerMessageID string) (*MailMessage, *Mailbox, error)

	// MarkOwnerLeft records that the user left the organization on each of their live personal
	// mailboxes there and returns those mailboxes.
	MarkOwnerLeft(ctx context.Context, organizationID, userID uint, at time.Time) ([]*Mailbox, error)
	// ClearOwnerLeft clears the departure of the user's live personal mailboxes awaiting handover
	// and returns those mailboxes.
	ClearOwnerLeft(ctx context.Context, organizationID, userID uint) ([]*Mailbox, error)
	// MailboxesGrantedTo lists the live shared mailboxes whose access history last granted the
	// membership access rather than removed it.
	MailboxesGrantedTo(ctx context.Context, organizationID, membershipID uint) ([]uint, error)
	// HandOverMailbox makes a personal mailbox shared, without owner, sealed before the boundary,
	// and cancels its broadcasts created before the boundary that are still running. It reports
	// false, changing nothing, when the mailbox is not personal any more.
	HandOverMailbox(ctx context.Context, mailboxID uint, sealedBefore time.Time) (bool, error)
	// SetMailboxWebhooks sets the webhook switch of a live shared mailbox. It reports whether the
	// value changed.
	SetMailboxWebhooks(ctx context.Context, mailboxID uint, enabled bool) (bool, error)
	// DeleteSealedHistory cancels and deletes the mailbox's broadcasts created before the boundary
	// and deletes its messages created before it, with their dependants.
	DeleteSealedHistory(ctx context.Context, mailboxID uint, before time.Time) (messages, broadcasts int64, err error)
	// ListMessagesForOrganization lists messages across the given mailboxes of the organization,
	// newest first, without bodies. No mailbox ids list nothing.
	ListMessagesForOrganization(ctx context.Context, organizationID uint, mailboxIDs []uint, filter MailLogFilter) ([]*MailMessage, int64, error)
	// StatsForOrganization counts messages of the given mailboxes per UTC day from since, for days
	// that have any. No mailbox ids count nothing.
	StatsForOrganization(ctx context.Context, organizationID uint, mailboxIDs []uint, since time.Time) ([]MailDayStats, error)
}
