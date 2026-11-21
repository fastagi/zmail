# Mailbox Access Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give each mailbox its own access rule (organization-wide, named members, or one person's
personal mailbox) so that no member reads or sends mail they were not granted, through any route.

**Architecture:** One pure domain function turns a mailbox, the request's verified
`OrganizationContext`, and the viewer's grant into capabilities. Every mailbox service method
resolves capabilities through one chokepoint (`authorizeMailbox`) before touching messages;
organization-wide reads (log, statistics) are filtered to readable mailbox IDs. Existing mailboxes
keep today's behavior (`organization` mode) until a manager previews and applies a change.

**Tech Stack:** Go (Gin, GORM, PostgreSQL), Next.js 16 / React 19 Web console, OpenAPI contract.

**Spec:** [docs/PRODUCT_REQUIREMENTS.zh-CN.md](../PRODUCT_REQUIREMENTS.zh-CN.md) R1 (permissions
and migration) and phase B. Change to [mailbox-organizations.md](mailbox-organizations.md), whose
section 16 deferred "access rules per mailbox". Written to
[BUSINESS_IMPLEMENTATION_PLAN_STANDARD.md](../BUSINESS_IMPLEMENTATION_PLAN_STANDARD.md); sections
that do not change are not repeated.

## Global Constraints

- Personal mailbox: readable by its owner only by default. Organization owner/admin roles never
  grant reading a personal mailbox.
- Shared mailbox permissions are exactly three levels: `view`, `send` (reply from the address),
  `manage` (change members).
- Existing mailboxes must behave exactly as today after deploy; changes go through preview first,
  and the applied result must equal the preview.
- Exposure surfaces that must all respect access: list, unread count, global search (log), message
  body, attachments, direct links, API key routes, webhook events. (AI notifications do not exist
  yet; R4 must consume `authorizeMailbox`.)
- PostgreSQL only; versioned migrations are frozen SQL; contract is additive (no field removed or
  retyped).
- Code, comments, and contract text are English.

## Review Focus

1. A member removed from the organization still has a `mailbox_grants` row → every route answers
   404 (membership resolves before grants). Test: Task 3, `TestRemovedMemberGrantGivesNothing`.
2. An admin searches the log with `q` matching a personal mailbox's subject → no row, total
   excludes it. Test: Task 3, `TestLogAndStatsSkipUnreadableMailboxes`.
3. Access changes between preview and apply → `409 MAILBOX.ACCESS_CHANGED`, nothing changed.
   Test: Task 4, `TestApplyAccessRejectsStaleVersion`.
4. An API key whose owner has `view` on the sender mailbox → `403 PERMISSION.DENIED`, nothing
   stored, send quota unchanged. Test: Task 3, `TestViewOnlyKeyCannotSend`.
5. A colleague without access opens a copied attachment or message URL → 404, no bytes. Test:
   Task 3, `TestDirectLinksRespectAccess`.

---

## 1. Executive Summary

**Outcome.** A team keeps personal mailboxes private and shares business mailboxes with exactly the
people who work them.

**In scope.** Access mode per mailbox, per-user grants, capability checks on every mailbox route,
filtered log and statistics, webhook exclusion for restricted mailboxes, preview-then-apply access
changes with optimistic versioning, Web console access dialog.

**Out of scope.** Aliases and bulk address creation (next phase B plan); reassigning a personal
mailbox whose owner left (open decision, section 13); per-mailbox webhooks; AI access (R4).

## 2. Actors and Capabilities

Capabilities: `read` (list messages, open, download, mark read), `send` (send, reply, delete
messages, broadcast/test from the address), `manage_access` (read and change the access rule),
`administer` (see the mailbox in the list, delete it).

| Mode | Viewer | read | send | manage_access | administer |
|---|---|---|---|---|---|
| `organization` | member | ✓ | ✓ | | |
| `organization` | owner/admin | ✓ | ✓ | ✓ | ✓ |
| `members` | grant `view` | ✓ | | | |
| `members` | grant `send` | ✓ | ✓ | | |
| `members` | grant `manage` | ✓ | ✓ | ✓ (grants only) | |
| `members` | owner/admin without grant | | | ✓ | ✓ |
| `personal` | `owner_user_id` | ✓ | ✓ | ✓ (own mailbox only) | |
| `personal` | owner/admin who is not the owner | | | | ✓ |

An owner/admin's role and a grant combine by union. A viewer with no capability gets
`404 MAILBOX.NOT_FOUND` (existence is not revealed); a viewer with some capability but not the
needed one gets `403 PERMISSION.DENIED`.

Mode change rules: only owner/admin may set mode `organization` or `members`, or set `personal`
on a non-personal mailbox. Leaving `personal` is allowed only to its owner. Grant holders with
`manage` change grants but not the mode.

## 3. Workflows

1. **Deploy.** Every mailbox is `organization`; nothing changes for anyone.
2. **Preview.** A manager picks a mailbox, chooses a mode, owner, and grants; the console shows who
   gains and who loses which capability, computed against current members.
3. **Apply.** The manager applies with the `access_version` the preview returned; a concurrent
   change fails with 409 and the console re-previews.
4. **Create.** `POST /mailboxes` accepts an optional rule; default stays `organization`.
5. **Leave.** Membership removal ends access at once; grant rows stay inert.

## 5. Domain Model

In `api/internal/domain/mailbox_access.go`:

- `MailboxAccessMode` (`organization`, `members`, `personal`) with `Valid() bool`.
- `MailboxPermission` (`view`, `send`, `manage`) with `Valid() bool`.
- `MailboxCapabilities{Read, Send, ManageAccess, Administer bool}` with `Any() bool`.
- `MailboxGrant{UserID uint; Permission MailboxPermission}`.
- `MailboxAccess{Mode MailboxAccessMode; OwnerUserID *uint; Grants []MailboxGrant; Version int}`.
- `MaxMailboxGrants = 100`.
- `func MailboxCapabilitiesFor(mailbox *Mailbox, viewer OrganizationContext, grant MailboxPermission) MailboxCapabilities`
  — the table in section 2; `grant` is `""` when the viewer has none.
In `api/internal/modules/mailbox/access_rules.go` (module errors stay in the module):

- `func validateMailboxAccessChange(mailbox *domain.Mailbox, current, next domain.MailboxAccess, viewer domain.OrganizationContext, memberIDs map[uint]bool) error`
  — returns `ErrMailboxAccessInvalid` (personal without owner, grants on personal, owner or grantee
  not a member, duplicate grantee, more than `MaxMailboxGrants`, bad enum) or
  `domain.ErrPermissionDenied` (mode rules in section 2).

Back in `api/internal/domain/mailbox.go`:
- `Mailbox` gains `AccessMode MailboxAccessMode`, `OwnerUserID *uint`, `AccessVersion int`, and
  `Capabilities MailboxCapabilities` (filled only by listings and lookups for a viewer).

## 6. Persistence

Migration `2026_11_19_090000_add_mailbox_access`:

- `ALTER TABLE mailboxes ADD COLUMN access_mode varchar(16) NOT NULL DEFAULT 'organization'`,
  `ADD COLUMN owner_user_id bigint REFERENCES users(id) ON DELETE SET NULL`,
  `ADD COLUMN access_version integer NOT NULL DEFAULT 0`,
  `ADD CONSTRAINT chk_mailboxes_access_mode CHECK (access_mode IN ('organization','members','personal'))`.
  No personal/owner CHECK: `ON DELETE SET NULL` may null an owner; such a mailbox is treated as
  owner-less personal (only `administer` for managers).
- `CREATE TABLE mailbox_grants (id bigserial PRIMARY KEY, mailbox_id bigint NOT NULL REFERENCES
  mailboxes(id) ON DELETE CASCADE, user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  permission varchar(16) NOT NULL CHECK (permission IN ('view','send','manage')), granted_by bigint
  NOT NULL, created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL)`,
  `CREATE UNIQUE INDEX idx_mailbox_grants_mailbox_user ON mailbox_grants (mailbox_id, user_id)`,
  `CREATE INDEX idx_mailbox_grants_user ON mailbox_grants (user_id, mailbox_id)`.
- Down drops the table and columns. No backfill: the default reproduces current behavior.

## 8. HTTP Contract

All under the existing `auth` + `organization_context` group; all answer `Vary` as today.

| Operation | Route | Needs | Success |
|---|---|---|---|
| List mailboxes | `GET /v1/mailboxes` | any capability (others omitted) | items gain `access_mode`, `owner_user_id`, `capabilities{read,send,manage_access}`; `unread_count` is `0` without `read` |
| Create mailbox | `POST /v1/mailboxes` | owner/admin | optional `access` object (same shape as below) |
| Get access | `GET /v1/mailboxes/:mailbox_id/access` | `manage_access` | `200 {access_mode, owner_user_id, grants[{user_id, permission}], access_version}` |
| Preview change | `POST /v1/mailboxes/:mailbox_id/access-previews` | `manage_access` | `200 {access_version, changes[{user_id, before{read,send,manage_access}, after{...}}]}` — only users whose capabilities differ, current members only, sorted by `user_id` |
| Apply change | `PUT /v1/mailboxes/:mailbox_id/access` | `manage_access` | body `{access_mode, owner_user_id, grants, access_version}`; `200` with the new access |

Errors: `422 MAILBOX.ACCESS_INVALID`, `409 MAILBOX.ACCESS_CHANGED`, `403 PERMISSION.DENIED`,
`404 MAILBOX.NOT_FOUND`. Existing routes keep paths and shapes; their authorization follows
section 2. Preview and apply share one diff function, so an apply with the previewed
`access_version` produces exactly the previewed `changes`.

```text
COMMAND ApplyMailboxAccess(viewer, mailbox_id, next, expected_version)
BIND viewer from organization context
LOAD mailbox (organization-scoped) ELSE MAILBOX.NOT_FOUND
AUTHORIZE capabilities.manage_access ELSE 404 if none, 403 otherwise
LOAD current access, organization member IDs
VALIDATE ValidateMailboxAccessChange(current, next, viewer, members)
BEGIN TRANSACTION
  UPDATE mailboxes SET mode, owner, access_version = access_version + 1
    WHERE id AND access_version = expected_version ELSE MAILBOX.ACCESS_CHANGED
  DELETE mailbox_grants WHERE mailbox_id; INSERT next.grants (granted_by = viewer.UserID)
COMMIT
RETURN 200 access
```

## 9. Authorization Matrix for Existing Routes

| Route | Needs |
|---|---|
| `GET /mailboxes/:id/messages`, `GET .../messages/:mid`, `GET .../attachments/:aid`, `PUT .../messages/:mid` | `read` |
| `POST /mailboxes/:id/messages`, `DELETE .../messages/:mid` | `send` |
| `DELETE /mailboxes/:id` | owner/admin role (unchanged) and `administer` |
| `GET /mail-messages`, `GET /mail-message-stats` | rows/counts from `read` mailboxes only |
| `POST/PUT /mail-broadcasts`, `POST .../sends`, `POST /mail-templates/:id/tests` | existing role rule and `send` on the sender mailbox |
| `POST /outbound-mail-messages` (API key) | key owner's `send` on the `from` mailbox |
| `GET /outbound-mail-messages/:id` (API key) | key owner's `read` on the message's mailbox |

A broadcast already `sending` keeps running when its starter loses `send`; managers pause or cancel
it. This is recorded in the contract.

## 13. Security, Privacy, Audit

- Webhooks: only `organization`-mode mailboxes publish `mail.message.*` events; endpoints are
  organization-level and would otherwise expose restricted subjects and recipients.
- Each apply logs `mailbox_access_changed` with organization, mailbox, actor, old and new mode,
  and counts of gained/lost users (no message content).
- **Open decision (from PRD §6.2):** handing over a personal mailbox whose owner left. Until
  decided, managers can only delete it; the mail is not exposed.

## 15. Verification

| Layer | Proof |
|---|---|
| Domain | Table-driven capability matrix; change validation |
| Migration | Golden schema; existing rows read back as `organization`, version 0 |
| Repository (PostgreSQL) | Grants replace atomically; stale version changes nothing; log/stats filter by mailbox IDs |
| Service/handler | Two-employee cross test over every route in section 9; review-focus tests |
| Contract | `contracts/MAILBOXES.md`, `openapi.yaml`, regenerated Web/Admin types, route catalog |
| Web | Capability-driven controls; access dialog preview → apply → 409 re-preview; mock parity |
| Acceptance | `docs/testing/ACCEPTANCE_TEST_PLAN.md` gains the two-employee and migration-preview cases |

---

## Implementation Tasks

### Task 1: Domain access rules

**Files:**
- Create: `api/internal/domain/mailbox_access.go`
- Create: `api/internal/domain/mailbox_access_test.go`
- Modify: `api/internal/domain/mailbox.go:164-178` (fields from section 5)
- Create: `api/internal/modules/mailbox/access_rules.go`, `access_rules_test.go`
- Modify: `api/internal/modules/mailbox/errors.go`, `error_mappings.go`, and
  `api/internal/domain/error_codes.go` (add `CodeMailboxAccessInvalid = "MAILBOX.ACCESS_INVALID"`,
  `CodeMailboxAccessChanged = "MAILBOX.ACCESS_CHANGED"` beside `CodeMailboxNotFound`)

**Interfaces:**
- Produces: every type and function listed in section 5; `ErrMailboxAccessInvalid` (422) and
  `ErrMailboxAccessChanged` (409) in package `mailbox`.

- [ ] **Step 1: Write the failing tests**

`TestMailboxCapabilitiesFor` — one table row per line of the section 2 table, plus: owner-less
personal mailbox gives a manager only `Administer`; an admin holding a `view` grant on a `members`
mailbox gets `Read`, `ManageAccess`, `Administer` but not `Send`.

`TestValidateMailboxAccessChange` (in `access_rules_test.go`) — cases: personal without owner → `ErrMailboxAccessInvalid`;
personal with grants → invalid; grantee not in `memberIDs` → invalid; 101 grants → invalid;
duplicate grantee → invalid; member with `manage` grant changing mode → `ErrPermissionDenied`;
admin leaving another user's personal mode → `ErrPermissionDenied`; personal owner switching to
`members` → nil; admin setting `members` with two grants → nil.

- [ ] **Step 2: Run** `cd api && go test ./internal/domain/ ./internal/modules/mailbox/ -run 'MailboxCapabilities|MailboxAccessChange'` — expect FAIL (undefined).
- [ ] **Step 3: Implement** section 5 in `mailbox_access.go` and `access_rules.go`; map the two new errors in `error_mappings.go`.
- [ ] **Step 4: Run** the same command — expect PASS; then `go test ./internal/modules/mailbox/...` still PASS.
- [ ] **Step 5: Commit** `feat(mailbox): add per-mailbox access rules to the domain`

### Task 2: Migration and repository

**Files:**
- Create: `api/database/migrations/2026_11_19_090000_add_mailbox_access.go`
- Modify: `api/database/migrations/testdata/schema.golden.sql`
- Modify: `api/internal/modules/mailbox/model.go` (`MailboxPO` columns; new `MailboxGrantPO`, table `mailbox_grants`)
- Modify: `api/internal/modules/mailbox/repository.go`, `repository_test.go` (add `&MailboxGrantPO{}` to `openRepository`)

**Interfaces:**
- Consumes: Task 1 types.
- Produces (repository interface in `service.go`):
  - `FindMailboxGrant(ctx, mailboxID, userID uint) (domain.MailboxPermission, error)` — `""` when none.
  - `ListMailboxGrantsForUser(ctx, organizationID, userID uint) (map[uint]domain.MailboxPermission, error)`
  - `GetMailboxAccess(ctx, organizationID, mailboxID uint) (domain.MailboxAccess, error)`
  - `ReplaceMailboxAccess(ctx, organizationID, mailboxID uint, expectedVersion int, next domain.MailboxAccess, actorID uint) error` — `ErrMailboxAccessChanged` on version mismatch.
  - `ListMessagesForOrganization(ctx, organizationID uint, mailboxIDs []uint, filter)` and
    `StatsForOrganization(ctx, organizationID uint, mailboxIDs []uint, since)` — empty `mailboxIDs`
    returns no rows (never "all").

- [ ] **Step 1: Write failing PostgreSQL tests** in `repository_test.go`:
  `TestExistingMailboxDefaultsToOrganizationAccess` (mode `organization`, version 0, no grants);
  `TestReplaceMailboxAccessIsAtomic` (replace 2 grants with 1 → exactly 1 row, version 1);
  `TestReplaceMailboxAccessRejectsStaleVersion` (expected 0 after a bump → `ErrMailboxAccessChanged`, grants unchanged);
  `TestLogAndStatsFilterByMailboxIDs` (two mailboxes, filter to one → only its messages and counts; empty slice → zero rows, total 0).
- [ ] **Step 2: Run** `cd api && go test ./internal/modules/mailbox/ -run 'MailboxAccess|FilterByMailboxIDs'` with the disposable PostgreSQL the suite uses — expect FAIL.
- [ ] **Step 3: Implement** the migration (section 6 SQL, frozen; no `AutoMigrate`), PO fields, and repository methods; update callers of the two changed signatures to pass all mailbox IDs for now so behavior is unchanged.
- [ ] **Step 4: Run** the same tests plus `go test ./database/migrations/...` (golden schema) — expect PASS.
- [ ] **Step 5: Commit** `feat(mailbox): persist mailbox access mode and grants`

### Task 3: Authorize every mailbox route

**Files:**
- Modify: `api/internal/modules/mailbox/service.go`, `broadcast_service.go`, `template_service.go`, `handler.go`, `broadcast_handler.go`, `template_handler.go`
- Modify: `api/internal/modules/mailbox/service_test.go` (fake repository gains grants), `handler_test.go`

**Interfaces:**
- Consumes: Task 1 capability function; Task 2 repository methods.
- Produces:
  - `func (s *service) authorizeMailbox(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, need func(domain.MailboxCapabilities) bool) (*domain.Mailbox, error)` — 404 when `!caps.Any()`, `domain.ErrPermissionDenied` when `!need(caps)`; returned mailbox has `Capabilities` set.
  - `func (s *service) readableMailboxIDs(ctx context.Context, viewer domain.OrganizationContext) ([]uint, error)`
  - Service methods that took `organizationID uint` as their scope now take `viewer domain.OrganizationContext`: `ListMailboxes`, `ListMessages`, `GetMessage`, `GetAttachment`, `SetMessageRead`, `DeleteMessage`, `SendMessage`, `SendFromAddress`, `GetOutboundMessage`, `ListLog`, `Stats`, broadcast create/update/send, template test send. Handlers pass the context from `organizationScope`/`managerScope`.
  - `ListMailboxes` returns mailboxes with `Capabilities.Any()` only; `UnreadCount = 0` without `Read`.

- [ ] **Step 1: Write failing tests** in `handler_test.go` with a fixture of two members (users 700, 701) and an admin (702) in organization 7:
  - `TestPersonalMailboxIsInvisibleToColleagues` — mailbox personal to 700; 701: list omits it, messages/detail/attachment/mark/delete/send → 404; 702: list shows it with `capabilities.read=false`, `unread_count` 0, detail → 403.
  - `TestMembersModeFollowsGrants` — `view` grant: read 200, send 403; `send` grant: send 201.
  - `TestRemovedMemberGrantGivesNothing` — grant for user 703 who has no membership: organization context fails first (404 `ORGANIZATION.NOT_FOUND`).
  - `TestLogAndStatsSkipUnreadableMailboxes` — admin `GET /mail-messages?q=<personal subject>` → 0 items, total 0; stats exclude its counts.
  - `TestViewOnlyKeyCannotSend` — API key owner with `view` → 403, fake repo has no new message, `CountOutboundSince` unchanged.
  - `TestDirectLinksRespectAccess` — 701 requests 700's attachment URL → 404, empty body.
  - `TestBroadcastNeedsSendOnSender` — admin without grant on a `members` mailbox creating a broadcast from it → 403.
  - Existing `TestMembersReadAndSendTheOrganizationsMail` stays green unchanged (organization mode).
- [ ] **Step 2: Run** `cd api && go test ./internal/modules/mailbox/` — expect the new tests FAIL.
- [ ] **Step 3: Implement** `authorizeMailbox`, `readableMailboxIDs`, the signature change, and the section 9 needs at each call site that currently calls `s.repo.FindMailbox(ctx, organizationID, …)` (service.go lines 347–436, broadcast_service.go 133 and 175, template_service.go 154). The background broadcast run (broadcast_service.go 379) keeps its organization-scoped lookup.
- [ ] **Step 4: Run** `go test ./internal/modules/mailbox/...` — expect PASS.
- [ ] **Step 5: Commit** `feat(mailbox): enforce mailbox access on every route`

### Task 4: Access endpoints and contract

**Files:**
- Create: `api/internal/modules/mailbox/access_service.go`, `access_handler.go`, `access_test.go`
- Modify: `api/internal/modules/mailbox/routes.go`, `dto.go`
- Modify: `contracts/MAILBOXES.md`, `contracts/openapi.yaml`; regenerate `web/src/http/generated/openapi.ts`, `admin/src/http/generated/openapi.ts`; route catalog

**Interfaces:**
- Consumes: Tasks 1–3.
- Produces:
  - `GetMailboxAccess(ctx, viewer, mailboxID) (domain.MailboxAccess, error)`
  - `PreviewMailboxAccess(ctx, viewer, mailboxID uint, next domain.MailboxAccess) (MailboxAccessPreview, error)` where `MailboxAccessPreview{Version int; Changes []MailboxAccessChange}` and `MailboxAccessChange{UserID uint; Before, After domain.MailboxCapabilities}`
  - `ApplyMailboxAccess(ctx, viewer, mailboxID uint, next domain.MailboxAccess, expectedVersion int) (domain.MailboxAccess, error)`
  - `func diffMailboxAccess(mailbox *domain.Mailbox, current, next domain.MailboxAccess, members []domain.OrganizationContext) []MailboxAccessChange` — shared by preview and apply.
  - Routes named `mailboxes.access.show`, `mailboxes.access_previews.store`, `mailboxes.access.update`; DTO JSON as in section 8.
  - `CreateMailbox` accepts optional `access`; applies it in the same transaction as the insert.

- [ ] **Step 1: Write failing tests** in `access_test.go`:
  - `TestPreviewListsOnlyChangedMembers` — `organization` → `members` with `send` for 700: changes = 701 and 702-as-member lose read/send; 700 unchanged is absent.
  - `TestApplyMatchesPreview` — apply with the previewed version; recompute capabilities for every member and compare with preview `after`.
  - `TestApplyAccessRejectsStaleVersion` — two applies with version 0 → second is 409 `MAILBOX.ACCESS_CHANGED`, grants from the first remain.
  - `TestMemberWithoutManageCannotSeeAccess` — `send` grant holder → `GET /access` 403.
  - `TestCreatePersonalMailbox` — admin creates with `{access_mode:"personal", owner_user_id:700}`; admin's detail → 403.
  - `TestAccessChangeIsLogged` — captured `slog` record `mailbox_access_changed` has counts and no subject.
- [ ] **Step 2: Run** `cd api && go test ./internal/modules/mailbox/ -run 'Access|CreatePersonal'` — expect FAIL.
- [ ] **Step 3: Implement** the service, handler, routes, DTOs; write the contract text (sections 2, 8, 9 rules including the running-broadcast note and webhook rule from Task 5), OpenAPI paths and schemas.
- [ ] **Step 4: Run** the tests, then `cd api && make route-catalog-check`, `make contract-generate` then `make contract-check` from the root — expect PASS and no drift.
- [ ] **Step 5: Commit** `feat(mailbox): preview and apply mailbox access`

### Task 5: Keep restricted mail out of webhooks

**Files:**
- Modify: `api/internal/modules/mailbox/webhook_events.go:117-128,153` (`MailNotification` gains `MailboxAccessMode domain.MailboxAccessMode`; `Notify` returns nil without publishing unless it is `organization`)
- Modify: the places in `service.go` that build `MailNotification` (set the field from the loaded mailbox)
- Test: `api/internal/modules/mailbox/webhook_events_test.go`

**Interfaces:**
- Consumes: `domain.MailboxAccessMode` (Task 1).

- [ ] **Step 1: Write failing test** `TestRestrictedMailboxPublishesNoWebhook` — inbound to a `personal` mailbox and a `members` mailbox: fake publisher receives 0 events; `organization` mailbox: 1 `mail.message.received`.
- [ ] **Step 2: Run** `go test ./internal/modules/mailbox/ -run RestrictedMailboxPublishesNoWebhook` — FAIL.
- [ ] **Step 3: Implement** the field and guard.
- [ ] **Step 4: Run** `go test ./internal/modules/mailbox/...` — PASS.
- [ ] **Step 5: Commit** `feat(mailbox): publish webhooks only for organization-wide mailboxes`

### Task 6: Web console

**Files:**
- Modify: `web/src/features/mailbox/types.ts`, `schemas.ts`, `services/mailbox-service.ts`, `hooks/use-mailbox.ts`, `server/mock-mailbox-store.ts`, `server/mailbox-route.ts`
- Create: `web/src/features/mailbox/components/mailbox-access-dialog.tsx`
- Modify: `web/src/features/mailbox/components/mailbox-workspace.tsx`, `compose-dialog.tsx`, `message-actions.tsx`
- Modify: `web/src/i18n/modules/mailbox/en-US.ts`, `zh-Hans.ts`
- Test: `web/src/test/mailbox-access-ui.test.tsx`, `web/src/test/mailbox-service.test.ts`, `web/src/test/mailbox-route.test.ts`

**Interfaces:**
- Consumes: Task 4 contract via regenerated `openapi.ts`.
- Produces: `getMailboxAccess(organizationId, mailboxId)`, `previewMailboxAccess(organizationId, mailboxId, input)`, `applyMailboxAccess(organizationId, mailboxId, input)` in `mailbox-service.ts`; `MailboxCapabilities` type.

- [ ] **Step 1: Write failing tests**:
  - `mailbox-access-ui.test.tsx`: a mailbox with `capabilities.read=false` shows a lock and no message list; without `send` the compose and reply buttons are absent; with `manage_access` an "Access" action opens the dialog; choosing "Personal" + owner → preview lists gained/lost members with their names from the organization members API; "Apply" sends the previewed `access_version`; a 409 shows the "changed, preview again" message and reloads the preview.
  - `mailbox-service.test.ts`: the three calls send `Organization-Id` and parse the section 8 shapes.
  - `mailbox-route.test.ts`: mock store returns 409 on a stale version (mock parity).
- [ ] **Step 2: Run** `cd web && corepack pnpm vitest run src/test/mailbox-access-ui.test.tsx src/test/mailbox-service.test.ts src/test/mailbox-route.test.ts` — FAIL.
- [ ] **Step 3: Implement** service calls, mock store, dialog, capability-driven controls, and both locales (zh-Hans labels mirror the PRD R1 wording for the three modes and three permissions).
- [ ] **Step 4: Run** the same tests, then `corepack pnpm type-check` and `corepack pnpm lint` — PASS.
- [ ] **Step 5: Commit** `feat(web): manage mailbox access with a preview`

### Task 7: Acceptance cases and release gate

**Files:**
- Modify: `docs/testing/ACCEPTANCE_TEST_PLAN.md`
- Modify: `docs/plans/mailbox-organizations.md` section 16 (link here as the delivered "later" item)

- [ ] **Step 1: Add acceptance cases** — (a) two employees, one personal mailbox each: cross-check list, unread count, log search, detail, attachment URL, API key, webhook; every cross access is denied. (b) Migration: preview converting an existing mailbox to `members`, apply, and confirm each member's real access equals the preview. (c) Departure: remove a member who held `manage`; they lose access; an admin still manages the mailbox.
- [ ] **Step 2: Run** `make check` once from the root — PASS.
- [ ] **Step 3: Commit** `docs: add mailbox access acceptance cases`
