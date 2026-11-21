# Zmail on FE5-0083

Deployed on 2026-11-17 at **https://zmail.im** on `38.58.181.154`.

## Status and Scope

The current Cloudflare-based version is live. This is a fresh installation;
accounts and messages from the old instance were not migrated. The domain's
catch-all now targets the new instance. Addresses must exist in the new database
to receive mail; unknown recipients are rejected.

Multi-provider configuration (SMTP and other vendors' APIs) is a subsequent
product development phase, not a capability delivered by this deployment.
Self-service registration remains disabled. Domain management is enabled for the
scoped zmail.im zone; initial Email Routing DNS activation currently requires the
operator OAuth step described in deploy/brand/README.md.
The initial organization is `ZGI`, with owner and mailbox `owner@zmail.im`.

Initial credentials are in the local private file
`/Users/stark/.zgi-access/zmail-fe5-owner.json` (mode 0600), and in the server's
root-only `/opt/zmail/deploy/initial-owner.json`. Do not put their contents in Git.

## Runtime

- Host directory: `/opt/zmail`.
- Compose manifest: `/opt/zmail/deploy/compose.yaml`.
- Environment: `/opt/zmail/deploy/.env`, readable only by root.
- Services: API, Web, broadcast worker, webhook worker; migration job exited 0.
- Shared database: `zgi-shared-postgres`, PostgreSQL 18.6, database `zmail`.
- Database network: external `1panel-network`; no public database port.
- Migration role: `zmail_owner`, connection limit 2.
- Application role: `zmail_app`, connection limit 4.
- Both roles are non-superusers without database creation, role creation,
  replication, or row-security bypass privileges. Connections use TLS.
- Database connection limit: 6. API pool: 2; each worker pool: 1.
- Private application network: `10.213.78.0/24`.
- Web binding: `127.0.0.1:18990`; API binding: `127.0.0.1:18991`.
- Existing 1Panel OpenResty proxies the site; its active configuration is
  `/opt/1panel/www/conf.d/zmail.conf`.

The application never uses the PostgreSQL administrator account. Existing
shared services were not restarted. The separate Admin application is not deployed.

## DNS, TLS, and Mail

- Apex A: `zmail.im` → `38.58.181.154`, DNS only.
- HTTP redirects to HTTPS. Existing MX, SPF, DKIM, and DMARC records remain.
- Let's Encrypt certificate: `/etc/letsencrypt/live/zmail.im/`, initially
  expiring 2027-02-15. The existing Certbot timer handles renewal; deploy hook
  `/etc/letsencrypt/renewal-hooks/deploy/zmail-openresty.sh` copies certificates
  into OpenResty and reloads it after a configuration check.
- Sending provider: Cloudflare Email Sending; shared trial domain `send.zmail.im`.
- Official mailbox and system MAIL_FROM: `hello@notify.zmail.im`, with a localized
  Zmail team display name. Replies route to its real ZGI mailbox.
- notify.zmail.im, send.zmail.im and zmail.im are verified domain records owned by ZGI.
  The shared domain is listed once and remains usable across organizations.
- Active persistent token: `zmail-fe5-mail-domains`, account Email Sending Edit,
  plus zmail.im-only DNS Edit, Zone Read and Email Routing Rules Edit. Restricted
  to source IP `38.58.181.154`, no automatic expiry; saved in the root-only env.
  The previous sending-only credential is retained for rollback. Local OAuth was
  used once to enable subdomain routing DNS and was not copied to the server.
- Incoming catch-all: `zmail-fe5-email-worker` →
  `https://zmail.im/v1/inbound-mail-messages`, authenticated with HMAC.
- Previous catch-all configuration is saved locally at
  `/Users/stark/.zgi-access/zmail-fe5-previous-catch-all.json`.
- The old Worker and server were not deleted. They no longer receive the
  `zmail.im` catch-all traffic. Old data was not purged.
- Delivery events for apex, notify and send feed `zmail-fe5-delivery-events`,
  consumed by the new Worker. Five retries at 60-second delay lead to
  `zmail-fe5-delivery-dead-letter`. Existing unrelated queues remain unchanged.
  Provider acceptance and recipient-server delivery are distinct timeline events.

## Source and Images

Base commit: `fb3493afa4969a3cd82a9d0eeddcc128da9ef4ae`.

The deployed Web source adds the local dependency patch from Next.js and
`eslint-config-next` 16.4.1 to 16.4.5 with an updated lockfile. The original build
stopped with `SecurityFatalError`; 16.4.5 fixes
[GHSA-mfr8-8qww-v76h](https://github.com/vercel/next.js/security/advisories/GHSA-mfr8-8qww-v76h)
and [GHSA-vc7v-qv4w-wrv8](https://github.com/vercel/next.js/security/advisories/GHSA-vc7v-qv4w-wrv8).
This is not a claim that all transitive dependency audit findings are resolved.

- API image: `zmail-api:fb3493afa496-mailrouting`.
- API patch source: `/opt/zmail/releases/fb3493afa496-mailrouting/source/api`.
  This adds literal reply routes for promoted shared subdomains and suppresses
  duplicate shared-domain listing; regression tests cover scoped access and failures.
- Web image: `zmail-web:fb3493afa496-next1645`.
- Patched source: `/opt/zmail/releases/fb3493afa496-next1645/source`.
- Uploaded archive SHA-256:
  `81d06502800cd5f0cc7cb02c84906e603eae0c40edfaf44824fd2b94d6782daf`.
- Logs: `/opt/zmail/logs/`.

The dependency patch and this record are local changes, not committed or pushed.

## Verification

- Complete `make check` passed after the dependency patch and again after the
  shared-subdomain routing correction. API package tests and all 25 Worker tests passed.
- API and Web Docker images built on the target x86-64 host.
- API and Web containers are healthy; both workers are running.
- Public `/health/ready` and `/login`: HTTP 200; HTTP redirects with 301.
- Initial owner successfully signed in to the live browser console.
- Anonymous mailbox access: 401; valid registration request: 403 (disabled).
- Both database roles authenticated over TLS; privilege and pool limits checked.
- Cloudflare token verified active from the server.
- Unsigned inbound request: 401; signed synthetic message: 201; identical
  redelivery: 200 with `duplicate: true`. One clearly labelled synthetic message
  remains in the owner's mailbox. This bypasses SMTP and the edge Worker.
- Catch-all was read back after update and points to the new Worker.
- On 2026-11-17 at 19:16 UTC, an explicitly requested test from
  `owner@zmail.im` to `xishudev@gmail.com` returned HTTP 201. Cloudflare accepted
  the message; persisted message 3 reads back as `sent` / `accepted`. Recipient
  inbox delivery is not yet confirmed.
- At 19:33 UTC, a branded test from `hello@notify.zmail.im` to the same authorized
  Gmail recipient was accepted (message 4). A real Cloudflare `delivered` event
  was persisted at 19:33:43 UTC: Gmail accepted the message at its mail server.
  A subsequent browser check of this message in the recipient Gmail account
  confirmed the Inbox label. Gmail Show original reported SPF PASS, DKIM PASS
  for notify.zmail.im, DMARC PASS, and delivery after 3 seconds.
- Public provider-required SPF, DKIM, bounce MX and DMARC records match for all
  three domains; DMARC is p=reject. Reports are under deploy/brand/reports.
- Four Chinese/English welcome and service templates were saved and previewed.
  They are reusable mailbox templates; existing password-reset/invitation body
  implementations are not replaced. System sender MAIL_FROM uses the official address.
- A newly created shared address received a real SMTP message through Cloudflare
  and the Worker (outbound message 5, inbound message 6). Its real reply reached
  the official hello mailbox (outbound message 7, inbound message 8). No synthetic delivery
  event was injected. Bounce/complaint paths are unit-tested, not live-induced.
- Receiver authentication verification is complete for the branded notify.zmail.im
  test message. This is point-in-time evidence, not a guarantee for future mail.
  The full product acceptance plan remains outside this targeted rollout.

## Backups and Operations

`/opt/zmail/deploy/backup.py` dumps the database using the non-superuser owner
role and archives the Compose/env/database-secret configuration. Files are
root-only under `/opt/zmail/backups`. Cron runs at 03:30 UTC each day.
A database dump was restored successfully into an isolated disposable
PostgreSQL 18 container, with all 27 public tables present.
A subsequent backup includes the initial owner, mailbox, and synthetic message.
Backups currently remain on this host; off-host storage and retention are not set.

Run as root on the host:

```sh
cd /opt/zmail/deploy
docker compose ps
docker compose logs --tail=100 api web broadcast-worker webhook-worker
/opt/zmail/deploy/backup.py
```

Do not print an interpolated Compose config or environment to shared logs:
these contain credentials. For upgrades, take a backup, build versioned images,
run the required checks, and review migrations before replacing the running
stack. Keep the prior image tags available for application rollback; a database
rollback requires a separate migration/restore decision.

## Professional Mail Rollout

See [brand assets and operations](../deploy/brand/README.md) and the
[implementation plan](plans/professional-mail-rollout.md). Configuration before
this change is retained as `.env.before-professional-mail` and
`compose.before-professional-mail.yaml` under the root-only deployment directory.

## v2 release (2026-11-20)

Source: `main` at `dc79ff5e914`, which carries slice ① (unified inbox, roles, v2 look; PRs 29
and 30), slice ⑤ (developer transactional mail; PR 33), and the two production patches above
(PRs 31 and 32).

- Backup before the change: `/opt/zmail/backups/zmail-20261120T001834Z.dump` and
  `zmail-config-20261120T001834Z.tar.gz`.
- Rehearsal: the backup was restored into a disposable `postgres:18.6-alpine` container with TLS,
  no published ports, and a separate network, as a non-superuser `zmail_owner`. All 15 new
  migrations applied in 313 ms. The new API and the previous API (`fb3493afa496-mailrouting`)
  both reached `/health/ready` 200 on the migrated schema, so an image rollback needs no schema
  rollback. The rehearsal container and network were removed.
- Production migration: the same 15 migrations, 436 ms, as `zmail_owner`. `zmail_app` holds
  table privileges on the new tables through the owner's default privileges.
- Images: `zmail-api:dc79ff5e9141` (migrate, api, webhook-worker, broadcast-worker) and
  `zmail-web:dc79ff5e9141`. Previous tags remain on the host. The previous compose file is
  `/opt/zmail/deploy/compose.before-v2.yaml`.
- Transactional mail stays off (`MAILBOX_TRANSACTIONAL_ENABLED` unset): turning it on needs a new
  `MAILBOX_SECRET_ENCRYPTION_KEY`. Lane limits use the defaults: broadcast and transactional each
  take `MAILBOX_DAILY_SEND_LIMIT`.
- `mail:zone:work` is not deployed; custom domains stay off.
- Smoke check, without sending mail: all four containers up, API and Web healthy; local and
  public `/health/ready` 200, `/login` 200; anonymous `/v1/inbox-messages` and
  `/v1/inbox-message-counts` 401. No errors in the API, Web, or webhook-worker logs.
- Found in the smoke check: broadcast-worker logged a scan error on every pass while no
  transactional message was queued (`min(created_at)` was NULL). Sending was unaffected; only the
  queue-age gauge stayed stale. The fix shipped as a follow-up API image (see below).

Rollback: if any test API keys were created after this release, disable them first, because the
previous code has no key mode and would treat them as live. Then restore
`compose.before-v2.yaml` and run `docker compose up -d --no-deps api webhook-worker
broadcast-worker web`. Old images are kept, and the schema needs no rollback.

### Follow-up API image (same day)

- `zmail-api:24083e926a84` (main after PR 34) now runs migrate, api, webhook-worker, and
  broadcast-worker; Web stays on `zmail-web:dc79ff5e9141`. Previous tag `zmail-api:dc79ff5e9141`
  is kept.
- Smoke check repeated: containers healthy, public `/health/ready` and `/login` 200, anonymous
  inbox routes 401, and no error lines in any of the four logs, including broadcast-worker.
- The migrate run for this image could not connect: the shared PostgreSQL had no ordinary
  connection slots left (`remaining connection slots are reserved for roles with the SUPERUSER
  attribute`). The image adds no migration, so nothing was skipped. A host-side socket count
  showed 97 established connections, 3 of them Zmail's (api, webhook-worker, broadcast-worker);
  the rest belong to other tenants on the shared instance. Zmail's role limits (owner 2, app 4)
  were not the cause. Raising `max_connections` or trimming other tenants' pools is an
  instance-level change for the operator.

### Transactional mail turned on (2026-11-20, at the user's request)

- A new 48-character `MAILBOX_SECRET_ENCRYPTION_KEY` was generated on the host, never printed,
  and stored with `MAILBOX_TRANSACTIONAL_ENABLED=true` in `/opt/zmail/deploy/transactional.env`
  (root, mode 600). Only `api` and `broadcast-worker` load it through `env_file`.
- The file is deliberately kept out of `.env`, because `backup.py` archives `.env` next to the
  database dumps, and the key must stay apart from database backups. It is therefore not
  backed up. Losing it only fails messages still queued (at most one hour) and resets
  idempotency for 24 hours. Rotation steps: `api/docs/DEPLOYMENT.md`, Transactional Mail.
- Previous compose file: `compose.before-transactional.yaml`. To turn the feature off, set
  `MAILBOX_TRANSACTIONAL_ENABLED=false` in the file and recreate `api`. Keep the key so the worker
  can drain.
- Smoke check, no mail sent: API healthy, no error lines in the API or broadcast-worker logs;
  anonymous `POST /v1/transactional-messages` now answers 401 (route active, auth required);
  public `/health/ready` and `/login` 200.

### Unread index and palette colors (2026-11-20, overnight run)

- Source `main` at `a4ccb875164` (PR 35). Images `zmail-api:a4ccb8751644`, `zmail-web:a4ccb8751644`;
  previous compose `compose.before-a4ccb8751644.yaml`; previous tags kept.
- Backup `zmail-20261120T012920Z.dump`. Rehearsal with `/opt/zmail/deploy/rehearse.sh` (disposable
  PG18, TLS, non-superuser owner): `add_mail_unread_index` applied in 39 ms; new and previous API both
  ready. Production migration 45 ms.
- Smoke check: four containers up, API and Web healthy, no error lines in any log; public
  `/health/ready` and `/login` 200; anonymous inbox and transactional routes 401.

## Verification-code masking (overnight run, 2026-11-20 ~02:10 UTC)

- Release: main `52fd321` (PR #36), images `zmail-api:52fd321cd2e1` and `zmail-web:52fd321cd2e1`; previous `a4ccb8751644` kept.
- Backup: `zmail-20261120T015333Z.dump`. No new migration ("Nothing to migrate"), so no rehearsal.
- Compose backup: `compose.before-52fd321cd2e1.yaml`. The web image was built before the API swap because the old web parses message details strictly and would reject the new `secrets_masked` field.
- Smoke: api and web healthy, workers up; public `/health/ready` and `/login` 200; unauthenticated `/v1/inbox-messages`, `POST /v1/transactional-messages`, and `POST .../reveal` 401; no error lines in the four containers' logs.
- Rollback: restore `compose.before-52fd321cd2e1.yaml` and `up -d` (images a4ccb8751644).

## Interaction polish (2026-11-20 ~10:20 UTC)

- Release: main `2b44aec` (PR #38, web-only; also carries test-only PR #37). Images `zmail-api:2b44aecb602f`, `zmail-web:2b44aecb602f`; previous `52fd321cd2e1` kept.
- Backup `zmail-20261120T101441Z.dump`; no new migration. Deploy lock `/opt/zmail/deploy/.deploy-lock` taken and released.
- Compose backup `compose.before-2b44aecb602f.yaml`. Smoke: api/web healthy, workers up, public 200/401 as expected, no error lines.

## Platform account ledger (2026-11-20 ~10:40 UTC)

- Release: main `341464d` (PR #39). Images `zmail-api:341464d27bd4`, `zmail-web:341464d27bd4`; previous `2b44aecb602f` kept.
- Backup: first `backup.py` attempt failed (pg_dump; likely the shared instance's connection slots), the retry succeeded: `zmail-20261120T103059Z.dump`.
- Rehearsal on a disposable PG18 from that dump: migration `2026_11_25_120000_create_platform_accounts` in 68 ms; new and old API images both ready 200; REHEARSAL-OK.
- Production migration 88 ms (new tables only). Compose backup `compose.before-341464d27bd4.yaml`.
- Smoke: api/web healthy, workers up; public 200; unauthenticated inbox, transactional, and platform-accounts 401; no error lines. Deploy lock taken and released.
- Rollback: restore the compose backup and `up -d`; the new tables are unused by the old images.

## Shared mailbox admin, phase 1 (2026-11-20 ~11:15 UTC)

- Release: main `07a84a2` (PR #41). Images `zmail-api:07a84a2c914e`, `zmail-web:07a84a2c914e`; previous `341464d27bd4` kept.
- Backup: first `backup.py` attempt failed (pg_dump, shared-instance connection slots again), retry succeeded: `zmail-20261120T110616Z.dump`.
- Rehearsal on a disposable PG18 from that dump: `2026_11_25_090000_add_mailbox_directory_index` in 44 ms; new and old API both ready 200; REHEARSAL-OK.
- Production migration 58 ms (partial index on `mail_messages`). Compose backup `compose.before-07a84a2c914e.yaml`.
- Smoke: four containers running, 0 restarts, 0 error lines in 4 minutes; public `/health/ready`, `/login`, `/console/admin` 200; unauthenticated inbox, transactional, platform-accounts, and mailbox-exports 401. Deploy lock taken and released.
- Rollback: restore the compose backup and `up -d`; the old images ignore the new index.

### Mailbox limit raised to 200 (2026-11-20 ~11:40 UTC, at the user's request)

- Added `MAILBOX_MAX_PER_ORGANIZATION: '200'` next to `MAILBOX_DAILY_SEND_LIMIT` in each service's environment in `compose.yaml`; previous file `compose.before-mailbox-limit.yaml`. Recreated api, webhook-worker, broadcast-worker (images unchanged, `07a84a2c914e`).
- Verified the variable on `zmail-api-1`; API healthy, public `/health/ready` 200, no error lines. Deploy lock taken and released.
- Reason: create 60 more shared mailboxes on zgi.app (10 existed; default limit 50). Rollback: restore the compose backup and recreate the three services.

## Role addresses on organization domains (2026-11-20 ~11:55 UTC)

- Release: main `79a7d09` (PR #43; also PR #44, migration-name ordering fix from the stability loop). Images `zmail-api:79a7d097795e`, `zmail-web:79a7d097795e`; previous `07a84a2c914e` kept.
- Backup `zmail-20261120T113023Z.dump`; no new migration ("Nothing to migrate"). Compose backup `compose.before-79a7d097795e.yaml`; `MAILBOX_MAX_PER_ORGANIZATION=200` carried over.
- Smoke: four containers running, 0 restarts, 0 error lines in 4 minutes; public `/health/ready`, `/login`, `/console/admin` 200; unauthenticated inbox and transactional 401. Deploy lock taken and released.

## Platform accounts page (2026-11-20 ~12:15 UTC)

- Release: main `4c810dc` (PR #45, web only). Images `zmail-api:4c810dc44f40`, `zmail-web:4c810dc44f40`; previous `79a7d097795e` kept.
- Backup: first attempt failed (pg_dump, shared-instance connection slots), retry `zmail-20261120T113813Z.dump`. No new migration. Compose backup `compose.before-4c810dc44f40.yaml`.
- Smoke: four containers running, 0 restarts, 0 error lines in 4 minutes; `/health/ready`, `/login`, `/console/admin` 200; unauthenticated inbox and platform-accounts 401. Deploy lock taken and released.

### Self-registration opened (2026-11-20 ~12:25 UTC, at the user's request)

- `.env`: `AUTH_REGISTRATION_ENABLED` changed from `false` to `true` (previous file `.env.before-registration`, root-only like `.env`). Recreated api, both workers, and web; images unchanged (`4c810dc44f40`).
- Verified: the variable is `true` on `zmail-api-1` and `zmail-web-1`; `/register` answers 200 instead of redirecting to `/login`; API healthy, no error lines. Deploy lock taken and released.
- Effect: anyone can create an account and their own organization; existing limits still apply (50→200 mailboxes per organization, 200 send attempts per 24 hours). Rollback: set it back to `false` and recreate the same services.

## AI triage, offline part (2026-11-20 ~12:50 UTC)

- Release: main `a0235a2` (PR #46). Images `zmail-api:a0235a2ad535`, `zmail-web:a0235a2ad535`; previous `4c810dc44f40` kept.
- Rehearsal on a disposable PG18 from the latest backup: `2026_11_26_090000_add_mail_triage` in 66 ms; new and old API both ready; REHEARSAL-OK. Production migration 112 ms (two new tables, one `DEFAULT false` column).
- Feature is off: `AI_TRIAGE_PROVIDER` unset (disabled), every switch defaults off; no triage worker started.
- Smoke: four containers running, 0 restarts, 0 error lines; public 200/401 as expected. Deploy lock taken and released.

## Staging stack (2026-11-20, at the user's request)

- `/opt/zmail-staging` (compose project `zmail-staging`), generated from the production compose by `make-staging.py`: own `postgres:18.6-alpine` container and volume (no published port; app role `zmail_staging` is not a superuser), network `10.213.79.0/24`, API on `127.0.0.1:18993`, Web on `127.0.0.1:18992`. Secrets generated on the host into `/opt/zmail-staging/.env` (root, 600), never printed.
- `APP_ENV=staging`, `MAILBOX_PROVIDER=log`, system mail off (no `MAIL_FROM`), no Cloudflare credentials, custom domains off, registration open, `MAILBOX_DOMAIN=mail.test.zmail.im`. Does not touch the shared PostgreSQL or production containers.
- Pending: DNS `test.zmail.im` A → 38.58.181.154 (added by the user), then certbot webroot certificate and the OpenResty vhost (`zmail-test-http.conf`, `zmail-test-https.conf`, deploy hook staged in `/opt/zmail-staging`).
- 2026-11-20 ~13:00 UTC: at the user's explicit instruction, added DNS `A test.zmail.im → 38.58.181.154` (DNS only, comment "Zmail staging") through the Cloudflare API from the server, using the existing zmail.im token without printing it; no other record touched. Certificate issued by certbot webroot (`/opt/1panel/www/zmail-test-acme`, expires 2027-02-18), deploy hook `/etc/letsencrypt/renewal-hooks/deploy/zmail-test-openresty.sh`, vhost `/opt/1panel/www/conf.d/zmail-test.conf` (adds `X-Robots-Tag: noindex`). `nginx -t` passed before each reload; zmail.im stayed 200. https://test.zmail.im `/health/ready`, `/login`, `/register` 200. A QA owner account was registered on staging; its credentials live only in the untracked `.claude/staging-test-account.local.env`.

## 2026-11-20 运营命令行（PR #47 + #48）部署 `9df0898b1cf5`

- 无新迁移；备份 → git archive → 构建 zmail-api/web:9df0898b1cf5 → compose 备份 compose.before-9df0898b1cf5.yaml → migrate → api/worker → web。
- 冒烟通过：4 容器健康，/health/ready、/login 200，未登录接口 401，日志无 error/panic。
- 运营操作：`mailbox:import --organization=1` 导入 zgi.app 61 个公共邮箱（11 个角色地址 + 50 个人名地址），61 created / 0 failed；组织 1 邮箱数 105/200。CSV 在 /opt/zmail/imports/zgi-app-mailboxes.csv（无敏感信息）。

## 2026-11-20 主线部署 `eeea0f1cf044`（PR #49 验证码注册、#50 AI 分流设置、#51 Cloudflare Message-ID 热修入库）

- 背景：12:23 UTC 有一次未走流程的热修把 api/worker 换成 `cce1116774b7`，并留下普通文件形式的 `.deploy-lock`。用户决定按推荐处理：热修走 PR #51 合入 main，删除遗留锁文件，再正常部署。
- 新迁移 `2026_11_27_090000_create_registration_email_codes`：用 13:27 备份预演通过，新旧（cce1116774b7）两个 API 镜像 /health/ready 均 200。
- 迁移、api/worker、web 依次切换；冒烟通过：4 容器健康、重启 0，/health/ready、/login、/console/admin 200，未登录接口 401，日志 0 error/panic。
- 生产系统发信已配置（cloudflare），注册验证码可发出；部署过程未真实发信。

## 2026-11-20 外部邮箱离线基础（PR #52）部署 `1629d6362094`

- 新迁移 `2026_11_28_090000_add_external_mail`（只改目录元数据 + 新表 + 部分唯一索引）：用最新备份预演通过，新旧（eeea0f1cf044）两个 API 镜像 /health/ready 均 200。
- `EXTERNAL_MAIL_PROVIDERS` 未设置，功能关闭；同步 worker 未部署。
- 冒烟通过：4 容器健康、重启 0，/health/ready、/login、/console/admin 200，未登录接口 401，日志 0 error/panic。
- 插曲：此前一次以错误提交号启动的部署在 git archive 前失败，只多做了一次备份并留下空的 releases/1629d63b8cd5 目录，已删除，未影响生产。

## 2026-11-20 平台运营统计（PR #53）部署 `da0855fb29cd`

- 迁移 `2026_11_29_090000_add_platform_operator_scope` 属于 operator 模块；生产 `OPTIONAL_STARTERS=organization,webhook,mailbox` 未启用 operator，所以预演和正式迁移均为 Nothing to migrate，`/v1/operator/*` 返回 404，行为不变。
- 新旧（1629d6362094）API 镜像在预演库上 /health/ready 均 200；冒烟通过：4 容器健康、重启 0，/health/ready、/login、/console/admin 200，未登录接口 401，日志 0 error/panic。
- 启用运营看板需要：生产启用 operator 模块、托管独立 Admin 静态站（域名/同源网关）、指定首位查阅人（`operator:grant <email> --scope=mail-metrics`）。待用户决定。

## 2026-11-20 启用平台运营后台（用户决定“按推荐办”）

- DNS：admin.zmail.im A → 38.58.181.154（仅 DNS，不代理），用服务器上的生产 token 经 Cloudflare API 添加，未打印。
- TLS：certbot webroot `/opt/1panel/www/zmail-admin-acme`，续期钩子 `/etc/letsencrypt/renewal-hooks/deploy/zmail-admin-openresty.sh`，证书在 `/opt/1panel/apps/openresty/openresty/conf/ssl/zmail-admin/`。
- 静态站：admin/ 用 main `da0855fb29cd` 构建（VITE_OPTIONAL_FEATURES=operator,organization,webhook,mailbox，VITE_API_BASE_URL=/api，zh-Hans），发布到 `/opt/1panel/www/zmail-admin/da0855fb29cd`，`current` 软链指向它；vhost `/opt/1panel/www/conf.d/zmail-admin.conf`，`/api/*` 同源转发到 127.0.0.1:18991，带 CSP/HSTS/noindex。
- API：备份 → 用 `rehearse-operator.sh`（加 operator 与 OPERATOR_ALLOWED_ORIGINS）预演通过 → compose 备份 `compose.before-operator.yaml` → 4 个服务 `OPTIONAL_STARTERS` 加 `operator`、`OPERATOR_ALLOWED_ORIGINS: https://admin.zmail.im` → 迁移 `create_platform_operators_table`、`add_platform_operator_scope` → 重建 api/worker。
- 冒烟：/health/ready、/login、admin.zmail.im 200；未登录 `/api/v1/operator/*` 与 `/v1/inbox-messages` 401。
- 回滚：恢复 `compose.before-operator.yaml` 并 `up -d --no-deps api webhook-worker broadcast-worker`；新表保留不影响旧行为。
- 待办：用户提供注册邮箱后执行 `operator:grant <email> --scope=mail-metrics`。

## 2026-11-20 性能修复（PR #54）部署 `74ebf0584e41`

- 新迁移 `2026_11_30_090000_add_mail_outbound_time_index`（部分索引；当前数据量下 47 ms 完成）：用 `rehearse-operator.sh`（含 operator 模块）预演通过，新旧（da0855fb29cd）API 镜像均 200。
- 冒烟通过：4 容器健康、重启 0，日志 0 error/panic；operator 模块仍启用，admin.zmail.im 未登录 API 401。

## 2026-11-20 组织广播正文查阅（PR #55）部署 `4acca0abab6a`

- 新迁移 `add_platform_operator_mail_content`、`create_mail_content_access_audits`：用 `rehearse-operator.sh` 预演通过，新旧（74ebf0584e41）API 均 200。
- 冒烟通过：4 容器健康、重启 0，日志 0 error/panic；未登录 `/api/v1/operator/content-access-audits` 401。
- Admin 静态站重新构建并发布到 `/opt/1panel/www/zmail-admin/4acca0abab6a`，`current` 已切换；旧版本目录保留可回切。
- 正文查阅只对组织广播开放，需要 grant 带 `mail-content` 附加项（任何 scope 默认都不含）。

## 2026-11-20 — Shared PostgreSQL connection pressure (Zmail-only mitigation)

- Symptom: brief 503s at ~17:14 and 19:14–19:16 UTC, `SQLSTATE 53300 remaining connection slots are reserved` on `zgi-shared-postgres` (instance at ~97/100). Zmail pools rotated connections every 15m idle / 1h lifetime, so each reconnect could hit a full instance.
- Change (compose only, no image change; backups `compose.before-dbpool.yaml`, `compose.before-dbidle1.yaml`): `DB_CONN_MAX_IDLE_TIME: 24h` and `DB_CONN_MAX_LIFETIME: 24h` on api, webhook-worker, broadcast-worker, migrate. API idle pool stays 1 so one of the four `zmail_app` role slots stays free for operator CLI commands.
- Instance-level configuration and other projects' containers were not touched; the root cause (instance near `max_connections`) is recorded as an open decision on the status board.
- Smoke: ready/login 200, protected endpoints 401, 4 containers running with 0 restarts, no error/panic in the 3-minute log scan.
- Operator access: granted `mail_metrics+mail_content` to the initial owner account (already organization owner, so no extra organization-admin grant).

## 2026-11-20 — Shared PostgreSQL `max_connections` 100 → 200 (user-approved)

- Decision: the user chose "raise the connection limit" on board decision `shared-pg` after the second user-visible 503 burst (19:14–19:16 UTC).
- Change: `/opt/1panel/apps/postgresql/shared-postgres/data/18/docker/postgresql.conf` `max_connections = 200` (backup `postgresql.conf.bak-20261120-maxconn100`); no other parameter changed, no Postgres admin account used. `postgres -C max_connections` reads 200.
- Restart: deploy lock taken, `docker restart -t 30 zgi-shared-postgres` at 19:38:57, accepting connections at 19:39:07 UTC (~10 s). Zmail returned 8 × 503 during that window (19:38:57–59); 0 errors, 0 5xx, 0 Postgres FATAL after 19:39:30.
- Other projects: container list and health identical before and after the restart; nothing restarting.
- Rollback: copy the `.bak` file back and restart the container again.

## 2026-11-20 ~20:15 UTC — `9d1f7a6d8645` (#57: which mailbox received a message; email log fix)

- Inbox rows label the receiving mailbox (Gmail-style), the sidebar lists mailboxes when there are no roles, the reading pane shows the receiving mailbox as a chip; `GET /v1/mail-messages` again answers `MailLogEntry` (the email log page had failed with "invalid data" since the organize fields leaked into it).
- Backup `zmail-20261120T200952Z.dump`; no migration (rehearsal: nothing to migrate, new and old API ready 200); compose backup `compose.before-9d1f7a6d8645.yaml`; DB pool settings (24h idle/lifetime, API idle 1) kept.
- Smoke: ready/login/admin 200, protected endpoints 401, 4 containers running with 0 restarts, no error/panic in the log scan. Rollback image: `4acca0abab6a`.

## 2026-11-20 ~20:45 UTC — `77aef94ccebf` (#59: one navigation on the mailboxes page)

- On `/console/mailboxes` the console sidebar folds to an icon rail; the mail sidebar shows in full from 1024px.
- No migration (rehearsal: nothing to migrate); compose backup `compose.before-77aef94ccebf.yaml`; DB pool settings kept.
- Smoke: ready/login/admin 200, protected endpoints 401, 4 containers running with 0 restarts, no error/panic. Rollback image: `9d1f7a6d8645`.

## 2026-11-20 ~21:10 UTC — `b3c58385b679` (#58: inbox navigation and mailbox filtering; #60 docs)

- Web-only change (no API, contract, or migration change); deployed at the user's request after the stability loop's full regression passed on this commit.
- Backup `zmail-20261120T210658Z.dump`; rehearsal: nothing to migrate, new and old API ready 200; compose backup `compose.before-b3c58385b679.yaml`; DB pool settings kept.
- Smoke: ready/login/admin 200, protected endpoints 401, 4 containers running with 0 restarts, no error/panic. Rollback image: `77aef94ccebf`.

## 2026-11-20 ~23:20 UTC — `79c1924e3d16` for api and workers (#61)

- #61 (mailbox sort settings) changed only `web/`; its web image `79c1924e3d16` was already live from another deploy, while api and workers stayed on `b3c58385b679`. At the user's request all four services now run `79c1924e3d16` (API code identical to `b3c58385b679`).
- Backup `zmail-20261120T231713Z.dump`; rehearsal: nothing to migrate, new and old API ready 200; compose backup `compose.before-79c1924e3d16.yaml`; DB pool settings kept.
- Smoke: ready/login/admin 200, protected endpoints 401, 4 containers running with 0 restarts, no error/panic. Rollback image: `b3c58385b679`.
