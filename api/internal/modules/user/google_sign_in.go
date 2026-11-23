package user

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	capcrypto "github.com/fastagi/zmail/api/internal/capabilities/crypto"
	"github.com/fastagi/zmail/api/internal/domain"
)

const (
	// googleSignInStateTTL is how long a started Google sign-in can be completed.
	googleSignInStateTTL = 10 * time.Minute
	// googleSignInDefaultReturnURL is where a sign-in without a usable return URL lands; it is the
	// Web shell's default landing page.
	googleSignInDefaultReturnURL = "/console"
	googleMaxReturnURLLength     = 2048
	googleMaxRedirectURILength   = 2048
	googleMaxStateLength         = 128
	googleMaxCodeLength          = 2048
	googleMaxNicknameLength      = 50
)

// GoogleSignInService is "Sign in with Google": start an authorization, then turn Google's answer
// into an ordinary user session.
type GoogleSignInService interface {
	GoogleSignInEnabled() bool
	StartGoogleSignIn(ctx context.Context, req *GoogleAuthorizationRequest) (*GoogleAuthorizationResponse, error)
	CompleteGoogleSignIn(ctx context.Context, req *GoogleSessionRequest) (*GoogleSessionResponse, error)
}

var _ GoogleSignInService = (*service)(nil)

// GoogleSignInEnabled reports whether the OAuth client is configured.
func (s *service) GoogleSignInEnabled() bool {
	return s.google != nil && s.googleStore != nil
}

// StartGoogleSignIn stores a pending sign-in (state and nonce digests, the PKCE verifier, the
// redirect URI, and the checked return URL) and returns Google's authorization URL with the state.
func (s *service) StartGoogleSignIn(ctx context.Context, req *GoogleAuthorizationRequest) (*GoogleAuthorizationResponse, error) {
	if !s.GoogleSignInEnabled() {
		return nil, domain.ErrGoogleNotConfigured
	}
	redirectURI, ok := validGoogleRedirectURI(req.RedirectURI, s.production)
	if !ok {
		return nil, fmt.Errorf("%w: redirect_uri must be an absolute https URL", domain.ErrInvalidInput)
	}
	state, errState := randomGoogleToken()
	nonce, errNonce := randomGoogleToken()
	verifier, errVerifier := randomGoogleToken()
	if err := errors.Join(errState, errNonce, errVerifier); err != nil {
		return nil, fmt.Errorf("generate google sign-in secrets: %w", err)
	}
	now := time.Now()
	if err := s.googleStore.SaveOAuthSignInState(ctx, &OAuthSignInStatePO{
		StateHash: capcrypto.SHA256Hex(state), Provider: identityProviderGoogle,
		NonceHash: capcrypto.SHA256Hex(nonce), CodeVerifier: verifier,
		RedirectURI: redirectURI, ReturnURL: normalizeGoogleReturnURL(req.ReturnURL),
		ExpiresAt: now.Add(googleSignInStateTTL), CreatedAt: now,
	}, now); err != nil {
		return nil, fmt.Errorf("store google sign-in state: %w", err)
	}
	return &GoogleAuthorizationResponse{
		AuthorizationURL: s.google.authURL(state, nonce, googlePKCEChallenge(verifier), redirectURI),
		State:            state,
		ExpiresIn:        int64(googleSignInStateTTL / time.Second),
	}, nil
}

// CompleteGoogleSignIn consumes the state, exchanges the code, verifies the ID token, resolves the
// account (subject, then verified email, then a new account), and issues a session through the same
// path as a password login.
func (s *service) CompleteGoogleSignIn(ctx context.Context, req *GoogleSessionRequest) (*GoogleSessionResponse, error) {
	if !s.GoogleSignInEnabled() {
		return nil, domain.ErrGoogleNotConfigured
	}
	state := strings.TrimSpace(req.State)
	code := strings.TrimSpace(req.Code)
	if state == "" || len(state) > googleMaxStateLength || code == "" || len(code) > googleMaxCodeLength {
		return nil, domain.ErrGoogleStateInvalid
	}
	now := time.Now()
	pending, err := s.googleStore.ConsumeOAuthSignInState(ctx, identityProviderGoogle, capcrypto.SHA256Hex(state), now)
	if err != nil {
		return nil, fmt.Errorf("consume google sign-in state: %w", err)
	}
	if pending == nil {
		return nil, domain.ErrGoogleStateInvalid
	}

	idToken, err := s.google.exchange(ctx, code, pending.CodeVerifier, pending.RedirectURI)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", domain.ErrGoogleSignInFailed, err)
	}
	claims, err := s.google.verifyIDToken(ctx, idToken, pending.NonceHash)
	if errors.Is(err, errGoogleNonceMismatch) {
		return nil, domain.ErrGoogleStateInvalid
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", domain.ErrGoogleSignInFailed, err)
	}
	if !claims.verified() {
		return nil, domain.ErrGoogleEmailUnverified
	}

	user, err := s.resolveGoogleUser(ctx, claims, now, true)
	if err != nil {
		return nil, err
	}
	issued, err := s.issueSignInSession(ctx, user, domain.SessionAudienceUser)
	if err != nil {
		return nil, err
	}
	return &GoogleSessionResponse{
		UserLoginResponse: UserLoginResponse{
			AccessToken: issued.AccessToken, TokenType: issued.TokenType, ExpiresIn: issued.ExpiresIn, User: user,
		},
		ReturnURL: pending.ReturnURL,
	}, nil
}

// resolveGoogleUser finds the account for a verified Google identity, linking or creating it. A
// refused account (disabled, two-factor) is checked before anything is written.
func (s *service) resolveGoogleUser(ctx context.Context, claims *googleIDClaims, now time.Time, retry bool) (*domain.User, error) {
	email := claims.normalizedEmail()
	userID, err := s.googleStore.FindIdentityUserID(ctx, identityProviderGoogle, claims.Subject)
	if err == nil {
		user, allowErr := s.googleSignInUser(ctx, userID)
		if allowErr != nil {
			return nil, allowErr
		}
		if recordErr := s.googleStore.RecordIdentitySignIn(ctx, identityProviderGoogle, claims.Subject, email, now); recordErr != nil {
			return nil, fmt.Errorf("record google sign-in: %w", recordErr)
		}
		return user, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("find google identity: %w", err)
	}

	userID, err = s.googleStore.FindUserIDByEmailFold(ctx, email)
	if err == nil {
		user, allowErr := s.googleSignInUser(ctx, userID)
		if allowErr != nil {
			return nil, allowErr
		}
		err = s.googleStore.LinkIdentity(ctx, user.ID, identityProviderGoogle, claims.Subject, email, now)
		if err == nil {
			if user.EmailVerifiedAt == nil {
				user.EmailVerifiedAt = &now
			}
			return user, nil
		}
		if retry && isGoogleIdentityConflict(err) {
			return s.resolveGoogleUser(ctx, claims, now, false)
		}
		return nil, fmt.Errorf("link google identity: %w", err)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("find account by google email: %w", err)
	}

	if !s.registrationOpen {
		return nil, domain.ErrGoogleRegistrationClosed
	}
	username, err := generatedGoogleUsername()
	if err != nil {
		return nil, err
	}
	po := &UserPO{
		Username: username, Email: email, Nickname: googleNickname(claims.Name),
		Status: 1, EmailVerifiedAt: &now,
	}
	if createErr := s.googleStore.CreateUserWithIdentity(ctx, po, identityProviderGoogle, claims.Subject, now); createErr != nil {
		if retry && isGoogleIdentityConflict(createErr) {
			// A concurrent sign-in for the same person created the account first.
			return s.resolveGoogleUser(ctx, claims, now, false)
		}
		if isGoogleIdentityConflict(createErr) {
			return nil, domain.ErrEmailAlreadyExists
		}
		return nil, fmt.Errorf("create google account: %w", createErr)
	}
	user := po.toDomain()
	if s.eventBus != nil {
		s.eventBus.PublishAsync(context.WithoutCancel(ctx), domain.NewUserCreatedEvent(user))
	}
	return user, nil
}

// googleSignInUser loads the account and applies the password login's refusals: a disabled account
// gets the same failure, and an account with two-factor sign-in must use its authenticator code.
func (s *service) googleSignInUser(ctx context.Context, userID uint) (*domain.User, error) {
	user, err := s.repo.FindByID(ctx, userID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrInvalidCredentials
	}
	if err != nil {
		return nil, fmt.Errorf("load google account: %w", err)
	}
	if !user.IsActive() {
		return nil, domain.ErrInvalidCredentials
	}
	if err := s.totp.checkSignIn(ctx, user.ID, ""); err != nil {
		return nil, err
	}
	return user, nil
}

// normalizeGoogleReturnURL keeps a same-origin path (one leading slash, no scheme or host) and
// falls back to the landing page for anything else.
func normalizeGoogleReturnURL(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" || len(value) > googleMaxReturnURLLength || !strings.HasPrefix(value, "/") ||
		strings.HasPrefix(value, "//") || strings.ContainsAny(value, "\\") || !utf8.ValidString(value) {
		return googleSignInDefaultReturnURL
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return googleSignInDefaultReturnURL
		}
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.User != nil {
		return googleSignInDefaultReturnURL
	}
	return value
}

// validGoogleRedirectURI accepts an absolute https URL without credentials or fragment (http only
// outside production). Google itself refuses any URI the OAuth client does not list.
func validGoogleRedirectURI(raw string, production bool) (string, bool) {
	value := strings.TrimSpace(raw)
	if value == "" || len(value) > googleMaxRedirectURILength {
		return "", false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || parsed.RawFragment != "" ||
		strings.Contains(value, "#") {
		return "", false
	}
	if parsed.Scheme != "https" && (production || parsed.Scheme != "http") {
		return "", false
	}
	return value, true
}

func randomGoogleToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func googlePKCEChallenge(verifier string) string {
	digest := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

// generatedGoogleUsername is a non-identifying username; the person signs in by email or Google.
func generatedGoogleUsername() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate username: %w", err)
	}
	return "user_" + hex.EncodeToString(raw), nil
}

func googleNickname(name string) string {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) <= googleMaxNicknameLength {
		return name
	}
	return string([]rune(name)[:googleMaxNicknameLength])
}
