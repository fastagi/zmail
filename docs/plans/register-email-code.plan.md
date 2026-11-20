# Registration email code — reduced implementation plan

Request: self-registration must prove the person owns the email. The user asks for a 6-digit code
by email and registers with it. Executed inline on `feat/register-email-code`.

## Scope and owner

- Owner: the API `user` starter (`api/internal/modules/user`). It owns the new
  `registration_email_codes` table, `POST /v1/register/email-codes`, and the `email_code` rule on
  `POST /v1/register`.
- Web owns the browser BFF routes `POST /api/auth/register/email-code` and the `code` field on
  `POST /api/auth/register`, and the two-step registration form.
- Contracts: `contracts/AUTHENTICATION.md`, `contracts/openapi.yaml`, generated Web and Admin types.
- Out of scope: email verification for existing accounts, changing an account's email, an
  email-verified flag on `users` (the model has none; an account created through a code is verified
  by construction), and a username field in the Web form (the browser contract keeps generating it).

## Behavior

1. `POST /v1/register/email-codes {email}`:
   - Registration closed: `403 AUTH.FORBIDDEN`, like `POST /v1/register`.
   - Otherwise always `202` with one generic message. The lookup, code issue, and mail run after the
     response on the same bounded background runner as password reset, so neither the body nor the
     timing depends on the address.
   - An address that already has an account gets **no code and no mail** (ruling: the
     enumeration-safe option that also cannot be used to mail existing users).
   - A code is 6 random digits, stored only as `SHA-256(lower(email) + NUL + code)`, valid 10
     minutes. Issuing a code supersedes every earlier code for that address (the newest row wins).
   - Per-address rules enforced in the database, always on and shared by replicas: no new code
     within 60 s of the previous one, at most 5 codes per address per rolling hour. A refused issue
     is logged and silently skipped (the response stays the same).
   - Per-IP and per-address hourly quotas through the existing `AuthAbuseGuard`
     (`AUTH_RATE_LIMIT_REGISTER_EMAIL_CODE_*`, defaults 10 per IP and 5 per address per hour; on
     with the other auth limits in production). These answer `429 COMMON.RATE_LIMITED` keyed only
     on the request, never on account state.
2. `POST /v1/register` gains `email_code` (6 digits). When `AUTH_REGISTRATION_EMAIL_CODE_REQUIRED`
   is true:
   - missing code → `400 AUTH.REGISTRATION_EMAIL_CODE_REQUIRED`;
   - no code issued, superseded, used, or wrong → `400 AUTH.REGISTRATION_EMAIL_CODE_INVALID`;
     a wrong guess counts one attempt;
   - expired, or 5 wrong attempts spent → `400 AUTH.REGISTRATION_EMAIL_CODE_EXPIRED` (request a new
     code).
   - The code is checked first, inside one transaction that locks the newest code row; the username
     and email conflict checks and the user insert run in that same transaction, and the code is
     marked consumed in it. Any failure rolls back, so a conflict never burns the code, and a wrong
     guess commits only its attempt count.
   - Because the code check runs before the conflict checks, `USER.EMAIL_ALREADY_EXISTS` is only
     reachable by someone holding a valid code for that address, which is never issued for an
     existing account — registration no longer enumerates emails when codes are required.
   When the switch is false the field is ignored and registration behaves as before.
3. Codes never appear in logs, URLs, audit metadata, or errors.

## Rulings

- `AUTH_REGISTRATION_EMAIL_CODE_REQUIRED` defaults to `true`: open registration without proof of
  email is the unsafe default. Cost: an instance with open registration and no system mailer
  cannot register anyone; startup logs `user.registration_email_code_mailer_missing` in that case.
  Kest (`run_local.sh` default, overridden by `register_email_code.kestenv`) and
  `deploy/self-host/create-user.sh` set it to `false`. Documented in `UPGRADING.md` (High).
- Response for the code request is `202 Accepted` with `{ message }`.
- Hash includes the address so a code row can only be used for the address it was mailed to.
- Kest has no system-mail log provider and codes must never be logged, so the Kest flow requests a
  code, then overwrites the stored hash with the hash of a known code through `psql` (precedent:
  `mailbox_admin.flow.md`), and drives every assertion through the API.
- Mail: one English message (`SendRegistrationCodeEmail`) on the existing system mailer. The mailer
  has no locale input, and Go source must stay English-only, so no Chinese copy is added there.
- Web: the form is two steps on one page: email + "Send code" (60 s countdown), then code, name,
  password, and terms. The BFF requires a 6-digit `code` and forwards it as `email_code`; the
  development mock accepts any 6 digits.

## Acceptance criteria

- AC-1 Code request answers the same `202` for a new address, an existing account, and a request
  inside the cooldown.
- AC-2 A new address gets one code mail; an existing account gets none; the stored row has a hash,
  never the code.
- AC-3 Wrong code → `REGISTRATION_EMAIL_CODE_INVALID`, attempt counted; 5 wrong → `..._EXPIRED`;
  expired → `..._EXPIRED`; missing → `..._REQUIRED`.
- AC-4 Right code registers; reusing it → `..._INVALID`; a username conflict leaves the code usable.
- AC-5 Cooldown 60 s and 5 codes per address per hour enforced in PostgreSQL.
- AC-6 Switch off: registration works without a code.
- AC-7 Web: send code, countdown, register with code; BFF guards same origin; zh-Hans and en-US copy.

## Verification

- Go unit tests (service, handler, guard), PostgreSQL repository and migration up/down tests,
  golden schema.
- `make contract-generate`, `make contract-check`.
- Web Vitest for the form, BFF routes, and adapter; type check and lint.
- Kest `api/tests/kest/register_email_code.flow.md`.
- One full `make check`.
