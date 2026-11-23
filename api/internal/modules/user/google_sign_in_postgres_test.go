package user

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/database/migrations"
	capcrypto "github.com/fastagi/zmail/api/internal/capabilities/crypto"
	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/migration"
	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

const (
	googleTestClientID     = "zmail-test.apps.googleusercontent.com"
	googleTestClientSecret = "zmail-test-secret"
	googleTestRedirectURI  = "https://app.example/api/auth/google/callback"
	googleTestKeyID        = "fake-google-key"
)

var (
	googleTestKeyOnce sync.Once
	googleTestKey     *rsa.PrivateKey
)

func googleSigningKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	googleTestKeyOnce.Do(func() {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		googleTestKey = key
	})
	return googleTestKey
}

// googleTestIdentity is the Google account the fake provider signs in as.
type googleTestIdentity struct {
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
}

// fakeGoogleGrant is one authorization the fake provider handed out.
type fakeGoogleGrant struct {
	challenge   string
	nonce       string
	redirectURI string
	identity    googleTestIdentity
}

// fakeGoogle is a local OIDC provider: an authorization code endpoint that checks PKCE, the client,
// and the redirect URI, and a key set that publishes the signing key. Real Google is never called.
type fakeGoogle struct {
	t      *testing.T
	server *httptest.Server
	key    *rsa.PrivateKey

	mu     sync.Mutex
	grants map[string]fakeGoogleGrant
	// tamper changes the claims of the next token before it is signed.
	tamper func(map[string]any)
	// tokenCalls counts code exchanges.
	tokenCalls int
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	t.Helper()
	fake := &fakeGoogle{t: t, key: googleSigningKey(t), grants: map[string]fakeGoogleGrant{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", fake.token)
	mux.HandleFunc("/jwks", fake.jwks)
	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakeGoogle) client() *googleOIDCClient {
	client := newGoogleOIDCClient(googleTestClientID, googleTestClientSecret)
	client.authEndpoint = f.server.URL + "/auth"
	client.tokenEndpoint = f.server.URL + "/token"
	client.keys.url = f.server.URL + "/jwks"
	return client
}

// approve plays the browser at Google's consent page: it reads the authorization URL the service
// built and returns the code Google would send back with the state.
func (f *fakeGoogle) approve(t *testing.T, authorizationURL string, identity googleTestIdentity) string {
	t.Helper()
	parsed, err := url.Parse(authorizationURL)
	require.NoError(t, err)
	require.Equal(t, f.server.URL+"/auth", parsed.Scheme+"://"+parsed.Host+parsed.Path)
	query := parsed.Query()
	require.Equal(t, googleTestClientID, query.Get("client_id"))
	require.Equal(t, "code", query.Get("response_type"))
	require.Equal(t, "S256", query.Get("code_challenge_method"))
	require.Equal(t, "openid email profile", query.Get("scope"))
	require.NotEmpty(t, query.Get("state"))
	require.NotEmpty(t, query.Get("nonce"))
	code := "code-" + randomTestToken(t)
	f.mu.Lock()
	f.grants[code] = fakeGoogleGrant{
		challenge: query.Get("code_challenge"), nonce: query.Get("nonce"),
		redirectURI: query.Get("redirect_uri"), identity: identity,
	}
	f.mu.Unlock()
	return code
}

func (f *fakeGoogle) token(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokenCalls++
	if r.Method != http.MethodPost || r.ParseForm() != nil {
		http.Error(w, `{"error":"invalid_request"}`, http.StatusBadRequest)
		return
	}
	grant, ok := f.grants[r.PostForm.Get("code")]
	delete(f.grants, r.PostForm.Get("code"))
	verifierDigest := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if !ok || r.PostForm.Get("grant_type") != "authorization_code" ||
		r.PostForm.Get("client_id") != googleTestClientID || r.PostForm.Get("client_secret") != googleTestClientSecret ||
		r.PostForm.Get("redirect_uri") != grant.redirectURI ||
		base64.RawURLEncoding.EncodeToString(verifierDigest[:]) != grant.challenge {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
		return
	}
	now := time.Now()
	claims := map[string]any{
		"iss": "https://accounts.google.com", "aud": googleTestClientID, "azp": googleTestClientID,
		"sub": grant.identity.Subject, "email": grant.identity.Email, "email_verified": grant.identity.EmailVerified,
		"name": grant.identity.Name, "nonce": grant.nonce, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}
	if f.tamper != nil {
		f.tamper(claims)
		f.tamper = nil
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": "fake-access", "token_type": "Bearer", "expires_in": 3599,
		"id_token": signGoogleTestToken(f.t, f.key, googleTestKeyID, claims),
	})
}

func (f *fakeGoogle) jwks(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{googleTestJWK(f.key, googleTestKeyID)}})
}

func googleTestJWK(key *rsa.PrivateKey, keyID string) map[string]string {
	return map[string]string{
		"kty": "RSA", "kid": keyID, "alg": "RS256", "use": "sig",
		"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}
}

func signGoogleTestToken(t *testing.T, key *rsa.PrivateKey, keyID string, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg": "RS256", "kid": keyID, "typ": "JWT"})
	require.NoError(t, err)
	payload, err := json.Marshal(claims)
	require.NoError(t, err)
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	require.NoError(t, err)
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func randomTestToken(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 12)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// googleSignInFixture is a migrated database, the user service over it, and a fake Google.
type googleSignInFixture struct {
	db     *gorm.DB
	repo   *repository
	svc    *service
	google *fakeGoogle
}

func newGoogleSignInFixture(t *testing.T) *googleSignInFixture {
	t.Helper()
	db := testplatform.OpenPostgres(t, nil)
	migrator := migration.NewMigrator(migration.NewDatabaseRepository(db, "migrations"), db, nil)
	migrator.RegisterMany(migrations.All())
	_, err := migrator.Run(migration.MigratorOptions{})
	require.NoError(t, err)

	repo := NewRepository(db)
	cfg := &config.Config{App: config.AppConfig{Env: "production"}}
	cfg.Authentication.RegistrationEmailCodeRequired = true
	cfg.Authentication.TOTPEncryptionKey = "totp-test-key-0123456789abcdef0123"
	svc := ProvideService(repo, repo, NewSessionService(repo, cfg), nil, nil, nil, nil, cfg)
	google := newFakeGoogle(t)
	svc.google = google.client()
	return &googleSignInFixture{db: db, repo: repo, svc: svc, google: google}
}

// signIn runs the whole flow for identity and returns the outcome of the callback.
func (f *googleSignInFixture) signIn(t *testing.T, identity googleTestIdentity, returnURL string) (*GoogleSessionResponse, error) {
	t.Helper()
	ctx := context.Background()
	started, err := f.svc.StartGoogleSignIn(ctx, &GoogleAuthorizationRequest{RedirectURI: googleTestRedirectURI, ReturnURL: returnURL})
	require.NoError(t, err)
	code := f.google.approve(t, started.AuthorizationURL, identity)
	return f.svc.CompleteGoogleSignIn(ctx, &GoogleSessionRequest{State: started.State, Code: code})
}

func (f *googleSignInFixture) createPasswordUser(t *testing.T, username, email string, status int) *domain.User {
	t.Helper()
	user := &domain.User{Username: username, Email: email, Password: "$2a$10$notarealhashnotarealhashnotarealhashnotarealhashnot", Status: status}
	require.NoError(t, f.repo.Create(context.Background(), user))
	// The column default would turn a zero status into an active account.
	require.NoError(t, f.db.Exec(`UPDATE users SET status = ? WHERE id = ?`, status, user.ID).Error)
	user.Status = status
	return user
}

func (f *googleSignInFixture) markEmailVerified(t *testing.T, userID uint) {
	t.Helper()
	require.NoError(t, f.db.Exec(`UPDATE users SET email_verified_at = now() WHERE id = ?`, userID).Error)
}

// issueSession signs the account in the ordinary way and returns the session's ID.
func (f *googleSignInFixture) issueSession(t *testing.T, user *domain.User) string {
	t.Helper()
	_, err := f.svc.sessions.Issue(context.Background(), user, domain.SessionAudienceUser)
	require.NoError(t, err)
	var row AuthenticationSessionPO
	require.NoError(t, f.db.Where("user_id = ?", user.ID).Order("created_at DESC").Take(&row).Error)
	return row.ID
}

func (f *googleSignInFixture) session(t *testing.T, id string) AuthenticationSessionPO {
	t.Helper()
	var row AuthenticationSessionPO
	require.NoError(t, f.db.Where("id = ?", id).Take(&row).Error)
	return row
}

func (f *googleSignInFixture) identities(t *testing.T) []UserIdentityPO {
	t.Helper()
	var rows []UserIdentityPO
	require.NoError(t, f.db.Order("id").Find(&rows).Error)
	return rows
}

func (f *googleSignInFixture) userCount(t *testing.T) int64 {
	t.Helper()
	var count int64
	require.NoError(t, f.db.Model(&UserPO{}).Count(&count).Error)
	return count
}

func TestGoogleSignInCreatesAccountForNewUser(t *testing.T) {
	f := newGoogleSignInFixture(t)
	before := f.userCount(t)

	result, err := f.signIn(t, googleTestIdentity{Subject: "1001", Email: "New.Person@Example.com", EmailVerified: true, Name: "New Person"}, "/console/admin/domains")
	require.NoError(t, err)

	assert.Equal(t, before+1, f.userCount(t))
	assert.Equal(t, "/console/admin/domains", result.ReturnURL, "the return URL rides in the stored state")
	assert.Equal(t, "Bearer", result.TokenType)
	assert.True(t, validAuthenticationCredential(result.AccessToken))
	assert.Positive(t, result.ExpiresIn)
	require.NotNil(t, result.User)
	assert.Equal(t, "new.person@example.com", result.User.Email, "the verified Google email, normalized")
	assert.Equal(t, "New Person", result.User.Nickname)
	assert.True(t, strings.HasPrefix(result.User.Username, "user_"))
	assert.NotContains(t, result.User.Username, "@")

	var stored UserPO
	require.NoError(t, f.db.First(&stored, result.User.ID).Error)
	assert.NotNil(t, stored.EmailVerifiedAt)
	assert.NotNil(t, stored.LastLogin, "the sign-in is recorded like a password login")
	assert.Empty(t, stored.Password, "a Google-only account has no password")

	identities := f.identities(t)
	require.Len(t, identities, 1)
	assert.Equal(t, result.User.ID, identities[0].UserID)
	assert.Equal(t, "google", identities[0].Provider)
	assert.Equal(t, "1001", identities[0].Subject)

	identity, err := NewSessionService(f.repo, nil).Authenticate(context.Background(), result.AccessToken)
	require.NoError(t, err, "the issued credential is an ordinary user session")
	assert.Equal(t, result.User.ID, identity.UserID)

	_, err = f.svc.Login(context.Background(), &UserLoginRequest{Username: "new.person@example.com", Password: ""})
	assert.ErrorIs(t, err, domain.ErrInvalidCredentials, "no password signs in to a Google-only account")
}

func TestGoogleSignInLinksExistingEmailWithoutDuplicating(t *testing.T) {
	f := newGoogleSignInFixture(t)
	existing := f.createPasswordUser(t, "alice", "Alice@Example.com", 1)
	f.markEmailVerified(t, existing.ID)
	earlier := f.issueSession(t, existing)
	before := f.userCount(t)

	result, err := f.signIn(t, googleTestIdentity{Subject: "2002", Email: "alice@example.com", EmailVerified: true, Name: "Alice"}, "")
	require.NoError(t, err)

	assert.Equal(t, before, f.userCount(t), "no second account")
	assert.Equal(t, existing.ID, result.User.ID)
	assert.Equal(t, "/console", result.ReturnURL, "no return URL falls back to the landing page")
	var stored UserPO
	require.NoError(t, f.db.First(&stored, existing.ID).Error)
	assert.NotNil(t, stored.EmailVerifiedAt, "linking proves the email")
	assert.Equal(t, existing.Password, stored.Password, "the password keeps working")
	assert.Nil(t, f.session(t, earlier).RevokedAt, "a verified account keeps its sessions")
	identities := f.identities(t)
	require.Len(t, identities, 1)
	assert.Equal(t, existing.ID, identities[0].UserID)

	again, err := f.signIn(t, googleTestIdentity{Subject: "2002", Email: "alice@example.com", EmailVerified: true}, "")
	require.NoError(t, err)
	assert.Equal(t, existing.ID, again.User.ID)
	assert.Len(t, f.identities(t), 1, "a second sign-in reuses the link")
}

// An account whose email was never proven may have been registered by someone else in the owner's
// name. Linking it to Google hands it to the Google account: the password and every earlier session
// stop working, and the owner can set a new password through forgot-password.
func TestGoogleSignInTakesOverAnUnverifiedAccount(t *testing.T) {
	f := newGoogleSignInFixture(t)
	squatted := f.createPasswordUser(t, "squatter", "owner@example.com", 1)
	earlier := f.issueSession(t, squatted)

	result, err := f.signIn(t, googleTestIdentity{Subject: "2112", Email: "owner@example.com", EmailVerified: true}, "")
	require.NoError(t, err)
	assert.Equal(t, squatted.ID, result.User.ID)

	var stored UserPO
	require.NoError(t, f.db.First(&stored, squatted.ID).Error)
	assert.Empty(t, stored.Password, "the pre-registered password no longer signs in")
	assert.NotNil(t, stored.EmailVerifiedAt)
	revoked := f.session(t, earlier)
	require.NotNil(t, revoked.RevokedAt, "sessions from before the link end")
	assert.Equal(t, sessionRevocationIdentityLinked, revoked.RevocationReason)
	_, err = NewSessionService(f.repo, &config.Config{}).Authenticate(context.Background(), result.AccessToken)
	assert.NoError(t, err, "the Google sign-in's own session works")
	require.Len(t, f.identities(t), 1)

}

// Deleting an account deletes its Google links, so the subject neither revives the account nor
// blocks a new one.
func TestGoogleSignInForgetsTheLinksOfADeletedAccount(t *testing.T) {
	f := newGoogleSignInFixture(t)
	first, err := f.signIn(t, googleTestIdentity{Subject: "2323", Email: "gone@example.com", EmailVerified: true}, "")
	require.NoError(t, err)

	require.NoError(t, f.svc.DeleteAccount(context.Background(), first.User.ID))
	assert.Empty(t, f.identities(t), "the deleted account's links are gone")

	_, err = f.signIn(t, googleTestIdentity{Subject: "2323", Email: "gone@example.com", EmailVerified: true}, "")
	assert.ErrorIs(t, err, domain.ErrEmailAlreadyExists, "the deleted account still holds its email")
	assert.Empty(t, f.identities(t), "the deleted account is not revived")

	again, err := f.signIn(t, googleTestIdentity{Subject: "2323", Email: "new@example.com", EmailVerified: true}, "")
	require.NoError(t, err, "the subject does not block a new account")
	assert.NotEqual(t, first.User.ID, again.User.ID)
}

func TestGoogleSignInMatchesSubjectAfterEmailChange(t *testing.T) {
	f := newGoogleSignInFixture(t)
	first, err := f.signIn(t, googleTestIdentity{Subject: "3003", Email: "old@example.com", EmailVerified: true}, "")
	require.NoError(t, err)
	other := f.createPasswordUser(t, "someone", "renamed@example.com", 1)

	second, err := f.signIn(t, googleTestIdentity{Subject: "3003", Email: "renamed@example.com", EmailVerified: true}, "")
	require.NoError(t, err)
	assert.Equal(t, first.User.ID, second.User.ID, "the subject wins over the email")
	assert.NotEqual(t, other.ID, second.User.ID)
	identities := f.identities(t)
	require.Len(t, identities, 1)
	assert.Equal(t, "renamed@example.com", identities[0].Email, "the link remembers the current Google email")
	assert.NotNil(t, identities[0].LastSignInAt)
}

func TestGoogleSignInRefusesUnverifiedEmail(t *testing.T) {
	f := newGoogleSignInFixture(t)
	f.createPasswordUser(t, "victim", "victim@example.com", 1)
	before := f.userCount(t)

	_, err := f.signIn(t, googleTestIdentity{Subject: "4004", Email: "victim@example.com", EmailVerified: false}, "")
	assert.ErrorIs(t, err, domain.ErrGoogleEmailUnverified)
	assert.Equal(t, before, f.userCount(t))
	assert.Empty(t, f.identities(t), "an unverified email links nothing")
}

func TestGoogleSignInRefusesBadStateAndNonce(t *testing.T) {
	f := newGoogleSignInFixture(t)
	ctx := context.Background()
	identity := googleTestIdentity{Subject: "5005", Email: "state@example.com", EmailVerified: true}

	t.Run("unknown state", func(t *testing.T) {
		started, err := f.svc.StartGoogleSignIn(ctx, &GoogleAuthorizationRequest{RedirectURI: googleTestRedirectURI})
		require.NoError(t, err)
		code := f.google.approve(t, started.AuthorizationURL, identity)
		_, err = f.svc.CompleteGoogleSignIn(ctx, &GoogleSessionRequest{State: "forged-" + started.State, Code: code})
		assert.ErrorIs(t, err, domain.ErrGoogleStateInvalid)
	})

	t.Run("replayed state", func(t *testing.T) {
		started, err := f.svc.StartGoogleSignIn(ctx, &GoogleAuthorizationRequest{RedirectURI: googleTestRedirectURI})
		require.NoError(t, err)
		code := f.google.approve(t, started.AuthorizationURL, identity)
		_, err = f.svc.CompleteGoogleSignIn(ctx, &GoogleSessionRequest{State: started.State, Code: code})
		require.NoError(t, err)
		calls := f.google.tokenCalls
		_, err = f.svc.CompleteGoogleSignIn(ctx, &GoogleSessionRequest{State: started.State, Code: code})
		assert.ErrorIs(t, err, domain.ErrGoogleStateInvalid)
		assert.Equal(t, calls, f.google.tokenCalls, "a spent state never reaches Google")
	})

	t.Run("expired state", func(t *testing.T) {
		started, err := f.svc.StartGoogleSignIn(ctx, &GoogleAuthorizationRequest{RedirectURI: googleTestRedirectURI})
		require.NoError(t, err)
		code := f.google.approve(t, started.AuthorizationURL, identity)
		require.NoError(t, f.db.Exec(`UPDATE oauth_sign_in_states SET expires_at = now() - interval '1 second'`).Error)
		_, err = f.svc.CompleteGoogleSignIn(ctx, &GoogleSessionRequest{State: started.State, Code: code})
		assert.ErrorIs(t, err, domain.ErrGoogleStateInvalid)
	})

	t.Run("nonce mismatch", func(t *testing.T) {
		started, err := f.svc.StartGoogleSignIn(ctx, &GoogleAuthorizationRequest{RedirectURI: googleTestRedirectURI})
		require.NoError(t, err)
		code := f.google.approve(t, started.AuthorizationURL, identity)
		f.google.tamper = func(claims map[string]any) { claims["nonce"] = "someone-elses-nonce" }
		_, err = f.svc.CompleteGoogleSignIn(ctx, &GoogleSessionRequest{State: started.State, Code: code})
		assert.ErrorIs(t, err, domain.ErrGoogleStateInvalid)
	})

	t.Run("code Google refuses", func(t *testing.T) {
		started, err := f.svc.StartGoogleSignIn(ctx, &GoogleAuthorizationRequest{RedirectURI: googleTestRedirectURI})
		require.NoError(t, err)
		_, err = f.svc.CompleteGoogleSignIn(ctx, &GoogleSessionRequest{State: started.State, Code: "never-issued"})
		assert.ErrorIs(t, err, domain.ErrGoogleSignInFailed)
	})

	t.Run("token for another client", func(t *testing.T) {
		started, err := f.svc.StartGoogleSignIn(ctx, &GoogleAuthorizationRequest{RedirectURI: googleTestRedirectURI})
		require.NoError(t, err)
		code := f.google.approve(t, started.AuthorizationURL, identity)
		f.google.tamper = func(claims map[string]any) { claims["aud"] = "attacker.apps.googleusercontent.com" }
		_, err = f.svc.CompleteGoogleSignIn(ctx, &GoogleSessionRequest{State: started.State, Code: code})
		assert.ErrorIs(t, err, domain.ErrGoogleSignInFailed)
	})
}

func TestGoogleSignInReturnURL(t *testing.T) {
	f := newGoogleSignInFixture(t)
	identity := googleTestIdentity{Subject: "6006", Email: "return@example.com", EmailVerified: true}
	for returnURL, want := range map[string]string{
		"/console/admin/domains":       "/console/admin/domains",
		"/console/inbox?folder=sent#x": "/console/inbox?folder=sent#x",
		"//evil.example/steal":         "/console",
		"/\\evil.example":              "/console",
		"https://evil.example/":        "/console",
		"javascript:alert(1)":          "/console",
		"console":                      "/console",
	} {
		result, err := f.signIn(t, identity, returnURL)
		require.NoError(t, err, returnURL)
		assert.Equal(t, want, result.ReturnURL, returnURL)
	}
}

func TestGoogleSignInRefusesDisabledAccount(t *testing.T) {
	f := newGoogleSignInFixture(t)
	f.createPasswordUser(t, "disabled", "disabled@example.com", 0)

	_, err := f.signIn(t, googleTestIdentity{Subject: "7007", Email: "disabled@example.com", EmailVerified: true}, "")
	assert.ErrorIs(t, err, domain.ErrInvalidCredentials, "the same failure as a password login")
	assert.Empty(t, f.identities(t), "a refused sign-in links nothing")
}

func TestGoogleSignInWhileRegistrationIsClosed(t *testing.T) {
	f := newGoogleSignInFixture(t)
	f.svc.registrationOpen = false
	existing := f.createPasswordUser(t, "member", "member@example.com", 1)
	before := f.userCount(t)

	_, err := f.signIn(t, googleTestIdentity{Subject: "8008", Email: "stranger@example.com", EmailVerified: true}, "")
	assert.ErrorIs(t, err, domain.ErrGoogleRegistrationClosed)
	assert.Equal(t, before, f.userCount(t))

	result, err := f.signIn(t, googleTestIdentity{Subject: "8009", Email: "member@example.com", EmailVerified: true}, "")
	require.NoError(t, err, "an existing account still signs in")
	assert.Equal(t, existing.ID, result.User.ID)
	result, err = f.signIn(t, googleTestIdentity{Subject: "8009", Email: "member@example.com", EmailVerified: true}, "")
	require.NoError(t, err, "a linked account still signs in")
	assert.Equal(t, existing.ID, result.User.ID)
}

func TestGoogleSignInKeepsTwoFactorAccountsBehindTheirCode(t *testing.T) {
	f := newGoogleSignInFixture(t)
	existing := f.createPasswordUser(t, "guarded", "guarded@example.com", 1)
	require.NoError(t, f.db.Exec(`INSERT INTO user_totp_factors (user_id, secret, confirmed_at, created_at, updated_at)
		VALUES (?, 'sealed', now(), now(), now())`, existing.ID).Error)

	_, err := f.signIn(t, googleTestIdentity{Subject: "9009", Email: "guarded@example.com", EmailVerified: true}, "")
	assert.ErrorIs(t, err, domain.ErrTOTPRequired)
	assert.Empty(t, f.identities(t))
}

func TestGoogleSignInPasswordResetSetsAPasswordForGoogleOnlyAccount(t *testing.T) {
	f := newGoogleSignInFixture(t)
	created, err := f.signIn(t, googleTestIdentity{Subject: "1111", Email: "reset@example.com", EmailVerified: true}, "")
	require.NoError(t, err)

	token := "reset-token-" + randomTestToken(t)
	require.NoError(t, f.repo.StorePasswordResetToken(context.Background(), created.User.ID, capcrypto.SHA256Hex(token), time.Now().Add(time.Hour)))
	require.NoError(t, f.svc.ConfirmPasswordReset(context.Background(), &UserPasswordResetConfirmRequest{Token: token, NewPassword: "a-new-password"}))

	signedIn, err := f.svc.Login(context.Background(), &UserLoginRequest{Username: "reset@example.com", Password: "a-new-password"})
	require.NoError(t, err)
	assert.Equal(t, created.User.ID, signedIn.User.ID)
}

func TestGoogleSignInNotConfigured(t *testing.T) {
	svc := NewService(nil, nil, nil, nil, nil, nil)
	assert.False(t, svc.GoogleSignInEnabled())
	_, err := svc.StartGoogleSignIn(context.Background(), &GoogleAuthorizationRequest{RedirectURI: googleTestRedirectURI})
	assert.ErrorIs(t, err, domain.ErrGoogleNotConfigured)
	_, err = svc.CompleteGoogleSignIn(context.Background(), &GoogleSessionRequest{State: "s", Code: "c"})
	assert.ErrorIs(t, err, domain.ErrGoogleNotConfigured)
}

func TestGoogleSignInRejectsUnsafeRedirectURI(t *testing.T) {
	f := newGoogleSignInFixture(t)
	for _, redirect := range []string{
		"", "/api/auth/google/callback", "http://app.example/api/auth/google/callback",
		"https://user@app.example/cb", "https://app.example/cb#fragment", "ftp://app.example/cb",
	} {
		_, err := f.svc.StartGoogleSignIn(context.Background(), &GoogleAuthorizationRequest{RedirectURI: redirect})
		assert.ErrorIs(t, err, domain.ErrInvalidInput, redirect)
	}
}
