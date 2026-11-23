package mailbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
)

// maxClientSearchResults bounds what one mail app search returns.
const maxClientSearchResults = 10000

// clientPassword is the part of a password row signing in needs.
type clientPassword struct {
	ID             uint
	OrganizationID uint
	MembershipID   uint
	UserID         uint
	ScopeIMAP      bool `gorm:"column:scope_imap"`
	ScopeSMTP      bool `gorm:"column:scope_smtp"`
}

// clientViewer is a membership as a mail app acts as it, with the account email it may sign in with.
type clientViewer struct {
	Viewer       domain.OrganizationContext
	AccountEmail string
}

// mailClientRepository reads what mail apps need; the PostgreSQL repository provides it.
type mailClientRepository interface {
	// FindClientPassword finds a password that is not revoked by its secret hash, or answers
	// ErrMailAppPasswordNotFound.
	FindClientPassword(ctx context.Context, secretHash string) (*clientPassword, error)
	// FindClientViewer resolves a password's membership, or answers domain.ErrOrganizationNotFound.
	FindClientViewer(ctx context.Context, organizationID, membershipID, userID uint) (*clientViewer, error)
	// TouchAppPassword records a sign-in unless one was recorded after notBefore.
	TouchAppPassword(ctx context.Context, id uint, ip string, protocol domain.MailClientProtocol, at, notBefore time.Time) error
	// ValidClientPasswords lists, of ids, the passwords that may still sign in: not revoked, with a
	// scope (scopes never change, and each sign-in checked its protocol's), membership present with
	// the mail_clients feature, and the organization switch on.
	ValidClientPasswords(ctx context.Context, ids []uint) (map[uint]bool, error)
	// FolderExists reports whether the mailbox has the user folder.
	FolderExists(ctx context.Context, mailboxID, folderID uint) (bool, error)
	ClientFolderState(ctx context.Context, mailboxID uint, key string, membershipID uint) (*domain.MailClientFolderState, error)
	ClientMessages(ctx context.Context, mailboxID uint, key string, membershipID uint, uids []uint32) ([]domain.MailClientMessage, error)
	// ClientMessageIDs maps uids of the folder to message ids, skipping uids it does not hold.
	ClientMessageIDs(ctx context.Context, mailboxID uint, key string, uids []uint32) (map[uint32]uint, error)
	SearchClient(ctx context.Context, mailboxID uint, key string, membershipID uint, criteria domain.MailClientSearch) ([]uint32, error)
}

var _ mailClientRepository = (*repository)(nil)

func (r *repository) FindClientPassword(ctx context.Context, secretHash string) (*clientPassword, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var row clientPassword
	err := r.conn(ctx).Table("mail_app_passwords").
		Select("id, organization_id, membership_id, user_id, scope_imap, scope_smtp").
		Where("secret_hash = ? AND revoked_at IS NULL", secretHash).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrMailAppPasswordNotFound
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *repository) FindClientViewer(ctx context.Context, organizationID, membershipID, userID uint) (*clientViewer, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var row struct {
		OrganizationName  string
		OrganizationSlug  string
		Role              string
		CanUseBroadcasts  bool
		CanUseContacts    bool
		CanUseTemplates   bool
		CanUseAPIKeys     bool `gorm:"column:can_use_api_keys"`
		CanUseMailClients bool
		Email             string
	}
	err := r.conn(ctx).Table("organization_memberships AS memberships").
		Select(`organizations.name AS organization_name, organizations.slug AS organization_slug, memberships.role,
			memberships.can_use_broadcasts, memberships.can_use_contacts, memberships.can_use_templates,
			memberships.can_use_api_keys, memberships.can_use_mail_clients, users.email`).
		Joins("JOIN organizations ON organizations.id = memberships.organization_id").
		Joins("JOIN users ON users.id = memberships.user_id").
		Where("memberships.id = ? AND memberships.organization_id = ? AND memberships.user_id = ?", membershipID, organizationID, userID).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrOrganizationNotFound
	}
	if err != nil {
		return nil, err
	}
	return &clientViewer{
		Viewer: domain.OrganizationContext{
			OrganizationID: organizationID, OrganizationName: row.OrganizationName, OrganizationSlug: row.OrganizationSlug,
			MembershipID: membershipID, UserID: userID, Role: domain.OrganizationRole(row.Role),
			Features: domain.MemberFeatures{
				Broadcasts: row.CanUseBroadcasts, Contacts: row.CanUseContacts, Templates: row.CanUseTemplates,
				APIKeys: row.CanUseAPIKeys, MailClients: row.CanUseMailClients,
			},
		},
		AccountEmail: row.Email,
	}, nil
}

func (r *repository) TouchAppPassword(ctx context.Context, id uint, ip string, protocol domain.MailClientProtocol, at, notBefore time.Time) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	var lastIP *string
	if ip != "" {
		lastIP = &ip
	}
	return r.conn(ctx).Exec(`UPDATE mail_app_passwords SET last_used_at = ?, last_used_ip = ?, last_used_protocol = ?, updated_at = ?
		WHERE id = ? AND (last_used_at IS NULL OR last_used_at < ?)`, at, lastIP, string(protocol), at, id, notBefore).Error
}

func (r *repository) ValidClientPasswords(ctx context.Context, ids []uint) (map[uint]bool, error) {
	valid := make(map[uint]bool, len(ids))
	if len(ids) == 0 {
		return valid, nil
	}
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []struct {
		ID                uint
		Role              string
		CanUseMailClients bool
	}
	err := r.conn(ctx).Table("mail_app_passwords AS passwords").
		Select("passwords.id, memberships.role, memberships.can_use_mail_clients").
		Joins(`JOIN organization_memberships AS memberships ON memberships.id = passwords.membership_id
			AND memberships.organization_id = passwords.organization_id AND memberships.user_id = passwords.user_id`).
		Joins("LEFT JOIN mail_client_settings AS settings ON settings.organization_id = passwords.organization_id").
		Where("passwords.id IN ? AND passwords.revoked_at IS NULL AND (passwords.scope_imap OR passwords.scope_smtp) AND COALESCE(settings.access_enabled, true)", ids).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		features := domain.OrganizationRole(row.Role).EffectiveFeatures(domain.MemberFeatures{MailClients: row.CanUseMailClients})
		valid[row.ID] = features.Has(domain.MemberFeatureMailClients)
	}
	return valid, nil
}

func (r *repository) FolderExists(ctx context.Context, mailboxID, folderID uint) (bool, error) {
	if r.db == nil {
		return false, domain.ErrServiceUnavailable
	}
	var count int64
	err := r.conn(ctx).Model(&MailFolderPO{}).Where("id = ? AND mailbox_id = ?", folderID, mailboxID).Count(&count).Error
	return count > 0, err
}

// clientFolderMessages selects a folder's messages that every surface shows.
const clientFolderMessages = `FROM mail_imap_uids AS uids JOIN mail_messages ON mail_messages.id = uids.message_id
	WHERE uids.mailbox_id = @mailbox AND uids.folder_key = @key AND ` + unsealedMessage

// clientReadByMember and clientStarredByMember are the member's own read and star.
const (
	clientReadByMember = `EXISTS (SELECT 1 FROM mail_message_reads AS reads
		WHERE reads.message_id = mail_messages.id AND reads.membership_id = @member)`
	clientStarredByMember = `EXISTS (SELECT 1 FROM mail_message_stars AS stars
		WHERE stars.message_id = mail_messages.id AND stars.membership_id = @member)`
	clientAnswered = `(mail_messages.rfc_message_id <> '' AND EXISTS (SELECT 1 FROM mail_messages AS replies
		WHERE replies.mailbox_id = mail_messages.mailbox_id AND replies.direction = 'outbound'
		AND replies.in_reply_to = mail_messages.rfc_message_id))`
)

func (r *repository) ClientFolderState(ctx context.Context, mailboxID uint, key string, membershipID uint) (*domain.MailClientFolderState, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	db := r.conn(ctx)
	// A folder that never held mail gets its row now, so the UIDVALIDITY a mail app sees first
	// is the one the first message is numbered under.
	if err := db.Exec(`INSERT INTO mail_imap_folders (mailbox_id, folder_key, uidvalidity, uidnext, created_at)
		VALUES (?, ?, extract(epoch FROM clock_timestamp())::bigint, 1, clock_timestamp())
		ON CONFLICT (mailbox_id, folder_key) DO NOTHING`, mailboxID, key).Error; err != nil {
		return nil, err
	}
	// The UIDs are read before UIDNEXT, so UIDNEXT is always above every UID returned.
	var rows []struct {
		UID    uint32
		Unseen bool
	}
	params := map[string]any{"mailbox": mailboxID, "key": key, "member": membershipID}
	if err := db.Raw(`SELECT uids.uid, (mail_messages.direction = 'inbound' AND NOT `+clientReadByMember+`) AS unseen `+
		clientFolderMessages+` ORDER BY uids.uid`, params).Scan(&rows).Error; err != nil {
		return nil, err
	}
	var folder struct {
		Uidvalidity int64
		Uidnext     int64
	}
	if err := db.Raw(`SELECT uidvalidity, uidnext FROM mail_imap_folders WHERE mailbox_id = ? AND folder_key = ?`,
		mailboxID, key).Scan(&folder).Error; err != nil {
		return nil, err
	}
	state := &domain.MailClientFolderState{
		UIDValidity: uint32(folder.Uidvalidity), UIDNext: uint32(folder.Uidnext), UIDs: make([]uint32, 0, len(rows)),
	}
	for _, row := range rows {
		state.UIDs = append(state.UIDs, row.UID)
		if row.Unseen {
			state.Unseen++
		}
	}
	return state, nil
}

func (r *repository) ClientMessages(ctx context.Context, mailboxID uint, key string, membershipID uint, uids []uint32) ([]domain.MailClientMessage, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	if uids != nil && len(uids) == 0 {
		return nil, nil
	}
	query := `SELECT uids.uid, mail_messages.id AS message_id, mail_messages.created_at,
		(mail_messages.direction = 'outbound' OR ` + clientReadByMember + `) AS seen,
		` + clientStarredByMember + ` AS flagged, ` + clientAnswered + ` AS answered ` + clientFolderMessages
	params := map[string]any{"mailbox": mailboxID, "key": key, "member": membershipID}
	if uids != nil {
		query += ` AND uids.uid IN @uids`
		params["uids"] = uids
	}
	var rows []struct {
		UID       uint32
		MessageID uint
		CreatedAt time.Time
		Seen      bool
		Flagged   bool
		Answered  bool
	}
	if err := r.conn(ctx).Raw(query+` ORDER BY uids.uid`, params).Scan(&rows).Error; err != nil {
		return nil, err
	}
	messages := make([]domain.MailClientMessage, 0, len(rows))
	for _, row := range rows {
		messages = append(messages, domain.MailClientMessage{
			UID: row.UID, MessageID: row.MessageID, Seen: row.Seen, Flagged: row.Flagged, Answered: row.Answered,
			InternalDate: row.CreatedAt.UTC(),
		})
	}
	return messages, nil
}

func (r *repository) ClientMessageIDs(ctx context.Context, mailboxID uint, key string, uids []uint32) (map[uint32]uint, error) {
	ids := make(map[uint32]uint, len(uids))
	if len(uids) == 0 {
		return ids, nil
	}
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []struct {
		UID       uint32
		MessageID uint
	}
	if err := r.conn(ctx).Raw(`SELECT uids.uid, mail_messages.id AS message_id `+clientFolderMessages+` AND uids.uid IN @uids`,
		map[string]any{"mailbox": mailboxID, "key": key, "uids": uids}).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		ids[row.UID] = row.MessageID
	}
	return ids, nil
}

func (r *repository) SearchClient(ctx context.Context, mailboxID uint, key string, membershipID uint, criteria domain.MailClientSearch) ([]uint32, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	builder := &clientSearchBuilder{params: map[string]any{"mailbox": mailboxID, "key": key, "member": membershipID}}
	condition := builder.build(criteria)
	var uids []uint32
	err := r.conn(ctx).Raw(`SELECT uids.uid `+clientFolderMessages+` AND `+condition+
		fmt.Sprintf(` ORDER BY uids.uid LIMIT %d`, maxClientSearchResults), builder.params).Scan(&uids).Error
	return uids, err
}

// clientSearchBuilder turns a search into one SQL condition with named parameters.
type clientSearchBuilder struct {
	params map[string]any
}

func (b *clientSearchBuilder) param(value any) string {
	name := fmt.Sprintf("p%d", len(b.params))
	b.params[name] = value
	return "@" + name
}

// like matches value as a literal substring ignoring case.
func (b *clientSearchBuilder) like(column, value string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
	return column + ` ILIKE ` + b.param("%"+escaped+"%") + ` ESCAPE '\'`
}

func (b *clientSearchBuilder) anyLike(columns []string, value string) string {
	parts := make([]string, 0, len(columns))
	for _, column := range columns {
		parts = append(parts, b.like(column, value))
	}
	return "(" + strings.Join(parts, " OR ") + ")"
}

func (b *clientSearchBuilder) build(criteria domain.MailClientSearch) string {
	conditions := []string{"TRUE"}
	add := func(condition string) { conditions = append(conditions, condition) }
	if criteria.MatchNothing {
		add("FALSE")
	}
	for _, set := range criteria.UIDSets {
		ranges := make([]string, 0, len(set))
		for _, uidRange := range set {
			ranges = append(ranges, "uids.uid BETWEEN "+b.param(uidRange.Start)+" AND "+b.param(uidRange.Stop))
		}
		if len(ranges) == 0 {
			ranges = append(ranges, "FALSE")
		}
		add("(" + strings.Join(ranges, " OR ") + ")")
	}
	flag := func(value *bool, condition string) {
		if value == nil {
			return
		}
		if *value {
			add(condition)
		} else {
			add("NOT " + condition)
		}
	}
	flag(criteria.Seen, "(mail_messages.direction = 'outbound' OR "+clientReadByMember+")")
	flag(criteria.Flagged, clientStarredByMember)
	flag(criteria.Answered, clientAnswered)
	if criteria.Since != nil {
		add("mail_messages.created_at >= " + b.param(*criteria.Since))
	}
	if criteria.Before != nil {
		add("mail_messages.created_at < " + b.param(*criteria.Before))
	}
	for _, value := range criteria.From {
		add(b.anyLike([]string{"mail_messages.from_address", "mail_messages.from_name"}, value))
	}
	for _, value := range criteria.To {
		add(b.like("mail_messages.to_addresses", value))
	}
	for _, value := range criteria.Cc {
		add(b.like("mail_messages.cc_addresses", value))
	}
	for _, value := range criteria.Subject {
		add(b.like("mail_messages.subject", value))
	}
	for _, value := range criteria.MessageID {
		add(b.anyLike([]string{"mail_messages.message_id", "mail_messages.rfc_message_id"}, value))
	}
	for _, value := range criteria.Body {
		add(b.like("mail_messages.text_body", value))
	}
	for _, value := range criteria.Text {
		add(b.anyLike([]string{"mail_messages.subject", "mail_messages.from_address", "mail_messages.from_name",
			"mail_messages.to_addresses", "mail_messages.cc_addresses", "mail_messages.text_body"}, value))
	}
	for _, not := range criteria.Not {
		add("NOT (" + b.build(not) + ")")
	}
	for _, or := range criteria.Or {
		add("((" + b.build(or[0]) + ") OR (" + b.build(or[1]) + "))")
	}
	return "(" + strings.Join(conditions, " AND ") + ")"
}
