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
)

// ExternalMailToken is the credential a source fetches with.
type ExternalMailToken struct {
	RefreshToken string
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

// ExternalMailFetch is one page of new mail and the cursor after it.
type ExternalMailFetch struct {
	Messages []ExternalMessage
	Cursor   string
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
	// Fetch returns the mail after cursor; an empty cursor starts the backfill. It returns
	// ErrExternalMailAuth when the provider refuses the token.
	Fetch(ctx context.Context, token ExternalMailToken, cursor string) (ExternalMailFetch, error)
	Revoke(ctx context.Context, token ExternalMailToken) error
}

// ExternalMailAuthKind is how a member grants a connector access.
type ExternalMailAuthKind string

// ExternalMailAuthOAuth2 is the OAuth 2.0 authorization code flow with PKCE.
const ExternalMailAuthOAuth2 ExternalMailAuthKind = "oauth2"

// ExternalMailCapabilities says what a connector can do with a bound account.
type ExternalMailCapabilities struct {
	// Read syncs the account's mail into its external mailbox.
	Read bool
}

// ExternalMailConnector is one provider the instance can bind: its id, how it authorizes, what it
// can do, and the source that speaks its protocol.
type ExternalMailConnector struct {
	ID           domain.ExternalMailProvider
	AuthKind     ExternalMailAuthKind
	Capabilities ExternalMailCapabilities
	Source       ExternalMailSource
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
	return &externalMailRegistry{}
}

// fakeExternalMailRegistry registers one fake source under the Gmail and Outlook ids.
func fakeExternalMailRegistry(source ExternalMailSource) *externalMailRegistry {
	read := ExternalMailCapabilities{Read: true}
	return &externalMailRegistry{connectors: []ExternalMailConnector{
		{ID: domain.ExternalMailProviderGmail, AuthKind: ExternalMailAuthOAuth2, Capabilities: read, Source: source},
		{ID: domain.ExternalMailProviderOutlook, AuthKind: ExternalMailAuthOAuth2, Capabilities: read, Source: source},
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
}

var _ ExternalMailSource = (*FakeExternalMailSource)(nil)

// NewFakeExternalMailSource returns an empty fake source.
func NewFakeExternalMailSource() *FakeExternalMailSource {
	return &FakeExternalMailSource{
		mail: map[string][]ExternalMessage{}, authFail: map[string]bool{},
		fetches: map[string]int{}, revoked: map[string]bool{}, challenges: map[string]bool{},
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

func (f *FakeExternalMailSource) Fetch(_ context.Context, token ExternalMailToken, cursor string) (ExternalMailFetch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetches[token.RefreshToken]++
	if f.authFail[token.RefreshToken] || f.revoked[token.RefreshToken] {
		return ExternalMailFetch{}, ErrExternalMailAuth
	}
	start := 0
	if cursor != "" {
		parsed, err := strconv.Atoi(cursor)
		if err != nil || parsed < 0 {
			return ExternalMailFetch{}, errors.New("fake external mail: invalid cursor")
		}
		start = parsed
	}
	all := f.mail[token.RefreshToken]
	start = min(start, len(all))
	page := append([]ExternalMessage(nil), all[start:]...)
	return ExternalMailFetch{Messages: page, Cursor: strconv.Itoa(len(all))}, nil
}

func (f *FakeExternalMailSource) Revoke(_ context.Context, token ExternalMailToken) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked[token.RefreshToken] = true
	return nil
}
