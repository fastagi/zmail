# External Mailboxes (Slice 4 Part A, Offline Part): Implementation Plan

Spec: [external-accounts.zh-CN.md](external-accounts.zh-CN.md) §3 (part A), §4, §5, §9. Part B
(platform accounts) is merged. This plan builds only what §9's last paragraph allows before the
Google and Microsoft OAuth applications exist: the data model, the `ExternalMailSource` seam with
the `disabled` (default) and in-memory `fake` implementations, the sync service and worker, the
`source` field on message summaries and unified inbox rows, connection list/revoke endpoints, and
the Web source badge. No OAuth flow, no real provider, no SDK dependency, no client secret is read.

Owner: the `mailbox` starter (API) owns every rule and record; `contracts/` joins it to `web/`.

## Rulings

| # | Ruling |
|---|---|
| R1 | Everything lives in the `mailbox` starter in new `external_mail_*.go` files plus `domain/external_mail.go`; existing files only gain hooks (message `source`, access rule, routes, manifest, send guard). |
| R2 | Config: `EXTERNAL_MAIL_PROVIDERS`, a comma-separated list, default empty (off). Only `fake` is accepted now: it stands in for both Gmail and Outlook. `gmail` and `outlook` are refused until their real sources exist. `fake` with `APP_ENV=production` fails validation. A non-empty list needs `MAILBOX_SECRET_ENCRYPTION_KEY` (32+ characters), which seals refresh tokens with the existing AES-GCM helper (`crypto.NewAESEncryptorFromString`). Env is read only in `internal/infra/config/config.go`. |
| R3 | One migration `2026_11_28_090000_add_external_mail`: `mailboxes.type` gains `external`; `mail_messages.source varchar(16) NOT NULL DEFAULT 'zmail'` (`zmail`, `gmail`, `outlook`; CHECK added `NOT VALID`, so no scan: every existing row takes the default) and nullable `external_id varchar(255)`; partial unique index `idx_mail_messages_external (mailbox_id, external_id) WHERE external_id IS NOT NULL` (`IF NOT EXISTS`, so large installations pre-create it `CONCURRENTLY`); table `external_mail_connections`. Down fails while an `external` mailbox row exists (the restored type CHECK rejects it). |
| R4 | `external_mail_connections`: one row per external mailbox (`mailbox_id` unique), `provider` (`gmail`/`outlook`), `account_address`, `owner_user_id` (the binder), `status` (`active`/`needs_reconnect`/`revoked`), `last_synced_at`, `last_error_code`, `encrypted_refresh_token` (base64 AES-GCM; cleared on revoke), `sync_cursor`, `backfill_days` (default 14, 1..90), `next_sync_at` (the poll schedule and claim lease). Unique `(organization_id, provider, lower(account_address))`. Due index `(next_sync_at) WHERE status = 'active'`. |
| R5 | Access (D3): an `external` mailbox is like a personal one whose owner is the binder, but read-only: the owner gets `view` and `see_history`, never `send` or `organize`; owners/admins get `administer` only (never reading). `send()` also refuses an external mailbox, so no path (console, API key, broadcast) can send from it. External mailboxes are never created through `POST /v1/mailboxes`. |
| R6 | Seam `ExternalMailSource { AuthURL; Exchange; Fetch; Revoke }` (spec §3.2). `disabled` returns `ErrExternalMailUnavailable` everywhere. `fake` serves scripted messages per refresh token from memory; its cursor is the count of messages delivered; it can be told to fail authentication. |
| R7 | Sync (`SyncExternalMail(ctx, limit)`): claim up to `limit` due active connections with `FOR UPDATE SKIP LOCKED`, pushing `next_sync_at` by the 2-minute interval (the per-connection rate limit). For each: decrypt the token, `Fetch(token, cursor)`, then in one transaction update the connection only `WHERE status = 'active'` and, if that matched, insert the messages `ON CONFLICT (mailbox_id, external_id) DO NOTHING` (inbound, `received`, `source` = provider, `created_at` = provider receive time, threaded like inbound mail). A revoke that lands mid-fetch therefore drops the batch. `ErrExternalMailAuth` sets `needs_reconnect` + `external.auth_failed`, which stops polling; any other fetch error keeps the connection active with `external.fetch_failed` and retries next interval. An undecryptable token is treated as an auth failure. |
| R8 | Worker command `mail:external:sync [--batch=20] [--poll=30s] [--once]`. With `EXTERNAL_MAIL_PROVIDERS` empty it logs `external_mail.worker_disabled` and exits 0 without opening the database. |
| R9 | HTTP: `GET /v1/external-mail-connections` lists the caller's own connections (bounded: 10, the per-user cap binding will enforce); `DELETE /v1/external-mail-connections/{id}` revokes (binder, or an owner/admin for offboarding): best-effort `Revoke` at the provider, then `revoked`, token and cursor cleared, already synced mail kept (§9 item 4 is open; deletion waits for that decision). Both answer `404 MAILBOX.EXTERNAL_CONNECTION_NOT_FOUND` when the instance has no provider, for another user's connection, or another organization's id. No bind/OAuth endpoints. |
| R10 | Contract: message summaries (mailbox listing, log, unified inbox rows) gain required `source` (`zmail`/`gmail`/`outlook`). The Web schema keeps it optional (older API still parses) and treats missing as `zmail`. |
| R11 | Web: a small badge beside the role tag in unified inbox rows for `gmail`/`outlook` only; nothing for `zmail`. en-US and zh-Hans strings. |
| R12 | Not in this slice: triage enqueue and webhooks for synced mail, attachments of synced mail, the binding page, retention of revoked history. |

## Tasks

1. **Migration** + manifest, golden schema, Up/Down and constraint tests, UPGRADING.md entry.
2. **Config** `internal/infra/config/external_mail.go` + reads in `config.go`, `.env.example`.
   Tests: defaults off, unknown/real providers refused, fake refused in production, key required.
3. **Domain + seam** `domain/external_mail.go`, `external_mail_source.go` (disabled, fake).
4. **Repository + sync service** `external_mail_repository.go`, `external_mail_service.go`.
   PostgreSQL tests: idempotent re-fetch, cursor advance, 401 → `needs_reconnect`, revoked stops
   fetching (including revoke during a fetch), private visibility, no send from external.
5. **Worker command** `mail:external:sync`; registration and disabled no-op tests.
6. **HTTP** list/revoke handlers, routes, error code; handler tests; route catalog.
7. **Contract** `contracts/openapi.yaml`, `contracts/MAILBOXES.md`; `make contract-generate`,
   `make contract-check`.
8. **Web** schema field, badge, i18n, Vitest.
9. **`make check`** from the worktree root.
