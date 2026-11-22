package organization

import (
	"context"
	"errors"
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
	fail       bool
	tokens     []string
}

func (m *recordingInvitationMailer) IsConfigured() bool { return m.configured }

func (m *recordingInvitationMailer) SendInvitation(
	_ context.Context, _, _ string, _ domain.OrganizationRole, token string, _ time.Time,
) error {
	m.tokens = append(m.tokens, token)
	if m.fail {
		return errors.New("provider rejected the message")
	}
	return nil
}

type invitationDeliveryFixture struct {
	db           *gorm.DB
	repo         *repository
	service      *service
	mailer       *recordingInvitationMailer
	organization *domain.Organization
	ownerID      uint
	adminID      uint
	memberID     uint
	now          time.Time
}

func newInvitationDeliveryFixture(t *testing.T) *invitationDeliveryFixture {
	t.Helper()
	db := newOrganizationRepositoryTestDB(t)
	repo := NewRepository(db)
	ownerID := createOrganizationTestUser(t, db, "delivery-owner")
	adminID := createOrganizationTestUser(t, db, "delivery-admin")
	memberID := createOrganizationTestUser(t, db, "delivery-member")
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
	now := time.Now().UTC().Truncate(time.Microsecond)
	svc.now = func() time.Time { return now }
	return &invitationDeliveryFixture{
		db: db, repo: repo, service: svc, mailer: mailer, organization: organization,
		ownerID: ownerID, adminID: adminID, memberID: memberID, now: now,
	}
}

func (f *invitationDeliveryFixture) invite(t *testing.T, email string) *OrganizationInvitationResult {
	t.Helper()
	result, err := f.service.Invite(context.Background(), f.ownerID, f.organization.ID,
		&CreateOrganizationInvitationRequest{Email: email, Role: domain.OrganizationRoleMember})
	require.NoError(t, err)
	return result
}

func (f *invitationDeliveryFixture) stored(t *testing.T, invitationID uint) *domain.OrganizationInvitation {
	t.Helper()
	var po OrganizationInvitationPO
	require.NoError(t, f.db.First(&po, invitationID).Error)
	return po.toDomain()
}

func TestInviteRecordsTheSendOutcomeOnTheInvitation(t *testing.T) {
	cases := []struct {
		name       string
		configured bool
		fail       bool
		want       InvitationEmailSendStatus
	}{
		{name: "accepted", configured: true, want: InvitationEmailSendStatusAcceptedByProvider},
		{name: "failed", configured: true, fail: true, want: InvitationEmailSendStatusFailed},
		{name: "not configured", want: InvitationEmailSendStatusNotConfigured},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newInvitationDeliveryFixture(t)
			f.mailer.configured, f.mailer.fail = tc.configured, tc.fail
			result := f.invite(t, "person@example.com")
			assert.Equal(t, tc.want, result.EmailSendStatus)

			stored := f.stored(t, result.Invitation.ID)
			assert.Equal(t, string(tc.want), stored.EmailSendStatus)
			require.NotNil(t, stored.EmailAttemptedAt)
			assert.True(t, f.now.Equal(*stored.EmailAttemptedAt))

			invitations, _, err := f.service.ListInvitations(context.Background(), f.ownerID, f.organization.ID, 1, 20)
			require.NoError(t, err)
			require.Len(t, invitations, 1)
			view := toInvitationResponse(invitations[0], f.now)
			require.NotNil(t, view.EmailSendStatus)
			assert.Equal(t, tc.want, *view.EmailSendStatus)
			require.NotNil(t, view.EmailAttemptedAt)
		})
	}
}

func TestRepositoryCreatesInvitationsAsPendingAndIgnoresStaleOutcomes(t *testing.T) {
	f := newInvitationDeliveryFixture(t)
	ctx := context.Background()
	invitation := newPendingInvitation(f.organization.ID, f.ownerID, "stale@example.com", "oinv_first.secret", f.now)
	require.NoError(t, f.repo.CreateInvitation(ctx, invitation, f.now))
	assert.Equal(t, string(InvitationEmailSendStatusPending), f.stored(t, invitation.ID).EmailSendStatus)
	assert.Nil(t, f.stored(t, invitation.ID).EmailAttemptedAt)

	rotated, err := f.repo.PrepareInvitationResend(ctx, f.organization.ID, invitation.ID,
		crypto.SHA256Hex("oinv_second.secret"), f.now)
	require.NoError(t, err)
	assert.Equal(t, crypto.SHA256Hex("oinv_second.secret"), rotated.TokenHash)

	// The first send finishes late: its outcome belongs to a token that is no longer current.
	recorded, err := f.repo.RecordInvitationEmailAttempt(ctx, invitation.ID,
		crypto.SHA256Hex("oinv_first.secret"), string(InvitationEmailSendStatusFailed), f.now)
	require.NoError(t, err)
	assert.False(t, recorded)
	assert.Equal(t, string(InvitationEmailSendStatusPending), f.stored(t, invitation.ID).EmailSendStatus)

	recorded, err = f.repo.RecordInvitationEmailAttempt(ctx, invitation.ID,
		crypto.SHA256Hex("oinv_second.secret"), string(InvitationEmailSendStatusAcceptedByProvider), f.now)
	require.NoError(t, err)
	assert.True(t, recorded)
	assert.Equal(t, string(InvitationEmailSendStatusAcceptedByProvider), f.stored(t, invitation.ID).EmailSendStatus)
}

func TestResendRotatesTheTokenAndOnlyTheNewTokenAccepts(t *testing.T) {
	f := newInvitationDeliveryFixture(t)
	inviteeID := createOrganizationTestUser(t, f.db, "rotating")
	invited := f.invite(t, "rotating@example.com")
	require.Len(t, f.mailer.tokens, 1)
	firstToken := f.mailer.tokens[0]

	result, err := f.service.ResendInvitation(context.Background(), f.adminID, f.organization.ID, invited.Invitation.ID)
	require.NoError(t, err)
	assert.Equal(t, InvitationEmailSendStatusAcceptedByProvider, result.EmailSendStatus)
	assert.Equal(t, invited.Invitation.ExpiresAt.UTC(), result.Invitation.ExpiresAt.UTC(), "resend keeps the expiry")
	require.Len(t, f.mailer.tokens, 2)
	secondToken := f.mailer.tokens[1]
	assert.NotEqual(t, firstToken, secondToken)

	_, err = f.service.AcceptInvitation(context.Background(), inviteeID, &AcceptOrganizationInvitationRequest{Token: firstToken})
	require.ErrorIs(t, err, domain.ErrOrganizationInvitationInvalid)
	membership, err := f.service.AcceptInvitation(context.Background(), inviteeID, &AcceptOrganizationInvitationRequest{Token: secondToken})
	require.NoError(t, err)
	assert.Equal(t, f.organization.ID, membership.OrganizationID)
}

func TestResendWithoutAMailerKeepsTheTokenAndRecordsNotConfigured(t *testing.T) {
	f := newInvitationDeliveryFixture(t)
	invited := f.invite(t, "unsent@example.com")
	before := f.stored(t, invited.Invitation.ID).TokenHash
	f.mailer.configured = false

	result, err := f.service.ResendInvitation(context.Background(), f.ownerID, f.organization.ID, invited.Invitation.ID)
	require.NoError(t, err)
	assert.Equal(t, InvitationEmailSendStatusNotConfigured, result.EmailSendStatus)
	stored := f.stored(t, invited.Invitation.ID)
	assert.Equal(t, before, stored.TokenHash)
	assert.Equal(t, string(InvitationEmailSendStatusNotConfigured), stored.EmailSendStatus)
}

func TestResendIsForManagersAndPendingInvitationsOnly(t *testing.T) {
	f := newInvitationDeliveryFixture(t)
	invited := f.invite(t, "guarded@example.com")
	ctx := context.Background()

	_, err := f.service.ResendInvitation(ctx, f.memberID, f.organization.ID, invited.Invitation.ID)
	require.ErrorIs(t, err, domain.ErrPermissionDenied)
	outsiderID := createOrganizationTestUser(t, f.db, "delivery-outsider")
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
	f := newInvitationDeliveryFixture(t)
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

	// Another invitation has its own budget.
	_, err = f.service.ResendInvitation(ctx, f.ownerID, f.organization.ID, other.Invitation.ID)
	require.NoError(t, err)
}

func TestResendDailyBudget(t *testing.T) {
	stores := ratelimit.NewMemoryStores()
	policy := NewInvitationPolicy(nil, stores)
	key := "7"
	for range invitationResendsPerDay {
		allowed, _, _ := policy.ResendDaily.Take(context.Background(), key)
		require.True(t, allowed)
	}
	allowed, _, _ := policy.ResendDaily.Take(context.Background(), key)
	assert.False(t, allowed)
}

func TestResendHandlerAnswersRateLimitWithRetryAfter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := newInvitationDeliveryFixture(t)
	invited := f.invite(t, "handler@example.com")
	handler := &Handler{service: f.service}
	handler.RegisterErrorMappings(response.DefaultErrorMapper)

	call := func() *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		c.Set("userID", f.ownerID)
		c.Params = gin.Params{
			{Key: "id", Value: strconv.FormatUint(uint64(f.organization.ID), 10)},
			{Key: "invitation_id", Value: strconv.FormatUint(uint64(invited.Invitation.ID), 10)},
		}
		handler.ResendInvitation(c)
		return recorder
	}

	first := call()
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	assert.Contains(t, first.Body.String(), `"email_send_status":"accepted_by_provider"`)
	assert.NotContains(t, first.Body.String(), "oinv_")

	second := call()
	require.Equal(t, http.StatusTooManyRequests, second.Code, second.Body.String())
	assert.Contains(t, second.Body.String(), `"error_code":"COMMON.RATE_LIMITED"`)
	assert.NotEmpty(t, second.Header().Get("Retry-After"))
}
