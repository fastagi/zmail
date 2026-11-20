# Operator Mail Metrics Acceptance Flow

End-to-end proof of the read-only platform mail metrics (`docs/plans/admin-mail-operations.plan.md`;
`contracts/OPERATORS.md`, Mail Metrics): an organization owner gets no operator access from the
role; a `mail_metrics` grant signs in to the operator session and reads the overview, the
organization row, and the outbound log of the day, but not an account-management route; a public
bearer session never reads the metrics.

Cast: owner A runs organization O and the personal mailbox `ops-<run>@`, from which A sends one
message. Viewer V belongs to no organization and holds a `mail_metrics` grant.

## How To Run

```bash
export FASTBUILD_TEST_POSTGRES_DSN=postgres://...      # disposable database (also used by the helper)
./tests/kest/run_local.sh tests/kest/operator_mail_metrics.flow.md
```

The server settings are in `operator_mail_metrics.kestenv` (the `log` mail provider, so nothing
leaves the machine). Grants are CLI-only, so the grant step writes the row with `psql` exactly as
`fastbuild operator:grant <email> --scope=mail-metrics` would. The operator session travels in an
HttpOnly cookie and Kest cannot capture response headers or exec output, so V's steps run through
the `operator-get.sh` helper: it signs in once with `curl`, keeps the cookie under
`tmp/kest/operator-metrics-<run_id>/`, and asserts the status and JSON fields of each read.
The helper steps need `sh`, `curl`, `awk`, `python3`, and `psql` on `PATH`.

```flow
@flow id=zmail-operator-mail-metrics
@name Zmail Operator Mail Metrics Acceptance
@version 1.0
@tags operator, mailbox, metrics, acceptance
@env local
```

## Setup

```step
@id register-a
@name Register owner A
@retry 2

POST /v1/register
Content-Type: application/json

{
  "username": "om_a_{{run_id}}",
  "email": "om_a_{{run_id}}@example.com",
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
  "username": "om_a_{{run_id}}@example.com",
  "password": "password123"
}

[Captures]
a_token = data.access_token

[Asserts]
status == 200
```

```step
@id create-org
@name A creates organization O

POST /v1/organizations
Content-Type: application/json
Authorization: Bearer {{a_token}}

{
  "name": "Metrics {{run_id}}"
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
  "local_part": "ops-{{run_id}}",
  "display_name": "Ops",
  "type": "personal",
  "owner_user_id": {{a_user_id}}
}

[Captures]
box_id = data.id

[Asserts]
status == 201
```

```step
@id send
@name A sends one message

POST /v1/mailboxes/{{box_id}}/messages
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "to": ["customer@example.com"],
  "subject": "metrics-{{run_id}}",
  "text": "Body text that the operator log never returns."
}

[Asserts]
status == 201
body.data.status == "sent"
```

```step
@id register-v
@name Register viewer V
@retry 2

POST /v1/register
Content-Type: application/json

{
  "username": "om_v_{{run_id}}",
  "email": "om_v_{{run_id}}@example.com",
  "password": "password123",
  "nickname": "Viewer V"
}

[Asserts]
status == 201
```

## The organization owner gets nothing from the role

```step
@id owner-sign-in
@name A cannot sign in to the operator session

POST /v1/operator/session
Content-Type: application/json
Origin: http://admin.localhost

{
  "identifier": "om_a_{{run_id}}@example.com",
  "password": "password123"
}

[Asserts]
status == 403
body.error_code == "OPERATOR.FORBIDDEN"
```

```step
@id bearer-metrics
@name A public bearer session cannot read the metrics

GET /v1/operator/mail-metrics
Authorization: Bearer {{a_token}}

[Asserts]
status == 401
```

## A metrics-only grant reads the metrics

```step
@id grant-v
@name Grant V the mail_metrics scope
@type exec

: "${FASTBUILD_TEST_POSTGRES_DSN:?export FASTBUILD_TEST_POSTGRES_DSN (the disposable test database)}"
granted=$(psql "$FASTBUILD_TEST_POSTGRES_DSN" -v ON_ERROR_STOP=1 -qtAc \
  "INSERT INTO platform_operators (user_id, granted_at, scope)
   SELECT id, now(), 'mail_metrics' FROM users WHERE email = 'om_v_{{run_id}}@example.com'
   RETURNING scope")
[ "$granted" = "mail_metrics" ] || { echo "grant failed: $granted" >&2; exit 1; }
```

```step
@id install-helper
@name V signs in and installs the cookie-carrying read helper
@type exec

dir="tmp/kest/operator-metrics-{{run_id}}"
mkdir -p "$dir"
base_url=${KEST_API_BASE_URL:-$(awk '/^  local:/{f=1} f && /base_url:/{print $2; exit}' .kest/config.yaml)}
body=$(curl -sS -D "$dir/headers" -X POST "$base_url/v1/operator/session" \
  -H 'Content-Type: application/json' -H 'Origin: http://admin.localhost' \
  --data '{"identifier":"om_v_{{run_id}}@example.com","password":"password123"}')
case "$body" in *'"capabilities":["mail_metrics:read"]'*) ;; *) echo "unexpected session: $body" >&2; exit 1 ;; esac
awk 'tolower($1)=="set-cookie:" {split($2, pair, ";"); print pair[1]; exit}' "$dir/headers" > "$dir/cookie"
[ -s "$dir/cookie" ] || { echo "no session cookie" >&2; exit 1; }
cat > "$dir/operator-get.sh" <<SH
#!/bin/sh
# usage: operator-get.sh <path> <status> [field=value ...]  (field is a dotted JSON path)
set -eu
path=\$1; want=\$2; shift 2
out=\$(mktemp)
code=\$(curl -sS -o "\$out" -w '%{http_code}' -H "Cookie: \$(cat $dir/cookie)" "$base_url\$path")
python3 - "\$out" "\$code" "\$want" "\$@" <<'PY'
import json, sys
body = json.load(open(sys.argv[1]))
code, want = sys.argv[2], sys.argv[3]
if code != want:
    sys.exit(f"status {code}, want {want}: {body}")
for check in sys.argv[4:]:
    field, expected = check.split("=", 1)
    value = body
    for part in field.split("."):
        value = value[int(part)] if isinstance(value, list) else value[part]
    if str(value) != expected:
        sys.exit(f"{field} = {value!r}, want {expected!r}")
for row in body.get("data") if isinstance(body.get("data"), list) else []:
    for secret in ("text_body", "html_body", "to_addresses", "text", "html"):
        if secret in row:
            sys.exit(f"content field {secret} leaked")
PY
rm -f "\$out"
SH
chmod +x "$dir/operator-get.sh"
```

```step
@id v-overview
@name V reads the platform overview with a 7-day trend
@type exec

d="tmp/kest/operator-metrics-{{run_id}}"
"$d/operator-get.sh" "/v1/operator/mail-metrics?days=7" 200 code=0
python3 - "$(curl -sS -H "Cookie: $(cat $d/cookie)" "$(awk '/^  local:/{f=1} f && /base_url:/{print $2; exit}' .kest/config.yaml)/v1/operator/mail-metrics?days=7")" <<'PY'
import json, sys
data = json.loads(sys.argv[1])["data"]
assert len(data["trend"]) == 7, data
assert data["day"]["sent"] >= 1 and data["organizations"] >= 1, data
PY
```

```step
@id v-organization
@name V reads O's row for today
@type exec

"tmp/kest/operator-metrics-{{run_id}}/operator-get.sh" "/v1/operator/mail-metrics/organizations?q=Metrics%20{{run_id}}" 200 \
  meta.total=1 data.0.id={{org_id}} data.0.members=1 data.0.mailboxes=1 data.0.sent=1 data.0.failed=0
```

```step
@id v-members
@name V opens O's member list
@type exec

"tmp/kest/operator-metrics-{{run_id}}/operator-get.sh" "/v1/operator/organizations/{{org_id}}/members" 200 \
  meta.total=1 data.0.role=owner
```

```step
@id v-log
@name V reads O's outbound log without content
@type exec

"tmp/kest/operator-metrics-{{run_id}}/operator-get.sh" "/v1/operator/outbound-messages?organization_id={{org_id}}&status=sent" 200 \
  meta.total=1 data.0.subject=metrics-{{run_id}} data.0.mailbox_id={{box_id}} "data.0.organization_name=Metrics {{run_id}}"
```

```step
@id v-manage
@name V cannot use an account-management route
@type exec

"tmp/kest/operator-metrics-{{run_id}}/operator-get.sh" "/v1/operator/users" 403 error_code=OPERATOR.CAPABILITY_REQUIRED
```
