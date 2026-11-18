package organization

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fastagi/zmail/api/internal/domain"
	infraevents "github.com/fastagi/zmail/api/internal/infra/events"
)

// recordingPublisher records published events; err makes every publication fail.
type recordingPublisher struct {
	published []infraevents.BasicEvent
	err       error
}

func (p *recordingPublisher) Publish(_ context.Context, event infraevents.BasicEvent) error {
	p.published = append(p.published, event)
	return p.err
}

func TestRemoveMemberPublishesRemovedAfterCommit(t *testing.T) {
	now := time.Date(2026, time.November, 19, 10, 0, 0, 0, time.UTC)
	committed := false
	repo := &fakeRepository{removeFn: func(_ context.Context, organizationID, userID, memberID uint) (*domain.OrganizationMembership, error) {
		committed = true
		return &domain.OrganizationMembership{ID: memberID, OrganizationID: organizationID, UserID: 7, Role: domain.OrganizationRoleMember}, nil
	}}
	publisher := &recordingPublisher{}
	svc := NewService(repo, nil, nil, InvitationPolicy{TTL: time.Hour}, publisher)
	svc.now = func() time.Time { return now }

	require.NoError(t, svc.RemoveMember(context.Background(), 3, 42, 30))
	require.True(t, committed)
	require.Len(t, publisher.published, 1)
	removed, ok := publisher.published[0].(domain.OrganizationMemberRemovedEvent)
	require.True(t, ok, "published %T", publisher.published[0])
	assert.Equal(t, domain.EventOrganizationMemberRemoved, removed.EventName())
	assert.Equal(t, uint(42), removed.OrganizationID)
	assert.Equal(t, uint(7), removed.UserID)
	assert.Equal(t, uint(30), removed.MembershipID)
	assert.True(t, now.Equal(removed.OccurredAt()))

	// A failed removal publishes nothing.
	repo.removeFn = func(context.Context, uint, uint, uint) (*domain.OrganizationMembership, error) {
		return nil, domain.ErrPermissionDenied
	}
	require.Error(t, svc.RemoveMember(context.Background(), 3, 42, 30))
	assert.Len(t, publisher.published, 1)

	// A failing subscriber never fails the committed removal.
	repo.removeFn = func(_ context.Context, organizationID, _, memberID uint) (*domain.OrganizationMembership, error) {
		return &domain.OrganizationMembership{ID: memberID, OrganizationID: organizationID, UserID: 7}, nil
	}
	publisher.err = errors.New("subscriber failed")
	require.NoError(t, svc.RemoveMember(context.Background(), 3, 42, 30))
}

func TestAcceptInvitationPublishesJoinedAfterCommit(t *testing.T) {
	now := time.Date(2026, time.November, 19, 10, 0, 0, 0, time.UTC)
	repo := &invitationRepositoryStub{
		acceptInvitationFn: func(_ context.Context, _ string, userID uint, _ time.Time) (*domain.OrganizationMembership, *domain.OrganizationInvitation, error) {
			return &domain.OrganizationMembership{ID: 55, OrganizationID: 42, UserID: userID, Role: domain.OrganizationRoleMember},
				&domain.OrganizationInvitation{ID: 73, OrganizationID: 42}, nil
		},
	}
	publisher := &recordingPublisher{}
	svc := NewService(repo, repo, &invitationMailerStub{}, InvitationPolicy{TTL: time.Hour}, publisher)
	svc.now = func() time.Time { return now }

	_, err := svc.AcceptInvitation(context.Background(), 29, &AcceptOrganizationInvitationRequest{Token: "oinv_test.secret"})
	require.NoError(t, err)
	require.Len(t, publisher.published, 1)
	joined, ok := publisher.published[0].(domain.OrganizationMemberJoinedEvent)
	require.True(t, ok)
	assert.Equal(t, domain.EventOrganizationMemberJoined, joined.EventName())
	assert.Equal(t, uint(42), joined.OrganizationID)
	assert.Equal(t, uint(29), joined.UserID)
	assert.Equal(t, uint(55), joined.MembershipID)

	repo.acceptInvitationFn = func(context.Context, string, uint, time.Time) (*domain.OrganizationMembership, *domain.OrganizationInvitation, error) {
		return nil, nil, domain.ErrOrganizationInvitationInvalid
	}
	_, err = svc.AcceptInvitation(context.Background(), 29, &AcceptOrganizationInvitationRequest{Token: "oinv_test.secret"})
	require.Error(t, err)
	assert.Len(t, publisher.published, 1, "a refused acceptance publishes nothing")
}

func TestCreatePublishesJoinedForTheOwner(t *testing.T) {
	repo := &fakeRepository{createFn: func(_ context.Context, organization *domain.Organization, owner *domain.OrganizationMembership) error {
		organization.ID, owner.ID, owner.OrganizationID = 42, 91, 42
		return nil
	}}
	publisher := &recordingPublisher{}
	svc := NewService(repo, nil, nil, InvitationPolicy{TTL: time.Hour}, publisher)

	_, err := svc.Create(context.Background(), 17, &CreateOrganizationRequest{Name: "Acme", Slug: "acme"})
	require.NoError(t, err)
	require.Len(t, publisher.published, 1)
	joined, ok := publisher.published[0].(domain.OrganizationMemberJoinedEvent)
	require.True(t, ok)
	assert.Equal(t, uint(42), joined.OrganizationID)
	assert.Equal(t, uint(17), joined.UserID)
	assert.Equal(t, uint(91), joined.MembershipID)
}
