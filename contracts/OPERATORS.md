# Platform Operator Contracts

The optional `operator` starter lets platform operators run the deployment from the static Admin
Console. It depends on the default `user` and `audit` starters and is selected with
`OPTIONAL_STARTERS=operator`. When it is not selected, no `/v1/operator` route and no `operator:*`
command exists.

Implementation plans: [`admin-operator-console.md`](../docs/plans/admin-operator-console.md),
[`admin-operator-starters.md`](../docs/plans/admin-operator-starters.md),
[`admin-mail-operations.plan.md`](../docs/plans/admin-mail-operations.plan.md), and
[`admin-mail-content.plan.md`](../docs/plans/admin-mail-content.plan.md).

## Operators

A platform operator is a user with a current operator grant. Grants are created and removed only
from the CLI on a host with database access:

```bash
fastbuild operator:grant <email> [--scope=full|mail-metrics][,mail-content]
fastbuild operator:revoke <email>
fastbuild operator:list
```

A grant has one scope, which decides its capabilities:

| Scope | Capabilities | Allows |
|---|---|---|
| `full` (default) | `operator:manage`, `mail_metrics:read` | Every operator route except reading bodies |
| `mail_metrics` | `mail_metrics:read` | Sign-in, [Mail Metrics](#mail-metrics), and the read-only [Organizations](#organizations) routes |

The content add-on (`,mail-content`, e.g. `--scope=mail-metrics,mail-content`) adds
`mail_content:read` to either scope: reading organization broadcast bodies and the
[content access records](#broadcast-content). No scope includes it, `full` included, so reading a
body always needs an explicit grant by the owner.

A grant command sets the whole grant: granting `--scope=mail-metrics` to a grant that has content
removes content. Granting the same scope and add-on twice, or revoking twice, is a no-op; any other
grant changes the grant. Each change writes an audit record with the system as actor, the
account's user ID as target, and the resulting `scope` and `mail_content` in its metadata.
`operator:list` shows SCOPE and CONTENT columns. Deleting an account removes its grant. A grant is not an organization role and not a
permission key: organization owners and admins receive no capability from their role.

## Browser Session

The Admin Console has no server runtime, so the Go API issues the operator session directly to the
Admin origin. The console enables sign-in when built with `VITE_OPTIONAL_FEATURES=operator`. Deployments must route the Admin origin's `/api/*` prefix to the Go API on the same
origin so the cookie is first-party.

| Rule | Value |
|---|---|
| Cookie name | `OPERATOR_SESSION_COOKIE_NAME`; default `__Host-fastbuild_operator` in production, `fastbuild_operator` otherwise |
| Cookie attributes | HttpOnly, `Secure` in production or whenever every allowed origin is `https`, `SameSite=Strict`, `Path=/`, no `Domain`, `Max-Age` = session lifetime |
| Credential | The opaque authentication session from the `user` starter, issued with the `operator` audience; never returned in a response body |
| Session lifetime | The existing absolute and idle session limits |
| Unsafe methods | Require `Origin` exactly equal to one of `OPERATOR_ALLOWED_ORIGINS` and header `X-CSRF-Token` |
| Safe methods | `Origin` may be absent (same-origin reads); when present it must equal one of `OPERATOR_ALLOWED_ORIGINS`, so an origin trusted only by `CORS_ALLOW_ORIGINS` cannot read operator responses |
| CSRF token | Returned by sign-in and `GET /v1/operator/session`; bound to one session |
| Caching | Every `/v1/operator` response sends `Cache-Control: private, no-store` |

Each protected request re-checks the session, the account status, and the operator grant against
current persistence, so disabling an account or revoking a grant takes effect on the next request.
Unsafe operator requests are recorded by the audit middleware with the operator as actor.

### Endpoints

| Operation | Method and path | Request | Success |
|---|---|---|---|
| Sign in | `POST /v1/operator/session` | `Origin`; `{ identifier, password }` | `200 { operator, csrf_token, capabilities }`, sets the cookie |
| Current operator | `GET /v1/operator/session` | Cookie | `200 { operator, csrf_token, capabilities }` |
| Sign out | `DELETE /v1/operator/session` | Cookie, `Origin`, `X-CSRF-Token` | `204`, expires the cookie |

`operator` is `{ id, username, email, nickname }`; `capabilities` lists the grant's capabilities
from the table above. `identifier` is a username or email of at most
100 characters; `password` is at most 128 characters.

Sign-in checks the operator grant after verifying credentials and before creating a session, so a
non-operator never receives a session. It shares the public login per-IP and per-account quotas.
The session is marked with the `operator` audience, and the operator routes accept only such
sessions: a credential from the public `POST /v1/login` is rejected with `401 AUTH.UNAUTHORIZED`
even when its account holds an operator grant.
Unknown, wrong, and disabled accounts all return `AUTH.INVALID_CREDENTIALS`.

Sign-out without a cookie, or with an already revoked session, returns `204` and expires the cookie.

### Errors

| Condition | HTTP | `error_code` |
|---|---:|---|
| Malformed body | 400 | `COMMON.INVALID_INPUT` |
| Field violation | 422 | `COMMON.VALIDATION_FAILED` |
| Missing, unknown, expired, or revoked session, or one not issued by operator sign-in | 401 | `AUTH.UNAUTHORIZED` |
| Wrong credentials, unknown or disabled account at sign-in | 401 | `AUTH.INVALID_CREDENTIALS` |
| Session whose account was later disabled | 403 | `AUTH.ACCOUNT_DISABLED` |
| Caller has no operator grant | 403 | `OPERATOR.FORBIDDEN` |
| Caller's grant does not include the route's capability | 403 | `OPERATOR.CAPABILITY_REQUIRED` |
| `Origin` missing on an unsafe method, or present on any method and not allowed by the operator starter | 403 | `OPERATOR.ORIGIN_REJECTED` |
| `Origin` outside both `CORS_ALLOW_ORIGINS` and `OPERATOR_ALLOWED_ORIGINS` | 403 | none: the kernel CORS policy rejects it before routing |
| `X-CSRF-Token` missing or not bound to this session | 403 | `OPERATOR.CSRF_REJECTED` |
| Target account holds an operator grant | 409 | `OPERATOR.TARGET_PROTECTED` |
| Message is not an outbound organization broadcast message ([Broadcast Content](#broadcast-content)) | 403 | `OPERATOR.CONTENT_NOT_ELIGIBLE` |
| Sign-in quota exceeded | 429 | `COMMON.RATE_LIMITED` |
| Persistence unavailable | 503 | `COMMON.SERVICE_UNAVAILABLE` |

On `401` or `OPERATOR.FORBIDDEN` the Admin Console returns to sign-in. On
`OPERATOR.CAPABILITY_REQUIRED` it stays signed in and shows the view as not permitted. On
`OPERATOR.CSRF_REJECTED` it refetches `GET /v1/operator/session` once and retries.

## Users

All routes require an operator session with `operator:manage`; unsafe methods also require `Origin`
and `X-CSRF-Token`. Unless a section says otherwise, every operator route below needs
`operator:manage`.

| Operation | Method and path | Success |
|---|---|---|
| List users | `GET /v1/operator/users?q=&status=&page=&per_page=` | `200` paginated `managed user` list |
| Get user | `GET /v1/operator/users/:id` | `200 managed user` |
| Disable | `POST /v1/operator/users/:id/disable` | `200 managed user` |
| Enable | `POST /v1/operator/users/:id/enable` | `200 managed user` |
| End sessions | `POST /v1/operator/users/:id/sessions/revoke` | `204` |

A managed user is `{ id, username, email, nickname, status, is_operator, created_at, last_login }`,
where `status` is `active` or `disabled` and `last_login` may be `null`. Password hashes, phone, bio,
and avatar are never returned.

- `q` (at most 100 characters) matches username or email case-insensitively as a literal substring;
  `%` and `_` are not wildcards. `status` is `active`, `disabled`, or `all` (default).
  `per_page` is 1–100 (default 15); out-of-range paging values fall back to the default or the
  maximum. Results are ordered by descending ID. An invalid `status` returns
  `400 COMMON.INVALID_INPUT`.
- Disabling revokes all of the account's sessions in the same transaction, so every existing
  credential fails on its next API call with `401 AUTH.UNAUTHORIZED`, and new logins fail with
  `AUTH.INVALID_CREDENTIALS`. Enabling does not restore sessions. Repeating either command returns
  the current account unchanged.
- Ending sessions revokes every active session without changing status.
- Accounts that hold an operator grant, including the caller's own, cannot be disabled, enabled, or
  signed out here: `409 OPERATOR.TARGET_PROTECTED`. Change operators from the CLI.
- An unknown account returns `404 USER.NOT_FOUND`; an ID of `0` returns `400 COMMON.INVALID_INPUT`,
  and a non-numeric ID matches no route (`404 COMMON.NOT_FOUND`).

## Audit Logs

`GET /v1/operator/audit-logs` returns platform-wide audit history in the audit entry shape defined
by [`AUDIT.md`](AUDIT.md), newest first (`created_at` then `id`, descending), in keyset pages
([Cursor pages](README.md#cursor-pages)).

| Query | Rule |
|---|---|
| `from`, `to` | RFC 3339 instants; `from` inclusive, `to` exclusive. `to` defaults to now, `from` to 30 days before `to`. The range must be ordered and at most 92 days |
| `user_id` | Positive user ID of the recorded user |
| `action`, `resource`, `method`, `request_id`, `status_code` | Exact matches with the same bounds as `AUDIT.md` |
| `cursor`, `per_page` | `cursor` from the previous page's `meta.next_cursor`, absent or empty for the newest page; `per_page` 1–100, default 15 |
| `page` | Removed in v0.24.0; rejected with `400 COMMON.INVALID_INPUT` |

Invalid values and ranges return `400 COMMON.INVALID_INPUT`. The endpoint reads only; operators
cannot modify or delete audit records.

## App Settings

Registered only when the `setting` starter is also selected; otherwise these paths return `404`.
Behavior, validation, `If-Match` preconditions, `ETag`s, and errors are exactly the app-scope rules of
[`SETTINGS.md`](SETTINGS.md); the setting starter serves them and the operator starter only
authorizes.

| Operation | Method and path | Request | Success |
|---|---|---|---|
| List app settings | `GET /v1/operator/settings` | — | `200` effective app settings, including non-public keys |
| Set override | `PATCH /v1/operator/settings/:key` | `If-Match: "setting-v{n}"`, `{ "value": … }` | `200` setting + `ETag` |
| Reset override | `DELETE /v1/operator/settings/:key` | `If-Match: "setting-v{n}"` | `204` + `ETag` |

Each change produces one audit record with the operator as actor; the setting starter adds the
setting's business change to that record.

## Organizations

Registered only when the `organization` starter is also selected; otherwise these paths return
`404`. An operator is not an organization member: these routes apply no membership check and never
set organization context. They are read-only and need `mail_metrics:read` (which every `full` grant
has), so the mail metrics views can open an organization's member list.

| Operation | Method and path | Success |
|---|---|---|
| List organizations | `GET /v1/operator/organizations?q=&page=&per_page=` | `200` paginated `operator organization` list |
| Get organization | `GET /v1/operator/organizations/:id` | `200 operator organization` |
| List members | `GET /v1/operator/organizations/:id/members?page=&per_page=` | `200` paginated `operator member` list |

An operator organization is `{ id, name, slug, created_by, member_count, suspended_at,
suspension_reason, daily_send_limit, created_at, updated_at }`. `suspended_at` is `null` unless the
organization is suspended, `suspension_reason` is `""` unless it is, and `daily_send_limit` is
`null` unless an operator set one.
An operator member is `{ id, user_id, username, nickname, email, role, joined_at }`, where `id` is
the membership ID and `role` is `owner`, `admin`, or `member`. Memberships of deleted accounts are
excluded from both the list and `member_count`.

- `q` (at most 100 characters) matches name or slug case-insensitively as a literal substring; `%`
  and `_` are not wildcards. Organizations are ordered by descending ID, members by ascending user
  ID. `per_page` is 1–100 (default 15). Invalid values return `400 COMMON.INVALID_INPUT`.
- An unknown organization returns `404 ORGANIZATION.NOT_FOUND`. Operator scope is global, so this
  discloses only that the ID does not exist.

### Organization Controls

These routes need `operator:manage`; unsafe methods also require `Origin` and `X-CSRF-Token`. Each
answers `200 operator organization` after the change and records one audit entry (`action`
`suspend`, `restore`, or `set_send_limit`, `resource` `organizations`) with the before and after
values.

| Operation | Method and path | Request |
|---|---|---|
| Suspend | `POST /v1/operator/organizations/:id/suspend` | `{ "reason": "…" }` |
| Restore | `POST /v1/operator/organizations/:id/restore` | — |
| Set send limit | `PUT /v1/operator/organizations/:id/send-limit` | `{ "daily_send_limit": 5000 }` or `null` |

- A suspended organization answers every request that names it in `Organization-Id`, browser
  session or API key, with `403 ORGANIZATION.SUSPENDED`. Nothing more is sent for it: broadcasts
  pause at the send limit, waiting messages keep waiting, and transactional mail is not sent.
  Inbound mail is still received. Restoring lifts all of this; it changes nothing else.
- `reason` is required: 1 to 500 characters after trimming, without control characters, otherwise
  `422 COMMON.VALIDATION_FAILED`. Suspending a suspended organization replaces the reason and keeps
  the first `suspended_at`. Restoring an active organization changes nothing.
- `daily_send_limit` is required and is `null` or an integer from 1 to 1000000, otherwise
  `422 COMMON.VALIDATION_FAILED`; a body that is not a JSON object is `400 COMMON.INVALID_INPUT`.
  A limit replaces the instance limit of every send lane (personal, broadcast, transactional) and
  the new-organization cap; `null` returns the organization to them.
- An unknown organization returns `404 ORGANIZATION.NOT_FOUND`.

## Mail Metrics

Registered only when the `mailbox` and `organization` starters are also selected; every route needs
`mail_metrics:read`. They read only and return no message body, HTML, recipient, or attachment.

| Operation | Method and path | Success |
|---|---|---|
| Overview | `GET /v1/operator/mail-metrics?date=&days=` | `200 mail metrics` |
| Organizations for a day | `GET /v1/operator/mail-metrics/organizations?date=&q=&page=&per_page=` | `200` paginated `mail organization` list |
| Outbound log | `GET /v1/operator/outbound-messages?date=&organization_id=&mailbox_id=&status=&page=&per_page=` | `200` paginated `outbound message` list |

- `date` is a UTC calendar day `YYYY-MM-DD`, default today (UTC); a future day returns
  `400 COMMON.INVALID_INPUT`. A day runs from 00:00 UTC to the next 00:00 UTC.
- `days` is 1–31 (default 7): the trend ends on `date`, oldest first, one entry per day including
  empty days.
- `per_page` is 1–100 (default 20). `q` is at most 100 characters and matches the organization name
  case-insensitively as a literal substring. `status` is `sent`, `failed`, or `queued`.
  `organization_id` and `mailbox_id` are positive integers. Invalid values return
  `400 COMMON.INVALID_INPUT`.

Counting rules:

- A counted message is an outbound message sent through Zmail (not a synced Gmail or Outlook copy)
  stored in a mailbox that is not deleted and belongs to an organization. It counts once, on the UTC
  day its record was created, under its current status: `sent`, `failed`, or `queued`. Test sends and
  the messages a broadcast created count; inbound mail, broadcast recipients that have no message
  yet, and transactional messages do not.
- `delivered`, `bounced`, and `complained` count the day's messages with at least one receipt of
  that kind, so duplicate receipts never change a number. They are a separate view from the status
  counts.
- `organizations` counts every existing organization, including those with no mail or mailbox.
  `members` counts distinct users with a membership in any organization (a user in two organizations
  counts once); deleted accounts are excluded and `disabled_members` reports how many are disabled.
  An organization row's `members` counts its memberships the same way.
- Membership and mail counts are aggregated separately and then joined, so member counts never
  multiply mail counts.

A mail metrics overview is `{ date, organizations, members, disabled_members, mailboxes, day, trend }`
with `day = { sent, failed, queued, delivered, bounced, complained }` and
`trend = [{ date, sent, failed, queued }]`.

A mail organization is `{ id, name, members, mailboxes, sent, failed, queued }`. Every organization
matching `q` is listed, including those with zero mail, ordered by `sent` descending and then by
ascending organization ID.

An outbound message is `{ id, organization_id, organization_name, mailbox_id, mailbox_address,
from_address, subject, status, failure_code, last_event, created_at, send_attempted_at, broadcast_id }`,
ordered by `created_at` and then `id`, both descending. `last_event` summarizes the delivery receipts
as in [`MAILBOXES.md`](MAILBOXES.md) and is empty when none arrived; `send_attempted_at` may be
`null`. `broadcast_id` names the broadcast of the same organization that created the message, or is
`null`; only such a message has a readable body ([Broadcast Content](#broadcast-content)).

## Broadcast Content

Registered only when the `mailbox` and `organization` starters are also selected. By the owner's
decision, an operator may read a body **only for organization broadcast mail**: never ordinary
business outbound, private inbox mail, synced Gmail or Outlook copies, or transactional system mail
(login codes, account recovery), which lives in its own table and has no route here.

| Operation | Method and path | Capability | Success |
|---|---|---|---|
| Read one broadcast message's text | `POST /v1/operator/outbound-messages/{organization_id}/{message_id}/content` with `{ reason }` | `mail_content:read` | `200 broadcast content` |
| Content access records | `GET /v1/operator/content-access-audits?organization_id=&viewer_user_id=&from=&to=&page=&per_page=` | `mail_content:read` or `operator:manage` | `200` paginated `content access` list |

Reading:

- The read is an unsafe request: `Origin` and `X-CSRF-Token` are required as on every unsafe
  operator route, and the audit middleware records the request (never its body).
- `reason` is trimmed and must be 1–500 characters with no control character other than line breaks
  and tabs; otherwise `422 COMMON.VALIDATION_FAILED`.
- No message with `message_id` is stored in an existing mailbox of `organization_id` (including a
  real message of another organization): `404 MAILBOX.MESSAGE_NOT_FOUND`.
- The message is in that organization but is not outbound, not sent through Zmail, or not created
  by a broadcast of that same organization: `403 OPERATOR.CONTENT_NOT_ELIGIBLE`. Refused reads write
  no content access record.
- In one transaction the API checks the message, stores the content access record, then loads the
  body. If the record cannot be stored or the transaction cannot commit, the response is
  `503 COMMON.SERVICE_UNAVAILABLE` and carries no body.
- Removing the add-on or revoking the grant applies on the next request, like every grant change.
- Neither the body nor the reason is written to the application log.

A broadcast content is `{ access_id, organization_id, message_id, broadcast_id, subject,
from_address, created_at, text, text_source }`. `text` is the stored text part (`text_source =
"text"`); when that is blank, it is the HTML part converted to readable text (`text_source =
"html"`): script, style, head, and embedded content dropped, block elements and `<br>` become line
breaks, entities decoded, nothing fetched. HTML, images, attachments, and recipients are never
returned; clients show `text` as plain text and must not cache it.

A content access is `{ id, viewer_user_id, organization_id, organization_name, message_id, reason,
accessed_at }`, newest first (`accessed_at`, then `id`, descending). It never holds a body, subject,
code, or recipient address, and it outlives the message, organization, and account it names
(`organization_name` is empty once the organization is gone). `from` and `to` are RFC 3339 instants;
`from` is inclusive, `to` exclusive and must be after `from`. `organization_id` and `viewer_user_id`
are positive integers and `per_page` is 1–100 (default 20). Invalid values return
`400 COMMON.INVALID_INPUT`.

## Webhooks

Registered only when the `webhook` starter is also selected. Every route is nested under one
organization and reuses the organization-scoped queries and representations of
[`WEBHOOKS.md`](WEBHOOKS.md), so signing secrets, ciphertext, event payloads, signatures, and target
response bodies are never returned.

| Operation | Method and path | Success |
|---|---|---|
| List endpoints | `GET /v1/operator/organizations/:id/webhook-endpoints` | `200` paginated endpoint list |
| List deliveries | `GET /v1/operator/organizations/:id/webhook-deliveries?endpoint_id=&status=` | `200` paginated delivery list |
| List attempts | `GET /v1/operator/organizations/:id/webhook-deliveries/:delivery_id/attempts` | `200` paginated attempt list |
| Replay delivery | `POST /v1/operator/organizations/:id/webhook-deliveries/:delivery_id/replay` | `200` delivery |

- An unknown organization returns `404 ORGANIZATION.NOT_FOUND` before any webhook lookup. A delivery
  that belongs to another organization returns `404 WEBHOOK.DELIVERY_NOT_FOUND`.
- `status` is a delivery status and `endpoint_id` a positive integer; invalid filters and IDs return
  `400 COMMON.INVALID_INPUT`.
- Replay is the operator replay rule of `WEBHOOKS.md` with the operator as actor: the delivery must
  be terminal, below the replay limit, and its endpoint active, otherwise
  `409 WEBHOOK.REPLAY_NOT_ALLOWED`. It keeps the original message ID and returns the delivery as
  `pending`. Each accepted replay starts one new delivery cycle, so the command is not idempotent.
  It writes the webhook starter's replay audit record in addition to the request audit record.
- Operators cannot create, update, disable, delete, or test endpoints or rotate secrets.

## Notification Deliveries

Registered only when the `notification` starter is also selected.

`GET /v1/operator/notification-deliveries?status=&channel=&user_id=&page=&per_page=` returns the
platform's channel delivery ledger, newest first (descending delivery ID), paginated.

A delivery is `{ id, notification_id, user_id, kind, channel, status, attempts, last_failure_code,
available_at, delivered_at, created_at, updated_at }`. `status` is `pending`, `processing`,
`delivered`, or `failed`; `channel` is `in_app` or `email`; `user_id` is the recipient;
`delivered_at` may be `null`. Notification titles, bodies, action URLs, and destination addresses are
never returned. Invalid filters return `400 COMMON.INVALID_INPUT`. The endpoint reads only.

## System Status

`GET /v1/operator/system` returns `{ version, revision, go_version, starters, database }`.
`version` and `revision` come from Go build metadata (`revision` is the first 12 characters of the
VCS revision, empty when unavailable); `starters` lists the default starters followed by the selected
optional starters; `database` is `ok` or `unavailable`.

## Configuration

| Variable | Required | Rule |
|---|---|---|
| `OPERATOR_ALLOWED_ORIGINS` | When selected | Exact `scheme://host[:port]` origins, comma separated; `https` in production; no wildcards or paths |
| `OPERATOR_SESSION_COOKIE_NAME` | No | Cookie token; in production it must start with `__Host-`, which is allowed only there |
