# Shared Connected Mailboxes (Roadmap C3): Reduced Plan

Spec: [mail-connectors-roadmap.zh-CN.md](mail-connectors-roadmap.zh-CN.md) §3.2, §4, §5.2;
[MAILBOX_ACCESS_PRD.zh-CN.md](../MAILBOX_ACCESS_PRD.zh-CN.md);
[contracts/MAILBOXES.md](../../contracts/MAILBOXES.md) (Access, External Mailboxes, Agent Grants).
Builds on [mailbox-origin.plan.md](mailbox-origin.plan.md) (C1), which already allows
`shared` + `connected` rows and keeps every connected mailbox read-only.

Owner: the `mailbox` starter (API) owns every rule, record, and route below. `contracts/` joins it to
`web/` and `admin/`, which only regenerate types here; their screens are separate work.

## Scope

An organization `owner` or `admin` binds a **team account** hosted at an outside provider (for
example `support@` at Google Workspace) through any connector, OAuth or app password, and assigns
members exactly like creating a shared Zmail mailbox. The result is a `shared` + `connected` mailbox
whose connection belongs to the **organization**, not to the person who bound it.

Out of scope: converting a personal connection into a shared one or back (unbind and bind again);
tenant-wide binding (C4); any Web or Admin screen.

## Actors

| Actor | May |
|---|---|
| Organization `owner` / `admin` | Start a shared binding; reconnect, disconnect, list, see sync runs of, and purge every shared connection; manage members, webhooks, triage, agent grants (existing matrix); delete the mailbox. Never reads mail without being a member (adding oneself is recorded, as today). |
| Member with `manage` on the shared connected mailbox | Reconnect, disconnect, list, see sync runs, purge that connection; manage members, webhooks, triage, agent grants (existing matrix). Reads it. |
| Member with `view` / `send` | Reads it with their own read state. `send` grants nothing more: the origin is read-only. |
| Other members | Nothing; the mailbox and connection are invisible (`404`). Starting a shared binding is `403 PERMISSION.DENIED`. |
| Person who bound it | Recorded as `bound_by_user_id` only. Leaving the organization does not revoke the connection and it never counts against their 10 personal connections. |

## Request Shape

Both binding routes take one optional object, `sharing`. Absent, the personal flow is unchanged.

```json
POST /v1/external-mail-authorizations
{ "provider": "gmail", "backfill_days": 30,
  "sharing": { "display_name": "Support", "members": [ { "user_id": 7, "permission": "manage" },
                                                      { "user_id": 9, "permission": "view" } ] } }

POST /v1/external-mail-credentials
{ "provider": "imap", "host": "imap.example.com", "username": "support@example.com",
  "password": "app-password", "sharing": { "members": [ { "user_id": 7, "permission": "manage" } ] } }
```

Why this shape: `members` is the same item shape and rule set as `POST /v1/mailboxes` with
`type: shared` (1 to 100 current members, unique, `view` / `send` / `manage`), so clients reuse the
member picker and the server reuses `validateMailboxAccess` and `resolveMembers`. One optional object
keeps the personal request byte-for-byte compatible, names the intent explicitly (no flag that could
be mistaken for "share my own account"), and leaves room for later shared-only options. The account
address is never taken from the caller, so `display_name` (optional, defaults to the account address)
is the only mailbox field in it.

Reconnecting a shared connection uses the existing `connection_id`: on the OAuth start route as
today, and, new, as an optional `connection_id` on the credentials route (an IMAP shared account has
no owner who could "post the password again" to restore it). `sharing` with `connection_id` is
`422 MAILBOX.EXTERNAL_AUTHORIZATION_INVALID`.

## Rulings

| # | Ruling |
|---|---|
| R1 | Migration `2026_12_07_090000_add_shared_external_mail`: `external_mail_connections.shared boolean NOT NULL DEFAULT false`, `bound_by_user_id bigint REFERENCES users(id) ON DELETE SET NULL` (backfilled from `owner_user_id`), `CHECK (NOT shared OR owner_user_id IS NULL)`, partial index `(organization_id) WHERE shared`; `external_mail_authorizations.sharing jsonb` (nullable). Down refuses while a shared connection exists. |
| R2 | A shared connection has `owner_user_id` NULL, `shared` true, `bound_by_user_id` the binder. Every owner-keyed path (per-member limit and lock, leave revocation, "own" listing) keeps filtering on `owner_user_id`, so none of them touches it. |
| R3 | Organization limit: `EXTERNAL_MAIL_MAX_SHARED_CONNECTIONS` (default 20, 1 to 100, a set but invalid value stops startup) live (`active` / `needs_reconnect`) shared connections per organization, counted under an organization binding lock. Over it: `409 MAILBOX.EXTERNAL_CONNECTION_LIMIT` (reused code). |
| R4 | Start: `sharing` requires `owner`/`admin` (`403 PERMISSION.DENIED`), validates the members as `POST /v1/mailboxes` does (`422 MAILBOX.ACCESS_INVALID`), and stores them on the pending sign-in. Completion re-checks the role and re-resolves the members (membership may change within the 10 minutes) before anything is stored; a refusal hands the grant back. |
| R5 | Completion with `sharing` and a new account: one transaction creates the `shared` + `connected` mailbox (address = account, outside the organization's mailbox limit like every connected mailbox), the `created` event, one `member_set` per member, the connection, and `connection_bound`. `201`. |
| R6 | Account uniqueness stays the existing index (organization, provider, lower(address)): a live connection of the other kind is `409 MAILBOX.EXTERNAL_ACCOUNT_TAKEN` in both directions. A revoked **personal** connection is freed for a shared binding as for any other binder (purge `rebound`, then bind). A revoked **shared** connection whose mailbox still exists is never taken over (`409 TAKEN`): it is restored by a reconnect or released by deleting the mailbox; once its mailbox is deleted it is freed like a revoked personal one. A shared binding that finds a live shared connection of the account is `409 TAKEN` (reconnect it instead). |
| R7 | May manage a connection (`mayManageConnection`): personal: its owner; shared: an `owner`/`admin`, or a `manage` member of its mailbox, and the mailbox must exist. Used by reconnect start and completion, `GET /v1/external-mail-connections` (extended), sync runs, purges, and `DELETE /v1/external-mail-connections/:id`. Anyone else: `404 MAILBOX.EXTERNAL_CONNECTION_NOT_FOUND`. |
| R8 | Reconnect of a shared connection: same account or `409 MISMATCH`; restores the same connection and mailbox (`connection_reconnected`, actor = caller); a `revoked` one needs a free organization place (R3). |
| R9 | Disconnect of a shared connection by anyone allowed by R7, or through the connection directory by an `owner`/`admin`: the existing revoke path (`revoked_at`, grant handed back, triage off, agent grants ended, retention purge), recorded as `connection_revoked` with the caller as actor, plus an organization audit entry (`reason` `shared_disconnect`). |
| R10 | `GET /v1/external-mail-connections` lists the caller's own personal connections, then the shared connections they may manage (all of them for `owner`/`admin`), each with `shared`. Bounded by 10 + 100. |
| R11 | Connection directory: a shared entry carries `shared: true`, the full `account_address`, and its `mailbox_id`, with `owner_user_id` / `owner_display_name` null; personal entries stay masked with `account_address` and `mailbox_id` null. `masked_account_address` stays on every entry. |
| R12 | Access, members, webhooks, triage, agent grants, deletion: no new rule. C1's `MailboxAccessFor` already gives shared connected mailboxes the shared matrix minus `send` / `organize`; `send()` refuses connected; agent `draft` is refused for connected; webhook and triage switches follow `ManageMembers`; `DeleteMailbox` revokes the connection; handover refuses connected. This iteration pins all of them with tests. |

## Workflows

1. Admin starts: `POST /v1/external-mail-authorizations` with `sharing` → role check → member
   validation → pending sign-in with `sharing` → `201 { authorization_url }`.
2. Provider callback → `POST .../completions` → take sign-in → role and member re-check → exchange
   code → `saveExternalBinding` under the binder's lock and the organization lock → R5 / R6.
3. IMAP: `POST /v1/external-mail-credentials` with `sharing` → role and member checks before the rate
   limit and the sign-in to the server → same tail.
4. Reconnect: start with `connection_id` (R7), complete (R7 again, R8). IMAP: credentials with
   `connection_id`.
5. Disconnect (R9), delete the mailbox (revokes), retention purge (unchanged).

## Failure Behavior

Every refusal after a grant was issued (`403`, `409`, `422` on members) hands it back to the
provider. Concurrent bindings of the same account resolve through the unique index to
`409 TAKEN`. Concurrent shared bindings of one organization serialize on the organization lock, so
the limit is never exceeded.

## Tests (PostgreSQL)

`shared_connected_mailbox_test.go`: who may bind (admin yes, member `403`, both flows); member
validation (empty, 101, duplicate, non-member, bad permission → `422`, nothing stored); OAuth and IMAP
create the mailbox, members, events, and connection correctly; two members read with separate read
state while a non-member and a non-member admin cannot; no send path (send, reply, compose from,
agent draft); binder leaving keeps it active; organization limit; uniqueness against personal in both
directions (live → `409`, revoked personal → freed); reconnect and disconnect authorization (manage
member and admin yes, view member and outsider `404`); listing; directory unmasked vs masked; webhook
and triage switch rules; agent grant issue rules; delete revokes; members `403` on start.
Config: limit default, bounds, invalid. Migration: Up/Down, CHECK, golden schema.

## Verification

`gofmt`, `golangci-lint` v2.14.0, `go test ./...` with `FASTBUILD_TEST_POSTGRES_DSN`, golden schema,
`make starter-catalog`, `cd contracts && pnpm generate && pnpm check`, Web/Admin type-check, then the
full local gate `make check` with `CI=true`.

## Open Questions For The Product Owner

1. Default organization limit of 20 shared connections (tunable 1 to 100): confirm, or tie it to a plan.
2. Should a `manage` member (not only owners/admins) be allowed to **bind** a team account into an
   existing shared mailbox? This plan says no: binding creates the mailbox, which only owners/admins do.
3. Converting a personal connection to shared (an employee's account becomes the team's) is out of
   scope; the workaround is unbind, then bind as shared (copies are re-synced from the backfill window).
