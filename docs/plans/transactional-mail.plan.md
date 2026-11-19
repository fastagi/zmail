# Transactional Mail (v2 slice 5) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a developer's backend send template-based transactional mail (verification codes, resets) with a key that can do nothing else, keep secret variables out of every read path, give transactional mail its own send lane, and report outcomes through a masked log and `email.*` webhooks.

**Architecture:** A `transactional_*.go` sub-domain inside the `mailbox` module owns two new tables (`transactional_messages`, `transactional_message_events`) and never writes `mail_messages`. Sending stores the rendered message AES-GCM encrypted, commits, then dispatches after commit; the `mail:broadcast:work` worker reconciles. Send limits split into three per-organization rolling-24h lanes (`personal`, `broadcast`, `transactional`). A shared organization `mail_suppressions` list blocks transactional sends and broadcast recipients. API keys gain an immutable `mode` (`live`/`test`); `apikey.RequireScopes` rejects test keys unless a route opts in.

**Tech Stack:** Go (Gin, Wire, GORM), PostgreSQL, OpenAPI contracts (redocly, oasdiff), Next.js 16 BFF + React 19, TanStack Query, next-intl, Vitest.

**Spec:** `docs/plans/transactional-mail.zh-CN.md` (approved; section numbers below refer to it). Visual reference for UI tasks: `docs/design/v2/Developer.dc.html`.

## Global Constraints

- PostgreSQL only; no SQLite. Migrations are frozen SQL via `execStatements` in `api/database/migrations/`, `UseTransaction: true`, every one with `Down`; update `api/database/migrations/testdata/schema.golden.sql` with `FASTBUILD_UPDATE_GOLDEN_SCHEMA=1`.
- Migration names (all later than the unified-inbox `2026_11_22_*` migrations): `2026_11_23_080000_add_send_lanes`, `2026_11_23_090000_add_api_key_mode`, `2026_11_23_100000_add_transactional_templates`, `2026_11_23_110000_create_transactional_messages`, `2026_11_23_120000_create_mail_suppressions`. `add_api_key_mode` registers in the apikey starter manifest; the other four in the mailbox manifest (`api/internal/modules/mailbox/provider.go`).
- **Rebase at execution time:** the unified-inbox plan merges to `main` first and adds mailbox migrations. Before each task that adds a migration, rebase onto `main` and recompute: the `assert.Len(t, migrations, N)` counts in `api/internal/starter/defaults_test.go` (each +1 for `add_api_key_mode`, on top of whatever `main` has), the mailbox manifest migration list order, `schema.golden.sql`, and `api/internal/wiring/wire_gen.go` (re-run `cd api && make wire`; never hand-merge `wire_gen.go`).
- Postgres tests: `FASTBUILD_TEST_POSTGRES_DSN='postgres://fastbuild:fastbuild_test@127.0.0.1:55450/postgres?sslmode=disable'`.
- Contract first per slice: change `contracts/openapi.yaml` and the owning `contracts/*.md` before code; run `make contract-check` and `cd contracts && corepack pnpm generate`. Every change is additive under oasdiff (new paths, optional request fields, new response fields, new error codes). Web zod schemas that mirror a changed response are `strictObject`, so the contract task of each slice also updates them and the mock stores so `cd web && corepack pnpm type-check` stays green.
- **No test, script, Kest flow or agent-run check may send real mail.** Use `fakeTransport`/`scriptedTransport` in Go tests, `MAILBOX_PROVIDER=log` for running servers, and simulator addresses on `test.zmail.im`. No real-delivery acceptance step exists in this plan (D32).
- English-only code, comments and API text; user copy only in `web/src/i18n/modules/developer/{en-US,zh-Hans}.ts` (and `mailbox`/`errors` modules where those surfaces change), both locales. Brand `Zmail`.
- Scope `mail:transactional` (D2); it neither includes nor is included by `mail:send`. `*` still grants it.
- Test key prefix `fastbuild_test_<short id>`; live keys keep `fastbuild_<short id>` (D3). `mode` is immutable; existing keys become `live`.
- Lane limits: `MAILBOX_DAILY_SEND_LIMIT` (personal, default 200), `MAILBOX_DAILY_BROADCAST_LIMIT` and `MAILBOX_DAILY_TRANSACTIONAL_LIMIT` (unset → value of `MAILBOX_DAILY_SEND_LIMIT`), each 1–100000. Rolling window `[now-24h, now)`, counted by `send_attempted_at`, failed attempts count.
- Transactional lane full → queue (`queued_reason=send_limit`); waiting count may reach the lane limit, then `429 MAILBOX.SEND_LIMIT_REACHED`. Queued longer than 1 hour → `failed` / `send.expired` (D15).
- New config: `MAILBOX_TRANSACTIONAL_ENABLED` (default false), `MAILBOX_SECRET_ENCRYPTION_KEY` (≥ 32 chars, required when enabled, else startup fails), `MAILBOX_SIMULATOR_DOMAIN` (default `test.zmail.im`), `MAILBOX_TRANSACTIONAL_RECIPIENT_PER_MINUTE` (default 3), `MAILBOX_TRANSACTIONAL_KEY_PER_SECOND` (default 20). Every new variable goes in `api/.env.example` (`TestEnvExampleDocumentsEveryVariableTheConfigReads`).
- Rate limits (fixed window, `ratelimit` stores, D20/D34): per key 20 per 1 s → `429 COMMON.RATE_LIMITED`; per (organization, mode, template, recipient) 3 per 60 s → `429 MAILBOX.RECIPIENT_RATE_LIMITED`; console test send 5 per minute per user → `429 COMMON.RATE_LIMITED`. All set `Retry-After` (seconds, ≥ 1). Rule names `mail.transactional.key`, `mail.transactional.recipient`, `mail.transactional.test_send`. Bucket keys never contain a raw address (use `crypto.SHA256Hex`).
- `Idempotency-Key` header required on `POST /v1/transactional-messages`, 1–255 chars accepted by the existing `validIdempotencyKey`; scope `(organization_id, mode, key)`; window 24 h from the first message's `created_at`, after which the key is released (set NULL) and reused.
- Secret masking: a value that parses as an `http`/`https` URL with a host shows `<host>/••••`; anything else shows `••••••` (exactly six U+2022). Recipient masking: first 2 chars of the local part + `••` (1 char when the local part has ≤ 2), domain kept: `li••@example.com`.
- Retention: transactional messages and events deleted 30 days after `created_at`; ciphertext wiped on every terminal state; DB check `status = 'queued' OR payload_ciphertext IS NULL`.
- Dispatch lease 2 minutes; retries only when `mailtransport.Error.Permanent == false && Responded == true`; delays 30 s, 2 min, 10 min (at most 3 retries, 4 provider calls); any other failure → `failed` / `transport.unknown_outcome`, never resent (D33).
- Simulator local parts: `delivered`, `bounced`, `soft-bounced`, `complained`, `suppressed`, optional `+tag`, on `MAILBOX_SIMULATOR_DOMAIN`. Simulated and test-mode messages never call the provider, never count in a lane, never write `mail_suppressions`. A test key sending to a normal address simulates `delivered`.
- Logs (`slog`) in transactional code carry only `request_id`, `organization_id`, `message_id`, `template_id`, `mode`, status and failure codes — never addresses, subjects, variables or ciphertext.
- Web: route `/console/developers`, tabs and mode in URL state `?tab=start|templates|logs|limits|webhooks&mode=live|test`; components branch only on `error_code`. Web tests live in `web/src/test/` (repo convention).

## Review Focus

1. A recipient given as `" LiLei@Example.COM "` (or a simulator address like `Bounced+u1@TEST.zmail.im`) must be treated as `lilei@example.com` everywhere: suppression match, idempotency fingerprint, recipient rate-limit bucket, simulator outcome, and the log `to` filter. Tests in Tasks 10 and 12.
2. A secret URL with credentials, such as `https://u:p@reset.acme.com/t?code=1`, must mask to `reset.acme.com/••••` (no userinfo, path or query); a schemeless `reset.acme.com/t` masks to `••••••`. Test in Task 10.
3. The same `Idempotency-Key` used by a live key and a test key of one organization must create two independent messages, and `GET /v1/transactional-messages/{id}` with the other mode's key must answer 404. Test in Task 10.
4. An empty-string value for a required (or secret) variable must be refused with `422 MAILBOX.TEMPLATE_VARIABLES_MISSING` and store nothing. Test in Task 10.
5. A queued message whose template sender domain lost verification before dispatch must end `failed` / `send.sender_unverified` with zero provider calls and its ciphertext wiped. Test in Task 11.

---

## Slice S1 — Send lanes

### Task 1: S1 contract — lane limits

**Files:**
- Modify: `contracts/openapi.yaml` (`MailboxDomain` ~L9997), `contracts/MAILBOXES.md` (`### Send` ~L812, `### Broadcasts` send-limit text)
- Generate: `web/src/http/generated/openapi.ts`, `admin/src/http/generated/openapi.ts`
- Modify: `web/src/features/mailbox/schemas.ts` (`mailboxDomainSchema`), `web/src/features/mailbox/server/mock-mailbox-store.ts` (`domains()`)

**Interfaces:**
- Produces: `MailboxDomain` gains required `daily_broadcast_limit` and `daily_transactional_limit` (integers ≥ 1). `daily_send_limit` is re-described as "the personal lane limit"; its value is unchanged.

- [ ] **Step 1:** Add the two fields to `MailboxDomain`; rewrite the MAILBOXES.md send-limit text: three lanes counted separately per organization over a rolling 24 h by provider-call start, failed attempts count, personal = console/replies/`outbound-mail-messages`/template tests, broadcast = each broadcast recipient (full → broadcast pauses), transactional = live non-simulated transactional messages (full → queue). Soft-limit wording stays.
- [ ] **Step 2:** Add both fields to `mailboxDomainSchema` (`positiveIntegerSchema`) and to the mock `domains()` output (both equal `MOCK_DAILY_SEND_LIMIT`).
- [ ] **Step 3: Run** `make contract-check && cd contracts && corepack pnpm generate && cd ../web && corepack pnpm type-check && cd ../admin && corepack pnpm type-check` — Expected: PASS, oasdiff reports no breaking change.
- [ ] **Step 4: Commit** `feat(contracts): split the mail send limit into three lanes`.

### Task 2: S1 API — lane migration, counting, configuration

**Files:**
- Create: `api/database/migrations/2026_11_23_080000_add_send_lanes.go`, `api/database/migrations/send_lanes_test.go`
- Modify: `api/database/migrations/testdata/schema.golden.sql`, `api/internal/modules/mailbox/provider.go` (manifest), `api/internal/infra/config/config.go` (`MailboxConfig`, load ~L685, `validateMailboxConfig` ~L1325), `api/.env.example`, `api/internal/domain/mailbox.go` (`CountOutboundSince`, `LeaseWaitingSends` signatures ~L867), `api/internal/modules/mailbox/model.go` (`MailMessagePO.SendLane`), `repository.go` (~L585–700), `service.go` (`Settings`, `SettingsFromConfig`, `send` ~L595), `queued_send_service.go`, `broadcast_service.go` (`run.send` ~L523), `dto.go` (~L285), `handler.go` (~L77), `UPGRADING.md`
- Test: `api/internal/modules/mailbox/queued_send_test.go`, `broadcast_test.go`, `service_test.go` (fake repo), `api/internal/infra/config/validate_test.go`

**Interfaces:**
- Produces:
  - `type SendLane string` in `api/internal/domain/mailbox.go`: `SendLanePersonal = "personal"`, `SendLaneBroadcast = "broadcast"`, `SendLaneTransactional = "transactional"`; `domain.MailMessage.SendLane SendLane`.
  - `CountOutboundSince(ctx, organizationID uint, lane domain.SendLane, since time.Time) (int64, error)` — counts `mail_messages` of that lane only.
  - `LeaseWaitingSends(...)` counts only `send_lane = 'personal'` attempts against `sendLimit`.
  - `Settings.DailyBroadcastLimit`, `Settings.DailyTransactionalLimit int`; `config.MailboxConfig.DailyBroadcastLimit`, `DailyTransactionalLimit int` (0 = inherit `DailySendLimit` in `SettingsFromConfig`).
  - `service.send(ctx, mailbox, input, queueAtLimit bool)` derives the lane: `queueAtLimit == false` (broadcast worker) → broadcast, else personal; the limit compared is the lane's.
  - Handler fields `daily_broadcast_limit`, `daily_transactional_limit` on `MailboxDomainResponse`.
- Migration: `ALTER TABLE mail_messages ADD COLUMN send_lane varchar(16) NOT NULL DEFAULT 'personal' CHECK (send_lane IN ('personal','broadcast'))`; backfill `UPDATE mail_messages m SET send_lane = 'broadcast' FROM mail_broadcast_recipients r WHERE r.message_id = m.id`; create `idx_mail_messages_lane_attempted ON mail_messages (send_lane, send_attempted_at) WHERE direction = 'outbound'` and drop `idx_mail_messages_send_attempted` (the spec's `mail_messages_send_attempted_idx` is this index). Down restores the old index and drops the column.

- [ ] **Step 1: Write failing tests:** `TestAddSendLanesBackfillsBroadcastRecipients` (message referenced by `mail_broadcast_recipients` → `broadcast`, others `personal`; Down then Up round-trips); `TestBroadcastLaneDoesNotConsumePersonalLane` (personal limit 3, 3 broadcast sends recorded → a console send is `201`, not queued); `TestPersonalLaneFullStillLetsBroadcastSend` (3 personal attempts → broadcast recipient sends, no `send_limit_reached`); `TestQueuedSendsCountOnlyPersonalAttempts`; config tests: `MAILBOX_DAILY_BROADCAST_LIMIT=0`/unset inherits `MAILBOX_DAILY_SEND_LIMIT`, `100001` rejected.
- [ ] **Step 2: Run** `cd api && FASTBUILD_TEST_POSTGRES_DSN='postgres://fastbuild:fastbuild_test@127.0.0.1:55450/postgres?sslmode=disable' go test ./database/migrations/... ./internal/modules/mailbox/... ./internal/infra/config/... -run 'SendLanes|Lane|QueuedSends|MailboxConfig|EnvExample'` — Expected: FAIL.
- [ ] **Step 3: Implement** the migration, golden schema, config, settings, lane-aware counting and the DTO fields. Add an `UPGRADING.md` entry: lanes replace the shared limit; defaults keep personal and broadcast at least as large as today; the organization's theoretical maximum triples, so set the two new limits against the Cloudflare account limit.
- [ ] **Step 4: Run** Step 2 command, then `cd api && go test ./internal/modules/mailbox/... ./internal/bootstrap/...` — Expected: PASS (existing send and broadcast tests unchanged).
- [ ] **Step 5: Commit** `feat(mailbox): count personal, broadcast and transactional sends in separate lanes`.

## Slice S2 — Transactional templates

### Task 3: S2 contract — template kind, key, sender, variable definitions

**Files:**
- Modify: `contracts/openapi.yaml` (`MailTemplateRequest` ~L12014, `MailTemplateSummary` ~L12056, `MailTemplate` ~L12086, `GET /v1/mail-templates` ~L4780, `ErrorCode` ~L7241, mail-template and mail-domain delete error descriptions), `contracts/MAILBOXES.md` (`### Templates` ~L909, Errors ~L1345)
- Generate: web/admin `openapi.ts`
- Modify: `web/src/features/mailbox/schemas.ts` (`mailTemplateSummarySchema`, `mailTemplateSchema`, `mailTemplateInputSchema`), `web/src/features/mailbox/server/mailbox-route.ts` + mock template store, `web/src/http/codes.ts`, `admin/src/http/codes.ts` only if its `satisfies` check requires it

**Interfaces:**
- Produces:
  - `MailTemplateRequest` optional: `kind` (`standard`|`transactional`, default `standard`), `key` (`^[a-z][a-z0-9-]{0,63}$`), `from_address` (≤ 320), `from_name` (≤ 100), `variable_definitions` (≤ 100 items of `{ name, secret: boolean, required: boolean }`).
  - `MailTemplateSummary` and `MailTemplate` gain required `kind`, `key` (nullable), `from` (nullable `{ address, name }`), `variable_definitions` (array, empty for standard).
  - `GET /v1/mail-templates?kind=standard|transactional` (absent → all).
  - Error codes: `409 MAILBOX.TEMPLATE_KEY_TAKEN`, `409 MAILBOX.TEMPLATE_KIND_IMMUTABLE`, `422 MAILBOX.TEMPLATE_KIND_MISMATCH`; `403 PERMISSION.DENIED` documented on template writes for a member touching a transactional template; `DELETE /v1/mail-domains/{domain_id}` `409 MAILBOX.DOMAIN_IN_USE` also when a transactional template sends from it; mailbox send, template tests and broadcast create/send document `422 MAILBOX.TEMPLATE_KIND_MISMATCH`.
  - Web: `ApiErrorCode.MAILBOX_TEMPLATE_KEY_TAKEN`, `MAILBOX_TEMPLATE_KIND_IMMUTABLE`, `MAILBOX_TEMPLATE_KIND_MISMATCH`; mock templates carry `kind: 'standard', key: null, from: null, variable_definitions: []`.

- [ ] **Step 1:** Write the openapi and MAILBOXES.md changes above, with spec §6.2 rules (every placeholder declared and every declaration used; secret ⇒ required; required ⇔ no fallback; sender domain verified or, with the `log` provider, the instance `MAILBOX_DOMAIN`; owner/admin only for transactional writes; transactional templates unusable for mailbox sends, template tests and broadcasts).
- [ ] **Step 2:** Update the web schemas, mock and codes.
- [ ] **Step 3: Run** `make contract-check && cd contracts && corepack pnpm generate && cd ../web && corepack pnpm type-check && corepack pnpm vitest run src/test/mail-templates-ui.test.tsx src/test/mailbox-route.test.ts` — Expected: PASS.
- [ ] **Step 4: Commit** `feat(contracts): transactional mail templates`.

### Task 4: S2 API — transactional templates

**Files:**
- Create: `api/database/migrations/2026_11_23_100000_add_transactional_templates.go`, `api/database/migrations/transactional_templates_test.go`, `api/internal/modules/mailbox/transactional_template.go`, `api/internal/modules/mailbox/transactional_template_test.go`
- Modify: `api/database/migrations/testdata/schema.golden.sql`, `api/internal/modules/mailbox/provider.go`, `api/internal/domain/mailbox.go` (`MailTemplate`), `api/internal/domain/error_codes.go`, `api/internal/modules/mailbox/model.go` (template PO), `template_repository.go`, `template_service.go`, `template_handler.go`, `errors.go`, `error_mappings.go`, `service.go` (`Service` interface ~L90), `broadcast_service.go` (create ~L185, start ~L233), `domain_repository.go` (`DeleteDomain` ~L219)

**Interfaces:**
- Produces:
  - Domain: `type MailTemplateKind string` (`MailTemplateStandard`, `MailTemplateTransactional`); `type TransactionalVariable struct { Name string; Secret, Required bool }`; `MailTemplate` gains `Kind`, `Key string`, `FromAddress`, `FromName string`, `Variables []TransactionalVariable`.
  - `TemplateInput` and `MailTemplateRequest` gain `Kind`, `Key`, `FromAddress`, `FromName`, `VariableDefinitions` (keep the `TemplateInput(r)` conversion valid).
  - `func validateTransactionalTemplate(template *domain.MailTemplate, parsed *parsedTemplate) error` in `transactional_template.go` — enforces §6.2; wraps `ErrMailTemplateInvalid` with the field name.
  - `CreateTemplate(ctx, viewer domain.OrganizationContext, input TemplateInput)`, `UpdateTemplate(ctx, viewer, id, input)`, `DeleteTemplate(ctx, viewer, id)` take the viewer (role check `viewer.Role.CanManageOrganization()` for transactional; `ErrMailTemplateKindImmutable` when `input.Kind != stored.Kind`); `ListTemplates(ctx, organizationID uint, kind domain.MailTemplateKind)` (`""` = all).
  - `func (r *repository) FindTemplateByKey(ctx, organizationID uint, key string) (*domain.MailTemplate, error)`.
  - New sentinels and mappings: `ErrMailTemplateKeyTaken` (409 `MAILBOX.TEMPLATE_KEY_TAKEN`), `ErrMailTemplateKindImmutable` (409 `MAILBOX.TEMPLATE_KIND_IMMUTABLE`), `ErrMailTemplateKindMismatch` (422 `MAILBOX.TEMPLATE_KIND_MISMATCH`); matching `domain.Code*` constants.
  - `applyTemplate`, `SendTemplateTest`, broadcast create and send return `ErrMailTemplateKindMismatch` for a transactional template; `DeleteDomain` returns `ErrMailDomainInUse` when `mail_templates.from_address LIKE '%@' || name`.
- Migration per spec §7.2: columns `kind`, `template_key`, `from_address`, `from_name`, `variable_definitions jsonb`; unique `mail_templates_key_idx ON mail_templates (organization_id, template_key) WHERE template_key IS NOT NULL`; check constraint tying the fields to `kind`. Down: `DELETE FROM mail_templates WHERE kind = 'transactional'`, then drop.

- [ ] **Step 1: Write failing tests:** `TestTransactionalTemplatesMigrationUpDown`; `TestTransactionalTemplateRequiresDeclaredPlaceholders` (undeclared `{{code}}` → `TEMPLATE_INVALID`; declared-but-unused → invalid); `TestSecretVariableCannotHaveFallback`; `TestOptionalVariableNeedsFallback`; `TestTransactionalTemplateSenderMustBeVerified` (pending domain → `ErrMailDomainNotVerified`; `log` provider + instance domain → ok); `TestMemberCannotWriteTransactionalTemplate` (create/update/delete → `domain.ErrPermissionDenied`; member still edits a standard one); `TestTemplateKindIsImmutable`; `TestTemplateKeyUniquePerOrganization` (Postgres → `ErrMailTemplateKeyTaken`, other org may reuse); `TestTransactionalTemplateRefusedForMailboxSendTestAndBroadcast`; `TestDeleteDomainUsedByTransactionalTemplate`; `TestListTemplatesFiltersByKind`; handler test mapping the three new codes.
- [ ] **Step 2: Run** `cd api && FASTBUILD_TEST_POSTGRES_DSN=... go test ./database/migrations/... ./internal/modules/mailbox/... -run 'TransactionalTemplate|Template|DeleteDomain'` — Expected: FAIL.
- [ ] **Step 3: Implement.** Map the Postgres unique violation on `mail_templates_key_idx` to `ErrMailTemplateKeyTaken`, beside the existing name mapping.
- [ ] **Step 4: Run** Step 2 command and `cd api && go test ./internal/modules/mailbox/... && make route-catalog-check` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(mailbox): transactional templates with declared and secret variables`.

### Task 5: S2 web — Developer page shell and Templates tab

**Files:**
- Create: `web/src/app/(protected)/(console)/console/developers/page.tsx`, `layout.tsx`; `web/src/features/developer/index.ts`, `types.ts`, `components/developer-console.tsx`, `components/transactional-templates-tab.tsx`, `components/transactional-template-dialog.tsx`, `hooks/use-developer-tab.ts`; `web/src/i18n/modules/developer/{index,en-US,zh-Hans}.ts`
- Modify: `web/src/i18n/module-names.ts`, `loader.ts`, `client-message-namespaces.ts` (`developer: ['developer', 'mailbox']`); `web/src/features/mailbox/services/mailbox-service.ts` (`templates(organizationId, kind?)`), `hooks/use-mail-templates.ts`, `components/mail-templates.tsx` (lists `kind=standard` only); mock BFF list filter by `kind`
- Test: `web/src/test/developer-templates-ui.test.tsx`, `web/src/test/mail-templates-ui.test.tsx`

**Interfaces:**
- Consumes: Task 3 shapes.
- Produces:
  - `DeveloperConsole` (client) with `role="tablist"` tabs `start | templates | logs | limits | webhooks` and a live/test mode switch; `useDeveloperTab(): { tab, mode, setTab, setMode }` reading/writing `?tab=` and `?mode=` (unknown values fall back to `start` / `live`). Tabs not yet built render a placeholder until their task.
  - Page shows only when `isWebFeatureEnabled('mailbox')` and organization feature are on (else `notFound()`); when `mailbox-domains` reports `transactional_enabled === false` (field arrives in Task 6; treat absent as false) the console shows the "not enabled" empty state.
  - `useMailTemplates(organizationId, kind?: 'standard' | 'transactional')`.
- Templates tab (spec §13.3): left list (name, key, "contains secrets" badge); right: subject, text body, variable chips (secret chips use the warning token and "· secret"), sample-value note, HTML preview via existing `renderTemplate` + `MailHtmlFrame` (there is no `/api/mail-template-previews` BFF route; the existing editor renders in the browser). Owner/admin see "New" and "Edit" opening `TransactionalTemplateDialog` (name, key, from: verified domain select + local part, from name, subject, text, HTML, variable table with name/required/secret; fallback input disabled for secrets). `409 MAILBOX.TEMPLATE_KEY_TAKEN` shows on the key field; `MAILBOX.TEMPLATE_INVALID` as a form error.

- [ ] **Step 1: Write failing tests:** `developer console keeps tab and mode in the URL`; `member sees transactional templates read-only`; `admin edits a transactional template and secret fallback is disabled`; `key taken shows inline error`; `templates page lists only standard templates`.
- [ ] **Step 2: Run** `cd web && corepack pnpm vitest run src/test/developer-templates-ui.test.tsx src/test/mail-templates-ui.test.tsx` — Expected: FAIL.
- [ ] **Step 3: Implement** with shadcn primitives and theme tokens, layout after `Developer.dc.html` (two columns ≥ 1024 px, stacked below); all copy in both locales.
- [ ] **Step 4: Run** Step 2 command and `cd web && corepack pnpm type-check && corepack pnpm lint` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(web): developer console with transactional templates`.

## Slice S3 — Transactional sending core

### Task 6: S3 contract — messages, log, test sends, key mode, event routing

**Files:**
- Modify: `contracts/openapi.yaml`, `contracts/MAILBOXES.md` (new `## Transactional Mail` section, `## Programmatic Sending`, `## Delivery Events`, Errors), `contracts/API_KEYS.md`
- Generate: web/admin `openapi.ts`
- Modify: `web/src/features/api-key/schemas.ts`, `web/src/features/api-key/server/mock-api-key-store.ts`, `web/src/features/mailbox/schemas.ts` (`mailboxDomainSchema`), `mock-mailbox-store.ts`, `web/src/http/codes.ts`

**Interfaces:**
- Produces (spec §9.1–9.5, 9.7, 9.9, 9.11, 9.15):
  - `POST /v1/transactional-messages` (`X-API-Key`, `Organization-Id`, required `Idempotency-Key`; body `TransactionalMessageRequest { template, to, variables }`; `202` new / `200` replay, `Location` header) and `GET /v1/transactional-messages/{message_id}`; schema `TransactionalMessage { id, mode, simulated, template, from: {address, name}, to, to_masked, subject, variables, status, queued_reason, failure_code, last_event, attempts, idempotency_key, created_at, sent_at, events: [{type, detail, occurred_at}] }`.
  - `GET /v1/transactional-mail-log` (query `mode`, `status`, `last_event`, `outcome` = `delivered|problem`, `template`, `to`, `since`, `until`, `cursor`, `per_page` ≤ 100) → items `TransactionalLogEntry` (no `to`, no `variables`), `meta { next_cursor, total_24h }`; `GET /v1/transactional-mail-log/{message_id}` adds `from`, `variables`, `events`.
  - `POST /v1/transactional-test-sends` body `{ template, mode, variables }` → `202 TransactionalMessage`.
  - `CreateAPIKeyRequest.mode` optional `live|test`; `APIKey.mode` required.
  - `MailboxDomain` adds `transactional_enabled`, `transactional_recipient_limit_per_minute`, `transactional_key_limit_per_second`, `simulator_domain`.
  - Delivery-event response adds `message_kind` (`mailbox|transactional`).
  - Error codes: `404 MAILBOX.TRANSACTIONAL_MESSAGE_NOT_FOUND`, `429 MAILBOX.RECIPIENT_RATE_LIMITED`, `503 MAILBOX.TRANSACTIONAL_DISABLED` (plus Task 3's).
  - `API_KEYS.md`: modes, `fastbuild_test_` prefix, test keys refused with `403 PERMISSION.DENIED` on every route not declaring test support; `MAILBOXES.md`: everything in spec §9.1–9.5 incl. the error table and the check order of §9.1.
  - Web: `apiKeySchema` gains `mode`; mock creates `fastbuild_test_<hex>` for `mode: 'test'`; `ApiErrorCode` gains the three new codes.

- [ ] **Step 1:** Write the contract text and schemas.
- [ ] **Step 2:** Update the web schemas, mocks and codes.
- [ ] **Step 3: Run** `cd contracts && corepack pnpm check && corepack pnpm check:registry && corepack pnpm generate && cd ../web && corepack pnpm type-check && corepack pnpm vitest run src/test/api-key-contract.test.ts src/test/api-key-route.test.ts` — Expected: PASS. Two checks are expected to fail until later tasks and are not fixed here: `corepack pnpm check:routes` (the new paths have no runtime route until Tasks 10, 12 and 13; Task 13 Step 4 runs the full `make contract-check`) and `cd api && go test ./internal/modules/apikey/... -run OpenAPI` (`TestAPIKeyDTOFieldsMatchOpenAPI` wants `mode`; Task 8 makes it pass).
- [ ] **Step 4: Commit** `feat(contracts): transactional messages, send log, and test API keys`.

### Task 7: Transport reports whether the provider answered

**Files:**
- Modify: `api/internal/infra/mailtransport/transport.go` (`Error`), `cloudflare.go` (~L147–188), `log.go`
- Test: `api/internal/infra/mailtransport/cloudflare_test.go`

**Interfaces:**
- Produces: `mailtransport.Error.Responded bool` — `true` only when an HTTP status was received: the `provider.http_*` branch, `transport.permanent_bounce`, `transport.suppressed`; `false` for `transport.unreachable`, `transport.invalid_response`, `transport.response_too_large`. Personal and broadcast paths do not read it.

- [ ] **Step 1: Write failing test** `TestCloudflareErrorRespondedOnlyWithHTTPStatus` (table: 503 → `Responded=true, Permanent=false`; 400 → `true, true`; connection refused, oversized body, invalid JSON on 200 → `false`; all-bounced 200 → `true`).
- [ ] **Step 2: Run** `cd api && go test ./internal/infra/mailtransport/... -run Responded` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `cd api && go test ./internal/infra/mailtransport/... ./internal/modules/mailbox/...` — Expected: PASS with no change to existing assertions.
- [ ] **Step 5: Commit** `feat(mailtransport): tell whether the provider returned an HTTP status`.

### Task 8: API key mode and default refusal of test keys

**Files:**
- Create: `api/database/migrations/2026_11_23_090000_add_api_key_mode.go`
- Modify: `api/database/migrations/testdata/schema.golden.sql`, `api/internal/modules/apikey/provider.go` (manifest), `model.go`, `dto.go`, `service.go` (`CreateForUser` ~L56), `handler.go` (`RegisterMiddleware` ~L37), `scope_middleware.go`, `api/internal/domain/api_key.go`, `api/internal/starter/defaults_test.go` (every `assert.Len(t, migrations, N)` +1), `api/internal/modules/mailbox/routes.go` (unchanged call sites keep refusing test keys)
- Test: `api/internal/modules/apikey/scope_middleware_test.go`, `service_test.go`, `handler_middleware_test.go`, `openapi_contract_test.go`

**Interfaces:**
- Produces:
  - `type APIKeyMode string` in domain: `APIKeyModeLive = "live"`, `APIKeyModeTest = "test"`; `domain.APIKey.Mode`.
  - `APIKeyCreateRequest.Mode string \`json:"mode,omitempty" binding:"omitempty,oneof=live test"\``; `APIKeyResponse.Mode string \`json:"mode"\``.
  - Context value `apiKeyModeContextKey = "apiKeyMode"` set in `RegisterMiddleware`; `func APIKeyModeFromContext(c *gin.Context) domain.APIKeyMode` (default live).
  - `RequireScopes(scopes ...string)` keeps its signature and now aborts `403 PERMISSION.DENIED` for a test key; new `RequireScopesAllowingTest(scopes ...string) gin.HandlerFunc` is the opt-in (D36; chosen over an options parameter so the existing call sites stay as they are).
  - Audit `create api_keys` change map gains `"mode"`.
- Migration: `ALTER TABLE api_keys ADD COLUMN mode varchar(8) NOT NULL DEFAULT 'live' CHECK (mode IN ('live','test'))`; Down drops it.

- [ ] **Step 1: Write failing tests:** `TestCreateTestKeyUsesTestPrefix` (prefix matches `^fastbuild_test_[a-z0-9_-]{8}$`, `mode=test`); `TestCreateKeyDefaultsToLive`; `TestRequireScopesRefusesTestKey` (test key with `mail:send` → 403 `PERMISSION.DENIED`); `TestRequireScopesAllowingTestAcceptsBothModes`; `TestAPIKeyDTOFieldsMatchOpenAPI` (now passes with `mode`); migration Up/Down via the golden test; `TestDefaultManifests*` counts.
- [ ] **Step 2: Run** `cd api && FASTBUILD_TEST_POSTGRES_DSN=... go test ./internal/modules/apikey/... ./internal/starter/... ./database/migrations/...` — Expected: FAIL.
- [ ] **Step 3: Implement.** Add an `UPGRADING.md` note: revoke test keys before rolling back this migration.
- [ ] **Step 4: Run** Step 2 command and `cd api && make route-catalog-check` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(apikey): immutable live and test key modes`.

### Task 9: Transactional and suppression schema and repository

**Files:**
- Create: `api/database/migrations/2026_11_23_110000_create_transactional_messages.go`, `2026_11_23_120000_create_mail_suppressions.go`, `api/database/migrations/transactional_messages_test.go`; `api/internal/domain/transactional_mail.go`; `api/internal/modules/mailbox/transactional_model.go`, `transactional_repository.go`, `suppression_repository.go`, `transactional_repository_test.go`
- Modify: `schema.golden.sql`, `api/internal/modules/mailbox/provider.go` (manifest + `wire.Bind(new(domain.TransactionalMailRepository), new(*repository))`)

**Interfaces:**
- Produces (domain, `transactional_mail.go`):
  - `TransactionalStatus` (`queued|sent|failed|suppressed`), `TransactionalQueuedReason` (`send_limit|retry`).
  - `TransactionalMessage { ID, OrganizationID uint; Mode APIKeyMode; Simulated bool; APIKeyID *uint; CreatedBy uint; TemplateID *uint; TemplateKey, FromAddress, FromName, ToAddress, SubjectMasked string; VariablesMasked map[string]string; PayloadCiphertext []byte; IdempotencyKey *string; IdempotencyFingerprint []byte; Status TransactionalStatus; QueuedReason *TransactionalQueuedReason; Attempts int; NextAttemptAt, LeaseUntil, SendAttemptedAt, SentAt *time.Time; ProviderMessageID *string; RFCMessageID, FailureCode string; LastEvent MailEventType; CreatedAt, UpdatedAt time.Time; Events []MailEvent }`.
  - `TransactionalOutcome { Status TransactionalStatus; FailureCode string; ProviderMessageID string; SentAt *time.Time }`.
  - `TransactionalLogFilter { Mode APIKeyMode; Status, LastEvent, Outcome, TemplateKey, To string; Since, Until time.Time; Cursor uint; PerPage int }`.
  - `MailSuppression { ID, OrganizationID uint; Email, Reason, Source string; CreatedAt time.Time }`; `MailSuppressionFilter { Email, Reason string; Cursor uint; PerPage int }`.
  - `TransactionalMailRepository` interface:
    - `CreateTransactional(ctx, m *TransactionalMessage) (bool, error)` — `ON CONFLICT (organization_id, mode, idempotency_key) DO NOTHING`; false when a concurrent winner exists.
    - `FindTransactionalByIdempotencyKey(ctx, organizationID uint, mode APIKeyMode, key string) (*TransactionalMessage, error)`; `ReleaseIdempotencyKey(ctx, id uint) error`.
    - `FindTransactional(ctx, organizationID, id uint) (*TransactionalMessage, error)`; `FindTransactionalByProviderID(ctx, providerMessageID string) (*TransactionalMessage, error)`.
    - `CountTransactionalAttemptsSince(ctx, organizationID uint, since time.Time) (int64, error)`; `CountTransactionalWaiting(ctx, organizationID uint) (int64, error)` (`queued_reason = 'send_limit'`).
    - `LeaseTransactional(ctx, organizationID uint, ids []uint, now, leaseUntil, windowStart time.Time, laneLimit, limit int) ([]*TransactionalMessage, error)` — under `pg_advisory_xact_lock(hashtext('zmail.transactional_send'), organization_id)`, `FOR UPDATE SKIP LOCKED`, due (`next_attempt_at <= now`, lease free); live non-simulated rows only while lane room (`laneLimit − attempts since windowStart − held leases`) remains, the rest get `queued_reason = 'send_limit'`; test/simulated rows always lease. `ids` empty = oldest due by `(next_attempt_at, id)`.
    - `TransactionalDueOrganizations(ctx, now time.Time, limit int) ([]uint, error)`.
    - `StartTransactionalAttempt(ctx, id uint, lease, at time.Time) (bool, error)` — CAS on `status='queued' AND lease_until = lease`; sets `send_attempted_at`, `attempts+1`, clears `queued_reason`.
    - `FinishTransactional(ctx, id uint, lease time.Time, outcome TransactionalOutcome) (bool, error)` — CAS; wipes ciphertext, lease, queue fields.
    - `ScheduleTransactionalRetry(ctx, id uint, lease, next time.Time, failureCode string) (bool, error)` — CAS; `queued_reason='retry'`, clears lease.
    - `ExpireTransactional(ctx, now time.Time, limit int) ([]*TransactionalMessage, error)` — `status='queued' AND created_at < now-1h` and lease free → failed `send.expired`.
    - `FailUnknownTransactional(ctx, now time.Time, limit int) ([]*TransactionalMessage, error)` — `status='queued' AND lease_until <= now AND send_attempted_at > lease_until - interval '2 minutes'` (attempt started inside the lost lease) → failed `transport.unknown_outcome`.
    - `PurgeTransactional(ctx, before time.Time, limit int) (int, error)`.
    - `AddTransactionalEvents(ctx, id uint, events []MailEvent) (int, MailEventType, error)` — same dedupe and `SummarizeMailEvents` recomputation as `AddMessageEvents`.
    - `ListTransactionalEvents(ctx, id uint, limit int) ([]MailEvent, error)`.
    - `ListTransactionalLog(ctx, organizationID uint, filter TransactionalLogFilter) ([]*TransactionalMessage, error)` (keyset `id < cursor ORDER BY id DESC LIMIT per_page+1`); `CountTransactionalSince(ctx, organizationID uint, mode APIKeyMode, templateKey string, since time.Time, cap int) (int64, error)`.
    - `IsSuppressed(ctx, organizationID uint, email string) (bool, error)`; `AddSuppression(ctx, s MailSuppression) error` (`ON CONFLICT (organization_id, email) DO NOTHING`); `ListSuppressions(ctx, organizationID uint, filter MailSuppressionFilter) ([]MailSuppression, error)`; `DeleteSuppression(ctx, organizationID, id uint) (*MailSuppression, error)` (nil when absent).
- Migrations exactly per spec §7.3 and §7.5 (columns, three check constraints, all listed indexes, events table with `UNIQUE (provider_event_id) WHERE NOT NULL`); suppression backfill from `mail_contacts` with `status = 'suppressed' AND status_reason IN ('bounced','complained')`, `lower(email)`, `source='backfill'`.

- [ ] **Step 1: Write failing tests:** `TestTransactionalMigrationsUpDown`; `TestTerminalRowRejectsCiphertext` (UPDATE to `sent` with ciphertext violates the check); `TestSimulatedRowRejectsAttemptTime`; `TestSuppressionBackfillTakesOnlyBouncedAndComplained`; `TestIdempotencyUniqueUnderConcurrency` (two goroutines, same key → one row, other returns false); `TestReleasedKeyCanBeReused`; `TestLeaseRespectsLaneRoomAndSkipsLocked` (limit 2, 1 attempt in window, 3 due → 1 leased, others `send_limit`; two concurrent leasers never exceed); `TestStaleLeaseHolderCannotFinish` (CAS false); `TestFailUnknownOnlyAfterAttemptInsideLostLease`; `TestExpireAfterOneHour`; `TestPurgeDeletesEventsAfterThirtyDays` (batch bounded); `TestLogKeysetPaginationStable` (insert between pages: no repeat, no gap); `TestSuppressionUniquePerOrganization`.
- [ ] **Step 2: Run** `cd api && FASTBUILD_TEST_POSTGRES_DSN=... go test ./database/migrations/... ./internal/modules/mailbox/... -run 'Transactional|Suppression|Lease|Purge|Keyset|Idempotency'` — Expected: FAIL.
- [ ] **Step 3: Implement** with migration-local SQL and GORM POs (`TransactionalMessagePO`, `TransactionalMessageEventPO`, `MailSuppressionPO`), explicit PO↔domain conversion.
- [ ] **Step 4: Run** Step 2 command — Expected: PASS.
- [ ] **Step 5: Commit** `feat(mailbox): transactional message and suppression storage`.

### Task 10: Send a transactional message (key route and console test send)

**Files:**
- Create: `api/internal/modules/mailbox/transactional_service.go`, `transactional_mask.go`, `transactional_handler.go`, `transactional_test.go`, `transactional_mask_test.go`, `transactional_handler_test.go`
- Modify: `api/internal/infra/config/config.go`, `api/.env.example`, `api/internal/modules/mailbox/service.go` (`Settings`, `SettingsFromConfig`, `NewService`, `Service` interface), `provider.go`, `routes.go`, `errors.go`, `error_mappings.go`, `dto.go` + `handler.go` (mailbox-domain fields), `api/internal/domain/error_codes.go`, `api/internal/wiring/wire.go` → `make wire`, every `NewService(` call in `api/internal/modules/mailbox/*_test.go`

**Interfaces:**
- Consumes: Task 4 `FindTemplateByKey`, `validateTransactionalTemplate`, `MailTemplate.Variables`; Task 8 `RequireScopesAllowingTest`, `APIKeyModeFromContext`, context `apiKeyID`; Task 9 repository.
- Produces:
  - `NewService(..., transactional domain.TransactionalMailRepository, limits *ratelimit.Stores, accounts accountDirectory)` appended after `settings`; `type accountDirectory interface { FindByID(ctx context.Context, id uint) (*domain.User, error) }` bound to `domain.UserRepository` in `ProviderSet`. Tests pass `ratelimit.NewMemoryStores()`.
  - `Settings` gains `TransactionalEnabled bool`, `SecretEncryptionKey`, `SimulatorDomain string`, `TransactionalRecipientPerMinute`, `TransactionalKeyPerSecond int`; config validation: key ≥ 32 when enabled, simulator domain passes `ValidMailboxDomain`, per-minute 1–1000, per-second 1–10000.
  - `transactional_mask.go`: `maskSecret(value string) string`, `maskRecipient(address string) string`, `transactionalFingerprint(key []byte, templateKey, to string, variables map[string]string) []byte` (HMAC-SHA256 with a key derived as `HMAC-SHA256(SHA256(secret), "transactional-idempotency")`; input is the JSON of `{template, to, variables}`), `simulatorOutcome(to, simulatorDomain string) (outcome string, simulated bool, ok bool)`.
  - `type TransactionalActor struct { OrganizationID, UserID uint; APIKeyID *uint; Mode domain.APIKeyMode }`; `type TransactionalSendInput struct { Template, To, IdempotencyKey string; Variables map[string]string }`.
  - `func (s *service) SendTransactional(ctx, actor TransactionalActor, input TransactionalSendInput) (*domain.TransactionalMessage, bool, error)` — order exactly spec §9.1: enabled → key limit → template + declared variables + fingerprint → idempotency lookup (replay `false`) → recipient limit → sender verified → render (empty string counts as missing) → mask → suppression → lane/waiting check → insert (+ `TransactionalNotification` `email.suppressed` when suppressed) → after commit `s.background(func(){ s.DispatchTransactionalMessage(...) })` when queued without reason.
  - `func (s *service) GetTransactional(ctx, organizationID uint, mode domain.APIKeyMode, id uint) (*domain.TransactionalMessage, error)` (mode mismatch → not found; ≤ 50 events).
  - `func (s *service) SendTransactionalTest(ctx, viewer domain.OrganizationContext, input TransactionalTestInput) (*domain.TransactionalMessage, error)` with `TransactionalTestInput { Template string; Mode domain.APIKeyMode; Variables map[string]string }`; recipient = account email; variables = sample values overlaid; key `test-send:<user_id>:<random hex>`.
  - `type TransactionalNotification struct { Type string; Message *domain.TransactionalMessage; BounceType, Reason, Key string; OccurredAt time.Time }`; `mailNotifier` gains `NotifyTransactional(ctx, TransactionalNotification) error` — the webhook implementation returns nil until Task 16; `fakeNotifier` records them.
  - `type RetryAfterError struct { Err error; RetryAfter time.Duration }` (`Unwrap` → `Err`); handlers set `Retry-After` from it. Sentinels and mappings: `ErrTransactionalDisabled` (503 `MAILBOX.TRANSACTIONAL_DISABLED`), `ErrTransactionalMessageNotFound` (404 `MAILBOX.TRANSACTIONAL_MESSAGE_NOT_FOUND`), `ErrMailRecipientRateLimited` (429 `MAILBOX.RECIPIENT_RATE_LIMITED`), `ErrTransactionalRateLimited` (429 `domain.CodeRateLimited`, used by the key and test-send limits).
  - Routes: key group `POST /transactional-messages` (`transactional_messages.store`, `middleware.BodyLimit(64 << 10)`, `apikey.RequireScopesAllowingTest(ScopeMailTransactional)`), `GET /transactional-messages/:message_id` (`transactional_messages.show`); session group `POST /transactional-test-sends` (`transactional_test_sends.store`). `ScopeMailTransactional = "mail:transactional"`.

- [ ] **Step 1: Write failing tests** (service with fake repo + `fakeTransport` and the real Postgres repo where noted):
  - `TestMaskSecret` (`482913` → `••••••`; `https://u:p@reset.acme.com/t?code=1` → `reset.acme.com/••••`; `reset.acme.com/t` → `••••••`) — Review Focus 2; `TestMaskRecipient` (`lilei@` → `li••@`, `ab@` → `a••@`).
  - `TestSecretNeverStoredInClear` (Postgres): send `code=482913` → no column but `payload_ciphertext` contains `482913` after decrypt; `subject_masked == "Acme 验证码：••••••"`.
  - `TestIdempotentReplayReturnsFirstMessage` (200, same id, one row); `TestIdempotencyConflictOnDifferentContent` (409); `TestExpiredKeyIsReleasedAndReused` (25 h later → new id, old key NULL); `TestLiveAndTestKeysHaveSeparateIdempotency` and `TestKeyReadsOnlyItsOwnMode` (Review Focus 3).
  - `TestRecipientNormalizedEverywhere` (`" LiLei@Example.COM "` hits a suppression on `lilei@example.com`, same fingerprint as lower case) — Review Focus 1.
  - `TestEmptyRequiredValueIsMissing` (Review Focus 4); `TestUndeclaredVariableRefused` (422 `MAILBOX.MESSAGE_INVALID`, field `variables.extra`); `TestUnknownSimulatorLocalPartRefused`.
  - `TestRecipientRateLimitFourthIn60Seconds` (429 + `Retry-After`; a replay with the same key does not consume budget); `TestKeyRateLimitTwentyFirstInOneSecond`.
  - `TestSuppressedRecipientStoredWithoutCiphertext` (202, `suppressed`, notification `email.suppressed`, no lane count).
  - `TestTransactionalLaneFullQueuesThenRefuses` (limit 2 → 3rd `queued_reason=send_limit`; waiting = limit → 429 `MAILBOX.SEND_LIMIT_REACHED`); `TestBroadcastLaneFullDoesNotDelayTransactional`.
  - `TestTestKeyNeverCallsProvider` (`fakeTransport.sent` empty, lane count 0).
  - Handler: missing scope → 403; test key on `POST /v1/outbound-mail-messages` → 403; missing `Idempotency-Key` → 422 `MAILBOX.IDEMPOTENCY_KEY_INVALID`; disabled → 503 `MAILBOX.TRANSACTIONAL_DISABLED`; body > 64 KiB → 413; `Location` header on 202; test send goes to the caller's account email and ignores any `to` in the body; test-send sixth per minute → 429.
- [ ] **Step 2: Run** `cd api && FASTBUILD_TEST_POSTGRES_DSN=... go test ./internal/modules/mailbox/... ./internal/infra/config/... -run 'Transactional|Mask|Recipient|Idempotent|Idempotency|KeyRate|TestKey|EnvExample'` — Expected: FAIL.
- [ ] **Step 3: Implement**, then `cd api && make wire && make route-catalog-check`.
- [ ] **Step 4: Run** Step 2 command and `cd api && go test ./internal/modules/mailbox/... ./internal/wiring/...` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(mailbox): send transactional mail with masked secrets and idempotency`.

### Task 11: Dispatch, retry, expiry, and the worker

**Files:**
- Create: `api/internal/modules/mailbox/transactional_dispatch.go`, `transactional_dispatch_test.go`
- Modify: `api/internal/domain/mailbox.go` (`MailBroadcastDispatcher` ~L615), `api/internal/bootstrap/operatorcommands/mail_broadcast.go` (`DispatchWebhooks`, `Description`), `mail_broadcast_test.go`, `api/internal/infra/metrics/metrics.go`, `metrics_test.go`
- Create: `api/internal/modules/mailbox/transactional_logging_test.go`

**Interfaces:**
- Consumes: Task 9 lease/CAS methods, Task 7 `Responded`, Task 10 notifications and `simulatorOutcome`.
- Produces:
  - `func (s *service) DispatchTransactionalMessage(ctx context.Context, id uint) error` and `func (s *service) DispatchTransactional(ctx context.Context, limit int) (int, error)` — both follow spec §12 pseudocode: lease → re-check suppression (→ `suppressed`, `email.suppressed`) → sender domain verified (→ `failed` `send.sender_unverified`) → decrypt (→ `failed` `send.payload_unavailable`) → test/simulated: finish per simulator table below → else `StartTransactionalAttempt`, provider call outside any transaction with `Message-ID = rfc_message_id` and no `Reply-To`, record with `context.WithoutCancel` + `deliveryRecordTimeout`.
  - `DispatchTransactional` first runs `ExpireTransactional` and `FailUnknownTransactional` (each publishing `email.failed`), then leases per due organization.
  - `func (s *service) PurgeExpiredTransactional(ctx context.Context, now time.Time, limit int) (int, error)` (30 days).
  - `MailBroadcastDispatcher` gains `DispatchTransactional` and `PurgeExpiredTransactional`; worker order per tick: transactional, queued sends, broadcasts, expired trash, expired transactional — each with what the batch has left.
  - `func transactionalRetryDelay(attempts int) (time.Duration, bool)` → 1: 30 s, 2: 2 min, 3: 10 min, ≥ 4: false.
  - Simulator outcomes: `delivered` → sent, events accepted+delivered; `bounced` → sent, accepted+bounced; `soft-bounced` → sent, accepted+deferred; `complained` → sent, accepted+delivered+complained; `suppressed` → suppressed, event rejected. Every simulated event has `detail = "simulated"`; notifications use keys `sim:<n>`.
  - Metrics in `internal/infra/metrics` (spec §15): `RecordTransactionalMessage(mode, outcome string)` → `zmail_transactional_messages_total{mode,outcome}`; `ObserveTransactionalDispatch(d time.Duration)` → `zmail_transactional_dispatch_seconds` (stored → accepted); `SetTransactionalQueueOldest(seconds float64)` → `zmail_transactional_queue_oldest_seconds`; `RecordSendLaneAttempt(lane string)` → `zmail_send_lane_attempts_total{lane}`; `RecordTransactionalRateLimited(rule string)` → `zmail_transactional_rate_limited_total{rule}` (called from Task 10's limiters). Structured events `transactional_message.accepted|dispatched|sent|retry_scheduled|failed|suppressed|expired|unknown_outcome|purged` and `transactional_payload.decrypt_failed`.
  - Provider results: accepted → `sent`, event `accepted` (+ per-recipient events from `sendEvents`), `email.sent`; `transport.suppressed` → `suppressed`, `email.suppressed`, no `mail_suppressions` row; `Permanent=false && Responded=true` → retry or, past the last delay, `failed` with the provider code; `Permanent=true` → `failed`; anything else → `failed` `transport.unknown_outcome`. Each `failed` → `email.failed`.

- [ ] **Step 1: Write failing tests:** `TestDispatchSendsOnceUnderConcurrentDispatchers` (two goroutines on one id, Postgres → one provider call); `TestRetryOn503ThenFail` (four 503s → calls 4, delays 30 s/2 min/10 min, `failed`); `TestNoRetryWithoutHTTPStatus` (unreachable, invalid_response, response_too_large → 1 call, `transport.unknown_outcome`); `TestCrashAfterProviderCallIsNotResent` (lease expired after attempt → `transport.unknown_outcome`, 0 calls); `TestQueuedOverOneHourExpires`; `TestSenderUnverifiedBeforeDispatch` (Review Focus 5: 0 calls, ciphertext NULL); `TestUndecryptablePayloadFails`; `TestRecipientSuppressedAfterEnqueue`; `TestSimulatorOutcomes` (table over the five local parts incl. `+tag` and upper case; never writes `mail_suppressions`); `TestLaneFullMessageWaitsThenSends`; `TestAcceptedWipesCiphertext`; `TestPurgeExpiredTransactional`; `TestDisabledFeatureStillDrainsQueue` (`TransactionalEnabled=false` → queued messages still dispatch); worker test `TestMailWorkerDispatchesTransactionalFirst` (calls order transactional, queued, broadcasts, purge, purge-transactional); `TestTransactionalLogsCarryNoPersonalData` (parses `transactional_*.go` with `go/ast` and fails on any `slog` attribute key outside `request_id, organization_id, message_id, template_id, mode, status, failure_code, attempts, duration_ms, count`); metric registration test in `metrics_test.go`.
- [ ] **Step 2: Run** `cd api && FASTBUILD_TEST_POSTGRES_DSN=... go test ./internal/modules/mailbox/... ./internal/bootstrap/operatorcommands/... -run 'Dispatch|Retry|Crash|Expire|Simulator|Purge|MailWorker'` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** Step 2 command and `cd api && make wire-check` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(mailbox): dispatch, retry and expire transactional mail in the worker`.

### Task 12: Send log read routes

**Files:**
- Create: `api/internal/modules/mailbox/transactional_log.go`, `transactional_log_test.go`
- Modify: `routes.go`, `transactional_handler.go`, `dto.go`

**Interfaces:**
- Consumes: Task 9 `ListTransactionalLog`, `CountTransactionalSince`, `FindTransactional`, `ListTransactionalEvents`.
- Produces:
  - `func (s *service) ListTransactionalLog(ctx, organizationID uint, filter domain.TransactionalLogFilter) (TransactionalLogPage, error)` with `TransactionalLogPage { Items []*domain.TransactionalMessage; NextCursor *uint; Total24h int64 }` (`Total24h` capped at 10000).
  - `func (s *service) GetTransactionalLogEntry(ctx, organizationID, id uint) (*domain.TransactionalMessage, error)`.
  - Routes (session group): `GET /transactional-mail-log` (`transactional_mail_log.index`), `GET /transactional-mail-log/:message_id` (`transactional_mail_log.show`); any member; `privateResponse`.
  - Validation → `400 COMMON.INVALID_INPUT`: enums, `to` ≤ 320 (lower-cased), range ≤ 30 days (default last 7), `per_page` 1–100 (default 50). `outcome=delivered` ⇒ `last_event='delivered'`; `outcome=problem` ⇒ `last_event IN (bounced, complained, failed, rejected) OR status IN (failed, suppressed)`; combined with `status`/`last_event` as an intersection. DTOs never contain `to` or variables in the list; the detail adds `from`, masked `variables`, `events`.

- [ ] **Step 1: Write failing tests:** `TestLogMasksRecipientAndOmitsVariables`; `TestLogFiltersByExactRecipientCaseInsensitive` (Review Focus 1); `TestLogOutcomeProblemIntersectsStatus`; `TestLogRangeOverThirtyDaysRefused`; `TestLogTotal24hCapped`; `TestLogEntryOtherOrganizationNotFound`; `TestTransactionalMailStaysOutOfMailboxLogAndStats` (`GET /v1/mail-messages` and stats unchanged after a transactional send).
- [ ] **Step 2: Run** `cd api && FASTBUILD_TEST_POSTGRES_DSN=... go test ./internal/modules/mailbox/... -run 'Log'` — Expected: FAIL.
- [ ] **Step 3: Implement**, then `cd api && make route-catalog-check`.
- [ ] **Step 4: Run** Step 2 command — Expected: PASS.
- [ ] **Step 5: Commit** `feat(mailbox): masked transactional send log`.

### Task 13: Delivery events, shared suppression list, and broadcast checks

**Files:**
- Modify: `api/internal/modules/mailbox/delivery_event.go` (`RecordDeliveryEvent` ~L135), `handler.go` (`ReceiveDeliveryEvent` ~L484), `dto.go` (`DeliveryEventResponse`), `broadcast_service.go` (`run.send` ~L470–560)
- Test: `api/internal/modules/mailbox/transactional_events_test.go`, `broadcast_test.go`

**Interfaces:**
- Consumes: Task 9 `FindTransactionalByProviderID`, `AddTransactionalEvents`, `AddSuppression`, `IsSuppressed`.
- Produces:
  - `RecordDeliveryEvent(ctx, delivery DeliveryEvent) (DeliveryEventResult, error)` with `DeliveryEventResult { Kind string /* "mailbox" | "transactional" */; MessageID uint; Event domain.MailEvent; Recorded bool }`; lookup `mail_messages` first, then `transactional_messages`, else `ErrMailMessageNotFound`.
  - Transactional branch (one transaction): add event (dedupe by `provider_event_id`), recompute `last_event`; for non-simulated `bounced`/`complained` → `AddSuppression{Source: "transactional"}`; notification `email.delivered|bounced(permanent)|bounced(temporary for deferred)|complained|suppressed(rejected)|failed` with key `event:<provider_event_id>`, only when inserted.
  - Mailbox branch unchanged, plus a broadcast message with `bounced`/`complained` → `AddSuppression{Source: "broadcast"}` (contact status untouched beyond today's rule).
  - Broadcast dispatch: after the contact-status check, `IsSuppressed(org, lower(contact.Email))` → `finish(domain.MailRecipientSkipped, 0, "contact_suppressed")` without touching the contact.
  - `DeliveryEventResponse.MessageKind string \`json:"message_kind"\``.

- [ ] **Step 1: Write failing tests:** `TestDeliveryEventRoutesToTransactional` (201, `message_kind=transactional`, `last_event=delivered`); `TestDuplicateTransactionalEventPublishesOnce`; `TestOutOfOrderEventsKeepSeverity` (`delivered` after `bounced` keeps `bounced`); `TestPermanentBounceSuppressesAcrossLanes` (transactional bounce → next transactional send `suppressed`, broadcast recipient skipped `contact_suppressed`, contact status unchanged); `TestDeferredAndRejectedDoNotSuppress`; `TestSimulatedBounceNeverSuppresses`.
- [ ] **Step 2: Run** `cd api && FASTBUILD_TEST_POSTGRES_DSN=... go test ./internal/modules/mailbox/... -run 'DeliveryEvent|Suppress|OutOfOrder'` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** Step 2 command and `cd api && bash ../.agents/skills/verification-before-completion/scripts/run-tiers.sh 1 ./internal/modules/mailbox/...` — Expected: PASS. Then `make contract-check` — Expected: PASS (all S3 routes now exist).
- [ ] **Step 5: Commit** `feat(mailbox): route delivery events to transactional mail and share suppressions`.

### Task 14: S3 web — BFF routes, mock, services, hooks

**Files:**
- Create: `web/src/features/developer/schemas.ts`, `services/developer-service.ts`, `hooks/use-developer.ts`, `server/developer-route.ts`, `server/mock-transactional-store.ts`; `web/src/app/api/transactional-mail-log/route.ts`, `web/src/app/api/transactional-mail-log/[messageId]/route.ts`, `web/src/app/api/transactional-test-sends/route.ts`
- Modify: `web/src/features/api-key/services/api-key-service.ts`, `server/api-key-route.ts` (forward `mode`), `web/src/features/developer/types.ts`
- Test: `web/src/test/developer-route.test.ts`, `web/src/test/api-key-route.test.ts`

**Interfaces:**
- Consumes: Task 6 contract.
- Produces:
  - Schemas `transactionalLogEntrySchema`, `transactionalLogPageSchema`, `transactionalMessageSchema` (strict, `assertContract` against generated types).
  - `developerService.log(organizationId, filters)`, `.logEntry(organizationId, id)`, `.testSend(organizationId, { template, mode, variables })`.
  - Hooks `useTransactionalLog(organizationId, filters)` (refetch every 10 s while the page is visible), `useTransactionalLogEntry(...)`, `useTransactionalTestSend(organizationId)` (invalidates the log's first page).
  - Route handlers follow `mailbox-route.ts`: `resolveMailboxRoute`, `guardMockBffRoute()`, `guardSameOriginMutation(request)` on POST, `forwardAuthenticatedGoApi` in go-api mode, `privateMailboxResponse`. The key route `/v1/transactional-messages` gets no BFF route.
  - Mock store: masked entries, simulated events, `429 COMMON.RATE_LIMITED` on the sixth test send per minute, `503 MAILBOX.TRANSACTIONAL_DISABLED` when the mock domain flag is off.

- [ ] **Step 1: Write failing tests** for each route in mock and proxy mode (proxy asserts upstream method, path and forwarded query), mock test send for `test` mode never "delivers", `403`/`429`/`503` pass through with `error_code`, and `POST /api/api-keys` forwards `mode`.
- [ ] **Step 2: Run** `cd web && corepack pnpm vitest run src/test/developer-route.test.ts src/test/api-key-route.test.ts` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** Step 2 command and `cd web && corepack pnpm type-check && corepack pnpm lint` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(web): BFF routes for the transactional send log and test sends`.

### Task 15: S3 web — Quick start and Send log tabs

**Files:**
- Create: `web/src/features/developer/components/quick-start-tab.tsx`, `send-log-tab.tsx`, `send-log-detail.tsx`, `web/src/features/developer/utils/snippets.ts`
- Modify: `developer-console.tsx`, `web/src/config/env.ts` (`NEXT_PUBLIC_DEVELOPER_API_ORIGIN`, optional absolute URL), `web/.env.example`, i18n `developer` and `errors` modules, `web/src/features/api-key/components/api-key-panel.tsx` (show `mode`)
- Test: `web/src/test/developer-ui.test.tsx`, `web/src/test/developer-snippets.test.ts`

**Interfaces:**
- Consumes: Task 14 hooks; `apiKeyService.create`; `mailboxService.mailDomains`.
- Produces: `buildSnippets({ apiOrigin, organizationId, apiKey, template, variables }): { curl: string; node: string; python: string }` — native HTTP only (Node global `fetch`, Python `urllib.request`), headers `X-API-Key`, `Organization-Id`, `Idempotency-Key`; origin falls back to `https://api.example.com` with an operator hint.
- Quick start (spec §13.3): three steps (verified domain; own key whose scopes are exactly `["mail:transactional"]` in the current mode; a message exists in this mode); create key with `scopes: ['mail:transactional']`, `mode` from the page; plaintext shown once with copy (`aria-live` feedback), afterwards prefix + last 4; holder-leaves-organization warning; "Send a test" → `testSend`, then polls the entry for up to 30 s; test mode says rendered but not sent; `429` shows the retry seconds.
- Send log: chips All / Delivered / Bounced·Blocked / Test (switches `mode=test`), exact recipient search, template filter, filters in the URL; columns time, template, masked recipient, masked subject, status chip (queued, sent, delivered, bounced, blocked, failed, test — text, not color only), idempotency key; "Older" loads the next cursor; detail panel with event timeline and the secret note; banner "kept 30 days · never in any mailbox's Sent". The Templates tab (Task 5) now shows "sent in the last 24 h" for the selected template from `useTransactionalLog({ template, per_page: 1 }).meta.total_24h`, requested only for the selected one.

- [ ] **Step 1: Write failing tests:** `snippets carry the headers and never an SDK import`; `creating a key shows it once then only the prefix`; `test mode test send says nothing was delivered`; `log chips map to outcome and mode query`; `log row never shows a full address`; `older button requests next_cursor`; `disabled instance shows the not-enabled state`.
- [ ] **Step 2: Run** `cd web && corepack pnpm vitest run src/test/developer-ui.test.tsx src/test/developer-snippets.test.ts` — Expected: FAIL.
- [ ] **Step 3: Implement** after `Developer.dc.html`; responsive stack under 1024 px; code blocks scroll horizontally.
- [ ] **Step 4: Run** Step 2 command, `cd web && corepack pnpm type-check && corepack pnpm lint` — Expected: PASS. The controller walks quick start → create test key → send a test → log detail in the built-in browser in mock mode.
- [ ] **Step 5: Commit** `feat(web): developer quick start and transactional send log`.

## Slice S4 — Webhooks

### Task 16: S4 contract and API — `email.*` webhook events

**Files:**
- Modify: `contracts/MAILBOXES.md` (`### Webhooks` ~L1249: six `email.*` types, payload table, event-id rule, not gated by the mailbox webhook switch, test/simulated published, retry schedule from `WEBHOOKS.md`), `api/internal/modules/mailbox/webhook_events.go`
- Test: `api/internal/modules/mailbox/webhook_events_test.go`, `transactional_dispatch_test.go`

**Interfaces:**
- Consumes: Task 10 `TransactionalNotification` and `NotifyTransactional` call sites (Tasks 10, 11, 13).
- Produces:
  - Constants `WebhookEmailSent = "email.sent"`, `WebhookEmailDelivered`, `WebhookEmailBounced`, `WebhookEmailComplained`, `WebhookEmailSuppressed`, `WebhookEmailFailed`; `NewWebhookCatalog` adds them with `validateTransactionalWebhookPayload`.
  - `transactionalWebhookPayload { MessageID uint \`json:"message_id"\`; Template string \`json:"template"\`; To string \`json:"to"\`; Mode string \`json:"mode"\`; Simulated bool \`json:"simulated"\`; BounceType string \`json:"bounce_type"\`; Reason string \`json:"reason"\` }` — every field always present, `bounce_type ∈ {permanent, temporary, ""}`, no subject/body/variables; validation disallows unknown fields.
  - `func transactionalWebhookEventID(messageID uint, eventType, key string) string` → `t<id>:<type without "email.">[:<first 12 bytes of sha256(key) hex>]`.
  - `webhookNotifier.NotifyTransactional` publishes with `Source: "mailbox"` whenever the webhook starter is selected (no mailbox switch check).

- [ ] **Step 1: Write failing tests:** `TestTransactionalWebhookPayloadHasNoSecrets` (marshal for a `code=482913` message; payload lacks `482913` and the subject); `TestTransactionalWebhookEventIDsAreStable`; `TestCatalogListsEmailEvents`; `TestDuplicatePublishDeduplicated` (same notification twice → one `webhook_events` row, Postgres); `TestSimulatedBouncePublishesPermanentBounce`; `TestTransactionalWebhookIgnoresMailboxSwitch`.
- [ ] **Step 2: Run** `cd api && FASTBUILD_TEST_POSTGRES_DSN=... go test ./internal/modules/mailbox/... ./internal/modules/webhook/... -run 'Webhook|Catalog'` — Expected: FAIL.
- [ ] **Step 3: Implement**; then `make contract-check`.
- [ ] **Step 4: Run** Step 2 command — Expected: PASS.
- [ ] **Step 5: Commit** `feat(mailbox): publish email webhook events for transactional mail`.

### Task 17: S4 web — Webhooks tab

**Files:**
- Create: `web/src/features/developer/components/developer-webhooks-tab.tsx`
- Modify: `web/src/features/webhook/components/webhook-management.tsx` (optional `eventTypePrefix?: string` prop filtering the selectable types; default unchanged), `web/src/features/webhook/server/mock-webhook-store.ts` (`eventTypes()` adds the six `email.*` types), `developer-console.tsx`, i18n `developer` module (one description per event)
- Test: `web/src/test/developer-webhooks-ui.test.tsx`, `web/src/test/webhook-ui.test.tsx`

**Interfaces:**
- Consumes: `WebhookManagement({ organizationId, eventTypePrefix })`.
- Produces: tab showing `WebhookManagement` with `eventTypePrefix="email."` and a sample payload panel built from Task 16's exact fields (`message_id, template, to, mode, simulated, bounce_type, reason`); retry copy follows `WEBHOOKS.md` (1 min, 5 min, 15 min, 1 h …), not the prototype's 1/5/30.

- [ ] **Step 1: Write failing tests:** `webhooks tab offers only email events`; `sample payload has the contract fields and no code`; existing `webhook-ui.test.tsx` still passes without the prop.
- [ ] **Step 2: Run** `cd web && corepack pnpm vitest run src/test/developer-webhooks-ui.test.tsx src/test/webhook-ui.test.tsx` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** Step 2 command, `cd web && corepack pnpm type-check && corepack pnpm lint` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(web): transactional webhook subscriptions in the developer console`.

## Slice S5 — Limits, protection, and suppressions

### Task 18: S5 contract and API — lane usage and suppression management

**Files:**
- Modify: `contracts/openapi.yaml` (`GET /v1/mail-send-lanes`, `GET /v1/mail-suppressions`, `DELETE /v1/mail-suppressions/{suppression_id}`), `contracts/MAILBOXES.md`; `api/internal/modules/mailbox/routes.go`, `transactional_handler.go`, `dto.go`
- Create: `api/internal/modules/mailbox/suppression_service.go`, `send_lane_service.go`, `suppression_test.go`
- Generate: web/admin `openapi.ts`

**Interfaces:**
- Consumes: Task 2 `CountOutboundSince(lane)`, `CountWaitingSends`; Task 9 counts and suppression methods; `managerScope(c)`; `s.recordAudit`.
- Produces:
  - `func (s *service) ListSendLanes(ctx, viewer domain.OrganizationContext) ([]SendLaneUsage, error)` with `SendLaneUsage { Lane domain.SendLane; Limit int; Used, Waiting int64 }`, fixed order transactional, broadcast, personal; broadcast `Waiting` = pending recipients of broadcasts whose `stop_reason = 'send_limit_reached'`. Response `{ lanes, window_hours: 24, computed_at }`. Owner/admin only, else 403.
  - `func (s *service) ListSuppressions(ctx, viewer, filter domain.MailSuppressionFilter) ([]domain.MailSuppression, *uint, error)` (exact lower-cased `email`, `reason`, keyset `created_at DESC, id DESC`, `per_page` ≤ 100) and `func (s *service) RemoveSuppression(ctx, viewer, id uint) error` — `204` even when absent; audit `mail_suppression.removed` (target id, reason) only when a row was deleted.
  - Routes `mail_send_lanes.index`, `mail_suppressions.index`, `mail_suppressions.destroy` (session group, `managerScope`).

- [ ] **Step 1:** Contract changes; `cd contracts && corepack pnpm check && corepack pnpm generate` (`check:routes` passes only after Step 3 adds the routes).
- [ ] **Step 2: Write failing tests:** `TestMemberCannotReadLaneUsage` (403); `TestLaneUsageCountsEachLane`; `TestRemoveSuppressionIsIdempotentAndAudited` (second delete 204, one audit record); `TestRemoveSuppressionOtherOrganizationDiscloseNothing` (204, row kept); `TestRemovedAddressCanReceiveTransactionalAgain` (contact status unchanged).
- [ ] **Step 3: Run** `cd api && FASTBUILD_TEST_POSTGRES_DSN=... go test ./internal/modules/mailbox/... -run 'Lane|Suppression'` — Expected: FAIL; implement; run again — Expected: PASS; then `cd api && make route-catalog-check` and `make contract-check` — Expected: PASS.
- [ ] **Step 4: Commit** `feat(mailbox): lane usage and suppression list management`.

### Task 19: S5 web — Limits and protection tab

**Files:**
- Create: `web/src/features/developer/components/limits-tab.tsx`, `suppression-list.tsx`; `web/src/app/api/mail-send-lanes/route.ts`, `web/src/app/api/mail-suppressions/route.ts`, `web/src/app/api/mail-suppressions/[suppressionId]/route.ts`
- Modify: `developer-route.ts`, `mock-transactional-store.ts`, `developer-service.ts`, `use-developer.ts`, `schemas.ts`, `developer-console.tsx`, i18n `developer`
- Test: `web/src/test/developer-limits-ui.test.tsx`, `web/src/test/developer-route.test.ts`

**Interfaces:**
- Consumes: Task 18 contract; `mailboxService.domains` limits.
- Produces: `developerService.sendLanes`, `.suppressions(filter)`, `.removeSuppression(id)`; hooks `useSendLanes` (enabled only for `canManage`), `useMailSuppressions`, `useRemoveMailSuppression` (invalidates the list).
- UI: owner/admin see `used / limit` with a progress bar per lane; members see limits only and "usage is visible to admins"; read-only protection rules from `mailbox-domains` (recipient per minute, key per second, idempotency 24 h, suppression list, secret variables); suppression list (owner/admin) with exact-address search and a confirm dialog warning that removal may cause another bounce.

- [ ] **Step 1: Write failing tests:** route tests (mock + proxy, 403 pass-through, DELETE 204); `member sees limits but no usage`; `admin removes a suppression after confirming`; `protection rules show the configured values`.
- [ ] **Step 2: Run** `cd web && corepack pnpm vitest run src/test/developer-limits-ui.test.tsx src/test/developer-route.test.ts` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** Step 2 command, `cd web && corepack pnpm type-check && corepack pnpm lint && corepack pnpm build` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(web): send lane usage and suppression list in the developer console`.

### Task 20: Acceptance flow, operations docs, final verification

**Files:**
- Create: `api/tests/kest/transactional_mail.flow.md`
- Modify: `UPGRADING.md`, `api/docs/DEPLOYMENT.md` (`MAILBOX_SECRET_ENCRYPTION_KEY` kept apart from backups; `RATE_LIMIT_STORE=postgres` for multiple replicas; key rotation: disable, drain, rotate), `api/docs/OBSERVABILITY.md` (log events and metrics of spec §15), `docs/plans/transactional-mail.zh-CN.md` (status line)

- [ ] **Step 1:** Write the Kest flow against a local API with `MAILBOX_PROVIDER=log`, `MAILBOX_TRANSACTIONAL_ENABLED=true`, a 32+ char key, `MAILBOX_DAILY_TRANSACTIONAL_LIMIT=2`, disposable PostgreSQL: create a live and a test key via `/v1/api-keys`; send to `delivered@test.zmail.im` and `bounced@test.zmail.im` (scenario 8); replay and conflicting replay (3); fourth send to one recipient → 429 with `Retry-After` (4); fill the lane → `send_limit` then 429 (5); a test key on `/v1/outbound-mail-messages` → 403; log shows `••••••` and masked recipient (1). Only simulator addresses or the `log` provider — nothing leaves the machine.
- [ ] **Step 2: Run** the flow: `kest run tests/kest/transactional_mail.flow.md -e local --fail-fast --var "run_id=$(date +%s)"` — Expected: all steps pass.
- [ ] **Step 3:** Write the docs changes. `UPGRADING.md` lists the five migrations with their rollback costs (spec §7): `add_transactional_templates` Down deletes transactional templates; `create_transactional_messages` Down loses up to 30 days of transactional records; revoke test keys before `add_api_key_mode` Down; the new variables and the `MAILBOX_SECRET_ENCRYPTION_KEY` rotation steps.
- [ ] **Step 4: Run** `cd api && go test ./internal/infra/mailtransport/... ./internal/modules/mailbox/... ./internal/modules/apikey/... ./internal/modules/webhook/... ./database/... && make wire-check && make route-catalog-check`, `cd web && corepack pnpm vitest run src/test/developer-*.test.ts* && corepack pnpm type-check && corepack pnpm lint`, `make contract-check`, `make agent-check-changed` — Expected: PASS. `make check` runs once only before an explicit release.
- [ ] **Step 5: Commit** `docs(mailbox): transactional mail acceptance flow and operations notes`.
