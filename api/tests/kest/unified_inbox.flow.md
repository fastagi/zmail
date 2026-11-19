# Unified Inbox Acceptance Flow

End-to-end proof of the unified inbox and mail roles (`docs/plans/unified-inbox.zh-CN.md`, §16;
`contracts/MAILBOXES.md`, sections Mail Roles and Unified Inbox): the list, counts, search, and
filters never show a mailbox the caller cannot read; roles group but grant nothing; today and
pending; deleting a role ungroups its mailboxes; assignment is idempotent.

Cast (§16): organization O, admin A (owner of O), members B and C. B owns the personal mailbox
`b-<run>@`, C owns `c-<run>@`, and the shared mailbox `team-<run>@` has B with `send` and C with
`view`. Mail roles: "Design desk" holds `b@`, "Official" holds `team@`. Every mailbox receives one
plain-text message during setup.

## How To Run

Start the API with `APP_ENV=test`, the organization and mailbox starters, and a disposable
PostgreSQL database. `run_local.sh` passes the starter variables through, so:

```bash
export FASTBUILD_TEST_POSTGRES_DSN=postgres://...      # disposable database (also used by helpers)
export OPTIONAL_STARTERS=organization,mailbox
export MAILBOX_DOMAIN=mail.localhost
export MAILBOX_PROVIDER=log                            # nothing leaves the machine
export MAILBOX_INBOUND_SECRET=kest-inbound-secret-at-least-32-chars   # 32+ characters
./tests/kest/run_local.sh tests/kest/unified_inbox.flow.md
```

The helper steps need `sh`, `curl`, `openssl`, `awk`, and `psql` on `PATH`.

## Helpers

The first step writes two scripts under `tmp/kest/unified-inbox-<run_id>/`:

- `inbound.sh <recipient> <subject>` builds a plain-text RFC 5322 message whose body is
  `hello <subject>`, signs it as the mail edge does (`X-Mailbox-Signature`, HMAC-SHA256 over
  `<t>.<recipient>.<raw body>` with `MAILBOX_INBOUND_SECRET`), posts it to
  `POST /v1/inbound-mail-messages`, and fails unless the answer is `201`.
- `invite-token.sh <invitation_id> <token>` makes an invitation acceptable without reading mail, as
  in `mailbox_access.flow.md`.

## Known Limits Of This Flow

- `today` is checked in UTC; a run that crosses 00:00 UTC between delivery and the check fails
  and is rerun.
- The 2-second search limit (`MAILBOX.SEARCH_TOO_BROAD`) and the threaded listing are covered by
  the API tests, not here.

```flow
@flow id=zmail-unified-inbox
@name Zmail Unified Inbox Acceptance
@version 1.0
@tags mailbox, inbox, roles, acceptance
@env local
```

## Setup

```step
@id install-helpers
@name Install inbound and invitation helpers
@type exec

dir="tmp/kest/unified-inbox-{{run_id}}"
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
now=$(LC_ALL=C date -u '+%a, %d %b %Y %H:%M:%S +0000')
printf 'From: Kest Sender <sender@example.net>\r\nTo: %s\r\nSubject: %s\r\nMessage-ID: <ui.%s.%s@kest.invalid>\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nhello %s\r\n' \
  "$rcpt" "$subject" "$$" "$(date +%s)" "$now" "$subject" > "$work/raw.eml"
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
# usage: invite-token.sh <invitation_id> <token>
set -eu
id=$1
token=$2
case "$id" in ''|*[!0-9]*) echo "invitation id must be numeric" >&2; exit 1 ;; esac
case "$token" in ''|*[!A-Za-z0-9-]*) echo "token must be non-empty [A-Za-z0-9-]" >&2; exit 1 ;; esac
: "${FASTBUILD_TEST_POSTGRES_DSN:?export FASTBUILD_TEST_POSTGRES_DSN (the disposable test database)}"
hash=$(printf '%s' "$token" | openssl dgst -sha256 | awk '{print $NF}')
updated=$(psql "$FASTBUILD_TEST_POSTGRES_DSN" -v ON_ERROR_STOP=1 -qtAc \
  "UPDATE organization_invitations SET token_hash = '$hash' WHERE id = $id RETURNING id" | head -n 1)
if [ "$updated" != "$id" ]; then
  echo "invitation $id not found" >&2
  exit 1
fi
echo "invitation $id accepts its token"
SH
chmod +x "$dir/inbound.sh" "$dir/invite-token.sh"
echo "$dir"
```

```step
@id register-a
@name Register admin A
@retry 2

POST /v1/register
Content-Type: application/json

{
  "username": "ui_a_{{run_id}}",
  "email": "ui_a_{{run_id}}@example.com",
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
  "username": "ui_a_{{run_id}}@example.com",
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
  "username": "ui_b_{{run_id}}",
  "email": "ui_b_{{run_id}}@example.com",
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
  "username": "ui_b_{{run_id}}@example.com",
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
  "username": "ui_c_{{run_id}}",
  "email": "ui_c_{{run_id}}@example.com",
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
  "username": "ui_c_{{run_id}}@example.com",
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
  "name": "Unified Inbox {{run_id}}"
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
  "email": "ui_b_{{run_id}}@example.com",
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

sh tmp/kest/unified-inbox-{{run_id}}/invite-token.sh {{b_invitation_id}} kest-invite-{{run_id}}-{{b_invitation_id}}
```

```step
@id accept-b
@name B accepts the invitation

POST /v1/organization-invitations/accept
Content-Type: application/json
Authorization: Bearer {{b_token}}

{
  "token": "kest-invite-{{run_id}}-{{b_invitation_id}}"
}

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
  "email": "ui_c_{{run_id}}@example.com",
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

sh tmp/kest/unified-inbox-{{run_id}}/invite-token.sh {{c_invitation_id}} kest-invite-{{run_id}}-{{c_invitation_id}}
```

```step
@id accept-c
@name C accepts the invitation

POST /v1/organization-invitations/accept
Content-Type: application/json
Authorization: Bearer {{c_token}}

{
  "token": "kest-invite-{{run_id}}-{{c_invitation_id}}"
}

[Asserts]
status == 200
body.data.role == "member"
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
```

```step
@id create-design-role
@name A creates the mail role "Design desk"

POST /v1/mail-roles
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{ "name": "Design desk {{run_id}}", "color": "orange" }

[Captures]
design_role = data.id

[Asserts]
status == 201
body.data.mailbox_ids.# == 0
```

```step
@id create-official-role
@name A creates the mail role "Official"

POST /v1/mail-roles
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{ "name": "Official {{run_id}}", "color": "blue" }

[Captures]
official_role = data.id

[Asserts]
status == 201
```

```step
@id assign-b
@name A puts b@ in Design desk

PUT /v1/mailboxes/{{b_box}}/role-assignment
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{ "role_id": {{design_role}} }

[Asserts]
status == 200
body.data.role_id == {{design_role}}
```

```step
@id assign-b-again
@name Assigning the same role again succeeds and changes nothing

PUT /v1/mailboxes/{{b_box}}/role-assignment
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{ "role_id": {{design_role}} }

[Asserts]
status == 200
body.data.role_id == {{design_role}}
```

```step
@id assign-team
@name A puts team@ in Official

PUT /v1/mailboxes/{{team_box}}/role-assignment
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{ "role_id": {{official_role}} }

[Asserts]
status == 200
body.data.role_id == {{official_role}}
```

```step
@id deliver-b
@name Inbound message to b@
@type exec

sh tmp/kest/unified-inbox-{{run_id}}/inbound.sh "{{b_address}}" "b-private-{{run_id}}"
```

```step
@id deliver-c
@name Inbound message to c@
@type exec

sh tmp/kest/unified-inbox-{{run_id}}/inbound.sh "{{c_address}}" "c-private-{{run_id}}"
```

```step
@id deliver-team
@name Inbound message to team@
@type exec

sh tmp/kest/unified-inbox-{{run_id}}/inbound.sh "{{team_address}}" "team-first-{{run_id}}"
```

## UI-1: C sees only the mailboxes C reads

```step
@id ui1-c-list
@name UI-1 C lists c@ and team@ mail, labelled with team@'s role, and nothing of b@

GET /v1/inbox-messages
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Captures]
team_msg = data.#(subject%"team-first-*").id

[Asserts]
status == 200
body.data.# == 2
body.data.#(subject%"c-private-*").mailbox_id == {{c_box}}
body.data.#(subject%"team-first-*").mailbox_id == {{team_box}}
body.data.#(subject%"team-first-*").role_id == {{official_role}}
body.data.#(subject%"team-first-*").snippet == "hello team-first-{{run_id}}"
body.meta.has_more == false
```

```step
@id ui1-c-roles
@name UI-1 C sees Official (it holds team@) but not Design desk

GET /v1/mail-roles
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.# == 1
body.data.0.id == {{official_role}}
body.data.0.mailbox_ids.# == 1
body.data.0.mailbox_ids.0 == {{team_box}}
```

```step
@id ui1-b-list
@name UI-1 B lists b@ and team@ mail

GET /v1/inbox-messages
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.# == 2
body.data.#(subject%"b-private-*").role_id == {{design_role}}
body.data.#(subject%"team-first-*").mailbox_id == {{team_box}}
```

## UI-2: counts cover only what C reads

```step
@id ui2-c-counts
@name UI-2 C counts two unread, one in Official, one ungrouped

GET /v1/inbox-message-counts
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.views.all_unread == 2
body.data.views.today == 2
body.data.views.pending == 0
body.data.mailboxes.# == 2
body.data.roles.# == 1
body.data.roles.0.role_id == {{official_role}}
body.data.roles.0.unread == 1
body.data.ungrouped_unread == 1
```

## UI-3: search and filters never reach b@

```step
@id ui3-c-search
@name UI-3 C searches "private": only c@ mail, never b@

GET /v1/inbox-messages?q=private-{{run_id}}
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.# == 1
body.data.0.mailbox_id == {{c_box}}
```

```step
@id ui3-c-mailbox-filter
@name UI-3 C narrows to b@: an empty page, no error

GET /v1/inbox-messages?mailbox_id={{b_box}}
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.# == 0
```

```step
@id ui3-c-role-filter
@name UI-3 C narrows to Design desk: an empty page, no error

GET /v1/inbox-messages?role_id={{design_role}}
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.# == 0
```

```step
@id ui3-c-ungrouped
@name UI-3 C narrows to ungrouped mailboxes: only c@

GET /v1/inbox-messages?role_id=none
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.# == 1
body.data.0.mailbox_id == {{c_box}}
```

## UI-9: the organization role reads nothing more

```step
@id ui9-a-list
@name UI-9 A, owner but reader of no mailbox, gets an empty page

GET /v1/inbox-messages
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.# == 0
```

```step
@id ui9-a-counts
@name UI-9 A counts zeros

GET /v1/inbox-message-counts
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.views.all_unread == 0
body.data.mailboxes.# == 0
body.data.roles.# == 0
```

```step
@id ui9-b-create-role
@name UI-9 B, a member, cannot create a mail role

POST /v1/mail-roles
Content-Type: application/json
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

{ "name": "Mine {{run_id}}" }

[Asserts]
status == 403
```

## UI-4 and UI-5: today and pending

```step
@id ui4-c-today
@name UI-4 C's today view holds both new messages

GET /v1/inbox-messages?view=today
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.# == 2
```

```step
@id ui5-c-stars
@name UI-5 C stars the team@ message

PUT /v1/mailboxes/{{team_box}}/messages/{{team_msg}}
Content-Type: application/json
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

{ "starred": true }

[Asserts]
status == 200
body.data.starred == true
```

```step
@id ui5-c-pending
@name UI-5 C's pending view holds the starred message

GET /v1/inbox-messages?view=pending
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.# == 1
body.data.0.id == {{team_msg}}
```

```step
@id ui5-b-pending
@name UI-5 stars are personal: B's pending view is empty

GET /v1/inbox-messages?view=pending
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.# == 0
```

## UI-8: deleting a role ungroups its mailboxes

```step
@id ui8-delete-official
@name UI-8 A deletes Official

DELETE /v1/mail-roles/{{official_role}}
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 204
```

```step
@id ui8-c-list
@name UI-8 C still lists both messages, now both among the mailboxes without a role

GET /v1/inbox-messages?role_id=none
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.# == 2
body.data.#(subject%"team-first-*").mailbox_id == {{team_box}}
```

```step
@id ui8-c-counts
@name UI-8 totals are unchanged; both unread are ungrouped

GET /v1/inbox-message-counts
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.views.all_unread == 2
body.data.roles.# == 0
body.data.ungrouped_unread == 2
```

```step
@id ui8-delete-again
@name UI-8 deleting Official again is not found

DELETE /v1/mail-roles/{{official_role}}
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 404
```
