package mailbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
	auditstarter "github.com/fastagi/zmail/api/internal/modules/audit"
	"github.com/fastagi/zmail/api/pkg/response"
)

const (
	// maxListedTenantPersonalConnections bounds the tenant assignments one member's own listing shows.
	maxListedTenantPersonalConnections = 100
	// DefaultTenantDirectoryPageSize and MaxTenantDirectoryPageSize bound one directory page.
	DefaultTenantDirectoryPageSize = 50
	MaxTenantDirectoryPageSize     = 100
	// MaxTenantAssignmentsPerRequest bounds one assignment batch.
	MaxTenantAssignmentsPerRequest = 100
	// tenantVerifierMarker prefixes the sealed tenant id of a pending admin consent. It is not
	// base64, so neither the sign-in completion nor an instance without tenants can open it.
	tenantVerifierMarker = "tenant:"
	// tenantCheckTimeout bounds the check of a tenant credential after a mailbox's token was refused.
	tenantCheckTimeout = 30 * time.Second
)

// Assignment kinds of a tenant mailbox.
const (
	TenantAssignPersonal = "personal"
	TenantAssignShared   = "shared"
)

// Outcomes of one assignment row.
const (
	TenantAssignmentCreated   = "created"
	TenantAssignmentRestored  = "restored"
	TenantAssignmentUnchanged = "unchanged"
	TenantAssignmentFailed    = "failed"
)

// ExternalMailTenantService lets an organization owner authorize a whole tenant once, and owners and
// admins assign mailboxes from its directory. Members, and API keys, have no part in it.
type ExternalMailTenantService interface {
	// AuthorizeExternalMailTenant proves a tenant credential and stores it (owners only). A provider
	// that grants access by admin consent answers with the consent page instead.
	AuthorizeExternalMailTenant(ctx context.Context, viewer domain.OrganizationContext, input ExternalMailTenantInput) (ExternalMailTenantAuthorizationResult, error)
	// CompleteExternalMailTenantConsent finishes an admin consent with what the provider returned.
	CompleteExternalMailTenantConsent(ctx context.Context, viewer domain.OrganizationContext, input ExternalMailTenantConsentInput) (ExternalMailTenantAuthorizationResult, error)
	// ListExternalMailTenants lists the organization's tenants (owners and admins).
	ListExternalMailTenants(ctx context.Context, viewer domain.OrganizationContext) ([]ExternalMailTenantView, error)
	// RevokeExternalMailTenant revokes the tenant and every connection of it (owners only).
	RevokeExternalMailTenant(ctx context.Context, viewer domain.OrganizationContext, id uint) error
	// ExternalMailTenantDirectory reads one page of the tenant's directory (owners and admins).
	ExternalMailTenantDirectory(ctx context.Context, viewer domain.OrganizationContext, id uint, cursor string, limit int) (*ExternalMailTenantDirectoryPage, error)
	// AssignExternalMailTenantMailboxes assigns mailboxes of the tenant, row by row (owners and
	// admins).
	AssignExternalMailTenantMailboxes(ctx context.Context, viewer domain.OrganizationContext, id uint, rows []ExternalMailTenantAssignmentInput) ([]ExternalMailTenantAssignmentResult, error)
}

// ExternalMailTenantInput authorizes a tenant.
type ExternalMailTenantInput struct {
	Provider domain.ExternalMailProvider
	Grant    ExternalMailTenantGrant
}

// ExternalMailTenantConsentInput is what the provider sent back to the callback after an admin
// consent.
type ExternalMailTenantConsentInput struct {
	State        string
	Tenant       string
	AdminConsent string
}

// ExternalMailTenantAuthorizationResult is a stored tenant (Created when new, otherwise
// re-authorized), or, when Consent is set, the admin consent page to send the browser to.
type ExternalMailTenantAuthorizationResult struct {
	Tenant  *ExternalMailTenantView
	Created bool
	Consent *ExternalMailAuthorizationStart
}

// ExternalMailTenantView is a tenant with its count of live assignments.
type ExternalMailTenantView struct {
	Tenant      *domain.ExternalMailTenant
	Assignments int
}

// ExternalMailTenantDirectoryEntry is one directory mailbox and what the organization holds of it.
type ExternalMailTenantDirectoryEntry struct {
	Entry ExternalMailDirectoryEntry
	// Connection is the live connection of this tenant assigning the mailbox; nil when unassigned.
	Connection *domain.ExternalMailConnection
	// Available is false while any live connection of the organization holds the address.
	Available bool
}

// ExternalMailTenantDirectoryPage is one directory page and the opaque cursor of the next.
type ExternalMailTenantDirectoryPage struct {
	Entries []ExternalMailTenantDirectoryEntry
	Next    string
}

// ExternalMailTenantAssignmentInput is one mailbox to assign: to a member (personal) or as a
// shared connected mailbox with members.
type ExternalMailTenantAssignmentInput struct {
	Address string
	Assign  string
	UserID  uint
	Sharing *domain.ExternalMailSharing
}

// ExternalMailTenantAssignmentResult is the outcome of one row; a failed row carries the public
// error code and can be sent again alone.
type ExternalMailTenantAssignmentResult struct {
	Address    string
	Status     string
	Connection *domain.ExternalMailConnection
	ErrorCode  string
}

var _ ExternalMailTenantService = (*service)(nil)

// tenantConnector returns the enabled connector of the provider when it serves tenants.
func (s *service) tenantConnector(provider domain.ExternalMailProvider) (ExternalMailConnector, error) {
	if !s.externalMailAvailable() {
		return ExternalMailConnector{}, ErrExternalMailProviderUnavailable
	}
	connector, registered := s.externalConnectors.lookup(provider)
	if !registered || connector.Tenant == nil || !connector.Capabilities.Read {
		return ExternalMailConnector{}, ErrExternalMailProviderUnavailable
	}
	return connector, nil
}

func isOrganizationOwner(viewer domain.OrganizationContext) bool {
	return viewer.UserID != 0 && viewer.Role == domain.OrganizationRoleOwner
}

func (s *service) AuthorizeExternalMailTenant(ctx context.Context, viewer domain.OrganizationContext, input ExternalMailTenantInput) (ExternalMailTenantAuthorizationResult, error) {
	if !isOrganizationOwner(viewer) {
		return ExternalMailTenantAuthorizationResult{}, domain.ErrPermissionDenied
	}
	connector, err := s.tenantConnector(input.Provider)
	if err != nil {
		return ExternalMailTenantAuthorizationResult{}, err
	}
	grant := input.Grant
	grant.TenantID = strings.TrimSpace(grant.TenantID)
	if grant.ServiceAccountKey == "" && grant.ClientSecret == "" {
		// No credential of the organization's own: the operator's application, after an admin
		// consented on the provider's page.
		return s.startTenantConsent(ctx, viewer, connector, grant.TenantID)
	}
	authorization, err := connector.Tenant.AuthorizeTenant(ctx, grant)
	if err != nil {
		return ExternalMailTenantAuthorizationResult{}, tenantAuthorizationError(err)
	}
	return s.saveTenant(ctx, viewer, input.Provider, authorization)
}

// tenantAuthorizationError maps a refused credential to the public refusal and keeps the rest.
func tenantAuthorizationError(err error) error {
	if errors.Is(err, ErrExternalMailAuth) {
		return ErrExternalMailAuthorizationFailed
	}
	return err
}

// startTenantConsent stores a single-use state, as a sign-in does, with the tenant id sealed behind
// the tenant marker, and returns the consent page pinned to that tenant.
func (s *service) startTenantConsent(ctx context.Context, viewer domain.OrganizationContext, connector ExternalMailConnector, tenantID string) (ExternalMailTenantAuthorizationResult, error) {
	state, err := randomToken()
	if err != nil {
		return ExternalMailTenantAuthorizationResult{}, err
	}
	consentURL, consents := connector.Tenant.TenantConsentURL(ExternalMailTenantConsent{
		TenantID: tenantID, State: state, RedirectURI: s.settings.ExternalMail.RedirectURL,
	})
	if !consents || tenantID == "" || len(tenantID) > 255 {
		return ExternalMailTenantAuthorizationResult{}, ErrExternalMailAuthorizationInvalid
	}
	sealed, err := s.encryptor.EncryptString(tenantID)
	if err != nil {
		return ExternalMailTenantAuthorizationResult{}, err
	}
	now := s.now().UTC()
	authorization := &domain.ExternalMailAuthorization{
		OrganizationID: viewer.OrganizationID, UserID: viewer.UserID, Provider: connector.ID, StateHash: hashState(state),
		SealedVerifier: tenantVerifierMarker + sealed, BackfillDays: domain.DefaultExternalMailBackfillDays,
		ExpiresAt: now.Add(domain.ExternalMailAuthorizationTTL), CreatedAt: now,
	}
	if err = s.externalMail.CreateExternalMailAuthorization(ctx, authorization); err != nil {
		return ExternalMailTenantAuthorizationResult{}, err
	}
	slog.InfoContext(ctx, "external_mail.tenant_consent_started",
		slog.Uint64("organization_id", uint64(viewer.OrganizationID)), slog.Uint64("user_id", uint64(viewer.UserID)),
		slog.String("provider", string(connector.ID)))
	return ExternalMailTenantAuthorizationResult{Consent: &ExternalMailAuthorizationStart{
		AuthorizationURL: consentURL, ExpiresAt: authorization.ExpiresAt,
	}}, nil
}

func (s *service) CompleteExternalMailTenantConsent(ctx context.Context, viewer domain.OrganizationContext, input ExternalMailTenantConsentInput) (ExternalMailTenantAuthorizationResult, error) {
	if !isOrganizationOwner(viewer) {
		return ExternalMailTenantAuthorizationResult{}, domain.ErrPermissionDenied
	}
	if !s.externalMailAvailable() {
		return ExternalMailTenantAuthorizationResult{}, ErrExternalMailAuthorizationNotFound
	}
	state := strings.TrimSpace(input.State)
	if state == "" {
		return ExternalMailTenantAuthorizationResult{}, ErrExternalMailAuthorizationNotFound
	}
	// Taking the state deletes it, so a consent completes once even when the check then fails.
	authorization, err := s.externalMail.TakeExternalMailAuthorization(ctx, hashState(state), s.now().UTC())
	if err != nil {
		return ExternalMailTenantAuthorizationResult{}, err
	}
	sealed, marked := strings.CutPrefix(authorization.SealedVerifier, tenantVerifierMarker)
	if !marked || authorization.OrganizationID != viewer.OrganizationID || authorization.UserID != viewer.UserID {
		return ExternalMailTenantAuthorizationResult{}, ErrExternalMailAuthorizationNotFound
	}
	pinned, err := s.encryptor.DecryptString(sealed)
	if err != nil {
		return ExternalMailTenantAuthorizationResult{}, ErrExternalMailAuthorizationNotFound
	}
	if !strings.EqualFold(strings.TrimSpace(input.AdminConsent), "true") {
		return ExternalMailTenantAuthorizationResult{}, ErrExternalMailAuthorizationFailed
	}
	if !strings.EqualFold(strings.TrimSpace(input.Tenant), pinned) {
		// The administrator consented for another tenant than the owner named.
		return ExternalMailTenantAuthorizationResult{}, ErrExternalMailAccountMismatch
	}
	connector, err := s.tenantConnector(authorization.Provider)
	if err != nil {
		return ExternalMailTenantAuthorizationResult{}, err
	}
	// The consent is proven by using it, never taken from the redirect alone.
	tenantAuthorization, err := connector.Tenant.AuthorizeTenant(ctx, ExternalMailTenantGrant{TenantID: pinned})
	if err != nil {
		return ExternalMailTenantAuthorizationResult{}, tenantAuthorizationError(err)
	}
	return s.saveTenant(ctx, viewer, authorization.Provider, tenantAuthorization)
}

// saveTenant stores a proven tenant credential: a new tenant, or a fresh credential for one this
// organization already holds. A tenant live in another organization is refused.
func (s *service) saveTenant(ctx context.Context, viewer domain.OrganizationContext, provider domain.ExternalMailProvider, authorization ExternalMailTenantAuthorization) (ExternalMailTenantAuthorizationResult, error) {
	identifier := strings.TrimSpace(authorization.TenantIdentifier)
	if identifier == "" || len(identifier) > 255 || authorization.Credential == "" {
		return ExternalMailTenantAuthorizationResult{}, ErrExternalMailAuthorizationFailed
	}
	sealed, err := s.encryptor.EncryptString(authorization.Credential)
	if err != nil {
		return ExternalMailTenantAuthorizationResult{}, err
	}
	displayName := truncateText(strings.TrimSpace(authorization.DisplayName), 255)
	now := s.now().UTC()
	var tenant *domain.ExternalMailTenant
	created := false
	err = s.repo.InTransaction(ctx, func(tx context.Context) error {
		existing, findErr := s.externalMail.FindLiveExternalMailTenant(tx, provider, identifier)
		switch {
		case errors.Is(findErr, ErrExternalMailTenantNotFound):
			tenant = &domain.ExternalMailTenant{
				OrganizationID: viewer.OrganizationID, Provider: provider, TenantIdentifier: identifier, DisplayName: displayName,
				EncryptedCredential: sealed, AuthorizedByUserID: &viewer.UserID, AuthorizedAt: now,
			}
			created = true
			return s.externalMail.CreateExternalMailTenant(tx, tenant)
		case findErr != nil:
			return findErr
		case existing.OrganizationID != viewer.OrganizationID:
			// A tenant reads for one organization at a time.
			return ErrExternalMailAccountTaken
		}
		var reauthorizeErr error
		tenant, reauthorizeErr = s.externalMail.ReauthorizeExternalMailTenant(tx, existing.ID, sealed, displayName, viewer.UserID, now)
		return reauthorizeErr
	})
	if errors.Is(err, gorm.ErrDuplicatedKey) || isUniqueViolation(err) {
		// Another authorization of the same tenant landed in the meantime.
		err = ErrExternalMailAccountTaken
	}
	if err != nil {
		return ExternalMailTenantAuthorizationResult{}, err
	}
	action := "update"
	if created {
		action = "create"
	}
	s.auditChange(ctx, auditstarter.Change{
		Action: action, Resource: "external_mail_tenants", TargetType: "external_mail_tenant",
		TargetID: strconv.FormatUint(uint64(tenant.ID), 10), Result: domain.AuditResultSuccess,
		Metadata: map[string]any{"provider": string(provider), "reason": "tenant_authorized"},
	})
	slog.InfoContext(ctx, "external_mail.tenant_authorized",
		slog.Uint64("organization_id", uint64(viewer.OrganizationID)), slog.Uint64("tenant_id", uint64(tenant.ID)),
		slog.String("provider", string(provider)), slog.Bool("created", created))
	view, err := s.tenantView(ctx, tenant)
	if err != nil {
		return ExternalMailTenantAuthorizationResult{}, err
	}
	return ExternalMailTenantAuthorizationResult{Tenant: view, Created: created}, nil
}

func (s *service) tenantView(ctx context.Context, tenant *domain.ExternalMailTenant) (*ExternalMailTenantView, error) {
	count, err := s.externalMail.CountLiveTenantConnections(ctx, tenant.ID)
	if err != nil {
		return nil, err
	}
	return &ExternalMailTenantView{Tenant: tenant, Assignments: count}, nil
}

func (s *service) ListExternalMailTenants(ctx context.Context, viewer domain.OrganizationContext) ([]ExternalMailTenantView, error) {
	if !viewer.Role.CanManageOrganization() {
		return nil, domain.ErrPermissionDenied
	}
	if s.externalMail == nil || !s.externalMailAvailable() {
		return []ExternalMailTenantView{}, nil
	}
	tenants, err := s.externalMail.ListExternalMailTenants(ctx, viewer.OrganizationID)
	if err != nil {
		return nil, err
	}
	views := make([]ExternalMailTenantView, 0, len(tenants))
	for _, tenant := range tenants {
		view, viewErr := s.tenantView(ctx, tenant)
		if viewErr != nil {
			return nil, viewErr
		}
		views = append(views, *view)
	}
	return views, nil
}

// managedTenant returns a tenant of the organization for an owner or admin.
func (s *service) managedTenant(ctx context.Context, viewer domain.OrganizationContext, id uint) (*domain.ExternalMailTenant, error) {
	if !viewer.Role.CanManageOrganization() {
		return nil, domain.ErrPermissionDenied
	}
	if !s.externalMailAvailable() {
		return nil, ErrExternalMailTenantNotFound
	}
	return s.externalMail.FindExternalMailTenant(ctx, viewer.OrganizationID, id)
}

// activeTenant returns an active tenant with its opened credential and connector.
func (s *service) activeTenant(ctx context.Context, viewer domain.OrganizationContext, id uint) (*domain.ExternalMailTenant, string, ExternalMailConnector, error) {
	tenant, err := s.managedTenant(ctx, viewer, id)
	if err != nil {
		return nil, "", ExternalMailConnector{}, err
	}
	if tenant.Status != domain.ExternalMailTenantActive {
		return nil, "", ExternalMailConnector{}, ErrExternalMailTenantInactive
	}
	connector, err := s.tenantConnector(tenant.Provider)
	if err != nil {
		return nil, "", ExternalMailConnector{}, err
	}
	credential, err := s.encryptor.DecryptString(tenant.EncryptedCredential)
	if err != nil {
		s.tenantLost(ctx, tenant)
		return nil, "", ExternalMailConnector{}, ErrExternalMailTenantInactive
	}
	return tenant, credential, connector, nil
}

// tenantLost stops every sync of a tenant whose credential the provider refused or this instance
// cannot open, until the owner authorizes it again.
func (s *service) tenantLost(ctx context.Context, tenant *domain.ExternalMailTenant) {
	lost, err := s.externalMail.MarkExternalMailTenantLost(ctx, tenant.ID, s.now().UTC())
	if err != nil {
		slog.ErrorContext(ctx, "external_mail.tenant_lost_failed", slog.Uint64("tenant_id", uint64(tenant.ID)), slog.String("error", err.Error()))
		return
	}
	if lost {
		slog.WarnContext(ctx, "external_mail.tenant_lost",
			slog.Uint64("organization_id", uint64(tenant.OrganizationID)), slog.Uint64("tenant_id", uint64(tenant.ID)),
			slog.String("provider", string(tenant.Provider)))
	}
}

func (s *service) RevokeExternalMailTenant(ctx context.Context, viewer domain.OrganizationContext, id uint) error {
	if !isOrganizationOwner(viewer) {
		return domain.ErrPermissionDenied
	}
	if s.externalMail == nil || s.encryptor == nil {
		return ErrExternalMailTenantNotFound
	}
	tenant, err := s.externalMail.FindExternalMailTenant(ctx, viewer.OrganizationID, id)
	if err != nil {
		return err
	}
	if tenant.Status == domain.ExternalMailTenantRevoked {
		return nil
	}
	var revoked []*domain.ExternalMailConnection
	err = s.repo.InTransaction(ctx, func(tx context.Context) error {
		var revokeErr error
		if revoked, revokeErr = s.externalMail.RevokeExternalMailTenant(tx, viewer.OrganizationID, tenant.ID, s.now().UTC()); revokeErr != nil {
			return revokeErr
		}
		// Every assigned mailbox stops at once and records why; the per-tenant ceiling bounds them.
		for _, connection := range revoked {
			if afterErr := s.afterConnectionRevoked(tx, connection, viewer.UserID, domain.MailboxAccessConnectionRevoked,
				map[string]any{"reason": "tenant_revoked"}); afterErr != nil {
				return afterErr
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.auditChange(ctx, auditstarter.Change{
		Action: "delete", Resource: "external_mail_tenants", TargetType: "external_mail_tenant",
		TargetID: strconv.FormatUint(uint64(tenant.ID), 10), Result: domain.AuditResultSuccess,
		Metadata: map[string]any{"provider": string(tenant.Provider), "reason": "tenant_revoked", "connections_revoked": len(revoked)},
	})
	slog.InfoContext(ctx, "external_mail.tenant_revoked",
		slog.Uint64("organization_id", uint64(viewer.OrganizationID)), slog.Uint64("tenant_id", uint64(tenant.ID)),
		slog.String("provider", string(tenant.Provider)), slog.Int("connections_revoked", len(revoked)))
	return nil
}

func (s *service) ExternalMailTenantDirectory(ctx context.Context, viewer domain.OrganizationContext, id uint, cursor string, limit int) (*ExternalMailTenantDirectoryPage, error) {
	tenant, credential, connector, err := s.activeTenant(ctx, viewer, id)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = DefaultTenantDirectoryPageSize
	}
	limit = min(limit, MaxTenantDirectoryPageSize)
	listed, err := connector.Tenant.ListTenantDirectory(ctx, credential, cursor, limit)
	if errors.Is(err, ErrExternalMailAuth) {
		s.tenantLost(ctx, tenant)
		return nil, ErrExternalMailTenantInactive
	}
	if err != nil {
		return nil, err
	}
	if len(listed.Entries) > MaxTenantDirectoryPageSize {
		listed.Entries = listed.Entries[:MaxTenantDirectoryPageSize]
	}
	addresses := make([]string, 0, len(listed.Entries))
	for _, entry := range listed.Entries {
		addresses = append(addresses, entry.Address)
	}
	live, err := s.externalMail.ListLiveConnectionsByAccounts(ctx, viewer.OrganizationID, tenant.Provider, addresses)
	if err != nil {
		return nil, err
	}
	byAddress := make(map[string]*domain.ExternalMailConnection, len(live))
	for _, connection := range live {
		byAddress[strings.ToLower(connection.AccountAddress)] = connection
	}
	page := &ExternalMailTenantDirectoryPage{Entries: make([]ExternalMailTenantDirectoryEntry, 0, len(listed.Entries)), Next: listed.Next}
	for _, entry := range listed.Entries {
		row := ExternalMailTenantDirectoryEntry{Entry: entry, Available: true}
		if connection, held := byAddress[strings.ToLower(entry.Address)]; held {
			row.Available = false
			if connection.TenantID != nil && *connection.TenantID == tenant.ID {
				row.Connection = connection
			}
		}
		page.Entries = append(page.Entries, row)
	}
	return page, nil
}

func (s *service) AssignExternalMailTenantMailboxes(ctx context.Context, viewer domain.OrganizationContext, id uint, rows []ExternalMailTenantAssignmentInput) ([]ExternalMailTenantAssignmentResult, error) {
	if !viewer.Role.CanManageOrganization() {
		return nil, domain.ErrPermissionDenied
	}
	if len(rows) == 0 || len(rows) > MaxTenantAssignmentsPerRequest {
		return nil, fmt.Errorf("%w: send 1 to %d assignments", ErrMailboxAccessInvalid, MaxTenantAssignmentsPerRequest)
	}
	tenant, credential, connector, err := s.activeTenant(ctx, viewer, id)
	if err != nil {
		return nil, err
	}
	results := make([]ExternalMailTenantAssignmentResult, 0, len(rows))
	seen := map[string]bool{}
	lost := false
	for _, row := range rows {
		address := strings.ToLower(strings.TrimSpace(row.Address))
		result := ExternalMailTenantAssignmentResult{Address: address}
		var rowErr error
		switch {
		case lost:
			rowErr = ErrExternalMailTenantInactive
		case seen[address]:
			rowErr = fmt.Errorf("%w: the address appears twice in the batch", ErrMailboxAccessInvalid)
		default:
			seen[address] = true
			result.Connection, result.Status, rowErr = s.assignTenantRow(ctx, viewer, tenant, credential, connector, address, row)
			if errors.Is(rowErr, ErrExternalMailAuth) {
				// The tenant credential stopped working mid-batch: stop the tenant and the remaining rows.
				s.tenantLost(ctx, tenant)
				lost, rowErr = true, ErrExternalMailTenantInactive
			}
		}
		if rowErr != nil {
			result.Status, result.Connection, result.ErrorCode = TenantAssignmentFailed, nil, tenantRowErrorCode(rowErr)
		}
		results = append(results, result)
	}
	return results, nil
}

// tenantRowErrorCode is the public code of a failed assignment row.
func tenantRowErrorCode(err error) string {
	switch {
	case errors.Is(err, domain.ErrPermissionDenied):
		return domain.CodePermissionDenied
	case errors.Is(err, ErrExternalMailDirectoryNotFound):
		return domain.CodeExternalMailAccountMismatch
	case errors.Is(err, ErrExternalMailTenantInactive):
		return domain.CodeMailboxAccessConflict
	}
	code := importErrorCode(err)
	if code == response.ErrorCodeInternal {
		// A provider that could not be reached is retried by sending the row again.
		return domain.CodeExternalMailServerUnreachable
	}
	return code
}

// assignTenantRow checks one row, finds its mailbox in the tenant's directory, and stores the
// assignment in its own transaction.
func (s *service) assignTenantRow(ctx context.Context, viewer domain.OrganizationContext, tenant *domain.ExternalMailTenant, credential string, connector ExternalMailConnector, address string, row ExternalMailTenantAssignmentInput) (*domain.ExternalMailConnection, string, error) {
	if parsed, err := mail.ParseAddress(address); err != nil || parsed.Address != address || len(address) > 320 {
		return nil, "", fmt.Errorf("%w: address must be one mail address", ErrMailboxAccessInvalid)
	}
	binding := externalBinding{
		provider: tenant.Provider, address: address, backfillDays: domain.DefaultExternalMailBackfillDays, tenantID: &tenant.ID,
	}
	var owner *domain.OrganizationMembership
	switch row.Assign {
	case TenantAssignPersonal:
		if row.UserID == 0 || row.Sharing != nil {
			return nil, "", fmt.Errorf("%w: a personal assignment takes user_id and no members", ErrMailboxAccessInvalid)
		}
		var err error
		if owner, err = s.currentMembership(ctx, viewer.OrganizationID, row.UserID); err != nil {
			return nil, "", err
		}
	case TenantAssignShared:
		if row.UserID != 0 || row.Sharing == nil {
			return nil, "", fmt.Errorf("%w: a shared assignment takes members and no user_id", ErrMailboxAccessInvalid)
		}
		var err error
		if binding.sharing, err = s.checkSharing(ctx, viewer, row.Sharing); err != nil {
			return nil, "", err
		}
	default:
		return nil, "", fmt.Errorf("%w: assign must be personal or shared", ErrMailboxAccessInvalid)
	}
	entry, err := connector.Tenant.LookupTenantMailbox(ctx, credential, address)
	if err != nil {
		return nil, "", err
	}
	binding.directoryID = entry.DirectoryID
	displayName := entry.DisplayName
	var connection *domain.ExternalMailConnection
	status := TenantAssignmentCreated
	err = s.repo.InTransaction(ctx, func(tx context.Context) error {
		// Assignments and shared bindings of the organization run one at a time, so the ceilings are
		// counted against committed rows.
		if lockErr := s.externalMail.LockSharedExternalMailBindings(tx, viewer.OrganizationID); lockErr != nil {
			return lockErr
		}
		existing, findErr := s.externalMail.FindExternalMailConnectionByAccount(tx, viewer.OrganizationID, tenant.Provider, address)
		switch {
		case errors.Is(findErr, ErrExternalMailConnectionNotFound):
		case findErr != nil:
			return findErr
		default:
			var done bool
			connection, status, done, findErr = s.assignOverExisting(tx, viewer, tenant, binding, owner, existing)
			if findErr != nil || done {
				return findErr
			}
			status = TenantAssignmentCreated
		}
		if limitErr := s.checkTenantAssignmentLimit(tx, tenant.ID); limitErr != nil {
			return limitErr
		}
		if binding.sharing != nil {
			connection, findErr = s.bindSharedAccount(tx, viewer, binding, "")
			return findErr
		}
		connection, findErr = s.bindTenantPersonal(tx, viewer, binding, owner, displayName)
		return findErr
	})
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		err = ErrExternalMailAccountTaken
	}
	if err != nil {
		return nil, "", err
	}
	if status != TenantAssignmentUnchanged {
		assignment := TenantAssignPersonal
		if binding.sharing != nil {
			assignment = TenantAssignShared
		}
		s.auditChange(ctx, auditstarter.Change{
			Action: "create", Resource: "external_mail_connections", TargetType: "external_mail_connection",
			TargetID: strconv.FormatUint(uint64(connection.ID), 10), Result: domain.AuditResultSuccess,
			Metadata: map[string]any{
				"provider": string(connection.Provider), "reason": "tenant_assignment", "tenant_id": tenant.ID,
				"assignment": assignment, "mailbox_id": connection.MailboxID, "owner_user_id": auditID(connection.OwnerUserID),
				"restored": status == TenantAssignmentRestored,
			},
		})
		slog.InfoContext(ctx, "external_mail.tenant_assigned",
			slog.Uint64("organization_id", uint64(viewer.OrganizationID)), slog.Uint64("tenant_id", uint64(tenant.ID)),
			slog.Uint64("connection_id", uint64(connection.ID)), slog.String("assignment", assignment), slog.String("status", status))
	}
	return connection, status, nil
}

// assignOverExisting settles a row whose address the organization already holds a connection of.
// done means the row is settled (unchanged or restored); otherwise the caller assigns it fresh,
// after a revoked connection of another kind was purged here.
func (s *service) assignOverExisting(tx context.Context, viewer domain.OrganizationContext, tenant *domain.ExternalMailTenant, binding externalBinding, owner *domain.OrganizationMembership, existing *domain.ExternalMailConnection) (*domain.ExternalMailConnection, string, bool, error) {
	ours := existing.TenantID != nil && *existing.TenantID == tenant.ID
	sameTarget := ours && ((binding.sharing != nil && existing.Shared) ||
		(binding.sharing == nil && !existing.Shared && owner != nil && existing.OwnerUserID != nil && *existing.OwnerUserID == owner.UserID))
	if existing.Status != domain.ExternalMailStatusRevoked {
		if sameTarget {
			// Sending a row again changes nothing: assignments are idempotent per address.
			return existing, TenantAssignmentUnchanged, true, nil
		}
		// Self-bound, a team account, another tenant's, or this tenant's for someone else.
		return nil, "", false, ErrExternalMailAccountTaken
	}
	_, mailboxErr := s.repo.FindMailbox(tx, existing.OrganizationID, existing.MailboxID)
	mailboxKept := mailboxErr == nil
	if mailboxErr != nil && !errors.Is(mailboxErr, ErrMailboxNotFound) {
		return nil, "", false, mailboxErr
	}
	if sameTarget && mailboxKept {
		restored, err := s.restoreTenantAssignment(tx, viewer, tenant, binding, existing)
		return restored, TenantAssignmentRestored, err == nil, err
	}
	if existing.Shared && mailboxKept {
		// A team mailbox is restored by its own reconnect or released by deleting it, never captured.
		return nil, "", false, ErrExternalMailAccountTaken
	}
	return nil, "", false, s.purgeForRebind(tx, viewer, existing)
}

// restoreTenantAssignment makes a revoked assignment of the same target live again, its copies kept.
func (s *service) restoreTenantAssignment(tx context.Context, viewer domain.OrganizationContext, tenant *domain.ExternalMailTenant, binding externalBinding, existing *domain.ExternalMailConnection) (*domain.ExternalMailConnection, error) {
	if err := s.checkTenantAssignmentLimit(tx, tenant.ID); err != nil {
		return nil, err
	}
	if existing.Shared {
		if err := s.checkSharedConnectionLimit(tx, viewer.OrganizationID); err != nil {
			return nil, err
		}
	}
	restored, err := s.externalMail.RestoreTenantConnection(tx, existing.ID, tenant.ID, binding.directoryID, s.now().UTC())
	if err != nil {
		return nil, err
	}
	if binding.sharing != nil {
		mailbox, findErr := s.repo.FindMailbox(tx, viewer.OrganizationID, existing.MailboxID)
		if findErr != nil {
			return nil, findErr
		}
		for _, member := range binding.sharing.members {
			if _, setErr := s.setMember(tx, viewer, mailbox, member); setErr != nil {
				return nil, setErr
			}
		}
	}
	return restored, s.recordConnectionEvent(tx, restored, domain.MailboxAccessConnectionReconnected, viewer.UserID)
}

// checkTenantAssignmentLimit refuses one more live connection of a tenant at its ceiling. It runs
// under the organization's shared binding lock.
func (s *service) checkTenantAssignmentLimit(tx context.Context, tenantID uint) error {
	live, err := s.externalMail.CountLiveTenantConnections(tx, tenantID)
	if err != nil {
		return err
	}
	if live >= s.settings.ExternalMail.TenantAssignmentLimit() {
		return ErrExternalMailConnectionLimit
	}
	return nil
}

// bindTenantPersonal creates a member's personal connected mailbox at an organization address of the
// tenant and its connection, due for its first sync at once. It never counts against the member's
// own limit: the tenant's ceiling bounds it.
func (s *service) bindTenantPersonal(tx context.Context, viewer domain.OrganizationContext, binding externalBinding, owner *domain.OrganizationMembership, displayName string) (*domain.ExternalMailConnection, error) {
	ownerID := owner.UserID
	if strings.TrimSpace(displayName) == "" {
		displayName = binding.address
	}
	mailbox := &domain.Mailbox{
		OrganizationID: viewer.OrganizationID, CreatedBy: viewer.UserID, Address: binding.address,
		DisplayName: truncateDisplayName(displayName), Type: domain.MailboxTypePersonal, Origin: domain.MailboxOriginConnected,
		OwnerUserID: &ownerID, ExternalTenantID: binding.tenantID,
	}
	if err := s.repo.CreateMailbox(tx, mailbox, 0); err != nil {
		return nil, err
	}
	if err := s.recordAccess(tx, mailbox, domain.MailboxAccessCreated, viewer.UserID, ownerID, map[string]any{
		"type": string(mailbox.Type), "owner_user_id": ownerID, "tenant_id": *binding.tenantID,
	}); err != nil {
		return nil, err
	}
	binder := viewer.UserID
	connection := &domain.ExternalMailConnection{
		OrganizationID: viewer.OrganizationID, MailboxID: mailbox.ID, Provider: binding.provider,
		AccountAddress: binding.address, OwnerUserID: &ownerID, BoundByUserID: &binder,
		BackfillDays: binding.backfillDays, NextSyncAt: s.now().UTC(),
		TenantID: binding.tenantID, TenantDirectoryID: binding.directoryID,
	}
	if err := s.externalMail.CreateExternalMailConnection(tx, connection); err != nil {
		return nil, err
	}
	if err := s.recordConnectionEvent(tx, connection, domain.MailboxAccessConnectionBound, viewer.UserID); err != nil {
		return nil, err
	}
	return connection, nil
}

// unassignTenantConnection disconnects one tenant assignment. Only owners and admins may: the address
// is the organization's, so the member it is assigned to cannot unbind it (403), and anyone else is
// answered not found.
func (s *service) unassignTenantConnection(ctx context.Context, viewer domain.OrganizationContext, connection *domain.ExternalMailConnection) error {
	if !viewer.Role.CanManageOrganization() {
		if _, err := s.managedConnection(ctx, viewer, connection.ID); err != nil {
			return err
		}
		return domain.ErrPermissionDenied
	}
	if connection.Shared {
		return s.disconnectShared(ctx, viewer, connection)
	}
	return s.forceDisconnect(ctx, viewer, connection)
}

// connectionToken is the token a connection is fetched with: its own sealed grant, or one its active
// tenant mints for its mailbox. It also returns the tenant of a tenant connection. A credential that
// is gone or cannot be opened is ErrExternalMailAuth.
func (s *service) connectionToken(ctx context.Context, connection *domain.ExternalMailConnection, connector ExternalMailConnector) (ExternalMailToken, *domain.ExternalMailTenant, error) {
	if connection.TenantID == nil {
		refreshToken, err := s.encryptor.DecryptString(connection.EncryptedRefreshToken)
		if err != nil {
			// A token this instance cannot open is as unusable as one the provider refuses.
			return ExternalMailToken{}, nil, fmt.Errorf("%w: sealed token unreadable", ErrExternalMailAuth)
		}
		return ExternalMailToken{RefreshToken: refreshToken}, nil, nil
	}
	tenant, err := s.externalMail.FindExternalMailTenant(ctx, connection.OrganizationID, *connection.TenantID)
	if err != nil {
		return ExternalMailToken{}, nil, err
	}
	if tenant.Status != domain.ExternalMailTenantActive || connector.Tenant == nil {
		// Nothing to check again: the tenant already waits for its owner.
		return ExternalMailToken{}, nil, fmt.Errorf("%w: tenant is not active", ErrExternalMailAuth)
	}
	credential, err := s.encryptor.DecryptString(tenant.EncryptedCredential)
	if err != nil {
		return ExternalMailToken{}, tenant, fmt.Errorf("%w: sealed tenant credential unreadable", ErrExternalMailAuth)
	}
	token, err := connector.Tenant.MailboxToken(ctx, credential, connection.AccountAddress, connection.TenantDirectoryID)
	return token, tenant, err
}

// checkTenantAfterRefusal tells a refused mailbox from a refused tenant: when the tenant credential
// no longer works either, every connection of the tenant stops at once; otherwise only the refused
// connection waits, as its sync already recorded.
func (s *service) checkTenantAfterRefusal(ctx context.Context, connector ExternalMailConnector, tenant *domain.ExternalMailTenant, timeout time.Duration) {
	credential, err := s.encryptor.DecryptString(tenant.EncryptedCredential)
	if err != nil {
		s.tenantLost(ctx, tenant)
		return
	}
	if connector.Tenant == nil {
		return
	}
	checkCtx, cancel := context.WithTimeout(ctx, min(timeout, tenantCheckTimeout))
	defer cancel()
	// A provider that could not be asked leaves the tenant as it is; the next refusal asks again.
	if err = connector.Tenant.VerifyTenant(checkCtx, credential); errors.Is(err, ErrExternalMailAuth) {
		s.tenantLost(ctx, tenant)
	}
}
