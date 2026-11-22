package mailbox

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Cloud Pub/Sub push requests carry an OIDC token Google signs for the push subscription's service
// account. googleOIDCVerifier checks it with the standard library only: RS256 against Google's
// published keys, the issuer, the configured audience, the expiry, and optionally the service
// account. Nothing about the token is logged or returned.

const (
	// oidcMaxTokenBytes bounds the bearer token read from a request.
	oidcMaxTokenBytes = 4096
	// oidcClockSkew tolerates clocks that differ a little from Google's.
	oidcClockSkew = 30 * time.Second
	// jwksMaxBytes and jwksMaxKeys bound the key set Zmail accepts.
	jwksMaxBytes = 64 << 10
	jwksMaxKeys  = 32
	// jwksTTL is how long a fetched key set is used; jwksRefetchSpacing is the least time between
	// two fetches, so tokens naming unknown keys cannot make Zmail fetch on every request.
	jwksTTL            = time.Hour
	jwksRefetchSpacing = time.Minute
	// jwksStaleLimit is how long an expired key set is still used while Google cannot be reached.
	jwksStaleLimit = 24 * time.Hour
	jwksTimeout    = 5 * time.Second
	// Bounds of an accepted RSA key.
	jwksMinKeyBits = 2048
	jwksMaxKeyBits = 8192
)

// googleIssuers are the issuers of Google-signed ID tokens.
var googleIssuers = map[string]bool{"accounts.google.com": true, "https://accounts.google.com": true}

// errJWKSUnusable means the key set could not be read or broke a bound.
var errJWKSUnusable = errors.New("google oidc: key set unusable")

// googleOIDCVerifier verifies the OIDC tokens of Pub/Sub push requests.
type googleOIDCVerifier struct {
	jwksURL        string
	audience       string
	serviceAccount string
	client         *http.Client

	mu          sync.Mutex
	keys        map[string]*rsa.PublicKey
	fetchedAt   time.Time
	lastAttempt time.Time
	// fetching is closed when the fetch in flight ends; nil while none runs. The lock is never held
	// across the network.
	fetching chan struct{}
}

func newGoogleOIDCVerifier(jwksURL, audience, serviceAccount string) *googleOIDCVerifier {
	return &googleOIDCVerifier{
		jwksURL: jwksURL, audience: audience, serviceAccount: strings.ToLower(serviceAccount),
		client: &http.Client{Timeout: jwksTimeout},
	}
}

// oidcClaims are the claims a push token is checked for.
type oidcClaims struct {
	Issuer        string          `json:"iss"`
	Audience      json.RawMessage `json:"aud"`
	ExpiresAt     int64           `json:"exp"`
	IssuedAt      int64           `json:"iat"`
	Email         string          `json:"email"`
	EmailVerified json.RawMessage `json:"email_verified"`
}

// audiences reads aud as one string or a list of strings.
func (c oidcClaims) audiences() []string {
	var single string
	if json.Unmarshal(c.Audience, &single) == nil {
		return []string{single}
	}
	var list []string
	if json.Unmarshal(c.Audience, &list) == nil && len(list) <= 8 {
		return list
	}
	return nil
}

// verified reads email_verified, which Google sends as a boolean (older tokens: a string).
func (c oidcClaims) verified() bool {
	var flag bool
	if json.Unmarshal(c.EmailVerified, &flag) == nil {
		return flag
	}
	var text string
	return json.Unmarshal(c.EmailVerified, &text) == nil && text == "true"
}

// Verify checks a compact JWS token at now. Any failure is ErrExternalMailPushUnauthenticated.
func (v *googleOIDCVerifier) Verify(ctx context.Context, token string, now time.Time) error {
	if token == "" || len(token) > oidcMaxTokenBytes {
		return ErrExternalMailPushUnauthenticated
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ErrExternalMailPushUnauthenticated
	}
	var header struct {
		Algorithm string `json:"alg"`
		KeyID     string `json:"kid"`
	}
	if !decodeJWTPart(parts[0], &header) || header.Algorithm != "RS256" || header.KeyID == "" || len(header.KeyID) > 128 {
		return ErrExternalMailPushUnauthenticated
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return ErrExternalMailPushUnauthenticated
	}
	key := v.key(ctx, header.KeyID, now)
	if key == nil {
		return ErrExternalMailPushUnauthenticated
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature) != nil {
		return ErrExternalMailPushUnauthenticated
	}
	// Only a token whose signature holds is read for its claims.
	var claims oidcClaims
	if !decodeJWTPart(parts[1], &claims) {
		return ErrExternalMailPushUnauthenticated
	}
	if !googleIssuers[claims.Issuer] || !v.audienceMatches(claims.audiences()) {
		return ErrExternalMailPushUnauthenticated
	}
	if claims.ExpiresAt == 0 || !now.Before(time.Unix(claims.ExpiresAt, 0).Add(oidcClockSkew)) ||
		(claims.IssuedAt != 0 && time.Unix(claims.IssuedAt, 0).After(now.Add(oidcClockSkew))) {
		return ErrExternalMailPushUnauthenticated
	}
	// The service account is required: without it any Google-signed token for the audience would pass.
	if v.serviceAccount == "" || !strings.EqualFold(claims.Email, v.serviceAccount) || !claims.verified() {
		return ErrExternalMailPushUnauthenticated
	}
	return nil
}

func (v *googleOIDCVerifier) audienceMatches(audiences []string) bool {
	for _, audience := range audiences {
		if audience != "" && audience == v.audience {
			return true
		}
	}
	return false
}

// decodeJWTPart decodes one base64url JSON part of a token.
func decodeJWTPart(part string, into any) bool {
	raw, err := base64.RawURLEncoding.DecodeString(part)
	return err == nil && json.Unmarshal(raw, into) == nil
}

// key returns the signing key with the id, fetching the key set when it is stale or lacks the id,
// at most once per jwksRefetchSpacing and by one caller at a time: the others wait for that fetch
// (within the key set timeout) instead of starting their own. A key set Google cannot serve is used
// stale for a while.
func (v *googleOIDCVerifier) key(ctx context.Context, keyID string, now time.Time) *rsa.PublicKey {
	v.mu.Lock()
	if key := v.keys[keyID]; key != nil && now.Sub(v.fetchedAt) < jwksTTL {
		v.mu.Unlock()
		return key
	}
	wait := v.fetching
	if wait == nil && (v.lastAttempt.IsZero() || now.Sub(v.lastAttempt) >= jwksRefetchSpacing) {
		v.lastAttempt = now
		done := make(chan struct{})
		v.fetching = done
		v.mu.Unlock()
		// A caller that hangs up must not make the shared fetch fail for everyone else.
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), jwksTimeout)
		keys, err := v.fetch(fetchCtx)
		cancel()
		v.mu.Lock()
		if err == nil {
			v.keys, v.fetchedAt = keys, now
		}
		v.fetching = nil
		close(done)
	} else if wait != nil {
		v.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
		case <-time.After(jwksTimeout):
		}
		v.mu.Lock()
	}
	defer v.mu.Unlock()
	if v.keys == nil || now.Sub(v.fetchedAt) >= jwksStaleLimit {
		return nil
	}
	return v.keys[keyID]
}

// fetch reads and checks the key set.
func (v *googleOIDCVerifier) fetch(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := v.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errJWKSUnusable, redactURLError(err))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: HTTP %d", errJWKSUnusable, response.StatusCode)
	}
	body, err := readBounded(response.Body, jwksMaxBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errJWKSUnusable, err)
	}
	return parseJWKS(body)
}

// parseJWKS reads the RSA signing keys of a key set; any key outside the bounds fails the whole set.
func parseJWKS(body []byte) (map[string]*rsa.PublicKey, error) {
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
		return nil, fmt.Errorf("%w: not JSON", errJWKSUnusable)
	}
	if len(set.Keys) == 0 || len(set.Keys) > jwksMaxKeys {
		return nil, fmt.Errorf("%w: %d keys", errJWKSUnusable, len(set.Keys))
	}
	keys := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, entry := range set.Keys {
		if entry.KeyType != "RSA" || entry.KeyID == "" || len(entry.KeyID) > 128 ||
			(entry.Algorithm != "" && entry.Algorithm != "RS256") || (entry.Use != "" && entry.Use != "sig") {
			continue
		}
		modulus, errN := base64.RawURLEncoding.DecodeString(entry.Modulus)
		exponent, errE := base64.RawURLEncoding.DecodeString(entry.Exponent)
		if errN != nil || errE != nil || len(exponent) == 0 || len(exponent) > 4 {
			return nil, fmt.Errorf("%w: malformed key", errJWKSUnusable)
		}
		n := new(big.Int).SetBytes(modulus)
		e := int(new(big.Int).SetBytes(exponent).Int64())
		if n.BitLen() < jwksMinKeyBits || n.BitLen() > jwksMaxKeyBits || e < 3 || e%2 == 0 {
			return nil, fmt.Errorf("%w: key outside bounds", errJWKSUnusable)
		}
		keys[entry.KeyID] = &rsa.PublicKey{N: n, E: e}
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("%w: no RSA signing key", errJWKSUnusable)
	}
	return keys, nil
}
