package mailbox

import (
	"context"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
)

// TestRosterRepositoryClaimsAReservationOnce reserves a mailbox on PostgreSQL, claims it for the
// joining user, and checks a second claim and another email find nothing.
func TestRosterRepositoryClaimsAReservationOnce(t *testing.T) {
	repo, db := openRepository(t)
	ctx := context.Background()
	const joiner = 77
	createOwner(t, db, joiner)
	reserved := &domain.Mailbox{
		OrganizationID: 1, Address: "alice@roster.example", Type: domain.MailboxTypePersonal, PendingOwnerEmail: "alice@gmail.com",
	}
	if err := repo.CreateMailbox(ctx, reserved, domain.MailboxesPerOrganizationCeiling); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	stored, err := repo.FindMailboxByAddress(ctx, reserved.Address)
	if err != nil || stored.PendingOwnerEmail != "alice@gmail.com" || stored.OwnerUserID != nil {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
	if owns, ownsErr := repo.OwnsPersonalMailbox(ctx, 1, joiner); ownsErr != nil || owns {
		t.Fatalf("owns before claim = %v, %v", owns, ownsErr)
	}

	other, err := repo.ClaimReservedMailboxes(ctx, 1, joiner, "mallory@gmail.com")
	if err != nil || len(other) != 0 {
		t.Fatalf("claim by another email = %v, %v", other, err)
	}
	wrongOrganization, err := repo.ClaimReservedMailboxes(ctx, 2, joiner, "alice@gmail.com")
	if err != nil || len(wrongOrganization) != 0 {
		t.Fatalf("claim in another organization = %v, %v", wrongOrganization, err)
	}
	claimed, err := repo.ClaimReservedMailboxes(ctx, 1, joiner, "alice@gmail.com")
	if err != nil || len(claimed) != 1 || claimed[0].OwnerUserID == nil || *claimed[0].OwnerUserID != joiner || claimed[0].PendingOwnerEmail != "" {
		t.Fatalf("claimed = %+v, %v", claimed, err)
	}
	again, err := repo.ClaimReservedMailboxes(ctx, 1, joiner, "alice@gmail.com")
	if err != nil || len(again) != 0 {
		t.Fatalf("second claim = %v, %v", again, err)
	}
	if owns, ownsErr := repo.OwnsPersonalMailbox(ctx, 1, joiner); ownsErr != nil || !owns {
		t.Fatalf("owns after claim = %v, %v", owns, ownsErr)
	}
}
