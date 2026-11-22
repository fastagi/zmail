# Connector Acceptance Report: `integration/connectors`, Round 2

- **Tested commit:** `d9d1d1ac1710309ef626fee4dacbde3384a17ae3` (`origin/integration/connectors`,
  "Merge remote-tracking branch 'origin/fix/connector-acceptance-api' into HEAD"; contains
  `origin/main` `1175e12`)
- **Date:** 2026-11-21 / 22 (UTC)
- **Tester:** independent acceptance agent (did not write the code; no product code changed)
- **Scope:** regression of round 1 ([`connectors-acceptance-main-1.md`](connectors-acceptance-main-1.md),
  D1 to D10); the sync case blocked in round 1; the mailbox origin model (personal/shared ×
  zmail/connected, manager visibility, connection directory, forced disconnect); agent grants and the
  agent mail feed; the reference gate. Contracts: `contracts/MAILBOXES.md` (Access, External Mailboxes,
  Connection Directory, Agent Grants, Inbound Delivery), `contracts/API_KEYS.md`, `contracts/AUDIT.md`.
  Requirements: `docs/PRODUCT_REQUIREMENTS.zh-CN.md` R2/R4, `docs/plans/mail-connectors-roadmap.zh-CN.md`
  §4, §5, §7, `docs/MAILBOX_ACCESS_PRD.zh-CN.md`.

## Summary

| # | Case | Result |
|---|---|---|
| A1 | D1 rebind of a revoked connection at the limit of 10 | Pass — fixed |
| A2 | D2 connected mailboxes in the mailbox limit and the directory | Pass — fixed |
| A3 | D3 managers get `see_history` on members' connected mailboxes | Pass — fixed |
| A4 | D4 `backfill_days` out of range error code | Pass — fixed |
| A5 | D5 disconnect confirmation hard-codes 30 days | Pass — changed (wording fixed; count and "delete now" still absent, see R1) |
| A6 | D6 mock BFF copies carry no `source` | Pass — fixed |
| A7 | D7 nothing runs `mail:external:sync` | Pass — fixed for `make dev` and self-host; Kubernetes deliberately out of scope |
| A8 | D8 `UPGRADING.md` contradicts itself on the purging worker | Pass — fixed |
| A9 | D9 focus not returned after closing the connect dialog | Pass — fixed |
| A10 | D10 callback error keeps "Connecting your mailbox" | Pass — fixed |
| B1 | Live sync through IMAP to a loopback server | Blocked — no supported override (by design) |
| B2 | Live sync through the `fake` connector | Blocked — no dev delivery hook |
| B3 | Automated coverage of "appears once with a source badge, second sync adds no copy" | Pass (tests run on this commit) |
| C1 | Mailbox list, directory (+search), export hide a member's connected mailbox | Pass |
| C2 | Unified inbox and its counts | Pass |
| C3 | Log/search (`/mail-messages`), statistics, operator metrics | Pass |
| C4 | Message, thread, attachment, counts, history routes by id | Pass |
| C5 | Write routes by id (triage switch, webhook, role, rename, read flag, reveal, handover, delete, grants) | Pass |
| C6 | Platform-account ledger | Pass |
| C7 | Connection directory shows only masked data | Pass |
| C8 | Forced disconnect works | Pass |
| C9 | Forced disconnect is recorded where managers can see it | **Fail** (N1) |
| C10 | Enabling receiving on a domain creates no routing rule for a connected address | Blocked live (no Cloudflare); automated test passes |
| C11 | Inbound delivery to a connected address | Pass |
| C12 | Organization mailbox limit ignores connected mailboxes | Pass |
| C13 | Web: manager and member views | Pass |
| D1 | Grant issuance rules (scope, test key, other's key, expiry, capabilities) | Pass |
| D2 | Owner/admin cannot grant on a member's personal or connected mailbox | Pass |
| D3 | Feed returns exactly the granted mailbox's new mail, in order | Pass |
| D4 | Feed replayable from a cursor; paging; bad parameters | Pass |
| D5 | Sender filter (domain, subdomain, look-alike) | Pass |
| D6 | Codes masked unless `reveal_secrets`; reveal audited | Pass |
| D7 | Drafts need `draft`, never send, replace, validate | **Fail** (minor, N2) |
| D8 | Key without scope / without grant vs missing message | Pass |
| D9 | Revocation: revoke | Pass |
| D10 | Revocation: grant expiry | Pass |
| D11 | Revocation: API key revoked; API key expired | Pass |
| D12 | Revocation: key owner removed from the organization | Pass |
| D13 | Revocation: shared-mailbox member loses view | Pass |
| D14 | Revocation: handover of a personal mailbox; drafts of sealed mail not listed | Pass |
| D15 | Revocation: connected account disconnected | Pass |
| D16 | Agent reads change no one's read state | Pass |
| E1 | Reference gate `make check` | **Fail** (exit 2: one Web Vitest timeout under load; passes alone; see E) |

**Totals: 37 pass, 3 fail, 3 blocked** (43 cases) (A5 "changed" is counted as pass; its residual is R1).

Defects by severity (new in round 2; none from round 1 is still present as filed):

| Id | Severity | One line |
|---|---|---|
| N1 | minor | A forced disconnect's audit row is readable only by the admin who did it (`/v1/audit-logs` is user-scoped) and platform operators; other owners/admins cannot see who disconnected an account, though the contract says they can |
| N2 | minor | `POST /v1/agent-mail-drafts` without `text` answers `422 COMMON.VALIDATION_FAILED`, not the documented `422 MAILBOX.MESSAGE_INVALID` |
| N3 | minor | With 10 live connections a revoked one is dropped from `GET /v1/external-mail-connections`, so its copies stay listed in the sidebar with no status and the member cannot reconnect or purge it from the UI |
| R1 | minor | (Residual of D5) the disconnect confirmation shows no synced-message count and offers no "delete now" (roadmap §5.1 item 7) |
| E1 | minor | `web/src/test/env-contract.test.ts` "keeps production source code behind src/config/env.ts" times out at the 5 s default under machine load, failing `make check` |

No blocker or major defect was found.

## Environment And Method

- macOS, Node 24 (`/opt/homebrew/opt/node@24/bin` first on PATH), host Go toolchain, dev PostgreSQL
  container `zmail-dev-postgres-1` on `127.0.0.1:55432`.
- **Own database:** `CREATE DATABASE zmail_accept2`; the API binary was built from this commit
  (`go build ./cmd/fastbuild`) and run with the environment of `scripts/dev.sh` `api_environment`
  except `DB_NAME=zmail_accept2`, `SERVER_PORT=8055`, CORS/operator origins on 3055/4255, plus
  `EXTERNAL_MAIL_PROVIDERS=fake`, a local 44-character `MAILBOX_SECRET_ENCRYPTION_KEY`,
  `EXTERNAL_MAIL_REDIRECT_URL=http://localhost:3055/console/mailboxes/connected/callback`, and
  `AUTH_REGISTRATION_EMAIL_CODE_REQUIRED=false`. `migrate`, `seed`, and `operator:grant
  admin@example.com` were run by hand. `mail:broadcast:work --poll=2s` ran next to it. The API was
  restarted for single cases with `MAILBOX_MAX_PER_ORGANIZATION=4` (C12/A2) and
  `EXTERNAL_MAIL_PROVIDERS=imap` (A4/B1), then back to `fake`.
- **Web:** `next dev --turbopack --port 3055` with the adapter on (`API_ADAPTER_ENABLED=true`,
  `API_UPSTREAM_URL=http://127.0.0.1:8055/v1`, `MOCK_BFF_ENABLED=false`), driven in the in-app browser
  pane. The pane was hidden during the run, so the page ran as a background tab; client renders were
  only observed after a screenshot (this delayed hydration on the callback page and is not a product
  issue).
- **Accounts (organization 1, "Accept Two Org"):** owner U = `user@example.com` (user 2), admin A =
  `admin@example.com` (user 1, also platform operator), members M1 = `acc2m1` (user 3) and M2 =
  `acc2m2` (user 4), both registered locally. Invitations were accepted with the round-1 method: the
  tester set `organization_invitations.token_hash` to the SHA-256 of a known local token and called
  `POST /v1/organization-invitations/accept`.
- **Mailboxes:** connected (fake Gmail) mailboxes 1–11 of M1 (`alice@`, `acc2@` … `acc11@external.invalid`;
  connection 2 revoked), 15 of M2 (`zo@external.invalid`), Zmail personal 12 (M1), 13 (M2), shared 14
  `acc2team@mail.localhost` (M1 `manage`, M2 `view`), personal 16 (U, limit probe).
- **Connected mail fixture:** the fake connector cannot be fed (Part B), so, as in round 1, one row was
  inserted into `mail_messages` for connected mailbox 1 (id 2, subject `ACC2FIXTURE connected secret
  subject`, sender `carol@secret-partner.test`, body with code `482913`, `source='gmail'`,
  `external_id='gm-fx-1'`) with one attachment row (id 1). It stands in for a synced copy; it does
  not test the sync.
- **Inbound mail:** signed with `MAILBOX_INBOUND_SECRET` from `scripts/dev.sh` (HMAC-SHA256 over
  `<t>.<lowercase recipient>.<raw body>`) and posted to `POST /v1/inbound-mail-messages`.
- API calls: curl scripts in the session scratchpad; leak checks grepped every response for
  `alice@external`, `ACC2FIXTURE`, `secret-partner`, and `482913`.
- No real mail provider was contacted. IMAP hosts used were loopback or `.invalid`.

## Part A — Regression Of Round 1

### A1 (D1) — Fixed

Steps: M1 binds `alice`, `acc2` … `acc10` (10 live) → 11th new account `acc11` → `409
MAILBOX.EXTERNAL_CONNECTION_LIMIT`. `DELETE /external-mail-connections/2` → `204`. Bind `acc11` →
`201` (10 live). Bind `acc2` again (own revoked connection) → **`409 MAILBOX.EXTERNAL_CONNECTION_LIMIT`**.
Reconnect of live connection 3 at the limit → `200`. Database: `active|10`, `revoked|1`.
`GET /external-mail-connections` → 10 rows, all live. Contract now has the row "the caller's own
`revoked` connection, while the caller holds 10 live … `409`" and this matches. See N3 for what the
member then sees of the revoked connection.

### A2 (D2) — Fixed

With 11 connected mailboxes of M1 in the organization: `GET /mailbox-directory` (owner, admin) → `[]`,
`meta.mailbox_count 0`; with `?q=alice`, `?type=personal` → empty; `GET /mailbox-exports?type=personal`
→ header only. API restarted with `MAILBOX_MAX_PER_ORGANIZATION=4` and 3 Zmail mailboxes: create →
`201` (id 16), next → `409 MAILBOX.LIMIT_REACHED`; `mailbox-directory` `meta.mailbox_count 4,
max_mailboxes 4`; import dry run → row `MAILBOX.LIMIT_REACHED`; M2 binding a new connected account at
that moment → `201`. Operator `GET /v1/operator/mail-metrics` → `mailboxes: 3`, organizations list
`mailboxes: 3` (Zmail only).

### A3 (D3) — Fixed

Owner and admin: connected mailboxes are absent from `GET /mailboxes`, and
`GET /mailboxes/1/access-events` → `404 MAILBOX.NOT_FOUND`. M1 (owner of the binding) has
`{view: true, see_history: true, send/administer/organize: false}`.

### A4 (D4) — Fixed

`EXTERNAL_MAIL_PROVIDERS=imap`: `POST /external-mail-credentials` with `backfill_days: 91` and `-1` →
`422 MAILBOX.EXTERNAL_AUTHORIZATION_INVALID`. With `fake`: `POST /external-mail-authorizations
{"provider":"gmail","backfill_days":91}` → `422 MAILBOX.EXTERNAL_AUTHORIZATION_INVALID`;
`backfill_days: 0` → `201` (the contract now says `0` takes 14). The IMAP password marker never
appeared in the API log or `audit_logs.metadata`. Note: `fake,imap` together refuses to start
("`fake` stands in for every provider and cannot be combined with a real one"), as designed.

### A5 (D5) — Changed

Web, M1, "Manage connected mailboxes" → Disconnect on `acc3`: "Disconnect acc3@external.invalid?
Syncing stops now. Mail already synced is kept for a limited time and then deleted." The hard-coded
"30 days" is gone. The confirmation still has no count of synced messages and no "delete now" choice
(roadmap §5.1 item 7) — residual R1. Not confirmed (connection 3 stayed `active`).

### A6 (D6) — Fixed (code and automated test; mock mode not run live)

`web/src/features/mailbox/server/mock-mailbox-store.ts` `syncExternal` now stores `source: provider`;
`web/src/test/external-mail-route.test.ts` asserts `source: 'gmail'` on every copy in the mailbox
listing and the unified inbox. The three external-mail Vitest files passed (40 tests). Mock mode was
not started live because only one `next dev` may run in `web/`.

### A7 (D7) — Fixed (Kubernetes deliberately excluded)

- `scripts/dev.sh` starts `mail:external:sync --poll=30s` as `[external-mail]` whenever
  `EXTERNAL_MAIL_PROVIDERS` is set (and the mailbox starter is on); `print_usage` documents it. The
  comment says the `fake` worker has its own empty source and records runs but no mail.
- `deploy/self-host/compose.yaml`: passes `EXTERNAL_MAIL_*` and `MAILBOX_SECRET_ENCRYPTION_KEY`, and
  defines `external-mail-worker` (`mail:external:sync --batch=20 --poll=30s`) in the `external-mail`
  profile. `docker compose config --services`: without a profile → `postgres migrate api
  broadcast-worker mail-zone-worker web webhook-worker`; with `COMPOSE_PROFILES=external-mail` →
  `external-mail-worker` added. `deploy/self-host/README.md` "Mail Connectors" tells operators to set
  the profile.
- `deploy/kubernetes` has no mail workers at all and no `mail:external:sync` (left out on purpose).
- Live: `fastbuild mail:external:sync --once` against this stack handled 9 connections, one `ok` run
  each (`fetched_count 0`, `stored_count 0`); M1 sees the run via `GET /external-mail-connections/3/sync-runs`
  (`200`), the owner gets `404 EXTERNAL_CONNECTION_NOT_FOUND`.

### A8 (D8) — Fixed

`UPGRADING.md` lines 126–131: "the mail worker (`mail:broadcast:work`) deletes the copies … The
deletion runs there, not in `mail:external:sync`". No contradiction remains; the self-host README says
`broadcast-worker` purges.

### A9 (D9) — Fixed

Web 1280 px: focus "Add external mailbox", Enter → dialog, focus on "Gmail"; Escape → dialog closed,
`document.activeElement` is the "Add external mailbox" button.

### A10 (D10) — Fixed

`/console/mailboxes/connected/callback?state=x&error=access_denied` → single `h1` "Could not connect
your mailbox", alert "The sign-in was cancelled, so nothing was connected.", address bar
`/console/mailboxes/connected/callback`. Unknown `state` → same heading, "This sign-in expired or was
already used…", focus on the heading.

## Part B — The Sync Case Blocked In Round 1

### B1 — IMAP against a loopback server: Blocked (no supported override)

`POST /external-mail-credentials` with host `127.0.0.1` or `localhost`, port 993 → `422
MAILBOX.EXTERNAL_SERVER_UNREACHABLE`. The only bypass is the struct field `AllowPrivate` on the IMAP
provider (`api/internal/modules/mailbox/external_mail_imap.go:125`, "lets tests reach loopback and
private addresses"), set only by tests. No environment variable, config key, or CLI flag reaches it
(`grep AllowPrivate` outside tests: none; `config.go` has `WEBHOOK_ALLOW_PRIVATE_TARGETS` only, for
webhooks). So no supported development override exists, by design.

### B2 — Fake connector: Blocked (no delivery hook)

`FakeExternalMailSource.Deliver` (`external_mail_source.go:256`) has no caller outside tests: no HTTP
route and no command (`fastbuild --help` lists no such command under `mail`). `mail:external:sync`
builds its own empty fake source in its own process, so even in the API process nothing can add mail.
The live run in A7 confirms: runs recorded, nothing fetched.

### B3 — Automated coverage: Pass

Run on this commit against PostgreSQL (`go test ./internal/modules/mailbox/ -count=1 -run …`), all
passed:

| Test | Covers |
|---|---|
| `TestExternalMailSyncIsIdempotent` | Delivered messages stored once with `source gmail`, inbound, provider receive time; a repeated fetch from an empty cursor adds no copy |
| `TestExternalMailReconnectRestoresWithoutDuplicates` | Reconnect refetches without second copies |
| `TestGmailSyncStoresOnce`, `TestOutlookSyncStoresOnceAndRotatesToken`, `TestIMAPSyncStoresMessagesOnce` | Each real adapter (against test doubles) stores a message once |
| `TestSyncedMailIsQueuedForTriage` | Post-store step for synced mail |
| `TestAgentGrantsOnConnectedMailbox` | A synced message reaches the owner's agent feed |
| `TestListingsNeverLoadOtherMembersConnectedMailboxes` | Listing/inbox repositories drop other members' connected mailboxes |
| Web `inbox-ui.test.tsx` (source badge), `external-mail-route.test.ts` | Badge rendering; `source` on every listed copy |

Gap: no single test drives "sync → `GET /v1/inbox-messages` shows the copy once with `source` → second
sync → still once" over HTTP; the pieces are covered separately. Live, the fixture copy appeared once
in M1's unified inbox with `source: "gmail"` and the Web showed a "Gmail" badge (C13).

Recommendation: a development-only hook that is refused outside `APP_ENV=development` and only with
`EXTERNAL_MAIL_PROVIDERS=fake`, for example `fastbuild mail:external:fake-deliver --connection=<id>
--file=<eml>` that writes into a **database-backed** fake source (a dev table keyed by the fake refresh
token) so the API and the separate sync worker share it; or an env switch
`EXTERNAL_MAIL_IMAP_ALLOW_PRIVATE_HOSTS=true` accepted only in development so a local IMAP server
(e.g. a container on loopback with a self-signed CA trusted via `SSL_CERT_FILE`) can be bound.

## Part C — Origin Model

Setup: M1's connected mailbox 1 with fixture message 2 and attachment 1. Every route below was called
as M1 (control), U (owner), A (admin), and M2 (member).

### C1–C6 — Entry points

| Route | M1 | U, A | M2 |
|---|---|---|---|
| `GET /mailboxes`, `?scope=usable` | `200`, lists mailboxes 1–11 | `200`, connected absent | `200`, absent |
| `GET /mailbox-directory` (`q=alice`, `type=personal`) | `403` | `200 []`, `mailbox_count` excludes them | `403` |
| `GET /mailbox-exports?type=personal|shared` | `403` | `200`, header only / Zmail rows | `403` |
| `GET /inbox-messages` (all, `mailbox_id=1`, `q=ACC2FIXTURE`) | `200`, fixture with `source: gmail` | `200`, no leak | `200`, no leak |
| `GET /inbox-message-counts` | `200`, mailboxes 1–11 listed | `200`, `mailboxes: []`, zeros | `200`, no leak |
| `GET /mail-messages` (all, `q`, `mailbox_id=1`) | `200`, fixture | `200`, no leak | `200`, no leak |
| `GET /mail-message-stats` (all, `mailbox_id=1`) | received 2 | received 0 | received 0 |
| `GET /mailboxes/1/messages`, `/messages/2`, `/threads/2`, `/messages/2/attachments/1`, `/message-counts`, `/access-events`, `/mail-folders`, `/mail-labels`, `/agent-grants`, `/agent-drafts`, `/members` | `200` (drafts `403`, members `409`) | **`404 MAILBOX.NOT_FOUND`** | `404` |
| `PUT /mailboxes/1/triage-setting {"enabled":true}` | `409 TRIAGE_UNAVAILABLE` (no AI model) | `404` | `404` |
| `PUT /mailboxes/1/webhook-setting`, `/role-assignment`, `PUT /mailboxes/1/messages/2 {"read":true}`, `POST …/messages/2/reveal`, `POST /mailboxes/1/agent-grants` | various | `404` | `404` |
| `PUT /mailboxes/1 {"display_name"}` (rename) | `403` | `404` | `403` (any id, also `9999`: role check first, no oracle) |
| `POST /mailboxes/1/handovers {"successor_user_id":2}` | `403` | `404` | `404` |
| `DELETE /mailboxes/1`, `DELETE /mailboxes/2` (revoked connection's mailbox) | — | `404` | — |
| `GET /platform-accounts?q=alice`, `?mailbox_id=1` | `200 []` | `200 []` | `200 []` |
| `POST /platform-accounts {"mailbox_id":1}` | — | `422` "must be a mailbox of the organization or null" (same answer as id `9999`) | — |

No response to U, A, or M2 contained the address, subject, sender, body, or code. Operator metrics count
Zmail mailboxes only (A2).

### C7 — Connection directory: Pass

`GET /external-mail-connection-directory` (U, A) → `200`, keys exactly `id, provider, status,
masked_account_address, owner_user_id, owner_display_name, last_synced_at, created_at`; masks
`al***@external.invalid`, `ac***@…`, `z***@external.invalid` (two-character local part keeps one).
Keyset paging (`per_page=3` → `has_more true`, `next_cursor`); `page=2` → `400 COMMON.INVALID_INPUT`.
M1 → `403 PERMISSION.DENIED`.

### C8 — Forced disconnect: Pass

M1 `DELETE /external-mail-connection-directory/12` → `403`. A → `204`, again → `204`, `9999` → `404
EXTERNAL_CONNECTION_NOT_FOUND`. M2's connection 12 → `revoked`; M2's mailbox 15 history:
`connection_force_disconnected` (actor 1, subject 4, `{connection_id, provider}`), no address.
Force-disconnecting M1's connection 1 also revoked its agent grant (`connection_revoked`, D15); M1
still reads the copy.

### C9 — Recorded where managers can see it: Fail (N1)

`audit_logs` row 100: `delete external_mail_connections 12`, metadata `owner_user_id 4, provider
gmail, reason forced_disconnect`, no address — correct. But `GET /v1/audit-logs?resource=external_mail_connections`
as U (the organization owner) → `200 []`; only A (the actor) sees it, and the platform operator via
`/v1/operator/audit-logs`. See N1.

### C10 — Domain receiving and connected addresses: Blocked live

`POST /v1/mail-domains` → `404 MAILBOX.DOMAINS_DISABLED` on this stack (no Cloudflare). Covered by
`TestConnectedAddressesStayOutOfDomains` (a connected `member@news.example.com` is not among
`ListMailboxesWithoutRoutingRule` and does not block `DeleteDomain`), passed on this commit.

### C11 — Inbound to a connected address: Pass

Signed delivery to `alice@external.invalid` → `404 MAILBOX.RECIPIENT_NOT_FOUND`.

### C12 — Mailbox limit: Pass

See A2.

### C13 — Web: Pass

- M1: sidebar "Connected by me" lists `alice@…` (Disconnected) and `acc2@…`–`acc11@…`; All mail shows
  the fixture once with a "Gmail" badge and the mailbox address; the code is masked in the snippet.
- U: sidebar "Team mailboxes" shows Zmail mailboxes only ("No access" on others' personal mailboxes),
  "Connected by me" empty, inbox "No mail yet". `/console/admin/mailboxes` lists only
  `acc2team@` and `acc2m1@` (Zmail).
- There is no Web page for the connection directory yet (the API is only reachable from the generated
  client); the roadmap lists a manager "external connections" list under C1. Not filed as a defect for
  this merge; noted under Not Tested.

## Part D — Agent Access (API)

Keys (all created by their owners through `POST /v1/api-keys`): K1, K2, K5, K6, K7 (`mail:agent`, M1),
Kns (`mail:send, mail:read`, M1), Kt (`mail:agent`, `mode: test`, M1), Kstar (`*`, M1), K3 and K4
(`mail:agent`, M2), KU (`mail:agent`, U). K7 was created with `expires_at` now + 90 s.

### D1 — Issuing: Pass

| Request (issuer → mailbox, key) | Result |
|---|---|
| M1 → 12, K1 `read,draft` | `201` grant 1 |
| M1 → 12, K2 `read`, `reveal_secrets`, `senders:["vendor.test"]` | `201` grant 2 |
| M1 → 12, K1 again | `409 MAILBOX.AGENT_GRANT_EXISTS` |
| M1 → 12, Kns (own, no scope) | `422 AGENT_GRANT_INVALID` "needs the mail:agent scope" |
| M1 → 12, Kt (own, test) | `422` "a test API key cannot hold an agent grant" |
| M1 → 12, K3 (M2's key) / key `999` | `422`, identical undifferentiated message |
| `expires_at` + 91 days / in the past | `422` "at most 90 days ahead" |
| `capabilities: ["draft"]` | `422` "must include read" |
| M1 → connected 1, `read,draft` | `422` "a connected mailbox is read-only" |
| M1 (`manage`) → shared 14, K3 (M2, `view`) `read` | `201` grant 3 |
| M2 → own 13, K4 `read,draft` | `201` grant 4 |
| M1 → connected 1, Kstar `read` | `201` grant 6 |

Each issue wrote `agent_granted` with no address.

### D2 — Managers cannot grant on a member's mailbox: Pass

U → 12 (own KU or M1's K5) → `403 PERMISSION.DENIED`; A → 12 → `403`; U → connected 1 → `404
MAILBOX.NOT_FOUND`; M2 → 12 → `404`; U → shared 14 (not a member) → `403`; M2 (`view`) → 14 → `403`.
U revoking grant 1 on 12 → `403`.

### D3–D5 — Feed content, order, cursor, filter: Pass

Delivered (signed inbound): to 12 — a1 `bob@vendor.test` (code 739201 and a sign-in link), a2
`eve@other.test`, a3 `ops@eu.vendor.test`, a4 `x@notvendor.test`; to 13 — a5; to 14 — a6. Redelivering
a1 → `200 {"duplicate": true}`; a bad signature → `401`.

| Key | `GET /v1/agent-mail-events` (event id, mailbox, message, grant) |
|---|---|
| K1 | `(1,12,3,1) (4,12,4,1) (6,12,5,1) (9,12,6,1)` — exactly the four new messages of 12, ascending; message 1 (stored before the grant) absent |
| K2 (filter `vendor.test`) | `(2,12,3,2) (7,12,5,2)` — bob@vendor.test and the subdomain; not eve, not `notvendor.test` |
| K3 | `(12,14,8,3)` |
| K4 | `(11,13,7,4)` |
| Kstar (connected mailbox, nothing synced), KU (no grant) | `[]`, `next_cursor "0"` |

K1 `limit=2` → two events, `next_cursor "4"`, `has_more true`; `after=4` twice → the same two events;
`after=9` → `[]`, `next_cursor "9"`. `limit=0`, `limit=101`, `after=abc` → `400 COMMON.INVALID_INPUT`.
The duplicate delivery wrote no second event.

### D6 — Masking and reveal: Pass

K1 reads message 3 → `secrets_masked: true`, text "Your verification code is ••••••. Or sign in:
vendor.test/••••". K2 (reveal) → `secrets_masked: false`, code and link in clear. `mail_agent_audit`:
`(grant 1, read)`, `(grant 2, read_revealed)`; the API log has one `mail.agent_secret_revealed` line.
`GET /mailboxes/12/agent-grants/1/activity` lists `read` and `draft` rows without subject or body.

### D7 — Drafts: Fail (minor, N2)

| Request | Result |
|---|---|
| K1 draft on 3 | `201` draft 1 `pending` |
| K1 draft on 3 again | `200`, body replaced, same id |
| K2 (no `draft`) on 3 | `403 PERMISSION.DENIED` |
| K3 (grant without `draft`) on 8 | `403` |
| K1 on 7 (other mailbox) | `404 MAILBOX.MESSAGE_NOT_FOUND` |
| K1 text > 100 KB | `422 MAILBOX.MESSAGE_INVALID` |
| K1 without `text` | **`422 COMMON.VALIDATION_FAILED`** (N2) |
| Kns | `403` |

Outbound `mail_messages` stayed 0 after the drafts and a broadcast-worker pass: nothing was sent.
`GET /mailboxes/12/agent-drafts`: M1 → the draft; U → `403`.

### D8 — No scope, no grant, missing message: Pass

Agent read of message 3 by KU (scope, no grant), K1 of pre-grant message 1, K1 of message 7 (another
mailbox), K2 of filtered-out message 4, Kstar of connected fixture 2, and K1 of nonexistent `99999` all
→ `404 MAILBOX.MESSAGE_NOT_FOUND`, identical. Kns (no scope) → `403 PERMISSION.DENIED` for both an
existing and a nonexistent message (as `API_KEYS.md` specifies; the answer does not depend on
existence). Kt → `403` "Test API keys are not accepted here". No key → `401`; a session token on the
agent route → `401 AUTH.UNAUTHORIZED`.

### D9–D15 — Revocation paths: Pass

A message b1 was delivered to 12 before the revocations; after each step a new message was delivered
to check that no event is written.

| Path | Feed | Message read | Durable record |
|---|---|---|---|
| Revoke grant 2 (`DELETE …/agent-grants/2` → `204`, again `204`, `999` → `404 AGENT_GRANT_NOT_FOUND`) | `[]` at once | `404` | `revoked`/`revoked`, `agent_revoked` event |
| Grant 7 `expires_at` now + 75 s, checked after expiry | `[]` | `404` | `status: expired` |
| K5 revoked (`DELETE /api-keys/5`) | `401 API_KEY.REVOKED` | `401` | — (listing still says `active`; see observations) |
| K7 key `expires_at` passed | `401 API_KEY.EXPIRED` | `401` | — |
| M2 removed from the organization | `404 ORGANIZATION.NOT_FOUND` | `404` | grant 4 `left_organization`; M2's draft 3 on 13 deleted; new mail to 13 wrote no event |
| M2 removed from shared 14 | `[]` | `404` | grant 3 `member_removed`; re-adding M2 does not revive it |
| Handover of 12 to A (`POST /mailboxes/12/handovers`) | `[]` | `404`; new draft → `404` | grants 1, 5, 7, 8 `handed_over`; draft 1 deleted; A `GET /mailboxes/12/agent-drafts` → `[]`; A sees no sealed message |
| Connection 1 force-disconnected | `[]` | — | grant 6 `connection_revoked` |

### D16 — Read state: Pass

M1's mailbox 12 before and after K1 and K2 reads: every message `read: false`,
`default_inbound_unread 5`; agent responses show `read: false`; `mail_message_reads` unchanged.

## Part E — Automated Gate

`make check` once from the repository root on the tested commit (Node 24 first on PATH,
`golangci-lint` v2.14.0, `CI=true`, `FASTBUILD_TEST_POSTGRES_DSN=…/zmail_test`). A first attempt
stopped at `contract-check` because the fresh worktree had no `node_modules` (`redocly: command not
found`); after `pnpm install --frozen-lockfile` in `contracts/`, `web/`, `admin/`, the run gave:

- Governance checks: all passed (including contract check).
- API Tier 0 + 1 (`go build`, `golangci-lint run`, `go test ./...`): **passed**.
- Web: type-check and lint passed; `pnpm test` **failed**: 1 of 1025 tests,
  `src/test/env-contract.test.ts` "keeps production source code behind src/config/env.ts" — "Test
  timed out in 5000ms". Tier 2 `pnpm build` passed. Exit **2**; make stopped before the Admin check.
- The machine was heavily loaded (load average 12–19 from other sessions). Re-run alone,
  `vitest run src/test/env-contract.test.ts` plus the two external-mail files → 3 files, 40 tests
  passed. Admin run separately (`type-check`, `lint`, `test`, `build`) → passed (22 files, 89 tests).

## Defects

### N1 — minor — Forced disconnects are not visible to the other owners and admins

Steps:
1. As admin A: `DELETE /v1/external-mail-connection-directory/12` (M2's connection) → `204`.
2. As owner U: `GET /v1/audit-logs?resource=external_mail_connections`.
3. As U: `GET /v1/external-mail-connection-directory`, and `GET /v1/mailboxes/15/access-events`.

Expected: `contracts/MAILBOXES.md` Connection Directory: "A forced disconnect … is also written to the
organization audit log (resource `external_mail_connections` …), so every owner and admin can see who
disconnected which account."

Actual: the row exists (`audit_logs.id 100`, actor 1, target 12, `owner_user_id 4`, `provider`,
`reason forced_disconnect`), but `GET /v1/audit-logs` returns only the caller's own records
(`contracts/AUDIT.md`: "returns only records whose `user_id` is the authenticated caller. It does not
expose organization-wide … history"), so U gets `[]`. The directory shows only `status: revoked`, and
the mailbox history (which has `connection_force_disconnected` with the actor) answers managers `404`
by design. Only the acting admin, the affected member, and platform operators can see who did it.
Either an organization-scoped audit view is needed or the contract sentence should be corrected.

### N2 — minor — Draft without `text` uses the generic validation code

Steps: `POST /v1/agent-mail-drafts` with `X-API-Key` of a key holding a `draft` grant, body
`{"message_id": 3}`.

Expected: `422 MAILBOX.MESSAGE_INVALID` ("`text` is required, and `text` and `html` are at most 100 KB
each (`422 MAILBOX.MESSAGE_INVALID`)").

Actual: `422 COMMON.VALIDATION_FAILED` with `errors.text ["is required"]` (binding tag). Oversize text
correctly answers `MAILBOX.MESSAGE_INVALID`. If the parenthetical was meant for the size rule only, the
contract should say so.

### N3 — minor — A revoked connection can drop out of the member's reach at 10 live connections

Steps:
1. M1 holds 10 live connections and revoked connection 2 (`acc2@external.invalid`, mailbox 2).
2. `GET /v1/external-mail-connections` (M1); Web "Manage connected mailboxes".

Expected: the member can see the revoked connection's state and act on it (reconnect when a place is
free, or purge its copies with `POST /external-mail-connections/2/purges`).

Actual: the list returns the 10 live connections only (contract: "live ones first … at most 10"), so
connection 2 appears nowhere with its id or status. The sidebar still lists `acc2@external.invalid`
with no status dot, and the dialog shows no row for it, so the member cannot reconnect or purge it from
the UI; the copies wait for the retention. Conforms to the contract's cap but leaves the copies
unmanageable; consider listing revoked connections beyond the cap or paging.

### R1 — minor — Disconnect confirmation (residual of D5)

The confirmation no longer states a wrong number of days, but still gives no count of synced messages
and no "delete now" option (roadmap §5.1 item 7); the purge route still has no Web entry.

### E1 — minor — Flaky Web test under load

`web/src/test/env-contract.test.ts` walks every source file within Vitest's 5 s default timeout; on a
loaded machine it times out and fails `make check`. Passed when run alone. Consider a longer timeout for
this test.

## Observations (not defects)

- After its key is revoked, a grant is still listed with `status: "active"` in
  `GET /mailboxes/:id/agent-grants`; it receives no events and the key fails with `401`. The contract
  defines key revocation as an authentication failure, not a grant status.
- Binding answers `last_synced_at: null`: "synced at once" means due for the worker at once; nothing
  syncs in the API process.
- The 422 for another member's or an unknown key carries the reason text "api_key_id must name an
  active mail:agent key of a member who can read this mailbox" — identical for every case, so it does
  not differentiate.
- `PUT /v1/mailboxes/:id` answers a member `403` for every id (role check first), so it reveals
  nothing about connected mailboxes.

## Not Tested, And Why

- **Live sync of new mail (B1/B2):** no supported way to feed mail into a running stack; covered by
  automated tests (B3). Recommendation in B3.
- **Domain receiving with a connected address (C10):** needs Cloudflare; organization domains are
  disabled on a local stack. Covered by `TestConnectedAddressesStayOutOfDomains`.
- **AI triage switched on by the owner:** no AI model configured (`409 MAILBOX.TRIAGE_UNAVAILABLE` for
  M1); managers get `404`.
- **Web page for the connection directory and forced disconnect:** none exists yet in `web/` or
  `admin/`; tested through the API only.
- **Mock BFF mode (D6):** verified by code and Vitest, not in a live mock-mode server.
- **Retention purge of agent rows with a connection's copies:** not waited for; automated
  (`TestAgentRowsNeverOutliveTheMailbox` exists).
- **Kubernetes bundle workers:** deliberately out of scope.
- **Real Gmail, Outlook, IMAP providers:** not allowed.

## Side Effects And Cleanup

- Everything ran in the database `zmail_accept2`, created for this run and dropped afterwards; the
  shared `fastbuild` and `zmail_test` databases were not written to by the stack (`make check` and the
  targeted Go tests use `zmail_test` as designed).
- Processes started (API, broadcast worker, `next dev` on 3055, one-off sync worker) were stopped.
- `pnpm install --frozen-lockfile` populated `node_modules` in this worktree only.
