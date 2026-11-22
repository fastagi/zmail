package mailbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
)

// ErrExternalMailConnectionNotFound hides a connection that does not exist, belongs to someone
// else, or whose feature is off.
var ErrExternalMailConnectionNotFound = errors.New("external mail connection not found")

// Errors of binding an outside account.
var (
	// ErrExternalMailAuthorizationNotFound hides a sign-in that is unknown, expired, used, or
	// started by someone else.
	ErrExternalMailAuthorizationNotFound = errors.New("external mail authorization not found")
	// ErrExternalMailAuthorizationFailed means the provider refused the authorization code.
	ErrExternalMailAuthorizationFailed = errors.New("external mail authorization failed")
	// ErrExternalMailAccountMismatch means a reconnect signed in to another account.
	ErrExternalMailAccountMismatch = errors.New("external mail account does not match the connection")
	// ErrExternalMailAccountTaken means another member of the organization bound the account.
	ErrExternalMailAccountTaken = errors.New("external mail account is bound by another member")
	// ErrExternalMailConnectionLimit means the member bound the most accounts allowed.
	ErrExternalMailConnectionLimit = errors.New("external mail connection limit reached")
	// ErrExternalMailAuthorizationInvalid means a sign-in request is malformed: backfill out of
	// range, or a provider that is not the reconnected connection's.
	ErrExternalMailAuthorizationInvalid = errors.New("external mail authorization request is invalid")
	// ErrExternalMailProviderUnavailable means the instance does not run the requested connector.
	ErrExternalMailProviderUnavailable = errors.New("external mail provider unavailable")
)

// ExternalMailConnectionPO is the persistent object of one external mail connection.
type ExternalMailConnectionPO struct {
	ID                    uint   `gorm:"primaryKey"`
	OrganizationID        uint   `gorm:"not null"`
	MailboxID             uint   `gorm:"not null;uniqueIndex"`
	Provider              string `gorm:"size:16;not null"`
	AccountAddress        string `gorm:"size:320;not null"`
	OwnerUserID           *uint
	Shared                bool `gorm:"not null;default:false"`
	BoundByUserID         *uint
	Status                string `gorm:"size:16;not null;default:'active'"`
	LastSyncedAt          *time.Time
	LastErrorCode         *string `gorm:"size:64"`
	EncryptedRefreshToken string  `gorm:"type:text;not null;default:''"`
	SyncCursor            string  `gorm:"type:text;not null;default:''"`
	BackfillDays          int     `gorm:"type:smallint;not null;default:14"`
	NextSyncAt            time.Time
	ConsecutiveFailures   int `gorm:"type:smallint;not null;default:0"`
	ConsecutiveMore       int `gorm:"type:smallint;not null;default:0"`
	RevokedAt             *time.Time
	PurgeRetryAt          *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

func (ExternalMailConnectionPO) TableName() string {
	return "external_mail_connections"
}

func (po *ExternalMailConnectionPO) toDomain() *domain.ExternalMailConnection {
	connection := &domain.ExternalMailConnection{
		ID: po.ID, OrganizationID: po.OrganizationID, MailboxID: po.MailboxID,
		Provider: domain.ExternalMailProvider(po.Provider), AccountAddress: po.AccountAddress,
		OwnerUserID: po.OwnerUserID, Shared: po.Shared, BoundByUserID: po.BoundByUserID,
		Status: domain.ExternalMailStatus(po.Status), LastSyncedAt: po.LastSyncedAt,
		EncryptedRefreshToken: po.EncryptedRefreshToken, SyncCursor: po.SyncCursor, BackfillDays: po.BackfillDays,
		NextSyncAt: po.NextSyncAt, ConsecutiveFailures: po.ConsecutiveFailures, ConsecutiveMore: po.ConsecutiveMore, RevokedAt: po.RevokedAt,
		CreatedAt: po.CreatedAt, UpdatedAt: po.UpdatedAt,
	}
	if po.LastErrorCode != nil {
		connection.LastErrorCode = *po.LastErrorCode
	}
	return connection
}

var _ domain.ExternalMailRepository = (*repository)(nil)

func (r *repository) CreateExternalMailConnection(ctx context.Context, connection *domain.ExternalMailConnection) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	po := &ExternalMailConnectionPO{
		OrganizationID: connection.OrganizationID, MailboxID: connection.MailboxID,
		Provider: string(connection.Provider), AccountAddress: connection.AccountAddress,
		OwnerUserID: connection.OwnerUserID, Shared: connection.Shared, BoundByUserID: connection.BoundByUserID,
		Status:                string(domain.ExternalMailStatusActive),
		EncryptedRefreshToken: connection.EncryptedRefreshToken, SyncCursor: connection.SyncCursor,
		BackfillDays: connection.BackfillDays, NextSyncAt: connection.NextSyncAt,
	}
	if po.BackfillDays == 0 {
		po.BackfillDays = domain.DefaultExternalMailBackfillDays
	}
	if err := r.conn(ctx).Create(po).Error; err != nil {
		return err
	}
	*connection = *po.toDomain()
	return nil
}

func (r *repository) ClaimExternalMailConnections(ctx context.Context, claim domain.ExternalMailClaim) ([]*domain.ExternalMailConnection, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	if claim.Limit <= 0 {
		return nil, nil
	}
	perProvider := claim.PerProvider
	if perProvider <= 0 || perProvider > claim.Limit {
		perProvider = claim.Limit
	}
	// The providers come from a loose index scan of the due index, one lookup each, so a provider
	// with many connections costs no more than one with few. Each provider offers its oldest due
	// connections up to its share; the pass then takes the first of every provider, the second of
	// every provider, and so on, so a provider that is down or backlogged cannot starve the others.
	var rows []claimedExternalMailConnection
	err := r.conn(ctx).Raw(`WITH RECURSIVE providers AS (
		  (SELECT provider FROM external_mail_connections WHERE status = 'active' ORDER BY provider LIMIT 1)
		  UNION ALL
		  SELECT (SELECT c.provider FROM external_mail_connections c
		          WHERE c.status = 'active' AND c.provider > p.provider ORDER BY c.provider LIMIT 1)
		  FROM providers p WHERE p.provider IS NOT NULL
		), ranked AS (
		  SELECT due.id, row_number() OVER (PARTITION BY p.provider ORDER BY due.next_sync_at, due.id) AS turn,
		    due.next_sync_at AS due_at
		  FROM providers p
		  CROSS JOIN LATERAL (
		    SELECT id, next_sync_at FROM external_mail_connections
		    WHERE status = 'active' AND provider = p.provider AND next_sync_at <= ?
		    ORDER BY next_sync_at, id
		    LIMIT ?
		    FOR UPDATE SKIP LOCKED) due
		  WHERE p.provider IS NOT NULL
		), chosen AS (
		  SELECT id, turn FROM ranked ORDER BY turn, due_at, id LIMIT ?
		)
		UPDATE external_mail_connections c SET next_sync_at = ?
		FROM chosen WHERE c.id = chosen.id
		RETURNING c.*, chosen.turn`, claim.Now, perProvider, claim.Limit, claim.LeaseUntil).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Turn != rows[j].Turn {
			return rows[i].Turn < rows[j].Turn
		}
		if rows[i].Provider != rows[j].Provider {
			return rows[i].Provider < rows[j].Provider
		}
		return rows[i].ID < rows[j].ID
	})
	connections := make([]*domain.ExternalMailConnection, 0, len(rows))
	for i := range rows {
		connections = append(connections, rows[i].toDomain())
	}
	return connections, nil
}

// claimedExternalMailConnection is a claimed row with its round-robin turn.
type claimedExternalMailConnection struct {
	ExternalMailConnectionPO
	Turn int
}

func (r *repository) SaveExternalMailSync(ctx context.Context, result domain.ExternalMailSyncResult) ([]*domain.MailMessage, bool, error) {
	if r.db == nil {
		return nil, false, domain.ErrServiceUnavailable
	}
	var stored []*domain.MailMessage
	applied := false
	err := r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		updates := map[string]any{
			"status": string(result.Status), "sync_cursor": result.Cursor,
			"next_sync_at": result.NextSyncAt, "updated_at": result.Now,
			"consecutive_failures": min(result.ConsecutiveFailures, maxConsecutiveFailures),
			"consecutive_more":     min(result.ConsecutiveMore, maxConsecutiveFailures),
		}
		if result.LastErrorCode == "" {
			updates["last_error_code"] = nil
		} else {
			updates["last_error_code"] = result.LastErrorCode
		}
		if result.SyncedAt != nil {
			updates["last_synced_at"] = *result.SyncedAt
		}
		if result.EncryptedRefreshToken != "" {
			// A rotated grant is saved with the page it came with: losing it would lose the account.
			updates["encrypted_refresh_token"] = result.EncryptedRefreshToken
		}
		// The status condition locks the row and drops the batch when a revoke or a lost grant
		// landed while the fetch ran.
		update := tx.Model(&ExternalMailConnectionPO{}).
			Where("id = ? AND status = ?", result.ConnectionID, string(domain.ExternalMailStatusActive)).
			Updates(updates)
		if update.Error != nil {
			return update.Error
		}
		applied = update.RowsAffected > 0
		if applied {
			for _, message := range result.Messages {
				created, err := createExternalMessage(tx, result.MailboxID, message)
				if err != nil {
					return err
				}
				if created {
					stored = append(stored, message)
				}
			}
		}
		if result.Run == nil {
			return nil
		}
		result.Run.Stored = len(stored)
		if !applied {
			result.Run.Outcome, result.Run.ErrorCode = domain.ExternalMailSyncDropped, ""
		}
		return recordExternalMailSyncRun(tx, result.Run)
	})
	if err != nil {
		return nil, false, err
	}
	return stored, applied, nil
}

// createExternalMessage stores one synced copy unless the mailbox already has its external id,
// threading it like inbound mail.
func createExternalMessage(tx *gorm.DB, mailboxID uint, message *domain.MailMessage) (bool, error) {
	if message.ExternalID == "" || message.MailboxID != mailboxID {
		return false, ErrMailMessageInvalid
	}
	po := newMailMessagePO(message)
	po.CreatedAt, po.UpdatedAt = message.CreatedAt, message.CreatedAt
	if err := findThread(tx, po, message); err != nil {
		return false, err
	}
	result := tx.Clauses(clause.OnConflict{
		Columns:     []clause.Column{{Name: "mailbox_id"}, {Name: "external_id"}},
		TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "external_id IS NOT NULL"}}},
		DoNothing:   true,
	}).Create(po)
	if result.Error != nil || result.RowsAffected == 0 {
		return false, result.Error
	}
	if err := startThread(tx, po); err != nil {
		return false, err
	}
	message.ID, message.ThreadID = po.ID, *po.ThreadID
	return true, nil
}

func (r *repository) ListExternalMailConnections(ctx context.Context, organizationID, ownerUserID uint) ([]*domain.ExternalMailConnection, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []ExternalMailConnectionPO
	err := r.conn(ctx).
		Where("organization_id = ? AND owner_user_id = ? AND NOT shared", organizationID, ownerUserID).
		// Live connections first: they are bounded by the per-member limit, so none is cut off by
		// revoked rows kept for a later rebind.
		Order("(status = 'revoked'), id ASC").
		Limit(domain.MaxExternalMailConnectionsPerUser).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	connections := make([]*domain.ExternalMailConnection, 0, len(rows))
	for i := range rows {
		connections = append(connections, rows[i].toDomain())
	}
	return connections, nil
}

func (r *repository) ListExternalMailConnectionDirectory(ctx context.Context, organizationID, beforeID uint, limit int) ([]*domain.ExternalMailConnection, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	query := r.conn(ctx).Where("organization_id = ?", organizationID)
	if beforeID != 0 {
		query = query.Where("id < ?", beforeID)
	}
	var rows []ExternalMailConnectionPO
	if err := query.Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	connections := make([]*domain.ExternalMailConnection, 0, len(rows))
	for i := range rows {
		connections = append(connections, rows[i].toDomain())
	}
	return connections, nil
}

func (r *repository) FindExternalMailConnectionByMailbox(ctx context.Context, organizationID, mailboxID uint) (*domain.ExternalMailConnection, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po ExternalMailConnectionPO
	err := r.conn(ctx).Where("organization_id = ? AND mailbox_id = ?", organizationID, mailboxID).First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrExternalMailConnectionNotFound
	}
	if err != nil {
		return nil, err
	}
	return po.toDomain(), nil
}

func (r *repository) FindExternalMailConnection(ctx context.Context, organizationID, id uint) (*domain.ExternalMailConnection, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po ExternalMailConnectionPO
	err := r.conn(ctx).Where("organization_id = ? AND id = ?", organizationID, id).First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrExternalMailConnectionNotFound
	}
	if err != nil {
		return nil, err
	}
	return po.toDomain(), nil
}

func (r *repository) RevokeExternalMailConnection(ctx context.Context, organizationID, id uint, now time.Time) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	result := r.conn(ctx).Model(&ExternalMailConnectionPO{}).
		Where("organization_id = ? AND id = ?", organizationID, id).
		Updates(map[string]any{
			"status": string(domain.ExternalMailStatusRevoked), "encrypted_refresh_token": "", "sync_cursor": "",
			"consecutive_failures": 0, "consecutive_more": 0, "revoked_at": gorm.Expr("COALESCE(revoked_at, ?)", now), "updated_at": now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrExternalMailConnectionNotFound
	}
	return nil
}

func (r *repository) FindExternalMailConnectionByAccount(ctx context.Context, organizationID uint, provider domain.ExternalMailProvider, address string) (*domain.ExternalMailConnection, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var po ExternalMailConnectionPO
	err := r.conn(ctx).
		Where("organization_id = ? AND provider = ? AND lower(account_address) = lower(?)", organizationID, string(provider), address).
		First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrExternalMailConnectionNotFound
	}
	if err != nil {
		return nil, err
	}
	return po.toDomain(), nil
}

func (r *repository) RebindExternalMailConnection(ctx context.Context, rebind domain.ExternalMailRebind) (*domain.ExternalMailConnection, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	updates := map[string]any{
		"status": string(domain.ExternalMailStatusActive), "encrypted_refresh_token": rebind.EncryptedRefreshToken,
		"last_error_code": nil, "next_sync_at": rebind.Now, "updated_at": rebind.Now,
		// A rebind before the retention ends keeps the copies: the purge is canceled.
		"consecutive_failures": 0, "consecutive_more": 0, "revoked_at": nil, "purge_retry_at": nil,
	}
	if rebind.ResetCursor {
		updates["sync_cursor"] = ""
	}
	var rows []ExternalMailConnectionPO
	err := r.conn(ctx).Model(&rows).Clauses(clause.Returning{}).
		Where("id = ?", rebind.ConnectionID).Updates(updates).Error
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrExternalMailConnectionNotFound
	}
	return rows[0].toDomain(), nil
}

// ExternalMailAuthorizationPO is the persistent object of one pending sign-in.
type ExternalMailAuthorizationPO struct {
	ID             uint   `gorm:"primaryKey"`
	OrganizationID uint   `gorm:"not null"`
	UserID         uint   `gorm:"not null"`
	Provider       string `gorm:"size:16;not null"`
	StateHash      string `gorm:"type:char(64);not null;uniqueIndex"`
	SealedVerifier string `gorm:"type:text;not null"`
	ConnectionID   *uint
	// Sharing is the JSON of domain.ExternalMailSharing; NULL for a member's own binding.
	Sharing      *string `gorm:"type:jsonb"`
	BackfillDays int     `gorm:"type:smallint;not null;default:14"`
	ExpiresAt    time.Time
	CreatedAt    time.Time
}

func (ExternalMailAuthorizationPO) TableName() string {
	return "external_mail_authorizations"
}

func (r *repository) CreateExternalMailAuthorization(ctx context.Context, authorization *domain.ExternalMailAuthorization) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	po := &ExternalMailAuthorizationPO{
		OrganizationID: authorization.OrganizationID, UserID: authorization.UserID,
		Provider: string(authorization.Provider), StateHash: authorization.StateHash,
		SealedVerifier: authorization.SealedVerifier, ConnectionID: authorization.ConnectionID,
		BackfillDays: authorization.BackfillDays, ExpiresAt: authorization.ExpiresAt, CreatedAt: authorization.CreatedAt,
	}
	if authorization.Sharing != nil {
		encoded, err := json.Marshal(authorization.Sharing)
		if err != nil {
			return err
		}
		po.Sharing = new(string(encoded))
	}
	return r.conn(ctx).Transaction(func(tx *gorm.DB) error {
		// Abandoned sign-ins expire within minutes; each start sweeps a bounded batch of them.
		if err := tx.Exec(`DELETE FROM external_mail_authorizations WHERE id IN (
			SELECT id FROM external_mail_authorizations WHERE expires_at < ? ORDER BY expires_at LIMIT 100)`,
			authorization.CreatedAt).Error; err != nil {
			return err
		}
		if err := tx.Create(po).Error; err != nil {
			return err
		}
		authorization.ID = po.ID
		return nil
	})
}

func (r *repository) TakeExternalMailAuthorization(ctx context.Context, stateHash string, now time.Time) (*domain.ExternalMailAuthorization, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	var rows []ExternalMailAuthorizationPO
	err := r.conn(ctx).Clauses(clause.Returning{}).
		Where("state_hash = ?", stateHash).Delete(&rows).Error
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 || !rows[0].ExpiresAt.After(now) {
		return nil, ErrExternalMailAuthorizationNotFound
	}
	po := rows[0]
	authorization := &domain.ExternalMailAuthorization{
		ID: po.ID, OrganizationID: po.OrganizationID, UserID: po.UserID,
		Provider: domain.ExternalMailProvider(po.Provider), StateHash: po.StateHash,
		SealedVerifier: po.SealedVerifier, ConnectionID: po.ConnectionID, BackfillDays: po.BackfillDays,
		ExpiresAt: po.ExpiresAt, CreatedAt: po.CreatedAt,
	}
	if po.Sharing != nil {
		authorization.Sharing = &domain.ExternalMailSharing{}
		if err := json.Unmarshal([]byte(*po.Sharing), authorization.Sharing); err != nil {
			return nil, fmt.Errorf("decode external mail sharing: %w", err)
		}
	}
	return authorization, nil
}

func (r *repository) LockExternalMailBindings(ctx context.Context, organizationID, ownerUserID uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	key := fmt.Sprintf("zmail.external_mail_binding:%d:%d", organizationID, ownerUserID)
	return r.conn(ctx).Exec(`SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`, key).Error
}

func (r *repository) CountLiveExternalMailConnections(ctx context.Context, organizationID, ownerUserID uint) (int, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	var count int64
	err := r.conn(ctx).Model(&ExternalMailConnectionPO{}).
		Where("organization_id = ? AND owner_user_id = ? AND NOT shared AND status <> ?", organizationID, ownerUserID, string(domain.ExternalMailStatusRevoked)).
		Count(&count).Error
	return int(count), err
}

func (r *repository) LockSharedExternalMailBindings(ctx context.Context, organizationID uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	key := fmt.Sprintf("zmail.external_mail_shared_binding:%d", organizationID)
	return r.conn(ctx).Exec(`SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`, key).Error
}

func (r *repository) CountLiveSharedExternalMailConnections(ctx context.Context, organizationID uint) (int, error) {
	if r.db == nil {
		return 0, domain.ErrServiceUnavailable
	}
	var count int64
	err := r.conn(ctx).Model(&ExternalMailConnectionPO{}).
		Where("organization_id = ? AND shared AND status <> ?", organizationID, string(domain.ExternalMailStatusRevoked)).
		Count(&count).Error
	return int(count), err
}

func (r *repository) ListSharedExternalMailConnections(ctx context.Context, organizationID, membershipID uint, all bool, limit int) ([]*domain.ExternalMailConnection, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	query := r.conn(ctx).Model(&ExternalMailConnectionPO{}).
		Where("external_mail_connections.organization_id = ? AND external_mail_connections.shared", organizationID).
		// A connection whose mailbox was deleted is revoked and waits for its purge; nobody manages it.
		Where(`EXISTS (SELECT 1 FROM mailboxes WHERE mailboxes.id = external_mail_connections.mailbox_id
			AND mailboxes.deleted_at IS NULL)`)
	if !all {
		query = query.Where(`EXISTS (SELECT 1 FROM mailbox_members WHERE mailbox_members.mailbox_id = external_mail_connections.mailbox_id
			AND mailbox_members.membership_id = ? AND mailbox_members.permission = ?)`, membershipID, string(domain.MailboxPermissionManage))
	}
	var rows []ExternalMailConnectionPO
	if err := query.Order("(status = 'revoked'), id ASC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	connections := make([]*domain.ExternalMailConnection, 0, len(rows))
	for i := range rows {
		connections = append(connections, rows[i].toDomain())
	}
	return connections, nil
}
