package mailbox

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
)

var _ ExternalMailTenantSource = (*FakeExternalMailSource)(nil)

// fakeTenantCredential is the credential a fake tenant stores: its identifier behind a prefix.
const fakeTenantCredential = "fake-tenant:"

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

// TenantConsentURL serves an admin consent page for any tenant.
func (f *FakeExternalMailSource) TenantConsentURL(consent ExternalMailTenantConsent) (string, bool) {
	query := url.Values{"tenant": {consent.TenantID}, "state": {consent.State}, "redirect_uri": {consent.RedirectURI}}
	return "https://external-mail.invalid/adminconsent?" + query.Encode(), true
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
	if !known || tenant.refused || grant.ServiceAccountKey == "refuse" {
		return ExternalMailTenantAuthorization{}, ErrExternalMailAuth
	}
	return ExternalMailTenantAuthorization{
		TenantIdentifier: identifier, DisplayName: identifier, Credential: fakeTenantCredential + identifier,
	}, nil
}

// tenant resolves a stored credential to its tenant, or ErrExternalMailAuth.
func (f *FakeExternalMailSource) tenant(credential string) (*fakeTenant, error) {
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
			return ExternalMailDirectoryPage{}, errors.New("fake external mail: invalid directory cursor")
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
	if _, err := f.tenant(credential); err != nil {
		return ExternalMailToken{}, err
	}
	if f.refusedMailboxes[strings.ToLower(address)] {
		return ExternalMailToken{}, ErrExternalMailAuth
	}
	return ExternalMailToken{AccessToken: TenantMailboxToken(address), DirectoryID: directoryID}, nil
}
