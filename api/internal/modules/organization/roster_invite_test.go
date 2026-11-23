package organization

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fastagi/zmail/api/internal/domain"
	auditstarter "github.com/fastagi/zmail/api/internal/modules/audit"
)

func TestInviteRosterMemberStoresRoleAndGrants(t *testing.T) {
	var stored *domain.OrganizationInvitation
	repo := &invitationRepositoryStub{
		findFn: func(context.Context, uint, uint) (*domain.OrganizationMembership, error) {
			return invitationManager(domain.OrganizationRoleAdmin), nil
		},
		createInvitationFn: func(_ context.Context, invitation *domain.OrganizationInvitation, _ time.Time) error {
			stored = invitation
			invitation.ID = 91
			return nil
		},
	}
	svc := NewService(repo, repo, &invitationMailerStub{}, InvitationPolicy{TTL: time.Hour}, nil)
	var audited []auditstarter.Change
	svc.recordAudit = func(_ context.Context, change auditstarter.Change) { audited = append(audited, change) }
	grants := domain.MemberFeatures{Broadcasts: true, APIKeys: true}

	invitation, status, err := svc.InviteRosterMember(context.Background(), 17, 42, " Alice@Gmail.com ", domain.OrganizationRoleMember, grants)

	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, uint(91), invitation.ID)
	assert.Equal(t, "alice@gmail.com", stored.Email)
	assert.Equal(t, grants, stored.Features)
	assert.Equal(t, string(InvitationEmailSendStatusNotConfigured), status)
	require.Len(t, audited, 1)
	assert.Equal(t, "roster_invite", audited[0].Action)
}

func TestInviteRosterMemberRefusesAMemberCaller(t *testing.T) {
	repo := &invitationRepositoryStub{
		findFn: func(context.Context, uint, uint) (*domain.OrganizationMembership, error) {
			return invitationManager(domain.OrganizationRoleMember), nil
		},
	}
	svc := NewService(repo, repo, &invitationMailerStub{}, InvitationPolicy{TTL: time.Hour}, nil)

	_, _, err := svc.InviteRosterMember(context.Background(), 17, 42, "alice@gmail.com", domain.OrganizationRoleMember, domain.DefaultMemberFeatures())

	assert.ErrorIs(t, err, domain.ErrPermissionDenied)
}

func TestInviteRosterMemberRejectsOwnerRoleAndBadEmail(t *testing.T) {
	svc := NewService(&invitationRepositoryStub{}, &invitationRepositoryStub{}, nil, InvitationPolicy{TTL: time.Hour}, nil)

	_, _, err := svc.InviteRosterMember(context.Background(), 17, 42, "alice@gmail.com", domain.OrganizationRoleOwner, domain.MemberFeatures{})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
	_, _, err = svc.InviteRosterMember(context.Background(), 17, 42, "not-an-email", domain.OrganizationRoleMember, domain.MemberFeatures{})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
}

func TestInviteKeepsTheNewMemberDefaults(t *testing.T) {
	var stored *domain.OrganizationInvitation
	repo := &invitationRepositoryStub{
		findFn: func(context.Context, uint, uint) (*domain.OrganizationMembership, error) {
			return invitationManager(domain.OrganizationRoleOwner), nil
		},
		createInvitationFn: func(_ context.Context, invitation *domain.OrganizationInvitation, _ time.Time) error {
			stored = invitation
			return nil
		},
	}
	svc := NewService(repo, repo, nil, InvitationPolicy{TTL: time.Hour}, nil)

	_, err := svc.Invite(context.Background(), 17, 42, &CreateOrganizationInvitationRequest{Email: "bo@example.com", Role: domain.OrganizationRoleMember})

	require.NoError(t, err)
	assert.Equal(t, domain.DefaultMemberFeatures(), stored.Features)
}

func TestFindPendingInvitationByEmailNormalizes(t *testing.T) {
	repo := &invitationRepositoryStub{
		findByEmailFn: func(organizationID uint, email string) (*domain.OrganizationInvitation, error) {
			assert.Equal(t, uint(42), organizationID)
			assert.Equal(t, "alice@gmail.com", email)
			return &domain.OrganizationInvitation{ID: 5}, nil
		},
	}
	svc := NewService(repo, repo, nil, InvitationPolicy{TTL: time.Hour}, nil)

	invitation, err := svc.FindPendingInvitationByEmail(context.Background(), 42, " ALICE@gmail.com")

	require.NoError(t, err)
	assert.Equal(t, uint(5), invitation.ID)
}
