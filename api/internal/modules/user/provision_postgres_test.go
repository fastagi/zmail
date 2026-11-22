package user

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/fastagi/zmail/api/internal/domain"
	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

func TestProvisionUserPostgres(t *testing.T) {
	db := testplatform.OpenPostgres(t, nil, &UserPO{}, &AuthenticationSessionPO{})
	ctx := context.Background()
	repo := NewRepository(db)
	svc := &service{repo: repo, admin: repo}
	const password = "correct-horse-battery"

	storedHash := func(t *testing.T, id uint) string {
		t.Helper()
		var row UserPO
		require.NoError(t, db.First(&row, id).Error)
		return row.Password
	}

	t.Run("creates an active account with a derived username", func(t *testing.T) {
		user, created, err := svc.ProvisionUser(ctx, domain.UserProvisionRequest{Email: " Z@Zmail.IM ", Password: password})
		require.NoError(t, err)
		assert.True(t, created)
		assert.Equal(t, "z@zmail.im", user.Email)
		assert.Equal(t, "z-zmail", user.Username)
		assert.True(t, user.IsActive())
		assert.Empty(t, user.Password)
		require.NoError(t, bcrypt.CompareHashAndPassword([]byte(storedHash(t, user.ID)), []byte(password)))
	})

	t.Run("resets the password, re-enables the account, and revokes its sessions", func(t *testing.T) {
		existing, err := svc.FindUserByEmail(ctx, "z@zmail.im")
		require.NoError(t, err)
		require.NoError(t, db.Model(&UserPO{}).Where("id = ?", existing.ID).Update("status", userStatusDisabled).Error)
		require.NoError(t, db.Create(&AuthenticationSessionPO{
			ID: "provision-session", UserID: existing.ID, TokenHash: "f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0",
			ExpiresAt: time.Now().Add(time.Hour), IdleExpiresAt: time.Now().Add(time.Hour), LastSeenAt: time.Now(),
		}).Error)

		user, created, err := svc.ProvisionUser(ctx, domain.UserProvisionRequest{Email: "z@zmail.im", Password: "another-long-secret"})
		require.NoError(t, err)
		assert.False(t, created)
		assert.Equal(t, existing.ID, user.ID)
		assert.True(t, user.IsActive())
		require.NoError(t, bcrypt.CompareHashAndPassword([]byte(storedHash(t, user.ID)), []byte("another-long-secret")))

		var session AuthenticationSessionPO
		require.NoError(t, db.First(&session, "id = ?", "provision-session").Error)
		assert.NotNil(t, session.RevokedAt)
	})

	t.Run("honors an explicit username and refuses a taken one", func(t *testing.T) {
		user, created, err := svc.ProvisionUser(ctx, domain.UserProvisionRequest{Email: "ops@zmail.im", Username: "ops-admin", Password: password})
		require.NoError(t, err)
		assert.True(t, created)
		assert.Equal(t, "ops-admin", user.Username)

		_, _, err = svc.ProvisionUser(ctx, domain.UserProvisionRequest{Email: "other@zmail.im", Username: "ops-admin", Password: password})
		assert.ErrorIs(t, err, domain.ErrUsernameAlreadyExists)
	})

	t.Run("rejects weak passwords, bad emails, and bad usernames before writing", func(t *testing.T) {
		cases := []domain.UserProvisionRequest{
			{Email: "new@zmail.im", Password: "short"},
			{Email: "new@zmail.im", Password: string(make([]byte, 73))},
			{Email: "not-an-email", Password: password},
			{Email: "Name <new@zmail.im>", Password: password},
			{Email: "new@zmail.im", Username: "a@b", Password: password},
			{Email: "new@zmail.im", Username: "ab", Password: password},
		}
		for _, req := range cases {
			_, _, err := svc.ProvisionUser(ctx, req)
			assert.Error(t, err, "%+v", req.Email)
		}
		_, err := svc.FindUserByEmail(ctx, "new@zmail.im")
		assert.ErrorIs(t, err, domain.ErrUserNotFound)
	})
}
