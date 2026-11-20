package user

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/database/migrations"
	"github.com/fastagi/zmail/api/internal/domain"
	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

const registrationEmailCodeMigration = "2026_11_27_090000_create_registration_email_codes"

func registrationCodeDatabase(t *testing.T) (*gorm.DB, *repository) {
	t.Helper()
	db := testplatform.OpenPostgres(t, nil, &UserPO{})
	require.NoError(t, migrations.All()[registrationEmailCodeMigration].Up(db))
	return db, NewRepository(db)
}

func issueCode(t *testing.T, repo *repository, email, code string, now time.Time) registrationEmailCodeIssue {
	t.Helper()
	issue, err := repo.IssueRegistrationEmailCode(context.Background(), email,
		registrationEmailCodeHash(email, code), now, defaultRegistrationEmailCodePolicy)
	require.NoError(t, err)
	return issue
}

func registerWithCode(repo *repository, email, code string, now time.Time, create func(context.Context) error) error {
	if create == nil {
		create = func(context.Context) error { return nil }
	}
	return repo.RegisterWithEmailCode(context.Background(), email, registrationEmailCodeHash(email, code), now,
		registrationEmailCodeMaxAttempts, create)
}

func TestRegistrationEmailCodeIssueLimits(t *testing.T) {
	db, repo := registrationCodeDatabase(t)
	start := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	email := "new@example.test"

	assert.Equal(t, registrationEmailCodeIssued, issueCode(t, repo, email, "111111", start))
	assert.Equal(t, registrationEmailCodeCoolingDown, issueCode(t, repo, email, "222222", start.Add(59*time.Second)))
	assert.Equal(t, registrationEmailCodeIssued, issueCode(t, repo, "other@example.test", "333333", start.Add(time.Second)),
		"the cooldown is per address")
	for minute := 1; minute < 5; minute++ {
		assert.Equal(t, registrationEmailCodeIssued, issueCode(t, repo, email, "444444", start.Add(time.Duration(minute)*time.Minute)))
	}
	assert.Equal(t, registrationEmailCodeCapped, issueCode(t, repo, email, "555555", start.Add(10*time.Minute)),
		"five codes per address per hour")
	assert.Equal(t, registrationEmailCodeIssued, issueCode(t, repo, email, "666666", start.Add(61*time.Minute)),
		"the window rolls")

	var stored []RegistrationEmailCodePO
	require.NoError(t, db.Where("email = ?", email).Find(&stored).Error)
	for _, row := range stored {
		assert.Len(t, row.CodeHash, 64)
		assert.NotContains(t, row.CodeHash, "666666")
		assert.WithinDuration(t, row.CreatedAt.Add(10*time.Minute), row.ExpiresAt, time.Second)
	}
}

func TestRegisterWithEmailCodeOutcomes(t *testing.T) {
	start := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

	t.Run("no code issued", func(t *testing.T) {
		_, repo := registrationCodeDatabase(t)
		require.ErrorIs(t, registerWithCode(repo, "a@example.test", "123456", start, nil), domain.ErrRegistrationEmailCodeInvalid)
	})

	t.Run("right code creates once, reuse is invalid", func(t *testing.T) {
		_, repo := registrationCodeDatabase(t)
		issueCode(t, repo, "a@example.test", "123456", start)
		created := 0
		create := func(context.Context) error { created++; return nil }
		require.NoError(t, registerWithCode(repo, "a@example.test", "123456", start.Add(time.Minute), create))
		require.ErrorIs(t, registerWithCode(repo, "a@example.test", "123456", start.Add(2*time.Minute), create),
			domain.ErrRegistrationEmailCodeInvalid)
		assert.Equal(t, 1, created)
	})

	t.Run("a code is bound to its address", func(t *testing.T) {
		_, repo := registrationCodeDatabase(t)
		issueCode(t, repo, "a@example.test", "123456", start)
		require.ErrorIs(t, registerWithCode(repo, "b@example.test", "123456", start, nil), domain.ErrRegistrationEmailCodeInvalid)
	})

	t.Run("expired", func(t *testing.T) {
		_, repo := registrationCodeDatabase(t)
		issueCode(t, repo, "a@example.test", "123456", start)
		require.ErrorIs(t, registerWithCode(repo, "a@example.test", "123456", start.Add(10*time.Minute+time.Second), nil),
			domain.ErrRegistrationEmailCodeExpired)
	})

	t.Run("five wrong guesses spend the code", func(t *testing.T) {
		_, repo := registrationCodeDatabase(t)
		issueCode(t, repo, "a@example.test", "123456", start)
		for range registrationEmailCodeMaxAttempts {
			require.ErrorIs(t, registerWithCode(repo, "a@example.test", "000000", start, nil), domain.ErrRegistrationEmailCodeInvalid)
		}
		require.ErrorIs(t, registerWithCode(repo, "a@example.test", "123456", start, nil), domain.ErrRegistrationEmailCodeExpired)
	})

	t.Run("a newer code supersedes the older one", func(t *testing.T) {
		_, repo := registrationCodeDatabase(t)
		issueCode(t, repo, "a@example.test", "111111", start)
		issueCode(t, repo, "a@example.test", "222222", start.Add(2*time.Minute))
		require.ErrorIs(t, registerWithCode(repo, "a@example.test", "111111", start.Add(3*time.Minute), nil),
			domain.ErrRegistrationEmailCodeInvalid)
		require.NoError(t, registerWithCode(repo, "a@example.test", "222222", start.Add(3*time.Minute), nil))
	})

	t.Run("a failed create keeps the code usable and rolls back its writes", func(t *testing.T) {
		db, repo := registrationCodeDatabase(t)
		issueCode(t, repo, "a@example.test", "123456", start)
		err := registerWithCode(repo, "a@example.test", "123456", start, func(ctx context.Context) error {
			require.NoError(t, repo.Create(ctx, &domain.User{Username: "ghost", Email: "a@example.test", Password: "x", Status: 1}))
			return domain.ErrUsernameAlreadyExists
		})
		require.ErrorIs(t, err, domain.ErrUsernameAlreadyExists)
		var users int64
		require.NoError(t, db.Model(&UserPO{}).Count(&users).Error)
		assert.Zero(t, users, "the account insert ran inside the code transaction")

		require.NoError(t, registerWithCode(repo, "a@example.test", "123456", start, func(ctx context.Context) error {
			return repo.Create(ctx, &domain.User{Username: "alice", Email: "a@example.test", Password: "x", Status: 1})
		}))
		require.NoError(t, db.Model(&UserPO{}).Count(&users).Error)
		assert.EqualValues(t, 1, users)
	})
}
