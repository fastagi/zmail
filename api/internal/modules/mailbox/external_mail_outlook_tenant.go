package mailbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
)

// Microsoft 365 tenants: the organization's own application registration in its Entra tenant, with
// the Microsoft Graph application permissions Mail.Read and User.Read.All and admin consent. The owner
// posts the tenant id with the application's client id and secret; tokens come from the client
// credentials flow. An application token is tenant-wide at Microsoft: Zmail only ever addresses the
// assigned mailbox with one, and the customer should restrict the application to the assigned
// mailboxes with RBAC for Applications in Exchange Online. The operator's own application is never
// used for tenants: an admin consent to it cannot be tied to the organization that claims it.
const (
	outlookTenantScope           = "https://graph.microsoft.com/.default"
	outlookMaxDirectoryResponse  = 1 << 20
	outlookTenantCredentialV     = 1
	outlookDirectoryUserFields   = "id,displayName,mail,userPrincipalName"
	outlookMaxClientSecretLength = 1024
	// outlookTokenCacheSize bounds the cached application tokens; outlookTokenCacheMargin is how long
	// before its expiry a cached token is no longer handed out.
	outlookTokenCacheSize   = 1024
	outlookTokenCacheMargin = 5 * time.Minute
)

// outlookTenantIDPattern is the shape of an Entra tenant id.
var outlookTenantIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// outlookTenantSource serves Microsoft 365 tenants with the organizations' own applications.
type outlookTenantSource struct {
	// graph is a connector instance for its login and Graph hosts and bounded reads; tests point it
	// at fakes.
	graph *outlookMailSource
	now   func() time.Time
	// tokens caches application tokens by a hash of the tenant and client, for their lifetime, so a
	// worker pass over many mailboxes of one tenant asks Microsoft once. Never logged.
	mu     sync.Mutex
	tokens map[string]outlookCachedToken
}

type outlookCachedToken struct {
	accessToken string
	usableUntil time.Time
}

var _ ExternalMailTenantSource = (*outlookTenantSource)(nil)

func newOutlookTenantSource(client config.ExternalMailOAuthClient) *outlookTenantSource {
	return &outlookTenantSource{graph: newOutlookMailSource(client), now: time.Now, tokens: map[string]outlookCachedToken{}}
}

// outlookTenantCredential is the sealed form of a Microsoft tenant: its id and its application's client.
type outlookTenantCredential struct {
	Version      int    `json:"v"`
	TenantID     string `json:"tenant_id"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// AuthorizeTenant proves the organization's application with a client credentials token for the
// tenant and one directory read.
func (o *outlookTenantSource) AuthorizeTenant(ctx context.Context, grant ExternalMailTenantGrant) (ExternalMailTenantAuthorization, error) {
	tenantID := strings.ToLower(strings.TrimSpace(grant.TenantID))
	clientID, secret := strings.TrimSpace(grant.ClientID), grant.ClientSecret
	if !outlookTenantIDPattern.MatchString(tenantID) || clientID == "" || secret == "" ||
		len(clientID) > 128 || len(secret) > outlookMaxClientSecretLength {
		return ExternalMailTenantAuthorization{}, ErrExternalMailAuthorizationInvalid
	}
	credential := outlookTenantCredential{Version: outlookTenantCredentialV, TenantID: tenantID, ClientID: clientID, ClientSecret: secret}
	if err := o.verify(ctx, credential); err != nil {
		return ExternalMailTenantAuthorization{}, err
	}
	encoded, err := json.Marshal(credential)
	if err != nil {
		return ExternalMailTenantAuthorization{}, err
	}
	return ExternalMailTenantAuthorization{TenantIdentifier: tenantID, Credential: string(encoded)}, nil
}

// VerifyTenant does what authorizing did: a fresh token and one directory read, so a removed
// permission is noticed as well as a removed application or secret.
func (o *outlookTenantSource) VerifyTenant(ctx context.Context, credential string) error {
	decoded, err := decodeOutlookTenantCredential(credential)
	if err != nil {
		return err
	}
	return o.verify(ctx, decoded)
}

func (o *outlookTenantSource) verify(ctx context.Context, credential outlookTenantCredential) error {
	accessToken, err := o.token(ctx, credential, true)
	if err != nil {
		return err
	}
	var listed outlookDirectoryUsers
	return o.get(ctx, accessToken, o.graph.graphURL("/users?$top=1&$select=id"), &listed)
}

type outlookDirectoryUser struct {
	ID                string `json:"id"`
	DisplayName       string `json:"displayName"`
	Mail              string `json:"mail"`
	UserPrincipalName string `json:"userPrincipalName"`
}

type outlookDirectoryUsers struct {
	Value    []outlookDirectoryUser `json:"value"`
	NextLink string                 `json:"@odata.nextLink"`
}

// ListTenantDirectory reads one page of the tenant's users; users without a mailbox address are left
// out. The cursor is only Graph's $skiptoken: the request is always /users with a fixed query built
// here, so a cursor can never point the tenant token at another resource.
func (o *outlookTenantSource) ListTenantDirectory(ctx context.Context, credential, cursor string, limit int) (ExternalMailDirectoryPage, error) {
	decoded, err := decodeOutlookTenantCredential(credential)
	if err != nil {
		return ExternalMailDirectoryPage{}, err
	}
	if cursor != "" && !validPageToken(cursor) {
		return ExternalMailDirectoryPage{}, fmt.Errorf("%w: malformed directory cursor", domain.ErrInvalidInput)
	}
	query := url.Values{"$select": {outlookDirectoryUserFields}, "$top": {strconv.Itoa(min(max(limit, 1), 999))}}
	if cursor != "" {
		query.Set("$skiptoken", cursor)
	}
	accessToken, err := o.token(ctx, decoded, false)
	if err != nil {
		return ExternalMailDirectoryPage{}, err
	}
	var listed outlookDirectoryUsers
	err = o.get(ctx, accessToken, o.graph.graphURL("/users?"+query.Encode()), &listed)
	if cursor != "" && errors.Is(err, errOutlookBadRequest) {
		// Graph refused the skip token: the caller's cursor, not the credential.
		return ExternalMailDirectoryPage{}, fmt.Errorf("%w: directory cursor refused", domain.ErrInvalidInput)
	}
	if err != nil {
		return ExternalMailDirectoryPage{}, err
	}
	page := ExternalMailDirectoryPage{Entries: []ExternalMailDirectoryEntry{}, Next: outlookSkipToken(listed.NextLink)}
	for _, user := range listed.Value {
		if entry, ok := user.entry(); ok {
			page.Entries = append(page.Entries, entry)
		}
	}
	return page, nil
}

// outlookSkipToken is the $skiptoken of a Graph next link, or empty when it has none usable.
func outlookSkipToken(link string) string {
	if link == "" {
		return ""
	}
	parsed, err := url.Parse(link)
	if err != nil {
		return ""
	}
	token := parsed.Query().Get("$skiptoken")
	if !validPageToken(token) {
		return ""
	}
	return token
}

func (u outlookDirectoryUser) entry() (ExternalMailDirectoryEntry, bool) {
	address := strings.ToLower(strings.TrimSpace(cleanText(u.Mail)))
	if address == "" || u.ID == "" {
		return ExternalMailDirectoryEntry{}, false
	}
	return ExternalMailDirectoryEntry{
		Address: address, DisplayName: truncateDisplayName(cleanText(u.DisplayName)), DirectoryID: u.ID,
	}, true
}

// LookupTenantMailbox finds the user whose mail address is address.
func (o *outlookTenantSource) LookupTenantMailbox(ctx context.Context, credential, address string) (ExternalMailDirectoryEntry, error) {
	decoded, err := decodeOutlookTenantCredential(credential)
	if err != nil {
		return ExternalMailDirectoryEntry{}, err
	}
	accessToken, err := o.token(ctx, decoded, false)
	if err != nil {
		return ExternalMailDirectoryEntry{}, err
	}
	filter := "mail eq '" + strings.ReplaceAll(address, "'", "''") + "'"
	query := url.Values{"$filter": {filter}, "$select": {outlookDirectoryUserFields}, "$top": {"2"}}
	var listed outlookDirectoryUsers
	if err = o.get(ctx, accessToken, o.graph.graphURL("/users?"+query.Encode()), &listed); err != nil {
		return ExternalMailDirectoryEntry{}, err
	}
	for _, user := range listed.Value {
		if entry, ok := user.entry(); ok && strings.EqualFold(entry.Address, address) {
			return entry, nil
		}
	}
	return ExternalMailDirectoryEntry{}, ErrExternalMailDirectoryNotFound
}

// MailboxToken is an application token; the fetch addresses only /users/{directoryID} with it.
func (o *outlookTenantSource) MailboxToken(ctx context.Context, credential, _, directoryID string) (ExternalMailToken, error) {
	decoded, err := decodeOutlookTenantCredential(credential)
	if err != nil {
		return ExternalMailToken{}, err
	}
	if directoryID == "" {
		return ExternalMailToken{}, errors.New("outlook tenant: the mailbox has no directory id")
	}
	accessToken, err := o.token(ctx, decoded, false)
	if err != nil {
		return ExternalMailToken{}, err
	}
	return ExternalMailToken{AccessToken: accessToken, DirectoryID: directoryID}, nil
}

// cacheKey identifies a tenant's client without keeping its secret in the map.
func (c outlookTenantCredential) cacheKey() string {
	sum := sha256.Sum256([]byte(c.TenantID + "\x00" + c.ClientID + "\x00" + c.ClientSecret))
	return hex.EncodeToString(sum[:])
}

// token runs the client credentials flow against the tenant, or hands out a cached token unless fresh
// is set. Microsoft's refusal (400, 401, 403: a deleted application or secret, consent withdrawn) is
// ErrExternalMailAuth: the application is the organization's own, so the tenant needs
// reauthorization. Throttling, server errors, and transport failures are plain errors the worker
// retries, and never stop the tenant.
func (o *outlookTenantSource) token(ctx context.Context, credential outlookTenantCredential, fresh bool) (string, error) {
	key := credential.cacheKey()
	if !fresh {
		o.mu.Lock()
		cached, ok := o.tokens[key]
		o.mu.Unlock()
		if ok && o.now().Before(cached.usableUntil) {
			return cached.accessToken, nil
		}
	}
	form := url.Values{
		"grant_type": {"client_credentials"}, "client_id": {credential.ClientID}, "client_secret": {credential.ClientSecret},
		"scope": {outlookTenantScope},
	}
	endpoint := strings.TrimRight(o.graph.loginBaseURL, "/") + "/" + url.PathEscape(credential.TenantID) + "/oauth2/v2.0/token"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("outlook tenant: build token request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := o.graph.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("outlook tenant: token request: %w", outlookRedactURLError(err))
	}
	defer response.Body.Close()
	body, err := outlookReadBounded(response.Body, outlookMaxTokenResponse)
	if err != nil {
		return "", fmt.Errorf("outlook tenant: token response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		o.forget(key)
		var failure struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &failure) != nil {
			failure.Error = ""
		}
		if response.StatusCode == http.StatusBadRequest || response.StatusCode == http.StatusUnauthorized ||
			response.StatusCode == http.StatusForbidden {
			return "", fmt.Errorf("%w: tenant token endpoint answered %d %s", ErrExternalMailAuth, response.StatusCode, outlookErrorCode(failure.Error))
		}
		return "", fmt.Errorf("outlook tenant: token endpoint answered %d %s", response.StatusCode, outlookErrorCode(failure.Error))
	}
	var grant struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err = json.Unmarshal(body, &grant); err != nil || grant.AccessToken == "" {
		return "", errors.New("outlook tenant: token endpoint answered without an access token")
	}
	o.remember(key, grant.AccessToken, time.Duration(grant.ExpiresIn)*time.Second)
	return grant.AccessToken, nil
}

// remember caches a token for its lifetime less a margin; a short-lived one is not cached. A full
// cache drops its expired entries, and starts over when none expired.
func (o *outlookTenantSource) remember(key, accessToken string, lifetime time.Duration) {
	if lifetime <= outlookTokenCacheMargin {
		o.forget(key)
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	now := o.now()
	if len(o.tokens) >= outlookTokenCacheSize {
		for cachedKey, cached := range o.tokens {
			if !now.Before(cached.usableUntil) {
				delete(o.tokens, cachedKey)
			}
		}
		if len(o.tokens) >= outlookTokenCacheSize {
			o.tokens = map[string]outlookCachedToken{}
		}
	}
	o.tokens[key] = outlookCachedToken{accessToken: accessToken, usableUntil: now.Add(lifetime - outlookTokenCacheMargin)}
}

func (o *outlookTenantSource) forget(key string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.tokens, key)
}

// errOutlookBadRequest marks a Graph 400, such as a skip token Graph no longer honors.
var errOutlookBadRequest = errors.New("outlook tenant: graph answered 400")

// get reads one bounded Graph directory answer. 401 and 403 (no User.Read.All, consent removed) are
// ErrExternalMailAuth; 400 is errOutlookBadRequest; 429 and 5xx are plain errors.
func (o *outlookTenantSource) get(ctx context.Context, accessToken, link string, out any) error {
	if !o.graph.graphLink(link) {
		return errors.New("outlook tenant: refusing a link outside the Graph host")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return fmt.Errorf("outlook tenant: build graph request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Accept", "application/json")
	response, err := o.graph.client.Do(request)
	if err != nil {
		return fmt.Errorf("outlook tenant: graph request: %w", outlookRedactURLError(err))
	}
	defer response.Body.Close()
	body, err := outlookReadBounded(response.Body, outlookMaxDirectoryResponse)
	if err != nil {
		return fmt.Errorf("outlook tenant: graph response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		var failure struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &failure) != nil {
			failure.Error.Code = ""
		}
		switch response.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return fmt.Errorf("%w: graph answered %d %s", ErrExternalMailAuth, response.StatusCode, outlookErrorCode(failure.Error.Code))
		case http.StatusBadRequest:
			return fmt.Errorf("%w %s", errOutlookBadRequest, outlookErrorCode(failure.Error.Code))
		}
		return fmt.Errorf("outlook tenant: graph answered %d %s", response.StatusCode, outlookErrorCode(failure.Error.Code))
	}
	if err = json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("outlook tenant: decode graph response: %w", err)
	}
	return nil
}

func decodeOutlookTenantCredential(raw string) (outlookTenantCredential, error) {
	var credential outlookTenantCredential
	if err := json.Unmarshal([]byte(raw), &credential); err != nil || credential.Version != outlookTenantCredentialV ||
		!outlookTenantIDPattern.MatchString(credential.TenantID) || credential.ClientID == "" || credential.ClientSecret == "" {
		return outlookTenantCredential{}, fmt.Errorf("%w: unreadable tenant credential", ErrExternalMailAuth)
	}
	return credential, nil
}
