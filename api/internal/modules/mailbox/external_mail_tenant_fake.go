package mailbox

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/fastagi/zmail/api/internal/domain"
)

var _ ExternalMailTenantSource = (*FakeExternalMailSource)(nil)

// fakeTenantCredential is the credential a fake tenant stores: its identifier behind a prefix.
const fakeTenantCredential = "fake-tenant:"

// Development tenants (api/docs/EXTERNAL_MAIL_DEVELOPMENT.md). A running stack has no way to call
// AddTenant, and its API and sync worker each hold their own in-memory fake, so the fake also accepts
// a synthetic credential and derives everything about such a tenant from its identifier alone: every
// process agrees on its directory without sharing state. The fake source exists only while `fake`
// is the active external mail source, which configuration refuses in production; no real connector
// knows any of this.
const (
	// FakeDevTenantSecret is the synthetic credential the fake accepts for a development tenant, as
	// the Google service account key or the Microsoft client secret. Nothing else accepts it.
	FakeDevTenantSecret = "fake-tenant-dev-only"
	// fakeDevTenantCredential is the stored credential of a development tenant: its identifier
	// behind a prefix of its own.
	fakeDevTenantCredential = "fake-dev-tenant:"
	// fakeDevTenantSuffix ends every development tenant identifier: a reserved, never resolvable
	// domain (RFC 2606).
	fakeDevTenantSuffix = ".invalid"
	// FakeDevTenantDirectorySize is the number of ordinary mailboxes of a development tenant's
	// directory, enough for several pages.
	FakeDevTenantDirectorySize = 30
	// FakeDevTenantRefusedLocal is the local part of the one directory mailbox whose token the fake
	// refuses while the tenant works, to exercise a single assignment losing access.
	FakeDevTenantRefusedLocal = "refused"
)

// fakeDevTenantIdentifier reports whether identifier names a development tenant: a lowercase domain
// under .invalid of at most 63 characters, such as acme.invalid.
func fakeDevTenantIdentifier(identifier string) bool {
	name, found := strings.CutSuffix(identifier, fakeDevTenantSuffix)
	if !found || name == "" || len(identifier) > 63 || strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") || strings.Contains(name, "..") {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '.' {
			return false
		}
	}
	return true
}

// fakeDevTenant is the development tenant named identifier: user01@ to user30@ its domain, in order,
// then refused@, whose mailbox token is always refused.
func fakeDevTenant(identifier string) *fakeTenant {
	directory := make([]ExternalMailDirectoryEntry, 0, FakeDevTenantDirectorySize+1)
	for i := 1; i <= FakeDevTenantDirectorySize; i++ {
		local := fmt.Sprintf("user%02d", i)
		directory = append(directory, ExternalMailDirectoryEntry{
			Address: local + "@" + identifier, DisplayName: fmt.Sprintf("Fake User %02d", i), DirectoryID: "fake-" + local + "-" + identifier,
		})
	}
	refused := FakeDevTenantRefusedLocal + "@" + identifier
	directory = append(directory, ExternalMailDirectoryEntry{
		Address: refused, DisplayName: "Refused Mailbox", DirectoryID: "fake-" + FakeDevTenantRefusedLocal + "-" + identifier,
	})
	return &fakeTenant{directory: directory, refusedMailbox: refused}
}

// AddTenant makes the source accept the tenant identifier with the directory, in order.
func (f *FakeExternalMailSource) AddTenant(identifier string, directory ...ExternalMailDirectoryEntry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tenants[strings.ToLower(identifier)] = &fakeTenant{directory: directory}
}

// RefuseTenant makes every later use of the tenant's credential answer ErrExternalMailAuth, as a
// deleted service account key or a removed admin consent does; false restores it.
func (f *FakeExternalMailSource) RefuseTenant(identifier string, refused bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if tenant, known := f.tenants[strings.ToLower(identifier)]; known {
		tenant.refused = refused
	}
}

// RefuseTenantMailbox makes the token of one tenant mailbox be refused while the tenant works.
func (f *FakeExternalMailSource) RefuseTenantMailbox(address string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refusedMailboxes[strings.ToLower(address)] = true
}

// TenantMailboxToken is the access token the fake mints for a tenant mailbox; Deliver mail to it.
func TenantMailboxToken(address string) string {
	return "tenant:" + strings.ToLower(address)
}

// AuthorizeTenant accepts a known tenant named by TenantID, or by the domain of AdminSubject; the
// service account key "refuse" is refused.
func (f *FakeExternalMailSource) AuthorizeTenant(_ context.Context, grant ExternalMailTenantGrant) (ExternalMailTenantAuthorization, error) {
	identifier := strings.ToLower(strings.TrimSpace(grant.TenantID))
	if identifier == "" {
		if at := strings.LastIndexByte(grant.AdminSubject, '@'); at >= 0 {
			identifier = strings.ToLower(grant.AdminSubject[at+1:])
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	tenant, known := f.tenants[identifier]
	if !known && (grant.ServiceAccountKey == FakeDevTenantSecret || grant.ClientSecret == FakeDevTenantSecret) &&
		fakeDevTenantIdentifier(identifier) {
		// A development tenant: the synthetic credential and a reserved domain.
		return ExternalMailTenantAuthorization{
			TenantIdentifier: identifier, DisplayName: identifier, Credential: fakeDevTenantCredential + identifier,
		}, nil
	}
	if !known || tenant.refused || grant.ServiceAccountKey == "refuse" {
		return ExternalMailTenantAuthorization{}, ErrExternalMailAuth
	}
	return ExternalMailTenantAuthorization{
		TenantIdentifier: identifier, DisplayName: identifier, Credential: fakeTenantCredential + identifier,
	}, nil
}

// tenant resolves a stored credential to its tenant, or ErrExternalMailAuth.
func (f *FakeExternalMailSource) tenant(credential string) (*fakeTenant, error) {
	if identifier, dev := strings.CutPrefix(credential, fakeDevTenantCredential); dev {
		if !fakeDevTenantIdentifier(identifier) {
			return nil, ErrExternalMailAuth
		}
		return fakeDevTenant(identifier), nil
	}
	identifier, found := strings.CutPrefix(credential, fakeTenantCredential)
	if !found {
		return nil, ErrExternalMailAuth
	}
	tenant, known := f.tenants[identifier]
	if !known || tenant.refused {
		return nil, ErrExternalMailAuth
	}
	return tenant, nil
}

func (f *FakeExternalMailSource) VerifyTenant(_ context.Context, credential string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, err := f.tenant(credential)
	return err
}

// ListTenantDirectory pages through the directory; the cursor is the index of the next entry.
func (f *FakeExternalMailSource) ListTenantDirectory(_ context.Context, credential, cursor string, limit int) (ExternalMailDirectoryPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tenant, err := f.tenant(credential)
	if err != nil {
		return ExternalMailDirectoryPage{}, err
	}
	start := 0
	if cursor != "" {
		if start, err = strconv.Atoi(cursor); err != nil || start < 0 {
			return ExternalMailDirectoryPage{}, fmt.Errorf("%w: invalid directory cursor", domain.ErrInvalidInput)
		}
	}
	start = min(start, len(tenant.directory))
	end := min(len(tenant.directory), start+max(limit, 1))
	page := ExternalMailDirectoryPage{Entries: append([]ExternalMailDirectoryEntry(nil), tenant.directory[start:end]...)}
	if end < len(tenant.directory) {
		page.Next = strconv.Itoa(end)
	}
	return page, nil
}

func (f *FakeExternalMailSource) LookupTenantMailbox(_ context.Context, credential, address string) (ExternalMailDirectoryEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tenant, err := f.tenant(credential)
	if err != nil {
		return ExternalMailDirectoryEntry{}, err
	}
	for _, entry := range tenant.directory {
		if strings.EqualFold(entry.Address, address) {
			return entry, nil
		}
	}
	return ExternalMailDirectoryEntry{}, ErrExternalMailDirectoryNotFound
}

func (f *FakeExternalMailSource) MailboxToken(_ context.Context, credential, address, directoryID string) (ExternalMailToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tenant, err := f.tenant(credential)
	if err != nil {
		return ExternalMailToken{}, err
	}
	if f.refusedMailboxes[strings.ToLower(address)] || (tenant.refusedMailbox != "" && strings.EqualFold(tenant.refusedMailbox, address)) {
		return ExternalMailToken{}, ErrExternalMailAuth
	}
	return ExternalMailToken{AccessToken: TenantMailboxToken(address), DirectoryID: directoryID}, nil
}
