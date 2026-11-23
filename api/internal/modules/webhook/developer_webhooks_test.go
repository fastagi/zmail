package webhook

import (
	"context"
	"fmt"
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

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/modules/organization"
	"github.com/fastagi/zmail/api/internal/modules/user"
	"github.com/fastagi/zmail/api/pkg/response"
)

// addWebhookDeveloper adds a member holding a developer grant for fast-ui.com.
func addWebhookDeveloper(t *testing.T, db *gorm.DB, organizationID uint) (uint, uint) {
	t.Helper()
	developer := user.UserPO{Username: "webhook-dev", Email: "dev@example.com", Password: "unused", Status: 1}
	require.NoError(t, db.Create(&developer).Error)
	membership := organization.OrganizationMembershipPO{
		OrganizationID: organizationID, UserID: developer.ID, Role: string(domain.OrganizationRoleMember),
	}
	require.NoError(t, db.Omit(clause.Associations).Create(&membership).Error)
	require.NoError(t, db.Create(&organization.MemberDeveloperGrantPO{
		MembershipID: membership.ID, OrganizationID: organizationID, ScopeDomains: `["fast-ui.com"]`,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}).Error)
	return developer.ID, membership.ID
}

func createSubscribedEndpoint(t *testing.T, repository *repository, organizationID, actorID uint, url string, now time.Time) *domain.WebhookEndpoint {
	t.Helper()
	return createEndpointIn(t, context.Background(), repository, organizationID, actorID, url, now)
}

// createDeveloperEndpoint creates an endpoint as a developer does: limited to their own.
func createDeveloperEndpoint(t *testing.T, repository *repository, organizationID, actorID uint, url string, now time.Time) *domain.WebhookEndpoint {
	t.Helper()
	return createEndpointIn(t, withEndpointOwner(context.Background(), actorID), repository, organizationID, actorID, url, now)
}

func createEndpointIn(t *testing.T, ctx context.Context, repository *repository, organizationID, actorID uint, url string, now time.Time) *domain.WebhookEndpoint {
	t.Helper()
	endpoint, err := repository.CreateEndpoint(ctx, endpointMutation{
		OrganizationID: organizationID, ActorID: actorID, Name: url, URL: url,
		URLHash: fmt.Sprintf("%064d", actorID), EventTypes: []string{"email.sent", "mail.message.sent"},
		SecretCiphertext: "encrypted-secret", SecretHint: "abcd1234", Now: now,
	})
	require.NoError(t, err)
	return endpoint
}

func broadcastMutation(organizationID uint, eventID, eventType string, origin *domain.WebhookEventOrigin, now time.Time) publicationMutation {
	return publicationMutation{
		OrganizationID: organizationID, MessageID: "msg_" + eventID, Source: "mailbox", EventID: eventID,
		Fingerprint: fmt.Sprintf("%064d", len(eventID)), EventType: eventType,
		PayloadJSON: `{"message_id":1}`, OccurredAt: now, Now: now, Origin: origin,
	}
}

func deliveredEndpoints(t *testing.T, result *publicationResult) []uint {
	t.Helper()
	ids := []uint{}
	for _, delivery := range result.Deliveries {
		ids = append(ids, delivery.EndpointID)
	}
	return ids
}

func TestDeveloperEndpointsReceiveOnlyProgrammaticMailInsideTheGrant(t *testing.T) {
	repository, db, organizationID, ownerID := newWebhookRepositoryTest(t)
	ctx := context.Background()
	now := time.Now().UTC()
	developerID, membershipID := addWebhookDeveloper(t, db, organizationID)
	managed := createSubscribedEndpoint(t, repository, organizationID, ownerID, "https://8.8.8.8/owner", now)
	personal := createDeveloperEndpoint(t, repository, organizationID, developerID, "https://8.8.4.4/dev", now)

	inside := &domain.WebhookEventOrigin{Programmatic: true, SenderDomain: "fast-ui.com", SenderDomainVerified: true}
	result, err := repository.Publish(ctx, broadcastMutation(organizationID, "t1:sent", "email.sent", inside, now))
	require.NoError(t, err)
	assert.ElementsMatch(t, []uint{managed.ID, personal.ID}, deliveredEndpoints(t, result))

	outside := &domain.WebhookEventOrigin{Programmatic: true, SenderDomain: "shu.ie", SenderDomainVerified: true}
	result, err = repository.Publish(ctx, broadcastMutation(organizationID, "t2:sent", "email.sent", outside, now))
	require.NoError(t, err)
	assert.Equal(t, []uint{managed.ID}, deliveredEndpoints(t, result))

	result, err = repository.Publish(ctx, broadcastMutation(organizationID, "m3:sent", "mail.message.sent", nil, now))
	require.NoError(t, err)
	assert.Equal(t, []uint{managed.ID}, deliveredEndpoints(t, result), "mailbox mail never reaches a developer endpoint")

	require.NoError(t, db.Where("membership_id = ?", membershipID).Delete(&organization.MemberDeveloperGrantPO{}).Error)
	result, err = repository.Publish(ctx, broadcastMutation(organizationID, "t4:sent", "email.sent", inside, now))
	require.NoError(t, err)
	assert.Equal(t, []uint{managed.ID}, deliveredEndpoints(t, result), "a withdrawn grant stops delivery")
}

// TestManagerEndpointsKeepReceivingAfterTheirCreatorLeaves: only endpoints a developer created
// follow their creator; an endpoint an owner or admin created stays the organization's.
func TestManagerEndpointsKeepReceivingAfterTheirCreatorLeaves(t *testing.T) {
	repository, db, organizationID, ownerID := newWebhookRepositoryTest(t)
	ctx := context.Background()
	now := time.Now().UTC()
	managed := createSubscribedEndpoint(t, repository, organizationID, ownerID, "https://8.8.8.8/owner", now)

	require.NoError(t, db.Where("organization_id = ? AND user_id = ?", organizationID, ownerID).
		Delete(&organization.OrganizationMembershipPO{}).Error)
	result, err := repository.Publish(ctx, broadcastMutation(organizationID, "m1:sent", "mail.message.sent", nil, now))
	require.NoError(t, err)
	assert.Equal(t, []uint{managed.ID}, deliveredEndpoints(t, result))
}

// TestDeveloperEndpointsStopWhenTheirCreatorLeaves: a departed developer's endpoint receives nothing.
func TestDeveloperEndpointsStopWhenTheirCreatorLeaves(t *testing.T) {
	repository, db, organizationID, _ := newWebhookRepositoryTest(t)
	ctx := context.Background()
	now := time.Now().UTC()
	developerID, membershipID := addWebhookDeveloper(t, db, organizationID)
	createDeveloperEndpoint(t, repository, organizationID, developerID, "https://8.8.4.4/dev", now)

	require.NoError(t, db.Delete(&organization.OrganizationMembershipPO{}, membershipID).Error)
	inside := &domain.WebhookEventOrigin{Programmatic: true, SenderDomain: "fast-ui.com", SenderDomainVerified: true}
	result, err := repository.Publish(ctx, broadcastMutation(organizationID, "t1:sent", "email.sent", inside, now))
	require.NoError(t, err)
	assert.Empty(t, deliveredEndpoints(t, result))
}

func TestDeveloperManagesOnlyTheirOwnEndpoints(t *testing.T) {
	repository, db, organizationID, ownerID := newWebhookRepositoryTest(t)
	now := time.Now().UTC()
	developerID, _ := addWebhookDeveloper(t, db, organizationID)
	managed := createSubscribedEndpoint(t, repository, organizationID, ownerID, "https://8.8.8.8/owner", now)
	personal := createDeveloperEndpoint(t, repository, organizationID, developerID, "https://8.8.4.4/dev", now)
	_, err := repository.Publish(context.Background(), broadcastMutation(organizationID, "t1:sent", "email.sent",
		&domain.WebhookEventOrigin{Programmatic: true, SenderDomain: "fast-ui.com"}, now))
	require.NoError(t, err)

	developerCtx := withEndpointOwner(context.Background(), developerID)
	endpoints, total, err := repository.ListEndpoints(developerCtx, organizationID, 1, 10)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	assert.Equal(t, personal.ID, endpoints[0].ID)
	assert.Equal(t, developerID, endpoints[0].CreatedBy)

	_, err = repository.ReplaceEndpointStatus(developerCtx, organizationID, managed.ID, developerID, false, managed.Version, now)
	require.ErrorIs(t, err, domain.ErrWebhookEndpointNotFound)
	err = repository.DeleteEndpoint(developerCtx, organizationID, managed.ID, developerID, managed.Version, now)
	require.ErrorIs(t, err, domain.ErrWebhookEndpointNotFound)
	_, err = repository.ReplaceEndpointStatus(developerCtx, organizationID, personal.ID, developerID, false, personal.Version, now)
	require.NoError(t, err)

	deliveries, total, err := repository.ListDeliveries(developerCtx, organizationID, deliveryFilter{}, 1, 10)
	require.NoError(t, err)
	require.EqualValues(t, 1, total, "a developer sees only the deliveries of their endpoints")
	assert.Equal(t, personal.ID, deliveries[0].EndpointID)

	everyone, _, err := repository.ListDeliveries(context.Background(), organizationID, deliveryFilter{}, 1, 10)
	require.NoError(t, err)
	for _, delivery := range everyone {
		if delivery.EndpointID == managed.ID {
			_, _, err = repository.ListAttempts(developerCtx, organizationID, delivery.ID, 1, 10)
			require.ErrorIs(t, err, domain.ErrWebhookDeliveryNotFound)
		}
	}

	all, total, err := repository.ListEndpoints(context.Background(), organizationID, 1, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total, "owners and admins see every endpoint")
	_ = all
}

func TestHandlerLetsDevelopersManageTheirOwnEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	registerWebhookHandlerTestMappings()
	service, organizationID, ownerID, _ := newWebhookServiceTest(t)
	handler := NewHandler(service)
	handler.RegisterErrorMappings(response.DefaultErrorMapper)
	managed, err := service.CreateEndpoint(t.Context(), organizationID, ownerID, endpointInput{
		Name: "Owner", URL: "http://127.0.0.1:8080/owner", EventTypes: []string{"webhook.test"},
	})
	require.NoError(t, err)

	const developerID = uint(777)
	developer := func(method, path, body string) (*httptest.ResponseRecorder, *gin.Context) {
		recorder, c := newWebhookHandlerContext(method, path, body, organizationID, developerID, domain.OrganizationRoleMember)
		viewer, _ := domain.OrganizationContextFromContext(c.Request.Context())
		viewer.Features = domain.MemberFeatures{Developer: true}
		viewer.DeveloperGrant = &domain.DeveloperGrant{Domains: []string{"fast-ui.com"}}
		c.Request = c.Request.WithContext(domain.WithOrganizationContext(c.Request.Context(), viewer))
		return recorder, c
	}

	recorder, c := developer(http.MethodPost, "/v1/webhook-endpoints",
		`{"name":"Mine","url":"http://127.0.0.1:8080/mine","event_types":["webhook.test"]}`)
	handler.CreateEndpoint(c)
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	assert.Contains(t, recorder.Body.String(), `"created_by":777`)

	recorder, c = developer(http.MethodGet, "/v1/webhook-endpoints", "")
	handler.ListEndpoints(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "/mine")
	assert.NotContains(t, recorder.Body.String(), "/owner", "a developer lists only their endpoints")

	id := strconv.FormatUint(uint64(managed.Endpoint.ID), 10)
	recorder, c = developer(http.MethodDelete, "/v1/webhook-endpoints/"+id, "")
	c.Params = gin.Params{{Key: "id", Value: id}}
	c.Request.Header.Set("If-Match", webhookEndpointETag(managed.Endpoint.Version))
	handler.DeleteEndpoint(c)
	assert.Equal(t, http.StatusNotFound, recorder.Code, "an admin's endpoint is invisible to a developer")
}
