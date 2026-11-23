package apikey

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/router"
)

// fakeSendingDomains serves the sending domains of the organizations a user belongs to.
type fakeSendingDomains struct {
	// domains maps organization id to its sending domains; members maps it to its users.
	domains map[uint][]string
	members map[uint][]uint
	calls   int
	// developers maps a user to a developer grant limiting their keys.
	developers map[uint]developerScope
}

type developerScope struct {
	grant   domain.DeveloperGrant
	allowed []string
}

func (f *fakeSendingDomains) DeveloperSendingScope(_ context.Context, _, userID uint) (*domain.DeveloperGrant, []string, error) {
	scope, ok := f.developers[userID]
	if !ok {
		return nil, nil, nil
	}
	grant := scope.grant
	return &grant, append([]string(nil), scope.allowed...), nil
}

func (f *fakeSendingDomains) SendingDomains(_ context.Context, organizationID, userID uint) ([]string, error) {
	f.calls++
	for _, member := range f.members[organizationID] {
		if member == userID {
			return append([]string(nil), f.domains[organizationID]...), nil
		}
	}
	return nil, domain.ErrOrganizationNotFound
}

func newRestrictedService() (*service, *fakeRepository, *fakeSendingDomains) {
	repo := newFakeRepository()
	directory := &fakeSendingDomains{
		domains: map[uint][]string{5: {"zmail.example", "acme.example", "news.acme.example"}, 6: {"other.example"}},
		members: map[uint][]uint{5: {42}, 6: {99}},
	}
	return ProvideService(repo, directory), repo, directory
}

func uintPtr(value uint) *uint { return &value }

func TestCreateUnrestrictedKeyStoresNoRestriction(t *testing.T) {
	svc, repo, directory := newRestrictedService()

	created, err := svc.CreateForUser(context.Background(), 42, &APIKeyCreateRequest{
		Name: "deploy", Scopes: []string{"mail:send"},
	})

	require.NoError(t, err)
	assert.Nil(t, created.APIKey.OrganizationID)
	assert.Nil(t, created.APIKey.SendingDomains)
	assert.Nil(t, repo.keys[created.APIKey.ID].SendingDomains)
	assert.Zero(t, directory.calls, "an unrestricted key needs no organization lookup")
}

func TestCreateOrganizationBoundKey(t *testing.T) {
	svc, repo, _ := newRestrictedService()

	created, err := svc.CreateForUser(context.Background(), 42, &APIKeyCreateRequest{
		Name: "deploy", Scopes: []string{"mail:send"}, OrganizationID: uintPtr(5),
	})

	require.NoError(t, err)
	require.NotNil(t, created.APIKey.OrganizationID)
	assert.Equal(t, uint(5), *created.APIKey.OrganizationID)
	assert.Nil(t, created.APIKey.SendingDomains, "an organization-bound key may send from every domain")
	stored := repo.keys[created.APIKey.ID]
	require.NotNil(t, stored.OrganizationID)
	assert.Equal(t, uint(5), *stored.OrganizationID)
}

func TestCreateDomainRestrictedKeyNormalizesDomains(t *testing.T) {
	svc, repo, _ := newRestrictedService()

	created, err := svc.CreateForUser(context.Background(), 42, &APIKeyCreateRequest{
		Name: "deploy", Scopes: []string{"mail:send"}, OrganizationID: uintPtr(5),
		SendingDomains: []string{" News.Acme.Example ", "acme.example", "ACME.example."},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"acme.example", "news.acme.example"}, created.APIKey.SendingDomains)
	assert.Equal(t, []string{"acme.example", "news.acme.example"}, repo.keys[created.APIKey.ID].SendingDomains)
}

func TestCreateRestrictedKeyRejectsInvalidBindings(t *testing.T) {
	cases := map[string]struct {
		request *APIKeyCreateRequest
		want    error
	}{
		"organization of which the user is not a member": {
			&APIKeyCreateRequest{Name: "k", Scopes: []string{"mail:send"}, OrganizationID: uintPtr(6)},
			domain.ErrOrganizationNotFound,
		},
		"domains without an organization": {
			&APIKeyCreateRequest{Name: "k", Scopes: []string{"mail:send"}, SendingDomains: []string{"acme.example"}},
			domain.ErrInvalidInput,
		},
		"an empty domain list": {
			&APIKeyCreateRequest{Name: "k", Scopes: []string{"mail:send"}, OrganizationID: uintPtr(5), SendingDomains: []string{}},
			domain.ErrInvalidInput,
		},
		"a domain the organization cannot send from": {
			&APIKeyCreateRequest{Name: "k", Scopes: []string{"mail:send"}, OrganizationID: uintPtr(5), SendingDomains: []string{"other.example"}},
			domain.ErrInvalidInput,
		},
		"a malformed domain": {
			&APIKeyCreateRequest{Name: "k", Scopes: []string{"mail:send"}, OrganizationID: uintPtr(5), SendingDomains: []string{"acme example"}},
			domain.ErrInvalidInput,
		},
		"organization zero": {
			&APIKeyCreateRequest{Name: "k", Scopes: []string{"mail:send"}, OrganizationID: uintPtr(0)},
			domain.ErrInvalidInput,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			svc, repo, _ := newRestrictedService()
			_, err := svc.CreateForUser(context.Background(), 42, tc.request)
			require.ErrorIs(t, err, tc.want)
			assert.Empty(t, repo.keys, "a refused key must not be stored")
		})
	}
}

func TestCreateRestrictedKeyWithoutDirectoryIsUnavailable(t *testing.T) {
	_, err := NewService(newFakeRepository()).CreateForUser(context.Background(), 42, &APIKeyCreateRequest{
		Name: "k", Scopes: []string{"mail:send"}, OrganizationID: uintPtr(5),
	})
	require.ErrorIs(t, err, domain.ErrServiceUnavailable)
}

func TestAPIKeyMiddlewareCarriesRestrictionInRequestContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := map[string]struct {
		key  *domain.APIKey
		want domain.APIKeyRestriction
	}{
		"unrestricted": {
			&domain.APIKey{ID: 3, UserID: 9, Scopes: []string{"mail:send"}},
			domain.APIKeyRestriction{},
		},
		"bound": {
			&domain.APIKey{ID: 3, UserID: 9, Scopes: []string{"mail:send"}, OrganizationID: uintPtr(5), SendingDomains: []string{"acme.example"}},
			domain.APIKeyRestriction{OrganizationID: 5, SendingDomains: []string{"acme.example"}},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			engine := gin.New()
			routes := router.New(engine)
			NewHandler(&apiKeyStaticService{key: tc.key}).RegisterMiddleware(routes)
			var seen domain.APIKeyRestriction
			routes.WithMiddleware("api_key").GET("/protected", func(c *gin.Context) {
				seen = domain.APIKeyRestrictionFromContext(c.Request.Context())
				c.Status(http.StatusNoContent)
			})
			request := httptest.NewRequest(http.MethodGet, "/protected", nil)
			request.Header.Set("X-API-Key", "zmail_ab12cd34.secret")
			result := httptest.NewRecorder()

			engine.ServeHTTP(result, request)

			assert.Equal(t, http.StatusNoContent, result.Code)
			assert.Equal(t, tc.want, seen)
		})
	}
}

func TestAPIKeyResponseShowsRestriction(t *testing.T) {
	unrestricted := toResponse(&domain.APIKey{ID: 1, Scopes: []string{"mail:send"}})
	assert.Nil(t, unrestricted.OrganizationID)
	assert.Nil(t, unrestricted.SendingDomains)

	restricted := toResponse(&domain.APIKey{ID: 1, Scopes: []string{"mail:send"}, OrganizationID: uintPtr(5), SendingDomains: []string{"acme.example"}})
	require.NotNil(t, restricted.OrganizationID)
	assert.Equal(t, uint(5), *restricted.OrganizationID)
	assert.Equal(t, []string{"acme.example"}, restricted.SendingDomains)
}
