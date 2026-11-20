package operator

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fastagi/zmail/api/internal/domain"
	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
	"github.com/fastagi/zmail/api/internal/modules/user"
)

func TestGrantRepositoryPostgres(t *testing.T) {
	db := testplatform.OpenPostgres(t, nil, &user.UserPO{})
	require.NoError(t, db.Exec(`CREATE TABLE platform_operators (
		user_id bigint NOT NULL,
		granted_at timestamptz NOT NULL,
		scope varchar(16) NOT NULL DEFAULT 'full',
		mail_content boolean NOT NULL DEFAULT false,
		CONSTRAINT platform_operators_pkey PRIMARY KEY (user_id)
	)`).Error)
	require.NoError(t, db.Exec(`ALTER TABLE platform_operators ADD CONSTRAINT fk_platform_operators_user
		FOREIGN KEY (user_id) REFERENCES users(id) ON UPDATE CASCADE ON DELETE CASCADE`).Error)

	operator := &user.UserPO{Username: "ops", Email: "ops@example.test", Password: "x", Status: 1}
	member := &user.UserPO{Username: "member", Email: "member@example.test", Password: "x", Status: 1}
	require.NoError(t, db.Create(operator).Error)
	require.NoError(t, db.Create(member).Error)

	ctx := context.Background()
	repo := NewRepository(db)
	now := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)

	full := domain.OperatorAccess{Scope: domain.OperatorScopeFull}
	metrics := domain.OperatorAccess{Scope: domain.OperatorScopeMailMetrics}
	change, grantedAt, err := repo.upsertGrant(ctx, operator.ID, full, now)
	require.NoError(t, err)
	assert.Equal(t, domain.OperatorGrantCreated, change)
	assert.True(t, grantedAt.Equal(now))

	change, grantedAt, err = repo.upsertGrant(ctx, operator.ID, full, now.Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, domain.OperatorGrantUnchanged, change, "repeating a grant is a no-op")
	assert.True(t, grantedAt.Equal(now), "the original grant time is kept")

	change, grantedAt, err = repo.upsertGrant(ctx, operator.ID, metrics, now.Add(2*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, domain.OperatorGrantScopeChanged, change)
	assert.True(t, grantedAt.Equal(now))
	access, found, err := repo.grantAccess(ctx, operator.ID)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, metrics, access)
	_, found, err = repo.grantAccess(ctx, member.ID)
	require.NoError(t, err)
	assert.False(t, found)

	withContent := domain.OperatorAccess{Scope: domain.OperatorScopeMailMetrics, MailContent: true}
	change, _, err = repo.upsertGrant(ctx, operator.ID, withContent, now)
	require.NoError(t, err)
	assert.Equal(t, domain.OperatorGrantScopeChanged, change, "adding content changes the grant")
	access, _, err = repo.grantAccess(ctx, operator.ID)
	require.NoError(t, err)
	assert.Equal(t, withContent, access)
	change, _, err = repo.upsertGrant(ctx, operator.ID, withContent, now)
	require.NoError(t, err)
	assert.Equal(t, domain.OperatorGrantUnchanged, change)
	change, _, err = repo.upsertGrant(ctx, operator.ID, full, now)
	require.NoError(t, err)
	assert.Equal(t, domain.OperatorGrantScopeChanged, change)
	access, _, err = repo.grantAccess(ctx, operator.ID)
	require.NoError(t, err)
	assert.Equal(t, full, access, "a grant command sets the whole grant, so content is removed")

	ok, err := repo.isOperator(ctx, operator.ID)
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = repo.isOperator(ctx, member.ID)
	require.NoError(t, err)
	assert.False(t, ok)

	ids, err := repo.operatorIDs(ctx, []uint{operator.ID, member.ID})
	require.NoError(t, err)
	assert.Equal(t, map[uint]bool{operator.ID: true}, ids)

	_, _, err = repo.upsertGrant(ctx, 999999, full, now)
	require.Error(t, err, "a grant must reference an existing user")

	require.NoError(t, db.Unscoped().Delete(&user.UserPO{}, operator.ID).Error)
	ok, err = repo.isOperator(ctx, operator.ID)
	require.NoError(t, err)
	assert.False(t, ok, "deleting the account removes its grant")

	removed, err := repo.deleteGrant(ctx, member.ID)
	require.NoError(t, err)
	assert.False(t, removed)
}
