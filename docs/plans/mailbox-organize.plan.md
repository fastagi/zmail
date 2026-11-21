# Mailbox Organize, Search, and Threads Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give every mailbox placements (inbox/sent, archive, folders, trash), labels, per-person stars, conversation threads that work in both directions, and Chinese-capable search.

**Architecture:** State lives on `mail_messages` (placement, folder, trash time, thread) plus two mailbox-owned tables (folders, labels), a message–label join, and a per-membership star table shaped like `mail_message_reads`. Zmail generates its own RFC `Message-ID` for outbound mail so external replies thread; threads are a listing-level grouping. Search uses `pg_trgm` GIN indexes with `ILIKE`.

**Tech Stack:** Go (Gin, Wire, GORM), PostgreSQL 13+ (`pg_trgm`), OpenAPI contracts (oasdiff), Next.js BFF + React 19 web, Vitest.

**Spec:** `docs/plans/mailbox-organize.zh-CN.md` (approved). Branch `feature/mailbox-organize`, cut from `feature/send-quota-queue` (PR #23, stacked on PR #22).

## Global Constraints

- PostgreSQL only; no SQLite anywhere. Migrations are frozen SQL via `execStatements`, one rollout concern each, every one with `Down` (see `api/database/README.md`).
- English-only source and API text; user copy only in `web/src/i18n/modules/mailbox/{en-US,zh-Hans}.ts`, both locales. Brand `Zmail`.
- Contract first: change `contracts/openapi.yaml` and `contracts/MAILBOXES.md` before code; `make contract-check` and `cd contracts && corepack pnpm generate` keep `web/` and `admin/` generated types in sync. Changes stay backward compatible under oasdiff.
- Placements: `default`, `archive`, `folder`, `trash`. `default` shows as Inbox for inbound and Sent for outbound.
- Trash is purged 30 days after `trashed_at`.
- Folders and labels belong to a mailbox, are flat, names 1–64 characters, unique per mailbox ignoring case, at most 100 of each per mailbox. Label colors come from the fixed palette `gray, red, orange, yellow, green, teal, blue, purple, pink`.
- Shared state: placement, folder, labels, trash. Per person: read (existing) and starred.
- Permissions: view → read, mark read, star, search. send → move placement, add/remove labels, move to trash, restore. organize → permanent delete, empty trash, create/rename/delete folders and labels. `organize` = a shared-mailbox member with `manage`, or the personal owner; never granted by the organization owner/admin role alone.
- No access → `404 MAILBOX.NOT_FOUND`; some access but not enough → `403 PERMISSION.DENIED` (existing rules).
- Threads never merge by subject. Thread lookup uses `rfc_message_id` within the same mailbox only.
- A list call without `placement` returns every placement except `trash`. Mailbox `unread_count` counts only inbound `default` messages the caller has not read.
- Organization log `GET /v1/mail-messages`, stats and `GET /v1/outbound-mail-messages/:id` ignore placement; permanently deleted messages leave them as today.
- Search: `q` 1–200 characters, matched with `ILIKE` against subject, from address/name, to/cc, text body; excludes trash; 2-second statement timeout → `422 MAILBOX.SEARCH_TOO_BROAD`.
- Batch updates: at most 100 message ids per call.
- Performance target: one shared mailbox with 200,000 messages — threaded first page p95 < 200 ms, search of 3+ characters p95 < 500 ms.
- Never send real mail from tests.

## Review Focus

1. A message moved to trash while still `queued` in the #23 send queue must never be sent afterwards (and restoring it must not resurrect the send). Test in Task 4.
2. An inbound reply whose `In-Reply-To` names a message in another mailbox (or another organization) must start a new thread, not join a foreign one. Test in Task 3.
3. A viewer with only `view` calling the batch endpoint with a mix of `starred` and `placement` must get `403` with nothing applied (no partial write). Test in Task 4.
4. Deleting a folder that still holds trashed messages must leave those messages in trash, not pull them back to `default`. Test in Task 2.
5. A search query containing `%`, `_` or `\` must match those characters literally, not as wildcards. Test in Task 5.

---

### Task 1: Contract

**Files:**
- Modify: `contracts/openapi.yaml`, `contracts/MAILBOXES.md`
- Generate: `web/src/http/generated/openapi.ts`, `admin/src/http/generated/openapi.ts`

**Interfaces:**
- Produces (paths under `/v1/mailboxes/{mailbox_id}`):
  - `GET messages` adds query params `placement`, `folder_id`, `label_id`, `starred`, `unread`, `has_attachments`, `q`, `group` (`thread`). With `group=thread` items are `MailThreadSummary { thread_id, latest: MailMessageSummary, message_count, unread, starred, label_ids }`.
  - `MailMessage` / list items add `placement`, `folder_id`, `label_ids`, `starred`, `thread_id`, `trashed_at`.
  - `PUT messages/{message_id}` body adds optional `starred`, `placement`, `folder_id`, `label_ids`.
  - `POST message-updates` body `{ message_ids (1–100), read?, starred?, placement?, folder_id?, add_label_ids?, remove_label_ids? }` → `200 { updated }`.
  - `DELETE messages/{message_id}`: not in trash → moved to trash (`204`, needs send); in trash → permanent (`204`, needs organize).
  - `POST trash-purges` → `200 { deleted }` (organize).
  - `GET threads/{thread_id}` → messages of the thread outside trash, oldest first.
  - `GET|POST mail-folders`, `PUT|DELETE mail-folders/{folder_id}`; `GET|POST mail-labels`, `PUT|DELETE mail-labels/{label_id}`. Folder `{ id, name, created_at }`; label `{ id, name, color, created_at }`.
  - `GET message-counts` → `{ placements: { default_inbound_unread, archive_unread }, folders: [{ folder_id, unread }], starred }`.
  - `access` object adds `organize`.
  - New error codes: `404 MAILBOX.FOLDER_NOT_FOUND`, `404 MAILBOX.LABEL_NOT_FOUND`, `409 MAILBOX.FOLDER_NAME_TAKEN`, `409 MAILBOX.LABEL_NAME_TAKEN`, `422 MAILBOX.FOLDER_LIMIT_REACHED`, `422 MAILBOX.LABEL_LIMIT_REACHED`, `422 MAILBOX.SEARCH_TOO_BROAD`.

- [ ] **Step 1:** Write the contract changes above in `openapi.yaml` and the behavior text (placements, permissions table, thread rules, search rules, unread_count change, DELETE semantics) in `MAILBOXES.md`.
- [ ] **Step 2: Run** `make contract-check` — Expected: PASS (oasdiff reports no breaking change).
- [ ] **Step 3: Run** `cd contracts && corepack pnpm generate`, then `cd web && corepack pnpm type-check` and `cd admin && corepack pnpm type-check` — Expected: PASS.
- [ ] **Step 4: Commit** `feat(contracts): mailbox placements, folders, labels, stars, threads, and search`.

### Task 2: Schema and organize repository

**Files:**
- Create: `api/database/migrations/2026_11_21_090000_add_mail_organize.go` (columns, tables, `pg_trgm`, indexes), `api/database/migrations/2026_11_21_090100_backfill_mail_threads.go`, `api/database/migrations/mail_organize_test.go`
- Modify: `api/database/migrations/testdata/schema.golden.sql`, `api/internal/modules/mailbox/model.go`, `api/internal/domain/mailbox.go`, `api/internal/modules/mailbox/repository.go`
- Create: `api/internal/modules/mailbox/organize_repository.go`, `organize_repository_test.go`

**Interfaces:**
- Produces:
  - Columns on `mail_messages`: `placement varchar(16) not null default 'default'`, `folder_id bigint null references mail_folders on delete set null`, `trashed_at timestamptz null`, `thread_id bigint null`, `references_header text not null default ''`, `rfc_message_id varchar(998) not null default ''`.
  - Tables `mail_folders(id, mailbox_id, name, created_at, updated_at)`, `mail_labels(id, mailbox_id, name, color, ...)`, `mail_message_labels(message_id, label_id)`, `mail_message_stars(membership_id, message_id, starred_at)`; cascades like `mail_message_reads`.
  - Indexes: `(mailbox_id, placement, thread_id, id desc)`, `(mailbox_id, rfc_message_id)`, `(placement, trashed_at)`, GIN `gin_trgm_ops` on `subject`, `from_address`, `from_name`, `to_addresses`, `cc_addresses`, `text_body`.
  - Domain: `type MailPlacement string` with `MailPlacementDefault/Archive/Folder/Trash`; `domain.MailFolder`, `domain.MailLabel`; `MailMessage` gains `Placement`, `FolderID *uint`, `LabelIDs []uint`, `Starred bool`, `ThreadID uint`, `References []string`, `RFCMessageID string`, `TrashedAt *time.Time`.
  - Repository: `CreateFolder/RenameFolder/DeleteFolder/ListFolders`, same four for labels, `CountFolders/CountLabels(ctx, mailboxID) (int64, error)`.
- Backfill rule: inbound rows copy `message_id` into `rfc_message_id`; `thread_id` filled in id order by matching `in_reply_to` against `rfc_message_id` in the same mailbox, else own id.

- [ ] **Step 1: Write failing tests:** `TestMailOrganizeMigrationUpDown` (up then down leaves the golden schema), `TestThreadBackfillLinksInReplyTo` (reply row gets parent's `thread_id`; reply to a message in another mailbox gets its own id), `TestFolderNameUniqueIgnoringCase` (`Contracts` vs `contracts` → `ErrMailFolderNameTaken`), `TestDeleteFolderMovesMessagesToDefaultButKeepsTrash` (Review Focus 4), `TestDeleteLabelDetachesFromMessages`.
- [ ] **Step 2: Run** `cd api && go test ./database/migrations/... ./internal/modules/mailbox/... -run 'MailOrganize|ThreadBackfill|Folder|Label'` — Expected: FAIL.
- [ ] **Step 3: Implement** the migrations (first `CREATE EXTENSION IF NOT EXISTS pg_trgm`), golden schema, models and repository. Folder delete in one transaction: `UPDATE mail_messages SET placement='default', folder_id=NULL WHERE folder_id=? AND placement='folder'`, then delete.
- [ ] **Step 4: Run** Step 2 command — Expected: PASS.
- [ ] **Step 5: Commit** `feat(mailbox): schema for placements, folders, labels, stars, and threads`.

### Task 3: Own Message-ID, References, and thread assignment

**Files:**
- Modify: `api/internal/infra/mailtransport/transport.go`, `cloudflare.go`, `log.go`; `api/internal/modules/mailbox/service.go` (build ~L790–860, send ~L620–660, inbound ~L900–920), `mime.go`, `queued_send_service.go`
- Test: `api/internal/infra/mailtransport/cloudflare_test.go`, `api/internal/modules/mailbox/service_test.go`, `mime_test.go`, `queued_send_test.go`

**Interfaces:**
- Consumes: Task 2 columns and `MailMessage` fields.
- Produces:
  - `mailtransport.Message` gains `MessageID string` and `References []string` (no angle brackets). Cloudflare sends `Message-ID: <id>`, `In-Reply-To: <id>`, `References: <a> <b> …`.
  - `func newRFCMessageID(domain string) string` → `<32 lowercase hex>@<domain>` without brackets, from `crypto/rand`; domain is the sending address's domain.
  - `func (r *repository) FindThreadID(ctx, mailboxID uint, ids []string) (uint, error)` — newest message in the mailbox whose `rfc_message_id` is in `ids`; `0` when none.
  - Outbound reply `References` = parent's `References` + parent's `rfc_message_id`, at most the last 20 ids.
- The id is set when the message is built (before insert), so idempotent replays and #23 queued sends reuse it.

- [ ] **Step 1: Write failing tests:** `TestCloudflareSendsMessageIDAndReferences`; `TestOutboundGetsOwnRFCMessageID` (format `^[0-9a-f]{32}@example\.com$`, stored before provider call); `TestIdempotentReplayKeepsRFCMessageID`; `TestQueuedSendUsesStoredRFCMessageID`; `TestInboundReplyToOutboundJoinsThread`; `TestInboundReplyToOtherMailboxStartsNewThread` (Review Focus 2); `TestReplyReferencesChain`; `TestParseReferencesHeader`.
- [ ] **Step 2: Run** `cd api && go test ./internal/infra/mailtransport/... ./internal/modules/mailbox/... -run 'MessageID|References|Thread|Queued'` — Expected: FAIL.
- [ ] **Step 3: Implement.** Thread assignment happens in the insert transaction: `FindThreadID` over `[in_reply_to] + references`; when 0, set `thread_id = id` after insert.
- [ ] **Step 4: Run** Step 2 command — Expected: PASS.
- [ ] **Step 5: Commit** `feat(mailbox): own Message-ID and two-way threading`.

### Task 4: Access, organize service, and message state

**Files:**
- Modify: `api/internal/domain/mailbox_access.go`, `api/internal/modules/mailbox/access_service.go`, `service.go` (`DeleteMessage`, `SetMessageRead`), `handler.go`, `routes.go`, `dto.go`, `errors.go`, `error_mappings.go`, `api/internal/bootstrap/operatorcommands/mail_broadcast.go`
- Create: `api/internal/modules/mailbox/organize_service.go`, `organize_handler.go`, `organize_test.go`
- Test: `access_test.go`, `handler_test.go`, `queued_send_test.go`

**Interfaces:**
- Consumes: Task 2 repository, Task 1 contract.
- Produces:
  - `MailboxAccess.Organize bool`; `func canOrganize(domain.MailboxAccess) bool`. Set for the personal owner and for shared members with `manage`.
  - `type MessageUpdate struct { Read, Starred *bool; Placement *domain.MailPlacement; FolderID *uint; AddLabelIDs, RemoveLabelIDs []uint; SetLabelIDs *[]uint }`.
  - `func (s *service) UpdateMessages(ctx, viewer, mailboxID uint, ids []uint, update MessageUpdate) (int, error)` — computes required access as the max over the fields (read/starred → view; others → send), checks once, then applies all in one transaction.
  - `func (s *service) PurgeTrash(ctx, viewer, mailboxID uint) (int, error)` and `func (s *service) PurgeExpiredTrash(ctx context.Context, now time.Time, limit int) (int, error)` (30 days).
  - Folder/label service methods for the Task 1 routes.
- Moving a `queued` message to trash cancels it exactly as today's delete does; restoring a canceled message keeps it canceled.
- `PurgeExpiredTrash` runs at the end of each `mail:broadcast:work` tick with the remaining batch.

- [ ] **Step 1: Write failing tests:** `TestOrganizeAccess` (personal owner yes; shared manage yes; shared send no; org admin non-member no); `TestBatchUpdateNeedsHighestAccess` (Review Focus 3: view-only with `starred`+`placement` → 403, nothing written); `TestStarIsPerPerson`; `TestDeleteMovesToTrashThenPermanent` (second delete without organize → 403); `TestTrashingQueuedMessageCancelsSend` and `TestRestoringCanceledMessageDoesNotSend` (Review Focus 1); `TestPurgeExpiredTrashAfter30Days`; `TestFolderLimit100`; `TestLabelColorMustBeInPalette`; handler tests mapping each new error code.
- [ ] **Step 2: Run** `cd api && go test ./internal/domain/... ./internal/modules/mailbox/... ./internal/bootstrap/...` — Expected: FAIL.
- [ ] **Step 3: Implement**, then `cd api && make wire && make route-catalog-check`.
- [ ] **Step 4: Run** Step 2 command and `cd api && bash ../.agents/skills/verification-before-completion/scripts/run-tiers.sh 1 ./internal/modules/mailbox/...` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(mailbox): placements, folders, labels, stars, and trash`.

### Task 5: Listing, threads, counts, and search

**Files:**
- Modify: `api/internal/modules/mailbox/repository.go` (`ListMessages` ~L357), `service.go`, `handler.go`, `dto.go`, `routes.go`
- Create: `api/internal/modules/mailbox/listing_test.go`, `api/internal/modules/mailbox/listing_bench_test.go`

**Interfaces:**
- Consumes: Tasks 2–4.
- Produces:
  - `domain.MailMessageFilter` gains `Placement`, `FolderID`, `LabelID`, `Starred`, `Unread`, `HasAttachments`, `Query`, `GroupThreads`.
  - `func (r *repository) ListThreads(ctx, mailboxID, membershipID uint, filter) ([]domain.MailThreadSummary, int64, error)` — groups only for placement views; `DISTINCT ON (thread_id)` within the placement, newest first.
  - `func (r *repository) ThreadMessages(ctx, mailboxID, threadID, membershipID uint) ([]*domain.MailMessage, error)`.
  - `func (r *repository) MessageCounts(ctx, mailboxID, membershipID uint) (domain.MailMessageCounts, error)`.
  - Search escapes `\`, `%`, `_` and runs under `SET LOCAL statement_timeout = '2s'`; a cancel maps to `ErrMailSearchTooBroad`.
  - `unread_count` on the mailbox switches to inbound `default` unread.

- [ ] **Step 1: Write failing tests:** `TestListWithoutPlacementExcludesTrash`; `TestThreadRowShowsLatestAndCount`; `TestThreadViewIncludesSentReplies`; `TestStarredViewIsNotGrouped`; `TestSearchFindsChineseSubstring` (subject `报价单确认` found by `报价单`); `TestSearchEscapesWildcards` (Review Focus 5: `50%` does not match `500`); `TestSearchTimeoutMapsToTooBroad` (fake timeout); `TestUnreadCountOnlyInbox`; `TestMessageCountsPerFolder`.
- [ ] **Step 2: Run** `cd api && go test ./internal/modules/mailbox/... -run 'List|Thread|Search|Count'` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** Step 2 command — Expected: PASS. Then run `BenchmarkThreadedListing200k` and `BenchmarkSearch200k` (seed 200,000 rows into disposable PostgreSQL, `-benchtime=50x`) and record p95 in the report — Expected: < 200 ms and < 500 ms. If over, add the index or query change the plan for that query shows (`EXPLAIN ANALYZE` in the report).
- [ ] **Step 5: Commit** `feat(mailbox): threaded listing, counts, and search`.

### Task 6: Web BFF, services, hooks, and mock

**Files:**
- Modify: `web/src/features/mailbox/server/mailbox-route.ts`, the mock mailbox store under `web/src/features/mailbox/server/`, `web/src/features/mailbox/services/mailbox-service.ts`, `hooks/use-mailbox.ts`, `schemas.ts`, `types.ts`
- Create: route files under `web/src/app/api/mailboxes/[mailboxId]/` for `message-updates`, `trash-purges`, `threads/[threadId]`, `mail-folders(/[folderId])`, `mail-labels(/[labelId])`, `message-counts`
- Test: `web/src/test/mailbox-route.test.ts`, `web/src/test/mailbox-service.test.ts`

**Interfaces:**
- Produces: services `updateMessages`, `purgeTrash`, `getThread`, `listFolders/createFolder/renameFolder/deleteFolder`, the same for labels, `getMessageCounts`; `listMailboxMessages` accepts the new filters. Hooks with `use` prefix; every mutation invalidates messages, counts and the mailbox list (unread count).
- Mock mirrors placements, per-person stars, threads, folder/label limits, search (case-insensitive substring) and `SEARCH_TOO_BROAD` for a 1-character query.

- [ ] **Step 1: Write failing route tests** in mock and proxy mode for each new route (proxy asserts upstream method and path), plus passthrough of `403 PERMISSION.DENIED` and `422 MAILBOX.SEARCH_TOO_BROAD`.
- [ ] **Step 2: Run** `cd web && corepack pnpm vitest run src/test/mailbox-route.test.ts src/test/mailbox-service.test.ts` — Expected: FAIL.
- [ ] **Step 3: Implement** following the existing mailbox adapter pattern.
- [ ] **Step 4: Run** Step 2 command and `cd web && corepack pnpm type-check && corepack pnpm lint` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(web): BFF routes for mailbox organize, threads, and search`.

### Task 7: Web UI

**Files:**
- Modify: `web/src/features/mailbox/components/mailbox-workspace.tsx`, `message-actions.tsx`, `email-detail.tsx`
- Create: `web/src/features/mailbox/components/mailbox-sidebar.tsx`, `message-list.tsx`, `thread-view.tsx`, `manage-folders-dialog.tsx`, `manage-labels-dialog.tsx`
- Modify: `web/src/i18n/modules/mailbox/en-US.ts`, `zh-Hans.ts`
- Test: `web/src/test/mailbox-ui.test.tsx`

**Behavior (spec §5.1–5.11):** sidebar with Inbox (unread), Starred, Sent, Archive, Trash, folders, labels; threaded rows with count, my unread/star state; multi-select with archive, move to folder, label, mark read, trash, restore, delete forever; search box (search results ungrouped, trash excluded, too-broad message "Enter a longer keyword"); thread view oldest first incl. sent replies; folder/label management dialogs; actions hidden by access (`view` sees only read/star; `organize` sees delete-forever, empty trash and management).

- [ ] **Step 1: Write failing UI tests:** `view-only member sees no move or label actions`, `archiving a thread row removes it from inbox`, `search shows ungrouped results and too-broad notice`, `thread view lists sent reply`, `trash shows restore and, for organize, delete forever`, `star is per person` (toggling does not change another member's fixture), `folder name taken shows inline error`.
- [ ] **Step 2: Run** `cd web && corepack pnpm vitest run src/test/mailbox-ui.test.tsx` — Expected: FAIL.
- [ ] **Step 3: Implement** with existing shadcn components; all copy via i18n in both locales.
- [ ] **Step 4: Run** Step 2 command, `corepack pnpm type-check && corepack pnpm lint` — Expected: PASS. The controller walks inbox → archive → folder → label → search → thread → trash → restore in the built-in browser in mock mode.
- [ ] **Step 5: Commit** `feat(web): mailbox folders, labels, stars, threads, and search`.

### Task 8: Acceptance flow, docs, and Message-ID check

**Files:**
- Modify: the kest acceptance flow for mailboxes (extend with organize steps), `docs/plans/mailbox-organize.zh-CN.md` (status), `contracts/MAILBOXES.md` if acceptance finds drift
- Create: `docs/runbooks/mail-message-id-check.md`

- [ ] **Step 1:** Extend the acceptance flow: two members of a shared mailbox — send member archives, view member sees it archived and cannot trash (403); stars stay per person; inbound reply to an outbound message lands in the same thread; search `报价` finds the message.
- [ ] **Step 2: Run** the flow against a local API and disposable PostgreSQL — Expected: all steps pass.
- [ ] **Step 3:** Write the runbook: send one message from staging to an inbox the operator controls, open its raw headers, and confirm `Message-ID` equals the stored `rfc_message_id`. This step sends real mail, so a person runs it; the plan does not.
- [ ] **Step 4: Run** `make agent-check-changed` — Expected: PASS.
- [ ] **Step 5: Commit** `docs(mailbox): acceptance flow and Message-ID runbook for mailbox organize`.
