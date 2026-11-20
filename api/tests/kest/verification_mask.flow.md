# Verification Mask Acceptance Flow

End-to-end proof of masked one-time codes (`docs/plans/verification-mask.zh-CN.md` §6;
`contracts/MAILBOXES.md`, Masked Secrets): an inbound message with a code and a reset link is
masked in the mailbox listing, the unified inbox, the organization log, and the detail; the reveal
endpoint returns the original to the mailbox owner; an outsider gets 404.

Cast: owner A of organization O with the personal mailbox `a-<run>@`; user D, who belongs to no
organization.

## How To Run

```bash
export FASTBUILD_TEST_POSTGRES_DSN=postgres://...      # disposable database
export OPTIONAL_STARTERS=organization,mailbox
export MAILBOX_DOMAIN=mail.localhost
export MAILBOX_PROVIDER=log                            # nothing leaves the machine
export MAILBOX_INBOUND_SECRET=kest-inbound-secret-at-least-32-chars
./tests/kest/run_local.sh tests/kest/verification_mask.flow.md
```

The helper step needs `sh`, `curl`, `openssl`, and `awk` on `PATH`.

```flow
@flow id=zmail-verification-mask
@name Zmail Verification Mask Acceptance
@version 1.0
@tags mailbox, inbox, secrets, acceptance
@env local
```

## Setup

```step
@id install-helpers
@name Install the inbound helper
@type exec

dir="tmp/kest/verification-mask-{{run_id}}"
mkdir -p "$dir"
cat > "$dir/inbound.sh" <<'SH'
#!/bin/sh
# usage: inbound.sh <recipient>
set -eu
rcpt=$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')
: "${MAILBOX_INBOUND_SECRET:?export MAILBOX_INBOUND_SECRET for both the API and kest}"
base_url=${KEST_API_BASE_URL:-$(awk '/^  local:/{f=1} f && /base_url:/{print $2; exit}' .kest/config.yaml)}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
now=$(LC_ALL=C date -u '+%a, %d %b %Y %H:%M:%S +0000')
printf 'From: Medium <noreply@medium.example>\r\nTo: %s\r\nSubject: Your verification code is 482913\r\nMessage-ID: <vm.%s.%s@kest.invalid>\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nUse code 482913 to sign in. Reset: https://id.example.com/reset?token=abc\r\n' \
  "$rcpt" "$$" "$(date +%s)" "$now" > "$work/raw.eml"
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
chmod +x "$dir/inbound.sh"
echo "$dir"
```

```step
@id register-a
@name Register owner A
@retry 2

POST /v1/register
Content-Type: application/json

{
  "username": "vm_a_{{run_id}}",
  "email": "vm_a_{{run_id}}@example.com",
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
  "username": "vm_a_{{run_id}}@example.com",
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
  "username": "vm_d_{{run_id}}",
  "email": "vm_d_{{run_id}}@example.com",
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
  "username": "vm_d_{{run_id}}@example.com",
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

{ "name": "Verification Mask {{run_id}}" }

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
```

```step
@id deliver
@name A code message arrives at a@
@type exec

sh tmp/kest/verification-mask-{{run_id}}/inbound.sh "{{a_address}}"
```

## Masked everywhere

```step
@id mailbox-list
@name The mailbox listing masks the subject

GET /v1/mailboxes/{{a_box}}/messages
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Captures]
code_msg = data.0.id

[Asserts]
status == 200
body.data.0.subject == "Your verification code is ••••••"
```

```step
@id inbox-list
@name The unified inbox masks the subject and the snippet

GET /v1/inbox-messages
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.0.subject == "Your verification code is ••••••"
body.data.0.snippet == "Use code •••••• to sign in. Reset: id.example.com/••••"
```

```step
@id org-log
@name The organization log masks the subject

GET /v1/mail-messages
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.0.subject == "Your verification code is ••••••"
```

```step
@id detail
@name The detail masks the body and says so

GET /v1/mailboxes/{{a_box}}/messages/{{code_msg}}
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.secrets_masked == true
body.data.text_body matches "^Use code •••••• to sign in. Reset: id.example.com/••••"
```

## Reveal

```step
@id reveal
@name A reveals the original

POST /v1/mailboxes/{{a_box}}/messages/{{code_msg}}/reveal
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.secrets_masked == false
body.data.subject == "Your verification code is 482913"
body.data.text_body matches "^Use code 482913 to sign in. Reset: https://id.example.com/reset.token=abc"
```

```step
@id reveal-outsider
@name D, outside the organization, cannot reveal it

POST /v1/mailboxes/{{a_box}}/messages/{{code_msg}}/reveal
Authorization: Bearer {{d_token}}
Organization-Id: {{org_id}}

[Asserts]
status >= 400
status < 500
```

```step
@id detail-again
@name Revealing changes nothing stored: the detail is still masked

GET /v1/mailboxes/{{a_box}}/messages/{{code_msg}}
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.secrets_masked == true
```
