package mailbox

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

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

// ExternalMailSource reads an outside mailbox. Real Gmail (gmail.readonly) and Outlook (Graph
// Mail.Read) sources are added once their OAuth applications exist.
type ExternalMailSource interface {
	AuthURL(state string) string
	Exchange(ctx context.Context, code string) (ExternalMailToken, ExternalMailAccount, error)
	// Fetch returns the mail after cursor; an empty cursor starts the backfill. It returns
	// ErrExternalMailAuth when the provider refuses the token.
	Fetch(ctx context.Context, token ExternalMailToken, cursor string) (ExternalMailFetch, error)
	Revoke(ctx context.Context, token ExternalMailToken) error
}

// newExternalMailSource builds the source the configuration names.
func newExternalMailSource(cfg config.ExternalMailConfig) ExternalMailSource {
	if cfg.Source() == config.ExternalMailSourceFake {
		return NewFakeExternalMailSource()
	}
	return disabledExternalMailSource{}
}

// disabledExternalMailSource is the default: it never reaches a provider.
type disabledExternalMailSource struct{}

func (disabledExternalMailSource) AuthURL(string) string { return "" }

func (disabledExternalMailSource) Exchange(context.Context, string) (ExternalMailToken, ExternalMailAccount, error) {
	return ExternalMailToken{}, ExternalMailAccount{}, ErrExternalMailUnavailable
}

func (disabledExternalMailSource) Fetch(context.Context, ExternalMailToken, string) (ExternalMailFetch, error) {
	return ExternalMailFetch{}, ErrExternalMailUnavailable
}

func (disabledExternalMailSource) Revoke(context.Context, ExternalMailToken) error {
	return ErrExternalMailUnavailable
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
}

var _ ExternalMailSource = (*FakeExternalMailSource)(nil)

// NewFakeExternalMailSource returns an empty fake source.
func NewFakeExternalMailSource() *FakeExternalMailSource {
	return &FakeExternalMailSource{
		mail: map[string][]ExternalMessage{}, authFail: map[string]bool{},
		fetches: map[string]int{}, revoked: map[string]bool{},
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

func (f *FakeExternalMailSource) AuthURL(state string) string {
	return "https://external-mail.invalid/authorize?state=" + state
}

func (f *FakeExternalMailSource) Exchange(_ context.Context, code string) (ExternalMailToken, ExternalMailAccount, error) {
	if code == "" {
		return ExternalMailToken{}, ExternalMailAccount{}, ErrExternalMailAuth
	}
	return ExternalMailToken{RefreshToken: "fake-" + code}, ExternalMailAccount{Address: code + "@external.invalid"}, nil
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
