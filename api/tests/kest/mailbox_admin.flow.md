# Shared Mailbox Administration Acceptance Flow

End-to-end proof of the organization admin area's API (`docs/plans/shared-mailbox-admin.zh-CN.md`;
`contracts/MAILBOXES.md`, Mailbox Directory and Import And Export): the admin lists the shared
mailbox directory with member and 24-hour send counts; a plain member is refused the directory,
imports, exports, and renames; an import dry run creates nothing and reports a member outside the
organization; the real run creates the valid row; a rename writes a `renamed` access event; the
export answers the CSV file; and `scope=usable` leaves out a shared mailbox the admin is not a
member of.

Cast: owner A of organization O; member B of O.

## How To Run

```bash
export FASTBUILD_TEST_POSTGRES_DSN=postgres://...      # disposable database
export OPTIONAL_STARTERS=organization,mailbox
export MAILBOX_DOMAIN=mail.localhost
export MAILBOX_PROVIDER=log                            # nothing leaves the machine
export MAILBOX_INBOUND_SECRET=kest-inbound-secret-at-least-32-chars
./tests/kest/run_local.sh tests/kest/mailbox_admin.flow.md
```

`run_local.sh` reads the server settings from `mailbox_admin.kestenv`. The helper steps need `sh`,
`curl`, `openssl`, `awk`, `grep`, and `psql` on `PATH`.

## Helpers For Steps That Plain HTTP Steps Cannot Express

The first step writes three scripts under `tmp/kest/mailbox-admin-<run_id>/`:

- `invite-token.sh <invitation_id> <token>` makes an invitation acceptable without reading mail,
  as in `mailbox_access.flow.md`: it stores the SHA-256 hash of the token on the invitation row in
  `FASTBUILD_TEST_POSTGRES_DSN`; the acceptance itself still goes through the API.
- `export.sh <bearer> <organization_id> <type> <expect>` downloads `GET /v1/mailbox-exports`, which
  answers a CSV file rather than JSON, and fails unless the answer is `200 text/csv` with an
  attachment file name, the expected header line, and a line containing `<expect>`.
- `listed.sh <bearer> <organization_id> <query> <address> <yes|no>` reads `GET /v1/mailboxes` with
  the given query and fails unless the address is (`yes`) or is not (`no`) in the list.

```flow
@flow id=zmail-mailbox-admin
@name Zmail Shared Mailbox Administration Acceptance
@version 1.0
@tags mailbox, organization, admin, acceptance
@env local
```

## Setup

```step
@id install-helpers
@name Install the invitation, export, and list helpers
@type exec

dir="tmp/kest/mailbox-admin-{{run_id}}"
mkdir -p "$dir"
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
cat > "$dir/export.sh" <<'SH'
#!/bin/sh
# usage: export.sh <bearer_token> <organization_id> <type> <expected_text>
set -eu
token=$1 org=$2 type=$3 expect=$4
base_url=${KEST_API_BASE_URL:-$(awk '/^  local:/{f=1} f && /base_url:/{print $2; exit}' .kest/config.yaml)}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
code=$(curl -sS -D "$work/headers" -o "$work/body.csv" -w '%{http_code}' \
  -H "Authorization: Bearer $token" -H "Organization-Id: $org" \
  "$base_url/v1/mailbox-exports?type=$type")
if [ "$code" != "200" ]; then
  echo "export returned $code: $(cat "$work/body.csv")" >&2
  exit 1
fi
grep -qi '^content-type: text/csv' "$work/headers" || { echo "not text/csv" >&2; cat "$work/headers" >&2; exit 1; }
grep -qi "^content-disposition: attachment; filename=\"$type-mailboxes-" "$work/headers" \
  || { echo "no attachment file name" >&2; cat "$work/headers" >&2; exit 1; }
# The file starts with a UTF-8 byte order mark; lines end with CRLF.
header=$(head -n 1 "$work/body.csv" | tr -d '\r' | sed 's/^\xEF\xBB\xBF//')
if [ "$header" != "address,display_name,members,member_count,sent_last_24h,created_at" ]; then
  echo "unexpected header: $header" >&2
  exit 1
fi
grep -qF "$expect" "$work/body.csv" || { echo "no line contains $expect" >&2; cat "$work/body.csv" >&2; exit 1; }
cat "$work/body.csv"
SH
cat > "$dir/listed.sh" <<'SH'
#!/bin/sh
# usage: listed.sh <bearer_token> <organization_id> <query> <address> <yes|no>
set -eu
token=$1 org=$2 query=$3 address=$4 want=$5
base_url=${KEST_API_BASE_URL:-$(awk '/^  local:/{f=1} f && /base_url:/{print $2; exit}' .kest/config.yaml)}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
code=$(curl -sS -o "$work/body.json" -w '%{http_code}' \
  -H "Authorization: Bearer $token" -H "Organization-Id: $org" \
  "$base_url/v1/mailboxes$query")
if [ "$code" != "200" ]; then
  echo "GET /v1/mailboxes$query returned $code: $(cat "$work/body.json")" >&2
  exit 1
fi
if grep -qF "\"address\":\"$address\"" "$work/body.json"; then found=yes; else found=no; fi
if [ "$found" != "$want" ]; then
  echo "GET /v1/mailboxes$query: $address listed=$found, want $want" >&2
  cat "$work/body.json" >&2
  exit 1
fi
echo "GET /v1/mailboxes$query: $address listed=$found"
SH
chmod +x "$dir/invite-token.sh" "$dir/export.sh" "$dir/listed.sh"
echo "$dir"
```

```step
@id register-a
@name Register owner A
@retry 2

POST /v1/register
Content-Type: application/json

{
  "username": "mad_a_{{run_id}}",
  "email": "mad_a_{{run_id}}@example.com",
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
  "username": "mad_a_{{run_id}}@example.com",
  "password": "password123"
}

[Captures]
a_token = data.access_token

[Asserts]
status == 200
```

```step
@id register-b
@name Register B
@retry 2

POST /v1/register
Content-Type: application/json

{
  "username": "mad_b_{{run_id}}",
  "email": "mad_b_{{run_id}}@example.com",
  "password": "password123",
  "nickname": "Member B"
}

[Captures]
b_user_id = data.id

[Asserts]
status == 201
```

```step
@id login-b
@name Sign in as B
@retry 2

POST /v1/login
Content-Type: application/json

{
  "username": "mad_b_{{run_id}}@example.com",
  "password": "password123"
}

[Captures]
b_token = data.access_token

[Asserts]
status == 200
```

```step
@id create-org
@name A creates organization O

POST /v1/organizations
Content-Type: application/json
Authorization: Bearer {{a_token}}

{ "name": "Mailbox Admin {{run_id}}" }

[Captures]
org_id = data.id

[Asserts]
status == 201
```

```step
@id invite-b
@name A invites B as member

POST /v1/organizations/{{org_id}}/invitations
Content-Type: application/json
Authorization: Bearer {{a_token}}

{
  "email": "mad_b_{{run_id}}@example.com",
  "role": "member"
}

[Captures]
b_invitation_id = data.invitation.id

[Asserts]
status == 201
```

```step
@id token-b
@name Make B's invitation acceptable
@type exec

sh tmp/kest/mailbox-admin-{{run_id}}/invite-token.sh {{b_invitation_id}} kest-invite-{{run_id}}-{{b_invitation_id}}
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
@id create-desk
@name A creates shared mailbox desk@ with A (manage) and B (send)

POST /v1/mailboxes
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "local_part": "desk-{{run_id}}",
  "display_name": "Desk",
  "type": "shared",
  "members": [
    { "user_id": {{a_user_id}}, "permission": "manage" },
    { "user_id": {{b_user_id}}, "permission": "send" }
  ]
}

[Captures]
desk_box = data.id
desk_address = data.address

[Asserts]
status == 201
```

```step
@id create-ops
@name A creates shared mailbox ops@ with only B, so A is not a member

POST /v1/mailboxes
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "local_part": "ops-{{run_id}}",
  "display_name": "Ops",
  "type": "shared",
  "members": [
    { "user_id": {{b_user_id}}, "permission": "view" }
  ]
}

[Captures]
ops_box = data.id
ops_address = data.address

[Asserts]
status == 201
```

```step
@id send-from-desk
@name A sends one message from desk@ (log provider: nothing leaves the machine)

POST /v1/mailboxes/{{desk_box}}/messages
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "to": ["someone-{{run_id}}@example.com"],
  "subject": "admin-count-{{run_id}}",
  "text": "Counts toward the 24-hour send total."
}

[Asserts]
status >= 200
status < 300
```

## Directory

```step
@id directory
@name A lists the shared mailbox directory with member and send counts

GET /v1/mailbox-directory?type=shared
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.#(display_name="Desk").member_count == 2
body.data.#(display_name="Desk").sent_last_24h == 1
body.data.#(display_name="Ops").member_count == 1
body.data.#(display_name="Ops").sent_last_24h == 0
body.meta.sent_last_24h == 1
body.meta.daily_send_limit exists
body.meta.has_more == false
```

```step
@id directory-search
@name A searches the directory by address

GET /v1/mailbox-directory?type=shared&q=ops-{{run_id}}
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.0.address == "{{ops_address}}"
body.data.0.display_name == "Ops"
```

## A member is refused

```step
@id member-directory
@name B may not list the directory

GET /v1/mailbox-directory?type=shared
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 403
body.error_code == "PERMISSION.DENIED"
```

```step
@id member-import
@name B may not import

POST /v1/mailbox-imports
Content-Type: application/json
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

{
  "dry_run": true,
  "rows": [
    { "address": "b-try-{{run_id}}@mail.localhost", "display_name": "", "members": "mad_b_{{run_id}}@example.com" }
  ]
}

[Asserts]
status == 403
body.error_code == "PERMISSION.DENIED"
```

```step
@id member-export
@name B may not export

GET /v1/mailbox-exports?type=shared
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 403
body.error_code == "PERMISSION.DENIED"
```

```step
@id member-rename
@name B may not rename a mailbox, even one B works in

PUT /v1/mailboxes/{{desk_box}}
Content-Type: application/json
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

{ "display_name": "Taken over" }

[Asserts]
status == 403
body.error_code == "PERMISSION.DENIED"
```

## Import

```step
@id import-dry-run
@name A checks two rows: one valid, one with a member outside O

POST /v1/mailbox-imports
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "dry_run": true,
  "rows": [
    { "address": "sales-{{run_id}}@mail.localhost", "display_name": "Sales", "members": "MAD_B_{{run_id}}@example.com:manage" },
    { "address": "ghost-{{run_id}}@mail.localhost", "display_name": "Ghost", "members": "nobody-{{run_id}}@example.com" }
  ]
}

[Asserts]
status == 200
body.data.dry_run == true
body.data.succeeded == 1
body.data.failed == 1
body.data.rows.0.status == "valid"
body.data.rows.1.status == "failed"
body.data.rows.1.error_code == "MAILBOX.IMPORT_MEMBER_NOT_FOUND"
body.data.rows.1.missing_emails.0 == "nobody-{{run_id}}@example.com"
```

```step
@id import-dry-run-created-nothing
@name The dry run created nothing

GET /v1/mailbox-directory?type=shared&q=sales-{{run_id}}
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.meta.mailbox_count == 2
```

```step
@id import-real
@name A imports the same rows: the valid one is created

POST /v1/mailbox-imports
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "dry_run": false,
  "rows": [
    { "address": "sales-{{run_id}}@mail.localhost", "display_name": "Sales", "members": "MAD_B_{{run_id}}@example.com:manage" },
    { "address": "ghost-{{run_id}}@mail.localhost", "display_name": "Ghost", "members": "nobody-{{run_id}}@example.com" }
  ]
}

[Captures]
sales_box = data.rows.0.mailbox_id

[Asserts]
status == 200
body.data.dry_run == false
body.data.succeeded == 1
body.data.failed == 1
body.data.rows.0.status == "created"
body.data.rows.0.mailbox_id exists
body.data.rows.1.error_code == "MAILBOX.IMPORT_MEMBER_NOT_FOUND"
```

```step
@id imported-members
@name The imported mailbox has B with manage

GET /v1/mailboxes/{{sales_box}}/members
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.0.user_id == {{b_user_id}}
body.data.0.permission == "manage"
```

```step
@id import-again
@name Importing the address again is refused: it is taken

POST /v1/mailbox-imports
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "dry_run": true,
  "rows": [
    { "address": "sales-{{run_id}}@mail.localhost", "display_name": "Sales", "members": "mad_b_{{run_id}}@example.com" }
  ]
}

[Asserts]
status == 200
body.data.rows.0.status == "failed"
body.data.rows.0.error_code == "MAILBOX.ADDRESS_TAKEN"
```

## Rename

```step
@id rename
@name A renames desk@

PUT /v1/mailboxes/{{desk_box}}
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{ "display_name": "Billing" }

[Asserts]
status == 200
body.data.display_name == "Billing"
```

```step
@id rename-event
@name The rename is in desk@'s access history

GET /v1/mailboxes/{{desk_box}}/access-events
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.0.type == "renamed"
body.data.0.actor_user_id == {{a_user_id}}
```

## Export

```step
@id export
@name A exports the shared mailboxes as CSV with member emails
@type exec

sh tmp/kest/mailbox-admin-{{run_id}}/export.sh {{a_token}} {{org_id}} shared "mad_b_{{run_id}}@example.com:manage"
```

## The usable scope

```step
@id usable-omits-ops
@name A's usable list leaves out ops@, which A is not a member of
@type exec

sh tmp/kest/mailbox-admin-{{run_id}}/listed.sh {{a_token}} {{org_id}} "?scope=usable" "{{ops_address}}" no
```

```step
@id usable-keeps-desk
@name A's usable list keeps desk@, which A is a member of
@type exec

sh tmp/kest/mailbox-admin-{{run_id}}/listed.sh {{a_token}} {{org_id}} "?scope=usable" "{{desk_address}}" yes
```

```step
@id unscoped-lists-ops
@name A's unscoped list still includes ops@
@type exec

sh tmp/kest/mailbox-admin-{{run_id}}/listed.sh {{a_token}} {{org_id}} "" "{{ops_address}}" yes
```
