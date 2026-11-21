# Mail Connectors and Agent Handoff: Implementation Plan

Requirement: [PRODUCT_REQUIREMENTS.zh-CN.md](../PRODUCT_REQUIREMENTS.zh-CN.md) R2 (unified inbox
across Zmail, Gmail, and Outlook) and R4 (agents read authorized mail and act on it), roadmap
stages C and D. Builds on the offline slice of
[external-accounts.zh-CN.md](external-accounts.zh-CN.md) §3 and
[external-mail-offline.plan.md](external-mail-offline.plan.md), which shipped the
`ExternalMailSource` seam, the fake source, the sync worker, list, and revoke.

Owner: the `mailbox` starter (API) owns every rule and record. `contracts/` joins it to `web/`.

## 1. Executive Summary

Zmail's value in the AI era is one trustworthy place where every mailbox a person or team runs —
Zmail addresses, Gmail, Outlook, and later any IMAP or API mail service — lands as one stream that
people and authorized agents can act on. That needs three layers, each independent of the
provider:

| Layer | Responsibility | Provider-specific part |
|---|---|---|
| **Connector** | Authorize, fetch with a cursor, revoke | One adapter file per provider |
| **Ingest** | Store a message once per mailbox, thread it, run the post-store pipeline | None |
| **Handoff** | Triage, webhooks, and owner-granted agent access from the same pipeline | None |

Adding a provider is then one adapter plus one registry line. No migration, contract change, or UI
change is needed, because the provider list is data.

## 2. What Exists and What Blocks Scale

| Existing | Gap this plan closes |
|---|---|
| `ExternalMailSource` interface, `disabled` and `fake` sources | One source per instance; the sync cannot pick a source by connection |
| `provider` and `mail_messages.source` CHECKs list `gmail` and `outlook` | Every new provider needs a migration |
| External mailbox stores the outside address in `mailboxes.address`, globally unique, also used by inbound delivery lookup | A second member binding the same account, or a rebind after deletion, collides; a signed delivery to that address could land in a private external mailbox |
| Sync stores copies but skips triage and the post-store pipeline | Synced mail never reaches AI triage or agents |
| No bind or reconnect endpoint | Status `needs_reconnect` has no way back |

## 3. Rulings

| # | Ruling |
|---|---|
| C1 | **Connector registry.** `ExternalMailConnector` = descriptor (`ID`, `AuthKind` `oauth2`, `Capabilities{Read}`) + `ExternalMailSource`. The registry is built once from `EXTERNAL_MAIL_PROVIDERS`. `fake` registers the fake source under both `gmail` and `outlook`. `gmail` and `outlook` themselves stay refused until slice 2 adds their adapters. The sync resolves the source per connection; a connection whose provider is not registered keeps its state and records `external.provider_unavailable`. |
| C2 | **Provider ids are data.** Migration `2026_12_03_090000_open_mail_connectors` replaces the list CHECKs on `external_mail_connections.provider` and `mail_messages.source` with the pattern `^[a-z][a-z0-9_]{1,15}$` (source CHECK added `NOT VALID`, no scan). The contract types `source` and `provider` as strings with known values documented; clients render unknown values generically. |
| C3 | **External addresses leave the delivery namespace.** The same migration replaces `idx_mailboxes_address` with a unique index `WHERE type <> 'external'`. `FindMailboxByAddress` excludes external mailboxes, so inbound delivery can never reach one. Uniqueness of an outside account stays on the connection: `(organization_id, provider, lower(account_address))`. |
| C4 | **Authorization is single use, short lived, and PKCE bound.** `POST /v1/external-mail-authorizations` stores a row in `external_mail_authorizations` with the SHA-256 of a 32-byte random `state`, the sealed PKCE verifier, the caller, organization, provider, optional reconnect `connection_id`, backfill days, and a 10-minute expiry. It answers the provider authorization URL. `POST /v1/external-mail-authorizations/completions` with `{state, code}` deletes the row in the same transaction it reads it, so a replay finds nothing. A state of another user, another organization, expired, or unknown answers `404 MAILBOX.EXTERNAL_AUTHORIZATION_NOT_FOUND`. |
| C5 | **One completion rule covers bind, reconnect, and rebind.** After the code exchange, the account is looked up by `(organization, provider, lower(address))`: none → create the external mailbox (owner = caller, read-only) and an active connection, respecting `MaxExternalMailConnectionsPerUser` (`409 MAILBOX.EXTERNAL_CONNECTION_LIMIT`); the caller's own connection in any state → seal the new token, set `active`, clear the error, sync now, keep the mailbox and cursor (a revoked one also gets its cursor cleared for a fresh backfill), so history is never duplicated (`external_id` stays unique per mailbox); another member's connection → `409 MAILBOX.EXTERNAL_ACCOUNT_TAKEN`. A reconnect started for connection N that signs in to a different account answers `409 MAILBOX.EXTERNAL_ACCOUNT_MISMATCH` and changes nothing. A refused code answers `422 MAILBOX.EXTERNAL_AUTHORIZATION_FAILED`. |
| C6 | **One ingest pipeline.** `afterInboundStored(tx, mailbox, message)` runs for every newly stored inbound message, whatever its source, inside the storing transaction: triage enqueue, then the notifications (`mail.message.received`; webhook filtering already drops non-shared mailboxes). `ReceiveInbound` and the sync both call it. The agent feed of slice 3 attaches here and nowhere else. |
| C7 | **Discovery.** `GET /v1/external-mail-connectors` lists the registered providers (`id`, `auth_kind`, `capabilities`) so the Web console renders one Connect button per provider without knowing them in advance. Empty when the feature is off. |
| C8 | **Privacy defaults hold.** External mailboxes stay private and read-only (ruling R5 of the offline plan). Organization owners and admins can revoke for offboarding but never read. Nothing in this plan publishes private mail to organization webhooks. |

## 4. Agent Handoff (slice 3, design for review)

Agents get mail through **mail agent grants**, never through organization webhooks, because most
external mail is private.

| Concept | Rule |
|---|---|
| Grant | A mailbox reader grants an agent (an API key of a user, tagged as an agent) `read` on chosen mailboxes, optional sender or label filters, an expiry (max 90 days), and optionally `draft` (create reply drafts, never send). Shared mailboxes need `manage`; personal and external mailboxes only their owner. |
| Feed | `GET /v1/agent-mail-events?after=<cursor>` returns new messages of granted mailboxes in arrival order with a durable cursor, plus an optional signed push to the agent's URL from the same pipeline (C6). |
| Safety | Bodies are delivered as data with a `secrets_masked` flag (verification codes stay masked unless the grant says otherwise); a grant cannot be widened by mail content; every read and draft is audited with grant id; revoking or expiry stops the feed at once. |
| Tasks | The feed is the input to agent tasks (triage → todo → draft → human approval → send). Sending stays a separate permission held by a person. |

Open decisions for slice 3 are in §8.

## 5. Delivery Slices

The product roadmap, role adaptation, and interaction design for these slices are in
[mail-connectors-roadmap.zh-CN.md](mail-connectors-roadmap.zh-CN.md); that document renumbers
them as iterations C1–C7 and D1–D2.

| Slice | Content | External dependency |
|---|---|---|
| **1 (this PR)** | C1–C7: registry, open provider ids, address namespace fix, authorization and completion endpoints, provider discovery, unified ingest, tests, contract | None |
| 2 | Gmail (`gmail.readonly`, history API) and Outlook (Graph `Mail.Read`, delta query) adapters over plain HTTPS with `httptest` tests; config of client ids and secrets | OAuth apps to go live; code and tests need none |
| 3 | Mail agent grants and feed (§4) | Product decisions §8 |
| 4 | Web: provider buttons, callback route, connection status, reconnect and revoke, source badge for any provider | None |
| 5 | Generic IMAP connector (app passwords: iCloud, Yahoo, QQ, 163, Fastmail, corporate servers) | A vetted IMAP dependency |

## 6. Tests (slice 1)

PostgreSQL tests with the fake source: bind creates a private read-only mailbox and an active
connection; the state is single use and bound to its user and organization; expiry; reconnect
restores `active` and keeps history without duplicates; rebind after revoke reuses the mailbox;
reconnect to a different account is refused; another member's account is refused; the per-user
limit; an unregistered provider is refused at start; inbound delivery never resolves an external
mailbox; synced mail is queued for triage; connector discovery is empty when off; only the owner may switch triage on for an external mailbox. Migration Up/Down
and golden schema.

## 7. Observability

Logs `external_mail.authorization_started`, `external_mail.bound`, `external_mail.reconnected`,
and the existing sync logs, each with connection id and provider, never tokens, codes, or states.

## 8. Decisions Needed

1. Slice 3: may an agent read verification codes in clear text when its grant says so, or are
   codes always masked for agents?
2. Slice 3: is the agent identity an API key of a person (simplest, inherits that person's
   access ceiling) or a separate organization-owned agent principal?
3. Slice 5: accept an IMAP library dependency (reviewed per DEPENDENCY_SECURITY.md), so that
   providers without OAuth can be connected with app passwords?
4. Still open from external-accounts §9: the OAuth apps, the callback host, and how long history
   stays after a revoke.
