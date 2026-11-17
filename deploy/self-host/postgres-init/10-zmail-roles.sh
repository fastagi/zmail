#!/bin/sh
# Runs once, when the postgres image initializes an empty data volume (/docker-entrypoint-initdb.d):
# creates the schema owner role and the API's runtime role. See zmail-roles.psql.
#
# The passwords arrive in the container environment from .env (POSTGRES_OWNER_PASSWORD and
# POSTGRES_APP_PASSWORD) and reach psql on stdin, never on a command line.
set -eu

: "${POSTGRES_OWNER_PASSWORD:?missing: run ./init-env.sh, or ./upgrade-db-roles.sh for an existing .env}"
: "${POSTGRES_APP_PASSWORD:?missing: run ./init-env.sh, or ./upgrade-db-roles.sh for an existing .env}"

for value in "$POSTGRES_OWNER_PASSWORD" "$POSTGRES_APP_PASSWORD"; do
	# init-env.sh generates hex; anything else could break out of the psql variable.
	printf '%s' "$value" | grep -Eq '^[0-9a-f]{32,128}$' || {
		echo "10-zmail-roles.sh: a database role password in .env is not 32 to 128 lowercase hex characters" >&2
		exit 1
	}
done

{
	printf '\\set owner_password %s\n' "$POSTGRES_OWNER_PASSWORD"
	printf '\\set app_password %s\n' "$POSTGRES_APP_PASSWORD"
	cat /docker-entrypoint-initdb.d/zmail-roles.psql
} | psql -X -q -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB"

echo "10-zmail-roles.sh: created zmail_owner (schema owner, migrations) and zmail_app (API runtime)"
