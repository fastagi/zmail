# Authentication Contracts

FastBuild currently ships two authentication boundaries. They share the global HTTP envelope and
`error_code` vocabulary, but their paths, credentials, user DTOs, and session ownership are not
interchangeable.

The Admin Console is a client of the browser auth contract only when a same-origin browser gateway or
explicit Go browser-session adapter implements that contract. Static JavaScript is not a third
credential authority.

## Browser Auth Contract

The Web feature service and development mock BFF use a browser-oriented, same-origin contract:

| Operation | Browser endpoint | Request | Successful `data` |
|---|---|---|---|
| Login | `POST /api/auth/login` | `{ email, password }` | `{ user: { id, email, name } }` |
| Request a registration code | `POST /api/auth/register/email-code` | `{ email }` | `{ success: true }`, whether or not the address has an account |
| Register | `POST /api/auth/register` | `{ name, email, password, code }` (`code`: 6 digits) | `{ user: { id, email, name } }` |
| Current session | `GET /api/auth/me` | none | `{ user: { id, email, name } }` |
| Rename the signed-in user | `PUT /api/auth/me` | `{ name }` (1 to 50 characters after trimming) | `{ user: { id, email, name } }`; calls `PUT /v1/users/profile` with `nickname` and changes nothing else |
| Logout | `POST /api/auth/logout` | none | `{ success: true }` |
| Request a password reset | `POST /api/auth/password-reset` | `{ email }` | `{ success: true }`, whether or not the address has an account |
| Reset the password | `POST /api/auth/password-reset/confirm` | `{ token, password }` (6 to 50 characters) | `{ success: true }`; an unknown or used token is `401 AUTH.PASSWORD_RESET_TOKEN_INVALID`, an expired one `401 AUTH.PASSWORD_RESET_TOKEN_EXPIRED` |
| Change the password | `PUT /api/auth/password` | `{ currentPassword, newPassword }` (`newPassword`: 6 to 50 characters, different from `currentPassword`) | `{ success: true }`; every session of the account ends, this browser's included |

Changing the password answers `401 AUTH.INVALID_CREDENTIALS` when `currentPassword` is wrong; the
session stays valid, so clients show a field error rather than treating it as a lost session.
`401 AUTH.UNAUTHORIZED` means the session itself is missing or ended. An unchanged password is
`422 COMMON.VALIDATION_FAILED` with a `newPassword` field error before any upstream call. Neither the Go API nor
the browser contract lets a user list their sessions or end one of them; the only other way to end
every session of an account is the operator route `POST /v1/operator/users/{user_id}/sessions/revoke`.

The browser user view deliberately has no `role` or permission field. The optional permission
starter is organization-scoped and resolves current grants through its own context endpoint;
assigning `admin`, `member`, or global permissions to the auth user view would invent authorization
meaning that the Go API cannot prove.

The development implementation owns an HttpOnly signed mock cookie. The production adapter owns a
separate HttpOnly API-token cookie. Unsafe operations must reject cross-origin browser requests
before reading a body or mutating state.

## Go API User Starter Contract

The Go `user` starter exposes server-side authentication-session endpoints under `/v1`:

| Operation | API endpoint | Request | Successful `data` |
|---|---|---|---|
| Login | `POST /v1/login` | `{ username, password }` | `{ access_token, token_type: "Bearer", expires_in, user }` |
| Logout | `POST /v1/logout` | Bearer session token | `{ success: true }` |
| Request a registration code | `POST /v1/register/email-codes` | `{ email }` | `202` generic message |
| Register | `POST /v1/register` | `{ username, password, email, nickname?, phone?, email_code? }` | API user |
| Request password reset | `POST /v1/password/reset` | `{ email }` | Generic message |
| Confirm password reset | `POST /v1/password/reset/confirm` | `{ token, new_password }` | Generic message |
| Profile | `GET /v1/users/profile` | Bearer token | API user |
| Update profile | `PUT /v1/users/profile` | `{ nickname?, avatar?, phone?, bio? }` | API user |
| Change password | `PUT /v1/users/password` | `{ old_password, new_password }` | `{ message }`; every session is revoked |
| Delete account | `DELETE /v1/users/account` | Bearer token | No content (`204`) |

`access_token` is an opaque, cryptographically random session credential. It is not a JWT and has
no client-readable claims. The API stores only its SHA-256 hash and resolves identity, account
status, revocation, absolute expiry, and idle expiry from current persistence on every protected
request. `expires_in` is the number of whole seconds remaining until the absolute session expiry;
the server remains authoritative if an idle timeout or security event ends the session earlier.

API keys remain the default machine-to-machine credential. Authentication sessions represent one
signed-in user session and must not be reused as API-key scope, organization role, or permission
evidence.

### Session Lifecycle

- A successful login creates one new session with at least 256 bits of CSPRNG entropy. Plaintext
  session credentials exist only in the login response and caller custody; persistence, logs,
  traces, audit metadata, and errors contain neither plaintext nor a reversible value.
- The default absolute lifetime is 30 days and the default idle timeout is 7 days. Activity is
  touched at a bounded interval so authentication adds one indexed read per request without adding
  one write per request. Both limits are enforced server-side.
- `POST /v1/logout` revokes the presented session before returning success. A revoked, expired, or
  unknown credential receives `401 AUTH.UNAUTHORIZED` on later requests.
- Password change, successful password reset, account disablement, and account deletion invalidate
  existing access. Password change and reset revoke every session for that user in the same
  database transaction as the password update.
- Disabled accounts receive `403 AUTH.ACCOUNT_DISABLED` even when their session has not otherwise
  expired. Session persistence failures fail closed as `503 COMMON.SERVICE_UNAVAILABLE` rather than
  accepting an unverifiable credential.
- Revoked and expired rows are retained for a bounded operational window and pruned by the shipped
  operator command. The session row deliberately stores no IP address, user agent, device label, or
  provider payload; downstream apps may add reviewed device policy at their own boundary.

Session policy is restart-scoped typed configuration:

```dotenv
AUTH_SESSION_TTL=720h
AUTH_SESSION_IDLE_TIMEOUT=168h
AUTH_SESSION_TOUCH_INTERVAL=5m
AUTH_SESSION_RETENTION=720h
```

`JWT_SECRET` and `JWT_EXPIRE_DAYS` no longer configure user authentication. This is an intentional
security-breaking migration: deploying the session migration invalidates previously issued
stateless JWTs, so existing users sign in again. FastBuild does not silently keep the old seven-day
unrevocable path beside the new authority.

The API user uses numeric identity and backend fields such as `username`, `nickname`, and
`status`. It does not directly emit the Web shell's `name` view.

A new `username` is 3-50 characters and must not contain `@`; registration rejects one that does
with `422 COMMON.VALIDATION_FAILED`. The login `username` field accepts a username or an email. A
value that contains `@` resolves to the account that owns that email before any account whose
legacy username equals it, so one account cannot capture another account's email sign-in.

### Two-Factor Sign-In

An account can add an authenticator app (RFC 6238 TOTP: SHA-1, six digits, 30-second step).

- `POST /v1/users/totp/enrollments` creates a pending secret and returns it with its `otpauth://`
  URI once, replacing an earlier pending one. Sign-in ignores a pending secret.
- `PUT /v1/users/totp` with `{ "code" }` confirms it and returns ten one-time recovery codes once.
  A wrong code is `422 AUTH.TOTP_INVALID`.
- `GET /v1/users/totp` reports `available`, `enabled`, `confirmed_at`, and
  `recovery_codes_remaining`.
- `DELETE /v1/users/totp` with a current code or unused recovery code turns it off and deletes the
  recovery codes.
- With a confirmed factor, `POST /v1/login` needs `totp_code` after a correct password: absent is
  `401 AUTH.TOTP_REQUIRED`, wrong or reused is `401 AUTH.TOTP_INVALID`. Both count against the
  account's sign-in throttle, which resets only after both factors pass. Codes from the previous,
  current, and next step are accepted, and a step is never accepted twice. A recovery code is
  accepted in the same field, case- and hyphen-insensitively, and is consumed atomically.
- Secrets are AES-GCM encrypted with `AUTH_TOTP_ENCRYPTION_KEY`; recovery codes are stored as
  SHA-256 hashes. Without the key, `available` is false and enrollment is `503`, while existing
  factors stay enforced.
- `POST /v1/operator/session` accepts the same optional `totp_code` with the same failures, so an
  operator account with two-factor sign-in signs in to the Admin Console with its code.

### Public Failure Semantics

- Unknown identifiers, wrong passwords, and disabled accounts all return HTTP `401` with
  `AUTH.INVALID_CREDENTIALS`. The service performs a bcrypt comparison against a fixed dummy hash
  when no account exists, so the missing-account path does not skip the dominant password work.
- Password-reset requests return the same success body for known and unknown email addresses.
  The account lookup, token storage, and mail delivery run after the response on a bounded
  background task, so neither the body nor the response time depends on whether the account
  exists. Failures are logged internally. A delivery in flight when the process stops is lost;
  the user requests another reset.
- Registration preserves `USER.USERNAME_ALREADY_EXISTS` and `USER.EMAIL_ALREADY_EXISTS` for
  starter UX. When email codes are required (the default) the code is checked first, and no code is
  ever mailed to an address that has an account, so `USER.EMAIL_ALREADY_EXISTS` is unreachable
  without a code and registration does not reveal which addresses have accounts. With codes turned
  off, both conflicts are visible to anyone; products with a stricter threat model keep codes on.

### Registration Email Codes

Registration proves that the person reads the email address. `POST /v1/register/email-codes`
mails a 6-digit code; `POST /v1/register` takes it as `email_code`.

- The code request always answers `202` with one generic message on an open instance. The account
  lookup, the code issue, and the mail run after the response on a bounded background task, like
  password reset, so neither the body nor the timing depends on the address.
- An address that already has an account is mailed **nothing**: no code and no "you already have an
  account" notice. Such a person signs in or resets their password instead. This is the
  enumeration-safe choice that also cannot be used to mail existing users repeatedly.
- A code is random, valid for 10 minutes, and bound to the address: the API stores only
  `SHA-256(lower(email) + NUL + code)`, never the code, and never logs it or puts it in a URL. A new
  code replaces every earlier one for the address.
- Per address, the database refuses a new code within 60 seconds of the previous one and after 5
  codes in a rolling hour. A refused request still answers the same `202` and mails nothing.
  Production also applies the `AUTH_RATE_LIMIT_REGISTER_EMAIL_CODE_*` per-IP and per-address quotas
  (defaults 10 and 5 per hour), which answer `429 COMMON.RATE_LIMITED`.
- `POST /v1/register` checks the newest code of the address in the same database transaction that
  checks the username and email and inserts the account, and consumes the code there. Any failure,
  including a conflict, rolls back and leaves the code usable. Failures, all `400`:

  | `error_code` | When |
  |---|---|
  | `AUTH.REGISTRATION_EMAIL_CODE_REQUIRED` | No `email_code` was sent. |
  | `AUTH.REGISTRATION_EMAIL_CODE_INVALID` | No code was issued, it was replaced or used, or it is wrong. A wrong code counts one attempt. |
  | `AUTH.REGISTRATION_EMAIL_CODE_EXPIRED` | The code is older than 10 minutes or 5 wrong attempts spent it. The user asks for a new code. |

- `AUTH_REGISTRATION_EMAIL_CODE_REQUIRED` (default `true`) is read at startup. With it `false`,
  `email_code` is ignored and registration works as before. Codes need a configured system mailer;
  an open instance that requires codes without one logs `user.registration_email_code_mailer_missing`
  at startup and nobody can register.
- The API user carries no email-verified flag: with codes required, every self-registered account
  proved its address.

### Closing Registration

Registration is open by default. `AUTH_REGISTRATION_ENABLED=false` closes it for every caller:
`POST /v1/register` then answers `403 AUTH.FORBIDDEN` without reading or writing an account, and the
browser adapter passes that status and code through. Login, password reset, and every existing
account keep working. The switch is read at startup. An instance that should not accept sign-ups
from the network keeps it `false` and creates accounts from a process started with it `true` that
only the operator can reach; the self-hosting bundle does this
([`../deploy/self-host/README.md`](../deploy/self-host/README.md)).

A production instance (`APP_ENV=production`) starts with no account at all: the migration that
creates the `admin` development account creates nothing in production.

### Abuse Protection

Production enables endpoint-specific authentication limits by default. Login and password-reset
flows use independent per-IP and normalized/hashed per-subject buckets; registration and reset
confirmation have their own route quotas.

The login subject budget counts failed sign-ins per account. It is keyed by the resolved account,
so the username and the email of one account share it, and by the normalized identifier when no
account matches, so unknown identifiers are throttled the same way. Each attempt takes one unit
before the password check and a correct password clears the account's budget, so a user signing in
normally never spends it. Once the budget is spent, even the right password returns `429` until
the window ends; this bounds online guessing at the cost of letting an attacker hold one known
account at the limit, which the per-IP quota and gateway controls must keep expensive. An auth limit always returns HTTP `429` with
`COMMON.RATE_LIMITED`, and does not reveal which bucket fired or expose quota counters.

IP identity is accepted from forwarding headers only when the direct upstream matches
`SERVER_TRUSTED_PROXIES`. The default trusts no proxies, so a client cannot rotate a spoofed
`X-Forwarded-For` value to evade quotas.

The built-in stores are bounded and process-local. Each auth rule retains at most
`AUTH_RATE_LIMIT_MAX_BUCKETS_PER_RULE` active identity buckets (default 10,000), then evicts the
least recently used bucket. Multi-replica production deployments must provide equivalent distributed
enforcement: `RATE_LIMIT_STORE=postgres` shares every auth quota across replicas through PostgreSQL,
or the gateway/WAF or an explicitly assembled shared limiter can enforce it. FastBuild does not ship
a built-in Redis rate-limit driver, and a shared implementation must not silently fall back to
independent per-process buckets. When the shared store fails, every policy keeps the canonical `429`
+ `COMMON.RATE_LIMITED` response shape for denials. These quotas are a starter baseline, not a substitute for MFA,
breached-credential checks, adaptive bot controls, or product-specific account recovery policy.

Reset delivery is deliberately not durable: a queue payload would store the plaintext token. At
most 32 deliveries run per process; requests beyond that are dropped and logged, well above what
the reset quotas admit.

## Production API Adapter

FastBuild ships a same-origin Web adapter with explicit route handlers for the Go `user` starter and
enabled optional starter features. Set browser requests to the same-origin `/api` route, enable the
adapter on the Web server, and configure its private Go API base URL:

```dotenv
NEXT_PUBLIC_API_URL=/api
API_ADAPTER_ENABLED=true
API_UPSTREAM_URL=http://api:8025/v1
API_UPSTREAM_TIMEOUT_MS=5000
API_UPSTREAM_MAX_RESPONSE_BYTES=1048576
API_CLIENT_IP_HEADER=x-real-ip
```

`API_UPSTREAM_URL` and `API_CLIENT_IP_HEADER` are server-only. The adapter calls only fixed paths
owned by checked-in Route Handlers; it is not an arbitrary proxy. Production startup rejects an
enabled adapter without a same-origin browser target, private upstream configuration, or an
explicit ingress-owned client-IP header. Upstream response bodies are rejected above
`API_UPSTREAM_MAX_RESPONSE_BYTES` before JSON parsing.

The former auth-only environment names remain accepted as deprecated aliases for one migration
window. Setting a canonical and deprecated name to different values fails startup. New deployments
and documentation must use the `API_*` names because the adapter now serves more than auth.

### Operation Mapping

| Browser operation | Adapter behavior |
|---|---|
| Login | Sends `{ username: email, password }` to `POST /v1/login`, validates the envelope/opaque credential/user, then sets the API session cookie. |
| Request a registration code | Sends `{ email }` to `POST /v1/register/email-codes` and answers `{ success: true }`. |
| Register | Sends a non-identifying generated username plus `{ email, nickname: name, password, email_code: code }` to `POST /v1/register`, then logs in by email and sets the cookie. |
| Current session | Reads the server-only opaque session cookie, sends `Authorization: Bearer ...` to `GET /v1/users/profile`, and maps the API user to `{ id, email, name }`. |
| Change the password | Sends `{ old_password: currentPassword, new_password: newPassword }` with the opaque credential to `PUT /v1/users/password`. On success it expires the cookie, because the API revoked every session. `AUTH.UNAUTHORIZED` also expires it; `AUTH.INVALID_CREDENTIALS` keeps it. `old_password` and `new_password` field errors map to `currentPassword` and `newPassword`. |
| Logout | Sends the opaque credential to `POST /v1/logout`, then expires the exact-scope cookie. An upstream `401` is idempotent success because the server session is already absent; an availability failure is reported while local credential custody is still removed. |

The generated username has the form `user_<random-id>` and exists only to satisfy the API starter's
current account model. Browser identity remains email-based. Registration accepts a 2-50 character
name, an email up to 100 characters, and an 8-50 character password so the adapter cannot accept a
payload that the Go DTO rejects.

Registration and automatic login are two upstream calls, not one transaction. If registration
succeeds and login then times out or becomes unavailable, the account remains created; the user can
recover through the normal login flow. Products requiring atomic account creation plus session
issuance should add that capability to the API contract instead of hiding compensation in Web.

### Session And Security Semantics

- Production stores the opaque bearer token in `__Host-fastbuild_auth`; non-production uses `fastbuild_auth`.
  The cookie is HttpOnly, `Secure` in production, `SameSite=Lax`, `Path=/`, has no `Domain`, and
  expires no later than the API-owned `expires_in` lifetime. Browser code never receives the token.
- The Web adapter validates only the bounded opaque credential shape and positive `expires_in`
  value before accepting login success. It does not decode identity or authorization claims from
  the credential; the Go API resolves current session and account state from persistence.
- Login, registration, registration codes, password change, and logout require exact same-origin browser mutations. The adapter forwards
  no incoming cookies, authorization headers, or arbitrary paths to Go. The allowed browser origin
  comes from validated `NEXT_PUBLIC_APP_URL`, not a proxy-normalized internal request URL.
- `API_CLIENT_IP_HEADER` names the ingress-owned header from which one validated IP is forwarded as
  `X-Forwarded-For`; missing, malformed, or comma-separated values are not forwarded. The Go API
  must trust only the Web adapter network through
  `SERVER_TRUSTED_PROXIES`; this preserves endpoint-specific source-IP and subject quotas without
  trusting a browser-supplied forwarding chain. Configure the ingress to overwrite a single-value
  header such as `X-Real-IP`; do not pass an appended forwarding chain into this setting.
- Canonical upstream status, `error_code`, field ownership, `request_id`, and rate-limit headers are
  preserved. Backend messages and field messages are replaced with adapter-owned generic text;
  `nickname` validation ownership maps to the browser `name` field and generated `username` errors
  are not exposed as user input.
- Every `/api/auth/*` success, validation failure, same-origin rejection, authentication failure,
  and backend-availability response sets `Cache-Control: private, no-store` and varies on `Cookie`.
  The shared response boundary merges existing `Vary`, request-ID, and rate-limit headers instead
  of replacing them. Authenticated browser responses must never rely on framework dynamism alone
  as a cache policy.
- Timeout, network, malformed-envelope, malformed-user, and malformed-token failures become
  `503 COMMON.TIMEOUT` or `503 COMMON.SERVICE_UNAVAILABLE`, never a false unauthenticated session.
- A missing, revoked, idle-expired, absolute-expired, or rejected token becomes
  `401 AUTH.UNAUTHORIZED`. A disabled API user becomes
  `403 AUTH.ACCOUNT_DISABLED`. Availability failures remain retryable and do not redirect to login.

## Admin Console Authentication Boundary

`admin/` has no server runtime, private environment, or Route Handler. A protected production Admin Console
therefore requires a same-origin `/api` origin that implements the Browser Auth Contract while
keeping the opaque Go API session credential outside browser JavaScript.

The gateway or Go browser adapter owns:

- an HttpOnly, Secure, appropriately scoped browser cookie;
- exact Origin and CSRF enforcement for unsafe methods;
- fixed operation-to-upstream mappings rather than a caller-controlled proxy;
- bounded timeouts, request/response bodies, and safe header forwarding;
- canonical status, `error_code`, field ownership, and `request_id` mapping;
- logout/revocation behavior that removes both upstream and browser credential custody.

The Go `/v1/login` `access_token` must not be stored in `localStorage`, `sessionStorage`, IndexedDB,
Zustand persistence, TanStack Query persistence, a URL, or an analytics/logging surface. A
client-side route guard is UX only and does not replace API authorization.

The optional `operator` starter is the shipped Go browser adapter for platform operators: it issues
an HttpOnly `SameSite=Strict` cookie session with exact-Origin and CSRF enforcement. See
[`OPERATORS.md`](OPERATORS.md). Other Admin Console audiences still need their own reviewed
gateway.
