# Container and Deployment Contract

FastBuild provides a production-oriented API image and a local Compose stack. It does not choose a
cloud, registry, rollout controller, secret store, or migration orchestrator for downstream apps.

## Surface Ownership

| Surface | Owner | Contract |
|---|---|---|
| `Dockerfile` | API core | Builds the production image and defines runtime-safe defaults. |
| `docker-compose.yml` | Local development | Runs the API and PostgreSQL on loopback with replaceable local credentials. |
| `scripts/verify-container.sh` | Verification | Builds with reviewed material evidence and exercises the production image contract. |
| `scripts/verify-compose.sh` | Verification | Rebuilds the local worktree or reuses one explicitly verified image, then exercises PostgreSQL and startup. |
| `.github/workflows/container.yml` | CI | Runs the same container verifier when API or container sources change. |
| `fastbuild notification:work` | Optional notification starter | Processes durable email deliveries with bounded database leases. |
| `fastbuild webhook:work` | Optional webhook starter | Processes signed outbound deliveries with bounded network calls and database leases. |
| `fastbuild mail:broadcast:work` | Optional mailbox starter | Sends direct sends that waited for room in the organization's send limit, oldest first, then the recipients of scheduled and running broadcasts, one message per contact, with database leases and the organization's send limit, and purges trash older than 30 days. It also retries, expires, and purges transactional messages. Run it wherever the API runs. |
| `fastbuild mail:external:sync` | Optional mailbox starter | Syncs members' connected outside accounts (`EXTERNAL_MAIL_PROVIDERS`) into their read-only connected mailboxes, each at most every two minutes, with row leases and backoff. It exits at once while no connector is enabled. Run it wherever the API runs once connectors are on. |
| `fastbuild mail:zone:work` | Optional mailbox starter | Activates managed mail zones once their registrar delegates them, sets their pending domains up, and removes zones still pending after 14 days. |
| `fastbuild mail:triage:work` | Optional mailbox starter | Classifies inbound mail queued for AI triage with the model `AI_TRIAGE_PROVIDER` selects, within each organization's daily cap, with leases, 1- and 5-minute retries, and `review` after three failed calls. Run it only once a provider is configured; with `disabled` it only defers queued rows. |
| `fastbuild mail:zone:release` | Optional mailbox starter | One-off operator command that removes a pending managed mail zone from Cloudflare and Zmail, so another organization can claim its apex. |
| `fastbuild organization:show` | Optional organization starter | Read-only operator command that finds an organization by ID or connected domain and prints its owners and admins, mailbox count and limit, and domains. |
| `fastbuild organization:grant-admin` | Optional organization starter | One-off operator command that makes an existing account an admin of an organization; a dry run unless `--commit`. |
| `fastbuild mailbox:import` | Optional mailbox starter | One-off operator command that checks, or with `--commit` creates, shared mailboxes from a CSV file through the same service as `POST /v1/mailbox-imports`. |
| [`deploy/kubernetes`](../../deploy/kubernetes/README.md) | Example | Hardened kustomize reference for the API, workers, retention jobs, migration job, and Web. |
| Production deployment manifests | Downstream app | Own secrets, network policy, replicas, migrations, rollout, and rollback. |

## Production Image

The image uses a multi-stage Go build and the distroless non-root runtime. The Dockerfile frontend,
Go builder, and distroless runtime use exact readable versions plus immutable multi-platform digests.
OCI labels carry the source, exact revision, version, and runtime-base digest. The image deliberately
does not copy `.env.example` or any `.env` file. Missing production configuration must fail at startup
rather than silently falling back to development values.

Image defaults:

```text
APP_ENV=production
APP_DEBUG=false
SERVER_MODE=release
SERVER_HOST=0.0.0.0
LOG_LEVEL=info
LOG_STDOUT=true
LOG_FILE_ENABLED=false
LOG_JSON=true
```

The image health check executes `/app/fastbuild health:check`, which probes
`http://127.0.0.1:${SERVER_PORT}/health/live` with a two-second deadline. Override the target only
with `HEALTHCHECK_URL` when the process genuinely listens elsewhere.

Build and verify the complete runtime contract:

```bash
make container-check
make compose-check
```

The verifier uses Buildx maximal metadata and requires the reviewed Dockerfile, builder, and runtime
digests in the BuildKit material set. It also checks OCI identity labels, the non-root user, image
health configuration, liveness, database-disabled readiness, JSON request logs on stdout, absence of
`/app/.env`, and a zero exit code after SIGTERM. Image-level SBOM, vulnerability, secret, EOL, and
downstream signing semantics live in [`../../docs/CONTAINER_SECURITY.md`](../../docs/CONTAINER_SECURITY.md).
Standalone `make compose-check` always rebuilds the current worktree so a stale local tag cannot
produce a false green result. In CI, the Compose verifier receives the explicit image tag built and
checked by the immediately preceding container step; a missing explicit image fails instead of
silently building a different artifact. It then starts PostgreSQL on random loopback ports, runs the
explicit local migration opt-in, waits for readiness, and completes a real starter registration.
When `OPTIONAL_STARTERS` contains `organization`, the same check also exercises PostgreSQL-backed
organization creation plus invitation create, duplicate conflict, list, revoke, and replacement
semantics without requiring an external email provider. It verifies the active organization CORS
preflight, required-header error, owner resolution, non-member non-disclosure, and current member
role against PostgreSQL. It then creates registered-user membership
fixtures and exercises the PII-minimized member directory, owner-only role mutation, account-delete
guard, admin removal, previous-owner leave, and a concurrent two-request ownership transfer. The
transfer gate requires exactly one `200`, one `403`, one persisted owner, and the previous owner
demoted to `admin`. A second concurrency gate races account deletion against organization creation;
either operation may win, but their status pair must be `201/409` or `204/404`, and a direct
PostgreSQL check must find zero memberships attached to soft-deleted users.
When `OPTIONAL_STARTERS` contains `notification`, the verifier also creates two isolated users,
inserts an internal publication with in-app and email deliveries, and exercises list, unread status,
channel validation, cross-user non-disclosure, preferences, and high-water read state over HTTP. It
runs one worker batch without provider credentials and requires the stable terminal result
`EMAIL.NOT_CONFIGURED`, verifies that no recipient or provider-response columns exist, rolls all
three notification tables down, reapplies them, and confirms the HTTP surface recovers.
When `OPTIONAL_STARTERS` contains `asset`, the verifier creates an upload intent, transfers bounded
bytes through the local token adapter, completes inspection, downloads and compares the attachment,
deletes it, verifies post-delete non-disclosure, and exercises the asset migration down/up cycle.
When it contains `organization,setting`, the verifier exercises public ETag revalidation, private
user/organization reads, strong versioned set/reset behavior, account cleanup, and migration
rollback/reapply. When it contains `organization,usage`, the verifier races exact replays and
hard-quota consumers, requires one accepted consume and durable denials at the limit, verifies
private user/organization summaries, account cleanup, receipt pruning, and migration rollback/reapply.
When it contains `organization,webhook`, the verifier creates a secret-bearing endpoint, proves
subsequent lists are secret-free, queues an idempotent fixed test event, dispatches one real local
HTTP attempt through the worker, verifies the minimized terminal ledger, and exercises the webhook
migration rollback/reapply cycle. The local receiver intentionally returns a non-success status; the
check proves transport and persistence behavior without pretending to validate an external consumer.

## Local Compose

`docker-compose.yml` is a development convenience, not a production manifest. It exposes API and
PostgreSQL ports on `127.0.0.1` by default, uses visibly local-only credentials, disables the optional
AI provider, and explicitly starts the single local API process with `fastbuild serve --migrate` after
PostgreSQL becomes healthy. This local opt-in is not the production migration strategy.

```bash
docker compose up --build --wait
docker compose down
```

Override local ports with `FASTBUILD_API_PORT` and `FASTBUILD_DB_PORT`. Override the local database credential
with `FASTBUILD_DB_PASSWORD`. Compose supplies a visibly local-only asset transfer key when the asset
starter is selected. Set `OPTIONAL_STARTERS=organization` to exercise the optional
ownership kernel, `OPTIONAL_STARTERS=notification` for notification persistence and HTTP state, or
`OPTIONAL_STARTERS=asset` for the private asset lifecycle using local object storage. Use
`OPTIONAL_STARTERS=organization,setting` for typed preferences or
`OPTIONAL_STARTERS=organization,usage` for trusted metering and quota checks. Use
`OPTIONAL_STARTERS=organization,webhook` for outbound delivery; Compose supplies a visibly local
encryption key and enables insecure/private targets only inside this development stack. The API
process and its local startup migration receive the same starter selection.
`ORGANIZATION_INVITATION_TTL` is forwarded to the API container and defaults to `168h`.
`docker compose down --volumes` also deletes local database data.

## Production Inputs

A downstream production deployment must inject at least:

- `CORS_ALLOW_ORIGINS`: explicit production browser origins.
- `CORS_ALLOW_HEADERS`: retain `Authorization` and `Organization-Id` when a cross-origin browser
  calls active-organization routes; FastBuild includes both in its default allow-list.
- `DB_DRIVER`, `DB_HOST`, `DB_PORT`, `DB_NAME`, `DB_USERNAME`, `DB_PASSWORD`, and a TLS-requiring
  `DB_SSLMODE` when the default database-backed starters remain installed. Budget
  `DB_MAX_OPEN_CONNS` per API, worker, and command replica against PostgreSQL or pooler capacity;
  see [`DATABASE.md`](DATABASE.md).
- `SERVER_TRUSTED_PROXIES`: only exact ingress/load-balancer IPs or CIDRs when forwarding headers are
  trusted.
- `OPTIONAL_STARTERS`: one identical additive selection for every API replica, migration job, and
  seeder job, plus notification workers and asset cleanup jobs when selected. Omit or set empty when
  no optional starter is enabled.
- Authentication policy when the defaults do not match product requirements:
  `AUTH_SESSION_TTL`, `AUTH_SESSION_IDLE_TIMEOUT`, `AUTH_SESSION_TOUCH_INTERVAL`, and
  `AUTH_SESSION_RETENTION`. Schedule `fastbuild auth-session:prune --batch=500`; no authentication signing
  secret exists.
- Audit retention policy: choose a reviewed cutoff from legal, privacy, security, and storage
  requirements, then schedule bounded `fastbuild audit:prune --before=<RFC3339> --batch=500` runs. FastBuild
  never selects or applies a universal audit retention period automatically.
- Asset deployments: `OBJECT_STORAGE_DRIVER=r2` plus the complete R2 secret group, exact provider
  CORS rules for browser PUT/GET, and a short lifecycle rule for `asset-uploads/`. Do not use a
  container filesystem as durable production storage.
- Transactional mail: a strong `MAILBOX_SECRET_ENCRYPTION_KEY` kept apart from database backups,
  and `RATE_LIMIT_STORE=postgres` with more than one API replica; see
  [Transactional Mail](#transactional-mail).
- Webhook deployments: a strong `WEBHOOK_ENCRYPTION_KEY`, dedicated worker replicas, restricted
  outbound egress, DNS/connection monitoring, and terminal-failure alerting. Never enable insecure
  HTTP or private targets in production.

Keep secrets in the deployment platform's secret store, not in the image, Compose file, repository,
command history, build arguments, or BuildKit metadata. Use secret mounts for build-only credentials.
Keep `/health/live` as the process liveness signal and `/health/ready` as the
traffic-readiness signal. The Docker image health check intentionally uses liveness so a temporary
database outage does not create a restart loop; an orchestrator should remove unready replicas from
traffic based on readiness.

Production logs belong on structured stdout. Collection, retention, rotation, compression, and
external storage are deployment responsibilities; the local file handler is not a production log
shipping system. See [`CONFIGURATION.md`](CONFIGURATION.md) for the typed configuration lifecycle
and [`DATABASE.md`](DATABASE.md) for database startup, pool, TLS, and query measurement ownership.

Run schema migration as an explicit pre-deploy job using the same image and production environment:

```bash
docker run --rm --entrypoint /app/fastbuild <runtime-env-arguments> <image> migrate --force
```

Do not run migrations independently in every application replica. The downstream deployment owns
serialization, failure handling, and rollback policy. Database mutation commands derive production
mode from the validated configuration snapshot; `db:migrate`, `db:rollback`, `db:reset`, and
`db:fresh` require an explicit `--force` in production. `serve --migrate` is rejected in production
because startup replicas are not a migration serialization mechanism. A mismatch in
`OPTIONAL_STARTERS` between the pre-deploy job and serving replicas is a deployment contract
violation: it can produce routes without tables or tables without owning runtime behavior.

## Notification Worker

When `notification` is selected and any publication requests the `email` channel, deploy the same
image as a separate long-running worker:

```bash
/app/fastbuild notification:work --batch=25 --poll=2s
```

Workers require the same database, email secrets, and `OPTIONAL_STARTERS` snapshot as HTTP replicas.
They may scale horizontally because row leases and lease-token completion prevent concurrent owners
from completing the same attempt. Provider calls are not readiness dependencies for the API
process. Monitor worker exits, terminal failure-code counts, oldest pending age, and processing rows
past lease expiry without logging recipients or notification content. Graceful `SIGTERM` stops new
batches and lets the current provider call obey its bounded context. See
[`NOTIFICATIONS.md`](NOTIFICATIONS.md) for retry, privacy, and removal semantics.

## Asset Cleanup Job

When `asset` is selected, run the same image periodically as a bounded cleanup job:

```bash
/app/fastbuild asset:prune --batch=100
```

The job requires the same database, R2 secrets, object-storage policy, and `OPTIONAL_STARTERS`
snapshot as HTTP replicas. Provider lifecycle rules on `asset-uploads/` remain defense in depth.
Monitor nonzero failed counts and oldest pending age without logging filenames, object keys, signed
URLs, checksums, or content. See [`ASSETS.md`](ASSETS.md) for lifecycle, recovery, and removal.

## Mail Broadcast Worker

When `mailbox` is selected and broadcasts are used, deploy the same image as a separate
long-running worker:

```bash
/app/fastbuild mail:broadcast:work --batch=25 --poll=2s
```

It needs the same database, mail provider settings, `MAILBOX_INBOUND_SECRET`, and
`MAILBOX_PUBLIC_URL` as HTTP replicas. Recipients are claimed with row leases, so several workers
can run; each recipient is sent with an idempotency key of its broadcast and contact, so a worker
that stops after the provider call does not mail that contact again. When the organization's
24-hour send limit is reached, the broadcast shows `stop_reason` `send_limit_reached` and resumes
when the window has room.

The same worker sends direct messages that waited for the send limit and deletes for good mailbox
trash older than 30 days. Run it wherever the API runs, even without broadcasts: without it, waiting
messages are never sent and the trash is never purged.

## External Mail Sync Worker

When `EXTERNAL_MAIL_PROVIDERS` enables connectors, deploy the same image as a separate long-running
worker:

```bash
/app/fastbuild mail:external:sync --batch=20 --poll=30s
```

It needs the same database, `EXTERNAL_MAIL_*` settings, and `MAILBOX_SECRET_ENCRYPTION_KEY` as HTTP
replicas. Connections are claimed with row leases, so several workers can run. Without it, bound
accounts are never synced. While `EXTERNAL_MAIL_PROVIDERS` is empty it exits at once with status 0,
so run it only once connectors are on, or under a restart policy that does not restart a clean
exit. Copies of revoked connections are deleted by `mail:broadcast:work`, not by this worker, once
`EXTERNAL_MAIL_REVOKED_RETENTION` has passed, whether or not connectors are on.

The same worker syncs mailboxes assigned from an authorized tenant (`POST /v1/external-mail-tenants`)
with a short-lived token the tenant credential mints for each mailbox; nothing else is configured
for it besides the per-tenant ceiling `EXTERNAL_MAIL_MAX_TENANT_ASSIGNMENTS` (default 1000). It
reaches Google's token endpoint and `admin.googleapis.com` (Directory API) for Workspace tenants, and
`login.microsoftonline.com` and `graph.microsoft.com` for Microsoft 365 tenants, so allow those hosts
in egress. A Microsoft 365 tenant authorized with the operator's application needs that application
(`EXTERNAL_MAIL_OUTLOOK_CLIENT_ID`) to request the Graph application permissions `Mail.Read` and
`User.Read.All` and to list `EXTERNAL_MAIL_REDIRECT_URL`, which receives the admin consent as well.

## Transactional Mail

`MAILBOX_TRANSACTIONAL_ENABLED=true` turns on `POST /v1/transactional-messages` and the console's
test sends. It requires `MAILBOX_SECRET_ENCRYPTION_KEY` (at least 32 characters), or the API and
worker refuse to start. Give every API replica and every `mail:broadcast:work` worker the same
value: the key encrypts message content while it waits to be sent and keys the idempotency
fingerprints. The API sends each message right after the request commits; the
`mail:broadcast:work` worker sends messages that waited for the transactional lane or a retry, fails
messages that waited more than 1 hour (`send.expired`) or whose provider call outlived its lease
(`transport.unknown_outcome`), and deletes messages older than 30 days. Without the worker, retries
and lane waits never finish and old messages are never purged.

- **Keep the key apart from database backups.** Store it in the secret store, never next to a dump
  or snapshot: a backup together with the key exposes the content of every message that was still
  waiting when the backup was taken. Content is wiped when a message reaches a final state, so a
  backup without the key holds only ciphertext of waiting messages.
- **Share the rate limits across replicas.** The per-key (`MAILBOX_TRANSACTIONAL_KEY_PER_SECOND`)
  and per-recipient (`MAILBOX_TRANSACTIONAL_RECIPIENT_PER_MINUTE`) limits use the shared rate-limit
  store. With more than one API replica set `RATE_LIMIT_STORE=postgres`; with the default `memory`
  store each replica enforces its own budget, so N replicas allow N times the limit.
- **Size the lane.** `MAILBOX_DAILY_TRANSACTIONAL_LIMIT` (unset: the value of
  `MAILBOX_DAILY_SEND_LIMIT`) counts live, non-simulated messages per organization over 24 hours,
  separately from the personal and broadcast lanes. Keep the sum of the three lanes within the
  provider account's sending limit.

To rotate `MAILBOX_SECRET_ENCRYPTION_KEY`:

1. Set `MAILBOX_TRANSACTIONAL_ENABLED=false` on every API replica and roll them out. New requests
   answer `503 MAILBOX.TRANSACTIONAL_DISABLED`. Keep the old key set: the worker keeps sending
   stored messages with it.
2. Drain: wait until no transactional message is `queued`, for example
   `SELECT count(*) FROM transactional_messages WHERE status = 'queued'` returns 0. Messages that
   wait for the lane fail with `send.expired` within an hour at most.
3. Set the new key on every API replica and worker, turn transactional mail back on, and roll out.

A message still queued under the old key fails with `send.payload_unavailable` and logs
`transactional_payload.decrypt_failed`. Within 24 hours after the rotation, repeating a request
with an `Idempotency-Key` used before it answers `409 MAILBOX.IDEMPOTENCY_CONFLICT`, because the
fingerprint changed; clients should use new keys after a rotation.

## Mail Zone Worker

When `mailbox` is selected with `MAILBOX_CUSTOM_DOMAINS_ENABLED=true`, deploy one more worker of the
same image:

```bash
/app/fastbuild mail:zone:work --batch=100 --poll=10m
```

Each pass reads up to `--batch` pending managed zones from Cloudflare, least recently checked
first. A zone Cloudflare reports `active` becomes active and its pending domains are set up; a zone
still pending 14 days after it was created is deleted at Cloudflare with its domains. The worker
waits `--poll` (at most `1h`) between passes even when a pass was full, so more pending zones than
`--batch` take several passes. It needs the same database and Cloudflare settings as HTTP replicas.

Each pass also sets up the pending domains of active zones that were never checked, such as when an
earlier worker stopped right after an activation. Before it exits, including on `SIGTERM` and after
`--once`, the worker waits up to two minutes for the domain setups it started, so give it a
`terminationGracePeriodSeconds` of at least 150 (or the equivalent stop timeout outside Kubernetes).

### Releasing A Pending Zone

An organization can hold an apex in a pending zone for up to 14 days without delegating it, which
keeps every other organization from connecting a name under it. When the real owner of the domain
asks for it, release the zone with the same image and settings:

```bash
/app/fastbuild mail:zone:release example.com          # refuses while domains live in the zone
/app/fastbuild mail:zone:release --force example.com  # also removes the zone's pending domains
```

The command deletes the zone at Cloudflare and in the database and logs the zone, its organization,
and its Cloudflare zone ID. It refuses an active zone: that zone serves DNS the registrar points at,
and its organization removes it from the console once the name servers point elsewhere. Tell the
organization that held the zone, since its pending domains disappear from its console.

### Operator Organization Commands

These one-off commands act on an organization without being one of its members, so they run only
on the server, with the same image and settings as the API. None of them reads or prints a
password or token.

```bash
/app/fastbuild organization:show --domain=example.com      # or --organization=<id>
/app/fastbuild organization:grant-admin --organization=<id> --email=person@example.com [--commit]
/app/fastbuild mailbox:import --organization=<id> --file=/tmp/mailboxes.csv [--actor=<user id>] [--commit]
```

`organization:show` prints the organization's ID and name, its owners and admins (user ID, email,
role), its mailbox count against the configured limit, and its connected domains with their
status; with `--domain` it also says whether that domain is verified.

`organization:grant-admin` needs an account that already uses the email (matched ignoring case):
it never creates an account. It adds an `admin` membership, raises a `member` to `admin`, and
leaves an admin or owner as they are; it never grants `owner`. Without `--commit` it only prints
what it would do. A written change is recorded in the audit log with the system as actor, as
`add_member` or `change_role` on `organization_members`.

`mailbox:import` reads a CSV file with the header `address,display_name,members`, the format of
the console import and of `GET /v1/mailbox-exports`; other columns are ignored and at most 500 rows
are read. A row whose `members` cell is empty gets the acting user as its one member with
`manage`. The acting user is `--actor`, which must be an owner or admin of the organization, or
else the organization's earliest owner. The rows go through the same import service as the
console, so Cloudflare routing rules, the mailbox limit, validation, and mailbox access events
behave the same. Without `--commit` it is a dry run. It prints one line per row (row, address,
`valid`/`created`/`failed`, error code, member emails that are not in the organization) and the
totals, and exits non-zero when any row failed. A committed import is recorded in the audit log
with the system as actor.

## Webhook Worker And Retention

When `webhook` is selected, deploy the same image as a separate long-running worker:

```bash
/app/fastbuild webhook:work --batch=25 --poll=2s
```

Workers require the same database, encryption key, delivery policy, and `OPTIONAL_STARTERS` snapshot
as HTTP replicas. Row leases and lease-token completion allow horizontal scaling, while endpoint DNS
resolution and bounded receiver calls happen outside claim transactions. Restrict worker egress at
the deployment boundary and monitor oldest pending age, lease expiry, retry/terminal failure codes,
and automatic endpoint disables without logging URLs, DNS answers, payloads, secrets, signatures,
response bodies, or free-form network errors.

Run retention as a bounded scheduled job:

```bash
/app/fastbuild webhook:prune
```

Operator replay is explicit through `webhook:replay`; automatic retries and replay preserve the
original message ID. See [`WEBHOOKS.md`](WEBHOOKS.md) for signing, retry, SSRF, privacy, rotation,
and removal semantics.

## Change Checklist

When changing container behavior:

1. Keep image defaults production-safe and Compose defaults local-only.
2. Do not copy environment files or credentials into the image.
3. Keep `health:check`, the Docker `HEALTHCHECK`, and actual health routes aligned.
4. Keep request logs visible through container stdout when file logging is disabled.
5. Run `make container-check`, the root image scan/SBOM commands, `docker compose config --quiet`, and
   the normal API verification tier.
6. Run `make compose-check` when Compose, migrations, database startup, or default starters change.
7. Keep digest, material-expectation, OCI-label, CI evidence, and governance updates in one review.
