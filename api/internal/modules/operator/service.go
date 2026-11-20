package operator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
)

// userLookup is the read-only view of accounts the operator starter needs. The user starter remains
// the only writer of user records.
type userLookup interface {
	FindByID(ctx context.Context, id uint) (*domain.User, error)
	FindByEmail(ctx context.Context, email string) (*domain.User, error)
}

type service struct {
	grants        grantStore
	users         userLookup
	signIn        domain.CredentialSignIn
	authenticator domain.SessionAuthenticator
	revoker       domain.SessionRevoker
	admin         domain.UserAdministrator
	audit         domain.AuditLogQuery
	now           func() time.Time
}

var _ domain.OperatorGrantStore = (*service)(nil)

// NewService creates the operator service from user-starter seams and the grant store.
func NewService(
	grants grantStore,
	users domain.UserRepository,
	signIn domain.CredentialSignIn,
	authenticator domain.SessionAuthenticator,
	revoker domain.SessionRevoker,
	admin domain.UserAdministrator,
	auditQuery domain.AuditLogQuery,
) *service {
	return &service{
		grants:        grants,
		users:         users,
		signIn:        signIn,
		authenticator: authenticator,
		revoker:       revoker,
		admin:         admin,
		audit:         auditQuery,
		now:           time.Now,
	}
}

// SignIn verifies credentials and issues a session only for a current operator of any scope. A
// verified non-operator is rejected before any session exists.
func (s *service) SignIn(ctx context.Context, identifier, password string) (*domain.IssuedSession, domain.OperatorScope, error) {
	if s.signIn == nil {
		return nil, "", domain.ErrServiceUnavailable
	}
	var scope domain.OperatorScope
	issued, err := s.signIn.SignIn(ctx, identifier, password, domain.SessionAudienceOperator, func(ctx context.Context, user *domain.User) error {
		granted, grantErr := s.requireOperator(ctx, user.ID)
		scope = granted
		return grantErr
	})
	if err != nil {
		return nil, "", err
	}
	return issued, scope, nil
}

// Authenticate resolves a session credential to a current operator account and its grant's scope.
func (s *service) Authenticate(ctx context.Context, credential string) (*domain.User, domain.OperatorScope, error) {
	if s.authenticator == nil {
		return nil, "", domain.ErrServiceUnavailable
	}
	identity, err := s.authenticator.Authenticate(ctx, credential)
	if err != nil {
		return nil, "", err
	}
	if identity == nil || identity.Audience != domain.SessionAudienceOperator {
		return nil, "", domain.ErrAuthenticationRequired
	}
	scope, grantErr := s.requireOperator(ctx, identity.UserID)
	if grantErr != nil {
		return nil, "", grantErr
	}
	user, err := s.users.FindByID(ctx, identity.UserID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", domain.ErrAuthenticationRequired
		}
		return nil, "", fmt.Errorf("load operator account: %w", err)
	}
	return user, scope, nil
}

// SignOut ends the presented session. Unknown or already revoked sessions succeed.
func (s *service) SignOut(ctx context.Context, credential string) error {
	if s.revoker == nil {
		return domain.ErrServiceUnavailable
	}
	return s.revoker.RevokeSession(ctx, credential)
}

// requireOperator returns the scope of the user's current grant. An unknown stored scope allows
// nothing and is treated as no grant.
func (s *service) requireOperator(ctx context.Context, userID uint) (domain.OperatorScope, error) {
	scope, found, err := s.grants.grantScope(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("check operator grant: %w", err)
	}
	if !found || !scope.Valid() {
		return "", domain.ErrOperatorForbidden
	}
	return scope, nil
}

// GrantOperator makes the account with this email a platform operator with the given scope, or
// changes the scope of its grant. Repeating it is a no-op.
func (s *service) GrantOperator(
	ctx context.Context,
	email string,
	scope domain.OperatorScope,
) (*domain.OperatorGrant, domain.OperatorGrantChange, error) {
	if !scope.Valid() {
		return nil, "", domain.ErrInvalidInput
	}
	user, err := s.findByEmail(ctx, email)
	if err != nil {
		return nil, "", err
	}
	change, grantedAt, err := s.grants.upsertGrant(ctx, user.ID, scope, s.now())
	if err != nil {
		return nil, "", err
	}
	return &domain.OperatorGrant{
		UserID:    user.ID,
		Username:  user.Username,
		Email:     user.Email,
		Scope:     scope,
		GrantedAt: grantedAt,
	}, change, nil
}

// RevokeOperator removes the grant for this email. Repeating it is a no-op.
func (s *service) RevokeOperator(ctx context.Context, email string) (uint, bool, error) {
	user, err := s.findByEmail(ctx, email)
	if err != nil {
		return 0, false, err
	}
	removed, err := s.grants.deleteGrant(ctx, user.ID)
	return user.ID, removed, err
}

// ListOperators returns every current grant, oldest first.
func (s *service) ListOperators(ctx context.Context) ([]domain.OperatorGrant, error) {
	rows, err := s.grants.listGrants(ctx)
	if err != nil {
		return nil, err
	}
	grants := make([]domain.OperatorGrant, 0, len(rows))
	for _, row := range rows {
		grant := domain.OperatorGrant{UserID: row.UserID, Scope: domain.OperatorScope(row.Scope), GrantedAt: row.GrantedAt}
		if user, err := s.users.FindByID(ctx, row.UserID); err == nil {
			grant.Username = user.Username
			grant.Email = user.Email
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("load operator account %d: %w", row.UserID, err)
		}
		grants = append(grants, grant)
	}
	return grants, nil
}

func (s *service) findByEmail(ctx context.Context, email string) (*domain.User, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return nil, domain.ErrUserNotFound
	}
	user, err := s.users.FindByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, domain.ErrUserNotFound
		}
		return nil, fmt.Errorf("find account by email: %w", err)
	}
	return user, nil
}
