# External Mail Contract

Part of the [Mailbox Contract](../MAILBOXES.md). Defines external mailbox connections, OAuth and app-password binding, shared connected mailboxes, Outlook and Gmail connectors, sync budgets and runs, retention of revoked copies, push subscriptions, connection directory, and tenant connections. See [`../MAILBOXES.md`](../MAILBOXES.md) for activation, ownership, route overview, and the master error catalog.

## External Mailboxes

A member can bind an outside account through a **connector**: Gmail, Outlook, or any
provider the instance adds later. Its mail is copied into a `personal` mailbox with `origin`
`connected` that the member owns alone (see [Access](INBOX.md#access)); every copy is inbound, `received`, carries the connector id as
`source` (`gmail`, `outlook`, ...), and the provider's receive time as `created_at`. A copy is stored
once per mailbox and provider message id, so a reconnect or a repeated fetch never adds a second
one. The first sync looks back 14 days unless the binding asked for 1 to 90.

Connector ids match `^[a-z][a-z0-9_]{1,15}$`; a new connector adds ids without a contract change, so
clients render an id they do not know generically. A connected mailbox carries the outside account's
address, but that address is never Zmail's to deliver to: inbound delivery, sending through Zmail's
provider, and the address check ignore connected mailboxes. Within one organization and connector an outside account belongs
to one member at a time, or to the organization as a [shared connected mailbox](#shared-connected-mailboxes);
the same address may still exist under another connector or as a Zmail mailbox, which is all the
relaxed address index allows.

Every newly stored message, delivered by Zmail or synced, goes through the same post-store step in
the transaction that stores it: it is queued for [AI triage](TRIAGE_AND_AGENTS.md#ai-triage) when the switches allow,
and announced as `mail.message.received` (webhooks still publish for shared mailboxes only). Only a
connected personal mailbox's owner may switch triage on for it. A connected mailbox whose connection
is not `active` queues nothing for triage and hands no queued message to the model, and any revoke,
including the one when the owner leaves, turns its switch off and drops its queued messages.

While the instance enables no connector (`EXTERNAL_MAIL_PROVIDERS` empty, the default) the connector
list is empty, starting a sign-in answers `404 MAILBOX.EXTERNAL_PROVIDER_UNAVAILABLE`, the connection
routes answer `404 MAILBOX.EXTERNAL_CONNECTION_NOT_FOUND`, and no account is synced.

### Binding

`GET /v1/external-mail-connectors` lists the instance's connectors in a stable order:
`{ "id": "gmail", "auth_kind": "oauth2", "capabilities": { "read": true, "send": false, "tenant": true } }`.
`capabilities.send` is `true` for a connector that can also send through the account's own server
(`imap`, when bound with `smtp_host`; see [Sending From A Connected Mailbox](#sending-from-a-connected-mailbox)).
`tenant` says an owner may authorize a whole tenant of the provider (see
[Tenant Connections](#tenant-connections)).

`POST /v1/external-mail-authorizations` with `{ "provider": "gmail" }` (optionally
`"backfill_days"`) starts a sign-in; with `{ "connection_id": 3 }` it reconnects that connection,
which must be the caller's own. Neither `provider` nor `connection_id`, a `provider` other than the
connection's, or a `backfill_days` outside 1 to 90 is `422 MAILBOX.EXTERNAL_AUTHORIZATION_INVALID`,
as on the app password route below; `0` or no `backfill_days` takes 14. It stores a pending sign-in,
single use and valid for 10 minutes, holding only the SHA-256 of a random `state` and the sealed
PKCE verifier, and answers `201`
`{ "authorization_url", "expires_at" }`. The browser goes to `authorization_url`; the provider sends
it back to `EXTERNAL_MAIL_REDIRECT_URL` with `state` and `code`.

`POST /v1/external-mail-authorizations/completions` with `{ "state", "code" }` uses the pending
sign-in up, whatever the outcome, then exchanges the code with the PKCE verifier:

| Account signed in to | Result |
|---|---|
| New in the organization for this connector | `201`: a private, read-only connected personal mailbox and an active connection, synced at once (`connection_bound`) |
| The caller's own connection, in any state | `200`: the same connection, active, error cleared, synced at once; mailbox and history kept (a `revoked` one backfills again, without copies, and needs a free place under the limit below) (`connection_reconnected`) |
| A reconnect that signed in to another account | `409 MAILBOX.EXTERNAL_ACCOUNT_MISMATCH`; nothing changes |
| Bound by another member, connection not `revoked` | `409 MAILBOX.EXTERNAL_ACCOUNT_TAKEN` |
| Bound by another member whose connection is `revoked` (they left, unbound it, or were disconnected) | `201`: the old connection's copies, mailbox, and connection are deleted for good at once (`copies_purged` with `reason` `rebound` and the caller as actor), then the account is bound into a new mailbox; `409 MAILBOX.EXTERNAL_ACCOUNT_TAKEN` only while the sync worker holds the old connection |
| A new account, or the caller's own `revoked` connection, while the caller holds 10 live (`active` or `needs_reconnect`) connections | `409 MAILBOX.EXTERNAL_CONNECTION_LIMIT`; nothing changes. Reconnecting a live connection never hits the limit |

On a `409` the grant is handed back to the provider. A refused code is
`422 MAILBOX.EXTERNAL_AUTHORIZATION_FAILED`; an unknown, expired, used, or another member's `state`
is `404 MAILBOX.EXTERNAL_AUTHORIZATION_NOT_FOUND`. Both routes are session only and never cached.

### Binding With An App Password

Connectors whose `auth_kind` is `password` never redirect: the generic `imap` connector (enabled by
`imap` in `EXTERNAL_MAIL_PROVIDERS`) reads INBOX of any provider that offers IMAP over TLS and app
passwords, such as iCloud, Yahoo, Fastmail, QQ, 163, or a self-hosted server.

`POST /v1/external-mail-credentials` takes
`{ "provider": "imap", "host", "port"?, "username", "password", "smtp_host"?, "smtp_port"?, "backfill_days"? }`.
Before anything is stored the server signs in once over implicit TLS on port 993 (the only port
accepted) with a verified certificate, and opens INBOX read-only, within 45 seconds in all. A host
that resolves to a loopback, private, shared, benchmarking, reserved, link-local, multicast, or
unspecified address, or to an IPv6 form embedding an IPv4 address (NAT64, 6to4, IPv4-compatible), is
refused.

The bound account is never named by the caller: it is the lowercased `username` when it contains
`@`, otherwise `username@host`. The sealed grant keeps the lowercased host, and restoring a
connection with credentials for another host starts its backfill over. The outcomes are those of a
completed sign-in: `201` for a new account, `200` for the caller's own connection restored with the
new password, `409 MAILBOX.EXTERNAL_ACCOUNT_TAKEN`, and `409 MAILBOX.EXTERNAL_CONNECTION_LIMIT`. In
addition:

| Failure | Result |
|---|---|
| The server refused the username or password | `422 MAILBOX.EXTERNAL_AUTHORIZATION_FAILED` |
| The host does not resolve, is not allowed, does not answer over TLS in time, its certificate does not verify, or the server refused the login for a transient reason (`UNAVAILABLE`, `LIMIT`, `INUSE`, `SERVERBUG`, `CONTACTADMIN`, or a `BAD`) | `422 MAILBOX.EXTERNAL_SERVER_UNREACHABLE` |
| A value carries a control character, or `backfill_days` is out of range | `422 MAILBOX.EXTERNAL_AUTHORIZATION_INVALID` |
| More than 10 attempts by the member in 15 minutes, or 30 by the organization's members in an hour | `429 COMMON.RATE_LIMITED` with `Retry-After` |
| The connector is not enabled or binds with OAuth | `404 MAILBOX.EXTERNAL_PROVIDER_UNAVAILABLE` |

The password is sealed at rest with the connection's grant, never logged, never returned, and never
written to audit metadata. The sync reads INBOX only and never changes the account: messages are
fetched with `BODY.PEEK`, and a message larger than 5 MiB is skipped. One sync of a connection takes
at most 45 seconds and bounds the bytes it accepts; a message the server fails to deliver is retried
on its own once and then skipped, so it never blocks the mailbox. Copies carry `source` `imap` and
are known by their Message-ID, so a mailbox the provider rebuilds (a new UIDVALIDITY) is read again
from the backfill window without second copies. Only a refused login without a transient reason
moves the connection to `needs_reconnect`; posting the credentials again restores it. Unbinding erases the stored password; revoking the app password itself is done at
the provider. The route is session only and never cached.

### Sending From A Connected Mailbox

An `imap` binding may name the account's outgoing server: `smtp_host`, and `smtp_port` 465
(implicit TLS, the default) or 587 (STARTTLS, required). The server then also signs in there with
the same login and certificate rules, within the same 45 seconds, and a refusal answers as for
IMAP. The connected mailbox then has `send` access for whoever may send from it (its owner, or a
shared connected mailbox's members with `send`), and a message written there, a reply included, is
delivered through that server from the account's address instead of through Zmail's provider. It
counts against the organization's send limits like any personal send, and a refusal by the server
fails the message with `failure_code` `smtp_auth_failed`, `smtp_rejected`, `smtp_unavailable`, or
`smtp_not_connected` (the connection is not active). Binding the account again without
`smtp_host` makes the mailbox read-only again. Gmail and Outlook connections stay read-only.

### Shared Connected Mailboxes

An `owner` or `admin` binds a **team account** hosted at an outside provider (for example
`support@` at Google Workspace) into a `shared` mailbox with `origin` `connected`, and assigns its
members exactly as `POST /v1/mailboxes` creates a shared mailbox. Both binding routes take one
optional object; without it the personal flow above is unchanged:

```json
{ "provider": "gmail",
  "sharing": { "display_name": "Support",
               "members": [ { "user_id": 7, "permission": "manage" }, { "user_id": 9, "permission": "view" } ] } }
```

`POST /v1/external-mail-authorizations` stores `sharing` with the pending sign-in;
`POST /v1/external-mail-credentials` takes it next to the credentials. `members` follows the shared
mailbox rules: 1 to 100 distinct current members, each `view`, `send`, or `manage`
(`422 MAILBOX.ACCESS_INVALID`). `display_name` is optional, at most 120 characters (not bytes, as
for every mailbox), and defaults to the account address cut to 120 characters
(`422 MAILBOX.ADDRESS_INVALID` when invalid). A caller who is not an `owner` or `admin` gets
`403 PERMISSION.DENIED`, checked before any attempt is counted or any server contacted; `sharing`
with `connection_id` is `422 MAILBOX.EXTERNAL_AUTHORIZATION_INVALID`. The role and the members are
checked again when the sign-in completes, before the code is exchanged, so a member who left or a
role that changed in between refuses the completion (`422 MAILBOX.ACCESS_INVALID` or
`403 PERMISSION.DENIED`) and nothing is stored. A shared sign-in is completed only by an API version
that knows shared bindings; an older one answers `404 MAILBOX.EXTERNAL_AUTHORIZATION_NOT_FOUND`.

A new account creates, in one transaction, the mailbox, its `created` and `member_set` access
events, the connection, and `connection_bound`, and answers `201` with `"shared": true`. The
connection belongs to the **organization**:

- It has no owner. Who bound it is recorded for the history only; it never counts toward that
  person's 10 connections, and leaving the organization does not revoke it.
- An organization holds at most `EXTERNAL_MAIL_MAX_SHARED_CONNECTIONS` (20 by default, 1 to 100)
  live (`active` or `needs_reconnect`) shared connections; one more, or restoring a revoked one at
  the limit, is `409 MAILBOX.EXTERNAL_CONNECTION_LIMIT`. Like every connected mailbox it is outside
  the organization's mailbox limit.
- An account is either a member's own or the team's: a live connection of the other kind, or a live
  shared connection of the same account, is `409 MAILBOX.EXTERNAL_ACCOUNT_TAKEN`. A revoked personal
  connection is freed for a shared binding as for any other binder (purged with `reason` `rebound`).
  A revoked shared connection stays the team's while its mailbox exists: it is restored by a
  reconnect, never taken over; once the mailbox is deleted, a new binding of either kind purges it
  and binds fresh. Converting a member's connection into a shared one, or back, is not offered:
  unbind and bind again.

Access follows the shared mailbox rules (see [Access](INBOX.md#access)), minus what the read-only origin
forbids: members read with their own read state according to `view`, `send`, or `manage`; nobody
sends, replies, composes from it, deletes mail for good, or changes its folders, whatever the
permission; an `owner` or `admin` who is not a member manages members, webhooks, and the history but
reads no mail until adding themselves, which is recorded. The member, webhook, and AI triage
switches and agent grants follow the shared mailbox matrix; an agent grant never gets `draft`. Handover
does not apply, and deleting the mailbox revokes its connection in the same transaction.

Whoever manages the mailbox's members (a `manage` member, an `owner`, or an `admin`) manages the
connection: they reconnect it with `connection_id` on either binding route (the same account, or
`409 MAILBOX.EXTERNAL_ACCOUNT_MISMATCH`; checked again at completion), unbind it, and read its sync
runs. Purging a revoked shared connection at once deletes the mailbox with every copy and member, so
only an `owner` or `admin`, who may delete the mailbox anyway, may do it; a `manage` member gets
`403 PERMISSION.DENIED`, and the worker's purge after the retention is unchanged. Everyone else gets
`404 MAILBOX.EXTERNAL_CONNECTION_NOT_FOUND`, and so does anyone once the mailbox is deleted. An unbind follows the revoke path below and records
`connection_revoked` with the caller as actor, plus an organization audit entry with `reason`
`shared_disconnect`, filed like a forced disconnect under the caller and every other `owner` and
`admin`; the mailbox and its members stay until the retention ends, the connection is
reconnected, or the mailbox is deleted.

### Connections

`GET /v1/external-mail-connections` lists the caller's own connections in the organization, then
the shared connections the caller manages, at most 220 in all; it never carries a token. The
caller's own live connections come first, id ascending (at most 10, the limit), then their 10 most
recently revoked ones, newest revocation first, so a member at the limit still sees the revoked ones
whose copies they may purge or rebind. Then come the caller's personal mailboxes
[assigned from a tenant](#tenant-connections), live ones first, at most 100, with `tenant_id` set (it
is `null` otherwise). Then come the shared connections the caller manages (every one for an `owner`
or `admin`, those of mailboxes held with `manage` otherwise) whose mailbox still exists, live ones
first and then id ascending, at most 100. Revoked connections are kept so a rebind restores the same
mailbox, and do not count toward the limits:

```json
{ "id": 3, "provider": "gmail", "account_address": "me@gmail.com", "mailbox_id": 41, "shared": false,
  "tenant_id": null, "status": "active", "last_synced_at": "2026-11-20T08:00:00Z", "last_error_code": null,
  "created_at": "2026-11-19T08:00:00Z", "push": null }
```

`push` is the connection's [push subscription](#push-subscriptions) while push is on, and `null` when
push is off or the connection has none.

| `status` | Meaning |
|---|---|
| `active` | Synced every two minutes, or within seconds of new mail while a [push subscription](#push-subscriptions) is shown to work |
| `needs_reconnect` | The provider refused the grant (`last_error_code` `external.auth_failed`); syncing stops until the member reconnects |
| `revoked` | Unbound; never synced again |

A provider failure that is not about the grant keeps the connection `active` with
`last_error_code` `external.fetch_failed` and retries with exponential backoff: two minutes after
the first failure in a row, then 4, 8, 16, 32 minutes, and at most one hour, each less up to a fifth
of random jitter. A successful fetch resets the backoff to the two-minute interval. A connection whose
connector the instance no longer runs stays `active` with its grant and records
`external.provider_unavailable` until the connector is enabled again.

`DELETE /v1/external-mail-connections/:connection_id` unbinds: the member who bound the account
(`connection_revoked`), or an `owner` or `admin` (`connection_force_disconnected`), may. The grant is revoked at the provider on a best-effort basis, the stored
token is erased, and syncing stops for good, including a fetch already running. Mail already synced
stays in the connected mailbox until the retention below ends or the member purges it. Unbinding a revoked connection answers `204` again and records
nothing. A member who leaves the organization has every connection there revoked the same way at
once (`connection_revoked_on_leave`): the accounts are their own, so the connected mailbox never
awaits a handover, the handover routes refuse it, and the copies stay readable by nobody.
Binding the account again after rejoining restores the same mailbox without copies. Another member's
connection and another organization's id are `404 MAILBOX.EXTERNAL_CONNECTION_NOT_FOUND`.

### Outlook

The `outlook` connector runs when `EXTERNAL_MAIL_PROVIDERS` lists it and its OAuth client is set
(`EXTERNAL_MAIL_OUTLOOK_CLIENT_ID`, `EXTERNAL_MAIL_OUTLOOK_CLIENT_SECRET`, and
`EXTERNAL_MAIL_OUTLOOK_TENANT`, `common` by default). It signs in through the Microsoft identity
platform with the delegated scopes `offline_access Mail.Read User.Read openid email` and binds the
account's `mail` address, or its user principal name when it has none. It syncs the Inbox through
Microsoft Graph message delta; Sent Items, Drafts, Deleted Items, and Junk Email are not synced, and
neither are drafts. A message removed at Microsoft stays in the connected mailbox. Graph returns at
most about 5,000 messages in the first sync's look-back, so the first sync of a busier Inbox may
stop short of it; later syncs read every new message. A body over 256 KiB is cut there, and a message whose
body Graph cannot return within the connector's bound is kept with its headers only. Unbinding erases
the stored grant; Microsoft has no per-grant revocation, so the member removes the app's consent in
their Microsoft account to end it there as well.

### Gmail

The `gmail` connector signs in on Google's consent page with the read-only Gmail scope and offline
access. The first sync copies the backfill window newest first, a page at a time, and then follows
the account's history from where the backfill began, so mail that arrives during the backfill is
not missed. Only inbound mail is copied: drafts, spam, trash, and sent mail that is not also in the
inbox are skipped; mail moved out of spam into the inbox is copied then. When Google no longer
replays the history, the sync starts the backfill over without copies. Only a grant Google refuses
or one that lost access to the mailbox moves the connection to `needs_reconnect`; rate limits,
quotas, outages, and a misconfigured OAuth client are retried at the next interval.

### Sync Budget

Each pass of the `mail:external:sync` worker takes due connections round-robin across providers:
the oldest due connection of every provider, then the second of every provider, and so on, and at
most `EXTERNAL_MAIL_PROVIDER_FETCHES_PER_PASS` (20 by default) of one provider. One provider's
outage or one large backfill therefore never starves the others. Each fetch has its own deadline,
`EXTERNAL_MAIL_FETCH_TIMEOUT` (60 seconds by default): a fetch that hits it is `fetch_failed`, backs
off, and the pass moves on to the next connection. A connection whose provider has another page
ready is fetched again at once, for at most `EXTERNAL_MAIL_MAX_IMMEDIATE_PAGES` (20 by default)
pages in a row; after that each further page waits `EXTERNAL_MAIL_MIN_FETCH_SPACING` (5 seconds by
default), so one backfill cannot monopolize the worker. A connection whose cursor the provider no
longer honors starts over from an empty cursor at once; the restart counts toward the same bound.

### Sync Runs

Every fetch records one sync run, in the transaction that saves its result. A run holds times,
counts, and codes, never message data. The worker keeps a connection's latest 50 runs and none older
than 14 days.

`GET /v1/external-mail-connections/:connection_id/sync-runs` lists the latest runs of the caller's
own connection, or of a shared connection they manage, newest first, at most 50. Anyone else, an `owner` or `admin` included, gets
`404 MAILBOX.EXTERNAL_CONNECTION_NOT_FOUND`. The response is never cached:

```json
{ "id": 912, "started_at": "2026-11-20T08:00:00Z", "finished_at": "2026-11-20T08:00:01Z",
  "outcome": "ok", "fetched_count": 25, "stored_count": 24, "more": true, "error_code": null,
  "timed_out": false, "throttled": false, "trigger": "poll" }
```

`trigger` is `poll` for a sync on the schedule and `push` for one that answered a provider
notification (see [Push Subscriptions](#push-subscriptions)).

| `outcome` | Meaning | `error_code` |
|---|---|---|
| `ok` | A page was saved | `null` |
| `auth_failed` | The provider refused the grant; the connection needs reconnecting | `external.auth_failed` |
| `fetch_failed` | Any other provider failure; the next attempt backs off | `external.fetch_failed` |
| `cursor_expired` | The provider no longer honored the cursor; the sync starts over within the backfill window without repeating stored copies | `null` |
| `provider_unavailable` | The instance no longer runs the connector | `external.provider_unavailable` |
| `dropped` | The connection was revoked or lost its grant while the fetch ran; nothing fetched was stored | `null` |

`fetched_count` is how many messages the provider returned and `stored_count` how many were new;
`more` says the provider had another page ready. `timed_out` marks a `fetch_failed` fetch that hit
its deadline, and `throttled` a page after which the next one waits the spacing because the
connection already fetched the most pages in a row allowed at once.

### Retention Of Revoked Copies

Revoking a connection, by its member, an `owner` or `admin`, or because the member left the
organization, starts a retention of `EXTERNAL_MAIL_REVOKED_RETENTION` (720 hours, 30 days, by
default; at least 24 hours, written as a duration such as `720h` or a day count such as `90d`; a
value that does not parse stops startup). Binding the same account again before it ends keeps the
mailbox and its copies and cancels the deletion. When it ends, the mail worker (`mail:broadcast:work`
ticks, which run whenever the mailbox starter is on, with or without `EXTERNAL_MAIL_PROVIDERS`)
deletes the copies for good in bounded batches, then deletes
the connected mailbox and the connection with its sync runs, and stores a `copies_purged` access
event on the deleted mailbox with `{ "connection_id", "provider", "reason": "retention" }`, the
member who bound the account as its subject. The event is an audit record: the access history route
of a deleted mailbox answers `404`, so it is never listed. Binding the account afterwards starts a
new mailbox.

`POST /v1/external-mail-connections/:connection_id/purges` deletes the copies of the caller's own
revoked connection, or, for an `owner` or `admin`, of a revoked shared connection, at once, the same way, with `reason` `owner` and the caller as the actor:

| Case | Answer |
|---|---|
| Every copy, the mailbox, and the connection are deleted | `200` `{ "deleted_messages": 120, "completed": true }` |
| More copies than one request deletes | `202` `{ "deleted_messages": 5000, "completed": false }`: the rest is due at once and the worker deletes it in its next passes |
| The connection is not revoked | `409 MAILBOX.EXTERNAL_CONNECTION_NOT_REVOKED` |
| Another member's connection, an `owner` or `admin` included | `404 MAILBOX.EXTERNAL_CONNECTION_NOT_FOUND` |

The route works while `EXTERNAL_MAIL_PROVIDERS` is empty, so switching the feature off never
strands copies. A purge only ever touches the connection's own mailbox, and only when that mailbox
holds synced copies; a purge that fails is logged and retried an hour later without holding up the
connections revoked after it. A claimed connection whose mailbox is gone is not fetched: its grant
is handed back, it is revoked, and its run is recorded as `dropped`.

### Push Subscriptions

Gmail and Outlook can tell Zmail that a connected mailbox received mail, so it syncs within seconds
instead of at the next poll. Push is off unless the operator sets `EXTERNAL_MAIL_PUSH_ENABLED=true`
and a provider's settings: for Gmail, `EXTERNAL_MAIL_GMAIL_PUSH_TOPIC`,
`EXTERNAL_MAIL_GMAIL_PUSH_AUDIENCE`, and `EXTERNAL_MAIL_GMAIL_PUSH_SERVICE_ACCOUNT`, all three or
startup fails; for Outlook, `EXTERNAL_MAIL_OUTLOOK_PUSH_NOTIFICATION_URL`. IMAP has no push (IMAP
IDLE is future work).

A notification is only a hint. It never carries mail, and nothing in it is trusted for mail. It
marks the connections it names due, and the regular sync reads the mail with each connection's own
grant, cursor, backfill window, and `received_not_before` floor. That sync still keeps the
connection's backoff, lease, fetch spacing (`EXTERNAL_MAIL_MIN_FETCH_SPACING`, so a throttled
backfill keeps its pace), and the per-provider budget. Polling stays the safety net.

When a notification arrives:

- The route records it on the subscription only, without waiting for any lock and without touching
  the connection. The `mail:external:sync` worker's next pass then makes the connection due, so the
  delay from a notification to the start of its sync is at most the worker's idle wait (two seconds
  by default while push is on) plus the coalescing window.
- If a sync is saving the subscription at that very moment, the route records the notification on
  the other subscriptions it names and answers `503 COMMON.SERVICE_UNAVAILABLE` with `Retry-After: 1`
  at once, rather than waiting or dropping it: Pub/Sub and Graph deliver it again a moment later.
- If a worker is fetching the connection, the connection syncs again once that fetch ends. This
  holds even when the fetch was itself answering an earlier notification.
- If the connection is backing off, the notification waits for the backoff.
- However many notifications arrive, one connection syncs at most once per
  `EXTERNAL_MAIL_PUSH_COALESCE_WINDOW` (10 seconds by default) because of them.

**Lifecycle.**

- **Subscribe and renew.** An `active` connection whose connector supports push subscribes after a
  sync that leaves no further page, and renews once half of the subscription's life has passed. Both
  run in the worker, with the grant the sync used. A mailbox [assigned from a tenant](#tenant-connections)
  uses the token the tenant credential mints for that one mailbox.
- **Long poll only while push is shown to work.** The poll slows to `EXTERNAL_MAIL_PUSH_POLL_INTERVAL`
  (15 minutes by default, 2 minutes to 1 hour), never past the next renewal or the expiry, only
  while the subscription is live and both of these hold:
  - A notification arrived, or the subscription went live, within `EXTERNAL_MAIL_PUSH_QUIET_LIMIT`
    (24 hours by default, 1 to 168 hours).
  - No poll has caught it missing mail.
- **Missed mail.** An incremental poll that reads a whole round and inserts a new copy, received
  more than five minutes after the subscription went live and more than five minutes before the
  poll, with no notification for it, marks the subscription degraded. Its renewal then becomes due,
  and the connection polls every two minutes until a change notification for it arrives again (a
  lifecycle event, `missed` included, does not clear it). Only copies the poll actually inserts
  count: a message the provider reports again (Graph delta also returns messages that were read,
  flagged, or categorized), a backfill, a restart after an expired cursor, and pages in the middle
  of a round never mark it. This catches a deleted Pub/Sub subscription, a misconfigured audience,
  or a provider that stopped delivering.
- **Failures.** A subscribe or renewal that fails backs off from two minutes to an hour. It records
  `external.auth_failed` or `external.fetch_failed` on the subscription only, and never changes the
  connection's status or stops its sync. The poll returns to two minutes once no subscription is live.
- **Stopping.** Every path stops the subscription in Zmail in the transaction that ends the
  connection, and its notifications are ignored from then on:
  - Unbinding by the member, a forced disconnect, a member's departure, and deleting the mailbox stop
    it at the provider too, and hand the grant back. Both happen only after that transaction
    committed; a departure's connections are released a few at a time, and a Gmail watch that
    another live connection of the same account shares is left running.
  - What is left to do at the provider is recorded in that same transaction, so a process that stops
    right after the commit loses nothing: the `mail:external:sync` worker performs it, retrying with
    backoff. The record outlives the purge of the connection's copies and is deleted with the
    organization. It keeps a sealed copy of the grant only for a connector whose revocation really
    revokes a provider grant (Gmail), never for a tenant mailbox, an app password, or a release that
    only stops a push subscription. The copy is never readable through the API and never logged, and
    is kept only until the release succeeds or for at most 24 hours; the mail worker's purge pass,
    which runs whether or not a connector is enabled, deletes it then. A Gmail grant that could not be
    handed back by then is not revoked by Zmail and does not lapse on its own: the member removes
    Zmail's access in their Google account to end it.
  - Deleting an organization deletes its pending releases undone. Binding an account again deletes
    the organization's pending releases of that address. Assigning or restoring a tenant mailbox
    deletes only those that stop a subscription: a member's own grant is still handed back, since the
    tenant reads with its own credential.
  - A release is also postponed, not dropped, while the member (or, for a shared connection, anyone
    in the organization) has a sign-in pending for the provider, and everything that would make a
    hand-back harmful is checked again right before the hand-back call. The release records, when
    it is made, whose connection it was, so the same rule applies after the copies were purged and
    the connection is gone: another member's sign-in never postpones the release of a member's own
    connection, and once that member's user account is deleted no sign-in postpones it.
  - A recorded release never touches an account that is in use again: binding the same account again
    in the organization deletes it; a release whose connection is active again is dropped unperformed;
    a watch is stopped only while no live connection watches the same Gmail address; and a grant is
    handed back only while no live connection, in any organization, reads the same account with a
    grant of its own, since at Google revoking one token revokes the user's whole grant. A Google
    account bound by several connections therefore keeps Zmail's access until its last binding goes.
  - A revoked tenant, a connection that needs reconnecting, and the retention purge stop it in Zmail
    only (the worker does so at most once a minute; the connection's notifications are ignored from
    the moment it stops being `active`). Their provider subscription lapses at its expiry: at most 7 days for Gmail, 70 hours for
    Outlook.
  - A subscription a sync created while the connection was being revoked is stopped again at the
    provider, unless another live connection of the same account (by Gmail's primary address)
    shares it.

**Gmail.**

- **Watch.** The connector calls `users.watch` on the account's `INBOX` with the operator's Cloud
  Pub/Sub topic. Google publishes to the topic, and a Pub/Sub push subscription posts to
  `POST /v1/external-mail-notifications/gmail` with an OIDC token.
- **Authentication.** The request counts only when all of these hold:
  - The token is RS256-signed by a key Google publishes at `EXTERNAL_MAIL_GMAIL_PUSH_JWKS_URL`. The
    key set is size- and count-bounded, cached for an hour, and refetched for an unknown key at most
    once a minute by one request while others wait.
  - Its issuer is `accounts.google.com` and its audience is `EXTERNAL_MAIL_GMAIL_PUSH_AUDIENCE`.
  - It has not expired.
  - It was issued to the verified service account `EXTERNAL_MAIL_GMAIL_PUSH_SERVICE_ACCOUNT`.

  Otherwise the answer is `401 AUTH.UNAUTHORIZED` and nothing changes.
- **Matching.** The subscription is keyed by the address Gmail's profile reports when it is created,
  which is the address notifications name, so a connection bound under an alias still matches. A
  notification makes every `active` connection of that address due (up to 1,000 per request, in one
  bounded query), in any organization, and each is synced with its own grant only. Every
  authenticated request is answered `204`.
- **Workspace tenants.** A Gmail mailbox assigned from a Workspace tenant is not pushed: its tokens
  come from the customer's own service account in the customer's Cloud project, and Gmail only
  publishes to a topic of the token's own project. Its subscription reads `unsupported`, it is
  polled every two minutes, and the connector asks again once a day without reporting a failure.

**Outlook.**

- **Subscription.** The connector creates a Microsoft Graph subscription on the mailbox's Inbox
  messages (`created`), with `EXTERNAL_MAIL_OUTLOOK_PUSH_NOTIFICATION_URL` as both the notification
  and the lifecycle URL, a fresh random `clientState`, and a 70-hour expiry. It renews with `PATCH`
  and creates a new subscription when Graph no longer knows the old one. Zmail stores only the
  SHA-256 of the `clientState`.
- **Handshake.** `POST /v1/external-mail-notifications/outlook?validationToken=...` is Graph's
  validation handshake: the token (at most 1,024 printable ASCII characters) is echoed as `text/plain`.
- **Notifications.** Any other request is a Graph notification collection. Its at most 20
  notifications are looked up together. Each counts only when its `clientState` matches the
  subscription's, compared in constant time, and only marks that subscription's connection due.
- **Lifecycle notifications.** `reauthorizationRequired` makes the renewal due now;
  `subscriptionRemoved` ends the subscription's life so the next sync creates it again; `missed`
  only marks the connection due. None of them clears a degraded mark.
- **Answers.** Every readable request is answered `202`. A body that is not JSON is
  `400 COMMON.INVALID_INPUT`.

**Both routes.**

- **No disclosure.** They need no session or API key and never say whether a subscription exists:
  a known and an unknown subscription get the same answer, which also tells the provider to stop
  retrying.
- **Limits.** Bodies are at most 64 KiB (`413 COMMON.REQUEST_TOO_LARGE`), and each request does a
  bounded amount of work. When a budget is used up, the answer is `429 COMMON.RATE_LIMITED` with
  `Retry-After`:
  - Only a request that authenticates a notification (a verified token, or a matching
    `clientState`) spends the provider's budget, `EXTERNAL_MAIL_PUSH_MAX_PER_MINUTE` a minute
    (6,000 by default). That budget is per replica, or shared across replicas with
    `RATE_LIMIT_STORE=postgres`.
  - A request that authenticates nothing (a refused token, a malformed body, or notifications that
    match no subscription) spends only its sender IP's budget of 120 a minute, and a validation
    handshake only the sender's separate budget of 30 handshakes a minute, so junk never blocks a new
    Outlook subscription. Both are kept in each replica's memory and never written to the database.
  - The sender is the address `SERVER_TRUSTED_PROXIES` resolves. In production push is refused at
    startup unless `SERVER_TRUSTED_PROXIES` names the proxies in front of the API or
    `EXTERNAL_MAIL_PUSH_DIRECT_EXPOSURE=true` states there is none, since behind an unnamed proxy every
    request would seem to come from it.
- **Push off.** While push is off, or the provider's push settings are not set, both routes answer
  `404 MAILBOX.EXTERNAL_PROVIDER_UNAVAILABLE` and read nothing.
- **Logging.** Nothing from a body is logged, only the provider and counts. Refused requests are
  counted by `zmail_external_mail_push_notifications_total` and logged at most once a minute per
  provider and reason, never once the sender is over its budget. The subscription id and secret
  never appear in a response, a log, or an audit row.

A connection's `push`, on the [connection list](#connections), is:

```json
{ "status": "active", "expires_at": "2026-11-27T08:00:00Z", "last_notified_at": "2026-11-20T08:00:04Z",
  "last_error_code": null }
```

| `status` | Meaning | Poll |
|---|---|---|
| `active` | A live subscription that is shown to work; new mail syncs within seconds | `EXTERNAL_MAIL_PUSH_POLL_INTERVAL` |
| `quiet` | Live, but no notification within `EXTERNAL_MAIL_PUSH_QUIET_LIMIT` (normal for a quiet mailbox) | two minutes |
| `degraded` | Live, but a poll found mail no notification announced; renewal is due, and an authenticated notification clears it | two minutes |
| `pending` | No live subscription yet, or it expired or was removed; the next attempt backs off | two minutes |
| `unsupported` | The provider cannot push for this connection (a Gmail mailbox assigned from a Workspace tenant) | two minutes |
| `stopped` | Ended with its connection | none |

`expires_at` is set while the subscription is live. Operators also see
`zmail_external_mail_push_notifications_total{provider,result}`,
`zmail_external_mail_push_degraded_total{provider}`, and
`zmail_external_mail_push_delay_seconds{provider}` (notification to sync start).

### Connection Directory

Owners and admins learn that members connected outside accounts, and can disconnect them, without
ever seeing the account's mail or full address. Members get `403 PERMISSION.DENIED` on both routes.

`GET /v1/external-mail-connection-directory` returns keyset pages of every connection of the
organization in any status, newest first (`cursor`, `per_page` 1 to 100, default 15; `page` is
`400 COMMON.INVALID_INPUT`). It is empty while external mail is off, and never cached:

```json
{ "id": 3, "provider": "gmail", "status": "active", "shared": false, "tenant_id": null, "account_address": null,
  "mailbox_id": null, "masked_account_address": "st***@gmail.com", "owner_user_id": 5,
  "owner_display_name": "stark", "last_synced_at": "2026-11-20T08:00:00Z",
  "created_at": "2026-11-19T08:00:00Z" }
```

The mask keeps the first two characters of the local part (one when it is that short) and the
domain. `owner_display_name` is the owner's nickname, or username without one, while they are a
member, otherwise `null`. No token, message, or count is ever returned, and for a member's own
account neither its full address nor its mailbox id. A shared connection is the organization's own
team account: it carries `"shared": true`, its full `account_address`, and its `mailbox_id`, with
`owner_user_id` and `owner_display_name` `null`. A tenant assignment carries its `tenant_id`, and,
being at an organization address, its full `account_address` and `mailbox_id` too.

`DELETE /v1/external-mail-connection-directory/:connection_id` force-disconnects any connection of
the organization with the effect of the owner's unbind and writes `connection_force_disconnected`
to the connected mailbox's history; a shared connection is unbound as in
[Shared Connected Mailboxes](#shared-connected-mailboxes) instead. A revoked connection answers `204` again and writes nothing;
another organization's id, and any id while external mail is off, is
`404 MAILBOX.EXTERNAL_CONNECTION_NOT_FOUND`. A forced disconnect that changed anything is also
written to the organization audit log (resource `external_mail_connections`, the connection id,
`provider`, and `owner_user_id`; never the address). The record is filed under the acting manager
and, as a copy with the same actor, under every other owner and admin of the organization, so each
of them finds it in `GET /v1/audit-logs?resource=external_mail_connections` and sees who
disconnected which account. The owner may bind the account again afterwards, which restores the
same mailbox like any rebind of their own; until then another member who proves control of the
account may bind it instead (see [Binding](#binding)).

### Tenant Connections

An organization **owner** authorizes a whole Google Workspace or Microsoft 365 tenant once; owners
and admins then pick mailboxes from its directory and assign each to a member (a personal connected
mailbox) or as a shared connected mailbox, without anyone signing in to the mailbox.

What this guarantees: authorizing a tenant grants no read access by itself; it creates no mailbox,
and only assigned mailboxes are synced, each with a short-lived token the tenant credential mints
for that one mailbox. Owners and admins decide who is assigned which mailbox; every assignment is
written to the organization audit log and the mailbox access history. A personal assignment cannot
be redirected to another reader while its member is in the organization (see the assignment rules
below). Every tenant route needs a session; there is none on the API key surface, so an agent never
gets tenant-level access, only the per-mailbox [agent grants](TRIAGE_AND_AGENTS.md#agent-grants) of assigned mailboxes,
issued as for any mailbox. Members get `403 PERMISSION.DENIED` on every tenant route.

Tenant connect is off unless the instance sets `EXTERNAL_MAIL_TENANTS_ENABLED=true` (and enables the
`gmail` or `outlook` connector). While it is off, as while external mail is off, no connector lists
`capabilities.tenant`, authorizing answers `404 MAILBOX.EXTERNAL_PROVIDER_UNAVAILABLE`, and the
directory and assignment routes answer `404 MAILBOX.EXTERNAL_CONNECTION_NOT_FOUND`. Listing
(`GET /v1/external-mail-tenants`) and revoking (`DELETE /v1/external-mail-tenants/:tenant_id`) keep
working, so an owner can always see and revoke a stored credential. Mailboxes already assigned keep
syncing, and owners and admins still disconnect them one by one with the connection routes.

`POST /v1/external-mail-tenants` (owner only; `admin` is `403 PERMISSION.DENIED`):

| Provider | Body | Answer |
|---|---|---|
| `gmail` (Google Workspace) | `service_account_key` (the JSON key file, as an object or a string) and `admin_subject` (an administrator's address) | `201` tenant |
| `outlook` (Microsoft 365) | `tenant_id` (the Entra tenant id, a GUID), `client_id`, and `client_secret` of the organization's own application | `201` tenant |

Only a credential the organization owns is accepted. A body without one, or with `client_id` and no
`client_secret` (or the reverse), is `422 MAILBOX.EXTERNAL_AUTHORIZATION_INVALID` before any provider
is asked. There is no admin consent redirect: an admin consent to an operator's application cannot be
tied to the organization that claims it.

- **Google**: the organization creates a service account in its own Google Cloud project, authorizes
  its client id for domain-wide delegation in the Workspace Admin console with the scopes
  `https://www.googleapis.com/auth/gmail.readonly` and
  `https://www.googleapis.com/auth/admin.directory.user.readonly`, and posts its key. Zmail signs an
  RS256 assertion with the key for the administrator (both scopes) and reads one Directory API user to
  learn the customer id, which becomes `tenant_identifier`; `display_name` is the administrator's
  domain. A mailbox token is an assertion with the mailbox as the subject and `gmail.readonly` only.
  The key's own `token_uri` is ignored. The customer revokes by deleting the key or the delegation.
- **Microsoft**: the organization registers an application in its own Entra tenant with the Graph
  application permissions `Mail.Read` and `User.Read.All`, grants admin consent there, and posts the
  tenant id with the application's client id and secret. Zmail proves them with a client credentials
  token and one Graph `/users` read before anything is stored, and repeats both whenever it checks
  the tenant, so a removed permission is noticed. Tokens are cached in memory for their lifetime, never
  logged.
- **Microsoft, scope**: an application token is tenant-wide at Microsoft; Zmail only ever
  addresses the assigned mailbox (`/users/{id}`) with it. The customer should restrict the
  application to the assigned mailboxes with **RBAC for Applications in Exchange Online** (a
  management scope and an `Application Mail.Read` role assignment; changes take 30 minutes to 2 hours
  at Microsoft). When revoking in Zmail, the customer should also delete the application's secret
  in Entra.

A malformed body is `422 MAILBOX.EXTERNAL_AUTHORIZATION_INVALID`; a credential the provider refuses
is `422 MAILBOX.EXTERNAL_AUTHORIZATION_FAILED`. A tenant identifier is live (not `revoked`) in at most
one organization of the instance: another organization's is `409 MAILBOX.EXTERNAL_ACCOUNT_TAKEN`.
An organization holds at most 50 live tenants; one more is `409 MAILBOX.EXTERNAL_CONNECTION_LIMIT`
(revoked ones do not count). Authorizing a tenant this organization already holds re-authorizes it in
place (`200`): the new credential replaces the old, the tenant is `active` again, and the assignments
the lost tenant credential stopped (`last_error_code` `external.tenant_auth_failed`) resume at once;
one stopped by its own mailbox's refusal stays `needs_reconnect`. A check that ran with a credential
the owner has since replaced never stops the re-authorized tenant. The tenant, never its credential:

```json
{ "id": 4, "provider": "gmail", "tenant_identifier": "C01abc2de", "display_name": "acme.com",
  "status": "active", "last_error_code": null, "assignments": 12, "authorized_by_user_id": 1,
  "authorized_at": "2026-12-08T09:00:00Z", "revoked_at": null,
  "created_at": "2026-12-08T09:00:00Z", "updated_at": "2026-12-08T09:00:00Z" }
```

| `status` | Meaning |
|---|---|
| `active` | Mints tokens for its assigned mailboxes |
| `needs_reauthorization` | The provider refused the tenant credential (`last_error_code` `external.tenant_auth_failed`); every assignment is `needs_reconnect` until the owner authorizes the tenant again |
| `revoked` | Revoked by the owner; kept for the record without its credential |

`GET /v1/external-mail-tenants` (owners and admins) lists the organization's tenants, live first,
then id ascending, at most 50. `DELETE /v1/external-mail-tenants/:tenant_id` (owner only) revokes the
tenant and, in one transaction, every connection of it: each stops syncing at once, follows the
revoke path (triage off, agent grants end) and records `connection_revoked` with
`detail.reason` `tenant_revoked`; the tenant's credential is erased. It answers `204`, again for a
revoked tenant; another organization's id is `404 MAILBOX.EXTERNAL_CONNECTION_NOT_FOUND`.

`GET /v1/external-mail-tenants/:tenant_id/directory` (owners and admins) reads one page of the
tenant's directory from the provider (`cursor`, `per_page` 1 to 100, default 50). The cursor is
opaque and carries only the provider's page token: Zmail always builds the provider request itself,
so a cursor never names another resource. A cursor that is malformed or that the provider refuses is
`400 COMMON.INVALID_INPUT` and says nothing about the tenant. Suspended and archived Google users and
Microsoft users without a mail address are left out. A tenant that is not `active` is
`409 MAILBOX.ACCESS_CONFLICT`, and so is one whose credential the provider refuses now (it becomes
`needs_reauthorization`):

```json
{ "address": "alice@acme.com", "display_name": "Alice", "directory_id": "1067...", "assigned": true,
  "assignment": "personal", "connection_id": 31, "mailbox_id": 88, "available": false }
```

`assigned` marks a live connection of this tenant (`assignment` `personal` or `shared`);
`available` is `false` while any live connection of the organization holds the address, such as a
member's own binding.

`POST /v1/external-mail-tenants/:tenant_id/assignments` (owners and admins) takes 1 to 100 rows
(`422 MAILBOX.ACCESS_INVALID` otherwise) and answers `200` with one result per row, in order:

```json
{ "assignments": [
  { "address": "alice@acme.com", "assign": "personal", "user_id": 7 },
  { "address": "support@acme.com", "assign": "shared", "display_name": "Support",
    "members": [ { "user_id": 7, "permission": "manage" }, { "user_id": 9, "permission": "view" } ] } ] }
```

```json
{ "address": "alice@acme.com", "status": "created", "connection_id": 31, "mailbox_id": 88, "error_code": null }
```

`status` is `created`, `restored` (a revoked assignment of the same target comes back with its
copies), `unchanged` (the same live assignment already exists: rows are idempotent per address), or
`failed` with `error_code`; each row is stored in its own transaction, so a failed row is retried by
sending it again alone. Row failures:

| `error_code` | Why |
|---|---|
| `MAILBOX.ACCESS_INVALID` | Not one address; `personal` without a current member `user_id`, or with members; `shared` without valid members (the `POST /v1/mailboxes` rules); the address twice in the batch |
| `MAILBOX.EXTERNAL_ACCOUNT_MISMATCH` | Not a mailbox of the tenant's directory (an alias is not its user's address) |
| `MAILBOX.EXTERNAL_ACCOUNT_TAKEN` | A live connection of the address that is a member's own, a shared one, another tenant's, or this tenant's with another target; a revoked shared one whose mailbox exists; or an address last assigned as personal to another member who is still in the organization |
| `MAILBOX.EXTERNAL_CONNECTION_LIMIT` | The tenant holds `EXTERNAL_MAIL_MAX_TENANT_ASSIGNMENTS` (1000 by default, 1 to 10000) live assignments, or a shared row would pass `EXTERNAL_MAIL_MAX_SHARED_CONNECTIONS` |
| `MAILBOX.ACCESS_CONFLICT` | The tenant credential stopped working, or the tenant was revoked, during the batch; this and later rows are not tried |
| `MAILBOX.EXTERNAL_SERVER_UNREACHABLE` | The provider could not be asked; retry the row |

Each row holds the tenant row shared until it commits, so a revocation waits for rows in progress and
revokes what they created. Restoring a shared assignment makes its members exactly the posted set:
the others are removed (`member_removed`).

A personal assignment cannot be redirected, and **a holder only ever reads mail received during their
own tenures**:

- Zmail keeps a record of every address any connection of the organization ever read, by any kind
  of binding (a member's own grant, a team account, a tenant assignment), under every connector
  (matched by address, ignoring case, and by directory id): who holds it now, its privacy floor, which
  only ever rises, and the member exempt from the floor. It is written when the binding is made, so
  it survives disconnects, purges, deletions, departures, handovers, and tenant revocation. A member's
  own binding is a tenure like any other: assigning the address from the tenant to someone else
  afterwards starts after it.
- The first personal holder is exempt, with the normal look-back, across departures and returns, for
  as long as nobody else has held the address. Once anyone else held it (another member, a shared
  mailbox, or a handover), nobody is exempt any more.
- Every other assignment, personal or shared, and every restore of one, starts its connection at the
  later of the floor and the moment of the assignment: the connector is asked for nothing older, and
  every copy the provider received before it, or without a receive time, is dropped before it is
  stored, whatever the connector returned. A connection's start never goes down and is never
  cleared. A member assigned the address again keeps the copies their mailbox still holds from their
  own earlier tenure, but nothing received while they did not hold it is fetched.
- While that member is in the organization, the address goes to nobody else, personal or shared
  (`409 MAILBOX.EXTERNAL_ACCOUNT_TAKEN`), unless it was handed over. The same member may have it back
  at any time, with the normal look-back.
- When the member leaves, the assignment awaits handover: it cannot be disconnected, purged, or its
  mailbox deleted (`409 MAILBOX.ACCESS_CONFLICT`), the worker's retention purge skips it, and it is not reassigned
  (`409 MAILBOX.EXTERNAL_ACCOUNT_TAKEN`). The only ways on are the handover, which seals it, or the
  member's return, which finds it intact.
- A departure or a handover raises the floor to that time; the handover's seal keeps the successor
  from what came before it.
- Once a tenant assigned the address, nobody but the exempt first holder binds it with an own grant,
  personal or shared (`409 MAILBOX.EXTERNAL_ACCOUNT_TAKEN`; matched by address and by the directory id
  the provider reports): such a binding has no floor. This strict rule is pending confirmation. Before
  any tenant assignment the earlier rules hold: a member who proves control of the account may bind it
  once the previous binding is revoked.
- After the tenant was revoked and authorized again, assigning the address back to its holder (or the
  same shared mailbox) restores the existing mailbox under the new tenant, under the same rules.

A mailbox assigned from a tenant, personal or shared, is read-only whatever the permission: sending
from a connected mailbox through its own SMTP server is only for an IMAP account bound with
`smtp_host`, and a tenant-assigned mailbox never sends even if its record says otherwise.

A personal assignment creates a `personal` + `connected` mailbox owned by the member with
`created` and `connection_bound` events naming the tenant. It is private to that member exactly like
a self-bound one, never counts toward their 10 own connections, and follows the leave rule in
[Departure And Handover](INBOX.md#departure-and-handover). The member cannot unbind, reconnect, or purge it
(`403 PERMISSION.DENIED`; a reconnect with its `connection_id` is
`422 MAILBOX.EXTERNAL_AUTHORIZATION_INVALID`); the member and owners and admins read its sync runs,
which carry counts and codes only, never message content or headers, and owners and admins purge it
once disconnected, unless it awaits handover, and cannot self-bind an assigned address
(`409 MAILBOX.EXTERNAL_ACCOUNT_TAKEN`). A shared assignment follows
[Shared Connected Mailboxes](#shared-connected-mailboxes) and counts toward the organization's shared
limit. Owners and admins unassign one mailbox with `DELETE /v1/external-mail-connections/:id` or the
connection directory route (`connection_force_disconnected`, or `connection_revoked` with an audit
`reason` `shared_disconnect` for a shared one).

When a mailbox token is refused, Zmail checks the tenant credential: if it is refused too, the tenant
becomes `needs_reauthorization` and every `active` assignment `needs_reconnect` in one statement, so
all sync stops at once; otherwise only that assignment becomes `needs_reconnect` (for example a
Microsoft mailbox outside the application's RBAC scope, or a deleted Google user). Every
authorization, re-authorization, revocation, and created or restored assignment is written to the
organization audit log (resources `external_mail_tenants` and `external_mail_connections`,
`reason` `tenant_authorized`, `tenant_revoked`, or `tenant_assignment`, with the tenant id), and
assignment and revocation events to the mailbox access history; neither ever carries a key, secret,
token, or credential.
