# Mailbox Access Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every mailbox is either one member's personal mailbox or a shared mailbox with named
members, and no route, report, or webhook reveals mail to anyone else.

**Architecture:** One pure domain function turns a mailbox, the request's verified
`OrganizationContext`, and the viewer's member row into an access value; every mailbox service
path goes through one chokepoint before touching messages. Member rows and per-person read rows
reference `organization_memberships(id)` with `ON DELETE CASCADE`, so leaving the organization
removes them in the same database transaction and a rejoin (new membership id) cannot revive them.
A personal mailbox's owner is keyed by user, so a returning owner gets it back until it is handed
over. Handover stores a seal boundary; messages created before it are invisible to everyone.

**Tech Stack:** Go (Gin, GORM, PostgreSQL, Wire), Next.js 16 / React 19 Web console, OpenAPI.

**Spec:** [docs/MAILBOX_ACCESS_PRD.zh-CN.md](../MAILBOX_ACCESS_PRD.zh-CN.md) (approved). Changes
[mailbox-organizations.md](mailbox-organizations.md), which deferred access rules per mailbox.
Written to [BUSINESS_IMPLEMENTATION_PLAN_STANDARD.md](../BUSINESS_IMPLEMENTATION_PLAN_STANDARD.md).

## Global Constraints

- Permission levels are exactly `view`, `send`, `manage`, each including the previous.
- Organization owner/admin roles never grant reading or sending mail by themselves.
- No capability at all on a mailbox: `404 MAILBOX.NOT_FOUND`. Some capability but not the needed
  one: `403 PERMISSION.DENIED`.
- Read state is per person; API key reads never change it.
- Messages created before a mailbox's seal boundary are invisible on every surface.
- PostgreSQL only; new migrations are frozen SQL; code and contract text are English.
- At most 100 members per shared mailbox; 50 mailboxes per organization (unchanged).

## Review Focus

1. A member removed from a shared mailbox while still in the organization still has read rows
   and an open browser tab → next request 404, and the read rows are gone. Test:
   `TestRemovingMemberDeletesTheirReads` (Task 1.1).
2. A delivery event (bounce) arrives after handover for a message sent before the boundary → not
   visible, not counted, no webhook. Test: `TestEventsOfSealedMessagesStayHidden` (Task 3.1).
   Decision: a permanent bounce or complaint for a sealed message still suppresses the contact.
   Suppression is organization-wide deliverability hygiene, not mailbox history, so sealing does
   not exempt it.
3. Two admins hand over the same pending mailbox at once → exactly one succeeds, the other gets
   `409 MAILBOX.ACCESS_CONFLICT`. Test: `TestConcurrentHandoverHasOneWinner` (Task 3.1).
4. An admin who is a shared member searches the log for a subject in another member's personal
   mailbox → no row and the total excludes it. Test: `TestLogSkipsUnreadableMailboxes` (Task 1.1).
5. An owner leaves, rejoins, and leaves again before handover → the boundary is the latest
   departure, so mail they could read while back is sealed. Test:
   `TestBoundaryIsLatestDeparture` (Task 3.1).

---

## 1. Executive Summary

**Outcome.** A team gives each employee a private mailbox and shares business mailboxes with
exactly the people who work them. Admins run the mailboxes without silently reading them. When
an employee leaves, their address keeps working for a successor while their earlier mail stays
sealed.

**Problem.** Today every member reads and sends from every mailbox, read state is shared, and
organization webhooks carry every message.

**In scope.** Mailbox type (personal or shared), member permissions, access enforcement on every
mailbox, message, log, statistics, broadcast, template test, API-key, and webhook path; per-person
read state; departure, handover, sealing, rejoin, deletion of sealed history; per-mailbox webhook
switch; access history; Web console surfaces.

**Non-goals.** Aliases and bulk provisioning, Gmail/Outlook, Agent access and AI triage,
organization-wide audit page, group grants, retention limits for sealed history.

**Deployable units.** `api/` and `web/` change; `admin/` only regenerates contract types.
`contracts/MAILBOXES.md` and `contracts/openapi.yaml` change.

**Success signal.** Acceptance cases AC-1 to AC-17 of the PRD pass against a running API with
zero successful cross-access attempts.

## 2. Actors and Access Surfaces

| Actor | Goal | Entry points | Read scope | Mutation scope | Denial |
|---|---|---|---|---|---|
| Member (any role) holding `view`/`send`/`manage` on a shared mailbox | Work the mailbox | `/v1/mailboxes/:id/messages...`, log, stats, broadcasts | Messages after the seal boundary of mailboxes they hold | `send`: send, reply, delete; `manage`: members, webhook switch | 404 without any access, 403 when insufficient |
| Personal owner | Use own mail | Same | Own personal mailbox, all its messages | Send, reply, delete in it | Cannot add members, change type, delete mailbox: 403 |
| Organization admin (owner/admin role) | Run mailboxes | `/v1/mailboxes`, members, handovers, sealed messages, access events | Mailbox metadata of every mailbox; no message without a member row | Create, delete, members (including self), handover, delete sealed history, webhook switch | Message routes without a member row: 403 |
| Backend service with a user's API key | Send and track mail | `/v1/outbound-mail-messages` | Its owner's `view` mailboxes | Its owner's `send` mailboxes | Same codes as the owner |
| Mail edge | Deliver mail and events | `/v1/inbound-mail-messages`, `/v1/mail-delivery-events` | none | Store messages and events by address | Unchanged |

Separation of duty: a `manage` holder who is not an admin cannot delete the mailbox, change its
type, hand it over, or delete sealed history. An admin cannot add anyone, including themselves, to
a personal mailbox.

## 3. End-to-End Workflows

1. **Create.** Admin posts `type=personal` with `owner_user_id`, or `type=shared` with 1–100
   `members`. Each user must be a current member (`domain.OrganizationRepository.FindForUser`).
   Mailbox, member rows, and a `created` access event are written in one transaction. Invalid
   input: `422 MAILBOX.ACCESS_INVALID`; nothing stored.
2. **Change members.** A `manage` holder or admin sets or removes one member. Removal deletes that
   member's read rows for the mailbox in the same transaction. Each change writes an access event.
3. **Read and send.** Each request resolves the viewer's access through `authorizeMailbox`; reads
   mark only the viewer's read row.
4. **Departure.** The organization module deletes the membership; the database cascades the
   member rows and read rows. After commit it publishes `organization.member.removed`; the mailbox
   module sets `owner_left_at` on the user's personal mailboxes in that organization and writes an
   `owner_left` access event. For shared mailboxes it records the lost membership from its own
   history: every `member_set` event stores `membership_id` in `detail`, so the handler finds the
   mailboxes whose latest `member_set`/`member_removed` event for that membership is `member_set`
   and writes `member_removed` with `detail.reason = "left_organization"`. If the handler fails, the mailbox simply stays personal for an owner
   who cannot sign in; handover then falls back to the handover time as the boundary (seals more,
   never less).
5. **Handover.** Admin posts a successor (current member) for a personal mailbox, pending or not.
   In one transaction guarded by `type = 'personal'`: the former owner's membership is read
   inside the transaction, and `sealed_before` becomes `owner_left_at` when the owner is not a
   member and `owner_left_at` is set, otherwise now (an owner who rejoined before the handover is
   sealed at the handover); type becomes `shared`; owner fields clear; broadcasts of the mailbox
   created before `sealed_before` that are still `scheduled`, `sending`, or `paused` are cancelled
   with their pending recipients, so a sealed broadcast sends no new mail the successor could
   read; the former owner's read rows on it are deleted; the successor gets a `send` member row; a
   `handed_over` event is written. A second concurrent handover finds `type = 'shared'`:
   `409 MAILBOX.ACCESS_CONFLICT`.
6. **Rejoin.** The organization module publishes `organization.member.joined` after a membership is
   created. The mailbox module clears `owner_left_at` on that user's still-personal mailboxes and
   writes an `owner_returned` event. Access never depended on the flag, so a missed event only
   leaves a stale "awaiting handover" label.
7. **Delete sealed history.** Admin sends the mailbox address as confirmation; messages with
   `created_at < sealed_before` and broadcasts from the mailbox with `created_at < sealed_before`
   are deleted with their dependants; a `sealed_deleted` event records the count.
8. **Webhook switch.** A `manage` holder or admin toggles `webhooks_enabled` on a shared mailbox;
   an access event records it.

## 4. Module Decomposition

| Module | Owns | Changes |
|---|---|---|
| `mailbox` (api) | Mailboxes, members, read rows, access events, seal, webhook switch | All rules in this plan |
| `organization` (api) | Memberships | Publishes `organization.member.removed` and `organization.member.joined` on the injected `events.EventBus` after commit |
| `domain` (api) | Shared types | `MailboxType`, `MailboxPermission`, `MailboxAccess`, `MailboxAccessFor`, membership event types |
| `web` | Console | Type at creation, capability-driven controls, access panel |

`mailbox` consumes `domain.OrganizationRepository.FindForUser` to validate members; it does not
read organization tables except through foreign keys and the one data migration.

## 5. Domain Model

In `api/internal/domain/mailbox_access.go`:

- `MailboxType` (`personal`, `shared`); `MailboxPermission` (`view`, `send`, `manage`) with
  `Includes(other MailboxPermission) bool`.
- `MailboxMember{MailboxID, MembershipID, UserID uint; Permission MailboxPermission; GrantedBy uint; CreatedAt time.Time}`.
- `MailboxAccess{View, Send, ManageMembers, Administer, SeeHistory bool}` with `Any() bool`.
- `func MailboxAccessFor(m *Mailbox, viewer OrganizationContext, member *MailboxMember) MailboxAccess`:
  - admin role (`viewer.Role.CanManageOrganization()`): `Administer`, `SeeHistory`, and on shared
    mailboxes `ManageMembers`;
  - personal and `m.OwnerUserID == viewer.UserID`: `View`, `Send`, `SeeHistory`;
  - shared and `member != nil && member.MembershipID == viewer.MembershipID`: `view` → `View`;
    `send` → also `Send`; `manage` → also `ManageMembers`, `SeeHistory`.
- `MailboxAccessEventType`: `created`, `member_set`, `member_removed`, `owner_left`,
  `owner_returned`, `handed_over`, `sealed_deleted`, `webhooks_changed`.
- `Mailbox` gains `Type MailboxType`, `OwnerUserID *uint`, `OwnerLeftAt *time.Time`,
  `SealedBefore *time.Time`, `WebhooksEnabled bool`, and `Access MailboxAccess` (filled for a viewer).
- `MaxMailboxMembers = 100`.
- Events in `api/internal/domain/organization.go`: `OrganizationMemberRemovedEvent` and
  `OrganizationMemberJoinedEvent`, each with `OrganizationID, UserID, MembershipID uint; OccurredAt time.Time`,
  names `organization.member.removed` and `organization.member.joined`.

Access is the **union** of the three rules above, not a first match: an admin who is also a
member of a shared mailbox gets `Administer` and `ManageMembers` from the role plus whatever the
member row grants (AC-7).

Invariants: a personal mailbox has an owner user id unless the user was deleted; a personal
mailbox has no member rows and `webhooks_enabled = false`; a message is visible only when its
mailbox grants `View` and `sealed_before` is null or `message.created_at >= sealed_before`.

## 6. Persistence Design

Migration `2026_11_19_090000_add_mailbox_access` (one transaction):

```sql
ALTER TABLE mailboxes
  ADD COLUMN type varchar(16) NOT NULL DEFAULT 'shared',
  ADD COLUMN owner_user_id bigint REFERENCES users(id) ON DELETE SET NULL,
  ADD COLUMN owner_left_at timestamptz,
  ADD COLUMN sealed_before timestamptz,
  ADD COLUMN webhooks_enabled boolean NOT NULL DEFAULT false,
  ADD CONSTRAINT chk_mailboxes_type CHECK (type IN ('personal','shared')),
  ADD CONSTRAINT chk_mailboxes_personal_webhooks CHECK (type = 'shared' OR NOT webhooks_enabled);
CREATE INDEX idx_mailboxes_owner ON mailboxes (organization_id, owner_user_id) WHERE deleted_at IS NULL AND type = 'personal';

CREATE TABLE mailbox_members (
  mailbox_id bigint NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
  membership_id bigint NOT NULL REFERENCES organization_memberships(id) ON DELETE CASCADE,
  user_id bigint NOT NULL,
  permission varchar(16) NOT NULL CHECK (permission IN ('view','send','manage')),
  granted_by bigint NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (mailbox_id, membership_id));
CREATE INDEX idx_mailbox_members_membership ON mailbox_members (membership_id, mailbox_id);

CREATE TABLE mailbox_access_events (
  id bigserial PRIMARY KEY,
  organization_id bigint NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  mailbox_id bigint NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
  type varchar(32) NOT NULL,
  actor_user_id bigint,
  subject_user_id bigint,
  detail jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL);
CREATE INDEX idx_mailbox_access_events_mailbox ON mailbox_access_events (mailbox_id, id DESC);
```

Backfill in the same migration, only for mailboxes with `organization_id IS NOT NULL` (the
earlier ownership migration deliberately left orphans with NULL): every existing mailbox stays
`shared`; its creator's membership in the mailbox's organization becomes a `manage` member row
(`granted_by` = creator) when it exists; one `created` access event per mailbox. Down drops the two
tables and the columns. Hard deletes of organization memberships (existing behavior) make the
cascades fire.

Migration `2026_11_19_100000_add_per_person_reads` (Task 2.1, one transaction):

```sql
CREATE TABLE mail_message_reads (
  message_id bigint NOT NULL REFERENCES mail_messages(id) ON DELETE CASCADE,
  membership_id bigint NOT NULL REFERENCES organization_memberships(id) ON DELETE CASCADE,
  read_at timestamptz NOT NULL,
  PRIMARY KEY (membership_id, message_id));
```

Backfill: every inbound message with `read_at IS NOT NULL` in a mailbox that has a `manage` member
row from the creator gets a read row for that membership. Then `ALTER TABLE mail_messages DROP
COLUMN read_at`. Down re-adds `read_at` and fills it from the creator's read rows.

## 7. State Machines

Mailbox type and ownership:

```text
personal(owner active) --owner leaves--> personal(awaiting handover, owner_left_at set)
personal(awaiting handover) --owner rejoins--> personal(owner active, owner_left_at cleared)
personal(any) --admin handover--> shared(sealed_before set)
shared --> shared (members change; never back to personal)
any --admin delete mailbox--> deleted (existing soft delete)
```

"Awaiting handover" is derived for display from `owner_left_at IS NOT NULL`; access is derived
from owner id and the viewer's verified membership only. Seal: `sealed_before` is written once at
handover and never moved; deleting sealed history removes rows, not the boundary.

## 8. HTTP Contract

All routes are in the existing `auth` + `organization_context` group and carry the existing
`Organization-Id` and `Vary` behavior. Stable `error_code` values added: `MAILBOX.ACCESS_INVALID`
(422), `MAILBOX.ACCESS_CONFLICT` (409).

| Operation | Route | Who | Success |
|---|---|---|---|
| List mailboxes | `GET /v1/mailboxes` | any member | Mailboxes where the viewer has any access; each item adds `type`, `owner_user_id`, `awaiting_handover`, `sealed_before`, `webhooks_enabled`, `access{view,send,manage_members,administer,see_history}`; `unread_count` is the viewer's own, `0` without `view` |
| Create mailbox | `POST /v1/mailboxes` | admin | Body adds required `type`; `owner_user_id` for personal; `members[{user_id, permission}]` (1–100) for shared |
| List members | `GET /v1/mailboxes/:mailbox_id/members` | `manage_members` | `200` items `{user_id, permission, granted_by, created_at}` ordered by `user_id`, at most 100 |
| Set member | `PUT /v1/mailboxes/:mailbox_id/members/:user_id` | `manage_members` | `{permission}`; `200` member |
| Remove member | `DELETE /v1/mailboxes/:mailbox_id/members/:user_id` | `manage_members` | `204` |
| Hand over | `POST /v1/mailboxes/:mailbox_id/handovers` | admin | `{successor_user_id}`; `201` mailbox |
| Delete sealed history | `DELETE /v1/mailboxes/:mailbox_id/sealed-messages` | admin | `{confirm_address}`; `200 {deleted_messages, deleted_broadcasts}` |
| Webhook switch | `PUT /v1/mailboxes/:mailbox_id/webhook-setting` | `manage_members` | `{enabled}`; `200` mailbox |
| Access history | `GET /v1/mailboxes/:mailbox_id/access-events` | `see_history` | Paginated, newest first, `page_size` ≤ 100; items `{id, type, actor_user_id, subject_user_id, detail, created_at}` |
| Mark read/unread | `PUT /v1/mailboxes/:mailbox_id/messages/:message_id` (existing) | `view` | Changes only the viewer's read row |

Existing routes keep paths and shapes; `read` on a message and `unread_count` become per viewer
(outbound messages are always `read: true`). `members` and `webhook-setting` on a personal mailbox:
`409 MAILBOX.ACCESS_CONFLICT`. Handover of a shared mailbox: `409`. Delete sealed history with no
boundary: `409`; wrong `confirm_address`: `422`.

```text
COMMAND CreateMailbox(viewer, local_part, display_name, domain, type, owner_user_id, members)
BIND viewer from organization context
VALIDATE address rules (existing); type in {personal, shared}
VALIDATE personal: owner_user_id set, members empty; shared: 1..100 members, unique user_id, valid permission ELSE MAILBOX.ACCESS_INVALID
AUTHORIZE viewer.Role.CanManageOrganization() ELSE PERMISSION.DENIED
LOAD membership for owner and each member via FindForUser ELSE MAILBOX.ACCESS_INVALID
BEGIN TRANSACTION mailbox
  CREATE routing rule first (existing order)
BEGIN TRANSACTION mailbox
  INSERT mailbox with RoutingRuleID (existing limit and address rules), member rows, access event created
COMMIT
ON FAILURE remove the routing rule (existing compensation)
RETURN 201 mailbox for viewer
```

```text
COMMAND SetMailboxMember(viewer, mailbox_id, user_id, permission)
LOAD mailbox in viewer organization ELSE MAILBOX.NOT_FOUND
REQUIRE access.Any() ELSE MAILBOX.NOT_FOUND
REQUIRE mailbox.type = shared ELSE MAILBOX.ACCESS_CONFLICT (checked before the capability, so admins and owners see 409)
AUTHORIZE access.ManageMembers ELSE PERMISSION.DENIED
VALIDATE permission valid; LOAD membership of user_id ELSE MAILBOX.ACCESS_INVALID
BEGIN TRANSACTION mailbox
  LOCK mailbox row FOR UPDATE
  REQUIRE member count < 100 when inserting ELSE MAILBOX.ACCESS_INVALID
  UPSERT member row (granted_by = viewer.UserID); INSERT access event member_set with detail.membership_id
COMMIT
RETURN 200 member
```

```text
COMMAND RemoveMailboxMember(viewer, mailbox_id, user_id)
LOAD and AUTHORIZE as SetMailboxMember
BEGIN TRANSACTION mailbox
  DELETE member row ELSE MAILBOX.NOT_FOUND
  DELETE read rows of that membership for messages of the mailbox
  INSERT access event member_removed
COMMIT
RETURN 204
```

```text
COMMAND HandOverMailbox(viewer, mailbox_id, successor_user_id)
AUTHORIZE viewer.Role.CanManageOrganization() ELSE PERMISSION.DENIED
LOAD mailbox in viewer organization ELSE MAILBOX.NOT_FOUND
LOAD successor membership ELSE MAILBOX.ACCESS_INVALID
DERIVE boundary = owner_left_at when owner has no membership and owner_left_at set, else now
BEGIN TRANSACTION mailbox
  UPDATE mailboxes SET type = shared, owner_user_id = NULL, owner_left_at = NULL, sealed_before = boundary
    WHERE id AND type = personal ELSE MAILBOX.ACCESS_CONFLICT
  DELETE read rows of the former owner's current membership (if any) for the mailbox
  INSERT member row (successor, send); INSERT access event handed_over
COMMIT
RETURN 201 mailbox for viewer
```

```text
COMMAND DeleteSealedHistory(viewer, mailbox_id, confirm_address)
AUTHORIZE viewer.Role.CanManageOrganization() ELSE PERMISSION.DENIED
LOAD mailbox ELSE MAILBOX.NOT_FOUND
REQUIRE sealed_before set ELSE MAILBOX.ACCESS_CONFLICT
VALIDATE confirm_address equals mailbox address (case-insensitive) ELSE MAILBOX.ACCESS_INVALID
BEGIN TRANSACTION mailbox
  CANCEL broadcasts of the mailbox with created_at < sealed_before that are scheduled, sending, or paused (existing cancel transition)
  DELETE broadcasts of the mailbox with created_at < sealed_before (and recipients)
  DELETE messages of the mailbox with created_at < sealed_before (events, attachments, reads cascade)
  INSERT access event sealed_deleted with counts
COMMIT
RETURN 200 counts
```

```text
COMMAND SetMailboxWebhooks(viewer, mailbox_id, enabled)
LOAD and AUTHORIZE as SetMailboxMember; REQUIRE shared ELSE MAILBOX.ACCESS_CONFLICT
BEGIN TRANSACTION mailbox
  UPDATE webhooks_enabled; INSERT access event webhooks_changed when changed
COMMIT
RETURN 200 mailbox
```

```text
COMMAND MarkMessageRead(viewer, mailbox_id, message_id, read)
LOAD mailbox; AUTHORIZE access.View ELSE 404/403
LOAD message in mailbox and after seal ELSE MAIL_MESSAGE.NOT_FOUND (existing code)
BEGIN TRANSACTION mailbox
  UPSERT or DELETE read row (viewer.MembershipID, message_id)
COMMIT
RETURN 200 message for viewer
```

```text
COMMAND SendFromMailbox(viewer, mailbox_id, input)
LOAD mailbox; AUTHORIZE access.Send ELSE 404 when !access.Any() else PERMISSION.DENIED
VALIDATE and send exactly as the existing send path (limit, idempotency, provider outcome)
RETURN existing 201 or 200 repeat
```

```text
COMMAND StartOrResumeBroadcast(viewer, broadcast_id)
AUTHORIZE viewer.Role.CanManageOrganization() ELSE PERMISSION.DENIED (existing)
LOAD broadcast and its sender mailbox; AUTHORIZE access.Send ELSE 404 when !access.Any() else PERMISSION.DENIED
CONTINUE with the existing start or resume transition
RETURN existing response
```

**Seal on every message lookup.** Every repository lookup of a message by id (find, delete,
mark read, attachment, outbound GET, idempotency lookup) takes the mailbox's `sealed_before` and
treats a sealed row as absent. In `send()`, an idempotency key that matches a sealed message
returns `409 MAILBOX.IDEMPOTENCY_CONFLICT` with no message data and sends nothing.

Queries: log and statistics take the viewer's visible mailbox ids and each mailbox's seal
boundary; an empty set returns zero rows. Broadcast list/detail/recipients require `View` on the
sender mailbox and hide broadcasts created before its seal boundary.

## 9. Authorization Matrix

| Route | Needs |
|---|---|
| Message list, detail, attachment, mark read | `View` and message after seal |
| Send, reply, delete message | `Send` |
| Delete mailbox | admin (`Administer`) |
| Log and statistics | rows of `View` mailboxes after seal only |
| Broadcast list, detail, recipients | `View` on sender mailbox, broadcast after seal |
| Broadcast create (existing member rule) | `Send` on sender mailbox |
| Broadcast update (existing member rule) | `Send` on the stored and on the new sender mailbox |
| Broadcast send, resume (existing admin rule) | `Send` on sender mailbox, checked at that call |
| Broadcast pause, cancel (existing admin rule) | no mailbox access needed; without `View` on the sender mailbox (or for a sealed broadcast) the response is only `{id, status}` |
| Broadcast delete (existing member rule) | `Send` on the stored sender mailbox |
| Template test send | existing rule and `Send` |
| API key send / get sent | key owner's `Send` / `View` |

A broadcast already sending or scheduled is not re-checked by the worker; resume re-checks.
Handover cancels the mailbox's broadcasts created before the seal boundary that are still
`scheduled`, `sending`, or `paused`, so the worker never sends a sealed broadcast after handover.

## 10. Cross-Module Data Flow

`organization` → `events.EventBus` → `mailbox`, synchronous `Publish` after the membership
transaction commits, using the existing bus. The organization module publishes `removed` from
`RemoveMember` and `joined` from every path that creates a membership (organization creation,
invitation acceptance, and any operator path in `organization/operator.go`; find them all by
searching for inserts of `OrganizationMembershipPO`). Handlers are idempotent (`owner_left_at` set to the event time, cleared on
join) and failures are logged without failing the membership change; section 3 states the safe
fallback.

## 11. Scheduled and Asynchronous Work

None new. Existing broadcast worker unchanged except that it reads no access rules.

## 12. Browser-Surface Plan

- Mailbox list: icon for personal, shared, awaiting handover; lock and no message pane without
  `access.view`; compose, reply, delete hidden without `access.send`.
- Create dialog: type choice; personal → owner picker; shared → member pickers with permission.
  Members come from the existing organization members API.
- Access panel per mailbox: members with permission editing (`manage_members`), webhook switch,
  history (`see_history`), handover (admin, personal), delete sealed history with a confirmation
  that requires typing the address.
- Both locales; loading, empty, and error states follow existing mailbox components.

## 13. Security, Privacy, and Audit

- Webhooks: `MailNotification` gains `MailboxType`, `WebhooksEnabled`, and `SealedBefore`, filled
  in `sendNotifications`, `ReceiveInbound`, and by extending the `FindOutboundMessageByProviderID`
  select. `Notify` publishes only when the type is shared, the switch is on, and
  `Message.CreatedAt` is not before `SealedBefore`.
- Access events store user ids and counts only, never message content.
- Each mutation also writes the existing structured request audit.
- 404 versus 403 never reveals a mailbox to someone without access.

## 14. Observability and Operations

Log `mailbox_member_event_failed` with organization, user, and error when a membership event
handler fails. After migrating, check `SELECT id FROM mailboxes m WHERE NOT EXISTS (SELECT 1 FROM
mailbox_members WHERE mailbox_id = m.id)` and add members to any listed mailbox. Rollback: run the
migration's down, which restores `read_at` from the creator's reads.

## 15. Test and Verification Strategy

| Layer | Proof |
|---|---|
| Domain | Table test of `MailboxAccessFor` for every row of section 5 |
| Migration (PostgreSQL) | Golden schema; backfill creates manage rows and read rows; down restores `read_at` |
| Repository (PostgreSQL) | Cascades on membership delete; per-person unread; seal filter; concurrent handover |
| Service and handler | Every row of section 9 for allowed and denied viewers; review-focus tests |
| Contract | `make contract-check`, generated Web/Admin types, `make route-catalog-check` |
| Web | Vitest for capability-driven controls, create dialog, access panel, confirmations |
| Acceptance | Kest flow `api/tests/kest/flows/mailbox-access.md` covering AC-1 to AC-17 |

## 16. Delivery Plan

Slices are vertical and ordered by dependency; each ends green and committed.

1. **Private and shared mailboxes** — types, members, enforcement, history, Web (AC-1–4, 6–8, 13–15).
2. **Per-person read state** — read rows, per-viewer unread and `read` (AC-5).
3. **Departure, handover, sealing, rejoin** — events, handover, seal filters, sealed deletion, Web (AC-10–12, 16, 17).
4. **Webhook switch** — switch, `Notify` guard, Web (AC-9).
5. **Acceptance and release gate** — kest flow, acceptance plan, `make check`.

## Acceptance

The PRD's AC-1 to AC-17 are the acceptance criteria; each is mapped to a task below and to the
kest flow in Task 5.1. Every cross-access attempt must fail.

## Readiness

readiness: READY WITH RECORDED ASSUMPTIONS

Assumptions: organization memberships remain hard-deleted (cascades depend on it); the event bus
delivers synchronously in-process; the product has no external API clients, so `type` can be
required on mailbox creation.

## Open Decisions

None.

---

## Implementation Tasks

Each task: write the named failing tests, run them red, implement, run green, run the listed
checks, commit. Implementers read `api/AGENTS.md` or `web/AGENTS.md` first.

### Task 1.1: Access model and enforcement (API)

**Files:** Create `api/internal/domain/mailbox_access.go`, `mailbox_access_test.go`,
`api/database/migrations/2026_11_19_090000_add_mailbox_access.go`,
`api/internal/modules/mailbox/access_service.go`, `access_handler.go`, `access_test.go`.
Modify `api/internal/domain/mailbox.go`, `error_codes.go`, `api/database/migrations/testdata/schema.golden.sql`,
mailbox `model.go`, `repository.go`, `service.go`, `broadcast_service.go`, `broadcast_handler.go`,
`template_service.go`, `handler.go`, `dto.go`, `routes.go`, `errors.go`, `error_mappings.go`,
`provider.go`, tests. This task's migration is `2026_11_19_090000_add_mailbox_access` (section 6, first block);
`mail_messages.read_at` stays untouched until Task 2.1.

**Interfaces produced:** section 5 types; repository `FindMailboxMember(ctx, mailboxID, membershipID uint) (*domain.MailboxMember, error)`,
`ListMailboxMembers`, `UpsertMailboxMember`, `DeleteMailboxMember`, `AddMailboxAccessEvent`,
`ListMailboxAccessEvents(ctx, mailboxID uint, page, size int)`; service
`authorizeMailbox(ctx, viewer domain.OrganizationContext, mailboxID uint, need func(domain.MailboxAccess) bool) (*domain.Mailbox, error)`
and `visibleMailboxes(ctx, viewer) ([]*domain.Mailbox, error)`. Service methods scoped by
`organizationID uint` take `viewer domain.OrganizationContext` instead.

- [ ] Tests first: `TestMailboxAccessFor` (domain table); in `access_test.go`
  `TestPersonalMailboxIsInvisibleToColleagues` (AC-1), `TestAdminSeesMetadataNotMail` (AC-2),
  `TestLogSkipsUnreadableMailboxes` and stats (AC-3), `TestViewMemberCannotSend` with no message
  stored and no quota used (AC-4), `TestApiKeyFollowsOwnerAccess` (AC-6),
  `TestAdminSelfGrantIsRecorded` (AC-7), `TestManageRequiredToChangeMembers` (AC-8),
  `TestBroadcastNeedsSendOnSender` and pause without access returning only `{id, status}` (AC-13),
  `TestBroadcastDeleteAndUpdateNeedSend`, `TestAccessIsUnionOfRules` (admin who is also a member),
  `TestPersonalMailboxMembersRouteIs409`, `TestMemberLimitUnderConcurrency` (PostgreSQL),
  migration backfill skips mailboxes with NULL organization (PostgreSQL),
  `TestBroadcastsHiddenWithoutView` (AC-15), `TestCreateValidation` (AC-14),
  `TestRemovingMemberDeletesTheirReads`, `TestMembershipDeleteCascadesMembers` (PostgreSQL),
  migration backfill test (PostgreSQL).
- [ ] Run red: `cd api && go test ./internal/domain/ ./internal/modules/mailbox/ ./database/migrations/`.
- [ ] Implement sections 5, 6, 8 (create, members, access events), 9.
- [ ] Run green, then `cd api && make wire-check && make route-catalog-check`.
- [ ] Update `contracts/MAILBOXES.md` (roles table, access section, new routes, per-viewer read note) and `contracts/openapi.yaml`; `make contract-generate && make contract-check` from the root.
- [ ] Commit `feat(mailbox): personal and shared mailboxes with member permissions`.

### Task 1.2: Console for types, members, and history (Web)

**Files:** `web/src/features/mailbox/types.ts`, `schemas.ts`, `services/mailbox-service.ts`,
`hooks/use-mailbox.ts`, `server/mock-mailbox-store.ts`, `server/mailbox-route.ts`,
`components/mailbox-workspace.tsx`, `compose-dialog.tsx`, `message-actions.tsx`, new
`components/mailbox-access-panel.tsx` and `components/create-mailbox-dialog.tsx` (or the existing
creation component), `web/src/i18n/modules/mailbox/en-US.ts`, `zh-Hans.ts`; tests
`web/src/test/mailbox-access-ui.test.tsx`, `mailbox-service.test.ts`, `mailbox-route.test.ts`.

- [ ] Tests first: locked mailbox shows no message list; no compose/reply/delete without `send`;
  create dialog requires owner or at least one member; panel lists members, edits permission,
  removes a member, shows history; members API calls carry `Organization-Id`; mock parity for new routes.
- [ ] Run red: `cd web && corepack pnpm vitest run src/test/mailbox-access-ui.test.tsx src/test/mailbox-service.test.ts src/test/mailbox-route.test.ts`.
- [ ] Implement; run green; `corepack pnpm type-check && corepack pnpm lint`.
- [ ] Commit `feat(web): mailbox types, members, and access history`.

### Task 2.1: Per-person read state (API + Web)

**Files:** `api/database/migrations/2026_11_19_100000_add_per_person_reads.go`, golden schema,
mailbox `model.go` (drop `ReadAt`), `repository.go`, `service.go`, `dto.go`, tests; `contracts/MAILBOXES.md`;
Web mock store and tests if the mock models read state.

- [ ] Tests first (PostgreSQL where SQL): `TestReadIsPerPerson` (AC-5: B reads, C's unread
  unchanged), `TestApiKeyReadDoesNotMarkRead`, `TestOutboundAlwaysRead`, unread count per viewer in
  `ListMailboxes`, mark unread deletes only the viewer's row.
- [ ] Run red, implement section 8 `MarkMessageRead` and per-viewer `read`, run green, contract check.
- [ ] Commit `feat(mailbox): per-person read state`.

### Task 3.1: Departure, handover, sealing, rejoin (API)

**Files:** `api/internal/domain/organization.go` (events), organization `membership_service.go`,
`invitation_service.go`, `service.go` (publish after commit) and tests; mailbox `access_service.go`,
`membership_events.go` (subscriber), `provider.go`, `repository.go`, `broadcast_*`, `webhook_events.go`
(sealed messages publish nothing), tests; contract.

- [ ] Tests first: `TestOwnerDepartureAwaitsHandover` (AC-10: requests fail, inbound stored, nobody
  reads), `TestHandoverSealsHistory` (AC-11 including log, stats, unread), `TestDeleteSealedHistory`
  (AC-12, wrong confirmation 422, no boundary 409), `TestRejoinBeforeHandoverRestores` (AC-16),
  `TestRejoinAfterHandoverDoesNotRestore` (AC-17), `TestEventsOfSealedMessagesStayHidden`,
  `TestConcurrentHandoverHasOneWinner` (PostgreSQL), `TestBoundaryIsLatestDeparture`,
  `TestSuccessorCannotDeleteOrReplaySealedMessages` (delete by id 404, reused idempotency key 409
  without data), `TestDepartureRecordsSharedMemberRemoval`, `TestDeleteSealedHistoryCancelsRunningBroadcast`,
  `TestMissedDepartureEventSealsAtHandover`, `TestHandoverCancelsSealedRunningBroadcasts`,
  `TestHandoverRereadsFormerOwnerInsideTransaction`, organization tests that `RemoveMember` and invitation
  acceptance publish their events after commit.
- [ ] Run red, implement workflows 4–7, run green, `make wire-check`, contract check.
- [ ] Commit `feat(mailbox): hand over a departed member's mailbox and seal its history`.

### Task 3.2: Handover and sealed history in the console (Web)

- [ ] Tests first: awaiting-handover icon and panel; successor picker posts `successor_user_id`;
  delete sealed history is disabled until the typed address matches; 409 shows a refresh message.
- [ ] Run red, implement, run green, type-check, lint.
- [ ] Commit `feat(web): mailbox handover and sealed history`.

### Task 4.1: Webhook switch (API + Web)

- [ ] Tests first: `TestWebhookOnlyForEnabledSharedMailbox` (AC-9: personal never, shared off by
  default, on after switch, covers sent, delivered, bounced too; a late bounce for a sealed
  message publishes nothing); switch on personal 409; switch
  writes history; Web toggle visible with `manage_members` only.
- [ ] Run red, implement section 8 `SetMailboxWebhooks` and the `Notify` guard, run green, contract check, Web checks.
- [ ] Commit `feat(mailbox): per-mailbox webhook switch`.

### Task 5.1: Acceptance and release gate

- [ ] Write `api/tests/kest/flows/mailbox-access.md` covering AC-1 to AC-17 with organization O,
  admin A, members B and C, following the kest-flow skill.
- [ ] Add the mailbox access cases to `docs/testing/ACCEPTANCE_TEST_PLAN.md`.
- [ ] Run the flow against a local API with disposable PostgreSQL; record results.
- [ ] Run `make check` once from the root.
- [ ] Commit `test: mailbox access acceptance flow`.
