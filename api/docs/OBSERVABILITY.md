# Observability And Sensitive Data

FastBuild treats logs, traces, local diagnostics, and audit records as durable data boundaries. They are
useful for correlation and operations, but they are not a second copy of requests or secrets.

## Request Logs

The default Gin request logger records status, latency, client IP, method, route-template path, and
`request_id`. It deliberately:

- records `/users/:id` rather than the concrete path parameter;
- records `unmatched` instead of attacker-controlled paths when no route matches;
- never records raw query strings or query values;
- never records request or response bodies;
- passes structured context through `pkg/redact` before any handler receives it.

Route templates and the unmatched-route sentinel reduce credential/PII exposure and bound log
cardinality. Do not place credentials in URLs even though FastBuild minimizes their telemetry surface.

## Redaction Contract

`pkg/redact` is the reusable, standard-library-only boundary for credential-shaped fields. It
normalizes key spelling and replaces password, secret, token, authorization, cookie, API-key,
session, OTP, private/signing/encryption-key, credential, and signature values with `[REDACTED]`.
Query-only `code`, `key`, and `sig` parameters are also treated as credentials. Nested string-key
maps and slices are copied and sanitized to a bounded depth; caller-owned values are not mutated.

This is defense in depth, not permission to log secrets. A secret hidden under an ambiguous key or
embedded in a free-form message cannot be classified reliably. Callers must still avoid credentials,
full email addresses, phone numbers, payment data, and raw provider payloads.

## Exception Center

The HTML exception center is development-only. It collects no body, redacts sensitive headers and
query values, uses the route template in place of concrete path parameters, and HTML-escapes every
dynamic field before rendering. `Authorization`, `Proxy-Authorization`, Cookie, `Set-Cookie`,
`X-API-Key`, CSRF tokens, OAuth codes, and similarly named values must never appear in the page.

Production must keep `APP_DEBUG=false`; production panic responses remain the normal JSON
`COMMON.INTERNAL` boundary and do not expose stack, request, SQL, or environment details.

## Database And Tracing

GORM logging always enables `ParameterizedQueries`, and the observed logger preserves GORM's
optional `ParamsFilter` interface while forcing bound parameters out before `Trace`. GORM's special
`Scan` recorder is also parameterized because it otherwise expands values before calling the
configured logger. SQL diagnostics therefore preserve statement shape while keeping values out of
stdout and the local exception timeline. OpenTelemetry uses GORM's parameterized statement shape as
`db.statement`. Do not disable parameterization to improve a debug screenshot; reproduce locally
with explicit non-secret fixtures instead.

HTTP server spans use the route template for the span name and `http.route`. FastBuild deliberately omits
the concrete `url.path`, raw query values, and free-form error messages from exported spans; traces
carry status and stable `error.type` summaries while structured logs own reviewed diagnostic detail.
Unmatched requests use only the HTTP method as the span name.

Tracing starts through one bootstrap helper, `bootstrap.InitTracing`, shared by the HTTP server
and every long-running worker (`workflow:work`, `workflow:schedule:work`, `notification:work`,
`webhook:work`). Each process uses the same resource, sampler, and W3C propagator, adds GORM spans
to its database handle, and flushes buffered spans through `bootstrap.ShutdownTracing` (bounded to
5 s) on exit. Exporter failures log the structured `tracing.init_failed` warning and the process
continues untraced. Workflow workers continue the dispatcher's trace from the queued `traceparent`
with one consumer span per job, named `workflow.job <type>` from the code-registered job type; job
data is never exported. Notification and webhook workers open one consumer span per dispatch batch
carrying only the processed count.

Outbound HTTP for the AI provider, webhook delivery, and R2 storage goes through
`pkg/tracedhttp`. Its client spans carry the method, a code-owned `peer.service` label (`openai`,
`webhook`, `r2`), the status code, and the stable `error.type`, and nothing else: no URL, host,
path, query, or header. Webhook URLs are customer-supplied and can embed credentials in the path,
and storage paths contain object keys, so `otelhttp` is intentionally not used because its client
spans always export `url.full`. The transport also never injects `traceparent` into requests to
these third parties. The span ends when response headers arrive, so it excludes body streaming.

## Database Metrics

When `METRICS_ENABLED=true`, `database.NewDB` installs a GORM callback plugin and a pool collector:

| Series | Labels | Source |
|---|---|---|
| `db_queries_total`, `db_query_duration_seconds` | `operation` (`create`, `query`, `update`, `delete`, `row`, `raw`), `table` | GORM callbacks |
| `db_connections_open`, `db_connections_in_use`, `db_connections_idle`, `db_connections_max_open` | none | `sql.DBStats` at scrape time |
| `db_connections_wait_total`, `db_connections_wait_duration_seconds_total` | none | `sql.DBStats` at scrape time |

Labels never contain SQL text or bound values. `table` is the statement's table name when it is a
plain lowercase identifier (optionally schema-qualified), `unknown` when GORM has none (most raw
SQL), and `other` for anything else or once 256 distinct table labels exist. The plugin costs two
allocations per statement; pool statistics are read only when Prometheus scrapes, with no
background goroutine. With metrics disabled, no callback is registered.

## Transactional Mail

Transactional mail (`POST /v1/transactional-messages`, the `mailbox` starter) logs one structured
`slog` event per state change. Every event carries only `organization_id`, `message_id`,
`template_id`, `mode`, and `status`, plus the listed extras; the request's `request_id` comes from
the request context. Recipients, subjects, variables, and the encrypted payload are never logged
(`transactional_logging_test.go` enforces this).

| Event | Level | When | Extra fields |
|---|---|---|---|
| `transactional_message.accepted` | info | The request stored a message (`status` `queued` or `suppressed`) | none |
| `transactional_message.dispatch_deferred` | warn | The in-process dispatch right after the request failed; the worker sends the message | `message_id` only |
| `transactional_message.dispatched` | info | A provider call returned (live, non-simulated messages only) | `attempts`, `duration_ms` |
| `transactional_message.sent` | info | The provider accepted the message, or a simulation finished | none |
| `transactional_message.failed` | info | The message failed for good, for example `send.sender_unverified` or a permanent provider code | `failure_code` |
| `transactional_message.suppressed` | info | The recipient joined the suppression list while the message waited, or the provider refused it as suppressed | none |
| `transactional_message.retry_scheduled` | info | A provider answered with a temporary error; another attempt is due | `attempts`, `failure_code` |
| `transactional_message.expired` | info | The worker failed a message queued for more than 1 hour (`send.expired`) | `failure_code` |
| `transactional_message.unknown_outcome` | info | A provider call returned no HTTP status, or its lease ran out mid-call; never resent | `attempts` or `failure_code` |
| `transactional_message.lease_lost` | warn | A dispatcher lost its lease before recording what it did | `organization_id`, `message_id` only |
| `transactional_message.event_recorded` | info | A delivery event (bounce, complaint, delivery) matched a message | `event` |
| `transactional_message.purged` | info | The worker deleted messages older than 30 days | `count` only |
| `transactional_payload.decrypt_failed` | info | A queued payload could not be decrypted, usually after a key rotation; the message fails with `send.payload_unavailable` | none |

A message suppressed when the request arrives logs only `accepted` with `status` `suppressed`; it
is counted once in `zmail_transactional_messages_total{outcome="suppressed"}` when it is stored
(an idempotent replay is not counted again).

When `METRICS_ENABLED=true`, the `mailbox` starter adds:

| Series | Type | Labels | Meaning |
|---|---|---|---|
| `zmail_transactional_messages_total` | counter | `mode` (`live`, `test`), `outcome` (`sent`, `failed`, `suppressed`) | Messages that reached a final state: through dispatch or the worker, or, for `suppressed`, also at acceptance |
| `zmail_transactional_dispatch_seconds` | histogram | none | Time from storing a message to the provider accepting it (live, non-simulated) |
| `zmail_transactional_queue_oldest_seconds` | gauge | none | Age of the oldest live message waiting to be sent, not counting `send_limit` waits; set by each worker pass |
| `zmail_send_lane_attempts_total` | counter | `lane` (`personal`, `broadcast`, `transactional`) | Provider send attempts per send lane |
| `zmail_transactional_rate_limited_total` | counter | `rule` (`mail.transactional.key`, `mail.transactional.recipient`, `mail.transactional.test_send`) | Requests refused by a transactional rate limit |

Every label value comes from a code-owned set. Suggested alerts (spec §15): the oldest-queue gauge
above 120 seconds; any `transactional_payload.decrypt_failed`; more than 5% of
`zmail_transactional_messages_total` with `outcome="failed"` over 5 minutes; and any
`transactional_message.unknown_outcome`, which needs a human because the recipient may or may not
have the message. HTTP spans cover the request until it commits; the worker
(`mail:broadcast:work`) traces its process like the other workers, and no per-message dispatch span
exists yet.

## Alerts

HTTP series label the route template in `path`, so each route can carry its own budget. With
`METRICS_ENABLED=true`, alert on the unified inbox (`docs/plans/unified-inbox.zh-CN.md` §13.2):

| Alert | Expression | For |
|---|---|---|
| Unified inbox list slow | `histogram_quantile(0.95, sum by (le) (rate(http_request_duration_seconds_bucket{method="GET",path="/v1/inbox-messages"}[5m]))) > 0.5` | 15m |
| Unified inbox counts slow | `histogram_quantile(0.95, sum by (le) (rate(http_request_duration_seconds_bucket{method="GET",path="/v1/inbox-message-counts"}[5m]))) > 0.5` | 15m |
| Unified inbox searches too broad | `sum(rate(http_requests_total{method="GET",path="/v1/inbox-messages",status="422"}[1h])) / sum(rate(http_requests_total{method="GET",path="/v1/inbox-messages"}[1h])) > 0.2` | 1h |

The route answers `422` only with `MAILBOX.SEARCH_TOO_BROAD`, and only a search can time out, so
the third ratio is measured against every list request: it fires later than a search-only ratio
would, never earlier. A sustained rise usually means the trigram index is missing or the data is
skewed toward one mailbox.

## Audit Records

Audit records intentionally persist actor, target, action, result, request correlation, and reviewed
business changes. The audit starter redacts sensitive `Changes` fields and recursively sanitizes
`Metadata` again at the service boundary before persistence. Feature code should record stable IDs
and state transitions, never bearer values, passwords, invitation tokens, provider payloads, or PII.

Request audit records are written by a bounded in-memory queue in batches of up to 200 rows every
50 ms (`AUDIT_WRITE_MODE=async`, the default), which raised write-request throughput by 50–70% in
the baseline measurements. A full queue falls back to a synchronous write, a failed batch is retried
row by row, and the HTTP kernel drains the queue after it stops accepting requests and before it
closes the database. Records still queued when a process is killed without a graceful shutdown are
lost; `AUDIT_WRITE_MODE=sync` restores a write before every response. Commands and other direct
`Record` callers always write synchronously.

## Logging APIs

- Business and event operations use the existing `log/slog` seams.
- HTTP runtime, request, exception-center, and configured output handlers use `pkg/logger`.
- Do not add another logging library or silently mix the two seams inside one operation.
- Use stable event names and structured fields; never interpolate a secret into the message.

## Verification

```bash
go test ./pkg/redact ./pkg/logger ./pkg/errors ./pkg/tracedhttp ./internal/infra/exception ./internal/infra/database ./internal/infra/tracing ./internal/infra/metrics ./internal/bootstrap ./internal/capabilities/workflow ./internal/modules/audit
go test ./pkg/redact -run '^$' -bench '^BenchmarkMap$' -benchmem -count=5
python3 ../.agents/skills/fastbuild-framework-review/scripts/check-sensitive-telemetry.py
```
