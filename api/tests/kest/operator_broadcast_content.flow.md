# Operator Broadcast Content Acceptance Flow

End-to-end proof of reading organization broadcast text (`docs/plans/admin-mail-content.plan.md`;
`contracts/OPERATORS.md`, Broadcast Content), spec acceptance row 4: an organization owner, a
public bearer session, and a `mail_metrics` grant cannot read a body; a grant with the content
add-on reads a broadcast message's text, which leaves one access record; ordinary outbound mail and
a mismatched organization are refused; removing content makes the next read fail.

Cast: owner A runs organization O and the personal mailbox `bc-<run>@`. A sends one ordinary
message, then a broadcast to one contact. Viewer V holds a `mail_metrics` grant; reader R holds
`mail_metrics` plus the content add-on.

## How To Run

```bash
export FASTBUILD_TEST_POSTGRES_DSN=postgres://...      # disposable database (also used by the helper)
./tests/kest/run_local.sh tests/kest/operator_broadcast_content.flow.md
```

The server settings are in `operator_broadcast_content.kestenv` (the `log` mail provider, so
nothing leaves the machine). The broadcast is delivered by one `mail:broadcast:work --once` run of
the binary `run_local.sh` built, with the same settings. Grants are CLI-only, so the grant steps
write the row with `psql` exactly as `fastbuild operator:grant <email> --scope=...` would. Operator
sessions travel in HttpOnly cookies, so V's and R's steps run through the `op.sh` helper: it signs
in with `curl`, keeps each cookie and CSRF token under `tmp/kest/operator-content-<run_id>/`, and
asserts status and JSON fields. The helper steps need `sh`, `curl`, `python3`, and `psql`.

```flow
@flow id=zmail-operator-broadcast-content
@name Zmail Operator Broadcast Content Acceptance
@version 1.0
@tags operator, mailbox, broadcast, content, acceptance
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
  "username": "bc_a_{{run_id}}",
  "email": "bc_a_{{run_id}}@example.com",
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
  "username": "bc_a_{{run_id}}@example.com",
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
  "name": "Content {{run_id}}"
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
  "local_part": "bc-{{run_id}}",
  "display_name": "Broadcasts",
  "type": "personal",
  "owner_user_id": {{a_user_id}}
}

[Captures]
box_id = data.id

[Asserts]
status == 201
```

```step
@id send-ordinary
@name A sends one ordinary message

POST /v1/mailboxes/{{box_id}}/messages
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "to": ["customer@example.com"],
  "subject": "ordinary-{{run_id}}",
  "text": "Ordinary business mail that no operator may read."
}

[Captures]
ordinary_id = data.id

[Asserts]
status == 201
body.data.status == "sent"
```

```step
@id template
@name A creates a broadcast template

POST /v1/mail-templates
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "name": "Content {{run_id}}",
  "subject": "broadcast-{{run_id}}",
  "html": "<p>Spring news {{run_id}}</p>",
  "text": "Spring news {{run_id}}"
}

[Captures]
template_id = data.id

[Asserts]
status == 201
```

```step
@id list
@name A creates a contact list

POST /v1/mail-contact-lists
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "name": "Content {{run_id}}"
}

[Captures]
list_id = data.id

[Asserts]
status == 201
```

```step
@id contact
@name One subscribed contact

POST /v1/mail-contacts
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "email": "reader-{{run_id}}@example.com",
  "list_ids": [{{list_id}}]
}

[Asserts]
status == 201
```

```step
@id broadcast
@name A drafts a broadcast

POST /v1/mail-broadcasts
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{
  "name": "content-{{run_id}}",
  "mailbox_id": {{box_id}},
  "template_id": {{template_id}},
  "list_id": {{list_id}}
}

[Captures]
broadcast_id = data.id

[Asserts]
status == 201
```

```step
@id broadcast-start
@name A starts the broadcast now

POST /v1/mail-broadcasts/{{broadcast_id}}/sends
Content-Type: application/json
Authorization: Bearer {{a_token}}
Organization-Id: {{org_id}}

{}

[Asserts]
status == 200
```

```step
@id broadcast-work
@name The broadcast worker sends the recipient once
@type exec

: "${FASTBUILD_TEST_POSTGRES_DSN:?export FASTBUILD_TEST_POSTGRES_DSN (the disposable test database)}"
eval "$(python3 - "$FASTBUILD_TEST_POSTGRES_DSN" <<'PY'
import shlex, sys
from urllib.parse import parse_qs, unquote, urlparse
v = urlparse(sys.argv[1])
pairs = {"DB_HOST": v.hostname, "DB_PORT": v.port or 5432, "DB_NAME": v.path.strip("/"),
         "DB_USERNAME": unquote(v.username or ""), "DB_PASSWORD": unquote(v.password or ""),
         "DB_SSLMODE": parse_qs(v.query).get("sslmode", ["disable"])[0]}
for key, value in pairs.items():
    print(f"export {key}={shlex.quote(str(value))}")
PY
)"
set -a
. tests/kest/operator_broadcast_content.kestenv
set +a
APP_ENV=test DB_ENABLED=true DB_DRIVER=postgres MAILBOX_PROVIDER=log AI_ENABLED=false TRACING_ENABLED=false \
  tmp/kest/fastbuild mail:broadcast:work --once >/dev/null
sent=$(psql "$FASTBUILD_TEST_POSTGRES_DSN" -v ON_ERROR_STOP=1 -qtAc \
  "SELECT count(*) FROM mail_broadcast_recipients WHERE broadcast_id = {{broadcast_id}} AND message_id IS NOT NULL")
[ "$sent" = "1" ] || { echo "broadcast recipient not sent: $sent" >&2; exit 1; }
```

```step
@id register-v
@name Register metrics viewer V
@retry 2

POST /v1/register
Content-Type: application/json

{
  "username": "bc_v_{{run_id}}",
  "email": "bc_v_{{run_id}}@example.com",
  "password": "password123",
  "nickname": "Viewer V"
}

[Asserts]
status == 201
```

```step
@id register-r
@name Register content reader R
@retry 2

POST /v1/register
Content-Type: application/json

{
  "username": "bc_r_{{run_id}}",
  "email": "bc_r_{{run_id}}@example.com",
  "password": "password123",
  "nickname": "Reader R"
}

[Asserts]
status == 201
```

```step
@id grants
@name Grant V mail_metrics and R mail_metrics with the content add-on
@type exec

: "${FASTBUILD_TEST_POSTGRES_DSN:?export FASTBUILD_TEST_POSTGRES_DSN (the disposable test database)}"
granted=$(psql "$FASTBUILD_TEST_POSTGRES_DSN" -v ON_ERROR_STOP=1 -qtAc \
  "INSERT INTO platform_operators (user_id, granted_at, scope, mail_content)
   SELECT id, now(), 'mail_metrics', email LIKE 'bc_r_%' FROM users
   WHERE email IN ('bc_v_{{run_id}}@example.com', 'bc_r_{{run_id}}@example.com')
   RETURNING mail_content" | sort | tr '\n' ' ')
[ "$granted" = "f t " ] || { echo "grant failed: $granted" >&2; exit 1; }
```

```step
@id install-helper
@name V and R sign in; install the cookie-carrying helper
@type exec

dir="tmp/kest/operator-content-{{run_id}}"
mkdir -p "$dir"
base_url=${KEST_API_BASE_URL:-$(awk '/^  local:/{f=1} f && /base_url:/{print $2; exit}' .kest/config.yaml)}
cat > "$dir/op.py" <<'PY'
"""usage: op.py <dir> <base_url> signin <who> <email>
       op.py <dir> <base_url> <who> <METHOD> <path> <status> [json-body|-] [field=value ...]"""
import json, subprocess, sys
d, base = sys.argv[1], sys.argv[2]
origin = "http://admin.localhost"
def curl(args):
    return subprocess.run(["curl", "-sS"] + args, check=True, capture_output=True, text=True).stdout
if sys.argv[3] == "signin":
    who, email = sys.argv[4], sys.argv[5]
    out = curl(["-D", f"{d}/{who}.headers", "-X", "POST", f"{base}/v1/operator/session",
                "-H", "Content-Type: application/json", "-H", f"Origin: {origin}",
                "--data", json.dumps({"identifier": email, "password": "password123"})])
    session = json.loads(out)["data"]
    cookie = next(line.split(":", 1)[1].strip().split(";")[0] for line in open(f"{d}/{who}.headers")
                  if line.lower().startswith("set-cookie:"))
    json.dump({"cookie": cookie, "csrf": session["csrf_token"]}, open(f"{d}/{who}.json", "w"))
    print(" ".join(session["capabilities"]))
    sys.exit(0)
who, method, path, want = sys.argv[3:7]
body = sys.argv[7] if len(sys.argv) > 7 else "-"
session = json.load(open(f"{d}/{who}.json"))
args = ["-o", f"{d}/last.json", "-w", "%{http_code}", "-X", method, f"{base}{path}",
        "-H", f"Cookie: {session['cookie']}", "-D", f"{d}/last.headers"]
if method != "GET":
    args += ["-H", f"Origin: {origin}", "-H", f"X-CSRF-Token: {session['csrf']}",
             "-H", "Content-Type: application/json", "--data", body if body != "-" else "{}"]
code = curl(args)
result = json.load(open(f"{d}/last.json"))
if code != want:
    sys.exit(f"{method} {path}: status {code}, want {want}: {result}")
if "no-store" not in open(f"{d}/last.headers").read().lower():
    sys.exit("operator response is cacheable")
for check in sys.argv[8:]:
    field, expected = check.split("=", 1)
    value = result
    for part in field.split("."):
        value = value[int(part)] if isinstance(value, list) else value[part]
    if str(value) != expected:
        sys.exit(f"{field} = {value!r}, want {expected!r}")
PY
caps=$(python3 "$dir/op.py" "$dir" "$base_url" signin v bc_v_{{run_id}}@example.com)
[ "$caps" = "mail_metrics:read" ] || { echo "V capabilities: $caps" >&2; exit 1; }
caps=$(python3 "$dir/op.py" "$dir" "$base_url" signin r bc_r_{{run_id}}@example.com)
[ "$caps" = "mail_metrics:read mail_content:read" ] || { echo "R capabilities: $caps" >&2; exit 1; }
printf '#!/bin/sh\nexec python3 %s/op.py %s %s "$@"\n' "$dir" "$dir" "$base_url" > "$dir/op.sh"
chmod +x "$dir/op.sh"
```

## Who cannot read a body

```step
@id owner-sign-in
@name The organization owner gets no operator session from the role

POST /v1/operator/session
Content-Type: application/json
Origin: http://admin.localhost

{
  "identifier": "bc_a_{{run_id}}@example.com",
  "password": "password123"
}

[Asserts]
status == 403
body.error_code == "OPERATOR.FORBIDDEN"
```

```step
@id bearer-read
@name A public bearer session cannot read a body

POST /v1/operator/outbound-messages/{{org_id}}/{{ordinary_id}}/content
Content-Type: application/json
Authorization: Bearer {{a_token}}
Origin: http://admin.localhost

{
  "reason": "Owner tries the operator route"
}

[Asserts]
status == 401
```

```step
@id log-marks-broadcast
@name The outbound log marks only the broadcast message
@type exec

d="tmp/kest/operator-content-{{run_id}}"
"$d/op.sh" v GET "/v1/operator/outbound-messages?organization_id={{org_id}}" 200 - \
  meta.total=2 data.0.subject=broadcast-{{run_id}} data.0.broadcast_id={{broadcast_id}} \
  data.1.id={{ordinary_id}} data.1.broadcast_id=None
```

```step
@id metrics-only-read
@name V (metrics only) cannot read the broadcast body
@type exec

d="tmp/kest/operator-content-{{run_id}}"
id=$(psql "$FASTBUILD_TEST_POSTGRES_DSN" -qtAc "SELECT message_id FROM mail_broadcast_recipients WHERE broadcast_id = {{broadcast_id}}")
"$d/op.sh" v POST "/v1/operator/outbound-messages/{{org_id}}/$id/content" 403 '{"reason":"Complaint 1"}' \
  error_code=OPERATOR.CAPABILITY_REQUIRED
"$d/op.sh" v GET "/v1/operator/content-access-audits" 403 - error_code=OPERATOR.CAPABILITY_REQUIRED
```

## The content grant reads, and leaves a record

```step
@id reader-read
@name R reads the broadcast text with a reason
@type exec

d="tmp/kest/operator-content-{{run_id}}"
id=$(psql "$FASTBUILD_TEST_POSTGRES_DSN" -qtAc "SELECT message_id FROM mail_broadcast_recipients WHERE broadcast_id = {{broadcast_id}}")
"$d/op.sh" r POST "/v1/operator/outbound-messages/{{org_id}}/$id/content" 200 '{"reason":"Complaint ticket {{run_id}}"}' \
  data.broadcast_id={{broadcast_id}} data.subject=broadcast-{{run_id}} data.text_source=text \
  "data.text=Spring news {{run_id}}"
python3 - "$d/last.json" <<'PY'
import json, sys
data = json.load(open(sys.argv[1]))["data"]
for leaked in ("html", "html_body", "to_addresses", "recipients", "attachments"):
    assert leaked not in data, leaked
PY
```

```step
@id reader-audit
@name The access record names R, O, the message, and the reason, and nothing else
@type exec

d="tmp/kest/operator-content-{{run_id}}"
"$d/op.sh" r GET "/v1/operator/content-access-audits?organization_id={{org_id}}" 200 - \
  meta.total=1 "data.0.reason=Complaint ticket {{run_id}}" "data.0.organization_name=Content {{run_id}}"
row=$(psql "$FASTBUILD_TEST_POSTGRES_DSN" -v ON_ERROR_STOP=1 -qtAc \
  "SELECT row_to_json(a)::text FROM mail_content_access_audits a WHERE organization_id = {{org_id}}")
case "$row" in *Spring*|*@example.com*|*broadcast-*) echo "audit row holds content: $row" >&2; exit 1 ;; esac
```

```step
@id reader-refusals
@name Ordinary outbound and a mismatched organization are refused without a record
@type exec

d="tmp/kest/operator-content-{{run_id}}"
id=$(psql "$FASTBUILD_TEST_POSTGRES_DSN" -qtAc "SELECT message_id FROM mail_broadcast_recipients WHERE broadcast_id = {{broadcast_id}}")
"$d/op.sh" r POST "/v1/operator/outbound-messages/{{org_id}}/{{ordinary_id}}/content" 403 '{"reason":"Complaint"}' \
  error_code=OPERATOR.CONTENT_NOT_ELIGIBLE
other=$((({{org_id}}) + 1000000))
"$d/op.sh" r POST "/v1/operator/outbound-messages/$other/$id/content" 404 '{"reason":"Complaint"}' \
  error_code=MAILBOX.MESSAGE_NOT_FOUND
"$d/op.sh" r POST "/v1/operator/outbound-messages/{{org_id}}/$id/content" 422 '{"reason":"   "}' \
  error_code=COMMON.VALIDATION_FAILED
"$d/op.sh" r GET "/v1/operator/content-access-audits?organization_id={{org_id}}" 200 - meta.total=1
```

## Removing content fails the next read

```step
@id remove-content
@name Re-grant R without the content add-on
@type exec

changed=$(psql "$FASTBUILD_TEST_POSTGRES_DSN" -v ON_ERROR_STOP=1 -qtAc \
  "UPDATE platform_operators SET mail_content = false
   WHERE user_id = (SELECT id FROM users WHERE email = 'bc_r_{{run_id}}@example.com') RETURNING mail_content")
[ "$changed" = "f" ] || { echo "update failed: $changed" >&2; exit 1; }
```

```step
@id reader-after-removal
@name R's next read fails on the same session
@type exec

d="tmp/kest/operator-content-{{run_id}}"
id=$(psql "$FASTBUILD_TEST_POSTGRES_DSN" -qtAc "SELECT message_id FROM mail_broadcast_recipients WHERE broadcast_id = {{broadcast_id}}")
"$d/op.sh" r POST "/v1/operator/outbound-messages/{{org_id}}/$id/content" 403 '{"reason":"Complaint 2"}' \
  error_code=OPERATOR.CAPABILITY_REQUIRED
```
