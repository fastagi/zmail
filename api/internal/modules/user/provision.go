package user

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/fastagi/zmail/api/internal/domain"
)

const (
	// minProvisionPasswordLength is stricter than public sign-up: provisioned accounts are often
	// privileged.
	minProvisionPasswordLength = 12
	// maxProvisionPasswordBytes is bcrypt's input limit.
	maxProvisionPasswordBytes = 72
	minUsernameLength         = 3
	maxUsernameLength         = 50
	maxProvisionEmailLength   = 100
)

var (
	_ domain.UserProvisioner = (*service)(nil)

	errProvisionEmail    = errors.New("email must be a plain address such as name@example.com")
	errProvisionPassword = fmt.Errorf("password must be %d to %d bytes", minProvisionPasswordLength, maxProvisionPasswordBytes)
	errProvisionUsername = fmt.Errorf("username must be %d to %d characters without @", minUsernameLength, maxUsernameLength)
)

// ProvisionUser creates an active account for req.Email, or, when one exists, replaces its password,
// re-enables it, and revokes its sessions. It serves the operator console only and publishes no
// UserCreated event, so no welcome mail is sent.
func (s *service) ProvisionUser(ctx context.Context, req domain.UserProvisionRequest) (*domain.User, bool, error) {
	email, err := normalizeProvisionEmail(req.Email)
	if err != nil {
		return nil, false, err
	}
	if n := len(req.Password); n < minProvisionPasswordLength || n > maxProvisionPasswordBytes {
		return nil, false, errProvisionPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, false, fmt.Errorf("failed to hash password: %w", err)
	}

	existing, err := s.FindUserByEmail(ctx, email)
	switch {
	case err == nil:
		return s.resetProvisionedUser(ctx, existing, string(hash))
	case !errors.Is(err, domain.ErrUserNotFound):
		return nil, false, err
	}

	username := strings.TrimSpace(req.Username)
	if username == "" {
		username = provisionUsername(email)
	}
	if n := len([]rune(username)); n < minUsernameLength || n > maxUsernameLength || strings.Contains(username, "@") {
		return nil, false, errProvisionUsername
	}
	user, err := s.createAccount(ctx, &UserRegisterRequest{Username: username, Email: email, Nickname: username}, string(hash), nil)
	if err != nil {
		return nil, false, err
	}
	user.Password = ""
	return user, true, nil
}

func (s *service) resetProvisionedUser(ctx context.Context, user *domain.User, hash string) (*domain.User, bool, error) {
	if s.admin == nil {
		return nil, false, domain.ErrServiceUnavailable
	}
	now := time.Now().UTC()
	if err := s.repo.UpdatePasswordAndRevokeSessions(ctx, user.ID, hash, now); err != nil {
		return nil, false, err
	}
	if !user.IsActive() {
		if err := s.admin.setUserStatus(ctx, user.ID, userStatusActive, now); err != nil {
			return nil, false, err
		}
	}
	refreshed, err := s.GetUser(ctx, user.ID)
	if err != nil {
		return nil, false, err
	}
	refreshed.Password = ""
	return refreshed, false, nil
}

func normalizeProvisionEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if email == "" || len(email) > maxProvisionEmailLength {
		return "", errProvisionEmail
	}
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email || parsed.Name != "" {
		return "", errProvisionEmail
	}
	return email, nil
}

// provisionUsername derives a username from the local part, padding a short one with the domain's
// first label, e.g. z@zmail.im becomes z-zmail.
func provisionUsername(email string) string {
	local, domainPart, _ := strings.Cut(email, "@")
	if len([]rune(local)) >= minUsernameLength {
		return local
	}
	label, _, _ := strings.Cut(domainPart, ".")
	return local + "-" + label
}
