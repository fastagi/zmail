#!/bin/sh
# Creates a Zmail account while public sign-up stays closed, and optionally an organization
# that the account owns. This is how a fresh instance gets its first owner, and how later
# teammates get an account before they are invited to an organization.
#
#   ./create-user.sh owner@example.com --organization "Acme"
#   ./create-user.sh teammate@example.com --name "Sam Doe"
#
# The password is asked for on the terminal (not echoed), or read from the first line of
# --password-file for automation. It is never a command-line argument and never printed.
#
# How it works: a second API process is started inside the private Compose network with
# registration enabled. It publishes no port and has no network alias, so neither the reverse
# proxy nor the console can reach it; only this script does, from inside the console container.
# The process is removed when the script ends, however it ends (normally, on an error, or on
# SIGINT, SIGTERM, or SIGHUP), and a leftover one from an earlier run that was killed outright is
# removed at the start. Run one copy of the script at a time. The serving API keeps registration
# closed throughout.
set -eu

PROGRAM="create-user.sh"
dir="$(cd "$(dirname "$0")" && pwd)"
# Marks the temporary registration-open API container, so a leftover one can be found and removed.
bootstrap_label="io.zmail.self-host.create-user=true"

saved_tty=""
bootstrap=""
# Idempotent: runs on EXIT and again when a signal handler exits.
cleanup() {
	if [ -n "$saved_tty" ]; then
		stty "$saved_tty" 2>/dev/null || true
		saved_tty=""
	fi
	if [ -n "$bootstrap" ]; then
		docker rm --force "$bootstrap" >/dev/null 2>&1 || true
		bootstrap=""
	fi
}
# A shell that receives a signal does not run the EXIT trap by itself (dash does not on SIGHUP), and
# without an explicit exit it would continue after the handler: each signal cleans up, then stops.
trap cleanup EXIT
trap 'cleanup; exit 129' HUP
trap 'cleanup; exit 130' INT
trap 'cleanup; exit 143' TERM

die() {
	echo "$PROGRAM: $*" >&2
	exit 1
}

usage() {
	echo "usage: $PROGRAM <email> [--name <display name>] [--organization <name>] [--password-file <file>]" >&2
	exit 2
}

compose() {
	docker compose --project-directory "$dir" --file "$dir/compose.yaml" "$@"
}

email=""
name=""
organization=""
password_file=""
while [ "$#" -gt 0 ]; do
	case "$1" in
	--name)
		[ "$#" -ge 2 ] || usage
		name="$2"
		shift 2
		;;
	--organization)
		[ "$#" -ge 2 ] || usage
		organization="$2"
		shift 2
		;;
	--password-file)
		[ "$#" -ge 2 ] || usage
		password_file="$2"
		shift 2
		;;
	-h | --help) usage ;;
	-*) usage ;;
	*)
		[ -z "$email" ] || usage
		email="$1"
		shift
		;;
	esac
done
[ -n "$email" ] || usage

one_line() {
	case "$2" in
	*"
"*) die "$1 must be a single line" ;;
	esac
}
one_line "the email" "$email"
one_line "the name" "$name"
one_line "the organization name" "$organization"
printf '%s' "$email" | grep -Eq '^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$' || die "not an email address: $email"
[ "${#email}" -le 100 ] || die "the email must be at most 100 characters"
[ -n "$name" ] || name="${email%%@*}"
[ "${#name}" -le 50 ] || die "the name must be at most 50 characters"
if [ -n "$organization" ]; then
	[ "${#organization}" -ge 2 ] && [ "${#organization}" -le 100 ] || die "the organization name must be 2 to 100 characters"
fi

command -v docker >/dev/null 2>&1 || die "docker is required"
[ -f "$dir/.env" ] || die "$dir/.env is missing: run ./init-env.sh first"
[ -n "$(compose ps --quiet --status running web 2>/dev/null)" ] || die "the stack is not running: docker compose up -d"

if [ -n "$password_file" ]; then
	[ -r "$password_file" ] || die "cannot read $password_file"
	IFS= read -r password <"$password_file" || [ -n "$password" ] || die "$password_file is empty"
elif [ -t 0 ]; then
	saved_tty="$(stty -g)"
	stty -echo
	printf 'Password for %s (8 to 50 characters, not echoed): ' "$email" >&2
	IFS= read -r password
	printf '\nRepeat the password: ' >&2
	IFS= read -r password_again
	printf '\n' >&2
	stty "$saved_tty"
	saved_tty=""
	[ "$password" = "$password_again" ] || die "the two passwords differ"
	unset password_again
else
	die "no terminal to ask for the password: pass --password-file <file>"
fi
[ "${#password}" -ge 8 ] && [ "${#password}" -le 50 ] || die "the password must be 8 to 50 characters"

# A run killed with SIGKILL (or a lost machine) cannot clean up: remove what it left behind.
stale="$(docker ps --all --quiet --filter "label=$bootstrap_label")"
if [ -n "$stale" ]; then
	echo "$PROGRAM: removing a leftover account-creation API process from an earlier run" >&2
	# shellcheck disable=SC2086 # one container ID per word
	docker rm --force $stale >/dev/null
fi

bootstrap="zmail-create-user-$$"
compose run --detach --rm --no-deps --name "$bootstrap" --label "$bootstrap_label" \
	--env AUTH_REGISTRATION_ENABLED=true --env AUTH_REGISTRATION_EMAIL_CODE_REQUIRED=false api >/dev/null ||
	die "could not start the account-creation API process"

ready=0
attempt=0
while [ "$attempt" -lt 60 ]; do
	if compose exec -T --env BOOTSTRAP_API="http://$bootstrap:8025" web node -e \
		'fetch(process.env.BOOTSTRAP_API + "/health/ready").then((r) => process.exit(r.ok ? 0 : 1)).catch(() => process.exit(1))' \
		>/dev/null 2>&1; then
		ready=1
		break
	fi
	attempt=$((attempt + 1))
	sleep 1
done
[ "$ready" -eq 1 ] || die "the account-creation API process did not become ready"

# Runs inside the console container. Input arrives on stdin, one value per line, so the password
# is in no argument list and no environment.
script='
const [email, name, organization, password] = require("node:fs").readFileSync(0, "utf8").split("\n");
const base = process.env.BOOTSTRAP_API;
const fail = (message) => { console.error(message); process.exit(1); };
async function post(path, body, token) {
  const headers = { "content-type": "application/json" };
  if (token) headers.authorization = "Bearer " + token;
  const response = await fetch(base + path, { method: "POST", headers, body: JSON.stringify(body) });
  let payload = null;
  try { payload = await response.json(); } catch { /* an empty or non-JSON body */ }
  return { status: response.status, payload };
}
(async () => {
  const username = "user_" + require("node:crypto").randomBytes(8).toString("hex");
  const created = await post("/v1/register", { username, email, password, nickname: name });
  if (created.status === 409) fail("an account with this email already exists; nothing was changed");
  if (created.status !== 201) {
    fail("the account was not created: HTTP " + created.status + " " + (created.payload?.error_code ?? ""));
  }
  console.log("created the account " + email);
  if (!organization) return;
  const login = await post("/v1/login", { username: email, password });
  const token = login.payload?.data?.access_token;
  if (login.status !== 200 || !token) {
    fail("the account exists, but signing in to create the organization failed: HTTP " + login.status);
  }
  const org = await post("/v1/organizations", { name: organization }, token);
  await post("/v1/logout", {}, token);
  if (org.status !== 201) {
    fail("the account exists, but the organization was not created: HTTP " + org.status + " " +
      (org.payload?.error_code ?? "") + "; create it in the console after signing in");
  }
  console.log("created the organization \"" + org.payload.data.name + "\" (" + org.payload.data.slug +
    "); " + email + " is its owner");
})().catch((error) => fail("unexpected failure: " + error.message));
'

printf '%s\n%s\n%s\n%s' "$email" "$name" "$organization" "$password" |
	compose exec -T --env BOOTSTRAP_API="http://$bootstrap:8025" web node -e "$script"
