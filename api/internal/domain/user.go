package domain

import (
	"context"
	"time"
)

// User represents the core domain entity.
// JSON tags control API output - Password is hidden by default.
//
// For most cases, just use response.Success(c, user) directly.
// The Password field is automatically excluded from JSON output.
type User struct {
	ID        uint       `json:"id"`
	Username  string     `json:"username"`
	Email     string     `json:"email"`
	Password  string     `json:"-"` // Always hidden in JSON output
	Nickname  string     `json:"nickname,omitempty"`
	Avatar    string     `json:"avatar,omitempty"`
	Phone     string     `json:"phone,omitempty"`
	Bio       string     `json:"bio,omitempty"`
	Status    int        `json:"status"`
	LastLogin *time.Time `json:"last_login,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// IsActive returns whether the user account is active
func (u *User) IsActive() bool {
	return u.Status == 1
}

// UserRepository defines the contract for user data operations
// Implementations live in modules/user/repository.go
type UserRepository interface {
	Create(ctx context.Context, user *User) error
	Update(ctx context.Context, user *User) error
	Delete(ctx context.Context, id uint) error
	FindByID(ctx context.Context, id uint) (*User, error)
	FindByEmail(ctx context.Context, email string) (*User, error)
	FindByUsername(ctx context.Context, username string) (*User, error)
	FindAll(ctx context.Context, page, pageSize int) ([]*User, int64, error)
}

// UserListFilter narrows an administrative user listing. Query matches username or email
// substrings; a nil Active lists every account.
type UserListFilter struct {
	Query  string
	Active *bool
}

// UserAdministrator is the user starter's seam for platform operators. The user starter remains the
// only writer of accounts and sessions; disabling an account revokes its sessions atomically.
// UserProvisionRequest names the account an operator provisions from the console.
type UserProvisionRequest struct {
	Email string
	// Username is optional; empty derives one from the email's local part.
	Username string
	// Password is the plaintext password, read from standard input and never from arguments.
	Password string
}

// UserProvisioner creates or resets an account from the operator console. It never sends mail.
type UserProvisioner interface {
	// ProvisionUser creates an active account, or, when the email already has one, replaces its
	// password, re-enables it, and revokes its sessions. created reports which happened.
	ProvisionUser(ctx context.Context, req UserProvisionRequest) (user *User, created bool, err error)
}

type UserAdministrator interface {
	ListUsers(ctx context.Context, filter UserListFilter, page, pageSize int) ([]*User, int64, error)
	GetUser(ctx context.Context, id uint) (*User, error)
	// FindUserByEmail returns the live account whose email matches ignoring case, without its
	// password hash, or ErrUserNotFound.
	FindUserByEmail(ctx context.Context, email string) (*User, error)
	SetUserActive(ctx context.Context, id uint, active bool) (*User, error)
	RevokeUserSessions(ctx context.Context, id uint) error
}
