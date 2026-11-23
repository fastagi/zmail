package user

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	capcrypto "github.com/fastagi/zmail/api/internal/capabilities/crypto"
)

func googleKeySetServer(t *testing.T, key *rsa.PrivateKey, fetches *atomic.Int32) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{googleTestJWK(key, googleTestKeyID)}})
	}))
	t.Cleanup(server.Close)
	return server
}

func TestGoogleIDTokenVerification(t *testing.T) {
	key := googleSigningKey(t)
	var fetches atomic.Int32
	server := googleKeySetServer(t, key, &fetches)
	now := time.Unix(1_900_000_000, 0)
	client := newGoogleOIDCClient(googleTestClientID, googleTestClientSecret)
	client.keys.url = server.URL
	client.now = func() time.Time { return now }
	nonce := "nonce-value"
	valid := func() map[string]any {
		return map[string]any{
			"iss": "https://accounts.google.com", "aud": googleTestClientID, "azp": googleTestClientID,
			"sub": "1234", "email": "Person@Example.com", "email_verified": true, "name": "Person",
			"nonce": nonce, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
		}
	}
	verify := func(token string) (*googleIDClaims, error) {
		return client.verifyIDToken(context.Background(), token, capcrypto.SHA256Hex(nonce))
	}

	claims, err := verify(signGoogleTestToken(t, key, googleTestKeyID, valid()))
	require.NoError(t, err)
	assert.Equal(t, "1234", claims.Subject)
	assert.Equal(t, "person@example.com", claims.normalizedEmail())
	assert.True(t, claims.verified())

	legacyIssuer := valid()
	legacyIssuer["iss"] = "accounts.google.com"
	_, err = verify(signGoogleTestToken(t, key, googleTestKeyID, legacyIssuer))
	assert.NoError(t, err, "Google also issues tokens without the scheme")

	stringVerified := valid()
	stringVerified["email_verified"] = "true"
	claims, err = verify(signGoogleTestToken(t, key, googleTestKeyID, stringVerified))
	require.NoError(t, err)
	assert.True(t, claims.verified(), "older tokens send the flag as a string")

	refused := map[string]func(map[string]any){
		"wrong issuer":      func(c map[string]any) { c["iss"] = "https://evil.example" },
		"wrong audience":    func(c map[string]any) { c["aud"] = "other.apps.googleusercontent.com" },
		"wrong azp":         func(c map[string]any) { c["azp"] = "other.apps.googleusercontent.com" },
		"expired":           func(c map[string]any) { c["exp"] = now.Add(-time.Hour).Unix() },
		"no expiry":         func(c map[string]any) { delete(c, "exp") },
		"issued in future":  func(c map[string]any) { c["iat"] = now.Add(time.Hour).Unix() },
		"no subject":        func(c map[string]any) { c["sub"] = "" },
		"subject too long":  func(c map[string]any) { c["sub"] = strings.Repeat("1", 256) },
		"audience as list":  func(c map[string]any) { c["aud"] = []string{"other", "another"} },
		"email too long":    func(c map[string]any) { c["email"] = strings.Repeat("a", 95) + "@example.com" },
		"email not address": func(c map[string]any) { c["email"] = "not-an-address" },
	}
	for name, mutate := range refused {
		claims := valid()
		mutate(claims)
		_, refusal := verify(signGoogleTestToken(t, key, googleTestKeyID, claims))
		assert.ErrorIs(t, refusal, errGoogleIDTokenInvalid, name)
	}

	wrongNonce := valid()
	wrongNonce["nonce"] = "other"
	_, err = verify(signGoogleTestToken(t, key, googleTestKeyID, wrongNonce))
	assert.ErrorIs(t, err, errGoogleNonceMismatch)

	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	_, err = verify(signGoogleTestToken(t, otherKey, googleTestKeyID, valid()))
	assert.ErrorIs(t, err, errGoogleIDTokenInvalid, "a signature from another key")

	good := signGoogleTestToken(t, key, googleTestKeyID, valid())
	parts := strings.Split(good, ".")
	forged, _ := json.Marshal(map[string]any{"iss": "https://accounts.google.com", "aud": googleTestClientID, "sub": "admin",
		"email": "admin@example.com", "email_verified": true, "nonce": nonce, "exp": now.Add(time.Hour).Unix()})
	_, err = verify(parts[0] + "." + base64.RawURLEncoding.EncodeToString(forged) + "." + parts[2])
	assert.ErrorIs(t, err, errGoogleIDTokenInvalid, "a payload swapped under a valid signature")

	none, _ := json.Marshal(map[string]string{"alg": "none", "kid": googleTestKeyID})
	_, err = verify(base64.RawURLEncoding.EncodeToString(none) + "." + parts[1] + ".")
	assert.ErrorIs(t, err, errGoogleIDTokenInvalid, "alg none")

	before := fetches.Load()
	for range 5 {
		_, err = verify(signGoogleTestToken(t, key, "unknown-key", valid()))
		assert.True(t, errors.Is(err, errGoogleIDTokenInvalid))
	}
	assert.LessOrEqual(t, fetches.Load()-before, int32(1), "unknown key ids refetch at most once per spacing")
}
