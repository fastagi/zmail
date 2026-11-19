# Upgrading FastBuild

This guide lists changes that a downstream fork must review when it pulls a newer FastBuild. Each entry
has an impact level:

- **High**: the fork fails to build, or behavior changes, until it acts.
- **Medium**: builds and runs, but a documented rule changed; code that follows the old rule should
  be updated.
- **Low**: cleanup that only matters if the fork used the affected code.

Newest changes come first. Merge the upstream branch, run `make check`, then work through the
entries in order.

## Unreleased — Mailbox organize

### High — `DELETE` on a message moves it to the trash

`DELETE /v1/mailboxes/:mailbox_id/messages/:message_id` no longer erases a message. The first call
moves it to the trash (`placement: "trash"`, `trashed_at` set; needs `send`), and the message stays
there for 30 days unless it is restored. Only a message that is already in the trash is deleted for
good by `DELETE` (needs `organize`), and `POST /v1/mailboxes/:mailbox_id/trash-purges` empties the
whole trash (needs `organize`). Code that used `DELETE` to erase mail, for retention or privacy,
calls it twice or empties the trash afterwards. Trashing a message that waits for the send limit
cancels its send (`failed`, `send.canceled`); restoring it does not send it.

`organize` is a new flag in `access`: the owner of a personal mailbox and `manage` members of a
shared one have it. Organization owners and admins do not get it from their role.

### High — Run `mail:broadcast:work` to purge the trash

`fastbuild mail:broadcast:work` now also deletes trash older than 30 days, with what is left of each
batch after waiting sends and broadcast recipients. Without the worker, the trash is never purged.
Run it wherever the API runs, as `api/docs/DEPLOYMENT.md` describes, even if no broadcasts are sent.

### High — The migration needs `pg_trgm` and locks `mail_messages` while it builds indexes

Run migrations: `2026_11_21_090000_add_mail_organize` adds placements, folders, labels, stars, and
threading columns, and `2026_11_21_090100_backfill_mail_threads` threads the existing mail. The
first one:

- runs `CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public`. The migration role needs the
  CREATE privilege on the database (`pg_trgm` is a trusted extension from PostgreSQL 13), or an
  operator with the rights creates the extension beforehand. On managed PostgreSQL, allow the
  extension first if the provider lists extensions.
- builds six trigram GIN indexes, including one on `text_body`, inside the migration's single
  transaction. On a large `mail_messages` table this blocks inbound delivery and sends until the
  build ends.

A large installation builds the trigram indexes beforehand, without blocking writes, and the
migration then skips them (it creates each one with `IF NOT EXISTS`). Run this before the migration,
outside a transaction (each `CONCURRENTLY` statement on its own), as a role that may create the
extension and owns `mail_messages`:

```sql
CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_mail_messages_trgm_subject
  ON mail_messages USING gin (subject public.gin_trgm_ops);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_mail_messages_trgm_from_address
  ON mail_messages USING gin (from_address public.gin_trgm_ops);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_mail_messages_trgm_from_name
  ON mail_messages USING gin (from_name public.gin_trgm_ops);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_mail_messages_trgm_to_addresses
  ON mail_messages USING gin (to_addresses public.gin_trgm_ops);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_mail_messages_trgm_cc_addresses
  ON mail_messages USING gin (cc_addresses public.gin_trgm_ops);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_mail_messages_trgm_text_body
  ON mail_messages USING gin (text_body public.gin_trgm_ops);
```

If a `CONCURRENTLY` build fails, it leaves an invalid index of that name, which the migration would
then skip; drop it (`DROP INDEX CONCURRENTLY idx_mail_messages_trgm_<column>;`) and build it again
before migrating. The other new indexes (`idx_mail_messages_threads`, `_rfc_message_id`, `_trash`,
`_folder`) are on columns the migration adds, so they are built inside it; they are B-tree indexes
and build much faster than the trigram ones.

### Medium — `unread_count` counts only the Inbox

A mailbox's `unread_count` is the number of inbound messages in the `default` placement (the Inbox)
that the caller has not read. Archived messages, messages in a folder, and messages in the trash no
longer count. `GET /v1/mailboxes/:mailbox_id/message-counts` gives the unread counts of the other
placements and folders and the starred count.

### Medium — Deploy the API before the web app

The web app requires `placement`, `folder_id`, `label_ids`, `starred`, `thread_id`, and
`trashed_at` on every message and `access.organize` on every mailbox, and fails to read message lists
and mailboxes from an API that does not send them yet.

### Low — Search in Chinese and other non-Latin scripts depends on the database locale

`pg_trgm` takes trigrams from non-ASCII text only when the database's `LC_CTYPE` classifies those
characters as letters (for example `en_US.UTF-8` or `C.UTF-8`). Under the `C` locale such searches
cannot use the trigram indexes and a large mailbox can answer `422 MAILBOX.SEARCH_TOO_BROAD`.

## Unreleased — Queued sends at the send limit

### High — A send at the limit answers `202` and waits instead of `429`

Run migrations: `2026_11_20_090000_add_send_queue` adds `queued_reason`, `send_attempted_at`, and
`send_lease_until` to `mail_messages` and backfills `send_attempted_at` for outbound mail. When the
organization's rolling 24-hour limit is full, `POST /v1/mailboxes/:mailbox_id/messages`,
`POST /v1/outbound-mail-messages`, and `POST /v1/mail-templates/:template_id/tests` store the
message as `queued` with `queued_reason: "send_limit"` and answer `202`; `429
MAILBOX.SEND_LIMIT_REACHED` now means as many messages already wait as the limit. Run
`fastbuild mail:broadcast:work` wherever the API sends mail: it sends waiting messages too. Message
responses gain `queued_reason`.

Deploy the API before the web app: the web app requires `queued_reason` on every message and fails
to read message lists from an API that does not send it yet.

## Unreleased — Organization domains

### Medium — New migration for connected domains

Run migrations: `2026_11_17_150000_create_mail_domains` adds an empty `mail_domains` table and
`mailboxes.routing_rule_id`. Nothing changes until `MAILBOX_CUSTOM_DOMAINS_ENABLED=true`, which also
needs `MAILBOX_CLOUDFLARE_EMAIL_WORKER` and a token with Zone Read, DNS Edit, Zone Settings Edit,
Email Routing Rules Edit, and Email Sending Edit.

### Medium — Broadcasts gain a paused status and owner-only controls

Run migrations: `2026_11_17_160000_add_broadcast_safety` adds columns to `mail_broadcasts` and
`mail_broadcast_recipients` and allows the status `paused`. Clients that list broadcast statuses
add `paused`. Members can no longer send or cancel a broadcast (`403 PERMISSION.DENIED`); owners
and admins can.

### Low — `GET /v1/mailbox-domains` items gain `kind` and `custom_domains_enabled`

Clients that assumed exactly one item should take the item with `kind: "shared"` as the instance
domain.

## v0.27.0 — Zmail campaigns and self-hosting

### Medium — Four new mailbox migrations

Run migrations: `2026_11_17_110000_create_mail_attachments`,
`2026_11_17_120000_create_mail_templates`, `2026_11_17_130000_create_mail_contacts`, and
`2026_11_17_140000_create_mail_broadcasts` add empty tables. Existing mail is unchanged.

### Medium — Broadcasts need a public URL and a worker

Set `MAILBOX_PUBLIC_URL` to the console's public origin (https in production) and run
`mail:broadcast:work` next to the API, as `api/docs/DEPLOYMENT.md` describes. Without the URL and the
inbound secret, `broadcasts_enabled` stays `false` and the broadcast routes refuse to send.

### Low — Registration can be closed

`AUTH_REGISTRATION_ENABLED` defaults to open. A deployment that should not accept sign-ups sets it
to `false` and creates accounts with `deploy/self-host/create-user.sh` or an equivalent operator-only
process.

## v0.26.0 — Admin deployment, route boundaries, and observability

### Medium — Web devtools routes are off in production

`/styleguide` and `/i18n-test` now return 404 in production builds. A deployment that relied on
them sets `NEXT_PUBLIC_DEVTOOLS_ENABLED=true` at build time; development builds keep them on. A
fork that removed these routes can leave the flag unset.

### Low — Database metrics replace the unused connection gauges

With `METRICS_ENABLED=true`, `database.NewDB` installs the query metrics plugin and pool collector.
The never-recorded `SetDBConnections` gauges are removed; dashboards should read the new `db_*`
pool series in `api/docs/OBSERVABILITY.md`. Worker processes now export spans when tracing is
configured, so size the collector for them.

### Low — Admin list pages use `DataTable` and `Pagination`

`admin/src/components/layout/pager.tsx` and the `users.page`, `users.previous`, and `users.next`
keys are removed; use `@/components/ui/pagination` and the `common.*` keys. Custom list pages can
adopt `@/components/ui/data-table`.

### Low — Admin Console deployment and weekly CI

The Admin Console now ships `admin/Dockerfile` and `deploy/kubernetes/base/admin.yaml`; a fork that
hosts it elsewhere can ignore them, and one that deploys it sets `OPERATOR_ALLOWED_ORIGINS` as in
`admin/docs/DEPLOYMENT.md`. Scheduled workflows run weekly and draft pull requests skip CI; forks
that want nightly runs restore the `cron` lines.

## v0.25.0 — Plugin registry, lifecycle, and templates

### Scheduled — `plugin:list` becomes `command:list`

`plugin:list` lists external commands (`fastbuild-<name>` executables on `PATH`); the name now
belongs to registry plugins. Use `command:list`; the alias will be removed in a later minor release.

### Medium — New core migration for shared rate limiting

Run migrations: `2026_10_11_000000_create_rate_limit_buckets_table` creates an empty UNLOGGED table
for every starter selection. Nothing changes until `RATE_LIMIT_STORE=postgres` (which needs
`DB_ENABLED=true`); then size `DB_MAX_OPEN_CONNS` for one extra round trip per rate-limited request.
Code that calls `user.NewAuthAbuseGuard(cfg)` directly passes a `*ratelimit.Stores` (nil keeps the
process-local stores); regenerate Wire.

### Low — Slot markers in core files

`api/internal/infra/config/starters.go`, `api/internal/starter/defaults.go`, several Web files, and
both theme entry stylesheets now carry `fastbuild:slot` comments. Keep them: `plugin:add`,
`plugin:remove`, and `make:module` insert and remove lines there. If a merge drops one,
`plugin:slots` lists where it belongs.

### Low — Admin tokens moved to `tokens.css`

Custom Admin token edits in `admin/src/styles/globals.css` belong in
`admin/src/styles/tokens.css`, which `globals.css` imports before the `admin.theme` slot.

### Low — Plugin error codes live in their module

Error codes declared in `api/internal/modules/<name>/errors.go` count as API codes for the
error-contract check, and browser code lists no longer need codes owned by such modules. Add
`x-error-codes` to a contract fragment that introduces codes.

## v0.24.0 — Renamed to FastBuild; audit offset pages removed

### High — Luas is now FastBuild

The project moved to `fastagi/fastbuild` and every identifier changed with the name. GitHub
redirects the old repository URL, but code and configuration must use the new names:

| Area | Before | After |
|---|---|---|
| Go module | `github.com/zgiai/luas/api` | `github.com/fastagi/zmail/api` |
| CLI and image binary | `luas`, `/app/luas` | `fastbuild`, `/app/fastbuild` |
| Environment variable prefix | `LUAS_` (for example `LUAS_ENV_FILE`) | `FASTBUILD_` |
| Operator session cookie | `__Host-luas_operator` / `luas_operator` | `__Host-fastbuild_operator` / `fastbuild_operator` |
| New API key prefix | `luas_` | `fastbuild_` |
| Project marker | `.luas-project.json` | `.fastbuild-project.json` |
| Images and Kubernetes objects | `luas-api`, `luas-web`, `luas-*` | `fastbuild-api`, `fastbuild-web`, `fastbuild-*` |
| Default database and Compose project | `luas`, `luas-dev` | `fastbuild`, `fastbuild-dev` |
| Packages | `luas-web`, `luas-admin`, `@luas/contracts` | `fastbuild-web`, `fastbuild-admin`, `@fastbuild/contracts` |

A fork updates its Go imports (`go mod edit -module` plus a replace of the import prefix), renames
`LUAS_*` variables in every environment, and rebuilds its images. Operators sign in again because
the cookie name changed. Existing API keys keep working: lookup uses the key hash, and only keys
issued from now on carry the `fastbuild_` prefix. A local `make dev` stack starts with a fresh
database because the Compose project name changed; `make dev-reset` under the old checkout removes
the old one.


### High — Audit history endpoints return keyset pages only

As scheduled in v0.23.0, `GET /v1/audit-logs` and `GET /v1/operator/audit-logs` no longer accept
`page`; a request with it returns `400 COMMON.INVALID_INPUT`. Every response is a keyset page with
`meta.per_page`, `meta.has_more`, and `meta.next_cursor`, and no `total`, `last_page`, or `links`.
Omit `cursor` (or send it empty) for the newest page and pass `meta.next_cursor` back for older
ones. In Go, use `ListForUserAfter` and `ListAuditLogsAfter` (and `FindByUserIDAfter` and
`FindAllAfter` on the repository); the offset methods are removed from the audit service,
`domain.AuditLogQuery`, and `domain.AuditLogRepository`.

## v0.23.0 — Go 1.26, database pool defaults, audit cursor pages, and batched audit writes

### High — Build with Go 1.26.9

`api/go.mod` requires Go 1.26.9 and the API image builds on `golang:1.26.9-alpine3.24`. Go 1.25 no
longer receives security fixes. Install Go 1.26.9 or let `GOTOOLCHAIN=auto` fetch it, and use
golangci-lint 2.14.0 or newer: releases built with Go 1.25 refuse to lint a Go 1.26 module.

### Medium — TypeScript 6 in the browser shells

Web and Admin build with TypeScript 6. Admin no longer sets the deprecated `baseUrl`; its `@/*`
path alias resolves relative to the tsconfig file. Forks that added `baseUrl`-relative imports
should switch them to the alias.

### Scheduled — Offset pages on audit history endpoints

`page` on `GET /v1/audit-logs` and `GET /v1/operator/audit-logs` is deprecated and will be removed in a
later minor release: each offset page counts every matching record. Send `cursor` instead (empty for
the newest page) and pass `meta.next_cursor` back for older pages. Cursor responses have
`meta.per_page`, `meta.has_more`, and `meta.next_cursor`, and no `total`, `last_page`, or `links`, so
a client that shows "page X of Y" or a total count must switch to next/previous navigation. Sending
both `cursor` and `page` returns `400 COMMON.INVALID_INPUT`.

### Medium — Request audit records are written after the response

`AUDIT_WRITE_MODE` defaults to `async`: request audit records are queued and written in batches, so
reading audit history immediately after a write can miss that write for about 50 ms, and a process
killed without a graceful shutdown loses the records still queued. Tests that assert on audit
history right after a request should set `cfg.Audit.WriteMode = config.AuditWriteModeSync`, as the
feature test harness does. Set `AUDIT_WRITE_MODE=sync` in deployments that need every record
committed before the response. Code that implements `domain.AuditLogRepository` adds `CreateBatch`,
`FindByUserIDAfter`, and `FindAllAfter`; code that implements `domain.AuditLogQuery` adds
`ListAuditLogsAfter`.

### Low — Audit keyset index migration

`2026_10_10_000000_add_audit_user_keyset_index` builds `(user_id, id)` with `CREATE INDEX
CONCURRENTLY` and then drops the single-column `user_id` index, so it does not block audit writes;
on a large table it takes time proportional to the table size.


### Low — Idle database connections follow the open limit

`DB_MAX_IDLE_CONNS` defaults to `DB_MAX_OPEN_CONNS` (previously 10). Each process keeps up to its
open limit of connections between bursts until `DB_CONN_MAX_IDLE_TIME` (15 minutes) retires them,
so PostgreSQL may show more idle connections than before. The total is still bounded by the
`DB_MAX_OPEN_CONNS` budget you already sized per replica. Set `DB_MAX_IDLE_CONNS` explicitly to keep
the old behavior, and remove `DB_MAX_IDLE_CONNS=10` from env files copied from the old example.

## v0.22.0 — Deprecation removals, authentication hardening, and structured production logs

### Low — `ROLE.NOT_FOUND` removed

The deprecated `ROLE.NOT_FOUND` value is gone from the API, `contracts/openapi.yaml`, and the Web
and Admin code lists, as scheduled in v0.21.0. Match `PERMISSION.ROLE_NOT_FOUND` instead.

### Medium — Operators sign in again after the session audience migration

`2026_10_04_000000_add_audience_to_authentication_sessions` marks every existing session as a
public-login session, and the operator routes now accept only sessions issued by
`POST /v1/operator/session`. Operators are signed out of the Admin Console once after the
migration. Public sessions are unaffected. Code that implements `domain.CredentialSignIn` or calls
`SessionService.Issue` passes a `domain.SessionAudience`.

### Medium — Production operator cookie names keep `__Host-`

Configuration validation rejects a production `OPERATOR_SESSION_COOKIE_NAME` without the `__Host-`
prefix. Unset it to use `__Host-fastbuild_operator`, or rename the custom value.

### Low — Login throttling counts failures per account

`AUTH_RATE_LIMIT_LOGIN_SUBJECT_MAX` now counts failed sign-ins per account instead of every
attempt per submitted identifier, and a correct password resets it. Review the value if it was
raised to absorb legitimate repeated sign-ins. `AuthAbuseGuard.AllowLoginSubject` is removed; the
user service enforces the budget.

### Low — JSON logging covers database and `log/slog` output

With `LOG_JSON=true`, database errors and slow queries arrive as `database.query_failed` and
`database.slow_query` events with `sql`, `rows`, and `elapsed_ms` fields instead of colored text,
and the ASCII banner is gone. Update log queries or alerts that matched the old text. Code that
calls `log/slog` now reaches the configured logger outputs. Console output in development is
unchanged.

## 2026-10-04 — Complete OpenAPI contract and error alignment

### Medium — Body validation is 422, invalid input is 400

The API now follows the global contract in `contracts/README.md`:

- A request body that fails field validation (`binding` tags through `handler.BindJSON`) returns
  `422 COMMON.VALIDATION_FAILED` with `errors` keyed by JSON field name. Before, it returned
  `400 COMMON.INVALID_INPUT` without field errors.
- `domain.ErrInvalidInput` maps to `400 COMMON.INVALID_INPUT`; it was `422` with the same code.
- A path ID of `0` returns `400` from `handler.ParseID`. Before, some permission routes returned an
  empty response for it.
- A body over a starter's own size cap returns `413 COMMON.REQUEST_TOO_LARGE`, as the global limit
  already did. Handlers that decode bodies themselves use `handler.WriteBodyError`.
- `GET /v1/operator/organizations/:id/members` returns `404 ORGANIZATION.NOT_FOUND` for an unknown
  organization instead of an empty page.

Clients that branch on `error_code`, as the contract requires, need no change. A client that
treated every `400` from a form submission as a field error should read `errors` from the `422`.

### Medium — Browser clients bind their schemas to the OpenAPI contract

`web/src/http/contract.ts` and `admin/src/http/contract.ts` derive request and response bodies
from the generated OpenAPI operations, and each feature service asserts that its Zod schemas accept
every body the contract allows. `corepack pnpm type-check` now fails when a client schema and the
contract drift; fix the side that is wrong rather than removing the assertion.

The Web setting, usage, and webhook features no longer reject a response because the server's
code-owned catalog has entries the client does not render. They read every entry with a generic
schema, validate the entries they render exactly, and ignore the rest, so deploying an API with a
larger catalog before the Web build no longer breaks those pages. A fork that relied on the client
rejecting unknown entries must check that on the server instead.

### Low — Every operation is in `contracts/openapi.yaml`

All 92 operations are described, and `make contract-check` fails when a route has no description or
a description has no route. Regenerate the browser types with `make contract-generate` after a
contract change. The health report's `latency_ms` is now milliseconds, and its duplicate
`duration` field is gone.

## 2026-10-03 — Security review fixes

### Medium — User writes are column-scoped

`user` repository `Update` now writes only `nickname`, `avatar`, `phone`, and `bio` and returns
`USER.NOT_FOUND` for a deleted account; sign-in records `last_login` through the new `RecordLogin`.
Before, both wrote the whole row they had read, so a sign-in or profile update racing a password
change, operator disable, or account deletion could restore the old password hash, re-enable the
account, or undelete it. A fork that relied on `Update` to persist other columns must add a
dedicated repository command for them.

### Medium — Usernames cannot contain `@`, and email sign-in resolves to the email owner

Registration rejects a username that contains `@` (`422`). A login identifier that contains `@`
matches the email first. Before, the documented username precedence was not applied at all (the
lookup returned the lowest ID), so an account registered with another account's email as its
username could capture that account's sign-in. Existing usernames are not changed.

### Low — Operator routes reject foreign origins on reads

Every `/v1/operator` request that carries an `Origin` outside `OPERATOR_ALLOWED_ORIGINS` now fails
with `403 OPERATOR.ORIGIN_REJECTED`, not only unsafe methods. Same-origin reads, which send no
`Origin`, are unaffected. The operator cookie is `Secure` whenever every allowed origin is `https`.

### Low — Webhook targets in IPv6 transition ranges are rejected

IPv4-compatible (`::/96`), NAT64 (`64:ff9b::/96`, `64:ff9b:1::/48`), Teredo (`2001::/32`), and 6to4
(`2002::/16`) destinations fail with `WEBHOOK.INVALID_TARGET` unless private targets are explicitly
allowed.

## 2026-10-03 — Dependency security updates

### Medium — Toolchain and framework minimums raised

All open advisories are closed. A fork must pick up the same minimums:

- Go `1.25.13` (seven standard-library advisories in `1.25.12`); the API builder image is pinned to
  the matching digest in `api/Dockerfile`, `api/scripts/verify-container.sh`, and the container
  governance check.
- Next.js `16.3.8` and `eslint-config-next` `16.3.8` (three remote-code-execution advisories in
  `16.2.x`), axios `1.20.0`, vitest `4.1.11`.
- `google.golang.org/grpc` `1.83.2`, `golang.org/x/crypto` `0.55.0`, and OpenTelemetry `1.45.0`.
- The Web base image is Node `22.23.3` on Alpine 3.24 (OpenSSL fix), pinned by digest in
  `web/Dockerfile`, `web/scripts/verify-container.sh`, and the container governance check.
- pnpm overrides in all three `pnpm-workspace.yaml` files now force patched `sharp`, `undici`,
  `js-yaml`, `browserslist`, `baseline-browser-mapping`, `brace-expansion`, and `@humanfs/node`.

axios `1.20` changed its method return types; `web/src/http/request.ts` narrows the unwrapped
payload in one helper. A fork that calls `axios` instances with two explicit type arguments
(`get<T, T>`) should do the same.

## 2026-10-02 — Admin Console starter administration

### Low — Operator routes and screens for organizations, webhooks, and notifications

When `operator` is selected together with `organization`, `webhook`, or `notification`, the API now
serves read-mostly operator routes for that starter under `/v1/operator`
([`contracts/OPERATORS.md`](contracts/OPERATORS.md)): an organization directory with members,
secret-free webhook endpoints, deliveries, and attempts with delivery replay, and the notification
delivery ledger without content. A deployment that selects those starters but must not expose them
to operators has to remove the mounts in `api/internal/modules/operator/routes.go`.

The Admin Console shows the matching screens only when `VITE_OPTIONAL_FEATURES` lists
`organization`, `webhook`, or `notification` in addition to `operator`. No migration.

### Low — `operator.NewHandler` takes a `Surfaces` struct

`operator.NewHandler(service, guard, cfg, settings)` became
`operator.NewHandler(service, guard, cfg, operator.Surfaces{...})`. Wire injects `Surfaces`; only a
fork that constructs the handler by hand must change the call.

## 2026-10-02 — Schema cleanup

### Low — `notification_preferences.user_id` loses its sequence default

Migration `2026_10_02_000000_drop_notification_preferences_user_id_sequence` drops the
`notification_preferences_user_id_seq` default left by the original `bigserial` definition. The
column is always the owning user's ID, so no application write used the sequence. Run migrations
with the `notification` starter selected; a fork that inserted preference rows without `user_id`
must now supply it.

## 2026-10-01 — Operations hardening

### Medium — CLI plugins no longer load from the current directory

`plugin:list` and plugin dispatch search only absolute `PATH` entries, and plugin names must match
`[a-z][a-z0-9-]*`. A plugin that lived only in the working directory must move onto `PATH`.

### Low — Durable task operations

New commands `workflow:tasks`, `workflow:retry`, `workflow:cancel`, and `workflow:prune` require
`QUEUE_DRIVER=postgres`. Finished tasks were never deleted before; schedule `workflow:prune`
(default retention 720h) to bound the `workflow_tasks` table.

## 2026-10-01 — Module ownership and generator

### Medium — Starters own their error mappings

`internal/bootstrap/domain_error_mappings.go` now maps only shared errors (not found, conflict,
invalid input, unavailable, authentication required, permission denied, legacy role). Each starter
maps its own errors in `error_mappings.go` through the new `assembly.ErrorModule` capability. A fork
that added mappings to the central file should move them into its starter's
`RegisterErrorMappings`; `registerDomainErrorMappings` now also takes the starter registry.

### Low — `make:module` generates a wired optional starter

The generator adds a starter manifest, error-mapping hook, `auth`-protected routes, and a frozen SQL
migration, and registers the starter constant and catalog entry. Generated packages drop underscores
(`blog_post` directory, `blogpost` package).

### Low — Test doubles and removed middleware

- `internal/infra/testing.FakeMailer` records email for any starter mail seam.
- The unused `internal/infra/middleware` CORS implementation was removed; the kernel uses
  `gin-contrib/cors`.

## 2026-10-01 — Platform operator starter

### Low — New optional `operator` starter

Opt-in only; nothing changes unless `operator` is added to `OPTIONAL_STARTERS`.

- Selecting it requires `OPERATOR_ALLOWED_ORIGINS` (exact Admin Console origins) and adds the
  `platform_operators` migration, `/v1/operator/*` routes, and `operator:grant`, `operator:revoke`,
  and `operator:list` commands. Grant the first operator with `fastbuild operator:grant <email>`.
- To require operator sign-in in the Admin Console, build it with `VITE_OPTIONAL_FEATURES=operator`
  and route the Admin origin's `/api/*` to the Go API on the same origin. Without the flag the
  console is unchanged.
- New error codes: `OPERATOR.FORBIDDEN`, `OPERATOR.ORIGIN_REJECTED`, `OPERATOR.CSRF_REJECTED`,
  `OPERATOR.TARGET_PROTECTED`. Clients that switch exhaustively on `error_code` should add them.
- `domain.CredentialSignIn` and `domain.SessionRevoker` are new user-starter seams; the public login
  now shares its credential check with operator sign-in without behavior change.
- `domain.UserAdministrator` is a new user-starter seam used by `/v1/operator/users`. Disabling an
  account through it revokes the account's sessions in the same transaction (reason
  `account_disabled`); ending sessions uses the new revocation reason `operator`.
- `domain.AuditLogRepository` gains `FindAll`, and `domain.AuditLogFilter` gains `UserID`, `From`, and
  `To`. Custom audit repositories must implement `FindAll` (newest first, `From` inclusive, `To`
  exclusive).
- The setting starter's `Handler` exposes `AppList`, `AppSet`, and `AppReset` for app-scoped settings.
  They perform no authorization; mount them only behind an authorizing starter such as `operator`.

## 2026-09-30 — Dead code and dialect cleanup

### High — Unused API packages removed

Removed with no production callers: `internal/infra/{queue,schedule,retry,http,lang,types,contracts}`,
`internal/infra/events/dispatcher.go` (global `SimpleDispatcher`), `pkg/{hash,utils,request,
resource,encryption,validation,support,events}`, and the `api/tests/unit` directory.

- Queue and scheduler code: import `internal/capabilities/workflow` directly. `queue.New()` is
  `workflow.NewQueueManager()`, `schedule.New()` is `workflow.NewScheduler()`, and
  `schedule.Global()` is `workflow.GlobalScheduler()`; other names are unchanged.
- `pkg/encryption`: use `internal/capabilities/crypto`.
- `pkg/events.Event`: use `events.BasicEvent` from `internal/infra/events`. The embedded field of
  `events.WrappedEvent` is now `BasicEvent`.
- Tests formerly in `tests/unit` now live beside their packages as `*_external_test.go`; CI targets
  (`test-race-critical`, `benchmark-workflow`) point at `./internal/capabilities/workflow`.
- `make governance` now rejects `support`, `utils`, `common`, and `helpers` packages under `pkg/`
  and `internal/infra/`.

### Medium — Schema builder is PostgreSQL-only

The MySQL grammar and the MySQL-only `ColumnDefinition.After` and `First` methods were removed.
`schema.NewGrammar` always returns the PostgreSQL grammar.

### Low — Deprecated registries and unused Web utilities removed

- `migrations.Default()`, `seeders.Default()`, and `seeders.RunDefault()` are gone; use
  `starter.DefaultMigrations()`, `starter.DefaultSeeders()`, or the configured variants.
- Web `src/utils` keeps only `cn`. The unused `string`, `date`, `object`, `array`, `validation`, and
  `event-bus` utilities and the `useEventBus` hook were removed.

### Scheduled — `ROLE.NOT_FOUND` error code

`ROLE.NOT_FOUND` is a deprecated legacy value in the public error-code enum and is no longer emitted
by any starter; access roles use `PERMISSION.ROLE_NOT_FOUND`. It will be removed from
`contracts/openapi.yaml` in a later release. Stop matching on it now.

## 2026-09-30 — API assembly hardening

### High — Released migrations frozen as SQL

Versioned migrations no longer call `AutoMigrate` on module persistence structs. Existing databases
need no action: migration names are unchanged and the schema is byte-identical.

- Write new migrations as SQL through `execStatements` or with the schema builder; never import
  `internal/modules` or `internal/capabilities` from `database/migrations`.
- `TestMigrationsProduceGoldenSchema` compares a fresh database with
  `database/migrations/testdata/schema.golden.sql`. When you add a migration, regenerate it with
  `FASTBUILD_UPDATE_GOLDEN_SCHEMA=1` and review the diff.

### High — Starter handlers injected as one struct

`starter.NewConfiguredRegistry`, `ConfiguredManifests`, `DefaultManifests`, and `OptionalManifests`
take a `*starter.Handlers` instead of positional handler parameters. Metadata-only callers pass
`nil`. A fork that added a starter adds one `Handlers` field and one manifest line, then runs
`make wire`.

### Medium — Starter names come from constants

Use `config.Starter*` constants and `cfg.Starters.Selected(name)` instead of string literals.
Add a constant and a `StarterNames()` entry for each new starter; a catalog test enforces the match.

### Medium — Removed domain event system and service locator

`internal/domain/{aggregate,events}.go` and `internal/infra/{container,lifecycle,pipeline,breaker}`
were removed. Publish events through the injected `events.EventBus`.

### Low — Wire drift check

CI runs `make wire-check`. Resolve `wire_gen.go` merge conflicts by running `make wire`, never by
hand. See `api/docs/adr/0014-wire-maintenance-posture.md`.
