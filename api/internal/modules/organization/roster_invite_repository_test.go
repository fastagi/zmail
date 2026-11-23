package organization

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fastagi/zmail/api/internal/capabilities/crypto"
	"github.com/fastagi/zmail/api/internal/domain"
)

func TestRepositoryInvitationGrantsBecomeTheMembershipGrants(t *testing.T) {
	db := newOrganizationRepositoryTestDB(t)
	repo := NewRepository(db)
	now := time.Now().UTC().Truncate(time.Second)
	ownerID := createOrganizationTestUser(t, db, "roster-owner")
	inviteeID := createOrganizationTestUser(t, db, "roster-member")
	organization := createInvitationTestOrganization(t, db, ownerID)

	// Contacts off is a false value the column default would otherwise turn on.
	grants := domain.MemberFeatures{Broadcasts: true, Contacts: false, Templates: true, APIKeys: true}
	invitation := newPendingInvitation(organization.ID, ownerID, "roster-member@example.com", "oinv_roster.secret", now)
	invitation.Features = grants
	require.NoError(t, repo.CreateInvitation(context.Background(), invitation, now))

	found, err := repo.FindPendingInvitationByEmail(context.Background(), organization.ID, "roster-member@example.com", now)
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, invitation.ID, found.ID)
	assert.Equal(t, grants, found.Features)

	missing, err := repo.FindPendingInvitationByEmail(context.Background(), organization.ID, "nobody@example.com", now)
	require.NoError(t, err)
	assert.Nil(t, missing)
	expired, err := repo.FindPendingInvitationByEmail(context.Background(), organization.ID, "roster-member@example.com", now.Add(8*24*time.Hour))
	require.NoError(t, err)
	assert.Nil(t, expired, "an expired invitation is not pending")

	membership, _, err := repo.AcceptInvitation(context.Background(), crypto.SHA256Hex("oinv_roster.secret"), inviteeID, now)
	require.NoError(t, err)
	assert.Equal(t, grants, membership.Features)

	var stored OrganizationMembershipPO
	require.NoError(t, db.First(&stored, membership.ID).Error)
	assert.Equal(t, grants, stored.features())

	accepted, err := repo.FindPendingInvitationByEmail(context.Background(), organization.ID, "roster-member@example.com", now)
	require.NoError(t, err)
	assert.Nil(t, accepted, "an accepted invitation is not pending")
}

func TestRepositoryInvitationWithoutGrantsKeepsDefaults(t *testing.T) {
	db := newOrganizationRepositoryTestDB(t)
	repo := NewRepository(db)
	now := time.Now().UTC().Truncate(time.Second)
	ownerID := createOrganizationTestUser(t, db, "default-owner")
	inviteeID := createOrganizationTestUser(t, db, "default-member")
	organization := createInvitationTestOrganization(t, db, ownerID)
	invitation := newPendingInvitation(organization.ID, ownerID, "default-member@example.com", "oinv_default.secret", now)
	invitation.Features = domain.DefaultMemberFeatures()
	require.NoError(t, repo.CreateInvitation(context.Background(), invitation, now))

	membership, _, err := repo.AcceptInvitation(context.Background(), crypto.SHA256Hex("oinv_default.secret"), inviteeID, now)

	require.NoError(t, err)
	assert.Equal(t, domain.DefaultMemberFeatures(), membership.Features)
}
