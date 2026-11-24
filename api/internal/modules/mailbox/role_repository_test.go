package mailbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
)

const (
	roleOrg      = 1
	otherRoleOrg = 2
)

// newRoleRepository opens a repository with two organizations. The schema has the migration's
// mailboxes.role_id foreign key, so deletes and assignments meet it as in production.
func newRoleRepository(t *testing.T) (*repository, *gorm.DB) {
	t.Helper()
	repo, db := openRepository(t)
	createOwner(t, db, roleOrg)
	createOwner(t, db, otherRoleOrg)
	return repo, db
}

func createRole(t *testing.T, repo *repository, organizationID uint, name string) *domain.MailRole {
	t.Helper()
	role := &domain.MailRole{OrganizationID: organizationID, Name: name, Color: domain.MailLabelColorBlue, Description: "Who writes " + name}
	if err := repo.CreateMailRole(context.Background(), role, domain.MaxMailRolesPerOrganization); err != nil {
		t.Fatalf("CreateMailRole(%q): %v", name, err)
	}
	return role
}

func createOrganizationMailbox(t *testing.T, repo *repository, organizationID uint, address string) *domain.Mailbox {
	t.Helper()
	mailbox := &domain.Mailbox{OrganizationID: organizationID, Address: address, Type: domain.MailboxTypeShared}
	if err := repo.CreateMailbox(context.Background(), mailbox, domain.MailboxesPerOrganizationCeiling); err != nil {
		t.Fatalf("CreateMailbox(%q): %v", address, err)
	}
	return mailbox
}

func assignRole(t *testing.T, repo *repository, mailboxID uint, roleID *uint) {
	t.Helper()
	if err := repo.AssignMailboxRole(context.Background(), roleOrg, mailboxID, roleID); err != nil {
		t.Fatalf("AssignMailboxRole(%d, %v): %v", mailboxID, roleID, err)
	}
}

// storedRoleID reads mailboxes.role_id, deleted or not.
func storedRoleID(t *testing.T, db *gorm.DB, mailboxID uint) *uint {
	t.Helper()
	var po MailboxPO
	if err := db.Unscoped().First(&po, mailboxID).Error; err != nil {
		t.Fatalf("load mailbox %d: %v", mailboxID, err)
	}
	return po.RoleID
}

func expectOptionalID(t *testing.T, name string, got *uint, want *uint) {
	t.Helper()
	switch {
	case want == nil && got != nil:
		t.Fatalf("%s: id = %d; want none", name, *got)
	case want != nil && (got == nil || *got != *want):
		t.Fatalf("%s: id = %v; want %d", name, got, *want)
	}
}

func roleNames(roles []*domain.MailRole) string {
	names := make([]string, 0, len(roles))
	for _, role := range roles {
		names = append(names, role.Name)
	}
	return strings.Join(names, ",")
}

func TestMailRoleCreateFindAndList(t *testing.T) {
	repo, _ := newRoleRepository(t)
	ctx := context.Background()
	owner := uint(42)
	sales := &domain.MailRole{OrganizationID: roleOrg, Name: "sales", Color: domain.MailLabelColorGreen, Description: "Deals", OwnerUserID: &owner}
	if err := repo.CreateMailRole(ctx, sales, domain.MaxMailRolesPerOrganization); err != nil {
		t.Fatalf("CreateMailRole: %v", err)
	}
	if sales.ID == 0 || sales.CreatedAt.IsZero() || sales.UpdatedAt.IsZero() {
		t.Fatalf("created role = %+v; want an id and timestamps", sales)
	}
	createRole(t, repo, roleOrg, "Design")
	createRole(t, repo, roleOrg, "billing")
	createRole(t, repo, otherRoleOrg, "Accounts")

	found, err := repo.FindMailRole(ctx, roleOrg, sales.ID)
	if err != nil {
		t.Fatalf("FindMailRole: %v", err)
	}
	if found.Name != "sales" || found.Color != domain.MailLabelColorGreen || found.Description != "Deals" ||
		found.OwnerUserID == nil || *found.OwnerUserID != owner || found.OrganizationID != roleOrg || found.MailboxIDs != nil {
		t.Fatalf("found role = %+v; want the stored sales role", found)
	}
	_, err = repo.FindMailRole(ctx, otherRoleOrg, sales.ID)
	expectError(t, "role of another organization", err, ErrMailRoleNotFound)

	roles, err := repo.ListMailRoles(ctx, roleOrg)
	if err != nil {
		t.Fatalf("ListMailRoles: %v", err)
	}
	if got := roleNames(roles); got != "billing,Design,sales" {
		t.Fatalf("roles = %s; want billing,Design,sales by name ignoring case", got)
	}
}

func TestMailRoleNameUniqueIgnoringCase(t *testing.T) {
	repo, _ := newRoleRepository(t)
	ctx := context.Background()
	createRole(t, repo, roleOrg, "Design")

	// The service trims names before they reach the repository.
	again := &domain.MailRole{OrganizationID: roleOrg, Name: strings.TrimSpace(" design "), Color: domain.MailLabelColorGray}
	expectError(t, "create a taken name in other case", repo.CreateMailRole(ctx, again, domain.MaxMailRolesPerOrganization), ErrMailRoleNameTaken)

	createRole(t, repo, otherRoleOrg, "design")
	other := createRole(t, repo, roleOrg, "Sales")
	other.Name = strings.TrimSpace("design ")
	expectError(t, "rename another role to a taken name", repo.UpdateMailRole(ctx, other), ErrMailRoleNameTaken)
	stored, err := repo.FindMailRole(ctx, roleOrg, other.ID)
	if err != nil || stored.Name != "Sales" {
		t.Fatalf("role after the refused rename = %+v, %v; want Sales", stored, err)
	}
}

// Review focus: a role renamed to its own name in another case keeps it.
func TestMailRoleRenameOwnCaseIsAllowed(t *testing.T) {
	repo, _ := newRoleRepository(t)
	ctx := context.Background()
	design := createRole(t, repo, roleOrg, "Design")
	created := design.UpdatedAt
	time.Sleep(5 * time.Millisecond)

	owner := uint(7)
	design.Name, design.Color, design.Description, design.OwnerUserID = "DESIGN", domain.MailLabelColorPink, "", &owner
	if err := repo.UpdateMailRole(ctx, design); err != nil {
		t.Fatalf("rename Design to DESIGN: %v", err)
	}
	if design.Name != "DESIGN" || design.Color != domain.MailLabelColorPink || design.Description != "" ||
		design.OwnerUserID == nil || *design.OwnerUserID != owner || !design.UpdatedAt.After(created) {
		t.Fatalf("updated role = %+v; want the replaced fields and a newer updated_at", design)
	}

	design.OwnerUserID = nil
	if err := repo.UpdateMailRole(ctx, design); err != nil {
		t.Fatalf("clear the owner: %v", err)
	}
	if design.OwnerUserID != nil {
		t.Fatalf("owner = %d; want none after the replace", *design.OwnerUserID)
	}

	missing := &domain.MailRole{ID: design.ID, OrganizationID: otherRoleOrg, Name: "X", Color: domain.MailLabelColorGray}
	expectError(t, "update in another organization", repo.UpdateMailRole(ctx, missing), ErrMailRoleNotFound)
}

func TestMailRoleDeleteUngroupsMailboxes(t *testing.T) {
	repo, db := newRoleRepository(t)
	ctx := context.Background()
	design := createRole(t, repo, roleOrg, "Design")
	sales := createRole(t, repo, roleOrg, "Sales")
	one := createOrganizationMailbox(t, repo, roleOrg, "one@mail.example")
	two := createOrganizationMailbox(t, repo, roleOrg, "two@mail.example")
	kept := createOrganizationMailbox(t, repo, roleOrg, "kept@mail.example")
	assignRole(t, repo, one.ID, &design.ID)
	assignRole(t, repo, two.ID, &design.ID)
	assignRole(t, repo, kept.ID, &sales.ID)
	if err := db.Exec(`UPDATE mailboxes SET deleted_at = now() WHERE id = ?`, two.ID).Error; err != nil {
		t.Fatalf("delete mailbox: %v", err)
	}

	expectError(t, "delete from another organization", repo.DeleteMailRole(ctx, otherRoleOrg, design.ID), ErrMailRoleNotFound)
	if err := repo.DeleteMailRole(ctx, roleOrg, design.ID); err != nil {
		t.Fatalf("DeleteMailRole: %v", err)
	}
	expectOptionalID(t, "mailbox of the deleted role", storedRoleID(t, db, one.ID), nil)
	expectOptionalID(t, "deleted mailbox of the deleted role", storedRoleID(t, db, two.ID), nil)
	expectOptionalID(t, "mailbox of another role", storedRoleID(t, db, kept.ID), &sales.ID)
	_, err := repo.FindMailRole(ctx, roleOrg, design.ID)
	expectError(t, "find the deleted role", err, ErrMailRoleNotFound)
	expectError(t, "delete twice", repo.DeleteMailRole(ctx, roleOrg, design.ID), ErrMailRoleNotFound)
}

func TestMailRoleAssignAcrossOrganizationsIsNotFound(t *testing.T) {
	repo, db := newRoleRepository(t)
	ctx := context.Background()
	design := createRole(t, repo, roleOrg, "Design")
	foreignRole := createRole(t, repo, otherRoleOrg, "Design")
	mailbox := createOrganizationMailbox(t, repo, roleOrg, "team@mail.example")
	foreignMailbox := createOrganizationMailbox(t, repo, otherRoleOrg, "foreign@mail.example")

	expectError(t, "role of another organization", repo.AssignMailboxRole(ctx, roleOrg, mailbox.ID, &foreignRole.ID), ErrMailRoleNotFound)
	expectOptionalID(t, "mailbox after the refused assignment", storedRoleID(t, db, mailbox.ID), nil)
	expectError(t, "mailbox of another organization", repo.AssignMailboxRole(ctx, roleOrg, foreignMailbox.ID, &design.ID), ErrMailboxNotFound)
	expectError(t, "missing mailbox and role", repo.AssignMailboxRole(ctx, roleOrg, 999_999, uintPtr(999_998)), ErrMailboxNotFound)
	expectError(t, "ungroup a mailbox of another organization", repo.AssignMailboxRole(ctx, roleOrg, foreignMailbox.ID, nil), ErrMailboxNotFound)
	expectOptionalID(t, "foreign mailbox", storedRoleID(t, db, foreignMailbox.ID), nil)

	before := storedMailboxUpdatedAt(t, db, mailbox.ID)
	time.Sleep(5 * time.Millisecond)
	assignRole(t, repo, mailbox.ID, &design.ID)
	expectOptionalID(t, "assigned mailbox", storedRoleID(t, db, mailbox.ID), &design.ID)
	if after := storedMailboxUpdatedAt(t, db, mailbox.ID); !after.After(before) {
		t.Fatalf("updated_at = %v; want after %v", after, before)
	}
	assignRole(t, repo, mailbox.ID, &design.ID)
	expectOptionalID(t, "assigned the same role again", storedRoleID(t, db, mailbox.ID), &design.ID)
	assignRole(t, repo, mailbox.ID, nil)
	expectOptionalID(t, "ungrouped mailbox", storedRoleID(t, db, mailbox.ID), nil)

	if err := db.Exec(`UPDATE mailboxes SET deleted_at = now() WHERE id = ?`, mailbox.ID).Error; err != nil {
		t.Fatalf("delete mailbox: %v", err)
	}
	expectError(t, "deleted mailbox", repo.AssignMailboxRole(ctx, roleOrg, mailbox.ID, &design.ID), ErrMailboxNotFound)
}

func storedMailboxUpdatedAt(t *testing.T, db *gorm.DB, mailboxID uint) time.Time {
	t.Helper()
	var po MailboxPO
	if err := db.Unscoped().First(&po, mailboxID).Error; err != nil {
		t.Fatalf("load mailbox %d: %v", mailboxID, err)
	}
	return po.UpdatedAt
}

// An assignment racing the role's delete waits for it and then finds the role gone, instead of
// storing a dangling role_id or failing on the foreign key.
func TestMailRoleAssignDeletedRoleIsNotFound(t *testing.T) {
	repo, db := newRoleRepository(t)
	ctx := context.Background()
	design := createRole(t, repo, roleOrg, "Design")
	mailbox := createOrganizationMailbox(t, repo, roleOrg, "team@mail.example")

	gone := createRole(t, repo, roleOrg, "Gone")
	if err := repo.DeleteMailRole(ctx, roleOrg, gone.ID); err != nil {
		t.Fatalf("DeleteMailRole: %v", err)
	}
	expectError(t, "role deleted before", repo.AssignMailboxRole(ctx, roleOrg, mailbox.ID, &gone.ID), ErrMailRoleNotFound)

	widenPool(t, db)
	deleting := db.Begin()
	defer deleting.Rollback()
	var pid int
	if err := deleting.Raw(`SELECT pg_backend_pid()`).Scan(&pid).Error; err != nil {
		t.Fatalf("backend pid: %v", err)
	}
	if err := deleting.Exec(`DELETE FROM mail_roles WHERE id = ?`, design.ID).Error; err != nil {
		t.Fatalf("delete the role: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- repo.AssignMailboxRole(ctx, roleOrg, mailbox.ID, &design.ID) }()
	waitUntilBlockedBy(t, db, pid)
	if err := deleting.Commit().Error; err != nil {
		t.Fatalf("commit the delete: %v", err)
	}
	select {
	case err := <-done:
		expectError(t, "role deleted meanwhile", err, ErrMailRoleNotFound)
	case <-time.After(10 * time.Second):
		t.Fatal("the assignment never finished")
	}
	expectOptionalID(t, "mailbox after the race", storedRoleID(t, db, mailbox.ID), nil)
}

// Review: two creates racing for the last free role slot; the advisory lock lets exactly one in.
func TestMailRoleConcurrentCreateStopsAt50(t *testing.T) {
	repo, db := newRoleRepository(t)
	ctx := context.Background()
	for i := range domain.MaxMailRolesPerOrganization - 1 {
		createRole(t, repo, roleOrg, fmt.Sprintf("Role %02d", i))
	}
	widenPool(t, db)

	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := range results {
		wg.Go(func() {
			role := &domain.MailRole{OrganizationID: roleOrg, Name: fmt.Sprintf("Racer %d", i), Color: domain.MailLabelColorGray}
			results[i] = repo.CreateMailRole(ctx, role, domain.MaxMailRolesPerOrganization)
		})
	}
	wg.Wait()

	limited := 0
	for _, err := range results {
		switch {
		case errors.Is(err, ErrMailRoleLimitReached):
			limited++
		case err != nil:
			t.Fatalf("racing create: %v", err)
		}
	}
	if limited != 1 {
		t.Fatalf("limited creates = %d (%v); want exactly one", limited, results)
	}
	roles, err := repo.ListMailRoles(ctx, roleOrg)
	if err != nil || len(roles) != domain.MaxMailRolesPerOrganization {
		t.Fatalf("roles = %d, %v; want %d", len(roles), err, domain.MaxMailRolesPerOrganization)
	}
	createRole(t, repo, otherRoleOrg, "Another organization still has room")
}

func TestMailRoleClearOwner(t *testing.T) {
	repo, _ := newRoleRepository(t)
	ctx := context.Background()
	leaving, staying := uint(3), uint(4)
	owned := func(organizationID uint, name string, owner uint) *domain.MailRole {
		role := &domain.MailRole{OrganizationID: organizationID, Name: name, Color: domain.MailLabelColorGray, OwnerUserID: &owner}
		if err := repo.CreateMailRole(ctx, role, domain.MaxMailRolesPerOrganization); err != nil {
			t.Fatalf("CreateMailRole(%q): %v", name, err)
		}
		return role
	}
	first := owned(roleOrg, "Design", leaving)
	second := owned(roleOrg, "Sales", leaving)
	other := owned(roleOrg, "Support", staying)
	foreign := owned(otherRoleOrg, "Design", leaving)

	cleared, err := repo.ClearMailRoleOwner(ctx, roleOrg, leaving)
	if err != nil || cleared != 2 {
		t.Fatalf("ClearMailRoleOwner = %d, %v; want 2", cleared, err)
	}
	for _, tc := range []struct {
		role *domain.MailRole
		want *uint
	}{{first, nil}, {second, nil}, {other, &staying}, {foreign, &leaving}} {
		stored, err := repo.FindMailRole(ctx, tc.role.OrganizationID, tc.role.ID)
		if err != nil {
			t.Fatalf("FindMailRole(%q): %v", tc.role.Name, err)
		}
		expectOptionalID(t, "owner of "+tc.role.Name, stored.OwnerUserID, tc.want)
	}
	if cleared, err := repo.ClearMailRoleOwner(ctx, roleOrg, leaving); err != nil || cleared != 0 {
		t.Fatalf("second ClearMailRoleOwner = %d, %v; want 0", cleared, err)
	}
}
