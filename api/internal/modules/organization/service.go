package organization

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/fastagi/zmail/api/internal/capabilities/crypto"
	"github.com/fastagi/zmail/api/internal/domain"
	infraevents "github.com/fastagi/zmail/api/internal/infra/events"
	auditstarter "github.com/fastagi/zmail/api/internal/modules/audit"
	"github.com/fastagi/zmail/api/internal/modules/user"
)

// Service defines the organization ownership kernel.
type Service interface {
	Create(ctx context.Context, userID uint, req *CreateOrganizationRequest) (*domain.OrganizationMembership, error)
	List(ctx context.Context, userID uint, page, pageSize int) ([]*domain.OrganizationMembership, int64, error)
	Get(ctx context.Context, userID, organizationID uint) (*domain.OrganizationMembership, error)
	ResolveContext(ctx context.Context, userID, organizationID uint) (*domain.OrganizationContext, error)
	Update(ctx context.Context, userID, organizationID uint, req *UpdateOrganizationRequest) (*domain.OrganizationMembership, error)
	ListMembers(ctx context.Context, userID, organizationID uint, page, pageSize int) ([]*domain.OrganizationMembership, int64, error)
	ChangeMemberRole(ctx context.Context, userID, organizationID, memberID uint, req *UpdateOrganizationMemberRequest) (*domain.OrganizationMembership, error)
	UpdateMemberFeatures(ctx context.Context, userID, organizationID, memberID uint, req *UpdateOrganizationMemberFeaturesRequest) (*domain.OrganizationMembership, error)
	RemoveMember(ctx context.Context, userID, organizationID, memberID uint) error
	TransferOwnership(ctx context.Context, userID, organizationID uint, req *TransferOrganizationOwnershipRequest) (*domain.OrganizationOwnershipTransfer, error)
	Invite(ctx context.Context, userID, organizationID uint, req *CreateOrganizationInvitationRequest) (*OrganizationInvitationResult, error)
	ListInvitations(ctx context.Context, userID, organizationID uint, page, pageSize int) ([]*domain.OrganizationInvitation, int64, error)
	RevokeInvitation(ctx context.Context, userID, organizationID, invitationID uint) error
	ResendInvitation(ctx context.Context, userID, organizationID, invitationID uint) (*OrganizationInvitationResult, error)
	AcceptInvitation(ctx context.Context, userID uint, req *AcceptOrganizationInvitationRequest) (*domain.OrganizationMembership, error)
}

// MembershipEventPublisher publishes membership events after the change commits; the injected
// events.EventBus implements it.
type MembershipEventPublisher interface {
	Publish(ctx context.Context, event infraevents.BasicEvent) error
}

type service struct {
	repo             domain.OrganizationRepository
	invitationRepo   domain.OrganizationInvitationRepository
	invitationMailer InvitationMailer
	invitationPolicy InvitationPolicy
	// events is nil when nothing listens, as in tests that do not need it.
	events MembershipEventPublisher
	// administration serves operator tooling; nil when repo does not implement it.
	administration administrationStore
	// recordAudit enriches the request's audit entry; tests replace it to inspect the metadata.
	recordAudit func(context.Context, auditstarter.Change)
	now         func() time.Time
}

var (
	_ Service                   = (*service)(nil)
	_ user.AccountDeletionGuard = (*service)(nil)
)

// NewService creates the organization service and its invitation workflow.
func NewService(
	repo domain.OrganizationRepository,
	invitationRepo domain.OrganizationInvitationRepository,
	invitationMailer InvitationMailer,
	invitationPolicy InvitationPolicy,
	events MembershipEventPublisher,
) *service {
	if invitationPolicy.TTL <= 0 || invitationPolicy.ResendCooldown == nil ||
		invitationPolicy.ResendDaily == nil || invitationPolicy.ResendOrganization == nil {
		fallback := NewInvitationPolicy(nil, nil)
		if invitationPolicy.TTL > 0 {
			fallback.TTL = invitationPolicy.TTL
		}
		fallback.PublicURL = invitationPolicy.PublicURL
		invitationPolicy = fallback
	}
	var administration administrationStore
	if seam, ok := repo.(administrationStore); ok {
		administration = seam
	}
	return &service{
		administration:   administration,
		repo:             repo,
		invitationRepo:   invitationRepo,
		invitationMailer: invitationMailer,
		invitationPolicy: invitationPolicy,
		events:           events,
		recordAudit:      auditstarter.RecordChange,
		now: func() time.Time {
			return time.Now().UTC()
		},
	}
}

func (s *service) Create(ctx context.Context, userID uint, req *CreateOrganizationRequest) (*domain.OrganizationMembership, error) {
	if userID == 0 || req == nil {
		return nil, domain.ErrInvalidInput
	}
	name := strings.TrimSpace(req.Name)
	if !validOrganizationName(name) {
		return nil, domain.ErrInvalidInput
	}

	slug := req.Slug
	if slug == "" {
		generated, err := crypto.GenerateKeyHex(8)
		if err != nil {
			return nil, fmt.Errorf("generate organization slug: %w", err)
		}
		slug = "org-" + generated
	}
	if !validOrganizationSlug(slug) {
		return nil, domain.ErrInvalidInput
	}

	organization := &domain.Organization{
		Name:      name,
		Slug:      slug,
		CreatedBy: userID,
	}
	owner := &domain.OrganizationMembership{
		UserID: userID,
		Role:   domain.OrganizationRoleOwner,
	}
	if err := s.repo.CreateWithOwner(ctx, organization, owner); err != nil {
		return nil, fmt.Errorf("create organization with owner: %w", err)
	}

	auditstarter.RecordChange(ctx, auditstarter.Change{
		Action:     "create",
		Resource:   "organizations",
		TargetType: "organization",
		TargetID:   strconv.FormatUint(uint64(organization.ID), 10),
		Result:     domain.AuditResultSuccess,
		Metadata: map[string]any{
			"role": domain.OrganizationRoleOwner,
		},
	})
	s.publish(ctx, domain.NewOrganizationMemberJoinedEvent(owner.OrganizationID, owner.UserID, owner.ID, s.now()))
	return owner, nil
}

// publish tells subscribers about a committed membership change. A subscriber's failure is
// logged and never undoes or fails the change.
func (s *service) publish(ctx context.Context, event infraevents.BasicEvent) {
	if s.events == nil {
		return
	}
	if err := s.events.Publish(ctx, event); err != nil {
		slog.ErrorContext(ctx, "organization membership event publication failed",
			slog.String("event", event.EventName()), slog.String("error", err.Error()))
	}
}

func (s *service) List(ctx context.Context, userID uint, page, pageSize int) ([]*domain.OrganizationMembership, int64, error) {
	if userID == 0 || page < 1 || pageSize < 1 {
		return nil, 0, domain.ErrInvalidInput
	}
	return s.repo.ListForUser(ctx, userID, page, pageSize)
}

func (s *service) Get(ctx context.Context, userID, organizationID uint) (*domain.OrganizationMembership, error) {
	if userID == 0 || organizationID == 0 {
		return nil, domain.ErrInvalidInput
	}
	return s.repo.FindForUser(ctx, organizationID, userID)
}

func (s *service) Update(ctx context.Context, userID, organizationID uint, req *UpdateOrganizationRequest) (*domain.OrganizationMembership, error) {
	if userID == 0 || organizationID == 0 || req == nil {
		return nil, domain.ErrInvalidInput
	}
	name := strings.TrimSpace(req.Name)
	if !validOrganizationName(name) {
		return nil, domain.ErrInvalidInput
	}

	membership, err := s.repo.FindForUser(ctx, organizationID, userID)
	if err != nil {
		return nil, err
	}
	if membership.Organization == nil {
		return nil, domain.ErrOrganizationNotFound
	}
	if !membership.Role.CanManageOrganization() {
		return nil, domain.ErrPermissionDenied
	}
	if membership.Organization.Name == name {
		return membership, nil
	}

	before := membership.Organization.Name
	membership.Organization.Name = name
	if err := s.repo.Update(ctx, membership.Organization); err != nil {
		return nil, fmt.Errorf("update organization: %w", err)
	}

	auditstarter.RecordChange(ctx, auditstarter.Change{
		Action:     "update",
		Resource:   "organizations",
		TargetType: "organization",
		TargetID:   strconv.FormatUint(uint64(organizationID), 10),
		Result:     domain.AuditResultSuccess,
		Changes: map[string]domain.AuditValueChange{
			"name": {Before: before, After: name},
		},
	})
	return membership, nil
}

// AccountDeletionGuardName identifies the optional starter guard.
func (s *service) AccountDeletionGuardName() string {
	return "organization"
}

// CheckAccountDeletion prevents orphaned or stale memberships after soft deletion.
func (s *service) CheckAccountDeletion(ctx context.Context, userID uint) error {
	if userID == 0 {
		return domain.ErrInvalidInput
	}
	total, owned, err := s.repo.CountMembershipsForUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("count organization memberships: %w", err)
	}
	if owned > 0 {
		return domain.ErrOrganizationOwnershipTransferRequired
	}
	if total > 0 {
		return domain.ErrOrganizationMembershipExitRequired
	}
	return nil
}
