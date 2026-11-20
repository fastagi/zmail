# Platform Mail Operations (Slice 2: Broadcast Content and Its Audit): Implementation Plan

Spec: [admin-mail-operations.zh-CN.md](admin-mail-operations.zh-CN.md) (权限和上线边界, 验收样例 row 4).
Builds on slice 1, [admin-mail-operations.plan.md](admin-mail-operations.plan.md): the operator
grant scope, capabilities, the outbound log, and the Operations area of the Admin Console.

Owners: the `operator` starter owns who may read (grant, capabilities, session); the `mailbox`
starter owns message content, the eligibility rule, and the content access audit records.
`contracts/OPERATORS.md` and `contracts/openapi.yaml` join the API to the Admin Console.

## Business decision (owner, 2026-11-20)

Operators may read a body **only for organization broadcast mail**. Ordinary business outbound,
private inbox mail, synced external copies, login codes, account recovery, and other transactional
system mail are never readable here. Transactional mail lives in its own table and is out of reach
by construction; the rest is excluded by the eligibility rule (R4).

## Rulings

| # | Ruling |
|---|---|
| R1 | **Content is an explicit add-on, not part of `full`.** `platform_operators` gains `mail_content boolean NOT NULL DEFAULT false`. The scope keeps its meaning (`full` → `operator:manage` + `mail_metrics:read`; `mail_metrics` → `mail_metrics:read`); `mail_content = true` adds `mail_content:read` to either. A full operator therefore cannot read bodies until the owner grants content explicitly, which keeps account administration and body reading separately granted, as the spec asks. |
| R2 | **CLI.** `operator:grant <email> [--scope=full\|mail-metrics][,mail-content]`, e.g. `--scope=mail-metrics,mail-content`. A grant command sets the whole grant: granting `--scope=mail-metrics` to a grant that had content removes content. Same scope and content again is a no-op; any difference is a change audited with `scope` and `mail_content` in the metadata. `operator:list` gains a CONTENT column. `operator:revoke` removes everything. |
| R3 | **Session.** `capabilities` may now include `mail_content:read`. Every request re-reads the grant, so removing content (re-grant without it, or revoke) makes the next read fail with `403 OPERATOR.CAPABILITY_REQUIRED` (or `OPERATOR.FORBIDDEN` after a revoke). |
| R4 | **Eligible message.** `POST /v1/operator/outbound-messages/{organization_id}/{message_id}/content` reads one message when: it exists in a mailbox that is not deleted and belongs to `organization_id` (otherwise `404 MAILBOX.MESSAGE_NOT_FOUND`, so a mismatched pair discloses nothing more than an unknown ID); and it is outbound, sent through Zmail (`source = 'zmail'`), and a recipient row of a broadcast of the same organization points at it (otherwise `403 OPERATOR.CONTENT_NOT_ELIGIBLE`). |
| R5 | **Reason.** Body `{ "reason": string }`, trimmed, 1–500 characters, no control characters except line breaks and tabs; otherwise `422 COMMON.VALIDATION_FAILED`. Needs `mail_content:read` only. Unsafe-method rules (Origin, CSRF) apply as on every operator route. |
| R6 | **Audit first.** One database transaction: check eligibility, insert the audit record, then load the body, then commit. If the insert or the commit fails, the response is `503 COMMON.SERVICE_UNAVAILABLE` and carries no body. The record holds `viewer_user_id`, `organization_id`, `message_id`, `reason`, `accessed_at` — never a body, subject, code, or recipient address. Refused reads (404/403) write no content audit row; the generic audit middleware still records the unsafe request. |
| R7 | **Snapshot.** Returns `{ access_id, organization_id, message_id, broadcast_id, subject, from_address, created_at, text, text_source }`. `text` is the stored text part; when it is blank, the HTML part converted to readable text (`text_source = "html"`): script, style, and head content dropped, block elements become line breaks, entities decoded, no URL fetched. Never HTML, images, attachments, or recipients. `Cache-Control: private, no-store` (already set for every operator route). No log line carries the body or the reason. |
| R8 | **Audit query.** `GET /v1/operator/content-access-audits?organization_id=&viewer_user_id=&from=&to=&page=&per_page=` needs `mail_content:read` **or** `operator:manage`. `from`/`to` are RFC 3339, `from` inclusive and `to` exclusive. Rows `{ id, viewer_user_id, organization_id, organization_name, message_id, reason, accessed_at }`, newest first (`accessed_at desc, id desc`), page size 1–100 (default 20). |
| R9 | **Outbound log** rows gain `broadcast_id` (nullable): set exactly when R4's broadcast condition holds, so the console shows "View content" only where the read can succeed. |
| R10 | **Migrations.** `2026_12_01_090000_add_platform_operator_mail_content` (operator starter): `ADD COLUMN mail_content boolean NOT NULL DEFAULT false`, catalog-only on PostgreSQL 11+, brief `ACCESS EXCLUSIVE` on the tiny grants table. Down drops the column, which only narrows grants. `2026_12_01_090100_create_mail_content_access_audits` (mailbox starter): new table and two indexes; no lock on existing tables. Down drops the table and its records (documented). Audit rows have no foreign keys so they outlive the message, mailbox, organization, or account. |
| R11 | **Admin Console.** In the outbound log, rows with `broadcast_id` get "View content", which opens a dialog asking for the reason, then shows the text. Rows without it show no action. The read is a mutation, never a cached query: nothing is stored in the TanStack Query cache, and the text is dropped when the dialog closes. A new "Content access" page lists audit records with organization, viewer, and time filters. Both show empty, error-with-retry, and forbidden states. Navigation shows each item only when the session's capabilities cover it. |

## Acceptance (spec table → proof)

| Spec row | Proof |
|---|---|
| 4 (member, org owner, metrics-only, content grant; audit; revoke) | Operator route test: member and org owner get `OPERATOR.FORBIDDEN`, metrics-only and full without content get `OPERATOR.CAPABILITY_REQUIRED`, the content grant passes; removing content or revoking fails the next read. PostgreSQL test: the read writes one audit row with no body or address in it. |
| Audit failure → no body | PostgreSQL test with the audit table unavailable: 503, no text in the response, no row. |
| Non-broadcast refusal | PostgreSQL test: personal outbound and inbound → `403 OPERATOR.CONTENT_NOT_ELIGIBLE`, no audit row. |
| Cross-organization mismatch | PostgreSQL test: a broadcast message read under another organization's ID → `404 MAILBOX.MESSAGE_NOT_FOUND`. |
| Console | Admin Vitest: action only on broadcast rows, reason required, text shown, states for the audit page. |

## Tasks

1. Contract: `contracts/OPERATORS.md`, `contracts/openapi.yaml`; `make contract-generate`, `make contract-check`.
2. Migrations + manifests, golden schema, Up/Down tests, `UPGRADING.md`.
3. Grant content flag, capability, CLI, any-of capability middleware; tests first.
4. Mailbox content read, audit store, audit query, outbound log `broadcast_id`; PostgreSQL tests first.
5. Wire, route catalog.
6. Admin Console dialog and audit page, i18n (en-US, zh-Hans), Vitest.
7. Kest flow (log provider only) extension.
8. `make check`.
