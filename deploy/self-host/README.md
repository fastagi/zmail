# Self-Hosting Zmail

Runs Zmail on one Linux server with Docker Compose: PostgreSQL, the API, the webhook, broadcast,
and mail zone workers (plus the mail connector worker once connectors are on), and the Web console.
A reverse proxy on the host terminates TLS.

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
- A `MAILBOX_CLOUDFLARE_API_TOKEN` that also has **Account → Zone: Edit**, DNS Edit, Zone
  Settings Edit, Email Routing Rules Edit, and Email Sending Edit, with zone resources set to all
  zones of the account.
- Optionally `MAILBOX_MAX_ZONES_PER_ORGANIZATION`, the managed zones one organization may create
  (default 3, at most 20).

Then run `docker compose up -d --wait` again.

A domain whose DNS is not in the Cloudflare account yet gets a managed zone: Zmail adds the apex to
the account, imports its records, and shows the organization the two Cloudflare name servers to set
at its registrar. The `mail-zone-worker` service (`mail:zone:work --batch=100 --poll=10m`) checks
pending zones every 10 minutes, sets their domains up once Cloudflare sees the delegation, and
removes zones still pending after 14 days. Zone: Edit is what lets Zmail create and delete those
zones, and a token scoped to named zones cannot reach the new ones.

Any owner or admin on the instance can also connect any domain in a zone the operator added to the
account, so keep zones organizations must not use in another Cloudflare account. See the Domains
and Managed Zones sections of [`contracts/MAILBOXES.md`](../../contracts/MAILBOXES.md), and
[`docs/runbooks/self-serve-domain-acceptance.md`](../../docs/runbooks/self-serve-domain-acceptance.md)
for a real end-to-end check, including the steps for installs that predate managed zones.

## Mail Connectors

Members can bind their own Gmail, Outlook, or IMAP accounts read-only (External Mailboxes in
[`contracts/MAILBOXES.md`](../../contracts/MAILBOXES.md)). It is off by default. To turn it on, set
in `.env`:

- `EXTERNAL_MAIL_PROVIDERS`, a comma list of `gmail`, `outlook`, and `imap`.
- `MAILBOX_SECRET_ENCRYPTION_KEY`, at least 32 characters (for example `openssl rand -hex 32`). It
  seals the stored grants and passwords: back it up with `.env` and never change it while
  connections exist.
- For `gmail` or `outlook`: `EXTERNAL_MAIL_REDIRECT_URL` set to
  `https://<host>/console/mailboxes/connected/callback`, and the provider's OAuth client
  (`EXTERNAL_MAIL_GMAIL_CLIENT_ID` and `EXTERNAL_MAIL_GMAIL_CLIENT_SECRET`, or
  `EXTERNAL_MAIL_OUTLOOK_CLIENT_ID`, `EXTERNAL_MAIL_OUTLOOK_CLIENT_SECRET`, and optionally
  `EXTERNAL_MAIL_OUTLOOK_TENANT`).
- `COMPOSE_PROFILES=external-mail`, which starts the `external-mail-worker` service
  (`mail:external:sync --batch=20 --poll=30s`). Without it accounts bind but are never synced.

Then run `docker compose up -d --wait` again. Copies of a disconnected account are deleted by
`broadcast-worker` once `EXTERNAL_MAIL_REVOKED_RETENTION` (default `720h`) has passed, whether or
not connectors are on.

## Sign in with Google

To offer "Continue with Google" on the sign-in and sign-up pages, create an OAuth client of type
"Web application" in Google Cloud Console, add `https://<host>/api/auth/google/callback` as an
authorized redirect URI, and set `GOOGLE_OAUTH_CLIENT_ID` and `GOOGLE_OAUTH_CLIENT_SECRET` in
`.env`. With registration closed (the default here), only people who already have an account (same
email) can use it. See "Sign in with Google" in
[`contracts/AUTHENTICATION.md`](../../contracts/AUTHENTICATION.md).

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
`zmail_owner`, which owns the schema. The API and the workers connect as `zmail_app`, which owns
nothing and can only read and write rows.

## Upgrade

```bash
git pull
docker compose build
docker compose up -d --wait
```
