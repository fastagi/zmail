# AI Triage Acceptance Flow

End-to-end proof of AI triage with the rule-based `fake` model (`docs/plans/ai-triage.zh-CN.md` §8;
`contracts/MAILBOXES.md`, AI Triage): nothing shows while the switches are off; with the
organization and mailbox switches on, new inbound mail is queued, the worker classifies it with a
reason, the unified inbox shows it with unread counts and a tab filter, a reader's override wins,
and a sender gets a reply draft that names its basis. An outsider reaches none of it, and turning
the organization switch off hides everything again.

Cast: owner A of organization O with the personal mailbox `a-<run>@`; user D, who belongs to no
organization.

## How To Run

```bash
export FASTBUILD_TEST_POSTGRES_DSN=postgres://...      # disposable database (also used by the worker step)
./tests/kest/run_local.sh tests/kest/ai_triage.flow.md
```

`run_local.sh` starts the API with `ai_triage.kestenv` (`AI_TRIAGE_PROVIDER=fake`). The helper steps
read the same file for `MAILBOX_INBOUND_SECRET` and the worker's settings, and need `sh`, `curl`,
`openssl`, `awk`, and `python3` on `PATH`.

## Helpers

The first step writes two scripts under `tmp/kest/ai-triage-<run_id>/`:

- `inbound.sh <recipient> <subject> <body>` signs a plain-text message as the mail edge does and
  posts it to `POST /v1/inbound-mail-messages`; anything but `201` fails the step.
- `work.sh` runs the triage worker once (`fastbuild mail:triage:work --once`) against the flow's
  database with `APP_ENV=test` and the kestenv's settings, as the deployed worker would.

## Known Limits Of This Flow

- The worker runs as a one-shot command between steps rather than as a polling process; its
  retry and backoff timing are covered by the API tests.
- The model is the rule-based `fake`; no real provider is configured or called.

```flow
@flow id=zmail-ai-triage
@name Zmail AI Triage Acceptance
@version 1.0
@tags mailbox, inbox, triage, acceptance
@env local
```

## Setup

```step
@id install-helpers
@name Install the inbound and worker helpers
@type exec

dir="tmp/kest/ai-triage-{{run_id}}"
mkdir -p "$dir"
cat > "$dir/inbound.sh" <<'SH'
#!/bin/sh
# usage: inbound.sh <recipient> <subject> <body>
set -eu
set -a
. tests/kest/ai_triage.kestenv
set +a
rcpt=$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')
base_url=${KEST_API_BASE_URL:-$(awk '/^  local:/{f=1} f && /base_url:/{print $2; exit}' .kest/config.yaml)}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
now=$(LC_ALL=C date -u '+%a, %d %b %Y %H:%M:%S +0000')
printf 'From: Kest Sender <sender@example.net>\r\nTo: %s\r\nSubject: %s\r\nMessage-ID: <at.%s.%s@kest.invalid>\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n' \
  "$rcpt" "$2" "$$" "$(date +%s%N)" "$now" "$3" > "$work/raw.eml"
t=$(date +%s)
sig=$({ printf '%s.%s.' "$t" "$rcpt"; cat "$work/raw.eml"; } | openssl dgst -sha256 -hmac "$MAILBOX_INBOUND_SECRET" | awk '{print $NF}')
code=$(curl -sS -o "$work/body.json" -w '%{http_code}' -X POST "$base_url/v1/inbound-mail-messages" \
  -H 'Content-Type: message/rfc822' \
  -H "X-Mailbox-Recipient: $rcpt" \
  -H "X-Mailbox-Signature: t=$t,v1=$sig" \
  --data-binary "@$work/raw.eml")
if [ "$code" != "201" ]; then
  echo "inbound delivery to $rcpt returned $code: $(cat "$work/body.json")" >&2
  exit 1
fi
cat "$work/body.json"
SH
cat > "$dir/work.sh" <<'SH'
#!/bin/sh
# usage: work.sh  (runs the triage worker once against the flow's database)
set -eu
: "${FASTBUILD_TEST_POSTGRES_DSN:?export FASTBUILD_TEST_POSTGRES_DSN (the disposable test database)}"
set -a
. tests/kest/ai_triage.kestenv
eval "$(python3 - "$FASTBUILD_TEST_POSTGRES_DSN" <<'PY'
import shlex, sys
from urllib.parse import parse_qs, unquote, urlparse
value = urlparse(sys.argv[1])
query = parse_qs(value.query)
for key, item in {
    "DB_HOST": value.hostname,
    "DB_PORT": value.port or 5432,
    "DB_NAME": value.path.strip("/"),
    "DB_USERNAME": unquote(value.username or ""),
    "DB_PASSWORD": unquote(value.password or ""),
    "DB_SSLMODE": query.get("sslmode", ["disable"])[0],
}.items():
    print(f"{key}={shlex.quote(str(item))}")
PY
)"
APP_ENV=test
DB_ENABLED=true
DB_DRIVER=postgres
AI_ENABLED=false
TRACING_ENABLED=false
MAILBOX_PROVIDER=log
set +a
tmp/kest/fastbuild mail:triage:work --once --batch=50
SH
chmod +x "$dir/inbound.sh" "$dir/work.sh"
echo "$dir"
```

```step
@id register-a
@name Register owner A
@retry 2

POST /v1/register
Content-Type: application/json

{
  "username": "at_a_{{run_id}}",
  "email": "at_a_{{run_id}}@example.com",
  "password": "password123",
  "nickname": "Owner A"
}

[Captures]
a_user_id = data.id

[Asserts]
status == 201
```

```step
@id login-a
@name Sign in as A
@retry 2

POST /v1/login
Content-Type: application/json

{
  "username": "at_a_{{run_id}}@example.com",
  "password": "password123"
}

[Captures]
a_token = data.access_token

[Asserts]
status == 200
```

```step
@id register-d
@name Register outsider D
@retry 2

POST /v1/register
Content-Type: application/json

{
  "username": "at_d_{{run_id}}",
  "email": "at_d_{{run_id}}@example.com",
  "password": "password123",
  "nickname": "Outsider D"
}

[Asserts]
status == 201
```

```step
@id login-d
@name Sign in as D
@retry 2

POST /v1/login
Content-Type: application/json

{
  "username": "at_d_{{run_id}}@example.com",
  "password": "password123"
}

[Captures]
d_token = data.access_token

[Asserts]
status == 200
```

```step
@id create-org
@name A creates organization O

POST /v1/organizations
Content-Type: application/json
Authorization: Bearer {{a_token}}

{ "name": "AI Triage {{run_id}}" }

[Captures]
org_id = data.id

[Asserts]
status == 201
```

```step
@id create-a-box
@name A creates the personal mailbox a@

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
a_box = data.id
a_address = data.address

[Asserts]
status == 201
body.data.ai_triage_enabled == false
```

## Off by default

```step
@id setting-off
@name The instance has a model; the organization switch starts off

GET /v1/mail-triage-setting
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.available == true
body.data.enabled == false
body.data.instance_daily_cap == 100
```

```step
@id deliver-before
@name Mail that arrives while triage is off
@type exec

sh tmp/kest/ai-triage-{{run_id}}/inbound.sh "{{a_address}}" "early-{{run_id}}" "Can you look at this before we turn triage on?"
```

```step
@id counts-off
@name Counts carry no triage while the organization switch is off

GET /v1/inbox-message-counts
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.views.all_unread == 1
body.data.triage exists
body.data.triage == ""
```

## Switches on

```step
@id setting-partial
@name Every field of the organization switch is required

PUT /v1/mail-triage-setting
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{ "enabled": true }

[Asserts]
status == 422
```

```step
@id setting-on
@name A turns triage and drafts on for O

PUT /v1/mail-triage-setting
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{ "enabled": true, "daily_message_cap": 50, "draft_enabled": true }

[Asserts]
status == 200
body.data.enabled == true
body.data.daily_message_cap == 50
body.data.draft_enabled == true
body.data.updated_at exists
```

```step
@id box-on
@name A turns triage on for a@

PUT /v1/mailboxes/{{a_box}}/triage-setting
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{ "enabled": true }

[Asserts]
status == 200
body.data.id == {{a_box}}
body.data.ai_triage_enabled == true
```

```step
@id deliver-question
@name A question arrives at a@
@type exec

sh tmp/kest/ai-triage-{{run_id}}/inbound.sh "{{a_address}}" "deck-{{run_id}}" "Your code is 482913. Can you send the deck?"
```

```step
@id deliver-notice
@name A newsletter arrives at a@
@type exec

sh tmp/kest/ai-triage-{{run_id}}/inbound.sh "{{a_address}}" "news-{{run_id}}" "This week in review."
```

```step
@id pending-hidden
@name Queued mail shows no result before the worker runs

GET /v1/inbox-messages?triage=reply,deadline,fyi,review
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.# == 0
```

```step
@id work
@name The triage worker runs once
@type exec

sh tmp/kest/ai-triage-{{run_id}}/work.sh
```

## Results, counts, and the tab filter

```step
@id inbox-triaged
@name New mail carries its category and reason; earlier mail was never queued

GET /v1/inbox-messages
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Captures]
deck_msg = data.#(subject%"deck-*").id
news_msg = data.#(subject%"news-*").id

[Asserts]
status == 200
body.data.# == 3
body.data.#(subject%"deck-*").triage.category == "reply"
body.data.#(subject%"deck-*").triage.source == "ai"
body.data.#(subject%"deck-*").triage.reason exists
body.data.#(subject%"news-*").triage.category == "fyi"
body.data.#(subject%"early-*").triage exists
body.data.#(subject%"early-*").triage == ""
```

```step
@id counts-on
@name Counts hold A's unread mail by category

GET /v1/inbox-message-counts
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.triage.reply == 1
body.data.triage.fyi == 1
body.data.triage.deadline == 0
body.data.triage.review == 0
```

```step
@id tab-reply
@name The reply tab lists only the question

GET /v1/inbox-messages?triage=reply,review
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.# == 1
body.data.0.id == {{deck_msg}}
```

```step
@id tab-grouped
@name A tab never combines with thread grouping

GET /v1/inbox-messages?triage=reply&group=thread
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 400
```

## Override and draft

```step
@id override
@name A moves the newsletter to reply

PUT /v1/mailboxes/{{a_box}}/messages/{{news_msg}}/triage
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{ "category": "reply" }

[Asserts]
status == 200
body.data.category == "reply"
body.data.source == "manual"
body.data.reason == ""
```

```step
@id override-review
@name review is the model's alone

PUT /v1/mailboxes/{{a_box}}/messages/{{news_msg}}/triage
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{ "category": "review" }

[Asserts]
status == 422
```

```step
@id counts-after-override
@name The override moves the count

GET /v1/inbox-message-counts
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.triage.reply == 2
body.data.triage.fyi == 0
```

```step
@id draft
@name A drafts a reply to the question

POST /v1/mailboxes/{{a_box}}/messages/{{deck_msg}}/reply-drafts
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.body exists
body.data.basis.0.kind == "message"
body.data.basis.0.id == {{deck_msg}}
```

## Outsiders and switching off

```step
@id outsider-override
@name D cannot reach O's triage

PUT /v1/mailboxes/{{a_box}}/messages/{{deck_msg}}/triage
Content-Type: application/json
Authorization: Bearer {{d_token}}
Organization-Id: {{org_id}}

{ "category": "fyi" }

[Asserts]
status == 404
```

```step
@id outsider-draft
@name D gets no draft

POST /v1/mailboxes/{{a_box}}/messages/{{deck_msg}}/reply-drafts
Authorization: Bearer {{d_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 404
```

```step
@id setting-off-again
@name A turns triage off for O

PUT /v1/mail-triage-setting
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{ "enabled": false, "daily_message_cap": 50, "draft_enabled": false }

[Asserts]
status == 200
body.data.enabled == false
```

```step
@id hidden-again
@name Results are hidden again and the draft is refused

GET /v1/inbox-messages
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.#(subject%"deck-*").triage exists
body.data.#(subject%"deck-*").triage == ""
```

```step
@id draft-off
@name A draft needs the organization switch

POST /v1/mailboxes/{{a_box}}/messages/{{deck_msg}}/reply-drafts
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 409
body.error_code == "MAILBOX.TRIAGE_UNAVAILABLE"
```
