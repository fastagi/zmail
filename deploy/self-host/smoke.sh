#!/bin/sh
# Checks a running Zmail instance from outside, through its public origin:
#
#   ./smoke.sh https://zmail.example.com
#
# It verifies the proxy routing, readiness, the console login page, that the API refuses
# unauthenticated calls and unsigned mail-edge requests, that public sign-up is closed, and that
# the well-known development credentials do not sign in. It changes nothing on the instance.
# Exit status: 0 when every check passes, 1 otherwise.
set -u

[ "$#" -eq 1 ] || {
	echo "usage: smoke.sh <public origin>, for example https://zmail.example.com" >&2
	exit 2
}
origin="${1%/}"
command -v curl >/dev/null 2>&1 || {
	echo "smoke.sh: curl is required" >&2
	exit 2
}

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
failures=0

pass() { echo "ok    $*"; }
fail() {
	echo "FAIL  $*"
	failures=$((failures + 1))
}

# request <output file> <curl arguments...>: prints the HTTP status, 000 when the request failed.
request() {
	output="$1"
	shift
	# shellcheck disable=SC2086 # SMOKE_CURL_OPTS is a list of options by design.
	curl --silent --show-error --max-time 30 ${SMOKE_CURL_OPTS:-} --output "$output" --write-out '%{http_code}' "$@" 2>"$work/curl.err" || true
}

expect_status() {
	label="$1"
	wanted="$2"
	shift 2
	status="$(request "$work/body" "$@")"
	case " $wanted " in
	*" $status "*) pass "$label (HTTP $status)" ;;
	*) fail "$label: HTTP $status, expected $wanted $(head -c 200 "$work/curl.err" 2>/dev/null)" ;;
	esac
}

expect_status "API readiness through the proxy: GET /health/ready" "200" "$origin/health/ready"

status="$(request "$work/login.html" "$origin/login")"
if [ "$status" = "200" ] && grep -qi '<html' "$work/login.html"; then
	pass "console login page: GET /login (HTTP 200, HTML)"
else
	fail "console login page: GET /login returned HTTP $status or no HTML"
fi

expect_status "API refuses an anonymous mailbox listing: GET /v1/mailboxes" "401" "$origin/v1/mailboxes"
expect_status "API refuses an unsigned inbound message: POST /v1/inbound-mail-messages" "401 503" \
	-X POST -H 'Content-Type: message/rfc822' --data-binary 'Subject: smoke' "$origin/v1/inbound-mail-messages"
expect_status "API refuses an invalid unsubscribe token: POST /v1/mail-unsubscribes/x" "404" \
	-X POST "$origin/v1/mail-unsubscribes/1.1.AAAAAAAAAAAAAAAAAAAAAA"
expect_status "public sign-up is closed: POST /v1/register" "403" \
	-X POST -H 'Content-Type: application/json' \
	--data '{"username":"smoke_probe","email":"smoke-probe@example.com","password":"smoke-probe-1"}' \
	"$origin/v1/register"
expect_status "the development account does not sign in: POST /v1/login" "401 403 429" \
	-X POST -H 'Content-Type: application/json' \
	--data '{"username":"admin@example.com","password":"secret"}' "$origin/v1/login"

if [ "$failures" -eq 0 ]; then
	echo "all checks passed"
	exit 0
fi
echo "$failures check(s) failed"
exit 1
