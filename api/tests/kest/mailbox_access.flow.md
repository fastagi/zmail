# Mailbox Access Acceptance Flow

End-to-end proof of the mailbox access rules (personal and shared mailboxes, member
permissions, per-person read state, departure, handover, sealing, rejoin, and the per-mailbox
webhook switch). It covers acceptance cases AC-1 to AC-17 of
`docs/MAILBOX_ACCESS_PRD.zh-CN.md` against the routes and error codes of
`docs/plans/mailbox-access.md` (sections 2, 3, 8, and 9).

Cast: organization O, admin A (owner of O), members B and C. B owns the personal mailbox
`b-<run>@`, C owns `c-<run>@`, and the shared mailbox `team-<run>@` has B with `send` and C
with `view`. Every mailbox receives one inbound message with an attachment during setup.

## How To Run

`run_local.sh` starts the API with `APP_ENV=test` and only the default starters. The API process
inherits the caller's environment, so export these before running this flow:

```bash
cd api
export FASTBUILD_TEST_POSTGRES_DSN=postgres://...      # disposable database (also used by helpers)
export OPTIONAL_STARTERS=organization,mailbox,webhook
export MAILBOX_DOMAIN=mail.localhost
export MAILBOX_PROVIDER=log                            # nothing leaves the machine
export MAILBOX_INBOUND_SECRET=kest-inbound-secret-change-me
export MAILBOX_PUBLIC_URL=http://localhost:3000        # enables broadcasts
export WEBHOOK_ENCRYPTION_KEY=kest-webhook-key-at-least-32-characters
./tests/kest/run_local.sh tests/kest/mailbox_access.flow.md
```

The helper steps need `sh`, `curl`, `openssl`, `awk`, and `psql` on `PATH`.

## Helpers For Steps That Plain HTTP Steps Cannot Express

The first step (`install-helpers`, an exec step) writes two scripts under `tmp/kest/`:

- `inbound.sh <recipient> <subject>` builds a multipart RFC 5322 message with one attachment,
  signs it as the mail edge does (`X-Mailbox-Signature`, HMAC-SHA256 over
  `<t>.<recipient>.<raw body>` with `MAILBOX_INBOUND_SECRET`, see `contracts/MAILBOXES.md`), posts
  it to `POST /v1/inbound-mail-messages`, fails unless the answer is `201`, and prints the
  response envelope. It reads the API base URL from `.kest/config.yaml` (written by
  `run_local.sh`) unless `KEST_API_BASE_URL` is set. Used by setup, AC-5, AC-9, AC-10, and
  AC-11.
- `invite-token.sh <invitation_id>` makes an invitation acceptable without reading mail. The API
  never returns invitation tokens and the test environment has no mail transport, so the helper
  generates a fresh token and stores its SHA-256 hash on the invitation row in
  `FASTBUILD_TEST_POSTGRES_DSN`. The acceptance itself still goes through
  `POST /v1/organization-invitations/accept`, so the membership-joined event fires as in
  production. Used to add B and C, and for B's rejoins (AC-16, AC-17).

No delivery-event (bounce) step is included: events match outbound messages by the provider's
message id, and the development `log` provider stores none, so sealed delivery events are left to
the API test `TestEventsOfSealedMessagesStayHidden`.

## Known Limits Of This Flow

- AC-9 counts webhook deliveries queued for one endpoint subscribed only to
  `mail.message.received`; delivery to the endpoint's URL is not part of the proof.
- AC-11 statistics compare C's `received` count for today before and after handover; a run that
  crosses midnight UTC between those two steps fails spuriously.
- AC-13 schedules the broadcast a day ahead and proves that it stays `scheduled` after A loses
  `send`, and that A can still cancel it. Running it to completion needs the broadcast worker and
  is covered by the API tests.
- The deny paths for broadcasts (AC-15) assert `404` only: the design fixes the status, not the
  broadcast route's `error_code`.

```flow
@flow id=zmail-mailbox-access
@name Zmail Mailbox Access Acceptance (AC-1 to AC-17)
@version 1.0
@tags mailbox, organization, access, acceptance
@env local
```

## Setup

```step
@id install-helpers
@name Install inbound delivery and invitation helpers
@type exec

dir="tmp/kest/mailbox-access-{{run_id}}"
mkdir -p "$dir"
cat > "$dir/inbound.sh" <<'SH'
#!/bin/sh
# usage: inbound.sh <recipient> <subject>
set -eu
rcpt=$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')
subject=$2
: "${MAILBOX_INBOUND_SECRET:?export MAILBOX_INBOUND_SECRET for both the API and kest}"
base_url=${KEST_API_BASE_URL:-$(awk '/^  local:/{f=1} f && /base_url:/{print $2; exit}' .kest/config.yaml)}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
boundary="kest-boundary-$$"
now=$(LC_ALL=C date -u '+%a, %d %b %Y %H:%M:%S +0000')
printf 'From: Kest Sender <sender@example.net>\r\nTo: %s\r\nSubject: %s\r\nMessage-ID: <%s.%s@kest.invalid>\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary="%s"\r\n\r\n--%s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nBody of %s\r\n--%s\r\nContent-Type: application/octet-stream\r\nContent-Disposition: attachment; filename="note.bin"\r\nContent-Transfer-Encoding: base64\r\n\r\nAAECAwQF\r\n--%s--\r\n' \
  "$rcpt" "$subject" "$subject" "$$" "$now" "$boundary" "$boundary" "$subject" "$boundary" "$boundary" > "$work/raw.eml"
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
cat > "$dir/invite-token.sh" <<'SH'
#!/bin/sh
# usage: invite-token.sh <invitation_id>
set -eu
id=$1
case "$id" in ''|*[!0-9]*) echo "invitation id must be numeric" >&2; exit 1 ;; esac
: "${FASTBUILD_TEST_POSTGRES_DSN:?export FASTBUILD_TEST_POSTGRES_DSN (the disposable test database)}"
token=$(openssl rand -hex 32)
hash=$(printf '%s' "$token" | openssl dgst -sha256 | awk '{print $NF}')
updated=$(psql "$FASTBUILD_TEST_POSTGRES_DSN" -v ON_ERROR_STOP=1 -qtAc \
  "UPDATE organization_invitations SET token_hash = '$hash' WHERE id = $id RETURNING id" | head -n 1)
if [ "$updated" != "$id" ]; then
  echo "invitation $id not found" >&2
  exit 1
fi
printf '{"token":"%s"}\n' "$token"
SH
chmod +x "$dir/inbound.sh" "$dir/invite-token.sh"
echo "$dir"

[Captures]
helpers = $line.0
```

```step
@id register-a
@name Register admin A
@retry 2

POST /v1/register
Content-Type: application/json

{
  "username": "ma_a_{{run_id}}",
  "email": "ma_a_{{run_id}}@example.com",
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
  "username": "ma_a_{{run_id}}@example.com",
  "password": "password123"
}

[Captures]
a_token = data.access_token

[Asserts]
status == 200
body.data.access_token exists
```

```step
@id register-b
@name Register member B
@retry 2

POST /v1/register
Content-Type: application/json

{
  "username": "ma_b_{{run_id}}",
  "email": "ma_b_{{run_id}}@example.com",
  "password": "password123",
  "nickname": "Member B"
}

[Captures]
b_user_id = data.id

[Asserts]
status == 201
body.data.id exists
```

```step
@id login-b
@name Sign in as B
@retry 2

POST /v1/login
Content-Type: application/json

{
  "username": "ma_b_{{run_id}}@example.com",
  "password": "password123"
}

[Captures]
b_token = data.access_token

[Asserts]
status == 200
body.data.access_token exists
```

```step
@id register-c
@name Register member C
@retry 2

POST /v1/register
Content-Type: application/json

{
  "username": "ma_c_{{run_id}}",
  "email": "ma_c_{{run_id}}@example.com",
  "password": "password123",
  "nickname": "Member C"
}

[Captures]
c_user_id = data.id

[Asserts]
status == 201
body.data.id exists
```

```step
@id login-c
@name Sign in as C
@retry 2

POST /v1/login
Content-Type: application/json

{
  "username": "ma_c_{{run_id}}@example.com",
  "password": "password123"
}

[Captures]
c_token = data.access_token

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
  "name": "Mailbox Access {{run_id}}"
}

[Captures]
org_id = data.id

[Asserts]
status == 201
body.data.role == "owner"
```

```step
@id invite-b
@name A invites B as member

POST /v1/organizations/{{org_id}}/invitations
Content-Type: application/json
Authorization: Bearer {{a_token}}

{
  "email": "ma_b_{{run_id}}@example.com",
  "role": "member"
}

[Captures]
b_invitation_id = data.invitation.id

[Asserts]
status == 201
body.data.invitation.id exists
```

```step
@id token-b
@name Make B's invitation acceptable
@type exec

sh {{helpers}}/invite-token.sh {{b_invitation_id}}

[Captures]
b_invite_token = token
```

```step
@id accept-b
@name B accepts the invitation

POST /v1/organization-invitations/accept
Content-Type: application/json
Authorization: Bearer {{b_token}}

{
  "token": "{{b_invite_token}}"
}

[Asserts]
status == 200
body.data.role == "member"
```

```step
@id context-b
@name Resolve B's membership in O

GET /v1/organization-context
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Captures]
b_member_id = data.membership_id

[Asserts]
status == 200
body.data.role == "member"
```

```step
@id invite-c
@name A invites C as member

POST /v1/organizations/{{org_id}}/invitations
Content-Type: application/json
Authorization: Bearer {{a_token}}

{
  "email": "ma_c_{{run_id}}@example.com",
  "role": "member"
}

[Captures]
c_invitation_id = data.invitation.id

[Asserts]
status == 201
body.data.invitation.id exists
```

```step
@id token-c
@name Make C's invitation acceptable
@type exec

sh {{helpers}}/invite-token.sh {{c_invitation_id}}

[Captures]
c_invite_token = token
```

```step
@id accept-c
@name C accepts the invitation

POST /v1/organization-invitations/accept
Content-Type: application/json
Authorization: Bearer {{c_token}}

{
  "token": "{{c_invite_token}}"
}

[Asserts]
status == 200
body.data.role == "member"
```

```step
@id domains
@name Inbound delivery and broadcasts are enabled

GET /v1/mailbox-domains
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.0.kind == "shared"
body.data.0.inbound_enabled == true
body.data.0.broadcasts_enabled == true
```

```step
@id create-b-box
@name A creates personal mailbox b@ owned by B

POST /v1/mailboxes
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "local_part": "b-{{run_id}}",
  "display_name": "B",
  "type": "personal",
  "owner_user_id": {{b_user_id}}
}

[Captures]
b_box = data.id
b_address = data.address

[Asserts]
status == 201
body.data.type == "personal"
body.data.owner_user_id == {{b_user_id}}
```

```step
@id create-c-box
@name A creates personal mailbox c@ owned by C

POST /v1/mailboxes
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "local_part": "c-{{run_id}}",
  "display_name": "C",
  "type": "personal",
  "owner_user_id": {{c_user_id}}
}

[Captures]
c_box = data.id
c_address = data.address

[Asserts]
status == 201
body.data.type == "personal"
```

```step
@id create-team-box
@name A creates shared mailbox team@ (B send, C view)

POST /v1/mailboxes
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "local_part": "team-{{run_id}}",
  "display_name": "Team",
  "type": "shared",
  "members": [
    { "user_id": {{b_user_id}}, "permission": "send" },
    { "user_id": {{c_user_id}}, "permission": "view" }
  ]
}

[Captures]
team_box = data.id
team_address = data.address

[Asserts]
status == 201
body.data.type == "shared"
body.data.webhooks_enabled == false
```

```step
@id deliver-b-setup
@name Inbound message with attachment to b@
@type exec

sh {{helpers}}/inbound.sh "{{b_address}}" "b-private-{{run_id}}"

[Captures]
b_msg = data.id
```

```step
@id deliver-c-setup
@name Inbound message with attachment to c@
@type exec

sh {{helpers}}/inbound.sh "{{c_address}}" "c-private-{{run_id}}"

[Captures]
c_msg = data.id
```

```step
@id deliver-team-setup
@name Inbound message with attachment to team@
@type exec

sh {{helpers}}/inbound.sh "{{team_address}}" "team-first-{{run_id}}"

[Captures]
team_msg = data.id
```

```step
@id b-reads-own-message
@name Control: B opens the b@ message and its attachment id

GET /v1/mailboxes/{{b_box}}/messages/{{b_msg}}
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Captures]
b_attachment = data.attachments.0.id

[Asserts]
status == 200
body.data.subject == "b-private-{{run_id}}"
body.data.attachments.0.id exists
```

## AC-1: A colleague cannot reach a personal mailbox

```step
@id ac1-c-list
@name AC-1 C's mailbox list has c@ and team@ only

GET /v1/mailboxes
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.# == 2
body.data.#(id=={{c_box}}).access.send == true
body.data.#(id=={{team_box}}).access.view == true
body.data.#(id=={{team_box}}).access.send == false
```

```step
@id ac1-c-messages
@name AC-1 C lists b@ messages

GET /v1/mailboxes/{{b_box}}/messages
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 404
body.error_code == "MAILBOX.NOT_FOUND"
```

```step
@id ac1-c-detail
@name AC-1 C opens a b@ message

GET /v1/mailboxes/{{b_box}}/messages/{{b_msg}}
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 404
body.error_code == "MAILBOX.NOT_FOUND"
```

```step
@id ac1-c-attachment
@name AC-1 C downloads a b@ attachment

GET /v1/mailboxes/{{b_box}}/messages/{{b_msg}}/attachments/{{b_attachment}}
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 404
body.error_code == "MAILBOX.NOT_FOUND"
```

```step
@id ac1-c-mark-read
@name AC-1 C marks a b@ message read

PUT /v1/mailboxes/{{b_box}}/messages/{{b_msg}}
Content-Type: application/json
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

{
  "read": true
}

[Asserts]
status == 404
body.error_code == "MAILBOX.NOT_FOUND"
```

```step
@id ac1-c-send
@name AC-1 C sends from b@

POST /v1/mailboxes/{{b_box}}/messages
Content-Type: application/json
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

{
  "to": ["{{c_address}}"],
  "subject": "ac1-forged-{{run_id}}",
  "text": "Should never be stored."
}

[Asserts]
status == 404
body.error_code == "MAILBOX.NOT_FOUND"
```

```step
@id ac1-nothing-sent
@name AC-1 control: nothing was sent from b@

GET /v1/mailboxes/{{b_box}}/messages?direction=outbound
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.meta.total == 0
```

## AC-2: The admin sees metadata, not mail

```step
@id ac2-a-list
@name AC-2 A lists every mailbox; b@ has no unread count

GET /v1/mailboxes
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.# == 3
body.data.#(id=={{b_box}}).type == "personal"
body.data.#(id=={{b_box}}).unread_count == 0
body.data.#(id=={{b_box}}).access.view == false
body.data.#(id=={{b_box}}).access.administer == true
```

```step
@id ac2-a-messages
@name AC-2 A lists b@ messages

GET /v1/mailboxes/{{b_box}}/messages
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 403
body.error_code == "PERMISSION.DENIED"
```

```step
@id ac2-a-detail
@name AC-2 A opens a b@ message

GET /v1/mailboxes/{{b_box}}/messages/{{b_msg}}
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 403
body.error_code == "PERMISSION.DENIED"
```

## AC-3: Log search and statistics skip unreadable mailboxes

```step
@id ac3-a-search
@name AC-3 A searches the log for the b@ subject

GET /v1/mail-messages?q=b-private-{{run_id}}
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.meta.total == 0
```

```step
@id ac3-b-search
@name AC-3 control: B finds the same subject

GET /v1/mail-messages?q=b-private-{{run_id}}
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.meta.total == 1
```

```step
@id ac3-a-stats
@name AC-3 A's statistics count no mail

GET /v1/mail-message-stats?days=1
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.0.received == 0
body.data.0.sent == 0
```

```step
@id ac3-c-stats
@name AC-3 C's statistics count c@ and team@ only

GET /v1/mail-message-stats?days=1
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.0.received == 2
```

## AC-4: View permission reads but cannot send

```step
@id ac4-c-list-team
@name AC-4 C lists team@ messages

GET /v1/mailboxes/{{team_box}}/messages?direction=inbound
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.meta.total == 1
```

```step
@id ac4-c-send-team
@name AC-4 C sends from team@

POST /v1/mailboxes/{{team_box}}/messages
Content-Type: application/json
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

{
  "to": ["{{c_address}}"],
  "subject": "ac4-view-only-{{run_id}}",
  "text": "Should never be stored."
}

[Asserts]
status == 403
body.error_code == "PERMISSION.DENIED"
```

```step
@id ac4-no-message-stored
@name AC-4 no outbound message exists in team@ (no quota used)

GET /v1/mailboxes/{{team_box}}/messages?direction=outbound
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.meta.total == 0
```

## AC-5: Read state is per person

```step
@id ac5-deliver
@name AC-5 a new message arrives in team@
@type exec

sh {{helpers}}/inbound.sh "{{team_address}}" "team-new-{{run_id}}"

[Captures]
team_new_msg = data.id
```

```step
@id ac5-b-before
@name AC-5 B sees two unread in team@

GET /v1/mailboxes
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.#(id=={{team_box}}).unread_count == 2
```

```step
@id ac5-b-opens
@name AC-5 B marks the new message read

PUT /v1/mailboxes/{{team_box}}/messages/{{team_new_msg}}
Content-Type: application/json
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

{
  "read": true
}

[Asserts]
status == 200
body.data.read == true
```

```step
@id ac5-b-after
@name AC-5 B's unread count drops by one

GET /v1/mailboxes
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.#(id=={{team_box}}).unread_count == 1
```

```step
@id ac5-c-after
@name AC-5 C's unread count is unchanged

GET /v1/mailboxes
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.#(id=={{team_box}}).unread_count == 2
```

```step
@id ac5-c-message-unread
@name AC-5 the message is still unread for C

GET /v1/mailboxes/{{team_box}}/messages?direction=inbound
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.#(id=={{team_new_msg}}).read == false
```

## AC-6: API keys follow their owner's access

```step
@id ac6-c-key
@name AC-6 C creates an API key with mail:send

POST /v1/api-keys
Content-Type: application/json
Authorization: Bearer {{c_token}}

{
  "name": "C mail key {{run_id}}",
  "scopes": ["mail:send", "mail:read"]
}

[Captures]
c_api_key = data.plaintext_key

[Asserts]
status == 201
body.data.plaintext_key exists
```

```step
@id ac6-key-send-team
@name AC-6 C's key sends from team@

POST /v1/outbound-mail-messages
Content-Type: application/json
X-API-Key: {{c_api_key}}
Organization-Id: {{org_id}}

{
  "from": "{{team_address}}",
  "to": ["{{c_address}}"],
  "subject": "ac6-team-{{run_id}}",
  "text": "Should never be stored."
}

[Asserts]
status == 403
body.error_code == "PERMISSION.DENIED"
```

```step
@id ac6-key-send-b
@name AC-6 C's key sends from b@

POST /v1/outbound-mail-messages
Content-Type: application/json
X-API-Key: {{c_api_key}}
Organization-Id: {{org_id}}

{
  "from": "{{b_address}}",
  "to": ["{{c_address}}"],
  "subject": "ac6-personal-{{run_id}}",
  "text": "Should never be stored."
}

[Asserts]
status == 404
body.error_code == "MAILBOX.NOT_FOUND"
```

```step
@id ac6-no-message-stored
@name AC-6 nothing was stored in team@ or b@

GET /v1/mail-messages?direction=outbound
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.meta.total == 0
```

## AC-7: An admin's self-grant is recorded

```step
@id ac7-a-grants-self
@name AC-7 A gives A view on team@

PUT /v1/mailboxes/{{team_box}}/members/{{a_user_id}}
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "permission": "view"
}

[Asserts]
status == 200
body.data.user_id == {{a_user_id}}
body.data.permission == "view"
body.data.granted_by == {{a_user_id}}
```

```step
@id ac7-a-reads
@name AC-7 A now reads team@ mail

GET /v1/mailboxes/{{team_box}}/messages/{{team_msg}}
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.subject == "team-first-{{run_id}}"
```

```step
@id ac7-history
@name AC-7 team@ history shows A granting A view

GET /v1/mailboxes/{{team_box}}/access-events
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.0.type == "member_set"
body.data.0.actor_user_id == {{a_user_id}}
body.data.0.subject_user_id == {{a_user_id}}
body.data.0.created_at exists
```

```step
@id ac7-a-leaves-team
@name AC-7 cleanup: A removes A from team@

DELETE /v1/mailboxes/{{team_box}}/members/{{a_user_id}}
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 204
```

## AC-8: Only manage may change members

```step
@id ac8-b-cannot-add
@name AC-8 B (send) tries to add A to team@

PUT /v1/mailboxes/{{team_box}}/members/{{a_user_id}}
Content-Type: application/json
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

{
  "permission": "view"
}

[Asserts]
status == 403
body.error_code == "PERMISSION.DENIED"
```

```step
@id ac8-a-grants-manage
@name AC-8 A gives B manage on team@

PUT /v1/mailboxes/{{team_box}}/members/{{b_user_id}}
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "permission": "manage"
}

[Asserts]
status == 200
body.data.permission == "manage"
```

```step
@id ac8-b-adds
@name AC-8 B (manage) adds A to team@

PUT /v1/mailboxes/{{team_box}}/members/{{a_user_id}}
Content-Type: application/json
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

{
  "permission": "view"
}

[Asserts]
status == 200
body.data.granted_by == {{b_user_id}}
```

```step
@id ac8-history
@name AC-8 history records B adding A

GET /v1/mailboxes/{{team_box}}/access-events
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.0.type == "member_set"
body.data.0.actor_user_id == {{b_user_id}}
body.data.0.subject_user_id == {{a_user_id}}
```

```step
@id ac8-b-removes-a
@name AC-8 cleanup: B removes A from team@

DELETE /v1/mailboxes/{{team_box}}/members/{{a_user_id}}
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 204
```

```step
@id ac8-personal-members-conflict
@name AC-8 personal mailboxes take no members

PUT /v1/mailboxes/{{b_box}}/members/{{c_user_id}}
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "permission": "view"
}

[Asserts]
status == 409
body.error_code == "MAILBOX.ACCESS_CONFLICT"
```

## AC-9: Only shared mailboxes with the switch on reach the organization webhook

```step
@id ac9-endpoint
@name AC-9 A subscribes an endpoint to mail.message.received

POST /v1/webhook-endpoints
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "name": "Mailbox access {{run_id}}",
  "url": "https://example.com/zmail-kest-{{run_id}}",
  "event_types": ["mail.message.received"]
}

[Captures]
endpoint_id = data.endpoint.id

[Asserts]
status == 201
body.data.endpoint.id exists
```

```step
@id ac9-deliver-b
@name AC-9 inbound to personal b@
@type exec

sh {{helpers}}/inbound.sh "{{b_address}}" "b-webhook-{{run_id}}"

[Captures]
b_webhook_msg = data.id
```

```step
@id ac9-deliver-team-off
@name AC-9 inbound to team@ with the switch off
@type exec

sh {{helpers}}/inbound.sh "{{team_address}}" "team-webhook-off-{{run_id}}"

[Captures]
team_off_msg = data.id
```

```step
@id ac9-no-deliveries-yet
@name AC-9 nothing was queued so far

GET /v1/webhook-deliveries?endpoint_id={{endpoint_id}}
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.meta.total == 0
```

```step
@id ac9-personal-switch-conflict
@name AC-9 the switch cannot be turned on for a personal mailbox

PUT /v1/mailboxes/{{b_box}}/webhook-setting
Content-Type: application/json
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

{
  "enabled": true
}

[Asserts]
status == 409
body.error_code == "MAILBOX.ACCESS_CONFLICT"
```

```step
@id ac9-c-cannot-switch
@name AC-9 C (view) cannot turn the switch on

PUT /v1/mailboxes/{{team_box}}/webhook-setting
Content-Type: application/json
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

{
  "enabled": true
}

[Asserts]
status == 403
body.error_code == "PERMISSION.DENIED"
```

```step
@id ac9-b-switch-on
@name AC-9 B (manage) turns the switch on for team@

PUT /v1/mailboxes/{{team_box}}/webhook-setting
Content-Type: application/json
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

{
  "enabled": true
}

[Asserts]
status == 200
body.data.webhooks_enabled == true
```

```step
@id ac9-switch-history
@name AC-9 history records the switch

GET /v1/mailboxes/{{team_box}}/access-events
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.0.type == "webhooks_changed"
body.data.0.actor_user_id == {{b_user_id}}
```

```step
@id ac9-deliver-team-on
@name AC-9 inbound to team@ with the switch on
@type exec

sh {{helpers}}/inbound.sh "{{team_address}}" "team-webhook-on-{{run_id}}"

[Captures]
team_on_msg = data.id
```

```step
@id ac9-one-delivery
@name AC-9 exactly one delivery was queued

GET /v1/webhook-deliveries?endpoint_id={{endpoint_id}}
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.meta.total == 1
body.data.0.event_type == "mail.message.received"
```

## AC-13: Broadcasts need send on the sender mailbox

```step
@id ac13-template
@name AC-13 setup: A creates a template

POST /v1/mail-templates
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "name": "Access {{run_id}}",
  "subject": "Access check {{run_id}}",
  "html": "<p>Hello</p>",
  "text": "Hello"
}

[Captures]
template_id = data.id

[Asserts]
status == 201
```

```step
@id ac13-list
@name AC-13 setup: A creates a contact list

POST /v1/mail-contact-lists
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "name": "Access {{run_id}}"
}

[Captures]
list_id = data.id

[Asserts]
status == 201
```

```step
@id ac13-contact
@name AC-13 setup: one subscribed contact

POST /v1/mail-contacts
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "email": "recipient-{{run_id}}@example.com",
  "list_ids": [{{list_id}}]
}

[Asserts]
status == 201
body.data.status == "subscribed"
```

```step
@id ac13-b-draft
@name AC-13 B drafts a broadcast from team@

POST /v1/mail-broadcasts
Content-Type: application/json
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

{
  "name": "team-broadcast-{{run_id}}",
  "mailbox_id": {{team_box}},
  "template_id": {{template_id}},
  "list_id": {{list_id}}
}

[Captures]
team_broadcast = data.id

[Asserts]
status == 201
body.data.status == "draft"
```

```step
@id ac13-schedule-time
@name AC-13 compute a send time one day ahead
@type exec

python3 -c 'import datetime; print((datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(days=1)).strftime("%Y-%m-%dT%H:%M:%SZ"))'

[Captures]
scheduled_at = $line.0
```

```step
@id ac13-a-start-denied
@name AC-13 A (not a team@ member) starts the broadcast

POST /v1/mail-broadcasts/{{team_broadcast}}/sends
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "scheduled_at": "{{scheduled_at}}"
}

[Asserts]
status == 403
body.error_code == "PERMISSION.DENIED"
```

```step
@id ac13-a-grants-send
@name AC-13 A gives A send on team@

PUT /v1/mailboxes/{{team_box}}/members/{{a_user_id}}
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "permission": "send"
}

[Asserts]
status == 200
body.data.permission == "send"
```

```step
@id ac13-grant-history
@name AC-13 history records A granting A send

GET /v1/mailboxes/{{team_box}}/access-events
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.0.type == "member_set"
body.data.0.actor_user_id == {{a_user_id}}
body.data.0.subject_user_id == {{a_user_id}}
```

```step
@id ac13-a-start
@name AC-13 A starts the broadcast

POST /v1/mail-broadcasts/{{team_broadcast}}/sends
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "scheduled_at": "{{scheduled_at}}"
}

[Asserts]
status == 200
body.data.status == "scheduled"
```

```step
@id ac13-a-leaves-team
@name AC-13 A removes A from team@ while the broadcast is scheduled

DELETE /v1/mailboxes/{{team_box}}/members/{{a_user_id}}
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 204
```

```step
@id ac13-still-scheduled
@name AC-13 the broadcast keeps its schedule

GET /v1/mail-broadcasts/{{team_broadcast}}
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.status == "scheduled"
```

```step
@id ac13-a-cancels
@name AC-13 A can still cancel it

POST /v1/mail-broadcasts/{{team_broadcast}}/cancellations
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{}

[Asserts]
status == 200
body.data.id == {{team_broadcast}}
body.data.status == "canceled"
```

## AC-14: Creation rules

```step
@id ac14-b-cannot-create
@name AC-14 member B creates a mailbox

POST /v1/mailboxes
Content-Type: application/json
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

{
  "local_part": "b-extra-{{run_id}}",
  "type": "personal",
  "owner_user_id": {{b_user_id}}
}

[Asserts]
status == 403
body.error_code == "PERMISSION.DENIED"
```

```step
@id ac14-personal-without-owner
@name AC-14 A creates a personal mailbox without an owner

POST /v1/mailboxes
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "local_part": "no-owner-{{run_id}}",
  "type": "personal"
}

[Asserts]
status == 422
body.error_code == "MAILBOX.ACCESS_INVALID"
```

```step
@id ac14-shared-without-members
@name AC-14 A creates a shared mailbox without members

POST /v1/mailboxes
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "local_part": "no-members-{{run_id}}",
  "type": "shared",
  "members": []
}

[Asserts]
status == 422
body.error_code == "MAILBOX.ACCESS_INVALID"
```

```step
@id ac14-nothing-created
@name AC-14 the organization still has three mailboxes

GET /v1/mailboxes
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.# == 3
```

## AC-15: Broadcasts of an unreadable mailbox are invisible

```step
@id ac15-b-draft
@name AC-15 setup: B drafts a broadcast from b@

POST /v1/mail-broadcasts
Content-Type: application/json
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

{
  "name": "b-broadcast-{{run_id}}",
  "mailbox_id": {{b_box}},
  "template_id": {{template_id}},
  "list_id": {{list_id}}
}

[Captures]
b_broadcast = data.id

[Asserts]
status == 201
```

```step
@id ac15-c-list
@name AC-15 C's broadcast list has only the team@ broadcast

GET /v1/mail-broadcasts
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.meta.total == 1
body.data.0.mailbox_id == {{team_box}}
```

```step
@id ac15-c-detail
@name AC-15 C opens the b@ broadcast

GET /v1/mail-broadcasts/{{b_broadcast}}
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 404
```

```step
@id ac15-c-recipients
@name AC-15 C lists the b@ broadcast recipients

GET /v1/mail-broadcasts/{{b_broadcast}}/recipients
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 404
```

```step
@id ac15-b-control
@name AC-15 control: B opens the b@ broadcast

GET /v1/mail-broadcasts/{{b_broadcast}}
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.mailbox_id == {{b_box}}
```

## AC-10: The owner leaves

```step
@id ac10-b-leaves
@name AC-10 B leaves organization O

DELETE /v1/organizations/{{org_id}}/members/{{b_member_id}}
Authorization: Bearer {{b_token}}

[Asserts]
status == 204
```

```step
@id ac10-b-locked-out
@name AC-10 B's mailbox requests fail

GET /v1/mailboxes
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 404
body.error_code == "ORGANIZATION.NOT_FOUND"
```

```step
@id ac10-b-message-locked-out
@name AC-10 B cannot open b@ mail

GET /v1/mailboxes/{{b_box}}/messages/{{b_msg}}
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 404
body.error_code == "ORGANIZATION.NOT_FOUND"
```

```step
@id ac10-deliver
@name AC-10 new mail to b@ is accepted
@type exec

sh {{helpers}}/inbound.sh "{{b_address}}" "b-while-away-{{run_id}}"

[Captures]
b_away_msg = data.id
```

```step
@id ac10-a-list
@name AC-10 b@ awaits handover

GET /v1/mailboxes
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.#(id=={{b_box}}).type == "personal"
body.data.#(id=={{b_box}}).awaiting_handover == true
body.data.#(id=={{b_box}}).unread_count == 0
```

```step
@id ac10-a-cannot-read
@name AC-10 A cannot read the new mail

GET /v1/mailboxes/{{b_box}}/messages/{{b_away_msg}}
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 403
body.error_code == "PERMISSION.DENIED"
```

```step
@id ac10-c-cannot-read
@name AC-10 C cannot read the new mail

GET /v1/mailboxes/{{b_box}}/messages/{{b_away_msg}}
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 404
body.error_code == "MAILBOX.NOT_FOUND"
```

```step
@id ac10-history
@name AC-10 b@ history records the departure

GET /v1/mailboxes/{{b_box}}/access-events
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.0.type == "owner_left"
body.data.0.subject_user_id == {{b_user_id}}
```

```step
@id ac10-team-members
@name AC-10 B's team@ membership is gone

GET /v1/mailboxes/{{team_box}}/members
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.# == 1
body.data.0.user_id == {{c_user_id}}
```

```step
@id ac10-team-history
@name AC-10 team@ history records B's removal

GET /v1/mailboxes/{{team_box}}/access-events
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.0.type == "member_removed"
body.data.0.subject_user_id == {{b_user_id}}
body.data.0.detail.reason == "left_organization"
```

## AC-16: Rejoining before handover restores the mailbox

```step
@id ac16-invite
@name AC-16 A invites B again

POST /v1/organizations/{{org_id}}/invitations
Content-Type: application/json
Authorization: Bearer {{a_token}}

{
  "email": "ma_b_{{run_id}}@example.com",
  "role": "member"
}

[Captures]
b_invitation_2 = data.invitation.id

[Asserts]
status == 201
```

```step
@id ac16-token
@name AC-16 make the invitation acceptable
@type exec

sh {{helpers}}/invite-token.sh {{b_invitation_2}}

[Captures]
b_invite_token_2 = token
```

```step
@id ac16-accept
@name AC-16 B rejoins

POST /v1/organization-invitations/accept
Content-Type: application/json
Authorization: Bearer {{b_token}}

{
  "token": "{{b_invite_token_2}}"
}

[Asserts]
status == 200
```

```step
@id ac16-context
@name AC-16 B's new membership

GET /v1/organization-context
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Captures]
b_member_id_2 = data.membership_id

[Asserts]
status == 200
```

```step
@id ac16-b-list
@name AC-16 b@ is B's personal mailbox again; team@ is not restored

GET /v1/mailboxes
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.# == 1
body.data.0.id == {{b_box}}
body.data.0.type == "personal"
body.data.0.awaiting_handover == false
body.data.0.access.send == true
```

```step
@id ac16-b-history-visible
@name AC-16 B sees all b@ mail, including mail from the absence

GET /v1/mailboxes/{{b_box}}/messages?direction=inbound
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.meta.total == 3
body.data.#(id=={{b_away_msg}}).subject == "b-while-away-{{run_id}}"
body.data.#(id=={{b_msg}}).subject == "b-private-{{run_id}}"
```

```step
@id ac16-b-team-denied
@name AC-16 B no longer reaches team@

GET /v1/mailboxes/{{team_box}}/messages
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 404
body.error_code == "MAILBOX.NOT_FOUND"
```

```step
@id ac16-history
@name AC-16 b@ history records the return

GET /v1/mailboxes/{{b_box}}/access-events
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.0.type == "owner_returned"
body.data.0.subject_user_id == {{b_user_id}}
```

## AC-11: Handover seals the history

```step
@id ac11-b-leaves-again
@name AC-11 setup: B leaves again (this departure is the seal boundary)

DELETE /v1/organizations/{{org_id}}/members/{{b_member_id_2}}
Authorization: Bearer {{b_token}}

[Asserts]
status == 204
```

```step
@id ac11-deliver-after
@name AC-11 setup: mail arrives after the departure
@type exec

sh {{helpers}}/inbound.sh "{{b_address}}" "b-after-leave-{{run_id}}"

[Captures]
b_after_msg = data.id
```

```step
@id ac11-c-stats-before
@name AC-11 C's received count before handover

GET /v1/mail-message-stats?days=1
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Captures]
c_received_before = data.0.received

[Asserts]
status == 200
```

```step
@id ac11-c-stats-expected
@name AC-11 expected count after handover (one more message)
@type exec

echo $(( {{c_received_before}} + 1 ))

[Captures]
c_received_expected = $line.0
```

```step
@id ac11-handover
@name AC-11 A hands b@ over to C

POST /v1/mailboxes/{{b_box}}/handovers
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "successor_user_id": {{c_user_id}}
}

[Asserts]
status == 201
body.data.type == "shared"
body.data.sealed_before exists
body.data.awaiting_handover == false
```

```step
@id ac11-handover-again
@name AC-11 a second handover conflicts

POST /v1/mailboxes/{{b_box}}/handovers
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "successor_user_id": {{c_user_id}}
}

[Asserts]
status == 409
body.error_code == "MAILBOX.ACCESS_CONFLICT"
```

```step
@id ac11-c-members
@name AC-11 C holds send on b@

GET /v1/mailboxes/{{b_box}}/members
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.# == 1
body.data.0.user_id == {{c_user_id}}
body.data.0.permission == "send"
```

```step
@id ac11-c-sees-after-only
@name AC-11 C sees only the mail that arrived after B left

GET /v1/mailboxes/{{b_box}}/messages?direction=inbound
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.meta.total == 1
body.data.0.id == {{b_after_msg}}
```

```step
@id ac11-c-unread
@name AC-11 C's unread count for b@ counts only that message

GET /v1/mailboxes
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.#(id=={{b_box}}).unread_count == 1
```

```step
@id ac11-c-sealed-detail
@name AC-11 C opens a sealed message

GET /v1/mailboxes/{{b_box}}/messages/{{b_msg}}
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 404
body.error_code == "MAILBOX.MESSAGE_NOT_FOUND"
```

```step
@id ac11-c-sealed-away
@name AC-11 mail from the first absence is sealed too

GET /v1/mailboxes/{{b_box}}/messages/{{b_away_msg}}
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 404
body.error_code == "MAILBOX.MESSAGE_NOT_FOUND"
```

```step
@id ac11-c-sealed-attachment
@name AC-11 C downloads a sealed attachment

GET /v1/mailboxes/{{b_box}}/messages/{{b_msg}}/attachments/{{b_attachment}}
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 404
body.error_code == "MAILBOX.MESSAGE_NOT_FOUND"
```

```step
@id ac11-c-search-sealed
@name AC-11 the log hides sealed subjects

GET /v1/mail-messages?q=b-private-{{run_id}}
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.meta.total == 0
```

```step
@id ac11-c-search-after
@name AC-11 the log shows the new subject

GET /v1/mail-messages?q=b-after-leave-{{run_id}}
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.meta.total == 1
```

```step
@id ac11-c-stats-after
@name AC-11 statistics add only the post-departure message

GET /v1/mail-message-stats?days=1
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.0.received == {{c_received_expected}}
```

```step
@id ac11-a-joins-b
@name AC-11 A adds A to b@ as view

PUT /v1/mailboxes/{{b_box}}/members/{{a_user_id}}
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "permission": "view"
}

[Asserts]
status == 200
```

```step
@id ac11-a-sealed-detail
@name AC-11 even as a member, A cannot open sealed mail

GET /v1/mailboxes/{{b_box}}/messages/{{b_msg}}
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 404
body.error_code == "MAILBOX.MESSAGE_NOT_FOUND"
```

```step
@id ac11-a-sees-after-only
@name AC-11 A sees only the post-departure message

GET /v1/mailboxes/{{b_box}}/messages?direction=inbound
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.meta.total == 1
```

```step
@id ac11-a-leaves-b
@name AC-11 cleanup: A removes A from b@

DELETE /v1/mailboxes/{{b_box}}/members/{{a_user_id}}
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 204
```

```step
@id ac11-history
@name AC-11 b@ history records the handover

GET /v1/mailboxes/{{b_box}}/access-events
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.2.type == "handed_over"
body.data.2.subject_user_id == {{c_user_id}}
```

## AC-17: Rejoining after handover does not restore

```step
@id ac17-invite
@name AC-17 A invites B again

POST /v1/organizations/{{org_id}}/invitations
Content-Type: application/json
Authorization: Bearer {{a_token}}

{
  "email": "ma_b_{{run_id}}@example.com",
  "role": "member"
}

[Captures]
b_invitation_3 = data.invitation.id

[Asserts]
status == 201
```

```step
@id ac17-token
@name AC-17 make the invitation acceptable
@type exec

sh {{helpers}}/invite-token.sh {{b_invitation_3}}

[Captures]
b_invite_token_3 = token
```

```step
@id ac17-accept
@name AC-17 B rejoins

POST /v1/organization-invitations/accept
Content-Type: application/json
Authorization: Bearer {{b_token}}

{
  "token": "{{b_invite_token_3}}"
}

[Asserts]
status == 200
```

```step
@id ac17-b-list
@name AC-17 B has no mailboxes

GET /v1/mailboxes
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.# == 0
```

```step
@id ac17-b-denied
@name AC-17 B cannot reach b@

GET /v1/mailboxes/{{b_box}}/messages/{{b_msg}}
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 404
body.error_code == "MAILBOX.NOT_FOUND"
```

```step
@id ac17-c-keeps
@name AC-17 b@ stays with C and the history stays sealed

GET /v1/mailboxes/{{b_box}}/messages?direction=inbound
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.meta.total == 1
body.data.0.id == {{b_after_msg}}
```

```step
@id ac17-c-sealed
@name AC-17 sealed mail is still unreadable

GET /v1/mailboxes/{{b_box}}/messages/{{b_msg}}
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 404
body.error_code == "MAILBOX.MESSAGE_NOT_FOUND"
```

## AC-12: Deleting sealed history

```step
@id ac12-c-cannot-delete
@name AC-12 C (send) cannot delete sealed history

DELETE /v1/mailboxes/{{b_box}}/sealed-messages
Content-Type: application/json
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

{
  "confirm_address": "{{b_address}}"
}

[Asserts]
status == 403
body.error_code == "PERMISSION.DENIED"
```

```step
@id ac12-wrong-confirmation
@name AC-12 a wrong confirmation is refused

DELETE /v1/mailboxes/{{b_box}}/sealed-messages
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "confirm_address": "{{team_address}}"
}

[Asserts]
status == 422
body.error_code == "MAILBOX.ACCESS_INVALID"
```

```step
@id ac12-no-boundary
@name AC-12 a mailbox without sealed history conflicts

DELETE /v1/mailboxes/{{team_box}}/sealed-messages
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "confirm_address": "{{team_address}}"
}

[Asserts]
status == 409
body.error_code == "MAILBOX.ACCESS_CONFLICT"
```

```step
@id ac12-delete
@name AC-12 A deletes b@'s sealed history

DELETE /v1/mailboxes/{{b_box}}/sealed-messages
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "confirm_address": "{{b_address}}"
}

[Asserts]
status == 200
body.data.deleted_messages == 3
body.data.deleted_broadcasts == 1
```

```step
@id ac12-history
@name AC-12 history records the deletion and its time

GET /v1/mailboxes/{{b_box}}/access-events
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.0.type == "sealed_deleted"
body.data.0.actor_user_id == {{a_user_id}}
body.data.0.created_at exists
```

```step
@id ac12-gone
@name AC-12 deletion cannot be undone: a repeat finds nothing

DELETE /v1/mailboxes/{{b_box}}/sealed-messages
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "confirm_address": "{{b_address}}"
}

[Asserts]
status == 200
body.data.deleted_messages == 0
body.data.deleted_broadcasts == 0
```

```step
@id ac12-broadcast-gone
@name AC-12 the sealed broadcast is gone

GET /v1/mail-broadcasts/{{b_broadcast}}
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 404
```

```step
@id ac12-c-keeps-new-mail
@name AC-12 mail after the boundary is untouched

GET /v1/mailboxes/{{b_box}}/messages?direction=inbound
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.meta.total == 1
body.data.0.id == {{b_after_msg}}
```

## Order

Steps run in document order; these edges make the chain explicit.

```edge
@from install-helpers
@to register-a
@on success
```

```edge
@from register-a
@to login-a
@on success
```

```edge
@from login-a
@to register-b
@on success
```

```edge
@from register-b
@to login-b
@on success
```

```edge
@from login-b
@to register-c
@on success
```

```edge
@from register-c
@to login-c
@on success
```

```edge
@from login-c
@to create-org
@on success
```

```edge
@from create-org
@to invite-b
@on success
```

```edge
@from invite-b
@to token-b
@on success
```

```edge
@from token-b
@to accept-b
@on success
```

```edge
@from accept-b
@to context-b
@on success
```

```edge
@from context-b
@to invite-c
@on success
```

```edge
@from invite-c
@to token-c
@on success
```

```edge
@from token-c
@to accept-c
@on success
```

```edge
@from accept-c
@to domains
@on success
```

```edge
@from domains
@to create-b-box
@on success
```

```edge
@from create-b-box
@to create-c-box
@on success
```

```edge
@from create-c-box
@to create-team-box
@on success
```

```edge
@from create-team-box
@to deliver-b-setup
@on success
```

```edge
@from deliver-b-setup
@to deliver-c-setup
@on success
```

```edge
@from deliver-c-setup
@to deliver-team-setup
@on success
```

```edge
@from deliver-team-setup
@to b-reads-own-message
@on success
```

```edge
@from b-reads-own-message
@to ac1-c-list
@on success
```

```edge
@from ac1-c-list
@to ac1-c-messages
@on success
```

```edge
@from ac1-c-messages
@to ac1-c-detail
@on success
```

```edge
@from ac1-c-detail
@to ac1-c-attachment
@on success
```

```edge
@from ac1-c-attachment
@to ac1-c-mark-read
@on success
```

```edge
@from ac1-c-mark-read
@to ac1-c-send
@on success
```

```edge
@from ac1-c-send
@to ac1-nothing-sent
@on success
```

```edge
@from ac1-nothing-sent
@to ac2-a-list
@on success
```

```edge
@from ac2-a-list
@to ac2-a-messages
@on success
```

```edge
@from ac2-a-messages
@to ac2-a-detail
@on success
```

```edge
@from ac2-a-detail
@to ac3-a-search
@on success
```

```edge
@from ac3-a-search
@to ac3-b-search
@on success
```

```edge
@from ac3-b-search
@to ac3-a-stats
@on success
```

```edge
@from ac3-a-stats
@to ac3-c-stats
@on success
```

```edge
@from ac3-c-stats
@to ac4-c-list-team
@on success
```

```edge
@from ac4-c-list-team
@to ac4-c-send-team
@on success
```

```edge
@from ac4-c-send-team
@to ac4-no-message-stored
@on success
```

```edge
@from ac4-no-message-stored
@to ac5-deliver
@on success
```

```edge
@from ac5-deliver
@to ac5-b-before
@on success
```

```edge
@from ac5-b-before
@to ac5-b-opens
@on success
```

```edge
@from ac5-b-opens
@to ac5-b-after
@on success
```

```edge
@from ac5-b-after
@to ac5-c-after
@on success
```

```edge
@from ac5-c-after
@to ac5-c-message-unread
@on success
```

```edge
@from ac5-c-message-unread
@to ac6-c-key
@on success
```

```edge
@from ac6-c-key
@to ac6-key-send-team
@on success
```

```edge
@from ac6-key-send-team
@to ac6-key-send-b
@on success
```

```edge
@from ac6-key-send-b
@to ac6-no-message-stored
@on success
```

```edge
@from ac6-no-message-stored
@to ac7-a-grants-self
@on success
```

```edge
@from ac7-a-grants-self
@to ac7-a-reads
@on success
```

```edge
@from ac7-a-reads
@to ac7-history
@on success
```

```edge
@from ac7-history
@to ac7-a-leaves-team
@on success
```

```edge
@from ac7-a-leaves-team
@to ac8-b-cannot-add
@on success
```

```edge
@from ac8-b-cannot-add
@to ac8-a-grants-manage
@on success
```

```edge
@from ac8-a-grants-manage
@to ac8-b-adds
@on success
```

```edge
@from ac8-b-adds
@to ac8-history
@on success
```

```edge
@from ac8-history
@to ac8-b-removes-a
@on success
```

```edge
@from ac8-b-removes-a
@to ac8-personal-members-conflict
@on success
```

```edge
@from ac8-personal-members-conflict
@to ac9-endpoint
@on success
```

```edge
@from ac9-endpoint
@to ac9-deliver-b
@on success
```

```edge
@from ac9-deliver-b
@to ac9-deliver-team-off
@on success
```

```edge
@from ac9-deliver-team-off
@to ac9-no-deliveries-yet
@on success
```

```edge
@from ac9-no-deliveries-yet
@to ac9-personal-switch-conflict
@on success
```

```edge
@from ac9-personal-switch-conflict
@to ac9-c-cannot-switch
@on success
```

```edge
@from ac9-c-cannot-switch
@to ac9-b-switch-on
@on success
```

```edge
@from ac9-b-switch-on
@to ac9-switch-history
@on success
```

```edge
@from ac9-switch-history
@to ac9-deliver-team-on
@on success
```

```edge
@from ac9-deliver-team-on
@to ac9-one-delivery
@on success
```

```edge
@from ac9-one-delivery
@to ac13-template
@on success
```

```edge
@from ac13-template
@to ac13-list
@on success
```

```edge
@from ac13-list
@to ac13-contact
@on success
```

```edge
@from ac13-contact
@to ac13-b-draft
@on success
```

```edge
@from ac13-b-draft
@to ac13-schedule-time
@on success
```

```edge
@from ac13-schedule-time
@to ac13-a-start-denied
@on success
```

```edge
@from ac13-a-start-denied
@to ac13-a-grants-send
@on success
```

```edge
@from ac13-a-grants-send
@to ac13-grant-history
@on success
```

```edge
@from ac13-grant-history
@to ac13-a-start
@on success
```

```edge
@from ac13-a-start
@to ac13-a-leaves-team
@on success
```

```edge
@from ac13-a-leaves-team
@to ac13-still-scheduled
@on success
```

```edge
@from ac13-still-scheduled
@to ac13-a-cancels
@on success
```

```edge
@from ac13-a-cancels
@to ac14-b-cannot-create
@on success
```

```edge
@from ac14-b-cannot-create
@to ac14-personal-without-owner
@on success
```

```edge
@from ac14-personal-without-owner
@to ac14-shared-without-members
@on success
```

```edge
@from ac14-shared-without-members
@to ac14-nothing-created
@on success
```

```edge
@from ac14-nothing-created
@to ac15-b-draft
@on success
```

```edge
@from ac15-b-draft
@to ac15-c-list
@on success
```

```edge
@from ac15-c-list
@to ac15-c-detail
@on success
```

```edge
@from ac15-c-detail
@to ac15-c-recipients
@on success
```

```edge
@from ac15-c-recipients
@to ac15-b-control
@on success
```

```edge
@from ac15-b-control
@to ac10-b-leaves
@on success
```

```edge
@from ac10-b-leaves
@to ac10-b-locked-out
@on success
```

```edge
@from ac10-b-locked-out
@to ac10-b-message-locked-out
@on success
```

```edge
@from ac10-b-message-locked-out
@to ac10-deliver
@on success
```

```edge
@from ac10-deliver
@to ac10-a-list
@on success
```

```edge
@from ac10-a-list
@to ac10-a-cannot-read
@on success
```

```edge
@from ac10-a-cannot-read
@to ac10-c-cannot-read
@on success
```

```edge
@from ac10-c-cannot-read
@to ac10-history
@on success
```

```edge
@from ac10-history
@to ac10-team-members
@on success
```

```edge
@from ac10-team-members
@to ac10-team-history
@on success
```

```edge
@from ac10-team-history
@to ac16-invite
@on success
```

```edge
@from ac16-invite
@to ac16-token
@on success
```

```edge
@from ac16-token
@to ac16-accept
@on success
```

```edge
@from ac16-accept
@to ac16-context
@on success
```

```edge
@from ac16-context
@to ac16-b-list
@on success
```

```edge
@from ac16-b-list
@to ac16-b-history-visible
@on success
```

```edge
@from ac16-b-history-visible
@to ac16-b-team-denied
@on success
```

```edge
@from ac16-b-team-denied
@to ac16-history
@on success
```

```edge
@from ac16-history
@to ac11-b-leaves-again
@on success
```

```edge
@from ac11-b-leaves-again
@to ac11-deliver-after
@on success
```

```edge
@from ac11-deliver-after
@to ac11-c-stats-before
@on success
```

```edge
@from ac11-c-stats-before
@to ac11-c-stats-expected
@on success
```

```edge
@from ac11-c-stats-expected
@to ac11-handover
@on success
```

```edge
@from ac11-handover
@to ac11-handover-again
@on success
```

```edge
@from ac11-handover-again
@to ac11-c-members
@on success
```

```edge
@from ac11-c-members
@to ac11-c-sees-after-only
@on success
```

```edge
@from ac11-c-sees-after-only
@to ac11-c-unread
@on success
```

```edge
@from ac11-c-unread
@to ac11-c-sealed-detail
@on success
```

```edge
@from ac11-c-sealed-detail
@to ac11-c-sealed-away
@on success
```

```edge
@from ac11-c-sealed-away
@to ac11-c-sealed-attachment
@on success
```

```edge
@from ac11-c-sealed-attachment
@to ac11-c-search-sealed
@on success
```

```edge
@from ac11-c-search-sealed
@to ac11-c-search-after
@on success
```

```edge
@from ac11-c-search-after
@to ac11-c-stats-after
@on success
```

```edge
@from ac11-c-stats-after
@to ac11-a-joins-b
@on success
```

```edge
@from ac11-a-joins-b
@to ac11-a-sealed-detail
@on success
```

```edge
@from ac11-a-sealed-detail
@to ac11-a-sees-after-only
@on success
```

```edge
@from ac11-a-sees-after-only
@to ac11-a-leaves-b
@on success
```

```edge
@from ac11-a-leaves-b
@to ac11-history
@on success
```

```edge
@from ac11-history
@to ac17-invite
@on success
```

```edge
@from ac17-invite
@to ac17-token
@on success
```

```edge
@from ac17-token
@to ac17-accept
@on success
```

```edge
@from ac17-accept
@to ac17-b-list
@on success
```

```edge
@from ac17-b-list
@to ac17-b-denied
@on success
```

```edge
@from ac17-b-denied
@to ac17-c-keeps
@on success
```

```edge
@from ac17-c-keeps
@to ac17-c-sealed
@on success
```

```edge
@from ac17-c-sealed
@to ac12-c-cannot-delete
@on success
```

```edge
@from ac12-c-cannot-delete
@to ac12-wrong-confirmation
@on success
```

```edge
@from ac12-wrong-confirmation
@to ac12-no-boundary
@on success
```

```edge
@from ac12-no-boundary
@to ac12-delete
@on success
```

```edge
@from ac12-delete
@to ac12-history
@on success
```

```edge
@from ac12-history
@to ac12-gone
@on success
```

```edge
@from ac12-gone
@to ac12-broadcast-gone
@on success
```

```edge
@from ac12-broadcast-gone
@to ac12-c-keeps-new-mail
@on success
```
