package organization

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/capabilities/crypto"
	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/ratelimit"
	auditstarter "github.com/fastagi/zmail/api/internal/modules/audit"
	"github.com/fastagi/zmail/api/pkg/response"
)

const invitationLinkTestOrigin = "https://app.example.com"

// tokenFromInvitationURL returns the one-time token an invitation link carries, failing unless the
// link has the shape invitation emails use: <console origin>/invite#<token>.
func tokenFromInvitationURL(t *testing.T, link string) string {
	t.Helper()
	require.True(t, strings.HasPrefix(link, invitationLinkTestOrigin+"/invite#"), link)
	token, err := url.QueryUnescape(strings.TrimPrefix(link, invitationLinkTestOrigin+"/invite#"))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(token, "oinv_"), token)
	return token
}

// captureInvitationLogs routes the default logger into a buffer for the rest of the test.
func captureInvitationLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buffer bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buffer, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buffer
}

func linkPolicy() InvitationPolicy {
	policy := NewInvitationPolicy(nil, ratelimit.NewMemoryStores())
	policy.PublicURL = invitationLinkTestOrigin
	return policy
}

func inviteStubRepository(role domain.OrganizationRole, persistedHash *string) *invitationRepositoryStub {
	return &invitationRepositoryStub{
		findFn: func(context.Context, uint, uint) (*domain.OrganizationMembership, error) {
			return invitationManager(role), nil
		},
		createInvitationFn: func(_ context.Context, invitation *domain.OrganizationInvitation, _ time.Time) error {
			if persistedHash != nil {
				*persistedHash = invitation.TokenHash
			}
			invitation.ID = 73
			return nil
		},
	}
}

func TestInviteReturnsTheInvitationLinkOnceToTheManager(t *testing.T) {
	for _, mailerConfigured := range []bool{true, false} {
		t.Run(fmt.Sprintf("mailer configured %v", mailerConfigured), func(t *testing.T) {
			logs := captureInvitationLogs(t)
			var persistedHash, emailedToken string
			repo := inviteStubRepository(domain.OrganizationRoleAdmin, &persistedHash)
			mailer := &invitationMailerStub{
				configured: mailerConfigured,
				sendFn: func(_ context.Context, _, _ string, _ domain.OrganizationRole, token string, _ time.Time) error {
					emailedToken = token
					return fmt.Errorf("provider rejected the message")
				},
			}
			var audited []auditstarter.Change
			svc := NewService(repo, repo, mailer, linkPolicy(), nil)
			svc.recordAudit = func(_ context.Context, change auditstarter.Change) { audited = append(audited, change) }

			result, err := svc.Invite(context.Background(), 17, 42, &CreateOrganizationInvitationRequest{
				Email: "member@example.com", Role: domain.OrganizationRoleMember,
			})
			require.NoError(t, err)

			token := tokenFromInvitationURL(t, result.InvitationURL)
			assert.Equal(t, persistedHash, crypto.SHA256Hex(token), "the link carries the stored invitation's token")
			if mailerConfigured {
				assert.Equal(t, emailedToken, token, "the link and the email carry the same token")
			}
			require.Len(t, audited, 1)
			assert.Equal(t, true, audited[0].Metadata["link_issued"])
			assert.NotContains(t, fmt.Sprint(audited[0]), token)
			assert.NotContains(t, logs.String(), token)
			assert.NotContains(t, logs.String(), "/invite#")
		})
	}
}

func TestInviteWithoutAConsoleOriginIssuesNoLink(t *testing.T) {
	repo := inviteStubRepository(domain.OrganizationRoleOwner, nil)
	var audited []auditstarter.Change
	svc := NewService(repo, repo, &invitationMailerStub{}, NewInvitationPolicy(nil, nil), nil)
	svc.recordAudit = func(_ context.Context, change auditstarter.Change) { audited = append(audited, change) }

	result, err := svc.Invite(context.Background(), 17, 42, &CreateOrganizationInvitationRequest{
		Email: "member@example.com", Role: domain.OrganizationRoleMember,
	})
	require.NoError(t, err)
	assert.Empty(t, result.InvitationURL)
	require.Len(t, audited, 1)
	assert.Equal(t, false, audited[0].Metadata["link_issued"])
}

func TestMembersNeverReceiveAnInvitationLink(t *testing.T) {
	repo := inviteStubRepository(domain.OrganizationRoleMember, nil)
	repo.reissueFn = func(context.Context, uint, uint, string, time.Time, time.Time) (*domain.OrganizationInvitation, error) {
		t.Fatal("a member must not reissue a token")
		return nil, nil
	}
	svc := NewService(repo, repo, &invitationMailerStub{configured: true}, linkPolicy(), nil)

	result, err := svc.Invite(context.Background(), 17, 42, &CreateOrganizationInvitationRequest{
		Email: "member@example.com", Role: domain.OrganizationRoleMember,
	})
	require.ErrorIs(t, err, domain.ErrPermissionDenied)
	assert.Nil(t, result)
	result, err = svc.ResendInvitation(context.Background(), 17, 42, 73)
	require.ErrorIs(t, err, domain.ErrPermissionDenied)
	assert.Nil(t, result)
}

func TestResendReturnsTheNewLinkOnlyWhenItMintsAToken(t *testing.T) {
	logs := captureInvitationLogs(t)
	var reissuedHash, emailedToken string
	repo := &invitationRepositoryStub{
		findFn: func(context.Context, uint, uint) (*domain.OrganizationMembership, error) {
			return invitationManager(domain.OrganizationRoleOwner), nil
		},
		reissueFn: func(_ context.Context, organizationID, invitationID uint, tokenHash string, expiresAt, _ time.Time) (*domain.OrganizationInvitation, error) {
			reissuedHash = tokenHash
			return &domain.OrganizationInvitation{
				ID: invitationID, OrganizationID: organizationID, Email: "member@example.com",
				Role: domain.OrganizationRoleMember, TokenHash: tokenHash, ExpiresAt: expiresAt,
			}, nil
		},
	}
	mailer := &invitationMailerStub{
		configured: true,
		sendFn: func(_ context.Context, _, _ string, _ domain.OrganizationRole, token string, _ time.Time) error {
			emailedToken = token
			return nil
		},
	}
	var audited []auditstarter.Change
	svc := NewService(repo, repo, mailer, linkPolicy(), nil)
	svc.recordAudit = func(_ context.Context, change auditstarter.Change) { audited = append(audited, change) }

	result, err := svc.ResendInvitation(context.Background(), 17, 42, 73)
	require.NoError(t, err)
	token := tokenFromInvitationURL(t, result.InvitationURL)
	assert.Equal(t, reissuedHash, crypto.SHA256Hex(token))
	assert.Equal(t, emailedToken, token)
	require.Len(t, audited, 1)
	assert.Equal(t, true, audited[0].Metadata["link_issued"])
	assert.NotContains(t, fmt.Sprint(audited[0]), token)
	assert.NotContains(t, logs.String(), token)

	// Without a mailer a resend keeps the existing token, so there is no new link to hand out.
	mailer.configured = false
	audited = nil
	result, err = svc.ResendInvitation(context.Background(), 17, 42, 74)
	require.NoError(t, err)
	assert.Empty(t, result.InvitationURL)
	require.Len(t, audited, 1)
	assert.Equal(t, false, audited[0].Metadata["link_issued"])
}

func TestInvitationLinkHandlersAreNoStoreAndListNeverCarriesALink(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := newInvitationResendFixture(t)
	f.service.invitationPolicy.PublicURL = invitationLinkTestOrigin
	h := &Handler{service: f.service}
	h.RegisterErrorMappings(response.DefaultErrorMapper)
	registerSharedInvitationErrorMappings()
	organizationID := strconv.FormatUint(uint64(f.organization.ID), 10)

	call := func(userID uint, method, body string, params gin.Params, handle func(*gin.Context)) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(method, "/v1/organizations/"+organizationID+"/invitations", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set("userID", userID)
		c.Params = params
		handle(c)
		return recorder
	}
	orgParam := gin.Params{{Key: "id", Value: organizationID}}

	created := call(f.adminID, http.MethodPost, `{"email":"linked@example.com","role":"member"}`, orgParam, h.Invite)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	assert.Equal(t, "private, no-store", created.Header().Get("Cache-Control"))
	var invited struct {
		Data struct {
			Invitation    OrganizationInvitationResponse `json:"invitation"`
			InvitationURL string                         `json:"invitation_url"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &invited))
	firstToken := tokenFromInvitationURL(t, invited.Data.InvitationURL)

	listed := call(f.adminID, http.MethodGet, "", orgParam, h.ListInvitations)
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	assert.Contains(t, listed.Body.String(), "linked@example.com")
	assert.NotContains(t, listed.Body.String(), "invitation_url")
	assert.NotContains(t, listed.Body.String(), "oinv_")
	assert.NotContains(t, listed.Body.String(), firstToken)

	invitationParams := gin.Params{
		{Key: "id", Value: organizationID},
		{Key: "invitation_id", Value: strconv.FormatUint(uint64(invited.Data.Invitation.ID), 10)},
	}
	denied := call(f.memberID, http.MethodPost, "", invitationParams, h.ResendInvitation)
	require.Equal(t, http.StatusForbidden, denied.Code, denied.Body.String())
	assert.Contains(t, denied.Body.String(), `"error_code":"`+domain.CodePermissionDenied+`"`)
	assert.NotContains(t, denied.Body.String(), "invite#")

	resent := call(f.ownerID, http.MethodPost, "", invitationParams, h.ResendInvitation)
	require.Equal(t, http.StatusCreated, resent.Code, resent.Body.String())
	assert.Equal(t, "private, no-store", resent.Header().Get("Cache-Control"))
	require.NoError(t, json.Unmarshal(resent.Body.Bytes(), &invited))
	secondToken := tokenFromInvitationURL(t, invited.Data.InvitationURL)
	assert.NotEqual(t, firstToken, secondToken)

	inviteeID := createOrganizationTestUser(t, f.db, "linked")
	_, err := f.service.AcceptInvitation(context.Background(), inviteeID,
		&AcceptOrganizationInvitationRequest{Token: firstToken})
	require.ErrorIs(t, err, domain.ErrOrganizationInvitationInvalid, "a resend invalidates the earlier link")
	_, err = f.service.AcceptInvitation(context.Background(), inviteeID,
		&AcceptOrganizationInvitationRequest{Token: secondToken})
	require.NoError(t, err)

	// An accepted invitation never yields a link again.
	accepted := call(f.ownerID, http.MethodPost, "", invitationParams, h.ResendInvitation)
	require.Equal(t, http.StatusNotFound, accepted.Code, accepted.Body.String())
	assert.NotContains(t, accepted.Body.String(), "invite#")
}

func TestRevokedAndExpiredInvitationsNeverYieldALink(t *testing.T) {
	f := newInvitationResendFixture(t)
	f.service.invitationPolicy.PublicURL = invitationLinkTestOrigin
	ctx := context.Background()

	revoked := f.invite(t, "revoked-link@example.com")
	require.NoError(t, f.service.RevokeInvitation(ctx, f.ownerID, f.organization.ID, revoked.Invitation.ID))
	result, err := f.service.ResendInvitation(ctx, f.ownerID, f.organization.ID, revoked.Invitation.ID)
	require.ErrorIs(t, err, domain.ErrOrganizationInvitationNotFound)
	assert.Nil(t, result)

	expired := f.invite(t, "expired-link@example.com")
	require.NoError(t, f.db.Model(&OrganizationInvitationPO{}).Where("id = ?", expired.Invitation.ID).
		Update("expires_at", time.Now().Add(-time.Hour)).Error)
	result, err = f.service.ResendInvitation(ctx, f.ownerID, f.organization.ID, expired.Invitation.ID)
	require.ErrorIs(t, err, domain.ErrOrganizationInvitationNotFound)
	assert.Nil(t, result)
}

// registerSharedInvitationErrorMappings installs the core mappings bootstrap registers for every
// starter, so handler tests see the real status of a shared domain error.
func registerSharedInvitationErrorMappings() {
	response.DefaultErrorMapper.Register(domain.ErrPermissionDenied, http.StatusForbidden, domain.CodePermissionDenied)
}

func TestOutsidersAndOtherOrganizationsAdminsGetNoInvitationLink(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := newInvitationResendFixture(t)
	f.service.invitationPolicy.PublicURL = invitationLinkTestOrigin
	h := &Handler{service: f.service}
	h.RegisterErrorMappings(response.DefaultErrorMapper)
	registerSharedInvitationErrorMappings()
	invited := f.invite(t, "kept@example.com")
	mintedBefore := len(f.mailer.tokens)
	storedBefore := f.stored(t, invited.Invitation.ID)

	outsiderID := createOrganizationTestUser(t, f.db, "link-outsider")
	otherOwnerID := createOrganizationTestUser(t, f.db, "link-other-owner")
	otherAdminID := createOrganizationTestUser(t, f.db, "link-other-admin")
	other := createInvitationTestOrganization2(t, f.db, otherOwnerID)
	require.NoError(t, f.db.Omit(clause.Associations).Create(&OrganizationMembershipPO{
		OrganizationID: other.ID, UserID: otherAdminID, Role: string(domain.OrganizationRoleAdmin),
	}).Error)

	organizationID := strconv.FormatUint(uint64(f.organization.ID), 10)
	invitationID := strconv.FormatUint(uint64(invited.Invitation.ID), 10)
	call := func(userID uint, body string, params gin.Params, handle func(*gin.Context)) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set("userID", userID)
		c.Params = params
		handle(c)
		return recorder
	}

	for name, userID := range map[string]uint{"outsider": outsiderID, "other organization admin": otherAdminID} {
		t.Run(name, func(t *testing.T) {
			created := call(userID, `{"email":"sneaky@example.com","role":"member"}`,
				gin.Params{{Key: "id", Value: organizationID}}, h.Invite)
			require.Equal(t, http.StatusNotFound, created.Code, created.Body.String())
			assert.Contains(t, created.Body.String(), `"error_code":"`+domain.CodeOrganizationNotFound+`"`)
			assert.NotContains(t, created.Body.String(), "invite#")

			resent := call(userID, "", gin.Params{
				{Key: "id", Value: organizationID}, {Key: "invitation_id", Value: invitationID},
			}, h.ResendInvitation)
			require.Equal(t, http.StatusNotFound, resent.Code, resent.Body.String())
			assert.Contains(t, resent.Body.String(), `"error_code":"`+domain.CodeOrganizationNotFound+`"`)
			assert.NotContains(t, resent.Body.String(), "invite#")
		})
	}

	assert.Len(t, f.mailer.tokens, mintedBefore, "no token was minted for a refused caller")
	assert.Equal(t, storedBefore.TokenHash, f.stored(t, invited.Invitation.ID).TokenHash)
	var sneaky int64
	require.NoError(t, f.db.Model(&OrganizationInvitationPO{}).Where("email = ?", "sneaky@example.com").Count(&sneaky).Error)
	assert.Zero(t, sneaky)
}
