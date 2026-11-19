# Unified Inbox, Mail Roles, and v2 Look Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** One read model across every mailbox the caller may read (views `all` / `today` / `pending` / `sent`, filters, cursor pages, threads, sidebar counts), organization-level mail roles that group mailboxes for display only, and the v2 look (global tokens, three-pane inbox, shortcuts, ⌘K) for the whole Web app.

**Architecture:** The API adds a read-only inbox read model and a mail-role module inside the existing `mailbox` starter. The readable set comes from a new access-row query (no unread counting) filtered by `domain.MailboxAccessFor(...).View`; listing merges per-mailbox `LATERAL` pages on a new `(mailbox_id, direction, created_at DESC, id DESC)` index; counts only touch readable mailboxes. Roles are a `mail_roles` table plus `mailboxes.role_id` (`ON DELETE SET NULL`). The Web BFF forwards whitelisted parameters; `/console/mailboxes` becomes the unified inbox while `?mailbox=` keeps the existing workspace.

**Tech Stack:** Go (Gin, GORM, Wire), PostgreSQL 13+, OpenAPI contracts (redocly, oasdiff in CI), Next.js 16 BFF + React 19, TanStack Query, Tailwind 4 tokens, `next/font/google`, Vitest, kest flows.

**Spec:** `docs/plans/unified-inbox.zh-CN.md` (approved; §-numbers and D-numbers below refer to it). Visual reference for UI tasks: `docs/design/v2/MainV2.dc.html`, with the numbers transcribed in spec §11.0 taking precedence. Branch `docs/v2-architecture` (or a feature branch cut from it).

## Global Constraints

- PostgreSQL only; no SQLite. Migrations are frozen SQL via `execStatements` with `UseTransaction: true`, each with `Down`; register the name in `api/internal/modules/mailbox/provider.go` (`WithStarterMigrationNames`) and refresh `api/database/migrations/testdata/schema.golden.sql` with `FASTBUILD_UPDATE_GOLDEN_SCHEMA=1`.
- Postgres tests run with `FASTBUILD_TEST_POSTGRES_DSN='postgres://fastbuild:fastbuild_test@127.0.0.1:55450/postgres?sslmode=disable'`; without it they skip, so every "Expected: PASS" below means with the DSN set.
- English-only source and API text; user copy only in `web/src/i18n/modules/mailbox/{en-US,zh-Hans}.ts`, both locales structurally identical. Brand `Zmail`.
- Contract first: `contracts/openapi.yaml` and `contracts/MAILBOXES.md` change in Task 1; `cd contracts && corepack pnpm generate` keeps `web/src/http/generated/openapi.ts` and `admin/src/http/generated/openapi.ts` in sync. Additions only (oasdiff).
- New routes live only in the session group (`auth.WithMiddleware("auth", "organization_context")`), never the API-key group. Route names: `inbox_messages.index`, `inbox_message_counts.index`, `mail_roles.index|store|update|destroy`, `mailboxes.role_assignment.update`.
- Reading comes only from `View` (personal owner or shared-mailbox member row). Owners/admins get no mail through roles or administration. Unreadable mailboxes, missing mailboxes, invisible roles, and other organizations' ids filter to an empty result, never `403`/`404`.
- Sealed messages (`unsealedMessage`) and `trash` never appear in any view or count.
- Views (§5.2): `all` = inbound + `default`; `today` = inbound, placement ≠ `trash`, `created_at ∈ [day_start, day_end)`; `pending` = my star, placement ∉ {`archive`,`trash`}, both directions; `sent` = outbound + `default`. Order `(created_at DESC, id DESC)`.
- Query parameters (§8.2): `view` (default `all`), `role_id` (positive integer or `none`), `mailbox_id`, `unread` (`true`/`false`), `q` (1–200 characters as sent, trimmed non-empty, no control characters; only with `view=all`), `group=thread` (only with `all`/`sent`, never with `unread` or `q`), `tz` (IANA, ≤ 64 characters, default `UTC`), `cursor`, `per_page` (1–100, default 15, out-of-range integers clamp). Repeated parameter, `page`, bad value, bad cursor, bad `tz`, or a forbidden combination → `400 COMMON.INVALID_INPUT`. Counts accept only `tz`.
- Search reuses `searchCondition`, `searchPattern`, `repository.search` (2-second `SET LOCAL statement_timeout`) → `422 MAILBOX.SEARCH_TOO_BROAD`. Logs never carry `q`, subjects, addresses, snippets, or role names.
- Snippet (D10): `substr(text_body, 1, 400)` at read time, whitespace runs collapsed to one space, trimmed, cut to 140 runes; HTML-only mail has `""`. No new column.
- Mail roles (§4.3): name trimmed 1–64 runes, unique per organization ignoring case; color from `gray, red, orange, yellow, green, teal, blue, purple, pink` (default `gray`); description trimmed 0–280 runes, no control characters; `owner_user_id` null or a current member; at most 50 per organization; a mailbox has at most one role. Roles grant and revoke nothing (D31). Writes need `viewer.Role.CanManageOrganization()`; assignment needs `canAdminister`.
- New error codes: `MAILBOX.ROLE_NOT_FOUND` 404, `MAILBOX.ROLE_NAME_TAKEN` 409, `MAILBOX.ROLE_LIMIT_REACHED` 422.
- BFF responses use `privateMailboxResponse` (`private, no-store`, vary `Cookie`, `Organization-Id`); mock handlers follow `web/AGENTS.md` (`guardMockBffRoute()`, `guardSameOriginMutation(request)` for writes).
- No feature flag (D27); no new dependency (no `cmdk`, D23); `admin/` behavior unchanged.
- Performance (D28): 10 readable mailboxes with 200,000 messages plus one unreadable mailbox with 50,000 — `all&group=thread` first and 20th page, `today`, `pending`, `sent` p95 < 200 ms; `unread=true` p95 < 300 ms; counts p95 < 300 ms and moving the unreadable mailbox from 50,000 to 500,000 changes counts p95 by ≤ 10%; 3+ character search p95 < 500 ms.
- Never send real mail from tests.

Resolved while planning (the spec left these open or assumed something the code does not do):

- `contracts check:routes` compares `openapi.yaml` with the Go route catalog in both directions, so it fails from Task 1 until Task 11 registers the last route. Task 1 proves the contract with `corepack pnpm check`; `make contract-check` passes in full from Task 11 on.
- The Web `mailboxSchema` is a zod `strictObject`, so the current Web would reject `role_id` instead of ignoring it (§8.1 assumed the opposite). Task 1 makes Web accept `role_id` before the API emits it.
- `NewService` has ~96 callers in tests. New repository seams are separate domain interfaces (`domain.MailInboxRepository`, `domain.MailRoleRepository`) that `NewService` takes from `repo` by type assertion; a nil seam returns `domain.ErrServiceUnavailable`. No Wire change.
- `domain.Mailbox.RoleID` is added in Task 2 (always nil until Task 10 adds the column), so role filtering and grouping are written once.
- `tz=Local` and an empty `tz=` are `400` (Go would otherwise resolve them to the server's zone or UTC silently).
- The API has no per-route body cap today; role handlers wrap the body in `http.MaxBytesReader` at 4 KiB (overflow → `400 COMMON.INVALID_INPUT`), and the BFF keeps `maxOrganizeBodyBytes`.
- Light `--text-faint` `#A8A49B` is 2.49:1 on white; it becomes `#8E8A81` (3.44:1). Light `--bg-subtle` becomes `#FBFAF7` so the existing `text-muted`/`bg-subtle` 4.5:1 guard holds.

## Review Focus

1. The Web already deployed parses `GET /v1/mailboxes` with a strict schema: an API that adds `role_id` must not blank the mailbox page. Test in Task 1.
2. Odd query values — `tz=Local`, `tz=`, `per_page=abc`, `role_id=0`, a cursor that is valid base64 but not `<nanos>.<id>`, or longer than 128 characters — must answer `400`, never `500` and never the server's local day. Test in Task 3.
3. Renaming a role to its own name in different case (`Design` → `DESIGN`) must succeed, while `design ` on another role is `409`. Test in Task 10.
4. A mailbox whose `view` was revoked after the list loaded: opening its row gets `404`; the reading pane shows an unavailable notice and the list and counts refetch, without a crash. Test in Task 5.
5. Pressing `j` on the last loaded row while `has_more` is true must load the next page and move to its first row, not stop. Test in Task 15.

---

## Slice S0 — Contracts

### Task 1: Contracts, vocabulary, and a role-tolerant Web schema

**Files:**
- Modify: `contracts/openapi.yaml`, `contracts/MAILBOXES.md`, `CONTEXT.md`
- Generate: `web/src/http/generated/openapi.ts`, `admin/src/http/generated/openapi.ts`
- Modify: `web/src/features/mailbox/schemas.ts` (`mailboxSchema`), `web/src/features/mailbox/server/mock-mailbox-store.ts` (mailboxes carry `role_id: null`)
- Test: `web/src/test/mailbox-service.test.ts`

**Interfaces:**
- Produces (OpenAPI, per §8.1–§8.8):
  - `GET /v1/inbox-messages` → `InboxCursorPageResponse { code, message, data: InboxItem[], meta: CursorPageMeta }`. `InboxItem` = every field of the single-mailbox list item plus `mailbox_address`, `role_id` (integer|null), `snippet`, optional `thread { message_count, message_ids, unread, starred, label_ids }`.
  - `GET /v1/inbox-message-counts` → `InboxCounts { views { all_unread, today, today_unread, pending, sent_waiting }, roles [{ role_id, unread }], ungrouped_unread, mailboxes [{ mailbox_id, unread }], today_starts_at, today_ends_at }`.
  - `GET|POST /v1/mail-roles`, `PUT|DELETE /v1/mail-roles/{role_id}` with `MailRole { id, name, color, description, owner_user_id, mailbox_ids, created_at, updated_at }` and `MailRoleInput { name, color?, description?, owner_user_id? }`; list response is bounded (≤ 50).
  - `PUT /v1/mailboxes/{mailbox_id}/role-assignment` with `MailboxRoleAssignment { role_id: integer|null }` (required) → `Mailbox`.
  - `Mailbox.role_id` (integer|null); error codes `MAILBOX.ROLE_NOT_FOUND`, `MAILBOX.ROLE_NAME_TAKEN`, `MAILBOX.ROLE_LIMIT_REACHED`.
- `MAILBOXES.md`: paths table +7 rows, `role_id` in the Mailboxes example, new sections "Unified Inbox" (views, parameters, combination rules, day boundary, cursor, threads, counts) and "Mail Roles", two Access-table rows, three error rows (§8.9).
- `CONTEXT.md`: glossary entry **Mail role** — organization-level grouping of mailboxes by business identity; not an organization role, not an access role, grants nothing.
- Web: `mailboxSchema` gains `role_id: optional(nullable(safeIdSchema))`; `Mailbox['role_id']` is `number | null | undefined`.

- [ ] **Step 1: Write the failing test** in `mailbox-service.test.ts`: `parses mailboxes with and without role_id` — a list response whose first item has `role_id: 3` and second has no `role_id` parses to `[3, undefined]`; an item with `role_id: 0` is an invalid response.
- [ ] **Step 2: Run** `cd web && corepack pnpm vitest run src/test/mailbox-service.test.ts` — Expected: FAIL (strict schema rejects `role_id`).
- [ ] **Step 3: Write** the contract, `MAILBOXES.md`, `CONTEXT.md` changes, the schema change, and `role_id: null` on mock mailboxes.
- [ ] **Step 4: Run** `cd contracts && corepack pnpm check && corepack pnpm generate`, then `cd web && corepack pnpm vitest run src/test/mailbox-service.test.ts && corepack pnpm type-check`, `cd admin && corepack pnpm type-check` — Expected: PASS. Then `cd contracts && corepack pnpm check:routes` — Expected: FAIL naming exactly the 7 new operations as missing from the Go route assembly (Tasks 3, 7, 11 clear it).
- [ ] **Step 5: Commit** `feat(contracts): unified inbox, inbox counts, and mail roles`.

## Slice S1 — "All mail" end to end

### Task 2: Inbox index, access rows, and the all/sent listing query

**Files:**
- Create: `api/database/migrations/2026_11_22_090100_add_mail_inbox_index.go`, `api/database/migrations/mail_inbox_index_test.go`
- Modify: `api/database/migrations/testdata/schema.golden.sql`, `api/internal/modules/mailbox/provider.go`, `UPGRADING.md`
- Create: `api/internal/domain/mail_inbox.go`
- Modify: `api/internal/domain/mailbox.go` (`Mailbox.RoleID`)
- Create: `api/internal/modules/mailbox/inbox_repository.go`, `api/internal/modules/mailbox/inbox_repository_test.go`
- Modify: `api/internal/modules/mailbox/listing_repository.go` (extract thread stats from `ListThreads`)

**Interfaces:**
- Produces:
  - Migration SQL (§6.2): `CREATE INDEX IF NOT EXISTS idx_mail_messages_inbox_time ON mail_messages (mailbox_id, direction, created_at DESC, id DESC)`; `Down` drops it. `UPGRADING.md` gets the `CREATE INDEX CONCURRENTLY IF NOT EXISTS` pre-build paragraph, like the organize trigram indexes.
  - `domain.Mailbox.RoleID *uint` — the mail role; nil when ungrouped (and always nil until Task 10).
  - In `api/internal/domain/mail_inbox.go`:
    ```go
    type InboxView string // InboxViewAll "all", InboxViewToday "today", InboxViewPending "pending", InboxViewSent "sent"
    func (v InboxView) Valid() bool
    type InboxCursor struct { CreatedAt time.Time; ID uint }
    type MailboxAccessRow struct { Mailbox *Mailbox; Member *MailboxMember } // Member nil without a member row
    type InboxQuery struct {
        MailboxIDs     []uint // already the readable, narrowed set; never empty
        MembershipID   uint
        View           InboxView
        Unread         *bool
        Query          string
        GroupThreads   bool
        DayStart, DayEnd time.Time // today only
        After          *InboxCursor
        Limit          int // per_page + 1
    }
    type InboxViewCounts struct { Today, TodayUnread, Pending, SentWaiting int }
    type MailInboxRepository interface {
        ListMailboxAccessRows(ctx context.Context, organizationID, membershipID uint) ([]MailboxAccessRow, error)
        CountUnreadByMailboxIDs(ctx context.Context, organizationID, readerMembershipID uint, mailboxIDs []uint) (map[uint]int, error)
        ListInbox(ctx context.Context, query InboxQuery) ([]*MailMessage, error)
        MessageSnippets(ctx context.Context, ids []uint) (map[uint]string, error)
        CountInboxViews(ctx context.Context, membershipID uint, mailboxIDs []uint, dayStart, dayEnd time.Time) (InboxViewCounts, error)
    }
    ```
  - `*repository` implements `ListMailboxAccessRows` (live mailboxes of the organization, `id ASC`, `LIMIT MaxMailboxesPerOrganization`, with the membership's member rows; no unread count), `ListInbox` for `all` and `sent` (§8.2 SQL: `unnest(@mailbox_ids) CROSS JOIN LATERAL ... LIMIT @limit` per mailbox, merged and limited; thread heads by the `NOT EXISTS newer` clause; `Thread` filled per mailbox via `threadInfos`; bodies omitted; `MailboxAddress` filled), and `MessageSnippets` (`substr(text_body, 1, 400)` by id). Other views return `domain.ErrInvalidInput` until Task 6.
  - `func threadInfos(db *gorm.DB, mailboxID, membershipID uint, view domain.MailMessageFilter, threadIDs []uint) (map[uint]domain.MailThreadInfo, error)` in `listing_repository.go`; `ListThreads` calls it, behavior unchanged.

- [ ] **Step 1: Write failing tests:** `TestMailInboxIndexMigrationKeepsPrecreatedIndex` (index built beforehand survives `Up`; `Down` drops it); in `inbox_repository_test.go`: `TestInboxAccessRowsListMemberRowsWithoutCounts` (deleted mailbox absent; `UnreadCount` 0; member row present only for the membership's shared mailboxes); `TestInboxListMergesMailboxesNewestFirst` (two mailboxes with interleaved `created_at`); `TestInboxCursorPagesWithoutGapsOrRepeats` (five messages with one equal `created_at`, `Limit` 3, page two via `After` of the last row; a message inserted between pages does not reappear); `TestInboxExcludesSealedArchivedAndTrash`; `TestInboxThreadHeadIsNewestInView` (thread whose newest message is archived: the row is the newest `default` message and `Thread.MessageIDs` equal `ListThreads` for that mailbox); `TestInboxThreadsNeverCrossMailboxes` (two mailboxes, mutual `In-Reply-To` → two rows); `TestInboxSentListsOutboundDefault`; `TestInboxSnippetsReadFirst400Characters` (5,000-character body → 400 characters; HTML-only → `""`).
- [ ] **Step 2: Run** `cd api && go test ./database/migrations/... -run 'MailInboxIndex|SchemaGolden' && go test ./internal/modules/mailbox/... -run 'Inbox|ThreadRow'` — Expected: FAIL.
- [ ] **Step 3: Implement** the migration (register in `provider.go`), golden schema, domain file, repository methods, and the `threadInfos` extraction.
- [ ] **Step 4: Run** Step 2 command — Expected: PASS (existing `TestThreadRowShowsLatestAndCount` still passes).
- [ ] **Step 5: Commit** `feat(mailbox): inbox time index and cross-mailbox listing query`.

### Task 3: Inbox list service, handler, and route

**Files:**
- Create: `api/internal/modules/mailbox/inbox_service.go`, `api/internal/modules/mailbox/inbox_handler.go`, `api/internal/modules/mailbox/inbox_test.go`
- Modify: `api/internal/modules/mailbox/service.go` (`Service` interface, `inbox` field, `NewService`), `routes.go`, `dto.go`, `handler_test.go`

**Interfaces:**
- Consumes: Task 2 `domain.MailInboxRepository`, `domain.InboxQuery`, `domain.MailboxAccessRow`.
- Produces:
  - `service.inbox domain.MailInboxRepository`, set in `NewService` by `repo.(domain.MailInboxRepository)`; nil → `domain.ErrServiceUnavailable`.
  - ```go
    type InboxRequest struct {
        View         domain.InboxView
        RoleID       uint // 0 = no role filter
        Ungrouped    bool // role_id=none
        MailboxID    uint
        Unread       *bool
        Query        string
        GroupThreads bool
        Location     *time.Location
        After        *domain.InboxCursor
        PerPage      int
    }
    type InboxItem struct { Message *domain.MailMessage; RoleID *uint; Snippet string }
    type InboxPage struct { Items []InboxItem; Next *domain.InboxCursor }
    func (s *service) ListInbox(ctx context.Context, viewer domain.OrganizationContext, request InboxRequest) (InboxPage, error)
    func (s *service) accessRows(ctx context.Context, viewer domain.OrganizationContext) ([]*domain.Mailbox, error) // Access filled, no unread
    func inboxSnippet(raw string) string
    func encodeInboxCursor(cursor domain.InboxCursor) string
    func decodeInboxCursor(token string) (*domain.InboxCursor, error) // ErrInvalidInput; ≤ 128 chars, base64url("<unix nanos>.<id>"), id > 0
    func parseInboxRequest(query url.Values) (InboxRequest, error)  // all §8.2 rules; wraps domain.ErrInvalidInput
    func (h *Handler) ListInboxMessages(c *gin.Context)
    ```
  - `ListInbox` order (§8.2 pseudo-code): readable = `accessRows` filtered by `Access.View`; ∩ `MailboxID`; then by `Mailbox.RoleID` (`Ungrouped` → nil; `RoleID` → equal); empty → empty page without calling `ListInbox` of the repository; else query with `Limit = PerPage+1`, cut, `Next` from the last kept row, fill `fillViewerState`, `MessageSnippets` → `inboxSnippet`, `MailboxAddress` and `RoleID` from the access rows. Outbound `read` is always true.
  - Route `auth.GET("/inbox-messages", h.ListInboxMessages).Name("inbox_messages.index")`; response via `response.SuccessCursorPage(c, items, perPage, encodeInboxCursor(next))` (empty string when no next) after `privateResponse(c)`. `InboxItemResponse` mirrors the single-mailbox list item plus `mailbox_address`, `role_id`, `snippet`, `thread` (omitted unless grouped).
  - Log `mail.inbox.list` with `request_id`, `organization_id`, `membership_id`, `view`, `grouped`, `has_query`, `readable_mailboxes`, `result_count`, `duration_ms`.

- [ ] **Step 1: Write failing tests** in `inbox_test.go`: `TestInboxListsOnlyReadableMailboxes` (Postgres access fixture `newPostgresAccessFixture`: B sees `b@` and `team@`, C sees only `team@`, admin gets an empty page); `TestInboxEmptyReadableSetSkipsQuery` (test double `inboxRecorder` embedding `*fakeRepository`, implementing `domain.MailInboxRepository` and recording calls: admin non-member → `ListInbox` never called); `TestInboxFilterOnUnreadableMailboxIsEmpty` (C with `MailboxID` = `b@` → 200 empty, no error); `TestInboxSnippetCollapsesAndCuts` (`"a \n\t b"` → `"a b"`; 200 mixed CJK/Latin runes → 140; `"   "` → `""`); `TestInboxCursorRoundTrip`. In `handler_test.go`: `TestInboxRequestValidation`, a table where each row expects `400 COMMON.INVALID_INPUT` — `view=bogus`, `view=all&view=sent`, `page=1`, `view=today&q=x`, `group=thread&unread=true`, `group=thread&view=today`, `q=` + 201 characters, `tz=Mars/Base`, `tz=Local`, `tz=`, `per_page=abc`, `role_id=0`, `cursor=` base64url of `"x.y"`, a 129-character cursor (Review Focus 2) — plus `per_page=0` → `meta.per_page` 1, `per_page=101` → 100, `role_id=none` → 200; `TestInboxItemShape` (item has `mailbox_address`, `role_id: null`, `snippet`, own `read`/`starred`, no `thread` unless grouped; last page `next_cursor: null`, `Cache-Control: private, no-store`).
- [ ] **Step 2: Run** `cd api && go test ./internal/modules/mailbox/... -run 'Inbox'` — Expected: FAIL.
- [ ] **Step 3: Implement**, then `cd api && make route-catalog-check`.
- [ ] **Step 4: Run** Step 2 command and `cd api && bash ../.agents/skills/verification-before-completion/scripts/run-tiers.sh 1 ./internal/modules/mailbox/...` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(mailbox): unified inbox listing endpoint`.

### Task 4: Web BFF, service, hook, and mock for the inbox list

**Files:**
- Modify: `web/src/app/api/_shared/success-response.ts`, `web/src/features/mailbox/server/mailbox-route.ts`, `web/src/features/mailbox/server/mock-mailbox-store.ts`, `web/src/features/mailbox/schemas.ts`, `web/src/features/mailbox/types.ts`, `web/src/features/mailbox/services/mailbox-service.ts`, `web/src/features/mailbox/hooks/use-mailbox.ts` (`mailboxKeys`, `invalidateMail`)
- Create: `web/src/app/api/inbox-messages/route.ts`, `web/src/features/mailbox/hooks/use-inbox.ts`
- Test: `web/src/test/mailbox-route.test.ts`, `web/src/test/mailbox-service.test.ts`, `web/src/test/mock-bff-parity/mailbox.ts`

**Interfaces:**
- Consumes: Task 1 contract.
- Produces:
  - `apiCursorPageResponse<T>(items: T[], meta: { per_page: number; has_more: boolean; next_cursor: string | null }, headers?: HeadersInit): NextResponse`.
  - `listInboxMessagesRoute(request: Request): Promise<NextResponse>` — validates the §8.2 grammar itself (same `400`s), forwards only `view, role_id, mailbox_id, unread, q, group, tz, cursor, per_page` to `inbox-messages`; mock calls `mockMailboxStore.listInbox(organizationId, viewer, query)`.
  - Types: `InboxView = 'all' | 'today' | 'pending' | 'sent'`; `InboxFilters { view: InboxView; roleId?: number | 'none'; mailboxId?: number; unread?: boolean; q?: string; group?: 'thread'; tz: string }`; `InboxItem` (zod `inboxItemSchema`); `InboxPage { items: InboxItem[]; meta: { per_page: number; has_more: boolean; next_cursor: string | null } }`.
  - `mailboxService.listInbox(organizationId: number, filters: InboxFilters, cursor?: string, perPage?: number): Promise<InboxPage>` (uses `request.getEnvelope`, `inboxPageEnvelopeSchema`).
  - `mailboxKeys.inbox(organizationId, filters)` under `[...mailboxKeys.organization(id), 'inbox']`; `invalidateMail` also invalidates that prefix.
  - `useInboxMessages(organizationId: number, filters: InboxFilters)` — `useInfiniteQuery`, `getNextPageParam` = `meta.next_cursor ?? undefined`, `liveRefresh`.
  - `useUpdateInboxMessages(organizationId: number)` — mutation over `{ mailboxId } & UpdateMailMessagesInput`, invalidating like `useUpdateMailMessages`.
  - Mock `listInbox` covers `all`, `sent`, `group=thread`, `mailbox_id`, `role_id` (null roles for now), cursor `(created_at, id)`, and the access rules already in the mock (`mailboxAccessFor`).

- [ ] **Step 1: Write failing tests:** route — `forwards only whitelisted inbox parameters` (proxy: `?view=all&group=thread&foo=1` reaches upstream `inbox-messages` with `view`, `group`, `per_page=15` only); `rejects page and repeated parameters without calling upstream`; `mock inbox hides another member's personal mailbox and pages by cursor` (two pages, no repeats, `next_cursor` null at the end, `Cache-Control` private); service — `listInbox parses a cursor page` and `rejects an item without mailbox_address`; parity — `listInbox` against the mock.
- [ ] **Step 2: Run** `cd web && corepack pnpm vitest run src/test/mailbox-route.test.ts src/test/mailbox-service.test.ts src/test/mock-bff-service-parity.test.ts` — Expected: FAIL.
- [ ] **Step 3: Implement** following the `listMailLogRoute` pattern.
- [ ] **Step 4: Run** Step 2 command and `cd web && corepack pnpm type-check && corepack pnpm lint` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(web): BFF route and query for the unified inbox`.

### Task 5: Unified inbox page (current look)

**Files:**
- Create: `web/src/features/mailbox/components/mailboxes-route.tsx`, `inbox-workspace.tsx`, `inbox-list.tsx`, `inbox-reading-pane.tsx`
- Modify: `web/src/app/(protected)/(console)/console/mailboxes/page.tsx`, `web/src/features/mailbox/hooks/use-inbox.ts`, `web/src/i18n/modules/mailbox/en-US.ts`, `zh-Hans.ts`
- Test: `web/src/test/inbox-ui.test.tsx` (create; mock hooks the way `mailbox-ui.test.tsx` does)

**Interfaces:**
- Consumes: Task 4 hooks; existing `useMailThread`, `useMailMessage`, `useMarkMailMessageRead`, `MessageDetail`, `ThreadView` (`thread-view.tsx`), `MailOrganizationBoundary`, `OrganizationLostState`.
- Produces:
  - `MailboxesRoute()` — renders `MailboxWorkspace` when the URL has `mailbox`, else `InboxWorkspace` (D20). The stored mailbox id no longer redirects a bare `/console/mailboxes`.
  - `InboxWorkspace()` — three panes; `view=all&group=thread`; `?open=<message id>` keeps the open row.
  - `InboxList({ pages, selectedId, onOpen, onLoadMore, hasMore })` — rows: unread dot, sender, `（n）` for threads, time, subject, snippet; loads the next page near the bottom.
  - `InboxReadingPane({ item, access })` — thread rows show `ThreadView` for `(mailbox_id, thread_id)`, single rows `MessageDetail`; opening an unread inbound message calls `useMarkMailMessageRead` and sets the row's `read` optimistically in the infinite-query cache, rolling back on error; Archive and Trash buttons send `useUpdateInboxMessages` with the row's `thread.message_ids` (or `[id]`) and `placement` `archive`/`trash`, rendered only when the mailbox's `access.send` (from `useMailboxes`).
  - i18n keys `inbox.empty.noMailboxes`, `inbox.empty.view`, `inbox.unavailable`, `inbox.actions.archive`, `inbox.actions.trash`, `inbox.title`.

- [ ] **Step 1: Write failing tests:** `renders the unified inbox by default and the mailbox workspace for ?mailbox=`; `opening a thread row loads the thread and marks it read`; `archive sends the row's thread message ids with placement archive`; `archive and trash are hidden without send`; `no readable mailbox shows the empty state and, for an admin, Manage mailboxes`; `a row whose mailbox became unreadable shows the unavailable notice and refetches list and mailboxes` (detail answers `404 MAILBOX.NOT_FOUND`; Review Focus 4); `scrolling to the end fetches the next page only while has_more`; `a failed list shows retry and a View by mailbox link to ?mailbox=` (§13.3).
- [ ] **Step 2: Run** `cd web && corepack pnpm vitest run src/test/inbox-ui.test.tsx src/test/mailbox-ui.test.tsx` — Expected: FAIL (new file), existing file PASS.
- [ ] **Step 3: Implement** with existing shadcn components and tokens; all copy in both locales.
- [ ] **Step 4: Run** Step 2 command, `corepack pnpm type-check && corepack pnpm lint` — Expected: PASS. The controller opens `/console/mailboxes` in mock mode in the built-in browser and walks: list → open thread → archive → `?mailbox=<id>` shows folders and trash.
- [ ] **Step 5: Commit** `feat(web): unified inbox as the default mail page`.

## Slice S2 — Views, filters, and counts

### Task 6: Today, pending, search, unread, and count queries

**Files:**
- Modify: `api/internal/modules/mailbox/inbox_repository.go`
- Test: `api/internal/modules/mailbox/inbox_repository_test.go`

**Interfaces:**
- Consumes: Task 2 types.
- Produces:
  - `ListInbox` now serves every view (§8.2): `today` (inbound, `placement <> 'trash'`, `created_at >= DayStart AND < DayEnd`, LATERAL on the inbox index), `pending` (from `mail_message_stars` of `MembershipID`, placement ∉ {archive, trash}, both directions), `Query` (inside `r.search`, placement ≠ trash, both directions, `searchCondition`), `Unread` (`unreadByMember`, negated for false). All keep `unsealedMessage` and the `(created_at, id) < After` keyset.
  - `CountUnreadByMailboxIDs` — the `ListMailboxes` grouped predicate plus `mailboxes.organization_id = ?`; empty ids → empty map without touching `r.db`.
  - `CountInboxViews` — `today` and `today_unread` (`COUNT(*)` and `COUNT(*) FILTER (WHERE unreadByMember)`), `pending`, `sent_waiting` (`queued_reason = 'send_limit'`), each limited to the given ids and unsealed.

- [ ] **Step 1: Write failing tests:** `TestInboxTodayIncludesArchivedNotTrash` (yesterday's unread absent; today's archived present; trashed absent; `created_at == DayStart` in, `== DayEnd` out); `TestInboxPendingIsMyStarsOutsideArchiveAndTrash` (starred outbound included; another member's star ignored); `TestInboxSearchSpansPlacementsButTrash`; `TestInboxSearchTimeoutIsTooBroad` (repository with `searchTimeout` of 1 ms and a `pg_sleep` pattern, as `TestSearchTimeoutMapsToTooBroad` does); `TestInboxUnreadNeverListsOutbound`; `TestInboxCountUnreadOnlyGivenMailboxes` (another organization's id counts nothing; `(&repository{}).CountUnreadByMailboxIDs(ctx, 1, 1, nil)` returns an empty map and nil error); `TestInboxCountUnreadMatchesListMailboxes` (same numbers as `ListMailboxes` for each readable mailbox); `TestInboxCountViews`.
- [ ] **Step 2: Run** `cd api && go test ./internal/modules/mailbox/... -run 'Inbox'` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** Step 2 command — Expected: PASS.
- [ ] **Step 5: Commit** `feat(mailbox): inbox views, search, unread filter, and count queries`.

### Task 7: Counts endpoint and the viewer's day

**Files:**
- Modify: `api/internal/modules/mailbox/inbox_service.go`, `inbox_handler.go`, `service.go` (`Service`), `routes.go`, `dto.go`
- Test: `api/internal/modules/mailbox/inbox_test.go`, `handler_test.go`

**Interfaces:**
- Consumes: Task 6 repository methods; `s.now`.
- Produces:
  - `func inboxDay(now time.Time, loc *time.Location) (start, end time.Time)` — `time.Date(y, m, d, 0, 0, 0, 0, loc)` of `now.In(loc)` and of the next date, returned in UTC.
  - `ListInbox` fills `DayStart`/`DayEnd` for `today` from `request.Location` (default `time.UTC`).
  - ```go
    type InboxCounts struct {
        AllUnread, Today, TodayUnread, Pending, SentWaiting int
        Roles           []InboxRoleCount    // { RoleID uint; Unread int }, role_id ascending, only roles with a readable mailbox
        UngroupedUnread int
        Mailboxes       []InboxMailboxCount // { MailboxID uint; Unread int }, ascending, readable only
        TodayStartsAt, TodayEndsAt time.Time
    }
    func (s *service) InboxCounts(ctx context.Context, viewer domain.OrganizationContext, loc *time.Location) (InboxCounts, error)
    func (h *Handler) InboxMessageCounts(c *gin.Context) // only tz; any other or repeated parameter → 400
    ```
  - Empty readable set → zeros and empty arrays without calling `CountUnreadByMailboxIDs` or `CountInboxViews`. `AllUnread = Σ Roles + UngroupedUnread`.
  - Route `auth.GET("/inbox-message-counts", h.InboxMessageCounts).Name("inbox_message_counts.index")`; JSON per Task 1.
  - A search answered with `ErrMailSearchTooBroad` logs `mail.inbox.search_too_broad` (`request_id`, `organization_id`, `query_length`, `readable_mailboxes`).

- [ ] **Step 1: Write failing tests:** `TestInboxDayBounds` (UTC; `Asia/Shanghai` at `2026-11-18T23:30Z` → `2026-11-18T16:00Z`–`2026-11-19T16:00Z`; `America/New_York` on 2026-03-08 → 23 h, on 2026-11-01 → 25 h); `TestInboxTodayUsesViewerTimezone` (Postgres fixture, `s.now` fixed: a message at `2026-11-18T17:00Z` is in Shanghai's today and not in UTC's); `TestInboxCountsGroupByRole` (`inboxRecorder` returning rows with `RoleID` set: sorted roles, ungrouped, sum identity, mailboxes ascending); `TestInboxCountsOnlyQueryReadableMailboxes` (recorder sees exactly C's readable ids; admin → no repository count call, zeros, `[]`); handler `TestInboxCountsRejectsOtherParameters` (`?view=all` and `?tz=UTC&tz=UTC` → 400) and `TestInboxSearchTooBroadIs422`.
- [ ] **Step 2: Run** `cd api && go test ./internal/modules/mailbox/... -run 'Inbox'` — Expected: FAIL.
- [ ] **Step 3: Implement**, then `cd api && make route-catalog-check`.
- [ ] **Step 4: Run** Step 2 command — Expected: PASS. `cd contracts && corepack pnpm check:routes` — Expected: FAIL naming only the 5 role operations.
- [ ] **Step 5: Commit** `feat(mailbox): unified inbox counts and the viewer's day`.

### Task 8: Inbox benchmarks

**Files:**
- Create: `api/internal/modules/mailbox/inbox_bench_test.go`
- Modify: `api/internal/modules/mailbox/listing_bench_test.go` (`TestMain` also drops the inbox scene)

**Interfaces:**
- Consumes: Tasks 2–7; the `measure` helper and scene pattern of `listing_bench_test.go`.
- Produces: a separately seeded scene (real migrations, seeded once per binary) per §14.3 — one organization, 11 mailboxes; 10 readable with 200,000 messages (one 100,000, nine ≈ 11,100), three per thread, 70% inbound, 5% trash, 5% folder, 10% archive, 80% read by the bench membership, 2,000 arriving today, 300 starred; one unreadable mailbox with 50,000. Benchmarks: `BenchmarkInboxAllThreadsFirstPage`, `BenchmarkInboxAllThreadsPage20`, `BenchmarkInboxToday`, `BenchmarkInboxPending`, `BenchmarkInboxSent`, `BenchmarkInboxUnread`, `BenchmarkInboxCounts`, `BenchmarkInboxCountsUnreadableGrowth` (adds 450,000 rows to the unreadable mailbox, then measures counts again), `BenchmarkInboxSearch`.

- [ ] **Step 1: Write** the scene and benchmarks; each reports p95 like the existing listing benchmarks.
- [ ] **Step 2: Run** `cd api && FASTBUILD_TEST_POSTGRES_DSN=... go test ./internal/modules/mailbox/... -run '^$' -bench 'Inbox' -benchtime 30x` — Expected: all p95 under the Global Constraints targets; counts growth ≤ 10%.
- [ ] **Step 3: If a target misses,** follow §14.3 in order: confirm `idx_mail_messages_inbox_time` in `EXPLAIN ANALYZE`; for `unread=true`, move the read anti-join inside the per-mailbox LATERAL `LIMIT`; no cache. Rerun Step 2.
- [ ] **Step 4: Record** p95 per benchmark and any `EXPLAIN ANALYZE` used in the task report.
- [ ] **Step 5: Commit** `test(mailbox): unified inbox benchmarks`.

### Task 9: Web views, filters, search, and sidebar counts

**Files:**
- Modify: `web/src/features/mailbox/server/mailbox-route.ts`, `mock-mailbox-store.ts`, `schemas.ts`, `types.ts`, `services/mailbox-service.ts`, `hooks/use-inbox.ts`, `hooks/use-mailbox.ts` (`mailboxKeys.inboxCounts`, `invalidateMail`), `components/inbox-workspace.tsx`, `components/inbox-list.tsx`, i18n files
- Create: `web/src/app/api/inbox-message-counts/route.ts`, `web/src/features/mailbox/components/inbox-sidebar.tsx`
- Test: `web/src/test/mailbox-route.test.ts`, `mailbox-service.test.ts`, `mock-bff-parity/mailbox.ts`, `inbox-ui.test.tsx`

**Interfaces:**
- Consumes: Tasks 4–5, Task 7 contract shape.
- Produces:
  - `getInboxMessageCountsRoute(request: Request)` — forwards only `tz`; mock `mockMailboxStore.inboxCounts(organizationId, viewer, tz)`.
  - Mock `listInbox` gains `today` (by `tz`), `pending`, `q` (case-insensitive substring, trash excluded, 1-character query → `422 MAILBOX.SEARCH_TOO_BROAD`), `unread`.
  - `mailboxService.inboxCounts(organizationId: number, tz: string): Promise<InboxCounts>`; `useInboxCounts(organizationId, tz)` (`liveRefresh`); `mailboxKeys.inboxCounts(organizationId, tz)`.
  - `useInboxTimezone(): string` — `localization.timezone` from `useUserSettings()` when `isWebFeatureEnabled('setting')`, else `Intl.DateTimeFormat().resolvedOptions().timeZone`.
  - URL state (D20): `?view=all|today|pending|sent`, `?box=<id>`, `?q=`, `?tab=unread`, `?open=`; invalid values fall back silently; a `400` from the list resets the URL to `view=all`.
  - `InboxSidebar({ counts, mailboxes, view, onView, onBox })` — views with numbers (`all_unread` emphasized; `sent` shows a "queued" badge when `sent_waiting > 0`), then readable mailboxes with unread counts (flat until Task 12).
  - List header: tabs "All / Unread" (D25; unread → `unread=true`, no `group`), search field, "Open in mailbox" link to `?mailbox=<id>` when narrowed to a box. Time shows `HH:mm` today, month-day this year, else with year, in the inbox timezone.

- [ ] **Step 1: Write failing tests:** route — `counts forward only tz`, `mock counts satisfy all_unread equal to the sum over mailboxes`; UI — `Today requests view=today with the inbox timezone`, `Sent shows the queued badge when sent_waiting is positive`, `Unread tab requests unread=true without grouping`, `search requests view=all with q and shows the too-broad notice on 422`, `box narrowing links to the mailbox workspace`, `an invalid view falls back to all`, `a 400 resets the URL to view=all`.
- [ ] **Step 2: Run** `cd web && corepack pnpm vitest run src/test/mailbox-route.test.ts src/test/mailbox-service.test.ts src/test/mock-bff-service-parity.test.ts src/test/inbox-ui.test.tsx` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** Step 2 command, `corepack pnpm type-check && corepack pnpm lint` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(web): inbox views, filters, search, and counts`.

## Slice S3 — Mail roles

### Task 10: Mail role schema and repository

**Files:**
- Create: `api/database/migrations/2026_11_22_090000_add_mail_roles.go`, `api/database/migrations/mail_roles_test.go`
- Modify: `api/database/migrations/testdata/schema.golden.sql`, `api/internal/modules/mailbox/provider.go`
- Create: `api/internal/domain/mail_role.go`, `api/internal/modules/mailbox/role_repository.go`, `api/internal/modules/mailbox/role_repository_test.go`
- Modify: `api/internal/modules/mailbox/model.go` (`MailboxPO.RoleID`, `toDomain`, `MailRolePO`), `errors.go`, `inbox_repository.go` (access rows carry `RoleID`)

**Interfaces:**
- Produces:
  - Migration: the §6.1 SQL verbatim; `Down` drops `mailboxes.role_id` then `mail_roles`.
  - In `mail_role.go`: `MaxMailRolesPerOrganization = 50`, `MaxMailRoleNameLength = 64`, `MaxMailRoleDescriptionLength = 280`;
    ```go
    type MailRole struct {
        ID, OrganizationID uint
        Name               string
        Color              MailLabelColor
        Description        string
        OwnerUserID        *uint
        MailboxIDs         []uint // filled by the service per viewer
        CreatedAt, UpdatedAt time.Time
    }
    type MailRoleRepository interface {
        ListMailRoles(ctx context.Context, organizationID uint) ([]*MailRole, error) // ORDER BY lower(name), id LIMIT 50
        FindMailRole(ctx context.Context, organizationID, id uint) (*MailRole, error)
        CreateMailRole(ctx context.Context, role *MailRole, limit int) error
        UpdateMailRole(ctx context.Context, role *MailRole) error
        DeleteMailRole(ctx context.Context, organizationID, id uint) error
        AssignMailboxRole(ctx context.Context, organizationID, mailboxID uint, roleID *uint) error
        ClearMailRoleOwner(ctx context.Context, organizationID, userID uint) (int64, error)
    }
    ```
  - Module errors `ErrMailRoleNotFound`, `ErrMailRoleNameTaken`, `ErrMailRoleLimitReached`. Create takes `pg_advisory_xact_lock(hashtext('mail_roles'), organization_id)`, counts, inserts; unique violation → `ErrMailRoleNameTaken`. Update/Delete with 0 rows → `ErrMailRoleNotFound`. Assign updates `mailboxes` `WHERE id = ? AND organization_id = ? AND deleted_at IS NULL` (0 rows → `ErrMailboxNotFound`; FK violation → `ErrMailRoleNotFound`), also sets `updated_at`.

- [ ] **Step 1: Write failing tests:** `TestMailRolesMigrationUpDown` (upgrade from `2026_11_21_090100`: existing mailboxes `role_id IS NULL`; `Down` then `Up` succeed); `TestMailRoleNameUniqueIgnoringCase` (`Design` then ` design ` trimmed by the caller → `ErrMailRoleNameTaken`); `TestMailRoleRenameOwnCaseIsAllowed` (`Design` → `DESIGN` on itself succeeds; Review Focus 3); `TestMailRoleColorCheck` (raw insert of `black` fails); `TestMailRoleDeleteUngroupsMailboxes`; `TestMailRoleAssignAcrossOrganizationsIsNotFound`; `TestMailRoleAssignDeletedRoleIsNotFound` (FK path); `TestMailRoleConcurrentCreateStopsAt50` (49 existing, two concurrent creates → exactly one `ErrMailRoleLimitReached`); `TestMailRoleClearOwner`; `TestInboxAccessRowsCarryRoleID`.
- [ ] **Step 2: Run** `cd api && go test ./database/migrations/... -run 'MailRoles|SchemaGolden' && go test ./internal/modules/mailbox/... -run 'MailRole|InboxAccessRows'` — Expected: FAIL.
- [ ] **Step 3: Implement**; refresh the golden schema.
- [ ] **Step 4: Run** Step 2 command — Expected: PASS.
- [ ] **Step 5: Commit** `feat(mailbox): mail role table and repository`.

### Task 11: Mail role service, routes, and role-aware inbox

**Files:**
- Create: `api/internal/modules/mailbox/role_service.go`, `role_handler.go`, `role_test.go`
- Modify: `service.go` (`Service`, `roles` field by type assertion like `inbox`), `routes.go`, `dto.go` (`MailboxResponse.RoleID`, role DTOs), `error_mappings.go`, `api/internal/domain/error_codes.go`, `handover_service.go` (`MemberRemoved`), `handler_test.go`, `inbox_test.go`

**Interfaces:**
- Consumes: Task 10 repository; `authorizeMailbox(..., canAdminister)`, `currentMembership`, `s.recordAudit`, `accessRows`, `CountUnreadByMailboxIDs`.
- Produces:
  - `type MailRoleInput struct { Name string; Color domain.MailLabelColor; Description string; OwnerUserID *uint }` (empty color → `gray`).
  - `ListMailRoles(ctx, viewer) ([]*domain.MailRole, error)`; `CreateMailRole(ctx, viewer, input MailRoleInput) (*domain.MailRole, error)`; `UpdateMailRole(ctx, viewer, roleID uint, input MailRoleInput) (*domain.MailRole, error)`; `DeleteMailRole(ctx, viewer, roleID uint) error`; `AssignMailboxRole(ctx, viewer, mailboxID uint, roleID *uint) (*domain.Mailbox, error)` — all on `*service` and `Service`.
  - Visibility (D16): `mailbox_ids` = visible (`Access.Any()`) mailboxes with that role, ascending; non-managers only get roles with a non-empty list; an owner who is no longer a member reads as `null`.
  - Check order: create `400 → 403 → 422 → 422 limit / 409`; update `400 → 403 → 404 → 422 → 409`; assign `400 → 404 MAILBOX.NOT_FOUND → 403 → 422 role_id → 404 ROLE_NOT_FOUND`. Field errors use `ValidationError` keys `name`, `color`, `description`, `owner_user_id`, `role_id`, answered by `respondOrganizeError`; `ErrMailboxAccessInvalid` from `currentMembership` becomes the `owner_user_id` field error, not `MAILBOX.ACCESS_INVALID`. Bodies capped at 4 KiB with `http.MaxBytesReader`. Assignment body decodes into `map[string]json.RawMessage` so a missing `role_id` is `422`.
  - Assignment returns the mailbox with `Access`, `RoleID`, and `UnreadCount` (via `CountUnreadByMailboxIDs` when `Access.View`).
  - Audit via `s.recordAudit`: `{Action: create|update|delete, Resource: "mail_roles", TargetType: "mail_role", TargetID, Metadata: {"changed_fields": [...]}}` (no names); assignment `{Action: "update", Resource: "mailboxes.role", TargetType: "mailbox", Changes: {"role_id": before → after}}`. Logs `mail.role.created|updated|deleted|assigned` with ids only.
  - `MemberRemoved`: after its transaction, `ClearMailRoleOwner(organizationID, userID)`; an error logs `mail.role.owner_clear_failed` and is not returned.
  - Routes (§8.1): `GET|POST /mail-roles` (`mail_roles.index|store`), `PUT|DELETE /mail-roles/:role_id` (`mail_roles.update|destroy`, `WhereNumber`), `PUT /mailboxes/:mailbox_id/role-assignment` (`mailboxes.role_assignment.update`). List handler carries `// fastbuild:bounded-list max=50 reason=MaxMailRolesPerOrganization`.
  - `GET /v1/mailboxes` and every `MailboxResponse` carry `role_id`. The inbox role filter and count grouping (Tasks 3, 7) now see real `RoleID`s.

- [ ] **Step 1: Write failing tests** in `role_test.go` (Postgres access fixture with roles "Design desk" = {`b@`} and "Official" = {`team@`}): `TestMailRoleWriteNeedsOwnerOrAdmin` (B → 403, no row); `TestMailRoleVisibility` (C does not get "Design desk"; B gets it with `mailbox_ids` = [`b@`]; admin gets both plus an empty role); `TestMailRoleValidation` (whitespace name, 65 runes, 281-rune description, control character, color `black` → 422 with the field key; missing color → `gray`); `TestMailRoleOwnerMustBeMember` and `TestMailRoleOwnerHiddenAfterLeaving`; `TestMailRoleOwnerClearedWhenMemberRemoved`; `TestMailRoleCheckOrder` (member with a bad body → 403; admin, missing role, bad body → 404); `TestMailRoleAssignAccess` (B on `team@` → 403; C on `b@` → 404 `MAILBOX.NOT_FOUND`; missing `role_id` → 422; same value twice → 200 both); `TestMailRoleAuditRecordsIdsNotNames`; in `inbox_test.go`: `TestInboxRoleFilter` (`role_id` = Official → `team@` only; `none` → ungrouped; C with Design desk → empty 200) and `TestInboxCountsMoveWhenRoleDeleted` (UI-8: totals unchanged, `team@` unread moves to `ungrouped_unread`); handler `TestMailboxListCarriesRoleID`.
- [ ] **Step 2: Run** `cd api && go test ./internal/modules/mailbox/... -run 'MailRole|Inbox|MailboxListCarriesRoleID'` — Expected: FAIL.
- [ ] **Step 3: Implement**, then `cd api && make route-catalog-check`.
- [ ] **Step 4: Run** Step 2 command, `cd api && bash ../.agents/skills/verification-before-completion/scripts/run-tiers.sh 1 ./internal/modules/mailbox/...`, and `make contract-check` — Expected: PASS (route check now complete).
- [ ] **Step 5: Commit** `feat(mailbox): mail roles and role-aware unified inbox`.

### Task 12: Web roles — BFF, management dialog, sidebar groups, row tags

**Files:**
- Modify: `web/src/features/mailbox/server/mailbox-route.ts`, `mock-mailbox-store.ts`, `schemas.ts`, `types.ts`, `services/mailbox-service.ts`, `hooks/use-inbox.ts`, `components/inbox-sidebar.tsx`, `components/inbox-list.tsx`, `components/inbox-workspace.tsx`, `components/manage-mailboxes-dialog.tsx`, `utils/mailbox-error.ts`, i18n files
- Create: `web/src/app/api/mail-roles/route.ts`, `web/src/app/api/mail-roles/[roleId]/route.ts`, `web/src/app/api/mailboxes/[mailboxId]/role-assignment/route.ts`, `web/src/features/mailbox/components/manage-roles-dialog.tsx`
- Test: `web/src/test/mailbox-route.test.ts`, `mailbox-service.test.ts`, `mock-bff-parity/mailbox.ts`, `inbox-ui.test.tsx`

**Interfaces:**
- Consumes: Task 11 contract behavior.
- Produces:
  - Routes: `listMailRolesRoute(request)`, `createMailRoleRoute(request)`, `updateMailRoleRoute(request, rawRoleId)`, `deleteMailRoleRoute(request, rawRoleId)`, `setMailboxRoleRoute(request, rawMailboxId)`; bodies capped by `maxOrganizeBodyBytes`.
  - Mock: roles per organization with the API's rules (limit 50, case-insensitive names, palette, visibility, `role_id` on mailboxes, `ON DELETE SET NULL`, counts `roles` / `ungrouped_unread`, inbox `role_id` filter).
  - `MailRole`, `MailRoleInput` types; `mailboxService.listRoles`, `createRole`, `updateRole`, `deleteRole`, `assignRole(organizationId, mailboxId, roleId: number | null): Promise<Mailbox>`.
  - Hooks: `useMailRoles(organizationId)`, `useCreateMailRole`, `useUpdateMailRole`, `useDeleteMailRole`, `useAssignMailboxRole`; each write invalidates `mailboxKeys.roles`, `mailboxKeys.list`, and the inbox counts prefix.
  - `ManageRolesDialog({ open, onOpenChange })` — owners/admins only: create, rename, color, description, owner, delete with confirmation "its mailboxes become ungrouped"; `409` under the name field; `422 MAILBOX.ROLE_LIMIT_REACHED` as a form error. `manage-mailboxes-dialog.tsx` gains a role select per mailbox.
  - Sidebar "Roles & mailboxes": roles in API order with color dot and unread; expanding lists the role's mailboxes; readable ones show unread and narrow (`?box=`), unreadable ones show a lock icon, no number, not clickable; "Ungrouped" last; "Manage roles" beside the heading for owners/admins. `?role=<id>|none` narrows the list; rows show the role tag unless narrowed to a role or box.
  - `utils/mailbox-error.ts` maps the three role codes to `errors.roleNotFound|roleNameTaken|roleLimitReached`.

- [ ] **Step 1: Write failing tests:** route — `mock role writes: member 403, duplicate 409, 51st 422, delete ungroups`, `proxy role routes use the right upstream method and path`, `role writes reject cross-origin requests`; UI — `sidebar groups mailboxes by role with Ungrouped last`, `an admin sees unreadable mailboxes locked without a number`, `a member does not see a role without a visible mailbox`, `Manage roles is only offered to owners and admins`, `a taken role name shows under the name field`, `narrowing to a role hides row role tags`.
- [ ] **Step 2: Run** `cd web && corepack pnpm vitest run src/test/mailbox-route.test.ts src/test/mailbox-service.test.ts src/test/mock-bff-service-parity.test.ts src/test/inbox-ui.test.tsx src/test/mock-bff-route-contract.test.ts` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** Step 2 command, `corepack pnpm type-check && corepack pnpm lint` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(web): mail roles in the unified inbox`.

## Slice S4 — v2 look and interaction

### Task 13: v2 tokens and self-hosted fonts across the Web app

**Files:**
- Modify: `web/src/themes/primitives.css`, `web/src/themes/light.css`, `web/src/themes/dark.css`, `web/src/app/globals.css` (`@theme inline` colors, `--font-sans`, `--font-serif`), `web/src/app/layout.tsx`, `web/scripts/check-theme-contrast.mjs`

**Interfaces:**
- Produces:
  - Semantic tokens with the §11.10 values (light / dark), legacy shadcn names mapped as the table says; `--primary-foreground` `#FFFFFF` / `#12110E`; light `--text-faint` `#8E8A81` and light `--bg-subtle` `#FBFAF7` (see Resolved while planning).
  - New tokens and Tailwind colors: `--text-faint` (`text-text-faint`), `--row-hover`, `--row-selected`, `--nav-hover`, `--nav-selected`, `--avatar-bg` (`bg-*` utilities via `--color-*`).
  - Fonts (D21) in `layout.tsx`: `Inter({ subsets: ['latin'], weight: ['400','500','600'], variable: '--font-inter', display: 'swap' })`, `Newsreader({ subsets: ['latin'], axes: ['opsz'], variable: '--font-newsreader', display: 'swap' })`, `Noto_Serif_SC({ weight: '600', preload: false, variable: '--font-noto-serif-sc', display: 'swap' })` on `<html>`. `--font-sans: var(--font-inter), 'PingFang SC', 'Noto Sans SC', ui-sans-serif, system-ui, sans-serif`; `--font-serif: var(--font-newsreader), var(--font-noto-serif-sc), 'Songti SC', 'STSong', Georgia, serif` (Tailwind `font-serif`).
  - Contrast guard adds a 3:1 tier: `text-faint` on `bg-canvas` and `bg-surface`, both modes.

- [ ] **Step 1: Add** the 3:1 `text-faint` pairs to `check-theme-contrast.mjs`.
- [ ] **Step 2: Run** `cd web && corepack pnpm lint:theme-contrast` — Expected: FAIL (`text-faint` undefined).
- [ ] **Step 3: Implement** tokens and fonts. Where a checked pair fails, change only the lightness of the failing token and name each change in the commit body.
- [ ] **Step 4: Run** `cd web && corepack pnpm lint && corepack pnpm type-check && corepack pnpm build` — Expected: PASS (route budgets included). `rg -n '#[0-9A-Fa-f]{3,8}\b' src/features src/components --glob '*.tsx'` — Expected: no new hits. The controller screenshots login, site home, each console page, and the mail page in light and dark at 1440×900 in the built-in browser and records anything that reads as broken.
- [ ] **Step 5: Commit** `feat(web): v2 design tokens and self-hosted fonts`.

### Task 14: v2 inbox layout, quota card, responsive behavior

**Files:**
- Create: `web/src/components/ui/kbd.tsx`, `web/src/features/mailbox/components/send-quota-card.tsx`
- Modify: `web/src/features/mailbox/components/inbox-workspace.tsx`, `inbox-sidebar.tsx`, `inbox-list.tsx`, `inbox-reading-pane.tsx`, i18n files
- Test: `web/src/test/inbox-ui.test.tsx`

**Interfaces:**
- Consumes: Task 13 tokens; `useMailStats(organizationId, 1)`, `useMailboxDomains`, `useInboxCounts`.
- Produces:
  - `Kbd({ children })` — §11.0 key cap (min 18×18, 1px border, 2px bottom, radius 5px, 10.5px).
  - Layout per §11.0: sidebar 248px (header with logo, organization name, `⌘K` cap; compose button 36px; nav rows 32px; group heading; role dots 7px; mailbox rows 30px with an empty right badge slot), list panel 470px (serif 28px title, subtitle "N mailboxes · M roles", tab bar, rows radius 10px with hover `E` `#` caps), reading pane flexible with a 52px toolbar (Reply R, Reply all A, Forward F | Archive E, Delete #, only with `send`) and a 640px `article` (serif 30px subject, "To <mailbox_address> · <role name>" line, 34px avatar, body 15.5px/1.8 in the existing sandboxed frame). Sidebar footer links: Mail, Broadcasts, Contacts, Manage (existing console routes) and Shortcuts. Fixed prototype colors map to `--nav-*`, `--row-*`, `--avatar-bg`.
  - `SendQuotaCard({ canManage })` (D24): title "Sent today"; "Mailboxes you can see: sent N · failed M (UTC day)" from stats; owners/admins also "Organization limit L / rolling 24 hours" from `daily_send_limit`; when `sent_waiting > 0`, "Limit reached, queued" linking to `?view=sent`. No progress bar, no breakdown, no count of queued mail.
  - Responsive (§11.11): ≥ 1280px three panes; 1024–1279px icon rail; < 1024px list or reading pane with a back button; < 768px sidebar in a `Sheet`. List `role="listbox"`, rows `role="option"` with `aria-selected`; transitions off under `prefers-reduced-motion`.

- [ ] **Step 1: Write failing tests:** `quota card shows own stats to members and the organization limit only to admins`, `quota card shows queued and links to sent when sent_waiting is positive`, `reading pane toolbar hides send actions without send`, `rows expose listbox and option roles with aria-selected`, `narrow layout shows a back button from the reading pane`.
- [ ] **Step 2: Run** `cd web && corepack pnpm vitest run src/test/inbox-ui.test.tsx` — Expected: FAIL.
- [ ] **Step 3: Implement** against §11.0, checking `docs/design/v2/MainV2.dc.html` side by side; parts §1.4 defers (AI tabs and reasons, code mask, "Later H", source badges, quota breakdown) are not rendered.
- [ ] **Step 4: Run** Step 2 command, `corepack pnpm type-check && corepack pnpm lint` — Expected: PASS. The controller screenshots the mail page at 1440×900 (light and dark) next to the prototype and checks column widths, radii, row structure, sidebar and quota card position (UI-11).
- [ ] **Step 5: Commit** `feat(web): v2 three-pane inbox and send quota card`.

### Task 15: Keyboard shortcuts, help, and the compose sender

**Files:**
- Create: `web/src/features/mailbox/hooks/use-mail-shortcuts.ts`, `web/src/features/mailbox/components/shortcut-help-dialog.tsx`
- Modify: `web/src/features/mailbox/components/inbox-workspace.tsx`, `inbox-list.tsx`, `inbox-reading-pane.tsx`, `hooks/use-inbox.ts`, i18n files
- Test: `web/src/test/inbox-ui.test.tsx`

**Interfaces:**
- Consumes: Task 5 actions, existing `ComposeDialog` (`fromOptions`, `quotedReplyDraft`, `replyAllDraft`, `forwardDraft`), `useUpdateInboxMessages`.
- Produces:
  - `useMailShortcuts(bindings: Partial<Record<MailShortcut, () => void>>, options: { enabled: boolean })` where `MailShortcut = 'next' | 'previous' | 'open' | 'archive' | 'trash' | 'star' | 'reply' | 'replyAll' | 'forward' | 'compose' | 'search' | 'help' | 'palette' | 'escape'`, keys per §11.7 (`j k Enter e # s r a f c / ? ⌘K/Ctrl+K Esc`, arrow keys alias `j`/`k`). Single keys are ignored in `input`, `textarea`, `select`, `contenteditable`, during `event.isComposing`, and while a modal is open; `Esc` and `⌘K` always work; other modifier combos pass through (D29).
  - Actions needing `send` (`e # r a f`) on a mailbox without it send nothing and toast `inbox.noSendPermission`; `c` needs at least one mailbox with `send`.
  - `useUpdateInboxMessage(organizationId)` for `s` (and the pane's star button): optimistic star in the infinite-query cache, rollback and toast on error.
  - `j` on the last loaded row with `hasNextPage` fetches the next page, then selects its first row.
  - Compose from the inbox (D26): `fromOptions` = mailboxes with `send`; default = narrowed box, then the stored mailbox (`zmail.mailbox.<org>`), then the first. Reply / reply all / forward keep the row's mailbox.
  - `ShortcutHelpDialog` opened by `?` and a "Shortcuts" link in the sidebar footer.

- [ ] **Step 1: Write failing tests:** `j j e archives the second row`, `r opens reply from the row's mailbox`, `e on a view-only mailbox sends nothing and toasts` (UI-7), `single keys are ignored in the search box and during IME composition`, `Escape clears the search box`, `s stars optimistically and rolls back on error`, `j past the last loaded row loads and selects the next page` (Review Focus 5), `c defaults the sender to the narrowed mailbox`, `? opens the shortcut help`.
- [ ] **Step 2: Run** `cd web && corepack pnpm vitest run src/test/inbox-ui.test.tsx` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** Step 2 command, `corepack pnpm type-check && corepack pnpm lint` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(web): inbox keyboard shortcuts and compose sender`.

### Task 16: ⌘K command palette

**Files:**
- Create: `web/src/features/mailbox/components/command-palette.tsx`
- Modify: `web/src/features/mailbox/components/inbox-workspace.tsx`, i18n files
- Test: `web/src/test/inbox-ui.test.tsx`

**Interfaces:**
- Consumes: `components/ui/dialog.tsx`, `useMailboxes`, `useMailRoles`, `mailboxService.listInbox`, `next-themes` `useTheme`, Task 15 `palette` binding.
- Produces: `CommandPalette({ open, onOpenChange, canManage, onAction })` — §11.0 geometry; `role="combobox"` input with `aria-activedescendant`, `role="listbox"` results; four groups of ≤ 5: mailboxes (prefix then substring match on visible addresses; readable → `?box=`, unreadable shown locked and not selectable), roles (`?role=`), mail (250 ms after typing stops, `mailboxService.listInbox(organizationId, { view: 'all', q, tz }, undefined, 5)`; `MAILBOX.SEARCH_TOO_BROAD` → "Enter a longer keyword" in that group; selecting opens the message), actions (compose, four views, light/dark, shortcut help; owners/admins also Manage mailboxes, Manage roles). Arrow keys move, `Enter` selects, `Esc` closes.

- [ ] **Step 1: Write failing tests:** `⌘K and Ctrl+K open the palette`, `mail search waits 250 ms and asks for five results`, `too-broad search shows the longer-keyword notice in the mail group`, `an unreadable mailbox is listed locked and cannot be selected`, `C never sees b@ mail in palette results` (mock responses from the C viewer), `arrow keys and Enter run the highlighted action`, `management actions only for owners and admins`.
- [ ] **Step 2: Run** `cd web && corepack pnpm vitest run src/test/inbox-ui.test.tsx` — Expected: FAIL.
- [ ] **Step 3: Implement** without new dependencies.
- [ ] **Step 4: Run** Step 2 command, `corepack pnpm type-check && corepack pnpm lint` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(web): mail command palette`.

### Task 17: Acceptance flow, upgrade notes, and final checks

**Files:**
- Create: `api/tests/kest/unified_inbox.flow.md`
- Modify: `docs/plans/unified-inbox.zh-CN.md` (status line), `api/docs/OBSERVABILITY.md` (the §13.2 alerts: `inbox_messages.index` and `inbox_message_counts.index` p95 > 500 ms for 15 minutes; `MAILBOX.SEARCH_TOO_BROAD` > 20% of inbox searches for 1 hour), `contracts/MAILBOXES.md` if acceptance finds drift

**Interfaces:**
- Consumes: everything above; `api/tests/kest/mailbox_organize.flow.md` as the format model.

- [ ] **Step 1: Write** the flow on the §16 organization (admin A; B, C; `b@`, `c@` personal; `team@` shared, B send, C view; roles "Design desk" = {`b@`}, "Official" = {`team@`}): UI-1 to UI-3 and UI-9 as API assertions (C's list, counts, `q`, `mailbox_id=<b@>`, `role_id=<Design desk>` return no `b@` mail and no error; A gets empty pages and zero counts; B creating a role → 403), UI-4 and UI-5 (today and pending sets), UI-8 (delete Official → `team@` ungrouped, totals unchanged), assignment idempotence.
- [ ] **Step 2: Run** `cd api && make test-kest` against a local API and disposable PostgreSQL — Expected: every step passes. No real mail is sent.
- [ ] **Step 3: Run** the spec §14.4 commands once: `cd api && go test ./internal/modules/mailbox/... -run 'Inbox|MailRole'`, `go test ./database/migrations/... -run 'MailRoles|MailInboxIndex|SchemaGolden'`, `make route-catalog-check`, `make contract-check`, `cd web && corepack pnpm vitest run src/test/inbox-ui.test.tsx src/test/mailbox-route.test.ts src/test/mailbox-service.test.ts src/test/mock-bff-route-contract.test.ts && corepack pnpm type-check && corepack pnpm lint` — Expected: PASS. Do not run `make check` (release gate only).
- [ ] **Step 4:** Set the spec status to implemented, then `make agent-check-changed` — Expected: PASS.
- [ ] **Step 5: Commit** `docs(mailbox): unified inbox acceptance flow`.
