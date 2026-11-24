package mailbox

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/modules/mailbox/externalmail"
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
type ExternalMailConnectionPO = externalmail.ExternalMailConnectionPO

// ExternalMailAuthorizationPO is the persistent object of one pending sign-in.
type ExternalMailAuthorizationPO = externalmail.ExternalMailAuthorizationPO

var _ domain.ExternalMailRepository = (*repository)(nil)

func (r *repository) externalMailRepo() *externalmail.Repository {
	return externalmail.NewRepository(r.db, externalmail.Errors{
		ConnectionNotFound:    ErrExternalMailConnectionNotFound,
		AuthorizationNotFound: ErrExternalMailAuthorizationNotFound,
		PurgeRefused:          ErrExternalMailPurgeRefused,
		TenantNotFound:        ErrExternalMailTenantNotFound,
		TenantInactive:        ErrExternalMailTenantInactive,
	}, externalmail.Hooks{
		CreateExternalMessage: createExternalMessage,
		DeleteMessages:        deleteMessages,
	})
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

func (r *repository) CreateExternalMailConnection(ctx context.Context, connection *domain.ExternalMailConnection) error {
	return r.externalMailRepo().CreateExternalMailConnection(ctx, connection)
}

func (r *repository) ClaimExternalMailConnections(ctx context.Context, claim domain.ExternalMailClaim) ([]*domain.ExternalMailConnection, error) {
	return r.externalMailRepo().ClaimExternalMailConnections(ctx, claim)
}

func (r *repository) SaveExternalMailSync(ctx context.Context, result domain.ExternalMailSyncResult) ([]*domain.MailMessage, bool, error) {
	return r.externalMailRepo().SaveExternalMailSync(ctx, result)
}

func (r *repository) ListExternalMailConnections(ctx context.Context, organizationID, ownerUserID uint) ([]*domain.ExternalMailConnection, error) {
	return r.externalMailRepo().ListExternalMailConnections(ctx, organizationID, ownerUserID)
}

func (r *repository) ListExternalMailConnectionDirectory(ctx context.Context, organizationID, beforeID uint, limit int) ([]*domain.ExternalMailConnection, error) {
	return r.externalMailRepo().ListExternalMailConnectionDirectory(ctx, organizationID, beforeID, limit)
}

func (r *repository) FindExternalMailConnectionByMailbox(ctx context.Context, organizationID, mailboxID uint) (*domain.ExternalMailConnection, error) {
	return r.externalMailRepo().FindExternalMailConnectionByMailbox(ctx, organizationID, mailboxID)
}

func (r *repository) FindExternalMailConnection(ctx context.Context, organizationID, id uint) (*domain.ExternalMailConnection, error) {
	return r.externalMailRepo().FindExternalMailConnection(ctx, organizationID, id)
}

func (r *repository) RevokeExternalMailConnection(ctx context.Context, organizationID, id uint, now time.Time) error {
	return r.externalMailRepo().RevokeExternalMailConnection(ctx, organizationID, id, now)
}

func (r *repository) FindExternalMailConnectionByAccount(ctx context.Context, organizationID uint, provider domain.ExternalMailProvider, address string) (*domain.ExternalMailConnection, error) {
	return r.externalMailRepo().FindExternalMailConnectionByAccount(ctx, organizationID, provider, address)
}

func (r *repository) RebindExternalMailConnection(ctx context.Context, rebind domain.ExternalMailRebind) (*domain.ExternalMailConnection, error) {
	return r.externalMailRepo().RebindExternalMailConnection(ctx, rebind)
}

func (r *repository) SetConnectedMailboxSend(ctx context.Context, organizationID, mailboxID uint, enabled bool) error {
	return r.externalMailRepo().SetConnectedMailboxSend(ctx, organizationID, mailboxID, enabled)
}

func (r *repository) CreateExternalMailAuthorization(ctx context.Context, authorization *domain.ExternalMailAuthorization) error {
	return r.externalMailRepo().CreateExternalMailAuthorization(ctx, authorization)
}

func (r *repository) TakeExternalMailAuthorization(ctx context.Context, stateHash string, now time.Time) (*domain.ExternalMailAuthorization, error) {
	return r.externalMailRepo().TakeExternalMailAuthorization(ctx, stateHash, now)
}

func (r *repository) LockExternalMailBindings(ctx context.Context, organizationID, ownerUserID uint) error {
	return r.externalMailRepo().LockExternalMailBindings(ctx, organizationID, ownerUserID)
}

func (r *repository) CountLiveExternalMailConnections(ctx context.Context, organizationID, ownerUserID uint) (int, error) {
	return r.externalMailRepo().CountLiveExternalMailConnections(ctx, organizationID, ownerUserID)
}

func (r *repository) LockSharedExternalMailBindings(ctx context.Context, organizationID uint) error {
	return r.externalMailRepo().LockSharedExternalMailBindings(ctx, organizationID)
}

func (r *repository) CountLiveSharedExternalMailConnections(ctx context.Context, organizationID uint) (int, error) {
	return r.externalMailRepo().CountLiveSharedExternalMailConnections(ctx, organizationID)
}

func (r *repository) ListSharedExternalMailConnections(ctx context.Context, organizationID, membershipID uint, all bool, limit int) ([]*domain.ExternalMailConnection, error) {
	return r.externalMailRepo().ListSharedExternalMailConnections(ctx, organizationID, membershipID, all, limit)
}
