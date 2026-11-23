package apikey

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fastagi/zmail/api/internal/domain"
)

func newDeveloperKeyService(grant domain.DeveloperGrant, allowed []string) (*service, *fakeSendingDomains) {
	svc, _, directory := newRestrictedService()
	directory.developers = map[uint]developerScope{42: {grant: grant, allowed: allowed}}
	return svc, directory
}

func TestDeveloperKeyGetsTheGrantsDomainsWhenNoneAreNamed(t *testing.T) {
	svc, _ := newDeveloperKeyService(domain.DeveloperGrant{Domains: []string{"acme.example"}}, []string{"acme.example"})
	created, err := svc.CreateForUser(context.Background(), 42, &APIKeyCreateRequest{
		Name: "orders", Scopes: []string{ScopeMailTransactional}, OrganizationID: uintPtr(5),
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"acme.example"}, created.APIKey.SendingDomains)
}

func TestDeveloperKeyRefusesDomainsOutsideTheGrant(t *testing.T) {
	svc, _ := newDeveloperKeyService(domain.DeveloperGrant{Domains: []string{"acme.example"}}, []string{"acme.example"})
	_, err := svc.CreateForUser(context.Background(), 42, &APIKeyCreateRequest{
		Name: "orders", Scopes: []string{ScopeMailTransactional}, OrganizationID: uintPtr(5),
		SendingDomains: []string{"acme.example", "news.acme.example"},
	})
	require.ErrorIs(t, err, domain.ErrDeveloperDomainNotAllowed)

	_, err = svc.CreateForUser(context.Background(), 42, &APIKeyCreateRequest{
		Name: "shared", Scopes: []string{ScopeMailTransactional}, OrganizationID: uintPtr(5),
		SendingDomains: []string{"zmail.example"},
	})
	require.ErrorIs(t, err, domain.ErrDeveloperDomainNotAllowed, "the shared domain is never in a developer scope")
}

func TestAllDomainsDeveloperKeyStaysOpenToTheLiveScope(t *testing.T) {
	svc, _ := newDeveloperKeyService(domain.DeveloperGrant{AllDomains: true}, []string{"acme.example", "news.acme.example"})
	created, err := svc.CreateForUser(context.Background(), 42, &APIKeyCreateRequest{
		Name: "all", Scopes: []string{ScopeMailTransactional}, OrganizationID: uintPtr(5),
	})
	require.NoError(t, err)
	assert.Nil(t, created.APIKey.SendingDomains, "the send-time check follows the grant as domains are verified")
	created, err = svc.CreateForUser(context.Background(), 42, &APIKeyCreateRequest{
		Name: "news", Scopes: []string{ScopeMailTransactional}, OrganizationID: uintPtr(5),
		SendingDomains: []string{"news.acme.example"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"news.acme.example"}, created.APIKey.SendingDomains)
}
