package organization

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/capabilities/crypto"
	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/ratelimit"
	"github.com/fastagi/zmail/api/pkg/response"
)

// recordingInvitationMailer captures every token it is asked to send.
type recordingInvitationMailer struct {
	configured bool
	tokens     []string
}

func (m *recordingInvitationMailer) IsConfigured() bool { return m.configured }

func (m *recordingInvitationMailer) SendInvitation(
	_ context.Context, _, _ string, _ domain.OrganizationRole, token string, _ time.Time,
) error {
	m.tokens = append(m.tokens, token)
	return nil
}

type invitationResendFixture struct {
	db           *gorm.DB
	repo         *repository
	service      *service
	mailer       *recordingInvitationMailer
	organization *domain.Organization
	ownerID      uint
	adminID      uint
	memberID     uint
}

func newInvitationResendFixture(t *testing.T) *invitationResendFixture {
	t.Helper()
	db := newOrganizationRepositoryTestDB(t)
	repo := NewRepository(db)
	ownerID := createOrganizationTestUser(t, db, "resend-owner")
	adminID := createOrganizationTestUser(t, db, "resend-admin")
	memberID := createOrganizationTestUser(t, db, "resend-member")
	organization := createInvitationTestOrganization(t, db, ownerID)
	for userID, role := range map[uint]domain.OrganizationRole{
		adminID:  domain.OrganizationRoleAdmin,
		memberID: domain.OrganizationRoleMember,
	} {
		require.NoError(t, db.Omit(clause.Associations).Create(&OrganizationMembershipPO{
			OrganizationID: organization.ID, UserID: userID, Role: string(role),
		}).Error)
	}
	mailer := &recordingInvitationMailer{configured: true}
	svc := NewService(repo, repo, mailer, NewInvitationPolicy(nil, ratelimit.NewMemoryStores()), nil)
	return &invitationResendFixture{
		db: db, repo: repo, service: svc, mailer: mailer, organization: organization,
		ownerID: ownerID, adminID: adminID, memberID: memberID,
	}
}

func (f *invitationResendFixture) invite(t *testing.T, email string) *OrganizationInvitationResult {
	t.Helper()
	result, err := f.service.Invite(context.Background(), f.ownerID, f.organization.ID,
		&CreateOrganizationInvitationRequest{Email: email, Role: domain.OrganizationRoleMember})
	require.NoError(t, err)
	return result
}

func (f *invitationResendFixture) stored(t *testing.T, invitationID uint) OrganizationInvitationPO {
	t.Helper()
	var po OrganizationInvitationPO
	require.NoError(t, f.db.First(&po, invitationID).Error)
	return po
}

func TestResendOnlyTheLatestEmailedTokenAccepts(t *testing.T) {
	f := newInvitationResendFixture(t)
	inviteeID := createOrganizationTestUser(t, f.db, "rotating")
	invited := f.invite(t, "rotating@example.com")
	require.Len(t, f.mailer.tokens, 1)

	result, err := f.service.ResendInvitation(context.Background(), f.adminID, f.organization.ID, invited.Invitation.ID)
	require.NoError(t, err)
	assert.Equal(t, InvitationEmailSendStatusAcceptedByProvider, result.EmailSendStatus)
	require.Len(t, f.mailer.tokens, 2)

	_, err = f.service.AcceptInvitation(context.Background(), inviteeID,
		&AcceptOrganizationInvitationRequest{Token: f.mailer.tokens[0]})
	require.ErrorIs(t, err, domain.ErrOrganizationInvitationInvalid)
	_, err = f.service.AcceptInvitation(context.Background(), inviteeID,
		&AcceptOrganizationInvitationRequest{Token: f.mailer.tokens[1]})
	require.NoError(t, err)
}

func TestResendWithoutAMailerKeepsTheLinkAndExpiry(t *testing.T) {
	f := newInvitationResendFixture(t)
	invited := f.invite(t, "unsent@example.com")
	before := f.stored(t, invited.Invitation.ID)
	f.mailer.configured = false

	result, err := f.service.ResendInvitation(context.Background(), f.ownerID, f.organization.ID, invited.Invitation.ID)
	require.NoError(t, err)
	assert.Equal(t, InvitationEmailSendStatusNotConfigured, result.EmailSendStatus)
	after := f.stored(t, invited.Invitation.ID)
	assert.Equal(t, before.TokenHash, after.TokenHash, "nothing carries a new token, so the old link must keep working")
	assert.True(t, before.ExpiresAt.Equal(after.ExpiresAt))
	require.NotNil(t, after.EmailSendStatus)
	assert.Equal(t, string(InvitationEmailSendStatusNotConfigured), *after.EmailSendStatus)
}

func TestResendIsForManagersAndPendingInvitationsOnly(t *testing.T) {
	f := newInvitationResendFixture(t)
	invited := f.invite(t, "guarded@example.com")
	ctx := context.Background()

	_, err := f.service.ResendInvitation(ctx, f.memberID, f.organization.ID, invited.Invitation.ID)
	require.ErrorIs(t, err, domain.ErrPermissionDenied)
	outsiderID := createOrganizationTestUser(t, f.db, "resend-outsider")
	_, err = f.service.ResendInvitation(ctx, outsiderID, f.organization.ID, invited.Invitation.ID)
	require.ErrorIs(t, err, domain.ErrOrganizationNotFound)
	_, err = f.service.ResendInvitation(ctx, f.ownerID, f.organization.ID, invited.Invitation.ID+1000)
	require.ErrorIs(t, err, domain.ErrOrganizationInvitationNotFound)

	require.NoError(t, f.service.RevokeInvitation(ctx, f.ownerID, f.organization.ID, invited.Invitation.ID))
	_, err = f.service.ResendInvitation(ctx, f.ownerID, f.organization.ID, invited.Invitation.ID)
	require.ErrorIs(t, err, domain.ErrOrganizationInvitationNotFound)

	expiring := f.invite(t, "expiring@example.com")
	f.service.now = func() time.Time { return expiring.Invitation.ExpiresAt.Add(time.Second) }
	_, err = f.service.ResendInvitation(ctx, f.ownerID, f.organization.ID, expiring.Invitation.ID)
	require.ErrorIs(t, err, domain.ErrOrganizationInvitationNotFound)
	assert.Len(t, f.mailer.tokens, 2, "refused resends send nothing")
}

func TestResendIsRateLimitedPerInvitation(t *testing.T) {
	f := newInvitationResendFixture(t)
	first := f.invite(t, "limited@example.com")
	other := f.invite(t, "other@example.com")
	ctx := context.Background()

	_, err := f.service.ResendInvitation(ctx, f.ownerID, f.organization.ID, first.Invitation.ID)
	require.NoError(t, err)
	_, err = f.service.ResendInvitation(ctx, f.ownerID, f.organization.ID, first.Invitation.ID)
	var retry *InvitationRetryAfterError
	require.ErrorAs(t, err, &retry)
	require.ErrorIs(t, err, ErrInvitationResendRateLimited)
	assert.Greater(t, retry.RetryAfter, time.Duration(0))
	assert.LessOrEqual(t, retry.RetryAfter, time.Minute)
	assert.Len(t, f.mailer.tokens, 3, "the refused resend sends nothing")

	_, err = f.service.ResendInvitation(ctx, f.ownerID, f.organization.ID, other.Invitation.ID)
	require.NoError(t, err, "another invitation has its own per-invitation budget")
}

func TestResendQuotasCoverTheDayAndTheOrganization(t *testing.T) {
	policy := NewInvitationPolicy(nil, ratelimit.NewMemoryStores())
	ctx := context.Background()
	for range invitationResendsPerDay {
		allowed, _, _ := policy.ResendDaily.Take(ctx, "7")
		require.True(t, allowed)
	}
	allowed, _, _ := policy.ResendDaily.Take(ctx, "7")
	assert.False(t, allowed)

	for range organizationResendsPerHour {
		allowed, _, _ = policy.ResendOrganization.Take(ctx, "42")
		require.True(t, allowed)
	}
	allowed, _, _ = policy.ResendOrganization.Take(ctx, "42")
	assert.False(t, allowed)
}

func TestResendOrganizationBudgetRefusesBeforeSending(t *testing.T) {
	f := newInvitationResendFixture(t)
	invited := f.invite(t, "org-budget@example.com")
	for range organizationResendsPerHour {
		allowed, _, _ := f.service.invitationPolicy.ResendOrganization.Take(
			context.Background(), strconv.FormatUint(uint64(f.organization.ID), 10))
		require.True(t, allowed)
	}

	_, err := f.service.ResendInvitation(context.Background(), f.ownerID, f.organization.ID, invited.Invitation.ID)
	require.ErrorIs(t, err, ErrInvitationResendRateLimited)
	assert.Len(t, f.mailer.tokens, 1)
	assert.Equal(t, crypto.SHA256Hex(f.mailer.tokens[0]), f.stored(t, invited.Invitation.ID).TokenHash)
}

func TestRecordingAnAttemptForASupersededTokenIsIgnored(t *testing.T) {
	f := newInvitationResendFixture(t)
	invited := f.invite(t, "stale@example.com")
	firstHash := crypto.SHA256Hex(f.mailer.tokens[0])
	_, err := f.service.ResendInvitation(context.Background(), f.ownerID, f.organization.ID, invited.Invitation.ID)
	require.NoError(t, err)

	// The first send reports late: its outcome must not replace the newer attempt's status.
	require.NoError(t, f.repo.RecordInvitationEmailAttempt(context.Background(), f.organization.ID,
		invited.Invitation.ID, firstHash, string(InvitationEmailSendStatusFailed), time.Now()))
	stored := f.stored(t, invited.Invitation.ID)
	require.NotNil(t, stored.EmailSendStatus)
	assert.Equal(t, string(InvitationEmailSendStatusAcceptedByProvider), *stored.EmailSendStatus)
}

func TestResendHandlerAnswersRateLimitWithRetryAfter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := newInvitationResendFixture(t)
	invited := f.invite(t, "handler@example.com")
	h := &Handler{service: f.service}
	h.RegisterErrorMappings(response.DefaultErrorMapper)

	call := func() *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		c.Set("userID", f.ownerID)
		c.Params = gin.Params{
			{Key: "id", Value: strconv.FormatUint(uint64(f.organization.ID), 10)},
			{Key: "invitation_id", Value: strconv.FormatUint(uint64(invited.Invitation.ID), 10)},
		}
		h.ResendInvitation(c)
		return recorder
	}

	first := call()
	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())
	assert.NotContains(t, first.Body.String(), "oinv_")

	second := call()
	require.Equal(t, http.StatusTooManyRequests, second.Code, second.Body.String())
	assert.Contains(t, second.Body.String(), `"error_code":"COMMON.RATE_LIMITED"`)
	retryAfter, err := strconv.Atoi(second.Header().Get("Retry-After"))
	require.NoError(t, err)
	assert.Positive(t, retryAfter)
}
