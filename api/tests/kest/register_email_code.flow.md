# Registration Email Code Acceptance Flow

End-to-end proof of registration with an email code (`docs/plans/register-email-code.plan.md`;
`contracts/AUTHENTICATION.md`, Registration Email Codes): a code request is accepted; a wrong code
is refused with `AUTH.REGISTRATION_EMAIL_CODE_INVALID`; the right code registers the account; the
same code cannot register a second account; and a request for an address that already has an
account gets the same `202`.

## How To Run

```bash
export FASTBUILD_TEST_POSTGRES_DSN=postgres://...      # disposable database
./tests/kest/run_local.sh tests/kest/register_email_code.flow.md
```

`run_local.sh` reads the server settings from `register_email_code.kestenv`, which opens
registration and requires codes. The helper step needs `sh`, `openssl`, `awk`, and `psql` on `PATH`.

## Reading The Code Without Mail

Kest has no system mailer, and the API never logs a code or stores it in plain text. After the code
request, the `known-code` step waits for the code row the API wrote in the background and replaces
its hash with the hash of a known code, `SHA-256(email + NUL + code)`, in
`FASTBUILD_TEST_POSTGRES_DSN`. Every assertion still goes through the API.

```flow
@flow id=zmail-register-email-code
@name Zmail Registration Email Code Acceptance
@version 1.0
@tags auth, registration, acceptance
@env local
```

## Request A Code

```step
@id request-code
@name Request a registration code

POST /v1/register/email-codes
Content-Type: application/json

{
  "email": "rec_{{run_id}}@example.com"
}

[Asserts]
status == 202
body.data.message exists
```

```step
@id known-code
@name Give the stored code a known value
@type exec

set -eu
: "${FASTBUILD_TEST_POSTGRES_DSN:?export FASTBUILD_TEST_POSTGRES_DSN (the disposable test database)}"
email="rec_{{run_id}}@example.com"
hash=$(printf '%s\000%s' "$email" "424242" | openssl dgst -sha256 | awk '{print $NF}')
for _ in 1 2 3 4 5 6 7 8 9 10; do
  updated=$(psql "$FASTBUILD_TEST_POSTGRES_DSN" -v ON_ERROR_STOP=1 -qtAc \
    "UPDATE registration_email_codes SET code_hash = '$hash' WHERE email = '$email' RETURNING id" | head -n 1)
  if [ -n "$updated" ]; then
    echo "code row $updated accepts the known code"
    exit 0
  fi
  sleep 1
done
echo "no registration code was stored for $email" >&2
exit 1
```

## Register

```step
@id wrong-code
@name A wrong code is refused

POST /v1/register
Content-Type: application/json

{
  "username": "rec_{{run_id}}",
  "email": "rec_{{run_id}}@example.com",
  "password": "password123",
  "email_code": "000000"
}

[Asserts]
status == 400
body.error_code == "AUTH.REGISTRATION_EMAIL_CODE_INVALID"
```

```step
@id missing-code
@name Registration without a code is refused

POST /v1/register
Content-Type: application/json

{
  "username": "rec_{{run_id}}",
  "email": "rec_{{run_id}}@example.com",
  "password": "password123"
}

[Asserts]
status == 400
body.error_code == "AUTH.REGISTRATION_EMAIL_CODE_REQUIRED"
```

```step
@id right-code
@name The right code registers the account

POST /v1/register
Content-Type: application/json

{
  "username": "rec_{{run_id}}",
  "email": "rec_{{run_id}}@example.com",
  "password": "password123",
  "nickname": "Code User",
  "email_code": "424242"
}

[Captures]
user_id = data.id

[Asserts]
status == 201
body.data.id exists
body.data.email == "rec_{{run_id}}@example.com"
```

```step
@id reuse-code
@name The used code cannot register again

POST /v1/register
Content-Type: application/json

{
  "username": "rec_again_{{run_id}}",
  "email": "rec_{{run_id}}@example.com",
  "password": "password123",
  "email_code": "424242"
}

[Asserts]
status == 400
body.error_code == "AUTH.REGISTRATION_EMAIL_CODE_INVALID"
```

```step
@id login
@name The new account signs in

POST /v1/login
Content-Type: application/json

{
  "username": "rec_{{run_id}}@example.com",
  "password": "password123"
}

[Asserts]
status == 200
body.data.access_token exists
```

```step
@id existing-account
@name A taken address gets the same answer

POST /v1/register/email-codes
Content-Type: application/json

{
  "email": "rec_{{run_id}}@example.com"
}

[Asserts]
status == 202
body.data.message exists
```
