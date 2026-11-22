package mailbox

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// selfBindAlice signs viewer in to alice@ with the account's own grant.
func (f *tenantFixture) selfBindAlice(t *testing.T, viewer domain.OrganizationContext) (*domain.ExternalMailConnection, error) {
	t.Helper()
	connection, _, err := f.service.CompleteExternalMailAuthorization(context.Background(), viewer,
		f.signIn(t, viewer, ExternalMailAuthorizationInput{Provider: domain.ExternalMailProviderGmail}), "alice")
	return connection, err
}

// TestTenantSelfBoundTenureCounts (N7): a member's own binding of the address is a tenure. Assigning
// the address from the tenant to someone else afterwards, whether the binding was only disconnected or
// also purged, gives them nothing from before; the same member assigned it keeps the look-back.
func TestTenantSelfBoundTenureCounts(t *testing.T) {
	for _, variant := range []string{"disconnected", "purged", "same member"} {
		t.Run(variant, func(t *testing.T) {
			f := newTenantFixture(t)
			ctx := context.Background()
			own, err := f.selfBindAlice(t, f.b)
			if err != nil {
				t.Fatalf("B binds alice@: %v", err)
			}
			f.deliverAlice("B own")
			f.sync(t)
			if !hasSubject(f.readableSubjects(t, f.b, own.MailboxID), "B own") {
				t.Fatal("B does not read their own binding")
			}
			if err = f.service.ForceDisconnectExternalMailConnection(ctx, f.admin, own.ID); err != nil {
				t.Fatalf("disconnect: %v", err)
			}
			if variant == "purged" {
				if result, purgeErr := f.service.PurgeExternalMailCopies(ctx, f.b, own.ID); purgeErr != nil || !result.Completed {
					t.Fatalf("purge = %+v, err %v", result, purgeErr)
				}
			}
			reader, viewer := uint(accessC), f.c
			if variant == "same member" {
				reader, viewer = accessB, f.b
			}
			result := f.assign(t, f.admin, personalRow("alice", reader))[0]
			if result.Connection == nil {
				t.Fatalf("assignment = %+v", result)
			}
			f.advance(3 * time.Minute)
			f.sync(t)
			subjects := f.readableSubjects(t, viewer, result.Connection.MailboxID)
			if variant == "same member" {
				if !hasSubject(subjects, "B own") {
					t.Fatalf("B lost the look-back of their own address: %v", subjects)
				}
				return
			}
			if hasSubject(subjects, "B own") {
				t.Fatalf("C reads B's mail from B's own binding: %v", subjects)
			}
		})
	}
}

// owners of alice@'s messages: "u<id>" for a member's personal tenant tenure, "s<id>" for a member's
// tenure with the account's own grant, "m<id>" for a shared mailbox's tenure.
type tenureModel struct {
	owner   map[string]string
	current string
	pending []string
	waiting bool
	early   []string
}

func (o *tenureModel) received(subject string) {
	switch {
	case o.waiting:
		o.pending = append(o.pending, subject)
	case o.current == "":
		o.early = append(o.early, subject)
	default:
		o.owner[subject] = o.current
	}
}

func (o *tenureModel) hold(key string) {
	for _, subject := range append(o.pending, o.early...) {
		o.owner[subject] = key
	}
	o.pending, o.early, o.waiting, o.current = nil, nil, false, key
}

func userKey(user uint) string       { return "u" + strconv.FormatUint(uint64(user), 10) }
func selfKey(user uint) string       { return "s" + strconv.FormatUint(uint64(user), 10) }
func mailboxKey(mailbox uint) string { return "m" + strconv.FormatUint(uint64(mailbox), 10) }

// tenureSeeds is how many random sequences the suite runs: 12 keeps the package within its time budget
// on a loaded machine. Run more with, for example,
// TENANT_TENURE_SEEDS=500 go test ./internal/modules/mailbox/ -run TestTenantTenureProperty -timeout 60m.
func tenureSeeds() uint64 {
	if raw := os.Getenv("TENANT_TENURE_SEEDS"); raw != "" {
		if n, err := strconv.ParseUint(raw, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return 12
}

// TestTenantTenureProperty (N4, N7): random sequences of personal and shared tenant assignments,
// member changes, unassignments, deletions, handovers, departures, returns, self-bindings and their
// forced disconnects, user deletions, and tenant revocations among three members. After every sync:
// nobody reads a message received outside their own tenures (a shared mailbox's tenure belongs to
// that mailbox), and whoever holds the address live reads what arrived during their tenure. One
// exception is the account's own credential: a member who binds it with the password or OAuth
// sign-in can read the account at the provider anyway, so within such a mailbox the mail of earlier
// own-grant tenures is no leak (never a tenant tenure's: that self-binding is refused). The oracle
// knows nothing of the code's floors. Run more sequences with TENANT_TENURE_SEEDS=500.
func TestTenantTenureProperty(t *testing.T) {
	users := []uint{accessB, accessC, accessD}
	for seed := uint64(1); seed <= tenureSeeds(); seed++ {
		t.Run(fmt.Sprintf("seed %d", seed), func(t *testing.T) {
			f := newTenantFixture(t)
			ctx := context.Background()
			random := rand.New(rand.NewPCG(seed, seed*7919))
			model := &tenureModel{owner: map[string]string{}}
			memberships := map[uint]uint{}
			left, deleted := map[uint]bool{}, map[uint]bool{}
			viewer := func(user uint) domain.OrganizationContext {
				v := viewerOf(accessOrg, user, domain.OrganizationRoleMember)
				if id, ok := memberships[user]; ok {
					v.MembershipID = id
				}
				return v
			}
			present := func(user uint) bool { return !left[user] && !deleted[user] }
			mailboxes, selfBound := map[uint]bool{}, map[uint]bool{}
			var connection *domain.ExternalMailConnection
			current := func() *domain.ExternalMailConnection {
				if connection == nil {
					return nil
				}
				got, err := f.service.externalMail.FindExternalMailConnection(ctx, accessOrg, connection.ID)
				if err != nil {
					return nil
				}
				return got
			}
			adopt := func(got *domain.ExternalMailConnection, key string) {
				connection = got
				mailboxes[got.MailboxID] = true
				model.hold(key)
			}
			var log []string
			for step := 0; step < 30; step++ {
				user := users[random.IntN(len(users))]
				live := current()
				action := ""
				switch random.IntN(11) {
				case 0, 1:
					action = fmt.Sprintf("assign %d", user)
					if !present(user) {
						break
					}
					result := f.assign(t, f.admin, personalRow("alice", user))[0]
					if result.Connection != nil && result.Status != TenantAssignmentUnchanged {
						adopt(result.Connection, userKey(user))
					}
				case 2:
					action = fmt.Sprintf("assign shared to %d", user)
					if !present(user) {
						break
					}
					row := ExternalMailTenantAssignmentInput{Address: tenantAddress("alice"), Assign: TenantAssignShared,
						Sharing: &domain.ExternalMailSharing{Members: []domain.ExternalMailSharingMember{{UserID: user, Permission: domain.MailboxPermissionManage}}}}
					result := f.assign(t, f.admin, row)[0]
					if result.Connection != nil && result.Status != TenantAssignmentUnchanged {
						adopt(result.Connection, mailboxKey(result.Connection.MailboxID))
					}
				case 3:
					action = fmt.Sprintf("toggle member %d", user)
					if live == nil || !live.Shared || !present(user) {
						break
					}
					if err := f.service.RemoveMailboxMember(ctx, f.admin, live.MailboxID, user); err != nil {
						_, _ = f.service.SetMailboxMember(ctx, f.admin, live.MailboxID, user, domain.MailboxPermissionView)
					}
				case 4:
					action = "unassign or disconnect"
					if live != nil {
						_ = f.service.ForceDisconnectExternalMailConnection(ctx, f.admin, live.ID)
					}
				case 5:
					action = "delete"
					if live != nil {
						_ = f.service.DeleteMailbox(ctx, f.admin, live.MailboxID)
					}
				case 6:
					action = fmt.Sprintf("handover to %d", user)
					if live != nil && !live.Shared && present(user) && userKey(user) != model.current {
						if _, err := f.service.HandOverMailbox(ctx, f.admin, live.MailboxID, user); err == nil {
							adopt(live, mailboxKey(live.MailboxID))
						}
					}
				case 7:
					action = fmt.Sprintf("self-bind %d", user)
					if !present(user) {
						break
					}
					if got, err := f.selfBindAlice(t, viewer(user)); err == nil {
						selfBound[got.MailboxID] = true
						adopt(got, selfKey(user))
					}
				case 8:
					holder := uint(0)
					for _, candidate := range users {
						if model.current == userKey(candidate) || model.current == selfKey(candidate) {
							holder = candidate
						}
					}
					if holder == 0 || deleted[holder] {
						break
					}
					if left[holder] {
						action = fmt.Sprintf("rejoin %d", holder)
						delete(f.memberships.absent, holder)
						memberships[holder] = 9000 + uint(step)
						f.memberships.ids[holder] = memberships[holder]
						if err := f.service.MemberJoined(ctx, domain.NewOrganizationMemberJoinedEvent(accessOrg, holder, memberships[holder], f.service.now())); err != nil {
							t.Fatalf("join: %v", err)
						}
						left[holder] = false
						if model.waiting {
							model.hold(userKey(holder))
						}
						break
					}
					action = fmt.Sprintf("leave %d", holder)
					f.leave(t, viewer(holder), f.advance(time.Minute))
					left[holder] = true
					if live != nil && !live.Shared && live.OwnerUserID != nil && *live.OwnerUserID == holder {
						if mailbox, err := f.repo.FindMailbox(ctx, accessOrg, live.MailboxID); err == nil && mailbox.AwaitingHandover() {
							model.waiting = true
						}
					}
				case 9:
					action = fmt.Sprintf("delete user %d", user)
					if deleted[user] {
						break
					}
					if !left[user] {
						f.leave(t, viewer(user), f.advance(time.Minute))
						left[user] = true
					}
					deleted[user] = true
					// What ON DELETE SET NULL does when the user account is deleted.
					for _, statement := range []string{
						`UPDATE mailboxes SET owner_user_id = NULL WHERE owner_user_id = ?`,
						`UPDATE external_mail_connections SET owner_user_id = NULL WHERE owner_user_id = ?`,
						`UPDATE external_mail_connections SET bound_by_user_id = NULL WHERE bound_by_user_id = ?`,
						`UPDATE external_mail_address_records SET user_id = NULL WHERE user_id = ?`,
						`UPDATE external_mail_address_records SET exempt_user_id = NULL WHERE exempt_user_id = ?`,
					} {
						if err := f.repo.(*repository).db.Exec(statement, user).Error; err != nil {
							t.Fatalf("delete user: %v", err)
						}
					}
				case 10:
					action = "revoke and re-authorize"
					if err := f.service.RevokeExternalMailTenant(ctx, f.owner, f.tenant.ID); err != nil {
						t.Fatalf("revoke: %v", err)
					}
					result, err := f.service.AuthorizeExternalMailTenant(ctx, f.owner, googleTenantInput())
					if err != nil {
						t.Fatalf("re-authorize: %v", err)
					}
					f.tenant = result.Tenant.Tenant
				}
				log = append(log, action)
				latest := fmt.Sprintf("s%d", step)
				model.received(latest)
				f.deliverAlice(latest)
				f.advance(3 * time.Minute)
				f.sync(t)

				// Safety: nobody reads a message received outside their own tenures.
				for _, reader := range users {
					if !present(reader) {
						continue
					}
					for mailboxID := range mailboxes {
						for _, read := range f.readableSubjects(t, viewer(reader), mailboxID) {
							owner := model.owner[read]
							ownCredential := selfBound[mailboxID] && len(owner) > 0 && owner[0] == 's'
							if owner != userKey(reader) && owner != selfKey(reader) && owner != mailboxKey(mailboxID) && !ownCredential {
								t.Fatalf("after %v: user %d reads %s (owner %q) in mailbox %d", log, reader, read, owner, mailboxID)
							}
						}
					}
				}
				// Liveness: whoever holds the address live reads what arrived during their tenure.
				live = current()
				if live == nil || live.Status != domain.ExternalMailStatusActive {
					continue
				}
				mailbox, err := f.repo.FindMailbox(ctx, accessOrg, live.MailboxID)
				if err != nil || mailbox.AwaitingHandover() {
					continue
				}
				var readers []uint
				if live.Shared && model.current == mailboxKey(live.MailboxID) {
					members, _ := f.repo.ListMailboxMembers(ctx, live.MailboxID)
					for _, member := range members {
						if present(member.UserID) {
							readers = append(readers, member.UserID)
						}
					}
				} else if !live.Shared && live.OwnerUserID != nil && present(*live.OwnerUserID) &&
					(model.current == userKey(*live.OwnerUserID) || model.current == selfKey(*live.OwnerUserID)) {
					readers = append(readers, *live.OwnerUserID)
				}
				for _, reader := range readers {
					if !hasSubject(f.readableSubjects(t, viewer(reader), live.MailboxID), latest) {
						t.Fatalf("after %v: holder %d cannot read %s received during their tenure", log, reader, latest)
					}
				}
			}
		})
	}
}
