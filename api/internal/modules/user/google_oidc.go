package user

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"sync"
	"time"

	capcrypto "github.com/fastagi/zmail/api/internal/capabilities/crypto"
)

// "Sign in with Google" talks to Google with the standard library only, like the Gmail connector
// and the Gmail push verifier: the authorization code is exchanged with PKCE and the client secret,
// and the ID token is checked against Google's published RS256 keys, the issuer, the audience, the
// authorized party, the expiry, and the nonce. Tokens, codes, and secrets are never logged.

const (
	googleAuthEndpoint  = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenEndpoint = "https://oauth2.googleapis.com/token"
	googleJWKSURL       = "https://www.googleapis.com/oauth2/v3/certs"
	googleSignInScope   = "openid email profile"

	googleRequestTimeout = 10 * time.Second
	// googleMaxTokenResponseBytes bounds the token endpoint answer; googleMaxIDTokenBytes the token.
	googleMaxTokenResponseBytes = 64 << 10
	googleMaxIDTokenBytes       = 8 << 10
	// googleClockSkew tolerates clocks that differ a little from Google's.
	googleClockSkew = time.Minute
	// googleMaxSubjectLength matches user_identities.subject; googleMaxEmailLength users.email.
	googleMaxSubjectLength = 255
	googleMaxEmailLength   = 100

	// Key set bounds and caching: a fetched set is used for an hour, at most one fetch runs per
	// minute (so tokens naming unknown keys cannot make Zmail fetch on every request), and an
	// expired set is still used for a day while Google cannot be reached.
	googleJWKSMaxBytes        = 64 << 10
	googleJWKSMaxKeys         = 32
	googleJWKSTTL             = time.Hour
	googleJWKSRefetchSpacing  = time.Minute
	googleJWKSStaleLimit      = 24 * time.Hour
	googleJWKSMinKeyBits      = 2048
	googleJWKSMaxKeyBits      = 8192
	googleJWKSMaxKeyIDLength  = 128
	googleMaxAudienceListSize = 8
)

// googleIssuers are the issuers of Google-signed ID tokens.
var googleIssuers = map[string]bool{"accounts.google.com": true, "https://accounts.google.com": true}

var (
	// errGoogleExchangeFailed means Google refused the code or answered unusably.
	errGoogleExchangeFailed = errors.New("google sign-in: code exchange failed")
	// errGoogleIDTokenInvalid means the ID token failed a signature or claim check.
	errGoogleIDTokenInvalid = errors.New("google sign-in: id token invalid")
	// errGoogleNonceMismatch means the ID token was not issued for this sign-in.
	errGoogleNonceMismatch = errors.New("google sign-in: nonce mismatch")
)

// googleOIDCClient is the OAuth client of "Sign in with Google". Endpoints are fields so tests point
// them at a local fake provider.
type googleOIDCClient struct {
	clientID      string
	clientSecret  string
	authEndpoint  string
	tokenEndpoint string
	httpClient    *http.Client
	keys          *googleKeySet
	now           func() time.Time
}

func newGoogleOIDCClient(clientID, clientSecret string) *googleOIDCClient {
	httpClient := &http.Client{Timeout: googleRequestTimeout}
	return &googleOIDCClient{
		clientID:      strings.TrimSpace(clientID),
		clientSecret:  strings.TrimSpace(clientSecret),
		authEndpoint:  googleAuthEndpoint,
		tokenEndpoint: googleTokenEndpoint,
		httpClient:    httpClient,
		keys:          &googleKeySet{url: googleJWKSURL, client: httpClient},
		now:           time.Now,
	}
}

// authURL is Google's account chooser and consent page for this sign-in.
func (g *googleOIDCClient) authURL(state, nonce, codeChallenge, redirectURI string) string {
	query := url.Values{
		"client_id":             {g.clientID},
		"redirect_uri":          {redirectURI},
		"response_type":         {"code"},
		"scope":                 {googleSignInScope},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
		"prompt":                {"select_account"},
	}
	return g.authEndpoint + "?" + query.Encode()
}

// exchange trades the authorization code for the ID token.
func (g *googleOIDCClient) exchange(ctx context.Context, code, codeVerifier, redirectURI string) (string, error) {
	form := url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {codeVerifier},
		"redirect_uri": {redirectURI}, "client_id": {g.clientID}, "client_secret": {g.clientSecret},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, g.tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", errGoogleExchangeFailed
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := g.httpClient.Do(request)
	if err != nil {
		slog.WarnContext(ctx, "user.google_sign_in_exchange_unreachable", "error_type", fmt.Sprintf("%T", err))
		return "", errGoogleExchangeFailed
	}
	defer response.Body.Close()
	body, err := readGoogleBounded(response.Body, googleMaxTokenResponseBytes)
	if err != nil {
		return "", errGoogleExchangeFailed
	}
	var answer struct {
		IDToken string `json:"id_token"`
		Error   string `json:"error"`
	}
	decodeErr := json.Unmarshal(body, &answer)
	if response.StatusCode != http.StatusOK || decodeErr != nil || answer.IDToken == "" {
		// Google's error is a fixed word such as invalid_grant; the description is not logged.
		reason := answer.Error
		if len(reason) > 32 {
			reason = reason[:32]
		}
		slog.WarnContext(ctx, "user.google_sign_in_exchange_refused", "status", response.StatusCode, "reason", reason)
		return "", errGoogleExchangeFailed
	}
	return answer.IDToken, nil
}

// googleIDClaims are the ID token claims sign-in reads.
type googleIDClaims struct {
	Issuer          string          `json:"iss"`
	Audience        json.RawMessage `json:"aud"`
	AuthorizedParty string          `json:"azp"`
	Subject         string          `json:"sub"`
	Email           string          `json:"email"`
	EmailVerified   json.RawMessage `json:"email_verified"`
	Name            string          `json:"name"`
	Nonce           string          `json:"nonce"`
	ExpiresAt       int64           `json:"exp"`
	IssuedAt        int64           `json:"iat"`
}

// audiences reads aud as one string or a short list of strings.
func (c googleIDClaims) audiences() []string {
	var single string
	if json.Unmarshal(c.Audience, &single) == nil {
		return []string{single}
	}
	var list []string
	if json.Unmarshal(c.Audience, &list) == nil && len(list) <= googleMaxAudienceListSize {
		return list
	}
	return nil
}

// verified reads email_verified, which Google sends as a boolean (older tokens: a string).
func (c googleIDClaims) verified() bool {
	var flag bool
	if json.Unmarshal(c.EmailVerified, &flag) == nil {
		return flag
	}
	var text string
	return json.Unmarshal(c.EmailVerified, &text) == nil && text == "true"
}

func (c googleIDClaims) normalizedEmail() string {
	return strings.ToLower(strings.TrimSpace(c.Email))
}

// verifyIDToken checks a compact JWS ID token. nonceHash is the SHA-256 hex of the nonce this
// sign-in sent to Google. Every failure is errGoogleIDTokenInvalid except a wrong nonce.
func (g *googleOIDCClient) verifyIDToken(ctx context.Context, token, nonceHash string) (*googleIDClaims, error) {
	if token == "" || len(token) > googleMaxIDTokenBytes {
		return nil, errGoogleIDTokenInvalid
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errGoogleIDTokenInvalid
	}
	var header struct {
		Algorithm string `json:"alg"`
		KeyID     string `json:"kid"`
	}
	if !decodeGoogleJWTPart(parts[0], &header) || header.Algorithm != "RS256" ||
		header.KeyID == "" || len(header.KeyID) > googleJWKSMaxKeyIDLength {
		return nil, errGoogleIDTokenInvalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errGoogleIDTokenInvalid
	}
	now := g.now()
	key := g.keys.key(ctx, header.KeyID, now)
	if key == nil {
		return nil, errGoogleIDTokenInvalid
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature) != nil {
		return nil, errGoogleIDTokenInvalid
	}
	// Only a token whose signature holds is read for its claims.
	var claims googleIDClaims
	if !decodeGoogleJWTPart(parts[1], &claims) {
		return nil, errGoogleIDTokenInvalid
	}
	if !googleIssuers[claims.Issuer] || !g.audienceMatches(claims) {
		return nil, errGoogleIDTokenInvalid
	}
	if claims.ExpiresAt == 0 || !now.Before(time.Unix(claims.ExpiresAt, 0).Add(googleClockSkew)) ||
		(claims.IssuedAt != 0 && time.Unix(claims.IssuedAt, 0).After(now.Add(googleClockSkew))) {
		return nil, errGoogleIDTokenInvalid
	}
	if claims.Subject == "" || len(claims.Subject) > googleMaxSubjectLength || !validGoogleEmail(claims.normalizedEmail()) {
		return nil, errGoogleIDTokenInvalid
	}
	if subtle.ConstantTimeCompare([]byte(capcrypto.SHA256Hex(claims.Nonce)), []byte(nonceHash)) != 1 {
		return nil, errGoogleNonceMismatch
	}
	return &claims, nil
}

// audienceMatches requires this client among the audiences and, when present or when there are
// several audiences, as the authorized party.
func (g *googleOIDCClient) audienceMatches(claims googleIDClaims) bool {
	audiences := claims.audiences()
	found := false
	for _, audience := range audiences {
		if audience != "" && audience == g.clientID {
			found = true
		}
	}
	if !found {
		return false
	}
	if claims.AuthorizedParty != "" || len(audiences) > 1 {
		return claims.AuthorizedParty == g.clientID
	}
	return true
}

func validGoogleEmail(email string) bool {
	if email == "" || len(email) > googleMaxEmailLength {
		return false
	}
	address, err := mail.ParseAddress(email)
	return err == nil && address.Address == email && strings.Contains(email, "@")
}

func decodeGoogleJWTPart(part string, into any) bool {
	raw, err := base64.RawURLEncoding.DecodeString(part)
	return err == nil && json.Unmarshal(raw, into) == nil
}

func readGoogleBounded(body io.Reader, limit int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, errors.New("google sign-in: response too large")
	}
	return raw, nil
}

// googleKeySet caches Google's signing keys. One fetch runs at a time; the lock that guards the
// cached keys is never held across the network.
type googleKeySet struct {
	url    string
	client *http.Client

	fetchMu     sync.Mutex
	mu          sync.Mutex
	keys        map[string]*rsa.PublicKey
	fetchedAt   time.Time
	lastAttempt time.Time
}

func (k *googleKeySet) key(ctx context.Context, keyID string, now time.Time) *rsa.PublicKey {
	if key := k.fresh(keyID, now); key != nil {
		return key
	}
	k.fetchMu.Lock()
	defer k.fetchMu.Unlock()
	// Another caller may have fetched while this one waited.
	if key := k.fresh(keyID, now); key != nil {
		return key
	}
	k.mu.Lock()
	allowed := k.lastAttempt.IsZero() || now.Sub(k.lastAttempt) >= googleJWKSRefetchSpacing
	if allowed {
		k.lastAttempt = now
	}
	k.mu.Unlock()
	if allowed {
		// A caller that hangs up must not make the shared fetch fail for everyone else.
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), googleRequestTimeout)
		keys, err := k.fetch(fetchCtx)
		cancel()
		if err != nil {
			slog.WarnContext(ctx, "user.google_sign_in_keys_unavailable", "err", err)
		} else {
			k.mu.Lock()
			k.keys, k.fetchedAt = keys, now
			k.mu.Unlock()
		}
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.keys == nil || now.Sub(k.fetchedAt) >= googleJWKSStaleLimit {
		return nil
	}
	return k.keys[keyID]
}

func (k *googleKeySet) fresh(keyID string, now time.Time) *rsa.PublicKey {
	k.mu.Lock()
	defer k.mu.Unlock()
	if key := k.keys[keyID]; key != nil && now.Sub(k.fetchedAt) < googleJWKSTTL {
		return key
	}
	return nil
}

func (k *googleKeySet) fetch(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, k.url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := k.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("google keys unreachable: %T", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google keys: HTTP %d", response.StatusCode)
	}
	body, err := readGoogleBounded(response.Body, googleJWKSMaxBytes)
	if err != nil {
		return nil, err
	}
	return parseGoogleJWKS(body)
}

// parseGoogleJWKS reads the RSA signing keys of a key set; a key outside the bounds fails the set.
func parseGoogleJWKS(body []byte) (map[string]*rsa.PublicKey, error) {
	var set struct {
		Keys []struct {
			KeyType   string `json:"kty"`
			KeyID     string `json:"kid"`
			Algorithm string `json:"alg"`
			Use       string `json:"use"`
			Modulus   string `json:"n"`
			Exponent  string `json:"e"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(body, &set); err != nil {
		return nil, errors.New("google keys: not JSON")
	}
	if len(set.Keys) == 0 || len(set.Keys) > googleJWKSMaxKeys {
		return nil, fmt.Errorf("google keys: %d keys", len(set.Keys))
	}
	keys := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, entry := range set.Keys {
		if entry.KeyType != "RSA" || entry.KeyID == "" || len(entry.KeyID) > googleJWKSMaxKeyIDLength ||
			(entry.Algorithm != "" && entry.Algorithm != "RS256") || (entry.Use != "" && entry.Use != "sig") {
			continue
		}
		modulus, errN := base64.RawURLEncoding.DecodeString(entry.Modulus)
		exponent, errE := base64.RawURLEncoding.DecodeString(entry.Exponent)
		if errN != nil || errE != nil || len(exponent) == 0 || len(exponent) > 4 {
			return nil, errors.New("google keys: malformed key")
		}
		n := new(big.Int).SetBytes(modulus)
		e := int(new(big.Int).SetBytes(exponent).Int64())
		if n.BitLen() < googleJWKSMinKeyBits || n.BitLen() > googleJWKSMaxKeyBits || e < 3 || e%2 == 0 {
			return nil, errors.New("google keys: key outside bounds")
		}
		keys[entry.KeyID] = &rsa.PublicKey{N: n, E: e}
	}
	if len(keys) == 0 {
		return nil, errors.New("google keys: no RSA signing key")
	}
	return keys, nil
}
