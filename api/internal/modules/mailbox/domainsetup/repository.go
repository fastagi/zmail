package domainsetup

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/database"
)

// Errors holds the sentinel errors returned by the domain and zone repository.
type Errors struct {
	DomainNotFound     error
	DomainTaken        error
	DomainLimitReached error
	DomainInUse        error
	DomainNotVerified  error
	ZoneNotFound       error
	ZoneLimitReached   error
	ZoneInUse          error
}

// Repository implements domain.MailDomainRepository and domain.MailZoneRepository against PostgreSQL.
type Repository struct {
	db                    *gorm.DB
	errs                  Errors
	zoneReservationPrefix string
}

var (
	_ domain.MailDomainRepository = (*Repository)(nil)
	_ domain.MailZoneRepository   = (*Repository)(nil)
)

// NewRepository creates a domain and zone persistence repository backed by db.
func NewRepository(db *gorm.DB, errs Errors, zoneReservationPrefix string) *Repository {
	return &Repository{
		db:                    db,
		errs:                  errs,
		zoneReservationPrefix: zoneReservationPrefix,
	}
}

func (r *Repository) conn(ctx context.Context) *gorm.DB {
	return database.ResolveContextDB(ctx, r.db).WithContext(ctx)
}

// TruncateRunes truncates value to at most limit Unicode code points.
func TruncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

// escapeLike makes user text match literally inside a LIKE pattern.
func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

func isUniqueViolation(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	var stateError interface{ SQLState() string }
	return errors.As(err, &stateError) && stateError.SQLState() == "23505"
}

func isForeignKeyViolation(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrForeignKeyViolated) {
		return true
	}
	var stateError interface{ SQLState() string }
	return errors.As(err, &stateError) && stateError.SQLState() == "23503"
}

// mailboxRow mirrors the mailboxes table columns needed by domain repository queries without
// importing package mailbox.
type mailboxRow struct {
	ID                   uint `gorm:"primaryKey"`
	OrganizationID       *uint
	CreatedBy            uint
	Address              string
	DisplayName          string
	RoutingRuleID        string
	Type                 string
	Origin               string
	ConnectedSend        bool
	OwnerUserID          *uint
	PendingOwnerEmail    *string
	OwnerLeftAt          *time.Time
	SealedBefore         *time.Time
	WebhooksEnabled      bool
	RoleID               *uint
	AITriageEnabled      bool `gorm:"column:ai_triage_enabled"`
	ExternalMailTenantID *uint
	CreatedAt            time.Time
	UpdatedAt            time.Time
	DeletedAt            gorm.DeletedAt
}

func (mailboxRow) TableName() string {
	return "mailboxes"
}

func (row *mailboxRow) toDomain() *domain.Mailbox {
	if row == nil {
		return nil
	}
	var organizationID uint
	if row.OrganizationID != nil {
		organizationID = *row.OrganizationID
	}
	var pendingOwnerEmail string
	if row.PendingOwnerEmail != nil {
		pendingOwnerEmail = *row.PendingOwnerEmail
	}
	origin := domain.MailboxOriginZmail
	if row.Origin != "" {
		origin = domain.MailboxOrigin(row.Origin)
	}
	return &domain.Mailbox{
		ID:                row.ID,
		OrganizationID:    organizationID,
		CreatedBy:         row.CreatedBy,
		Address:           row.Address,
		DisplayName:       row.DisplayName,
		RoutingRuleID:     row.RoutingRuleID,
		Type:              domain.MailboxType(row.Type),
		Origin:            origin,
		ConnectedSend:     row.ConnectedSend,
		OwnerUserID:       row.OwnerUserID,
		OwnerLeftAt:       row.OwnerLeftAt,
		PendingOwnerEmail: pendingOwnerEmail,
		SealedBefore:      row.SealedBefore,
		WebhooksEnabled:   row.WebhooksEnabled,
		RoleID:            row.RoleID,
		AITriageEnabled:   row.AITriageEnabled,
		ExternalTenantID:  row.ExternalMailTenantID,
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
	}
}
