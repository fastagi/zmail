package externalmail

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/database"
)

// MaxConsecutiveFailures keeps the failure count inside its smallint column; the backoff is
// capped long before it.
const MaxConsecutiveFailures = 10000

// Errors holds the sentinel errors returned by the external mail repository.
type Errors struct {
	ConnectionNotFound    error
	AuthorizationNotFound error
	PurgeRefused          error
	TenantNotFound        error
	TenantInactive        error
}

// Hooks supplies parent-package message persistence helpers without creating an import cycle.
type Hooks struct {
	CreateExternalMessage func(tx *gorm.DB, mailboxID uint, message *domain.MailMessage) (bool, error)
	DeleteMessages        func(tx *gorm.DB, ids []uint) (int, error)
}

// Repository implements the external mail connection, push, release, ops, and tenant persistence
// repositories against PostgreSQL.
type Repository struct {
	db    *gorm.DB
	errs  Errors
	hooks Hooks
}

var (
	_ domain.ExternalMailRepository        = (*Repository)(nil)
	_ domain.ExternalMailPushRepository    = (*Repository)(nil)
	_ domain.ExternalMailReleaseRepository = (*Repository)(nil)
	_ domain.ExternalMailOpsRepository     = (*Repository)(nil)
	_ domain.ExternalMailTenantRepository  = (*Repository)(nil)
)

// NewRepository creates an external mail persistence repository backed by db.
func NewRepository(db *gorm.DB, errs Errors, hooks Hooks) *Repository {
	return &Repository{
		db:    db,
		errs:  errs,
		hooks: hooks,
	}
}

func (r *Repository) conn(ctx context.Context) *gorm.DB {
	return database.ResolveContextDB(ctx, r.db).WithContext(ctx)
}

// mailboxRow mirrors the mailboxes table columns updated by external mail repository queries
// without importing package mailbox.
type mailboxRow struct {
	ID                   uint `gorm:"primaryKey"`
	OrganizationID       *uint
	Origin               string
	ConnectedSend        bool
	ExternalMailTenantID *uint
	UpdatedAt            time.Time
	DeletedAt            gorm.DeletedAt
}

func (mailboxRow) TableName() string {
	return "mailboxes"
}

func optionalText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func optionalUserID(id uint) *uint {
	if id == 0 {
		return nil
	}
	return &id
}

// laterTime is the later of two optional times; nil when both are.
func laterTime(a, b *time.Time) *time.Time {
	switch {
	case a == nil:
		return b
	case b == nil || a.After(*b):
		return a
	}
	return b
}
