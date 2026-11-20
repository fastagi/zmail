# Shared Mailbox Administration (Phase 1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give organization owners and admins an admin area in `web/` with a shared-mailbox page (directory, search, cursor pages, create, rename, members, delete, CSV import with preview, CSV export), backed by a configurable per-organization mailbox limit.

**Architecture:** The API adds an owner/admin-only directory (`GET /v1/mailbox-directory`, keyset pages, member and 24-hour send counts aggregated for the page's ids only), a display-name update, a server-side import whose dry run and real run share one per-row validation path, and a server-generated CSV export. `GET /v1/mailboxes` gains `scope=usable` for the workspace. The web admin area lives under `/console/admin` inside the existing console shell, gated client-side by the organization role (the API stays the authority), and absorbs the domain pages from the self-serve-domains branch.

**Tech Stack:** Go (Gin, Wire, GORM), PostgreSQL, OpenAPI contracts (redocly, oasdiff), Next.js 16 BFF + React 19, TanStack Query, next-intl, Vitest.

**Spec:** `docs/plans/shared-mailbox-admin.zh-CN.md` (approved). Start from `main` once PR #22, PR #23 and `feature/self-serve-domains` are merged; if they are not, stack on `feature/mailbox-organize` and rebase after self-serve-domains merges. Task 8 is written against the self-serve-domains web layout (`console/mail-domain/page.tsx`, `console/mail-domain/[zoneId]/dns/page.tsx`).

## Global Constraints

- PostgreSQL only; migrations are frozen SQL via `execStatements`, each with `Down`.
- English source and API text; user copy only in `web/src/i18n/modules/{mailbox,nav,console}/{en-US,zh-Hans}.ts`, both locales. Brand `Zmail`.
- Contract first: `contracts/openapi.yaml` and `contracts/MAILBOXES.md` change before code; `make contract-check` passes (no oasdiff breaking change); `cd contracts && corepack pnpm generate` keeps `web/` and `admin/` types in sync. The `admin/` app is the platform operator console and is otherwise untouched.
- The admin area and every new endpoint are for organization `owner` and `admin` only. Web: anyone else gets the console 404. API: anyone else gets `403 PERMISSION.DENIED`.
- `MAILBOX_MAX_PER_ORGANIZATION`: default `50`, valid `1`–`5000`; anything else fails startup with `MAILBOX_MAX_PER_ORGANIZATION must be between 1 and 5000`. List queries are bounded by the ceiling `5000`, never by the configured value.
- Lowering the limit below an organization's current count changes no existing mailbox; only new creates and import rows get `MAILBOX.LIMIT_REACHED`.
- `sent_last_24h` (per mailbox and organization) is the predicate of `CountOutboundSince`: outbound messages with `send_attempted_at` in the last rolling 24 hours, failed and broadcast sends included. UI label "24-hour send attempts" / "24 小时发信次数".
- Admin surfaces never carry message content. No new JSON endpoint returns member email addresses; only the CSV export contains them.
- Import: 1–500 rows, shared mailboxes only, members matched by account email on the server, row validation identical to `POST /v1/mailboxes`, each created row in its own transaction, no organization invitations sent.
- No new web dependency for CSV: an in-house RFC 4180 parser and writer.
- Never send real mail from tests.

## Review Focus

1. Lowering `MAILBOX_MAX_PER_ORGANIZATION` below the current count must not hide any existing mailbox from `GET /v1/mailboxes`, `scope=usable`, the directory or member rows (bounds use the 5000 ceiling). Test in Task 2.
2. A directory search containing `%`, `_` or `\` matches those characters literally. Test in Task 3.
3. The same address twice in one file with different case (`Sales@…`, `sales@…`): the second row is `MAILBOX.IMPORT_DUPLICATE_ROW` in both dry run and real run, and exactly one mailbox is created. Test in Task 4.
4. A display name starting with `=`, `+`, `-` or `@` is exported as a text cell (`'` prefix), and re-importing that file restores the original name. Tests in Tasks 4 and 5.
5. A file saved by Excel (UTF-8 BOM, CRLF, quoted field holding `;`, a newline and `""`) parses into the same rows as a plain file. Test in Task 7.

---

### Task 1: Contract

**Files:**
- Modify: `contracts/openapi.yaml`, `contracts/MAILBOXES.md`, `api/internal/domain/error_codes.go`, `web/src/http/codes.ts`, `admin/src/http/codes.ts`
- Generate: `web/src/http/generated/openapi.ts`, `admin/src/http/generated/openapi.ts`

**Interfaces:**
- Produces:
  - `GET /v1/mailboxes` adds optional query `scope` (enum `usable`; any other value → `422 COMMON.VALIDATION_FAILED`). `usable` = mailboxes the caller can view or send, plus, for owners and admins, personal mailboxes with `awaiting_handover: true`. Without `scope` the behavior is unchanged: every mailbox the caller has any access to, unpaginated; the table row reads "at most 5000 items".
  - `GET /v1/mailbox-directory` (`listMailboxDirectory`): query `type` (`shared`|`personal`, optional, omitted = both), `q` (1–120 characters after trimming; case-insensitive substring of `address` or `display_name`; `%`, `_`, `\` literal), `cursor`, `per_page` (default 15, max 100). Newest first. `page` or a malformed `cursor` → `400 COMMON.INVALID_INPUT`. Items `MailboxDirectoryEntry { id, address, display_name, type, owner_user_id, awaiting_handover, created_at, member_count, sent_last_24h }` (no `unread_count`, no `access`). `meta` = `MailboxDirectoryMeta { per_page, has_more, next_cursor, daily_send_limit, sent_last_24h, mailbox_count, max_mailboxes }`, the last four organization-wide.
  - `PUT /v1/mailboxes/{mailbox_id}` (`updateMailbox`): body `{ display_name: string }` (required; `""` clears) → `200` `Mailbox`. Same display-name rules as create → `422 MAILBOX.ADDRESS_INVALID`; `404 MAILBOX.NOT_FOUND`; `403`.
  - `POST /v1/mailbox-imports` (`importMailboxes`): body `{ dry_run: boolean (required), rows: [{ address: string ≤320, display_name?: string ≤480, members: string ≤8192 }] (1–500) }` → `200 MailboxImportResult { dry_run, succeeded, failed, rows: [{ row (1-based index into rows), address, status: valid|created|failed, mailbox_id: integer|null, error_code: string|null, missing_emails: string[] }] }`. Body violations → `422 COMMON.VALIDATION_FAILED`; `403`; `413`.
  - Members cell grammar: `email[:permission]` joined by `;`; segments trimmed; empty segments ignored; permission `view`|`send`|`manage`, default `send`; email compared ignoring case. One leading `'` is removed from `address`, `display_name` and `members` when the next character is `=`, `+`, `-` or `@`.
  - Row error codes: `MAILBOX.ADDRESS_INVALID`, `MAILBOX.ADDRESS_RESERVED`, `MAILBOX.ADDRESS_TAKEN`, `MAILBOX.IMPORT_DUPLICATE_ROW` (new: an earlier row has the same address), `MAILBOX.DOMAIN_NOT_FOUND`, `MAILBOX.DOMAIN_NOT_VERIFIED`, `MAILBOX.ACCESS_INVALID` (bad grammar, 0 or more than 100 members, unknown permission, member listed twice), `MAILBOX.IMPORT_MEMBER_NOT_FOUND` (new: emails in `missing_emails` are not members of the organization), `MAILBOX.LIMIT_REACHED` (counted cumulatively in row order), `MAILBOX.DOMAIN_PROVIDER_FAILED`, `COMMON.INTERNAL`.
  - `GET /v1/mailbox-exports?type=shared|personal` (`exportMailboxes`, `type` required → else `422 COMMON.VALIDATION_FAILED`): `200 text/csv; charset=utf-8`, `Content-Disposition: attachment; filename="shared-mailboxes-YYYYMMDD.csv"` (UTC date; `personal-mailboxes-…` for personal), UTF-8 BOM, header `address,display_name,members,member_count,sent_last_24h,created_at`; `members` as `email:permission` joined by `;`, ordered by email; `created_at` RFC 3339 UTC; cells starting with `=`, `+`, `-`, `@` get a `'` prefix. `403`.
  - Access event type `renamed`: subject `null`, detail `{ "from", "to" }`; an unchanged name writes nothing. The `detail` sentence in MAILBOXES.md allows display names.
  - `max_mailboxes` and the "at most 50 mailboxes" sentence now name `MAILBOX_MAX_PER_ORGANIZATION` (default 50, 1–5000) and the lowering rule.
  - Codes: Go `CodeMailboxImportDuplicateRow = "MAILBOX.IMPORT_DUPLICATE_ROW"`, `CodeMailboxImportMemberNotFound = "MAILBOX.IMPORT_MEMBER_NOT_FOUND"`; TS `MAILBOX_IMPORT_DUPLICATE_ROW`, `MAILBOX_IMPORT_MEMBER_NOT_FOUND` in both `codes.ts`.

- [ ] **Step 1:** Write the above in `openapi.yaml` (paths, `MailboxDirectoryEntry`, `MailboxDirectoryMeta`, `UpdateMailboxRequest`, `MailboxImportRequest`, `MailboxImportResult`, the `renamed` enum value) and `MAILBOXES.md` (path table rows, a "Mailbox Directory" section, "Import And Export" section, the limit and `scope` rules, the access event row). Add the codes to the three code files.
- [ ] **Step 2: Run** `make contract-check` — Expected: PASS, oasdiff reports no breaking change.
- [ ] **Step 3: Run** `cd contracts && corepack pnpm generate`, then `cd web && corepack pnpm type-check` and `cd admin && corepack pnpm type-check` — Expected: PASS.
- [ ] **Step 4: Commit** `feat(contracts): mailbox directory, rename, import, export, and usable scope`.

### Task 2: Configurable mailbox limit and `scope=usable`

**Files:**
- Modify: `api/internal/infra/config/config.go`, `api/internal/infra/config/validate_test.go`, `api/docs/CONFIGURATION.md`, `api/.env.example`, `deploy/self-host/compose.yaml`
- Modify: `api/internal/domain/mailbox.go`, `api/internal/modules/mailbox/service.go`, `access_service.go`, `repository.go`, `access_repository.go`, `seal_repository.go`, `handler.go`, `dto.go`, `service_test.go` (fake repository)
- Create: `api/internal/modules/mailbox/limit_test.go`
- Test: `api/internal/modules/mailbox/handler_test.go`

**Interfaces:**
- Produces:
  - `config.DefaultMailboxMaxPerOrganization = 50`, `config.MailboxMaxPerOrganizationCeiling = 5000`, `MailboxConfig.MaxPerOrganization int` from `MAILBOX_MAX_PER_ORGANIZATION`.
  - `domain.MaxMailboxesPerOrganization` is removed; `domain.MailboxesPerOrganizationCeiling = 5000` bounds `ListMailboxes`, `ListUsableMailboxes`, `ListMembershipMailboxMembers` and `MailboxesGrantedTo`.
  - `mailbox.Settings.MaxMailboxes int` (filled by `SettingsFromConfig`, default 50); `Service.MaxMailboxes() int`; `CreateMailbox` passes `s.settings.MaxMailboxes` to `repo.CreateMailbox`; `ListDomains` reports it as `max_mailboxes`.
  - `type MailboxListScope string`; `MailboxListScopeAll MailboxListScope = ""`, `MailboxListScopeUsable MailboxListScope = "usable"`; `Service.ListMailboxes(ctx, viewer domain.OrganizationContext, scope MailboxListScope) ([]*domain.Mailbox, error)`.
  - `domain.MailboxRepository.ListUsableMailboxes(ctx, organizationID, membershipID, userID uint) ([]*Mailbox, error)`: live mailboxes that are personal and owned by `userID`, or have a member row for `membershipID`, plus (service-side, managers only) personal mailboxes awaiting handover; unread counts filled as in `ListMailboxes` (extract `fillUnreadCounts(ctx, mailboxes []*domain.Mailbox, readerMembershipID uint) error`). The service keeps only `View || Send`, or awaiting-handover personal mailboxes for managers.
  - `ListMailboxesRequest { Scope string \`form:"scope" binding:"omitempty,oneof=usable"\` }`; `ListMailboxes` marker becomes `// fastbuild:bounded-list max=5000 reason=an organization owns at most domain.MailboxesPerOrganizationCeiling mailboxes`.

- [ ] **Step 1: Write failing tests:**
  - `TestMailboxMaxPerOrganization` (config): unset → `50`; `"5000"` → `5000`; `"0"` and `"5001"` → error text `MAILBOX_MAX_PER_ORGANIZATION must be between 1 and 5000`.
  - `TestMailboxCeilingMatchesConfig`: `domain.MailboxesPerOrganizationCeiling == config.MailboxMaxPerOrganizationCeiling`.
  - `TestCreateUsesConfiguredLimit`: `MaxMailboxes: 3`, fourth create → `ErrMailboxLimitReached`.
  - `TestLoweredLimitKeepsExistingMailboxes` (PostgreSQL, Review Focus 1): three mailboxes created under limit 3; a service with `MaxMailboxes: 2` lists 3 for the admin (all scope), member B still gets `team@` in usable scope, `CreateMailbox` → `ErrMailboxLimitReached`, `DeleteMailbox` succeeds.
  - `TestUnscopedListReturnsMoreThan50` (PostgreSQL): `MaxMailboxes: 60`, admin creates 60 shared mailboxes, all-scope list has 60.
  - `TestMembershipRowsBeyond50` (PostgreSQL): one membership on 55 mailboxes → `ListMembershipMailboxMembers` and `MailboxesGrantedTo` return 55.
  - `TestUsableScope` (fake and PostgreSQL): admin (non-member) → `[]`; B → `[b@, team@]`; C → `[team@]`; D → `[]`; admin all scope → 2 items.
  - `TestUsableScopeKeepsAwaitingHandoverForAdmins`: after B leaves the organization, admin usable list = `[b@]` with `awaiting_handover: true`; C's usable list does not contain `b@`.
  - Handler `TestListMailboxesScopeParam`: `?scope=usable` reaches the service as `MailboxListScopeUsable`; `?scope=all` → `422 COMMON.VALIDATION_FAILED`. `TestListDomainsReportsConfiguredMax`: settings `MaxMailboxes: 75` → every item `max_mailboxes: 75`.
- [ ] **Step 2: Run** `cd api && go test ./internal/infra/config/... ./internal/modules/mailbox/... -run 'MaxPerOrganization|Ceiling|Limit|Beyond50|MoreThan50|Usable|ScopeParam|ConfiguredMax'` — Expected: FAIL.
- [ ] **Step 3: Implement** the interfaces; document the variable in `CONFIGURATION.md`, `.env.example` and the self-host compose file next to `MAILBOX_DAILY_SEND_LIMIT`.
- [ ] **Step 4: Run** Step 2 command, then `cd api && bash ../.agents/skills/verification-before-completion/scripts/run-tiers.sh 1 ./internal/modules/mailbox/...` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(mailbox): configurable mailbox limit and usable list scope`.

### Task 3: Directory listing and display-name update

**Files:**
- Create: `api/internal/domain/mailbox_directory.go`, `api/internal/modules/mailbox/directory_repository.go`, `directory_service.go`, `directory_handler.go`, `directory_cursor.go`, `directory_test.go`, `directory_bench_test.go`
- Create: `api/database/migrations/2026_11_22_090000_add_mailbox_directory_index.go`
- Modify: `api/database/migrations/testdata/schema.golden.sql`, `api/internal/modules/mailbox/provider.go` (migration name, Wire), `service.go` (interface, `NewService` gains `directory domain.MailboxDirectoryRepository` after `organize`), `access_service.go`, `repository.go`, `routes.go`, `dto.go`, `api/internal/domain/mailbox.go`, `api/internal/domain/mailbox_access.go`, `api/pkg/response/cursor.go`, `api/internal/wiring/wire_gen.go` (generated), `api/internal/modules/README.md` (generated)
- Test: `api/pkg/response/cursor_test.go`, `api/internal/modules/mailbox/handler_test.go`, `access_test.go`

**Interfaces:**
- Consumes: Task 2 `Settings.MaxMailboxes`, `domain.MailboxesPerOrganizationCeiling`.
- Produces:
  - `domain.MailboxDirectoryFilter { Type MailboxType; Query string; BeforeID uint; Limit int }`; `domain.MailboxDirectoryEntry { Mailbox *Mailbox; MemberCount int; SentLast24h int64 }`.
  - `domain.MailboxDirectoryRepository { ListMailboxDirectory(ctx, organizationID uint, filter MailboxDirectoryFilter, sentSince time.Time) ([]*MailboxDirectoryEntry, error); CountMailboxes(ctx, organizationID uint) (int64, error) }` (Task 5 adds one method).
  - `Service.MailboxDirectory(ctx, viewer, filter domain.MailboxDirectoryFilter) (*MailboxDirectoryPage, error)`; `MailboxDirectoryPage { Entries []*domain.MailboxDirectoryEntry; NextBeforeID uint; MailboxCount int64; SentLast24h int64 }`. The service asks for `Limit+1` rows; `NextBeforeID` is the last returned id when there were more.
  - `encodeDirectoryCursor(id uint) string` (base64url of the decimal id) and `decodeDirectoryCursor(token string) (uint, error)` wrapping `domain.ErrInvalidInput`.
  - `response.SuccessCursorPageWithMeta(c *gin.Context, items any, perPage int, nextCursor string, extra map[string]any)`: `meta` = `per_page`, `has_more`, `next_cursor` plus `extra`; the three paging keys win over `extra`.
  - `domain.MailboxAccessRenamed MailboxAccessEventType = "renamed"`; `domain.MailboxRepository.UpdateMailboxDisplayName(ctx, organizationID, id uint, displayName string) error`; `normalizeDisplayName(raw string) (string, error)` extracted from `CreateMailbox`; `Service.RenameMailbox(ctx, viewer, mailboxID uint, displayName string) (*domain.Mailbox, error)` — manager only, writes the name and the `renamed` event in one transaction, unchanged name writes nothing.
  - Routes `auth.GET("/mailbox-directory", h.ListMailboxDirectory).Name("mailbox_directory.index")`, `auth.PUT("/mailboxes/:mailbox_id", h.UpdateMailbox).Name("mailboxes.update").WhereNumber("mailbox_id")`; DTOs `ListMailboxDirectoryRequest { Type \`oneof=shared personal\`; Q \`max=120\` }`, `MailboxDirectoryEntryResponse`, `UpdateMailboxRequest { DisplayName *string \`binding:"required"\` }`.
- Query shape: page first (`WHERE organization_id = ? AND deleted_at IS NULL [AND type = ?] [AND (address ILIKE ? ESCAPE '\' OR display_name ILIKE ? ESCAPE '\')] [AND id < ?] ORDER BY id DESC LIMIT ?`, with `escapeLike`), then grouped counts over the page's ids only: `mailbox_members` by `mailbox_id`, and `mail_messages` with `direction = 'outbound' AND send_attempted_at >= ?` by `mailbox_id`. Migration adds `idx_mail_messages_mailbox_send_attempted ON mail_messages (mailbox_id, send_attempted_at) WHERE direction = 'outbound'`.

- [ ] **Step 1: Write failing tests:**
  - `TestSuccessCursorPageWithMetaKeepsPagingKeys`: `extra {"per_page": 1, "sent_last_24h": 37}` with perPage 15 → `meta.per_page == 15`, `meta.sent_last_24h == 37`.
  - `TestDirectoryCursorRoundTrip`: `decodeDirectoryCursor(encodeDirectoryCursor(42)) == 42`; `"%%"` and a 129-character token → `domain.ErrInvalidInput`.
  - `TestDirectoryCounts` (PostgreSQL): `team@` with 2 members, 3 outbound attempts in the last 24 h (one failed, one from a broadcast) and 1 attempt 25 h ago → `MemberCount 2`, `SentLast24h 3`; page `SentLast24h` equals `CountOutboundSince`; a deleted mailbox's attempts count in the organization total but have no row.
  - `TestDirectoryPagesNewestFirst` (PostgreSQL): 5 mailboxes, `Limit 2` → ids `[5,4]`, `[3,2]`, `[1]`, last page `NextBeforeID 0`.
  - `TestDirectorySearch` (PostgreSQL): `q "SAL"` finds `sales@` and display name `Wholesale`; `type shared` excludes `b@`.
  - `TestDirectorySearchEscapesWildcards` (PostgreSQL, Review Focus 2): display names `50% off` and `500 club`; `q "50%"` returns only the first; `q "a_b"` does not match `axb@`.
  - `TestDirectoryRequiresManager`: member viewer → `domain.ErrPermissionDenied`.
  - `TestRenameMailbox`: admin renames `team@` to `Support` → `display_name "Support"`, one `renamed` event `{ "from": "", "to": "Support" }`; same name again → no new event; `"<x>"` → `ErrMailboxAddressInvalid`; member viewer → `domain.ErrPermissionDenied`; renaming gives the non-member admin no `View`.
  - Handler `TestListMailboxDirectoryHandler`: item JSON keys are exactly `id, address, display_name, type, owner_user_id, awaiting_handover, created_at, member_count, sent_last_24h`; `meta` has `daily_send_limit`, `sent_last_24h`, `mailbox_count`, `max_mailboxes`; `?page=2` → `400 COMMON.INVALID_INPUT`; `?q=` 121 characters → `422 COMMON.VALIDATION_FAILED`. `TestUpdateMailboxHandler`: missing `display_name` → `422 COMMON.VALIDATION_FAILED`; success `200`.
  - `TestMailboxDirectoryIndexMigrationUpDown`: up then down leaves the golden schema.
- [ ] **Step 2: Run** `cd api && go test ./pkg/response/... ./database/migrations/... ./internal/modules/mailbox/... -run 'CursorPage|DirectoryCursor|Directory|Rename|UpdateMailbox|DirectoryIndex'` — Expected: FAIL.
- [ ] **Step 3: Implement**, then `cd api && make wire && make route-catalog-check && make starter-catalog`.
- [ ] **Step 4: Run** Step 2 command — Expected: PASS. Then `cd api && go test ./internal/modules/mailbox/ -run '^$' -bench BenchmarkMailboxDirectory1000 -benchtime=50x`: seeds 1,000 shared mailboxes × 5 members and 20,000 outbound attempts in the last 24 h, measures the first page (`per_page` 15) without and with `q "sales"`. Record p95 in the commit body; budget < 50 ms and < 100 ms. Over budget → attach `EXPLAIN ANALYZE` and fix the query or index.
- [ ] **Step 5: Commit** `feat(mailbox): admin mailbox directory and display-name update`.

### Task 4: Import

**Files:**
- Modify: `api/internal/domain/organization.go`, `api/internal/modules/organization/membership_repository.go`, `membership_repository_test.go`
- Modify: `api/internal/modules/mailbox/access_service.go` (`membershipDirectory`, plan/commit split), `errors.go`, `error_mappings.go`, `dto.go`, `routes.go`, `service.go`, `service_test.go` (`fakeMemberships` emails)
- Create: `api/internal/modules/mailbox/import_service.go`, `import_handler.go`, `import_test.go`

**Interfaces:**
- Consumes: Task 2 `Settings.MaxMailboxes`; Task 3 `normalizeDisplayName`, `CountMailboxes`.
- Produces:
  - `domain.OrganizationRepository.FindMembershipsByEmails(ctx, organizationID uint, emails []string) ([]*OrganizationMembership, error)` — `User` loaded; matches `LOWER(users.email) = ANY(?)` within the organization only. Added to `membershipDirectory`.
  - `planMailbox(ctx, viewer, input CreateMailboxInput, members []resolvedMember, owner *domain.OrganizationMembership) (*mailboxPlan, error)` (normalize, display name, domain, taken check through `FindMailboxByAddress` for every domain) and `commitMailbox(ctx, viewer, plan *mailboxPlan) (*domain.Mailbox, error)` (routing rule, transaction with `s.settings.MaxMailboxes`, `created` + `member_set` events). `CreateMailbox` = validate → resolve → plan → commit; its public behavior is unchanged.
  - `ErrMailboxImportDuplicateRow`, `ErrMailboxImportMemberNotFound`; `error_mappings.go` registers from one table `mailboxErrorCodes []struct{ err error; status int; code string }`, and `importErrorCode(err error) string` reads the same table (unknown → `COMMON.INTERNAL`).
  - `MailboxImportRow { Address, DisplayName, Members string }`; `MailboxImportStatus` = `valid`|`created`|`failed`; `MailboxImportRowResult { Row int; Address string; Status MailboxImportStatus; MailboxID uint; ErrorCode string; MissingEmails []string }`; `MailboxImportResult { DryRun bool; Succeeded, Failed int; Rows []MailboxImportRowResult }`.
  - `Service.ImportMailboxes(ctx, viewer, rows []MailboxImportRow, dryRun bool) (*MailboxImportResult, error)`; `parseImportMembers(cell string) ([]importMember, error)` with `importMember { email string; permission domain.MailboxPermission }`; `stripFormulaGuard(value string) string`.
  - Route `auth.POST("/mailbox-imports", h.ImportMailboxes).Name("mailbox_imports.store")`; `CreateMailboxImportRequest { DryRun *bool \`binding:"required"\`; Rows []MailboxImportRowRequest \`binding:"required,min=1,max=500,dive"\` }`.
- Row order of checks: guard strip → address split at the last `@` (missing → `ADDRESS_INVALID`) → duplicate of any earlier row (lowercased) → display name → members grammar and count → email lookup (one batched call for the whole file before the loop) → `validateMailboxAccess` → `planMailbox` → dry run: cumulative limit (`CountMailboxes` + valid rows so far ≥ max → `LIMIT_REACHED`) / real run: `commitMailbox`.

- [ ] **Step 1: Write failing tests** (fake memberships give user `n` the email `u<n>@people.test`; a user of organization 8 has `other@people.test`):
  - `TestFindMembershipsByEmailsIgnoresCaseAndOtherOrganizations` (PostgreSQL, organization package): `U2@People.test` finds user 2; an email of another organization's member finds nothing.
  - `TestImportDryRunCreatesNothing`: rows `sales@mail.example`, `admin@mail.example`, `team@mail.example` (members `u2@people.test`) → `valid`, `failed MAILBOX.ADDRESS_RESERVED`, `failed MAILBOX.ADDRESS_TAKEN`; `Succeeded 1, Failed 2`; mailbox count unchanged.
  - `TestImportDuplicateRowIgnoresCase` (Review Focus 3): `Sales@mail.example`, `sales@mail.example` → row 2 `MAILBOX.IMPORT_DUPLICATE_ROW` in dry and real run; real run creates exactly one.
  - `TestImportMemberNotInOrganization`: members `u2@people.test:send;stranger@else.test;other@people.test` → `MAILBOX.IMPORT_MEMBER_NOT_FOUND`, `MissingEmails ["stranger@else.test","other@people.test"]`.
  - `TestImportMembersCellGrammar` (table): `""` → `ACCESS_INVALID`; `u2@people.test:owner` → `ACCESS_INVALID`; `u2@people.test;U2@people.test` → `ACCESS_INVALID`; ` u2@people.test ; u3@people.test:view ;` → valid with `send`, `view`; 101 distinct members → `ACCESS_INVALID`.
  - `TestImportLimitCountsRowOrder`: 2 existing, `MaxMailboxes 4`, 3 valid rows → dry `valid, valid, failed MAILBOX.LIMIT_REACHED`; real `created, created, failed MAILBOX.LIMIT_REACHED`.
  - `TestImportRowsCommitIndependently`: organization domain whose fake provider fails the routing rule for row 2 → `created`, `failed MAILBOX.DOMAIN_PROVIDER_FAILED`, `created`; each created mailbox has one `created` event and one `member_set` per member.
  - `TestImportCommitSeesLaterChanges`: dry run `valid`; the address is created meanwhile; real run → `failed MAILBOX.ADDRESS_TAKEN`.
  - `TestImportStripsFormulaGuard` (Review Focus 4): display name `'=Sales` → stored `=Sales`; `'x` stays `'x`.
  - `TestImportRequiresManager`: member viewer → `domain.ErrPermissionDenied`, nothing created.
  - Handler `TestImportMailboxesHandler`: 501 rows and missing `dry_run` → `422 COMMON.VALIDATION_FAILED`; a failed row serializes exactly as `{"row":2,"address":"admin@mail.example","status":"failed","mailbox_id":null,"error_code":"MAILBOX.ADDRESS_RESERVED","missing_emails":[]}`.
- [ ] **Step 2: Run** `cd api && go test ./internal/modules/organization/... ./internal/modules/mailbox/... -run 'FindMembershipsByEmails|Import'` — Expected: FAIL.
- [ ] **Step 3: Implement**, then `cd api && make wire && make route-catalog-check`.
- [ ] **Step 4: Run** Step 2 command and `cd api && go test ./internal/modules/mailbox/... ./internal/modules/organization/...` — Expected: PASS (existing create tests prove the refactor kept `CreateMailbox`).
- [ ] **Step 5: Commit** `feat(mailbox): CSV-row mailbox import with dry run`.

### Task 5: Export

**Files:**
- Modify: `api/internal/domain/organization.go`, `api/internal/modules/organization/membership_repository.go`, `membership_repository_test.go`, `api/internal/domain/mailbox_directory.go`, `api/internal/modules/mailbox/directory_repository.go`, `routes.go`, `service.go`, `dto.go`
- Create: `api/internal/modules/mailbox/export_service.go`, `export_csv.go`, `export_handler.go`, `export_test.go`

**Interfaces:**
- Consumes: Task 3 `ListMailboxDirectory` (with `Limit: domain.MailboxesPerOrganizationCeiling`), Task 4 `membershipDirectory`.
- Produces:
  - `domain.OrganizationRepository.ListMembershipsByUserIDs(ctx, organizationID uint, userIDs []uint) ([]*OrganizationMembership, error)` with `User` loaded; added to `membershipDirectory`.
  - `domain.MailboxDirectoryRepository.ListMembersOfMailboxes(ctx, mailboxIDs []uint) ([]*MailboxMember, error)` (`mailbox_id = ANY(?)`).
  - `MailboxExportMember { Email string; Permission domain.MailboxPermission }`; `MailboxExportRow { Entry *domain.MailboxDirectoryEntry; Members []MailboxExportMember }`; `Service.ExportMailboxes(ctx, viewer, mailboxType domain.MailboxType) ([]MailboxExportRow, error)` — manager only, at most 5000 rows.
  - `writeMailboxCSV(w io.Writer, rows []MailboxExportRow) error` (BOM, `encoding/csv`, CRLF), `csvCell(value string) string`, `exportFilename(mailboxType domain.MailboxType, now time.Time) string`.
  - Route `auth.GET("/mailbox-exports", h.ExportMailboxes).Name("mailbox_exports.index")`; `ExportMailboxesRequest { Type string \`form:"type" binding:"required,oneof=shared personal"\` }`. The handler sets `Content-Type: text/csv; charset=utf-8`, `Content-Disposition`, `X-Content-Type-Options: nosniff`, and `privateResponse`.

- [ ] **Step 1: Write failing tests:**
  - `TestListMembershipsByUserIDs` (PostgreSQL, organization package): returns emails for members of the organization only.
  - `TestExportCSVShape`: output starts with bytes `EF BB BF`; line 1 `address,display_name,members,member_count,sent_last_24h,created_at`; `team@` line `team@mail.example,,u2@people.test:send;u3@people.test:view,2,1,2026-11-19T10:00:00Z` (fixed clock, one attempt).
  - `TestExportGuardsFormulaCells` (Review Focus 4): display names `=SUM(A1)`, `+1`, `-x`, `@x` export as `'=SUM(A1)`, `'+1`, `'-x`, `'@x`.
  - `TestExportOnlyRequestedType`: `type shared` has no `b@` line.
  - `TestExportFilename`: shared, 2026-11-19 → `shared-mailboxes-20261119.csv`; personal → `personal-mailboxes-20261119.csv`.
  - Handler `TestExportMailboxesHandler`: `Content-Type text/csv; charset=utf-8`; `Content-Disposition attachment; filename="shared-mailboxes-20261119.csv"`; missing `type` → `422 COMMON.VALIDATION_FAILED`; member role → `403 PERMISSION.DENIED`.
- [ ] **Step 2: Run** `cd api && go test ./internal/modules/organization/... ./internal/modules/mailbox/... -run 'ListMembershipsByUserIDs|Export'` — Expected: FAIL.
- [ ] **Step 3: Implement**, then `cd api && make wire && make route-catalog-check`.
- [ ] **Step 4: Run** Step 2 command and `cd api && bash ../.agents/skills/verification-before-completion/scripts/run-tiers.sh 1 ./internal/modules/mailbox/...` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(mailbox): server-generated mailbox CSV export`.

### Task 6: Web BFF, services, hooks, and mock

**Files:**
- Create: `web/src/app/api/mailbox-directory/route.ts` (GET), `web/src/app/api/mailbox-imports/route.ts` (POST), `web/src/app/api/mailbox-exports/route.ts` (GET)
- Modify: `web/src/app/api/mailboxes/[mailboxId]/route.ts` (add PUT), `web/src/features/mailbox/server/mailbox-route.ts`, `mock-mailbox-store.ts`, `web/src/features/mailbox/{types.ts,schemas.ts}`, `services/mailbox-service.ts`, `hooks/use-mailbox.ts`, `components/mailbox-workspace.tsx`, `components/create-mailbox-form.tsx`
- Test: `web/src/test/mailbox-route.test.ts`, `web/src/test/mailbox-service.test.ts`

**Interfaces:**
- Consumes: Task 1 generated types and codes.
- Produces:
  - Types `MailboxListScope = 'usable'`, `MailboxDirectoryEntry`, `MailboxDirectoryMeta`, `MailboxDirectoryPage { items: MailboxDirectoryEntry[]; meta: MailboxDirectoryMeta }`, `MailboxDirectoryFilters { type?: MailboxType; q?: string }`, `MailboxImportRowInput { address: string; display_name: string; members: string }`, `MailboxImportRowResult`, `MailboxImportResult`. Constants in `schemas.ts`: `MAILBOX_IMPORT_MAX_ROWS = 500`, `MAILBOX_DIRECTORY_QUERY_MAX_LENGTH = 120`, `MAILBOX_EXPORT_MAX_BYTES = 24 * 1024 * 1024`; route constant `maxMailboxImportBodyBytes = 4 * 1024 * 1024`.
  - Routes: `listMailboxesRoute` forwards `scope=usable` and answers any other `scope` value with `422 COMMON.VALIDATION_FAILED` itself, in both modes; `listMailboxDirectoryRoute`, `updateMailboxRoute(request, rawMailboxId)`, `importMailboxesRoute`, `exportMailboxesRoute` (go-api: `responseType: 'binary'`, `maxResponseBytes: MAILBOX_EXPORT_MAX_BYTES`). Each new route checks `mayManageMailboxes` before reading a body; the directory forwards only `type`, `q`, `cursor`, `per_page`.
  - Mock store methods `directory(organizationId, viewer, filters, cursor, perPage)`, `rename(organizationId, mailboxId, viewer, displayName)`, `importRows(organizationId, viewer, rows, dryRun)` (same rules and codes as Task 4, through the mock create path), `exportCsv(organizationId, viewer, type)`; `list` takes the scope.
  - Service `mailboxService.list(organizationId, scope?: MailboxListScope)`, `directory(organizationId, filters, cursor?: string): Promise<MailboxDirectoryPage>`, `rename(organizationId, mailboxId, displayName): Promise<Mailbox>`, `importMailboxes(organizationId, rows, dryRun): Promise<MailboxImportResult>`, `exportMailboxes(organizationId, type): Promise<{ blob: Blob; filename: string }>` (filename from `Content-Disposition`).
  - Keys and hooks: `mailboxKeys.list(organizationId, scope?)`, `mailboxKeys.directory(organizationId, filters, cursor)`; `useMailboxes(organizationId, scope?)`, `useMailboxDirectory(organizationId, filters, cursor)`, `useRenameMailbox(organizationId)`, `useImportMailboxes(organizationId)` (a real run invalidates `mailboxKeys.organization`), `useExportMailboxes(organizationId)`.
  - `mailbox-workspace.tsx` calls `useMailboxes(organizationId, 'usable')`. `CreateMailboxForm` props become `mailboxCount?: number` (client limit check only when given; the workspace no longer passes it and relies on `409 MAILBOX.LIMIT_REACHED`) and `fixedType?: MailboxType`.

- [ ] **Step 1: Write failing tests** (proxy mode asserts upstream method, path and query; mock mode asserts the envelope):
  - `GET /api/mailbox-directory?type=shared&q=sa&cursor=abc&per_page=20&page=3&x=1` → upstream `GET mailbox-directory?type=shared&q=sa&cursor=abc&per_page=20`.
  - `GET /api/mailboxes?scope=usable` → upstream `mailboxes?scope=usable`; `?scope=all` → `422 COMMON.VALIDATION_FAILED` in mock and proxy mode, no upstream call.
  - `PUT /api/mailboxes/12` `{ display_name: 'Billing' }` → upstream `PUT mailboxes/12`; cross-origin → refused before the body is read.
  - `POST /api/mailbox-imports` with 501 rows → `422 COMMON.VALIDATION_FAILED`, no upstream call.
  - `GET /api/mailbox-exports?type=shared` → passthrough keeps `content-disposition` `attachment; filename="shared-mailboxes-20261119.csv"`.
  - Mock: member role → `403 PERMISSION.DENIED` on directory, PUT, imports, exports; dry run of `Sales@…`, `sales@…` → row 2 `MAILBOX.IMPORT_DUPLICATE_ROW`; directory meta has `daily_send_limit`.
  - Service: `directory` rejects a payload without `meta.next_cursor` key (invalid response); `exportMailboxes` returns filename `shared-mailboxes-20261119.csv`.
- [ ] **Step 2: Run** `cd web && corepack pnpm vitest run src/test/mailbox-route.test.ts src/test/mailbox-service.test.ts` — Expected: FAIL.
- [ ] **Step 3: Implement** following the existing mailbox adapter pattern.
- [ ] **Step 4: Run** Step 2 command plus `src/test/mock-bff-route-contract.test.ts src/test/mailbox-ui.test.tsx`, then `cd web && corepack pnpm type-check && corepack pnpm lint` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(web): BFF routes for the mailbox directory, rename, import, and export`.

### Task 7: Shared mailboxes admin page

**Files:**
- Create: `web/src/features/mailbox/components/admin/shared-mailboxes-admin.tsx`, `create-shared-mailbox-dialog.tsx`, `rename-mailbox-dialog.tsx`, `mailbox-members-drawer.tsx`, `delete-mailbox-dialog.tsx`, `import-mailboxes-dialog.tsx`
- Create: `web/src/features/mailbox/utils/csv.ts`
- Modify: `web/src/features/mailbox/utils/mailbox-error.ts`, `web/src/features/mailbox/index.ts`, `web/src/i18n/modules/mailbox/en-US.ts`, `zh-Hans.ts`
- Test: `web/src/test/mailbox-csv.test.ts`, `web/src/test/shared-mailboxes-admin-ui.test.tsx`

**Interfaces:**
- Consumes: Task 6 hooks, services and types; existing `useMailboxDomains`, `useOrganizationPeople`, `useMailboxMembers`, `useSetMailboxMember`, `useRemoveMailboxMember`, `useDeleteMailbox`, `CreateMailboxForm`.
- Produces:
  - `SharedMailboxesAdmin()` (page body, exported from the feature index): quota line, Export / Import / Create buttons, search (300 ms debounce, `type: 'shared'`), table (name, address, members, 24-hour send attempts, actions: Edit name, then a menu with Manage members and Delete), Previous/Next pager over a client-side stack of cursors.
  - `parseCsv(text: string): string[][]`, `toCsv(rows: readonly (readonly string[])[]): string` (CRLF, quotes a field holding `,`, `"`, CR or LF), `readImportRows(text: string): { ok: true; rows: MailboxImportRowInput[] } | { ok: false; error: 'empty' | 'missing_columns' | 'too_many_rows' }`. Headers match trimmed and case-insensitively: `address`|`邮箱地址`, `display_name`|`名称`, `members`|`成员`; other columns are ignored, so a failed-rows file re-imports as is. Template download = header line only.
  - Import flow: file → `readImportRows` → dry run → preview (counts plus each failed row with its reason) → Confirm sends the same rows with `dryRun false` → result list → "Download failed rows" = `address,display_name,members,error` with the localized reason.
- Copy the spec fixes (en-US / zh-Hans):
  - Quota: `24-hour sends: {sent} / {limit}` / `24 小时已发 {sent} / {limit}`.
  - Column: `24-hour send attempts` / `24 小时发信次数`; note under the table: `Sends from deleted mailboxes still count toward the total, so the rows can add up to less.` / `已删除邮箱的发信仍计入总数，所以各行相加可能小于顶部总数。`
  - Delete: `Its mail is deleted too. The address stays reserved and can never be used again.` / `邮件一并删除，地址永久保留不可再用。`
  - `MAILBOX.IMPORT_MEMBER_NOT_FOUND`: `Not in the organization. Invite them first: {emails}` / `不在组织中，请先邀请：{emails}`.

- [ ] **Step 1: Write failing tests:**
  - `mailbox-csv.test.ts`: Review Focus 5 — `parseCsv('﻿address,display_name,members\r\n"x@d.test","Sales ""EU""\nTeam","a@p.test:send;b@p.test"\r\n')` → `[['address','display_name','members'],['x@d.test','Sales "EU"\nTeam','a@p.test:send;b@p.test']]`; blank lines and a trailing newline add no row; `toCsv([['a,b','c"d']])` → `'"a,b","c""d"\r\n'`; `readImportRows` accepts `邮箱地址,名称,成员`, returns `too_many_rows` for 501 data rows and `missing_columns` without `members`.
  - `shared-mailboxes-admin-ui.test.tsx` (services mocked as in `mailbox-ui.test.tsx`): `shows the organization send total` ("24-hour sends: 37 / 200"); `lists shared mailboxes` (row `docs@zgi.ai` has `5` and `0`); `searches by name or address` (typing `bus` → `directory(1, { type: 'shared', q: 'bus' }, undefined)`); `pages with the cursor` (Next → cursor `c2`; Previous → `undefined`; Next disabled when `has_more` is false); `renames a mailbox` (`rename(1, 7, 'Billing')`); `deletes after confirmation` (dialog shows the delete copy, then `remove(1, 7)`); `manages members in a drawer` (`setMember(1, 7, 3, 'view')`); `creates only shared mailboxes` (no type choice, payload `type: 'shared'`); `import previews then creates` (dry run with `true`, preview "1 ready, 1 with errors" and the not-in-organization copy, Confirm calls with `false`, download yields header `address,display_name,members,error`); `rejects a file over 500 rows before upload` (no service call); `export downloads the server file` (`exportMailboxes(1, 'shared')`); `explains that rows can add up to less` (note visible).
- [ ] **Step 2: Run** `cd web && corepack pnpm vitest run src/test/mailbox-csv.test.ts src/test/shared-mailboxes-admin-ui.test.tsx` — Expected: FAIL.
- [ ] **Step 3: Implement** with existing shadcn primitives (`table`, `dialog`, `sheet`, `dropdown-menu`); all copy via i18n in both locales; map the two new codes in `mailbox-error.ts`.
- [ ] **Step 4: Run** Step 2 command, `cd web && corepack pnpm type-check && corepack pnpm lint` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(web): shared mailbox admin page with import and export`.

### Task 8: Admin area, routing, and gating

**Files:**
- Create: `web/src/app/(protected)/(console)/console/admin/layout.tsx`, `admin/page.tsx`, `admin/mailboxes/page.tsx`, `admin/domains/page.tsx`, `admin/domains/[zoneId]/dns/page.tsx`
- Delete: `web/src/app/(protected)/(console)/console/mail-domain/` (`layout.tsx`, `page.tsx`, `[zoneId]/dns/page.tsx`; their bodies move unchanged to the `admin/domains` pages)
- Create: `web/src/features/mailbox/components/admin/admin-shell.tsx`, `admin-entry-link.tsx`
- Modify: `web/src/constants/routes.ts`, `web/src/app/(protected)/(console)/console/layout.tsx`, `console/page.tsx`, `web/next.config.ts`, `web/src/i18n/modules/nav/{en-US,zh-Hans}.ts`, the console home quick-link copy in `web/src/i18n/modules/console/{en-US,zh-Hans}.ts`
- Test: `web/src/test/admin-area.test.tsx`; update `web/src/test/mail-domains-ui.test.tsx`, `mail-domains-refresh.test.tsx`

**Interfaces:**
- Consumes: Task 7 `SharedMailboxesAdmin`; `useMailOrganization`, `mailOrganizationHref`.
- Produces:
  - `ROUTES.CONSOLE.ADMIN = '/console/admin'`, `ADMIN_MAILBOXES = '/console/admin/mailboxes'`; `MAIL_DOMAIN` becomes `'/console/admin/domains'` and `MAIL_ZONE_DNS` `'/console/admin/domains/:zoneId/dns'` (keys kept, so `mail-dns-records.tsx` and `getMailZoneDnsRoute` need no edit).
  - `admin/layout.tsx` (server): `notFound()` unless the `mailbox` and `organization` features are on; provides the mailbox message namespace; renders `<AdminShell>`. `admin/page.tsx` redirects to `ADMIN_MAILBOXES`.
  - `AdminShell({ children })` (client): loading and error states from `useMailOrganization`; `notFound()` when ready and `!organization.canManage`; left nav "Domains" → `MAIL_DOMAIN`, "Shared mailboxes" → `ADMIN_MAILBOXES`, both through `mailOrganizationHref`, active item `aria-current="page"`.
  - `AdminEntryLink()` (client): reads `useOrganizations()` and the `org` parameter with the same fallback as `useMailOrganization` but never writes the URL; renders "Admin console" → `/console/admin?org=<id>` only for `owner`/`admin`. The console header renders it after the organization switcher when both features are on.
  - Console sidebar drops the `nav.mailDomain` item and the home page drops the domain quick link (members would land on a 404); their message keys are removed.
  - `next.config.ts` `redirects()`: `/console/mail-domain` → `/console/admin/domains` and `/console/mail-domain/:zoneId/dns` → `/console/admin/domains/:zoneId/dns`, `permanent: false` (query, including `org`, is kept by Next.js).
  - Nav copy: `admin` "Admin console" / "管理后台", `adminDomains` "Domains" / "域名管理", `adminSharedMailboxes` "Shared mailboxes" / "公共邮箱".

- [ ] **Step 1: Write failing tests** in `admin-area.test.tsx`: `a member gets the console 404` (`notFound` mock called for role `member`); `owners and admins see the admin navigation` (links `/console/admin/domains?org=1` and `/console/admin/mailboxes?org=1`, current one `aria-current="page"`); `the entry link shows only for owners and admins` (owner → `/console/admin?org=1`; member → absent); `old domain paths redirect` (`await nextConfig.redirects()` contains both entries exactly); `the console sidebar no longer lists Domain`. Update the domain tests' expected hrefs to `/console/admin/domains/9/dns?org=1` and `/console/admin/domains?org=1`, and their `usePathname` to `/console/admin/domains`.
- [ ] **Step 2: Run** `cd web && corepack pnpm vitest run src/test/admin-area.test.tsx src/test/mail-domains-ui.test.tsx src/test/mail-domains-refresh.test.tsx` — Expected: FAIL.
- [ ] **Step 3: Implement** with `git mv` for the two domain pages so history follows them.
- [ ] **Step 4: Run** Step 2 command, `src/test/console-route-boundaries.test.tsx src/test/route-accessibility-contract.test.ts`, then `cd web && corepack pnpm type-check && corepack pnpm lint && corepack pnpm build` — Expected: PASS; the build lists `/console/admin`, `/console/admin/domains`, `/console/admin/domains/[zoneId]/dns`, `/console/admin/mailboxes` and no `/console/mail-domain` page.
- [ ] **Step 5: Commit** `feat(web): organization admin area with domains and shared mailboxes`.

### Task 9: Acceptance flow and release gate

**Files:**
- Create: `api/tests/kest/mailbox_admin.flow.md`
- Modify: `docs/plans/shared-mailbox-admin.zh-CN.md` (status line), `contracts/MAILBOXES.md` only if the flow finds drift

- [ ] **Step 1:** Write the flow: the admin lists the directory (`member_count`, `sent_last_24h`, `meta.daily_send_limit`); a plain member gets `403 PERMISSION.DENIED` on the directory, imports, exports and `PUT /v1/mailboxes/:id`; import dry run (one valid row, one `MAILBOX.IMPORT_MEMBER_NOT_FOUND`) creates nothing, the real run creates the valid row; rename writes a `renamed` access event; export returns the CSV header line; the admin's `GET /v1/mailboxes?scope=usable` omits a shared mailbox the admin is not a member of while the unscoped list includes it.
- [ ] **Step 2: Run** `cd api && make test-kest` against a local API and disposable PostgreSQL — Expected: every step passes.
- [ ] **Step 3:** In the built-in browser, mock mode: as owner open the console, follow "Admin console", search, page, create, rename, change a member, delete, import a two-row file with one bad member, download failed rows, export; as member confirm `/console/admin/mailboxes` shows the 404 and `/console/mail-domain?org=1` redirects to `/console/admin/domains?org=1`.
- [ ] **Step 4: Run** `make check` once (contract, API, web and admin gates) — Expected: PASS.
- [ ] **Step 5: Commit** `test(mailbox): acceptance flow for shared mailbox administration`.
