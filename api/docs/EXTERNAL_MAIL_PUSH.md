# External Mail Push Subscriptions

Push lets Gmail and Outlook tell Zmail that a connected mailbox received mail, so the
`mail:external:sync` worker fetches that connection within seconds instead of at its next poll. It is
off by default. The behavior and the public routes are specified in
[contracts/MAILBOXES.md](../../contracts/MAILBOXES.md#push-subscriptions). This page lists what an
operator creates and configures.

A notification is only a hint that marks a connection due. Mail is still read by the regular sync
with the connection's own grant, so a push setup that is misconfigured or failing never loses mail:

- The connection keeps polling every two minutes until push is shown to work.
- It polls every 15 minutes (`EXTERNAL_MAIL_PUSH_POLL_INTERVAL`) only while notifications keep
  arriving (within `EXTERNAL_MAIL_PUSH_QUIET_LIMIT`) and no poll has found mail that no notification
  announced.

## Common Settings

| Variable | Default | Meaning |
|---|---|---|
| `EXTERNAL_MAIL_PUSH_ENABLED` | `false` | Turns push on. While off, nothing subscribes and both notification routes answer `404` without reading the database. |
| `EXTERNAL_MAIL_PUSH_POLL_INTERVAL` | `15m` | Poll of a connection whose push is shown to work (2m to 1h). |
| `EXTERNAL_MAIL_PUSH_QUIET_LIMIT` | `24h` | How long a subscription without any notification still counts as working (1h to 168h). After it the connection polls every two minutes, which is the safe choice for a quiet mailbox too. |
| `EXTERNAL_MAIL_PUSH_COALESCE_WINDOW` | `10s` | Least time between two notification-triggered syncs of one connection (1s to 5m). |
| `EXTERNAL_MAIL_PUSH_MAX_PER_MINUTE` | `6000` | Authenticated notification requests per provider and minute. The budget is per replica, or shared by all replicas with `RATE_LIMIT_STORE=postgres`, where each authenticated request then writes one rate-limit row. Requests that authenticate nothing spend only a per-IP budget of 120 a minute, kept in memory. |

Run the worker with a short idle wait so a due connection is picked up quickly, for example
`/app/fastbuild mail:external:sync --batch=20 --poll=5s`. The worker also subscribes and renews:

- A subscription is created after a connection's first complete sync.
- It is renewed when half its life has passed.
- Both happen inside that connection's sync, so renewals are bounded by the existing per-provider
  budget.

Both notification routes are public (`POST /v1/external-mail-notifications/gmail` and
`.../outlook`) and must be reachable from the provider over HTTPS at the API's public origin. They
need no session or API key and do a bounded amount of work per request. Set
`SERVER_TRUSTED_PROXIES` correctly so the per-IP budget sees the sender's address.

## Gmail (Google Cloud)

`users.watch` publishes only to a Pub/Sub topic in the **same Cloud project as the OAuth client**
whose token makes the call. Create everything below in the project that owns the operator's Gmail
OAuth client (`EXTERNAL_MAIL_GMAIL_CLIENT_ID`):

1. Enable the **Cloud Pub/Sub API**.
2. Create a **topic**, for example `projects/<project>/topics/zmail-gmail`. Grant the role
   **Pub/Sub Publisher** to `gmail-api-push@system.gserviceaccount.com`, which is how Gmail
   publishes. Grant it on that topic only, never at the project level.
3. Create a dedicated **service account** for push authentication, for example
   `zmail-gmail-push@<project>.iam.gserviceaccount.com`, with **no roles** of its own. Grant the
   Pub/Sub service agent (`service-<project-number>@gcp-sa-pubsub.iam.gserviceaccount.com`) the role
   **Service Account Token Creator** on this service account only, so Pub/Sub can mint its OIDC
   tokens.
4. Create a **push subscription** on the topic with these settings:
   - Endpoint `https://<api-origin>/v1/external-mail-notifications/gmail`.
   - **Authentication enabled**, with the service account above.
   - An audience you choose (by default the endpoint URL).
   - A short message retention (for example 10 minutes) and an acknowledgement deadline of 10 to 20
     seconds: a lost notification costs at most one poll interval.
5. Configure Zmail. The three Gmail settings are required together, and startup fails otherwise:

   ```bash
   EXTERNAL_MAIL_PUSH_ENABLED=true
   EXTERNAL_MAIL_GMAIL_PUSH_TOPIC=projects/<project>/topics/zmail-gmail
   EXTERNAL_MAIL_GMAIL_PUSH_AUDIENCE=https://<api-origin>/v1/external-mail-notifications/gmail
   EXTERNAL_MAIL_GMAIL_PUSH_SERVICE_ACCOUNT=zmail-gmail-push@<project>.iam.gserviceaccount.com
   ```

Zmail accepts a push request only when its OIDC token passes every check:

- RS256, signed by a key Google publishes at `EXTERNAL_MAIL_GMAIL_PUSH_JWKS_URL` (default
  `https://www.googleapis.com/oauth2/v3/certs`).
- Issuer `accounts.google.com`.
- The configured audience.
- Unexpired.
- Issued to the configured service account, with a verified email.

The service account is required: without it, any Google-signed token for the audience would pass.
Never rely on a secret in the endpoint URL.

- **Network.** The worker and the API reach `gmail.googleapis.com` (watch, profile, and stop) and
  `www.googleapis.com` (signing keys); allow both in egress.
- **Scope.** `users.watch` needs only the read-only Gmail scope the connector already holds.
- **Primary address.** Each watch is keyed by the primary address Gmail's profile reports, which is
  what notifications name, so an account bound under an alias still gets its notifications.

**Workspace tenants are polled, not pushed.** A mailbox assigned from a Workspace tenant reads with a
token minted by the customer's own service account in the customer's project. Gmail refuses to watch
it with the operator's topic, so Zmail does not try. Its push state reads `unsupported`, it polls
every two minutes, and the connector asks again once a day without reporting a failure. Pushing to
such mailboxes would need a topic in each customer's project, which Zmail does not configure.

## Outlook (Microsoft Entra)

Graph change notifications need no extra Azure resource. The application registration the connector
already uses creates the subscriptions with the permissions it already has:

| Mailbox | Application | Permission |
|---|---|---|
| A member's own mailbox | The operator's | Delegated `Mail.Read` |
| A Microsoft 365 tenant mailbox | The organization's own | Application `Mail.Read` |

Keep the tenant application restricted to the assigned mailboxes with RBAC for Applications in
Exchange Online; a subscription can only read what the token can read. No `Mail.ReadWrite` or
`Mail.ReadBasic.All` is needed.

1. Expose `https://<api-origin>/v1/external-mail-notifications/outlook` publicly over HTTPS, with a
   certificate from a public CA and TLS 1.2 or later. Graph calls it with a validation handshake
   when a subscription is created and expects the answer within 10 seconds.
2. Configure Zmail:

   ```bash
   EXTERNAL_MAIL_PUSH_ENABLED=true
   EXTERNAL_MAIL_OUTLOOK_PUSH_NOTIFICATION_URL=https://<api-origin>/v1/external-mail-notifications/outlook
   ```

Each subscription carries a random `clientState`. Zmail stores only its SHA-256 hash and compares
notifications against it in constant time; a notification without the right `clientState` is
ignored. Lifecycle notifications (`reauthorizationRequired`, `subscriptionRemoved`, `missed`) arrive
at the same URL and make the next sync renew or recreate the subscription. Subscriptions are
requested for 70 hours and renewed after 35.

## Operations

- **Read models.** Each connection's push state is on `GET /v1/external-mail-connections` (`push`).
  Its `status` is one of `active`, `quiet`, `degraded`, `pending`, `unsupported`, or `stopped`. Each
  sync run records `trigger` `poll` or `push`.
- **Metrics.**
  - `zmail_external_mail_push_notifications_total{provider,result}`, where `result` is `accepted`,
    `ignored`, `validation`, `unauthenticated`, `malformed`, or `rate_limited`.
  - `zmail_external_mail_push_degraded_total{provider}`: subscriptions a poll caught missing mail.
  - `zmail_external_mail_push_delay_seconds{provider}`: the time from a notification to the start of
    the sync it triggered.

  A rising degraded count usually means the push subscription was deleted, its audience or service
  account changed, or the topic lost its publisher grant.
- **Logs.** The events are `external_mail.push_received`, `external_mail.push_refused`,
  `external_mail.push_subscribed`, `external_mail.push_subscribe_failed`,
  `external_mail.push_degraded`, `external_mail.push_unsubscribed`, and `external_mail.push_stopped`.
  They carry the provider, counts, and connection ids only, never a body, token, address, or secret.
- **Switching push off.** This stops creating and renewing subscriptions at once. Open provider
  subscriptions lapse at their expiry (at most 7 days for Gmail, 70 hours for Outlook), and their
  notifications are answered `404` meanwhile.
- **Revoking.**
  - Every revocation stops the subscription in Zmail in its own transaction.
  - Unbinding by the member, a forced disconnect, a member's departure, and deleting the mailbox also
    stop it at the provider, after that transaction commits. A departure's connections are released
    four at a time.
  - Subscriptions of connections that lose their grant or belong to a revoked tenant lapse at the
    provider.
