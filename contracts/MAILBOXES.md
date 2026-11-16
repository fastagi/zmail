# Mailbox Contract

This contract owns the optional `mailbox` starter across the Go API, the Web mailbox feature, the
fixed production API adapter, the development mock BFF, and the Cloudflare Email Worker that
delivers inbound mail. It defines mailboxes owned by an organization on one configured domain,
sending through a mail provider from a session or an API key, and signed inbound delivery. It does
not define attachments, per-mailbox access rules, custom domains per organization, mailing lists,
or delivery webhooks.

## Activation

The API registers these routes only when `OPTIONAL_STARTERS` contains `organization` and
`mailbox`; `mailbox` alone is refused at startup. The Web feature is active only when
`NEXT_PUBLIC_OPTIONAL_FEATURES` contains `organization` and `mailbox`. Configuration keys are
listed in [`api/docs/CONFIGURATION.md`](../api/docs/CONFIGURATION.md).

## Ownership And Paths

Mailboxes and their messages belong to an organization. Every request names the organization
with the `Organization-Id` header defined in [`ORGANIZATIONS.md`](ORGANIZATIONS.md), and the API
resolves the caller's membership of it on each request: a missing header is
`400 ORGANIZATION.CONTEXT_REQUIRED`, a malformed one `400 ORGANIZATION.CONTEXT_INVALID`, and an
organization the caller does not belong to is `404 ORGANIZATION.NOT_FOUND`, exactly like one that
does not exist. A mailbox or message of another organization is likewise reported as missing.

| Role | May |
|---|---|
| `member` | List domains and mailboxes; list, read, send, mark read or unread, and delete messages; read the log and statistics |
| `owner`, `admin` | Everything a member may, and create and delete mailboxes |

A member who creates or deletes a mailbox gets `403 PERMISSION.DENIED`. The user who created a
mailbox is recorded but has no special rights to it; removing that user from the organization
changes nothing about the mailbox. A removed member loses access on their next request. Read state
is shared: a message one member reads is read for the whole organization.

| Operation | Browser route | Go API route | Success |
|---|---|---|---|
| List domains | `GET /api/mailbox-domains` | `GET /v1/mailbox-domains` | `200` envelope, at most 1 item |
| List mailboxes | `GET /api/mailboxes` | `GET /v1/mailboxes` | `200` envelope, at most 50 items |
| Create mailbox | `POST /api/mailboxes` | `POST /v1/mailboxes` | `201` envelope |
| Delete mailbox | `DELETE /api/mailboxes/:id` | `DELETE /v1/mailboxes/:mailbox_id` | `204` |
| List messages | `GET /api/mailboxes/:id/messages` | `GET /v1/mailboxes/:mailbox_id/messages` | Paginated envelope |
| Send message | `POST /api/mailboxes/:id/messages` | `POST /v1/mailboxes/:mailbox_id/messages` | `201` envelope |
| Get message | `GET /api/mailboxes/:id/messages/:messageId` | `GET /v1/mailboxes/:mailbox_id/messages/:message_id` | `200` envelope |
| Mark read or unread | `PUT /api/mailboxes/:id/messages/:messageId` | `PUT /v1/mailboxes/:mailbox_id/messages/:message_id` | `200` envelope |
| Delete message | `DELETE /api/mailboxes/:id/messages/:messageId` | `DELETE /v1/mailboxes/:mailbox_id/messages/:message_id` | `204` |
| List the log | `GET /api/mail-messages` | `GET /v1/mail-messages` | Paginated envelope |
| Per-day statistics | `GET /api/mail-message-stats` | `GET /v1/mail-message-stats` | `200` envelope, at most 90 items |
| Send with an API key | none | `POST /v1/outbound-mail-messages` | `201`, or `200` for a repeat |
| Get a sent message with an API key | none | `GET /v1/outbound-mail-messages/:message_id` | `200` envelope |
| Inbound delivery | none | `POST /v1/inbound-mail-messages` | `201`, or `200` for a repeat |
| Delivery event | none | `POST /v1/mail-delivery-events` | `201`, or `200` for a repeat |

The Web production API adapter owns the HttpOnly API session credential and forwards only these
fixed relative paths together with the `Organization-Id` header. Every member response is
`Cache-Control: private, no-store` and varies on `Authorization` and `Organization-Id`. The inbound route
has no browser route: only the mail edge calls it.

## Mailboxes

`POST /v1/mailboxes`:

```json
{ "local_part": "hello", "display_name": "Hello Team" }
```

- `local_part` is trimmed and lowercased. It is 1 to 64 characters of lowercase ASCII letters and
  digits, with single inner `.`, `_`, or `-`. Anything else is `422 MAILBOX.ADDRESS_INVALID`.
- Role addresses (`abuse`, `admin`, `administrator`, `hostmaster`, `mailer-daemon`, `no-reply`,
  `noreply`, `postmaster`, `root`, `security`, `support`, `webmaster`) are
  `422 MAILBOX.ADDRESS_RESERVED`.
- `display_name` is optional, at most 120 characters, and may not contain `<`, `>`, or `"`; a
  violation is also `422 MAILBOX.ADDRESS_INVALID`.
- The address is `<local_part>@<configured domain>`. `GET /v1/mailbox-domains` returns that domain
  with `max_mailboxes` (per organization), the outbound `provider` (`cloudflare` delivers; `log` only records and is
  for development), `inbound_enabled`, and `daily_send_limit` (per organization).
- An address that is or ever was a mailbox is
  `409 MAILBOX.ADDRESS_TAKEN`: deleting a mailbox keeps its address reserved, so a later owner
  never receives mail meant for the previous one.
- An organization owns at most 50 mailboxes; one more is `409 MAILBOX.LIMIT_REACHED`.

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "id": 12,
    "address": "hello@mail.example.com",
    "display_name": "Hello Team",
    "unread_count": 0,
    "created_at": "2026-11-16T10:00:00Z"
  }
}
```

`unread_count` is the number of unread inbound messages in the mailbox; sent messages never
count.

Deleting a mailbox deletes its messages in the same transaction; the address stays reserved and
later mail for it is answered `404 MAILBOX.RECIPIENT_NOT_FOUND`. Deleting a user account does not
touch mailboxes: the organization starter already refuses to delete an account that still belongs
to an organization.

## Messages

Listing returns summaries, newest first, without bodies. `direction=inbound` is the inbox and
`direction=outbound` is the sent folder; any other value is `400 COMMON.INVALID_INPUT`.

| Field | Meaning |
|---|---|
| `direction` | `inbound` or `outbound` |
| `status` | `received` for inbound. Outbound is `queued` while the provider call is in flight, then `sent` or `failed`. |
| `from` | `{ "address", "name" }` |
| `to`, `cc` | Lowercase addresses |
| `read` | Inbound messages start unread; outbound messages start read. |
| `failure_code` | Stable provider or transport reason when `status` is `failed`, otherwise empty |
| `last_event` | Summary of the delivery events of an outbound message; see [Delivery Events](#delivery-events). Empty when none are recorded. |

The detail response adds `text_body`, `html_body`, `message_id`, `in_reply_to`, and `events`, the
delivery history of an outbound message. `html_body` is
untrusted sender content: a client must render it only inside a sandbox that runs no script and
loads nothing with the user's credentials.

### Send

```json
{
  "to": ["bob@example.com"],
  "cc": [],
  "subject": "Hello",
  "text": "Plain body",
  "html": "<p>HTML body</p>",
  "in_reply_to": "<previous-id@example.com>"
}
```

- `to` has 1 to 50 bare addresses; `to` and `cc` together have at most 50 after duplicates are
  removed. Addresses are lowercased. Display-name forms are rejected.
- `subject` is required, at most 998 characters, with no line breaks.
- At least one of `text` and `html` is required; each is at most 256 KiB.
- `in_reply_to` is an optional Message-ID. It is sent as the `In-Reply-To` and `References` headers.
- Any of these violations is `422 MAILBOX.MESSAGE_INVALID`.
- The sender is always the mailbox address and display name; a request cannot set `from`.

The API stores the message as `queued`, calls the provider, and records the real outcome before it
answers:

| Outcome | Response | Stored status |
|---|---|---|
| Provider accepted | `201` with the message | `sent` |
| Provider rejected or was unreachable | `502 MAILBOX.DELIVERY_FAILED` | `failed`, with `failure_code` |
| No provider configured | `503 MAILBOX.SENDER_NOT_CONFIGURED` | `failed` |
| Organization's rolling 24-hour limit reached | `429 MAILBOX.SEND_LIMIT_REACHED` | nothing stored |

A send is never reported as successful before the provider accepts it. The send limit is counted
per organization, and failed attempts count toward it. The limit is a soft bound: concurrent requests may exceed it by the number of
requests in flight. There is no automatic retry. A client that retries a `502` without an
`Idempotency-Key` may send a duplicate when the first attempt reached the provider but the answer
was lost (`failure_code` `transport.unreachable`); see [Idempotency](#idempotency).

### Log And Statistics

`GET /v1/mail-messages` lists messages across every mailbox of the organization, newest first, as
summaries with the owning `mailbox_address` and without bodies. Read a message through
`GET /v1/mailboxes/:mailbox_id/messages/:message_id`.

| Query | Meaning |
|---|---|
| `direction` | `inbound` or `outbound` |
| `status` | `received`, `queued`, `sent`, or `failed` |
| `mailbox_id` | One mailbox. Another organization's mailbox matches nothing. |
| `q` | Case-insensitive match on the subject, the sender address, or a recipient address; at most 200 characters. `%` and `_` match literally. |
| `page`, `per_page` | Pagination |

An unknown `direction` or `status`, a non-positive `mailbox_id`, or an over-long `q` is
`400 COMMON.INVALID_INPUT`. Messages of deleted mailboxes do not appear.

`GET /v1/mail-message-stats?days=N` returns one entry per UTC day for the last `N` days ending
today, oldest first, including days without mail. `N` defaults to 7 and is 1 to 90.

```json
{ "day": "2026-11-16", "sent": 3, "failed": 1, "received": 2 }
```

`sent` and `failed` count outbound messages by their final status; a message still `queued` is in
neither. `received` counts inbound messages.

### Idempotency

Both send routes accept an optional `Idempotency-Key` header of 1 to 255 printable ASCII
characters; anything else is `422 MAILBOX.IDEMPOTENCY_KEY_INVALID`. The key names one send within
one mailbox. A later send in the same mailbox with the same key never calls the provider again:

| First attempt | Repeat with the same content | Repeat with different content |
|---|---|---|
| `sent` | `200` with the stored message | `409 MAILBOX.IDEMPOTENCY_CONFLICT` |
| `failed` | The same error as the first attempt, with the stored message kept | `409 MAILBOX.IDEMPOTENCY_CONFLICT` |
| Still `queued` | `409 MAILBOX.SEND_IN_PROGRESS` | `409 MAILBOX.IDEMPOTENCY_CONFLICT` |

Content is compared after normalization, so address case, duplicate recipients, and surrounding
whitespace in the subject do not matter. A failed attempt is replayed rather than retried because
the provider may have accepted it before the answer was lost; to send again on purpose, use a new
key. Without a key, every request is a new message.

## Programmatic Sending

Backend services send with an API key instead of a browser session. Create the key with
`POST /v1/api-keys` (see [`API_KEYS.md`](API_KEYS.md)) and pass it as `X-API-Key`, together with
the `Organization-Id` header. A key belongs to a user, and it acts with that user's current
membership of the named organization: the same header errors apply, and removing the user from the
organization stops the key from working there on the next request. Keys owned by an organization
are not defined.

| Route | Required scope |
|---|---|
| `POST /v1/outbound-mail-messages` | `mail:send` |
| `GET /v1/outbound-mail-messages/:message_id` | `mail:read` |

```json
{
  "from": "hello@mail.example.com",
  "to": ["bob@example.com"],
  "subject": "Your receipt",
  "text": "Thanks for your order."
}
```

`from` must be the bare address of a mailbox of that organization; any other address, including
another organization's mailbox, is `404 MAILBOX.NOT_FOUND`. The remaining fields, limits, outcomes, and
`Idempotency-Key` behavior are those of [Send](#send). A key without the scope gets
`403 PERMISSION.DENIED`. The read route returns only outbound messages, so a `mail:read` key cannot
read mail that was received. Both routes are limited to the organization in the header.

## Delivery Events

`status` says whether the provider took a message. What happened to it afterwards is recorded as
events, one per recipient:

| Event | Meaning |
|---|---|
| `accepted` | The provider took the message. Recorded once per message, with no recipient. |
| `delivered` | The recipient's mail server accepted the message. |
| `deferred` | A temporary failure; the provider is still retrying. |
| `bounced` | The recipient's mail server refused the message for good. |
| `failed` | The provider gave up without a bounce. |
| `rejected` | The provider did not attempt the recipient, for example a suppressed address. |
| `complained` | The recipient reported the message as spam. |

Each event has `type`, `recipient`, `occurred_at`, and `detail`. `detail` holds short
machine-readable tokens such as `5.1.1 permanent_failure` or `suppressed`. Free-form provider text,
including the SMTP response line, is never stored or returned.

Events come from two places. The send call records `accepted` and whatever the provider reported
for each recipient at once (`delivered`, `bounced`, `rejected`). Later events arrive from the mail
edge through `POST /v1/mail-delivery-events`.

`last_event` is the most severe current state among the recipients, where a recipient's current
state is its latest event: `complained`, then `bounced`, `failed`, `rejected`, `deferred`,
`delivered`. With no recipient event it is `accepted` when the provider took the message. So a
message to two recipients, one delivered and one bounced, shows `bounced`; a recipient deferred
and then delivered counts as `delivered`.

### Webhooks

When the `webhook` starter is selected, an organization's webhook endpoints can subscribe to mail
events. Endpoints, signing, retries, and delivery history are those of [WEBHOOKS.md](WEBHOOKS.md);
this section defines only the mail events.

| Event type | Published when |
|---|---|
| `mail.message.sent` | The provider took an outbound message. |
| `mail.message.failed` | The provider refused an outbound message at send time, or later gave up on a recipient without a bounce. |
| `mail.message.received` | An inbound message was stored in a mailbox. |
| `mail.message.delivered` | A recipient's mail server accepted the message. |
| `mail.message.delivery_delayed` | A temporary failure; the provider is still retrying. |
| `mail.message.bounced` | A recipient's mail server refused the message for good. |
| `mail.message.complained` | A recipient reported the message as spam. |
| `mail.message.suppressed` | The provider did not attempt a recipient on its suppression list. |

Every mail event has the same `data` object, with every field always present:

| Field | Value |
|---|---|
| `message_id` | The message's id, as used by the message routes. |
| `mailbox_id` | The mailbox that holds the message. |
| `mailbox_address` | That mailbox's address. |
| `direction` | `outbound` or `inbound`. |
| `from` | The sender address. |
| `to` | The `to` recipients, possibly empty. |
| `recipient` | The address a per-recipient event is about; empty for `sent`, `received`, and a send-time `failed`. |
| `reason` | A short machine-readable code such as `5.1.1 permanent_failure`, `suppressed`, or a send failure code; empty when there is nothing to explain. |

The payload never contains a subject, a body, or free-form provider text. A receiver that needs
the content reads the message through the API with its own credentials.

A webhook is queued in the same database transaction as the state change it reports, so an event
is published exactly when its change is stored, and each change publishes once: a repeated inbound
delivery, a repeated provider event, or an idempotent replay of a send publishes nothing new. A
send to several recipients publishes one `sent` and then one event per recipient outcome.

If the webhook cannot be queued, the mail state is still recorded and the failure is logged; that
one webhook is lost. Mail that was really sent or received is never dropped because of a webhook
failure. Without the `webhook` starter nothing is published.

### Event Intake

`POST /v1/mail-delivery-events` carries one Cloudflare Email Sending event
(`cf.email.sending.message.*`, schema version 1) as JSON, at most 64 KiB, with
`X-Mailbox-Signature: t=<unix seconds>,v1=<hex>`. `v1` is HMAC-SHA256, keyed with
`MAILBOX_INBOUND_SECRET`, over `<t>.delivery-event.<body>`. The fixed `delivery-event` scope takes
the place of the recipient in the inbound signature, so a signed inbound message cannot be replayed
as an event. The same five-minute window applies.

| Condition | Response | Mail edge action |
|---|---|---|
| Recorded | `201` `{ "message_id", "type", "duplicate": false }` | Acknowledge |
| Event already recorded | `200` with `duplicate: true` | Acknowledge |
| Missing, stale, or wrong signature | `401 MAILBOX.INBOUND_SIGNATURE_INVALID` | Retry |
| No outbound message has the event's message id | `404 MAILBOX.MESSAGE_NOT_FOUND` | Acknowledge |
| Not a usable Email Sending event | `422 MAILBOX.DELIVERY_EVENT_INVALID` | Acknowledge |
| No inbound secret configured | `503 MAILBOX.INBOUND_DISABLED` | Retry |

An event is matched to a message by the provider's message id, which the send call stores as
`message_id`, and is idempotent by the provider's event id. Opens and clicks are not tracked.
Messages sent with the development `log` provider have no provider id and never receive events.

## Inbound Delivery

`POST /v1/inbound-mail-messages` carries one raw RFC 5322 message (`Content-Type: message/rfc822`,
at most 5 MiB) and two headers:

| Header | Value |
|---|---|
| `X-Mailbox-Recipient` | The envelope recipient |
| `X-Mailbox-Signature` | `t=<unix seconds>,v1=<hex>` |

`v1` is HMAC-SHA256, keyed with `MAILBOX_INBOUND_SECRET`, over
`<t>.<lowercase recipient>.<raw body>`. The API compares in constant time and rejects a timestamp
more than five minutes from its clock. The signature is checked before the recipient is looked up,
so an unsigned caller cannot learn which addresses exist.

| Condition | Response | Mail edge action |
|---|---|---|
| Stored | `201` `{ "id", "duplicate": false }` | Accept |
| Same message already stored for this mailbox | `200` `{ "id", "duplicate": true }` | Accept |
| Missing, stale, or wrong signature | `401 MAILBOX.INBOUND_SIGNATURE_INVALID` | Temporary failure |
| No mailbox has the recipient address | `404 MAILBOX.RECIPIENT_NOT_FOUND` | Reject (bounce) |
| Larger than 5 MiB | `413 MAILBOX.INBOUND_TOO_LARGE` | Reject (bounce) |
| Not a parseable message with a sender | `422 MAILBOX.INBOUND_UNPARSEABLE` | Reject (bounce) |
| No inbound secret configured | `503 MAILBOX.INBOUND_DISABLED` | Temporary failure |

The inbound route carries no organization header: the recipient address identifies the mailbox,
and the mailbox its organization. A message is identified by its `Message-ID`, or by a hash of its
content when it has none, once per mailbox. The API stores the first `text/plain` and first `text/html` part, converted to UTF-8 and
truncated to 256 KiB each. Attachments are not stored.

## Errors

| Code | Status |
|---|---|
| `MAILBOX.NOT_FOUND` | 404 |
| `MAILBOX.MESSAGE_NOT_FOUND` | 404 |
| `MAILBOX.ADDRESS_TAKEN` | 409 |
| `MAILBOX.LIMIT_REACHED` | 409 |
| `MAILBOX.ADDRESS_INVALID` | 422 |
| `MAILBOX.ADDRESS_RESERVED` | 422 |
| `MAILBOX.MESSAGE_INVALID` | 422 |
| `MAILBOX.IDEMPOTENCY_CONFLICT` | 409 |
| `MAILBOX.SEND_IN_PROGRESS` | 409 |
| `MAILBOX.IDEMPOTENCY_KEY_INVALID` | 422 |
| `MAILBOX.SEND_LIMIT_REACHED` | 429 |
| `MAILBOX.DELIVERY_FAILED` | 502 |
| `MAILBOX.SENDER_NOT_CONFIGURED` | 503 |
| `MAILBOX.INBOUND_SIGNATURE_INVALID` | 401 |
| `MAILBOX.RECIPIENT_NOT_FOUND` | 404 |
| `MAILBOX.INBOUND_TOO_LARGE` | 413 |
| `MAILBOX.INBOUND_UNPARSEABLE` | 422 |
| `MAILBOX.INBOUND_DISABLED` | 503 |
| `MAILBOX.DELIVERY_EVENT_INVALID` | 422 |
