# Mailbox Contract

This contract owns the optional `mailbox` starter across the Go API, the Web mailbox feature, the
fixed production API adapter, the development mock BFF, and the Cloudflare Email Worker that
delivers inbound mail. It defines user-owned mailboxes on one configured domain, sending through a
mail provider from a session or an API key, and signed inbound delivery. It does not define attachments, organization-shared
mailboxes, custom domains per user, mailing lists, or delivery webhooks.

## Activation

The API registers these routes only when `OPTIONAL_STARTERS` contains `mailbox`. The Web feature
is active only when `NEXT_PUBLIC_OPTIONAL_FEATURES` contains `mailbox`. Configuration keys are
listed in [`api/docs/CONFIGURATION.md`](../api/docs/CONFIGURATION.md).

## Ownership And Paths

An authenticated user may manage only their own mailboxes and the messages in them. A mailbox or
message that belongs to another user is reported exactly like one that does not exist.

| Operation | Browser route | Go API route | Success |
|---|---|---|---|
| List domains | `GET /api/mailbox-domains` | `GET /v1/mailbox-domains` | `200` envelope, at most 1 item |
| List mailboxes | `GET /api/mailboxes` | `GET /v1/mailboxes` | `200` envelope, at most 10 items |
| Create mailbox | `POST /api/mailboxes` | `POST /v1/mailboxes` | `201` envelope |
| Delete mailbox | `DELETE /api/mailboxes/:id` | `DELETE /v1/mailboxes/:mailbox_id` | `204` |
| List messages | `GET /api/mailboxes/:id/messages` | `GET /v1/mailboxes/:mailbox_id/messages` | Paginated envelope |
| Send message | `POST /api/mailboxes/:id/messages` | `POST /v1/mailboxes/:mailbox_id/messages` | `201` envelope |
| Get message | `GET /api/mailboxes/:id/messages/:messageId` | `GET /v1/mailboxes/:mailbox_id/messages/:message_id` | `200` envelope |
| Mark read or unread | `PUT /api/mailboxes/:id/messages/:messageId` | `PUT /v1/mailboxes/:mailbox_id/messages/:message_id` | `200` envelope |
| Delete message | `DELETE /api/mailboxes/:id/messages/:messageId` | `DELETE /v1/mailboxes/:mailbox_id/messages/:message_id` | `204` |
| Send with an API key | none | `POST /v1/outbound-mail-messages` | `201`, or `200` for a repeat |
| Get a sent message with an API key | none | `GET /v1/outbound-mail-messages/:message_id` | `200` envelope |
| Inbound delivery | none | `POST /v1/inbound-mail-messages` | `201`, or `200` for a repeat |

The Web production API adapter owns the HttpOnly API session credential and forwards only these
fixed relative paths. Every user response is `Cache-Control: private, no-store`. The inbound route
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
- The address is `<local_part>@<configured domain>`. An address that is or ever was a mailbox is
  `409 MAILBOX.ADDRESS_TAKEN`: deleting a mailbox keeps its address reserved, so a later owner
  never receives mail meant for the previous one.
- A user owns at most 10 mailboxes; one more is `409 MAILBOX.LIMIT_REACHED`.

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "id": 12,
    "address": "hello@mail.example.com",
    "display_name": "Hello Team",
    "created_at": "2026-11-16T10:00:00Z"
  }
}
```

Deleting a mailbox deletes its messages in the same transaction. Deleting the account
(`DELETE /v1/users/account`) does the same for every mailbox the user owns, inside the account
transaction; the addresses stay reserved and later mail for them is answered
`404 MAILBOX.RECIPIENT_NOT_FOUND`.

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

The detail response adds `text_body`, `html_body`, `message_id`, and `in_reply_to`. `html_body` is
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
| Rolling 24-hour limit reached | `429 MAILBOX.SEND_LIMIT_REACHED` | nothing stored |

A send is never reported as successful before the provider accepts it. Failed attempts count toward
the send limit. The limit is a soft bound: concurrent requests may exceed it by the number of
requests in flight. There is no automatic retry. A client that retries a `502` without an
`Idempotency-Key` may send a duplicate when the first attempt reached the provider but the answer
was lost (`failure_code` `transport.unreachable`); see [Idempotency](#idempotency).

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
`POST /v1/api-keys` (see [`API_KEYS.md`](API_KEYS.md)) and pass it as `X-API-Key`.

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

`from` must be the bare address of a mailbox the key's owner owns; any other address, including
another user's mailbox, is `404 MAILBOX.NOT_FOUND`. The remaining fields, limits, outcomes, and
`Idempotency-Key` behavior are those of [Send](#send). A key without the scope gets
`403 PERMISSION.DENIED`. The read route returns only outbound messages, so a `mail:read` key cannot
read mail that was received.

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

A message is identified by its `Message-ID`, or by a hash of its content when it has none, once per
mailbox. The API stores the first `text/plain` and first `text/html` part, converted to UTF-8 and
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
