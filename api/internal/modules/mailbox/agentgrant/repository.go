package agentgrant

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
	"github.com/fastagi/zmail/api/internal/infra/database"
)

// Repository implements domain.MailAgentRepository against PostgreSQL.
type Repository struct {
	db                 *gorm.DB
	errMailboxNotFound error
}

var _ domain.MailAgentRepository = (*Repository)(nil)

// NewRepository creates an agent grant repository backed by db.
func NewRepository(db *gorm.DB, errMailboxNotFound error) *Repository {
	return &Repository{db: db, errMailboxNotFound: errMailboxNotFound}
}

func (r *Repository) conn(ctx context.Context) *gorm.DB {
	return database.ResolveContextDB(ctx, r.db).WithContext(ctx)
}

func (r *Repository) inTransaction(ctx context.Context, fn func(context.Context) error) error {
	if _, ok := database.TransactionFromContext(ctx); ok {
		return fn(ctx)
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(database.ContextWithTransaction(ctx, tx))
	})
}

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

const effectiveAgentGrant = `g.revoked_at IS NULL AND g.expires_at > @now
	AND EXISTS (SELECT 1 FROM api_keys AS k WHERE k.id = g.api_key_id AND k.deleted_at IS NULL
	  AND k.revoked_at IS NULL AND (k.expires_at IS NULL OR k.expires_at > @now))
	AND EXISTS (SELECT 1 FROM mailboxes AS box WHERE box.id = g.mailbox_id AND box.deleted_at IS NULL AND (
	  (box.type = 'shared' AND EXISTS (SELECT 1 FROM mailbox_members AS m
	    WHERE m.mailbox_id = box.id AND m.user_id = g.key_user_id))
	  OR (box.type <> 'shared' AND box.owner_user_id = g.key_user_id AND box.owner_left_at IS NULL))
	  AND (box.origin <> 'connected' OR EXISTS (SELECT 1 FROM external_mail_connections AS c
	    WHERE c.mailbox_id = box.id AND c.status <> 'revoked')))`

const unsealedMessage = `NOT EXISTS (SELECT 1 FROM mailboxes AS seal WHERE seal.id = mail_messages.mailbox_id
	AND seal.sealed_before IS NOT NULL AND mail_messages.created_at < seal.sealed_before)`

func (r *Repository) FindAgentKey(ctx context.Context, keyID uint) (*domain.APIKey, error) {
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

func (r *Repository) CreateAgentGrant(ctx context.Context, grant *domain.MailAgentGrant, maxLive int) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	po, err := newMailAgentGrantPO(grant)
	if err != nil {
		return err
	}
	return r.inTransaction(ctx, func(tx context.Context) error {
		db := r.conn(tx)
		var locked struct{ ID uint }
		lock := db.Table("mailboxes").Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id").Where("id = ? AND deleted_at IS NULL", grant.MailboxID).First(&locked)
		if lock.Error != nil {
			if errors.Is(lock.Error, gorm.ErrRecordNotFound) {
				return r.errMailboxNotFound
			}
			return lock.Error
		}
		var counts struct {
			Live    int64
			SameKey int64
		}
		err := db.Raw(`SELECT count(*) AS live, count(*) FILTER (WHERE g.api_key_id = @key) AS same_key
			FROM mail_agent_grants AS g WHERE g.mailbox_id = @mailbox AND `+effectiveAgentGrant,
			map[string]any{"mailbox": grant.MailboxID, "key": grant.APIKeyID, "now": grant.CreatedAt.UTC()}).
			Scan(&counts).Error
		if err != nil {
			return err
		}
		if counts.SameKey > 0 {
			return ErrExists
		}
		if counts.Live >= int64(maxLive) {
			return ErrLimit
		}
		if err := db.Create(po).Error; err != nil {
			return err
		}
		grant.ID = po.ID
		return nil
	})
}

func afterSeal(query *gorm.DB, column string, sealedBefore *time.Time) *gorm.DB {
	if sealedBefore == nil {
		return query
	}
	return query.Where(column+" >= ?", sealedBefore.UTC())
}

func (r *Repository) FindAgentGrant(ctx context.Context, mailboxID, grantID uint, sealedBefore *time.Time) (*domain.MailAgentGrant, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po MailAgentGrantPO
	query := afterSeal(r.conn(ctx).Where("id = ? AND mailbox_id = ?", grantID, mailboxID), "created_at", sealedBefore)
	if err := query.First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return po.toDomain(), nil
}

func (r *Repository) ListAgentGrants(ctx context.Context, mailboxID uint, sealedBefore *time.Time, page, pageSize int) ([]*domain.MailAgentGrant, int64, error) {
	if r.db == nil {
		return nil, 0, domain.ErrServiceUnavailable
	}
	var total int64
	counted := afterSeal(r.conn(ctx).Model(&MailAgentGrantPO{}).Where("mailbox_id = ?", mailboxID), "created_at", sealedBefore)
	if err := counted.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []mailAgentGrantRow
	err := afterSeal(r.conn(ctx).Table("mail_agent_grants AS g").
		Select("g.*, COALESCE(k.name, '') AS api_key_name, COALESCE(k.key_prefix, '') AS api_key_prefix").
		Joins("LEFT JOIN api_keys AS k ON k.id = g.api_key_id").
		Where("g.mailbox_id = ?", mailboxID), "g.created_at", sealedBefore).
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

func (r *Repository) listGrants(query *gorm.DB) ([]*domain.MailAgentGrant, error) {
	var rows []MailAgentGrantPO
	if err := query.Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	return grantsOf(rows), nil
}

func grantsOf(rows []MailAgentGrantPO) []*domain.MailAgentGrant {
	grants := make([]*domain.MailAgentGrant, 0, len(rows))
	for i := range rows {
		grants = append(grants, rows[i].toDomain())
	}
	return grants
}

func (r *Repository) ListLiveAgentGrantsOfMailbox(ctx context.Context, mailboxID uint, now time.Time) ([]*domain.MailAgentGrant, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []MailAgentGrantPO
	err := r.conn(ctx).Raw(`SELECT g.* FROM mail_agent_grants AS g WHERE g.mailbox_id = @mailbox AND `+effectiveAgentGrant+` ORDER BY g.id`,
		map[string]any{"mailbox": mailboxID, "now": now.UTC()}).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return grantsOf(rows), nil
}

func (r *Repository) ListLiveAgentGrantsOfKey(ctx context.Context, organizationID, keyID uint, now time.Time) ([]*domain.MailAgentGrant, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	return r.listGrants(r.conn(ctx).Where("organization_id = ? AND api_key_id = ? AND revoked_at IS NULL AND expires_at > ?",
		organizationID, keyID, now.UTC()))
}

func (r *Repository) RevokeAgentGrants(ctx context.Context, revocation domain.MailAgentGrantRevocation) ([]*domain.MailAgentGrant, error) {
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

var agentEventLockKey = func() int32 {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte("zmail.mail_agent_events"))
	return int32(hash.Sum32() & 0x7fffffff)
}()

func (r *Repository) AppendAgentEvents(ctx context.Context, organizationID uint, events []*domain.MailAgentEvent) error {
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

func (r *Repository) ListAgentEvents(ctx context.Context, grantIDs []uint, after uint, limit int) ([]*domain.MailAgentEvent, error) {
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

func (r *Repository) ListAgentMessageEvents(ctx context.Context, grantIDs []uint, messageID uint) ([]*domain.MailAgentEvent, error) {
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

func (r *Repository) AddAgentAudit(ctx context.Context, entry *domain.MailAgentAuditEntry) error {
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

func (r *Repository) ListAgentAudit(ctx context.Context, grantID uint, sealedBefore *time.Time, limit int) ([]*domain.MailAgentAuditEntry, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []MailAgentAuditPO
	query := afterSeal(r.conn(ctx).Where("grant_id = ?", grantID), "created_at", sealedBefore)
	if err := query.Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	entries := make([]*domain.MailAgentAuditEntry, 0, len(rows))
	for i := range rows {
		entries = append(entries, rows[i].toDomain())
	}
	return entries, nil
}

func (r *Repository) SaveAgentDraft(ctx context.Context, draft *domain.MailAgentDraft) (bool, error) {
	if r.db == nil {
		return false, domain.ErrServiceUnavailable
	}
	now := draft.UpdatedAt.UTC()
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
		"org": draft.OrganizationID, "mailbox": draft.MailboxID, "grant": draft.GrantID, "message": draft.MessageID,
		"key": draft.APIKeyID, "text": draft.TextBody, "html": draft.HTMLBody, "now": now,
	}).Scan(&saved).Error
	if err != nil || len(saved) == 0 {
		return false, err
	}
	*draft = *saved[0].toDomain()
	return saved[0].Inserted, nil
}

func (r *Repository) ListAgentDrafts(ctx context.Context, mailboxID uint, page, pageSize int) ([]*domain.MailAgentDraft, int64, error) {
	if r.db == nil {
		return nil, 0, domain.ErrServiceUnavailable
	}
	query := r.conn(ctx).Model(&MailAgentDraftPO{}).Where("mail_agent_drafts.mailbox_id = ?", mailboxID).
		Where(`EXISTS (SELECT 1 FROM mail_messages WHERE mail_messages.id = mail_agent_drafts.message_id
		  AND ` + unsealedMessage + `)`)
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

func (r *Repository) DeleteAgentDrafts(ctx context.Context, mailboxID uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Where("mailbox_id = ?", mailboxID).Delete(&MailAgentDraftPO{}).Error
}

func (r *Repository) PruneAgentAudit(ctx context.Context, before time.Time, limit int) (int64, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	result := r.conn(ctx).Exec(`DELETE FROM mail_agent_audit WHERE id IN (
		SELECT id FROM mail_agent_audit WHERE created_at < ? ORDER BY id LIMIT ?)`, before.UTC(), limit)
	return result.RowsAffected, result.Error
}

func (r *Repository) PruneAgentEvents(ctx context.Context, before time.Time, limit int) (int64, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	result := r.conn(ctx).Exec(`DELETE FROM mail_agent_events WHERE id IN (
		SELECT id FROM mail_agent_events WHERE created_at < ? ORDER BY id LIMIT ?)`, before.UTC(), limit)
	return result.RowsAffected, result.Error
}
