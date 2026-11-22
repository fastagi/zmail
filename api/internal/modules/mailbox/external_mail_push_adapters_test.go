package mailbox

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
)

var pushTestNow = time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

const (
	pushTestAudience       = "https://zmail.example/v1/external-mail-notifications/gmail"
	pushTestServiceAccount = "gmail-push@zmail-project.iam.gserviceaccount.com"
)

// pushTestKeys is a fake Google key server: it serves the public keys of the RSA keys it holds
// and counts its fetches.
type pushTestKeys struct {
	server  *httptest.Server
	keys    map[string]*rsa.PrivateKey
	fetches atomic.Int32
	// body replaces the served key set when set.
	body []byte
}

var (
	pushTestKeyOnce sync.Once
	pushTestKeyA    *rsa.PrivateKey
	pushTestKeyB    *rsa.PrivateKey
)

func pushTestRSAKeys(t *testing.T) (*rsa.PrivateKey, *rsa.PrivateKey) {
	t.Helper()
	pushTestKeyOnce.Do(func() {
		var err error
		if pushTestKeyA, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
			panic(err)
		}
		if pushTestKeyB, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
			panic(err)
		}
	})
	return pushTestKeyA, pushTestKeyB
}

func newPushTestKeys(t *testing.T) *pushTestKeys {
	t.Helper()
	keyA, _ := pushTestRSAKeys(t)
	k := &pushTestKeys{keys: map[string]*rsa.PrivateKey{"key-a": keyA}}
	k.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		k.fetches.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if k.body != nil {
			_, _ = w.Write(k.body)
			return
		}
		_, _ = w.Write(jwksOf(k.keys))
	}))
	t.Cleanup(k.server.Close)
	return k
}

func jwksOf(keys map[string]*rsa.PrivateKey) []byte {
	entries := []map[string]string{}
	for id, key := range keys {
		entries = append(entries, map[string]string{
			"kty": "RSA", "kid": id, "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		})
	}
	body, _ := json.Marshal(map[string]any{"keys": entries})
	return body
}

// signPushToken signs claims as Google signs a push subscription's OIDC token.
func signPushToken(t *testing.T, key *rsa.PrivateKey, keyID, algorithm string, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": algorithm, "kid": keyID, "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	signing := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(signing))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(signature)
}

// goodPushClaims are the claims of a valid token at pushTestNow.
func goodPushClaims() map[string]any {
	return map[string]any{
		"iss": "https://accounts.google.com", "aud": pushTestAudience, "email": pushTestServiceAccount,
		"email_verified": true, "iat": pushTestNow.Add(-time.Minute).Unix(), "exp": pushTestNow.Add(59 * time.Minute).Unix(),
		"sub": "1234567890",
	}
}

func withClaim(claims map[string]any, key string, value any) map[string]any {
	copied := map[string]any{}
	for k, v := range claims {
		copied[k] = v
	}
	if value == nil {
		delete(copied, key)
	} else {
		copied[key] = value
	}
	return copied
}

// TestGoogleOIDCVerifier: only an RS256 token signed by a published key, from Google, for the
// configured audience and service account, and unexpired, passes. Each refused token differs from
// the accepted one in one respect only, and each refusal is shown to depend on its check: with
// that check satisfied the same kind of token passes.
func TestGoogleOIDCVerifier(t *testing.T) {
	keyA, keyB := pushTestRSAKeys(t)
	keys := newPushTestKeys(t)
	verifier := newGoogleOIDCVerifier(keys.server.URL, pushTestAudience, pushTestServiceAccount)
	ctx := context.Background()
	good := signPushToken(t, keyA, "key-a", "RS256", goodPushClaims())
	if err := verifier.Verify(ctx, good, pushTestNow); err != nil {
		t.Fatalf("a valid token was refused: %v", err)
	}
	tampered := strings.Split(good, ".")
	forged := tampered[0] + "." + base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"https://accounts.google.com","aud":"`+pushTestAudience+`","exp":9999999999}`)) + "." + tampered[2]
	refused := map[string]string{
		"wrong audience":        signPushToken(t, keyA, "key-a", "RS256", withClaim(goodPushClaims(), "aud", "https://attacker.example/")),
		"audience list without": signPushToken(t, keyA, "key-a", "RS256", withClaim(goodPushClaims(), "aud", []string{"https://attacker.example/"})),
		"wrong issuer":          signPushToken(t, keyA, "key-a", "RS256", withClaim(goodPushClaims(), "iss", "https://evil.example")),
		"expired": signPushToken(t, keyA, "key-a", "RS256",
			withClaim(withClaim(goodPushClaims(), "exp", pushTestNow.Add(-time.Hour).Unix()), "iat", pushTestNow.Add(-2*time.Hour).Unix())),
		"no expiry":              signPushToken(t, keyA, "key-a", "RS256", withClaim(goodPushClaims(), "exp", nil)),
		"issued in the future":   signPushToken(t, keyA, "key-a", "RS256", withClaim(goodPushClaims(), "iat", pushTestNow.Add(time.Hour).Unix())),
		"other service account":  signPushToken(t, keyA, "key-a", "RS256", withClaim(goodPushClaims(), "email", "someone@attacker.example")),
		"unverified email":       signPushToken(t, keyA, "key-a", "RS256", withClaim(goodPushClaims(), "email_verified", false)),
		"bad signature":          signPushToken(t, keyB, "key-a", "RS256", goodPushClaims()),
		"forged claims":          forged,
		"unknown key id":         signPushToken(t, keyB, "key-b", "RS256", goodPushClaims()),
		"wrong algorithm":        signPushToken(t, keyA, "key-a", "RS512", goodPushClaims()),
		"algorithm none":         tampered[0][:0] + base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","kid":"key-a"}`)) + "." + tampered[1] + ".",
		"not a token":            "not-a-token",
		"empty":                  "",
		"oversized":              strings.Repeat("a", oidcMaxTokenBytes+1),
		"signature not base64":   tampered[0] + "." + tampered[1] + ".***",
		"two parts":              tampered[0] + "." + tampered[1],
		"unpadded audience bait": signPushToken(t, keyA, "key-a", "RS256", withClaim(goodPushClaims(), "aud", "")),
	}
	for name, token := range refused {
		if err := verifier.Verify(ctx, token, pushTestNow); !errors.Is(err, ErrExternalMailPushUnauthenticated) {
			t.Errorf("%s: Verify = %v, want unauthenticated", name, err)
		}
	}

	// Each check is what refuses its case: satisfy that one check and the case passes.
	satisfied := map[string]func() error{
		"wrong audience": func() error {
			return newGoogleOIDCVerifier(keys.server.URL, "https://attacker.example/", pushTestServiceAccount).Verify(ctx, refused["wrong audience"], pushTestNow)
		},
		"expired": func() error {
			return newGoogleOIDCVerifier(keys.server.URL, pushTestAudience, pushTestServiceAccount).Verify(ctx, refused["expired"], pushTestNow.Add(-90*time.Minute))
		},
		"other service account": func() error {
			return newGoogleOIDCVerifier(keys.server.URL, pushTestAudience, "").Verify(ctx, refused["other service account"], pushTestNow)
		},
		"unknown key id": func() error {
			keys.keys["key-b"] = keyB
			defer delete(keys.keys, "key-b")
			return newGoogleOIDCVerifier(keys.server.URL, pushTestAudience, pushTestServiceAccount).Verify(ctx, refused["unknown key id"], pushTestNow)
		},
		"bad signature": func() error {
			keys.keys["key-a"] = keyB
			defer func() { keys.keys["key-a"] = keyA }()
			return newGoogleOIDCVerifier(keys.server.URL, pushTestAudience, pushTestServiceAccount).Verify(ctx, refused["bad signature"], pushTestNow)
		},
		"wrong issuer (accounts.google.com without scheme)": func() error {
			token := signPushToken(t, keyA, "key-a", "RS256", withClaim(goodPushClaims(), "iss", "accounts.google.com"))
			return verifier.Verify(ctx, token, pushTestNow)
		},
		"audience in a list": func() error {
			token := signPushToken(t, keyA, "key-a", "RS256", withClaim(goodPushClaims(), "aud", []string{"other", pushTestAudience}))
			return verifier.Verify(ctx, token, pushTestNow)
		},
	}
	for name, check := range satisfied {
		if err := check(); err != nil {
			t.Errorf("%s: with the check satisfied the token is still refused: %v", name, err)
		}
	}
}

// TestGoogleOIDCVerifierKeySetBounds: an oversized or malformed key set, too many keys, or a weak
// key is never used; an unknown key id refetches at most once per spacing; a key set Google cannot
// serve is used stale for a while.
func TestGoogleOIDCVerifierKeySetBounds(t *testing.T) {
	keyA, _ := pushTestRSAKeys(t)
	token := signPushToken(t, keyA, "key-a", "RS256", goodPushClaims())
	ctx := context.Background()
	weak, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	many := map[string]*rsa.PrivateKey{}
	for i := 0; i <= jwksMaxKeys; i++ {
		many[string(rune('a'+i%26))+strings.Repeat("x", i)] = keyA
	}
	many["key-a"] = keyA
	oversized := jwksOf(map[string]*rsa.PrivateKey{"key-a": keyA})
	oversized = append(oversized[:len(oversized)-1], []byte(`,"padding":"`+strings.Repeat("p", jwksMaxBytes)+`"}`)...)
	for name, body := range map[string][]byte{
		"oversized":     oversized,
		"too many keys": jwksOf(many),
		"weak key":      jwksOf(map[string]*rsa.PrivateKey{"key-a": weak}),
		"not JSON":      []byte("<html>"),
		"no keys":       []byte(`{"keys":[]}`),
	} {
		keys := newPushTestKeys(t)
		keys.body = body
		verifier := newGoogleOIDCVerifier(keys.server.URL, pushTestAudience, pushTestServiceAccount)
		if err := verifier.Verify(ctx, token, pushTestNow); !errors.Is(err, ErrExternalMailPushUnauthenticated) {
			t.Errorf("%s: Verify = %v, want unauthenticated", name, err)
		}
	}

	keys := newPushTestKeys(t)
	verifier := newGoogleOIDCVerifier(keys.server.URL, pushTestAudience, pushTestServiceAccount)
	_, keyB := pushTestRSAKeys(t)
	unknown := signPushToken(t, keyB, "key-b", "RS256", goodPushClaims())
	for i := 0; i < 20; i++ {
		_ = verifier.Verify(ctx, unknown, pushTestNow.Add(time.Duration(i)*time.Second))
	}
	if got := keys.fetches.Load(); got != 1 {
		t.Fatalf("20 tokens with an unknown key id fetched the key set %d times, want 1", got)
	}
	if err := verifier.Verify(ctx, token, pushTestNow.Add(30*time.Second)); err != nil {
		t.Fatalf("a known key after an unknown one: %v", err)
	}
	keys.server.Close()
	lasting := signPushToken(t, keyA, "key-a", "RS256", withClaim(goodPushClaims(), "exp", pushTestNow.Add(72*time.Hour).Unix()))
	if err := verifier.Verify(ctx, lasting, pushTestNow.Add(2*time.Hour)); err != nil {
		t.Fatalf("a stale key set while Google is unreachable: %v", err)
	}
	if err := verifier.Verify(ctx, lasting, pushTestNow.Add(jwksStaleLimit+time.Hour)); err == nil {
		t.Fatal("a key set past the stale limit was still used")
	}
}

// fakeGmailPushServer is a Google token endpoint and Gmail API that records watch and stop calls.
type fakeGmailPushServer struct {
	server *httptest.Server
	mu     sync.Mutex
	calls  []string
	bodies []map[string]any
	auth   []string
	status int
}

func newFakeGmailPushServer(t *testing.T) *fakeGmailPushServer {
	t.Helper()
	f := &fakeGmailPushServer{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			_ = r.ParseForm()
			if r.PostForm.Get("refresh_token") == "refused" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"access-for-` + r.PostForm.Get("refresh_token") + `"}`))
		case "/gmail/v1/users/me/watch", "/gmail/v1/users/me/stop":
			f.calls = append(f.calls, r.URL.Path)
			f.auth = append(f.auth, r.Header.Get("Authorization"))
			var body map[string]any
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			f.bodies = append(f.bodies, body)
			if f.status != 0 {
				w.WriteHeader(f.status)
				_, _ = w.Write([]byte(`{"error":{"errors":[{"reason":"forbidden","domain":"global"}]}}`))
				return
			}
			if strings.HasSuffix(r.URL.Path, "/stop") {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			_, _ = w.Write([]byte(`{"historyId":"1234","expiration":"` + jsonMillis(pushTestNow.Add(7*24*time.Hour)) + `"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func jsonMillis(at time.Time) string {
	return big.NewInt(at.UnixMilli()).String()
}

func (f *fakeGmailPushServer) push(t *testing.T, jwks string) *gmailPushSource {
	t.Helper()
	mail := newGmailMailSource(config.ExternalMailOAuthClient{ClientID: "client", ClientSecret: "secret"})
	mail.tokenEndpoint = f.server.URL + "/token"
	mail.apiBase = f.server.URL + "/gmail/v1"
	return newGmailPushSource(mail, config.ExternalMailPushConfig{
		Enabled: true, GmailTopic: "projects/zmail-project/topics/gmail", GmailAudience: pushTestAudience,
		GmailServiceAccount: pushTestServiceAccount, GmailJWKSURL: jwks,
	})
}

// TestGmailPushWatchAndStop: a watch names the operator's topic and the Inbox, with the member's
// fresh token or a tenant's mailbox token as it is, and reports Google's expiry; stop calls stop;
// a refused grant is ErrExternalMailAuth.
func TestGmailPushWatchAndStop(t *testing.T) {
	google := newFakeGmailPushServer(t)
	push := google.push(t, "https://unused.invalid/certs")
	ctx := context.Background()
	got, err := push.Subscribe(ctx, ExternalMailPushRequest{
		Token: ExternalMailToken{RefreshToken: "member-grant"}, AccountAddress: " Dana@Gmail.Example ", Secret: "unused", Now: pushTestNow,
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if got.LookupKey != "dana@gmail.example" || got.SubscriptionID != "" || !got.ExpiresAt.Equal(pushTestNow.Add(7*24*time.Hour)) {
		t.Fatalf("Subscribe = %+v", got)
	}
	body := google.bodies[0]
	if body["topicName"] != "projects/zmail-project/topics/gmail" || body["labelFilterBehavior"] != "INCLUDE" ||
		google.auth[0] != "Bearer access-for-member-grant" {
		t.Fatalf("watch body %v auth %q", body, google.auth[0])
	}
	if _, err = push.Subscribe(ctx, ExternalMailPushRequest{
		Token: ExternalMailToken{AccessToken: "tenant-mailbox-token", DirectoryID: "u-1"}, AccountAddress: "x@corp.example", Now: pushTestNow,
	}); err != nil || google.auth[1] != "Bearer tenant-mailbox-token" {
		t.Fatalf("tenant watch err %v auth %q", err, google.auth[1])
	}
	if err = push.Unsubscribe(ctx, ExternalMailPushRequest{Token: ExternalMailToken{RefreshToken: "member-grant"}}); err != nil ||
		google.calls[2] != "/gmail/v1/users/me/stop" {
		t.Fatalf("Unsubscribe err %v calls %v", err, google.calls)
	}
	if _, err = push.Subscribe(ctx, ExternalMailPushRequest{Token: ExternalMailToken{RefreshToken: "refused"}, AccountAddress: "a@b.example", Now: pushTestNow}); !errors.Is(err, ErrExternalMailAuth) {
		t.Fatalf("refused grant: %v", err)
	}
	google.status = http.StatusForbidden
	if _, err = push.Subscribe(ctx, ExternalMailPushRequest{Token: ExternalMailToken{AccessToken: "t"}, AccountAddress: "a@b.example", Now: pushTestNow}); !errors.Is(err, ErrExternalMailAuth) {
		t.Fatalf("watch refused for the grant: %v", err)
	}
}

// pubsubBody is a Pub/Sub push request carrying a Gmail notification.
func pubsubBody(address string) []byte {
	data := base64.StdEncoding.EncodeToString([]byte(`{"emailAddress":"` + address + `","historyId":"9876"}`))
	body, _ := json.Marshal(map[string]any{
		"message":      map[string]any{"data": data, "messageId": "1", "publishTime": "2026-11-22T12:00:00Z"},
		"subscription": "projects/zmail-project/subscriptions/gmail-push",
	})
	return body
}

// TestGmailPushParseNotification: the token is checked before the body; an authenticated request
// names the lowercased address; an authenticated body Zmail cannot read yields no hint.
func TestGmailPushParseNotification(t *testing.T) {
	keyA, _ := pushTestRSAKeys(t)
	keys := newPushTestKeys(t)
	push := newFakeGmailPushServer(t).push(t, keys.server.URL)
	ctx := context.Background()
	token := signPushToken(t, keyA, "key-a", "RS256", goodPushClaims())
	deliver := func(authorization string, body []byte) (ExternalMailPushParsed, error) {
		header := http.Header{}
		if authorization != "" {
			header.Set("Authorization", authorization)
		}
		return push.ParseNotification(ctx, ExternalMailPushDelivery{Header: header, Query: url.Values{}, Body: body, Now: pushTestNow})
	}
	parsed, err := deliver("Bearer "+token, pubsubBody("Dana@Gmail.Example"))
	if err != nil || len(parsed.Hints) != 1 || parsed.Hints[0].Key != "dana@gmail.example" || !parsed.Hints[0].Authenticated {
		t.Fatalf("parsed %+v err %v", parsed, err)
	}
	for name, authorization := range map[string]string{
		"no header": "", "basic": "Basic " + token, "no scheme": token, "wrong token": "Bearer x.y.z",
	} {
		if _, err := deliver(authorization, pubsubBody("dana@gmail.example")); !errors.Is(err, ErrExternalMailPushUnauthenticated) {
			t.Errorf("%s: %v, want unauthenticated", name, err)
		}
	}
	for name, body := range map[string][]byte{
		"not JSON": []byte("nope"), "data not base64": []byte(`{"message":{"data":"***"}}`),
		"no address": pubsubBody(""), "not an address": pubsubBody("nobody"),
	} {
		if parsed, err := deliver("Bearer "+token, body); err != nil || len(parsed.Hints) != 0 {
			t.Errorf("%s: parsed %+v err %v, want an acknowledged request without hints", name, parsed, err)
		}
	}
}

// fakeGraphPush is a Graph subscriptions endpoint.
type fakeGraphPush struct {
	server *httptest.Server
	mu     sync.Mutex
	known  map[string]bool
	calls  []string
	bodies []map[string]any
	auth   []string
	next   int
}

func newFakeGraphPush(t *testing.T) *fakeGraphPush {
	t.Helper()
	f := &fakeGraphPush{known: map[string]bool{}}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/oauth2/v2.0/token") {
			_ = r.ParseForm()
			_, _ = w.Write([]byte(`{"access_token":"access-` + r.PostForm.Get("refresh_token") + `","refresh_token":"rotated-` + r.PostForm.Get("refresh_token") + `"}`))
			return
		}
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		f.bodies = append(f.bodies, body)
		id := strings.TrimPrefix(r.URL.Path, "/v1.0/subscriptions/")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1.0/subscriptions":
			f.next++
			created := "1f0e2c3a-0000-4000-8000-00000000000" + string(rune('0'+f.next))
			f.known[created] = true
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": created, "expirationDateTime": body["expirationDateTime"]})
		case r.Method == http.MethodPatch && f.known[id]:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "expirationDateTime": body["expirationDateTime"]})
		case r.Method == http.MethodDelete && f.known[id]:
			delete(f.known, id)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"ResourceNotFound"}}`))
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeGraphPush) push() *outlookPushSource {
	mail := newOutlookMailSource(config.ExternalMailOAuthClient{ClientID: "client", ClientSecret: "secret"})
	mail.loginBaseURL = f.server.URL + "/login"
	mail.graphBaseURL = f.server.URL + "/v1.0"
	return newOutlookPushSource(mail, config.ExternalMailPushConfig{Enabled: true, OutlookNotificationURL: "https://zmail.example/v1/external-mail-notifications/outlook"})
}

// TestOutlookPushSubscribeRenewAndStop: a new subscription watches the Inbox's created messages with
// the clientState and both notification URLs; a renewal patches the expiry; a subscription Graph no
// longer knows is created again; a tenant mailbox is addressed by its directory id; stop deletes it;
// the rotated grant is handed back.
func TestOutlookPushSubscribeRenewAndStop(t *testing.T) {
	graph := newFakeGraphPush(t)
	push := graph.push()
	ctx := context.Background()
	created, err := push.Subscribe(ctx, ExternalMailPushRequest{
		Token: ExternalMailToken{RefreshToken: "rt-1"}, AccountAddress: "dana@contoso.example", Secret: "client-state-1", Now: pushTestNow,
	})
	if err != nil || !created.Created || created.LookupKey != created.SubscriptionID || created.RefreshToken != "rotated-rt-1" ||
		!created.ExpiresAt.Equal(pushTestNow.Add(outlookPushLife)) {
		t.Fatalf("created %+v err %v", created, err)
	}
	body := graph.bodies[0]
	if body["changeType"] != "created" || body["resource"] != "me/mailFolders('inbox')/messages" || body["clientState"] != "client-state-1" ||
		body["notificationUrl"] != "https://zmail.example/v1/external-mail-notifications/outlook" || body["lifecycleNotificationUrl"] != body["notificationUrl"] {
		t.Fatalf("create body %v", body)
	}
	renewed, err := push.Subscribe(ctx, ExternalMailPushRequest{
		Token: ExternalMailToken{RefreshToken: "rotated-rt-1"}, SubscriptionID: created.SubscriptionID, Secret: "unused", Now: pushTestNow.Add(time.Hour),
	})
	if err != nil || renewed.Created || renewed.SubscriptionID != created.SubscriptionID || graph.calls[1] != "PATCH /v1.0/subscriptions/"+created.SubscriptionID {
		t.Fatalf("renewed %+v err %v calls %v", renewed, err, graph.calls)
	}
	recreated, err := push.Subscribe(ctx, ExternalMailPushRequest{
		Token: ExternalMailToken{AccessToken: "app-token", DirectoryID: "user-42"}, AccountAddress: "eve@contoso.example",
		SubscriptionID: "00000000-dead-4000-8000-000000000000", Secret: "client-state-2", Now: pushTestNow,
	})
	if err != nil || !recreated.Created || recreated.SubscriptionID == created.SubscriptionID {
		t.Fatalf("recreated %+v err %v", recreated, err)
	}
	if last := graph.bodies[len(graph.bodies)-1]; last["resource"] != "users/user-42/mailFolders('inbox')/messages" || graph.auth[len(graph.auth)-1] != "Bearer app-token" {
		t.Fatalf("tenant subscription body %v auth %q", last, graph.auth[len(graph.auth)-1])
	}
	if err = push.Unsubscribe(ctx, ExternalMailPushRequest{Token: ExternalMailToken{AccessToken: "app-token"}, SubscriptionID: recreated.SubscriptionID}); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	if err = push.Unsubscribe(ctx, ExternalMailPushRequest{Token: ExternalMailToken{AccessToken: "app-token"}, SubscriptionID: recreated.SubscriptionID}); err != nil {
		t.Fatalf("Unsubscribe of an unknown subscription: %v", err)
	}
}

// TestOutlookPushParseNotification: the handshake echoes a printable token only; notifications are
// read for their id, clientState, and known lifecycle event, at most maxExternalMailPushHints.
func TestOutlookPushParseNotification(t *testing.T) {
	push := newFakeGraphPush(t).push()
	ctx := context.Background()
	parse := func(query url.Values, body string) (ExternalMailPushParsed, error) {
		return push.ParseNotification(ctx, ExternalMailPushDelivery{Header: http.Header{}, Query: query, Body: []byte(body), Now: pushTestNow})
	}
	parsed, err := parse(url.Values{"validationToken": {"Validation: Token 123"}}, "")
	if err != nil || parsed.Validation != "Validation: Token 123" {
		t.Fatalf("handshake = %+v %v", parsed, err)
	}
	for name, token := range map[string]string{
		"empty": "", "control character": "a\nb", "oversized": strings.Repeat("v", outlookMaxValidationToken+1), "non-ASCII": "tökén",
	} {
		if _, err := parse(url.Values{"validationToken": {token}}, ""); !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("%s handshake: %v, want invalid input", name, err)
		}
	}
	if _, err := parse(url.Values{}, "not json"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("malformed body: %v", err)
	}
	items := []map[string]string{
		{"subscriptionId": "sub-1", "clientState": "s1", "lifecycleEvent": "reauthorizationRequired"},
		{"subscriptionId": "sub/../2", "clientState": "s2"},
		{"subscriptionId": "sub-3", "clientState": "s3", "lifecycleEvent": "somethingNew"},
	}
	for i := 0; i < 30; i++ {
		items = append(items, map[string]string{"subscriptionId": "bulk", "clientState": "x"})
	}
	raw, _ := json.Marshal(map[string]any{"value": items})
	parsed, err = parse(url.Values{}, string(raw))
	if err != nil || len(parsed.Hints) != maxExternalMailPushHints {
		t.Fatalf("parsed %d hints err %v", len(parsed.Hints), err)
	}
	if parsed.Hints[0] != (ExternalMailPushHint{Key: "sub-1", Secret: "s1", Lifecycle: domain.ExternalMailPushReauthorize}) ||
		parsed.Hints[1] != (ExternalMailPushHint{Key: "sub-3", Secret: "s3"}) || parsed.Hints[0].Authenticated {
		t.Fatalf("hints %+v", parsed.Hints[:2])
	}
}
