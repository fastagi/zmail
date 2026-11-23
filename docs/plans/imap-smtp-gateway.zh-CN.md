# IMAP/SMTP 网关：产品与技术方案

2026-11-23 · 草案，待负责人拍板
上游文档：[external-accounts-and-clients.zh-CN.md](external-accounts-and-clients.zh-CN.md) §4.2「B：客户端」中的 IMAP + SMTP 网关（该文档排为 P2，本文按"客户端 P1"展开）。
代码依据：`origin/main` `4a1d3105`（本工作树落后于 main，引用的文件以 main 为准）。
本文只是方案，不包含代码、DNS、端口或部署变更。

## 0. 一页结论

- **做什么**：员工用系统自带邮件 App（iPhone 邮件、Apple Mail、Outlook、Thunderbird、Foxmail、安卓 Gmail App 等）收发自己的 Zmail 邮箱。
- **怎么做**：在 API 镜像里新增一个 Go 进程 `mail:gateway`，以独立 compose 服务运行，提供两部分能力：
  - IMAP4rev1/rev2（993，隐式 TLS），基于 `emersion/go-imap/v2` 的 `imapserver`，后端直接读写 Zmail 的 PostgreSQL 邮件库；
  - SMTP 提交（465 隐式 TLS、587 STARTTLS），基于 `emersion/go-smtp`，复用现有发信路径 `service.send`，额度和审计与网页发信相同。
- **认证**：只接受按设备生成的**应用专用密码**（可选 IMAP、SMTP 两种范围，只显示一次，库里只存哈希，可单独撤销）。提供组织级开关和成员功能授权；登录失败按 IP 和账号限流。
- **可见范围**：和网页完全一致，由 `domain.MailboxAccessFor` 决定。个人邮箱放在根目录，共享邮箱放在 `Shared/` 命名空间下。
- **验证码**：IMAP 下发**完整原文**，不遮罩（理由见 §5.6）。管理员可以按组织关闭客户端访问。
- **需要负责人拍板**：新增 `imap.zmail.im`、`smtp.zmail.im` 的 DNS 记录；FE5 开放 993、465、587 端口；证书签发方式；提高共享 PostgreSQL 的连接数上限；保存原始邮件带来的存储增长。完整清单见 §6.6。

---

## 1. 用户场景、目标与非目标

### 1.1 场景

| 角色 | 场景 | 期望 |
|---|---|---|
| 员工 | 在 iPhone 自带"邮件"里加上 `alice@zgi.app`，在通知中心看到新邮件 | 2 分钟内完成添加；新邮件 1 分钟内出现（P1c 加入 IDLE 后在 10 秒内） |
| 员工 | 在 Outlook 桌面版（Windows/Mac）中和其他邮箱一起处理 Zmail 邮件，回复带附件 | 发出的邮件出现在网页版"已发送"中，计入组织额度 |
| 员工（共享邮箱成员） | 在 Thunderbird 里同时看个人邮箱和 `support@` | 共享邮箱单独成组；已读、星标只影响自己，和网页一致 |
| 员工 | 手机丢了 | 在网页上撤销这台手机的密码，1 分钟内断开，其他设备不受影响 |
| 管理员（owner/admin） | 出于合规考虑只允许网页访问，或只给部分成员开放客户端 | 组织级开关 + 成员功能授权；能看到谁建了几个客户端密码、最近使用时间（看不到信件内容） |
| IT | 批量给员工配好 Mac 和 iPhone | `.mobileconfig` 描述文件、Thunderbird 自动配置、Outlook Autodiscover；有一页可以直接发给员工的配置说明 |

### 1.2 目标

1. 覆盖主流客户端：iOS/macOS 邮件、Outlook（经典版 Windows、Mac、iOS/Android）、新版 Outlook for Windows、Thunderbird、Foxmail、安卓 Gmail App（"其他"账户）、三星邮件。
2. 收件、整理（已读、星标、移动、删除）、发件三件事和网页语义一致：权限、额度、审计都不能绕开。
3. 安全默认值：只走 TLS；只接受应用专用密码；可撤销；限流；完整审计。
4. 运维成本低：同一个镜像、同一个数据库，不引入第二套邮件存储。

### 1.3 非目标（v1 不做）

- **对外收信（MX/SMTP 25 端口）**：收信仍走 Cloudflare Email Routing → Worker → `POST /v1/inbound-mail-messages`，网关不接收外部投递。
- POP3、Exchange ActiveSync/EWS/MAPI、CalDAV、CardDAV、JMAP。
- IMAP 上的 OAuth2（XOAUTH2/OAUTHBEARER）：要求客户端预置 Zmail 的 client_id，主流客户端都做不到，见 §3.3。
- 通过 IMAP 管理文件夹（CREATE/RENAME/DELETE）：v1 返回 `NO [NOPERM]`，文件夹只在网页上管理（P2 再评估）。
- 通过 APPEND 把旧邮件导入 INBOX 或自定义文件夹：v1 拒绝，导入走现有的导入功能。
- 外部接入邮箱（`origin = connected`）的 IMAP 暴露：它们只是外部账号的副本，员工的客户端可以直接连原账号。
- 原生 App 和 Web Push：另见 PWA 方案（`docs/plans/web-push-pwa.plan.md`）。

---

## 2. 竞品做法

| 产品 | 客户端协议 | 认证方式 | 管理员控制 | 一键配置 |
|---|---|---|---|---|
| Fastmail | IMAP、SMTP、POP、CalDAV、CardDAV；自家 App 用 JMAP[^fm-jmap] | 只接受应用专用密码，每个应用单独一个，可以限定范围（仅邮件、仅日历等），能看到最近一次使用的时间和 IP[^fm-app][^fm-blog] | Basic 套餐不支持 IMAP/SMTP[^fm-app] | iOS 描述文件[^fm-ios] |
| Google Workspace | IMAP、SMTP、POP | 自 2025-05-01 起停用"不够安全的应用"，只剩 OAuth 和应用专用密码[^g-lsa] | 管理员可以按组织单元开关 IMAP/POP[^g-imap] | 客户端内置 Google 登录（OAuth） |
| Microsoft 365 | IMAP、POP、SMTP AUTH、EAS、MAPI | IMAP/POP 的基本认证已于 2022-10 永久停用[^ms-basic]；SMTP AUTH 的基本认证暂缓，计划 2026 年底对存量租户默认关闭[^ms-smtp] | 每个邮箱单独开关协议 | Autodiscover |
| Zoho Mail | IMAP、POP、SMTP | 开启两步验证或 SAML 后必须用应用专用密码（按设备命名）[^zoho-asp] | 组织邮件策略控制是否允许 IMAP；免费版没有 IMAP；连接数过多会被暂停[^zoho-imap] | 管理后台提供 Autodiscovery 配置[^zoho-ad] |
| 腾讯企业邮 | IMAP、POP、SMTP、Exchange | 开启"安全登录"后只能用**客户端专用密码**[^exmail][^ali-3p] | 管理员策略决定是否允许第三方客户端 | 无 |
| 飞书邮箱 | `imap.feishu.cn`、`smtp.feishu.cn` | **三方客户端安全密码**：只能在 PC 端生成，可以建多个、互不影响，管理员可以强制启用[^fs][^ali-fs] | 管理后台 → 邮箱 → 用户功能权限 → 第三方邮箱客户端[^ali-fs] | 无 |
| 网易企业邮 / 163 | IMAP 993、SMTP 465 | **客户端授权密码**，只显示一次，丢了只能重置[^ali-163] | 管理后台可以强制二次验证 | 无 |

结论：

1. 国内企业邮箱（腾讯、飞书、网易）的做法一致，都是"管理员开关 + 员工自助生成客户端专用密码"。Fastmail 是做得最细的：按设备生成、可限定范围、能看到最近使用情况。**Zmail 照此做，并补上 Fastmail 的"最近使用时间和 IP"，以及撤销后立即断开。**
2. Google 和微软推 OAuth 有一个前提：他们自己是身份提供方，客户端里内置了他们的 OAuth 应用。Zmail 没有这个条件，应用专用密码是唯一现实的选择（见 §3.3）。
3. 只有 Zoho 和微软提供了 Autodiscover 一类的一键配置；国内三家都要手填。一键配置是 Zmail 可以做出差异的地方。

---

## 3. 方案取舍

### 3.1 现状（代码事实）

| 方面 | 现状 | 对网关的影响 |
|---|---|---|
| 邮件存储 | `mail_messages`：`direction`、`placement`（`default`/`archive`/`folder`/`trash`）、`folder_id`、`trashed_at`、`thread_id`、`rfc_message_id`、`references_header`、`text_body`/`html_body`（各 ≤256 KiB）、`source`（zmail/gmail/outlook）、`send_lane` 等 | IMAP 文件夹可以由 placement 推导出来 |
| **原始邮件** | **不保存**。入站在 `mime.go` 的 `parseMail` 中解析后只留首个 text、首个 html 和附件；Reply-To、Date 原值、DKIM、自定义头全部丢失 | IMAP 的 `BODY[]` 需要 RFC 5322 原文，必须补存储（§5.5） |
| 附件 | `mail_attachments.content` bytea，每封最多 10 个，合计 ≤4 MiB；入站原文 ≤5 MiB | 可以用来重新合成 MIME |
| 已读、星标 | `mail_message_reads`、`mail_message_stars`，按 `membership_id` 区分，**每人一份**；外发邮件始终为已读 | 直接对应 `\Seen`、`\Flagged`，天然是每个登录用户各自一份 |
| 文件夹、标签 | `mail_folders`、`mail_labels`、`mail_message_labels`；属于邮箱，所有人共享；平铺，每个邮箱最多 100 个 | 文件夹映射为 IMAP 文件夹；标签 v1 不暴露（P2 做成 keyword） |
| 草稿 | `mail_drafts`：每人一份，结构化字段（to/cc/subject/body），附件留在浏览器 | 与 IMAP Drafts 的 MIME 模型不兼容（§5.4） |
| 权限 | `domain.MailboxAccessFor(mailbox, viewer, member)` 返回 `view`/`send`/`organize`/…；`mailboxes.type` 为 personal/shared，`origin` 为 zmail/connected；`sealed_before` 之前的历史对新主人隐藏 | 网关每次选中文件夹、每次写操作都通过同一个函数判断 |
| 发信 | `service.SendMessage` → `service.send(ctx, mailbox, input, queueAtLimit)`；按个人通道 24 小时滚动额度；满额排队（`queued_reason = send_limit`），由 `mail:broadcast:work` 的 `dispatchOrganizationQueue` 补发；退信率过高时暂停（`MAILBOX.SENDING_PAUSED`）；`SendInput` 只有 to/cc（≤50），**没有 bcc** | SMTP 提交解析 MIME 后交给 `service.send`；bcc 需要补（§5.7） |
| 遮罩 | `secret_mask.go`：`maskSecretsInText`、`maskSecretsInHTML`；`POST .../reveal` 记 `mail.secret_revealed` | 需要决定 IMAP 是否遮罩（§5.6） |
| 现有 IMAP | `external_mail_imap.go` 是一个**用标准库手写的 IMAP 客户端**，`external_mail_smtp.go` 用 `net/smtp`；`api/go.mod` **没有** emersion 系列依赖 | 网关要引入新依赖，需要按 `docs/DEPENDENCY_SECURITY.md` 评审 |
| 限流 | `internal/infra/ratelimit/limiter.go`，PostgreSQL 表 `rate_limit_buckets`（也可用 Redis） | 登录限流直接复用 |
| 进程模型 | 同一镜像通过 operator 命令运行 worker（`internal/bootstrap/operatorcommands/mail_*.go`），compose 中有 api、web、broadcast-worker、webhook-worker | 新增 `mail:gateway` 命令和 `mail-gateway` 服务 |
| 生产资源 | FE5：数据库角色 `zmail_app` 连接上限 4（API 池 2，每个 worker 1）；OpenResty 反代 `127.0.0.1:18991` | **连接数是硬约束**（§6.4） |

### 3.2 候选方案

| 方案 | 做法 | 优点 | 缺点 |
|---|---|---|---|
| **A. 自建 Go 网关（推荐）** | `go-imap/v2` `imapserver` + `go-smtp`，后端直接读写 PostgreSQL；发信调用 `service.send` | 单一数据源；权限、额度、遮罩、审计共用同一份代码；已读和星标天然按人区分；部署只多一个容器 | 要自己实现 UID、通知、MIME 合成；`go-imap/v2` 仍是 beta（v2.0.0-beta.9，2026-11-12）[^goimap] |
| B. Dovecot + Postfix 同步 | Dovecot 存 Maildir，Zmail 双向同步；Postfix 做 submission 后回灌 | 协议兼容性最好，IDLE、CONDSTORE、QRESYNC 全都有 | 两份存储，双向同步冲突（已读每人一份 vs Dovecot 每个账号一份；共享邮箱的移动要回写）；Postfix 发信会绕过 `service.send` 的额度和暂停逻辑，除非再做 milter；镜像和运维成本翻倍；FE5 是共享机器 |
| C. 只做 JMAP | 实现 RFC 8620/8621 | 协议现代，适合自家 App | 主流系统邮件 App 都不支持 JMAP[^fm-jmap]，解决不了"用 iPhone 邮件"这个需求 |
| D. Dovecot 加 Lua/SQL 驱动直读 PostgreSQL | 用 Dovecot 当协议前端 | 协议成熟 | Dovecot 的存储插件要写 C 或复杂的 Lua，UID、标记、命名空间仍要自己映射；多一个语言栈 |

**推荐 A**，理由：

1. **权限一致**是硬性要求（见 `enterprise-flows-must-work`：隔离、按成员授权）。只有 A 能让 IMAP 的每一次访问都经过 `MailboxAccessFor`，发出的每一封信都经过 `service.send` 的额度、暂停和审计。
2. **已读每人一份**与 IMAP 每个登录用户一份 `\Seen` 的模型正好一致，不需要同步。
3. 部署：同一个镜像加一个服务，FE5 上不需要新增 Postfix 或 Dovecot 这类长期维护的组件。
4. beta 风险可控：`imapserver` 已经提供 `Session`、`SessionIMAP4rev2`（NAMESPACE、MOVE）、`SessionIdle`、`MailboxTracker`/`SessionTracker`（处理多会话的序号和 EXPUNGE 推送），以及参考实现 `imapmemserver`[^goimap]。依赖锁定到具体版本，用兼容性矩阵（§8）兜底。

### 3.3 为什么不做 IMAP OAuth

XOAUTH2 要求客户端认识 Zmail 的授权服务器。iOS 邮件、Outlook、Thunderbird 只内置了 Google、微软、Yahoo 等少数几家，Thunderbird 也只认它内置列表中的提供方。自建身份提供方无法让这些客户端弹出 Zmail 登录页。所以 v1 只用应用专用密码，这也和腾讯、飞书、网易、Fastmail 的做法一致。

---

## 4. 认证与授权

### 4.1 应用专用密码（客户端密码）

新表 `mail_client_passwords`：

| 列 | 说明 |
|---|---|
| `id` | 主键 |
| `organization_id`、`membership_id`、`user_id` | 密码属于"某人在某组织"：一个用户在多个组织时各自分开 |
| `name` | 设备名，用户填写，例如"Alice 的 iPhone"，1–64 个字符 |
| `scopes` | `imap`、`smtp`，至少一个；默认两个都选 |
| `secret_hash` | `HMAC-SHA256(pepper, secret)`，唯一索引；pepper 来自新环境变量 `MAIL_CLIENT_PASSWORD_PEPPER` |
| `hint` | 末 4 位，用于在列表中辨认 |
| `created_at`、`last_used_at`、`last_used_ip`（只到 /24 或 /48）、`last_used_protocol` | 写入做节流，每个连接每 5 分钟最多写一次 |
| `revoked_at`、`revoked_by`、`revoke_reason` | 撤销后保留 90 天供审计，之后清除 |

规则：

- 格式为 16 个小写字母，分 4 组：`abcd efgh ijkl mnop`。手机上好输，约 75 bit 熵。输入时忽略空格和大小写。熵足够高，所以用快速 HMAC 就够了，不需要 argon2（与 `contracts/API_KEYS.md` 中"只存 SHA-256"的思路一致）。
- **明文只显示一次**：创建接口返回明文，界面复制后清掉；之后的读取一律不含明文。
- 每人每组织最多 20 个。
- 创建前要求**近期登录验证**：10 分钟内输过密码，开了两步验证的还要输 TOTP（复用 `contracts/AUTHENTICATION.md` 的两步验证）。原因是客户端密码会绕过两步验证。
- 用户名：完整的邮箱地址（个人或共享邮箱地址），兼容各客户端"用户名 = 邮箱"的默认行为；也可以用账号邮箱。
- 校验流程：先按 `secret_hash` 找到密码行；再确认 `revoked_at` 为空、范围包含当前协议、组织开关打开、成员有 `mail_clients` 功能、membership 仍有效；然后按用户名解析出邮箱，用 `MailboxAccessFor` 判断该成员是否有 `view`；最后确定登录后能看到的邮箱集合（§5.2）。任何一步失败都返回同一个 `NO [AUTHENTICATIONFAILED]` 或 `535 5.7.8`，不泄露是哪一步失败。

接口（新增到 `contracts/MAILBOXES.md` 的"Mail Clients"一节）：

- `GET /v1/mail-client-passwords`：列出自己的密码。
- `POST /v1/mail-client-passwords`：参数 `{ name, scopes }`，返回 `{ ..., secret }`，只此一次。
- `DELETE /v1/mail-client-passwords/:id`：撤销自己的密码。
- `GET /v1/organizations/:id/mail-client-passwords`：owner/admin 查看全组织的密码列表，只含设备名、成员、范围、最近使用时间，**不含明文，也没有任何信件内容**。
- `DELETE /v1/organizations/:id/mail-client-passwords/:id`：管理员强制撤销。

### 4.2 组织策略与功能授权（目标 D）

- **组织开关** `mail_client_access`：`enabled` 或 `disabled`，放在组织设置里，只有 owner/admin 能改。关闭后：现有密码保留但全部不可用，所有连接在 60 秒内断开，网页上的设置页显示"管理员已关闭客户端访问"。**默认值需要拍板**（§6.6）。建议新组织默认开启，与 Google、腾讯一致；企业租户可以一键关闭。
- **成员功能授权**：在 `ORGANIZATIONS.md` 的功能目录（`broadcasts`、`contacts`、`templates`、`api_keys`）后新增第五项 `mail_clients`。owner/admin 始终拥有。邀请默认给 `mail_clients`，因为它不涉及批量发信，发信仍受个人通道额度约束，和 `contacts`、`templates` 同级。没有这项功能时，创建密码返回 `403 ORGANIZATION.FEATURE_NOT_GRANTED`，已有密码登录失败。
- 变更立即生效：撤销、关闭开关、去掉功能、移出组织、从共享邮箱移除，都通过 `pg_notify('mail_client_auth', ...)` 通知网关。网关对受影响的会话重新校验：失去全部权限就断开，只失去某个共享邮箱就把它从 LIST 中去掉，已选中的直接 `BYE`。即使通知丢失，每个会话也会每 60 秒兜底重新校验一次。**保证撤销后 1 分钟内断开。**

### 4.3 防暴力破解与限流

复用 `internal/infra/ratelimit`，新增以下桶：

| 桶 | 阈值 | 超出时 |
|---|---|---|
| `mailclient:fail:ip:<ip>` | 每 15 分钟 20 次失败 | 该 IP 封 15 分钟，连接直接 `BYE` 或 `421` |
| `mailclient:fail:user:<username>` | 每 15 分钟 10 次失败 | 该用户名封 15 分钟（只对未知 IP 生效，见下） |
| `mailclient:auth:ip:<ip>` | 每分钟 60 次尝试（含成功） | 拖慢 |
| 每次失败 | 固定延迟 1 秒再响应 | — |

- 已经用这个用户名成功登录过的 IP（24 小时内）不受用户名桶影响，避免攻击者把正常员工锁在外面。
- 新版 Outlook for Windows 会**通过微软云**同步 IMAP 账号：凭据保存在微软的服务器上，连接从微软的 IP 发起[^ms-cloud]。所以按 IP 的阈值不能太低，失败计数以"IP + 用户名"为主。
- 认证前 60 秒超时；明文端口不提供；`LOGINDISABLED` 只出现在没有 TLS 的连接上（实际不会有，因为 993、465 是隐式 TLS，587 必须先 STARTTLS）。

### 4.4 审计

- 用户审计（`contracts/AUDIT.md`）新增事件：`mail_client_password.created`、`.revoked`（含管理员强制撤销）、`.first_used`。
- 新表 `mail_client_sessions`：`password_id`、`protocol`、粗化后的 IP、开始和结束时间、退出原因（logout/revoked/policy/timeout）、读取字节数、发送封数。保留 90 天，**不记录任何主题、正文或收件人**。
- 共享邮箱的 `mailbox_access_events` 不按登录写（太多）；但第一次通过客户端访问某个共享邮箱时写一条 `client_access_started`，`detail` 为 `{ password_id }`。
- SMTP 发出的信沿用发信日志和 webhooks，`mail_messages` 上新增 `submitted_via = 'smtp'`，方便排查。

---

## 5. 概念映射

### 5.1 登录后的邮箱集合

- 用户名是个人邮箱地址，或账号邮箱：根目录是该成员的 `type = personal`、`origin = zmail` 邮箱；`Shared/<地址>/…` 下列出所有对其有 `view` 的共享邮箱。
- 用户名是共享邮箱地址（且有 `view`）：根目录就是这个共享邮箱。适合"把 support@ 单独加成一个账户"，这样客户端发信默认用 support@ 的身份。
- 不暴露：`origin = connected` 的邮箱（§1.3）；owner/admin 只有 `administer` 没有 `view` 的邮箱（和网页一致）。
- `NAMESPACE`：个人命名空间 `""`，共享命名空间 `"Shared/"`，分隔符 `/`。命名空间名用 ASCII，避免老客户端的 modified UTF-7 兼容问题；文件夹名中的中文按 RFC 3501 modified UTF-7 编码，客户端声明 `UTF8=ACCEPT` 时直接用 UTF-8。

### 5.2 文件夹

| IMAP 文件夹 | SPECIAL-USE | 来源 | v1 |
|---|---|---|---|
| `INBOX` | — | `direction = inbound` 且 `placement = default` | ✓ |
| `Sent` | `\Sent` | `direction = outbound` 且 `placement = default`，不含 `failed` | ✓ |
| `Archive` | `\Archive` | `placement = archive` | ✓ |
| `Trash` | `\Trash` | `placement = trash` | ✓ |
| `<文件夹名>` | — | `placement = folder` 且 `folder_id` 对应的 `mail_folders` | ✓ |
| `Drafts` | `\Drafts` | 新表 `mail_client_drafts`（§5.4） | P1c |
| `Junk` | `\Junk` | Zmail 没有垃圾邮件分类 | 不暴露；客户端会在本地建一个 |

- 已封存（`sealed_before`）和已删除的邮件在所有文件夹中都不可见，与网页规则相同。
- `Sent` 中 `status = failed` 的邮件不显示；`queued` 的显示，与网页一致，客户端能看到"已提交"。
- 外发邮件归档后进入 `Archive`，和入站邮件放在一起，与网页的 placement 语义一致。

### 5.3 标记

| IMAP | Zmail | 需要的权限 |
|---|---|---|
| `\Seen` | `mail_message_reads`（当前 membership） | `view`；外发邮件始终为 `\Seen`，STORE 无效 |
| `\Flagged` | `mail_message_stars`（当前 membership） | `view` |
| `\Answered` | v1：存在 `in_reply_to` 指向它的外发邮件即视为已回复（只读计算） | — |
| `\Deleted` + `EXPUNGE` | 不在 Trash 中：移到 Trash（`send`）。在 Trash 中：永久删除（`organize`）。没有权限时返回 `NO [NOPERM]`，并清除 `\Deleted` | 见左 |
| `\Draft` | 只出现在 Drafts 中 | — |
| `MOVE`（RFC 6851） | 修改 `placement`/`folder_id`，**所有人可见**（共享） | `send` |
| `COPY` | v1 当作 MOVE 拒绝：`NO [CANNOT]`，因为 Zmail 一封邮件只能在一个位置。只有 COPY 到 Trash 时按"移到 Trash"处理（兼容老客户端） | — |
| 自定义 keyword（`$Label1` 等） | v1 不持久化（`PERMANENTFLAGS` 不含 `\*`）；P2 把 `mail_labels` 映射为 keyword | — |

在共享邮箱中执行移动或删除会影响所有成员。设置页需要提示这一点，与网页的提示保持一致。

### 5.4 Drafts 与 APPEND

- `APPEND` 到 `Sent`：Outlook、Thunderbird、Apple Mail 用 SMTP 发出后会再把一份副本 APPEND 到 Sent。Zmail 发信时已经存过一份，所以按 `Message-ID` 加发件邮箱，在最近 15 分钟的外发邮件中查找。找到就直接返回已有邮件的 `APPENDUID`，不新建；找不到（例如从别的服务器发的）就拒绝，返回 `NO [CANNOT]`。
- `APPEND` 到 `Drafts`（P1c）：客户端会不停地保存、删除草稿，而且草稿是完整的 MIME。新表 `mail_client_drafts(membership_id, mailbox_id, uid, raw bytea ≤ 6 MiB, created_at)` 每人一份，只在客户端之间同步。**v1 不和网页的 `mail_drafts` 互通**：结构化草稿与 MIME 互转会丢附件和格式。在 P1c 之前不暴露 Drafts 文件夹，客户端会把草稿存在本地。
- `APPEND` 到其他文件夹：拒绝，返回 `NO [CANNOT]`。

### 5.5 UID、UIDVALIDITY 与原文

**UID 策略**：IMAP 要求同一文件夹内 UID 按"进入顺序"严格递增。直接用 `mail_messages.id` 是不行的：一封旧邮件后来被移进某个文件夹时，它的 id 比文件夹里已有的 UID 都小，客户端用 UIDNEXT 增量同步就会漏掉它。因此：

- 新表 `mail_imap_folders(mailbox_id, folder_key, uidvalidity, uidnext, highest_modseq)`，其中 `folder_key` 取 `inbox`、`sent`、`archive`、`trash`、`folder:<folder_id>` 之一。
- 新表 `mail_imap_uids(mailbox_id, folder_key, uid, message_id, modseq)`，主键 `(mailbox_id, folder_key, uid)`，并在 `message_id` 上建唯一索引：一封邮件同一时刻只在一个文件夹里。
- **用 PostgreSQL 触发器维护**：在 `mail_messages` 的 INSERT 和 `UPDATE OF placement, folder_id` 时，删掉旧的 UID 行，再从目标文件夹的 `uidnext` 取号插入新行，并 `pg_notify('mail_imap', mailbox_id)`。`mail_message_reads`、`mail_message_stars` 的变更也触发 modseq 递增和通知。用触发器而不是在 Go 里打点，原因是 placement 有多条写入路径：入站、发信、`organize_repository`、30 天清理 Trash 的 worker、删除文件夹时把邮件移回 `default`、交接封存等。只有触发器能保证一条都不漏。
- `UIDVALIDITY`：建 `mail_imap_folders` 行时取当前 Unix 秒。正常情况下永不改变。删除一个文件夹后再建同名文件夹，`folder_id` 不同，`folder_key` 也就不同，自然得到新的 UIDVALIDITY。
- 首次上线做回填：按 `id` 的顺序给存量邮件编号。
- 多个会话之间的序号和 EXPUNGE 推送交给 `imapserver.MailboxTracker`：每个网关进程、每个文件夹一个 tracker，由 NOTIFY 驱动。
- `CONDSTORE`/`QRESYNC`：表里已经有 `modseq`，P2 打开即可（iOS 和 Thunderbird 都支持，同步更省流量）。

**原文（`BODY[]`）**：

- 新表 `mail_message_sources(message_id PK, raw bytea, size, sha256, origin)`：
  - 入站：在 `POST /v1/inbound-mail-messages` 落库的同一事务中保存原始字节（≤5 MiB）；
  - SMTP 提交：保存客户端提交的原文（发出时再加上 Zmail 的 Message-ID 等头）；
  - 网页、API 发信：保存发信时构造的 MIME（如果传输层不产出 MIME，就在存储时用同一套合成器生成）。
- 存量邮件和外部同步副本没有原文，按需**合成** RFC 5322。合成内容包括：From（`from_name` + `from_address`）、To/Cc（JSON 数组）、Subject（RFC 2047）、Date（`created_at`）、Message-ID（`rfc_message_id`）、In-Reply-To、References、`multipart/mixed` 下的 `multipart/alternative`（text + html），再加上 `mail_attachments`。合成结果有损：Reply-To、原始 Date、DKIM 签名、内联图片的 Content-ID 都没有了。邮件头加 `X-Zmail-Reconstructed: 1` 方便排查。
- `RFC822.SIZE`、`BODYSTRUCTURE`、`ENVELOPE` 由原文解析得出（用 `emersion/go-message`），按 `message_id` 放进进程内 LRU 缓存（例如 64 MiB），避免每次 FETCH 都重新解析 5 MiB 的邮件。
- 存储增长：入站上限 5 MiB/封，与 `mail_attachments` 中的附件**重复存了一份**。P2 可以改成"原文里的附件只存引用"或移到对象存储（S3 SDK 已在 `go.mod` 中）。v1 先简单处理，监控表的大小（§6.6 拍板项）。

### 5.6 验证码遮罩：IMAP 下发什么

现状：网页、API 中所有入站正文都经过 `maskSecretsInText`/`maskSecretsInHTML` 遮罩，通过 `POST .../reveal` 可以看原文，并记录 `mail.secret_revealed`。

**决定：IMAP 下发完整原文，不遮罩。** 理由：

1. 用户的预期：用客户端就是为了在手机上直接看到并复制验证码；iOS 还会自动识别邮件中的验证码并在输入框上方提示。遮罩会让这些功能全部失效，员工会认为"邮件坏了"。
2. 技术上：`BODY[]` 是原文，遮罩就要改写 MIME，这会破坏 DKIM 签名，还会让 `RFC822.SIZE` 和实际内容不一致。
3. 威胁模型：网页遮罩针对的是旁人瞥屏、截图外泄、交给 AI 处理（契约规定交给 AI 的必须是遮罩版）。IMAP 客户端是用户本人主动配置的设备，它的风险在"设备丢失、密码泄露"，这由可撤销的设备密码、1 分钟断开、组织开关来控制。
4. 一致性：访问控制与 `reveal` 相同（都要求 `view`）。区别只在于 reveal 每次都写审计，而 IMAP 改为按会话审计（`mail_client_sessions`）。

代价与缓解：

- 原文会落到设备的本地缓存里。管理员如果在意，可以关闭组织开关，或只给部分成员 `mail_clients` 功能。
- 新版 Outlook 会把内容同步到微软云[^ms-cloud]，设置页要明确提示。
- 预留 P2 组织策略 `mail_client_content = full | masked`：`masked` 模式下下发合成的、遮罩后的 MIME，并标注 `X-Zmail-Masked: 1`，代价是 DKIM 失效、附件不变。先不做，等有客户要求再说。
- 新邮件通知（`mail.received`）仍按契约只推送遮罩后的主题，网关不改变这一点。

### 5.7 SMTP 提交 → 现有发信路径

1. 认证：`AUTH PLAIN`/`LOGIN`，只在 TLS 连接上接受；需要 `smtp` 范围。
2. `MAIL FROM` 和 `From:` 头必须是该成员**有 `send` 权限**的 Zmail 邮箱地址（个人邮箱，或 send 级别以上的共享邮箱）。不允许代发其他地址：返回 `550 5.7.1 Sender address not allowed`。`From:` 中的显示名会被忽略，按契约总是用邮箱的 `display_name`。
3. `RCPT TO` 收集所有收件人，不在 `To`/`Cc` 头中的就是 bcc。去重后上限 50 个，超出返回 `452 4.5.3`。**需要给 `SendInput` 和 `mailtransport.Message` 增加 bcc**：只放进投递信封，不写入头，也不存进 `cc_addresses`。新增列 `bcc_addresses`，只对发件人自己可见。
4. `SIZE` 声明 8 MiB。用 `parseMail` 解析：首个 text、首个 html 各 ≤256 KiB，附件 ≤10 个、合计 ≤4 MiB，主题 ≤998 个字符。超出返回 `552 5.3.4`，并附上可读原因（例如"附件超过 4 MiB"）。
5. 组装 `SendInput{to, cc, bcc, subject, text, html, in_reply_to, attachments}`，`Idempotency-Key = "smtp:" + sha256(mailbox_id + Message-ID)`。客户端重试同一封邮件不会重复发送。
6. 调用 `service.send(ctx, mailbox, input, queueAtLimit = true)`，结果映射如下：

| `service.send` 结果 | SMTP 应答 |
|---|---|
| `sent` | `250 2.0.0 Ok <id>` |
| 排队（`202`，`send_limit`） | `250 2.0.0 Queued`：已接受，由 worker 补发；在网页"已发送"中显示为排队中 |
| `MAILBOX.SEND_LIMIT_REACHED`（排队也满了） | `452 4.3.1 Daily send limit reached, try later` |
| `MAILBOX.SENDING_PAUSED` | `550 5.7.1 Sending paused for this organization` |
| `MAILBOX.DELIVERY_FAILED` / 上游不可达 | `451 4.4.1`（临时错误，客户端会重试；有幂等键保护） |
| `MAILBOX.SENDER_NOT_CONFIGURED` | `451 4.3.5` |
| `MAILBOX.MESSAGE_INVALID` | `554 5.6.0` + 原因 |

7. 已知损失：Zmail 会按 `SendInput` 重新构造 MIME，客户端的自定义头、内联图片的 `cid:` 引用（Outlook 签名里的图片）可能失效。P1b 至少要保留内联图片的 Content-ID，作为测试用例。能否把原始 MIME 直接透传给 Cloudflare Email Sending，列为开放问题（§9）。
8. 发出后，邮件按常规出现在网页的"已发送"中，IMAP 的 `Sent` 通过触发器同步可见。

### 5.8 IDLE 与推送

- `IDLE`（RFC 2177）：会话订阅本进程中对应文件夹的 `MailboxTracker`。网关只占**一条**专用的 PostgreSQL 连接，执行 `LISTEN mail_imap` 和 `LISTEN mail_client_auth`，在进程内分发。
- 连接断开或通知丢失时，所有活跃 tracker 每 30 秒兜底轮询一次 `highest_modseq`。
- 服务端每 25 分钟主动发一次 `* OK Still here`，防止 NAT 超时；29 分钟后结束 IDLE（RFC 2177 建议客户端每 29 分钟重发）。
- iOS 限制：iOS 邮件对非 Exchange 账号**不支持真正的后台推送**，只会"获取新数据"（15 分钟到 1 小时一次，或打开 App 时）。前台时 IDLE 生效。这是平台限制，设置页要写明；更实时的提醒靠 PWA/Web Push。
- 支持的扩展（v1）：`IMAP4rev1`、`IMAP4rev2`、`IDLE`、`NAMESPACE`、`MOVE`、`UIDPLUS`、`SPECIAL-USE`、`LIST-STATUS`、`ID`、`ENABLE`、`UTF8=ACCEPT`、`APPENDLIMIT`。暂不支持：`CONDSTORE`/`QRESYNC`（P2）、`COMPRESS=DEFLATE`（P2）、`QUOTA`、`ACL`。

### 5.9 搜索

`SEARCH`/`UID SEARCH` 翻译成 SQL：FROM、TO、CC、SUBJECT、BODY/TEXT 用 `ILIKE`，作用于 `text_body`；日期条件用 `created_at`；标记条件 join reads/stars。限定在当前文件夹，结果最多 10000 条。复杂条件（OR/NOT 嵌套超过 4 层）返回 `NO [LIMIT]`。

---

## 6. 部署与运维

### 6.1 拓扑

```
客户端 ──993/465/587 (TLS)──▶ FE5 38.58.181.154 ──▶ mail-gateway 容器（Go，自己终止 TLS）
                                                     │  1 条 LISTEN 连接 + 小连接池
                                                     ▼
                                              zgi-shared-postgres / zmail
客户端 ──HTTPS──▶ OpenResty ──▶ web / api（不变；新增 autoconfig、autodiscover、mobileconfig 路由）
```

- 进程：`server mail:gateway`，新增 operator 命令，与 `mail:broadcast:work` 同一个镜像。
- compose 新增服务 `mail-gateway`：`ports: ["993:993", "465:465", "587:587"]`；证书目录只读挂载；`mem_limit: 384m`；`restart: unless-stopped`；健康检查用内部 HTTP 端口 `127.0.0.1:18992/health`，只监听本机，同时暴露 Prometheus 指标。
- TLS 由 Go 终止：最低 TLS 1.2，证书热加载（收到 SIGHUP 时重新读取，或在 `tls.Config.GetCertificate` 里按文件修改时间重新读取）。不经过 OpenResty stream，少一跳，也不改 OpenResty 配置。

### 6.2 主机名与 Cloudflare

- 根据 `docs/DEPLOYMENT_FE5_0083.md`，apex `zmail.im` 目前是 **DNS only**（A → 38.58.181.154），证书是 Let's Encrypt。即便如此，仍然**建议使用独立主机名**：
  - `imap.zmail.im` 和 `smtp.zmail.im`，两条 A 记录，**DNS only（灰云）**，指向 38.58.181.154；
  - 原因：Cloudflare 的 HTTP 代理只代理 HTTP(S) 端口，993、465、587 都不经过它。将来 apex 改成橙云时，邮件客户端不受影响；换服务器时也只需改这两条记录。
- **`smtp.zmail.im` 只用于提交**，与 MX 无关（MX 仍指向 Cloudflare Email Routing），不影响收信。
- 组织自有域名（例如 `zgi.app`）的员工也连接 `imap.zmail.im`，证书只需要覆盖 Zmail 自己的主机名，不用给客户的每个域名签证书。
- 备选：Cloudflare Spectrum 代理任意 TCP 端口，但完整 TCP 支持需要 Enterprise，Pro/Business 只支持部分协议并按流量收费[^spectrum]。花钱且没有必要，**不推荐**。

### 6.3 证书

- 一张证书覆盖 `imap.zmail.im` 和 `smtp.zmail.im`（以及 P1c 的 `autoconfig.zmail.im`/`autodiscover.zmail.im`，如果采用）。
- 方式一（与现有做法一致）：certbot webroot，HTTP-01。需要 OpenResty 给这两个主机名加一个只服务 `/.well-known/acme-challenge/` 的 80 端口 vhost（`test.zmail.im` 用的就是这种方式）。部署钩子：`docker compose kill -s HUP mail-gateway`。
- 方式二：certbot DNS-01，使用 Cloudflare 令牌。现有令牌带 zmail.im 的 DNS Edit 权限，但用它会自动改 DNS，**需要负责人同意**。
- 到期监控：剩余不足 21 天时告警。

### 6.4 防火墙、资源与连接上限

| 项 | 建议值 | 说明 |
|---|---|---|
| 开放端口 | 993、465、587，TCP 入站 | 主机防火墙（1Panel/ufw）和云厂商安全组都要放行；**不开** 143、110、25 |
| 全局并发连接 | 1000 | 超出时 IMAP 返回 `* BYE [UNAVAILABLE]`，SMTP 返回 `421` |
| 每用户连接 | 20 | iOS/Outlook 每个账号会开 3–6 条连接 |
| 每 IP 连接 | 100 | 办公室 NAT 和微软云同步都会集中在少数 IP 上 |
| 未认证超时 | 60 秒；已认证非 IDLE 空闲 30 分钟 | 符合 RFC 9051 的自动登出 |
| 单次 FETCH | 每批最多 500 封；每连接下行限速 5 MiB/s | 防止首次同步把带宽占满 |
| 内存 | 每个连接约 50–100 KiB（goroutine + TLS 缓冲），1000 条约 100 MiB，加上 64 MiB 的解析缓存 | `mem_limit 384m` |
| **数据库连接** | 网关连接池 2 + 1 条 LISTEN | **`zmail_app` 现在上限是 4**，必须调高到 7 左右；这是共享 PostgreSQL，需要拍板 |

### 6.5 监控与告警

- Prometheus（`client_golang` 已在依赖中）：`mail_gateway_connections{protocol}`、`mail_gateway_auth_total{protocol,result}`、`mail_gateway_command_seconds{command}`、`mail_gateway_fetch_bytes_total`、`mail_gateway_smtp_submissions_total{outcome}`、`mail_gateway_notify_lag_seconds`、`mail_gateway_tracker_count`。
- 告警：认证失败率 5 分钟内超过 50% 且超过 100 次（撞库）；IDLE 通知延迟超过 60 秒；证书剩余不足 21 天；连接数超过上限的 80%。
- 日志：只记录 ID（组织、用户、密码、邮箱、邮件）、命令名、耗时和结果；**不记录**主题、地址、正文、密码；生产环境禁止开启 `imapserver.Options.DebugWriter`（它会输出凭据）。Sentry 沿用现有配置。
- 运维命令：`mail:gateway:sessions`（列出活跃会话）、`mail:gateway:kick --password=<id>`（踢下线）。

### 6.6 需要负责人拍板

自主运行的 agent **不能**自行修改 zmail.im 的 DNS 或路由、开放服务器端口、或产生费用。以下事项均需负责人确认：

| # | 事项 | 建议 | 类别 |
|---|---|---|---|
| 1 | 新增 DNS：`imap.zmail.im`、`smtp.zmail.im`，A → 38.58.181.154，DNS only | 同意 | 修改 zmail.im DNS |
| 2 | 可选 DNS：`_imaps._tcp`、`_submission._tcp` SRV（RFC 6186）[^rfc6186]，`_autodiscover._tcp` SRV；可选的 `autoconfig`、`autodiscover` 子域名 | P1c 再加；可以先只用 apex 路径（§7），不改 DNS | 修改 zmail.im DNS |
| 3 | FE5 开放 993、465、587 入站（主机防火墙 + 云安全组）；先确认这几个端口没有被这台共享机器上的其他服务占用 | 同意；不开 25、143 | 开放服务器端口 |
| 4 | 证书签发方式：HTTP-01（OpenResty 新增 80 端口 vhost）还是 DNS-01（使用 Cloudflare 令牌） | HTTP-01，与 `test.zmail.im` 一致 | 修改路由 |
| 5 | 共享 PostgreSQL 上 `zmail_app` 的连接上限从 4 调到 7 | 同意，或先让网关和 API 共用连接，牺牲隔离 | 共享资源 |
| 6 | `mail_message_sources` 存原文，附件重复存储，数据库磁盘增长 | 先存 PostgreSQL；超过阈值后迁到对象存储（会产生费用） | 存储和费用 |
| 7 | 组织开关 `mail_client_access` 和成员功能 `mail_clients` 的默认值 | 新组织默认开；邀请默认授予 | 产品策略 |
| 8 | IMAP 下发验证码原文（§5.6） | 同意 | 产品策略 |
| 9 | 是否允许、或仅提示新版 Outlook 经微软云同步 | 只提示，不拦截 | 产品策略 |
| 10 | Cloudflare Spectrum | 不买 | 费用 |
| 11 | staging 是否也部署网关（`imap.test.zmail.im` 等，同样需要 DNS 和端口） | P1a 在 staging 上先跑，需同时批准 1、3 在 test 上的对应项 | 修改 DNS 和端口 |

---

## 7. 一键配置

### 7.1 网页设置页"在手机/电脑邮件客户端使用"

位置：`/console/settings/mail-clients`（Next.js，`web/src/features/mailbox/...`），从个人菜单和邮箱页的"⋯"进入。

- 顶部：显示组织开关和功能授权的状态（被关闭时显示原因）；服务器参数卡片（可一键复制）：
  - 收件：`imap.zmail.im`，端口 993，SSL/TLS；
  - 发件：`smtp.zmail.im`，端口 465 SSL/TLS，或 587 STARTTLS；
  - 用户名：完整邮箱地址；
  - 密码：客户端专用密码。
- "我的客户端密码"列表：设备名、范围、创建时间、最近使用时间（相对时间 + 粗略地区）、"撤销"按钮。
- "添加设备"向导：选择客户端 → 生成密码（需要近期验证）→ 显示该客户端的步骤，带截图：
  - **iPhone/iPad**：在 Safari 中点"安装描述文件"（§7.2），否则显示手动步骤（设置 → App → 邮件 → 邮件账户 → 添加账户 → 其他）。
  - **Mac 邮件**：下载 `.mobileconfig`，双击后在"系统设置 → 隐私与安全性 → 描述文件"中安装。
  - **Outlook（Windows 经典版 / Mac / 手机）**：输入邮箱地址后 Autodiscover 自动填写；失败时选"IMAP"手动填写。**新版 Outlook** 额外提示：账号会同步到微软云[^ms-cloud]。
  - **Thunderbird**：输入邮箱和密码后自动识别（§7.3）。
  - **安卓 Gmail App**：添加账号 → 其他 → 个人（IMAP），手动填写；三星邮件同理。
  - **Foxmail**：新建账号 → 手动设置 → IMAP。
- 共享邮箱：列出可以加为独立账户的共享邮箱（用户名填共享邮箱地址），并说明"在共享邮箱中移动或删除会影响所有成员"。

### 7.2 iOS / macOS `.mobileconfig`

- `GET /v1/mail-client-profiles/:token`：一次性令牌，5 分钟有效，用一次即作废，返回 `application/x-apple-aspen-config`，`Cache-Control: no-store`。令牌由 `POST /v1/mail-client-passwords` 加上 `{ profile: true }` 生成。
- 负载为 `com.apple.mail.managed`[^apple-mail]：
  - `EmailAccountType = EmailTypeIMAP`；
  - `EmailAddress`；
  - `IncomingMailServerHostName = imap.zmail.im`、`IncomingMailServerPortNumber = 993`、`IncomingMailServerUseSSL = true`；
  - `IncomingMailServerUsername` 为邮箱地址，`IncomingPassword` 为**新生成的客户端密码**；
  - `OutgoingMailServerHostName = smtp.zmail.im`、`OutgoingMailServerPortNumber = 465`、`OutgoingMailServerUseSSL = true`、`OutgoingPasswordSameAsIncomingPassword = true`；
  - `PayloadDisplayName = "Zmail – alice@zgi.app"`。
- 密码嵌在描述文件里，所以文件本身就是凭据：一次性下载，服务端不保留文件。设备名默认填"iPhone 描述文件 + 日期"。
- 用 `zmail.im` 的 TLS 证书做 CMS 签名，系统会显示"已验证"，而不是"未签名"。
- 目标：在 iPhone 上从打开设置页到收到第一封邮件不超过 2 分钟（Safari 下载 → 设置 → 已下载描述文件 → 安装 → 完成）。

### 7.3 Thunderbird autoconfig

Thunderbird 按以下顺序查找[^tb-ac]：本地 ISP 文件 → `https://autoconfig.<域名>/mail/config-v1.1.xml?emailaddress=…` → `https://<域名>/.well-known/autoconfig/mail/config-v1.1.xml` → Mozilla ISP 数据库。

- `@zmail.im` 地址：由 Next.js 在 `https://zmail.im/.well-known/autoconfig/mail/config-v1.1.xml` 返回静态 XML（IMAP 993 SSL、SMTP 465 SSL、`%EMAILADDRESS%`、`password-cleartext`，在 TLS 内传输）。**不需要改 DNS。**
- 组织自有域名：要在客户的 DNS 中加 `autoconfig.<域名>` CNAME，并给这个名字签证书，成本高。v1 改为在设置页提示"Thunderbird 识别失败时选手动配置"。
  - 对于托管在 Zmail Managed Zones（`mail_zones`、`dns_record_service.go`）的域名，P2 可以一键写入 RFC 6186 SRV 记录（`_imaps._tcp` → `imap.zmail.im:993`、`_submission._tcp` → `smtp.zmail.im:587`）。新版 Outlook 也会参考 `_imaps._tcp`[^ad-srv]。
- 也可以申请把 `zmail.im` 加入 Mozilla ISP 数据库，免费，但只覆盖 `@zmail.im`。

### 7.4 Outlook Autodiscover

经典 Outlook 依次尝试 `https://<域名>/autodiscover/autodiscover.xml`、`https://autodiscover.<域名>/autodiscover/autodiscover.xml`、`_autodiscover._tcp.<域名>` SRV[^ms-ad]。

- `@zmail.im`：Next.js（或 API）在 `POST https://zmail.im/autodiscover/autodiscover.xml` 返回 POX 格式的 `<Protocol><Type>IMAP</Type><Server>imap.zmail.im</Server><Port>993</Port><SSL>on</SSL>…` 和 SMTP 465，同样不需要改 DNS。
- 组织自有域名：P2 通过 Managed Zones 写入 `_autodiscover._tcp` SRV，指向 `zmail.im:443`。
- 只响应 IMAP/SMTP 设置，不伪装成 Exchange（避免 Outlook 尝试 MAPI/EWS）。

---

## 8. 分阶段计划与验收标准

客户端兼容性矩阵（每阶段都要回归）：iOS 18/26 邮件、macOS 邮件、Outlook 经典版 Windows、新版 Outlook for Windows、Outlook for Mac、Outlook iOS/Android、Thunderbird 最新 ESR、Foxmail 7.x、安卓 Gmail App、三星邮件。

| 阶段 | 内容 | 验收标准 |
|---|---|---|
| **P1a：只读 IMAP + 客户端密码**（约 2 周） | `mail_client_passwords`、创建/撤销接口、组织开关、`mail_clients` 功能；`mail:gateway` 命令和 compose 服务；`imapserver` 会话：LOGIN/AUTHENTICATE PLAIN、LIST/LSUB/NAMESPACE/STATUS/SELECT/EXAMINE/FETCH/SEARCH/STORE（只限 `\Seen`、`\Flagged`）；`mail_imap_folders`/`mail_imap_uids` 加触发器和回填；`mail_message_sources` 入站落库，存量合成；共享命名空间；限流、审计、指标；设置页的最小版（参数 + 密码管理） | ① staging 上 iPhone 邮件手动添加账号成功，INBOX、Sent、Archive、Trash 和自定义文件夹的封数与网页一致 ② 在客户端标为已读后网页 5 秒内同步，另一位共享邮箱成员的未读数不变 ③ 无权限的共享邮箱不出现在 LIST 中；用别人的地址登录失败 ④ 撤销密码后已连接的客户端在 60 秒内断开，重新登录失败 ⑤ 关闭组织开关后，全组织的客户端在 60 秒内断开 ⑥ 同一 IP 连续 20 次错误密码后被封 15 分钟，正常员工从其他 IP 登录不受影响 ⑦ 带附件的入站邮件在 iOS 和 Thunderbird 中附件可以打开，验证码完整显示 ⑧ 日志中没有任何主题、地址或密码 |
| **P1b：SMTP 提交 + 整理** | `go-smtp`，465/587；From 校验；bcc 打通 `SendInput`；MIME → `SendInput`（保留 cid 内联图片）；幂等键；结果映射；APPEND 到 Sent 去重；MOVE、`\Deleted`/EXPUNGE、COPY 到 Trash | ① 在 iPhone、Outlook、Thunderbird 中各发一封带 2 个附件、1 个 bcc 的邮件：收件人收到，bcc 不出现在头中；网页"已发送"里各只有一份 ② 个人通道额度设为 3：第 4 封返回"已排队"，worker 补发后状态变为 sent；排队也满后客户端收到 452 并提示稍后重试 ③ 用没有 send 权限的共享邮箱地址作为 From 被拒（550） ④ 在 iOS 上左滑删除 → 网页中进入垃圾箱；在 Trash 中彻底删除：没有 organize 时返回 NOPERM，邮件仍在 ⑤ 5 MiB 附件返回 552 并给出可读原因 ⑥ 在网络中断时重试同一封邮件，只发出一次 |
| **P1c：IDLE + 一键配置 + Drafts** | LISTEN/NOTIFY 分发、`MailboxTracker`、30 秒兜底轮询；`.mobileconfig`（签名、一次性令牌）；`zmail.im` 上的 autoconfig 和 autodiscover 路由；分客户端的设置页向导；`mail_client_drafts` | ① iPhone 从打开设置页到收到第一封邮件 **≤2 分钟**（3 名非技术员工实测，取中位数） ② Thunderbird 输入 `x@zmail.im` 和密码后自动填好全部服务器参数 ③ 经典 Outlook 输入地址后自动配置成功 ④ 在 Thunderbird 前台 IDLE 时，新的入站邮件 **≤10 秒**出现；网页中的移动操作 ≤10 秒反映到客户端 ⑤ Thunderbird 保存草稿后在另一台 Thunderbird 上可见 ⑥ 500 条并发 IDLE 连接下网关内存 <250 MiB，PostgreSQL 连接数 ≤3 |
| **P2：完善**（按需） | CONDSTORE/QRESYNC；COMPRESS；标签映射为 keyword；Managed Zones 一键写入 SRV/autodiscover；`masked` 内容策略；原文附件去重或迁移到对象存储；文件夹的 CREATE/RENAME；外部接入邮箱只读暴露；生产上线 | 视试点反馈决定 |
| **生产上线** | P1a–P1c 在 staging 通过后，在 §6.6 第 1、3、4、5 项获批的前提下上线 FE5 | 生产冒烟：用真实 iPhone 加 Outlook 收发各一封；指标和告警生效；回滚方式为停止 `mail-gateway` 服务（不影响 API 和 Web） |

测试分层（与 `AGENTS.md` 的验证要求一致）：

- 单元测试：UID 分配、标记映射、SMTP 结果映射、MIME 合成、限流。
- 集成测试：用一次性 PostgreSQL 验证触发器和 NOTIFY，用 `imapclient` 和 `net/smtp` 跑端到端会话。
- 兼容性：用 imaptest（Dovecot 的协议压力测试工具）跑一轮，再按上面的客户端矩阵人工验收。

---

## 9. 风险与开放问题

| # | 风险或问题 | 影响 | 缓解或下一步 |
|---|---|---|---|
| 1 | `go-imap/v2` 仍是 beta（v2.0.0-beta.9） | API 变更、边界 bug | 锁定版本；跑 imaptest 和客户端矩阵；业务逻辑隔离在 `Session` 实现之下，升级时只改适配层 |
| 2 | 存量邮件没有原文，只能合成 | Reply-To 丢失，客户端"回复"可能发错对象；DKIM 无法验证 | 从上线起入站保存原文；合成的邮件加标记头；评估是否在入站时把 `Reply-To` 解析进新列（成本很低，建议 P1a 一起做） |
| 3 | SMTP 提交会重新构造 MIME | Outlook 签名图片、自定义头丢失 | P1b 保留 cid 内联图片；**开放问题**：Cloudflare Email Sending 是否接受原始 MIME 透传 |
| 4 | 共享邮箱中的移动和删除对所有人生效，客户端没有提示 | 误操作影响同事 | 设置页明确说明；P2 可以提供"共享邮箱只读挂载"选项 |
| 5 | 触发器给写路径增加了开销 | 入站和批量更新变慢 | 触发器只做一次 UPSERT 和一次 NOTIFY；回填分批进行；用 `v2_hot_paths_bench_test.go` 对比基准 |
| 6 | 共享 PostgreSQL 连接数紧张 | 网关可能挤占 API | 独立连接上限；网关连接池 2；`LISTEN` 单连接 |
| 7 | 新版 Outlook 经微软云同步 | 凭据和内容出境，流量来自微软 IP | 设置页提示；限流以"IP + 用户"为主；P2 可以按组织屏蔽微软 IP 段 |
| 8 | iOS 对 IMAP 不做真正的后台推送 | 员工觉得"不实时" | 写进设置页说明；用 PWA/Web Push 补充 |
| 9 | 草稿在网页和客户端之间不互通 | 网页里存的草稿在客户端中看不到 | 明确告知；P2 评估把 `mail_drafts` 改为 MIME 存储 |
| 10 | 验证码原文进入设备缓存 | 设备丢失时泄露 | 设备密码可撤销、1 分钟断开；组织开关；P2 `masked` 策略 |
| 11 | 原文存储增长 | 共享 PostgreSQL 磁盘 | 监控 `mail_message_sources` 的体积；超过阈值后迁到对象存储（拍板项 6） |
| 12 | 外部接入邮箱是否要暴露 | 员工可能希望"一个客户端看所有邮箱" | v1 不做，员工可以在客户端里直接添加原账号；P2 再评估只读暴露 |
| 13 | **开放问题**：`mail:gateway` 是单进程。将来多实例时，tracker 在各实例之间如何同步 | — | NOTIFY 天然广播给所有实例，每个实例各自维护 tracker；不需要粘性会话 |
| 14 | **开放问题**：契约中 `Inbound Delivery` 一节写着"Attachments are not stored"，与 `Attachments` 一节矛盾 | 文档一致性 | 实现 P1a 时一并修订契约 |

---

来源（访问日期均为 2026-11-23）：

[^fm-app]: [Fastmail：App passwords](https://www.fastmail.help/hc/articles/360058752854)
[^fm-blog]: [Fastmail：What's up with app passwords?](https://www.fastmail.com/blog/whats-up-with-app-passwords/)
[^fm-jmap]: [Fastmail API 文档（JMAP）](https://fastmail.com/dev/)
[^fm-ios]: [Fastmail：Set up iOS devices](https://www.fastmail.help/hc/en-us/articles/7908267139215)
[^g-lsa]: [Google Workspace：Transition from less secure apps to OAuth](https://knowledge.workspace.google.com/admin/sync/transition-from-less-secure-apps-to-oauth)
[^g-imap]: [Google Workspace：Turn POP & IMAP on or off for users](https://support.google.com/a/answer/105694)
[^ms-basic]: [Microsoft Learn：Deprecation of Basic authentication in Exchange Online](https://learn.microsoft.com/en-us/exchange/clients-and-mobile-in-exchange-online/deprecation-of-basic-authentication-exchange-online)
[^ms-smtp]: [Microsoft Exchange Team：Updated SMTP AUTH Basic Authentication deprecation timeline](https://techcommunity.microsoft.com/blog/Exchange/updated-exchange-online-smtp-auth-basic-authentication-deprecation-timeline/4489835)
[^ms-cloud]: [Microsoft：Sync your account in Outlook to the Microsoft Cloud](https://support.microsoft.com/en-us/outlook/getstarted/sync-your-account-in-outlook-to-the-microsoft-cloud)
[^ms-ad]: [Microsoft：Outlook 2016 implementation of Autodiscover](https://support.microsoft.com/en-us/help/3211279)
[^ad-srv]: [DirectAdmin：Autodiscover（含新版 Outlook 所需的 SRV 记录）](https://docs.directadmin.com/other-hosting-services/email/autodiscover.html)
[^zoho-asp]: [Zoho：Application Specific Password](https://help.zoho.com/portal/en/kb/mail/access-from-external-mail-clients/articles/authentication-error-during-outlook-iphone-imap-configuration)
[^zoho-imap]: [Zoho Mail：IMAP Access](https://www.zoho.com/mail/help/faq/imap-access1.html)
[^zoho-ad]: [Zoho Mail：Autodiscovery Settings](https://www.zoho.com.cn/mail/help/adminconsole/autodiscovery-settings.html)
[^exmail]: [腾讯企业邮客户端设置](https://open.work.weixin.qq.com/help2/pc/19886)
[^ali-3p]: [阿里邮箱：三方客户端访问与安全设置（含腾讯企业邮客户端专用密码说明）](https://help.aliyun.com/zh/document_detail/606337.html)
[^fs]: [飞书邮箱：通过第三方客户端登录](https://feishu.cn/hc/en-US/articles/902478147400-log-in-to-feishu-mail-through-a-third-party-email-client)
[^ali-fs]: [阿里邮箱：原飞书邮箱用户搬家须知（管理员开启第三方客户端、三方客户端安全密码）](https://www.alibabacloud.com/help/zh/alibaba-mail/latest/precautions-for-users-of-original-flying-book-mailbox-moving-1)
[^ali-163]: [阿里邮箱：网易企业邮迁移说明（客户端授权密码）](https://help.aliyun.com/document_detail/444380.html)
[^goimap]: [pkg.go.dev：github.com/emersion/go-imap/v2/imapserver](https://pkg.go.dev/github.com/emersion/go-imap/v2/imapserver)
[^spectrum]: [Cloudflare Spectrum 文档](https://developers.cloudflare.com/spectrum/)
[^apple-mail]: [Apple Developer：Mail payload（com.apple.mail.managed）](https://developer.apple.com/documentation/devicemanagement/mail)
[^tb-ac]: [Mozilla Wiki：Thunderbird Autoconfiguration](https://wiki.mozilla.org/Thunderbird:Autoconfiguration)
[^rfc6186]: [RFC 6186：Use of SRV Records for Locating Email Submission/Access Services](https://www.rfc-editor.org/rfc/rfc6186)；隐式 TLS 端口见 [RFC 8314](https://www.rfc-editor.org/rfc/rfc8314)
