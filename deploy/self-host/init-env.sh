#!/bin/sh
# Creates deploy/self-host/.env (mode 0600) and the database TLS pair, once.
#
#   ./init-env.sh https://zmail.example.com
#
# The one argument is the public origin: the https address browsers and hosts use, exactly as the
# reverse proxy serves it (scheme, host, optional port, nothing after it). Every secret is drawn
# from the system CSPRNG through `openssl rand`; none is printed. An existing .env is never
# overwritten: delete it yourself only when you mean to throw the instance away, because the
# values in it cannot be regenerated (see README.md in this directory, "Secrets").
set -eu

die() {
	echo "init-env.sh: $*" >&2
	exit 1
}

[ "$#" -eq 1 ] || die "usage: init-env.sh <public origin>, for example https://zmail.example.com"
origin="$1"

command -v openssl >/dev/null 2>&1 || die "openssl is required"

# Production mode needs https (the session cookie is Secure and carries the __Host- prefix) and
# refuses loopback origins.
if ! printf '%s' "$origin" | grep -Eq '^https://[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:[0-9]{1,5})?$'; then
	die "the public origin must look like https://zmail.example.com (https, a host name, an optional port, no path or trailing slash): $origin"
fi
case "$origin" in
*localhost* | *127.0.0.1*) die "the public origin must not be a loopback address: production mode refuses it" ;;
esac

dir="$(cd "$(dirname "$0")" && pwd)"
env_file="$dir/.env"
tls_dir="$dir/postgres-tls"

[ ! -e "$env_file" ] || die "$env_file already exists; it is never overwritten"
[ ! -e "$tls_dir" ] || die "$tls_dir already exists without a .env; move it away or restore the .env that belongs to it"

umask 077

random_hex() {
	value="$(openssl rand -hex "$1")" || die "openssl rand failed"
	[ "${#value}" -eq $(($1 * 2)) ] || die "openssl rand returned an unexpected value"
	printf '%s' "$value"
}

postgres_password="$(random_hex 24)"
postgres_owner_password="$(random_hex 24)"
postgres_app_password="$(random_hex 24)"
session_secret="$(random_hex 32)"
webhook_key="$(random_hex 32)"
inbound_secret="$(random_hex 32)"

# A self-signed pair for the database listener. The API connects with sslmode=require inside the
# private Compose network: the link is encrypted, the certificate is not verified.
mkdir "$tls_dir"
if ! openssl req -x509 -newkey rsa:2048 -nodes -days 3650 -subj "/CN=postgres" \
	-keyout "$tls_dir/server.key" -out "$tls_dir/server.crt" >/dev/null 2>&1; then
	rm -rf "$tls_dir"
	die "openssl could not create the database TLS pair"
fi
chmod 0600 "$tls_dir/server.key"
chmod 0644 "$tls_dir/server.crt"

tmp_file="$env_file.tmp.$$"
trap 'rm -f "$tmp_file"' EXIT
cat >"$tmp_file" <<EOF
# Zmail self-hosting configuration. Written by init-env.sh; keep it private and back it up
# together with postgres-tls/. Every variable is described in README.md in this directory.

ZMAIL_PUBLIC_ORIGIN=$origin

ZMAIL_WEB_PORT=18990
ZMAIL_API_PORT=18991

# The bootstrap superuser (administration only), the schema owner (the migrate job), and the
# API's runtime role. See README.md in this directory.
POSTGRES_PASSWORD=$postgres_password
POSTGRES_OWNER_PASSWORD=$postgres_owner_password
POSTGRES_APP_PASSWORD=$postgres_app_password
SESSION_SECRET=$session_secret
WEBHOOK_ENCRYPTION_KEY=$webhook_key

# Public sign-up stays closed: create accounts with ./create-user.sh.
AUTH_REGISTRATION_ENABLED=false

# Mail. The domain must be set up for Cloudflare Email Routing and Email Sending. Fill in the
# account id and a token with Email Sending permission before the first docker compose up.
MAILBOX_DOMAIN=mail.example.com
MAILBOX_CLOUDFLARE_ACCOUNT_ID=
MAILBOX_CLOUDFLARE_API_TOKEN=
# Shared with the Cloudflare Email Worker: set the Worker secret MAILBOX_INBOUND_SECRET to this value.
MAILBOX_INBOUND_SECRET=$inbound_secret
MAILBOX_DAILY_SEND_LIMIT=200
EOF
chmod 0600 "$tmp_file"
mv "$tmp_file" "$env_file"
trap - EXIT

echo "wrote $env_file (mode 0600) and $tls_dir/"
echo "public origin: $origin"
echo "next: set MAILBOX_DOMAIN and the two MAILBOX_CLOUDFLARE_* values in $env_file,"
echo "      then: docker compose build && docker compose up -d --wait   (run in $dir)"
