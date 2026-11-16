# Mailbox — Implementation Plan

The first product capability of Zmail: user-owned mailboxes that send through Cloudflare Email
Service and receive from Cloudflare Email Routing. Written to
[BUSINESS_IMPLEMENTATION_PLAN_STANDARD.md](../BUSINESS_IMPLEMENTATION_PLAN_STANDARD.md). Public HTTP
behavior is owned by [contracts/MAILBOXES.md](../../contracts/MAILBOXES.md); this plan records why
it is shaped that way.

## 1. Executive Summary

**Outcome.** A signed-in user claims addresses on the deployment's mail domain, reads mail sent to
them, and sends mail from them, through the Web console or the HTTP API.

**Problem.** Cloudflare offers inbound routing and outbound sending as separate primitives with no
mailbox between them. Its reference client, Agentic Inbox, shows the pattern but reports a message
as sent before the provider accepts it, has no per-mailbox authorization, and stores everything in
Durable Objects that a Go and PostgreSQL product cannot query.

**In scope.** Mailboxes on one configured domain; sending text and HTML with reply threading;
signed inbound delivery with duplicate suppression; inbox and sent listings; read state; deletion;
a Web console page; a Cloudflare Email Worker; a development transport that needs no Cloudflare
account.

**Out of scope for this slice.** Attachments, organization-shared mailboxes, several domains,
background retry of failed sends, search, spam filtering, bounce and
complaint ingestion, and an Admin Console view. Each is a later slice, not a hidden gap: section 13
and the contract say what a caller must not assume.

## 2. Actors and Access Surfaces

| Actor | Surface | May |
|---|---|---|
| Signed-in user | Web console `/console/mailboxes`; `/v1/mailboxes...` with a session token | Manage only their own mailboxes and messages |
| Backend service | `/v1/outbound-mail-messages` with a user's API key | Send from that user's mailboxes (`mail:send`) and read what was sent (`mail:read`) |
| Mail edge (Cloudflare Email Worker) | `POST /v1/inbound-mail-messages` with an HMAC signature | Deliver one raw message to an existing address |
| Mail provider (Cloudflare Email Sending) | Called by the API | Accept or reject an outbound message |
| Operator | Environment configuration, Cloudflare dashboard | Choose the domain, provider credentials, inbound secret, and send limit |

Anonymous callers have no access. Operators have no mailbox-reading route on purpose.

## 3. End-to-End Workflows

1. **Claim an address.** The user submits a local part. The API validates and lowercases it,
   refuses role addresses, and inserts `<local>@<domain>` under a per-user lock that enforces the
   10-mailbox limit. A taken address, including one whose mailbox was deleted, is refused.
2. **Send.** The API validates recipients, subject, and body, checks the rolling 24-hour limit,
   stores the message as `queued`, calls the provider with a bounded timeout, then records `sent` or
   `failed` with a stable reason. The HTTP answer reflects the recorded outcome.
3. **Receive.** Cloudflare Email Routing hands the message to the Worker. The Worker signs
   `<timestamp>.<recipient>.<raw>` and posts the raw message. The API verifies the signature, finds
   the mailbox, parses the message, and stores it once. The Worker turns `404`, `413`, and `422`
   into a bounce and anything else into a temporary failure, so the sending server retries.
4. **Read.** Listing returns summaries without bodies. Opening a message returns its bodies; the
   console marks it read and renders HTML only inside a script-free sandbox.
5. **Delete.** Deleting a message removes it. Deleting a mailbox removes its messages in the same
   transaction and keeps the address reserved.

## 4. Module Decomposition

| Owner | Owns |
|---|---|
| `api/internal/modules/mailbox` (optional starter `mailbox`) | Mailbox and message rules, tables, user routes, the inbound route, signature verification, MIME parsing, error codes |
| `api/internal/infra/mailtransport` | Provider adapters: Cloudflare Email Sending REST and a development log transport |
| `api/internal/infra/config` | Typed `MailboxConfig` and its startup validation |
| `deploy/cloudflare/email-worker` | The mail edge: signing and forwarding, bounce versus retry |
| `web/src/features/mailbox` | Browser routes (API adapter and mock BFF), the console page |

The module depends on the `user` and `audit` starters only. Nothing else writes its tables.

## 5. Domain Model and Vocabulary

- **Mailbox**: one address owned by exactly one user.
- **Mail message**: one stored message in one mailbox. `direction` is `inbound` or `outbound`.
- **Mail edge**: the component that receives SMTP on the product's behalf and calls the API.
- **Mail transport**: the adapter that hands an outbound message to a provider.

Invariants: an address is globally unique forever; a message belongs to one mailbox; the sender of
an outbound message is always its mailbox; an inbound message is stored at most once per mailbox.

## 6. Persistence Design

Migration `2026_11_16_100000_create_mailbox_tables` (frozen SQL).

- `mailboxes(id, user_id → users RESTRICT, address, display_name, timestamps, deleted_at)`.
  `address` has a unique index that includes soft-deleted rows, which is what keeps a released
  address reserved. A partial index on `user_id` serves listing.
- `mail_messages(id, mailbox_id → mailboxes CASCADE, direction, status, from_address, from_name,
  to_addresses, cc_addresses, subject, text_body, html_body, message_id, in_reply_to, dedupe_key,
  failure_code, read_at, timestamps)` with check constraints on `direction` and `status`.
  `(mailbox_id, direction, id DESC)` serves both folders. A unique partial index on
  `(mailbox_id, dedupe_key)` makes inbound delivery idempotent.

Recipients are JSON arrays in text columns: they are read and written whole and never queried.
Bodies are capped at 256 KiB each, so a row stays bounded. Messages are hard-deleted. Account
deletion soft-deletes the user's mailboxes and deletes their messages inside the account
transaction, through the `user` starter's account deletion policy.

## 7. State Machines

Outbound `status`: `queued → sent` or `queued → failed`. Both are terminal. A row left `queued`
means the process stopped between the provider call and the status write; the outcome is unknown
and is shown as such. Inbound `status` is always `received`. Read state toggles freely.

## 8. HTTP Contract Design

Ten routes, listed with their rules and error codes in `contracts/MAILBOXES.md` and
`contracts/openapi.yaml`. Decisions worth recording:

- A send that the provider rejects answers `502 MAILBOX.DELIVERY_FAILED` and keeps the message as
  `failed`. Answering `201` or `202` first and delivering in the background would repeat the
  reference client's defect of showing undelivered mail as sent.
- Another user's mailbox answers `404`, never `403`, so identifiers do not disclose existence.
- The inbound route verifies the signature before looking up the recipient for the same reason.
- Mailbox and domain lists are bounded (10 and 1) and therefore not paginated; message lists are.

## 9. Authorization Matrix

| Operation | Owner | Other user | Anonymous | Mail edge |
|---|---|---|---|---|
| List domains, list or create mailboxes | Yes | Own data only | 401 | n/a |
| Delete mailbox; list, read, send, update, delete messages | Yes | 404 | 401 | n/a |
| Inbound delivery | n/a | n/a | 401 | Valid signature only |

Every repository query that takes a mailbox id also takes the owner id or a mailbox id the service
already resolved for that owner.

## 10. Cross-Module Data Flow

`mailbox` reads the authenticated user id set by the `user` starter's session middleware and
references `users(id)`. When selected it registers an account deletion cleaner with the `user`
starter, which runs inside the account transaction. It publishes no events and consumes none. Write requests are recorded by the
`audit` starter's request auditing, which stores no request bodies.

## 11. Scheduled and Asynchronous Work

None. Sending is synchronous inside the request with a provider timeout (default 15 seconds). The
outcome write uses a context detached from the request, bounded to 5 seconds, so a client that
disconnects mid-send cannot leave a delivered message looking queued.

## 12. Browser-Surface Plan

One console page, `/console/mailboxes`, gated by the `mailbox` Web feature: mailbox list with
create and delete, inbox and sent folders, message detail, compose and reply. It follows the
plugin-feature convention checked by the Web guard tests. Sender HTML is rendered only in an
`iframe` with an empty `sandbox` attribute. There is no Admin Console view.

## 13. Security, Privacy, and Audit

- **Inbound authenticity.** HMAC-SHA256 with a shared secret of at least 32 characters, compared in
  constant time, with a five-minute timestamp window that bounds replay. An empty secret disables
  the route (`503`).
- **Header injection.** Recipients must be bare addresses; subjects may not contain line breaks;
  the `from` address is never client-supplied; `Message-ID` is left to the provider.
- **Address takeover.** Role addresses are reserved and released addresses are never reissued.
- **Untrusted content.** Inbound HTML is stored as received and marked untrusted in the contract.
- **Provider text.** Only short dotted provider reasons are stored as `failure_code`; free-form
  provider text is replaced by `provider.http_<status>`.
- **Logs.** The log transport records counts and sizes, never subjects or bodies.
- **Abuse.** A per-user rolling 24-hour send limit (default 200) counts failed attempts too.
- **Production guard.** Startup fails in production unless the domain is explicit and the provider
  is `cloudflare`, so a deployment cannot silently pretend to deliver.

Known limits, stated rather than hidden: the send limit is a soft bound under concurrency; a
`502` retried without an `Idempotency-Key` can duplicate a message whose first attempt reached the
provider; there is no spam or malware filtering beyond the provider's.

## 14. Observability and Operations

Requests carry the scaffold's request id, metrics, and tracing. Failed sends are visible per message
through `status` and `failure_code`. Operations are documented in
`deploy/cloudflare/email-worker/README.md` and `api/docs/CONFIGURATION.md`.

## 15. Test and Verification Strategy

| Layer | Proof |
|---|---|
| Address rules, MIME parsing, signature | Unit tests, including GBK mail and a signature vector shared with the Worker tests |
| Service | In-memory repository and transport: ownership, validation, send outcomes, limit, inbound paths |
| Repository | Disposable PostgreSQL: limit under lock, reserved addresses, idempotent inbound insert, listing without bodies, cascade, account cleanup inside a rolled-back transaction |
| Transport | `httptest` server: request shape and every failure mapping |
| Configuration | Validation table for production and development |
| Migration | Golden schema and rollback tests |
| Contract | OpenAPI lint, generated types, runtime route comparison |
| Worker | `node --test`: signing, bounce versus retry |
| Web | Route, service, UI, and mock-parity tests |
| End to end | A running API with real migrations, driven through the real Worker code |

## 16. Delivery Plan

1. API module, transport, configuration, migration, contract, Worker (this slice).
2. Web console page (this slice).
3. Programmatic sending (second slice): `POST /v1/outbound-mail-messages` and its status lookup
   behind API keys with the `mail:send` and `mail:read` scopes, and an `Idempotency-Key` on both
   send routes. A key replays the first attempt's outcome, including a failure, because the provider
   may have accepted a message whose answer was lost; migration
   `2026_11_16_110000_add_mail_message_idempotency` adds the key, a request fingerprint, and a
   unique partial index per mailbox that also settles two concurrent requests with one key.
4. Delivery events (third slice): the send call stores the provider's message id and what it
   reported for each recipient; a `mail_message_events` table keeps the history, and
   `mail_messages.last_event` summarizes it for listings as the most severe current state among
   the recipients. Later events arrive from a Cloudflare Queues event subscription through the
   Email Worker's queue consumer and `POST /v1/mail-delivery-events`, signed with the inbound
   secret under a separate scope and idempotent by the provider's event id. Only identifiers,
   status codes, and classifications are stored; provider free text is dropped. Opens and clicks
   are out of scope.
5. Attachments (fifth slice): up to 10 files and 4 MiB per message, sent as base64 and parsed from
   inbound MIME. Migration `2026_11_17_110000_create_mail_attachments` stores the bytes in
   PostgreSQL (`bytea`) with the message rather than in object storage through the `asset`
   starter: the files are small and bounded, they must be deleted with their message, and the
   mailbox starter keeps no dependency on object storage. Downloads are always opaque
   (`application/octet-stream`, `attachment`, `nosniff`); the declared type is untrusted. Moving
   larger files to object storage is a later decision with its own plan.
6. Later slices, each with its own plan: background retry on the PostgreSQL job queue; bounce and complaint ingestion; organization-shared
   mailboxes; search.
