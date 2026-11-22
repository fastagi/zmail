# Upgrading FastBuild

This guide lists changes that a downstream fork must review when it pulls a newer FastBuild. Each entry
has an impact level:

- **High**: the fork fails to build, or behavior changes, until it acts.
- **Medium**: builds and runs, but a documented rule changed; code that follows the old rule should
  be updated.
- **Low**: cleanup that only matters if the fork used the affected code.

Newest changes come first. Merge the upstream branch, run `make check`, then work through the
entries in order.

## Unreleased — Tenant connect

An organization `owner` authorizes a whole Google Workspace (service account with domain-wide
delegation) or Microsoft 365 tenant (application permissions by admin consent, or the organization's
own application) once; owners and admins assign mailboxes from its directory to members or as shared
connected mailboxes. Contract: `contracts/MAILBOXES.md`, Tenant Connections. Plan:
`docs/plans/tenant-connect.plan.md`.

### Medium — New routes and response fields

Six session-only routes under `/v1/external-mail-tenants`. `ExternalMailConnection` and
`ExternalMailConnectionDirectoryEntry` gain the required, nullable `tenant_id`; the connector
`capabilities` gain the required `tenant` boolean. A client with strict response schemas must accept
them. `GET /v1/external-mail-connections` also lists the caller's tenant assignments (at most 100)
between their own and the shared connections, so it may return up to 210 items. A directory entry of
a tenant assignment carries its full `account_address` and `mailbox_id`. No error code was added:
tenants reuse `MAILBOX.EXTERNAL_*`, `MAILBOX.ACCESS_INVALID`, and `MAILBOX.ACCESS_CONFLICT`.

### Medium — Owners and admins administer tenant-assigned personal mailboxes

`MailboxAccessFor` (and every listing that hides other members' connected personal mailboxes) now
hides only those a member bound with their own grant (`Mailbox.PrivateConnected`). A personal
mailbox assigned from a tenant is at the organization's address: owners and admins get `administer`
and `see_history` (never `view`), it appears in the mailbox directory, and when its owner leaves it
awaits handover instead of being revoked; the handover makes its connection the organization's.
Code that checked `Connected() && Type == personal` to mean "a member's own account" should use
`PrivateConnected()`.

### High — Roll every API instance forward before an owner starts a Microsoft admin consent

A pending admin consent is stored like a sign-in, with its tenant id sealed behind a `tenant:` marker
that an older instance cannot open: completing it there answers
`404 MAILBOX.EXTERNAL_AUTHORIZATION_NOT_FOUND` and the owner starts again. An older instance would also
try to sync a tenant connection with an empty account token, which fails as `needs_reconnect`; roll
every API and `mail:external:sync` instance forward before assigning mailboxes.

### Low — Migration `2026_12_08_090000_add_external_mail_tenants` and a new limit

The migration creates `external_mail_tenants` (a tenant identifier is live in one organization of the
instance), adds `external_mail_connections.tenant_id` and `tenant_directory_id` with a CHECK that a
tenant connection stores no account token, and `mailboxes.external_mail_tenant_id` with a CHECK that
only a connected mailbox has one; no table is rewritten. Its Down refuses while a connection or mailbox
comes from a tenant. `EXTERNAL_MAIL_MAX_TENANT_ASSIGNMENTS` (default 1000, 1 to 10000; a set but
invalid value stops startup) bounds the live assignments of one tenant. A Microsoft operator
application used for tenants needs the Graph application permissions `Mail.Read` and `User.Read.All`.

## Unreleased — Shared connected mailboxes

An `owner` or `admin` can bind a team account at an outside provider into a `shared` mailbox with
`origin` `connected` and assign members as for any shared mailbox; the connection belongs to the
organization. Contract: `contracts/MAILBOXES.md`, Shared Connected Mailboxes. Plan:
`docs/plans/shared-connected-mailboxes.plan.md`.

### Medium — New response fields and request options on the connector routes

`ExternalMailConnection` gains the required `shared` boolean, and `ExternalMailConnectionDirectoryEntry`
gains the required `shared`, `account_address`, and `mailbox_id` (both `null` for a member's own
account, filled for a shared connection, whose `owner_user_id` is `null`). A client with strict
response schemas must accept them. `POST /v1/external-mail-authorizations` and
`POST /v1/external-mail-credentials` accept an optional `sharing` object (and the credentials route an
optional `connection_id`); a request without them behaves as before. These routes can now answer
`403 PERMISSION.DENIED` and `422 MAILBOX.ACCESS_INVALID`. `GET /v1/external-mail-connections` also
lists the shared connections the caller manages after their own, so it may return up to 110 items,
and the sync-runs, purge, and unbind routes accept a shared connection from whoever manages its
mailbox's members. No error code was added. The credentials route's body limit rises from 4 KiB
to 16 KiB to carry 100 members.

### High — Roll every API instance forward before anyone binds a team account

A sign-in started as shared on this version must be completed by this version. An older instance
would ignore the stored `sharing`, so a shared sign-in seals its verifier behind a marker the older
code cannot open: such a completion answers `404 MAILBOX.EXTERNAL_AUTHORIZATION_NOT_FOUND` before the
code is exchanged, and the admin signs in again once every instance runs this version. Nothing is
misbound, but shared binding is unreliable until the rollout completes. Rolling back the migration
deletes pending shared sign-ins and refuses while a shared connection exists.

### Medium — Display names count characters; shared purges need an owner or admin

Every mailbox display name (`POST /v1/mailboxes`, renames, `sharing.display_name`) is now limited to
120 characters, not 120 bytes, matching the column and the contract: multi-byte names up to 120
characters that were refused before are accepted. Purging a revoked shared connection's copies at
once (`POST /v1/external-mail-connections/{connection_id}/purges`) deletes its mailbox, so it is
reserved to owners and admins; a `manage` member gets `403 PERMISSION.DENIED`.

### Low — Migration `2026_12_07_090000_add_shared_external_mail` and a new limit

The migration adds `external_mail_connections.shared` and `bound_by_user_id` (backfilled from the
owner), a CHECK that a shared connection has no owner, a partial index, and
`external_mail_authorizations.sharing`; constant defaults rewrite no table. Its Down refuses while a
shared connection exists. `EXTERNAL_MAIL_MAX_SHARED_CONNECTIONS` (default 20, 1 to 100; a set but
invalid value stops startup) bounds the live shared connections of one organization; add it to your
environment if you template it.

## Unreleased — Mail connector acceptance fixes

### Medium — Restoring a revoked connection counts toward the limit of 10

Binding an account again whose connection the member revoked makes that connection live, so it now
answers `409 MAILBOX.EXTERNAL_CONNECTION_LIMIT` (and hands the grant back) while the member already
holds 10 live connections, on both `POST /v1/external-mail-authorizations/completions` and
`POST /v1/external-mail-credentials`. Reconnecting a live connection is unchanged. Bindings of one
member now run one at a time, so two at once never both pass the limit.

### Low — An out-of-range `backfill_days` is `MAILBOX.EXTERNAL_AUTHORIZATION_INVALID`

`POST /v1/external-mail-authorizations` and `POST /v1/external-mail-credentials` answer a
`backfill_days` outside 1 to 90 with `422 MAILBOX.EXTERNAL_AUTHORIZATION_INVALID`, as the contract
states, instead of `422 COMMON.VALIDATION_FAILED`.

### Medium — Run `mail:external:sync` where connectors are on

Nothing ran the sync worker before. `make dev` now starts it when `EXTERNAL_MAIL_PROVIDERS` is set,
and `deploy/self-host` passes the `EXTERNAL_MAIL_*` settings and `MAILBOX_SECRET_ENCRYPTION_KEY` and
adds the `external-mail-worker` service in the `external-mail` Compose profile. A deployment of your
own adds the worker as `api/docs/DEPLOYMENT.md` describes.

## Unreleased — Mail agent grants and the agent mail feed

A person can let an API key (an agent) receive one mailbox's new mail through a replayable feed,
read those messages (masked unless the grant says otherwise), and store reply drafts for review.
Contract: `contracts/MAILBOXES.md`, Agent Grants; `contracts/API_KEYS.md`. Plan:
`docs/plans/mail-agent-grants.plan.md`.

### Medium — New `mail:agent` scope, agent routes, and access event types

Three API-key routes need the new `mail:agent` scope: `GET /v1/agent-mail-events`,
`GET /v1/agent-mail-messages/{message_id}`, and `POST /v1/agent-mail-drafts`. A key with `*` holds
it too, but still reaches no mailbox without a grant. Five session routes manage grants and drafts
under `/v1/mailboxes/{mailbox_id}/agent-grants` and `/v1/mailboxes/{mailbox_id}/agent-drafts`, with
four new error codes (`MAILBOX.AGENT_GRANT_NOT_FOUND`, `MAILBOX.AGENT_GRANT_INVALID`,
`MAILBOX.AGENT_GRANT_LIMIT`, `MAILBOX.AGENT_GRANT_EXISTS`). The mailbox access history gains the
types `agent_granted` and `agent_revoked`; a client with a strict enum must accept them. Removing a
shared mailbox member, handing over a personal mailbox, a member leaving the organization, and
disconnecting a connected mailbox's account now also revoke the agent grants they affect (and delete
the mailbox's drafts where its mail becomes unreadable), in the same transaction. Deleting a mailbox,
including the end of a revoked connection's purge, deletes its agent grants, events, drafts, and
audit rows. A connected mailbox accepts read-only grants: `draft` is refused.

### Low — Migration `2026_12_05_090000_add_mail_agent_grants` and a prune command

The migration only creates `mail_agent_grants`, `mail_agent_events`, `mail_agent_audit`, and
`mail_agent_drafts`; it locks no existing table. Every newly stored inbound message reads the
mailbox's live grants (a partial index keeps this one cheap lookup) and, only when one matches,
writes its events under a per-organization transaction lock. Every
`mail:broadcast:work` pass deletes feed events older than `MAIL_AGENT_EVENT_RETENTION` (default
`720h`) and agent audit rows older than `MAIL_AGENT_AUDIT_RETENTION` (default `2160h`) in small
batches; `mail:agent-events:prune [--before=<RFC3339>]` deletes old events at once.
`MAIL_AGENT_READS_PER_MINUTE` (default 600) limits each key's agent requests. The mailbox
repository reads `api_keys` (never writes it) to check a key when a grant is issued and when it
decides which grants still receive mail.

## Unreleased — Mailbox origin: connected mailboxes and manager connection controls

A mailbox now has an `origin` (`zmail` or `connected`) beside its `type` (`personal` or `shared`).
The `external` type is gone: every external mailbox becomes `personal` + `connected`. Contract:
`contracts/MAILBOXES.md`, Access and Connection Directory. Plan: `docs/plans/mailbox-origin.plan.md`.

### High — `type` loses `external`; `Mailbox.origin` is required

`domain.MailboxTypeExternal` is removed; check `Mailbox.Connected()` (or `Origin ==
domain.MailboxOriginConnected`) instead. The HTTP `Mailbox` gains a required `origin`, and
`MailboxType` no longer lists `external`. A client that branched on `type === 'external'` must test
`origin === 'connected'`. The mailbox access history has five new event types (`connection_bound`,
`connection_reconnected`, `connection_revoked`, `connection_force_disconnected`,
`connection_revoked_on_leave`); a client with a strict enum must accept them.

### Medium — Managers no longer see members' connected accounts

The owner and admin roles grant nothing on a connected personal mailbox: it disappears from their
mailbox lists, the mailbox directory and its `mailbox_count`, the export, and the unified inbox, and
its routes answer them `404` (previously they saw it locked with `administer`). The organization's
mailbox limit counts `origin = 'zmail'` only. Managers use the new
`GET /v1/external-mail-connection-directory` (masked addresses) and
`DELETE /v1/external-mail-connection-directory/{connection_id}` (forced disconnect). A connected
mailbox never sends, never gets `organize`, never awaits a handover, and the handover routes refuse
it. `domain.ExternalMailRepository` gains `ListExternalMailConnectionDirectory`.

### Medium — Connected accounts: ownership, revoke side effects, and audit

Within one organization and connector an outside account belongs to one member while their
connection is live; a `revoked` connection of another member no longer blocks a new binder, whose
binding purges the old copies and mailbox at once (`copies_purged`, reason `rebound`). Any revoke
turns the connected mailbox's AI triage off and drops its queued messages, and triage skips a
connected mailbox whose connection is not active. Deleting a connected mailbox revokes its
connection first. A forced disconnect is also written to the organization audit log. Domain
routing, domain deletion, and operator mailbox counts ignore connected mailboxes.
`domain.MailboxRepository.ListMailboxes` and `domain.MailInboxRepository.ListMailboxAccessRows`
take the viewer's user id and drop other members' connected personal mailboxes in SQL;
`domain.MailTriageRepository` gains `StopMailboxTriage` and `domain.ExternalMailRepository` gains
`FindExternalMailConnectionByMailbox`. `CreateMailbox` ignores the limit for a connected mailbox.

### Low — Migration `2026_12_04_090000_add_mailbox_origin`

Adds `mailboxes.origin varchar(16) NOT NULL DEFAULT 'zmail'` (catalog only, no rewrite) with
`chk_mailboxes_origin`, converts `external` rows (soft-deleted ones included), restores
`chk_mailboxes_type` to `personal` and `shared`, and rebuilds `idx_mailboxes_address` as
`WHERE origin = 'zmail'`. mailboxes is bounded per organization, so the scans are short. Rolling back
turns connected personal mailboxes back into external ones and fails while a connected shared
mailbox exists.
## Unreleased — Mail connectors: sync runs, backoff, provider budget, and retention

The connector sync is now operable. Contract: `contracts/MAILBOXES.md`, External Mailboxes (Sync
Budget, Sync Runs, Retention Of Revoked Copies). Migration `2026_12_06_090000_add_external_mail_sync_ops`.

### High — Revoked external copies are deleted after 30 days

Revoking a connection (by its member, a manager, or a member leaving) now sets `revoked_at`, and the
mail worker (`mail:broadcast:work`) deletes the copies, the external mailbox, and the connection once
`EXTERNAL_MAIL_REVOKED_RETENTION` (default `720h`, at least `24h`, `90d` accepted; a value that does not
parse stops startup) has passed. The deletion runs there, not in `mail:external:sync`, whether or not
`EXTERNAL_MAIL_PROVIDERS` is set; `MailBroadcastDispatcher` gains
`PurgeRevokedExternalMail`. Connections revoked before the upgrade
start their retention at the migration, so their copies are deleted one retention after the upgrade;
raise the retention to keep them longer. Binding the
account again before the deadline cancels the deletion. `POST
/v1/external-mail-connections/:connection_id/purges` lets the member delete them at once
(`409 MAILBOX.EXTERNAL_CONNECTION_NOT_REVOKED` while the connection is bound). Each purge stores a
`copies_purged` access event on the deleted mailbox as an audit record; it is never listed.

### Medium — Failed fetches back off; passes are budgeted per provider

An `external.fetch_failed` fetch now retries after 2, 4, 8 ... minutes, at most an hour, with jitter,
instead of every two minutes. Each fetch has a deadline, `EXTERNAL_MAIL_FETCH_TIMEOUT` (default
`60s`); a fetch that hits it is `fetch_failed`. A pass takes due connections round-robin across
providers, at most `EXTERNAL_MAIL_PROVIDER_FETCHES_PER_PASS` (default 20) per provider. A connection
with more pages is fetched again at once for at most `EXTERNAL_MAIL_MAX_IMMEDIATE_PAGES` (default 20)
pages in a row, then waits `EXTERNAL_MAIL_MIN_FETCH_SPACING` (default `5s`) before each further page.
An `ExternalMailSource` must honor its context: a fetch that ignores cancellation still holds the pass
until it returns. `ExternalMailRepository.ClaimExternalMailConnections` takes a
`domain.ExternalMailClaim`, and the repository gains the `ExternalMailOpsRepository` methods; a fork
with its own implementation must add them.

### Low — Sync runs and metrics

Each fetch writes a row to `external_mail_sync_runs` (latest 50 per connection, 14 days), readable by
the connection's member through `GET /v1/external-mail-connections/:connection_id/sync-runs`. The
`external_mail.synced` and `external_mail.sync_failed` log events gain `outcome`, `latency_ms`,
`page_size`, and `more`, and Prometheus gains `zmail_external_mail_syncs_total{provider,outcome}`,
`zmail_external_mail_sync_seconds{provider}`, and `zmail_external_mail_copies_purged_total`.
## Unreleased — Mail connectors: binding, reconnecting, and open provider ids

External mailboxes can now be bound and reconnected through a provider-neutral connector registry,
and synced mail goes through the same post-store step as Zmail's own inbound mail. Contract:
`contracts/MAILBOXES.md`, External Mailboxes. Plan: `docs/plans/mail-connectors.plan.md`.

### Low — Outlook connector

`EXTERNAL_MAIL_PROVIDERS=outlook` with an Outlook OAuth client now syncs Outlook.com and
Microsoft 365 Inboxes through Microsoft Graph delta. The app registration needs the delegated
Graph permissions `Mail.Read`, `User.Read`, `offline_access`, `openid`, and `email`, and
`EXTERNAL_MAIL_REDIRECT_URL` as a Web redirect URI. A fork with its own Outlook source should
remove it or give its connector another id; the registry keeps the first connector per id.

### Medium — `source` and `provider` are connector ids, not a fixed list

`source` on message summaries and `provider` on connections were `gmail` or `outlook`; they are now
any id matching `^[a-z][a-z0-9_]{1,15}$` (`zmail` stays Zmail's own mail). A client with a strict
enum must accept any such id and render one it does not know generically. Three routes are new:
`GET /v1/external-mail-connectors`, `POST /v1/external-mail-authorizations`, and
`POST /v1/external-mail-authorizations/completions`, with seven `MAILBOX.EXTERNAL_*` error codes.
`EXTERNAL_MAIL_REDIRECT_URL` names the Web callback page.

### Medium — External mailboxes leave the delivery namespace; triage consent is the owner's

`FindMailboxByAddress` and the address directory ignore `external` mailboxes, so inbound delivery,
sending from an address, and address availability never resolve one. Only the owner of an
`external` mailbox may switch AI triage on for it, as for a personal mailbox; previously an
organization manager could. A fork that implements `ExternalMailSource` must adopt the new
`AuthURL(ExternalMailAuthRequest)` and `Exchange(ctx, ExternalMailCodeExchange)` signatures, which
carry the PKCE challenge and verifier.

### Medium — `ExternalMailSource.Fetch` takes a request and may page, rotate, or expire

`Fetch(ctx, ExternalMailFetchRequest)` replaces `Fetch(ctx, token, cursor)`. The request carries
the account address, the backfill window, and the sync time. The result may set `More` (the
connection is fetched again at once), `RefreshToken` (a rotated grant, saved with the page), and a
source may answer `ErrExternalMailCursorExpired` (the sync restarts from an empty cursor). Real
connectors register themselves with `registerExternalMailConnector` from their own file.
`EXTERNAL_MAIL_PROVIDERS` now accepts `gmail`, `outlook`, and `imap`; the OAuth connectors need
`EXTERNAL_MAIL_<PROVIDER>_CLIENT_ID`, `_CLIENT_SECRET`, and `EXTERNAL_MAIL_REDIRECT_URL`. Listing a
provider whose adapter is not compiled in enables nothing for it.

### Low — Migration `2026_12_03_090000_open_mail_connectors`

Replaces the provider and source CHECKs with the connector pattern (the source CHECK is added
`NOT VALID`, no scan), rebuilds `idx_mailboxes_address` as a partial index that skips external
mailboxes (mailboxes is bounded per organization), and creates `external_mail_authorizations`.
Rolling back fails while a connector other than `gmail` or `outlook`, or two external mailboxes with
one address, exist.

### Low — The Gmail connector is real

`EXTERNAL_MAIL_PROVIDERS=gmail` with `EXTERNAL_MAIL_GMAIL_CLIENT_ID` and
`EXTERNAL_MAIL_GMAIL_CLIENT_SECRET` now binds Gmail accounts through Google's consent page (scope
`gmail.readonly`, offline access) and syncs them over the Gmail API: a backfill page by page, then
history from the historyId recorded when the backfill started. Its Google OAuth application must list
`EXTERNAL_MAIL_REDIRECT_URL`. Only inbound mail is copied: drafts, spam, trash, and sent mail that is
not also in the inbox are skipped. Connections bound with `fake` keep a cursor the connector cannot
read; their first Gmail fetch starts the backfill over, without copies.

## Unreleased — Platform mail operations: broadcast content and its audit

Operators can read the sent text of organization broadcast messages, with a reason and an access
record stored first. Contract: `contracts/OPERATORS.md`, Broadcast Content. Plan:
`docs/plans/admin-mail-content.plan.md`.

### Medium — A content add-on on operator grants; no existing grant reads bodies

`platform_operators` gains `mail_content` (default `false`). It adds the new capability
`mail_content:read` to either scope; `full` does not include it. Grant it with
`fastbuild operator:grant <email> --scope=full,mail-content` or `--scope=mail-metrics,mail-content`.
A grant command now sets the whole grant, so re-running `operator:grant <email>` without
`,mail-content` removes content from a grant that had it. `operator:list` adds a CONTENT column;
grant audit metadata adds `mail_content`.

`GET`/`POST /v1/operator/session` may list `mail_content:read`. New routes (with the `mailbox` and
`organization` starters): `POST /v1/operator/outbound-messages/{organization_id}/{message_id}/content`
and `GET /v1/operator/content-access-audits`. Outbound log rows add `broadcast_id` (nullable). New
error code `OPERATOR.CONTENT_NOT_ELIGIBLE` (403); clients that switch exhaustively on `error_code`
should add it.

A fork that calls `domain.OperatorGrantStore.GrantOperator` now passes a `domain.OperatorAccess`
(scope plus content flag) instead of a `domain.OperatorScope`.

### Low — Operator content migrations

Run migrations:

- `2026_12_01_090000_add_platform_operator_mail_content` adds
  `platform_operators.mail_content boolean NOT NULL DEFAULT false`. The constant default is
  recorded in the catalog only (PostgreSQL 11+), so the table is not rewritten; the `ALTER` takes a
  brief `ACCESS EXCLUSIVE` lock on the grants table (a handful of rows) that blocks operator
  sign-in and operator requests for that moment only. Rolling back drops the column, which only
  removes content from grants.
- `2026_12_01_090100_create_mail_content_access_audits` creates `mail_content_access_audits` and
  its indexes. It only creates a new table and locks no existing one. Rolling back drops the table
  and every access record in it; export them first if they must be kept.

## Unreleased — Hot-path benchmarks and two query-plan fixes

Benchmarks for the unified inbox triage filter, external sources, operator mail metrics, platform
accounts, and registration codes, with the measured effect of the fixes below:
`docs/benchmarks/v2-hot-paths.md`.

### Low — Outbound time index for platform mail metrics, and building it beforehand

Run migrations: `2026_11_30_090000_add_mail_outbound_time_index` adds
`idx_mail_messages_outbound_time`, which the operator mail overview, per-organization counts, and
outbound log read instead of scanning all of `mail_messages`. Building it holds the write lock on
`mail_messages`; on a large installation build it first, outside a transaction, as a role that
owns `mail_messages`, and the migration then skips it:

```sql
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_mail_messages_outbound_time
  ON mail_messages (created_at, id) INCLUDE (mailbox_id, status, source)
  WHERE direction = 'outbound';
```

If the `CONCURRENTLY` build fails, drop the invalid index
(`DROP INDEX CONCURRENTLY idx_mail_messages_outbound_time;`) and build it again before migrating.
Down drops the index and loses nothing. Because the index includes `source`, rolling back
`2026_11_28_090000_add_external_mail` needs this migration rolled back first, as the migrator's
reverse order already does.

The unified inbox triage filter (`triage=`) now reads each mailbox's results from the existing
`idx_mail_triage_results`; no schema change. A fork that changed `inboxFilters` should move its
triage condition to `inboxTriageFilter`.

## Unreleased — Platform mail operations (metrics and outbound log)

Read-only platform mail metrics for the Admin Console, behind a new operator grant scope.
Contract: `contracts/OPERATORS.md`, Operators and Mail Metrics. Plan:
`docs/plans/admin-mail-operations.plan.md`.

### Medium — Operator grants have a scope; organization reads accept metrics-only grants

`platform_operators` gains `scope` (`full` or `mail_metrics`). Every existing grant becomes `full`
and keeps exactly what it allowed. `fastbuild operator:grant <email>` still grants `full`;
`--scope=mail-metrics` grants only the read-only mail metrics, the outbound log, and the operator
organization reads (`GET /v1/operator/organizations`, `/:id`, `/:id/members`). Granting another
scope to an existing operator changes the scope. `operator:list` adds a SCOPE column.

`GET`/`POST /v1/operator/session` add `capabilities`. A session whose grant does not cover a route
gets `403 OPERATOR.CAPABILITY_REQUIRED` (new code) instead of `OPERATOR.FORBIDDEN`; a client that
signs out on `OPERATOR.FORBIDDEN` should stay signed in on the new code. A fork that calls
`domain.OperatorGrantStore.GrantOperator` must pass a scope and read a `domain.OperatorGrantChange`.

New routes (with the `mailbox` and `organization` starters): `GET /v1/operator/mail-metrics`,
`GET /v1/operator/mail-metrics/organizations`, `GET /v1/operator/outbound-messages`. They never
return message bodies.

### Low — Operator scope migration

Run migrations: `2026_11_29_090000_add_platform_operator_scope` adds
`platform_operators.scope varchar(16) NOT NULL DEFAULT 'full'` and the CHECK
`platform_operators_scope_check`. The constant default is recorded in the catalog only (PostgreSQL
11+), so the table is not rewritten; adding the CHECK scans the grants table, which holds a handful
of rows, under a brief `ACCESS EXCLUSIVE` lock that blocks operator sign-in and operator requests
for that moment only. No other table is locked. Rolling back deletes every `mail_metrics` grant
first, because without the column it would read as a full grant; re-grant them after rolling forward.

## Unreleased — External mailboxes (offline part)

Read-only external mailboxes: the data model, the sync worker, message `source`, and two connection
routes. Binding (the Gmail and Outlook OAuth flow) does not exist yet, and nothing changes until an
operator sets `EXTERNAL_MAIL_PROVIDERS`. Contract: `contracts/MAILBOXES.md`, External Mailboxes.

### Medium — Message summaries carry `source`; mailboxes may be `external`

Every message summary (mailbox listings, the log, unified inbox rows, the detail) gains a required
`source`: `zmail`, `gmail`, or `outlook`. Existing mail is `zmail`. `Mailbox.type` and directory rows
may now be `external`; create, directory filters, and export still accept `personal` and `shared`
only. A client with a strict schema must accept both. A fork that builds `domain.MailboxAccessFor`
results by hand should note that an `external` mailbox gives its owner `view` and `see_history` only,
and that `send()` refuses one whatever the access says.

New error code: `MAILBOX.EXTERNAL_CONNECTION_NOT_FOUND` (`404`), which both
`/v1/external-mail-connections` routes answer while `EXTERNAL_MAIL_PROVIDERS` is empty.

### Low — External mail migration, and building its index beforehand

Run migrations: `2026_11_28_090000_add_external_mail` allows `mailboxes.type = 'external'`, adds
`mail_messages.source varchar(16) NOT NULL DEFAULT 'zmail'` and nullable `mail_messages.external_id`,
creates the partial unique index `idx_mail_messages_external (mailbox_id, external_id) WHERE
external_id IS NOT NULL`, and creates `external_mail_connections`. What it locks:

- `mail_messages` columns: a constant default and a nullable column are recorded in the catalog only
  (PostgreSQL 11+, including 18), so the table is not rewritten; the `ACCESS EXCLUSIVE` lock is held
  only for that catalog change. The `source` CHECK is added `NOT VALID`, so no row is scanned; every
  existing row takes the default `zmail`.
- `mailboxes`: the type CHECK is replaced, which scans `mailboxes` under its lock; the table is small
  (bounded per organization).
- The unique index reads all of `mail_messages` while holding the write lock. A large installation
  builds it first, outside a transaction, then migrates; `IF NOT EXISTS` makes the migration skip it:

```sql
ALTER TABLE mail_messages ADD COLUMN IF NOT EXISTS external_id varchar(255);
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_mail_messages_external
  ON mail_messages (mailbox_id, external_id) WHERE external_id IS NOT NULL;
```

If the `CONCURRENTLY` build fails, drop the invalid index
(`DROP INDEX CONCURRENTLY idx_mail_messages_external;`) and build it again before migrating.
Rolling the migration back fails while any `external` mailbox row exists (including soft-deleted
ones); delete those rows first.

### Low — New setting and worker, off by default

`EXTERNAL_MAIL_PROVIDERS` (default empty) lists the enabled sources. Only `fake` exists yet: it
serves scripted mail from memory for tests and local demos, and `APP_ENV=production` refuses to
start with it; `gmail` and `outlook` are refused until their sources exist. A non-empty list needs
`MAILBOX_SECRET_ENCRYPTION_KEY` (32+ characters), which seals refresh tokens. The new command
`mail:external:sync [--batch=20] [--poll=30s] [--once]` polls each active connection at most every
two minutes; with the list empty it logs `external_mail.worker_disabled` and exits 0, so it is not
added to Compose yet.

## Unreleased — Registration email codes

Self-registration now proves that the person reads the email address: `POST /v1/register/email-codes`
mails a 6-digit code, and `POST /v1/register` takes it as `email_code`. Contract:
`contracts/AUTHENTICATION.md`, Registration Email Codes.

### High — Registration requires an email code and a working system mailer by default

`AUTH_REGISTRATION_EMAIL_CODE_REQUIRED` defaults to `true`. With registration open, a client that
calls `POST /v1/register` without `email_code` now gets `400 AUTH.REGISTRATION_EMAIL_CODE_REQUIRED`,
and the code only arrives through the system mailer (`MAIL_PROVIDER`, `MAIL_FROM`, and its key). An
open instance without a mailer can register nobody; it logs `user.registration_email_code_mailer_missing`
at startup. Default `true` is the safe choice: open registration without proof of email lets anyone
claim any address. Act on one of:

- Configure the system mailer and update every registration client to request a code first. The
  Web console does this already; its `POST /api/auth/register` now requires `code`.
- Set `AUTH_REGISTRATION_EMAIL_CODE_REQUIRED=false` where no mail can be sent, such as local
  development, test harnesses, and processes that create accounts out of band.
  `deploy/self-host/create-user.sh` and `api/tests/kest/run_local.sh` already do.

New error codes: `AUTH.REGISTRATION_EMAIL_CODE_REQUIRED`, `AUTH.REGISTRATION_EMAIL_CODE_INVALID`,
and `AUTH.REGISTRATION_EMAIL_CODE_EXPIRED` (all `400`). Production adds the
`AUTH_RATE_LIMIT_REGISTER_EMAIL_CODE_*` quotas (10 per IP and 5 per address per hour by default).
A fork that implements `user.UserMailer` or `user.AuthService` adds `SendRegistrationCodeEmail` and
`RequestRegistrationEmailCode`.

### Low — One new table for registration codes

Run migrations: `2026_11_27_090000_create_registration_email_codes` creates
`registration_email_codes`. It only creates a new table and its index, so it locks nothing
existing.

## Unreleased — AI triage (offline part)

### Low — Two new tables and a mailbox switch for AI triage

Run migrations: `2026_11_26_090000_add_mail_triage` creates `mail_triage` (one row per inbound
message queued for or given a triage result) and `mail_triage_settings` (one row per organization),
and adds `mailboxes.ai_triage_enabled boolean NOT NULL DEFAULT false`. The new tables lock nothing
existing; the column has a constant default, which PostgreSQL 11+ records in the catalog only, so
`mailboxes` is not rewritten. Rolling back `2026_11_22_090000_add_mail_roles` now needs this
migration rolled back first.

Nothing changes until an operator opts in: `AI_TRIAGE_PROVIDER` defaults to `disabled`, and every
organization and mailbox switch defaults off. `AI_TRIAGE_PROVIDER=fake` is for tests and local
demos and refuses to start with `APP_ENV=production`. Results are written by a new worker,
`fastbuild mail:triage:work --batch=20 --poll=5s`; run it only once a provider is configured.

## Unreleased — Shared mailbox administration

Organization owners and admins get an admin area in the Web console with a shared-mailbox page:
`GET /v1/mailbox-directory`, `PUT /v1/mailboxes/:mailbox_id` (rename), `POST /v1/mailbox-imports`,
and `GET /v1/mailbox-exports`. Contract: `contracts/MAILBOXES.md`, Mailbox Directory and Import And
Export.

### Medium — The per-organization mailbox limit is configurable

`MAILBOX_MAX_PER_ORGANIZATION` (default `50`, valid `1` to `5000`) replaces the fixed limit of 50;
anything outside the range fails startup. `domain.MaxMailboxesPerOrganization` is gone: a fork that
bounded a query by it uses `domain.MailboxesPerOrganizationCeiling` (5000), and a fork that checked
the limit reads `Service.MaxMailboxes()`. Lowering the value below an organization's count keeps its
mailboxes; only new creates and import rows answer `409 MAILBOX.LIMIT_REACHED`.
`GET /v1/mailboxes` may now return up to 5000 items, and `scope=usable` narrows it to the caller's own
mailboxes; the Web workspace uses that scope.

### Medium — The migration locks `mail_messages` while it builds the directory index

Run migrations: `2026_11_25_090000_add_mailbox_directory_index` adds
`idx_mail_messages_mailbox_send_attempted`, a partial B-tree index on
`(mailbox_id, send_lane, send_attempted_at)` for outbound mail, which the directory's per-mailbox send
counts read. It is built inside the migration's transaction, so on a large `mail_messages` table it
blocks inbound delivery and sends until the build ends.

A large installation builds the index beforehand, without blocking writes, and the migration then
skips it. Run this before the migration, outside a transaction, as a role that owns `mail_messages`:

```sql
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_mail_messages_mailbox_send_attempted
  ON mail_messages (mailbox_id, send_lane, send_attempted_at)
  WHERE direction = 'outbound';
```

If the `CONCURRENTLY` build fails, drop the invalid index
(`DROP INDEX CONCURRENTLY idx_mail_messages_mailbox_send_attempted;`) and build it again before
migrating. Down drops the index and loses nothing.

## Unreleased — Platform account ledger

### Low — Two new tables for platform accounts

Run migrations: `2026_11_25_120000_create_platform_accounts` creates `platform_accounts` and
`platform_account_events` for the platform account ledger and owner handover
(`/v1/platform-accounts`, `/v1/platform-account-handovers`; see `contracts/MAILBOXES.md`, Platform
Accounts). It only creates new tables and their indexes, so it takes no lock on an existing table
and needs no preparation on a large installation. Its foreign keys reference `organizations`,
`mail_roles`, `mailboxes`, and `users`; rolling back `2026_11_22_090000_add_mail_roles` now needs
this migration rolled back first.

## Unreleased — Unread count index

### Medium — The migration locks `mail_messages` while it builds the unread index

Run migrations: `2026_11_24_090000_add_mail_unread_index` adds `idx_mail_messages_unread`, a partial
B-tree index on `(mailbox_id, id) INCLUDE (created_at)` for inbound Inbox mail. Unread counts (the
mailbox list and the unified inbox sidebar) read it instead of scanning all of `mail_messages`, so
they no longer slow down as other mailboxes and organizations grow. It is built inside the
migration's transaction, so on a large `mail_messages` table it blocks inbound delivery and sends
until the build ends.

A large installation builds the index beforehand, without blocking writes, and the migration then
skips it. Run this before the migration, outside a transaction, as a role that owns `mail_messages`:

```sql
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_mail_messages_unread
  ON mail_messages (mailbox_id, id) INCLUDE (created_at)
  WHERE direction = 'inbound' AND placement = 'default';
```

If the `CONCURRENTLY` build fails, drop the invalid index
(`DROP INDEX CONCURRENTLY idx_mail_messages_unread;`) and build it again before migrating.

## Unreleased — Transactional mail

Backend services can send one-off messages such as verification codes and password resets through
`POST /v1/transactional-messages`, from transactional templates, with live and test API keys,
simulator addresses, a suppression list, and a separate send lane. The feature is off until
`MAILBOX_TRANSACTIONAL_ENABLED=true`. Contract: `contracts/MAILBOXES.md`, Transactional Mail.

### High — Five migrations, three with a rollback cost

Run migrations. They apply in this order; roll back in reverse:

| Migration | Change | Rollback (Down) cost |
|---|---|---|
| `2026_11_23_080000_add_send_lanes` | Adds `mail_messages.send_lane`, marks every message a broadcast recipient points at as `broadcast`, and replaces `idx_mail_messages_send_attempted` with `idx_mail_messages_lane_attempted` | None: drops the column and restores the old index; older code counts one shared limit again |
| `2026_11_23_090000_add_api_key_mode` (apikey starter) | Adds `api_keys.mode` (`varchar(8)`, default `live`, checked to `live` or `test`); existing keys become `live` | **Test keys are revoked.** Down runs `UPDATE api_keys SET revoked_at = now(), updated_at = now() WHERE mode = 'test' AND revoked_at IS NULL` before dropping the column, because older code treats every key as live and a surviving test key would send real mail. Re-applying Up does not restore them; issue new test keys |
| `2026_11_23_100000_add_transactional_templates` | Adds `kind`, `template_key`, `from_address`, `from_name`, and `variable_definitions` to `mail_templates`, the unique index `mail_templates_key_idx`, and a check tying the fields to the kind; existing templates become `standard` | **Transactional templates are deleted**: Down runs `DELETE FROM mail_templates WHERE kind = 'transactional'` before dropping the columns |
| `2026_11_23_110000_create_transactional_messages` | Adds `transactional_messages` and `transactional_message_events` | **Up to 30 days of transactional records are lost**: Down drops both tables |
| `2026_11_23_120000_create_mail_suppressions` | Adds `mail_suppressions` and fills it from contacts suppressed with reason `bounced` or `complained` (lowercased, source `backfill`) | The suppression list is lost; contacts keep their own status |

### High — New configuration; the encryption key needs its own handling

| Variable | Default | Meaning |
|---|---|---|
| `MAILBOX_TRANSACTIONAL_ENABLED` | `false` | Accept transactional messages; when `false`, new requests answer `503 MAILBOX.TRANSACTIONAL_DISABLED` and the worker still sends stored ones |
| `MAILBOX_SECRET_ENCRYPTION_KEY` | empty | At least 32 characters, required when enabled or startup fails. Encrypts content while a message waits and keys the idempotency fingerprints. Same value on every API replica and worker; keep it apart from database backups |
| `MAILBOX_DAILY_BROADCAST_LIMIT`, `MAILBOX_DAILY_TRANSACTIONAL_LIMIT` | value of `MAILBOX_DAILY_SEND_LIMIT` | Per-organization 24-hour lane limits, 1 to 100000 (see the send lanes entry below) |
| `MAILBOX_SIMULATOR_DOMAIN` | `test.zmail.im` | `delivered@`, `bounced@`, `soft-bounced@`, `complained@`, and `suppressed@` (optional `+tag`) on it are simulated, never sent |
| `MAILBOX_TRANSACTIONAL_RECIPIENT_PER_MINUTE` | `3` | Sends of one template to one recipient per organization and mode per minute (1 to 1000) |
| `MAILBOX_TRANSACTIONAL_KEY_PER_SECOND` | `20` | Sends per API key per second (1 to 10000) |

The rate limits use the shared rate-limit store: with more than one API replica set
`RATE_LIMIT_STORE=postgres`. Run `fastbuild mail:broadcast:work` wherever the API runs; it also
retries, expires, and purges transactional messages.

To rotate `MAILBOX_SECRET_ENCRYPTION_KEY`: (1) set `MAILBOX_TRANSACTIONAL_ENABLED=false` and roll out,
keeping the old key so the worker can drain; (2) wait until
`SELECT count(*) FROM transactional_messages WHERE status = 'queued'` is 0 (lane waits expire within
an hour); (3) set the new key everywhere, re-enable, and roll out. A message still queued under the
old key fails with `send.payload_unavailable`, and for 24 hours a request repeating an
`Idempotency-Key` from before the rotation answers `409 MAILBOX.IDEMPOTENCY_CONFLICT`. Details:
`api/docs/DEPLOYMENT.md`, Transactional Mail.

### Medium — The send limit is split into three lanes

The single per-organization send limit is replaced by three lanes, each counted separately over a
rolling 24 hours by provider-call start, failed attempts included:

- `personal` (`MAILBOX_DAILY_SEND_LIMIT`, default `200`): console sends, replies,
  `POST /v1/outbound-mail-messages`, and template test sends. Full → the message waits, as before.
- `broadcast` (`MAILBOX_DAILY_BROADCAST_LIMIT`): each broadcast recipient. Full → the broadcast
  pauses with `stop_reason` `send_limit_reached`, as before.
- `transactional` (`MAILBOX_DAILY_TRANSACTIONAL_LIMIT`): live, non-simulated transactional
  messages. Full → the message waits with `queued_reason` `send_limit`; once as many wait as the
  lane allows, `429 MAILBOX.SEND_LIMIT_REACHED`; a message waiting more than 1 hour fails with
  `send.expired`.

The two new limits take the value of `MAILBOX_DAILY_SEND_LIMIT` when unset or `0`. The defaults
keep the personal and broadcast lanes at least as large as the shared limit was, so no organization
gets less room. Because lanes no longer share the room, an organization's theoretical maximum per
24 hours is the sum of the three (triple the old limit with the defaults): set
`MAILBOX_DAILY_BROADCAST_LIMIT` and `MAILBOX_DAILY_TRANSACTIONAL_LIMIT` so the total stays within
the Cloudflare account's sending limit.

`GET /v1/mailbox-domains` adds `daily_broadcast_limit` and `daily_transactional_limit`;
`daily_send_limit` now means the personal lane. In Go, `domain.MailboxRepository.CountOutboundSince`
takes a `domain.SendLane`, and `LeaseWaitingSends` counts only personal attempts.

### Medium — API keys are live or test; scoped routes refuse test keys

Every API key has an immutable `mode`: `live` (the default) or `test`. `POST /v1/api-keys`
accepts an optional `mode`; key responses add `mode`, so clients that validate responses strictly
must accept it. Test keys are issued with the `fastbuild_test_` prefix and never send mail.
`apikey.RequireScopes` now also answers `403 PERMISSION.DENIED` for a valid test key; a route that
supports test keys declares it with `apikey.RequireScopesAllowingTest` and reads the mode with
`apikey.APIKeyModeFromContext`. Fork routes that authenticate API keys without `RequireScopes`
must check the mode themselves. `domain.APIKey` gains `Mode`. The new scope `mail:transactional`
neither includes nor is included by `mail:send`; `*` grants both.

### Medium — Templates have a kind; template writes take the viewer

Templates gain `kind` (`standard`, the default, or `transactional`). A transactional template has
a `key` unique in its organization, a sender (`from_address` on a verified domain of the
organization, or the instance `MAILBOX_DOMAIN` with the `log` provider, and an optional
`from_name`), and `variable_definitions`. Only owners and admins write transactional templates.
Mailbox sends, template tests, and broadcasts (create, update, send) refuse a transactional
template with `422 MAILBOX.TEMPLATE_KIND_MISMATCH`; `DELETE /v1/mail-domains/{id}` answers
`409 MAILBOX.DOMAIN_IN_USE` while a transactional template sends from the domain.
`GET /v1/mail-templates` accepts `?kind=`. Template responses add `kind`, `key`, `from`, and
`variable_definitions`; clients that validate responses strictly must accept them. In Go,
`Service.CreateTemplate`, `UpdateTemplate`, and `DeleteTemplate` take a
`domain.OrganizationContext` instead of IDs; `ListTemplates` and
`domain.MailTemplateRepository.ListTemplates` take a `domain.MailTemplateKind` (empty lists all);
the repository adds `FindTemplateByKey`.

### Medium — Deploy the API before the web app

The web app requires `mode` on every API key; `kind`, `key`, `from`, and `variable_definitions` on
every template; and `daily_broadcast_limit`, `daily_transactional_limit`, `transactional_enabled`,
`transactional_recipient_limit_per_minute`, `transactional_key_limit_per_second`, and
`simulator_domain` on every `GET /v1/mailbox-domains` item. It validates these responses strictly,
so every mail page (and the API key and developer pages) fails to load against an API that does
not send them yet.

### Low — New routes, webhook events, and repository seams

New routes: `POST /v1/transactional-messages` and `GET /v1/transactional-messages/{id}` (API key,
`mail:transactional`, test keys accepted), `GET /v1/transactional-mail-log`,
`GET /v1/transactional-mail-log/{id}`, `POST /v1/transactional-test-sends`,
`GET /v1/mail-send-lanes`, and `GET`/`DELETE /v1/mail-suppressions`. Webhook subscribers can receive
the transactional `email.*` events listed in `contracts/MAILBOXES.md`. In Go, the mailbox repository
implements the new `domain.TransactionalMailRepository`. Observability adds the
`transactional_message.*` log events and `zmail_transactional_*` metrics listed in
`api/docs/OBSERVABILITY.md`.

## Unreleased — Unified inbox

### Medium — The migration locks `mail_messages` while it builds the inbox index

Run migrations: `2026_11_22_090100_add_mail_inbox_index` adds `idx_mail_messages_inbox_time`, a
B-tree index on `(mailbox_id, direction, created_at DESC, id DESC)` that the unified inbox reads.
It is built inside the migration's transaction, so on a large `mail_messages` table it blocks
inbound delivery and sends until the build ends.

A large installation builds the index beforehand, without blocking writes, and the migration then
skips it (it creates the index with `IF NOT EXISTS`). Run this before the migration, outside a
transaction, as a role that owns `mail_messages`:

```sql
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_mail_messages_inbox_time
  ON mail_messages (mailbox_id, direction, created_at DESC, id DESC);
```

If the `CONCURRENTLY` build fails, it leaves an invalid index of that name, which the migration
would then skip; drop it (`DROP INDEX CONCURRENTLY idx_mail_messages_inbox_time;`) and build it
again before migrating.

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

## Unreleased — Managed zones

### Medium — Self-serve domains need a wider token and a zone worker

Run migrations: `2026_11_19_100000_create_mail_zones` adds `mail_zones` and the
`mail_domains.mail_zone_id` and `mail_domains.capability` columns. With
`MAILBOX_CUSTOM_DOMAINS_ENABLED=true`, a domain whose apex is not in the Cloudflare account now gets
a managed zone that Zmail creates, instead of `422 MAILBOX.DOMAIN_ZONE_NOT_FOUND`. Before enabling
or keeping the feature on:

- Widen `MAILBOX_CLOUDFLARE_API_TOKEN`: add **Account → Zone: Edit** to DNS Edit, Zone Settings
  Edit, Email Routing Rules Edit, and Email Sending Edit, and set its zone resources to all zones of
  the account. Advice to scope the token to selected zones no longer applies; keep zones
  organizations must not use in a separate Cloudflare account.
- Run one more worker of the API image: `mail:zone:work --poll=10m --batch=100`. Docker Compose
  installs get it as the `mail-zone-worker` service in `deploy/self-host/compose.yaml`.
- `MAILBOX_MAX_ZONES_PER_ORGANIZATION` bounds the managed zones per organization (default 3,
  1 to 20).

[docs/runbooks/self-serve-domain-acceptance.md](docs/runbooks/self-serve-domain-acceptance.md)
covers these steps for existing deployments and a real end-to-end check.

## Unreleased — Organization domains

### Medium — New migration for connected domains

Run migrations: `2026_11_17_150000_create_mail_domains` adds an empty `mail_domains` table and
`mailboxes.routing_rule_id`. Nothing changes until `MAILBOX_CUSTOM_DOMAINS_ENABLED=true`, which also
needs `MAILBOX_CLOUDFLARE_EMAIL_WORKER` and a token with Zone Read, DNS Edit, Zone Settings Edit,
Email Routing Rules Edit, and Email Sending Edit; managed zones (above) widen it further.

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
