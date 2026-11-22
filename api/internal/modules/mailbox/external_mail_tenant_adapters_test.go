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
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
)

// fakeGoogleTenant is a token endpoint that checks service account assertions and a Directory API.
type fakeGoogleTenant struct {
	t          *testing.T
	server     *httptest.Server
	public     *rsa.PublicKey
	mu         sync.Mutex
	subjects   []string
	scopes     []string
	refuseSubs map[string]bool
	users      []map[string]any
}

func newFakeGoogleTenant(t *testing.T, public *rsa.PublicKey) *fakeGoogleTenant {
	t.Helper()
	g := &fakeGoogleTenant{t: t, public: public, refuseSubs: map[string]bool{}}
	for _, user := range []struct{ id, email, name string }{
		{"u1", "alice@acme.example", "Alice"}, {"u2", "bob@acme.example", "Bob"}, {"u3", "old@acme.example", "Old"},
	} {
		g.users = append(g.users, map[string]any{
			"id": user.id, "primaryEmail": user.email, "customerId": "C0acme", "name": map[string]any{"fullName": user.name},
			"suspended": user.id == "u3",
		})
	}
	g.server = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.server.Close)
	return g
}

func (g *fakeGoogleTenant) source() *gmailTenantSource {
	source := newGmailTenantSource()
	source.tokenEndpoint = g.server.URL + "/token"
	source.directoryBase = g.server.URL + "/admin/directory/v1"
	source.now = func() time.Time { return time.Unix(1_800_000_000, 0) }
	return source
}

// verify checks an RS256 assertion against the test key and returns its claims.
func (g *fakeGoogleTenant) verify(assertion string) (map[string]any, bool) {
	parts := strings.Split(assertion, ".")
	if len(parts) != 3 {
		return nil, false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, false
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(g.public, crypto.SHA256, digest[:], signature) != nil {
		return nil, false
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	claims := map[string]any{}
	return claims, json.Unmarshal(raw, &claims) == nil
}

func (g *fakeGoogleTenant) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/token":
		_ = r.ParseForm()
		claims, ok := g.verify(r.PostForm.Get("assertion"))
		if r.PostForm.Get("grant_type") != gmailJWTBearerGrant || !ok || claims["aud"] != g.server.URL+"/token" ||
			claims["iss"] != "zmail@project.iam.gserviceaccount.com" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		subject, _ := claims["sub"].(string)
		scope, _ := claims["scope"].(string)
		g.subjects, g.scopes = append(g.subjects, subject), append(g.scopes, scope)
		if g.refuseSubs[subject] {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized_client"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"at-` + subject + `","expires_in":3600}`))
	case r.URL.Path == "/admin/directory/v1/users":
		if r.Header.Get("Authorization") != "Bearer at-it@acme.example" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		users, next := g.users, ""
		if r.URL.Query().Get("maxResults") == "1" || r.URL.Query().Get("pageToken") == "" && r.URL.Query().Get("maxResults") == "2" {
			if r.URL.Query().Get("maxResults") == "2" {
				users, next = g.users[:2], "page-2"
			} else {
				users = g.users[:1]
			}
		} else if r.URL.Query().Get("pageToken") == "page-2" {
			users = g.users[2:]
		} else if r.URL.Query().Get("pageToken") != "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"errors":[{"reason":"invalid"}]}}`))
			return
		}
		body, _ := json.Marshal(map[string]any{"users": users, "nextPageToken": next})
		_, _ = w.Write(body)
	case strings.HasPrefix(r.URL.Path, "/admin/directory/v1/users/"):
		key := strings.TrimPrefix(r.URL.Path, "/admin/directory/v1/users/")
		for _, user := range g.users {
			if user["primaryEmail"] == key || key == "alias@acme.example" && user["id"] == "u1" {
				body, _ := json.Marshal(user)
				_, _ = w.Write(body)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"errors":[{"reason":"notFound"}]}}`))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func testServiceAccountKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	return testServiceAccountKeyBits(t, 2048)
}

func testServiceAccountKeyBits(t *testing.T, bits int) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	encoded, _ := json.Marshal(map[string]string{
		"type": "service_account", "client_email": "zmail@project.iam.gserviceaccount.com", "private_key_id": "k1",
		"private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		// A key's own token endpoint is never used.
		"token_uri": "https://evil.example/token",
	})
	return key, string(encoded)
}

// TestGmailTenantSource: the service account signs RS256 assertions for the administrator (both
// scopes) and for each mailbox (gmail.readonly only); the directory pages, skips suspended users, and
// refuses aliases; a refused credential is ErrExternalMailAuth.
func TestGmailTenantSource(t *testing.T) {
	key, keyFile := testServiceAccountKey(t)
	google := newFakeGoogleTenant(t, &key.PublicKey)
	source := google.source()
	ctx := context.Background()

	for name, grant := range map[string]ExternalMailTenantGrant{
		"not a key":   {ServiceAccountKey: `{"type":"authorized_user"}`, AdminSubject: "it@acme.example"},
		"no admin":    {ServiceAccountKey: keyFile},
		"bad admin":   {ServiceAccountKey: keyFile, AdminSubject: "IT <it@acme.example>"},
		"bad pem":     {ServiceAccountKey: `{"type":"service_account","client_email":"a@b","private_key":"nope"}`, AdminSubject: "it@acme.example"},
		"oversized":   {ServiceAccountKey: strings.Repeat(" ", gmailMaxServiceAccountKey+1), AdminSubject: "it@acme.example"},
		"not json at": {ServiceAccountKey: "{", AdminSubject: "it@acme.example"},
	} {
		if _, err := source.AuthorizeTenant(ctx, grant); !errors.Is(err, ErrExternalMailAuthorizationInvalid) {
			t.Errorf("%s = %v, want invalid", name, err)
		}
	}
	authorization, err := source.AuthorizeTenant(ctx, ExternalMailTenantGrant{ServiceAccountKey: keyFile, AdminSubject: "IT@acme.example"})
	if err != nil || authorization.TenantIdentifier != "C0acme" || authorization.DisplayName != "acme.example" {
		t.Fatalf("authorize = %+v, err %v", authorization, err)
	}
	if google.subjects[0] != "it@acme.example" || google.scopes[0] != gmailTenantMailScope+" "+gmailTenantDirectoryScope {
		t.Fatalf("admin assertion = %v %v", google.subjects, google.scopes)
	}
	if strings.Contains(authorization.Credential, "evil.example") {
		t.Fatal("the key's token_uri was kept")
	}

	page, err := source.ListTenantDirectory(ctx, authorization.Credential, "", 2)
	if err != nil || len(page.Entries) != 2 || page.Next != "page-2" || page.Entries[0] != (ExternalMailDirectoryEntry{
		Address: "alice@acme.example", DisplayName: "Alice", DirectoryID: "u1",
	}) {
		t.Fatalf("first page = %+v, err %v", page, err)
	}
	if page, err = source.ListTenantDirectory(ctx, authorization.Credential, "page-2", 2); err != nil || len(page.Entries) != 0 || page.Next != "" {
		t.Fatalf("second page (only a suspended user) = %+v, err %v", page, err)
	}
	if entry, lookupErr := source.LookupTenantMailbox(ctx, authorization.Credential, "bob@acme.example"); lookupErr != nil || entry.DirectoryID != "u2" {
		t.Fatalf("lookup = %+v, err %v", entry, lookupErr)
	}
	for _, address := range []string{"alias@acme.example", "nobody@acme.example", "old@acme.example"} {
		if _, err = source.LookupTenantMailbox(ctx, authorization.Credential, address); !errors.Is(err, ErrExternalMailDirectoryNotFound) {
			t.Errorf("lookup %s = %v, want not found", address, err)
		}
	}

	token, err := source.MailboxToken(ctx, authorization.Credential, "bob@acme.example", "u2")
	last := len(google.subjects) - 1
	if err != nil || token.AccessToken != "at-bob@acme.example" || token.RefreshToken != "" ||
		google.subjects[last] != "bob@acme.example" || google.scopes[last] != gmailTenantMailScope {
		t.Fatalf("mailbox token = %+v, err %v, assertion %s %s", token, err, google.subjects[last], google.scopes[last])
	}
	google.refuseSubs["bob@acme.example"] = true
	if _, err = source.MailboxToken(ctx, authorization.Credential, "bob@acme.example", "u2"); !errors.Is(err, ErrExternalMailAuth) {
		t.Fatalf("refused mailbox = %v", err)
	}
	if err = source.VerifyTenant(ctx, authorization.Credential); err != nil {
		t.Fatalf("verify: %v", err)
	}
	google.refuseSubs["it@acme.example"] = true
	if err = source.VerifyTenant(ctx, authorization.Credential); !errors.Is(err, ErrExternalMailAuth) {
		t.Fatalf("refused tenant = %v", err)
	}
	if err = source.VerifyTenant(ctx, "{}"); !errors.Is(err, ErrExternalMailAuth) {
		t.Fatalf("unreadable credential = %v", err)
	}

	// A cursor is a page token only, never a URL; a malformed or refused one is the caller's input and
	// never an authorization failure.
	google.refuseSubs["it@acme.example"] = false
	for _, cursor := range []string{"https://evil.example/users", "page token", strings.Repeat("a", maxDirectoryPageToken+1), "stale-token"} {
		if _, err = source.ListTenantDirectory(ctx, authorization.Credential, cursor, 2); !errors.Is(err, domain.ErrInvalidInput) || errors.Is(err, ErrExternalMailAuth) {
			t.Errorf("cursor %.30q = %v, want invalid input", cursor, err)
		}
	}
	// Keys outside 2048 to 4096 bits are refused.
	_, weak := testServiceAccountKeyBits(t, 1024)
	if _, err = source.AuthorizeTenant(ctx, ExternalMailTenantGrant{ServiceAccountKey: weak, AdminSubject: "it@acme.example"}); !errors.Is(err, ErrExternalMailAuthorizationInvalid) {
		t.Fatalf("1024-bit key = %v, want invalid", err)
	}
}

// TestGmailFetchWithTenantToken: a tenant token reads users/me as the delegated mailbox without any
// refresh.
func TestGmailFetchWithTenantToken(t *testing.T) {
	var authorizations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			t.Error("a tenant fetch refreshed a token")
		}
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/gmail/v1/users/me/profile":
			_, _ = w.Write([]byte(`{"emailAddress":"bob@acme.example","historyId":"7"}`))
		case "/gmail/v1/users/me/messages":
			_, _ = w.Write([]byte(`{"messages":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	source := newGmailMailSource(config.ExternalMailOAuthClient{ClientID: "id", ClientSecret: "secret"})
	source.tokenEndpoint, source.apiBase = server.URL+"/token", server.URL+"/gmail/v1"
	fetched, err := source.Fetch(context.Background(), ExternalMailFetchRequest{
		Token: ExternalMailToken{AccessToken: "at-bob", DirectoryID: "u2"}, AccountAddress: "bob@acme.example", Now: time.Now(),
	})
	if err != nil || fetched.RefreshToken != "" || fetched.Cursor == "" {
		t.Fatalf("fetch = %+v, err %v", fetched, err)
	}
	for _, header := range authorizations {
		if header != "Bearer at-bob" {
			t.Fatalf("authorization header %q", header)
		}
	}
}

// fakeMicrosoftTenant is a client credentials token endpoint and a Graph directory.
type fakeMicrosoftTenant struct {
	server      *httptest.Server
	mu          sync.Mutex
	forms       []url.Values
	paths       []string
	tokenStatus int
	usersStatus int
	tenantID    string
}

func newFakeMicrosoftTenant(t *testing.T) *fakeMicrosoftTenant {
	t.Helper()
	m := &fakeMicrosoftTenant{tenantID: "0b1e2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"}
	m.server = httptest.NewServer(http.HandlerFunc(m.serve))
	t.Cleanup(m.server.Close)
	return m
}

func (m *fakeMicrosoftTenant) source() *outlookTenantSource {
	source := newOutlookTenantSource(config.ExternalMailOAuthClient{ClientID: "operator-app", ClientSecret: "operator-secret"})
	source.graph.loginBaseURL = m.server.URL + "/login"
	source.graph.graphBaseURL = m.server.URL + "/v1.0"
	return source
}

func (m *fakeMicrosoftTenant) tokenRequests() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.forms)
}

func (m *fakeMicrosoftTenant) serve(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	m.paths = append(m.paths, r.URL.RequestURI())
	query := r.URL.Query()
	switch {
	case r.URL.Path == "/login/"+m.tenantID+"/oauth2/v2.0/token":
		_ = r.ParseForm()
		m.forms = append(m.forms, r.PostForm)
		if m.tokenStatus != 0 {
			w.WriteHeader(m.tokenStatus)
			_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
			return
		}
		if r.PostForm.Get("grant_type") != "client_credentials" || r.PostForm.Get("scope") != outlookTenantScope ||
			r.PostForm.Get("client_id") != "own-app" || r.PostForm.Get("client_secret") != "own-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"app-token","token_type":"Bearer","expires_in":3599}`))
	case r.URL.Path == "/v1.0/users" && m.usersStatus != 0:
		w.WriteHeader(m.usersStatus)
		_, _ = w.Write([]byte(`{"error":{"code":"Authorization_RequestDenied"}}`))
	case r.URL.Path == "/v1.0/users" && query.Get("$filter") != "":
		if query.Get("$filter") == "mail eq 'bob@m365.example'" {
			_, _ = w.Write([]byte(`{"value":[{"id":"u-bob","displayName":"Bob","mail":"Bob@m365.example"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"value":[]}`))
	case r.URL.Path == "/v1.0/users" && query.Get("$skiptoken") == "":
		next := m.server.URL + "/v1.0/users?$select=id&$top=2&$skiptoken=RFNwdAIAAQ"
		_, _ = w.Write([]byte(`{"value":[{"id":"u-alice","displayName":"Alice","mail":"alice@m365.example"},
			{"id":"u-room","displayName":"No mailbox","mail":null}],"@odata.nextLink":"` + next + `"}`))
	case r.URL.Path == "/v1.0/users" && query.Get("$skiptoken") == "RFNwdAIAAQ":
		_, _ = w.Write([]byte(`{"value":[{"id":"u-bob","displayName":"Bob","mail":"bob@m365.example"}]}`))
	case r.URL.Path == "/v1.0/users":
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"Request_BadRequest"}}`))
	case strings.HasPrefix(r.URL.Path, "/v1.0/users/u-bob/mailFolders/inbox/messages/delta"):
		_, _ = w.Write([]byte(`{"value":[],"@odata.deltaLink":"` + m.server.URL + `/v1.0/users/u-bob/mailFolders/inbox/messages/delta?$deltatoken=1"}`))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// TestOutlookTenantSource: only the organization's own application is accepted; the client
// credentials flow proves it with a directory read, at authorization and at every check; the
// directory pages by skip token on a server-built URL; tokens are cached; an app token fetches
// /users/{id}, never /me.
func TestOutlookTenantSource(t *testing.T) {
	microsoft := newFakeMicrosoftTenant(t)
	source := microsoft.source()
	ctx := context.Background()

	for name, grant := range map[string]ExternalMailTenantGrant{
		"domain":         {TenantID: "contoso.example", ClientID: "own-app", ClientSecret: "own-secret"},
		"operator app":   {TenantID: microsoft.tenantID},
		"id without key": {TenantID: microsoft.tenantID, ClientID: "own-app"},
		"key without id": {TenantID: microsoft.tenantID, ClientSecret: "own-secret"},
	} {
		if _, err := source.AuthorizeTenant(ctx, grant); !errors.Is(err, ErrExternalMailAuthorizationInvalid) {
			t.Errorf("%s = %v, want invalid", name, err)
		}
	}
	if microsoft.tokenRequests() != 0 {
		t.Fatal("an invalid grant reached Microsoft")
	}
	own, err := source.AuthorizeTenant(ctx, ExternalMailTenantGrant{TenantID: strings.ToUpper(microsoft.tenantID), ClientID: "own-app", ClientSecret: "own-secret"})
	if err != nil || own.TenantIdentifier != microsoft.tenantID || microsoft.forms[0].Get("client_id") != "own-app" {
		t.Fatalf("own app = %+v, err %v", own, err)
	}
	if !strings.Contains(strings.Join(microsoft.paths, " "), "/v1.0/users?$top=1&$select=id") {
		t.Fatalf("authorization read no directory user: %v", microsoft.paths)
	}

	page, err := source.ListTenantDirectory(ctx, own.Credential, "", 2)
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Address != "alice@m365.example" || page.Next != "RFNwdAIAAQ" {
		t.Fatalf("first page = %+v, err %v", page, err)
	}
	if page, err = source.ListTenantDirectory(ctx, own.Credential, page.Next, 2); err != nil || page.Entries[0].DirectoryID != "u-bob" {
		t.Fatalf("second page = %+v, err %v", page, err)
	}
	// A cursor is a skip token on the fixed /users query, never a link: a URL is malformed before any
	// request, and a token Graph refuses is the caller's input; neither is an authorization failure.
	before := len(microsoft.paths)
	for _, cursor := range []string{"https://evil.example/v1.0/users", microsoft.server.URL + "/v1.0/users/u-bob/messages", "a b"} {
		if _, err = source.ListTenantDirectory(ctx, own.Credential, cursor, 2); !errors.Is(err, domain.ErrInvalidInput) || errors.Is(err, ErrExternalMailAuth) {
			t.Errorf("cursor %q = %v, want invalid input", cursor, err)
		}
	}
	if len(microsoft.paths) != before {
		t.Fatalf("a malformed cursor reached Microsoft: %v", microsoft.paths[before:])
	}
	if _, err = source.ListTenantDirectory(ctx, own.Credential, "stale", 2); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("refused skip token = %v, want invalid input", err)
	}
	for _, path := range microsoft.paths {
		if strings.HasPrefix(path, "/v1.0/") && !strings.HasPrefix(path, "/v1.0/users?") {
			t.Fatalf("directory listing requested %s", path)
		}
	}
	if entry, lookupErr := source.LookupTenantMailbox(ctx, own.Credential, "bob@m365.example"); lookupErr != nil || entry.DirectoryID != "u-bob" {
		t.Fatalf("lookup = %+v, err %v", entry, lookupErr)
	}
	if _, err = source.LookupTenantMailbox(ctx, own.Credential, "o'hara@m365.example"); !errors.Is(err, ErrExternalMailDirectoryNotFound) {
		t.Fatalf("unknown lookup = %v", err)
	}

	// The application token is cached for its lifetime: mailbox tokens ask Microsoft once.
	tokens := microsoft.tokenRequests()
	for range 3 {
		if _, err = source.MailboxToken(ctx, own.Credential, "bob@m365.example", "u-bob"); err != nil {
			t.Fatalf("mailbox token: %v", err)
		}
	}
	if microsoft.tokenRequests() != tokens {
		t.Fatalf("%d token requests for cached mailbox tokens", microsoft.tokenRequests()-tokens)
	}
	token, err := source.MailboxToken(ctx, own.Credential, "bob@m365.example", "u-bob")
	if err != nil || token.AccessToken != "app-token" || token.DirectoryID != "u-bob" {
		t.Fatalf("mailbox token = %+v, err %v", token, err)
	}
	mail := newOutlookMailSource(config.ExternalMailOAuthClient{ClientID: "operator-app", ClientSecret: "operator-secret"})
	mail.loginBaseURL, mail.graphBaseURL = microsoft.server.URL+"/login", microsoft.server.URL+"/v1.0"
	if _, err = mail.Fetch(ctx, ExternalMailFetchRequest{Token: token, AccountAddress: "bob@m365.example", Now: time.Now()}); err != nil {
		t.Fatalf("app-only fetch: %v", err)
	}
	for _, path := range microsoft.paths {
		if strings.Contains(path, "/me/") {
			t.Fatalf("app-only fetch addressed /me: %s", path)
		}
	}

	// A check takes a fresh token and reads the directory, so a removed permission is noticed.
	tokens = microsoft.tokenRequests()
	if err = source.VerifyTenant(ctx, own.Credential); err != nil || microsoft.tokenRequests() != tokens+1 {
		t.Fatalf("verify: %v, %d token requests", err, microsoft.tokenRequests()-tokens)
	}
	microsoft.usersStatus = http.StatusForbidden
	if err = source.VerifyTenant(ctx, own.Credential); !errors.Is(err, ErrExternalMailAuth) {
		t.Fatalf("permission removed = %v, want an authorization failure", err)
	}
	// Throttling and server errors never count as a refused tenant; Microsoft's refusal does.
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		microsoft.usersStatus = status
		if err = source.VerifyTenant(ctx, own.Credential); err == nil || errors.Is(err, ErrExternalMailAuth) {
			t.Errorf("directory %d = %v, want a retryable error", status, err)
		}
		microsoft.usersStatus = 0
		microsoft.tokenStatus = status
		if err = source.VerifyTenant(ctx, own.Credential); err == nil || errors.Is(err, ErrExternalMailAuth) {
			t.Errorf("token %d = %v, want a retryable error", status, err)
		}
		microsoft.tokenStatus = 0
	}
	microsoft.tokenStatus = http.StatusUnauthorized
	if err = source.VerifyTenant(ctx, own.Credential); !errors.Is(err, ErrExternalMailAuth) {
		t.Fatalf("refused application = %v", err)
	}
	if _, err = source.MailboxToken(ctx, own.Credential, "bob@m365.example", "u-bob"); !errors.Is(err, ErrExternalMailAuth) {
		t.Fatalf("a refused application kept a cached token: %v", err)
	}
}
