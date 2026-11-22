# Transactional Mail Acceptance Flow

End-to-end proof of the transactional mail API on a local server
(`docs/plans/transactional-mail.zh-CN.md` §16, key scenarios 1, 3, 4, 5, and 8; acceptance T-4,
T-5, T-7, and T-9; `contracts/MAILBOXES.md`, Transactional Mail). Scenario 7 (suppression) needs a
real permanent bounce, which no simulated or test-mode message produces, so it stays with
`TestPermanentBounceSuppressesAcrossLanes` in the API tests.

Cast: admin A owns organization O, a transactional template `otp-<run>` with two secret variables,
a live key, and a test key.

## How To Run

Nothing in this flow leaves the machine. Start the API with the `log` provider, transactional mail
on, and a transactional lane of two messages per 24 hours, against a disposable PostgreSQL
database:

```bash
export OPTIONAL_STARTERS=organization,mailbox
export MAILBOX_DOMAIN=mail.localhost
export MAILBOX_PROVIDER=log                       # nothing leaves the machine
export MAILBOX_TRANSACTIONAL_ENABLED=true
export MAILBOX_SECRET_ENCRYPTION_KEY="$(openssl rand -hex 24)"   # throwaway, never commit
export MAILBOX_DAILY_TRANSACTIONAL_LIMIT=2
export FASTBUILD_TEST_POSTGRES_DSN='postgres://fastbuild:fastbuild_test@127.0.0.1:55450/<fresh database>?sslmode=disable'
bash tests/kest/run_local.sh tests/kest/transactional_mail.flow.md
```

`run_local.sh` migrates the database, starts the API on a free port, and runs
`kest run tests/kest/transactional_mail.flow.md -e local --fail-fast --var "run_id=<now>"`.
Recipients are simulator addresses on `test.zmail.im` (never delivered) or, for the live lane
scenario, `example.com` addresses handed to the `log` provider. The template sends from the
instance domain, which the `log` provider accepts without a verified domain. A fresh organization
guarantees an empty transactional lane. Messages that wait for the lane are not sent here: the
mail worker is not running, and sending them later is covered by
`internal/modules/mailbox/transactional_dispatch_test.go`.

Two Kest workarounds: the template body spells its `{{code}}` and `{{link}}` placeholders with
JSON `\u007b`/`\u007d` escapes, because Kest would otherwise resolve them as flow variables; and
short `sleep` steps give the in-process dispatch time to finish, because `@retry` repeats only
failed requests, not failed assertions.

```flow
@flow id=zmail-transactional-mail
@name Zmail Transactional Mail Acceptance
@version 1.0
@tags mailbox, transactional, acceptance
@env local
```

## Setup

```step
@id register-a
@name Register admin A
@retry 2

POST /v1/register
Content-Type: application/json

{
  "username": "txn_a_{{run_id}}",
  "email": "txn_a_{{run_id}}@example.com",
  "password": "password123",
  "nickname": "Admin A"
}

[Asserts]
status == 201
body.data.id exists
```

```step
@id login-a
@name Sign in as A
@retry 2

POST /v1/login
Content-Type: application/json

{
  "username": "txn_a_{{run_id}}@example.com",
  "password": "password123"
}

[Captures]
a_token = data.access_token

[Asserts]
status == 200
body.data.access_token exists
```

```step
@id create-org
@name A creates organization O

POST /v1/organizations
Content-Type: application/json
Authorization: Bearer {{a_token}}

{
  "name": "Transactional {{run_id}}"
}

[Captures]
org_id = data.id

[Asserts]
status == 201
```

```step
@id create-template
@name A creates a transactional template with two secret variables

POST /v1/mail-templates
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "name": "Verification code {{run_id}}",
  "kind": "transactional",
  "key": "otp-{{run_id}}",
  "from_address": "no-reply@mail.localhost",
  "from_name": "Acme",
  "subject": "Acme verification code: \u007b\u007bcode\u007d\u007d",
  "text": "Your code is \u007b\u007bcode\u007d\u007d. Reset your password at \u007b\u007blink\u007d\u007d.",
  "variable_definitions": [
    {"name": "code", "secret": true, "required": true},
    {"name": "link", "secret": true, "required": true}
  ]
}

[Asserts]
status == 201
body.data.kind == "transactional"
body.data.key == "otp-{{run_id}}"
```

```step
@id create-live-key
@name A creates a live key with mail:transactional

POST /v1/api-keys
Content-Type: application/json
Authorization: Bearer {{a_token}}

{
  "name": "Live transactional {{run_id}}",
  "scopes": ["mail:transactional"]
}

[Captures]
live_key = data.plaintext_key

[Asserts]
status == 201
body.data.api_key.mode == "live"
```

```step
@id create-test-key
@name A creates a test key with mail:transactional and mail:send

POST /v1/api-keys
Content-Type: application/json
Authorization: Bearer {{a_token}}

{
  "name": "Test transactional {{run_id}}",
  "scopes": ["mail:transactional", "mail:send"],
  "mode": "test"
}

[Captures]
test_key = data.plaintext_key

[Asserts]
status == 201
body.data.api_key.mode == "test"
body.data.plaintext_key matches "^zmail_test_"
```

## Scenario 8: Test mode and simulator addresses

A test key never calls the provider and never counts in the lane; simulator addresses produce the
event their local part names.

```step
@id s8-delivered
@name Scenario 8 a test key sends to delivered@test.zmail.im

POST /v1/transactional-messages
Content-Type: application/json
X-API-Key: {{test_key}}
Organization-Id: {{org_id}}
Idempotency-Key: s8-delivered-{{run_id}}

{
  "template": "otp-{{run_id}}",
  "to": "delivered@test.zmail.im",
  "variables": {"code": "482913", "link": "https://u:p@reset.acme.com/t?code=1"}
}

[Captures]
delivered_id = data.id

[Asserts]
status == 202
body.data.mode == "test"
body.data.simulated == true
body.data.status == "queued"
```

```step
@id s8-bounced
@name Scenario 8 a test key sends to bounced@test.zmail.im

POST /v1/transactional-messages
Content-Type: application/json
X-API-Key: {{test_key}}
Organization-Id: {{org_id}}
Idempotency-Key: s8-bounced-{{run_id}}

{
  "template": "otp-{{run_id}}",
  "to": "bounced@test.zmail.im",
  "variables": {"code": "482913", "link": "https://reset.acme.com/t"}
}

[Captures]
bounced_id = data.id

[Asserts]
status == 202
body.data.simulated == true
```

```step
@id s8-wait-dispatch
@name Scenario 8 give the in-process dispatch time to finish
@type exec

sleep 2
```

```step
@id s8-delivered-done
@name Scenario 8 the delivered simulation ends sent with a delivered event

GET /v1/transactional-messages/{{delivered_id}}
X-API-Key: {{test_key}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.status == "sent"
body.data.last_event == "delivered"
body.data.attempts == 0
```

```step
@id s8-bounced-done
@name Scenario 8 the bounced simulation records a permanent bounce

GET /v1/transactional-messages/{{bounced_id}}
X-API-Key: {{test_key}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.last_event == "bounced"
body.data.attempts == 0
```

```step
@id s8-no-suppression
@name Scenario 8 a simulated bounce does not write the suppression list

GET /v1/mail-suppressions
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.# == 0
```

```step
@id s8-other-mode
@name Scenario 8 a live key cannot read a test message

GET /v1/transactional-messages/{{delivered_id}}
X-API-Key: {{live_key}}
Organization-Id: {{org_id}}

[Asserts]
status == 404
body.error_code == "MAILBOX.TRANSACTIONAL_MESSAGE_NOT_FOUND"
```

```step
@id s8-outbound-refused
@name Scenario 8 a test key on outbound-mail-messages is refused

POST /v1/outbound-mail-messages
Content-Type: application/json
X-API-Key: {{test_key}}
Organization-Id: {{org_id}}

{
  "from": "a-{{run_id}}@mail.localhost",
  "to": ["delivered@test.zmail.im"],
  "subject": "Must not send",
  "text": "A test key never sends mail."
}

[Asserts]
status == 403
body.error_code == "PERMISSION.DENIED"
```

## Scenario 1: Secrets never show

```step
@id s1-api-masked
@name Scenario 1 the API key route returns masked secrets

GET /v1/transactional-messages/{{delivered_id}}
X-API-Key: {{test_key}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.to == "delivered@test.zmail.im"
body.data.to_masked == "de••@test.zmail.im"
body.data.subject == "Acme verification code: ••••••"
body.data.variables.code == "••••••"
body.data.variables.link == "reset.acme.com/••••"
```

```step
@id s1-log-list
@name Scenario 1 the console log lists the message with the masked recipient

GET /v1/transactional-mail-log?mode=test&template=otp-{{run_id}}
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.# == 2
body.data.0.id == {{bounced_id}}
body.data.0.to_masked == "bo••@test.zmail.im"
body.data.1.id == {{delivered_id}}
body.data.1.to_masked == "de••@test.zmail.im"
body.data.1.subject == "Acme verification code: ••••••"
```

```step
@id s1-log-detail
@name Scenario 1 the console log entry shows only masked values

GET /v1/transactional-mail-log/{{delivered_id}}
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.to_masked == "de••@test.zmail.im"
body.data.variables.code == "••••••"
body.data.variables.link == "reset.acme.com/••••"
```

## Scenario 3: Idempotent replay

```step
@id s3-first
@name Scenario 3 the first send with a key is stored

POST /v1/transactional-messages
Content-Type: application/json
X-API-Key: {{test_key}}
Organization-Id: {{org_id}}
Idempotency-Key: s3-{{run_id}}

{
  "template": "otp-{{run_id}}",
  "to": "delivered+idem@test.zmail.im",
  "variables": {"code": "111111", "link": "https://reset.acme.com/a"}
}

[Captures]
idem_id = data.id

[Asserts]
status == 202
```

```step
@id s3-replay
@name Scenario 3 the same key and content replays the first message

POST /v1/transactional-messages
Content-Type: application/json
X-API-Key: {{test_key}}
Organization-Id: {{org_id}}
Idempotency-Key: s3-{{run_id}}

{
  "template": "otp-{{run_id}}",
  "to": " Delivered+IDEM@Test.zmail.im ",
  "variables": {"code": "111111", "link": "https://reset.acme.com/a"}
}

[Asserts]
status == 200
body.data.id == {{idem_id}}
```

```step
@id s3-conflict
@name Scenario 3 the same key with other content conflicts

POST /v1/transactional-messages
Content-Type: application/json
X-API-Key: {{test_key}}
Organization-Id: {{org_id}}
Idempotency-Key: s3-{{run_id}}

{
  "template": "otp-{{run_id}}",
  "to": "delivered+idem@test.zmail.im",
  "variables": {"code": "222222", "link": "https://reset.acme.com/a"}
}

[Asserts]
status == 409
body.error_code == "MAILBOX.IDEMPOTENCY_CONFLICT"
```

## Scenario 4: Per-recipient rate limit

Three sends of one template to one recipient fit in a minute; the fourth is refused with
`Retry-After`. The replays above did not use this recipient's room.

```step
@id s4-send-1
@name Scenario 4 first send to the recipient

POST /v1/transactional-messages
Content-Type: application/json
X-API-Key: {{test_key}}
Organization-Id: {{org_id}}
Idempotency-Key: s4-1-{{run_id}}

{
  "template": "otp-{{run_id}}",
  "to": "delivered+rate@test.zmail.im",
  "variables": {"code": "000001", "link": "https://reset.acme.com/r"}
}

[Asserts]
status == 202
```

```step
@id s4-send-2
@name Scenario 4 second send to the recipient

POST /v1/transactional-messages
Content-Type: application/json
X-API-Key: {{test_key}}
Organization-Id: {{org_id}}
Idempotency-Key: s4-2-{{run_id}}

{
  "template": "otp-{{run_id}}",
  "to": "delivered+rate@test.zmail.im",
  "variables": {"code": "000002", "link": "https://reset.acme.com/r"}
}

[Asserts]
status == 202
```

```step
@id s4-send-3
@name Scenario 4 third send to the recipient

POST /v1/transactional-messages
Content-Type: application/json
X-API-Key: {{test_key}}
Organization-Id: {{org_id}}
Idempotency-Key: s4-3-{{run_id}}

{
  "template": "otp-{{run_id}}",
  "to": "Delivered+Rate@TEST.zmail.im",
  "variables": {"code": "000003", "link": "https://reset.acme.com/r"}
}

[Asserts]
status == 202
```

```step
@id s4-send-4
@name Scenario 4 the fourth send in a minute is refused

POST /v1/transactional-messages
Content-Type: application/json
X-API-Key: {{test_key}}
Organization-Id: {{org_id}}
Idempotency-Key: s4-4-{{run_id}}

{
  "template": "otp-{{run_id}}",
  "to": "delivered+rate@test.zmail.im",
  "variables": {"code": "000004", "link": "https://reset.acme.com/r"}
}

[Asserts]
status == 429
body.error_code == "MAILBOX.RECIPIENT_RATE_LIMITED"
```

Kest asserts on status and body only, so this step repeats the refused send with `curl` and checks
`Retry-After` (whole seconds, at least 1). It reads the API address from the `local` environment
in `.kest/config.yaml`, which `run_local.sh` writes.

```step
@id s4-retry-after
@name Scenario 4 the refusal carries Retry-After of at least one second
@type exec

base="$(awk '/^  local:/ { getline; print $2 }' .kest/config.yaml)"
headers="$(curl -sS -o /dev/null -D - -X POST "$base/v1/transactional-messages" -H 'Content-Type: application/json' -H 'X-API-Key: {{test_key}}' -H 'Organization-Id: {{org_id}}' -H 'Idempotency-Key: s4-5-{{run_id}}' --data '{"template":"otp-{{run_id}}","to":"delivered+rate@test.zmail.im","variables":{"code":"000005","link":"https://reset.acme.com/r"}}')"
printf '%s\n' "$headers" | head -n 1 | grep -q ' 429' || { echo "expected 429"; exit 1; }
seconds="$(printf '%s\n' "$headers" | tr -d '\r' | awk 'tolower($1) == "retry-after:" { print $2 }')"
[ -n "$seconds" ] && [ "$seconds" -ge 1 ] || { echo "Retry-After missing or below 1: '$seconds'"; exit 1; }
echo "Retry-After: $seconds"
```

## Scenario 5: The transactional lane

The lane holds two live messages per 24 hours. The `log` provider accepts the first two; the
next two wait with `queued_reason` `send_limit`; the fifth is refused and stores nothing.

```step
@id s5-send-1
@name Scenario 5 the first live send goes out

POST /v1/transactional-messages
Content-Type: application/json
X-API-Key: {{live_key}}
Organization-Id: {{org_id}}
Idempotency-Key: s5-1-{{run_id}}

{
  "template": "otp-{{run_id}}",
  "to": "lane-1-{{run_id}}@example.com",
  "variables": {"code": "500001", "link": "https://reset.acme.com/l"}
}

[Captures]
lane_1 = data.id

[Asserts]
status == 202
body.data.mode == "live"
body.data.simulated == false
body.data.status == "queued"
```

```step
@id s5-wait-dispatch-1
@name Scenario 5 give the in-process dispatch time to finish
@type exec

sleep 2
```

```step
@id s5-send-1-sent
@name Scenario 5 the first live message is sent

GET /v1/transactional-messages/{{lane_1}}
X-API-Key: {{live_key}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.status == "sent"
body.data.attempts == 1
```

```step
@id s5-send-2
@name Scenario 5 the second live send goes out

POST /v1/transactional-messages
Content-Type: application/json
X-API-Key: {{live_key}}
Organization-Id: {{org_id}}
Idempotency-Key: s5-2-{{run_id}}

{
  "template": "otp-{{run_id}}",
  "to": "lane-2-{{run_id}}@example.com",
  "variables": {"code": "500002", "link": "https://reset.acme.com/l"}
}

[Captures]
lane_2 = data.id

[Asserts]
status == 202
body.data.status == "queued"
```

```step
@id s5-wait-dispatch-2
@name Scenario 5 give the in-process dispatch time to finish
@type exec

sleep 2
```

```step
@id s5-send-2-sent
@name Scenario 5 the second live message is sent

GET /v1/transactional-messages/{{lane_2}}
X-API-Key: {{live_key}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.status == "sent"
```

```step
@id s5-wait-1
@name Scenario 5 the third live send waits for the lane

POST /v1/transactional-messages
Content-Type: application/json
X-API-Key: {{live_key}}
Organization-Id: {{org_id}}
Idempotency-Key: s5-3-{{run_id}}

{
  "template": "otp-{{run_id}}",
  "to": "lane-3-{{run_id}}@example.com",
  "variables": {"code": "500003", "link": "https://reset.acme.com/l"}
}

[Asserts]
status == 202
body.data.status == "queued"
body.data.queued_reason == "send_limit"
```

```step
@id s5-wait-2
@name Scenario 5 the fourth live send also waits

POST /v1/transactional-messages
Content-Type: application/json
X-API-Key: {{live_key}}
Organization-Id: {{org_id}}
Idempotency-Key: s5-4-{{run_id}}

{
  "template": "otp-{{run_id}}",
  "to": "lane-4-{{run_id}}@example.com",
  "variables": {"code": "500004", "link": "https://reset.acme.com/l"}
}

[Asserts]
status == 202
body.data.queued_reason == "send_limit"
```

```step
@id s5-refused
@name Scenario 5 a send beyond the waiting cap is refused

POST /v1/transactional-messages
Content-Type: application/json
X-API-Key: {{live_key}}
Organization-Id: {{org_id}}
Idempotency-Key: s5-5-{{run_id}}

{
  "template": "otp-{{run_id}}",
  "to": "lane-5-{{run_id}}@example.com",
  "variables": {"code": "500005", "link": "https://reset.acme.com/l"}
}

[Asserts]
status == 429
body.error_code == "MAILBOX.SEND_LIMIT_REACHED"
```

```step
@id s5-not-stored
@name Scenario 5 the refused send stored nothing, and test sends did not count

GET /v1/transactional-mail-log?mode=live&template=otp-{{run_id}}
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.# == 4
```

```step
@id s5-lane-usage
@name Scenario 5 the transactional lane shows two attempts

GET /v1/mail-send-lanes
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.lanes.#(lane%"transactional").used == 2
body.data.lanes.#(lane%"transactional").limit == 2
body.data.lanes.#(lane%"transactional").waiting == 2
```
