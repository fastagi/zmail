package mailbox

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	auditstarter "github.com/fastagi/zmail/api/internal/modules/audit"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/ratelimit"
)

const (
	// MaxAppPasswordsPerMember bounds the mail client passwords one member holds per organization.
	MaxAppPasswordsPerMember = 20
	// MaxAppPasswordNameLength bounds a device name, in characters.
	MaxAppPasswordNameLength = 64
	// AppPasswordRevokedRetention is how long a revoked password stays in the directory before the
	// mail worker deletes it.
	AppPasswordRevokedRetention = 90 * 24 * time.Hour
	// appPasswordAttemptLimit failed credential checks per user per appPasswordAttemptWindow.
	appPasswordAttemptLimit  = 5
	appPasswordAttemptWindow = 15 * time.Minute
	ruleAppPasswordAttempts  = "mailbox_app_password_attempts"
	// appPasswordLetters is the secret's length; it is shown in groups of appPasswordGroup.
	appPasswordLetters = 16
	appPasswordGroup   = 4
	// MailClientIMAPSecurity is the IMAP connection security shown to members: TLS from the start.
	MailClientIMAPSecurity = "ssl_tls"
)

// Reasons a member's mail apps cannot sign in, in the order they are checked.
const (
	MailClientAccessUnavailable          = "unavailable"
	MailClientAccessOrganizationDisabled = "organization_disabled"
	MailClientAccessFeatureNotGranted    = "feature_not_granted"
)

// CreateAppPasswordInput creates a mail client password after confirming the account credentials.
type CreateAppPasswordInput struct {
	Name            string
	Scopes          []domain.MailClientProtocol
	CurrentPassword string
	TOTPCode        string
}

// MailClientAccess says whether the viewer's mail apps can sign in, and why not.
type MailClientAccess struct {
	Enabled bool
	Reason  string
}

// MailClientServer is the public IMAP server mail apps connect to.
type MailClientServer struct {
	IMAPHost     string
	IMAPPort     int
	IMAPSecurity string
}

// AppPasswordList is the viewer's own passwords with where and whether mail apps sign in.
type AppPasswordList struct {
	Items  []*domain.MailAppPassword
	Limit  int
	Access MailClientAccess
	Server MailClientServer
}

func newAppPasswordAttempts(stores *ratelimit.Stores) ratelimit.Store {
	if stores == nil {
		stores = ratelimit.NewMemoryStores()
	}
	return stores.New(ratelimit.Rule{Name: ruleAppPasswordAttempts, Max: appPasswordAttemptLimit, Window: appPasswordAttemptWindow})
}

func (s *service) appPasswordStore() (appPasswordStore, error) {
	if s.appPasswords == nil {
		return nil, domain.ErrServiceUnavailable
	}
	return s.appPasswords, nil
}

// ListAppPasswords lists the viewer's own passwords; it needs neither the feature nor the switch,
// so a member can always see and remove a device.
func (s *service) ListAppPasswords(ctx context.Context, viewer domain.OrganizationContext) (*AppPasswordList, error) {
	store, err := s.appPasswordStore()
	if err != nil {
		return nil, err
	}
	items, err := store.ListAppPasswords(ctx, viewer.OrganizationID, viewer.MembershipID)
	if err != nil {
		return nil, err
	}
	settings, err := store.FindMailClientSettings(ctx, viewer.OrganizationID)
	if err != nil {
		return nil, err
	}
	access := MailClientAccess{Enabled: true}
	switch {
	case s.settings.AppPasswordPepper == "":
		access = MailClientAccess{Reason: MailClientAccessUnavailable}
	case !settings.AccessEnabled:
		access = MailClientAccess{Reason: MailClientAccessOrganizationDisabled}
	case !viewer.CanUse(domain.MemberFeatureMailClients):
		access = MailClientAccess{Reason: MailClientAccessFeatureNotGranted}
	}
	return &AppPasswordList{
		Items: items, Limit: MaxAppPasswordsPerMember, Access: access,
		Server: MailClientServer{IMAPHost: s.settings.ClientIMAPHost, IMAPPort: s.settings.ClientIMAPPort, IMAPSecurity: MailClientIMAPSecurity},
	}, nil
}

// CreateAppPassword creates a password for the viewer and returns it with its secret, which is
// never readable again. The feature and the organization switch are checked before the
// credentials, so a refused member never spends their attempt budget.
func (s *service) CreateAppPassword(ctx context.Context, viewer domain.OrganizationContext, input CreateAppPasswordInput) (*domain.MailAppPassword, string, error) {
	store, err := s.appPasswordStore()
	if err != nil {
		return nil, "", err
	}
	if !viewer.CanUse(domain.MemberFeatureMailClients) {
		return nil, "", domain.ErrOrganizationFeatureNotGranted
	}
	settings, err := store.FindMailClientSettings(ctx, viewer.OrganizationID)
	if err != nil {
		return nil, "", err
	}
	if !settings.AccessEnabled {
		return nil, "", ErrMailClientAccessDisabled
	}
	if s.settings.AppPasswordPepper == "" {
		return nil, "", ErrMailAppPasswordPepperMissing
	}
	name, imap, smtp, err := validateAppPasswordInput(input)
	if err != nil {
		return nil, "", err
	}
	if err := s.reverifyForAppPassword(ctx, viewer, input); err != nil {
		return nil, "", err
	}

	display, normalized := newAppPasswordSecret()
	password := &domain.MailAppPassword{
		OrganizationID: viewer.OrganizationID, MembershipID: viewer.MembershipID, UserID: viewer.UserID,
		Name: name, ScopeIMAP: imap, ScopeSMTP: smtp, Hint: normalized[appPasswordLetters-appPasswordGroup:],
		CreatedAt: s.now().UTC(),
	}
	if err := store.CreateAppPassword(ctx, password, hashAppPassword([]byte(s.settings.AppPasswordPepper), normalized), MaxAppPasswordsPerMember); err != nil {
		return nil, "", err
	}
	scopes := make([]string, 0, 2)
	for _, scope := range password.Scopes() {
		scopes = append(scopes, string(scope))
	}
	s.auditChange(ctx, auditstarter.Change{
		Action: "create", Resource: "mail_app_passwords", TargetType: "mail_app_password",
		TargetID: strconv.FormatUint(uint64(password.ID), 10), Result: domain.AuditResultSuccess,
		Metadata: map[string]any{"name_length": utf8.RuneCountInString(name), "scopes": scopes},
	})
	return password, display, nil
}

// reverifyForAppPassword spends one attempt of the user's budget and confirms the credentials; a
// success gives the budget back.
func (s *service) reverifyForAppPassword(ctx context.Context, viewer domain.OrganizationContext, input CreateAppPasswordInput) error {
	if s.reverifier == nil {
		return domain.ErrServiceUnavailable
	}
	bucket := "user:" + strconv.FormatUint(uint64(viewer.UserID), 10)
	if s.appPasswordAttempts != nil {
		if allowed, _, resetAt := s.appPasswordAttempts.Take(ctx, bucket); !allowed {
			return &RetryAfterError{Err: ErrMailAppPasswordAttemptsLimited, RetryAfter: time.Until(resetAt)}
		}
	}
	err := s.reverifier.ReverifyCredentials(ctx, viewer.UserID, input.CurrentPassword, strings.TrimSpace(input.TOTPCode))
	validation := &ValidationError{}
	switch {
	case err == nil:
		if s.appPasswordAttempts != nil {
			s.appPasswordAttempts.Reset(ctx, bucket)
		}
		return nil
	case errors.Is(err, domain.ErrInvalidCredentials):
		validation.add("current_password", "is incorrect")
	case errors.Is(err, domain.ErrTOTPRequired):
		validation.add("totp_code", "is required")
	case errors.Is(err, domain.ErrTOTPInvalid):
		validation.add("totp_code", "is incorrect")
	default:
		return err
	}
	return validation
}

// validateAppPasswordInput trims the name and reads the scopes, answering every field error at once.
func validateAppPasswordInput(input CreateAppPasswordInput) (name string, imap, smtp bool, err error) {
	validation := &ValidationError{}
	name = strings.TrimSpace(input.Name)
	switch length := utf8.RuneCountInString(name); {
	case length == 0:
		validation.add("name", "is required")
	case length > MaxAppPasswordNameLength:
		validation.add("name", "must be at most 64 characters")
	case strings.ContainsFunc(name, unicode.IsControl):
		validation.add("name", "must not contain control characters")
	}
	if len(input.Scopes) == 0 {
		validation.add("scopes", "is required")
	}
	for _, scope := range input.Scopes {
		switch {
		case !scope.IsValid():
			validation.add("scopes", "must be imap or smtp")
		case (scope == domain.MailClientProtocolIMAP && imap) || (scope == domain.MailClientProtocolSMTP && smtp):
			validation.add("scopes", "must not repeat a protocol")
		case scope == domain.MailClientProtocolIMAP:
			imap = true
		default:
			smtp = true
		}
	}
	if input.CurrentPassword == "" {
		validation.add("current_password", "is required")
	}
	return name, imap, smtp, validation.orNil()
}

// RevokeAppPassword revokes one of the viewer's own passwords.
func (s *service) RevokeAppPassword(ctx context.Context, viewer domain.OrganizationContext, id uint) error {
	store, err := s.appPasswordStore()
	if err != nil {
		return err
	}
	password, err := store.RevokeAppPassword(ctx, appPasswordRevocation{
		OrganizationID: viewer.OrganizationID, MembershipID: viewer.MembershipID, ID: id,
		RevokedBy: viewer.UserID, Reason: domain.MailAppPasswordRevokedByUser, At: s.now().UTC(),
	})
	if err != nil {
		return err
	}
	s.auditRevocation(ctx, password, false)
	return nil
}

// AppPasswordDirectory pages every password of the organization for its owners and admins.
func (s *service) AppPasswordDirectory(ctx context.Context, viewer domain.OrganizationContext, page, perPage int) ([]*domain.MailAppPassword, int64, error) {
	if !viewer.Role.CanManageOrganization() {
		return nil, 0, domain.ErrPermissionDenied
	}
	store, err := s.appPasswordStore()
	if err != nil {
		return nil, 0, err
	}
	page, perPage = max(page, 1), min(max(perPage, 1), 100)
	passwords, total, err := store.AppPasswordDirectory(ctx, viewer.OrganizationID,
		s.now().UTC().Add(-AppPasswordRevokedRetention), (page-1)*perPage, perPage)
	if err != nil || len(passwords) == 0 || s.memberships == nil {
		return passwords, total, err
	}
	userIDs := make([]uint, 0, len(passwords))
	for _, password := range passwords {
		userIDs = append(userIDs, password.UserID)
	}
	memberships, err := s.memberships.ListMembershipsByUserIDs(ctx, viewer.OrganizationID, userIDs)
	if err != nil {
		return nil, 0, err
	}
	accounts := make(map[uint]*domain.User, len(memberships))
	for _, membership := range memberships {
		if membership.User != nil {
			accounts[membership.UserID] = membership.User
		}
	}
	// A password ends with its membership, so every owner is a member; the name is the nickname,
	// or the username without one, as in the other directories.
	for _, password := range passwords {
		if account, ok := accounts[password.UserID]; ok {
			password.UserEmail = account.Email
			password.UserName = strings.TrimSpace(account.Nickname)
			if password.UserName == "" {
				password.UserName = account.Username
			}
		}
	}
	return passwords, total, nil
}

// ForceRevokeAppPassword revokes any member's password of the organization; owners and admins only.
func (s *service) ForceRevokeAppPassword(ctx context.Context, viewer domain.OrganizationContext, id uint) error {
	if !viewer.Role.CanManageOrganization() {
		return domain.ErrPermissionDenied
	}
	store, err := s.appPasswordStore()
	if err != nil {
		return err
	}
	password, err := store.RevokeAppPassword(ctx, appPasswordRevocation{
		OrganizationID: viewer.OrganizationID, ID: id,
		RevokedBy: viewer.UserID, Reason: domain.MailAppPasswordRevokedByAdmin, At: s.now().UTC(),
	})
	if err != nil {
		return err
	}
	s.auditRevocation(ctx, password, true)
	return nil
}

func (s *service) auditRevocation(ctx context.Context, password *domain.MailAppPassword, byAdmin bool) {
	s.auditChange(ctx, auditstarter.Change{
		Action: "delete", Resource: "mail_app_passwords", TargetType: "mail_app_password",
		TargetID: strconv.FormatUint(uint64(password.ID), 10), Result: domain.AuditResultSuccess,
		Metadata: map[string]any{"by_admin": byAdmin, "membership_id": password.MembershipID},
	})
}

// MailClientSettings reads the organization switch; every member may read it.
func (s *service) MailClientSettings(ctx context.Context, viewer domain.OrganizationContext) (MailClientSettings, error) {
	store, err := s.appPasswordStore()
	if err != nil {
		return MailClientSettings{}, err
	}
	return store.FindMailClientSettings(ctx, viewer.OrganizationID)
}

// UpdateMailClientSettings turns mail app sign-in on or off for the organization; owners and admins only.
func (s *service) UpdateMailClientSettings(ctx context.Context, viewer domain.OrganizationContext, enabled bool) (MailClientSettings, error) {
	if !viewer.Role.CanManageOrganization() {
		return MailClientSettings{}, domain.ErrPermissionDenied
	}
	store, err := s.appPasswordStore()
	if err != nil {
		return MailClientSettings{}, err
	}
	return store.SaveMailClientSettings(ctx, viewer.OrganizationID, enabled, viewer.UserID, s.now().UTC())
}

var _ domain.MailAppPasswordPruner = (*service)(nil)

// PurgeRevokedAppPasswords deletes up to limit passwords revoked longer than their retention.
func (s *service) PurgeRevokedAppPasswords(ctx context.Context, now time.Time, limit int) (int, error) {
	if s.appPasswords == nil {
		return 0, nil
	}
	return s.appPasswords.PurgeRevokedAppPasswords(ctx, now.Add(-AppPasswordRevokedRetention), limit)
}

// newAppPasswordSecret draws 16 uniformly random lowercase letters and returns them shown in
// groups of four and normalized.
func newAppPasswordSecret() (display, normalized string) {
	letters := make([]byte, 0, appPasswordLetters)
	buffer := make([]byte, 32)
	for len(letters) < appPasswordLetters {
		if _, err := rand.Read(buffer); err != nil {
			panic("mailbox: crypto/rand failed: " + err.Error())
		}
		for _, b := range buffer {
			// 234 is the largest multiple of 26 below 256: rejecting the rest keeps letters uniform.
			if b < 234 && len(letters) < appPasswordLetters {
				letters = append(letters, 'a'+b%26)
			}
		}
	}
	normalized = string(letters)
	groups := make([]string, 0, appPasswordLetters/appPasswordGroup)
	for i := 0; i < appPasswordLetters; i += appPasswordGroup {
		groups = append(groups, normalized[i:i+appPasswordGroup])
	}
	return strings.Join(groups, " "), normalized
}

// normalizeAppPassword drops whitespace and lowers the case, so mail apps may send the secret
// with or without its spaces.
func normalizeAppPassword(secret string) string {
	return strings.ToLower(strings.Join(strings.Fields(secret), ""))
}

// hashAppPassword is the stored, keyed hash of a normalized secret: hex HMAC-SHA256 with the pepper.
func hashAppPassword(pepper []byte, normalized string) string {
	mac := hmac.New(sha256.New, pepper)
	mac.Write([]byte(normalized))
	return hex.EncodeToString(mac.Sum(nil))
}
