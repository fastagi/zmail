package operator

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
)

// grantPO is one platform-operator grant. Only its scope is ever updated.
type grantPO struct {
	UserID    uint      `gorm:"column:user_id;primaryKey"`
	GrantedAt time.Time `gorm:"column:granted_at"`
	Scope     string    `gorm:"column:scope"`
}

func (grantPO) TableName() string {
	return "platform_operators"
}

type grantStore interface {
	isOperator(ctx context.Context, userID uint) (bool, error)
	grantScope(ctx context.Context, userID uint) (scope domain.OperatorScope, found bool, err error)
	operatorIDs(ctx context.Context, userIDs []uint) (map[uint]bool, error)
	upsertGrant(ctx context.Context, userID uint, scope domain.OperatorScope, now time.Time) (domain.OperatorGrantChange, time.Time, error)
	deleteGrant(ctx context.Context, userID uint) (bool, error)
	listGrants(ctx context.Context) ([]grantPO, error)
	ping(ctx context.Context) error
}

type repository struct {
	db *gorm.DB
}

// NewRepository creates the operator grant repository.
func NewRepository(db *gorm.DB) *repository {
	return &repository{db: db}
}

func (r *repository) conn(ctx context.Context) (*gorm.DB, error) {
	if r == nil || r.db == nil {
		return nil, domain.ErrServiceUnavailable
	}
	return r.db.WithContext(ctx), nil
}

func (r *repository) isOperator(ctx context.Context, userID uint) (bool, error) {
	db, err := r.conn(ctx)
	if err != nil {
		return false, err
	}
	var count int64
	if err := db.Model(&grantPO{}).Where("user_id = ?", userID).Limit(1).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *repository) grantScope(ctx context.Context, userID uint) (domain.OperatorScope, bool, error) {
	db, err := r.conn(ctx)
	if err != nil {
		return "", false, err
	}
	var rows []grantPO
	if err := db.Where("user_id = ?", userID).Limit(1).Find(&rows).Error; err != nil {
		return "", false, err
	}
	if len(rows) == 0 {
		return "", false, nil
	}
	return domain.OperatorScope(rows[0].Scope), true, nil
}

func (r *repository) operatorIDs(ctx context.Context, userIDs []uint) (map[uint]bool, error) {
	result := make(map[uint]bool, len(userIDs))
	if len(userIDs) == 0 {
		return result, nil
	}
	db, err := r.conn(ctx)
	if err != nil {
		return nil, err
	}
	var ids []uint
	if err := db.Model(&grantPO{}).Where("user_id IN ?", userIDs).Pluck("user_id", &ids).Error; err != nil {
		return nil, err
	}
	for _, id := range ids {
		result[id] = true
	}
	return result, nil
}

// upsertGrant creates the grant or changes its scope. The original grant time is kept.
func (r *repository) upsertGrant(
	ctx context.Context,
	userID uint,
	scope domain.OperatorScope,
	now time.Time,
) (domain.OperatorGrantChange, time.Time, error) {
	db, err := r.conn(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	row := grantPO{UserID: userID, GrantedAt: now.UTC(), Scope: string(scope)}
	insert := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if insert.Error != nil {
		return "", time.Time{}, insert.Error
	}
	if insert.RowsAffected == 1 {
		return domain.OperatorGrantCreated, row.GrantedAt, nil
	}
	update := db.Model(&grantPO{}).Where("user_id = ? AND scope <> ?", userID, string(scope)).
		Update("scope", string(scope))
	if update.Error != nil {
		return "", time.Time{}, update.Error
	}
	var existing grantPO
	if err := db.Where("user_id = ?", userID).First(&existing).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", time.Time{}, domain.ErrConflict
		}
		return "", time.Time{}, err
	}
	if update.RowsAffected == 1 {
		return domain.OperatorGrantScopeChanged, existing.GrantedAt, nil
	}
	return domain.OperatorGrantUnchanged, existing.GrantedAt, nil
}

func (r *repository) deleteGrant(ctx context.Context, userID uint) (bool, error) {
	db, err := r.conn(ctx)
	if err != nil {
		return false, err
	}
	result := db.Where("user_id = ?", userID).Delete(&grantPO{})
	return result.RowsAffected > 0, result.Error
}

func (r *repository) listGrants(ctx context.Context) ([]grantPO, error) {
	db, err := r.conn(ctx)
	if err != nil {
		return nil, err
	}
	var rows []grantPO
	if err := db.Order("granted_at ASC, user_id ASC").Limit(1000).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *repository) ping(ctx context.Context) error {
	db, err := r.conn(ctx)
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}
