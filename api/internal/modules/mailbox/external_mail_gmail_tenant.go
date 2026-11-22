package mailbox

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// Google Workspace tenants: the organization's own service account with domain-wide delegation. The
// owner authorizes its client id in the Workspace Admin console for gmailTenantMailScope and
// gmailTenantDirectoryScope and hands Zmail the JSON key with an administrator to read the directory
// as. Zmail signs a JWT with the key (RFC 7523) for one subject at a time: the administrator to read
// the directory, each assigned mailbox to read its mail. The key's own token_uri is ignored, so a
// key cannot point Zmail at another host.
const (
	gmailDirectoryBase = "https://admin.googleapis.com/admin/directory/v1"
	// gmailTenantMailScope is the only scope a mailbox token carries.
	gmailTenantMailScope = "https://www.googleapis.com/auth/gmail.readonly"
	// gmailTenantDirectoryScope lists the directory's users and nothing else.
	gmailTenantDirectoryScope = "https://www.googleapis.com/auth/admin.directory.user.readonly"
	gmailJWTBearerGrant       = "urn:ietf:params:oauth:grant-type:jwt-bearer"
	// gmailAssertionLifetime is the lifetime Google allows at most for an assertion.
	gmailAssertionLifetime = time.Hour
	// gmailMaxServiceAccountKey bounds the key an owner may post; a real one is about 2.4 KiB.
	gmailMaxServiceAccountKey = 16 << 10
	gmailMaxDirectoryBytes    = 1 << 20
	gmailTenantCredentialV    = 1
)

// gmailTenantSource serves Google Workspace tenants.
type gmailTenantSource struct {
	// tokenEndpoint is also the assertion's audience; tests point both endpoints at a local server.
	tokenEndpoint string
	directoryBase string
	httpClient    *http.Client
	now           func() time.Time
}

var _ ExternalMailTenantSource = (*gmailTenantSource)(nil)

func newGmailTenantSource() *gmailTenantSource {
	return &gmailTenantSource{
		tokenEndpoint: gmailTokenEndpoint, directoryBase: gmailDirectoryBase,
		httpClient: &http.Client{Timeout: gmailRequestTimeout}, now: time.Now,
	}
}

// gmailTenantCredential is the sealed form of a Workspace tenant: the key's signer, the
// administrator the directory is read as, and the customer id.
type gmailTenantCredential struct {
	Version      int    `json:"v"`
	ClientEmail  string `json:"client_email"`
	PrivateKey   string `json:"private_key"`
	PrivateKeyID string `json:"private_key_id,omitempty"`
	AdminSubject string `json:"admin_subject"`
	CustomerID   string `json:"customer_id"`
}

// AuthorizeTenant reads the service account key, proves both delegated scopes with a token for the
// administrator, and reads one directory user to learn the customer id.
func (g *gmailTenantSource) AuthorizeTenant(ctx context.Context, grant ExternalMailTenantGrant) (ExternalMailTenantAuthorization, error) {
	if len(grant.ServiceAccountKey) > gmailMaxServiceAccountKey {
		return ExternalMailTenantAuthorization{}, ErrExternalMailAuthorizationInvalid
	}
	var key struct {
		Type         string `json:"type"`
		ClientEmail  string `json:"client_email"`
		PrivateKey   string `json:"private_key"`
		PrivateKeyID string `json:"private_key_id"`
	}
	if err := json.Unmarshal([]byte(grant.ServiceAccountKey), &key); err != nil || key.Type != "service_account" ||
		strings.TrimSpace(key.ClientEmail) == "" {
		return ExternalMailTenantAuthorization{}, ErrExternalMailAuthorizationInvalid
	}
	admin := strings.ToLower(strings.TrimSpace(grant.AdminSubject))
	parsed, err := mail.ParseAddress(admin)
	if err != nil || parsed.Address != admin {
		return ExternalMailTenantAuthorization{}, ErrExternalMailAuthorizationInvalid
	}
	if _, err = parseServiceAccountKey(key.PrivateKey); err != nil {
		return ExternalMailTenantAuthorization{}, ErrExternalMailAuthorizationInvalid
	}
	credential := gmailTenantCredential{
		Version: gmailTenantCredentialV, ClientEmail: strings.TrimSpace(key.ClientEmail), PrivateKey: key.PrivateKey,
		PrivateKeyID: key.PrivateKeyID, AdminSubject: admin,
	}
	accessToken, err := g.adminToken(ctx, credential)
	if err != nil {
		return ExternalMailTenantAuthorization{}, err
	}
	var listed gmailDirectoryUsers
	if err = g.directoryGet(ctx, accessToken, "/users", url.Values{
		"customer": {"my_customer"}, "maxResults": {"1"}, "projection": {"basic"},
	}, &listed); err != nil {
		return ExternalMailTenantAuthorization{}, err
	}
	if len(listed.Users) == 0 || strings.TrimSpace(listed.Users[0].CustomerID) == "" {
		return ExternalMailTenantAuthorization{}, fmt.Errorf("%w: the directory names no customer", ErrExternalMailAuth)
	}
	credential.CustomerID = strings.TrimSpace(listed.Users[0].CustomerID)
	encoded, err := json.Marshal(credential)
	if err != nil {
		return ExternalMailTenantAuthorization{}, err
	}
	return ExternalMailTenantAuthorization{
		TenantIdentifier: credential.CustomerID, DisplayName: admin[strings.LastIndexByte(admin, '@')+1:], Credential: string(encoded),
	}, nil
}

// VerifyTenant mints the administrator's token for both scopes again.
func (g *gmailTenantSource) VerifyTenant(ctx context.Context, credential string) error {
	decoded, err := decodeGmailTenantCredential(credential)
	if err != nil {
		return err
	}
	_, err = g.adminToken(ctx, decoded)
	return err
}

// gmailDirectoryUser is the part of a Directory API user the tenant reads.
type gmailDirectoryUser struct {
	ID           string `json:"id"`
	PrimaryEmail string `json:"primaryEmail"`
	CustomerID   string `json:"customerId"`
	Suspended    bool   `json:"suspended"`
	Archived     bool   `json:"archived"`
	Name         struct {
		FullName string `json:"fullName"`
	} `json:"name"`
}

func (u gmailDirectoryUser) entry() ExternalMailDirectoryEntry {
	return ExternalMailDirectoryEntry{
		Address: strings.ToLower(strings.TrimSpace(u.PrimaryEmail)), DisplayName: truncateDisplayName(cleanText(u.Name.FullName)),
		DirectoryID: u.ID,
	}
}

type gmailDirectoryUsers struct {
	Users         []gmailDirectoryUser `json:"users"`
	NextPageToken string               `json:"nextPageToken"`
}

// ListTenantDirectory reads one page of the customer's users by address; suspended and archived
// users, whose mail cannot be read, are left out. The cursor is Google's page token.
func (g *gmailTenantSource) ListTenantDirectory(ctx context.Context, credential, cursor string, limit int) (ExternalMailDirectoryPage, error) {
	decoded, err := decodeGmailTenantCredential(credential)
	if err != nil {
		return ExternalMailDirectoryPage{}, err
	}
	accessToken, err := g.adminToken(ctx, decoded)
	if err != nil {
		return ExternalMailDirectoryPage{}, err
	}
	query := url.Values{
		"customer": {decoded.CustomerID}, "maxResults": {strconv.Itoa(min(max(limit, 1), 500))},
		"orderBy": {"email"}, "projection": {"basic"}, "viewType": {"admin_view"},
	}
	if cursor != "" {
		if !validPageToken(cursor) {
			return ExternalMailDirectoryPage{}, fmt.Errorf("%w: malformed directory cursor", domain.ErrInvalidInput)
		}
		query.Set("pageToken", cursor)
	}
	var listed gmailDirectoryUsers
	err = g.directoryGet(ctx, accessToken, "/users", query, &listed)
	var status *gmailStatusError
	if cursor != "" && errors.As(err, &status) && status.Status == http.StatusBadRequest {
		// Google refused the page token: the caller's cursor, not the credential.
		return ExternalMailDirectoryPage{}, fmt.Errorf("%w: directory cursor refused", domain.ErrInvalidInput)
	}
	if err != nil {
		return ExternalMailDirectoryPage{}, err
	}
	if !validPageToken(listed.NextPageToken) {
		listed.NextPageToken = ""
	}
	page := ExternalMailDirectoryPage{Entries: []ExternalMailDirectoryEntry{}, Next: listed.NextPageToken}
	for _, user := range listed.Users {
		if user.Suspended || user.Archived || user.ID == "" || user.PrimaryEmail == "" {
			continue
		}
		page.Entries = append(page.Entries, user.entry())
	}
	return page, nil
}

// LookupTenantMailbox reads the user of an address. An alias resolves to its user at Google; it is
// not that user's address, so it is answered not found and only primary addresses are assigned.
func (g *gmailTenantSource) LookupTenantMailbox(ctx context.Context, credential, address string) (ExternalMailDirectoryEntry, error) {
	decoded, err := decodeGmailTenantCredential(credential)
	if err != nil {
		return ExternalMailDirectoryEntry{}, err
	}
	accessToken, err := g.adminToken(ctx, decoded)
	if err != nil {
		return ExternalMailDirectoryEntry{}, err
	}
	var user gmailDirectoryUser
	err = g.directoryGet(ctx, accessToken, "/users/"+url.PathEscape(address), url.Values{"projection": {"basic"}, "viewType": {"admin_view"}}, &user)
	var status *gmailStatusError
	if errors.As(err, &status) && status.Status == http.StatusNotFound {
		return ExternalMailDirectoryEntry{}, ErrExternalMailDirectoryNotFound
	}
	if err != nil {
		return ExternalMailDirectoryEntry{}, err
	}
	if !strings.EqualFold(strings.TrimSpace(user.PrimaryEmail), address) || user.CustomerID != decoded.CustomerID ||
		user.Suspended || user.Archived || user.ID == "" {
		return ExternalMailDirectoryEntry{}, ErrExternalMailDirectoryNotFound
	}
	return user.entry(), nil
}

// MailboxToken mints a gmail.readonly token with the mailbox as the delegated subject: it reads that
// mailbox and no other.
func (g *gmailTenantSource) MailboxToken(ctx context.Context, credential, address, directoryID string) (ExternalMailToken, error) {
	decoded, err := decodeGmailTenantCredential(credential)
	if err != nil {
		return ExternalMailToken{}, err
	}
	accessToken, err := g.assertionToken(ctx, decoded, address, gmailTenantMailScope)
	if err != nil {
		return ExternalMailToken{}, err
	}
	return ExternalMailToken{AccessToken: accessToken, DirectoryID: directoryID}, nil
}

// adminToken is the administrator's token for both scopes, so it also proves the mail delegation.
func (g *gmailTenantSource) adminToken(ctx context.Context, credential gmailTenantCredential) (string, error) {
	return g.assertionToken(ctx, credential, credential.AdminSubject, gmailTenantMailScope+" "+gmailTenantDirectoryScope)
}

// assertionToken exchanges a signed assertion for subject at the token endpoint. Google refuses a
// key, a delegation, or a subject with 400 or 401 and an OAuth error: ErrExternalMailAuth. Throttling
// and server errors are plain errors the worker retries.
func (g *gmailTenantSource) assertionToken(ctx context.Context, credential gmailTenantCredential, subject, scope string) (string, error) {
	key, err := parseServiceAccountKey(credential.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("%w: unreadable service account key", ErrExternalMailAuth)
	}
	assertion, err := signServiceAccountJWT(key, credential.PrivateKeyID, map[string]any{
		"iss": credential.ClientEmail, "sub": subject, "scope": scope, "aud": g.tokenEndpoint,
		"iat": g.now().Unix(), "exp": g.now().Add(gmailAssertionLifetime).Unix(),
	})
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {gmailJWTBearerGrant}, "assertion": {assertion}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, g.tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("gmail tenant token: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := g.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("gmail tenant token: %w", redactURLError(err))
	}
	defer response.Body.Close()
	var tokens gmailTokenResponse
	body, readErr := readBounded(response.Body, gmailMaxTokenBytes)
	if readErr == nil {
		readErr = json.Unmarshal(body, &tokens)
	}
	if response.StatusCode != http.StatusOK {
		statusErr := &gmailStatusError{Operation: "tenant token", Status: response.StatusCode, Reason: shortReason(tokens.Error)}
		if response.StatusCode == http.StatusBadRequest || response.StatusCode == http.StatusUnauthorized ||
			response.StatusCode == http.StatusForbidden {
			return "", fmt.Errorf("%w: %w", ErrExternalMailAuth, statusErr)
		}
		return "", statusErr
	}
	if readErr != nil || tokens.AccessToken == "" {
		return "", errors.New("gmail tenant token: malformed answer")
	}
	return tokens.AccessToken, nil
}

// directoryGet reads a bounded Directory API answer. 401 and 403 mean the administrator or the
// delegation no longer reads the directory: ErrExternalMailAuth.
func (g *gmailTenantSource) directoryGet(ctx context.Context, accessToken, path string, query url.Values, target any) error {
	address := strings.TrimRight(g.directoryBase, "/") + path + "?" + query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return fmt.Errorf("gmail directory: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Accept", "application/json")
	response, err := g.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("gmail directory: %w", redactURLError(err))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		reason, _ := gmailErrorReason(response.Body)
		statusErr := &gmailStatusError{Operation: "directory", Status: response.StatusCode, Reason: reason}
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			return fmt.Errorf("%w: %w", ErrExternalMailAuth, statusErr)
		}
		return statusErr
	}
	body, err := readBounded(response.Body, gmailMaxDirectoryBytes)
	if err != nil {
		return fmt.Errorf("gmail directory: %w", err)
	}
	if err := json.Unmarshal(body, target); err != nil {
		return errors.New("gmail directory: malformed answer")
	}
	return nil
}

// decodeGmailTenantCredential reads a sealed tenant; one it cannot read is a lost credential.
func decodeGmailTenantCredential(raw string) (gmailTenantCredential, error) {
	var credential gmailTenantCredential
	if err := json.Unmarshal([]byte(raw), &credential); err != nil || credential.Version != gmailTenantCredentialV ||
		credential.ClientEmail == "" || credential.AdminSubject == "" || credential.CustomerID == "" {
		return gmailTenantCredential{}, fmt.Errorf("%w: unreadable tenant credential", ErrExternalMailAuth)
	}
	return credential, nil
}

// Bounds of a service account's RSA key.
const (
	minServiceAccountKeyBits = 2048
	maxServiceAccountKeyBits = 4096
	// maxDirectoryPageToken bounds a provider page token carried as a cursor.
	maxDirectoryPageToken = 2048
)

// validPageToken reports whether a provider page token is a bounded run of URL-safe characters. It
// never holds a scheme or a host, so a cursor cannot name another resource.
func validPageToken(token string) bool {
	if token == "" || len(token) > maxDirectoryPageToken {
		return false
	}
	for _, char := range token {
		switch {
		case 'a' <= char && char <= 'z', 'A' <= char && char <= 'Z', '0' <= char && char <= '9':
		case strings.ContainsRune("-_.~+/=!*'()", char):
		default:
			return false
		}
	}
	return true
}

// parseServiceAccountKey reads the RSA key of a service account key file, PKCS #8, as Google issues
// it, or PKCS #1, of 2048 to 4096 bits.
func parseServiceAccountKey(raw string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(raw))
	if block == nil {
		return nil, errors.New("service account key: no PEM block")
	}
	var rsaKey *rsa.PrivateKey
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		var ok bool
		if rsaKey, ok = key.(*rsa.PrivateKey); !ok {
			return nil, errors.New("service account key: not an RSA key")
		}
	} else if rsaKey, err = x509.ParsePKCS1PrivateKey(block.Bytes); err != nil {
		return nil, err
	}
	if bits := rsaKey.N.BitLen(); bits < minServiceAccountKeyBits || bits > maxServiceAccountKeyBits {
		return nil, fmt.Errorf("service account key: %d bits, want %d to %d", bits, minServiceAccountKeyBits, maxServiceAccountKeyBits)
	}
	return rsaKey, nil
}

// signServiceAccountJWT signs claims with RS256 (RFC 7515, RFC 7518).
func signServiceAccountJWT(key *rsa.PrivateKey, keyID string, claims map[string]any) (string, error) {
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	if keyID != "" {
		header["kid"] = keyID
	}
	encodedHeader, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	encodedClaims, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := base64.RawURLEncoding.EncodeToString(encodedHeader) + "." + base64.RawURLEncoding.EncodeToString(encodedClaims)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign service account assertion: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}
