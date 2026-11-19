# Mailbox Organize Acceptance Flow

End-to-end proof of mailbox organize on a shared mailbox (`docs/plans/mailbox-organize.zh-CN.md`;
`contracts/MAILBOXES.md`, sections Placements, Updating Messages, Threads, Listing And Search, and
Counts): shared placement, the `send` and `view` split for organizing, per-person stars, threading
an inbound reply onto an outbound message, and search.

Cast: organization O, admin A (owner of O), members B and C. The shared mailbox `team-<run>@` has B
with `send` and C with `view`.

This flow has its own organization and cast instead of extending
`mailbox_access.flow.md`: that flow asserts exact totals, unread counts, and per-day statistics of
`team@` while memberships change, so adding messages to it would change those numbers.

## How To Run

Start the API with `APP_ENV=test`, the organization and mailbox starters, and a disposable
PostgreSQL database, then run kest from a directory whose `.kest/config.yaml` points the `local`
environment at the API:

```bash
export FASTBUILD_TEST_POSTGRES_DSN=postgres://...      # disposable database (also used by helpers)
export OPTIONAL_STARTERS=organization,mailbox
export MAILBOX_DOMAIN=mail.localhost
export MAILBOX_PROVIDER=log                            # nothing leaves the machine
export MAILBOX_INBOUND_SECRET=kest-inbound-secret-at-least-32-chars   # 32+ characters
# ... migrate and start the API with these variables, then:
kest run tests/kest/mailbox_organize.flow.md -e local --fail-fast --var "run_id=$(date +%s)"
```

`run_local.sh` starts the API without the mailbox starters, so it cannot run this flow as is.
kest 0.7.2 drops values captured by `@type exec` steps and does not substitute `{{variables}}`
inside an assertion's or capture's JSON path, so this flow never captures from an exec step and
selects list items by literal queries (`#(subject%"Re: *")`, `#(direction="outbound")`), then
compares ids on the right-hand side.

The helper steps need `sh`, `curl`, `openssl`, `awk`, `sed`, and `psql` on `PATH`.

## Helpers

The first step (`install-helpers`, an exec step) writes two scripts under
`tmp/kest/mailbox-organize-<run_id>/` (relative to where kest runs):

- `inbound.sh <recipient> <subject> <replied_message_id>` reads the stored `rfc_message_id` of the
  replied message from `FASTBUILD_TEST_POSTGRES_DSN` (the API does not return the Message-ID Zmail
  generates: the detail's `message_id` of an outbound message is the provider's identifier, empty
  with the `log` provider), builds a plain-text RFC 5322 message with `In-Reply-To` set to it and
  the UTF-8 body `Re <quote>: accepted <subject>`, where `<quote>` is the Chinese word for
  quote (U+62A5 U+4EF7), signs it as the
  mail edge does (`X-Mailbox-Signature`, HMAC-SHA256 over `<t>.<recipient>.<raw body>` with
  `MAILBOX_INBOUND_SECRET`), posts it to `POST /v1/inbound-mail-messages`, and fails unless the
  answer is `201`. It reads the API base URL from `.kest/config.yaml` unless `KEST_API_BASE_URL`
  is set.
- `invite-token.sh <invitation_id> <token>` makes an invitation acceptable without reading mail by
  storing the SHA-256 hash of the given token on the invitation row in
  `FASTBUILD_TEST_POSTGRES_DSN`, exactly as in `mailbox_access.flow.md`.

## Known Limits Of This Flow

- B's send goes through the `log` provider, so the outbound message is `sent` without leaving the
  machine. Whether a real provider keeps Zmail's `Message-ID` on the wire is checked by hand with
  `docs/runbooks/mail-message-id-check.md`.
- The inbound reply names the outbound message only in `In-Reply-To`; `References` matching is
  covered by the API tests.

```flow
@flow id=zmail-mailbox-organize
@name Zmail Mailbox Organize Acceptance
@version 1.0
@tags mailbox, organize, threads, search, acceptance
@env local
```

## Setup

```step
@id install-helpers
@name Install inbound reply and invitation helpers
@type exec

dir="tmp/kest/mailbox-organize-{{run_id}}"
mkdir -p "$dir"
cat > "$dir/inbound.sh" <<'SH'
#!/bin/sh
# usage: inbound.sh <recipient> <subject> <replied_message_id>
set -eu
rcpt=$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')
subject=$2
replied=$3
case "$replied" in ''|*[!0-9]*) echo "replied message id must be numeric" >&2; exit 1 ;; esac
: "${MAILBOX_INBOUND_SECRET:?export MAILBOX_INBOUND_SECRET for both the API and kest}"
: "${FASTBUILD_TEST_POSTGRES_DSN:?export FASTBUILD_TEST_POSTGRES_DSN (the disposable test database)}"
rfc=$(psql "$FASTBUILD_TEST_POSTGRES_DSN" -v ON_ERROR_STOP=1 -qtAc \
  "SELECT rfc_message_id FROM mail_messages WHERE id = $replied" | head -n 1)
case "$rfc" in *@*) ;; *) echo "message $replied has no stored rfc_message_id" >&2; exit 1 ;; esac
in_reply_to="<$rfc>"
base_url=${KEST_API_BASE_URL:-$(awk '/^  local:/{f=1} f && /base_url:/{print $2; exit}' .kest/config.yaml)}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
now=$(LC_ALL=C date -u '+%a, %d %b %Y %H:%M:%S +0000')
printf 'From: Kest Customer <customer@example.net>\r\nTo: %s\r\nSubject: %s\r\nMessage-ID: <reply.%s@kest.invalid>\r\nIn-Reply-To: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\nRe \346\212\245\344\273\267: accepted %s\r\n' \
  "$rcpt" "$subject" "$$" "$in_reply_to" "$now" "$subject" > "$work/raw.eml"
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
echo "In-Reply-To: $in_reply_to"
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
  "username": "mo_a_{{run_id}}",
  "email": "mo_a_{{run_id}}@example.com",
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
  "username": "mo_a_{{run_id}}@example.com",
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
  "username": "mo_b_{{run_id}}",
  "email": "mo_b_{{run_id}}@example.com",
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
  "username": "mo_b_{{run_id}}@example.com",
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
  "username": "mo_c_{{run_id}}",
  "email": "mo_c_{{run_id}}@example.com",
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
  "username": "mo_c_{{run_id}}@example.com",
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
  "name": "Mailbox Organize {{run_id}}"
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
  "email": "mo_b_{{run_id}}@example.com",
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

sh tmp/kest/mailbox-organize-{{run_id}}/invite-token.sh {{b_invitation_id}} kest-invite-{{run_id}}-{{b_invitation_id}}
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
  "email": "mo_c_{{run_id}}@example.com",
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

sh tmp/kest/mailbox-organize-{{run_id}}/invite-token.sh {{c_invitation_id}} kest-invite-{{run_id}}-{{c_invitation_id}}
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
```

## OR-1: An inbound reply joins the thread of the outbound message

```step
@id or1-b-sends
@name OR-1 B sends a quote from team@ (log provider)

POST /v1/mailboxes/{{team_box}}/messages
Content-Type: application/json
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

{
  "to": ["customer-{{run_id}}@example.net"],
  "subject": "\u62a5\u4ef7 quote-{{run_id}}",
  "text": "\u8bf7\u67e5\u6536\u62a5\u4ef7 (please find the quote)."
}

[Captures]
out_msg = data.id

[Asserts]
status == 201
body.data.status == "sent"
body.data.placement == "default"
```

```step
@id or1-b-detail
@name OR-1 the outbound message starts its own thread

GET /v1/mailboxes/{{team_box}}/messages/{{out_msg}}
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.thread_id == {{out_msg}}
```

```step
@id or1-reply
@name OR-1 the customer's reply arrives in team@ with In-Reply-To
@type exec

sh tmp/kest/mailbox-organize-{{run_id}}/inbound.sh "{{team_address}}" "Re: quote-{{run_id}}" {{out_msg}}
```

```step
@id or1-find-reply
@name OR-1 B finds the reply in the Inbox, in the outbound message's thread

GET /v1/mailboxes/{{team_box}}/messages?placement=default&direction=inbound
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Captures]
reply_msg = data.#(subject%"Re: *").id

[Asserts]
status == 200
body.meta.total == 1
body.data.#(subject%"Re: *").subject == "Re: quote-{{run_id}}"
body.data.#(subject%"Re: *").thread_id == {{out_msg}}
```

```step
@id or1-reply-detail
@name OR-1 the reply carries its In-Reply-To and the outbound message's thread

GET /v1/mailboxes/{{team_box}}/messages/{{reply_msg}}
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.in_reply_to exists
body.data.thread_id == {{out_msg}}
```

```step
@id or1-c-thread
@name OR-1 C opens the thread: the quote, then the reply

GET /v1/mailboxes/{{team_box}}/threads/{{out_msg}}
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.# == 2
body.data.0.id == {{out_msg}}
body.data.0.direction == "outbound"
body.data.1.id == {{reply_msg}}
body.data.1.direction == "inbound"
```

## OR-2: The send member archives; the view member sees it and cannot trash

```step
@id or2-b-archives
@name OR-2 B archives the reply

PUT /v1/mailboxes/{{team_box}}/messages/{{reply_msg}}
Content-Type: application/json
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

{
  "placement": "archive"
}

[Asserts]
status == 200
body.data.placement == "archive"
body.data.trashed_at == ""
```

```step
@id or2-c-sees-archived
@name OR-2 C sees the reply archived

GET /v1/mailboxes/{{team_box}}/messages/{{reply_msg}}
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.placement == "archive"
```

```step
@id or2-c-archive-list
@name OR-2 C's Archive holds the reply

GET /v1/mailboxes/{{team_box}}/messages?placement=archive
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.meta.total == 1
body.data.0.id == {{reply_msg}}
```

```step
@id or2-c-inbox-empty
@name OR-2 C's Inbox no longer shows it

GET /v1/mailboxes/{{team_box}}/messages?placement=default&direction=inbound
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.meta.total == 0
```

```step
@id or2-c-cannot-delete
@name OR-2 C cannot move the reply to the trash with DELETE

DELETE /v1/mailboxes/{{team_box}}/messages/{{reply_msg}}
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 403
body.error_code == "PERMISSION.DENIED"
```

```step
@id or2-c-cannot-trash
@name OR-2 C cannot move the reply to the trash with an update

PUT /v1/mailboxes/{{team_box}}/messages/{{reply_msg}}
Content-Type: application/json
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

{
  "placement": "trash"
}

[Asserts]
status == 403
body.error_code == "PERMISSION.DENIED"
```

```step
@id or2-c-batch-mixed
@name OR-2 C's batch of a star and a trash move is refused as a whole

POST /v1/mailboxes/{{team_box}}/message-updates
Content-Type: application/json
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

{
  "message_ids": [{{reply_msg}}],
  "starred": true,
  "placement": "trash"
}

[Asserts]
status == 403
body.error_code == "PERMISSION.DENIED"
```

```step
@id or2-still-archived
@name OR-2 nothing changed: still archived and not starred for C

GET /v1/mailboxes/{{team_box}}/messages/{{reply_msg}}
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.placement == "archive"
body.data.trashed_at == ""
body.data.starred == false
```

## OR-3: Stars are per person

```step
@id or3-c-stars
@name OR-3 C stars the reply (view is enough)

PUT /v1/mailboxes/{{team_box}}/messages/{{reply_msg}}
Content-Type: application/json
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

{
  "starred": true
}

[Asserts]
status == 200
body.data.starred == true
```

```step
@id or3-b-not-starred
@name OR-3 the reply is not starred for B

GET /v1/mailboxes/{{team_box}}/messages/{{reply_msg}}
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.starred == false
```

```step
@id or3-c-starred-list
@name OR-3 C's Starred view holds the reply

GET /v1/mailboxes/{{team_box}}/messages?starred=true
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.meta.total == 1
body.data.0.id == {{reply_msg}}
```

```step
@id or3-b-starred-list
@name OR-3 B's Starred view is empty

GET /v1/mailboxes/{{team_box}}/messages?starred=true
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.meta.total == 0
```

```step
@id or3-c-counts
@name OR-3 C counts one starred message

GET /v1/mailboxes/{{team_box}}/message-counts
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.starred == 1
```

```step
@id or3-b-counts
@name OR-3 B counts none

GET /v1/mailboxes/{{team_box}}/message-counts
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.starred == 0
```

## OR-4: Searching the Chinese word for quote finds the messages

The query below is the Chinese word for quote (U+62A5 U+4EF7), percent-encoded as UTF-8. The send
request above writes it as JSON escapes so this file stays English-only.

```step
@id or4-c-search
@name OR-4 C searches for quote in Chinese: the quote by subject, the archived reply by body

GET /v1/mailboxes/{{team_box}}/messages?q=%E6%8A%A5%E4%BB%B7
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.meta.total == 2
body.data.#(direction="outbound").id == {{out_msg}}
body.data.#(direction="inbound").id == {{reply_msg}}
```

```step
@id or4-b-trashes
@name OR-4 B moves the reply to the trash

DELETE /v1/mailboxes/{{team_box}}/messages/{{reply_msg}}
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 204
```

```step
@id or4-search-skips-trash
@name OR-4 search no longer finds the trashed reply

GET /v1/mailboxes/{{team_box}}/messages?q=%E6%8A%A5%E4%BB%B7
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.meta.total == 1
body.data.0.id == {{out_msg}}
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
@to create-team-box
@on success
```

```edge
@from create-team-box
@to or1-b-sends
@on success
```

```edge
@from or1-b-sends
@to or1-b-detail
@on success
```

```edge
@from or1-b-detail
@to or1-reply
@on success
```

```edge
@from or1-reply
@to or1-find-reply
@on success
```

```edge
@from or1-find-reply
@to or1-reply-detail
@on success
```

```edge
@from or1-reply-detail
@to or1-c-thread
@on success
```

```edge
@from or1-c-thread
@to or2-b-archives
@on success
```

```edge
@from or2-b-archives
@to or2-c-sees-archived
@on success
```

```edge
@from or2-c-sees-archived
@to or2-c-archive-list
@on success
```

```edge
@from or2-c-archive-list
@to or2-c-inbox-empty
@on success
```

```edge
@from or2-c-inbox-empty
@to or2-c-cannot-delete
@on success
```

```edge
@from or2-c-cannot-delete
@to or2-c-cannot-trash
@on success
```

```edge
@from or2-c-cannot-trash
@to or2-c-batch-mixed
@on success
```

```edge
@from or2-c-batch-mixed
@to or2-still-archived
@on success
```

```edge
@from or2-still-archived
@to or3-c-stars
@on success
```

```edge
@from or3-c-stars
@to or3-b-not-starred
@on success
```

```edge
@from or3-b-not-starred
@to or3-c-starred-list
@on success
```

```edge
@from or3-c-starred-list
@to or3-b-starred-list
@on success
```

```edge
@from or3-b-starred-list
@to or3-c-counts
@on success
```

```edge
@from or3-c-counts
@to or3-b-counts
@on success
```

```edge
@from or3-b-counts
@to or4-c-search
@on success
```

```edge
@from or4-c-search
@to or4-b-trashes
@on success
```

```edge
@from or4-b-trashes
@to or4-search-skips-trash
@on success
```
