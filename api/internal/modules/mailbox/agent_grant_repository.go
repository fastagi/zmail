package mailbox

import (
	"context"
	"encoding/json"
	"errors"
	"hash/fnv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
)

var _ domain.MailAgentRepository = (*repository)(nil)

// agentKeyRow is the part of an api_keys row a grant needs. The mailbox starter depends on the
// apikey starter, so the table always exists; this reads it and never writes it.
type agentKeyRow struct {
	ID        uint
	UserID    uint
	Name      string
	KeyPrefix string
	Scopes    string
	Mode      string
	ExpiresAt *time.Time
	RevokedAt *time.Time
}

func (r *repository) FindAgentKey(ctx context.Context, keyID uint) (*domain.APIKey, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []agentKeyRow
	err := r.conn(ctx).Raw(`SELECT id, user_id, name, key_prefix, scopes, mode, expires_at, revoked_at FROM api_keys
		WHERE id = ? AND deleted_at IS NULL`, keyID).Scan(&rows).Error
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	row := rows[0]
	mode := domain.APIKeyMode(row.Mode)
	if !mode.Valid() {
		mode = domain.APIKeyModeLive
	}
	return &domain.APIKey{
		ID: row.ID, UserID: row.UserID, Name: row.Name, KeyPrefix: row.KeyPrefix, Scopes: decodeAgentKeyScopes(row.Scopes), Mode: mode,
		ExpiresAt: row.ExpiresAt, RevokedAt: row.RevokedAt,
	}, nil
}

// decodeAgentKeyScopes reads the stored scopes: a JSON array, or the comma list of older rows.
// Anything unreadable grants no scope.
func decodeAgentKeyScopes(value string) []string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "[") {
		var scopes []string
		if json.Unmarshal([]byte(value), &scopes) != nil {
			return nil
		}
		return scopes
	}
	var scopes []string
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			scopes = append(scopes, part)
		}
	}
	return scopes
}

func (r *repository) CreateAgentGrant(ctx context.Context, grant *domain.MailAgentGrant, maxLive int) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	po, err := newMailAgentGrantPO(grant)
	if err != nil {
		return err
	}
	return r.InTransaction(ctx, func(tx context.Context) error {
		db := r.conn(tx)
		var locked MailboxPO
		lock := db.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id = ?", grant.MailboxID).First(&locked)
		if lock.Error != nil {
			if errors.Is(lock.Error, gorm.ErrRecordNotFound) {
				return ErrMailboxNotFound
			}
			return lock.Error
		}
		live := db.Model(&MailAgentGrantPO{}).
			Where("mailbox_id = ? AND revoked_at IS NULL AND expires_at > ?", grant.MailboxID, grant.CreatedAt)
		var sameKey int64
		if err := live.Session(&gorm.Session{}).Where("api_key_id = ?", grant.APIKeyID).Count(&sameKey).Error; err != nil {
			return err
		}
		if sameKey > 0 {
			return ErrMailAgentGrantExists
		}
		var count int64
		if err := live.Session(&gorm.Session{}).Count(&count).Error; err != nil {
			return err
		}
		if count >= int64(maxLive) {
			return ErrMailAgentGrantLimit
		}
		if err := db.Create(po).Error; err != nil {
			return err
		}
		grant.ID = po.ID
		return nil
	})
}

func (r *repository) FindAgentGrant(ctx context.Context, mailboxID, grantID uint) (*domain.MailAgentGrant, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po MailAgentGrantPO
	if err := r.conn(ctx).Where("id = ? AND mailbox_id = ?", grantID, mailboxID).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMailAgentGrantNotFound
		}
		return nil, err
	}
	return po.toDomain(), nil
}

func (r *repository) ListAgentGrants(ctx context.Context, mailboxID uint, page, pageSize int) ([]*domain.MailAgentGrant, int64, error) {
	if r.db == nil {
		return nil, 0, domain.ErrServiceUnavailable
	}
	var total int64
	if err := r.conn(ctx).Model(&MailAgentGrantPO{}).Where("mailbox_id = ?", mailboxID).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []mailAgentGrantRow
	err := r.conn(ctx).Table("mail_agent_grants AS g").
		Select("g.*, COALESCE(k.name, '') AS api_key_name, COALESCE(k.key_prefix, '') AS api_key_prefix").
		Joins("LEFT JOIN api_keys AS k ON k.id = g.api_key_id").
		Where("g.mailbox_id = ?", mailboxID).
		Order("g.id DESC").Offset((page - 1) * pageSize).Limit(pageSize).
		Scan(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	grants := make([]*domain.MailAgentGrant, 0, len(rows))
	for i := range rows {
		grant := rows[i].toDomain()
		grant.APIKeyName, grant.APIKeyPrefix = rows[i].APIKeyName, rows[i].APIKeyPrefix
		grants = append(grants, grant)
	}
	return grants, total, nil
}

func (r *repository) listGrants(query *gorm.DB) ([]*domain.MailAgentGrant, error) {
	var rows []MailAgentGrantPO
	if err := query.Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	grants := make([]*domain.MailAgentGrant, 0, len(rows))
	for i := range rows {
		grants = append(grants, rows[i].toDomain())
	}
	return grants, nil
}

func (r *repository) ListLiveAgentGrantsOfMailbox(ctx context.Context, mailboxID uint, now time.Time) ([]*domain.MailAgentGrant, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	return r.listGrants(r.conn(ctx).Where("mailbox_id = ? AND revoked_at IS NULL AND expires_at > ?", mailboxID, now.UTC()))
}

func (r *repository) ListLiveAgentGrantsOfKey(ctx context.Context, organizationID, keyID uint, now time.Time) ([]*domain.MailAgentGrant, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	return r.listGrants(r.conn(ctx).Where("organization_id = ? AND api_key_id = ? AND revoked_at IS NULL AND expires_at > ?",
		organizationID, keyID, now.UTC()))
}

func (r *repository) RevokeAgentGrants(ctx context.Context, revocation domain.MailAgentGrantRevocation) ([]*domain.MailAgentGrant, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	if revocation.OrganizationID == 0 || (revocation.MailboxID == 0 && revocation.GrantID == 0 && revocation.KeyUserID == 0) {
		return nil, errors.New("mail agent revocation needs an organization and a narrower selector")
	}
	at := revocation.At.UTC()
	var revokedBy *uint
	if revocation.RevokedBy != 0 {
		revokedBy = &revocation.RevokedBy
	}
	conditions := []string{"organization_id = @org", "revoked_at IS NULL"}
	if revocation.MailboxID != 0 {
		conditions = append(conditions, "mailbox_id = @mailbox")
	}
	if revocation.GrantID != 0 {
		conditions = append(conditions, "id = @grant")
	}
	if revocation.KeyUserID != 0 {
		conditions = append(conditions, "key_user_id = @user")
	}
	var rows []MailAgentGrantPO
	err := r.conn(ctx).Raw(`UPDATE mail_agent_grants
		SET revoked_at = @at, revoked_by = @by, revoke_reason = @reason, updated_at = @at
		WHERE `+strings.Join(conditions, " AND ")+` RETURNING *`, map[string]any{
		"org": revocation.OrganizationID, "mailbox": revocation.MailboxID, "grant": revocation.GrantID,
		"user": revocation.KeyUserID, "at": at, "by": revokedBy, "reason": string(revocation.Reason),
	}).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	grants := make([]*domain.MailAgentGrant, 0, len(rows))
	for i := range rows {
		grants = append(grants, rows[i].toDomain())
	}
	return grants, nil
}

// agentEventLockKey names the transaction lock that orders one organization's event inserts.
var agentEventLockKey = func() int32 {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte("zmail.mail_agent_events"))
	return int32(hash.Sum32() & 0x7fffffff)
}()

// AppendAgentEvents takes the organization's transaction-scoped advisory lock before inserting, and
// holds it until the storing transaction ends. Event ids of one organization are therefore drawn
// and committed in the same order, so a reader that saw id N never later finds a smaller id: the
// feed cursor cannot skip an event.
func (r *repository) AppendAgentEvents(ctx context.Context, organizationID uint, events []*domain.MailAgentEvent) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	if len(events) == 0 {
		return nil
	}
	db := r.conn(ctx)
	if err := db.Exec(`SELECT pg_advisory_xact_lock(?, ?)`, agentEventLockKey, int32(organizationID&0x7fffffff)).Error; err != nil {
		return err
	}
	rows := make([]MailAgentEventPO, 0, len(events))
	for _, event := range events {
		rows = append(rows, MailAgentEventPO{
			GrantID: event.GrantID, OrganizationID: organizationID, MailboxID: event.MailboxID,
			MessageID: event.MessageID, Type: string(event.Type), CreatedAt: event.CreatedAt.UTC(),
		})
	}
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "grant_id"}, {Name: "message_id"}},
		DoNothing: true,
	}).Create(&rows).Error
}

func (r *repository) ListAgentEvents(ctx context.Context, grantIDs []uint, after uint, limit int) ([]*domain.MailAgentEvent, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	if len(grantIDs) == 0 {
		return []*domain.MailAgentEvent{}, nil
	}
	var rows []MailAgentEventPO
	err := r.conn(ctx).Where("grant_id IN ? AND id > ?", grantIDs, after).Order("id").Limit(limit).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	events := make([]*domain.MailAgentEvent, 0, len(rows))
	for i := range rows {
		events = append(events, rows[i].toDomain())
	}
	return events, nil
}

func (r *repository) ListAgentMessageEvents(ctx context.Context, grantIDs []uint, messageID uint) ([]*domain.MailAgentEvent, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	if len(grantIDs) == 0 {
		return []*domain.MailAgentEvent{}, nil
	}
	var rows []MailAgentEventPO
	if err := r.conn(ctx).Where("grant_id IN ? AND message_id = ?", grantIDs, messageID).Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	events := make([]*domain.MailAgentEvent, 0, len(rows))
	for i := range rows {
		events = append(events, rows[i].toDomain())
	}
	return events, nil
}

func (r *repository) AddAgentAudit(ctx context.Context, entry *domain.MailAgentAuditEntry) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	po := &MailAgentAuditPO{
		OrganizationID: entry.OrganizationID, MailboxID: entry.MailboxID, GrantID: entry.GrantID,
		APIKeyID: entry.APIKeyID, MessageID: entry.MessageID, Action: string(entry.Action), CreatedAt: entry.CreatedAt.UTC(),
	}
	if err := r.conn(ctx).Create(po).Error; err != nil {
		return err
	}
	entry.ID = po.ID
	return nil
}

func (r *repository) ListAgentAudit(ctx context.Context, grantID uint, limit int) ([]*domain.MailAgentAuditEntry, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []MailAgentAuditPO
	if err := r.conn(ctx).Where("grant_id = ?", grantID).Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	entries := make([]*domain.MailAgentAuditEntry, 0, len(rows))
	for i := range rows {
		entries = append(entries, rows[i].toDomain())
	}
	return entries, nil
}

func (r *repository) SaveAgentDraft(ctx context.Context, draft *domain.MailAgentDraft) (bool, error) {
	if r.db == nil {
		return false, domain.ErrServiceUnavailable
	}
	now := draft.UpdatedAt.UTC()
	po := &MailAgentDraftPO{
		OrganizationID: draft.OrganizationID, MailboxID: draft.MailboxID, GrantID: draft.GrantID, MessageID: draft.MessageID,
		APIKeyID: draft.APIKeyID, TextBody: draft.TextBody, HTMLBody: draft.HTMLBody, Status: "pending",
		CreatedAt: now, UpdatedAt: now,
	}
	// xmax = 0 tells a fresh insert from an update of the existing row.
	var saved []struct {
		MailAgentDraftPO
		Inserted bool
	}
	err := r.conn(ctx).Raw(`INSERT INTO mail_agent_drafts
		  (organization_id, mailbox_id, grant_id, message_id, api_key_id, text_body, html_body, status, created_at, updated_at)
		VALUES (@org, @mailbox, @grant, @message, @key, @text, @html, 'pending', @now, @now)
		ON CONFLICT (grant_id, message_id) DO UPDATE
		  SET text_body = EXCLUDED.text_body, html_body = EXCLUDED.html_body, updated_at = EXCLUDED.updated_at
		RETURNING *, (xmax = 0) AS inserted`, map[string]any{
		"org": po.OrganizationID, "mailbox": po.MailboxID, "grant": po.GrantID, "message": po.MessageID,
		"key": po.APIKeyID, "text": po.TextBody, "html": po.HTMLBody, "now": now,
	}).Scan(&saved).Error
	if err != nil || len(saved) == 0 {
		return false, err
	}
	*draft = *saved[0].toDomain()
	return saved[0].Inserted, nil
}

func (r *repository) ListAgentDrafts(ctx context.Context, mailboxID uint, page, pageSize int) ([]*domain.MailAgentDraft, int64, error) {
	if r.db == nil {
		return nil, 0, domain.ErrServiceUnavailable
	}
	query := r.conn(ctx).Model(&MailAgentDraftPO{}).Where("mailbox_id = ?", mailboxID)
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []MailAgentDraftPO
	if err := query.Session(&gorm.Session{}).Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	drafts := make([]*domain.MailAgentDraft, 0, len(rows))
	for i := range rows {
		drafts = append(drafts, rows[i].toDomain())
	}
	return drafts, total, nil
}

func (r *repository) PruneAgentEvents(ctx context.Context, before time.Time, limit int) (int64, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	result := r.conn(ctx).Exec(`DELETE FROM mail_agent_events WHERE id IN (
		SELECT id FROM mail_agent_events WHERE created_at < ? ORDER BY id LIMIT ?)`, before.UTC(), limit)
	return result.RowsAffected, result.Error
}
