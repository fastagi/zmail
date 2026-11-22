# Connector Acceptance Report: `main`, Round 3

- **Tested commit:** `4cef3f3ea4175af246fd15b04773a2ebbc31279f` (`origin/main`, "Merge pull request #197 from
  fastagi/fix/invitation-copy-link")
- **Baseline (round 2):** `d9d1d1ac1710309ef626fee4dacbde3384a17ae3`
  ([`connectors-acceptance-main-2.md`](connectors-acceptance-main-2.md))
- **Date:** 2026-11-22 (UTC)
- **Tester:** independent acceptance agent (did not write the code; no product code changed)
- **Scope:** what merged after round 2: tenant connect (#141), push subscriptions (#178, #194, #196),
  provider releases (#194, #196), the round-2 fixes (#135), the Web `tenant_id` schema fix (#181), and
  the one-time invitation link (#195 / #197). Contracts: `contracts/MAILBOXES.md` (External Mailboxes,
  Shared Connected Mailboxes, Connections, Push Subscriptions, Connection Directory, Tenant
  Connections, Agent Grants), `contracts/ORGANIZATIONS.md` (Invitation Lifecycle), `UPGRADING.md`,
  `api/docs/EXTERNAL_MAIL_PUSH.md`, `api/docs/EXTERNAL_MAIL_DEVELOPMENT.md`.

## Summary

| # | Case | Result |
|---|---|---|
| A1 | Bind an own mailbox with the `fake` connector | Pass |
| A2 | Bind a shared (team) mailbox; member refused | Pass |
| A3 | First sync and incremental sync through the dev-only fake delivery route; no duplicate copy | Pass |
| A4 | Reconnect (same account, other account, another member's connection) | Pass |
| A5 | Member unbind and purge | Pass |
| A6 | Admin forced disconnect; audit copy visible to the other managers (round-2 N1) | Pass |
| A7 | Member leaving revokes the member's connection | Pass |
| A8 | Agent grants and the agent feed on connected mailboxes; revoke on unbind | Pass |
| A9 | Revoked connection still listed at 10 live connections (round-2 N3) | Pass |
| A10 | Web connection list in a real browser against the real API (with `tenant_id`, with and without `push`) | Pass |
| A11 | Web admin connection directory in a real browser, including keyset paging | Pass |
| A12 | Sync runs (API and the Web dialog) | Pass |
| A13 | Web IMAP credentials form (API restarted with `EXTERNAL_MAIL_PROVIDERS=imap`) | Pass |
| A14 | No mail content reaches owners, admins, or other members | Pass |
| B1 | Tenant switch off: no `tenant` capability, authorize `404`, directory and assignment `404` | Pass |
| B2 | Tenant authorize: admin and member refused, malformed body refused | Pass |
| B3 | Tenant authorize as owner with the `fake` connector | **Fail** (D1) |
| B4 | Directory paging; personal and shared assignment; private to its member | Blocked live (D1); automated tests pass |
| B5 | Member leaves: awaiting handover (no disconnect, purge, delete, reassign); handover seals history | Blocked live (D1); automated tests pass |
| B6 | "Only mail from your own tenure" on reassignment | Blocked live (D1); automated tests pass |
| B7 | Tenant revoke stops every assignment; tenant mailboxes never send or reply | Blocked live (D1); automated tests pass |
| B8 | List and revoke a stored tenant while the switch is off | Pass (fixture row) |
| C1 | Push off: both notification routes answer `404` | Pass |
| C2 | Push on: a notification makes the connection sync within the documented delay (`trigger` `push`) | Pass |
| C3 | A burst of notifications coalesces to one push sync per window | Pass |
| C4 | Unauthenticated junk is rate limited and does not block a real notification; handshake budget; body limits | Pass |
| C5 | Production config refuses push without `SERVER_TRUSTED_PROXIES` or the direct-exposure flag (and both together) | Pass |
| C6 | Gmail push refused without its service account (development and production) | Pass |
| C7 | Worker idle interval: 2 s with push on and no `--poll`, 30 s otherwise | Pass |
| C8 | `push` object on the connection read model (`active`, `last_notified_at`, `stopped`, `null` while off) | Pass |
| C9 | Gmail OIDC, real Graph handshake and delivery, end-to-end latency, delay metric | Blocked (needs real Google/Microsoft) |
| D1 | Revoke records a provider release and clears it once performed | Pass |
| D2 | Rebind while a release is still pending deletes it; the connection keeps working | Pass |
| D3 | Release expiry by the mail worker (`mail:broadcast:work`) | Pass |
| D4 | Release of a purged personal connection waits only for its own member's sign-in | **Fail** (D2) |
| E1 | Invitation link shown once after create (API and Web) | Pass |
| E2 | New link after resend with a mailer configured; the old link stops working | Pass |
| E3 | Resend without a mailer: no link, token kept | Pass |
| E4 | Never in the invitation list; `Cache-Control: private, no-store`; not in DOM or storage after closing | Pass |
| E5 | Members cannot list, create, or resend (no link) | Pass |
| E6 | Copy fallback when the clipboard refuses | Pass |
| E7 | zh-Hans confirmation and fallback | Pass |
| F1 | Migrations Up from the round-2 schema with round-2 data (address records backfilled) | Pass |
| F2 | Down (rollback of the 12 new migrations) and Up again | Pass |
| F3 | `UPGRADING.md` covers the new switches and migrations | Pass |

**Totals: 38 pass, 2 fail, 5 blocked** (45 cases).

Defects (none blocks use; none is a data or privacy risk):

| Id | Severity | One line |
|---|---|---|
| D1 | P3 | The `fake` connector advertises `capabilities.tenant: true` but a running stack has no way to register a fake tenant, so every tenant authorization answers `422 MAILBOX.EXTERNAL_AUTHORIZATION_FAILED` and the tenant flow cannot be exercised locally |
| D2 | P3 | A provider release whose connection was purged is postponed by **any** member's pending sign-in for the provider, not only by its own member's, which the contract does not say and which can keep delaying the hand-back until the 24-hour expiry |

## Environment And Method

- macOS, Node 24 (`/opt/homebrew/opt/node@24/bin` first on PATH), host Go toolchain, dev PostgreSQL
  container `zmail-dev-postgres-1` on `127.0.0.1:55432`.
- **Own databases** on that container, all created for this run and dropped at the end:
  `zmail_accept3` (the stack), `zmail_accept3_gotest` (`FASTBUILD_TEST_POSTGRES_DSN` for the Go
  tests, so `zmail_test` was not used), `zmail_accept3_migr` (upgrade path). `fastbuild` and
  `zmail_test` were not touched.
- **API:** binary built from the tested commit (`go build ./cmd/fastbuild`), run with the environment
  of `scripts/dev.sh` `api_environment` except `DB_NAME=zmail_accept3`, `SERVER_PORT=8065`,
  CORS/operator origins on 3065/4265, plus `EXTERNAL_MAIL_PROVIDERS=fake`, a local 44-character
  `MAILBOX_SECRET_ENCRYPTION_KEY`, `EXTERNAL_MAIL_REDIRECT_URL=http://localhost:3065/console/mailboxes/connected/callback`,
  `AUTH_REGISTRATION_EMAIL_CODE_REQUIRED=false`. `migrate`, `seed`, `operator:grant admin@example.com`
  by hand. Workers: `mail:broadcast:work --poll=2s` and `mail:external:sync` (first `--poll=30s` as
  `make dev` does, later without `--poll`). The API and the sync worker were restarted for single
  parts with: `EXTERNAL_MAIL_TENANTS_ENABLED=true` (part B);
  `EXTERNAL_MAIL_PUSH_ENABLED=true` and `EXTERNAL_MAIL_OUTLOOK_PUSH_NOTIFICATION_URL=https://zmail-r3.invalid/...` (part C);
  `MAIL_PROVIDER=` and `MAIL_FROM=` (no mailer, E3); `EXTERNAL_MAIL_PROVIDERS=imap` (A13); then back
  to the defaults.
- **Web:** `next dev --turbopack --port 3065` with the adapter on (`API_ADAPTER_ENABLED=true`,
  `API_UPSTREAM_URL=http://127.0.0.1:8065/v1`, `MOCK_BFF_ENABLED=false`), driven in the desktop
  app's built-in browser pane (real session cookies, real BFF, real API). The pane ran in the
  background, so some client renders appeared only after a screenshot, as in round 2; the pane's
  viewport (1024×768) differs from its screenshot frame (800×600), so a few buttons were activated
  by focusing them and pressing Enter. Neither is a product issue.
- **Accounts (organization 1, "Accept Three Org (synthetic)"):** owner U = `user` (user 2), admin
  A = `admin` (user 1, also operator), members M1 `r3m1` (user 3), M2 `r3m2` (4), M3 `r3m3` (5), M4
  `r3m4` (6), registered locally with `@accept3.test` addresses. They joined through the new
  invitation links (E1), so no token was written to the database by hand this time.
- **Fake accounts:** `<code>@external.invalid` (the fake binds the sign-in code as the local part):
  `r3alice` (M1, connection 1), `r3team` (shared, Outlook, connection 2: M1 `manage`, M2 `view`),
  `r3bob` (M1, 3), `r3carl` (M2, 4), `r3dave` (M3, 5), `r3erin` (M1, 6), `r3m4acc01`–`11` (M4,
  7–17), `r3frank` (M2, 18).
- **Mail:** delivered with `POST /v1/external-mail-connections/:id/fake-deliveries`
  (`api/docs/EXTERNAL_MAIL_DEVELOPMENT.md`), synthetic senders `carol@sender.test`, subjects tagged
  `R3SECRET`, `R3SHARED`, `R3AGENT`, `R3REBIND`.
- API calls: curl scripts in the session scratchpad (`c <who> <METHOD> <path> [json]` prints
  `[who METHOD path] <status> <body>`). Database reads with `psql` on `zmail_accept3`.
- No real mail provider was contacted. Hosts used were `.invalid`, `.test`, or loopback.

## Part A — Regression With Every New Switch At Its Default

Tenants off, push off for all of A.

### A1 — Own binding: Pass

`GET /external-mail-connectors` → `gmail` and `outlook`, `capabilities.tenant: false`.
`POST /external-mail-authorizations {"provider":"gmail"}` (M1) → `201` with an
`authorization_url` at `external-mail.invalid`; completion with code `r3alice` → `201` connection 1,
`mailbox_id 1`, `shared false`, `tenant_id null`, `push null`.

### A2 — Shared binding: Pass

U: `{"provider":"outlook","sharing":{"display_name":"R3 Team Support","members":[M1 manage, M2 view]}}`
→ `201` connection 2, `"shared": true`. M1 starting a shared binding → `403 PERMISSION.DENIED`. M2
(`view`) reads the shared copies (`GET /inbox-messages?mailbox_id=2` lists `R3SHARED hello team`,
`source outlook`); M3 (not a member) → `GET /mailboxes/2/messages` `404 MAILBOX.NOT_FOUND`; M2's
connection list is empty (does not manage it), M1's lists 1 and 2; M2 fake delivery to 2 → `404`.

### A3 — First and incremental sync: Pass

M1 fake delivery to connection 1 `message_id r3-a-1@sender.test` → `201`; the same id again →
`201`; `r3-a-2` → `201`. `mail_messages` for mailbox 1: exactly two rows (`r3-a-1`, `r3-a-2`), both
`source gmail`, `inbound`. Sync runs: `fetched 1 stored 1`, then `fetched 1 stored 0` (the repeat),
then `fetched 1 stored 1`, each `trigger poll`; the worker's own run before them `fetched 0`.

### A4 — Reconnect: Pass

M1 `POST /external-mail-authorizations {"connection_id":1}` and completion with `r3alice` → `200`,
same connection and mailbox, copies kept (2). Completion with `r3other` → `409
MAILBOX.EXTERNAL_ACCOUNT_MISMATCH`. M2 starting a reconnect of connection 1 → `404
MAILBOX.EXTERNAL_CONNECTION_NOT_FOUND`.

### A5 — Unbind and purge: Pass

M1 binds `r3bob` (3). Purge while active → `409 MAILBOX.EXTERNAL_CONNECTION_NOT_REVOKED`.
`DELETE /external-mail-connections/3` → `204`, again `204`. M2 purge → `404`. M1's list shows 3
`revoked`. M1 purge → `200 {"deleted_messages":0,"completed":true}`; the connection row is gone.
History of mailbox 3: `connection_bound`, `connection_revoked`, `copies_purged` (`reason owner`).

### A6 — Forced disconnect: Pass (round-2 N1 fixed)

M2 binds `r3carl` (4). M2 `DELETE /external-mail-connection-directory/4` → `403`. A → `204`, again
`204`. Mailbox 4 history: `connection_force_disconnected`, actor 1, subject 4. `GET
/audit-logs?resource=external_mail_connections`: U → one row (`delete`, `owner_user_id 4`, `provider
gmail`, `reason forced_disconnect`, no address); A → its own row; M2 → `[]`. The round-2 gap (only
the actor saw it) is closed.

### A7 — Member leaving: Pass

M3 binds `r3dave` (5), then `DELETE /organizations/1/members/5` (leave) → `204`. Connection 5
`revoked`; mailbox 5 history `connection_revoked_on_leave` (no actor, subject 5). In the Web
directory the row shows "Member #5" (no display name after leaving, as the contract says).

### A8 — Agent grants and feed: Pass

M1 creates key 2 (`mail:agent`). On connected mailbox 1: `read,draft` → `422
MAILBOX.AGENT_GRANT_INVALID` "a connected mailbox is read-only"; `read` → `201` grant 1. On shared
connected mailbox 2 (M1 `manage`): `read,draft` → `422`; `read` → `201` grant 2. U granting on 1 →
`404 MAILBOX.NOT_FOUND`. Fresh fake deliveries to 1 and 2 →
`GET /agent-mail-events` (`X-API-Key`) → `[(event 1, mailbox 1, message 13, grant 1), (2, 2, 14, 2)]`,
`next_cursor "2"`. `GET /agent-mail-messages/13` → the copy with `source gmail` and the code masked
(`code ••••••`). Revocation: M1 binds `r3erin` (6), grants `read` (grant 3), delivers, unbinds →
grant 3 `revoked`, `revoke_reason connection_revoked`; the feed returns nothing for it.
(A grant needs `expires_at`; without it `422 COMMON.VALIDATION_FAILED`, as documented.)

### A9 — Revoked connection at the limit: Pass (round-2 N3 fixed)

M4 binds `r3m4acc01`–`10` (10 live), unbinds `acc01` (7), binds `acc11` (`201`, 10 live again);
rebinding `acc01` → `409 MAILBOX.EXTERNAL_CONNECTION_LIMIT`. `GET /external-mail-connections` (M4)
→ 11 rows, including 7 `revoked`.

### A10 — Web connection list in a real browser: Pass

Signed in as M1 at `/console/mailboxes`: sidebar "Connected by me" lists `r3alice` and `r3erin`
(Gmail), "Team mailboxes" lists `r3team`; All mail shows the synced copies once each with "Gmail" or
"Outlook" badges. "Manage connected mailboxes" opens "Connected mailboxes" with `r3alice` (Syncing),
`r3erin` (Disconnected, with Reconnect and "Delete synced mail now"), `r3team` (Syncing); network:
`GET /api/external-mail-connections` `200` and three `…/sync-runs` `200`; no console errors. The
API payload carried `tenant_id: null`. The same dialog was opened again during part C while every row
carried a non-null `push` object: it rendered the same, no console error. The round-1/2 breakage
("Web schema rejected `tenant_id`") does not recur. As M1 (member)
`/console/admin/external-connections` shows the console 404 page.

### A11 — Web admin connection directory: Pass

Signed in as U at `/console/admin/external-connections`: 15 rows on the first page, masked
addresses (`r3***@external.invalid`), providers, member names (`r3m4`, `r3m1`, `r3m2`, "Member #5"),
statuses, and the shared row `r3team@external.invalid` "Team mailbox", member "Organization";
"Addresses are masked. Message contents and message counts are never shown here". Next →
`GET /api/external-mail-connection-directory?per_page=15&cursor=Mg` `200`, page 2 shows the remaining
row (`r3m1`, the oldest connection). No console errors.

### A12 — Sync runs: Pass

`GET /external-mail-connections/1/sync-runs` (M1) → runs as in A3, each with `trigger`; U → `404
MAILBOX.EXTERNAL_CONNECTION_NOT_FOUND`. The Web dialog shows "Last check …: up to date" and "Last
successful sync …" from them.

### A13 — IMAP credentials form: Pass

API with `EXTERNAL_MAIL_PROVIDERS=imap`. Web "Add external mailbox" → "IMAP" → app-password help →
"Connect with an app password" (provider presets, host, port 993, username, app password, optional
SMTP, 14/30/90 days). Submitting host `imap.r3-acceptance.invalid`, user
`r3imap@r3-acceptance.invalid`, a synthetic password → `POST /api/external-mail-credentials` `422`,
message "The mail server could not be reached over TLS on port 993…", password field cleared. The
password string appears neither in the API log nor in `audit_logs.metadata` (both `0` matches).

### A14 — No leak: Pass

U, A, and M2: `GET /inbox-messages` and `GET /mail-messages` contain no `R3SECRET` (0 matches);
`GET /mailboxes/1/messages` → `404 MAILBOX.NOT_FOUND`; sync runs and fake delivery of others'
connections → `404`.

## Part B — Tenant Connect

### B1 — Switch off: Pass

Default stack: connectors list `tenant: false`; owner `POST /external-mail-tenants` (gmail and
outlook bodies, and a malformed one) → `404 MAILBOX.EXTERNAL_PROVIDER_UNAVAILABLE`;
`GET /external-mail-tenants/1/directory` and `POST …/assignments` → `404
MAILBOX.EXTERNAL_CONNECTION_NOT_FOUND`; `GET /external-mail-tenants` → `200 []` (U, A), `403` (M1).

### B2 — Refusals with the switch on: Pass

`EXTERNAL_MAIL_TENANTS_ENABLED=true`: connectors list `tenant: true`. A and M1 `POST
/external-mail-tenants` → `403 PERMISSION.DENIED`; owner `{"provider":"outlook","client_id":"c"}`
(no secret) → `422 MAILBOX.EXTERNAL_AUTHORIZATION_INVALID`; A and M1 `DELETE
/external-mail-tenants/1` → `403`; M1 list → `403`.

### B3 — Owner authorizes a fake tenant: Fail (D1)

Owner `{"provider":"gmail","service_account_key":{"type":"service_account"},"admin_subject":"admin@acme-r3.invalid"}`
→ `422 MAILBOX.EXTERNAL_AUTHORIZATION_FAILED`; Outlook form with a GUID tenant id → the same. Run
twice with the switch on (16:08 and 16:24, after a fresh API restart), each time with both bodies. Cause: `FakeExternalMailSource`
accepts only tenants added with `AddTenant`, which only tests call; see D1.

### B4–B7 — Tenant behaviour: Blocked live (D1); automated tests pass

Run on this commit against the own test database (`go test ./internal/modules/mailbox/ -count=1
-run 'Tenant|Push|ProviderRelease|OIDC|ExternalMailSync' -v`): 95 top-level tests, all `PASS`, none
skipped. The tenant ones and what they cover:

| Case | Tests |
|---|---|
| B4 directory paging, personal/shared assignment, privacy | `TestTenantDirectoryAndAssignments`, `TestTenantAssignmentAccess`, `TestTenantAssignmentLimits`, `TestTenantMalformedCursorKeepsTenant`, `TestTenantManagementNeverShowsMail`, `TestTenantSharedRestoreReplacesMembers`, `TestTenantRoutes` |
| B5 leave → awaiting handover, handover seals | `TestTenantPersonalAssignmentOnLeave`, `TestTenantDepartedHolderIsNotReassigned`, `TestTenantWaitingMailboxIsNotDeleted`, `TestTenantDisconnectedThenDepartedIsKept`, `TestTenantHandoverBoundaryHoldsForLaterAssignees`, `TestTenantHandedOverAddressIsNotSelfBound` |
| B6 own-tenure rule | `TestTenantTenureProperty` (randomized, 12 seeds), `TestTenantFloorSurvivesReassignment`, `TestTenantFloorDropsUndatedMail`, `TestTenantSelfBoundTenureCounts`, `TestTenantPersonalAssignmentIsNotRedirected`, `TestTenantHolderMatchesDirectoryID`, `TestTenantSelfBindMatchesDirectoryID`, `TestTenantReauthorizedTenantRestoresSameHolder` |
| B7 revoke stops everything; never sends | `TestTenantRevocation`, `TestTenantRevocationWaitsForAssignment`, `TestTenantCredentialLoss`, `TestTenantStaleCredentialNeverStopsReauthorizedTenant`, `TestTenantMailboxNeverSends` |
| Switch and ownership | `TestTenantSwitchedOff`, `TestTenantRoutesWhileSwitchedOff`, `TestTenantAuthorizationIsForOwners`, `TestTenantMicrosoftOwnApplicationOnly`, `TestTenantOrganizationLimit`, `TestTenantOtherOrganizationIDs` |

These use the fake tenant source and PostgreSQL; they are not an end-to-end run through a browser or
the running API.

### B8 — List and revoke while the switch is off: Pass (fixture)

Because no tenant can be authorized (D1), one row was inserted into `external_mail_tenants`
(organization 1, `gmail`, identifier `r3-fixture-customer`, `active`, a placeholder credential).
With the switch off: `GET /external-mail-tenants` (A) → `200` with the tenant (`assignments 0`, no
credential field); `GET …/1/directory` (U) → `404`; A `DELETE /external-mail-tenants/1` → `403`; U
→ `204`, again `204`. The row is `revoked`, credential erased (length 0), `revoked_at` set; the
audit log has `delete external_mail_tenants` with `reason tenant_revoked`, `connections_revoked 0`.

## Part C — Push

### C1 — Push off: Pass

`POST /v1/external-mail-notifications/gmail` and `/outlook` (with a body, and the
`validationToken` handshake) → `404 MAILBOX.EXTERNAL_PROVIDER_UNAVAILABLE`.

### How push was exercised locally

With `EXTERNAL_MAIL_PUSH_ENABLED=true` and only the Outlook notification URL set, the sync worker
subscribed every active fake connection after its next sync (`external_mail_subscriptions` rows
`fake-sub-1`…`12`, `status active`, 70-hour expiry). The `fake` source parses both routes as a Graph
notification collection (`subscriptionId`, `clientState`). The `clientState` is random and Zmail keeps
only its SHA-256, so the tester replaced `secret_hash` of the shared Outlook connection's
subscription (connection 2) with the SHA-256 of a known synthetic value and sent that value. This
fixture replaces only the secret the provider would echo; everything after it is the real route,
service, and worker.

### C2 — Notification to sync: Pass

16:11:33.192 notification → `202`; `last_notified_at` 16:11:33.209; sync run 61 of connection 2
started 16:11:33.682, `triggered_by push`, outcome `ok`. Delay 0.49 s, within the documented
idle wait plus coalescing window (about 12 s).

### C3 — Burst coalescing: Pass

20 authenticated notifications, one every 0.5 s from 16:13:00 (all `202`): exactly two push syncs,
16:13:02.05 and 16:13:12.09 (10.04 s apart, `EXTERNAL_MAIL_PUSH_COALESCE_WINDOW` 10 s), the second
being the one owed for notifications that arrived during the window.

### C4 — Junk and limits: Pass

- 125 notifications with a wrong `clientState` from one IP: 120 × `202`, then `429` with
  `Retry-After: 60`.
- Right after, from the same IP, a valid notification → `202`, and a push sync ran at 16:13:28.
  Junk does not block real notifications.
- 32 handshakes: 30 × `200`, then `429`. A minute later the handshake echoes the token as
  `text/plain; charset=utf-8` with `nosniff`.
- 70,000-byte body → `413`. Non-JSON body → `400 COMMON.INVALID_INPUT`.
- `zmail_external_mail_push_notifications_total{provider="outlook"}`: `accepted 22`,
  `ignored 121`, `rate_limited 9`, `validation 30`.
- `external_mail.push_refused` logged 3 times in all (once a minute per reason). The known
  `clientState` and the subscription id never appear in the API log (0 matches).

### C5 — Production refusals: Pass

`fastbuild doctor` (which runs `config.Load()` as startup does) with `EXTERNAL_MAIL_PROVIDERS=gmail,outlook`
and dummy OAuth clients (no provider contacted); production base made otherwise valid
(`DB_SSLMODE=require`, https origins, mailbox Cloudflare placeholders):

| Setting | Result |
|---|---|
| Production, push off | `0 fail` |
| Production, push on (Outlook), no proxies, no flag | fail: "EXTERNAL_MAIL_PUSH_ENABLED=true in production needs SERVER_TRUSTED_PROXIES … or EXTERNAL_MAIL_PUSH_DIRECT_EXPOSURE=true" |
| … with `EXTERNAL_MAIL_PUSH_DIRECT_EXPOSURE=true` | `0 fail` |
| … with `SERVER_TRUSTED_PROXIES=10.0.0.0/8` | `0 fail` |
| … with both | fail: "EXTERNAL_MAIL_PUSH_DIRECT_EXPOSURE=true contradicts SERVER_TRUSTED_PROXIES" |
| Development, push on (Outlook), no proxies | `0 fail` |

### C6 — Gmail push needs its service account: Pass

Topic and audience without `EXTERNAL_MAIL_GMAIL_PUSH_SERVICE_ACCOUNT` → fail "EXTERNAL_MAIL_GMAIL_PUSH_SERVICE_ACCOUNT
is required when EXTERNAL_MAIL_GMAIL_PUSH_TOPIC is set", in development and in production; with
`zmail-gmail-push@r3-proj.iam.gserviceaccount.com` → `0 fail` in both.

### C7 — Worker idle interval: Pass

`mail:external:sync` without `--poll`: push on → `external_mail.worker_started {"poll":"2s"}`; push
off → `{"poll":"30s"}`.

### C8 — `push` on the read model: Pass

Push on: U's list → connection 2 `push: {"status":"active","expires_at":"2026-11-25T14:11:13Z",
"last_notified_at":"2026-11-22T16:13:26Z","last_error_code":null}`. A forced disconnect of
connection 17 → `push: {"status":"stopped","expires_at":null,…}`, subscription row `stopped`,
`external_mail.push_unsubscribed {"connection_id":17}` logged after the commit. After restarting
with push off, every row has `push: null`. The Web rendered the list with `push` objects (A10).

### C9 — Not exercisable locally: Blocked

See "Not Verifiable Without Real Providers".

## Part D — Provider Releases

### D1 — Record and clear: Pass

Forced disconnect of connection 17 (push on): the API log shows `INSERT INTO
external_mail_provider_releases`, the lease update, `external_mail.push_unsubscribed`, then `DELETE
FROM external_mail_provider_releases WHERE id = $1`; no row remains for connection 17. Releases with
a sealed grant were seen for Gmail connections only (connection 18: `length(encrypted_grant) > 0`).

### D2 — Rebind with a pending release: Pass

Connection 6 (`r3erin`, M1) had release 4 pending (postponed three times, `attempts 3`). M1 rebinds
`r3erin` → `200`, the same connection 6; release 4 is deleted in the same step (release 1 of
connection 3 untouched). A fake delivery afterwards → `201`, run `ok` `fetched 1 stored 1`; the
mailbox holds its old and new copy. A hand-back that really failed at a provider cannot be produced
with the fake (its revoke always succeeds); `TestExternalMailProviderReleaseSparesARebind` and
`…RechecksBeforeTheHandBack` cover it and pass.

### D3 — Expiry by the mail worker: Pass

M2 starts a Gmail sign-in (left pending), binds and unbinds `r3frank` (18) → release 6 postponed
(`sign_in_pending`), sealed grant present. Its `created_at`/`expires_at` were moved 25 h / 1 h into
the past (the table's check requires `expires_at > created_at`). Within 3 s the mail worker logged
`external_mail.provider_release_expired {"releases":1}` (in `mail:broadcast:work`'s log) and the row
was gone.

### D4 — Postponement of a purged connection's release: Fail (D2)

See D2 below.

## Part E — Invitation Link (Issue 195)

### E1 — Shown once after create: Pass

API: `POST /organizations/1/invitations` (U) → `201` with `invitation_url`
`http://localhost:3065/invite#oinv_…` (token in the fragment) and `Cache-Control: private, no-store`;
five links were used to accept (A, M1–M4). Audit `invite` rows carry `link_issued: true`, no email,
token, or link. Web (U): "Invite member" → "Invitation created", "Share this link with
r3web1@accept3.test only. It is shown just this once." with "Copy invitation link".

### E2 — Resend with a mailer: Pass

`POST /organizations/1/invitations/5/emails` → `201` with a new `invitation_url`; accepting with the
old token → `404 ORGANIZATION.INVITATION.INVALID`, with the new one → `200`. Audit `resend` has
`token_rotated: true`, `link_issued: true`. Web: Resend → "New invitation link … The previous link no
longer works. It is shown just this once." with a copy action.

### E3 — Without a mailer: Pass

`MAIL_PROVIDER=` `MAIL_FROM=`: create → `201`, `email_send_status not_configured`, with an
`invitation_url` (a token was minted); resend a minute later → `201`, `not_configured`, **no**
`invitation_url`, `expires_at` unchanged (token kept), as the contract says.

### E4 — Never in the list: Pass

`GET /organizations/1/invitations` (U): no `invitation_url`, `oinv_`, or `token` (0 matches). After
closing the Web confirmation: `document` HTML, `localStorage`, and `sessionStorage` contain no
`oinv_`, the URL has no token.

### E5 — Members: Pass

M1 → list, create, resend → `403 PERMISSION.DENIED`.

### E6 — Copy fallback: Pass

With `navigator.clipboard.writeText` made to reject: "Copying failed. Select the link and copy it
manually." and a selected read-only field holding the link.

### E7 — zh-Hans: Pass

Language switched to Simplified Chinese in the header menu, then an invitation for
`r3web2@accept3.test` created: the confirmation title ("Invitation created"), the "share with this
address only, shown just this once" line naming the address, the copy button, and, with the
clipboard refusing, the copy-failure message were all shown in Simplified Chinese, next to the
selected link (checked on a screenshot; the strings are not quoted here because this report must
stay English-only). The language was set back to English afterwards.

## Part F — Upgrade Path

On `zmail_accept3_migr`:

1. The round-2 commit's API (`git archive d9d1d1a api`, built in the scratchpad) ran `migrate`
   (63 migrations, last `2026_12_06_090000_add_external_mail_sync_ops`) and `seed`; its API on port
   8067 created an organization and bound two fake Gmail accounts, then unbound one (connection 1
   `active`, 2 `revoked`).
2. **Up (F1):** the tested binary's `migrate` → 12 migrations, including
   `2026_12_14_090000_add_external_mail_tenants`, `2026_12_15_090000_add_external_mail_push`, and
   `2026_12_16_090000_add_external_mail_provider_releases` (and `2026_12_01_090000_create_user_totp`,
   which carries an older timestamp and is applied in the same batch). Total 75. New tables
   `external_mail_address_records`, `external_mail_tenants`, `external_mail_subscriptions`,
   `external_mail_provider_releases`. Both existing connections got an address record (`gmail`,
   holder user 2), the backfill described for N7.
3. **Down (F2):** `migrate:rollback` → the 12 rolled back in reverse order, "Rollback completed (12
   migrations)"; 63 remain, the four new tables are gone, and the connections are intact (`1 active`,
   `2 revoked`). **Up again** → 12 migrated, 2 address records.

### F3 — `UPGRADING.md`: Pass

It documents `invitation_url` (deploy the Web first), the push production requirement
(`SERVER_TRUSTED_PROXIES` or `EXTERNAL_MAIL_PUSH_DIRECT_EXPOSURE`, not both), the three new
migrations, and that `EXTERNAL_MAIL_TENANTS_ENABLED` and `EXTERNAL_MAIL_PUSH_ENABLED` default to off.

## Automated Checks Run

- Go, own database `zmail_accept3_gotest`:
  `go test ./internal/modules/mailbox/ -run 'Tenant|Push|ProviderRelease|OIDC|ExternalMailSync'`
  → 95 top-level tests pass, 0 skipped (32.6 s).
- Then the whole packages: `go test ./internal/modules/mailbox/... ./internal/infra/config/...
  ./database/... -count=1` → `ok` for mailbox (160.8 s), config, and `database/migrations` (golden
  schema and rollback tests, 37.1 s).
- `go test ./internal/modules/organization/... -run Invitation` → ok.
- Web Vitest: `organization-route`, `organization-ui`, `organization-invitation-link-cache`,
  `organization-contract`, `external-mail-service`, `external-mail-schemas` → 6 files, 83 tests
  passed.
- `make check` was not run (no release boundary; the brief asked for `make governance`, which passed
  on this report, see the PR).

## Defects

### D1 — P3 — The fake connector cannot authorize a tenant in a running stack

Tested commit `4cef3f3`. Steps (reproduced twice, Gmail and Outlook forms):
1. Run the API with `EXTERNAL_MAIL_PROVIDERS=fake` and `EXTERNAL_MAIL_TENANTS_ENABLED=true`.
2. `GET /v1/external-mail-connectors` → both connectors `capabilities.tenant: true`.
3. As the owner, `POST /v1/external-mail-tenants` with
   `{"provider":"gmail","service_account_key":{"type":"service_account"},"admin_subject":"admin@acme-r3.invalid"}`,
   or `{"provider":"outlook","tenant_id":"00000000-0000-4000-8000-000000000003","client_id":"c","client_secret":"s"}`.

Expected: a development path to a working fake tenant, like the fake sign-in (`<code>` binds
`<code>@external.invalid`) and the fake delivery route, so the tenant flow (directory, assignment,
leave, handover, revoke) can be accepted end to end locally.

Actual: `422 MAILBOX.EXTERNAL_AUTHORIZATION_FAILED` every time. `FakeExternalMailSource.AuthorizeTenant`
accepts only identifiers registered with `AddTenant`, and `AddTenant` has no caller outside tests;
`api/docs/EXTERNAL_MAIL_DEVELOPMENT.md` says nothing about tenants. Development tooling only; no
production impact. Suggestion: register a fixed fake tenant (for example any `admin_subject` at
`*.invalid` with a small generated directory) while `fake` is active, and document it.

### D2 — P3 — A purged connection's provider release is postponed by any member's sign-in

Tested commit `4cef3f3`. Steps:
1. M1 has a Gmail sign-in pending (started and abandoned), binds `r3bob`, unbinds it → release 1 is
   postponed (`sign_in_pending`), correctly, because M1 has a sign-in pending.
2. M1 purges the connection (`POST /external-mail-connections/3/purges` → `200`); the connection row
   is deleted, the release stays (as documented).
3. M1's pending sign-in expires (16:12:59). M2 (another member) starts a Gmail sign-in (16:14:20).
4. The sync worker's next attempt (16:17:42): `external_mail.provider_release_postponed
   {"connection_id":3,"provider":"gmail","reason":"sign_in_pending"}`; `external_mail_authorizations`
   then held only M2's row.
5. Again: M4 starts a Gmail sign-in at 16:21:59 (pending until 16:31:59); the worker's attempt at
   16:31:37 logs the same `provider_release_postponed` for connection 3, and release 1 moves to
   `attempts 5`, next attempt 16:57:39. M1 had no sign-in pending at either time.

Expected (`contracts/MAILBOXES.md`, Push Subscriptions, Stopping): "A release is also postponed, not
dropped, while the member (or, for a shared connection, anyone in the organization) has a sign-in
pending for the provider." Connection 3 was a personal connection of M1.

Actual: once the connection is purged, `releaseBlocked` (`api/internal/modules/mailbox/external_mail_push.go`)
no longer knows the owner and counts every member's pending sign-ins ("any member's when the
connection has none or is gone"). In an organization where members often connect Gmail, the
hand-back of a purged connection can keep slipping (backoff up to an hour) until the 24-hour expiry,
after which Zmail never revokes the Google grant (the contract then asks the member to remove the
access by hand). Either keep the owner on the release record so the check stays per member, or state
the purged-connection rule in the contract. No data or privacy exposure: nothing is read with the
grant meanwhile.

## Observations (not defects)

- With the `fake` source, the Gmail notification route also parses a Graph-shaped body
  (`{"value":[]}` → `204` without any token) while Gmail push settings are unset; with real
  connectors the route needs the OIDC token and answers `404` without the Gmail settings. Fake-only.
- The fake source subscribes Gmail connections for push even when only the Outlook push URL is set
  (all 12 subscriptions were created). Fake-only.
- `zmail_external_mail_push_delay_seconds` is observed in the `mail:external:sync` process, which
  serves no `/metrics`; the API's `/metrics` showed only the notification counter.
- The secret masking in the Web snippet also masked the word `R3AGENT` in the subject "R3AGENT after
  grant code 482913" (shown as "•••••• after grant code ••••••"); the same word was kept in
  "R3AGENT post-grant personal code ••••••". A heuristic false positive on a synthetic word, cosmetic.
- With `MAIL_PROVIDER=log` the invitation email, including the token, is written to the API log by
  design of the development log mailer; production refuses that mailer.
- The tenant revoke also leaves a generic `destroy external_mail_tenants` audit row next to the
  specific `delete … reason tenant_revoked` row.

## Not Verifiable Without Real Providers

- Tenant connect end to end (B3–B7) on a running stack: blocked by D1 locally; against real
  providers it needs a Workspace test tenant with a service account and domain-wide delegation, and a
  Microsoft 365 tenant with an application holding `Mail.Read` and `User.Read.All`.
- Gmail push: Pub/Sub delivery, OIDC verification against Google's keys, `users.watch` and
  `users.stop` scope, primary address versus alias, Pub/Sub retry with the `503` answer.
- Outlook push: the real Graph validation handshake (source addresses versus the 30-per-minute
  handshake budget, the 10-second answer), `created` for mail moved into the Inbox, lifecycle events,
  the 3-second notification answer.
- End-to-end latency (target P95 under 30 s) and the delay histogram.
- Whether revoking one Google token revokes the user's whole grant, and a hand-back that a provider
  really refuses.
- Real Gmail, Outlook, and IMAP accounts in general (not allowed in this run).

## Side Effects And Cleanup

- Databases `zmail_accept3`, `zmail_accept3_gotest`, `zmail_accept3_migr` were created for this run
  and dropped at the end; `fastbuild` and `zmail_test` were not written to.
- Processes started (API on 8065, round-2 API on 8067, `mail:broadcast:work`, `mail:external:sync`,
  `next dev` on 3065) were stopped. The browser pane was signed out and its language set back to
  English.
- `pnpm install --frozen-lockfile` populated `node_modules` in this worktree only.
