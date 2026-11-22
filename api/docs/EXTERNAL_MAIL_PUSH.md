# External Mail Push Subscriptions

Push lets Gmail and Outlook tell Zmail that a connected mailbox received mail, so the
`mail:external:sync` worker fetches that connection within seconds instead of at its next poll. It is
off by default. The behavior and the public routes are specified in
[contracts/MAILBOXES.md](../../contracts/MAILBOXES.md#push-subscriptions); this page is what an
operator creates and configures.

A notification is only a hint that marks a connection due. Mail is still read by the regular sync
with the connection's own grant, so a misconfigured or failing push never loses mail: the connection
keeps polling every two minutes until a subscription is live, and every 15 minutes
(`EXTERNAL_MAIL_PUSH_POLL_INTERVAL`) while one is.

## Common Settings

| Variable | Default | Meaning |
|---|---|---|
| `EXTERNAL_MAIL_PUSH_ENABLED` | `false` | Turns push on. While off, nothing subscribes and both notification routes answer `404` without reading the database. |
| `EXTERNAL_MAIL_PUSH_POLL_INTERVAL` | `15m` | Poll of a connection with a live subscription (2m to 1h). |
| `EXTERNAL_MAIL_PUSH_COALESCE_WINDOW` | `10s` | Least time between two notification-triggered syncs of one connection (1s to 5m). |
| `EXTERNAL_MAIL_PUSH_MAX_PER_MINUTE` | `6000` | Notification requests each replica accepts per provider and minute. |

Run the worker with a short idle wait so a due connection is picked up quickly, for example
`/app/fastbuild mail:external:sync --batch=20 --poll=5s`. The worker also subscribes and renews: a
subscription is created after a connection's first complete sync and renewed when half its life has
passed, inside that connection's sync, so renewals are bounded by the existing per-provider budget.

Both notification routes are public (`POST /v1/external-mail-notifications/gmail` and
`.../outlook`) and must be reachable from the provider over HTTPS at the API's public origin. They
need no session or API key and do a bounded amount of work per request.

## Gmail (Google Cloud)

Create these in the Google Cloud project that owns the Gmail OAuth client (or, for a Workspace
tenant, any project the operator controls):

1. Enable the **Cloud Pub/Sub API**.
2. Create a **topic**, for example `projects/<project>/topics/zmail-gmail`. Grant the role
   **Pub/Sub Publisher** on that topic, and only that topic, to
   `gmail-api-push@system.gserviceaccount.com`, which is how Gmail publishes. Do not grant it at the
   project level.
3. Create a dedicated **service account** for push authentication, for example
   `zmail-gmail-push@<project>.iam.gserviceaccount.com`, with **no roles** of its own. Grant the
   Pub/Sub service agent (`service-<project-number>@gcp-sa-pubsub.iam.gserviceaccount.com`) the role
   **Service Account Token Creator** on this service account only, so Pub/Sub can mint its OIDC
   tokens.
4. Create a **push subscription** on the topic with endpoint
   `https://<api-origin>/v1/external-mail-notifications/gmail`, **authentication enabled**, the
   service account above, and an audience you choose (by default the endpoint URL). Set a short
   message retention (for example 10 minutes) and an acknowledgement deadline of 10 to 20 seconds:
   a lost notification costs at most one poll interval.
5. Configure Zmail:

   ```bash
   EXTERNAL_MAIL_PUSH_ENABLED=true
   EXTERNAL_MAIL_GMAIL_PUSH_TOPIC=projects/<project>/topics/zmail-gmail
   EXTERNAL_MAIL_GMAIL_PUSH_AUDIENCE=https://<api-origin>/v1/external-mail-notifications/gmail
   EXTERNAL_MAIL_GMAIL_PUSH_SERVICE_ACCOUNT=zmail-gmail-push@<project>.iam.gserviceaccount.com
   ```

Zmail authenticates every push request by its OIDC token: RS256 signed by a key Google publishes at
`EXTERNAL_MAIL_GMAIL_PUSH_JWKS_URL` (default `https://www.googleapis.com/oauth2/v3/certs`), issuer
`accounts.google.com`, the configured audience, unexpired, and, when set, the configured service
account with a verified email. Setting the service account is strongly recommended: without it any
Google-signed token for the same audience is accepted. Never rely on a secret in the endpoint URL.
The worker and API reach `gmail.googleapis.com` (watch and stop) and `www.googleapis.com` (signing
keys); allow them in egress. `users.watch` needs only the read-only Gmail scope the connector already
holds; Workspace tenants need no extra delegated scope.

## Outlook (Microsoft Entra)

Graph change notifications need no extra Azure resource. The application registration the connector
already uses (the operator's for member grants, the organization's own for a Microsoft 365 tenant)
creates subscriptions with the permissions it already has: delegated `Mail.Read` for a member's own
mailbox, application `Mail.Read` for tenant mailboxes. Keep the tenant application restricted to the
assigned mailboxes with RBAC for Applications in Exchange Online; a subscription can only read what
the token can read. No `Mail.ReadWrite` or `Mail.ReadBasic.All` is needed.

1. Expose `https://<api-origin>/v1/external-mail-notifications/outlook` publicly over HTTPS with a
   certificate from a public CA, TLS 1.2 or later. Graph calls it with a validation handshake when a
   subscription is created and expects the answer within 10 seconds.
2. Configure Zmail:

   ```bash
   EXTERNAL_MAIL_PUSH_ENABLED=true
   EXTERNAL_MAIL_OUTLOOK_PUSH_NOTIFICATION_URL=https://<api-origin>/v1/external-mail-notifications/outlook
   ```

Each subscription carries a random `clientState` that Zmail stores only as a SHA-256 hash and
compares in constant time; a notification without the right `clientState` is ignored. Lifecycle
notifications (`reauthorizationRequired`, `subscriptionRemoved`, `missed`) arrive at the same URL and
make the next sync renew or recreate the subscription. Subscriptions are requested for 70 hours and
renewed after 35.

## Operations

- Each connection's push state is on `GET /v1/external-mail-connections` (`push`), and each sync run
  records `trigger` `poll` or `push`.
- Metrics: `zmail_external_mail_push_notifications_total{provider,result}` (`accepted`, `ignored`,
  `validation`, `unauthenticated`, `malformed`, `rate_limited`) and
  `zmail_external_mail_push_delay_seconds{provider}`, the time from a notification to the start of
  the sync it triggered.
- Logs: `external_mail.push_received`, `external_mail.push_refused`, `external_mail.push_subscribed`,
  `external_mail.push_subscribe_failed`, `external_mail.push_unsubscribed`, `external_mail.push_stopped`.
  They carry the provider, counts, and connection ids only, never a body, token, address, or secret.
- Switching push off stops creating and renewing subscriptions at once; open provider subscriptions
  lapse at their expiry (at most 7 days for Gmail, 70 hours for Outlook), and their notifications are
  answered `404` meanwhile.
- Subscriptions of connections that are revoked, lose their grant, or belong to a revoked tenant are
  stopped in Zmail by the worker; unbinding by the member, a forced disconnect, and a member's
  departure also stop them at the provider.
