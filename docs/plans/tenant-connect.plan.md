# Tenant Connect (Roadmap C4): Reduced Plan

Spec: [mail-connectors-roadmap.zh-CN.md](mail-connectors-roadmap.zh-CN.md) §3.2, §4, §5.2, §7 (C4), §13;
[MAILBOX_ACCESS_PRD.zh-CN.md](../MAILBOX_ACCESS_PRD.zh-CN.md);
[contracts/MAILBOXES.md](../../contracts/MAILBOXES.md) (Access, External Mailboxes, Tenant Connections, Agent Grants).
Builds on [shared-connected-mailboxes.plan.md](shared-connected-mailboxes.plan.md) (C3).

Owner: the `mailbox` starter (API) owns every rule, record, and route below. `contracts/` joins it to
`web/` and `admin/`, which only regenerate types here; their screens are separate work.

## Scope

An organization **owner** authorizes a whole Google Workspace or Microsoft 365 tenant once. Owners
and admins then pick mailboxes from that tenant's directory and assign each one either to a member
(a personal connected mailbox) or as a shared connected mailbox with members, without anyone signing
in to the mailbox. Each assigned mailbox is synced by the existing worker with a short-lived token the
tenant credential mints for that one mailbox.

Out of scope: any Web or Admin screen; a scheduled directory reconciliation (roadmap §12, tenant directory sync);
sending; push subscriptions.

## Actors

| Actor | May |
|---|---|
| Organization `owner` | Authorize, re-authorize, and revoke a tenant; everything an admin may. Never reads mail through the tenant. |
| Organization `admin` | List tenants, read a tenant's directory, assign mailboxes, unassign them (disconnect). Cannot authorize or revoke. |
| Member | Nothing on tenants (`403 PERMISSION.DENIED`). Reads the personal mailboxes assigned to them; shared ones per their permission. Cannot unbind, reconnect, or purge a tenant assignment (`403` / `422`). |
| Agent (API key) | No tenant route exists on the API key surface. Only the existing per-mailbox grants, issued by whoever may today. |

## Provider Flows

**Google Workspace: domain-wide delegation with the customer's own service account.** The owner
creates a service account in the customer's Google Cloud project, authorizes its client id in the
Workspace Admin console for `https://www.googleapis.com/auth/gmail.readonly` and
`https://www.googleapis.com/auth/admin.directory.user.readonly`, and posts the JSON key with an
admin address (`admin_subject`). Zmail signs an RS256 JWT (standard library), exchanges it at the
configured token endpoint (never the key's own `token_uri`) for a directory token as the admin, and
reads one Directory API user to learn the `customerId`. The key, the admin subject, and the customer
id are sealed together. Why: Google has no admin-consent redirect for domain-wide delegation; a
customer-owned key keeps the trust anchor in the customer's hands, needs no Google app review, and is
revoked by the customer by deleting the key or the delegation.

**Microsoft 365: the organization's own application with application permissions.** The owner posts
the Entra tenant id with the client id and secret of an application registered in the customer's own
tenant (Graph application permissions `Mail.Read` and `User.Read.All`, admin consent granted there);
Zmail proves them at once with a client credentials token and one `/users` read. A client id without a
secret, or the reverse, is `422`.

Removed after review (default adopted, pending confirmation): the operator-application variant below
was implemented first and then withdrawn, because a consent redirect's `state`, `tenant`, and
`admin_consent` are caller-supplied and an admin consent to a shared operator application cannot be
tied to the organization claiming the tenant. Future work: bring it back only with an OIDC admin
sign-in proof in the same flow (an `id_token` whose `tid` is the tenant and whose `wids` show an
administrator role). The withdrawn design, for the record:

1. *Operator's application* (default): the owner posts the Entra tenant id (a GUID). The answer is
   `202` with an `authorization_url` pinned to that tenant:
   `{login}/{tenant}/v2.0/adminconsent?client_id&scope=https://graph.microsoft.com/.default&redirect_uri&state`.
   The single-use state is stored exactly like a sign-in's (hash only, 10 minutes, one use, same
   user), with the tenant id sealed behind a `tenant:` marker that neither the personal completion
   nor an older instance can open. Microsoft sends the browser back to `EXTERNAL_MAIL_REDIRECT_URL`
   with `admin_consent`, `tenant`, and `state`; the callback page posts them to
   `POST /v1/external-mail-tenants/completions`. Zmail requires `admin_consent=True`, the pinned
   tenant, and then proves the consent with a client credentials token for that tenant and one
   Graph `/users` read. The operator's application needs the application permissions `Mail.Read`
   and `User.Read.All`.
2. *Organization's own application*: the owner also posts `client_id` and `client_secret` of an app
   registration in their tenant; there is no redirect, the credential is proven at once (`201`).

Either way the customer should scope the app to the assigned mailboxes with **RBAC for
Applications in Exchange Online** (a management scope plus an `Application Mail.Read` role
assignment); until then `Mail.Read` reads every mailbox of the tenant at Microsoft, though Zmail only
reads assigned ones. Why the default: it reuses the redirect URL and the state mechanism; the second
variant exists because only the customer's own app removes the residual risk below.

## Review Decisions (default adopted, pending confirmation)

| # | Decision |
|---|---|
| A | Microsoft tenants only with the organization's own application; the operator-app consent flow, its completion route, and its pending-state marker are removed. |
| B | A directory cursor carries only the provider's page token (Graph `$skiptoken`, Google `pageToken`), checked for shape; the server builds every provider URL. A malformed or refused cursor is `400 COMMON.INVALID_INPUT` and never stops the tenant. |
| C | `EXTERNAL_MAIL_TENANTS_ENABLED` (default false). Off: the tenant list is empty, authorizing is `404 MAILBOX.EXTERNAL_PROVIDER_UNAVAILABLE`, id routes are `404 MAILBOX.EXTERNAL_CONNECTION_NOT_FOUND`, no connector advertises `tenant`; assigned mailboxes keep syncing. |
| D | `external_mail_address_records` remembers the last personal assignee of an address; while that member is in the organization nobody else gets the address (personal, shared, or self-bound): `409 MAILBOX.EXTERNAL_ACCOUNT_TAKEN`. Only the handover releases it. Owners and admins may purge a disconnected personal assignment; the record survives. |
| E | Each assignment transaction holds the tenant row `FOR SHARE` and requires `active`; revocation takes it `FOR UPDATE`. |
| F | Marking a tenant lost matches the `authorized_at` of the credential the check used. |
| G | With the organization's own application every token-endpoint refusal (400/401/403) is tenant-caused; 429, 5xx, and transport errors never stop the tenant. |
| H | Checking a Microsoft tenant repeats the `/users?$top=1` read. |
| I | At most 50 live tenants per organization (`409 MAILBOX.EXTERNAL_CONNECTION_LIMIT`); the listing shows live tenants first. |
| N1 | Second review: the personal-assignment record survives departure, handover, purge, and tenant revocation, and stores a privacy boundary (the departure time, or the handover time when handed over while still a member, whichever is later). A mailbox created or restored for someone other than the last personal holder (or as shared) starts at that boundary: the fetch asks for nothing older and every older copy is dropped at ingest. An assignment awaiting handover cannot be disconnected, purged (manually or by the retention worker), or reassigned. Self-binding by anyone but the last personal holder stays refused (the simpler safe choice). |
| N4 | Third review: the address record keeps a floor that only ever rises and an exempt member (the first personal holder while nobody else held it). Rule: a holder only ever reads mail received during their own tenures. Every non-exempt assignment or restore starts at max(floor, now); `received_not_before` only rises (`GREATEST`) and is never cleared. A returning holder keeps copies their mailbox still holds but gets no refetch of mail from outside their tenure. Proven by a property test over random assign/unassign/delete/handover/leave/rejoin/revoke sequences among three members. |
| N5 | After revoke and re-authorization, assigning back to the same target restores the existing mailbox and connection under the new tenant id. |
| N6 | Mail without a provider receive time is dropped when a floor is set (it shows as fetched but not stored in the sync run); the self-bind check also matches the directory id the provider reports (Outlook `/me` id); a departed member's mailbox awaiting handover cannot be deleted (409). Strict self-bind rule kept, pending confirmation. |
| N7 | Fourth review: a member's own binding is a tenure too. The address record is written by every binding of any kind (own grant, team account, tenant), under every connector, at the moment it is made, so no disconnect or purge erases it; the migration records existing connections. A tenant assignment to anyone but the exempt first holder therefore starts after it. The personal-holder and self-binding refusals apply only once a tenant has assigned the address (`tenant_held`), so own-grant-only histories keep the C3 rules. Simplest design without a gap: evidence is written before it could ever be lost, never reconstructed later. |
| N8 | Handovers of tenant mailboxes and departures take the organization's binding lock that assignments take. The tenure property test now covers shared assignments and member changes, self-bindings and their forced disconnects, and user deletion, checks liveness, and runs 12 seeds in the suite to keep the package within its time budget (`TENANT_TENURE_SEEDS=500 go test ./internal/modules/mailbox/ -run TestTenantTenureProperty -timeout 60m` runs more). |
| N2 | Listing and revoking tenants work while tenant connect or external mail is off; authorize, directory, and assignments stay unavailable. |
| N3 | Owner/admin views of a personal assignment's connection (sync runs, purge result, dev delivery) carry no message content or headers (tested); the holder record matches the address case-insensitively and the directory id. |
| J | Unique violations go through `isUniqueViolation`; a restored shared assignment's members are replaced by the posted set; Microsoft application tokens are cached in memory for their lifetime (1024 entries, keyed by a hash); service account keys must have 2048 to 4096 bits; `mailboxes.external_mail_tenant_id` is indexed; re-authorization resumes only the connections the tenant credential stopped. |

## Rulings

| # | Ruling |
|---|---|
| R1 | Migration `2026_12_08_090000_add_external_mail_tenants`: table `external_mail_tenants` (organization, provider, tenant_identifier, display_name, status `active`/`needs_reauthorization`/`revoked`, encrypted_credential, authorized_by_user_id, last_error_code, authorized_at, revoked_at, timestamps); CHECKs on provider shape, status, non-empty identifier, and "a live tenant holds a credential"; unique `(provider, lower(tenant_identifier)) WHERE status <> 'revoked'` across the instance. `external_mail_connections` gains `tenant_id` and `tenant_directory_id` with `CHECK (tenant_id IS NULL OR encrypted_refresh_token = '')`; `mailboxes` gains `external_mail_tenant_id` with `CHECK (external_mail_tenant_id IS NULL OR origin = 'connected')`. Down refuses while a tenant exists. |
| R2 | Seam: `ExternalMailConnector.Tenant` (nil = no tenant support, listed as `capabilities.tenant`) implements `ExternalMailTenantSource`: `TenantConsentURL`, `AuthorizeTenant`, `VerifyTenant`, `ListTenantDirectory` (paged, ≤ 100, opaque cursor), `LookupTenantMailbox`, `MailboxToken`. `ExternalMailToken` gains `AccessToken` and `DirectoryID`: a source given an access token reads that mailbox without refreshing (Gmail `/users/me` as the delegated subject, Graph `/users/{id}`). |
| R3 | Authorize (owner only; Microsoft with the organization's own application only, see Review Decisions). A tenant identifier already live in this organization is re-authorized in place (`200`): new credential, status `active`, and its `needs_reconnect` connections return to `active`. Live in another organization: `409 MAILBOX.EXTERNAL_ACCOUNT_TAKEN`. A malformed request: `422 MAILBOX.EXTERNAL_AUTHORIZATION_INVALID`; a refused credential: `422 MAILBOX.EXTERNAL_AUTHORIZATION_FAILED`; a provider without tenant support: `404 MAILBOX.EXTERNAL_PROVIDER_UNAVAILABLE`. Authorizing creates no mailbox and grants nobody any access. |
| R4 | Directory (owner/admin, tenant `active` else `409 MAILBOX.ACCESS_CONFLICT`): one provider page, each row `{address, display_name, directory_id, assigned, assignment, connection_id, mailbox_id, available}`. `assigned` is a live connection of this tenant; `available` is false when any live connection of the organization holds the address. |
| R5 | Assignments (owner/admin): 1 to 100 rows, each `{address, assign: "personal", user_id}` or `{address, assign: "shared", display_name?, members}`; per-row result `{address, status: created/restored/unchanged/failed, connection_id, mailbox_id, error_code}`, `200` overall. Each row: the address must be in the tenant directory (`MAILBOX.EXTERNAL_ACCOUNT_MISMATCH`), personal needs a current member (`MAILBOX.ACCESS_INVALID`), shared follows `POST /v1/mailboxes` members (`MAILBOX.ACCESS_INVALID`). Idempotent per address: the same live assignment answers `unchanged`. |
| R6 | Account uniqueness keeps `idx_external_mail_connections_account`. A live connection of the address that is self-bound, shared (C3), another tenant's, or this tenant's with another target: `MAILBOX.EXTERNAL_ACCOUNT_TAKEN`; and a member's self-binding of a live tenant-assigned address is `409 TAKEN`. A revoked tenant assignment with the same target and its mailbox is restored; other revoked connections are freed as in C3 (purge, then assign). |
| R7 | Limits under the organization's shared binding lock: the per-tenant ceiling `EXTERNAL_MAIL_MAX_TENANT_ASSIGNMENTS` (default 1000, 1 to 10000) counts live connections of the tenant; a shared assignment also counts against `EXTERNAL_MAIL_MAX_SHARED_CONNECTIONS`; a personal assignment never counts against the member's 10. Over either: `MAILBOX.EXTERNAL_CONNECTION_LIMIT` on the row. |
| R8 | A personal assignment is a `personal` + `connected` mailbox owned by the member, with `external_mail_tenant_id` set. It is private like a self-bound one: owners and admins never read it. Because the address is the organization's, the organization role administers it as a Zmail personal mailbox (sees it in the directory, deletes it, hands it over, reads its access history). |
| R9 | Leave: a personal assignment follows the Zmail personal-mailbox rule: `owner_left` (awaits handover), agent grants end, the connection is **not** revoked and keeps syncing, and nobody reads it. Handover (existing route) seals history before the departure, converts the mailbox to `shared` + `connected`, makes the connection the organization's (`shared`, no owner), and adds the successor with `send`, which on a connected mailbox reads only. A rejoin before handover restores it. What the connected origin makes impossible: the successor cannot send as the address (read-only origin); nothing else falls back to revoke-on-leave. |
| R10 | A shared assignment follows C3 rules exactly (members, webhooks, triage, agent grants, read-only). |
| R11 | Sync: for a tenant connection the worker loads the tenant; a tenant that is not `active` is an authorization failure. Otherwise it mints the mailbox token and fetches with it. An authorization failure is checked against the tenant (`VerifyTenant`): a refused tenant credential marks the tenant `needs_reauthorization` and every live connection of it `needs_reconnect` in one statement (all sync stops at once); a credential that still works marks only that connection `needs_reconnect`. |
| R12 | Revoke tenant (owner only): one transaction marks it `revoked`, erases its credential, revokes every connection of it through the existing revoke steps (triage off, agent grants end, `connection_revoked` with `reason: tenant_revoked`). Unassigning one mailbox is the existing connection disconnect by an owner/admin, recorded as `connection_force_disconnected`. Members cannot. |
| R13 | Audit: `external_mail_tenants` create/update/delete and each created/restored assignment (`external_mail_connections` create, `reason: tenant_assignment`) in the organization audit log; `connection_bound` / `connection_reconnected` / revocations in the mailbox access history with `tenant_id`. Never a credential, key, secret, or token, in either log or in slog. |

## Workflows

1. Google: `POST /v1/external-mail-tenants {provider: gmail, service_account_key, admin_subject}` → `201` tenant.
2. Microsoft: `POST /v1/external-mail-tenants {provider: outlook, tenant_id, client_id, client_secret}` → `201`/`200`.
3. `GET /v1/external-mail-tenants/{id}/directory?cursor&per_page` → pick rows →
   `POST /v1/external-mail-tenants/{id}/assignments` → per-row results; retry failed rows.
4. Worker syncs assigned mailboxes; credential loss stops the whole tenant; re-authorize restores it.
5. `DELETE /v1/external-mail-tenants/{id}` revokes everything at once.

## Failure Behavior

Provider calls have per-request timeouts (20 s) and bounded responses (token 64 KiB, directory page
1 MiB). Assignment rows run one at a time, each in its own transaction, so a failed row leaves the
others intact and can be retried alone. A credential is never logged; provider error text is reduced
to a short code.

## Tests (PostgreSQL + httptest)

Service (fake connector): owner-only authorize, admin/member refusals, agents have no route;
re-authorize and cross-organization uniqueness; directory marks; assignment rows (personal, shared,
idempotent, mismatch, non-member, taken by self-bound and vice versa, ceiling, shared limit, member's
limit untouched); access (member reads personal, admin administers without reading, shared members);
leave → awaiting handover with sync continuing → handover seals and converts; tenant credential loss
→ all `needs_reconnect`, re-authorize restores; tenant revoke stops all; audit and access history
without secrets. Adapters (httptest): Google JWT signature and claims, directory paging, lookup,
mailbox token; Microsoft consent URL, client credentials, directory paging and host pinning, lookup,
app-only fetch path. Migration Up/Down, constraints, golden schema. Config bounds.

## Verification

`gofmt`, `golangci-lint` v2.14.0, `go test ./...` with `FASTBUILD_TEST_POSTGRES_DSN`, golden schema,
`make starter-catalog`, `make route-catalog-check`, `cd contracts && pnpm generate && pnpm check`,
Web/Admin type-check, then the local gate `make check` with `CI=true`.

## Open Questions For The Product Owner

1. Defaults adopted: per-tenant ceiling 1000 (1 to 10000); a handover of a personal assignment does
   not check the organization's shared limit.
2. Decided after review: only the organization's own Microsoft application. Should the operator
   application return later with an OIDC admin sign-in proof?
3. `GET /v1/external-mail-tenants` is open to admins too (they need the ids to assign). Confirm.
4. Should a scheduled directory reconciliation (roadmap §12) flag deleted or suspended directory users?
