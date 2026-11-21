# Mailbox Origin and Manager Connection Controls (Roadmap C1): Reduced Plan

Spec: [mail-connectors-roadmap.zh-CN.md](mail-connectors-roadmap.zh-CN.md) §3, §4, §8 and
[MAILBOX_ACCESS_PRD.zh-CN.md](../MAILBOX_ACCESS_PRD.zh-CN.md). Loop task T5.

Owner: the `mailbox` starter (API) owns every rule and record; `contracts/` joins it to `web/` and
`admin/`, which only follow the schema.

## Scope

A mailbox has two independent dimensions: **access** (`type`: `personal` or `shared`, the existing
permission model, unchanged) and **origin** (`zmail` or `connected`). The third type `external`
added by the connector foundation becomes `personal` + `connected`. The product is not launched, so
rows are converted in place.

## Rulings

| # | Ruling |
|---|---|
| R1 | Migration `2026_12_04_090000_add_mailbox_origin`: `mailboxes.origin varchar(16) NOT NULL DEFAULT 'zmail'` with `chk_mailboxes_origin`; `external` rows become `personal` + `connected`; `chk_mailboxes_type` back to `('personal','shared')`; `idx_mailboxes_address` becomes `WHERE origin = 'zmail'`. `idx_mailboxes_owner` (`type = 'personal'`) now covers connected personal mailboxes, which owner lookups want. `chk_mailboxes_personal_webhooks` already forbids webhooks on every personal mailbox and is kept. Down reverses all of it. |
| R2 | Access (`domain.MailboxAccessFor`): a connected mailbox follows its type's view rules, but never grants `send` or `organize`. For a connected **personal** mailbox the organization role grants nothing at all, so owners and admins neither see nor administer another member's own account; the owner gets `view` + `see_history`. A connected shared mailbox (C3) keeps the role's `administer`, `manage_members`, `see_history`. |
| R3 | Because every listing, the unified inbox, search, counts, triage, roles, and `authorizeMailbox` derive from R2, a manager's surfaces drop other members' connected personal mailboxes without further filters. The manager-only mailbox directory, its `mailbox_count`, and the export filter in SQL: they never list a connected personal mailbox. Rename and platform-account linking refuse it as not found. |
| R4 | The per-organization mailbox limit counts `origin = 'zmail'` only. Delivery lookup, send-from address, and the address-taken check skip `origin = 'connected'`. `send()` refuses any connected mailbox. `POST /v1/mailboxes` never creates one. |
| R5 | Departure: a connected personal mailbox never enters awaiting-handover (`MarkOwnerLeft`/`ClearOwnerLeft` touch `origin = 'zmail'` only); handover refuses it with `409 MAILBOX.ACCESS_CONFLICT` (an admin cannot reach it anyway: 404). Its connections are revoked as before. |
| R6 | Manager endpoints: `GET /v1/external-mail-connection-directory` (owner/admin, keyset by id descending, `cursor` + `per_page` 1..100, default 15) returns id, provider, status, masked account address (`st***@gmail.com`), owner user id and display name, `last_synced_at`, `created_at`; never mail or counts. `DELETE /v1/external-mail-connection-directory/{connection_id}` is a forced disconnect with the owner's revoke effect. Members get `403`. The existing owner revoke route stays; a manager using it on another member's connection is recorded as a forced disconnect. |
| R7 | Access history on the connected mailbox: `connection_bound`, `connection_reconnected`, `connection_revoked`, `connection_force_disconnected`, `connection_revoked_on_leave`, detail `connection_id` and `provider` only, never the address. |
| R8 | Contract: `Mailbox.origin` required; `MailboxType` loses `external`; new schemas and routes; `MAILBOXES.md` rules; `UPGRADING.md`. Web and Admin regenerate types; the Web zod schema accepts `origin` (optional until every API emits it) and drops `external`. |

## Acceptance

- Migration converts an `external` row to `personal` + `connected`, the old type is rejected, and
  two members may each hold a connected copy of one address while a Zmail address stays unique.
- A manager's mailbox list (both scopes), directory and its count, export, unified inbox and its
  counts, and search never include another member's connected mailbox or its address; the owner
  still sees and reads it.
- Sending from and handing over a connected mailbox are refused.
- The connection directory masks addresses, pages by cursor, and answers `403` to members; forced
  disconnect revokes and is recorded; bind, reconnect, revoke, and leave are recorded.

## Verification

`gofmt`, `go vet ./...`, full `go test ./...` with `FASTBUILD_TEST_POSTGRES_DSN` set, golden schema,
`make starter-catalog`, `cd contracts && pnpm generate && pnpm check && pnpm check:routes`, Web and
Admin `pnpm type-check` and Vitest, `make governance`.
