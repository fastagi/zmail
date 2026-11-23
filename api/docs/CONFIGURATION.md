# Configuration

FastBuild uses one typed configuration authority: [`internal/infra/config.Config`](../internal/infra/config/config.go).
Environment files are inputs to that startup snapshot; they are not a second configuration API.

## Authority And Lifecycle

1. A process calls `config.Load()` during bootstrap.
2. `pkg/env` resolves process variables and optional environment files.
3. `config.Load()` converts those strings into typed fields and validates cross-field invariants.
4. Bootstrap gives that same `*config.Config` pointer to logging and the Wire dependency graph.
5. The snapshot remains fixed for the lifetime of that process.

New runtime code should receive `*config.Config` or a smaller capability-owned typed config through
dependency injection. `config.GlobalConfig` remains a read-only compatibility surface for existing
middleware and the welcome route; do not make new features depend on it.

`config.Use()` exists for tests and alternate bootstraps that already hold a fully constructed
configuration. `config.LoadFresh()` exists for tests and one-shot diagnostics. Neither function
rebuilds already constructed services.

`config.LoadAIConfig()` is the capability-scoped loader for `ai:chat`; it returns the same typed
`AIConfig` used by the full snapshot without requiring unrelated database or authentication-session settings. Runtime
packages still do not read environment variables directly. It also applies the same enablement,
endpoint, timeout, and byte-limit validation as the full application loader.

## Precedence

From highest to lowest:

1. Process environment variables supplied by the shell, container runtime, or orchestrator.
2. The file explicitly selected by process variable `FASTBUILD_ENV_FILE`.
3. `.env.<APP_ENV>.local`.
4. `.env.local`.
5. `.env.<APP_ENV>`.
6. `.env`.
7. Defaults in `config.Load()`.

An explicitly present process value, including an empty string, wins over every file value.
`FASTBUILD_IGNORE_ENV_FILES=true` in the process environment skips layers 2–6, so tools such as the
contract route check assemble the application from explicit values only.
`APP_ENV` file selection is resolved from the process environment first, then `FASTBUILD_ENV_FILE`, then
the base `.env`. `GO_ENV` and `GIN_MODE` are accepted as fallback inputs and normalized to the same
application environment. Environment-specific and local files cannot change the identity after file
selection, so the loaded file set and production defaults cannot disagree.

`LoadFresh()` removes values injected by the previous file snapshot before resolving the layers
again. If application or test code changed one of those values at runtime, the changed process value
is preserved as the new highest-priority input. Missing optional files are normal, while malformed
files and a missing explicitly selected `FASTBUILD_ENV_FILE` fail typed configuration loading.

`production`, `prod`, and `release` are equivalent production aliases. Without explicit overrides,
they disable application debug output and local file logging, select Gin release mode, emit JSON logs
to stdout, enable the scaffold rate-limit guardrails, and keep metrics disabled.

## Restart, Not Hot Reload

Configuration changes require a process restart. Watching `.env` and mutating a global key/value map
would leave database pools, HTTP servers, clients, middleware, and workers on different snapshots.
FastBuild therefore does not expose runtime configuration watching. `make air` rebuilds and restarts the
development process, which creates a complete new dependency graph.

## Database Runtime Policy

Database configuration is a strict subsection of the startup snapshot. PostgreSQL is the only
relational database compatibility target for runtime, migrations, repositories, and integration
tests. SQLite adapters and tests must remain absent. `DB_DRIVER` must be `postgres`, pool limits must
remain finite and internally coherent, and connection idle, lifetime, startup timeout, and
slow-query durations must be positive. Database durations prefer unit-bearing values such as `15m`
and `1h`; legacy integer seconds remain accepted for compatibility. Malformed values fail instead
of silently selecting defaults.

Production PostgreSQL requires `DB_SSLMODE=require`, `verify-ca`, or `verify-full`. The runtime
percent-encodes credentials into a URI, applies the pool policy before a single deadline-bound ping,
and closes a failed startup pool. Complete configuration, lifecycle, query-baseline, and benchmark
guidance lives in [`DATABASE.md`](DATABASE.md).

## Optional Starter Activation

`OPTIONAL_STARTERS` is a comma-separated, additive list of canonical starter names. The default is
empty; `audit`, `apikey`, and `user` remain active without being named. Available values are
`organization`, `permission`, `notification`, `asset`, `setting`, `usage`, `webhook`, and
`operator`; permission, setting, usage, and webhook explicitly depend on organization, while
notification, asset, and operator can be selected independently:

```dotenv
OPTIONAL_STARTERS=organization,permission
ORGANIZATION_INVITATION_TTL=168h
# or: OPTIONAL_STARTERS=notification
# or: OPTIONAL_STARTERS=asset
# or: OPTIONAL_STARTERS=organization,setting
# or: OPTIONAL_STARTERS=organization,usage
# or: OPTIONAL_STARTERS=organization,webhook
```

Selection is resolved through `internal/starter` from the same typed configuration snapshot used by
Wire. Unknown names, duplicates, non-lowercase names, default starter names, missing dependencies,
and dependency cycles fail before server startup or CLI database work. Dependency order is resolved
deterministically. HTTP routes, runtime hooks, migrations, and seeders consume the same selection;
do not maintain a second starter list in a command or deployment script.

The operator CLI reads the same catalog without initializing the database or HTTP application:

```bash
go run ./cmd/fastbuild starter:list
go run ./cmd/fastbuild starter:list --format=json
go run ./cmd/fastbuild starter:enable permission --dry-run
go run ./cmd/fastbuild starter:enable permission
go run ./cmd/fastbuild starter:check
go run ./cmd/fastbuild starter:disable organization --cascade
```

`starter:enable` adds transitive dependencies. `starter:disable` protects selected dependents unless
`--cascade` is explicit. Both commands support `--env-file`, write with an atomic same-directory
replacement, and refuse to modify `.env.example`. When the target `.env` does not exist, its sibling
`.env.example` is used as the initial template. The JSON catalog is schema-versioned for external
project tooling.

All API replicas, migration jobs, and seeder jobs for one environment must receive the same value.
Changing it requires a deployment and, when enabling a persistence-owning starter, its pre-deploy
migration. It is not a per-request flag and must not be toggled independently across replicas.

`ORGANIZATION_INVITATION_TTL` controls the lifetime of one-time invitation tokens and defaults to
`168h` (7 days). It must be positive when `organization` is selected. Invitation expiry is evaluated
against this immutable startup policy; changing the value affects newly created invitations only and
requires a process restart like every other configuration change.

## Authentication Session Policy

The default `user` starter issues opaque server-side authentication sessions. Policy is typed and
restart-scoped; no JWT signing key or browser-readable claim is involved:

| Variable | Default | Validation |
|---|---:|---|
| `AUTH_SESSION_TTL` | `720h` | Absolute lifetime, 15 minutes through 180 days |
| `AUTH_SESSION_IDLE_TIMEOUT` | `168h` | Inactivity lifetime, 5 minutes through the absolute TTL |
| `AUTH_SESSION_TOUCH_INTERVAL` | `5m` | Activity-write throttle, positive, no more than one hour or the idle timeout |
| `AUTH_SESSION_RETENTION` | `720h` | Terminal-row retention, zero through 365 days |
| `AUTH_REGISTRATION_ENABLED` | `true` | Boolean; `false` closes public registration (`POST /v1/register` answers `403 AUTH.FORBIDDEN`) |
| `AUTH_TOTP_ENCRYPTION_KEY` | empty | Encrypts authenticator secrets for two-factor sign-in (at least 32 characters). Empty turns new enrollment off; existing factors are still enforced and recovery codes still work. Changing it makes existing authenticator codes fail until users sign in with a recovery code and re-enroll |
| `GOOGLE_OAUTH_CLIENT_ID` | empty | OAuth client ID of "Sign in with Google"; set together with the secret, at most 255 characters. Empty hides the button and `/v1/auth/google/*` answers `503 AUTH.GOOGLE_NOT_CONFIGURED`. The client's authorized redirect URI is `<NEXT_PUBLIC_APP_URL>/api/auth/google/callback` |
| `GOOGLE_OAUTH_CLIENT_SECRET` | empty | OAuth client secret of "Sign in with Google"; set together with the client ID. Separate from `EXTERNAL_MAIL_GMAIL_CLIENT_SECRET` |

The API remains authoritative even when a browser cookie has a longer local lifetime: revocation,
idle expiry, account state, password security events, and persistence availability are checked on
every protected request. Run `fastbuild auth-session:prune --batch=500` as a bounded scheduled job.

`JWT_SECRET` and `JWT_EXPIRE_DAYS` are retired keys. A non-empty legacy value fails startup instead
of creating the false impression that stateless JWTs are still accepted. See
[`AUTHENTICATION.md`](AUTHENTICATION.md) for the breaking migration and rollback boundary.

## Rate-Limit Resource Policy

The production-default global and authentication limiters are process-local fixed-window stores.
`MIDDLEWARE_RATE_LIMIT_MAX_BUCKETS` caps active client-IP buckets for the global store;
`AUTH_RATE_LIMIT_MAX_BUCKETS_PER_RULE` caps each endpoint/dimension store. Both default to 10,000
and must be positive when their limiter is enabled. Reaching the cap evicts the least recently used
bucket, bounding memory while preserving hot abusive identities. With N replicas, process-local
stores admit up to N times every configured budget.

| Key | Default | Meaning |
|---|---|---|
| `RATE_LIMIT_STORE` | `memory` | `memory` keeps per-replica buckets; `postgres` shares one budget per key across replicas through the `rate_limit_buckets` table and requires `DB_ENABLED=true` plus migrations |
| `RATE_LIMIT_STORE_TIMEOUT` | `50ms` | Budget for one shared decision, greater than 0 and at most `1s`; checked only for `postgres` |
| `RATE_LIMIT_STORE_FAILURE` | `local` | Decision when PostgreSQL errors or times out: `local` enforces a process-local budget, `open` allows, `closed` denies with `429`; checked only for `postgres` |

The shared store costs one PostgreSQL round trip and one pooled connection per rate-limited request,
stores only SHA-256 key digests, and needs no cleanup job: each process deletes bounded batches of
expired rows opportunistically, and `fastbuild ratelimit:prune` does the same on demand. The
`MAX_BUCKETS` keys still bound the `local` fallback buckets. See
[`MIDDLEWARE.md`](MIDDLEWARE.md#shared-postgresql-store) for the algorithm, outage metrics, and why a
gateway/WAF remains the flood-protection layer.

There is no automatic Redis activation. The removed `REDIS_HOST`, `REDIS_PORT`, `REDIS_PASSWORD`,
and `REDIS_DB` keys had no assembled runtime consumer and therefore violated the typed configuration
authority. The generic Redis cache adapter remains available for explicit downstream composition;
it receives a borrowed client and typed adapter config directly, does not read environment variables,
and is documented in [`CACHE.md`](CACHE.md). It is not a rate-limit driver. Multi-replica rate
limiting uses `RATE_LIMIT_STORE=postgres` or the gateway/WAF.

## Platform Operator Configuration

Selecting `operator` requires `OPERATOR_ALLOWED_ORIGINS`: the exact Admin Console origins
(`scheme://host[:port]`, comma separated, no wildcards or paths, `https` in production). Unsafe
operator requests must carry one of these origins. `OPERATOR_SESSION_COOKIE_NAME` optionally renames
the session cookie; the default is `__Host-fastbuild_operator` in production and `fastbuild_operator` elsewhere,
and the `__Host-` prefix is rejected outside production because it requires a `Secure` cookie. See
[`../../contracts/OPERATORS.md`](../../contracts/OPERATORS.md).

## Outbound Webhook Configuration

Selecting `webhook` requires `WEBHOOK_ENCRYPTION_KEY` with at least 32 characters. It protects
endpoint signing secrets at rest and must be supplied from the deployment secret store. Keep the
same key available to HTTP replicas, workers, replay/prune jobs, and migration-safe rollback windows;
changing it without an explicit data-key migration makes existing endpoints unreadable.

`WEBHOOK_REQUEST_TIMEOUT` bounds each receiver call and defaults to `15s` with a hard maximum of
`30s`. `WEBHOOK_MAX_RESPONSE_BYTES` bounds response draining and defaults to 65,536 bytes. Responses
are never persisted. `WEBHOOK_SECRET_OVERLAP` controls the previous-secret signing window and
defaults to `24h`; `WEBHOOK_EVENT_RETENTION` controls terminal replay history and defaults to `720h`
(30 days). Startup rejects values outside the documented safety bounds.

`WEBHOOK_ALLOW_INSECURE_HTTP` and `WEBHOOK_ALLOW_PRIVATE_TARGETS` exist only for local verification.
Production rejects either override. Local Compose enables them so its isolated verifier can dispatch
to the API container itself; downstream production deployments should also enforce outbound network
policy independently of application validation. See [`WEBHOOKS.md`](WEBHOOKS.md).

## AI Capability Configuration

`AI_ENABLED` defaults to `false`. Enabling it requires an explicit `AI_DEFAULT_MODEL` and the secret
for the selected registered provider; FastBuild intentionally does not choose a model whose behavior and
availability can change independently of the scaffold. The built-in provider requires
`OPENAI_API_KEY`. `OPENAI_BASE_URL` must be an absolute HTTP(S) URL without credentials, query, or
fragment, and production requires HTTPS.

`AI_REQUEST_TIMEOUT` defaults to `120s` and bounds both a one-shot request and the complete lifetime
of a streaming session. `AI_MAX_INPUT_BYTES`, `AI_MAX_RESPONSE_BYTES`, and
`AI_MAX_STREAM_EVENT_BYTES` default to 1 MiB, 4 MiB, and 1 MiB respectively. Typed startup validation
keeps each value inside its documented hard range and ensures the stream-event cap cannot exceed the
response cap. See [`AI.md`](AI.md) for transport, privacy, error, retry, and product-boundary rules.

## Object Storage And Asset Configuration

`OBJECT_STORAGE_DRIVER` selects the provider-neutral object adapter. It defaults to `disabled`,
except that selecting `asset` outside production defaults it to `local`. A production process with
the asset starter fails validation unless the driver is explicitly `r2`; container-local storage is
never a production fallback.

The local driver uses `OBJECT_STORAGE_LOCAL_ROOT` and is intended for private development data only.
The R2 driver requires the all-or-none group `R2_ACCESS_KEY_ID`, `R2_SECRET_ACCESS_KEY`,
`R2_BUCKET`, `R2_REGION`, and `R2_ENDPOINT`. Production requires an HTTPS endpoint.
`OBJECT_STORAGE_REQUEST_TIMEOUT` bounds provider operations and defaults to `30s`.

The local asset adapter additionally requires a purpose-specific `ASSET_TRANSFER_SIGNING_KEY` of at
least 32 characters. R2 uses provider-owned presigning and does not consume that local key. Asset
policy is independently typed through `ASSET_MAX_BYTES`, `ASSET_UPLOAD_GRANT_TTL`,
`ASSET_DOWNLOAD_GRANT_TTL`, and `ASSET_PENDING_TTL`. Size and lifetime bounds fail startup rather
than weakening upload or cleanup behavior. See [`ASSETS.md`](ASSETS.md) for the storage boundary,
CORS/lifecycle deployment responsibilities, and privacy rules.

## Email Provider Configuration

The optional email capability reads `MAIL_PROVIDER`, `MAIL_FROM`, `RESEND_API_KEY`,
`MAIL_CLOUDFLARE_ACCOUNT_ID`, `MAIL_CLOUDFLARE_API_TOKEN`, and `MAIL_REQUEST_TIMEOUT` from the same
typed snapshot. `MAIL_PROVIDER` is `resend` (the default) or `cloudflare`. With Resend, sender and API
key are an all-or-none pair; partial configuration fails startup instead of silently skipping
delivery. With Cloudflare, `MAIL_FROM` is required and must be on a domain enabled for Cloudflare
Email Sending, such as `Zmail <noreply@mail.example.com>`; the account ID and token default to the
`MAILBOX_CLOUDFLARE_*` values. `log` sends nothing: it writes each message, including one-time
codes and reset tokens, to the API log as `email.log_delivery`, so startup rejects it unless
`APP_ENV` is `development`; `make dev` uses it. When configured, the sender must be a valid mailbox
and the timeout must be positive. The default provider request timeout is 10 seconds.

The timeout is a duration value such as `3s` or `500ms`, not a bare millisecond count. It bounds each
provider call independently and composes with caller cancellation. See [`EMAIL.md`](EMAIL.md) for the
64 KiB response cap, privacy rules, best-effort semantics, and downstream adapter boundary.

## Web Push Configuration

The notification starter's `web_push` channel reads `WEB_PUSH_VAPID_PUBLIC_KEY`,
`WEB_PUSH_VAPID_PRIVATE_KEY`, and `WEB_PUSH_SUBJECT`. The keys are a base64url P-256 VAPID pair (RFC
8292): the public key is the 65-byte uncompressed point browsers subscribe with, the private key the
32-byte scalar. `WEB_PUSH_SUBJECT` is a `mailto:` or `https:` contact for push services. Push is on
only when all three are set and the pair is valid; otherwise it is off without failing startup,
`GET /v1/web-push-config` answers `enabled: false`, subscription registration answers
`503 NOTIFICATION.WEB_PUSH_DISABLED`, and pending push deliveries fail with
`WEB_PUSH.NOT_CONFIGURED`. A partial or invalid setting logs one `notification.web_push_disabled`
warning at boot that names the problem, never the values. See
[`../../contracts/NOTIFICATIONS.md`](../../contracts/NOTIFICATIONS.md).

## Mailbox Starter Configuration

The optional `mailbox` starter requires the `organization` starter and reads `MAILBOX_DOMAIN`, `MAILBOX_PROVIDER`,
`MAILBOX_CLOUDFLARE_ACCOUNT_ID`, `MAILBOX_CLOUDFLARE_API_TOKEN`, `MAILBOX_REQUEST_TIMEOUT`,
`MAILBOX_INBOUND_SECRET`, `MAILBOX_DAILY_SEND_LIMIT`, `MAILBOX_DAILY_BROADCAST_LIMIT`,
`MAILBOX_DAILY_TRANSACTIONAL_LIMIT`, `MAILBOX_MAX_PER_ORGANIZATION`, `MAILBOX_PUBLIC_URL`,
`MAILBOX_CUSTOM_DOMAINS_ENABLED`, `MAILBOX_CLOUDFLARE_EMAIL_WORKER`,
`MAILBOX_MAX_ZONES_PER_ORGANIZATION`, `MAILBOX_TRANSACTIONAL_ENABLED`,
`MAILBOX_SECRET_ENCRYPTION_KEY`, `MAILBOX_SIMULATOR_DOMAIN`,
`MAILBOX_TRANSACTIONAL_RECIPIENT_PER_MINUTE`, and `MAILBOX_TRANSACTIONAL_KEY_PER_SECOND` from the
same typed snapshot. They are
validated only when the starter is selected.

| Key | Default | Rule |
|---|---|---|
| `MAILBOX_DOMAIN` | `mail.localhost` outside production | The address domain of every mailbox. Production requires an explicit lowercase domain. |
| `MAILBOX_PROVIDER` | `log` outside production | `log` records outbound mail without delivering it and is rejected in production. `cloudflare` delivers through the Cloudflare Email Sending REST API. |
| `MAILBOX_CLOUDFLARE_ACCOUNT_ID`, `MAILBOX_CLOUDFLARE_API_TOKEN` | empty | Both are required when the provider is `cloudflare`. The token is a secret. |
| `MAILBOX_REQUEST_TIMEOUT` | `15s` | Bounds one provider call; greater than 0 and at most `60s`. |
| `MAILBOX_INBOUND_SECRET` | empty | Shared secret of at least 32 characters that signs deliveries from the mail edge. Empty keeps inbound mail disabled. |
| `MAIL_APP_PASSWORD_PEPPER` | empty | Secret of at least 32 characters that keys the HMAC-SHA256 of every mail client password (see `contracts/MAILBOXES.md`, Mail Client Access). No default, also outside production: empty keeps creating mail client passwords off. Changing it makes every existing password stop working. |
| `MAIL_GATEWAY_PUBLIC_IMAP_HOST`, `MAIL_GATEWAY_PUBLIC_IMAP_PORT` | `imap.zmail.im`, `993` | The IMAP server the console shows members for their mail apps; a host name and a port from 1 to 65535. |
| `MAIL_GATEWAY_PUBLIC_SMTP_HOST`, `MAIL_GATEWAY_PUBLIC_SMTP_PORT`, `MAIL_GATEWAY_PUBLIC_SUBMISSION_PORT` | `smtp.zmail.im`, `465`, `587` | The SMTP server the console shows members for sending: the implicit-TLS port and the STARTTLS port; a host name and ports from 1 to 65535. The gateway certificate must cover this host. |
| `MAIL_GATEWAY_IMAP_ADDR` | empty | Implicit-TLS IMAP listen address of `fastbuild mail:gateway`, such as `:993`. Empty leaves IMAP off. With every gateway address empty the command exits cleanly. Any address requires the mailbox starter, `MAIL_APP_PASSWORD_PEPPER`, and both TLS files. |
| `MAIL_GATEWAY_SMTPS_ADDR`, `MAIL_GATEWAY_SUBMISSION_ADDR` | empty | SMTP submission listen addresses of `fastbuild mail:gateway`: implicit TLS (public port 465) and STARTTLS (public port 587; AUTH only after STARTTLS). A container binds unprivileged ports such as `:1465` and `:1587` and publishes `465:1465` and `587:1587`. Each stays off when empty; they share the TLS files, sign-in limits, and connection caps with IMAP, and need a mail client password with the `smtp` scope. |
| `MAIL_GATEWAY_TLS_CERT_FILE`, `MAIL_GATEWAY_TLS_KEY_FILE` | empty | PEM certificate chain and key of the gateway; re-read when they change on disk, so a renewed certificate needs no restart. TLS 1.2 is the minimum. |
| `MAIL_GATEWAY_HEALTH_ADDR` | `127.0.0.1:18992` | Serves `/health` and Prometheus `/metrics` of the gateway; keep it on a private interface. |
| `MAIL_GATEWAY_MAX_CONNECTIONS`, `MAIL_GATEWAY_MAX_CONNECTIONS_PER_USER`, `MAIL_GATEWAY_MAX_CONNECTIONS_PER_IP` | `1000`, `20`, `100` | Connection caps of one gateway process; the total is 1 to 100000 and each per-user and per-address cap at most the total. |
| `MAIL_GATEWAY_REVALIDATE_INTERVAL` | `20s` | How often live sessions are re-checked against password revocation, the organization switch, the member feature, and mailbox access; greater than 0 and at most `30s`, so a revoked session ends within a minute. |
| `MAILBOX_DAILY_SEND_LIMIT` | `200` | The personal lane: console sends, replies, API outbound messages, and template test sends one organization may attempt per rolling 24 hours; 1 to 100000. |
| `MAILBOX_DAILY_BROADCAST_LIMIT` | value of `MAILBOX_DAILY_SEND_LIMIT` | The broadcast lane: broadcast recipients one organization may attempt per rolling 24 hours; 1 to 100000. Unset or `0` takes the personal limit. |
| `MAILBOX_NEW_ORGANIZATION_DAILY_SEND_LIMIT` | `0` | Caps each lane of an organization younger than `MAILBOX_NEW_ORGANIZATION_PERIOD`, and the personal messages it may have waiting; 0 to 100000, `0` turns the cap off. Lane limits below the cap are unchanged. |
| `MAILBOX_NEW_ORGANIZATION_PERIOD` | `168h` | How long after creation an organization counts as new; greater than 0 and at most `8784h` when the cap is set. |
| `MAILBOX_DAILY_TRANSACTIONAL_LIMIT` | value of `MAILBOX_DAILY_SEND_LIMIT` | The transactional lane: live, non-simulated transactional messages one organization may attempt per rolling 24 hours; 1 to 100000. Unset or `0` takes the personal limit. Lanes are counted separately, so an organization's total can reach the sum of the three; keep it within the provider account's limit. |
| `MAILBOX_MAX_PER_ORGANIZATION` | `50` | Mailboxes one organization may own across all domains; 1 to 5000. Lowering it below an organization's current count keeps every existing mailbox; only new creates and import rows get `409 MAILBOX.LIMIT_REACHED`. |
| `MAILBOX_PUBLIC_URL` | empty | Public origin of the Web console that broadcast unsubscribe links and organization invitation links (in email and in the manager's one-time copy) point at. Empty keeps broadcasts off and invitations link-free. |
| `MAILBOX_CUSTOM_DOMAINS_ENABLED` | `false` | Lets owners and admins connect their own domains: into a zone the operator added to the Cloudflare account, or into a managed zone Zmail creates for an apex the account does not hold. Requires the `cloudflare` provider. The token then needs Account → Zone: Edit, DNS Edit, Zone Settings Edit, Email Routing Rules Edit, and Email Sending Edit, with zone resources set to all zones of the account, since a token scoped to named zones cannot reach the zones Zmail creates later. Any organization may claim a domain in any zone the operator added, so keep zones organizations must not use in a separate Cloudflare account. |
| `MAILBOX_CLOUDFLARE_EMAIL_WORKER` | empty | The Email Worker that routing rules of connected domains deliver to; required when custom domains are enabled. |
| `MAILBOX_MAX_ZONES_PER_ORGANIZATION` | `3` | Managed zones Zmail may create in the Cloudflare account for one organization; 1 to 20. |
| `MAILBOX_TRANSACTIONAL_ENABLED` | `false` | Accepts `POST /v1/transactional-messages` and console test sends. Requires `MAILBOX_SECRET_ENCRYPTION_KEY`; off, both answer `503 MAILBOX.TRANSACTIONAL_DISABLED`. |
| `MAILBOX_SECRET_ENCRYPTION_KEY` | empty | At least 32 characters; required when transactional mail is enabled. Encrypts queued transactional content (AES-256-GCM) and keys the idempotency fingerprints. The key is a secret: keep it apart from database backups, and turn transactional mail off and let the queue drain before rotating it. |
| `MAILBOX_SIMULATOR_DOMAIN` | `test.zmail.im` | Addresses on this domain (`delivered`, `bounced`, `soft-bounced`, `complained`, `suppressed`, each with an optional `+tag`) are simulated and never reach the provider. |
| `MAILBOX_TRANSACTIONAL_RECIPIENT_PER_MINUTE` | `3` | Transactional messages of one organization, mode, template, and recipient per minute; 1 to 1000. |
| `MAILBOX_TRANSACTIONAL_KEY_PER_SECOND` | `20` | Transactional messages one API key may send per second; 1 to 10000. The rate limits use `RATE_LIMIT_STORE`: with `memory`, each replica counts on its own. |

See [`../../contracts/MAILBOXES.md`](../../contracts/MAILBOXES.md) for the HTTP behavior and
[`../../deploy/cloudflare/email-worker/README.md`](../../deploy/cloudflare/email-worker/README.md)
for the mail edge that signs inbound deliveries.

## Secrets And Deployment

FastBuild does not serialize a configuration cache. A cache would duplicate database
credentials, provider keys, and other secrets on disk while bypassing the deployment platform's
secret lifecycle. Production images also do not contain `.env` or `.env.example`; inject required
values through the runtime or a mounted secret source.

Keep `.env.example` as the schema of available keys and safe local examples. It is not a claim that
every deployment must set every key. Required and conditional values belong in typed validation.

Local file logging is intentionally simple. Production should use structured stdout and let the
runtime platform own collection, retention, rotation, and compression; FastBuild does not advertise
in-process rotation or a storage sink that does not actually persist records.

## Adding A Key

1. Add the typed field to `Config` or the owning nested config.
2. Read it once in `config.Load()` with an explicit default.
3. Add validation for unsafe or contradictory combinations.
4. Document it in `.env.example` without a real secret.
5. Pass a smaller typed configuration into the owning capability when practical.
6. Add loading and validation tests, including production behavior when relevant.

Do not add dot-notation string accessors, parallel dynamic repositories, or package-specific calls to
`os.Getenv` for runtime settings.

## Doctor

Run the diagnostic from the API directory:

```bash
cd api && go run ./cmd/fastbuild doctor
```

Use `--env-example=/path/to/.env.example` for a downstream schema. Doctor rejects malformed or
duplicate declarations, runs the same typed configuration validation as bootstrap, and reports
security-sensitive combinations. Provider model IDs are treated as provider-owned opaque values;
FastBuild only checks whether the selected provider has a built-in adapter and its required connection
settings.

## Verification

```bash
go test ./pkg/env -run '^TestEnv_'
go test ./internal/infra/config ./internal/infra/email ./internal/infra/storage ./internal/infra/console/commands
go test -race ./pkg/env ./internal/infra/config ./internal/infra/email ./internal/infra/storage ./internal/infra/console/commands
```
