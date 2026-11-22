# Connector Push Subscriptions (C7): Reduced Implementation Plan

Roadmap: [mail-connectors-roadmap.zh-CN.md](mail-connectors-roadmap.zh-CN.md) row C7 (§7), §11 to §13
("push is only a hint, polling stays the safety net, subscriptions must be renewed"). Loop task T11
(second half). Scope: API, worker, and contract only; no Web UI.

Owner: the `mailbox` starter (`api/internal/modules/mailbox/`) owns every rule and record;
`contracts/MAILBOXES.md` and `contracts/openapi.yaml` own the public behavior.

This is a reduced plan under §1 of the standard: the change adds one table and two public routes,
but it adds no authorization rule for members (the new routes are provider callbacks authenticated
by the provider's proof) and every decision below was fixed by the owner's task brief. Each is
recorded as "default adopted, pending confirmation" and is easy to reverse: switching
`EXTERNAL_MAIL_PUSH_ENABLED` off restores today's behavior exactly.

## Behavior

Gmail and Outlook tell Zmail that a connected mailbox has new mail. Zmail treats that only as a hint:
it marks the connection due so the existing sync worker fetches it within seconds instead of at the
next two-minute poll. Mail is still read only by the normal fetch, with the connection's own grant,
cursor, backfill window, `received_not_before` floor, backoff, lease, and per-provider budget. When
push works, the poll of that connection slows to 15 minutes; when it does not, nothing changes.

## Rulings (default adopted, pending confirmation)

| # | Ruling |
|---|---|
| P1 | A notification never carries or is trusted for mail content. It sets `pending_at` on the subscription row and lowers the connection's `next_sync_at` to `max(now, last push sync + coalescing window)` only when the connection is `active`, has no failures in a row (backoff stands), and is not leased: the lower is a compare-and-set against the `next_sync_at` the last save scheduled (`scheduled_sync_at`), so a claim lease is never shortened. A hint that arrives during a fetch stays pending and the worker promotes it at its next pass. |
| P2 | Polling stays. With a live subscription (status `active`, not expired, push enabled) a successful sync without more pages schedules the next poll after `EXTERNAL_MAIL_PUSH_POLL_INTERVAL` (15 minutes by default, 2 minutes to 1 hour), never later than the next renewal. Without one it stays two minutes. |
| P3 | Seam: an optional `ExternalMailPushSource` on `ExternalMailConnector.Push` with `Subscribe` (create or renew), `Unsubscribe`, and `ParseNotification` (authenticate and read a request into hints: subscription identity, Outlook `clientState`, lifecycle event; or a validation echo). IMAP has none (IMAP IDLE is future work). The fake source implements it for tests. |
| P4 | Gmail: `users.watch` on `INBOX` to the operator's Cloud Pub/Sub topic (`EXTERNAL_MAIL_GMAIL_PUSH_TOPIC`), renewed when half of its 7-day life has passed. Delivered by a Pub/Sub push subscription with OIDC authentication: the request's bearer JWT must be RS256, signed by a key from Google's JWKS (`EXTERNAL_MAIL_GMAIL_PUSH_JWKS_URL`, cached for an hour, refetched for an unknown key id at most once a minute, body at most 64 KiB and 32 keys, RSA 2048 to 8192 bits), issued by `accounts.google.com`, for the audience `EXTERNAL_MAIL_GMAIL_PUSH_AUDIENCE`, unexpired, and, when `EXTERNAL_MAIL_GMAIL_PUSH_SERVICE_ACCOUNT` is set, for that verified service account email. Standard library crypto only. Identity: the lowercased `emailAddress` of the message data; it may name several connections (the same account bound twice), each synced with its own grant. `users.stop` is skipped while another live row watches the same address. |
| P5 | Outlook: a Graph subscription on the mailbox's Inbox messages (`created`), resource `me/...` for a member grant or `users/{directory id}/...` for a tenant mailbox, with `notificationUrl` and `lifecycleNotificationUrl` both `EXTERNAL_MAIL_OUTLOOK_PUSH_NOTIFICATION_URL`, a 4,200-minute expiry (inside both the old 4,230-minute and the current 10,080-minute mail limits), renewed (PATCH) at half-life and recreated when Graph answers 404. A fresh random 32-byte `clientState` per created subscription; only its SHA-256 is stored and it is compared in constant time. The validation handshake echoes `validationToken` as `text/plain` (at most 1,024 printable ASCII characters). Lifecycle `reauthorizationRequired` makes renewal due now; `subscriptionRemoved` also ends the row's life (renewal recreates it); `missed` only marks the connection due. Every lifecycle event marks the connection due. |
| P6 | Model: `external_mail_subscriptions`, one row per connection (unique, `ON DELETE CASCADE`): provider, `lookup_key` (Gmail address or Graph subscription id), `provider_subscription_id`, `secret_hash`, `status` (`pending`, `active`, `stopped`), `expires_at`, `renew_after`, `pending_at`, `last_notified_at`, `last_push_sync_at`, `scheduled_sync_at`, `consecutive_failures`, `last_error_code`. `external_mail_sync_runs.trigger` (`poll`, `push`). Migration `2026_12_15_090000_add_external_mail_push` (later than every existing one), Up/Down tests, golden schema, starter registration. |
| P7 | Endpoints `POST /v1/external-mail-notifications/gmail` and `POST /v1/external-mail-notifications/outlook`: no session or API key; authenticated as in P4/P5; body at most 64 KiB (Gmail 16 KiB); at most 20 hints per request; one rate limit bucket per provider (`EXTERNAL_MAIL_PUSH_MAX_PER_MINUTE`, 6,000 by default); answers `204` (Gmail) or `202` (Outlook) for every authenticated request whether or not a subscription matches, so nothing reveals one and the provider stops retrying an unknown one. A Gmail request that fails authentication is `401 AUTH.UNAUTHORIZED`; malformed bodies are `400 VALIDATION.INVALID`. With push off, or the provider not configured for push, the routes answer `404 MAILBOX.EXTERNAL_PROVIDER_UNAVAILABLE` and do no database work. No new error code. |
| P8 | Lifecycle: subscribe (and renew) inside the sync of the connection, after a successful fetch that left no more pages, under the connection's lease (renewed for the call), with the grant the fetch used, so Outlook's rotated refresh tokens are saved in the same transaction and no second worker ever uses the grant concurrently. Renewal is therefore bounded per pass by the existing claim budget. A failed subscribe or renewal backs off (the existing two-minutes-to-one-hour backoff), records `last_error_code`, never changes the connection's status, and polling continues. Stop: revoking a connection (owner, forced, shared, member departure, mailbox deletion) stops the subscription at the provider with the grant still at hand, best effort, and marks the row `stopped` in the revoke transaction. Every other path that ends an active connection (tenant revocation or loss, `needs_reconnect`, retention purge) is covered by the worker's reconciliation, which marks the rows of non-active connections `stopped` in bounded batches each pass, and by the endpoints, which ignore a row whose connection is not `active`; the purge deletes the row by cascade. Those provider subscriptions lapse at their expiry (at most 7 days for Gmail, 70 hours for Outlook). |
| P9 | Tenant mailboxes subscribe with the token the tenant credential mints for that one mailbox (`MailboxToken`) and nothing wider. |
| P10 | Privacy: a hint only selects connections by its provider and identity; the sync still enforces `received_not_before`; nothing from a body is logged except the provider name and counts; secrets (`clientState`, JWTs, tokens) never appear in logs, errors, audit rows, or responses. |
| P11 | Observability: sync runs gain `trigger`; the connection list gains `push` (`status`, `expires_at`, `last_notified_at`, `last_error_code`, or `null`); a histogram `zmail_external_mail_push_delay_seconds{provider}` (hint received to fetch start) and a counter `zmail_external_mail_push_notifications_total{provider,result}` use the existing metrics seam. |

## Acceptance Criteria And Verification

PostgreSQL and `httptest` tests, never a real provider or credential:

- JWT verification: good token; wrong audience, wrong issuer, expired, bad signature, unknown key id,
  wrong algorithm, oversized JWKS; each security test also runs against a deliberately broken check
  to show it can fail.
- Outlook: validation handshake, `clientState` mismatch ignored without a sync, lifecycle events.
- Coalescing: a burst of notifications yields one sync per window; a lease is never shortened.
- Unknown subscription answered like a known one; cross-organization isolation (a hint for mailbox
  A never marks B due, including the same address in two organizations only syncing each with its
  own grant).
- Renewal before expiry; failed subscribe and renewal fall back to polling without stopping sync.
- Stop on owner revoke, forced disconnect, member departure, tenant revocation, `needs_reconnect`,
  and retention purge.
- Tenant-assigned connection subscribes with its mailbox token; `received_not_before` still enforced
  on push-triggered syncs.
- Switch off: endpoints inert and no subscription created.
- Gate: `make check` from the worktree root (local only).

## Not Covered

Web UI for push state; IMAP IDLE; Gmail and Graph rich notifications; the 10,000-connection load
test and the P95 30-second latency proof (need real providers, `blocked: go-live`); stopping
provider subscriptions that lapse on their own after a tenant revocation or `needs_reconnect`.
