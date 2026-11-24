package roster

import (
	"errors"
	"strconv"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

var (
	// ErrPlatformAccountNotFound reports an account that does not exist, belongs to another
	// organization, or that the viewer may not see.
	ErrPlatformAccountNotFound = errors.New("platform account not found")
	// ErrPlatformAccountTaken reports another account of the organization with the same platform
	// and handle, ignoring case.
	ErrPlatformAccountTaken = errors.New("platform account already in the ledger")
)

// PlatformAccountPO is one row of the platform account ledger.
type PlatformAccountPO struct {
	ID             uint    `gorm:"primaryKey"`
	OrganizationID uint    `gorm:"not null;uniqueIndex:idx_platform_accounts_identity,priority:1"`
	Platform       string  `gorm:"size:64;not null;uniqueIndex:idx_platform_accounts_identity,priority:2,expression:lower(platform)"`
	Handle         string  `gorm:"size:200;not null;uniqueIndex:idx_platform_accounts_identity,priority:3,expression:lower(handle)"`
	ProfileURL     *string `gorm:"size:2048"`
	RoleID         *uint
	MailboxID      *uint
	OwnerUserID    *uint
	Status         string `gorm:"size:32;not null;default:'active'"`
	Notes          string `gorm:"size:2000;not null;default:''"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (PlatformAccountPO) TableName() string {
	return "platform_accounts"
}

// ToDomain converts the persistent platform account row to its domain model.
func (po *PlatformAccountPO) ToDomain() *domain.PlatformAccount {
	account := &domain.PlatformAccount{
		ID: po.ID, OrganizationID: po.OrganizationID, Platform: po.Platform, Handle: po.Handle,
		RoleID: po.RoleID, MailboxID: po.MailboxID, OwnerUserID: po.OwnerUserID,
		Status: domain.PlatformAccountStatus(po.Status), Notes: po.Notes, CreatedAt: po.CreatedAt, UpdatedAt: po.UpdatedAt,
	}
	if po.ProfileURL != nil {
		account.ProfileURL = *po.ProfileURL
	}
	return account
}

// PlatformAccountEventPO is one entry of an account's history. AccountID has no foreign key: the
// deleted event outlives the account.
type PlatformAccountEventPO struct {
	ID             uint   `gorm:"primaryKey"`
	OrganizationID uint   `gorm:"not null"`
	AccountID      uint   `gorm:"not null;index:idx_platform_account_events_account,priority:1"`
	Kind           string `gorm:"size:32;not null"`
	ActorUserID    *uint
	FromValue      *string `gorm:"size:200"`
	ToValue        *string `gorm:"size:200"`
	CreatedAt      time.Time
}

func (PlatformAccountEventPO) TableName() string {
	return "platform_account_events"
}

// ToDomain converts the persistent platform account event row to its domain model.
func (po *PlatformAccountEventPO) ToDomain() *domain.PlatformAccountEvent {
	event := &domain.PlatformAccountEvent{
		ID: po.ID, OrganizationID: po.OrganizationID, AccountID: po.AccountID,
		Kind: domain.PlatformAccountEventKind(po.Kind), ActorUserID: po.ActorUserID, CreatedAt: po.CreatedAt,
	}
	if po.FromValue != nil {
		event.From = *po.FromValue
	}
	if po.ToValue != nil {
		event.To = *po.ToValue
	}
	return event
}

// UintText formats an id as decimal text.
func UintText(id uint) string {
	return strconv.FormatUint(uint64(id), 10)
}

// OptionalIDText formats an optional id as event text: empty for none.
func OptionalIDText(id *uint) string {
	if id == nil {
		return ""
	}
	return UintText(*id)
}

// OptionalText stores empty text as NULL.
func OptionalText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
