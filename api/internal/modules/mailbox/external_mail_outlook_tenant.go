package mailbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
)

// Microsoft 365 tenants: application permissions granted by an administrator's consent. The
// application is the operator's (EXTERNAL_MAIL_OUTLOOK_CLIENT_ID, consented through the admin consent
// redirect) or the organization's own (its client id and secret posted by the owner). It needs the
// Microsoft Graph application permissions Mail.Read and User.Read.All. Its tokens come from the client
// credentials flow and are tenant-wide at Microsoft: Zmail only ever addresses the assigned mailbox
// with one, and the customer should restrict the application to the assigned mailboxes with RBAC for
// Applications in Exchange Online.
const (
	outlookTenantScope           = "https://graph.microsoft.com/.default"
	outlookMaxDirectoryResponse  = 1 << 20
	outlookTenantCredentialV     = 1
	outlookDirectoryUserFields   = "id,displayName,mail,userPrincipalName"
	outlookMaxClientSecretLength = 1024
)

// outlookTenantIDPattern is the shape of an Entra tenant id. Only the id is accepted, never a domain,
// so the consent page and the completion name the same tenant.
var outlookTenantIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// outlookTenantSource serves Microsoft 365 tenants. It reuses the delegated connector's endpoints.
type outlookTenantSource struct {
	// clientID and clientSecret are the operator's application.
	clientID     string
	clientSecret string
	// graph is a connector instance for its Graph host and bounded reads; tests point it at fakes.
	graph *outlookMailSource
}

var _ ExternalMailTenantSource = (*outlookTenantSource)(nil)

func newOutlookTenantSource(client config.ExternalMailOAuthClient) *outlookTenantSource {
	return &outlookTenantSource{
		clientID: strings.TrimSpace(client.ClientID), clientSecret: client.ClientSecret, graph: newOutlookMailSource(client),
	}
}

// outlookTenantCredential is the sealed form of a Microsoft tenant: its id and, for an organization's
// own application, its client.
type outlookTenantCredential struct {
	Version      int    `json:"v"`
	TenantID     string `json:"tenant_id"`
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
}

// TenantConsentURL is the admin consent page of the operator's application, pinned to the tenant.
func (o *outlookTenantSource) TenantConsentURL(consent ExternalMailTenantConsent) (string, bool) {
	if o.clientID == "" || !outlookTenantIDPattern.MatchString(consent.TenantID) {
		return "", false
	}
	query := url.Values{
		"client_id": {o.clientID}, "scope": {outlookTenantScope}, "redirect_uri": {consent.RedirectURI}, "state": {consent.State},
	}
	return strings.TrimRight(o.graph.loginBaseURL, "/") + "/" + url.PathEscape(strings.ToLower(consent.TenantID)) +
		"/v2.0/adminconsent?" + query.Encode(), true
}

// AuthorizeTenant proves the application's access with a client credentials token for the tenant and
// one directory read.
func (o *outlookTenantSource) AuthorizeTenant(ctx context.Context, grant ExternalMailTenantGrant) (ExternalMailTenantAuthorization, error) {
	tenantID := strings.ToLower(strings.TrimSpace(grant.TenantID))
	clientID, secret := strings.TrimSpace(grant.ClientID), grant.ClientSecret
	if !outlookTenantIDPattern.MatchString(tenantID) || (clientID == "") != (secret == "") ||
		len(clientID) > 128 || len(secret) > outlookMaxClientSecretLength {
		return ExternalMailTenantAuthorization{}, ErrExternalMailAuthorizationInvalid
	}
	credential := outlookTenantCredential{Version: outlookTenantCredentialV, TenantID: tenantID, ClientID: clientID, ClientSecret: secret}
	accessToken, err := o.token(ctx, credential)
	if err != nil {
		return ExternalMailTenantAuthorization{}, err
	}
	var listed outlookDirectoryUsers
	if err = o.get(ctx, accessToken, o.graph.graphURL("/users?$top=1&$select=id"), &listed); err != nil {
		return ExternalMailTenantAuthorization{}, err
	}
	encoded, err := json.Marshal(credential)
	if err != nil {
		return ExternalMailTenantAuthorization{}, err
	}
	return ExternalMailTenantAuthorization{TenantIdentifier: tenantID, Credential: string(encoded)}, nil
}

func (o *outlookTenantSource) VerifyTenant(ctx context.Context, credential string) error {
	decoded, err := decodeOutlookTenantCredential(credential)
	if err != nil {
		return err
	}
	_, err = o.token(ctx, decoded)
	return err
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
// out. The cursor is Graph's next link, which must point at the Graph host.
func (o *outlookTenantSource) ListTenantDirectory(ctx context.Context, credential, cursor string, limit int) (ExternalMailDirectoryPage, error) {
	decoded, err := decodeOutlookTenantCredential(credential)
	if err != nil {
		return ExternalMailDirectoryPage{}, err
	}
	link := cursor
	if link == "" {
		link = o.graph.graphURL("/users?$select=" + outlookDirectoryUserFields + "&$top=" + strconv.Itoa(min(max(limit, 1), 999)))
	} else if !o.graph.graphLink(link) {
		return ExternalMailDirectoryPage{}, fmt.Errorf("%w: the directory cursor does not point at the Graph host", domain.ErrInvalidInput)
	}
	accessToken, err := o.token(ctx, decoded)
	if err != nil {
		return ExternalMailDirectoryPage{}, err
	}
	var listed outlookDirectoryUsers
	if err = o.get(ctx, accessToken, link, &listed); err != nil {
		return ExternalMailDirectoryPage{}, err
	}
	page := ExternalMailDirectoryPage{Entries: []ExternalMailDirectoryEntry{}}
	if listed.NextLink != "" && o.graph.graphLink(listed.NextLink) {
		page.Next = listed.NextLink
	}
	for _, user := range listed.Value {
		if entry, ok := user.entry(); ok {
			page.Entries = append(page.Entries, entry)
		}
	}
	return page, nil
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
	accessToken, err := o.token(ctx, decoded)
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
	accessToken, err := o.token(ctx, decoded)
	if err != nil {
		return ExternalMailToken{}, err
	}
	return ExternalMailToken{AccessToken: accessToken, DirectoryID: directoryID}, nil
}

// token runs the client credentials flow against the tenant. Every refusal (400, 401, 403: no
// consent, a removed application, an expired secret) is ErrExternalMailAuth: an application token has
// no member to blame, so the tenant needs reauthorization. 429 and 5xx are retried.
func (o *outlookTenantSource) token(ctx context.Context, credential outlookTenantCredential) (string, error) {
	clientID, secret := credential.ClientID, credential.ClientSecret
	if clientID == "" {
		clientID, secret = o.clientID, o.clientSecret
	}
	form := url.Values{
		"grant_type": {"client_credentials"}, "client_id": {clientID}, "client_secret": {secret}, "scope": {outlookTenantScope},
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
	var grant outlookGrant
	if err = json.Unmarshal(body, &grant); err != nil || grant.AccessToken == "" {
		return "", errors.New("outlook tenant: token endpoint answered without an access token")
	}
	return grant.AccessToken, nil
}

// get reads one bounded Graph directory answer. 401 and 403 (no User.Read.All, consent removed) are
// ErrExternalMailAuth.
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
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			return fmt.Errorf("%w: graph answered %d %s", ErrExternalMailAuth, response.StatusCode, outlookErrorCode(failure.Error.Code))
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
		!outlookTenantIDPattern.MatchString(credential.TenantID) {
		return outlookTenantCredential{}, fmt.Errorf("%w: unreadable tenant credential", ErrExternalMailAuth)
	}
	return credential, nil
}
