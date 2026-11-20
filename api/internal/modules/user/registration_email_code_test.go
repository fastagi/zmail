package user

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/events"
	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// fakeEmailCodeStore records issued codes and runs registration callbacks like the repository.
type fakeEmailCodeStore struct {
	issued       []issuedEmailCode
	issueResult  registrationEmailCodeIssue
	issueErr     error
	registerErr  error
	registerArgs []string
}

type issuedEmailCode struct {
	email     string
	codeHash  string
	expiresAt time.Time
	policy    registrationEmailCodePolicy
}

func (s *fakeEmailCodeStore) IssueRegistrationEmailCode(
	_ context.Context,
	email string,
	codeHash string,
	now time.Time,
	policy registrationEmailCodePolicy,
) (registrationEmailCodeIssue, error) {
	if s.issueErr != nil {
		return "", s.issueErr
	}
	if s.issueResult != "" && s.issueResult != registrationEmailCodeIssued {
		return s.issueResult, nil
	}
	s.issued = append(s.issued, issuedEmailCode{
		email: email, codeHash: codeHash, expiresAt: now.Add(policy.TTL), policy: policy,
	})
	return registrationEmailCodeIssued, nil
}

func (s *fakeEmailCodeStore) RegisterWithEmailCode(
	ctx context.Context,
	email string,
	codeHash string,
	_ time.Time,
	_ int,
	create func(context.Context) error,
) error {
	s.registerArgs = append(s.registerArgs, email, codeHash)
	if s.registerErr != nil {
		return s.registerErr
	}
	return create(ctx)
}

func newEmailCodeTestService(repo *fakeRepo, store *fakeEmailCodeStore, mailer UserMailer) *service {
	svc := NewService(repo, repo, &fakeSessionIssuer{}, events.NewEventBus(), mailer, NewAccountDeletionPolicy())
	svc.runPasswordReset = runInline
	svc.runRegistrationCode = runInline
	svc.emailCodes = store
	svc.emailCodeRequired = true
	return svc
}

func TestRequestRegistrationEmailCodeMailsACodeMatchingTheStoredHash(t *testing.T) {
	store := &fakeEmailCodeStore{}
	mailer := testplatform.NewFakeMailer()
	svc := newEmailCodeTestService(&fakeRepo{}, store, mailer)

	require.NoError(t, svc.RequestRegistrationEmailCode(context.Background(),
		&RegistrationEmailCodeRequest{Email: " New@Example.test "}))

	sent := mailer.AssertSent(t, "new@example.test", "registration_code")
	assert.Regexp(t, regexp.MustCompile(`^[0-9]{6}$`), sent.Token)
	require.Len(t, store.issued, 1)
	issued := store.issued[0]
	assert.Equal(t, "new@example.test", issued.email, "codes are keyed by the normalized address")
	assert.Equal(t, registrationEmailCodeHash("new@example.test", sent.Token), issued.codeHash,
		"only the hash of the mailed code may be stored")
	assert.NotContains(t, issued.codeHash, sent.Token)
	assert.Equal(t, 10*time.Minute, issued.policy.TTL)
	assert.Equal(t, 60*time.Second, issued.policy.Cooldown)
	assert.Equal(t, time.Hour, issued.policy.Window)
	assert.Equal(t, 5, issued.policy.MaxPerWindow)
}

func TestRequestRegistrationEmailCodeSendsNothingToAnExistingAccount(t *testing.T) {
	store := &fakeEmailCodeStore{}
	mailer := testplatform.NewFakeMailer()
	repo := &fakeRepo{findByEmailFn: func(context.Context, string) (*domain.User, error) {
		return &domain.User{ID: 3, Email: "taken@example.test"}, nil
	}}
	svc := newEmailCodeTestService(repo, store, mailer)

	require.NoError(t, svc.RequestRegistrationEmailCode(context.Background(),
		&RegistrationEmailCodeRequest{Email: "taken@example.test"}))

	mailer.AssertNothingSent(t)
	assert.Empty(t, store.issued)
}

func TestRequestRegistrationEmailCodeSendsNothingWhenTheStoreRefuses(t *testing.T) {
	for _, refusal := range []registrationEmailCodeIssue{registrationEmailCodeCoolingDown, registrationEmailCodeCapped} {
		store := &fakeEmailCodeStore{issueResult: refusal}
		mailer := testplatform.NewFakeMailer()
		svc := newEmailCodeTestService(&fakeRepo{}, store, mailer)

		require.NoError(t, svc.RequestRegistrationEmailCode(context.Background(),
			&RegistrationEmailCodeRequest{Email: "new@example.test"}), "a refusal never changes the answer")
		mailer.AssertNothingSent(t)
	}
}

func TestRequestRegistrationEmailCodeHidesProcessingFailures(t *testing.T) {
	store := &fakeEmailCodeStore{issueErr: errors.New("database down")}
	mailer := testplatform.NewFakeMailer()
	svc := newEmailCodeTestService(&fakeRepo{}, store, mailer)

	require.NoError(t, svc.RequestRegistrationEmailCode(context.Background(),
		&RegistrationEmailCodeRequest{Email: "new@example.test"}))
	mailer.AssertNothingSent(t)
}

func TestRequestRegistrationEmailCodeRefusesClosedRegistration(t *testing.T) {
	store := &fakeEmailCodeStore{}
	mailer := testplatform.NewFakeMailer()
	svc := newEmailCodeTestService(&fakeRepo{}, store, mailer)
	svc.registrationOpen = false

	err := svc.RequestRegistrationEmailCode(context.Background(),
		&RegistrationEmailCodeRequest{Email: "new@example.test"})

	require.ErrorIs(t, err, domain.ErrRegistrationDisabled)
	mailer.AssertNothingSent(t)
	assert.Empty(t, store.issued)
}

func TestRegisterRequiresAnEmailCodeWhenConfigured(t *testing.T) {
	var created int
	repo := &fakeRepo{createFn: func(context.Context, *domain.User) error { created++; return nil }}
	store := &fakeEmailCodeStore{}
	svc := newEmailCodeTestService(repo, store, testplatform.NewFakeMailer())

	_, err := svc.Register(context.Background(), &UserRegisterRequest{
		Username: "alice", Email: "alice@example.test", Password: "password123",
	})

	require.ErrorIs(t, err, domain.ErrRegistrationEmailCodeRequired)
	assert.Zero(t, created)
	assert.Empty(t, store.registerArgs, "a missing code never reaches the code store")
}

func TestRegisterCreatesTheAccountInsideTheCodeCheck(t *testing.T) {
	var created *domain.User
	var insideCheck bool
	store := &fakeEmailCodeStore{}
	repo := &fakeRepo{createFn: func(_ context.Context, user *domain.User) error {
		insideCheck = len(store.registerArgs) == 2
		created = user
		return nil
	}}
	svc := newEmailCodeTestService(repo, store, testplatform.NewFakeMailer())

	user, err := svc.Register(context.Background(), &UserRegisterRequest{
		Username: "alice", Email: "Alice@Example.test", Password: "password123", EmailCode: "123456",
	})

	require.NoError(t, err)
	require.NotNil(t, created)
	assert.True(t, insideCheck, "the account is created inside the code transaction")
	assert.Equal(t, user, created)
	assert.Equal(t, "Alice@Example.test", created.Email, "the account keeps the address as entered")
	assert.Equal(t, []string{"alice@example.test", registrationEmailCodeHash("alice@example.test", "123456")}, store.registerArgs)
}

func TestRegisterReturnsTheCodeStoreError(t *testing.T) {
	for _, storeErr := range []error{domain.ErrRegistrationEmailCodeInvalid, domain.ErrRegistrationEmailCodeExpired} {
		var created int
		repo := &fakeRepo{createFn: func(context.Context, *domain.User) error { created++; return nil }}
		svc := newEmailCodeTestService(repo, &fakeEmailCodeStore{registerErr: storeErr}, testplatform.NewFakeMailer())

		_, err := svc.Register(context.Background(), &UserRegisterRequest{
			Username: "alice", Email: "alice@example.test", Password: "password123", EmailCode: "000000",
		})

		require.ErrorIs(t, err, storeErr)
		assert.Zero(t, created)
	}
}

func TestRegisterChecksConflictsOnlyAfterTheCode(t *testing.T) {
	var lookedUp bool
	repo := &fakeRepo{findByEmailFn: func(context.Context, string) (*domain.User, error) {
		lookedUp = true
		return &domain.User{ID: 9}, nil
	}}
	svc := newEmailCodeTestService(repo, &fakeEmailCodeStore{registerErr: domain.ErrRegistrationEmailCodeInvalid}, testplatform.NewFakeMailer())

	_, err := svc.Register(context.Background(), &UserRegisterRequest{
		Username: "alice", Email: "taken@example.test", Password: "password123", EmailCode: "000000",
	})

	require.ErrorIs(t, err, domain.ErrRegistrationEmailCodeInvalid,
		"without a valid code the caller cannot learn that the address has an account")
	assert.False(t, lookedUp)
}

func TestRegisterIgnoresTheEmailCodeWhenNotRequired(t *testing.T) {
	var created int
	repo := &fakeRepo{createFn: func(context.Context, *domain.User) error { created++; return nil }}
	store := &fakeEmailCodeStore{registerErr: domain.ErrRegistrationEmailCodeInvalid}
	svc := newEmailCodeTestService(repo, store, testplatform.NewFakeMailer())
	svc.emailCodeRequired = false

	_, err := svc.Register(context.Background(), &UserRegisterRequest{
		Username: "alice", Email: "alice@example.test", Password: "password123", EmailCode: "999999",
	})

	require.NoError(t, err)
	assert.Equal(t, 1, created)
	assert.Empty(t, store.registerArgs)
}

func TestProvideServiceReadsTheEmailCodeSwitch(t *testing.T) {
	repo := &fakeRepo{findByEmailFn: func(context.Context, string) (*domain.User, error) {
		return nil, gorm.ErrRecordNotFound
	}}
	build := func(required bool) *service {
		return ProvideService(
			repo, repo, &fakeSessionIssuer{}, events.NewEventBus(), &fakeUserMailer{},
			NewAccountDeletionPolicy(), newAuthAbuseGuard(config.AuthenticationRateLimitConfig{}),
			&config.Config{Authentication: config.AuthenticationConfig{RegistrationEmailCodeRequired: required}},
		)
	}
	assert.True(t, build(true).emailCodeRequired)
	assert.False(t, build(false).emailCodeRequired)
}

func TestRegistrationEmailCodeHashBindsTheAddress(t *testing.T) {
	assert.Len(t, registrationEmailCodeHash("a@example.test", "123456"), 64)
	assert.NotEqual(t,
		registrationEmailCodeHash("a@example.test", "123456"),
		registrationEmailCodeHash("b@example.test", "123456"))
}
