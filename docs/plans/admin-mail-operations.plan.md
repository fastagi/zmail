# Platform Mail Operations (Slice 1: Metrics and Outbound Log): Implementation Plan

Spec: [admin-mail-operations.zh-CN.md](admin-mail-operations.zh-CN.md). Reuses the operator grant,
the operator browser session, and the audit trail of
[admin-operator-console.md](admin-operator-console.md).

Owners: the `operator` starter owns who may read (grants, capabilities, session); the
`organization` starter owns organization and membership aggregates; the `mailbox` starter owns
mail aggregates and the outbound log. `contracts/OPERATORS.md` and `contracts/openapi.yaml` join
the API to the Admin Console.

## Scope

| Spec module | This slice |
|---|---|
| Platform overview | In: organization count, distinct members (active, disabled shown apart), mailbox count, one UTC day's sent/failed/queued plus delivered/bounced/complained, a daily trend of up to 31 days (default 7) |
| Organizations and members | In: per-organization list for one day; the members link reuses the existing operator member list |
| Outbound log and content | In: the outbound log (metadata only). **Out:** reading a body snapshot |
| Content access audit | **Out** (it only exists to audit body reads) |

### Pending business decision (blocks the out-of-scope part only)

The spec's last paragraph leaves open who the initial platform reviewer is and how far body reading
goes (organization broadcasts only, or ordinary business mail too). Until the owner decides:

- no endpoint returns a message body, HTML, recipients, or attachments to an operator;
- there is no content-read capability, reason prompt, or content audit record;
- the outbound log list never preloads a body (the spec requires that anyway).

The follow-up slice adds a `mail_content:read` capability on the same grant model, a
`POST`-with-reason read that writes the audit record before returning the snapshot, sensitive-kind
exclusion (private inbox, login codes, account recovery), and the audit query. Aggregate definitions
in this slice do not change.

## Rulings

| # | Ruling |
|---|---|
| R1 | **Separate read-only capability.** `platform_operators` gains `scope`: `full` (existing grants, unchanged) or `mail_metrics`. Capabilities: `full` → `operator:manage` + `mail_metrics:read`; `mail_metrics` → `mail_metrics:read` only. Organization owners/admins get nothing automatically; grants stay CLI-only. |
| R2 | **CLI.** `operator:grant <email> [--scope=full\|mail-metrics]` (default `full`, so existing scripts keep their meaning). Granting an existing account another scope changes the scope and is audited like a grant; the same scope again is a no-op. `operator:list` gains a SCOPE column. `operator:revoke` removes either scope. |
| R3 | **Session.** Either scope may sign in to the Admin Console. `GET/POST /v1/operator/session` add `capabilities` (array). Every request re-reads the grant, so a scope change applies on the next request. A route the session's capabilities do not cover returns `403 OPERATOR.CAPABILITY_REQUIRED` (the console shows a forbidden state and stays signed in); no grant at all is still `403 OPERATOR.FORBIDDEN` (the console signs out). |
| R4 | **Routes needing `mail_metrics:read`:** the three new routes plus the read-only organization surface the spec reuses (`GET /operator/organizations`, `/organizations/:id`, `/organizations/:id/members`). Every other operator route needs `operator:manage`. Member rows keep the existing operator member shape (including email), as the spec reuses that list. |
| R5 | **New routes** (registered only with the `mailbox` and `organization` starters): `GET /v1/operator/mail-metrics?date=&days=`, `GET /v1/operator/mail-metrics/organizations?date=&q=&page=&per_page=`, `GET /v1/operator/outbound-messages?date=&organization_id=&mailbox_id=&status=&page=&per_page=`. `date` is a UTC `YYYY-MM-DD`, default today, never in the future; `days` 1–31, default 7; pages default 20, max 100. |
| R6 | **Counting rules** (spec section on counting rules). A counted message is a `mail_messages` row with `direction = 'outbound'`, `source = 'zmail'` (synced Gmail/Outlook copies were not sent by Zmail), in a mailbox that is not soft-deleted and belongs to an organization. Its day is `created_at` in UTC; each row counts once under its current status (`sent`, `failed`, `queued`). Delivered/bounced/complained count distinct messages with at least one such event, so duplicate receipts never inflate anything. Inbound mail, pending broadcast recipients without a message, and transactional messages (their own table and contract) are not counted. Sealed history counts: it was really sent. |
| R7 | **No fan-out.** The organization starter aggregates organizations and memberships (`PlatformOrganizationDirectory` seam); the mailbox starter aggregates mailboxes and messages per organization; the mailbox operator handler joins the two results in memory by organization ID. Members exclude soft-deleted users; disabled users are counted in `members` and reported apart as `disabled_members`. The platform member count is distinct users across all organizations. |
| R8 | **Organization list order**: sent desc, then organization ID asc; zero-send organizations are included. The join loads the directory rows of every organization matching `q` (ID, name, member count only); acceptable for the expected platform size, and the follow-up for very large deployments is a daily rollup table. |
| R9 | **Outbound log**: one UTC day; ordered `created_at desc, id desc`; returns `id, organization_id, organization_name, mailbox_id, mailbox_address, from_address, subject, status, failure_code, last_event, created_at, send_attempted_at`. Never a body, HTML, recipients, or attachments. |
| R10 | **Admin Console**: an "Operations" area (`/console/operations`, `/console/operations/organizations`, `/console/operations/messages`) behind the build feature `mailbox`; navigation hides items the session's capabilities do not cover. Every view distinguishes empty, error-with-retry, and forbidden; a failed request never renders as zero. The selected date travels in the URL and survives filter changes, paging, and navigation between the three views. |
| R11 | **Migration** `2026_11_29_090000_add_platform_operator_scope`: `ADD COLUMN scope varchar(16) NOT NULL DEFAULT 'full'` plus a CHECK. Catalog-only column add on PostgreSQL 11+; the CHECK scans the (tiny) grants table under a brief `ACCESS EXCLUSIVE` lock. Dropping the column would silently turn a `mail_metrics` grant into a full one, so Down deletes `mail_metrics` grants first. |

## Acceptance (spec table → proof)

| Spec row | Proof |
|---|---|
| 1 (2 orgs, 2 and 3 members, one shared → 2 orgs, 2, 3, distinct 4) | PostgreSQL test in `mailbox` operator metrics |
| 2 (2 sent, 1 failed, 1 inbound, other org zero → 2/1, log shows those two, zero row kept) | PostgreSQL test |
| 3 count part (two `delivered` receipts → sent unchanged) | PostgreSQL test; the snapshot part waits for the content slice |
| 4 (body URL access) | Out of scope: no body route exists; permission tests cover the metrics routes for member, org owner, metrics-only, full operator |
| 5 (both sides of UTC midnight; API failure shows error and retry) | PostgreSQL test for the day split; Admin Vitest for error/retry, empty, forbidden |

## Tasks

1. Contract: `contracts/OPERATORS.md` and `contracts/openapi.yaml`; `make contract-generate`,
   `make contract-check`.
2. Migration + operator manifest, golden schema, Up/Down test, `UPGRADING.md`.
3. Operator grant scope, capabilities, middleware split, CLI flag; handler and repository tests.
4. Organization directory seam; mailbox aggregates, outbound log, handler; PostgreSQL tests first.
5. Wire, route catalog.
6. Admin Console Operations area, i18n (en-US, zh-Hans), Vitest for the states.
7. Kest flow `operator_mail_metrics.flow.md` (log provider only).
8. `make check`.
