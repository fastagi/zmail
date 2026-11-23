package apikey

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fastagi/zmail/api/internal/capabilities/crypto"
	"github.com/fastagi/zmail/api/internal/capabilities/idgen"
	"github.com/fastagi/zmail/api/internal/domain"
	auditstarter "github.com/fastagi/zmail/api/internal/modules/audit"
)

// lastUsedAtThrottle skips the LastUsedAt write if the previous update is
// fresher than this window. Sub-minute precision on "last used" tracking
// is not useful and the write amplification on hot keys is real.
const lastUsedAtThrottle = time.Minute

// Issued key prefixes. A test key's prefix tells it apart at a glance. Keys issued earlier under
// the scaffold's `fastbuild_` prefixes keep working: validation looks a key up by its hash alone.
const (
	liveKeyPrefix = "zmail_"
	testKeyPrefix = "zmail_test_"
)

// The scopes a new key may carry: Zmail's mail and template scopes, or the `*` wildcard.
const (
	ScopeMailAgent         = "mail:agent"
	ScopeMailRead          = "mail:read"
	ScopeMailSend          = "mail:send"
	ScopeMailTransactional = "mail:transactional"
	// ScopeTemplatesCreate lets an AI tool save organization templates; it never sends, edits,
	// or deletes.
	ScopeTemplatesCreate = "templates:create"
	ScopeAll             = "*"
)

var knownScopes = []string{
	ScopeMailAgent, ScopeMailRead, ScopeMailSend, ScopeMailTransactional, ScopeTemplatesCreate,
}

// KnownScopes lists the named scopes a new key may carry, sorted.
func KnownScopes() []string {
	return slices.Clone(knownScopes)
}

const (
	MaxAPIKeyScopes      = 32
	MaxAPIKeyScopeLength = 65
)

var apiKeyScopePattern = regexp.MustCompile(`^(?:\*|[a-z][a-z0-9_-]{0,31}:[a-z][a-z0-9_-]{0,31})$`)

// Service defines API key operations.
type Service interface {
	CreateForUser(ctx context.Context, userID uint, req *APIKeyCreateRequest) (*CreateResult, error)
	ListForUser(ctx context.Context, userID uint, page, pageSize int) ([]*domain.APIKey, int64, error)
	RevokeForUser(ctx context.Context, userID, id uint) error
	Validate(ctx context.Context, plaintext string, requiredScopes ...string) (*domain.APIKey, error)
}

// CreateResult carries the persisted key metadata plus the one-time plaintext key.
type CreateResult struct {
	APIKey       *domain.APIKey
	PlaintextKey string
}

type service struct {
	repo domain.APIKeyRepository
	// sendingDomains confirms an organization binding and its domains; nil refuses every binding.
	sendingDomains domain.SendingDomainDirectory
}

// NewService creates a new API key service that issues only unrestricted keys.
func NewService(repo domain.APIKeyRepository) *service {
	return &service{repo: repo}
}

// ProvideService creates the API key service that may also bind keys to an organization and to
// its sending domains, as confirmed by sendingDomains.
func ProvideService(repo domain.APIKeyRepository, sendingDomains domain.SendingDomainDirectory) *service {
	return &service{repo: repo, sendingDomains: sendingDomains}
}

// MaxAPIKeySendingDomains bounds the domains one key may be restricted to.
const MaxAPIKeySendingDomains = 50

var sendingDomainPattern = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func (s *service) CreateForUser(ctx context.Context, userID uint, req *APIKeyCreateRequest) (*CreateResult, error) {
	if userID == 0 || req == nil {
		return nil, domain.ErrInvalidInput
	}

	name := strings.TrimSpace(req.Name)
	if name == "" || utf8.RuneCountInString(name) > 100 {
		return nil, domain.ErrInvalidInput
	}
	scopes, err := normalizeAPIKeyScopes(req.Scopes)
	if err != nil {
		return nil, err
	}
	// A key with no scope reaches nothing, and a scope no route checks is a typo.
	if len(scopes) == 0 {
		return nil, domain.ErrInvalidInput
	}
	for _, scope := range scopes {
		if scope != ScopeAll && !slices.Contains(knownScopes, scope) {
			return nil, domain.ErrInvalidInput
		}
	}
	if req.ExpiresAt != nil && !req.ExpiresAt.After(time.Now()) {
		return nil, domain.ErrInvalidInput
	}
	mode := domain.APIKeyModeLive
	if req.Mode != "" {
		mode = domain.APIKeyMode(req.Mode)
		if !mode.Valid() {
			return nil, domain.ErrInvalidInput
		}
	}

	organizationID, sendingDomains, err := s.resolveRestriction(ctx, userID, req)
	if err != nil {
		return nil, err
	}

	secret, err := crypto.GenerateKeyHex(24)
	if err != nil {
		return nil, fmt.Errorf("failed to generate api key secret: %w", err)
	}

	keyPrefix := liveKeyPrefix + strings.ToLower(idgen.ShortID())
	if mode == domain.APIKeyModeTest {
		keyPrefix = testKeyPrefix + strings.ToLower(idgen.ShortID())
	}
	plaintext := keyPrefix + "." + secret

	apiKey := &domain.APIKey{
		UserID:    userID,
		Name:      name,
		KeyPrefix: keyPrefix,
		KeyHash:   crypto.SHA256Hex(plaintext),
		Scopes:    scopes,
		Mode:      mode,
		ExpiresAt: req.ExpiresAt,
		// The binding is fixed at creation: a key never gains or loses a restriction later.
		OrganizationID: organizationID,
		SendingDomains: sendingDomains,
	}

	if err := s.repo.Create(ctx, apiKey); err != nil {
		return nil, fmt.Errorf("failed to create api key: %w", err)
	}

	auditstarter.RecordChange(ctx, auditstarter.Change{
		Action:     "create",
		Resource:   "api_keys",
		TargetType: "api_key",
		TargetID:   strconv.FormatUint(uint64(apiKey.ID), 10),
		Result:     domain.AuditResultSuccess,
		Changes: map[string]domain.AuditValueChange{
			"name":            {After: apiKey.Name},
			"scopes":          {After: append([]string(nil), apiKey.Scopes...)},
			"expires_at":      {After: apiKey.ExpiresAt},
			"mode":            {After: string(apiKey.Mode)},
			"organization_id": {After: apiKey.OrganizationID},
			"sending_domains": {After: apiKey.SendingDomains},
		},
	})

	return &CreateResult{
		APIKey:       apiKey,
		PlaintextKey: plaintext,
	}, nil
}

func (s *service) ListForUser(ctx context.Context, userID uint, page, pageSize int) ([]*domain.APIKey, int64, error) {
	if userID == 0 {
		return nil, 0, domain.ErrInvalidInput
	}
	return s.repo.FindByUserID(ctx, userID, page, pageSize)
}

func (s *service) RevokeForUser(ctx context.Context, userID, id uint) error {
	if userID == 0 || id == 0 {
		return domain.ErrInvalidInput
	}

	now := time.Now()
	changed, err := s.repo.Revoke(ctx, userID, id, now)
	if err != nil {
		if errors.Is(err, domain.ErrServiceUnavailable) || errors.Is(err, domain.ErrAPIKeyNotFound) {
			return err
		}
		return fmt.Errorf("failed to revoke api key: %w", err)
	}
	if !changed {
		return nil
	}

	auditstarter.RecordChange(ctx, auditstarter.Change{
		Action:     "revoke",
		Resource:   "api_keys",
		TargetType: "api_key",
		TargetID:   strconv.FormatUint(uint64(id), 10),
		Result:     domain.AuditResultSuccess,
		Changes: map[string]domain.AuditValueChange{
			"revoked_at": {After: now},
		},
	})
	return nil
}

func (s *service) Validate(ctx context.Context, plaintext string, requiredScopes ...string) (*domain.APIKey, error) {
	plaintext = strings.TrimSpace(plaintext)
	if plaintext == "" {
		return nil, domain.ErrAPIKeyInvalid
	}

	key, err := s.repo.FindByHash(ctx, crypto.SHA256Hex(plaintext))
	if err != nil {
		if errors.Is(err, domain.ErrServiceUnavailable) {
			return nil, err
		}
		return nil, domain.ErrAPIKeyInvalid
	}

	now := time.Now()
	if key.RevokedAt != nil {
		return nil, domain.ErrAPIKeyRevoked
	}
	if key.ExpiresAt != nil && key.ExpiresAt.Before(now) {
		return nil, domain.ErrAPIKeyExpired
	}
	normalizedRequiredScopes, err := normalizeAPIKeyScopes(requiredScopes)
	if err != nil {
		return nil, fmt.Errorf("invalid required api key scope configuration: %w", err)
	}
	for _, scope := range normalizedRequiredScopes {
		if !key.HasScope(scope) {
			return nil, domain.ErrPermissionDenied
		}
	}

	if key.LastUsedAt == nil || now.Sub(*key.LastUsedAt) >= lastUsedAtThrottle {
		if err := s.repo.RecordUse(ctx, key.ID, now, now.Add(-lastUsedAtThrottle)); err != nil {
			// Auth already succeeded; degrade gracefully on the write.
			log.Printf("apikey: failed to update LastUsedAt for key %d: %v", key.ID, err)
		} else {
			key.LastUsedAt = &now
		}
	}

	return key, nil
}

// resolveRestriction checks an optional binding: the owner must be a member of the organization,
// and every domain must be one the organization's mailboxes may send from. Domains need an
// organization, and a domain list, when given, must name at least one domain.
func (s *service) resolveRestriction(ctx context.Context, userID uint, req *APIKeyCreateRequest) (*uint, []string, error) {
	if req.OrganizationID == nil {
		if req.SendingDomains != nil {
			return nil, nil, domain.ErrInvalidInput
		}
		return nil, nil, nil
	}
	organizationID := *req.OrganizationID
	if organizationID == 0 {
		return nil, nil, domain.ErrInvalidInput
	}
	var requested []string
	if req.SendingDomains != nil {
		normalized, err := normalizeSendingDomains(req.SendingDomains)
		if err != nil {
			return nil, nil, err
		}
		requested = normalized
	}
	if s.sendingDomains == nil {
		return nil, nil, domain.ErrServiceUnavailable
	}
	available, err := s.sendingDomains.SendingDomains(ctx, organizationID, userID)
	if err != nil {
		return nil, nil, err
	}
	for _, name := range requested {
		if !slices.Contains(available, name) {
			return nil, nil, domain.ErrInvalidInput
		}
	}
	return &organizationID, requested, nil
}

// normalizeSendingDomains lowercases, trims a trailing dot, removes duplicates, and sorts.
func normalizeSendingDomains(domains []string) ([]string, error) {
	if len(domains) == 0 || len(domains) > MaxAPIKeySendingDomains {
		return nil, domain.ErrInvalidInput
	}
	normalized := make([]string, 0, len(domains))
	for _, name := range domains {
		name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
		if len(name) > 253 || !sendingDomainPattern.MatchString(name) {
			return nil, domain.ErrInvalidInput
		}
		if !slices.Contains(normalized, name) {
			normalized = append(normalized, name)
		}
	}
	slices.Sort(normalized)
	return normalized, nil
}

func normalizeAPIKeyScopes(scopes []string) ([]string, error) {
	if len(scopes) > MaxAPIKeyScopes {
		return nil, domain.ErrInvalidInput
	}

	normalized := make([]string, 0, len(scopes))
	seen := make(map[string]struct{}, len(scopes))

	for _, scope := range scopes {
		scope = strings.ToLower(strings.TrimSpace(scope))
		if scope == "" || len(scope) > MaxAPIKeyScopeLength || !apiKeyScopePattern.MatchString(scope) {
			return nil, domain.ErrInvalidInput
		}
		if _, exists := seen[scope]; exists {
			continue
		}
		seen[scope] = struct{}{}
		normalized = append(normalized, scope)
	}

	slices.Sort(normalized)
	return normalized, nil
}
