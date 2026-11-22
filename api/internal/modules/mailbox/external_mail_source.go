package mailbox

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
)

var (
	// ErrExternalMailUnavailable means the instance has no external mail source.
	ErrExternalMailUnavailable = errors.New("external mail source is unavailable")
	// ErrExternalMailAuth means the provider refused the token (HTTP 401 or a revoked grant): the
	// owner must bind the account again.
	ErrExternalMailAuth = errors.New("external mail authorization failed")
	// ErrExternalMailCursorExpired means the provider no longer honors the cursor (a Gmail
	// historyId that is too old, a Graph delta token that expired): the sync starts over from an
	// empty cursor, bounded by the connection's backfill window, and stored copies are not repeated.
	ErrExternalMailCursorExpired = errors.New("external mail cursor expired")
)

// ExternalMailToken is the credential a source fetches with: an account's own refresh token, or an
// access token a tenant credential minted for one mailbox.
type ExternalMailToken struct {
	RefreshToken string
	// AccessToken, when set, is used as it is, without a refresh: a tenant's short-lived token scoped
	// to the one mailbox DirectoryID (or the account address) names.
	AccessToken string
	// DirectoryID is the mailbox's id in its tenant's directory; empty for an account's own grant.
	DirectoryID string
}

// ExternalMailAccount is the outside account an authorization code was exchanged for.
type ExternalMailAccount struct {
	Address string
}

// ExternalMessage is one message as the provider returned it.
type ExternalMessage struct {
	// ExternalID is the provider's message id; a message is stored once per mailbox and id.
	ExternalID  string
	FromAddress string
	FromName    string
	To          []string
	Cc          []string
	Subject     string
	TextBody    string
	HTMLBody    string
	// RFCMessageID, InReplyTo, and References thread the copy like inbound mail, without brackets.
	RFCMessageID string
	InReplyTo    string
	References   []string
	// ReceivedAt is when the provider received the message; zero takes the sync time.
	ReceivedAt time.Time
}

// ExternalMailFetchRequest is what a source needs to read one page of an account.
type ExternalMailFetchRequest struct {
	Token ExternalMailToken
	// Cursor is the opaque position the source returned last; empty starts the backfill. A source
	// encodes whatever its provider needs (one historyId, a delta link per folder, UIDs per
	// folder) and nothing else reads it.
	Cursor string
	// AccountAddress is the bound account, for providers that address a mailbox by it.
	AccountAddress string
	// BackfillDays bounds how far back a fetch from an empty cursor reads.
	BackfillDays int
	// Now is the sync time, so the backfill window does not depend on the wall clock.
	Now time.Time
}

// ExternalMailFetch is one page of new mail and the cursor after it.
type ExternalMailFetch struct {
	Messages []ExternalMessage
	Cursor   string
	// More means another page is ready: the connection is fetched again at once instead of after
	// the sync interval, so a long backfill proceeds page by page and resumes after a restart.
	More bool
	// RefreshToken is set when the provider rotated the grant during this fetch; it is sealed and
	// saved in the transaction that stores the page. Empty keeps the stored token.
	// A source that rotated the grant and then failed returns it next to its error, and it is
	// saved all the same.
	RefreshToken string
}

// ExternalMailAuthRequest is what a sign-in page needs: the single-use state, the S256 PKCE
// challenge, and where the provider sends the browser back.
type ExternalMailAuthRequest struct {
	State         string
	CodeChallenge string
	RedirectURI   string
}

// ExternalMailCodeExchange turns the code the provider returned into a grant.
type ExternalMailCodeExchange struct {
	Code         string
	CodeVerifier string
	RedirectURI  string
}

// ExternalMailSource reads an outside mailbox. Each connector (Gmail, Outlook, and later any other
// provider) implements it once; nothing outside its adapter knows the provider's protocol.
type ExternalMailSource interface {
	// AuthURL is the provider page the member signs in on.
	AuthURL(request ExternalMailAuthRequest) string
	// Exchange returns the grant and the account it reads; ErrExternalMailAuth when the provider
	// refuses the code.
	Exchange(ctx context.Context, exchange ExternalMailCodeExchange) (ExternalMailToken, ExternalMailAccount, error)
	// Fetch returns one page of mail after the request's cursor. It returns ErrExternalMailAuth
	// when the provider refuses the token and ErrExternalMailCursorExpired when it no longer
	// honors the cursor.
	Fetch(ctx context.Context, request ExternalMailFetchRequest) (ExternalMailFetch, error)
	Revoke(ctx context.Context, token ExternalMailToken) error
}

// ExternalMailAuthKind is how a member grants a connector access.
type ExternalMailAuthKind string

const (
	// ExternalMailAuthOAuth2 is the OAuth 2.0 authorization code flow with PKCE.
	ExternalMailAuthOAuth2 ExternalMailAuthKind = "oauth2"
	// ExternalMailAuthPassword is an app password the member types in, for providers without
	// OAuth; it never goes through the sign-in redirect.
	ExternalMailAuthPassword ExternalMailAuthKind = "password"
)

// ExternalMailConnectorBuilder builds one connector from the configuration, or reports false when
// the configuration does not enable it.
type ExternalMailConnectorBuilder func(cfg config.ExternalMailConfig) (ExternalMailConnector, bool)

// externalMailConnectorBuilders are the real connectors compiled into the binary, in registration
// order. Each adapter file adds its own builder with registerExternalMailConnector from an init
// function, so adding a provider touches no shared file.
var externalMailConnectorBuilders []ExternalMailConnectorBuilder

// registerExternalMailConnector adds a connector builder; call it from an adapter's init.
func registerExternalMailConnector(builder ExternalMailConnectorBuilder) {
	externalMailConnectorBuilders = append(externalMailConnectorBuilders, builder)
}

// ExternalMailCapabilities says what a connector can do with a bound account.
type ExternalMailCapabilities struct {
	// Read syncs the account's mail into its external mailbox.
	Read bool
}

// ExternalMailConnector is one provider the instance can bind: its id, how it authorizes, what it
// can do, and the source that speaks its protocol. Tenant is set when an owner may authorize a whole
// tenant of the provider at once; nil otherwise.
type ExternalMailConnector struct {
	ID           domain.ExternalMailProvider
	AuthKind     ExternalMailAuthKind
	Capabilities ExternalMailCapabilities
	Source       ExternalMailSource
	Tenant       ExternalMailTenantSource
}

// ExternalMailTenantGrant is what an owner hands over to authorize a tenant. Each provider reads
// the fields it needs: Google a service account key and an admin to read the directory as;
// Microsoft the tenant id, with an application of the organization's own or, without one, the
// operator's application after an admin consented.
type ExternalMailTenantGrant struct {
	ServiceAccountKey string
	AdminSubject      string
	TenantID          string
	ClientID          string
	ClientSecret      string
}

// ExternalMailTenantAuthorization is a tenant credential the provider accepted.
type ExternalMailTenantAuthorization struct {
	// TenantIdentifier is the provider's stable id of the tenant.
	TenantIdentifier string
	// DisplayName labels the tenant, such as its primary domain; may be empty.
	DisplayName string
	// Credential is the source's own encoding of the tenant credential; the caller seals it and
	// nothing else reads it.
	Credential string
}

// ExternalMailTenantConsent is the admin consent page of a provider that grants tenant access by a
// redirect, with the single-use state and the callback.
type ExternalMailTenantConsent struct {
	TenantID    string
	State       string
	RedirectURI string
}

// ExternalMailDirectoryEntry is one mailbox of a tenant's directory.
type ExternalMailDirectoryEntry struct {
	Address     string
	DisplayName string
	// DirectoryID is the provider's stable id of the mailbox's user.
	DirectoryID string
}

// ExternalMailDirectoryPage is one page of a tenant's directory and the opaque cursor of the next.
type ExternalMailDirectoryPage struct {
	Entries []ExternalMailDirectoryEntry
	Next    string
}

// ErrExternalMailDirectoryNotFound means an address is not a mailbox of the tenant's directory.
var ErrExternalMailDirectoryNotFound = errors.New("external mail address is not in the tenant directory")

// ExternalMailTenantSource authorizes a whole tenant of a provider and reads its mailboxes with the
// tenant credential, one mailbox per token. Every method returns ErrExternalMailAuth when the
// provider refuses the credential.
type ExternalMailTenantSource interface {
	// TenantConsentURL is the admin consent page for the operator's application; false when the
	// provider takes the credential directly instead.
	TenantConsentURL(consent ExternalMailTenantConsent) (string, bool)
	// AuthorizeTenant proves the grant with the provider and returns the credential to store.
	AuthorizeTenant(ctx context.Context, grant ExternalMailTenantGrant) (ExternalMailTenantAuthorization, error)
	// VerifyTenant checks that a stored credential still works.
	VerifyTenant(ctx context.Context, credential string) error
	// ListTenantDirectory returns at most limit mailboxes after cursor; an empty cursor starts over.
	ListTenantDirectory(ctx context.Context, credential, cursor string, limit int) (ExternalMailDirectoryPage, error)
	// LookupTenantMailbox returns the directory entry of one address, or
	// ErrExternalMailDirectoryNotFound.
	LookupTenantMailbox(ctx context.Context, credential, address string) (ExternalMailDirectoryEntry, error)
	// MailboxToken mints a short-lived token that reads only the one mailbox.
	MailboxToken(ctx context.Context, credential, address, directoryID string) (ExternalMailToken, error)
}

// externalMailRegistry holds the instance's connectors in a stable order. Adding a provider is one
// adapter and one entry here; storage, sync, binding, and the API are provider-neutral.
type externalMailRegistry struct {
	connectors []ExternalMailConnector
}

// lookup returns the connector with the id.
func (r *externalMailRegistry) lookup(id domain.ExternalMailProvider) (ExternalMailConnector, bool) {
	if r == nil {
		return ExternalMailConnector{}, false
	}
	for _, connector := range r.connectors {
		if connector.ID == id {
			return connector, true
		}
	}
	return ExternalMailConnector{}, false
}

// list returns the connectors in registration order.
func (r *externalMailRegistry) list() []ExternalMailConnector {
	if r == nil {
		return nil
	}
	return append([]ExternalMailConnector(nil), r.connectors...)
}

// newExternalMailRegistry builds the connectors the configuration enables. The fake source stands
// in for both Gmail and Outlook, so local demos and tests exercise the real provider ids.
func newExternalMailRegistry(cfg config.ExternalMailConfig) *externalMailRegistry {
	if cfg.Source() == config.ExternalMailSourceFake {
		return fakeExternalMailRegistry(NewFakeExternalMailSource())
	}
	registry := &externalMailRegistry{}
	for _, build := range externalMailConnectorBuilders {
		connector, enabled := build(cfg)
		if !enabled || !connector.ID.Valid() || connector.Source == nil {
			continue
		}
		if _, duplicate := registry.lookup(connector.ID); !duplicate {
			registry.connectors = append(registry.connectors, connector)
		}
	}
	return registry
}

// fakeExternalMailRegistry registers one fake source under the Gmail and Outlook ids; a fake that
// also serves tenants is registered as their tenant source too.
func fakeExternalMailRegistry(source ExternalMailSource) *externalMailRegistry {
	read := ExternalMailCapabilities{Read: true}
	var tenant ExternalMailTenantSource
	if serves, ok := source.(ExternalMailTenantSource); ok {
		tenant = serves
	}
	return &externalMailRegistry{connectors: []ExternalMailConnector{
		{ID: domain.ExternalMailProviderGmail, AuthKind: ExternalMailAuthOAuth2, Capabilities: read, Source: source, Tenant: tenant},
		{ID: domain.ExternalMailProviderOutlook, AuthKind: ExternalMailAuthOAuth2, Capabilities: read, Source: source, Tenant: tenant},
	}}
}

// pkceChallenge is the S256 challenge of a code verifier (RFC 7636).
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// FakeExternalMailSource serves scripted mail from memory, keyed by refresh token, for tests and
// local demos. Its cursor is the number of a token's messages already delivered, so a fetch after
// a reset cursor returns the same messages again, as a reconnect does.
type FakeExternalMailSource struct {
	mu       sync.Mutex
	mail     map[string][]ExternalMessage
	authFail map[string]bool
	fetches  map[string]int
	revoked  map[string]bool
	// challenges are the PKCE challenges of sign-in pages served; an exchange must prove one.
	challenges map[string]bool
	// PageSize caps the messages of one fetch; zero returns everything at once.
	PageSize int
	// rotations maps a token to the one the next fetch with it hands out; aliases maps a rotated
	// token back to the account's first token.
	rotations map[string]string
	aliases   map[string]string
	// spent marks tokens replaced by a rotation.
	spent map[string]bool
	// expired marks tokens whose next fetch with a cursor answers ErrExternalMailCursorExpired.
	expired map[string]bool
	// requests keeps the last fetch request per account, for assertions.
	requests map[string]ExternalMailFetchRequest
	// tenants are the tenants this source accepts, by identifier: their directory, in order, and
	// whether their credential is refused.
	tenants map[string]*fakeTenant
	// refusedMailboxes are tenant mailboxes whose token is refused while the tenant still works.
	refusedMailboxes map[string]bool
}

// fakeTenant is one tenant a fake source serves.
type fakeTenant struct {
	directory []ExternalMailDirectoryEntry
	refused   bool
}

var _ ExternalMailSource = (*FakeExternalMailSource)(nil)

// NewFakeExternalMailSource returns an empty fake source.
func NewFakeExternalMailSource() *FakeExternalMailSource {
	return &FakeExternalMailSource{
		mail: map[string][]ExternalMessage{}, authFail: map[string]bool{},
		fetches: map[string]int{}, revoked: map[string]bool{}, challenges: map[string]bool{},
		rotations: map[string]string{}, aliases: map[string]string{}, expired: map[string]bool{}, spent: map[string]bool{},
		requests: map[string]ExternalMailFetchRequest{}, tenants: map[string]*fakeTenant{}, refusedMailboxes: map[string]bool{},
	}
}

// Deliver appends messages to the account behind refreshToken.
func (f *FakeExternalMailSource) Deliver(refreshToken string, messages ...ExternalMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mail[refreshToken] = append(f.mail[refreshToken], messages...)
}

// FailAuth makes every later fetch with refreshToken answer ErrExternalMailAuth.
func (f *FakeExternalMailSource) FailAuth(refreshToken string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.authFail[refreshToken] = true
}

// Fetches reports how many fetches used refreshToken.
func (f *FakeExternalMailSource) Fetches(refreshToken string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fetches[refreshToken]
}

// Revoked reports whether refreshToken was revoked.
func (f *FakeExternalMailSource) Revoked(refreshToken string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.revoked[refreshToken]
}

// AuthURL serves a sign-in page address and remembers its challenge.
func (f *FakeExternalMailSource) AuthURL(request ExternalMailAuthRequest) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.challenges[request.CodeChallenge] = true
	query := url.Values{"state": {request.State}, "code_challenge": {request.CodeChallenge},
		"code_challenge_method": {"S256"}, "redirect_uri": {request.RedirectURI}}
	return "https://external-mail.invalid/authorize?" + query.Encode()
}

// Exchange grants the account "<code>@external.invalid" with refresh token "fake-<code>", when the
// verifier answers a challenge this source served. The code "refuse" is refused.
func (f *FakeExternalMailSource) Exchange(_ context.Context, exchange ExternalMailCodeExchange) (ExternalMailToken, ExternalMailAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if exchange.Code == "" || exchange.Code == "refuse" || !f.challenges[pkceChallenge(exchange.CodeVerifier)] {
		return ExternalMailToken{}, ExternalMailAccount{}, ErrExternalMailAuth
	}
	token := "fake-" + exchange.Code
	delete(f.revoked, token)
	delete(f.authFail, token)
	return ExternalMailToken{RefreshToken: token}, ExternalMailAccount{Address: exchange.Code + "@external.invalid"}, nil
}

// RotateOnNextFetch makes the next fetch with token hand out next as the rotated grant; token
// stops working afterwards, as a rotated refresh token does.
func (f *FakeExternalMailSource) RotateOnNextFetch(token, next string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rotations[token] = next
}

// ExpireCursor makes the next fetch with token and a cursor answer ErrExternalMailCursorExpired.
func (f *FakeExternalMailSource) ExpireCursor(token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.expired[f.account(token)] = true
}

// LastRequest returns the last fetch request made for the account behind token.
func (f *FakeExternalMailSource) LastRequest(token string) ExternalMailFetchRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[f.account(token)]
}

// account resolves a possibly rotated token to the account's first token, which keys its mail.
func (f *FakeExternalMailSource) account(token string) string {
	if first, rotated := f.aliases[token]; rotated {
		return first
	}
	return token
}

func (f *FakeExternalMailSource) Fetch(_ context.Context, request ExternalMailFetchRequest) (ExternalMailFetch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	token := request.Token.RefreshToken
	if token == "" {
		// A tenant fetch reads with the token minted for its one mailbox.
		token = request.Token.AccessToken
	}
	account := f.account(token)
	f.fetches[account]++
	f.requests[account] = request
	if f.spent[token] || f.authFail[token] || f.revoked[token] || f.authFail[account] || f.revoked[account] {
		return ExternalMailFetch{}, ErrExternalMailAuth
	}
	if request.Cursor != "" && f.expired[account] {
		delete(f.expired, account)
		return ExternalMailFetch{}, ErrExternalMailCursorExpired
	}
	start := 0
	if request.Cursor != "" {
		parsed, err := strconv.Atoi(request.Cursor)
		if err != nil || parsed < 0 {
			return ExternalMailFetch{}, errors.New("fake external mail: invalid cursor")
		}
		start = parsed
	}
	all := f.mail[account]
	start = min(start, len(all))
	end := len(all)
	if f.PageSize > 0 {
		end = min(end, start+f.PageSize)
	}
	fetched := ExternalMailFetch{
		Messages: append([]ExternalMessage(nil), all[start:end]...), Cursor: strconv.Itoa(end), More: end < len(all),
	}
	if next, rotate := f.rotations[token]; rotate {
		delete(f.rotations, token)
		f.aliases[next] = account
		// The old token is spent once a new one is issued.
		f.spent[token] = true
		fetched.RefreshToken = next
	}
	return fetched, nil
}

func (f *FakeExternalMailSource) Revoke(_ context.Context, token ExternalMailToken) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked[f.account(token.RefreshToken)] = true
	f.revoked[token.RefreshToken] = true
	return nil
}
