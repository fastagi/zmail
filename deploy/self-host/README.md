# Self-Hosting Zmail

Runs Zmail on one Linux server with Docker Compose: PostgreSQL, the API, the webhook and
broadcast workers, and the Web console. A reverse proxy on the host terminates TLS.

## Install

1. Point a host name at the server, for example `zmail.example.com`.
2. Create the configuration once:

   ```bash
   ./init-env.sh https://zmail.example.com
   ```

   It writes `.env` (mode 0600) with random secrets and `postgres-tls/` with the database
   certificate. Then edit `.env`: set `MAILBOX_DOMAIN`, `MAILBOX_CLOUDFLARE_ACCOUNT_ID`, and
   `MAILBOX_CLOUDFLARE_API_TOKEN` (a token with Email Sending permission).
3. Build and start:

   ```bash
   docker compose build
   docker compose up -d --wait
   ```

   Every `up` runs the `migrate` job before the API starts.
4. Add the site from `Caddyfile.example` to the host's Caddy, run `caddy validate`, and reload
   it. Some older Caddy builds (2.6.2 among them) can crash during a reload and take every site
   on the host down; if `systemctl reload caddy` fails, run `systemctl restart caddy` at once
   and check the other sites.
5. Create the first account and its organization. Public sign-up stays closed
   (`AUTH_REGISTRATION_ENABLED=false`):

   ```bash
   ./create-user.sh owner@example.com --organization "Acme"
   ```

6. Check the instance from outside:

   ```bash
   ./smoke.sh https://zmail.example.com
   ```

## Mail Edge

Inbound mail and delivery events arrive from the Cloudflare Email Worker in
[`deploy/cloudflare/email-worker`](../cloudflare/email-worker/README.md). Set its secret
`MAILBOX_INBOUND_SECRET` to the value in `.env`, and its `MAILBOX_INBOUND_URL` to
`https://<host>/v1/inbound-mail-messages`.

## Organization Domains

To let organizations connect their own domains, set in `.env`:

- `MAILBOX_CUSTOM_DOMAINS_ENABLED=true`
- `MAILBOX_CLOUDFLARE_EMAIL_WORKER` to the name of the Email Worker above.
- A `MAILBOX_CLOUDFLARE_API_TOKEN` that also has Zone Read, DNS Edit, Zone Settings Edit,
  Email Routing Rules Edit, and Email Sending Edit.

Then run `docker compose up -d --wait` again. Any owner or admin on the instance can connect any
domain the token can reach, so scope the token to the zones organizations may use. See the Domains
section of [`contracts/MAILBOXES.md`](../../contracts/MAILBOXES.md).

## Backups

`backup.sh` writes a `pg_dump` archive of the database, checked with `pg_restore --list`, and a
tarball of `.env` and `postgres-tls/` to `/opt/zmail/backups` (root only), and deletes files older
than 14 days. Schedule it once a day:

```sh
(crontab -l 2>/dev/null; echo "30 19 * * * $PWD/backup.sh >> /var/log/zmail-backup.log 2>&1") | crontab -
```

`ZMAIL_BACKUP_DIR` and `ZMAIL_BACKUP_KEEP_DAYS` change the place and the retention. Copy the files
off the server too: a backup on the same disk does not survive the disk.

To restore, unpack the secrets archive into this directory, start only `postgres`, and load the
dump:

```sh
tar -xzf /opt/zmail/backups/zmail-secrets-<time>.tar.gz
docker compose up -d --wait postgres
docker compose exec -T postgres pg_restore -U zmail -d zmail --clean --if-exists < /opt/zmail/backups/zmail-<time>.dump
docker compose up -d --wait
```

## Secrets

`.env` and `postgres-tls/` cannot be regenerated: back them up together with the database. A new
`MAILBOX_INBOUND_SECRET` must also be set on the Email Worker, and changing
`MAILBOX_INBOUND_SECRET` invalidates every unsubscribe link already sent.

## Database Roles

`zmail` is the image's bootstrap superuser, for administration only. The `migrate` job connects as
`zmail_owner`, which owns the schema. The API and both workers connect as `zmail_app`, which owns
nothing and can only read and write rows.

## Upgrade

```bash
git pull
docker compose build
docker compose up -d --wait
```
