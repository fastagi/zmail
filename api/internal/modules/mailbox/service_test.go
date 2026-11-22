package mailbox

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
)

// testCreator is the user test mailboxes are created by. Creating grants nothing: the helpers
// make the creator a manage member, and as() acts as that user.
const testCreator = 99

// membershipIDOf is the membership id the fake directory gives a user in an organization.
func membershipIDOf(organizationID, userID uint) uint {
	return organizationID*1000 + userID
}

// viewerOf is a verified membership of userID in organizationID with role.
func viewerOf(organizationID, userID uint, role domain.OrganizationRole) domain.OrganizationContext {
	return domain.OrganizationContext{
		OrganizationID: organizationID, OrganizationName: "Org", OrganizationSlug: "org",
		MembershipID: membershipIDOf(organizationID, userID), UserID: userID, Role: role,
	}
}

// as acts as testCreator, an owner of organizationID.
func as(organizationID uint) domain.OrganizationContext {
	return viewerOf(organizationID, testCreator, domain.OrganizationRoleOwner)
}

// sharedInput is a shared mailbox whose one member is testCreator with manage.
func sharedInput(localPart, displayName, domainName string) CreateMailboxInput {
	return CreateMailboxInput{
		LocalPart: localPart, DisplayName: displayName, Domain: domainName, Type: domain.MailboxTypeShared,
		Members: []MailboxMemberInput{{UserID: testCreator, Permission: domain.MailboxPermissionManage}},
	}
}

// fakeMemberships treats every user as a member of every organization unless listed in absent.
type fakeMemberships struct {
	absent map[uint]bool
	// ids overrides the membership id of a user, as a rejoin with a new membership does.
	ids map[uint]uint
	// managers are the owners and admins ListManagerUserIDs returns.
	managers []uint
}

func (f *fakeMemberships) ListManagerUserIDs(context.Context, uint) ([]uint, error) {
	return slices.Clone(f.managers), nil
}

func (f *fakeMemberships) FindForUser(_ context.Context, organizationID, userID uint) (*domain.OrganizationMembership, error) {
	if f.absent[userID] {
		return nil, domain.ErrOrganizationNotFound
	}
	id := membershipIDOf(organizationID, userID)
	if override, ok := f.ids[userID]; ok {
		id = override
	}
	return &domain.OrganizationMembership{
		ID: id, OrganizationID: organizationID, UserID: userID, Role: domain.OrganizationRoleMember,
	}, nil
}

// FindMembershipsByEmails finds user n by u<n>@people.test, ignoring case, unless n is absent.
// Every other email, other@people.test of organization 8 included, belongs to no member here.
func (f *fakeMemberships) FindMembershipsByEmails(ctx context.Context, organizationID uint, emails []string) ([]*domain.OrganizationMembership, error) {
	found := make([]*domain.OrganizationMembership, 0, len(emails))
	for _, email := range emails {
		email = strings.ToLower(strings.TrimSpace(email))
		digits, ok := strings.CutPrefix(email, "u")
		if !ok {
			continue
		}
		if digits, ok = strings.CutSuffix(digits, "@people.test"); !ok {
			continue
		}
		userID, err := strconv.ParseUint(digits, 10, 32)
		if err != nil || userID == 0 {
			continue
		}
		membership, err := f.FindForUser(ctx, organizationID, uint(userID))
		if err != nil {
			continue
		}
		membership.User = &domain.User{ID: uint(userID), Email: email}
		found = append(found, membership)
	}
	return found, nil
}

// ListMembershipsByUserIDs gives user n the email u<n>@people.test, unless n is absent.
func (f *fakeMemberships) ListMembershipsByUserIDs(ctx context.Context, organizationID uint, userIDs []uint) ([]*domain.OrganizationMembership, error) {
	found := make([]*domain.OrganizationMembership, 0, len(userIDs))
	for _, userID := range userIDs {
		membership, err := f.FindForUser(ctx, organizationID, userID)
		if err != nil {
			continue
		}
		membership.User = &domain.User{ID: userID, Username: fmt.Sprintf("user%d", userID), Email: fmt.Sprintf("u%d@people.test", userID)}
		found = append(found, membership)
	}
	return found, nil
}

// fakeRepository is an in-memory domain.MailboxRepository for service tests.
type fakeRepository struct {
	organizationCreatedAt map[uint]time.Time
	mailboxes             map[uint]*domain.Mailbox
	// members is keyed by mailbox id and membership id.
	members      map[[2]uint]*domain.MailboxMember
	accessEvents []*domain.MailboxAccessEvent
	// reads is keyed by membership id and message id.
	reads    map[[2]uint]time.Time
	messages map[uint]*domain.MailMessage
	events   map[uint][]domain.MailEvent
	// attachments holds the stored files of each message, with content.
	attachments map[uint][]domain.MailAttachment
	templates   map[uint]*domain.MailTemplate
	nextID      uint
	// hideIdempotencyLookup simulates a concurrent request that stores its message between
	// this request's lookup and its insert.
	hideIdempotencyLookup bool
	lastMessageFilter     domain.MailMessageFilter
	lastMessageReader     uint
	lastLogFilter         domain.MailLogFilter
	lastLogMailboxes      []uint
	lastStatsSince        time.Time
	lastStatsMailboxes    []uint
	stats                 []domain.MailDayStats
	transactions          int
}

var (
	_ domain.MailboxRepository      = (*fakeRepository)(nil)
	_ domain.MailTemplateRepository = (*fakeRepository)(nil)
)

func (r *fakeRepository) CreateTemplate(_ context.Context, template *domain.MailTemplate, limit int) error {
	owned := 0
	for _, existing := range r.templates {
		if existing.OrganizationID != template.OrganizationID {
			continue
		}
		owned++
		if existing.Name == template.Name {
			return ErrMailTemplateNameTaken
		}
		if template.Key != "" && existing.Key == template.Key {
			return ErrMailTemplateKeyTaken
		}
	}
	if owned >= limit {
		return ErrMailTemplateLimitReached
	}
	template.ID = r.id()
	stored := *template
	r.templates[template.ID] = &stored
	return nil
}

func (r *fakeRepository) ListTemplates(_ context.Context, organizationID uint, kind domain.MailTemplateKind) ([]*domain.MailTemplate, error) {
	var listed []*domain.MailTemplate
	for _, template := range r.templates {
		if template.OrganizationID == organizationID && (kind == "" || template.Kind == kind) {
			copied := *template
			listed = append(listed, &copied)
		}
	}
	return listed, nil
}

func (r *fakeRepository) FindTemplate(_ context.Context, organizationID, id uint) (*domain.MailTemplate, error) {
	template, ok := r.templates[id]
	if !ok || template.OrganizationID != organizationID {
		return nil, ErrMailTemplateNotFound
	}
	copied := *template
	return &copied, nil
}

func (r *fakeRepository) FindTemplateByKey(_ context.Context, organizationID uint, key string) (*domain.MailTemplate, error) {
	for _, template := range r.templates {
		if template.OrganizationID == organizationID && template.Key == key && key != "" {
			copied := *template
			return &copied, nil
		}
	}
	return nil, ErrMailTemplateNotFound
}

func (r *fakeRepository) UpdateTemplate(_ context.Context, template *domain.MailTemplate) error {
	existing, ok := r.templates[template.ID]
	if !ok || existing.OrganizationID != template.OrganizationID {
		return ErrMailTemplateNotFound
	}
	for _, other := range r.templates {
		if other.ID != template.ID && other.OrganizationID == template.OrganizationID && other.Name == template.Name {
			return ErrMailTemplateNameTaken
		}
		if other.ID != template.ID && other.OrganizationID == template.OrganizationID && template.Key != "" && other.Key == template.Key {
			return ErrMailTemplateKeyTaken
		}
	}
	template.CreatedBy, template.CreatedAt = existing.CreatedBy, existing.CreatedAt
	stored := *template
	r.templates[template.ID] = &stored
	return nil
}

func (r *fakeRepository) DeleteTemplate(_ context.Context, organizationID, id uint) error {
	template, ok := r.templates[id]
	if !ok || template.OrganizationID != organizationID {
		return ErrMailTemplateNotFound
	}
	delete(r.templates, id)
	return nil
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{
		mailboxes:   map[uint]*domain.Mailbox{},
		members:     map[[2]uint]*domain.MailboxMember{},
		reads:       map[[2]uint]time.Time{},
		messages:    map[uint]*domain.MailMessage{},
		events:      map[uint][]domain.MailEvent{},
		attachments: map[uint][]domain.MailAttachment{},
		templates:   map[uint]*domain.MailTemplate{},
	}
}

// InTransaction restores the stored state when fn fails, as a database transaction would.
func (r *fakeRepository) InTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	messages := make(map[uint]domain.MailMessage, len(r.messages))
	for id, message := range r.messages {
		messages[id] = *message
	}
	events := make(map[uint][]domain.MailEvent, len(r.events))
	for id, stored := range r.events {
		events[id] = append([]domain.MailEvent(nil), stored...)
	}
	r.transactions++
	err := fn(ctx)
	if err == nil {
		return nil
	}
	r.messages = make(map[uint]*domain.MailMessage, len(messages))
	for id, message := range messages {
		restored := message
		r.messages[id] = &restored
	}
	r.events = events
	return err
}

func (r *fakeRepository) id() uint {
	r.nextID++
	return r.nextID
}

func (r *fakeRepository) CreateMailbox(_ context.Context, mailbox *domain.Mailbox, limit int) error {
	owned := 0
	for _, existing := range r.mailboxes {
		if existing.Address == mailbox.Address {
			return ErrMailboxAddressTaken
		}
		if existing.OrganizationID == mailbox.OrganizationID {
			owned++
		}
	}
	if owned >= limit {
		return ErrMailboxLimitReached
	}
	mailbox.ID = r.id()
	stored := *mailbox
	r.mailboxes[mailbox.ID] = &stored
	return nil
}

func (r *fakeRepository) ListMailboxes(_ context.Context, organizationID, readerMembershipID, _ uint) ([]*domain.Mailbox, error) {
	var owned []*domain.Mailbox
	for _, mailbox := range r.mailboxes {
		if mailbox.OrganizationID == organizationID {
			copied := *mailbox
			unread := 0
			for _, message := range r.messages {
				_, read := r.reads[[2]uint{readerMembershipID, message.ID}]
				if message.MailboxID == mailbox.ID && message.Direction == domain.MailDirectionInbound && !read && !r.sealed(message) {
					unread++
				}
			}
			copied.UnreadCount = unread
			owned = append(owned, &copied)
		}
	}
	sort.Slice(owned, func(i, j int) bool { return owned[i].ID < owned[j].ID })
	return owned, nil
}

func (r *fakeRepository) ListUsableMailboxes(ctx context.Context, organizationID, membershipID, userID uint, includeAwaitingHandover bool) ([]*domain.Mailbox, error) {
	all, err := r.ListMailboxes(ctx, organizationID, membershipID, 0)
	if err != nil {
		return nil, err
	}
	usable := make([]*domain.Mailbox, 0, len(all))
	for _, mailbox := range all {
		personal := mailbox.Type == domain.MailboxTypePersonal
		_, member := r.members[[2]uint{mailbox.ID, membershipID}]
		owned := personal && mailbox.OwnerUserID != nil && *mailbox.OwnerUserID == userID
		awaiting := includeAwaitingHandover && personal && mailbox.OwnerLeftAt != nil
		if owned || member || awaiting {
			usable = append(usable, mailbox)
		}
	}
	return usable, nil
}

func (r *fakeRepository) UpdateMailboxDisplayName(_ context.Context, organizationID, id uint, displayName string) error {
	mailbox, ok := r.mailboxes[id]
	if !ok || mailbox.OrganizationID != organizationID {
		return ErrMailboxNotFound
	}
	mailbox.DisplayName = displayName
	return nil
}

func (r *fakeRepository) ListMailboxDirectory(_ context.Context, organizationID uint, filter domain.MailboxDirectoryFilter, sentSince time.Time) ([]*domain.MailboxDirectoryEntry, error) {
	var entries []*domain.MailboxDirectoryEntry
	query := strings.ToLower(filter.Query)
	for _, mailbox := range r.mailboxes {
		if mailbox.OrganizationID != organizationID || (filter.Type != "" && mailbox.Type != filter.Type) ||
			(filter.BeforeID != 0 && mailbox.ID >= filter.BeforeID) ||
			(query != "" && !strings.Contains(strings.ToLower(mailbox.Address), query) && !strings.Contains(strings.ToLower(mailbox.DisplayName), query)) {
			continue
		}
		copied := *mailbox
		entry := &domain.MailboxDirectoryEntry{Mailbox: &copied}
		for key := range r.members {
			if key[0] == mailbox.ID {
				entry.MemberCount++
			}
		}
		for _, message := range r.messages {
			if message.MailboxID == mailbox.ID && message.Direction == domain.MailDirectionOutbound &&
				(message.SendLane == "" || message.SendLane == domain.SendLanePersonal) &&
				message.SendAttemptedAt != nil && !message.SendAttemptedAt.Before(sentSince) {
				entry.SentLast24h++
			}
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Mailbox.ID > entries[j].Mailbox.ID })
	if filter.Limit > 0 && len(entries) > filter.Limit {
		entries = entries[:filter.Limit]
	}
	return entries, nil
}

func (r *fakeRepository) CountMailboxes(_ context.Context, organizationID uint) (int64, error) {
	var count int64
	for _, mailbox := range r.mailboxes {
		if mailbox.OrganizationID == organizationID {
			count++
		}
	}
	return count, nil
}

func (r *fakeRepository) ListMembersOfMailboxes(_ context.Context, mailboxIDs []uint) ([]*domain.MailboxMember, error) {
	var members []*domain.MailboxMember
	for _, member := range r.members {
		if slices.Contains(mailboxIDs, member.MailboxID) {
			copied := *member
			members = append(members, &copied)
		}
	}
	sort.Slice(members, func(i, j int) bool {
		if members[i].MailboxID != members[j].MailboxID {
			return members[i].MailboxID < members[j].MailboxID
		}
		return members[i].UserID < members[j].UserID
	})
	return members, nil
}

func (r *fakeRepository) MailboxAddressUsed(_ context.Context, address string) (bool, error) {
	address = strings.ToLower(strings.TrimSpace(address))
	for _, mailbox := range r.mailboxes {
		if mailbox.Address == address {
			return true, nil
		}
	}
	return false, nil
}

func (r *fakeRepository) FindMailbox(_ context.Context, organizationID, id uint) (*domain.Mailbox, error) {
	mailbox, ok := r.mailboxes[id]
	if !ok || mailbox.OrganizationID != organizationID {
		return nil, ErrMailboxNotFound
	}
	copied := *mailbox
	return &copied, nil
}

func (r *fakeRepository) FindMailboxByAddress(_ context.Context, address string) (*domain.Mailbox, error) {
	for _, mailbox := range r.mailboxes {
		if mailbox.Address == address {
			copied := *mailbox
			return &copied, nil
		}
	}
	return nil, ErrMailboxNotFound
}

func (r *fakeRepository) FindMailboxMember(_ context.Context, mailboxID, membershipID uint) (*domain.MailboxMember, error) {
	member, ok := r.members[[2]uint{mailboxID, membershipID}]
	if !ok {
		return nil, nil
	}
	copied := *member
	return &copied, nil
}

func (r *fakeRepository) ListMailboxMembers(_ context.Context, mailboxID uint) ([]*domain.MailboxMember, error) {
	var listed []*domain.MailboxMember
	for key, member := range r.members {
		if key[0] == mailboxID {
			copied := *member
			listed = append(listed, &copied)
		}
	}
	sort.Slice(listed, func(i, j int) bool { return listed[i].UserID < listed[j].UserID })
	return listed, nil
}

func (r *fakeRepository) ListMembershipMailboxMembers(_ context.Context, _, membershipID uint) ([]*domain.MailboxMember, error) {
	var listed []*domain.MailboxMember
	for key, member := range r.members {
		if key[1] == membershipID {
			copied := *member
			listed = append(listed, &copied)
		}
	}
	return listed, nil
}

func (r *fakeRepository) UpsertMailboxMember(_ context.Context, member *domain.MailboxMember, limit int) (bool, error) {
	key := [2]uint{member.MailboxID, member.MembershipID}
	if existing, ok := r.members[key]; ok {
		existing.Permission, existing.GrantedBy = member.Permission, member.GrantedBy
		*member = *existing
		return false, nil
	}
	count := 0
	for k := range r.members {
		if k[0] == member.MailboxID {
			count++
		}
	}
	if count >= limit {
		return false, ErrMailboxMemberLimit
	}
	stored := *member
	r.members[key] = &stored
	return true, nil
}

func (r *fakeRepository) DeleteMailboxMember(_ context.Context, mailboxID, userID uint) (*domain.MailboxMember, error) {
	for key, member := range r.members {
		if key[0] == mailboxID && member.UserID == userID {
			delete(r.members, key)
			return member, nil
		}
	}
	return nil, ErrMailboxMemberNotFound
}

func (r *fakeRepository) AddMailboxAccessEvent(_ context.Context, event *domain.MailboxAccessEvent) error {
	// Events count separately, so the ids tests expect for mailboxes and messages stay stable.
	event.ID = uint(len(r.accessEvents) + 1)
	stored := *event
	r.accessEvents = append(r.accessEvents, &stored)
	return nil
}

func (r *fakeRepository) ListMailboxAccessEvents(_ context.Context, mailboxID uint, _, _ int) ([]*domain.MailboxAccessEvent, int64, error) {
	var listed []*domain.MailboxAccessEvent
	for i := len(r.accessEvents) - 1; i >= 0; i-- {
		if r.accessEvents[i].MailboxID == mailboxID {
			listed = append(listed, r.accessEvents[i])
		}
	}
	return listed, int64(len(listed)), nil
}

func (r *fakeRepository) DeleteMailbox(ctx context.Context, organizationID, id uint) error {
	if _, err := r.FindMailbox(ctx, organizationID, id); err != nil {
		return err
	}
	delete(r.mailboxes, id)
	for key := range r.members {
		if key[0] == id {
			delete(r.members, key)
		}
	}
	return nil
}

func (r *fakeRepository) CreateMessage(_ context.Context, message *domain.MailMessage) (bool, error) {
	if message.IdempotencyKey != "" {
		for _, existing := range r.messages {
			if existing.MailboxID == message.MailboxID && existing.IdempotencyKey == message.IdempotencyKey {
				*message = *existing
				return false, nil
			}
		}
	}
	message.ID = r.id()
	message.ThreadID = r.threadFor(message)
	message.AttachmentCount = len(message.Attachments)
	for i := range message.Attachments {
		message.Attachments[i].ID, message.Attachments[i].MessageID = r.id(), message.ID
		message.Attachments[i].Size = len(message.Attachments[i].Content)
	}
	r.attachments[message.ID] = append([]domain.MailAttachment(nil), message.Attachments...)
	stored := *message
	stored.Attachments = nil
	r.messages[message.ID] = &stored
	return true, nil
}

// threadFor joins the thread of the oldest message of the same mailbox that has the message's own
// id or that the message replies to or references, as the database repository does, or starts the
// message's own thread.
func (r *fakeRepository) threadFor(message *domain.MailMessage) uint {
	ids := append([]string{message.RFCMessageID, message.InReplyTo}, message.References...)
	var oldest *domain.MailMessage
	for _, existing := range r.messages {
		if existing.MailboxID != message.MailboxID || existing.RFCMessageID == "" || r.sealed(existing) ||
			!slices.Contains(ids, existing.RFCMessageID) {
			continue
		}
		if oldest == nil || existing.ID < oldest.ID {
			oldest = existing
		}
	}
	if oldest == nil {
		return message.ID
	}
	return oldest.ThreadID
}

func (r *fakeRepository) FindMessageByRFCMessageID(_ context.Context, mailboxID uint, rfcMessageID string) (*domain.MailMessage, error) {
	var oldest *domain.MailMessage
	for _, existing := range r.messages {
		if existing.MailboxID == mailboxID && rfcMessageID != "" && existing.RFCMessageID == rfcMessageID &&
			!r.sealed(existing) && (oldest == nil || existing.ID < oldest.ID) {
			oldest = existing
		}
	}
	if oldest == nil {
		return nil, ErrMailMessageNotFound
	}
	copied := *oldest
	return &copied, nil
}

func (r *fakeRepository) ListMessageAttachments(_ context.Context, messageID uint) ([]domain.MailAttachment, error) {
	listed := make([]domain.MailAttachment, 0, len(r.attachments[messageID]))
	for _, attachment := range r.attachments[messageID] {
		attachment.Content = nil
		listed = append(listed, attachment)
	}
	return listed, nil
}

func (r *fakeRepository) FindMessageAttachment(_ context.Context, messageID, id uint) (*domain.MailAttachment, error) {
	for _, attachment := range r.attachments[messageID] {
		if attachment.ID == id {
			return &attachment, nil
		}
	}
	return nil, ErrMailMessageNotFound
}

func (r *fakeRepository) FindMessageByIdempotencyKey(_ context.Context, mailboxID uint, key string) (*domain.MailMessage, error) {
	if r.hideIdempotencyLookup {
		return nil, ErrMailMessageNotFound
	}
	for _, existing := range r.messages {
		if existing.MailboxID == mailboxID && existing.IdempotencyKey == key {
			copied := *existing
			return &copied, nil
		}
	}
	return nil, ErrMailMessageNotFound
}

func (r *fakeRepository) FindOutboundMessageForOrganization(_ context.Context, organizationID, id uint) (*domain.MailMessage, error) {
	message, ok := r.messages[id]
	if !ok || message.Direction != domain.MailDirectionOutbound || r.sealed(message) {
		return nil, ErrMailMessageNotFound
	}
	mailbox, ok := r.mailboxes[message.MailboxID]
	if !ok || mailbox.OrganizationID != organizationID {
		return nil, ErrMailMessageNotFound
	}
	copied := *message
	return &copied, nil
}

func (r *fakeRepository) CreateInboundMessage(ctx context.Context, message *domain.MailMessage) (bool, error) {
	for _, existing := range r.messages {
		if existing.MailboxID == message.MailboxID && existing.DedupeKey == message.DedupeKey {
			*message = *existing
			return false, nil
		}
	}
	_, err := r.CreateMessage(ctx, message)
	return true, err
}

func (r *fakeRepository) UpdateMessageDelivery(_ context.Context, id uint, status domain.MailStatus, messageID, rfcMessageID, failureCode string) error {
	message, ok := r.messages[id]
	if !ok {
		return ErrMailMessageNotFound
	}
	message.RFCMessageID = rfcMessageID
	message.Status, message.MessageID, message.FailureCode = status, messageID, failureCode
	return nil
}

func (r *fakeRepository) ListMessages(_ context.Context, mailboxID, membershipID uint, filter domain.MailMessageFilter) ([]*domain.MailMessage, int64, error) {
	r.lastMessageFilter, r.lastMessageReader = filter, membershipID
	var listed []*domain.MailMessage
	for _, message := range r.messages {
		if message.MailboxID != mailboxID || r.sealed(message) {
			continue
		}
		if filter.Direction != "" && message.Direction != filter.Direction {
			continue
		}
		_, readByReader := r.reads[[2]uint{membershipID, message.ID}]
		read := message.Direction == domain.MailDirectionOutbound || readByReader
		if filter.Unread != nil && read == *filter.Unread {
			continue
		}
		if filter.Query != "" && !fakeMessageMatchesQuery(message, filter.Query) {
			continue
		}
		listed = append(listed, message)
	}
	slices.SortFunc(listed, func(left, right *domain.MailMessage) int {
		return cmp.Compare(right.ID, left.ID)
	})
	total := int64(len(listed))
	if filter.PerPage > 0 {
		page := max(filter.Page, 1)
		start := min((page-1)*filter.PerPage, len(listed))
		end := min(start+filter.PerPage, len(listed))
		listed = listed[start:end]
	}
	return listed, total, nil
}

func fakeMessageMatchesQuery(message *domain.MailMessage, query string) bool {
	needle := strings.ToLower(query)
	if strings.Contains(strings.ToLower(message.Subject), needle) ||
		strings.Contains(strings.ToLower(message.FromAddress), needle) ||
		strings.Contains(strings.ToLower(message.FromName), needle) ||
		strings.Contains(strings.ToLower(message.TextBody), needle) {
		return true
	}
	for _, recipient := range message.To {
		if strings.Contains(strings.ToLower(recipient), needle) {
			return true
		}
	}
	for _, recipient := range message.Cc {
		if strings.Contains(strings.ToLower(recipient), needle) {
			return true
		}
	}
	return false
}

func (r *fakeRepository) FindMessage(_ context.Context, mailboxID, id uint) (*domain.MailMessage, error) {
	message, ok := r.messages[id]
	if !ok || message.MailboxID != mailboxID || r.sealed(message) {
		return nil, ErrMailMessageNotFound
	}
	copied := *message
	return &copied, nil
}

// sealed reports whether a message was created before its mailbox's seal boundary.
func (r *fakeRepository) sealed(message *domain.MailMessage) bool {
	mailbox, ok := r.mailboxes[message.MailboxID]
	return ok && mailbox.SealedBefore != nil && message.CreatedAt.Before(*mailbox.SealedBefore)
}

func (r *fakeRepository) SetMessageRead(_ context.Context, mailboxID, id, membershipID uint, read bool, at time.Time) error {
	message, ok := r.messages[id]
	if !ok || message.MailboxID != mailboxID || r.sealed(message) {
		return ErrMailMessageNotFound
	}
	if membershipID == 0 {
		return domain.ErrInvalidInput
	}
	if message.Direction == domain.MailDirectionOutbound {
		return nil
	}
	key := [2]uint{membershipID, id}
	if !read {
		delete(r.reads, key)
		return nil
	}
	if _, exists := r.reads[key]; !exists {
		r.reads[key] = at
	}
	return nil
}

func (r *fakeRepository) ReadMessages(_ context.Context, membershipID uint, messageIDs []uint) (map[uint]bool, error) {
	read := map[uint]bool{}
	for _, id := range messageIDs {
		if _, ok := r.reads[[2]uint{membershipID, id}]; ok {
			read[id] = true
		}
	}
	return read, nil
}

// StarredMessages reports no stars: the in-memory scenes never star, PostgreSQL ones do.
func (r *fakeRepository) StarredMessages(_ context.Context, _ uint, messageIDs []uint) (map[uint]bool, error) {
	return make(map[uint]bool, len(messageIDs)), nil
}

// MessageLabelIDs reports no labels: the in-memory scenes never label, PostgreSQL ones do.
func (r *fakeRepository) MessageLabelIDs(_ context.Context, messageIDs []uint) (map[uint][]uint, error) {
	return make(map[uint][]uint, len(messageIDs)), nil
}

func (r *fakeRepository) DeleteMailboxReads(_ context.Context, mailboxID, membershipID uint) error {
	for key := range r.reads {
		if message, ok := r.messages[key[1]]; (membershipID == 0 || key[0] == membershipID) && ok && message.MailboxID == mailboxID {
			delete(r.reads, key)
		}
	}
	return nil
}

func (r *fakeRepository) DeleteMessage(_ context.Context, mailboxID, id uint) error {
	message, ok := r.messages[id]
	if !ok || message.MailboxID != mailboxID || r.sealed(message) {
		return ErrMailMessageNotFound
	}
	delete(r.messages, id)
	return nil
}

func (r *fakeRepository) OrganizationCreatedAt(_ context.Context, organizationID uint) (time.Time, error) {
	return r.organizationCreatedAt[organizationID], nil
}

func (r *fakeRepository) CountOutboundSince(_ context.Context, organizationID uint, lane domain.SendLane, _ time.Time) (int64, error) {
	var count int64
	for _, message := range r.messages {
		mailbox, ok := r.mailboxes[message.MailboxID]
		messageLane := message.SendLane
		if messageLane == "" {
			messageLane = domain.SendLanePersonal
		}
		if ok && mailbox.OrganizationID == organizationID && message.Direction == domain.MailDirectionOutbound &&
			message.QueuedReason == "" && messageLane == lane {
			count++
		}
	}
	return count, nil
}

// The in-memory repository keeps waiting messages but never dispatches them: the queue worker
// is tested against PostgreSQL, where leases and SKIP LOCKED are real.
func (r *fakeRepository) WaitingSendOrganizations(context.Context) ([]uint, error) { return nil, nil }

func (r *fakeRepository) LeaseWaitingSends(context.Context, uint, time.Time, time.Time, time.Time, int, int) ([]*domain.MailMessage, error) {
	return nil, nil
}

func (r *fakeRepository) StartWaitingSend(context.Context, uint, time.Time) (bool, error) {
	return false, nil
}

func (r *fakeRepository) CancelWaitingSend(_ context.Context, id uint, failureCode string) error {
	if message, ok := r.messages[id]; ok && message.QueuedReason != "" {
		message.Status, message.FailureCode, message.QueuedReason = domain.MailStatusFailed, failureCode, ""
	}
	return nil
}

func (r *fakeRepository) CountWaitingSends(_ context.Context, organizationID uint) (int64, error) {
	var count int64
	for _, message := range r.messages {
		mailbox, ok := r.mailboxes[message.MailboxID]
		if ok && mailbox.OrganizationID == organizationID && message.QueuedReason != "" {
			count++
		}
	}
	return count, nil
}

func (r *fakeRepository) ListMessagesForOrganization(_ context.Context, organizationID uint, mailboxIDs []uint, filter domain.MailLogFilter) ([]*domain.MailMessage, int64, error) {
	r.lastLogFilter = filter
	r.lastLogMailboxes = mailboxIDs
	var listed []*domain.MailMessage
	for _, message := range r.messages {
		if mailbox, ok := r.mailboxes[message.MailboxID]; ok && mailbox.OrganizationID == organizationID &&
			slices.Contains(mailboxIDs, message.MailboxID) && !r.sealed(message) {
			listed = append(listed, message)
		}
	}
	return listed, int64(len(listed)), nil
}

func (r *fakeRepository) StatsForOrganization(_ context.Context, _ uint, mailboxIDs []uint, since time.Time) ([]domain.MailDayStats, error) {
	r.lastStatsSince = since
	r.lastStatsMailboxes = mailboxIDs
	if len(mailboxIDs) == 0 {
		return nil, nil
	}
	return r.stats, nil
}

func (r *fakeRepository) AddMessageEvents(_ context.Context, messageID uint, events []domain.MailEvent) (int, domain.MailEventType, error) {
	message, ok := r.messages[messageID]
	if !ok {
		return 0, "", ErrMailMessageNotFound
	}
	added := 0
	for _, event := range events {
		duplicate := false
		for _, stored := range r.events[messageID] {
			if event.ProviderEventID != "" && stored.ProviderEventID == event.ProviderEventID {
				duplicate = true
			}
		}
		if duplicate {
			continue
		}
		event.ID = r.id()
		event.MessageID = messageID
		r.events[messageID] = append(r.events[messageID], event)
		added++
	}
	message.LastEvent = domain.SummarizeMailEvents(r.events[messageID])
	return added, message.LastEvent, nil
}

func (r *fakeRepository) ListMessageEvents(_ context.Context, messageID uint) ([]domain.MailEvent, error) {
	return append([]domain.MailEvent(nil), r.events[messageID]...), nil
}

func (r *fakeRepository) FindOutboundMessageByProviderID(_ context.Context, providerMessageID string) (*domain.MailMessage, *domain.Mailbox, error) {
	for _, message := range r.messages {
		if message.Direction == domain.MailDirectionOutbound && message.MessageID == providerMessageID && providerMessageID != "" {
			copied := *message
			mailbox := &domain.Mailbox{ID: message.MailboxID}
			if stored, ok := r.mailboxes[message.MailboxID]; ok {
				copied.MailboxAddress, copied.OrganizationID = stored.Address, stored.OrganizationID
				boxCopy := *stored
				mailbox = &boxCopy
			}
			return &copied, mailbox, nil
		}
	}
	return nil, nil, ErrMailMessageNotFound
}

func (r *fakeRepository) updateOwned(organizationID, userID uint, change func(*domain.Mailbox) bool) []*domain.Mailbox {
	var changed []*domain.Mailbox
	for _, mailbox := range r.mailboxes {
		if mailbox.OrganizationID == organizationID && mailbox.Type == domain.MailboxTypePersonal &&
			mailbox.OwnerUserID != nil && *mailbox.OwnerUserID == userID && change(mailbox) {
			copied := *mailbox
			changed = append(changed, &copied)
		}
	}
	sort.Slice(changed, func(i, j int) bool { return changed[i].ID < changed[j].ID })
	return changed
}

func (r *fakeRepository) MarkOwnerLeft(_ context.Context, organizationID, userID uint, at time.Time) ([]*domain.Mailbox, error) {
	return r.updateOwned(organizationID, userID, func(mailbox *domain.Mailbox) bool {
		left := at
		mailbox.OwnerLeftAt = &left
		return true
	}), nil
}

func (r *fakeRepository) ClearOwnerLeft(_ context.Context, organizationID, userID uint) ([]*domain.Mailbox, error) {
	return r.updateOwned(organizationID, userID, func(mailbox *domain.Mailbox) bool {
		if mailbox.OwnerLeftAt == nil {
			return false
		}
		mailbox.OwnerLeftAt = nil
		return true
	}), nil
}

func (r *fakeRepository) MailboxesGrantedTo(_ context.Context, organizationID, membershipID uint) ([]uint, error) {
	latest := map[uint]domain.MailboxAccessEventType{}
	for _, event := range r.accessEvents {
		mailbox, ok := r.mailboxes[event.MailboxID]
		if !ok || mailbox.Type != domain.MailboxTypeShared || event.OrganizationID != organizationID ||
			fmt.Sprint(event.Detail["membership_id"]) != fmt.Sprint(membershipID) {
			continue
		}
		switch event.Type {
		case domain.MailboxAccessCreated, domain.MailboxAccessMemberSet, domain.MailboxAccessHandedOver, domain.MailboxAccessMemberRemoved:
			latest[event.MailboxID] = event.Type
		default:
		}
	}
	var granted []uint
	for id, eventType := range latest {
		if eventType != domain.MailboxAccessMemberRemoved {
			granted = append(granted, id)
		}
	}
	slices.Sort(granted)
	return granted, nil
}

func (r *fakeRepository) HandOverMailbox(_ context.Context, mailboxID uint, sealedBefore time.Time) (bool, error) {
	mailbox, ok := r.mailboxes[mailboxID]
	if !ok || mailbox.Type != domain.MailboxTypePersonal {
		return false, nil
	}
	boundary := sealedBefore
	mailbox.Type, mailbox.OwnerUserID, mailbox.OwnerLeftAt, mailbox.SealedBefore = domain.MailboxTypeShared, nil, nil, &boundary
	return true, nil
}

func (r *fakeRepository) SetMailboxWebhooks(_ context.Context, mailboxID uint, enabled bool) (bool, error) {
	mailbox, ok := r.mailboxes[mailboxID]
	if !ok || mailbox.Type != domain.MailboxTypeShared || mailbox.WebhooksEnabled == enabled {
		return false, nil
	}
	mailbox.WebhooksEnabled = enabled
	return true, nil
}

func (r *fakeRepository) DeleteSealedHistory(_ context.Context, mailboxID uint, before time.Time) (int64, int64, error) {
	var deleted int64
	for id, message := range r.messages {
		if message.MailboxID == mailboxID && message.CreatedAt.Before(before) {
			delete(r.messages, id)
			delete(r.attachments, id)
			delete(r.events, id)
			deleted++
		}
	}
	return deleted, 0, nil
}

// fakeNotifier records the notifications the service publishes; failures makes that many
// publications fail first.
type fakeNotifier struct {
	sent          []MailNotification
	transactional []TransactionalNotification
	failures      int
}

func (n *fakeNotifier) NotifyTransactional(_ context.Context, notification TransactionalNotification) error {
	if n.failures > 0 {
		n.failures--
		return errors.New("webhook outbox unavailable")
	}
	n.transactional = append(n.transactional, notification)
	return nil
}

func (n *fakeNotifier) Notify(_ context.Context, notification MailNotification) error {
	if n.failures > 0 {
		n.failures--
		return errors.New("webhook outbox unavailable")
	}
	n.sent = append(n.sent, notification)
	return nil
}

func (n *fakeNotifier) types() []string {
	types := make([]string, 0, len(n.sent))
	for _, notification := range n.sent {
		types = append(types, notification.Type)
	}
	return types
}

// fakeTransport records what the service hands to the provider.
type fakeTransport struct {
	sent   []mailtransport.Message
	result mailtransport.Result
	err    error
}

func (t *fakeTransport) Send(_ context.Context, mail mailtransport.Message) (mailtransport.Result, error) {
	t.sent = append(t.sent, mail)
	return t.result, t.err
}

type serviceFixture struct {
	service   *service
	repo      *fakeRepository
	transport *fakeTransport
	notifier  *fakeNotifier
	contacts  *fakeContacts
	now       time.Time
}

func newServiceFixture(t *testing.T) *serviceFixture {
	t.Helper()
	fixture := &serviceFixture{
		repo:      newFakeRepository(),
		transport: &fakeTransport{},
		notifier:  &fakeNotifier{},
		contacts:  newFakeContacts(),
		now:       time.Unix(1_800_000_000, 0).UTC(),
	}
	fixture.service = NewService(fixture.repo, &fakeMemberships{}, fixture.repo, fixture.contacts, &stubBroadcasts{}, nil, nil, nil, nil, fixture.transport, fixture.notifier, Settings{
		Domain:           "mail.example",
		InboundSecret:    testInboundSecret,
		InboundTolerance: 5 * time.Minute,
		DailySendLimit:   3,
	}, nil, nil, nil)
	fixture.service.now = func() time.Time { return fixture.now }
	return fixture
}

func (f *serviceFixture) mailbox(t *testing.T, organizationID uint, localPart string) *domain.Mailbox {
	t.Helper()
	mailbox, err := f.service.CreateMailbox(context.Background(), as(organizationID), sharedInput(localPart, "Test Sender", ""))
	if err != nil {
		t.Fatalf("CreateMailbox(%q): %v", localPart, err)
	}
	return mailbox
}

func TestCreateMailboxBuildsAddressOnTheConfiguredDomain(t *testing.T) {
	fixture := newServiceFixture(t)
	mailbox := fixture.mailbox(t, 7, "  Hello ")
	if mailbox.Address != "hello@mail.example" || mailbox.OrganizationID != 7 || mailbox.DisplayName != "Test Sender" {
		t.Fatalf("mailbox = %+v", mailbox)
	}

	if _, err := fixture.service.CreateMailbox(context.Background(), as(8), sharedInput("HELLO", "", "")); !errors.Is(err, ErrMailboxAddressTaken) {
		t.Fatalf("duplicate address error = %v", err)
	}
	if _, err := fixture.service.CreateMailbox(context.Background(), as(7), sharedInput("postmaster", "", "")); !errors.Is(err, ErrMailboxAddressReserved) {
		t.Fatalf("reserved address error = %v", err)
	}
	if _, err := fixture.service.CreateMailbox(context.Background(), as(7), sharedInput("ok", `Eve <eve@evil.test>`, "")); !errors.Is(err, ErrMailboxAddressInvalid) {
		t.Fatalf("display name error = %v", err)
	}
}

func TestMailboxesAreInvisibleToOtherOrganizations(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	mailbox := fixture.mailbox(t, 1, "owner")
	message, _, err := fixture.service.SendMessage(ctx, as(1), mailbox.ID, SendInput{To: []string{"x@example.com"}, Subject: "s", Text: "b"})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	const stranger = 2
	if _, _, err := fixture.service.ListMessages(ctx, as(stranger), mailbox.ID, domain.MailMessageFilter{}); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("ListMessages error = %v", err)
	}
	if _, err := fixture.service.GetMessage(ctx, as(stranger), mailbox.ID, message.ID); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("GetMessage error = %v", err)
	}
	if _, _, err := fixture.service.SendMessage(ctx, as(stranger), mailbox.ID, SendInput{To: []string{"x@example.com"}, Subject: "s", Text: "b"}); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("SendMessage error = %v", err)
	}
	if _, err := fixture.service.SetMessageRead(ctx, as(stranger), mailbox.ID, message.ID, false); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("SetMessageRead error = %v", err)
	}
	if err := fixture.service.DeleteMessage(ctx, as(stranger), mailbox.ID, message.ID); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("DeleteMessage error = %v", err)
	}
	if err := fixture.service.DeleteMailbox(ctx, as(stranger), mailbox.ID); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("DeleteMailbox error = %v", err)
	}
	if len(fixture.transport.sent) != 1 {
		t.Fatalf("transport calls = %d; another organization's send must not reach the provider", len(fixture.transport.sent))
	}
}

func TestSendMessageRecordsSentAndNormalizesRecipients(t *testing.T) {
	fixture := newServiceFixture(t)
	mailbox := fixture.mailbox(t, 1, "hello")

	message, _, err := fixture.service.SendMessage(context.Background(), as(1), mailbox.ID, SendInput{
		To:        []string{"Bob@Example.com", "bob@example.com"},
		Cc:        []string{"bob@example.com", "amy@example.org"},
		Subject:   "  Greetings ",
		Text:      "Hello",
		InReplyTo: "<prev@example.com>",
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if message.Status != domain.MailStatusSent || message.FailureCode != "" || !message.Read {
		t.Fatalf("message = %+v", message)
	}
	if fixture.repo.messages[message.ID].Status != domain.MailStatusSent {
		t.Fatalf("stored status = %q", fixture.repo.messages[message.ID].Status)
	}

	sent := fixture.transport.sent[0]
	if sent.FromAddress != "hello@mail.example" || sent.FromName != "Test Sender" {
		t.Fatalf("from = %q %q", sent.FromAddress, sent.FromName)
	}
	if len(sent.To) != 1 || sent.To[0] != "bob@example.com" || len(sent.Cc) != 1 || sent.Cc[0] != "amy@example.org" {
		t.Fatalf("recipients = %v %v; duplicates across to and cc must collapse", sent.To, sent.Cc)
	}
	if sent.Subject != "Greetings" || sent.InReplyTo != "prev@example.com" {
		t.Fatalf("subject = %q, in_reply_to = %q", sent.Subject, sent.InReplyTo)
	}
}

func TestSendMessageReportsProviderFailureInsteadOfSent(t *testing.T) {
	fixture := newServiceFixture(t)
	mailbox := fixture.mailbox(t, 1, "hello")
	fixture.transport.err = &mailtransport.Error{Code: "email.sending.error.throttled"}

	message, _, err := fixture.service.SendMessage(context.Background(), as(1), mailbox.ID, SendInput{To: []string{"x@example.com"}, Subject: "s", Text: "b"})
	if !errors.Is(err, ErrMailDeliveryFailed) {
		t.Fatalf("error = %v; want ErrMailDeliveryFailed", err)
	}
	if message == nil || message.Status != domain.MailStatusFailed || message.FailureCode != "email.sending.error.throttled" {
		t.Fatalf("message = %+v", message)
	}
	stored := fixture.repo.messages[message.ID]
	if stored.Status != domain.MailStatusFailed || stored.FailureCode != "email.sending.error.throttled" {
		t.Fatalf("stored = %+v; the failure must be persisted", stored)
	}

	fixture.transport.err = mailtransport.ErrNotConfigured
	_, _, err = fixture.service.SendMessage(context.Background(), as(1), mailbox.ID, SendInput{To: []string{"x@example.com"}, Subject: "s", Text: "b"})
	if !errors.Is(err, ErrMailSenderNotConfigured) {
		t.Fatalf("error = %v; want ErrMailSenderNotConfigured", err)
	}
}

func TestSendMessageRecordsOutcomeAfterTheCallerCancels(t *testing.T) {
	fixture := newServiceFixture(t)
	mailbox := fixture.mailbox(t, 1, "hello")
	ctx, cancel := context.WithCancel(context.Background())
	canceling := &cancelingRepository{fakeRepository: fixture.repo}
	fixture.service.repo = canceling
	fixture.service.transport = transportFunc(func(context.Context, mailtransport.Message) error {
		cancel() // the client disconnects while the provider call is in flight
		return nil
	})

	message, _, err := fixture.service.SendMessage(ctx, as(1), mailbox.ID, SendInput{To: []string{"x@example.com"}, Subject: "s", Text: "b"})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if canceling.updateContextErr != nil {
		t.Fatalf("delivery was recorded with a canceled context: %v", canceling.updateContextErr)
	}
	if fixture.repo.messages[message.ID].Status != domain.MailStatusSent {
		t.Fatalf("stored status = %q; want sent", fixture.repo.messages[message.ID].Status)
	}
}

type transportFunc func(context.Context, mailtransport.Message) error

func (f transportFunc) Send(ctx context.Context, mail mailtransport.Message) (mailtransport.Result, error) {
	return mailtransport.Result{}, f(ctx, mail)
}

type cancelingRepository struct {
	*fakeRepository
	updateContextErr error
}

func (r *cancelingRepository) UpdateMessageDelivery(ctx context.Context, id uint, status domain.MailStatus, messageID, rfcMessageID, failureCode string) error {
	r.updateContextErr = ctx.Err()
	return r.fakeRepository.UpdateMessageDelivery(ctx, id, status, messageID, rfcMessageID, failureCode)
}

func TestSendMessageValidatesInput(t *testing.T) {
	fixture := newServiceFixture(t)
	mailbox := fixture.mailbox(t, 1, "hello")
	tooMany := make([]string, 0, domain.MaxMailRecipients+1)
	for i := 0; i <= domain.MaxMailRecipients; i++ {
		tooMany = append(tooMany, "user"+string(rune('a'+i%26))+string(rune('a'+i/26))+"@example.com")
	}

	cases := map[string]SendInput{
		"no recipients":       {Subject: "s", Text: "b"},
		"bad recipient":       {To: []string{"nope"}, Subject: "s", Text: "b"},
		"too many recipients": {To: tooMany, Subject: "s", Text: "b"},
		"empty subject":       {To: []string{"x@example.com"}, Subject: "  ", Text: "b"},
		"header injection":    {To: []string{"x@example.com"}, Subject: "s\r\nBcc: evil@example.com", Text: "b"},
		"no body":             {To: []string{"x@example.com"}, Subject: "s", Text: "  "},
		"bad in_reply_to":     {To: []string{"x@example.com"}, Subject: "s", Text: "b", InReplyTo: "not an id"},
	}
	for name, input := range cases {
		if _, _, err := fixture.service.SendMessage(context.Background(), as(1), mailbox.ID, input); !errors.Is(err, ErrMailMessageInvalid) {
			t.Fatalf("%s: error = %v; want ErrMailMessageInvalid", name, err)
		}
	}
	if len(fixture.transport.sent) != 0 || len(fixture.repo.messages) != 0 {
		t.Fatalf("invalid input reached the provider or storage")
	}
}

func TestSendAtLimitQueues(t *testing.T) {
	fixture := newServiceFixture(t)
	mailbox := fixture.mailbox(t, 1, "hello")
	input := SendInput{To: []string{"x@example.com"}, Subject: "s", Text: "b"}
	for i := 0; i < 3; i++ {
		if _, _, err := fixture.service.SendMessage(context.Background(), as(1), mailbox.ID, input); err != nil {
			t.Fatalf("send %d: %v", i+1, err)
		}
	}
	message, created, err := fixture.service.SendMessage(context.Background(), as(1), mailbox.ID, input)
	if err != nil {
		t.Fatalf("send at the limit: %v", err)
	}
	if !created || message.Status != domain.MailStatusQueued || message.QueuedReason != domain.MailQueuedForSendLimit {
		t.Fatalf("message = %+v, created %v; want a new queued message waiting for the send limit", message, created)
	}
	if message.SendAttemptedAt != nil {
		t.Fatalf("a waiting message has an attempt time: %v", message.SendAttemptedAt)
	}
	if len(fixture.transport.sent) != 3 {
		t.Fatalf("transport calls = %d; want 3, the queued message is not sent yet", len(fixture.transport.sent))
	}
	if stored := fixture.repo.messages[message.ID]; stored == nil || stored.QueuedReason != domain.MailQueuedForSendLimit {
		t.Fatalf("stored = %+v; want the waiting message stored", stored)
	}
}

func TestSendAtWaitingCapIsRejected(t *testing.T) {
	fixture := newServiceFixture(t)
	mailbox := fixture.mailbox(t, 1, "hello")
	input := SendInput{To: []string{"x@example.com"}, Subject: "s", Text: "b"}
	// Three sends fill the window, three more fill the waiting cap of one day's limit.
	for i := 0; i < 6; i++ {
		if _, _, err := fixture.service.SendMessage(context.Background(), as(1), mailbox.ID, input); err != nil {
			t.Fatalf("send %d: %v", i+1, err)
		}
	}
	if _, _, err := fixture.service.SendMessage(context.Background(), as(1), mailbox.ID, input); !errors.Is(err, ErrMailSendLimitReached) {
		t.Fatalf("error = %v; want ErrMailSendLimitReached at the waiting cap", err)
	}
	if len(fixture.repo.messages) != 6 {
		t.Fatalf("stored messages = %d; want 6, nothing stored at the cap", len(fixture.repo.messages))
	}
}

func TestReplayOfWaitingMessageAnswers202(t *testing.T) {
	fixture := newServiceFixture(t)
	mailbox := fixture.mailbox(t, 1, "hello")
	input := SendInput{To: []string{"x@example.com"}, Subject: "s", Text: "b"}
	for i := 0; i < 3; i++ {
		if _, _, err := fixture.service.SendMessage(context.Background(), as(1), mailbox.ID, input); err != nil {
			t.Fatalf("send %d: %v", i+1, err)
		}
	}
	keyed := input
	keyed.IdempotencyKey = "waiting-1"
	first, _, err := fixture.service.SendMessage(context.Background(), as(1), mailbox.ID, keyed)
	if err != nil || first.QueuedReason != domain.MailQueuedForSendLimit {
		t.Fatalf("first = %+v, %v; want a waiting message", first, err)
	}
	replayed, created, err := fixture.service.SendMessage(context.Background(), as(1), mailbox.ID, keyed)
	if err != nil {
		t.Fatalf("replay of a waiting message: %v; want the same message, not SEND_IN_PROGRESS", err)
	}
	if created || replayed.ID != first.ID || replayed.QueuedReason != domain.MailQueuedForSendLimit {
		t.Fatalf("replayed = %+v, created %v; want message %d still waiting", replayed, created, first.ID)
	}
}

func TestSentMessageRecordsItsAttemptTime(t *testing.T) {
	fixture := newServiceFixture(t)
	mailbox := fixture.mailbox(t, 1, "hello")
	message, _, err := fixture.service.SendMessage(context.Background(), as(1), mailbox.ID,
		SendInput{To: []string{"x@example.com"}, Subject: "s", Text: "b"})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if message.SendAttemptedAt == nil || !message.SendAttemptedAt.Equal(fixture.now) || message.QueuedReason != "" {
		t.Fatalf("message = %+v; want attempted at %v and not waiting", message, fixture.now)
	}
}

func inboundRaw(messageID string) []byte {
	return crlf(
		"From: Alice <alice@sender.test>",
		"To: hello@mail.example",
		"Subject: Inbound",
		"Message-ID: <"+messageID+">",
		"",
		"Hi from outside.",
	)
}

func (f *serviceFixture) delivery(recipient string, raw []byte) InboundDelivery {
	return InboundDelivery{
		Recipient: recipient,
		Signature: signedHeader(f.now, recipient, raw),
		Raw:       raw,
	}
}

func TestReceiveInboundStoresOnceAndDeduplicatesRetries(t *testing.T) {
	fixture := newServiceFixture(t)
	mailbox := fixture.mailbox(t, 1, "hello")
	raw := inboundRaw("in-1@sender.test")

	message, created, err := fixture.service.ReceiveInbound(context.Background(), fixture.delivery("Hello@Mail.Example", raw))
	if err != nil || !created {
		t.Fatalf("ReceiveInbound = %v, created %v", err, created)
	}
	if message.MailboxID != mailbox.ID || message.Direction != domain.MailDirectionInbound ||
		message.Status != domain.MailStatusReceived || message.Read {
		t.Fatalf("message = %+v", message)
	}
	if message.FromAddress != "alice@sender.test" || message.Subject != "Inbound" || message.TextBody != "Hi from outside." {
		t.Fatalf("message = %+v", message)
	}

	again, created, err := fixture.service.ReceiveInbound(context.Background(), fixture.delivery("hello@mail.example", raw))
	if err != nil || created || again.ID != message.ID {
		t.Fatalf("retry = id %d, created %v, err %v; want the stored message", again.ID, created, err)
	}
	if len(fixture.repo.messages) != 1 {
		t.Fatalf("stored messages = %d; want 1", len(fixture.repo.messages))
	}
}

func TestReceiveInboundRejectsUntrustedOrUndeliverableMail(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.mailbox(t, 1, "hello")
	raw := inboundRaw("in-2@sender.test")
	ctx := context.Background()

	unsigned := InboundDelivery{Recipient: "hello@mail.example", Signature: "t=1,v1=00", Raw: raw}
	if _, _, err := fixture.service.ReceiveInbound(ctx, unsigned); !errors.Is(err, ErrMailInboundSignature) {
		t.Fatalf("bad signature error = %v", err)
	}
	// An unsigned request for an unknown address gets the same answer, so it cannot probe addresses.
	unsigned.Recipient = "nobody@mail.example"
	if _, _, err := fixture.service.ReceiveInbound(ctx, unsigned); !errors.Is(err, ErrMailInboundSignature) {
		t.Fatalf("unsigned unknown recipient error = %v", err)
	}
	if _, _, err := fixture.service.ReceiveInbound(ctx, fixture.delivery("nobody@mail.example", raw)); !errors.Is(err, ErrMailRecipientNotFound) {
		t.Fatalf("unknown recipient error = %v", err)
	}
	garbage := []byte("not a message")
	if _, _, err := fixture.service.ReceiveInbound(ctx, fixture.delivery("hello@mail.example", garbage)); !errors.Is(err, ErrMailInboundUnparseable) {
		t.Fatalf("garbage error = %v", err)
	}
	oversized := make([]byte, MaxInboundBytes+1)
	if _, _, err := fixture.service.ReceiveInbound(ctx, fixture.delivery("hello@mail.example", oversized)); !errors.Is(err, ErrMailInboundTooLarge) {
		t.Fatalf("oversized error = %v", err)
	}

	fixture.service.settings.InboundSecret = ""
	if _, _, err := fixture.service.ReceiveInbound(ctx, fixture.delivery("hello@mail.example", raw)); !errors.Is(err, ErrMailInboundDisabled) {
		t.Fatalf("disabled error = %v", err)
	}
	if len(fixture.repo.messages) != 0 {
		t.Fatalf("stored messages = %d; nothing should be stored", len(fixture.repo.messages))
	}
}

func TestSetMessageReadTogglesState(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.mailbox(t, 1, "hello")
	ctx := context.Background()
	message, _, err := fixture.service.ReceiveInbound(ctx, fixture.delivery("hello@mail.example", inboundRaw("in-3@sender.test")))
	if err != nil {
		t.Fatalf("ReceiveInbound: %v", err)
	}

	read, err := fixture.service.SetMessageRead(ctx, as(1), message.MailboxID, message.ID, true)
	if err != nil || !read.Read || fixture.repo.reads[[2]uint{as(1).MembershipID, message.ID}] != fixture.now {
		t.Fatalf("read = %+v, err %v", read, err)
	}
	unread, err := fixture.service.SetMessageRead(ctx, as(1), message.MailboxID, message.ID, false)
	if err != nil || unread.Read {
		t.Fatalf("unread = %+v, err %v", unread, err)
	}
	if _, err := fixture.service.SetMessageRead(ctx, as(1), message.MailboxID, 999, true); !errors.Is(err, ErrMailMessageNotFound) {
		t.Fatalf("missing message error = %v", err)
	}
}

func TestSendMessageWithAnIdempotencyKeySendsOnce(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	mailbox := fixture.mailbox(t, 1, "hello")
	input := SendInput{To: []string{"x@example.com"}, Subject: "s", Text: "b", IdempotencyKey: " order-42 "}

	first, created, err := fixture.service.SendMessage(ctx, as(1), mailbox.ID, input)
	if err != nil || !created || first.IdempotencyKey != "order-42" {
		t.Fatalf("first send = %+v, created %v, err %v", first, created, err)
	}
	// The retry differs only in ways normalization removes.
	retry := input
	retry.To = []string{"X@Example.com"}
	retry.Subject = " s "
	again, created, err := fixture.service.SendMessage(ctx, as(1), mailbox.ID, retry)
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("retry = id %d, created %v, err %v; want the first message %d", again.ID, created, err, first.ID)
	}
	if len(fixture.transport.sent) != 1 {
		t.Fatalf("transport calls = %d; a retry must not send again", len(fixture.transport.sent))
	}

	changed := input
	changed.Text = "a different body"
	if _, _, err := fixture.service.SendMessage(ctx, as(1), mailbox.ID, changed); !errors.Is(err, ErrMailIdempotencyConflict) {
		t.Fatalf("changed content error = %v; want ErrMailIdempotencyConflict", err)
	}

	// The key is scoped to one mailbox.
	other := fixture.mailbox(t, 1, "other")
	if _, created, err := fixture.service.SendMessage(ctx, as(1), other.ID, input); err != nil || !created {
		t.Fatalf("same key in another mailbox = created %v, err %v", created, err)
	}
	if len(fixture.transport.sent) != 2 {
		t.Fatalf("transport calls = %d; want 2", len(fixture.transport.sent))
	}
}

func TestSendMessageReplaysAFailedOrUnfinishedAttempt(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	mailbox := fixture.mailbox(t, 1, "hello")
	input := SendInput{To: []string{"x@example.com"}, Subject: "s", Text: "b", IdempotencyKey: "k1"}

	fixture.transport.err = &mailtransport.Error{Code: "transport.unreachable"}
	failed, _, err := fixture.service.SendMessage(ctx, as(1), mailbox.ID, input)
	if !errors.Is(err, ErrMailDeliveryFailed) {
		t.Fatalf("first send error = %v", err)
	}

	// The provider may have accepted the first attempt, so the same key must not send again
	// even now that the provider would answer.
	fixture.transport.err = nil
	again, created, err := fixture.service.SendMessage(ctx, as(1), mailbox.ID, input)
	if !errors.Is(err, ErrMailDeliveryFailed) || created || again.ID != failed.ID || again.FailureCode != "transport.unreachable" {
		t.Fatalf("replay = %+v, created %v, err %v", again, created, err)
	}
	if len(fixture.transport.sent) != 1 {
		t.Fatalf("transport calls = %d; want 1", len(fixture.transport.sent))
	}

	fixture.repo.messages[failed.ID].Status = domain.MailStatusQueued
	if _, _, err := fixture.service.SendMessage(ctx, as(1), mailbox.ID, input); !errors.Is(err, ErrMailSendInProgress) {
		t.Fatalf("unfinished attempt error = %v; want ErrMailSendInProgress", err)
	}
}

func TestSendMessageSurvivesAConcurrentRequestWithTheSameKey(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	mailbox := fixture.mailbox(t, 1, "hello")
	input := SendInput{To: []string{"x@example.com"}, Subject: "s", Text: "b", IdempotencyKey: "race"}
	first, _, err := fixture.service.SendMessage(ctx, as(1), mailbox.ID, input)
	if err != nil {
		t.Fatalf("first send: %v", err)
	}

	// The second request misses the lookup and loses the insert to the unique index.
	fixture.repo.hideIdempotencyLookup = true
	again, created, err := fixture.service.SendMessage(ctx, as(1), mailbox.ID, input)
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("racing send = id %d, created %v, err %v", again.ID, created, err)
	}
	if len(fixture.transport.sent) != 1 || len(fixture.repo.messages) != 1 {
		t.Fatalf("transport calls = %d, messages = %d; want 1 and 1", len(fixture.transport.sent), len(fixture.repo.messages))
	}
}

func TestSendMessageRejectsAnUnusableIdempotencyKey(t *testing.T) {
	fixture := newServiceFixture(t)
	mailbox := fixture.mailbox(t, 1, "hello")
	for _, key := range []string{"has space", "非ascii", strings.Repeat("k", 256), "line\nbreak"} {
		input := SendInput{To: []string{"x@example.com"}, Subject: "s", Text: "b", IdempotencyKey: key}
		if _, _, err := fixture.service.SendMessage(context.Background(), as(1), mailbox.ID, input); !errors.Is(err, ErrMailIdempotencyKeyInvalid) {
			t.Fatalf("key %q error = %v; want ErrMailIdempotencyKeyInvalid", key, err)
		}
	}
	if len(fixture.transport.sent) != 0 {
		t.Fatalf("an invalid key reached the provider")
	}
}

func TestSendFromAddressUsesOnlyTheOrganizationsMailbox(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	fixture.mailbox(t, 1, "mine")
	fixture.mailbox(t, 2, "theirs")
	input := SendInput{To: []string{"x@example.com"}, Subject: "s", Text: "b"}

	message, created, err := fixture.service.SendFromAddress(ctx, as(1), "Mine@Mail.Example", input)
	if err != nil || !created || message.FromAddress != "mine@mail.example" {
		t.Fatalf("SendFromAddress = %+v, created %v, err %v", message, created, err)
	}
	for _, from := range []string{"theirs@mail.example", "nobody@mail.example"} {
		if _, _, err := fixture.service.SendFromAddress(ctx, as(1), from, input); !errors.Is(err, ErrMailboxNotFound) {
			t.Fatalf("from %q error = %v; want ErrMailboxNotFound", from, err)
		}
	}
	if _, _, err := fixture.service.SendFromAddress(ctx, as(1), "Mine <mine@mail.example>", input); !errors.Is(err, ErrMailMessageInvalid) {
		t.Fatalf("display-name from error = %v", err)
	}
	if len(fixture.transport.sent) != 1 {
		t.Fatalf("transport calls = %d; want 1", len(fixture.transport.sent))
	}

	if found, err := fixture.service.GetOutboundMessage(ctx, as(1), message.ID); err != nil || found.ID != message.ID {
		t.Fatalf("GetOutboundMessage = %+v, %v", found, err)
	}
	if _, err := fixture.service.GetOutboundMessage(ctx, as(2), message.ID); !errors.Is(err, ErrMailMessageNotFound) {
		t.Fatalf("another user's lookup error = %v", err)
	}
}

func TestSendFingerprintSeparatesFieldBoundaries(t *testing.T) {
	a := sendFingerprint([]string{"a@x.io"}, nil, "ab", "c", "", "", "")
	b := sendFingerprint([]string{"a@x.io"}, nil, "a", "bc", "", "", "")
	if a == b {
		t.Fatal("moving text between subject and body kept the same fingerprint")
	}
}

func TestListLogValidatesFiltersAndTrimsTheQuery(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	mine := fixture.mailbox(t, 1, "mine")
	fixture.mailbox(t, 2, "theirs")
	if _, _, err := fixture.service.SendMessage(ctx, as(1), mine.ID, SendInput{To: []string{"x@example.com"}, Subject: "s", Text: "b"}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	messages, total, err := fixture.service.ListLog(ctx, as(1), domain.MailLogFilter{Query: "  receipt  "})
	if err != nil || total != 1 || len(messages) != 1 {
		t.Fatalf("ListLog = %d messages, total %d, err %v", len(messages), total, err)
	}
	if fixture.repo.lastLogFilter.Query != "receipt" {
		t.Fatalf("query = %q; want it trimmed", fixture.repo.lastLogFilter.Query)
	}
	if _, total, _ := fixture.service.ListLog(ctx, as(2), domain.MailLogFilter{}); total != 0 {
		t.Fatalf("another user sees %d messages", total)
	}

	invalid := []domain.MailLogFilter{
		{Direction: "sideways"},
		{Status: "delivered"},
		{Query: strings.Repeat("q", 201)},
		{Query: "bad\x00query"},
	}
	for _, filter := range invalid {
		if _, _, err := fixture.service.ListLog(ctx, as(1), filter); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("filter %+v error = %v; want ErrInvalidInput", filter, err)
		}
	}
}

func TestListMessagesValidatesFiltersAndForwardsSearchAndUnread(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	mine := fixture.mailbox(t, 1, "mine")

	unreadInbound, _, err := fixture.service.ReceiveInbound(ctx, fixture.delivery("mine@mail.example", crlf(
		"From: Alice <alice@sender.test>",
		"To: mine@mail.example",
		"Subject: Invoice #42",
		"Message-ID: <in-search-1@sender.test>",
		"",
		"Body.",
	)))
	if err != nil {
		t.Fatalf("ReceiveInbound: %v", err)
	}
	readInbound, _, err := fixture.service.ReceiveInbound(ctx, fixture.delivery("mine@mail.example", crlf(
		"From: Billing <billing@sender.test>",
		"To: mine@mail.example",
		"Subject: Invoice #43 paid",
		"Message-ID: <in-search-2@sender.test>",
		"",
		"Body.",
	)))
	if err != nil {
		t.Fatalf("ReceiveInbound: %v", err)
	}
	if _, err = fixture.service.SetMessageRead(ctx, as(1), mine.ID, readInbound.ID, true); err != nil {
		t.Fatalf("SetMessageRead: %v", err)
	}
	if _, _, err = fixture.service.SendMessage(ctx, as(1), mine.ID, SendInput{
		To: []string{"bob@example.com"}, Cc: []string{"carol@example.org"}, Subject: "Receipt", Text: "Thanks",
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	messages, total, err := fixture.service.ListMessages(ctx, as(1), mine.ID, domain.MailMessageFilter{Query: "invoice"})
	if err != nil || total != 2 || len(messages) != 2 {
		t.Fatalf("ListMessages(query) = %d messages, total %d, err %v", len(messages), total, err)
	}
	if fixture.repo.lastMessageFilter.Query != "invoice" {
		t.Fatalf("forwarded query = %q; want %q", fixture.repo.lastMessageFilter.Query, "invoice")
	}
	if fixture.repo.lastMessageReader != as(1).MembershipID {
		t.Fatalf("reader = %d; want the viewer's membership", fixture.repo.lastMessageReader)
	}

	unreadOnly := true
	unreadMessages, total, err := fixture.service.ListMessages(ctx, as(1), mine.ID, domain.MailMessageFilter{Unread: &unreadOnly})
	if err != nil || total != 1 || len(unreadMessages) != 1 || unreadMessages[0].ID != unreadInbound.ID {
		t.Fatalf("ListMessages(unread=true) = %+v, total %d, err %v", unreadMessages, total, err)
	}

	readOnly := false
	readMessages, total, err := fixture.service.ListMessages(ctx, as(1), mine.ID, domain.MailMessageFilter{Unread: &readOnly})
	if err != nil || total != 2 || len(readMessages) != 2 {
		t.Fatalf("ListMessages(unread=false) = %+v, total %d, err %v", readMessages, total, err)
	}

	// 200 multi-byte runes (>200 UTF-8 bytes) fit within the 200-character limit.
	if _, _, err := fixture.service.ListMessages(ctx, as(1), mine.ID, domain.MailMessageFilter{Query: strings.Repeat("é", 200)}); err != nil {
		t.Fatalf("200-rune UTF-8 query error = %v; want nil", err)
	}

	for _, filter := range []domain.MailMessageFilter{
		{Direction: "sideways"},
		{Query: strings.Repeat("q", 201)},
		{Query: strings.Repeat("é", 201)},
		// The handler trims the text; untrimmed text reaching the service is refused.
		{Query: "  invoice  "},
		{Query: "inv\x00oice"},
		{Query: "inv\noice"},
		{Query: string([]byte{0xff})},
	} {
		if _, _, err := fixture.service.ListMessages(ctx, as(1), mine.ID, filter); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("filter %+v error = %v; want ErrInvalidInput", filter, err)
		}
	}
}

func TestStatsCoversEveryDayOfTheWindow(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.now = time.Date(2026, 11, 16, 15, 30, 0, 0, time.UTC)
	fixture.mailbox(t, 1, "counted")
	fixture.repo.stats = []domain.MailDayStats{
		{Day: time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC), Sent: 4, Failed: 1, Received: 2},
	}

	stats, err := fixture.service.Stats(context.Background(), as(1), 3)
	if err != nil || len(stats) != 3 {
		t.Fatalf("Stats = %v, %v", stats, err)
	}
	if want := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC); !fixture.repo.lastStatsSince.Equal(want) {
		t.Fatalf("window starts %v; want %v", fixture.repo.lastStatsSince, want)
	}
	if stats[0].Sent != 0 || !stats[0].Day.Equal(time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("first day = %+v; want an empty 14th", stats[0])
	}
	if stats[1].Sent != 4 || stats[1].Failed != 1 || stats[1].Received != 2 {
		t.Fatalf("second day = %+v", stats[1])
	}
	if !stats[2].Day.Equal(time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)) || stats[2].Received != 0 {
		t.Fatalf("last day = %+v; want an empty today", stats[2])
	}

	for _, days := range []int{0, -1, MaxStatsDays + 1} {
		if _, err := fixture.service.Stats(context.Background(), as(1), days); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("days %d error = %v; want ErrInvalidInput", days, err)
		}
	}
}

func TestSendMessageRecordsWhatTheProviderSaidPerRecipient(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	mailbox := fixture.mailbox(t, 1, "hello")
	fixture.transport.result = mailtransport.Result{
		MessageID:  "prov-1@mail.example",
		Delivered:  []string{"Bob@Example.com"},
		Queued:     []string{"amy@example.org"},
		Bounced:    []string{"gone@example.net"},
		Suppressed: []string{"blocked@example.net"},
	}

	message, _, err := fixture.service.SendMessage(ctx, as(1), mailbox.ID, SendInput{
		To: []string{"bob@example.com", "amy@example.org", "gone@example.net", "blocked@example.net"}, Subject: "s", Text: "b",
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if message.MessageID != "prov-1@mail.example" || fixture.repo.messages[message.ID].MessageID != "prov-1@mail.example" {
		t.Fatalf("provider message id not stored: %q", message.MessageID)
	}
	// The worst current state among the recipients is what a listing shows.
	if message.LastEvent != domain.MailEventBounced {
		t.Fatalf("summary = %q; want bounced", message.LastEvent)
	}
	detail, err := fixture.service.GetMessage(ctx, as(1), mailbox.ID, message.ID)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	got := map[string]domain.MailEventType{}
	for _, event := range detail.Events {
		got[event.Recipient] = event.Type
	}
	want := map[string]domain.MailEventType{
		"":                    domain.MailEventAccepted,
		"bob@example.com":     domain.MailEventDelivered,
		"gone@example.net":    domain.MailEventBounced,
		"blocked@example.net": domain.MailEventRejected,
	}
	if len(got) != len(want) {
		t.Fatalf("events = %v; want %v (a queued recipient has no event yet)", got, want)
	}
	for recipient, eventType := range want {
		if got[recipient] != eventType {
			t.Fatalf("event for %q = %q; want %q", recipient, got[recipient], eventType)
		}
	}
}

func TestSendMessageRecordsRefusalsEvenWhenNobodyIsAccepted(t *testing.T) {
	fixture := newServiceFixture(t)
	mailbox := fixture.mailbox(t, 1, "hello")
	fixture.transport.result = mailtransport.Result{Suppressed: []string{"blocked@example.net"}}
	fixture.transport.err = &mailtransport.Error{Code: "transport.suppressed", Permanent: true}

	message, _, err := fixture.service.SendMessage(context.Background(), as(1), mailbox.ID, SendInput{To: []string{"blocked@example.net"}, Subject: "s", Text: "b"})
	if !errors.Is(err, ErrMailDeliveryFailed) || message.Status != domain.MailStatusFailed {
		t.Fatalf("send = %+v, %v; want a failed message", message, err)
	}
	events := fixture.repo.events[message.ID]
	if len(events) != 1 || events[0].Type != domain.MailEventRejected || events[0].Detail != "suppressed" {
		t.Fatalf("events = %+v; want one rejected event and no accepted event", events)
	}
}

func deliveryEventBody(eventType, eventID, messageID, recipient string) []byte {
	return []byte(`{"type":"cf.email.sending.message.` + eventType + `","source":{"type":"email.sending","domain":"mail.example"},` +
		`"payload":{"eventId":"` + eventID + `","messageId":"` + messageID + `","sender":"hello@mail.example","recipient":"` + recipient + `",` +
		`"subject":"Private subject","terminal":true,"delivery":{"status":"` + eventType + `","smtpStatusCode":550,"smtpEnhancedStatusCode":"5.1.1",` +
		`"smtpResponse":"550 5.1.1 The email account that you tried to reach does not exist <secret free text>"},` +
		`"bounce":{"type":"hard","classification":"permanent_failure","reason":"Mailbox does not exist!"}},` +
		`"metadata":{"eventSchemaVersion":1,"eventTimestamp":"2027-01-15T08:00:05Z"}}`)
}

func (f *serviceFixture) signedEvent(raw []byte) DeliveryEvent {
	return DeliveryEvent{Raw: raw, Signature: signedHeader(f.now, deliveryEventScope, raw)}
}

func TestRecordDeliveryEventUpdatesTheMessageOnce(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	mailbox := fixture.mailbox(t, 1, "hello")
	fixture.transport.result = mailtransport.Result{MessageID: "prov-2@mail.example", Queued: []string{"bob@example.com"}}
	sent, _, err := fixture.service.SendMessage(ctx, as(1), mailbox.ID, SendInput{To: []string{"bob@example.com"}, Subject: "s", Text: "b"})
	if err != nil || sent.LastEvent != domain.MailEventAccepted {
		t.Fatalf("send = %+v, %v; want an accepted message", sent, err)
	}

	raw := deliveryEventBody("bounced", "evt-1", "<prov-2@mail.example>", "Bob@Example.com")
	result, err := fixture.service.RecordDeliveryEvent(ctx, fixture.signedEvent(raw))
	if err != nil || !result.Recorded {
		t.Fatalf("RecordDeliveryEvent = %+v, err %v", result, err)
	}
	if result.Kind != DeliveryEventKindMailbox || result.MessageID != sent.ID {
		t.Fatalf("result = %+v; want mailbox message %d", result, sent.ID)
	}
	if message, _ := fixture.service.GetMessage(ctx, as(1), mailbox.ID, sent.ID); message == nil || message.LastEvent != domain.MailEventBounced {
		t.Fatalf("message = %+v; want message %d summarized as bounced", message, sent.ID)
	}
	event := result.Event
	if event.Type != domain.MailEventBounced || event.Recipient != "bob@example.com" {
		t.Fatalf("event = %+v", event)
	}
	// Only short machine-readable tokens are kept; the SMTP response line and the free-form
	// reason are provider text and must not be stored.
	if event.Detail != "5.1.1 permanent_failure" {
		t.Fatalf("detail = %q; want %q", event.Detail, "5.1.1 permanent_failure")
	}
	if !event.OccurredAt.Equal(time.Date(2027, 1, 15, 8, 0, 5, 0, time.UTC)) {
		t.Fatalf("occurred at %v; want the provider's timestamp", event.OccurredAt)
	}

	result, err = fixture.service.RecordDeliveryEvent(ctx, fixture.signedEvent(raw))
	if err != nil || result.Recorded {
		t.Fatalf("redelivery = recorded %v, err %v; want an acknowledged duplicate", result.Recorded, err)
	}
	if len(fixture.repo.events[sent.ID]) != 2 {
		t.Fatalf("events = %d; want accepted + bounced", len(fixture.repo.events[sent.ID]))
	}
}

func TestRecordDeliveryEventRejectsUntrustedOrUnusableEvents(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx := context.Background()
	mailbox := fixture.mailbox(t, 1, "hello")
	fixture.transport.result = mailtransport.Result{MessageID: "prov-3@mail.example"}
	if _, _, err := fixture.service.SendMessage(ctx, as(1), mailbox.ID, SendInput{To: []string{"bob@example.com"}, Subject: "s", Text: "b"}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	valid := deliveryEventBody("delivered", "evt-2", "prov-3@mail.example", "bob@example.com")

	unsigned := DeliveryEvent{Raw: valid, Signature: "t=1,v1=00"}
	if _, err := fixture.service.RecordDeliveryEvent(ctx, unsigned); !errors.Is(err, ErrMailInboundSignature) {
		t.Fatalf("unsigned error = %v", err)
	}
	// A signature made for inbound mail must not be accepted for an event with the same bytes.
	crossed := DeliveryEvent{Raw: valid, Signature: signedHeader(fixture.now, "hello@mail.example", valid)}
	if _, err := fixture.service.RecordDeliveryEvent(ctx, crossed); !errors.Is(err, ErrMailInboundSignature) {
		t.Fatalf("inbound signature reused for an event: error = %v", err)
	}

	invalid := map[string][]byte{
		"not json":          []byte("nope"),
		"unknown type":      deliveryEventBody("opened", "evt-3", "prov-3@mail.example", "bob@example.com"),
		"routing event":     []byte(strings.Replace(string(valid), "cf.email.sending.message.", "cf.email.routing.message.", 1)),
		"missing event id":  deliveryEventBody("delivered", "", "prov-3@mail.example", "bob@example.com"),
		"missing message":   deliveryEventBody("delivered", "evt-4", "", "bob@example.com"),
		"invalid recipient": deliveryEventBody("delivered", "evt-5", "prov-3@mail.example", "not-an-address"),
	}
	for name, raw := range invalid {
		if _, err := fixture.service.RecordDeliveryEvent(ctx, fixture.signedEvent(raw)); !errors.Is(err, ErrMailDeliveryEventInvalid) {
			t.Fatalf("%s: error = %v; want ErrMailDeliveryEventInvalid", name, err)
		}
	}
	oversized := make([]byte, MaxDeliveryEventBytes+1)
	if _, err := fixture.service.RecordDeliveryEvent(ctx, fixture.signedEvent(oversized)); !errors.Is(err, ErrMailDeliveryEventInvalid) {
		t.Fatalf("oversized error = %v", err)
	}

	unknown := deliveryEventBody("delivered", "evt-6", "someone-elses@other.example", "bob@example.com")
	if _, err := fixture.service.RecordDeliveryEvent(ctx, fixture.signedEvent(unknown)); !errors.Is(err, ErrMailMessageNotFound) {
		t.Fatalf("unknown message error = %v; want ErrMailMessageNotFound", err)
	}

	fixture.service.settings.InboundSecret = ""
	if _, err := fixture.service.RecordDeliveryEvent(ctx, fixture.signedEvent(valid)); !errors.Is(err, ErrMailInboundDisabled) {
		t.Fatalf("disabled error = %v", err)
	}
}

func TestSummarizeMailEventsUsesEachRecipientsLatestState(t *testing.T) {
	at := func(minute int) time.Time { return time.Date(2027, 1, 1, 0, minute, 0, 0, time.UTC) }
	cases := []struct {
		name   string
		events []domain.MailEvent
		want   domain.MailEventType
	}{
		{"nothing recorded", nil, ""},
		{"accepted only", []domain.MailEvent{{Type: domain.MailEventAccepted, OccurredAt: at(0)}}, domain.MailEventAccepted},
		{"deferred then delivered is delivered", []domain.MailEvent{
			{Type: domain.MailEventAccepted, OccurredAt: at(0)},
			{Type: domain.MailEventDeferred, Recipient: "a@x.io", OccurredAt: at(1)},
			{Type: domain.MailEventDelivered, Recipient: "a@x.io", OccurredAt: at(2)},
		}, domain.MailEventDelivered},
		{"events arriving out of order", []domain.MailEvent{
			{Type: domain.MailEventDelivered, Recipient: "a@x.io", OccurredAt: at(2)},
			{Type: domain.MailEventDeferred, Recipient: "a@x.io", OccurredAt: at(1)},
		}, domain.MailEventDelivered},
		{"one bounce outweighs a delivery", []domain.MailEvent{
			{Type: domain.MailEventDelivered, Recipient: "a@x.io", OccurredAt: at(1)},
			{Type: domain.MailEventBounced, Recipient: "b@x.io", OccurredAt: at(1)},
		}, domain.MailEventBounced},
		{"a complaint after delivery", []domain.MailEvent{
			{Type: domain.MailEventDelivered, Recipient: "a@x.io", OccurredAt: at(1)},
			{Type: domain.MailEventComplained, Recipient: "a@x.io", OccurredAt: at(9)},
		}, domain.MailEventComplained},
	}
	for _, test := range cases {
		if got := domain.SummarizeMailEvents(test.events); got != test.want {
			t.Fatalf("%s: summary = %q; want %q", test.name, got, test.want)
		}
	}
}

var ownMessageIDPattern = regexp.MustCompile(`^[0-9a-f]{32}@example\.com$`)

func TestOutboundGetsOwnRFCMessageID(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.service.settings.Domain = "example.com"
	mailbox := fixture.mailbox(t, 1, "hello")
	var sentID, storedAtSend string
	fixture.service.transport = transportFunc(func(_ context.Context, mail mailtransport.Message) error {
		sentID = mail.MessageID
		for _, stored := range fixture.repo.messages {
			if stored.Direction == domain.MailDirectionOutbound && stored.Status == domain.MailStatusQueued {
				storedAtSend = stored.RFCMessageID
			}
		}
		return nil
	})

	message, _, err := fixture.service.SendMessage(context.Background(), as(1), mailbox.ID,
		SendInput{To: []string{"bob@example.org"}, Subject: "Hi", Text: "Hello"})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if !ownMessageIDPattern.MatchString(message.RFCMessageID) {
		t.Fatalf("rfc_message_id = %q; want 32 lowercase hex characters at the sending domain", message.RFCMessageID)
	}
	if storedAtSend != message.RFCMessageID {
		t.Fatalf("stored before the provider call = %q; want %q", storedAtSend, message.RFCMessageID)
	}
	if sentID != message.RFCMessageID {
		t.Fatalf("provider got Message-ID %q; want %q", sentID, message.RFCMessageID)
	}
	if message.ThreadID != message.ID || fixture.repo.messages[message.ID].ThreadID != message.ID {
		t.Fatalf("thread_id = %d; want a new thread %d", message.ThreadID, message.ID)
	}

	other, _, err := fixture.service.SendMessage(context.Background(), as(1), mailbox.ID,
		SendInput{To: []string{"bob@example.org"}, Subject: "Hi", Text: "Hello"})
	if err != nil {
		t.Fatalf("second SendMessage: %v", err)
	}
	if other.RFCMessageID == message.RFCMessageID || !ownMessageIDPattern.MatchString(other.RFCMessageID) {
		t.Fatalf("second rfc_message_id = %q; want a new id", other.RFCMessageID)
	}
}

func TestNewRFCMessageIDUsesTheSendingDomain(t *testing.T) {
	id := newRFCMessageID("example.com")
	if !ownMessageIDPattern.MatchString(id) {
		t.Fatalf("newRFCMessageID = %q", id)
	}
}

func TestIdempotentReplayKeepsRFCMessageID(t *testing.T) {
	fixture := newServiceFixture(t)
	mailbox := fixture.mailbox(t, 1, "hello")
	input := SendInput{To: []string{"bob@example.org"}, Subject: "Hi", Text: "Hello", IdempotencyKey: "replay-1"}

	first, created, err := fixture.service.SendMessage(context.Background(), as(1), mailbox.ID, input)
	if err != nil || !created || first.RFCMessageID == "" {
		t.Fatalf("first = %+v, created %v, err %v; want a stored message with its own id", first, created, err)
	}
	replayed, created, err := fixture.service.SendMessage(context.Background(), as(1), mailbox.ID, input)
	if err != nil || created || replayed.RFCMessageID != first.RFCMessageID {
		t.Fatalf("replay rfc_message_id = %q, created %v, err %v; want %q", replayed.RFCMessageID, created, err, first.RFCMessageID)
	}
	// A concurrent request that stored the message first is answered from the stored row too.
	fixture.repo.hideIdempotencyLookup = true
	raced, created, err := fixture.service.SendMessage(context.Background(), as(1), mailbox.ID, input)
	if err != nil || created || raced.RFCMessageID != first.RFCMessageID {
		t.Fatalf("raced rfc_message_id = %q, created %v, err %v; want %q", raced.RFCMessageID, created, err, first.RFCMessageID)
	}
	if len(fixture.transport.sent) != 1 {
		t.Fatalf("provider calls = %d; want 1", len(fixture.transport.sent))
	}
}

// receiveReply delivers one signed inbound message to the mailbox through the service.
func (f *queueFixture) receiveReply(t *testing.T, mailbox *domain.Mailbox, messageID string, headers ...string) *domain.MailMessage {
	t.Helper()
	lines := []string{
		"From: Alice <alice@sender.test>", "To: " + mailbox.Address, "Subject: Re: Hello", "Message-ID: <" + messageID + ">",
	}
	lines = append(lines, headers...)
	lines = append(lines, "", "Reply body.")
	raw := crlf(lines...)
	message, created, err := f.service.ReceiveInbound(context.Background(), InboundDelivery{
		Recipient: mailbox.Address, Signature: signedHeader(f.clock, mailbox.Address, raw), Raw: raw,
	})
	if err != nil || !created {
		t.Fatalf("ReceiveInbound(%s) = created %v, err %v", messageID, created, err)
	}
	return message
}

func (f *queueFixture) storedThread(t *testing.T, id uint) uint {
	t.Helper()
	stored := f.stored(t, id)
	if stored.ThreadID == nil {
		t.Fatalf("message %d has no thread_id", id)
	}
	return *stored.ThreadID
}

func TestInboundReplyUsesProviderAssignedMessageID(t *testing.T) {
	f := newQueueFixture(t)
	const wireID = "assigned@provider.example"
	f.transport.result = mailtransport.Result{MessageID: wireID, RFCMessageID: wireID}
	sent := f.mustSend(t, "Hello", false)
	if got := f.stored(t, sent.ID).RFCMessageID; got != wireID {
		t.Fatalf("stored RFC Message-ID = %q, want %q", got, wireID)
	}
	reply := f.receiveReply(t, f.team, "reply@sender.test", "In-Reply-To: <"+wireID+">")
	if got := f.storedThread(t, reply.ID); got != sent.ID {
		t.Fatalf("reply thread = %d, want original thread %d", got, sent.ID)
	}
}

func TestInboundReplyToOutboundJoinsThread(t *testing.T) {
	f := newQueueFixture(t)
	sent := f.mustSend(t, "Hello", false)
	if sent.RFCMessageID == "" || f.storedThread(t, sent.ID) != sent.ID {
		t.Fatalf("outbound = rfc %q, thread %d; want its own id and a new thread", sent.RFCMessageID, f.storedThread(t, sent.ID))
	}
	if stored := f.stored(t, sent.ID); stored.RFCMessageID != sent.RFCMessageID {
		t.Fatalf("stored rfc_message_id = %q; want %q", stored.RFCMessageID, sent.RFCMessageID)
	}

	reply := f.receiveReply(t, f.team, "reply-1@sender.test", "In-Reply-To: <"+sent.RFCMessageID+">")
	if reply.ThreadID != sent.ID || f.storedThread(t, reply.ID) != sent.ID {
		t.Fatalf("reply thread = %d; want the outbound thread %d", reply.ThreadID, sent.ID)
	}
	if stored := f.stored(t, reply.ID); stored.RFCMessageID != "reply-1@sender.test" {
		t.Fatalf("inbound rfc_message_id = %q; want the sender's Message-ID", stored.RFCMessageID)
	}

	// A reply that names the thread only in References joins it too.
	viaReferences := f.receiveReply(t, f.team, "reply-2@sender.test",
		"In-Reply-To: <unknown@elsewhere.test>", "References: <"+sent.RFCMessageID+"> <unknown@elsewhere.test>")
	if f.storedThread(t, viaReferences.ID) != sent.ID {
		t.Fatalf("References-only reply thread = %d; want %d", f.storedThread(t, viaReferences.ID), sent.ID)
	}

	// The same subject alone never joins a thread.
	fresh := f.receiveReply(t, f.team, "fresh@sender.test")
	if f.storedThread(t, fresh.ID) != fresh.ID {
		t.Fatalf("unrelated message thread = %d; want a new thread %d", f.storedThread(t, fresh.ID), fresh.ID)
	}
}

// Review Focus 2: a reply naming a message of another mailbox starts its own thread.
func TestInboundReplyToOtherMailboxStartsNewThread(t *testing.T) {
	f := newQueueFixture(t)
	sent := f.mustSend(t, "Hello", false)
	reply := f.receiveReply(t, f.personal, "cross@sender.test",
		"In-Reply-To: <"+sent.RFCMessageID+">", "References: <"+sent.RFCMessageID+">")
	if f.storedThread(t, reply.ID) != reply.ID || reply.ThreadID != reply.ID {
		t.Fatalf("cross-mailbox reply thread = %d; want a new thread %d, not team's %d",
			f.storedThread(t, reply.ID), reply.ID, sent.ID)
	}
	found, err := f.repo.(*repository).FindThreadID(context.Background(), f.personal.ID, []string{sent.RFCMessageID})
	if err != nil || found != 0 {
		t.Fatalf("FindThreadID in another mailbox = %d, %v; want 0", found, err)
	}
}

func TestReplyReferencesChain(t *testing.T) {
	f := newQueueFixture(t)
	sent := f.mustSend(t, "Hello", false)
	reply := f.receiveReply(t, f.team, "reply-1@sender.test",
		"In-Reply-To: <"+sent.RFCMessageID+">", "References: <"+sent.RFCMessageID+">")

	before := len(f.transport.sent)
	answer, _, err := f.service.SendMessage(context.Background(), f.b, f.team.ID, SendInput{
		To: []string{"alice@sender.test"}, Subject: "Re: Hello", Text: "Thanks", InReplyTo: "<reply-1@sender.test>",
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	wantChain := sent.RFCMessageID + " reply-1@sender.test"
	if got := strings.Join(f.transport.sent[before].References, " "); got != wantChain {
		t.Fatalf("References sent = %q; want the parent's References plus its Message-ID %q", got, wantChain)
	}
	if f.transport.sent[before].InReplyTo != "reply-1@sender.test" {
		t.Fatalf("In-Reply-To sent = %q", f.transport.sent[before].InReplyTo)
	}
	if stored := f.stored(t, answer.ID); stored.ReferencesHeader != wantChain {
		t.Fatalf("stored References = %q; want %q", stored.ReferencesHeader, wantChain)
	}
	if f.storedThread(t, answer.ID) != sent.ID || f.storedThread(t, reply.ID) != sent.ID {
		t.Fatalf("answer thread = %d; want %d", f.storedThread(t, answer.ID), sent.ID)
	}

	// A long chain keeps only the newest ids, ending with the parent.
	var long []string
	for i := 0; i < 25; i++ {
		long = append(long, fmt.Sprintf("<old-%d@sender.test>", i))
	}
	f.receiveReply(t, f.team, "long@sender.test", "References: "+strings.Join(long, " "))
	before = len(f.transport.sent)
	if _, _, err := f.service.SendMessage(context.Background(), f.b, f.team.ID, SendInput{
		To: []string{"alice@sender.test"}, Subject: "Re: Long", Text: "Thanks", InReplyTo: "long@sender.test",
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	chain := f.transport.sent[before].References
	if len(chain) != 20 || chain[len(chain)-1] != "long@sender.test" || chain[0] != "old-6@sender.test" {
		t.Fatalf("long References = %v; want the last 20 ids ending with the parent", chain)
	}

	// A reply to a message this mailbox does not have still references it.
	before = len(f.transport.sent)
	if _, _, err := f.service.SendMessage(context.Background(), f.b, f.team.ID, SendInput{
		To: []string{"alice@sender.test"}, Subject: "Re: ?", Text: "Thanks", InReplyTo: "unknown@elsewhere.test",
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if got := strings.Join(f.transport.sent[before].References, " "); got != "unknown@elsewhere.test" {
		t.Fatalf("References for an unknown parent = %q; want just In-Reply-To", got)
	}
}

// A message sent to its own mailbox is stored twice with one Message-ID: the inbound copy joins the
// outbound thread, and a reply to that id joins it too.
func TestSelfSentMessageStaysInOneThread(t *testing.T) {
	f := newQueueFixture(t)
	sent, _, err := f.service.SendMessage(context.Background(), f.b, f.team.ID,
		SendInput{To: []string{f.team.Address}, Subject: "Note to self", Text: "body"})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	copied := f.receiveReply(t, f.team, sent.RFCMessageID)
	if f.storedThread(t, copied.ID) != sent.ID {
		t.Fatalf("inbound copy thread = %d; want the outbound thread %d", f.storedThread(t, copied.ID), sent.ID)
	}
	reply := f.receiveReply(t, f.team, "self-reply@sender.test", "In-Reply-To: <"+sent.RFCMessageID+">")
	if f.storedThread(t, reply.ID) != sent.ID {
		t.Fatalf("reply thread = %d; want %d", f.storedThread(t, reply.ID), sent.ID)
	}
}

// A forged inbound mail that reuses one of our Message-IDs never becomes the target of later
// replies, nor the parent whose References a reply copies.
func TestForgedMessageIDDoesNotCaptureReplies(t *testing.T) {
	f := newQueueFixture(t)
	sent := f.mustSend(t, "Hello", false)
	f.receiveReply(t, f.team, sent.RFCMessageID, "References: <evil@attacker.test>")

	reply := f.receiveReply(t, f.team, "real-reply@sender.test", "In-Reply-To: <"+sent.RFCMessageID+">")
	if f.storedThread(t, reply.ID) != sent.ID {
		t.Fatalf("reply thread = %d; want the original thread %d", f.storedThread(t, reply.ID), sent.ID)
	}

	before := len(f.transport.sent)
	if _, _, err := f.service.SendMessage(context.Background(), f.b, f.team.ID, SendInput{
		To: []string{"bob@example.com"}, Subject: "Re: Hello", Text: "again", InReplyTo: sent.RFCMessageID,
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if got := strings.Join(f.transport.sent[before].References, " "); got != sent.RFCMessageID {
		t.Fatalf("References = %q; want only the original %q, not the forged chain", got, sent.RFCMessageID)
	}
}

func TestNewOrganizationSendsWithinTheNewOrganizationCap(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.service.settings.NewOrganizationDailySendLimit = 1
	fixture.service.settings.NewOrganizationPeriod = 7 * 24 * time.Hour
	fixture.repo.organizationCreatedAt = map[uint]time.Time{
		1: fixture.now.Add(-time.Hour),
		2: fixture.now.Add(-8 * 24 * time.Hour),
	}
	input := SendInput{To: []string{"x@example.com"}, Subject: "s", Text: "b"}

	young := fixture.mailbox(t, 1, "young")
	if _, _, err := fixture.service.SendMessage(context.Background(), as(1), young.ID, input); err != nil {
		t.Fatalf("first send of the new organization: %v", err)
	}
	message, _, err := fixture.service.SendMessage(context.Background(), as(1), young.ID, input)
	if err != nil || message.QueuedReason != domain.MailQueuedForSendLimit {
		t.Fatalf("second send = %+v, %v; want it waiting at the new-organization cap", message, err)
	}
	if _, _, err := fixture.service.SendMessage(context.Background(), as(1), young.ID, input); !errors.Is(err, ErrMailSendLimitReached) {
		t.Fatalf("third send error = %v; want ErrMailSendLimitReached, the waiting cap follows the new-organization cap", err)
	}

	settled := fixture.mailbox(t, 2, "settled")
	for i := 0; i < 3; i++ {
		message, _, err := fixture.service.SendMessage(context.Background(), as(2), settled.ID, input)
		if err != nil || message.QueuedReason != "" {
			t.Fatalf("send %d of the settled organization = %+v, %v; want it sent under the lane limit", i+1, message, err)
		}
	}
}
