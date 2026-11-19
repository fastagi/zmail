# Mail Send Queue Acceptance Flow

End-to-end proof that a direct send at the daily send limit is queued instead of refused
(`docs/plans/send-quota-queue.md`, acceptance Q-1 to Q-5; `contracts/MAILBOXES.md`, send
outcomes). Acceptance case C7 of `docs/testing/ACCEPTANCE_TEST_PLAN.md` covers the web side.

Cast: admin A owns organization O and the personal mailbox `a-<run>@`.

## How To Run

Start the API with `APP_ENV=test`, the organization and mailbox starters, the `log` provider,
and a send limit of one message per 24 hours, against a disposable PostgreSQL database:

```bash
export OPTIONAL_STARTERS=organization,mailbox
export MAILBOX_DOMAIN=mail.localhost
export MAILBOX_PROVIDER=log                 # nothing leaves the machine
export MAILBOX_DAILY_SEND_LIMIT=1
# ... migrate and start the API with these variables, then:
kest run tests/kest/mail_send_queue.flow.md -e local --fail-fast --var "run_id=$(date +%s)"
```

The database must hold no other outbound mail for O, which a fresh organization guarantees. The
flow does not run the mail worker: sending waiting messages once the window has room is covered
by the API tests in `internal/modules/mailbox/queued_send_test.go`.

```flow
@flow id=zmail-mail-send-queue
@name Zmail Mail Send Queue Acceptance
@version 1.0
@tags mailbox, send-limit, acceptance
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
  "username": "sq_a_{{run_id}}",
  "email": "sq_a_{{run_id}}@example.com",
  "password": "password123",
  "nickname": "Admin A"
}

[Captures]
a_user_id = data.id

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
  "username": "sq_a_{{run_id}}@example.com",
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
  "name": "Send Queue {{run_id}}"
}

[Captures]
org_id = data.id

[Asserts]
status == 201
```

```step
@id create-box
@name A creates a personal mailbox

POST /v1/mailboxes
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "local_part": "a-{{run_id}}",
  "display_name": "A",
  "type": "personal",
  "owner_user_id": {{a_user_id}}
}

[Captures]
box_id = data.id

[Asserts]
status == 201
```

## Q-1: The send within the limit goes out

```step
@id q1-send
@name Q-1 the first send is attempted at once

POST /v1/mailboxes/{{box_id}}/messages
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "to": ["bob@example.com"],
  "subject": "q1-first-{{run_id}}",
  "text": "Within the limit."
}

[Asserts]
status == 201
body.data.status == "sent"
```

## Q-2: The send at the limit is queued

```step
@id q2-send
@name Q-2 the second send is accepted and waits

POST /v1/mailboxes/{{box_id}}/messages
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "to": ["bob@example.com"],
  "subject": "q2-waiting-{{run_id}}",
  "text": "At the limit."
}

[Captures]
waiting_id = data.id

[Asserts]
status == 202
body.data.status == "queued"
body.data.queued_reason == "send_limit"
```

```step
@id q2-list
@name Q-2 the outbox shows the waiting message

GET /v1/mailboxes/{{box_id}}/messages?direction=outbound
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.meta.total == 2
body.data.#(subject%"q2-waiting-*").queued_reason == "send_limit"
body.data.#(subject%"q2-waiting-*").id == {{waiting_id}}
```

## Q-3: A deleted waiting message is gone

```step
@id q3-delete
@name Q-3 deleting the waiting message

DELETE /v1/mailboxes/{{box_id}}/messages/{{waiting_id}}
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 204
```

```step
@id q3-gone
@name Q-3 the waiting message no longer exists

GET /v1/mailboxes/{{box_id}}/messages/{{waiting_id}}
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 404
```

## Q-4: The waiting room is capped at the limit

```step
@id q4-queue
@name Q-4 the next send takes the free waiting place

POST /v1/mailboxes/{{box_id}}/messages
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "to": ["bob@example.com"],
  "subject": "q4-waiting-{{run_id}}",
  "text": "Takes the waiting place."
}

[Asserts]
status == 202
body.data.queued_reason == "send_limit"
```

```step
@id q4-refused
@name Q-4 a send beyond the waiting cap is refused and stores nothing

POST /v1/mailboxes/{{box_id}}/messages
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "to": ["bob@example.com"],
  "subject": "q4-refused-{{run_id}}",
  "text": "Beyond the cap."
}

[Asserts]
status == 429
body.error_code == "MAILBOX.SEND_LIMIT_REACHED"
```

```step
@id q4-count
@name Q-4 the refused send was not stored

GET /v1/mailboxes/{{box_id}}/messages?direction=outbound
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.meta.total == 2
```
