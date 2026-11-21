# Connector Acceptance Report: `main`, Round 1

- **Tested commit:** `51526e165d439357c4df78a189e317f03aac51d4` (`origin/main`, "Merge pull request #64")
- **Date:** 2026-11-21
- **Tester:** independent acceptance agent (did not write the code; no product code changed)
- **Scope:** mail connectors on `main`: connector layer, Gmail / Outlook / IMAP adapters, binding and
  reconnect, sync operations (sync runs, backoff, retention purge), and the member-facing Web connect
  flow. Contract: `contracts/MAILBOXES.md` "External Mailboxes"; requirements:
  `docs/PRODUCT_REQUIREMENTS.zh-CN.md` R2 and `docs/plans/mail-connectors-roadmap.zh-CN.md` §4, §5.1, §7.

## Summary

| # | Case | Result |
|---|---|---|
| 1 | Connector discovery (on with `fake`, empty and Web entry hidden when off) | Pass |
| 2 | Bind end to end; replayed state refused; no state or code left in the URL | Pass |
| 3 | Sync: copy appears once with a source badge; a second sync adds no copy | Blocked on the live stack (covered only by automated tests) |
| 4 | Privacy against a second member and owner/admin | Pass (manager-visible metadata recorded; see D2, D3) |
| 5 | Read-only: no send / reply / forward from a connected mailbox | Pass |
| 6 | Reconnect restores the same connection; other account refused | Pass |
| 7 | Disconnect stops sync; rebind restores the mailbox; limit of 10 counts live only | **Fail** (D1) |
| 8 | Removing the member revokes their connections at once | Pass |
| 9 | Sync runs owner-only; purge owner-only on revoked; retention config validation | Pass |
| 10 | IMAP binding safety (private hosts, port, rate limit, password never echoed) | Pass (minor contract deviation D4) |
| 11 | Web callback error handling for each code and `error=access_denied` | Pass |
| 12 | Layout and keyboard smoke test at 375 px and 1280 px | **Fail** (minor, D9) |
| — | Reference gate `make check` | See [Automated Gate](#automated-gate) |

**Totals: 9 pass, 2 fail, 1 blocked.**

Defects by severity:

| Id | Severity | One line |
|---|---|---|
| D1 | major | Rebinding a revoked connection skips the 10-connection limit; the 11th live connection is hidden from `GET /v1/external-mail-connections` |
| D2 | major | External mailboxes count toward `MAILBOX_MAX_PER_ORGANIZATION` and are listed (address, owner) in the owner/admin `GET /v1/mailbox-directory` |
| D7 | major | Nothing runs `mail:external:sync` in `make dev` or in the shipped deploy bundles, so bound accounts never sync after the first bind |
| D3 | minor | Owners/admins get `see_history: true` on another member's external mailbox and can read its access history; the contract grants them `administer` only |
| D4 | minor | IMAP `backfill_days` out of range answers `COMMON.VALIDATION_FAILED`, not the documented `MAILBOX.EXTERNAL_AUTHORIZATION_INVALID` |
| D5 | minor | Disconnect confirmation hard-codes "kept for 30 days" whatever `EXTERNAL_MAIL_REVOKED_RETENTION` is |
| D6 | minor | Mock BFF copies carry no `source`, so the mock inbox shows no source badge |
| D8 | minor | `UPGRADING.md` says both `mail:external:sync` and `mail:broadcast:work` delete revoked copies |
| D9 | minor | Closing the connect dialog with Escape leaves focus on `<body>` instead of the trigger |
| D10 | minor | The callback page keeps the heading "Connecting your mailbox" on every error state |

## Environment And Method

- macOS, Node 24 (`/opt/homebrew/opt/node@24/bin` first on PATH), Go toolchain from the host, dev
  PostgreSQL container on `127.0.0.1:55432`.
- **Stack A (real API, fake connector):** `make dev` with `FASTBUILD_API_PORT=8035
  FASTBUILD_WEB_PORT=3010 FASTBUILD_ADMIN_PORT=4183 EXTERNAL_MAIL_PROVIDERS=fake
  MAILBOX_SECRET_ENCRYPTION_KEY=<local 40-char string>
  EXTERNAL_MAIL_REDIRECT_URL=http://localhost:3010/console/mailboxes/connected/callback`.
  `scripts/dev.sh` runs the Web app with `API_ADAPTER_ENABLED=true` and `MOCK_BFF_ENABLED=false`, so
  this stack is the real Go API behind the Web adapter.
- **Stack B (second API on 8036, same database):** the built binary `serve` with
  `EXTERNAL_MAIL_PROVIDERS=imap` (case 10), then restarted with the variable empty (feature off,
  cases 1 and 9), then once with `MAILBOX_MAX_PER_ORGANIZATION=12` (D2). It also ran with
  `AUTH_REGISTRATION_EMAIL_CODE_REQUIRED=false` so three extra local test members could register.
- **Stack C (Web mock mode):** `next dev` on 3010 with the adapter off and `MOCK_BFF_ENABLED` unset
  (the documented "local scaffold development" mode, `web/docs/MOCK_BFF.md`), signed in with the
  mock fixture account from `web/src/features/auth/server/mock-identity.ts`. Two `next dev` servers
  cannot share `web/` ("Another next dev server is already running"), so stack C ran after stack A
  was stopped.
- API cases used curl scripts kept in the session scratchpad: `POST /v1/login`, then
  `Authorization: Bearer` plus `Organization-Id`. Web cases used the in-app browser pane.
- **Accounts.** Organization 2 ("Accept Connectors 20907") owned by `user@example.com` (U, id 2), with
  `admin@example.com` as `admin` (A, id 1) and three local test members registered on stack B:
  M1 (id 3), M2 (id 4), M3 (id 5). Invitation tokens are never returned by the API and no mailer is
  configured, so for each invitation the tester set `token_hash` in the dev database to the SHA-256
  of a known local token and then accepted it through `POST /v1/organization-invitations/accept`.
- **The fake connector's sign-in page** is `https://external-mail.invalid/authorize?...`, which a
  browser cannot open. The Web flow was driven up to that redirect, then the "provider redirect" was
  simulated by opening the callback URL with the real `state` (read from the session-storage entry
  the connect dialog writes) and a chosen `code`. The fake grants `<code>@external.invalid`.
- **Synced mail.** The fake source keeps mail in the API process's memory and is filled only through
  `FakeExternalMailSource.Deliver`, which no HTTP route or CLI reaches, so a running stack cannot be
  fed (case 3). To check privacy, read-only behaviour, the source badge, and purge counts on real
  rows, the tester inserted labelled fixture rows into `mail_messages` (subjects starting with
  `ACCEPT`, `source = 'gmail'`, an `external_id`) for mailboxes 6, 15, and 17. These rows stand in
  for synced copies; they do not test the sync itself.
- The sync worker was run on demand as `fastbuild mail:external:sync --once` with the same
  environment as stack A.

## Case Details

### 1. Connector discovery — Pass

| Step | Expected | Actual |
|---|---|---|
| `GET /v1/external-mail-connectors` on stack A (`fake`) | Non-empty, stable order | `200` `[gmail oauth2 read, outlook oauth2 read]`, `Cache-Control: private, no-store` |
| Same on stack B with `imap` | IMAP only | `200` `[{"id":"imap","auth_kind":"password",...}]`; `POST /v1/external-mail-authorizations {"provider":"gmail"}` → `404 MAILBOX.EXTERNAL_PROVIDER_UNAVAILABLE` |
| Same on stack B with the variable empty | Empty list; start `404 EXTERNAL_PROVIDER_UNAVAILABLE`; connection routes `404 EXTERNAL_CONNECTION_NOT_FOUND` | `200 []`; start `404 MAILBOX.EXTERNAL_PROVIDER_UNAVAILABLE`; list, delete, sync-runs `404 MAILBOX.EXTERNAL_CONNECTION_NOT_FOUND`; credentials `404 EXTERNAL_PROVIDER_UNAVAILABLE`; completion `404 EXTERNAL_AUTHORIZATION_NOT_FOUND` |
| Web inbox against the feature-off API | No "Add external mailbox" entry | Sidebar shows "Connected by me" with the existing mailbox only; no "Add external mailbox" and no manage icon. Already-synced copies stay readable by their owner |

### 2. Bind end to end — Pass

API (stack A, M1): `POST /v1/external-mail-authorizations {"provider":"gmail","backfill_days":7}` →
`201` with `authorization_url` and `expires_at` (+10 min), `no-store`. Completion with code `alice` →
`201` connection 3, `account_address` `alice@external.invalid`, `mailbox_id` 6, `status` `active`.
Mailbox 6 is `type: "external"`, `owner_user_id: 3`, M1 access `view` + `see_history` only.
Replaying the same `state` → `404 MAILBOX.EXTERNAL_AUTHORIZATION_NOT_FOUND`. M2 completing M1's
`state` → `404 EXTERNAL_AUTHORIZATION_NOT_FOUND`, and the state is then used up for M1 too, as the
contract says.

Web, real API (U): Inbox → "Add external mailbox" → Gmail → consent step (read-only, private, removed
on leaving; 14/30/90 days) → 30 days chosen with the keyboard → "Continue to Gmail" → browser left
for `external-mail.invalid`. Opening
`/console/mailboxes/connected/callback?state=<state>&code=uweb` completed the binding: the page
replaced the address with the bare path before anything else, then went to
`/console/mailboxes?org=2&box=17`; the session-storage entry was removed; "Connected by me" listed
`uweb@external.invalid` with a green dot. Opening the same callback URL again showed "This sign-in
expired or was already used" and the address bar was cleaned again.

Web, mock mode (stack C): Outlook → Continue → the mock "provider page" is the app's own callback →
toast "Connected. Syncing the last 14 days of mail." → inbox on `admin@outlook.com` with three mock
copies; URL `/console/mailboxes?org=1&box=1`, no `state` or `code`. Replaying that `state` (taken from
the dev server log) with a re-seeded session entry → "This sign-in expired or was already used".

Observation (not a defect): the `next dev` request log prints the callback URL with `state` and
`code`. The browser has to send them once; the server log is development output only.

### 3. Sync — Blocked on the live stack; covered by automated tests

- Mail for the fake connector can only be added through `FakeExternalMailSource.Deliver` inside the
  API process (no route or command), and `mail:external:sync` builds its own empty fake source in its
  own process. So a running stack cannot receive new external mail; "appears once" and "a second sync
  adds no copy" could not be observed end to end.
- What was observed live: the worker run once claimed the due connections and recorded one `ok` run
  per connection (`fetched_count 0`, `stored_count 0`), set `last_synced_at`, and skipped revoked
  ones. With a fixture row standing in for a synced copy, the unified inbox shows the row once with a
  "Gmail" source badge and the mailbox address, and the reading pane says "Read-only copy from
  Gmail. Reply from Gmail itself."
- `make dev` does not start the sync worker at all (D7), so in the documented dev stack a bound
  account shows "Not synced yet" indefinitely.
- Automated coverage (all passed on this commit with PostgreSQL):
  `TestExternalMailSyncIsIdempotent`, `TestExternalMailReconnectRestoresWithoutDuplicates`,
  `TestExternalMailRebindAfterRevokeReusesMailbox`, `TestExternalMailSyncAdvancesCursor`,
  `TestExternalMailBackfillProceedsPageByPage`, `TestExternalMailCursorExpiryResyncs`,
  `TestSyncedMailIsQueuedForTriage`, `TestInboundDeliveryNeverReachesExternalMailbox`
  (`api/internal/modules/mailbox/external_mail_test.go`, `external_mail_binding_test.go`); Web badge in
  `web/src/test/inbox-ui.test.tsx`.

### 4. Privacy — Pass

Mailbox 6 (M1's) with one fixture copy, message 9.

| Surface | M2 (member) | U (owner) and A (admin) |
|---|---|---|
| `GET /v1/mailboxes` | not listed | listed (see below) |
| `GET /mailboxes/6/messages`, `/messages/9`, `/threads/9`, attachment | `404 MAILBOX.NOT_FOUND` | `403 PERMISSION.DENIED` |
| `GET /mailboxes/6/message-counts` | `404` | `403` |
| `PUT /mailboxes/6/triage-setting {"enabled":true}` | `404` | `403 PERMISSION.DENIED` |
| `GET /inbox-messages` (all, `mailbox_id=6`, `q=ACCEPT`) | empty | empty |
| `GET /inbox-message-counts` | mailbox 6 absent, all zero | mailbox 6 absent, all zero |
| `GET /mail-messages`, `/mail-message-stats` | fixture absent, received 0 | fixture absent, received 0 (M1 sees received 1) |
| `GET /external-mail-connections/3/sync-runs` | `404 EXTERNAL_CONNECTION_NOT_FOUND` | `404 EXTERNAL_CONNECTION_NOT_FOUND` |
| Web inbox (U) | — | M1's mailboxes appear in no sidebar group; "No mailbox to read yet" |

M1 (owner of the binding) can read it; switching triage on answered `409 MAILBOX.TRIAGE_UNAVAILABLE`
because no AI model is configured, so the positive case could not be shown (covered by
`TestSyncedMailIsQueuedForTriage`).

**What a manager can still see on `main`:**

- `GET /v1/mailboxes` row: `address` (`alice@external.invalid`), `display_name`, `type: "external"`,
  `owner_user_id`, `created_at`, `unread_count: 0` (the manager's own count, always 0), and
  `access: { administer: true, see_history: true }` (D3).
- `GET /v1/mailbox-directory` (no `type`): the same address, owner, and `member_count 0`, and the
  mailbox counts toward `meta.mailbox_count` (D2).
- `GET /v1/mailboxes/6/access-events`: `200` (empty list, because binding writes no event) — D3.
- An owner/admin may rename it (`PUT /v1/mailboxes/6` → `200`, display name changed) and may revoke
  the connection (`DELETE /v1/external-mail-connections/12` by U → `204`), as the contract allows.
- Nothing else: no subject, sender, body, count, or sync run.

### 5. Read-only — Pass

As M1 on mailbox 6: send `POST /mailboxes/6/messages` → `403`; reply (same route with
`in_reply_to_message_id`) → `403`; move to trash / archive → `403`; delete → `403`; create folder →
`403`; add member → `409 MAILBOX.ACCESS_CONFLICT`. Read/unread and star → `200` (allowed by the
contract). API key with `mail:send`, `POST /v1/outbound-mail-messages {"from":"alice@external.invalid"}`
→ `404 MAILBOX.NOT_FOUND` (key revoked afterwards). Owner U: handover → `409 ACCESS_CONFLICT`.
Web: the reading pane offers only "Mark unread" and star; `r`, `f`, `e`, `#` do nothing; `c` says
"You don't have a mailbox you can send from."

Cosmetic: the `409` message for members on an external mailbox reads "a personal mailbox has no
members".

### 6. Reconnect — Pass

M1 `{"connection_id":3}` + code `alice` → `200`, same id 3, mailbox 6, `active`. With code `bob` →
`409 MAILBOX.EXTERNAL_ACCOUNT_MISMATCH`, nothing changed. M2 `{"connection_id":3}` → `404
EXTERNAL_CONNECTION_NOT_FOUND`. M2 binding `alice` → `409 EXTERNAL_ACCOUNT_TAKEN`. Code `refuse` →
`422 EXTERNAL_AUTHORIZATION_FAILED`. Web: after the worker marked connection 14 `needs_reconnect`, the
inbox showed "Your Gmail account needs reconnecting" with Reconnect and an amber dot; Reconnect →
provider → callback with the right code → toast "Reconnected. Mail is syncing again.", green dot.

### 7. Disconnect and rebind — Fail (D1)

- Disconnect: M2 deleting M1's connection → `404`; M1 → `204`, again → `204`; status `revoked`. A
  worker pass after that synced every other active connection and did not touch the revoked one (no
  sync run). Pass.
- Rebind: `alice` again → `200`, same connection 3 and mailbox 6, fixture copy still there, no
  duplicate. Pass.
- Limit: with 10 live connections the 11th new account → `409 MAILBOX.EXTERNAL_CONNECTION_LIMIT`;
  after revoking one, a new account → `201`. Pass.
- **But** with 10 live connections, rebinding the revoked one → `200`, giving 11 live connections
  (D1).
- Web: Connected mailboxes → Disconnect → inline confirmation → status "Disconnected" with
  Reconnect. The confirmation text hard-codes 30 days (D5).

### 8. Leaving the organization — Pass

M1 had 9 live connections. `DELETE /v1/organizations/2/members/5` by U → `204`; immediately all 9
rows were `revoked` with `revoked_at` set. M1 → `404 ORGANIZATION.NOT_FOUND`; U on mailbox 6 → `403`.
After M1 rejoined, binding `alice` → `200`, connection 3 and mailbox 6 restored with the copy, `read`
reset to false (read records are dropped on leaving).

### 9. Sync operations — Pass

- `GET .../connections/5/sync-runs`: M1 → `200` (`no-store`) with the run; U, A, M2 → `404
  EXTERNAL_CONNECTION_NOT_FOUND`.
- `POST .../connections/5/purges` while active → `409 MAILBOX.EXTERNAL_CONNECTION_NOT_REVOKED`.
  Revoked connection 4: U and A → `404`; M1 → `200 {"deleted_messages":0,"completed":true}`; again →
  `404`. Connection 12 revoked by the owner U, with one copy: M1 purge → `200
  {"deleted_messages":1,"completed":true}`, mailbox 15 gone (`404 MAILBOX.NOT_FOUND`).
- With `EXTERNAL_MAIL_PROVIDERS` empty (stack B) M1's purge of revoked connection 5 → `200`.
- Built binary: `fastbuild doctor` with `EXTERNAL_MAIL_REVOKED_RETENTION=bogus` → fails, "must be a
  positive number, or a duration such as 90s, 720h, or 30d"; `0d` → fails; `23h` → fails "must be
  between 24h0m0s and 8760h0m0s"; `90d` and `720h` → `config.Load() succeeds`. `serve` and
  `mail:broadcast:work --once` with `30days` exit 1 with the same message.

### 10. IMAP binding safety — Pass (D4)

Stack B with `EXTERNAL_MAIL_PROVIDERS=imap`; M2 then M3; password a unique marker string.

| Attempt | Result |
|---|---|
| hosts `127.0.0.1`, `localhost`, `10.1.2.3`, `192.168.1.10`, `100.64.0.1`, `198.18.0.1`, `169.254.169.254`, `::1`, `::ffff:127.0.0.1`, `64:ff9b::a00:1` | each `422 MAILBOX.EXTERNAL_SERVER_UNREACHABLE` |
| 11th attempt by M2 within 15 minutes | `429 COMMON.RATE_LIMITED`, `Retry-After: 900` (and every attempt after) |
| ports `143`, `465` (M3) | `422 EXTERNAL_SERVER_UNREACHABLE`; the code refuses any port other than 993 before dialling (`external_mail_imap.go:371`) |
| host with a control character | `422 MAILBOX.EXTERNAL_AUTHORIZATION_INVALID` |
| `backfill_days: 91` | `422 COMMON.VALIDATION_FAILED` (D4) |
| `provider: "gmail"` | `404 MAILBOX.EXTERNAL_PROVIDER_UNAVAILABLE` |
| Password in any response body, the API log, or `audit_logs.metadata` | never found |

No real IMAP server was contacted; every host was private, loopback, reserved, or `.invalid`.

### 11. Web callback errors — Pass

Browser (real API): `notFound` (unknown or replayed state) "This sign-in expired or was already
used…"; `mismatch` "You signed in to a different account than the one you are reconnecting…";
`taken` "Another member of your organization already connected this account."; `failed` (code
`refuse`) "The provider did not accept the sign-in. Try again."; `error=access_denied` "The sign-in
was cancelled, so nothing was connected."; `error=server_error` → the `failed` text; `state` without
`code` "This page is missing the details the provider sends back…". The address bar was cleaned
on every path. "Try another account" appears when the sign-in is known.
`MAILBOX.EXTERNAL_CONNECTION_LIMIT` and the dialog's `EXTERNAL_PROVIDER_UNAVAILABLE` were not
reproducible from the browser and are covered by `web/src/test/external-mail-ui.test.tsx`
(`it.each` over the five completion codes; "goes back to the providers and reports a failed start").
The three external-mail Vitest files passed: 36 tests.

### 12. Layout and keyboard — Fail (minor, D9)

- 375 px: provider list and consent step fit the width (`scrollWidth` 375), buttons full width, no
  horizontal scroll; callback error state fits, alert has `role="alert"`, one `h1`.
- 1280 px: dialog centred at `sm:max-w-md`; callback page centred.
- Keyboard: opening the dialog focuses the first provider; Tab/Enter choose a provider; arrow keys
  change the backfill radio with a visible focus ring; Tab reaches Back and Continue; Enter starts
  the sign-in; Escape closes the dialog. After Escape, focus is on `<body>`, not on the "Add external
  mailbox" button (D9).
- Buttons are 36 px tall (below the 44 px touch-target guideline; not filed).

## Defects

### D1 — major — Rebinding a revoked connection bypasses the limit of 10 and hides a live connection

Steps (stack A, fake connector, one member):
1. Bind 10 different accounts (`POST /v1/external-mail-authorizations {"provider":"gmail"}`, then
   completions with codes `a1`…`a10`).
2. `DELETE /v1/external-mail-connections/<a1 id>` → `204`.
3. Bind a new account `a11` → `201` (10 live).
4. Bind `a1` again → **`200`**, connection active.
5. `GET /v1/external-mail-connections`.

Expected: the member never has more than 10 live connections (contract: "Revoked connections … do
not count toward the limit of 10"; roadmap §4.2.3, ten personal connections per member), so step 4 is `409
MAILBOX.EXTERNAL_CONNECTION_LIMIT`; and the list ("at most 10") shows every live connection.

Actual: 11 live connections (`select count(*) … where status <> 'revoked'` → 11). The list returns
10, so one live, syncing connection (id 13 in this run) is not shown anywhere in the UI and cannot be
disconnected from it. The contract's outcome table lists the caller's own connection "in any state"
as `200` and the limit only for "a new account", so either the contract needs a row for restoring a
revoked connection at the limit or the code needs the check.

### D2 — major — External mailboxes count toward the organization mailbox limit and appear in the directory

Steps:
1. Start the API with `MAILBOX_MAX_PER_ORGANIZATION=12`; the organization has 9 live external
   mailboxes bound by M1 and no other mailbox.
2. As the owner, create personal mailboxes with `POST /v1/mailboxes` → 3 × `201`, then the 4th →
   **`409 MAILBOX.LIMIT_REACHED`**, with only 3 Zmail mailboxes.
3. `GET /v1/mailbox-directory` (no `type`) as owner/admin.

Expected: `contracts/MAILBOXES.md` Access: "the directory and export accept `personal` and `shared`
only"; Directory: "`type` (`shared` or `personal`) narrows the list; absent lists both". Roadmap
§4.2.3: connected mailboxes do not take any of the organization's 50 mailbox places.

Actual: the directory lists the external mailboxes with `address`, `owner_user_id`, `type:
"external"`; `meta.mailbox_count` counts them; and `CountMailboxes` / `CreateMailbox`
(`api/internal/modules/mailbox/directory_repository.go:81`, `repository.go:59`) count every mailbox
row, so members' private bindings use up the organization's mailbox limit and can block owners from
creating mailboxes (also mailbox import). The three probe mailboxes were deleted afterwards.

### D7 — major — No process runs `mail:external:sync` in the dev stack or the deploy bundles

Steps: `make dev` with `EXTERNAL_MAIL_PROVIDERS=fake …`; bind an account; wait.

Expected: "`active`: Synced every two minutes" (contract, Connections).

Actual: `scripts/dev.sh` starts `serve`, `webhook:work`, and `mail:broadcast:work` only; the
connection keeps `last_synced_at: null` and the Web says "Not synced yet" until someone runs
`fastbuild mail:external:sync` by hand. `deploy/self-host/compose.yaml` and `deploy/kubernetes`
define no `mail:external:sync` service and pass no `EXTERNAL_MAIL_*` variables, so an operator who
enables a connector on the shipped bundles gets bindings that never sync after the first fetch
(there is none). Also: with `fake`, a separate worker process has its own empty fake source, so the
dev demo can never show synced mail even when the worker runs.

### D3 — minor — Owners and admins get `see_history` on members' external mailboxes

Steps: as owner or admin, `GET /v1/mailboxes` and `GET /v1/mailboxes/<external id>/access-events`.

Expected: "External … Only that member, the owner, reads them, with `view` and `see_history` …
Owners and admins get `administer` only."

Actual: `access.see_history: true` for owner and admin, and access-events → `200`. The list is empty
today because binding writes no event, so nothing leaks yet, but any future event would be visible.

### D4 — minor — IMAP `backfill_days` out of range uses the wrong error code

Steps: `POST /v1/external-mail-credentials` with `backfill_days: 91` (IMAP enabled).

Expected: `422 MAILBOX.EXTERNAL_AUTHORIZATION_INVALID` (contract table "`backfill_days` is out of
range").

Actual: `422 COMMON.VALIDATION_FAILED` with `errors.backfill_days ["must be at most 90"]`
(binding tag in `external_mail_credentials_handler.go:26`). Related observation: on
`POST /v1/external-mail-authorizations`, `backfill_days: 0` is accepted and treated as the default 14
while the contract says 1 to 90.

### D5 — minor — Disconnect confirmation hard-codes the retention

Steps: Inbox → Connected by me → manage → Disconnect.

Expected: the retention the instance uses (`EXTERNAL_MAIL_REVOKED_RETENTION`, e.g. `90d`); roadmap
§5.1 item 7 also asks for the number of synced messages and a "delete now" choice.

Actual: "Syncing stops now. Mail already synced is kept for 30 days." from
`web/src/i18n/modules/mailbox/en-US.ts:1643`, regardless of configuration; no count and no
immediate purge in the UI (the purge route has no Web route yet, as the contract table says).

### D6 — minor — Mock BFF copies have no `source`

Steps: stack C (mock mode), connect Outlook, open All mail.

Expected: every copy carries the connector id as `source` (contract) and shows a source badge, as it
does against the real API.

Actual: `/api/inbox-messages` items from `mock-external-mail-store` omit `source`; the Web reads a
missing `source` as `zmail`, so no "Outlook" badge appears in mock mode.

### D8 — minor — `UPGRADING.md` contradicts itself on which worker purges

`UPGRADING.md` lines 21-24: "the `mail:external:sync` worker deletes the copies … The deletion runs
in the mail worker (`mail:broadcast:work`)". The contract and the code use `mail:broadcast:work`.

### D9 — minor — Focus is not returned after closing the connect dialog

Steps (1280 px): focus the sidebar "Add external mailbox" button, press Enter, then Escape.

Expected: focus returns to the button that opened the dialog.

Actual: `document.activeElement` is `<body>`. Observed in the in-app browser pane; a keyboard user
loses their place.

### D10 — minor — Error states keep the "Connecting your mailbox" heading

On every callback error the `h1` still reads "Connecting your mailbox" above the error alert, which
reads as if work is still in progress.

## Not Tested, And Why

- **Live sync and de-duplication (case 3):** the fake source cannot be fed in a running stack (see
  case 3); covered by automated tests only.
- **Real Gmail, Outlook, or IMAP providers:** not allowed in this run; the IMAP success path, TLS
  verification, and `needs_reconnect` on a refused login are covered only by
  `external_mail_imap_test.go`.
- **AI triage switched on by the owner:** no AI model configured (`409 TRIAGE_UNAVAILABLE`).
- **Retention expiry by the worker** (30-day or `90d` clock): not waited for; covered by
  `TestExternalMailRetentionPurgesRevokedCopies` and `TestExternalMailRetentionRunsWithProvidersOff`.
- **`202` partial purge, backoff schedule, fetch deadline, round-robin budget:** automated tests only.
- **`EXTERNAL_MAIL_PROVIDERS=fake` refused in production:** `doctor` in production stops earlier on
  `DB_SSLMODE`; covered by the config tests.
- **Expired (10-minute) sign-in:** not waited for; covered by
  `TestExternalMailAuthorizationIsSingleUseAndBound`.
- **Admin Console:** connectors have no admin page yet (roadmap §5.2 is later work).

## Side Effects On The Shared Dev Database

- Created organization 2, three local test members, connections 3–14, mailboxes 6–17 (7 and 15
  purged), four fixture `mail_messages` rows (subjects starting with `ACCEPT`), and invitation rows
  whose `token_hash` was set by the tester. Probe mailboxes 18–20 were deleted.
- Running `mail:external:sync --once` claims every due connection in the database. Connection 1
  (organization 1, `stark.personal@external.invalid`, created earlier by another session) was sealed
  with a different `MAILBOX_SECRET_ENCRYPTION_KEY`, so the run moved it from `active` to
  `needs_reconnect` (`external.auth_failed`). Reconnecting it from its own stack restores it.
  This is by design ("A token this instance cannot open is as unusable as one the provider
  refuses"), but it means a key change on an instance sends every member to reconnect.

## Automated Gate

`make check` from the repository root on the tested commit, with Node 24 first on PATH,
`golangci-lint` v2.14.0, `CI=true`, and
`FASTBUILD_TEST_POSTGRES_DSN=postgres://fastbuild:…@127.0.0.1:55432/zmail_test?sslmode=disable`:
**passed (exit 0)**. Governance checks all passed (including the error-contract, OpenAPI-route,
and database-boundary checks); Tier 0 + 1 (`go build`, `golangci-lint run`, `go test ./...`) passed;
Tier 2 (Web and Admin type-check, lint, tests, and `pnpm build`) passed.

Targeted runs on the same commit: `go test ./internal/modules/mailbox/ ./internal/infra/config/
-run 'ExternalMail|LeavingTheOrganization|InboundDeliveryNever|SyncedMailIsQueued|ExternalMailbox'`
→ 50 tests passed; `pnpm vitest run` on the three external-mail test files → 36 tests passed.

The gate passing while D1, D2, and D7 exist means the automated tests do not cover rebinding at the
limit, the mailbox limit and directory with external mailboxes, or the worker processes the stack and
deploy bundles run.
