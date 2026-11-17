#!/bin/sh
# Backs up a running Zmail bundle: the database as a pg_dump custom-format archive, and the files
# that cannot be regenerated (.env and postgres-tls/). Run it from cron once a day:
#
#   30 3 * * * /opt/zmail/src/deploy/self-host/backup.sh >> /var/log/zmail-backup.log 2>&1
#
# Backups go to ZMAIL_BACKUP_DIR (default /opt/zmail/backups) and are kept for
# ZMAIL_BACKUP_KEEP_DAYS days (default 14). The secrets archive holds every password and key of
# the instance, so the directory is private to root; copy both files off the server as well.
#
# Restore into a fresh bundle (see README.md, "Backups"):
#   docker compose exec -T postgres pg_restore -U zmail -d zmail --clean --if-exists < zmail-<time>.dump

set -eu

here=$(cd "$(dirname "$0")" && pwd)
backup_dir=${ZMAIL_BACKUP_DIR:-/opt/zmail/backups}
keep_days=${ZMAIL_BACKUP_KEEP_DAYS:-14}
stamp=$(date -u +%Y%m%dT%H%M%SZ)

umask 077
mkdir -p "$backup_dir"
chmod 700 "$backup_dir"

dump="$backup_dir/zmail-$stamp.dump"
secrets="$backup_dir/zmail-secrets-$stamp.tar.gz"
partial="$dump.partial"
trap 'rm -f "$partial"' EXIT

cd "$here"
docker compose exec -T postgres pg_dump -U zmail -d zmail --format=custom --compress=6 > "$partial"

# A dump that pg_restore cannot list is not a backup.
tables=$(docker compose exec -T postgres pg_restore --list < "$partial" | grep -c ' TABLE ' || true)
if [ "$tables" -lt 1 ]; then
	echo "$(date -u +%FT%TZ) backup FAILED: the dump lists no tables" >&2
	exit 1
fi
mv "$partial" "$dump"
tar -czf "$secrets" -C "$here" .env postgres-tls

find "$backup_dir" -maxdepth 1 -type f \( -name 'zmail-*.dump' -o -name 'zmail-secrets-*.tar.gz' \) \
	-mtime +"$keep_days" -delete

size=$(du -h "$dump" | cut -f1)
echo "$(date -u +%FT%TZ) backup ok: $dump ($size, $tables tables) and $(basename "$secrets")"
