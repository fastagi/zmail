package mailbox

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
)

// publicMailboxClaim records one mailbox owned by a user on a shared/public domain, live or deleted.
type publicMailboxClaim struct {
	ID             uint
	OrganizationID uint
	Address        string
	Deleted        bool
}

// publicMailboxStore queries and serializes per-user public-domain mailbox claims across all
// organizations, including soft-deleted rows so deleted mailboxes stay counted against the cap.
type publicMailboxStore interface {
	ListUserPublicMailboxes(ctx context.Context, userID uint, domains []string) ([]publicMailboxClaim, error)
	LockPublicMailboxClaim(ctx context.Context, userID uint) error
}

var _ publicMailboxStore = (*repository)(nil)

// ListUserPublicMailboxes lists every Zmail mailbox owned by userID on any of domains across all
// organizations, including soft-deleted tombstones, ordered oldest first.
func (r *repository) ListUserPublicMailboxes(ctx context.Context, userID uint, domains []string) ([]publicMailboxClaim, error) {
	if r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	normalized := make([]string, 0, len(domains))
	for _, raw := range domains {
		if name := normalizeSettingDomain(raw); name != "" {
			normalized = append(normalized, name)
		}
	}
	if userID == 0 || len(normalized) == 0 {
		return nil, nil
	}
	var rows []struct {
		ID             uint
		OrganizationID *uint
		Address        string
		DeletedAt      gorm.DeletedAt
	}
	err := r.conn(ctx).Unscoped().
		Model(&MailboxPO{}).
		Select("id, organization_id, address, deleted_at").
		Where("origin = ? AND owner_user_id = ?", string(domain.MailboxOriginZmail), userID).
		Where("LOWER(split_part(address, '@', 2)) IN ?", normalized).
		Order("id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	claims := make([]publicMailboxClaim, 0, len(rows))
	for _, row := range rows {
		var orgID uint
		if row.OrganizationID != nil {
			orgID = *row.OrganizationID
		}
		claims = append(claims, publicMailboxClaim{
			ID:             row.ID,
			OrganizationID: orgID,
			Address:        strings.ToLower(row.Address),
			Deleted:        row.DeletedAt.Valid,
		})
	}
	return claims, nil
}

// LockPublicMailboxClaim holds a transaction-scoped advisory lock per user so concurrent claim
// requests cannot both pass the global per-user public mailbox limit.
func (r *repository) LockPublicMailboxClaim(ctx context.Context, userID uint) error {
	if r.db == nil {
		return domain.ErrServiceUnavailable
	}
	return r.conn(ctx).Exec(`SELECT pg_advisory_xact_lock(hashtext('public_mailbox_claim'), ?)`, int32(userID)).Error //nolint:gosec // lock key only needs to be stable per user
}
