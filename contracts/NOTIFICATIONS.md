# Notification Contract

The optional `notification` starter owns a user-facing notification center and the reliable
delivery boundary behind it. It depends only on the default `user` and `audit` starters, so
single-user applications can enable it without adopting organizations or generalized permissions.

The starter keeps two concepts separate:

- a **notification** is one immutable application event addressed to one FastBuild user;
- a **delivery** is that notification's channel-specific execution record.

One notification may therefore have an immediately delivered `in_app` delivery, an independently
retried `email` delivery, and a `web_push` delivery to the user's subscribed browsers. Provider-specific messages are implementation details of a delivery, not
new notification records.

## Activation

Enable the API starter for every API process, migration job, and notification worker:

```dotenv
OPTIONAL_STARTERS=notification
```

Enable the matching browser feature in the same Web build:

```dotenv
NEXT_PUBLIC_OPTIONAL_FEATURES=notification
```

Selection is additive and restart-scoped. Enabling the Web feature without either the production
adapter or development mock BFF returns `503 COMMON.SERVICE_UNAVAILABLE`; it never falls back to
fabricated production data.

## Publication Model

Downstream API modules publish through `domain.NotificationPublisher`; there is deliberately no
browser or public HTTP endpoint that can create notifications. A publication contains:

| Field | Rule |
|---|---|
| `user_id` | Existing FastBuild user; delivery routing always resolves the current user record |
| `idempotency_key` | Required, caller-owned, 1-128 ASCII letters, digits, `.`, `_`, `:`, or `-` |
| `kind` | Required lowercase dotted identifier, 3-100 bytes, such as `billing.invoice_paid` |
| `title` | Plain text, 1-160 Unicode characters |
| `body` | Plain text, 1-4,000 Unicode characters |
| `action_url` | Optional same-origin relative path beginning with one `/`, at most 2,048 bytes |
| `channels` | Unique subset of `in_app`, `email`, and `web_push` |
| `required_channels` | Unique subset of `channels` that user preferences cannot disable |

`(user_id, idempotency_key)` is unique. Repeating the same publication returns the original
notification and does not create duplicate deliveries. Reusing a key with different immutable
content fails closed as an idempotency conflict.

Unknown channels, malformed keys, absolute or protocol-relative action URLs, and conflicting
replays are rejected before persistence. Titles and bodies are stored as plain text. The email
adapter HTML-escapes them; the Web renders them as text and never as HTML.

## Preference Priority

Every user has effective global channel preferences. Missing rows resolve to:

```json
{
  "in_app_enabled": true,
  "email_enabled": true,
  "web_push_enabled": true
}
```

For each publication, channel selection is evaluated in this order:

1. the publication's declared channels are the upper bound;
2. a required channel is always selected;
3. a non-required channel is selected only when the user's current global preference enables it;
4. `web_push` is then dropped, required or not, when the user has no browser subscription at
   publication time, so no `web_push` delivery row exists for a user without devices.

Preferences affect future publications only. Enabling a channel does not retroactively create
deliveries; disabling a channel does not delete existing records or cancel an already claimed
delivery. Security-sensitive downstream workflows may declare reviewed required channels.

## Delivery Lifecycle

`in_app` delivery is committed atomically with the notification and begins as `delivered`. Email
and `web_push` deliveries begin as `pending` and are processed by `fastbuild notification:work`
with the same leases, retry delays, and attempt budget. Each worker round claims `web_push` first
with up to half of its batch and fills the rest with email.

Email and `web_push` delivery states are:

| State | Meaning |
|---|---|
| `pending` | Eligible at `available_at`, including a transient retry |
| `processing` | Owned by one worker lease until `lease_expires_at` |
| `delivered` | Provider accepted the message |
| `failed` | Permanently invalid, unconfigured, or retry budget exhausted |

Workers claim due rows in short database transactions using row locking, a random lease token, and
`SKIP LOCKED` where supported. Provider I/O happens outside the claim transaction. Completion
updates require the same lease token, so an expired worker cannot overwrite a newer worker's result.
Expired `processing` rows are reclaimable.

Transient failures use bounded exponential retry and a maximum attempt count. Persistent failure
data stores only a stable local code such as `EMAIL.NOT_CONFIGURED`, `EMAIL.PROVIDER_REJECTED`, or
`EMAIL.PROVIDER_UNAVAILABLE`; recipient addresses, title/body text, credentials, provider response
bodies, and free-form error strings are never copied into the delivery ledger or logs.

Every email retry uses the stable provider key `notification-email-<delivery_id>`. The first attempt
binds a hash of the current recipient route; if that route changes before a retry, the delivery fails
closed instead of sending the same provider operation to a different address.

## Web Push

`web_push` sends an RFC 8291 (`aes128gcm`) encrypted message with RFC 8292 VAPID authorization to
every subscription the user holds when the delivery is processed. It is available only when
`WEB_PUSH_VAPID_PUBLIC_KEY`, `WEB_PUSH_VAPID_PRIVATE_KEY`, and `WEB_PUSH_SUBJECT` are all set and
valid (see `api/docs/CONFIGURATION.md`); otherwise registration answers
`503 NOTIFICATION.WEB_PUSH_DISABLED` and pending pushes fail with `WEB_PUSH.NOT_CONFIGURED`.

The payload is exactly this JSON, at most 3,000 bytes; the body is cut with `…` to fit:

```json
{
  "title": "Ada Lovelace",
  "body": "Quarterly numbers",
  "url": "/console/mailboxes?org=1&mailbox=2&open=3",
  "tag": "n-81"
}
```

`url` is the notification's `action_url`, or `/` without one; `tag` is `n-<notification_id>`, and the
same value is sent as the push `Topic`, so a push service or the service worker replaces rather than
stacks a repeat. Messages use `TTL` 24 hours and `Urgency: normal`.

Each subscription's push-service answer is applied on its own:

| Answer | Effect on the subscription |
|---|---|
| Accepted (2xx) | `last_success_at` is set and `failure_count` resets to 0 |
| Gone (404 or 410) | The subscription is deleted |
| Rejected (other 4xx, or an unencryptable subscription) | `failure_count` increments; the subscription is deleted at 3 |
| Transient (429, 5xx, network, or the 45-second send budget elapsed) | No change |

The delivery is `delivered` when at least one subscription accepted it. When none did and at least
one failure was transient, it returns to `pending` with `WEB_PUSH.PUSH_SERVICE_UNAVAILABLE` and the
standard retry delay, failing with `WEB_PUSH.RETRY_EXHAUSTED` after the last attempt. Otherwise it
fails with `WEB_PUSH.REJECTED` (some subscription refused it), `WEB_PUSH.NO_SUBSCRIPTIONS` (none left,
or every one was gone), `WEB_PUSH.RECIPIENT_UNAVAILABLE` (deleted account), or
`WEB_PUSH.INVALID_MESSAGE`. Push endpoints, keys, and payload text are never logged or copied into the
delivery ledger.

### Subscriptions

A subscription is one browser `PushSubscription`. Its endpoint must be an `https` URL without
credentials or port on a known browser push service (`fcm.googleapis.com`,
`push.services.mozilla.com`, `push.apple.com`, `notify.windows.com`, or their subdomains), so a stored
endpoint can never make the API call an arbitrary host. `keys.p256dh` must decode (base64url or
base64, padding optional) to a 65-byte uncompressed P-256 point and `keys.auth` to 16 bytes.
`device_label` is optional plain text up to 120 characters.

Registration is an idempotent upsert keyed by the SHA-256 of the endpoint: repeating it refreshes the
keys and label and keeps the id. When the endpoint belonged to another user, it moves to the caller
with a new `created_at` and no `last_success_at`, so a shared browser notifies only the account that
subscribed last. A user keeps at most 20 subscriptions; registering one more deletes the least
recently registered. Deleting the user deletes their subscriptions.

The shipped worker uses the database ledger as its durable source of truth. It does not depend on
the process-local memory queue and can be run independently in multiple replicas.

## HTTP API

All endpoints require the standard bearer token and operate only on the authenticated user's data.
List operations are paginated and ordered newest first.

| Operation | Endpoint | Successful `data` |
|---|---|---|
| List center records | `GET /v1/notifications?status=all\|unread` | Paginated notifications |
| Read center status | `GET /v1/notification-status` | `{ unread_count }` |
| Replace one read state | `PATCH /v1/notifications/:id` | Notification |
| Mark existing records read | `PUT /v1/notification-read-state` | `{ updated_count, unread_count }` |
| Read preferences | `GET /v1/notification-preferences` | Effective preferences |
| Replace preferences | `PUT /v1/notification-preferences` | Effective preferences |
| Read push availability | `GET /v1/web-push-config` | `{ enabled, public_key }` |
| List own devices | `GET /v1/web-push-subscriptions` | Array of subscriptions, newest first, at most 20 |
| Register this device | `PUT /v1/web-push-subscriptions` | Subscription |
| Remove one device | `DELETE /v1/web-push-subscriptions/:id` | `204`, no body |

A notification response is plain-text and user scoped:

```json
{
  "id": 81,
  "kind": "billing.invoice_paid",
  "title": "Invoice paid",
  "body": "Invoice 1042 was paid successfully.",
  "action_url": "/console/invoices/1042",
  "is_read": false,
  "read_at": null,
  "created_at": "2026-07-15T20:00:00Z"
}
```

`status` accepts only `all` or `unread` and defaults to `all`. `PATCH` accepts exactly
`{ "is_read": boolean }`. Setting the same state twice is idempotent.

Bulk read state uses a high-water mark rather than an unbounded list:

```json
{
  "through_id": 81
}
```

Only delivered `in_app` records owned by the current user with `id <= through_id` are marked read.
Notifications created concurrently above the high-water mark remain unread.

Preference replacement requires `in_app_enabled` and `email_enabled`, accepts an optional
`web_push_enabled` (omitted keeps the stored value, so older clients cannot turn push back on), and is
idempotent:

```json
{
  "in_app_enabled": true,
  "email_enabled": false,
  "web_push_enabled": true
}
```

`GET /v1/web-push-config` returns `{ "enabled": true, "public_key": "<base64url VAPID key>" }`, or
`{ "enabled": false, "public_key": "" }` when push is not configured. The browser subscribes with
`applicationServerKey` set to that key.

`PUT /v1/web-push-subscriptions` accepts the browser subscription:

```json
{
  "endpoint": "https://fcm.googleapis.com/fcm/send/abc",
  "keys": { "p256dh": "BNc…", "auth": "tBH…" },
  "device_label": "Chrome on macOS"
}
```

and returns, like each list item, the subscription without endpoint or keys:

```json
{
  "id": 12,
  "device_label": "Chrome on macOS",
  "created_at": "2026-12-23T09:00:00Z",
  "last_success_at": null
}
```

`DELETE /v1/web-push-subscriptions/:id` removes only the caller's own subscription; any other id
answers `404 NOTIFICATION.WEB_PUSH_SUBSCRIPTION_NOT_FOUND`.

## Browser Adapter

The fixed same-origin adapter mirrors the resources under `/api`:

| Browser endpoint | Upstream endpoint |
|---|---|
| `GET /api/notifications` | `GET /v1/notifications` |
| `PATCH /api/notifications/:id` | `PATCH /v1/notifications/:id` |
| `GET /api/notification-status` | `GET /v1/notification-status` |
| `PUT /api/notification-read-state` | `PUT /v1/notification-read-state` |
| `GET/PUT /api/notification-preferences` | matching `/v1/notification-preferences` |
| `GET /api/web-push-config` | `GET /v1/web-push-config` |
| `GET/PUT /api/web-push-subscriptions` | matching `/v1/web-push-subscriptions` |
| `DELETE /api/web-push-subscriptions/:id` | `DELETE /v1/web-push-subscriptions/:id` |

Every response is `Cache-Control: private, no-store` and varies on `Cookie`. Unsafe methods enforce
same-origin checks before authentication or body parsing. Successful upstream and mock payloads are
strictly validated before entering React Query state. The mock BFF is development-only and must
preserve ownership, pagination, read high-water marks, preferences, envelopes, and stable errors.

## Audit And Privacy

Preference changes and user-initiated read-state changes emit audit metadata containing only user,
notification, channel-setting, and count identifiers. Publication content, recipient addresses,
and delivery provider details are excluded from audit records.

Push subscriptions are device secrets: the endpoint and keys are stored only for sending, are never
returned by any endpoint, logged, or audited, and only their SHA-256 identifies an endpoint. A push
payload leaves the API encrypted for the device, but the device's lock screen may show it, so
publishers put no more in a pushed title or body than they would show there.

Notification rows contain user-visible content and require an application retention policy.
Downstream apps should avoid secrets, reset tokens, credentials, and unnecessary personal data in
titles, bodies, idempotency keys, kinds, and action URLs.

## Stable Errors

| HTTP status | `error_code` | Meaning |
|---|---|---|
| 404 | `NOTIFICATION.NOT_FOUND` | The notification is absent, not delivered in-app, or owned by another user |
| 409 | `NOTIFICATION.IDEMPOTENCY_CONFLICT` | Internal publication reused a key with different immutable content |
| 422 | `NOTIFICATION.INVALID_CHANNEL` | Internal publication declared an unsupported or inconsistent channel |
| 404 | `NOTIFICATION.WEB_PUSH_SUBSCRIPTION_NOT_FOUND` | The push subscription is absent or owned by another user |
| 422 | `NOTIFICATION.WEB_PUSH_ENDPOINT_INVALID` | The endpoint is not an `https` URL on a known browser push service |
| 422 | `NOTIFICATION.WEB_PUSH_KEYS_INVALID` | `p256dh` is not an uncompressed P-256 point or `auth` is not 16 bytes |
| 503 | `NOTIFICATION.WEB_PUSH_DISABLED` | The instance has no valid VAPID configuration |
| 503 | `COMMON.SERVICE_UNAVAILABLE` | Notification persistence or the selected backend is unavailable |

Malformed JSON, field validation, global envelopes, and `request_id` behavior follow
[`README.md`](README.md). The internal publication errors are documented even though browser clients
cannot publish, because downstream modules and operational tests must branch on stable semantics.

## Deliberate Deferrals

The starter does not ship SMS, native mobile push, Slack/chat, broadcast/WebSocket fan-out, digests, schedules,
topics, segments, organization-level preference inheritance, a template editor, provider callbacks,
or an administrator activity UI. Those concerns can extend the publisher and delivery adapter seams
without changing the notification center contract.
