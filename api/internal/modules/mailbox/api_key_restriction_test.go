package mailbox

import (
	"context"
	"errors"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
)

func restrictedTo(domains ...string) context.Context {
	return domain.WithAPIKeyRestriction(context.Background(), domain.APIKeyRestriction{OrganizationID: accessOrg, SendingDomains: domains})
}

// A domain-restricted key sends only from its domains; any other from address is refused before
// anything is stored, sent, or counted.
func TestDomainRestrictedKeySendsOnlyFromItsDomains(t *testing.T) {
	f := newAccessFixture(t)

	_, _, err := f.service.SendFromAddress(restrictedTo("other.example"), f.b, f.team.Address, sendInput())
	if !errors.Is(err, domain.ErrAPIKeyDomainNotAllowed) {
		t.Fatalf("send from another domain error = %v; want ErrAPIKeyDomainNotAllowed", err)
	}
	if len(f.transport.sent) != 0 || f.outboundCount(t) != 0 {
		t.Fatal("a refused send stored a message or reached the provider")
	}

	if _, _, err := f.service.SendFromAddress(restrictedTo("other.example", "mail.example"), f.b, f.team.Address, sendInput()); err != nil {
		t.Fatalf("send from an allowed domain: %v", err)
	}
	// An organization-bound key without a domain list may send from every domain.
	bound := domain.WithAPIKeyRestriction(context.Background(), domain.APIKeyRestriction{OrganizationID: accessOrg})
	if _, _, err := f.service.SendFromAddress(bound, f.b, f.team.Address, sendInput()); err != nil {
		t.Fatalf("send with an organization-only binding: %v", err)
	}
}

// The restriction compares the domain of the template's from address on a transactional send.
func TestDomainRestrictedKeySendsTransactionalOnlyFromItsDomains(t *testing.T) {
	f := newTxFixture(t, nil)
	ctx := domain.WithAPIKeyRestriction(context.Background(), domain.APIKeyRestriction{OrganizationID: 10, SendingDomains: []string{"other.example"}})

	_, _, err := f.service.SendTransactional(ctx, liveActor(), codeInput("restricted-1", "lilei@example.com", "482913"))
	if !errors.Is(err, domain.ErrAPIKeyDomainNotAllowed) {
		t.Fatalf("transactional send from another domain error = %v; want ErrAPIKeyDomainNotAllowed", err)
	}
	if f.count(t) != 0 || len(f.transport.sent) != 0 {
		t.Fatal("a refused transactional send stored or sent a message")
	}

	allowed := domain.WithAPIKeyRestriction(context.Background(), domain.APIKeyRestriction{OrganizationID: 10, SendingDomains: []string{"mail.example"}})
	if _, _, err := f.service.SendTransactional(allowed, liveActor(), codeInput("restricted-2", "lilei@example.com", "482913")); err != nil {
		t.Fatalf("transactional send from an allowed domain: %v", err)
	}
}

// A member may restrict a key to the shared domain and the organization's verified domains, never
// to a pending domain or another organization's; a non-member may restrict to nothing.
func TestSendingDomainsListsWhatTheOrganizationMaySendFrom(t *testing.T) {
	f := newTxFixture(t, func(settings *Settings) { settings.CustomDomainsEnabled = true })
	for _, row := range []struct {
		organization uint
		name, status string
	}{
		{10, "acme.example", "verified"},
		{10, "pending.example", "pending"},
		{11, "other.example", "verified"},
	} {
		if err := f.db.Exec(`INSERT INTO mail_domains (organization_id, name, zone_id, zone_name, status, created_by, created_at, updated_at)
			VALUES (?, ?, 'zone-'||?, ?, ?, ?, now(), now())`, row.organization, row.name, row.name, row.name, row.status, txUser).Error; err != nil {
			t.Fatalf("seed domain %s: %v", row.name, err)
		}
	}

	domains, err := f.service.SendingDomains(context.Background(), 10, txUser)
	if err != nil {
		t.Fatalf("SendingDomains: %v", err)
	}
	want := []string{"mail.example", "acme.example"}
	if len(domains) != len(want) || domains[0] != want[0] || domains[1] != want[1] {
		t.Fatalf("SendingDomains = %v; want %v", domains, want)
	}

	f.service.memberships = &fakeMemberships{absent: map[uint]bool{txUser: true}}
	if _, err := f.service.SendingDomains(context.Background(), 10, txUser); !errors.Is(err, domain.ErrOrganizationNotFound) {
		t.Fatalf("non-member SendingDomains error = %v; want ErrOrganizationNotFound", err)
	}
}
