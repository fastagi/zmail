package mailbox

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
)

func TestMailboxCeilingMatchesConfig(t *testing.T) {
	if domain.MailboxesPerOrganizationCeiling != config.MailboxMaxPerOrganizationCeiling {
		t.Fatalf("domain ceiling %d != config ceiling %d",
			domain.MailboxesPerOrganizationCeiling, config.MailboxMaxPerOrganizationCeiling)
	}
}

func TestSettingsFromConfigTakesTheMailboxLimit(t *testing.T) {
	if got := SettingsFromConfig(nil).MaxMailboxes; got != config.DefaultMailboxMaxPerOrganization {
		t.Fatalf("default MaxMailboxes = %d; want %d", got, config.DefaultMailboxMaxPerOrganization)
	}
	cfg := &config.Config{}
	cfg.Mailbox.MaxPerOrganization = 75
	if got := SettingsFromConfig(cfg).MaxMailboxes; got != 75 {
		t.Fatalf("MaxMailboxes = %d; want 75", got)
	}
}

// limitService is an access-scenario service on the given repository with its own mailbox limit.
func limitService(repo domain.MailboxRepository, memberships *fakeMemberships, maxMailboxes int) *service {
	settings := accessSettings()
	settings.MaxMailboxes = maxMailboxes
	return NewService(repo, memberships, nil, newFakeContacts(), &stubBroadcasts{}, nil,
		nil, nil, nil, &fakeTransport{}, &fakeNotifier{}, settings, nil, nil, nil, nil)
}

// sharedFor is a shared mailbox C views.
func sharedFor(localPart string) CreateMailboxInput {
	return CreateMailboxInput{LocalPart: localPart, Type: domain.MailboxTypeShared,
		Members: []MailboxMemberInput{{UserID: accessC, Permission: domain.MailboxPermissionView}}}
}

func TestCreateUsesConfiguredLimit(t *testing.T) {
	svc := limitService(newFakeRepository(), newAccessMemberships(), 3)
	admin := viewerOf(accessOrg, accessAdmin, domain.OrganizationRoleAdmin)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := svc.CreateMailbox(ctx, admin, sharedFor(fmt.Sprintf("box%d", i))); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	if _, err := svc.CreateMailbox(ctx, admin, sharedFor("box3")); !errors.Is(err, ErrMailboxLimitReached) {
		t.Fatalf("fourth create error = %v; want ErrMailboxLimitReached", err)
	}
	if svc.MaxMailboxes() != 3 {
		t.Fatalf("MaxMailboxes() = %d", svc.MaxMailboxes())
	}
}

func TestUnsetLimitTakesTheDefault(t *testing.T) {
	svc := limitService(newFakeRepository(), newAccessMemberships(), 0)
	if svc.MaxMailboxes() != config.DefaultMailboxMaxPerOrganization {
		t.Fatalf("MaxMailboxes() = %d; want the default", svc.MaxMailboxes())
	}
}

// TestLoweredLimitKeepsExistingMailboxes is Review Focus 1: lowering the limit below the current
// count hides nothing and only refuses new mailboxes.
func TestLoweredLimitKeepsExistingMailboxes(t *testing.T) {
	f := newPostgresAccessFixture(t)
	ctx := context.Background()
	if _, err := f.service.CreateMailbox(ctx, f.admin, sharedFor("third")); err != nil {
		t.Fatalf("create third: %v", err)
	}
	lowered := limitService(f.repo, f.memberships, 2)

	all, err := lowered.ListMailboxes(ctx, f.admin, MailboxListScopeAll)
	if err != nil || len(all) != 3 {
		t.Fatalf("admin list = %d mailboxes, %v; want 3", len(all), err)
	}
	usable, err := lowered.ListMailboxes(ctx, f.b, MailboxListScopeUsable)
	if err != nil || !slices.ContainsFunc(usable, func(m *domain.Mailbox) bool { return m.ID == f.team.ID }) {
		t.Fatalf("B's usable list = %v, %v; want team@", addressesOf(usable), err)
	}
	if _, err := lowered.CreateMailbox(ctx, f.admin, sharedFor("fourth")); !errors.Is(err, ErrMailboxLimitReached) {
		t.Fatalf("create under a lowered limit = %v; want ErrMailboxLimitReached", err)
	}
	if err := lowered.DeleteMailbox(ctx, f.admin, f.team.ID); err != nil {
		t.Fatalf("delete under a lowered limit: %v", err)
	}
}

func TestUnscopedListReturnsMoreThan50(t *testing.T) {
	repo, db := openRepository(t)
	createOwner(t, db, accessOrg)
	svc := limitService(repo, newAccessMemberships(), 60)
	admin := viewerOf(accessOrg, accessAdmin, domain.OrganizationRoleAdmin)
	ctx := context.Background()
	for i := 0; i < 60; i++ {
		if _, err := svc.CreateMailbox(ctx, admin, sharedFor(fmt.Sprintf("box%d", i))); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	all, err := svc.ListMailboxes(ctx, admin, MailboxListScopeAll)
	if err != nil || len(all) != 60 {
		t.Fatalf("list = %d mailboxes, %v; want 60", len(all), err)
	}
}

func TestMembershipRowsBeyond50(t *testing.T) {
	repo, db := openRepository(t)
	createOwner(t, db, accessOrg)
	svc := limitService(repo, newAccessMemberships(), 60)
	admin := viewerOf(accessOrg, accessAdmin, domain.OrganizationRoleAdmin)
	ctx := context.Background()
	for i := 0; i < 55; i++ {
		if _, err := svc.CreateMailbox(ctx, admin, sharedFor(fmt.Sprintf("box%d", i))); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	membership := membershipIDOf(accessOrg, accessC)
	rows, err := repo.ListMembershipMailboxMembers(ctx, accessOrg, membership)
	if err != nil || len(rows) != 55 {
		t.Fatalf("member rows = %d, %v; want 55", len(rows), err)
	}
	granted, err := repo.MailboxesGrantedTo(ctx, accessOrg, membership)
	if err != nil || len(granted) != 55 {
		t.Fatalf("granted = %d, %v; want 55", len(granted), err)
	}
	usable, err := repo.ListUsableMailboxes(ctx, accessOrg, membership, accessC, false)
	if err != nil || len(usable) != 55 {
		t.Fatalf("usable = %d, %v; want 55", len(usable), err)
	}
}

func addressesOf(mailboxes []*domain.Mailbox) []string {
	addresses := make([]string, 0, len(mailboxes))
	for _, mailbox := range mailboxes {
		addresses = append(addresses, mailbox.Address)
	}
	return addresses
}

func TestUsableScope(t *testing.T) {
	for name, build := range map[string]func(*testing.T) *accessFixture{
		"fake":       newAccessFixture,
		"postgresql": newPostgresAccessFixture,
	} {
		t.Run(name, func(t *testing.T) {
			f := build(t)
			ctx := context.Background()
			list := func(viewer domain.OrganizationContext, scope MailboxListScope) []string {
				t.Helper()
				mailboxes, err := f.service.ListMailboxes(ctx, viewer, scope)
				if err != nil {
					t.Fatalf("ListMailboxes: %v", err)
				}
				return addressesOf(mailboxes)
			}
			if got := list(f.admin, MailboxListScopeUsable); len(got) != 0 {
				t.Fatalf("admin usable = %v; want none", got)
			}
			if got := list(f.b, MailboxListScopeUsable); !slices.Equal(got, []string{"b@mail.example", "team@mail.example"}) {
				t.Fatalf("B usable = %v", got)
			}
			if got := list(f.c, MailboxListScopeUsable); !slices.Equal(got, []string{"team@mail.example"}) {
				t.Fatalf("C usable = %v", got)
			}
			if got := list(f.d, MailboxListScopeUsable); len(got) != 0 {
				t.Fatalf("D usable = %v; want none", got)
			}
			if got := list(f.admin, MailboxListScopeAll); len(got) != 2 {
				t.Fatalf("admin all = %v; want 2", got)
			}
			usable, err := f.service.ListMailboxes(ctx, f.c, MailboxListScopeUsable)
			if err != nil || usable[0].UnreadCount != 1 || !usable[0].Access.View {
				t.Fatalf("C's team@ = %+v, %v; want access and one unread", usable[0], err)
			}
		})
	}
}

func TestUsableScopeKeepsAwaitingHandoverForAdmins(t *testing.T) {
	for name, build := range map[string]func(*testing.T) *accessFixture{
		"fake":       newAccessFixture,
		"postgresql": newPostgresAccessFixture,
	} {
		t.Run(name, func(t *testing.T) {
			f := build(t)
			ctx := context.Background()
			f.leave(t, f.b, time.Now().UTC())
			usable, err := f.service.ListMailboxes(ctx, f.admin, MailboxListScopeUsable)
			if err != nil || len(usable) != 1 || usable[0].ID != f.personal.ID || usable[0].OwnerLeftAt == nil {
				t.Fatalf("admin usable = %v, %v; want b@ awaiting handover", addressesOf(usable), err)
			}
			theirs, err := f.service.ListMailboxes(ctx, f.c, MailboxListScopeUsable)
			if err != nil || slices.Contains(addressesOf(theirs), "b@mail.example") {
				t.Fatalf("C usable = %v, %v; must not hold b@", addressesOf(theirs), err)
			}
		})
	}
}
