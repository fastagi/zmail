# IMAP/SMTP 网关 P1a 实施计划：原文留存 + 设备客户端密码 + 只读 IMAP

> 给执行 agent：按任务顺序逐个实现，每个任务先写失败测试再写实现；每个任务结束时单独提交一次（只在负责人要求时推送）。

**Goal**：员工在网页上为每台设备生成客户端专用密码，用系统邮件 App 通过 IMAPS 只读地收取自己有权查看的 Zmail 邮箱（个人邮箱放在根目录，共享邮箱放在 `Shared/` 下），并且可以同步 `\Seen`、`\Flagged`；入站邮件从此保存 RFC 5322 原文。

**Architecture**：API 镜像新增 operator 命令 `fastbuild mail:gateway`，进程内由 `emersion/go-imap/v2` 的 `imapserver` 自己终止 TLS。协议适配层放在 `api/internal/modules/mailbox/imapgateway`（只有这个包 import go-imap），只依赖 `internal/domain` 里新增的接口 `domain.MailClientStore`；接口由 mailbox 模块实现，读写都走现有的 `authorizeMailbox` / `MailboxAccessFor` / `UpdateMessages`。UID 由 PostgreSQL 触发器维护（`mail_imap_folders` + `mail_imap_uids`）。设备密码以 `HMAC-SHA256(pepper, secret)` 的形式存在 `mail_app_passwords`；每个进程有一个后台 sweep，定期校验所有活跃会话，撤销或停用后 60 秒内断开连接。

**Tech Stack**：Go 1.26、Gin、Wire、GORM、PostgreSQL（触发器 + plpgsql）、`github.com/emersion/go-imap/v2 v2.0.0-beta.9`（连带 `go-message`、`go-sasl`）、Next.js 16 / React 19 / shadcn（设置页）、Vitest。

**Spec**：[imap-smtp-gateway.zh-CN.md](imap-smtp-gateway.zh-CN.md)（§4 认证与授权、§5.1–5.3、§5.5、§5.6、§5.9、§8 中的 P1a 行）。代码基线：`origin/main` `4a1d3105`。

---

## 已定（不再讨论）

1. 依赖：IMAP 服务端用 `github.com/emersion/go-imap/v2`，**精确锁定** `v2.0.0-beta.9`（MIT 许可）；P1b 用 `github.com/emersion/go-smtp v0.26.0`。`go.mod` 不写范围版本。beta 风险靠协议级集成测试兜住：测试里用真实的 go-imap v2 `imapclient` 经 TLS 连接网关。
2. 新进程 `fastbuild mail:gateway` 打包在 API 镜像里，以后作为独立的 compose 服务运行。P1a 只监听一个可配置地址（本地示例 `127.0.0.1:1993`），TLS 证书和私钥路径来自配置，没配置时网关处于禁用状态。
3. 客户端密码：按用户、按设备名生成，范围为 `imap`/`smtp`，明文只显示一次，库里只存哈希，可撤销，列表接口永远不返回明文。组织开关 `mail_client_access` 默认开启；成员功能 `mail_clients` 按 spec 设置。撤销或停用后，已有会话必须在 60 秒内断开。
4. 可见范围只由 `MailboxAccessFor` 决定：个人邮箱在根目录，共享邮箱在 `Shared/` 下。
5. IMAP 下发完整原文，不遮罩验证码（spec §5.6）。
6. 浏览器需要处理的错误码要同时加到 `api/internal/domain/error_codes.go`、`web/src/http/codes.ts`、`admin/src/http/codes.ts` 和 openapi 的 `ErrorCode` 枚举里；只在模块内部使用的错误放在 `api/internal/modules/mailbox/errors.go`。
7. 新迁移要更新 golden schema（`FASTBUILD_UPDATE_GOLDEN_SCHEMA=1`），写 Up/Down 测试，并在 `UPGRADING.md` 里写明锁表影响。只支持 PostgreSQL。
8. 先改契约：`contracts/*.md` + `contracts/openapi.yaml`，再执行 `cd contracts && corepack pnpm generate` 重新生成类型。

## 本计划对 spec 空白处的裁定

| # | 问题 | 裁定 |
|---|---|---|
| R1 | 表名：spec 写的是 `mail_client_passwords`，任务要求 `mail_app_passwords` | 用 `mail_app_passwords`，路由用 `/v1/mail-app-passwords`。为了不和外部账号的“App Password 绑定”（MAILBOXES.md §Binding With An App Password）混淆，契约小节命名为 “Mail Client Access”，界面上统一叫“客户端专用密码” |
| R2 | 哈希算法：argon2id 还是仓库现有的 hasher | 仓库现有两种：bcrypt（用户密码）和 SHA-256（API key 的 `key_hash`）。密码是服务端生成的 16 个字母（约 75 bit 熵），所以采用 spec §4.1 的 `HMAC-SHA256(MAIL_APP_PASSWORD_PEPPER, normalized)`，用唯一索引直接查找。bcrypt/argon2 会让每次 IMAP 登录多花 50–100 ms 的 CPU，而且无法按哈希值查找。计算量对所有输入都一样，满足 api/AGENTS.md“未知身份也要做哈希计算”的要求 |
| R3 | spec 里的“近期登录验证（10 分钟内）”，仓库里没有对应机制 | P1a 在创建请求体里要求 `current_password`；账号开了两步验证时还要 `totp_code`。错误统一返回 `422 COMMON.VALIDATION_FAILED` 加字段错误（不用 401，避免 BFF 误判为会话过期）。失败次数计入按用户的限流桶 |
| R4 | 组织开关存在哪里 | mailbox 模块新建表 `mail_client_settings(organization_id PK, access_enabled)`，没有行就等于开启。做法参考 `mail_triage_settings`，不依赖可选的 setting starter |
| R5 | `mail_client_sessions` 表和 `pg_notify` 即时断开 | 推迟到 P1c。P1a 只靠进程内 sweep（默认每 20 秒一次）保证 60 秒内断开；会话结束只写结构化日志（只含 ID） |
| R6 | 现有 golden dump 不包含函数和触发器 | 任务 1 扩展 `dumpSchema`，输出 `FUNCTION`（名称 + `md5(prosrc)`）和 `TRIGGER`（`pg_get_triggerdef`），否则触发器被改动时没有测试能发现 |
| R7 | `modseq`/`highest_modseq` | P1a 不建这两列（没有 CONDSTORE，也没有 IDLE）。P1c/P2 用新迁移加上，并同时给 reads/stars 加触发器 |
| R8 | 用户文件夹名里有 `/`，或与 `INBOX`/`Sent`/`Archive`/`Trash` 重名 | 对外显示时把 `/` 换成 `∕`（U+2215）；重名的加后缀 ` (1)`。由 `folderKey` 到名字的映射在每次 LIST 时算出，SELECT 时按同一规则反查 |
| R9 | P1a 是否接受 `smtp` 范围 | 接受并存下来，默认两种范围都选，这样 P1b 上线后不用重新生成密码。界面把 SMTP 标为“即将支持” |
| R10 | 撤销后的密码行保留多久 | 撤销后保留 90 天，由 `mail:broadcast:work` 新增一个清理步骤删除。成员被移出组织时，密码行随 membership 级联删除 |

## Global Constraints

- 网关和 HTTP API 共用 `service` 的权限判断：IMAP 的每次 SELECT/STATUS/FETCH/STORE 都重新调用 `authorizeMailbox`，禁止在网关里自己写 SQL 判断权限。
- `imapgateway` 包只依赖 `internal/domain` 和 go-imap，不 import `mailbox`。go-imap 的类型不能出现在 `domain` 或 `mailbox` 里。
- 日志只写 ID（组织、membership、密码、邮箱、邮件）、命令名、耗时和结果，**绝不写**主题、地址、正文、密码或用户名。`imapserver.Options.DebugWriter` 永远是 nil。
- 限流沿用 `internal/infra/ratelimit.Stores`。按 IP 和按用户名分别设桶，不合并成一个 `IP+subject` 键（api/AGENTS.md）。
- 只有 TLS 监听。P1a 不提供明文端口和 STARTTLS，TLS 最低版本为 1.2。
- 资源都有上限：全局 1000 个连接、每用户 20 个、每 IP 100 个；未认证超时 60 秒，已认证空闲 30 分钟；单次 FETCH 最多 500 封；原文缓存 64 MiB LRU。
- 验证顺序：先做最窄的检查，跑 targeted `go test` / Vitest，最后跑一次相关 tier。本计划不跑 `make check`（不是发布边界）。

## Review Focus（最可能出错的 5 处）

1. **UID 单调性与提交顺序**：两个事务同时向同一文件夹插邮件时，如果 UID 小的那个后提交，用 UIDNEXT 增量同步的客户端会永远漏掉它。触发器对 `mail_imap_folders` 行执行 `INSERT … ON CONFLICT DO UPDATE`，这个行锁会一直持有到事务提交，从而把同一文件夹的取号串行化。测试 `TestMailImapUidTriggerSerializesConcurrentInserts` 必须用两个真实事务来验证。邮件移出再移回同一文件夹时，必须拿到比原 UIDNEXT 更大的**新** UID。
2. **UIDVALIDITY 不能悄悄变化**：只有新建 `mail_imap_folders` 行时才取 `clock_timestamp()` 的秒数。迁移 Down 再 Up 会重新编号，这时 UIDVALIDITY 必须同时变化（测试要断言变化了）。删除文件夹再建同名文件夹时 `folder_key` 不同，自然得到新的 UIDVALIDITY。任何代码都不允许 UPDATE `uidvalidity`。
3. **网页和 IMAP 并发改已读状态**：`\Seen` 只能经 `service.UpdateMessages(... MessageUpdate{Read: &v})` 写入（底层 `setReads` 用 `ON CONFLICT DO NOTHING`/DELETE，是幂等的）。外发邮件永远是已读，STORE `-FLAGS \Seen` 时回报的 FLAGS 仍含 `\Seen`。FETCH `BODY[]`（非 PEEK）会隐式标记已读，但在 EXAMINE 下不能标记。共享邮箱中一个成员的已读不影响另一个成员。
4. **会话已打开时撤销密码**：认证只发生在 LOGIN 那一刻，会话后续不会再查密码。sweep 必须同时覆盖这几种情况：撤销、关闭组织开关、取消 `mail_clients` 功能、成员被移出（密码行级联删除）、失去某个共享邮箱的 view 权限（已选中就 BYE，没选中就在下次 LIST 时隐藏）。sweep 本身出错时不能放行，要记日志并在下一轮重试；连续失败超过 60 秒时主动断开所有会话。
5. **暴力破解与锁定**：失败时固定延迟 1 秒，并统一返回 `NO [AUTHENTICATIONFAILED] Authentication failed`，不区分密码错误、范围不符、地址无权、组织关闭等原因。IP 桶（20 次/15 分钟）和用户名桶（10 次/15 分钟）互相独立。24 小时内用这个用户名成功登录过的 IP 不受用户名桶限制，避免攻击者把正常员工锁在外面。锁定期间就算密码正确也要拒绝。用户名作为限流键之前先转小写，并取 SHA-256 前 16 位，避免日志或键里出现地址。

---

## Task 1：契约 + 三个迁移（原文、UID、客户端密码与权限）

**Files**
- 修改 `contracts/MAILBOXES.md`：新增 “Mail Client Access” 一节（端点、字段、错误码、IMAP 文件夹映射、只读范围、原文策略），并顺带修正 spec §9 #14 提到的 “Attachments are not stored” 矛盾。
- 修改 `contracts/ORGANIZATIONS.md`：功能目录新增第五项 `mail_clients`（邀请默认授予）。`contracts/AUDIT.md`：新增 `mail_app_password.created`、`mail_app_password.revoked`。
- 修改 `contracts/openapi.yaml`：新增路径、schema 和 `ErrorCode` 枚举值；执行 `cd contracts && corepack pnpm generate`，生成 `web/src/http/generated/openapi.ts` 和 `admin/src/http/generated/openapi.ts`。
- 新建 `api/database/migrations/2026_12_24_090000_add_mail_message_sources.go`、`2026_12_24_100000_add_mail_imap_uids.go`、`2026_12_24_110000_add_mail_app_passwords.go`，以及对应的测试 `mail_message_sources_test.go`、`mail_imap_uids_test.go`、`mail_app_passwords_test.go`。
- 修改 `api/database/migrations/schema_golden_postgres_test.go`（`dumpSchema` 增加 FUNCTION/TRIGGER 输出）、`testdata/schema.golden.sql`、`UPGRADING.md`。

**Interfaces（表）**
- `mail_message_sources(message_id bigint PK REFERENCES mail_messages(id) ON DELETE CASCADE, raw bytea NOT NULL, size integer NOT NULL, sha256 varchar(64) NOT NULL, origin varchar(16) NOT NULL, created_at timestamptz NOT NULL)`。约束：`chk_mail_message_sources_size CHECK (size = octet_length(raw) AND size BETWEEN 1 AND 5242880)`、`chk_..._sha256 CHECK (sha256 ~ '^[0-9a-f]{64}$')`、`chk_..._origin CHECK (origin IN ('inbound','smtp','composed'))`。
- `mail_imap_folders(mailbox_id bigint REFERENCES mailboxes(id) ON DELETE CASCADE, folder_key varchar(32), uidvalidity bigint NOT NULL, uidnext bigint NOT NULL DEFAULT 1, created_at timestamptz NOT NULL, PRIMARY KEY (mailbox_id, folder_key))`。约束：`folder_key ~ '^(inbox|sent|archive|trash|folder:[1-9][0-9]*)$'`，`uidvalidity`、`uidnext` 都在 `1..4294967295` 之间。
- `mail_imap_uids(mailbox_id bigint, folder_key varchar(32), uid bigint, message_id bigint NOT NULL REFERENCES mail_messages(id) ON DELETE CASCADE, PRIMARY KEY (mailbox_id, folder_key, uid))`，加 `UNIQUE INDEX idx_mail_imap_uids_message (message_id)`。
- SQL 函数 `mail_imap_folder_key(direction text, status text, placement text, folder_id bigint) RETURNS text IMMUTABLE`：
  - `trash` → `trash`；`archive` → `archive`；`folder` → `'folder:'||folder_id`；
  - `default` 且 inbound → `inbox`；`default` 且 outbound 且 `status <> 'failed'` → `sent`；
  - 其他情况 → NULL（不分配 UID）。
- 触发器函数 `mail_imap_assign_uid()`：`AFTER INSERT OR UPDATE OF placement, folder_id, status, mailbox_id ON mail_messages FOR EACH ROW`。新旧 key 相同时直接返回；否则先 `DELETE FROM mail_imap_uids WHERE message_id = NEW.id`，新 key 不为 NULL 时执行 `INSERT INTO mail_imap_folders … VALUES (…, extract(epoch FROM clock_timestamp())::bigint, 2) ON CONFLICT (mailbox_id, folder_key) DO UPDATE SET uidnext = mail_imap_folders.uidnext + 1 RETURNING uidnext - 1`，再插入 uid 行。另加触发器 `mail_imap_forget_folder()`：`AFTER DELETE ON mail_folders`，删除 `folder:<id>` 对应的 folders 行。
- 回填：迁移里用 `row_number() OVER (PARTITION BY mailbox_id, key ORDER BY id)` 一次性编号，再写入 folders，`uidnext = max+1`。
- `mail_app_passwords(id bigserial PK, organization_id bigint NOT NULL → organizations ON DELETE CASCADE, membership_id bigint NOT NULL → organization_memberships ON DELETE CASCADE, user_id bigint NOT NULL → users ON DELETE CASCADE, name varchar(64) NOT NULL, scope_imap boolean NOT NULL, scope_smtp boolean NOT NULL, secret_hash varchar(64) NOT NULL, hint varchar(4) NOT NULL, last_used_at timestamptz, last_used_ip varchar(64), last_used_protocol varchar(8), revoked_at timestamptz, revoked_by bigint, revoke_reason varchar(16), created_at, updated_at timestamptz NOT NULL)`。约束：`CHECK (scope_imap OR scope_smtp)`、`char_length(name) BETWEEN 1 AND 64`、`secret_hash ~ '^[0-9a-f]{64}$'`、`revoke_reason IN ('user','admin')`、`(revoked_at IS NULL) = (revoke_reason IS NULL)`。索引：`UNIQUE idx_mail_app_passwords_secret (secret_hash)`、`idx_mail_app_passwords_membership (membership_id) WHERE revoked_at IS NULL`、`idx_mail_app_passwords_revoked (revoked_at) WHERE revoked_at IS NOT NULL`。
- `mail_client_settings(organization_id bigint PK → organizations ON DELETE CASCADE, access_enabled boolean NOT NULL DEFAULT true, updated_by bigint, created_at, updated_at timestamptz NOT NULL)`。
- `organization_memberships`、`organization_invitations` 各加一列 `can_use_mail_clients boolean NOT NULL DEFAULT true`。

**Steps**
1. 先写契约和 openapi。用 `cd contracts && corepack pnpm lint`（或仓库里的 contract guard）确认 openapi 有效，再运行 generate。
2. 写失败测试 `TestGoldenDumpIncludesTriggers`（断言 dump 中出现 `TRIGGER mail_imap_assign_uid` 和 `FUNCTION mail_imap_folder_key`），再扩展 `dumpSchema`。
3. 写失败测试：
   - `TestMailMessageSourcesUpDown`：Down 后表消失；Up 后与 golden 一致；size/sha/origin 约束生效；删除邮件时级联删除原文。
   - `TestMailImapUidTrigger`：入站两封 → INBOX 的 uid 为 1、2；第 1 封移到 archive → archive uid 1，INBOX 只剩 uid 2；再移回 INBOX → uid 3；外发 `failed` 的邮件没有 uid 行，`queued` → `sent`；删除邮件后 uid 行消失；删除文件夹后 folders 行消失。
   - `TestMailImapUidTriggerSerializesConcurrentInserts`：事务 A 插入后不提交，事务 B 插入同一文件夹时被阻塞；A 提交后 B 的 uid = A 的 uid + 1。
   - `TestMailImapUidsBackfillAndUpDown`：迁移前已有的邮件按 id 顺序编号；Down→Up 后 `uidvalidity` 变化，且 schema 与 golden 一致。
   - `TestMailAppPasswordsConstraints`：没有范围、名字为空、hash 格式错误、hash 重复都失败；删除 membership 时级联删除；`can_use_mail_clients` 默认为 true。
4. 写三个迁移（`execStatements`，`UseTransaction: true`，Down 用 `DROP … IF EXISTS`，并删掉函数和两列）。
5. `cd api && FASTBUILD_UPDATE_GOLDEN_SCHEMA=1 go test ./database/migrations -run TestMigrationsProduceGoldenSchema`，然后不带变量再跑一遍全包。
6. `UPGRADING.md` 写一条 Medium：给 `mail_messages` 加触发器要短暂持有 `SHARE ROW EXCLUSIVE` 锁；回填会扫描整张 `mail_messages`，要在低峰期运行；两张 membership 相关表加常量默认值的列不会重写表。

**Verify**：`cd api && go test ./database/migrations/...`；`cd contracts && corepack pnpm generate && git diff --stat`。

**Commit**：`feat(api): add mail sources, IMAP UID tables, and app password schema`

## Task 2：入站路径保存原文 + 存量邮件合成

**Files**：`api/internal/domain/mailbox.go`（`MailMessage` 增加 `RawSource []byte`，只用于写入）、`api/internal/modules/mailbox/model.go`（`MailMessageSourcePO`）、`repository.go`、`service.go`、`message_state_repository.go`（`deleteMessageChunk` 的依赖表列表加上 `MailMessageSourcePO`）、新建 `message_source.go` 和 `message_source_test.go`。

**Interfaces**
```go
type MailMessageSourcePO struct { MessageID uint `gorm:"primaryKey;autoIncrement:false"`; Raw []byte; Size int; SHA256 string `gorm:"column:sha256"`; Origin string; CreatedAt time.Time }
func createMessageSource(tx *gorm.DB, messageID uint, raw []byte, origin string, at time.Time) error
func (r *repository) FindMessageSource(ctx context.Context, messageID uint) ([]byte, error) // ErrMailMessageSourceNotFound
// composeMessageSource rebuilds RFC 5322 for messages without a stored source; reuses buildMIMEMessage.
func composeMessageSource(message *domain.MailMessage, attachments []domain.MailAttachment) ([]byte, error)
func (s *service) messageSource(ctx context.Context, message *domain.MailMessage) ([]byte, error)
```

**Steps**
1. 写失败测试：
   - `TestReceiveInboundStoresRawSource`：`ReceiveInbound` 之后 `mail_message_sources.raw` 与输入逐字节相同，`origin = inbound`。
   - `TestReceiveInboundDuplicateKeepsOneSource`：重复投递只存一份原文。
   - `TestComposeMessageSourceRoundTrips`：合成结果经 `parseMail` 解析后，From/To/Cc/Subject（含中文）、Message-ID、In-Reply-To、References、text、html、附件名和内容都一致，且带 `X-Zmail-Reconstructed: 1`，`Date` 取 `created_at`。
   - `TestPurgeTrashDeletesSource`：彻底删除邮件时原文一起删除。
2. `ReceiveInbound` 设置 `message.RawSource = delivery.Raw`；`CreateInboundMessage` 在同一事务里、`createAttachments` 之后调用 `createMessageSource`（只在 `RawSource != nil` 时）。外部同步和导入路径不设这个字段，所以不写原文。
3. `composeMessageSource` 把消息转成 `mailtransport.Message` 交给 `buildMIMEMessage`。如果 `buildMIMEMessage` 缺 In-Reply-To/References/Date 参数，就扩展它的输入结构，并保证现有的 `external_mail_smtp_test.go` 不受影响。
4. `go test ./internal/modules/mailbox -run 'RawSource|ComposeMessageSource|PurgeTrash'`。

**Commit**：`feat(api): keep inbound raw source and compose RFC 5322 for older mail`

## Task 3：客户端密码服务、组织开关、成员功能与 HTTP 接口

**Files**：`api/internal/domain/member_feature.go`（`MemberFeatureMailClients = "mail_clients"`，并加进 catalog、`Default`、`All`、`Has`、`Patch`）、`api/internal/modules/organization/{model.go,context_repository.go,invitation_repository.go,member_feature_repository.go}`（`can_use_mail_clients`）、新建 `api/internal/domain/mail_client.go`、`api/internal/modules/mailbox/app_password_{model,repository,service,handler}.go` 及 `app_password_test.go`、`routes.go`、`errors.go`、`error_mappings.go`、`dto.go`、`api/internal/domain/error_codes.go`、`web/src/http/codes.ts`、`admin/src/http/codes.ts`、`api/internal/infra/config/config.go`（`Mailbox.AppPasswordPepper`）。

**Interfaces**
```go
// domain/mail_client.go
type MailAppPassword struct { ID, OrganizationID, MembershipID, UserID uint; Name string; ScopeIMAP, ScopeSMTP bool; Hint string; LastUsedAt *time.Time; LastUsedIP, LastUsedProtocol string; RevokedAt *time.Time; CreatedAt time.Time }
type MailClientProtocol string // "imap" | "smtp"
// mailbox
type CreateAppPasswordInput struct { Name string; Scopes []domain.MailClientProtocol; CurrentPassword, TOTPCode string }
func (s *service) ListAppPasswords(ctx context.Context, viewer domain.OrganizationContext) (*AppPasswordList, error) // items + access{enabled, reason} + server{imap_host, imap_port}
func (s *service) CreateAppPassword(ctx context.Context, viewer domain.OrganizationContext, input CreateAppPasswordInput) (*domain.MailAppPassword, string, error)
func (s *service) RevokeAppPassword(ctx context.Context, viewer domain.OrganizationContext, id uint) error
func (s *service) AppPasswordDirectory(ctx context.Context, viewer domain.OrganizationContext, page, size int) ([]*domain.MailAppPassword, int64, error) // owner/admin
func (s *service) ForceRevokeAppPassword(ctx context.Context, viewer domain.OrganizationContext, id uint) error
func (s *service) MailClientSettings(ctx context.Context, viewer domain.OrganizationContext) (bool, error)
func (s *service) UpdateMailClientSettings(ctx context.Context, viewer domain.OrganizationContext, enabled bool) (bool, error)
func (s *service) PurgeRevokedAppPasswords(ctx context.Context, before time.Time, limit int) (int, error)
func newAppPasswordSecret() (display string, normalized string) // 16 lowercase letters, crypto/rand, rejection sampling
func hashAppPassword(pepper []byte, normalized string) string     // hex HMAC-SHA256
func normalizeAppPassword(input string) string                     // strip spaces, lowercase
```
- 路由（`auth` + `organization_context`）：`GET/POST /v1/mail-app-passwords`、`DELETE /v1/mail-app-passwords/:password_id`、`GET /v1/mail-app-password-directory`（分页）、`DELETE /v1/mail-app-password-directory/:password_id`、`GET/PUT /v1/mail-client-settings`。POST 加上 `requireFeature(domain.MemberFeatureMailClients)` 和 `BodyLimit(2048)`。列表接口带 `// fastbuild:bounded-list max=20 reason=per-member app password cap`。
- 错误码（浏览器需要处理）：`MAILBOX.APP_PASSWORD_LIMIT_REACHED`（409）、`MAILBOX.APP_PASSWORD_NOT_FOUND`（404）、`MAILBOX.MAIL_CLIENT_ACCESS_DISABLED`（403）。模块内部：`ErrMailAppPasswordPepperMissing` → 503 `COMMON.SERVICE_UNAVAILABLE`。
- 用户审计事件：`mail_app_password.created` / `.revoked`，metadata 只含 `password_id`、`name_length`、`scopes`、`by_admin`。

**Steps**
1. 写失败测试：
   - `TestCreateAppPasswordReturnsSecretOnce`：secret 匹配 `^[a-z]{4}( [a-z]{4}){3}$`；数据库里只存 hash，hint 为后 4 位；之后的 List 不含 secret 字段（对 DTO 做 JSON 序列化后断言没有 `secret`）。
   - `TestCreateAppPasswordRequiresCurrentPasswordAndTOTP`：密码错误或缺 TOTP 时返回 422 加字段错误，第 6 次失败返回 429。
   - `TestCreateAppPasswordLimit`：第 21 个返回 409；撤销一个之后可以再建。
   - `TestCreateAppPasswordFeatureAndSwitch`：没有 `mail_clients` 功能 → 403 `ORGANIZATION.FEATURE_NOT_GRANTED`；组织开关关闭 → 403 `MAILBOX.MAIL_CLIENT_ACCESS_DISABLED`；owner/admin 始终有这项功能。
   - `TestRevokeAppPasswordOwnOnly`：撤销别人的密码返回 404；管理员通过 directory 可以撤销，`revoke_reason = admin`。
   - `TestAppPasswordDirectoryHidesSecretsAndContent`。
   - `TestHashAppPasswordNormalizes`：`"ABCD efgh IJKL mnop"` 与 `"abcdefghijklmnop"` 的 hash 相同。
   - `TestMemberFeatureCatalogIncludesMailClients`（domain 包）。
   - 组织模块现有的 feature 相关测试要同步更新。
2. 先补 domain、organization 的列和映射，再写 repository（创建时用 `SELECT … FROM organization_memberships WHERE id = ? FOR UPDATE` 锁住 membership，再数未撤销的密码）、service、handler、DTO 和错误映射。
3. `mail:broadcast:work` 的 steps 末尾加 `PurgeRevokedAppPasswords(now-90d)`。
4. `cd api && make wire`（如果 provider 有变化）；`go test ./internal/domain/... ./internal/modules/organization/... ./internal/modules/mailbox -run 'AppPassword|MemberFeature|MailClient'`；`make route-catalog-check`。

**Commit**：`feat(api): per-device mail app passwords, client access switch, mail_clients feature`

## Task 4：Web 设置页“邮件客户端”标签

**Files**：`web/src/app/(protected)/(console)/console/settings/page.tsx`（`settingsTabs` 加上 `'mail-clients'`，用 `MemberFeatureGate feature="mail_clients"` 控制显示，只在 `isWebFeatureEnabled('mailbox')` 时出现）、新建 `web/src/app/api/mail-app-passwords/route.ts`、`[passwordId]/route.ts`、`web/src/app/api/mail-client-settings/route.ts`、`web/src/features/mailbox/server/mailbox-route.ts`（代理条目）、`server/mock-mail-app-password-store.ts`、`mail-client-schemas.ts`、`hooks/use-mail-app-passwords.ts`、`components/mail-clients-tab.tsx`、`components/mail-client-guides.tsx`、`web/src/features/organization/{types.ts,schemas.ts,utils/member-features.ts}`、i18n `web/src/i18n/modules/{settings,mailbox,organization}/{zh-Hans,en-US}.ts`、测试 `web/src/test/mail-clients-tab-ui.test.tsx`。

**界面行为**
- 顶部是服务器参数卡（主机、端口 `993`/本地 `1993`、SSL/TLS、用户名为完整邮箱地址），每项可以一键复制，数据来自 List 的 `server` 字段。SMTP 一行标“即将支持”。
- 组织开关关闭或没有功能授权时，显示原因（“管理员已关闭邮件客户端访问”），并禁用“添加设备”。
- “我的客户端密码”列表：设备名、范围、创建时间、最近使用（相对时间）、“撤销”按钮（二次确认）。
- “添加设备”对话框：设备名 + 范围 + 当前密码 + （开了两步验证时）验证码 → 显示 `abcd efgh ijkl mnop` 和复制按钮，提示“关闭后无法再次查看”。关闭对话框时清空 state 和 React Query 缓存里的 secret。
- 分客户端步骤（Tabs）：iPhone/iPad、Mac 邮件、Outlook、Thunderbird、安卓 Gmail/三星、Foxmail。P1a 只写手动填写步骤，另外说明：iOS 不支持后台实时推送；新版 Outlook 会经微软云同步；共享邮箱位于 `Shared/` 下，P1a 是只读的。
- owner/admin 额外看到一张卡片“组织设置：允许成员使用邮件客户端”（Switch → `PUT /api/mail-client-settings`）。
- 组织成员页的功能开关列表多出“邮件客户端”（catalog 驱动，只需补 i18n 和类型）。

**Steps**
1. 写失败测试（Vitest + Testing Library，mock fetch）：
   - `shows the secret once and forgets it after closing`：关闭后 DOM 和 queryClient 中都找不到 secret。
   - `lists devices without secrets and revokes after confirmation`。
   - `explains when the organization turned client access off`。
   - `hides the tab without the mail_clients grant`（扩展 `settings-tabs.test.tsx`）。
   - `admin toggles organization access`。
2. 实现 BFF 路由（`privateMailboxResponse`，`Cache-Control: no-store`）、schema（zod，`secret` 只出现在 create 响应的 schema 里）、hook 和组件。
3. `cd web && corepack pnpm vitest run src/test/mail-clients-tab-ui.test.tsx src/test/settings-tabs.test.tsx && corepack pnpm type-check && corepack pnpm lint`。

**Commit**：`feat(web): mail clients settings tab with show-once app passwords`

## Task 5：IMAP 后端（认证、LIST/SELECT/FETCH/SEARCH/STORE，只读整理）

**Files**：`api/go.mod`/`go.sum`（`github.com/emersion/go-imap/v2 v2.0.0-beta.9`，按 `docs/DEPENDENCY_SECURITY.md` 记录评审）、`api/internal/domain/mail_client.go`（store 接口）、新建 `api/internal/modules/mailbox/mail_client_store.go`、`mail_client_store_test.go`、新建包 `api/internal/modules/mailbox/imapgateway/`：`server.go`、`session.go`、`names.go`、`fetch.go`、`search.go`、`limiter.go`、`source_cache.go`，以及 `*_test.go`、`integration_test.go`。

**Interfaces**
```go
// domain/mail_client.go
type MailClientPrincipal struct { PasswordID uint; Viewer OrganizationContext; RootMailboxID uint /*0 = none*/; Username string }
type MailClientFolder struct { MailboxID uint; Key string /*inbox|sent|archive|trash|folder:<id>*/; Name string; Shared bool; MailboxAddress string; SpecialUse string }
type MailClientFolderState struct { UIDValidity, UIDNext uint32; UIDs []uint32; Unseen int }
type MailClientMessage struct { UID uint32; MessageID uint; Seen, Flagged, Answered bool; InternalDate time.Time }
type MailClientSearch struct { UIDs []uint32 /*optional bound*/; Seen, Flagged *bool; Since, Before *time.Time; From, To, Cc, Subject, Body, Text []string }
type MailClientStore interface {
	AuthenticateClient(ctx context.Context, username, password string, protocol MailClientProtocol, ip string) (*MailClientPrincipal, error) // ErrMailClientAuthFailed only
	RevalidateClients(ctx context.Context, principals []*MailClientPrincipal) (map[uint]bool, error)
	ClientFolders(ctx context.Context, p *MailClientPrincipal) ([]MailClientFolder, error)
	ClientFolderState(ctx context.Context, p *MailClientPrincipal, mailboxID uint, key string) (*MailClientFolderState, error)
	ClientMessages(ctx context.Context, p *MailClientPrincipal, mailboxID uint, key string, uids []uint32) ([]MailClientMessage, error)
	ClientMessageSource(ctx context.Context, p *MailClientPrincipal, mailboxID uint, key string, uid uint32) ([]byte, error)
	SetClientFlag(ctx context.Context, p *MailClientPrincipal, mailboxID uint, key string, uids []uint32, flag MailClientFlag, value bool) error // MailClientFlagSeen | MailClientFlagFlagged
	SearchClient(ctx context.Context, p *MailClientPrincipal, mailboxID uint, key string, criteria MailClientSearch) ([]uint32, error)
}
// imapgateway
func NewServer(store domain.MailClientStore, cfg Config, logger *slog.Logger) *Server
func (s *Server) Serve(ctx context.Context, ln net.Listener) error // wraps imapserver.New(&imapserver.Options{NewSession, Caps, TLSConfig, Logger})
type session struct { conn *imapserver.Conn; principal *domain.MailClientPrincipal; selected *selection; tracker *imapserver.SessionTracker }
```
- 认证顺序（`AuthenticateClient`）：normalize → hash → `secret_hash` 唯一查找 → 未撤销 → 范围包含 imap → 组织开关打开 → `ResolveContext` 后 `CanUse(mail_clients)` → 把用户名解析成邮箱：先按地址匹配，有 `view` 就成为根目录；如果是账号邮箱，根目录取这个人的 personal/zmail 邮箱 → 节流更新 `last_used_*`（同一密码 5 分钟内最多写一次，IP 只保留 /24 或 /48）。任何一步失败都返回同一个 `domain.ErrMailClientAuthFailed`。
- 文件夹：根目录为 `INBOX`、`Sent (\Sent)`、`Archive (\Archive)`、`Trash (\Trash)` 和用户文件夹（R8）；共享邮箱为 `Shared/<address>/INBOX` 等。不出现 `origin = connected` 的邮箱，也不出现只有 `administer` 没有 `view` 的邮箱。NAMESPACE 为 `(("" "/")) NIL (("Shared/" "/"))`。
- 查询统一 join `mail_imap_uids` 和 `mail_messages`，套用现有的 `unsealedMessage` 条件，按 uid 排序；Unseen 只统计入站且当前 membership 没有 read 行的邮件。
- P1a 拒绝的命令：CREATE/DELETE/RENAME → `NO [NOPERM]`；APPEND/COPY/MOVE → `NO [CANNOT]`；EXPUNGE、UID EXPUNGE 和 STORE `+FLAGS`/`FLAGS` 带 `\Deleted` → `NO [CANNOT]`；`\Draft`、`\Answered` 和自定义 keyword 会被忽略。PERMANENTFLAGS 为 `(\Seen \Flagged)`。
- CAPABILITY：`IMAP4rev1 IMAP4rev2 NAMESPACE UIDPLUS SPECIAL-USE LIST-STATUS ID ENABLE UTF8=ACCEPT AUTH=PLAIN`。不声明 IDLE、MOVE，这两项留到 P1c/P1b。
- 序号管理：每个会话 SELECT 时建一个 `imapserver.MailboxTracker`；NOOP/Poll 时重新取 UID 列表并做差，新增的调用 `QueueNumMessages`，消失的调用 `QueueExpunge`。跨会话共享 tracker 留到 P1c。
- FETCH：`UID FLAGS INTERNALDATE RFC822.SIZE ENVELOPE BODYSTRUCTURE BODY[...]/BODY.PEEK[...]/BINARY[...]`，用 imapserver 的 `Extract*` 辅助函数从原文生成（以 beta.9 实际导出的为准）。原文先查 `mail_message_sources`，没有就用 `composeMessageSource` 合成，放进 64 MiB LRU。非 PEEK 的 BODY 在 SELECT（非 EXAMINE）下调用 `SetClientFlag(seen)`。
- SEARCH：映射到 `MailClientSearch`。地址和主题条件用 `ILIKE`（走现有 trgm 索引），BODY/TEXT 作用于 `text_body`，结果最多 10000 条。OR/NOT 嵌套超过 4 层返回 `NO [LIMIT]`。
- 限流：`limiter.go` 封装 `ratelimit.Stores`，桶为 `mailclient:auth:ip`（60/分钟）、`mailclient:fail:ip`（20/15 分钟）、`mailclient:fail:user:<sha256[:16]>`（10/15 分钟），另有进程内的“24 小时内成功登录过的 (user, ip)”LRU（上限 10000 条）和封禁表。失败后固定 `time.Sleep(1s)`，测试里可以注入。

**Steps**
1. 写失败单元测试：
   - `TestFolderNamesEscapeAndDeduplicate`。
   - `TestFolderKeyForPlacement`：Go 侧的映射和 SQL 函数结果一致，用表驱动测试，并在集成测试里与 SQL 对照。
   - `TestSearchCriteriaTranslation`（嵌套过深返回 LIMIT）。
   - `TestLimiterLocksIPAndUserIndependently`（成功过的 IP 不受用户名桶影响；封禁期间密码正确也拒绝）。
   - `TestSourceCacheBounded`。
2. 写失败的 store 测试（disposable PostgreSQL，真实 service）：
   - `TestAuthenticateClientUniformFailure`：错误密码、已撤销、只有 smtp 范围、别人的地址、组织关闭、功能被取消，返回的错误都是同一个值。
   - `TestClientFoldersFollowMailboxAccess`：没有 view 的共享邮箱不出现；connected 邮箱不出现；以共享邮箱地址登录时根目录就是它。
   - `TestClientFolderStateMatchesWebCounts`：与 `ListMessages` / `MessageCounts` 对比。
   - `TestSetClientFlagIsPerMember`：成员 A 标为已读，成员 B 的 unseen 不变，网页 `GET` 的 `read` 为 true。
3. 写失败的协议集成测试 `integration_test.go`：测试内生成自签证书，`tls.Listen` 到 `127.0.0.1:0`，客户端用 `imapclient.DialTLS` 连接，`InsecureSkipVerify` 只在测试里打开。
   - `TestIMAPLoginListSelectFetch`：LOGIN 成功，LIST 含 `Shared/support@…/INBOX`，SELECT INBOX 的 EXISTS 与网页一致，`UID FETCH 1:* (BODY.PEEK[])` 的结果与 inbound 原文逐字节相同，旧邮件带 `X-Zmail-Reconstructed`。
   - `TestIMAPAuthFailureIsGeneric`。
   - `TestIMAPStoreSeenFlagged`。
   - `TestIMAPReadOnlyCommandsRefused`。
   - `TestIMAPUIDStableAcrossWebMove`：网页把邮件归档后，NOOP 收到 EXPUNGE；SELECT Archive 时新 uid ≥ 移动前 Archive 的 UIDNEXT；UIDVALIDITY 不变。
   - `TestIMAPVerificationCodeNotMasked`：原文里的 6 位验证码原样出现。
4. 实现 store → 适配层 → server，直到测试通过。
5. `cd api && go test ./internal/modules/mailbox/imapgateway/... ./internal/modules/mailbox -run 'Client|IMAP'`，然后 `bash ../.agents/skills/verification-before-completion/scripts/run-tiers.sh 1 ./internal/modules/mailbox/...`。

**Commit**：`feat(api): read-only IMAP gateway backend on go-imap v2 beta.9`

## Task 6：`mail:gateway` 命令、配置、会话撤销 sweep

**Files**：`api/internal/infra/config/config.go`（新建 `MailGatewayConfig`）及其测试、`api/docs/CONFIGURATION.md`、`api/.env.example`、新建 `api/internal/bootstrap/operatorcommands/mail_gateway.go` 和 `mail_gateway_test.go`、`manifest.go`（注册）、`api/internal/app`（`Application.MailClients domain.MailClientStore`）、wiring provider、`imapgateway/registry.go`、`imapgateway/tls.go`、`imapgateway/health.go`，以及各自的测试。

**Interfaces**
```go
type MailGatewayConfig struct {
	IMAPAddr           string        // MAIL_GATEWAY_IMAP_ADDR; "" disables; .env.example: 127.0.0.1:1993
	TLSCertFile        string        // MAIL_GATEWAY_TLS_CERT_FILE
	TLSKeyFile         string        // MAIL_GATEWAY_TLS_KEY_FILE
	HealthAddr         string        // MAIL_GATEWAY_HEALTH_ADDR, default 127.0.0.1:18992
	PublicIMAPHost     string        // MAIL_GATEWAY_PUBLIC_IMAP_HOST (shown in settings), default imap.zmail.im
	PublicIMAPPort     int           // MAIL_GATEWAY_PUBLIC_IMAP_PORT, default 993
	MaxConnections     int           // 1000
	MaxPerUser         int           // 20
	MaxPerIP           int           // 100
	RevalidateInterval time.Duration // MAIL_GATEWAY_REVALIDATE_INTERVAL, default 20s, max 30s
}
func (c MailGatewayConfig) Enabled() bool         // addr && cert && key
func (c MailGatewayConfig) Validate() error       // partial config, interval > 30s, missing MAIL_APP_PASSWORD_PEPPER
type MailGatewayCommand struct{ output *console.Output } // Name() "mail:gateway"; Usage() "mail:gateway [--once-check]"
type sessionRegistry struct { mu sync.Mutex; byID map[uint64]*liveSession; byUser map[uint]int; byIP map[string]int }
func (r *sessionRegistry) Sweep(ctx context.Context, store domain.MailClientStore) (closed int, err error)
func newReloadingCertificate(certFile, keyFile string, checkEvery time.Duration) (*reloadingCertificate, error) // GetCertificate re-reads on mtime change
```
- 命令流程：`initMailboxApplication` → `cfg.MailGateway.Enabled()` 为 false 时输出 “mail gateway disabled: set MAIL_GATEWAY_IMAP_ADDR, MAIL_GATEWAY_TLS_CERT_FILE, MAIL_GATEWAY_TLS_KEY_FILE” 并退出 0 → `Validate` → 加载证书 → 监听 → 启动 sweep goroutine 和 health HTTP（`/health` 加 Prometheus `/metrics`）→ 收到 SIGTERM 后先停止 accept，向所有会话发 `BYE Server shutting down`，最多等 10 秒。
- Sweep：每隔 `RevalidateInterval` 收集所有活跃 principal，调用 `RevalidateClients`（一条查询批量检查密码有效、组织开关、`can_use_mail_clients`/role、membership 是否存在），无效的会话发 `BYE [ALERT] Access revoked` 并关闭；有效的会话重新计算可见邮箱集合，已选中的邮箱不再可见时同样 BYE。连续失败时间超过 `60s - RevalidateInterval` 就关闭全部会话（fail closed）。
- 指标：`mail_gateway_connections{protocol}`、`mail_gateway_auth_total{result}`、`mail_gateway_sweep_closed_total{reason}`、`mail_gateway_command_seconds{command}`。

**Steps**
1. 写失败测试：
   - `TestMailGatewayConfigDefaultsAndValidation`：没配置时禁用；只配了 cert 没配 key 报错；间隔大于 30 秒报错；启用了网关但没有 pepper 报错。
   - `TestMailGatewayCommandRegistered`：manifest 里有这个命令。
   - `TestMailGatewayDisabledExitsCleanly`。
   - `TestSessionRegistryLimits`：每用户第 21 个、每 IP 第 101 个连接被拒，返回 `BYE [UNAVAILABLE]`。
   - `TestSweepClosesRevokedSessions`：用 fake store 覆盖撤销、组织关闭、功能取消、membership 删除、选中的共享邮箱失权五种情况。
   - `TestSweepFailsClosed`。
   - `TestReloadingCertificatePicksUpNewFile`。
   - 集成测试 `TestIMAPRevokeDisconnectsWithinInterval`：interval 设为 200ms，网页 DELETE 密码后，客户端连接在 1 秒内被关闭，再次 LOGIN 失败。
   - 集成测试 `TestIMAPOrgSwitchDisconnects`。
   - `TestGatewayLogsContainNoSecrets`：捕获 slog 输出，断言里面没有密码、主题、地址、用户名。
2. 实现配置、registry、TLS、health、命令和 wiring（`cd api && make wire`）。
3. `go test ./internal/infra/config/... ./internal/bootstrap/operatorcommands/... ./internal/modules/mailbox/imapgateway/...`；`cd api && make route-catalog-check`（确认没有新增 HTTP 路由）。

**Commit**：`feat(api): mail:gateway command with TLS, limits, and revocation sweep`

## Task 7：本地端到端验证流程（不碰服务器和 DNS）

只做验证，不提交产品代码。如果需要可复用的脚本，放到 `api/scripts/mail-gateway-smoke.sh` 并随 Task 6 一起提交。

1. 证书：`openssl req -x509 -newkey rsa:2048 -nodes -days 7 -subj "/CN=localhost" -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" -keyout $SCRATCH/gw.key -out $SCRATCH/gw.crt`（`$SCRATCH` 指会话 scratchpad 目录）。
2. 本地 API：先用一次性 PostgreSQL 跑 `go run ./cmd/fastbuild migrate`，再启动 `SERVER_PORT=18098 MAILBOX_INBOUND_SECRET=dev-secret MAIL_APP_PASSWORD_PEPPER=$(openssl rand -hex 32) OPTIONAL_STARTERS=…,mailbox go run ./cmd/server`。
3. 网关：用同一组环境变量加上 `MAIL_GATEWAY_IMAP_ADDR=127.0.0.1:1993 MAIL_GATEWAY_TLS_CERT_FILE=$SCRATCH/gw.crt MAIL_GATEWAY_TLS_KEY_FILE=$SCRATCH/gw.key MAIL_GATEWAY_PUBLIC_IMAP_PORT=1993`，运行 `go run ./cmd/fastbuild mail:gateway`。
4. 准备数据：登录 → 建组织和个人邮箱 → 再建一个共享邮箱并加入成员 → `POST http://127.0.0.1:18098/v1/mail-app-passwords` 拿到 secret → 用 `t=<unix>,v1=<hex HMAC-SHA256(secret, "<t>.<recipient>.<raw>")>` 签名，通过 `POST /v1/inbound-mail-messages` 投递一封带附件和验证码的邮件（`X-Mailbox-Recipient`、`X-Mailbox-Signature` 两个头）。
5. 协议检查：
   - `openssl s_client -connect 127.0.0.1:1993 -quiet -CAfile $SCRATCH/gw.crt`，手动输入 `a LOGIN alice@mail.localhost "abcd efgh ijkl mnop"`、`b LIST "" "*"`、`c SELECT INBOX`、`d UID FETCH 1:* (FLAGS BODY.PEEK[])`、`e STORE 1 +FLAGS (\Seen)`、`f CREATE x`（期望 NO）、`g LOGOUT`。
   - `curl --cacert $SCRATCH/gw.crt --user 'alice@mail.localhost:abcdefghijklmnop' 'imaps://127.0.0.1:1993/INBOX;UID=1'`，输出应与投递的原文一致。
   - go-imap 客户端脚本（放在 scratchpad，`go run`）：列出所有文件夹的 EXISTS/UNSEEN，与 `GET /v1/inbox-message-counts` 对比。
6. 撤销检查：保持一个 `openssl s_client` 会话处于空闲状态，在网页上撤销密码，60 秒内应收到 `* BYE`；关闭组织开关后重复一次。
7. 锁定检查：循环 21 次错误密码，第 21 次之后正确密码也被拒；换 `127.0.0.2` 源地址（`-bind`）登录不受影响。
8. 日志检查：`grep -iE 'alice@|subject|abcd' gateway.log` 应该没有结果。

---

## 后续（P1a 之后，单独出计划）

- **P1b SMTP 提交与整理**：`go-smtp v0.26.0`，465/587；校验 From 必须是有 `send` 权限的邮箱；给 `SendInput`/`mailtransport.Message` 增加 bcc 和 `bcc_addresses` 列；MIME 转 `SendInput` 时保留 cid 内联图片；幂等键为 `smtp:`+sha256；`service.send` 结果到 SMTP 应答码的映射；APPEND 到 Sent 去重；MOVE、`\Deleted`/EXPUNGE、COPY 到 Trash；`mail_message_sources.origin = smtp/composed`。
- **P1c IDLE + 一键配置 + Drafts**：单连接 `LISTEN mail_imap`/`mail_client_auth`，触发器里调用 `pg_notify`，跨会话共享 `MailboxTracker`，每 30 秒兜底轮询；`.mobileconfig`、autoconfig、autodiscover；`mail_client_drafts`；`mail_client_sessions` 审计表和 `.first_used` 事件；运维命令 `mail:gateway:sessions` / `mail:gateway:kick`。
- **P2**：CONDSTORE/QRESYNC（新增 modseq 列和 reads/stars 触发器）、COMPRESS、标签映射为 keyword、Managed Zones 写 SRV 记录、`masked` 内容策略、原文里的附件去重或迁到对象存储、外部接入邮箱只读暴露。
- **部署**（需要负责人按 spec §6.6 拍板）：compose 服务 `mail-gateway`、DNS `imap.zmail.im`、FE5 开放 993 端口、证书、调高 `zmail_app` 的连接上限。这些都不在本计划范围内。
