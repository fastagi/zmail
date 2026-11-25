package mailbox

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

func (r *fakeRepository) UpsertPendingMailboxMember(_ context.Context, pending *domain.MailboxPendingMember) error {
	email := strings.ToLower(strings.TrimSpace(pending.Email))
	now := time.Now().UTC()
	for _, existing := range r.pendingMembers {
		if existing.MailboxID == pending.MailboxID && existing.Email == email {
			existing.Permission = pending.Permission
			existing.GrantedBy = pending.GrantedBy
			existing.UpdatedAt = now
			*pending = *existing
			return nil
		}
	}
	r.nextPendingMemberID++
	stored := &domain.MailboxPendingMember{
		ID:             r.nextPendingMemberID,
		OrganizationID: pending.OrganizationID,
		MailboxID:      pending.MailboxID,
		Email:          email,
		Permission:     pending.Permission,
		GrantedBy:      pending.GrantedBy,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	r.pendingMembers = append(r.pendingMembers, stored)
	*pending = *stored
	return nil
}

func (r *fakeRepository) ClaimPendingMailboxMembers(_ context.Context, organizationID uint, email string) ([]*domain.MailboxPendingMember, error) {
	normalized := strings.ToLower(strings.TrimSpace(email))
	var claimed []*domain.MailboxPendingMember
	var remaining []*domain.MailboxPendingMember
	for _, row := range r.pendingMembers {
		if row.OrganizationID == organizationID && row.Email == normalized {
			copied := *row
			claimed = append(claimed, &copied)
		} else {
			remaining = append(remaining, row)
		}
	}
	r.pendingMembers = remaining
	return claimed, nil
}

func TestSetPendingMailboxMemberAndClaimOnJoin(t *testing.T) {
	f := newRosterFixture(t)
	ctx := context.Background()
	f.service.accounts = rosterAccounts{99: "Colleague@Example.com"}

	shared, err := f.service.CreateMailbox(ctx, as(7), sharedInput("team", "Team", "news.example.com"))
	if err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}

	// Non-manager is rejected.
	memberViewer := domain.OrganizationContext{
		OrganizationID: 7, UserID: 3, MembershipID: 300, Role: domain.OrganizationRoleMember,
	}
	if _, err := f.service.SetPendingMailboxMember(ctx, memberViewer, shared.ID, "colleague@example.com", domain.MailboxPermissionSend); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("non-manager err = %v; want ErrPermissionDenied", err)
	}

	// Invalid email or permission is rejected.
	if _, err := f.service.SetPendingMailboxMember(ctx, as(7), shared.ID, "not-an-email", domain.MailboxPermissionSend); !errors.Is(err, ErrMailboxAccessInvalid) {
		t.Fatalf("invalid email err = %v; want ErrMailboxAccessInvalid", err)
	}
	if _, err := f.service.SetPendingMailboxMember(ctx, as(7), shared.ID, "colleague@example.com", domain.MailboxPermission("owner")); !errors.Is(err, ErrMailboxAccessInvalid) {
		t.Fatalf("invalid permission err = %v; want ErrMailboxAccessInvalid", err)
	}

	// Manager creates and then idempotently updates the pending grant.
	first, err := f.service.SetPendingMailboxMember(ctx, as(7), shared.ID, "  Colleague@Example.com ", domain.MailboxPermissionView)
	if err != nil {
		t.Fatalf("SetPendingMailboxMember first: %v", err)
	}
	if first.Email != "colleague@example.com" || first.Permission != domain.MailboxPermissionView {
		t.Fatalf("first = %+v", first)
	}
	updated, err := f.service.SetPendingMailboxMember(ctx, as(7), shared.ID, "colleague@example.com", domain.MailboxPermissionSend)
	if err != nil {
		t.Fatalf("SetPendingMailboxMember update: %v", err)
	}
	if updated.ID != first.ID || updated.Permission != domain.MailboxPermissionSend {
		t.Fatalf("updated = %+v; want id=%d permission=send", updated, first.ID)
	}
	if len(f.repo.pendingMembers) != 1 {
		t.Fatalf("pendingMembers = %d; want 1", len(f.repo.pendingMembers))
	}

	// When the colleague joins the organization, MemberJoined claims the pending grant.
	event := domain.NewOrganizationMemberJoinedEvent(7, 99, 9900, time.Now().UTC())
	if err := f.service.MemberJoined(ctx, event); err != nil {
		t.Fatalf("MemberJoined: %v", err)
	}
	if len(f.repo.pendingMembers) != 0 {
		t.Fatalf("pendingMembers after join = %d; want 0", len(f.repo.pendingMembers))
	}
	member, err := f.repo.FindMailboxMember(ctx, shared.ID, 9900)
	if err != nil {
		t.Fatalf("FindMailboxMember: %v", err)
	}
	if member == nil || member.UserID != 99 || member.Permission != domain.MailboxPermissionSend || member.GrantedBy != as(7).UserID {
		t.Fatalf("claimed member = %+v", member)
	}

	// Calling MemberJoined again is a no-op and does not fail.
	if err := f.service.MemberJoined(ctx, event); err != nil {
		t.Fatalf("second MemberJoined: %v", err)
	}
}

func TestSetPendingMailboxMemberRejectsPersonalMailbox(t *testing.T) {
	f := newRosterFixture(t)
	ctx := context.Background()
	personal, err := f.service.CreateMailbox(ctx, as(7), CreateMailboxInput{
		LocalPart: "mine", Domain: "news.example.com", Type: domain.MailboxTypePersonal, OwnerUserID: as(7).UserID,
	})
	if err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	if _, err := f.service.SetPendingMailboxMember(ctx, as(7), personal.ID, "colleague@example.com", domain.MailboxPermissionSend); !errors.Is(err, ErrMailboxAccessConflict) {
		t.Fatalf("personal mailbox err = %v; want ErrMailboxAccessConflict", err)
	}
}
