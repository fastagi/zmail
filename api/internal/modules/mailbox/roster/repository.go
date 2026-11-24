package roster

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/database"
)

// Repository implements the mailbox roster, directory, member welcome, and platform account
// persistence operations against PostgreSQL.
type Repository struct {
	db                 *gorm.DB
	errMailboxNotFound error
}

var (
	_ domain.MailboxRosterRepository    = (*Repository)(nil)
	_ domain.MailboxDirectoryRepository = (*Repository)(nil)
	_ domain.PlatformAccountRepository  = (*Repository)(nil)
)

// NewRepository builds a roster repository backed by db.
func NewRepository(db *gorm.DB, errMailboxNotFound error) *Repository {
	return &Repository{db: db, errMailboxNotFound: errMailboxNotFound}
}

func (r *Repository) conn(ctx context.Context) *gorm.DB {
	return database.ResolveContextDB(ctx, r.db).WithContext(ctx)
}

// mailboxRow mirrors the mailboxes table columns needed by roster, directory, and welcome queries.
type mailboxRow struct {
	ID                   uint   `gorm:"primaryKey"`
	OrganizationID       *uint  `gorm:"index:idx_mailboxes_organization"`
	CreatedBy            uint   `gorm:"not null"`
	Address              string `gorm:"size:320;not null"`
	DisplayName          string `gorm:"size:120;not null;default:''"`
	RoutingRuleID        string `gorm:"size:64;not null;default:''"`
	Type                 string `gorm:"size:16;not null;default:'shared'"`
	Origin               string `gorm:"size:16;not null;default:'zmail'"`
	ConnectedSend        bool   `gorm:"not null;default:false"`
	OwnerUserID          *uint
	PendingOwnerEmail    *string `gorm:"size:320"`
	OwnerLeftAt          *time.Time
	SealedBefore         *time.Time
	WebhooksEnabled      bool `gorm:"not null;default:false"`
	RoleID               *uint
	AITriageEnabled      bool `gorm:"column:ai_triage_enabled;not null;default:false"`
	ExternalMailTenantID *uint
	CreatedAt            time.Time
	UpdatedAt            time.Time
	DeletedAt            gorm.DeletedAt
}

func (mailboxRow) TableName() string { return "mailboxes" }

func (po *mailboxRow) toDomain() *domain.Mailbox {
	if po == nil {
		return nil
	}
	var organizationID uint
	if po.OrganizationID != nil {
		organizationID = *po.OrganizationID
	}
	var pendingOwnerEmail string
	if po.PendingOwnerEmail != nil {
		pendingOwnerEmail = *po.PendingOwnerEmail
	}
	origin := domain.MailboxOriginZmail
	if po.Origin != "" {
		origin = domain.MailboxOrigin(po.Origin)
	}
	return &domain.Mailbox{
		ID:                po.ID,
		OrganizationID:    organizationID,
		CreatedBy:         po.CreatedBy,
		Address:           po.Address,
		DisplayName:       po.DisplayName,
		RoutingRuleID:     po.RoutingRuleID,
		Type:              domain.MailboxType(po.Type),
		Origin:            origin,
		ConnectedSend:     po.ConnectedSend,
		OwnerUserID:       po.OwnerUserID,
		OwnerLeftAt:       po.OwnerLeftAt,
		PendingOwnerEmail: pendingOwnerEmail,
		SealedBefore:      po.SealedBefore,
		WebhooksEnabled:   po.WebhooksEnabled,
		RoleID:            po.RoleID,
		AITriageEnabled:   po.AITriageEnabled,
		ExternalTenantID:  po.ExternalMailTenantID,
		CreatedAt:         po.CreatedAt,
		UpdatedAt:         po.UpdatedAt,
	}
}

// mailboxMemberRow mirrors the mailbox_members table columns needed by directory queries.
type mailboxMemberRow struct {
	MailboxID    uint   `gorm:"primaryKey;autoIncrement:false"`
	MembershipID uint   `gorm:"primaryKey;autoIncrement:false"`
	UserID       uint   `gorm:"not null"`
	Permission   string `gorm:"size:16;not null"`
	GrantedBy    uint   `gorm:"not null"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (mailboxMemberRow) TableName() string { return "mailbox_members" }

func (po *mailboxMemberRow) toDomain() *domain.MailboxMember {
	return &domain.MailboxMember{
		MailboxID:    po.MailboxID,
		MembershipID: po.MembershipID,
		UserID:       po.UserID,
		Permission:   domain.MailboxPermission(po.Permission),
		GrantedBy:    po.GrantedBy,
		CreatedAt:    po.CreatedAt,
	}
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func escapeLike(value string) string {
	return likeEscaper.Replace(value)
}

func searchPattern(query string) string {
	return "%" + likeEscaper.Replace(query) + "%"
}

func isUniqueViolation(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	var stateError interface{ SQLState() string }
	return errors.As(err, &stateError) && stateError.SQLState() == "23505"
}

// nonEmptyIDs keeps an empty id list from rendering as an invalid IN (); zero matches no row.
func nonEmptyIDs(ids []uint) []uint {
	if len(ids) == 0 {
		return []uint{0}
	}
	return ids
}

// ClaimReservedMailboxes gives the user every live personal mailbox of the organization reserved
// for the email and clears the reservation in the same statement, so a mailbox is claimed once.
func (r *Repository) ClaimReservedMailboxes(ctx context.Context, organizationID, userID uint, email string) ([]*domain.Mailbox, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	if organizationID == 0 || userID == 0 || email == "" {
		return nil, domain.ErrInvalidInput
	}
	var rows []mailboxRow
	err := r.conn(ctx).Raw(`UPDATE mailboxes SET owner_user_id = ?, pending_owner_email = NULL, updated_at = now()
		WHERE organization_id = ? AND pending_owner_email = ? AND type = 'personal'
		  AND owner_user_id IS NULL AND deleted_at IS NULL
		RETURNING *`, userID, organizationID, email).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	mailboxes := make([]*domain.Mailbox, 0, len(rows))
	for i := range rows {
		mailboxes = append(mailboxes, rows[i].toDomain())
	}
	return mailboxes, nil
}

// OwnsPersonalMailbox reports whether the user owns a live Zmail personal mailbox in the
// organization; a connected outside account does not count.
func (r *Repository) OwnsPersonalMailbox(ctx context.Context, organizationID, userID uint) (bool, error) {
	if r.db == nil {
		return false, domain.ErrServiceUnavailable
	}
	var count int64
	err := r.conn(ctx).Model(&mailboxRow{}).
		Where("organization_id = ? AND owner_user_id = ? AND type = 'personal' AND origin = 'zmail'", organizationID, userID).
		Limit(1).Count(&count).Error
	return count > 0, err
}

// ListMailboxDirectory reads the page first, then grouped counts over the page's ids only, so
// its cost follows the page size rather than the organization's mailbox count. The send count is
// served by idx_mail_messages_mailbox_send_attempted.
func (r *Repository) ListMailboxDirectory(ctx context.Context, organizationID uint, filter domain.MailboxDirectoryFilter, sentSince time.Time) ([]*domain.MailboxDirectoryEntry, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	// A connected personal mailbox is a member's own outside account, never the organization's to list,
	// unless it was assigned from the organization's tenant: that address is the organization's.
	query := r.conn(ctx).Where("organization_id = ? AND NOT (origin = ? AND type = ? AND external_mail_tenant_id IS NULL)", organizationID,
		string(domain.MailboxOriginConnected), string(domain.MailboxTypePersonal))
	if filter.Type != "" {
		query = query.Where("type = ?", string(filter.Type))
	}
	if filter.Query != "" {
		pattern := "%" + escapeLike(filter.Query) + "%"
		query = query.Where(`(address ILIKE ? ESCAPE '\' OR display_name ILIKE ? ESCAPE '\')`, pattern, pattern)
	}
	if filter.BeforeID != 0 {
		query = query.Where("id < ?", filter.BeforeID)
	}
	var rows []mailboxRow
	if err := query.Order("id DESC").Limit(filter.Limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	entries := make([]*domain.MailboxDirectoryEntry, 0, len(rows))
	ids := make([]uint, 0, len(rows))
	for i := range rows {
		entries = append(entries, &domain.MailboxDirectoryEntry{Mailbox: rows[i].toDomain()})
		ids = append(ids, rows[i].ID)
	}
	if len(ids) == 0 {
		return entries, nil
	}

	var memberCounts []struct {
		MailboxID uint
		Count     int
	}
	if err := r.conn(ctx).Model(&mailboxMemberRow{}).
		Select("mailbox_id, COUNT(*) AS count").
		Where("mailbox_id IN ?", ids).
		Group("mailbox_id").
		Scan(&memberCounts).Error; err != nil {
		return nil, err
	}
	var sentCounts []struct {
		MailboxID uint
		Count     int64
	}
	if err := r.conn(ctx).Table("mail_messages").
		Select("mailbox_id, COUNT(*) AS count").
		Where("mailbox_id IN ? AND direction = ? AND send_lane = ? AND send_attempted_at >= ?",
			ids, string(domain.MailDirectionOutbound), string(domain.SendLanePersonal), sentSince).
		Group("mailbox_id").
		Scan(&sentCounts).Error; err != nil {
		return nil, err
	}
	var deletedCounts []struct {
		MailboxID uint
		Count     int64
	}
	if err := r.conn(ctx).Table("mail_deleted_sends").
		Select("mailbox_id, COUNT(*) AS count").
		Where("mailbox_id IN ? AND send_lane = ? AND send_attempted_at >= ?", ids, string(domain.SendLanePersonal), sentSince).
		Group("mailbox_id").
		Scan(&deletedCounts).Error; err != nil {
		return nil, err
	}
	byID := make(map[uint]*domain.MailboxDirectoryEntry, len(entries))
	for _, entry := range entries {
		byID[entry.Mailbox.ID] = entry
	}
	for _, count := range memberCounts {
		byID[count.MailboxID].MemberCount = count.Count
	}
	for _, count := range append(sentCounts, deletedCounts...) {
		byID[count.MailboxID].SentLast24h += count.Count
	}
	return entries, nil
}

// CountMailboxes counts Zmail's own live mailboxes in the organization.
func (r *Repository) CountMailboxes(ctx context.Context, organizationID uint) (int64, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	var count int64
	// The count is the one the organization's mailbox limit bounds: Zmail's own mailboxes.
	err := r.conn(ctx).Model(&mailboxRow{}).
		Where("organization_id = ? AND origin = ?", organizationID, string(domain.MailboxOriginZmail)).Count(&count).Error
	return count, err
}

// UpdateMailboxDisplayName sets the display name of a live mailbox of the organization.
func (r *Repository) UpdateMailboxDisplayName(ctx context.Context, organizationID, id uint, displayName string) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	result := r.conn(ctx).Model(&mailboxRow{}).
		Where("id = ? AND organization_id = ?", id, organizationID).
		Updates(map[string]any{"display_name": strings.TrimSpace(displayName), "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return r.errMailboxNotFound
	}
	return nil
}

// MailboxAddressUsed counts deleted mailboxes too: a deleted mailbox keeps its address reserved.
func (r *Repository) MailboxAddressUsed(ctx context.Context, address string) (bool, error) {
	if r.db == nil {
		return false, domain.ErrServiceUnavailable
	}
	var count int64
	if err := r.conn(ctx).Unscoped().Model(&mailboxRow{}).
		Where("address = ? AND origin = ?", strings.ToLower(strings.TrimSpace(address)), string(domain.MailboxOriginZmail)).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// mailboxMembersChunk bounds the ids of one members query.
const mailboxMembersChunk = 1000

// ListMembersOfMailboxes reads members in chunks of mailbox ids; at most
// MailboxesPerOrganizationCeiling mailboxes of MaxMailboxMembers members each.
func (r *Repository) ListMembersOfMailboxes(ctx context.Context, mailboxIDs []uint) ([]*domain.MailboxMember, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	members := make([]*domain.MailboxMember, 0, len(mailboxIDs))
	for start := 0; start < len(mailboxIDs); start += mailboxMembersChunk {
		chunk := mailboxIDs[start:min(start+mailboxMembersChunk, len(mailboxIDs))]
		var rows []mailboxMemberRow
		if err := r.conn(ctx).Where("mailbox_id IN ?", chunk).Order("mailbox_id ASC, user_id ASC").Find(&rows).Error; err != nil {
			return nil, err
		}
		for i := range rows {
			members = append(members, rows[i].toDomain())
		}
	}
	return members, nil
}
