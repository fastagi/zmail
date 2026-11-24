# API Key Contract

This contract owns the default `apikey` starter across the Go API, the Web API key feature, the
fixed production API adapter, and the development mock BFF. It defines user-owned key management
and route-level scope attenuation. It does not define product roles, generalized RBAC, usage
metering, quotas, or billing.

## Ownership And Paths

An authenticated user may manage only their own API keys.

| Operation | Browser route | Go API route | Success |
|---|---|---|---|
| List keys | `GET /api/api-keys` | `GET /v1/api-keys` | Paginated envelope |
| Create key | `POST /api/api-keys` | `POST /v1/api-keys` | `201` envelope |
| Revoke key | `DELETE /api/api-keys/:id` | `DELETE /v1/api-keys/:id` | `204` |

The Web production API adapter owns the HttpOnly API session credential and forwards only these
fixed relative paths. The browser never receives the Go authentication-session credential. Every browser response is
`Cache-Control: private, no-store` and varies on `Cookie`.

## Create

Request:

```json
{
  "name": "Deployment automation",
  "scopes": ["mail:send", "mail:read"],
  "expires_at": "2027-01-01T00:00:00Z",
  "mode": "live"
}
```

- `name` is trimmed and contains 1 to 100 Unicode code points.
- `scopes` contains 1 to 32 input values. Each value is normalized to lowercase and duplicates are
  removed. The result must be non-empty and every value must be one of Zmail's scopes
  (`mail:send`, `mail:read`, `mail:transactional`, `mail:agent`, `templates:create`, see
  [Authentication And Scopes](#authentication-and-scopes)) or the explicit `*` wildcard. An empty
  list, or any other value, is `400 COMMON.INVALID_INPUT` (a list over 32 values or a value over 65
  characters is `422 COMMON.VALIDATION_FAILED`), so a key that could reach nothing is never issued.
- `expires_at` is optional and must be in the future.
- `mode` is optional, `live` (the default) or `test`; anything else is `422 COMMON.VALIDATION_FAILED`.
  See [Modes](#modes).
- `organization_id` is optional. When present the caller must be a member of that organization
  (`404 ORGANIZATION.NOT_FOUND` otherwise) and the key is bound to it. See
  [Restrictions](#restrictions).
- `sending_domains` is optional and allowed only with `organization_id`. It holds 1 to 50 domain
  names; each is trimmed, lowercased, stripped of a trailing dot, and deduplicated, and each must
  be a domain the organization may send from: the instance's shared domain or one of the
  organization's verified domains. A list without `organization_id`, an empty list, or any other
  domain is `400 COMMON.INVALID_INPUT`.

The response returns metadata plus `plaintext_key`. The plaintext appears only in this successful
create response:

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "api_key": {
      "id": 42,
      "user_id": 7,
      "name": "Deployment automation",
      "key_prefix": "zmail_ab12cd34",
      "mode": "live",
      "scopes": ["mail:read", "mail:send"],
      "organization_id": 5,
      "sending_domains": ["acme.example"],
      "created_at": "2026-07-15T12:00:00Z",
      "updated_at": "2026-07-15T12:00:00Z"
    },
    "plaintext_key": "zmail_ab12cd34.<secret>"
  }
}
```

The API persists only the SHA-256 hash. List responses and later reads never contain
`plaintext_key` or `key_hash`. The Web shows the plaintext in one dialog, clears it when the dialog
closes, and immediately resets React Query mutation data instead of placing it in query cache.

## Modes

Every key has a `mode`, fixed when it is created and returned on every key:

| Mode | Prefix | Behavior |
|---|---|---|
| `live` | `zmail_<short id>` | Acts for real. Every key created before modes existed is `live`. |
| `test` | `zmail_test_<short id>` | Accepted only by routes that declare test support, and there never sends mail or counts in a send limit. |

Keys issued before the Zmail rename carry `fastbuild_` or `fastbuild_test_` and keep working with
their original prefix: a key is found by the hash of its whole plaintext, never by its prefix.

A route declares test support explicitly; every other route refuses a valid test key with
`403 PERMISSION.DENIED`, checked together with its scopes, so a route that forgets to declare test
support refuses test keys. Today only `POST /v1/transactional-messages` and
`GET /v1/transactional-messages/:message_id` declare it (see
[`mail/DELIVERY.md`](mail/DELIVERY.md#transactional-mail)). A test key and a live key of one owner see
disjoint data on those routes.

## Restrictions

A key may be bound, at creation and for good, to one organization and optionally to some of that
organization's sending domains, so a leaked key cannot act elsewhere. Existing keys and keys
created without these fields stay unrestricted: their responses omit both fields, and the database
stores `NULL` for both.

| Field on the key | Effect on API key routes |
|---|---|
| `organization_id` | The key acts in that organization. `Organization-Id` becomes optional: when absent the bound organization is used; a different value is `403 API_KEY.ORGANIZATION_MISMATCH`. The owner's current membership and feature grants still apply exactly as for an unrestricted key. A `member` with the `developer` grant may only name `sending_domains` inside the grant's scope (`403 DEVELOPER.DOMAIN_NOT_ALLOWED`); omitting them copies an explicit scope's domains, and an all-domains scope leaves the key open (see [mail/DELIVERY.md](mail/DELIVERY.md#developer-access)). |
| `sending_domains` | Every send checks the domain of the from address (for `POST /v1/outbound-mail-messages` the `from` field, for `POST /v1/transactional-messages` the template's `from_address`). Matching is exact and ignores case; a subdomain is a different domain. Any other domain is `403 API_KEY.DOMAIN_NOT_ALLOWED` and nothing is stored or sent. |

A restriction never widens a key: a domain later removed from the organization stays listed on the
key but no longer sends, because the mailbox rules refuse it. Mail clients signing in with mail
client passwords over SMTP are not API keys and are unaffected.

## List And Revoke

List metadata may contain `last_used_at`, `expires_at`, and `revoked_at`. New scope storage uses a
JSON string array; the API reads legacy comma-separated rows for compatibility but never writes that
format again.

Revocation is idempotent for the owner: repeated deletes return `204` without rewriting the
revocation timestamp. A missing key or a key owned by another user returns `404 API_KEY.NOT_FOUND`,
so ownership is not disclosed. Revocation updates only `revoked_at` and `updated_at`; a concurrent
last-used update cannot clear revocation.

## Authentication And Scopes

API clients send the plaintext key in `X-API-Key` to routes using the starter-owned `api_key`
middleware. Validation rejects missing, unknown, expired, and revoked keys. Successful validation
adds the key owner, scopes, mode, and restriction to request context.

Scopes attenuate what a key may do; they never elevate the key beyond its owning user. Scope checks
are exact, except that `*` matches every required scope. A route that needs scopes applies
`apikey.RequireScopes(...)` after API key authentication. Missing API key identity returns
`401 AUTH.UNAUTHORIZED`; insufficient scope, or a test key on a route without test support,
returns `403 PERMISSION.DENIED`.

Zmail's mail routes use these scopes (see [`mail/DELIVERY.md`](mail/DELIVERY.md#programmatic-sending)):
`mail:send`, `mail:read`, `mail:transactional`, and `mail:agent`. `mail:agent` lets a key use the
agent mail feed, agent message reads, and agent drafts, and nothing else; by itself it reaches no
mailbox: each mailbox needs a live agent grant issued to the key
([`mail/TRIAGE_AND_AGENTS.md`](mail/TRIAGE_AND_AGENTS.md#agent-grants)), and the key still never exceeds its owner's access.
`templates:create` lets a key create organization templates with `POST /v1/agent-mail-templates`
and nothing else: it neither sends nor reads, edits, or deletes templates
([`mail/DELIVERY.md`](mail/DELIVERY.md#create-a-template-from-an-ai-tool)).

`last_used_at` is operational metadata, not an authorization decision. Its write is best-effort,
atomic, and throttled to at most once per minute per active key.

## Errors

| HTTP status | `error_code` | Meaning |
|---|---|---|
| 400 | `COMMON.INVALID_INPUT` | Direct API input fails semantic scope, expiry, or restriction validation |
| 401 | `AUTH.UNAUTHORIZED` | Browser management session or `X-API-Key` header is missing |
| 401 | `API_KEY.INVALID` | Client key is unknown or malformed |
| 401 | `API_KEY.EXPIRED` | Client key has expired |
| 401 | `API_KEY.REVOKED` | Client key was revoked |
| 403 | `PERMISSION.DENIED` | Authenticated key lacks a required scope, or is a test key on a route without test support |
| 403 | `API_KEY.ORGANIZATION_MISMATCH` | An organization-bound key sent `Organization-Id` naming another organization |
| 403 | `API_KEY.DOMAIN_NOT_ALLOWED` | A domain-restricted key tried to send from an address on another domain |
| 404 | `ORGANIZATION.NOT_FOUND` | Create named an `organization_id` the caller is not a member of |
| 404 | `API_KEY.NOT_FOUND` | Owned management resource was not found |
| 413 | `COMMON.REQUEST_TOO_LARGE` | Browser create body exceeds its limit |
| 422 | `COMMON.VALIDATION_FAILED` | Request fields fail schema validation |
| 503 | `COMMON.SERVICE_UNAVAILABLE` | Adapter, mock backend, or persistence is unavailable |

## Deliberate Deferrals

- Rotation is create-new, migrate callers, then revoke-old; there is no rotate endpoint.
- Zmail's mail scopes are the whole catalog a new key may use. Stored keys keep whatever scopes they
  were issued with; a route checks scopes with the `namespace:action` grammar.
- Usage metering, quotas, billing, and key-level rate plans remain separate starter decisions. A
  key bound to an organization is still owned by its user, not by the organization.
- A restriction cannot be edited; create a new key with the restriction you need and revoke the old
  one.
- Organization roles (`owner`, `admin`, `member`) are not API key scopes or generalized RBAC roles.
