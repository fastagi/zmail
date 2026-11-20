# Platform Accounts Acceptance Flow

End-to-end proof of the platform account ledger and owner handover
(`docs/plans/external-accounts.zh-CN.md` §2; `contracts/MAILBOXES.md`, Platform Accounts): the
owner records an account registered with a mailbox and finds it by that address; the ledger is
unique per platform and handle, ignoring case; a member who cannot read the mailbox never sees the
account, its address, or its history; only owners and admins write; a batch handover moves every
account of one user to another and records an `owner_changed` event; deleting an account hides its
history.

Cast: organization O, owner A, members B and C. B owns the personal mailbox `b-<run>@`; C holds no
mailbox access. A records the YouTube account `@kest-<run>`, registered with `b@` and run by B.

## How To Run

```bash
export FASTBUILD_TEST_POSTGRES_DSN=postgres://...      # disposable database (also used by the helper)
export OPTIONAL_STARTERS=organization,mailbox
export MAILBOX_DOMAIN=mail.localhost
export MAILBOX_PROVIDER=log                            # nothing leaves the machine
export MAILBOX_INBOUND_SECRET=kest-inbound-secret-at-least-32-chars
./tests/kest/run_local.sh tests/kest/platform_accounts.flow.md
```

The helper step needs `sh`, `openssl`, `awk`, and `psql` on `PATH`.

## Helpers

The first step writes `invite-token.sh <invitation_id> <token>` under
`tmp/kest/platform-accounts-<run_id>/`. It makes an invitation acceptable without reading mail,
as in `unified_inbox.flow.md`.

```flow
@flow id=zmail-platform-accounts
@name Zmail Platform Accounts Acceptance
@version 1.0
@tags mailbox, ledger, handover, acceptance
@env local
```

## Setup

```step
@id install-helpers
@name Install the invitation helper
@type exec

dir="tmp/kest/platform-accounts-{{run_id}}"
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
chmod +x "$dir/invite-token.sh"
echo "$dir"
```

```step
@id register-a
@name Register owner A
@retry 2

POST /v1/register
Content-Type: application/json

{
  "username": "pa_a_{{run_id}}",
  "email": "pa_a_{{run_id}}@example.com",
  "password": "password123",
  "nickname": "Owner A"
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
  "username": "pa_a_{{run_id}}@example.com",
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
  "username": "pa_b_{{run_id}}",
  "email": "pa_b_{{run_id}}@example.com",
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
  "username": "pa_b_{{run_id}}@example.com",
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
  "username": "pa_c_{{run_id}}",
  "email": "pa_c_{{run_id}}@example.com",
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
  "username": "pa_c_{{run_id}}@example.com",
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
  "name": "Platform Accounts {{run_id}}"
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
  "email": "pa_b_{{run_id}}@example.com",
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

sh tmp/kest/platform-accounts-{{run_id}}/invite-token.sh {{b_invitation_id}} kest-invite-{{run_id}}-{{b_invitation_id}}
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
  "email": "pa_c_{{run_id}}@example.com",
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

sh tmp/kest/platform-accounts-{{run_id}}/invite-token.sh {{c_invitation_id}} kest-invite-{{run_id}}-{{c_invitation_id}}
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

## Ledger

```step
@id pa1-create
@name PA-1 A records a YouTube account registered with b@ and run by B

POST /v1/platform-accounts
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "platform": "YouTube",
  "handle": "@kest-{{run_id}}",
  "profile_url": "https://www.youtube.com/@kest-{{run_id}}",
  "mailbox_id": {{b_box}},
  "owner_user_id": {{b_user_id}},
  "notes": "Kest acceptance"
}

[Captures]
account_id = data.id

[Asserts]
status == 201
body.data.status == "active"
body.data.mailbox_id == {{b_box}}
body.data.mailbox_address == "{{b_address}}"
body.data.owner_user_id == {{b_user_id}}
```

```step
@id pa1-taken
@name PA-1 the same platform and handle in other case is taken

POST /v1/platform-accounts
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "platform": "youtube",
  "handle": "@KEST-{{run_id}}"
}

[Asserts]
status == 409
body.error_code == "MAILBOX.PLATFORM_ACCOUNT_TAKEN"
```

```step
@id pa2-search-address
@name PA-2 A finds the account by its registration address

GET /v1/platform-accounts?q={{b_address}}
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.# == 1
body.data.0.id == {{account_id}}
body.data.0.handle == "@kest-{{run_id}}"
body.meta.total == 1
```

```step
@id pa3-b-sees
@name PA-3 B, who reads b@ and runs the account, sees it with its address

GET /v1/platform-accounts?q={{b_address}}
Authorization: Bearer {{b_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.# == 1
body.data.0.mailbox_address == "{{b_address}}"
```

```step
@id pa3-c-list
@name PA-3 C cannot read b@ and does not see the account

GET /v1/platform-accounts
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.# == 0
body.meta.total == 0
```

```step
@id pa3-c-search
@name PA-3 searching by b@'s address finds nothing for C

GET /v1/platform-accounts?q={{b_address}}
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.# == 0
```

```step
@id pa3-c-events
@name PA-3 C cannot read the account's history

GET /v1/platform-accounts/{{account_id}}/events
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 404
body.error_code == "MAILBOX.PLATFORM_ACCOUNT_NOT_FOUND"
```

```step
@id pa3-c-write
@name PA-3 a member cannot write the ledger

POST /v1/platform-accounts
Content-Type: application/json
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

{
  "platform": "Medium",
  "handle": "c-{{run_id}}"
}

[Asserts]
status == 403
```

## Handover

```step
@id pa4-c-handover
@name PA-4 a member cannot hand accounts over

POST /v1/platform-account-handovers
Content-Type: application/json
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

{
  "from_user_id": {{b_user_id}},
  "to_user_id": {{c_user_id}}
}

[Asserts]
status == 403
```

```step
@id pa4-handover
@name PA-4 A hands every account of B to C

POST /v1/platform-account-handovers
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "from_user_id": {{b_user_id}},
  "to_user_id": {{c_user_id}}
}

[Asserts]
status == 201
body.data.from_user_id == {{b_user_id}}
body.data.to_user_id == {{c_user_id}}
body.data.account_ids.# == 1
body.data.account_ids.0 == {{account_id}}
```

```step
@id pa4-events
@name PA-4 the history records the handover, newest first

GET /v1/platform-accounts/{{account_id}}/events
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.# == 2
body.data.0.kind == "owner_changed"
body.data.0.from == "{{b_user_id}}"
body.data.0.to == "{{c_user_id}}"
body.data.1.kind == "created"
```

```step
@id pa4-c-owns
@name PA-4 C now runs the account and sees it, still without b@'s address

GET /v1/platform-accounts?owner_user_id={{c_user_id}}
Authorization: Bearer {{c_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 200
body.data.# == 1
body.data.0.id == {{account_id}}
body.data.0.owner_user_id == {{c_user_id}}
body.data.0.mailbox_id == {{b_box}}
```

```step
@id pa4-handover-again
@name PA-4 handing over again moves nothing

POST /v1/platform-account-handovers
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "from_user_id": {{b_user_id}},
  "to_user_id": {{c_user_id}}
}

[Asserts]
status == 201
body.data.account_ids.# == 0
```

## Delete

```step
@id pa5-delete
@name PA-5 A deletes the account

DELETE /v1/platform-accounts/{{account_id}}
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 204
```

```step
@id pa5-events-gone
@name PA-5 the deleted account's history answers not found

GET /v1/platform-accounts/{{account_id}}/events
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

[Asserts]
status == 404
body.error_code == "MAILBOX.PLATFORM_ACCOUNT_NOT_FOUND"
```
