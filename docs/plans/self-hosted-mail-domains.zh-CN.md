# 自建收发信 + 客户自助接入域名（任意 DNS 服务商）— 设计

状态：草案，待评审。按 [BUSINESS_IMPLEMENTATION_PLAN_STANDARD.md](../BUSINESS_IMPLEMENTATION_PLAN_STANDARD.md)
编写，是 [mail-domains.md](mail-domains.md)（Cloudflare 托管域名）的后继：那份计划已定的内容（域名列表页、
记录状态、`pending/verified`、邮箱选择域名）不重复。对外 HTTP 行为以
[contracts/MAILBOXES.md](../../contracts/MAILBOXES.md) 为准。

依据：本地原型 `spike/self-hosted-mail`（`mailserver/` + `MAILBOX_PROVIDER=selfhosted`）已跑通
「Zmail 发信 → 自建中继 DKIM 签名 → SMTP 投递到自建 MX → DKIM 校验通过 → 进入 Zmail 收件箱」，
以及外部 SMTP 来信、未知收件人 550、拒绝开放转发、外部投递安全开关。原型代码不合并，本设计重新实现。

## 1. 概要

**目标。** 任何公司把自己的域名（DNS 在阿里云、腾讯云、GoDaddy、Cloudflare 都行）接入 Zmail：
在控制台输入 `example.com`，照着页面把 5 条 DNS 记录加到自己的 DNS 服务商，点「检查」，通过后就能创建
`hello@example.com` 收发信。做法和飞书、腾讯企业邮一样：MX 指向我们自己的服务器，发信由我们自己的服务器签名投递。

**为什么。** 现在接入域名要求 DNS 在 Cloudflare 且属于实例同一个账号（`mail-domains.md` 的前提），
绝大多数客户做不到；收信依赖 Cloudflare Email Routing + Worker，发信依赖 Cloudflare Email Sending。
自建收发信去掉这两个依赖，也去掉「Cloudflare token 能管到的 zone 就是信任边界」这个多租户隐患。

**本期范围。**

1. 自建邮件边缘服务 `mailserver/`（第 4 个可部署单元）：收信 SMTP、发信中继、DKIM 签名、本地队列与重试、
   投递结果回报。
2. 域名接入新增「DNS 模式」：所有权 TXT 证明、记录清单、基于公网 DNS 的检查、每日复检、被转手时自动停用。
   域名有两种能力：**收发**（MX 指向 Zmail）和**仅发信**（MX 不动，继续用原来的邮件系统收信）。
   已在别处收信的公司，可以主域名仅发信，或用子域名（如 `mail.example.com`）收发。〔用户决定〕
3. 共享发信 IP 的自动保护：新域名 14 天预热上限、退信率过高时自动暂停发信。不做人工审批。〔用户决定〕
4. 控制台域名页适配：按记录逐条复制、显示每条的检查结果、常见 DNS 服务商的填写提示。

**非目标（本期不做）。** IMAP/POP/SMTP 提交（Outlook、手机自带邮件客户端登录）；垃圾邮件文件夹 UI；
病毒扫描；DKIM 自动轮换；客户自带发信 IP；MTA-STS 强制与 DANE；运营后台的 IP 信誉看板。

**影响的可部署单元。** `mailserver/`（新）、`api/`、`web/`、`contracts/`。`admin/` 不变。

**成功标准。** 一个 DNS 不在 Cloudflare 的测试域名，从输入域名到收发第一封真实邮件不超过 15 分钟；
发往 Gmail、Outlook、QQ 邮箱的信 SPF、DKIM、DMARC 全部 `pass` 并进入收件箱（非垃圾箱）；
边缘服务重启或宕机 10 分钟，期间的来信和发信都不丢（来信靠对方 MTA 重试；发信见 4.3 第 6 步）。

## 2. 方案取舍

| 方案 | 做法 | 优点 | 代价 |
|---|---|---|---|
| **A. Go 自研边缘（选用）** | 原型路线：Go 单二进制，go-smtp 收发，本地磁盘队列，Rspamd 作为可选旁路做反垃圾 | 和仓库技术栈一致；与 Zmail 的签名接口直接对接；原型已验证 | 队列、重试、退信解析要自己写对；成熟度不如 Postfix |
| B. Postfix + Rspamd + Go 胶水 | Postfix 管收发与队列，Go 写 milter/策略服务对接 Zmail | 队列和投递几十年验证 | 两套配置体系；容器化和多租户 DKIM 配置复杂；调试难 |
| C. 收信自建、发信走 SES 等 | 只自建 MX，发信用第三方 | 送达率起步最好 | 仍有外部依赖与账号审核；用户已明确要自己运营收发 |

选 A：用户要求自己运营收发信，A 是唯一已经跑通的路径，且只多一个 Go 服务。为降低 A 的风险：
队列格式简单（每封一个文件 + 元数据），重试策略照搬 Postfix 的指数退避，送达问题通过第 14 节的指标尽早发现。
若上线后队列/投递成为瓶颈，可以把边缘服务的「出站投递」部分换成 Postfix 而不改 API 接口（B 作为退路）。

## 3. 角色与访问面

| 角色 | 入口 | 可读 | 可改 | 拒绝时 |
|---|---|---|---|---|
| 组织所有者、管理员 | Web 控制台，带 `Organization-Id` 的会话 | 本组织域名及记录 | 添加、检查、删除本组织域名；在已验证域名上建邮箱 | 无权限 `403 AUTH.FORBIDDEN`；他组织域名 `404` |
| 组织成员 | 同上 | 本组织域名及状态 | 无 | 同上 |
| 外部发信方 | SMTP 25 端口 | — | 只能投给已接入且未停用域名下存在的邮箱 | RCPT 阶段 `550 5.1.1` / `550 5.7.1` |
| 邮件边缘服务 | 内部签名接口（第 8.2 节） | 已验证域名列表、DKIM 密钥、收件人是否存在 | 投递来信、上报投递结果 | `401`（签名错）|
| 实例运营者 | 配置、部署 | — | 发信 IP、PTR、MX 主机名、密钥 | — |

## 4. 端到端流程

### 4.1 接入域名（DNS 模式）

1. 所有者输入 `example.com` 并选择能力：收发（`send_receive`）或仅发信（`send_only`）。页面先查一次该域名现有的 MX：
   若已指向别的邮件系统，默认选「仅发信」，并提示「想在 Zmail 收信又不影响现有邮箱，可改用子域名，如 mail.example.com」。
   随后 APIAPI 校验格式、不是共享域名、本组织未添加过、未被别的组织**验证**占用；
   生成所有权令牌和该域名的 DKIM 密钥对；保存为 `pending`；立即返回需要添加的记录清单（第 5.3 节）。
2. 所有者到自己的 DNS 服务商添加记录，回到页面点「检查」。
3. API 通过公网 DNS 逐条查询并比对，每条记录得到 `ok / missing / conflict`。
   第 5.3 节中标为「阻塞」的记录全部 `ok` 时转为 `verified`；DMARC 缺失只给警告。
   `send_receive` 域名的 MX 不阻塞验证，但只有 MX `ok` 时边缘服务才接收该域名来信（「收信已开启」）；
   这样收发域名可以先开始发信，客户择时切换 MX。
4. 转为 `verified` 的同一事务里，删除其他组织对同名域名的 `pending` 认领（谁先证明所有权归谁）。
5. 边缘服务在下一次配置同步（≤ 60 秒）后开始接收该域名的来信、并能用它的 DKIM 密钥签名。
6. 失败与恢复：DNS 查询超时记为本条 `missing` 并附 `last_error`，可重复检查；检查是只读的、幂等的。
   DNS 生效慢是常态，页面提示「刚添加的记录可能需要几分钟到几小时生效」。

### 4.2 收信

```
外部 MTA ──SMTP:25──▶ mailserver ──RCPT 检查──▶ API /v1/mail-edge/recipient-checks
                         │ STARTTLS、大小限制、SPF/DKIM/DMARC 评估、(可选) Rspamd 打分
                         └──POST /v1/inbound-mail-messages（HMAC 签名，每个收件人一份）──▶ API 存信
```

1. `RCPT TO` 阶段：域名不在「收信已开启」列表（未验证、仅发信、或 MX 未指向我们）→ `550 5.7.1 Relay access denied`；
   退信子域名 `zbounce.<域名>` 的地址总是接收（见 4.3 第 5 步）；域名已停用 → `550 5.7.1`；
   邮箱不存在 → `550 5.1.1`。在 RCPT 阶段拒绝，不接受后再退信（避免反向散射垃圾）。
   recipient-checks 不可达时回 `451 4.3.0`，发信方会重试。
2. `DATA` 阶段：评估 SPF（信封发件人 + 连接 IP）、DKIM、DMARC（对齐 From 域），结果写入
   `Authentication-Results` 头；DMARC 结果为 `fail` 且对方策略为 `reject` 时回 `550 5.7.1`。
   配置了 Rspamd 时：动作为 `reject` 回 `550`，`add header` 写入 `X-Spam-*` 头后照常投递。
3. 投给 API：每个收件人一份。API `2xx` → 接受；`4xx` → 该收件人永久失败；`5xx` / 超时 → `451` 让对方重试。
   API 不可用超过对方重试窗口时会退信，这是可接受的（同现在 Cloudflare Worker 的行为）。

### 4.3 发信

```
API 发送 ──POST /send（Bearer）──▶ mailserver ──写入磁盘队列──▶ 立即回 queued/delivered
                                        └─ 投递线程：按收件域解析 MX → SMTP(STARTTLS) → 结果
                                           └─ POST /v1/mail-delivery-events（HMAC 签名）──▶ API 更新状态
```

1. `/send` 只接受发件域名在本实例已验证（或共享域名）且未停用的请求，否则 `422`。
2. 生成 MIME、用该域名的 DKIM 密钥签名、写入队列文件（`fsync` 后）再回复。回复沿用 Cloudflare 格式：
   同步投递成功的收件人放 `delivered`，其余放 `queued`。API 已把 `queued` 当作「已接受」处理。
3. 投递线程按收件人域名分组投递。`2xx` → 成功；`5xx` → 永久失败；`4xx` / 连接失败 → 按
   5 分钟、15 分钟、30 分钟、1 小时……最长间隔 4 小时退避重试，**5 天**后放弃，记为永久失败。
4. 每个收件人的最终结果和「延迟中」状态，用 Cloudflare 事件格式（`cf.email.sending.message.delivered` /
   `.bounced` / `.deferred`，schema 版本 1；API 现在只认 `deferred`，不是 `delayed`）POST 到已有的 `/v1/mail-delivery-events`，API 侧代码不改。
   上报失败的事件留在队列里重试，API 侧按事件 id 去重（已有能力）。
5. 信封发件人（`MAIL FROM`，即 Return-Path）用退信子域名上的 VERP 地址：`b-<投递令牌>@zbounce.example.com`，
   两种能力都一样。`zbounce` 的 MX 指向我们，所以仅发信域名的退信也回到我们；SPF 只检查这个子域名，
   客户主域名上已有的 SPF 不用改；DMARC 按宽松对齐（子域名对齐主域名）通过，DKIM 也独立对齐。
   投递令牌映射到「消息 + 收件人」，退信能精确对应。〔由「仅发信」决定推出〕
6. 异步退信（对方先 250 接受、后发 DSN）：发到 `zbounce` 地址的信一律不进收件箱，按投递令牌转成 `.bounced` 事件；
   令牌无效的直接丢弃并计数。反馈环（ARF 投诉报告，Outlook JMRP / Yahoo CFL 注册后发来）同样到 `zbounce`，转成投诉计数。
7. 边缘服务不可达（连接失败、超时、`5xx`）时，API 不再把消息直接记为失败，而是复用现有发信排队：
   `status=queued`、`queued_reason=provider_unavailable`，由已有的 queued-send 调度按 1、5、15、30 分钟退避重试，
   1 小时后仍失败才记为 `failed`。这是对 API 的改动（新增一个 `queued_reason` 枚举值），属于切片 1。〔实现方决定〕

### 4.4 共享 IP 保护（预热与自动暂停）

保护以「组织 × 发件域名」为单位（共享域名也按组织分别计）：

1. **预热**：域名转为 `verified` 后 14 天内，该组织在该域名上的 24 小时滚动发信量上限为
   `MAILBOX_NEW_DOMAIN_DAILY_LIMIT`（默认 100），与组织总上限取较小者。超出时和现有限额一样进入排队，
   `queued_reason=domain_warmup`。
2. **自动暂停**：滚动 24 小时内发信 ≥ 50 封且硬退信率 ≥ 5%，或投诉率 ≥ 0.3%，暂停该组织在该域名上的发信：
   新发信进入排队（`queued_reason=domain_paused`），已在边缘队列里的不撤回。
3. 第一次暂停 24 小时后自动恢复；7 天内第二次暂停则保持暂停，直到运营者执行
   `fastbuild mail:sending:resume --organization <id> --domain <name>`。运营后台界面本期不做。
4. 暂停与恢复写审计记录，并通知组织所有者和管理员（复用通知中心）。

### 4.5 每日复检与停用

1. 定时任务每 24 小时检查一次每个已验证域名（错峰，单实例同时最多 4 个 DNS 查询）。
2. 某条非所有权记录缺失 → 保持 `verified`，该条显示 `missing`，页面和邮箱列表出现警告。
3. **所有权 TXT 连续缺失 72 小时** → `suspended`：拒收该域名来信、拒发该域名的信（`422 MAILBOX.DOMAIN_SUSPENDED`），
   已存邮件保留可读。用途：域名过期被别人买走时，新所有者删掉我们的记录，我们就不再代表新所有者收发信。
4. `suspended` 后手动检查通过即恢复 `verified`。停用没有期限，也没有单独的「归档」状态：域名名称一直被该组织占用，
   已存邮件可读、邮箱可删；组织删除域名（需先删邮箱）后名称才释放。〔实现方决定〕

## 5. 领域模型

### 5.1 术语

- **邮件边缘（mail edge）**：`mailserver/` 服务，Zmail 自己运营的 MX 与发信中继。只做传输，不做业务规则。
- **接入模式（onboarding）**：`cloudflare`（`mail-domains.md` 的现有流程）或 `dns`（本设计）。
  由实例的 `MAILBOX_PROVIDER` 决定：`cloudflare` → `cloudflare`；`selfhosted` → `dns`。一个实例只用一种。
  本期不支持实例切换模式：`cloudflare` 模式的行为（包括记录缺失时 `verified → pending`）完全不变；
  本设计的状态规则只作用于 `onboarding=dns` 的行。若以后要从 Cloudflare 迁到自建，另做迁移设计。〔实现方决定〕
- **所有权令牌**：每次「组织 × 域名」认领随机生成的 32 字节十六进制串，发布为 TXT 证明控制权。
- **停用（suspended）**：已验证域名失去所有权证明后的状态。

### 5.2 不变式

- 域名小写、≤ 253 字符、不是共享域名；同一域名在实例内最多一个 `verified` 或 `suspended`；
  同一组织不能重复添加同一域名；每个组织最多 10 个域名（沿用）。
- 只有 `verified` 域名能新建邮箱、能发信；只有 `send_receive` 且 MX `ok` 的 `verified` 域名能收信。
- 发信暂停只影响发信，不影响收信和已存邮件。
- DKIM 私钥只在 API 数据库（加密）和边缘服务内存里出现，不进日志、不出现在任何浏览器响应里。

### 5.3 需要客户添加的记录（DNS 模式）

以运营者配置 `MAILBOX_MX_HOST=mx.zmail.example`、`MAILBOX_SPF_INCLUDE=_spf.zmail.example` 为例：

| 用途 | 类型 | 主机记录 | 值 | 适用 | 是否阻塞验证 |
|---|---|---|---|---|---|
| 所有权 | TXT | `_zmail.example.com` | `zmail-verify=<令牌>` | 两种 | 是 |
| DKIM 签名 | TXT | `zmail1._domainkey.example.com` | `v=DKIM1; k=rsa; p=<公钥>` | 两种 | 是 |
| 退信 MX | MX | `zbounce.example.com` | `mx.zmail.example`，优先级 10 | 两种 | 是 |
| 退信 SPF | TXT | `zbounce.example.com` | `v=spf1 include:_spf.zmail.example -all` | 两种 | 是 |
| 收信 MX | MX | `example.com` | `mx.zmail.example`，优先级 10 | 仅收发 | 否（MX `ok` 才开启收信；存在其他 MX 时为 `conflict`，提示「会把这个域名的全部来信切到 Zmail」）|
| DMARC | TXT | `_dmarc.example.com` | `v=DMARC1; p=none` | 两种 | 否：已有任意 DMARC 即 `ok`；缺失只警告（Gmail 对批量发信方要求 DMARC，页面明确提示）|

主域名的 SPF 不需要改：SPF 校验的是信封发件人所在的 `zbounce` 子域名。

运营者自己负责：`mx.zmail.example` 的 A 记录与 TLS 证书、`_spf.zmail.example` 列出所有发信 IP、
每个发信 IP 的 PTR 反向解析指向发信主机名。SPF 用 `include` 而不是写死 IP，运营者换 IP 时客户无需改动。

DKIM 选用 TXT 直接发布公钥（而不是 CNAME 委托给运营者域名）：不需要运营者 DNS 的自动化接口，
这正是去掉 Cloudflare 依赖的目的。代价：轮换密钥需要客户改一次 DNS，本期不做轮换。

## 6. 持久化

### 6.1 修改 `mail_domains`（迁移 `2026_11_xx_add_dns_onboarding_to_mail_domains`）

| 列 | 类型 | 说明 |
|---|---|---|
| `onboarding` | `varchar(20) NOT NULL DEFAULT 'cloudflare'`，CHECK `IN ('cloudflare','dns')` | 现有行回填 `cloudflare` |
| `zone_id`、`zone_name` | 改为 `NOT NULL DEFAULT ''` | DNS 模式为空 |
| `ownership_token` | `varchar(64) NOT NULL DEFAULT ''` | DNS 模式必填 |
| `dkim_selector` | `varchar(63) NOT NULL DEFAULT ''` | 本期固定 `zmail1` |
| `dkim_public_key` | `text NOT NULL DEFAULT ''` | base64 DER |
| `dkim_private_key` | `bytea` 可空 | AES-256-GCM 加密，密钥 `MAILBOX_DKIM_ENCRYPTION_KEY`，与 webhook 密钥同样的加密工具 |
| `ownership_missing_since` | `timestamptz` 可空 | 所有权 TXT 首次检查缺失的时间；恢复时清空 |
| `suspended_at` | `timestamptz` 可空 | |
| `status` | CHECK 扩为 `IN ('pending','verified','suspended')` | |
| `capability` | `varchar(20) NOT NULL DEFAULT 'send_receive'`，CHECK `IN ('send_receive','send_only')` | Cloudflare 行为 `send_receive` |
| `receiving_enabled` | `boolean NOT NULL DEFAULT false` | 最近一次检查 MX `ok` 且为 `send_receive`；Cloudflare 行按现状回填 `true` |
| `verified_at` | 已有 | 预热窗口从这里起算 |

新表 `mail_sending_guards`（`mailbox` 模块唯一写入者）：`organization_id`、`domain`（varchar(253)，含共享域名）、
`paused_at`、`paused_until`（可空，空表示需运营者恢复）、`pause_reason`（`bounce_rate`/`complaint_rate`）、
`pauses_in_window`、时间戳；主键 `(organization_id, domain)`。退信与投诉计数从现有 `mail_messages` 与投递事件实时聚合
（24 小时窗口，已有索引够用；若慢再加汇总表）。

新表 `mail_bounce_tokens` 不需要：投递令牌是 `mail_messages.id` 与收件人序号的 HMAC 签名编码，边缘服务原样上报，API 验签后解析。

索引：删除 `idx_mail_domains_name`（全局唯一）；新增
`UNIQUE (organization_id, name)` 和部分唯一索引 `UNIQUE (name) WHERE status IN ('verified','suspended')`。
后者在数据库层保证「同名只有一个组织真正拥有」，两个组织并发验证时，后提交者得到唯一约束冲突，
服务层转成 `409 MAILBOX.DOMAIN_TAKEN`。

回滚：删除新增列与索引、恢复全局唯一索引；若存在同名多行 `pending`，回滚先删除较新的行（迁移注释写明会丢数据）。

### 6.2 共享域名的 DKIM

共享域名 `MAILBOX_DOMAIN` 不在 `mail_domains` 表里。运营者用新命令 `fastbuild mail:dkim:generate`
生成密钥，私钥放进 `MAILBOX_SHARED_DKIM_PRIVATE_KEY`（PEM，环境变量或密钥文件），命令同时打印要发布的 TXT 记录。

### 6.3 边缘服务的本地状态

只有发信队列：数据目录（持久卷）下每封信一个 `.eml` 和一个 `.json`（收件人、每个收件人的尝试次数和下次时间、
Zmail 消息 id）。不连数据库。卷丢失 = 队列中未投递的信丢失，所以部署要求持久卷；这是选 A 方案明确接受的代价。

## 7. 状态机

```
            检查：所有阻塞记录 ok
 pending ─────────────────────────▶ verified ◀───────────┐
    │                                  │                  │ 检查：所有权 TXT 恢复
    │ 别的组织先验证了同名域名           │ 所有权 TXT 连续缺失 72h
    ▼                                  ▼                  │
 (删除)                             suspended ────────────┘
```

- `verified` 时非所有权记录缺失只改记录状态，不改域名状态（沿用 `mail-domains.md` 「已有邮箱继续工作」的原则）。
- `pending` 超过 30 天未验证由每日任务删除，释放给别人认领。
- 删除域名：仍有邮箱时 `409 MAILBOX.DOMAIN_IN_USE`（沿用）。

## 8. HTTP 契约

### 8.1 浏览器面（改动）

| 路由 | 改动 |
|---|---|
| `GET /v1/mail-domains`、`GET /v1/mail-domains/{id}` | 响应新增 `onboarding`、`status` 可为 `suspended`、`suspended_at`；`records[].purpose` 新增 `ownership`、`bounce`；`records[]` 新增 `required`（布尔）、`applies_to`（`send_receive`/`send_only`/`both`）、`hint`（如「已有其他 MX，切换会把全部来信转到 Zmail」）|
| `PATCH /v1/mail-domains/{id}` | 新增：所有者/管理员修改 `capability`（`send_only` ↔ `send_receive`）；改为 `send_only` 立即关闭收信 |
| `POST /v1/mail-domains` | 请求新增 `capability`（DNS 模式必填）。DNS 模式：不再返回 `422 MAILBOX.DOMAIN_ZONE_NOT_FOUND`；同名被他组织验证时 `409 MAILBOX.DOMAIN_TAKEN`；仅被他组织 `pending` 时允许 |
| `POST /v1/mail-domains/{id}/setups` | DNS 模式下 `404`（不能替客户写 DNS），控制台不显示「设置」按钮 |
| `POST /v1/mail-domains/{id}/checks` | DNS 模式：同步执行（≤ 10 秒，单条超时 3 秒），返回检查后的域名 |
| `POST /v1/mailboxes/{id}/messages` 等发信路由 | 发件域名停用时 `422 MAILBOX.DOMAIN_SUSPENDED`；预热或暂停时照常 `202`，`queued_reason` 新增 `domain_warmup`、`domain_paused`、`provider_unavailable` |
| `GET /v1/mail-domains/{id}` | 另含 `capability`、`receiving_enabled`、`warmup_ends_at`、本组织在该域名上的 `sending_paused`（含原因与恢复时间）|

都是新增字段或新增枚举值。注意：`purpose`、`status` 新增枚举值在 oasdiff 里可能被判为响应变宽（警告而非错误），
Web 端解析需对未知值容错，并在同一个 PR 里更新生成类型。

### 8.2 边缘服务内部接口（新增，不给浏览器）

都用 `X-Mailbox-Signature: t=<unix>,v1=<hex>`，HMAC 密钥 `MAILBOX_EDGE_SECRET`（不复用 inbound 密钥，权限更高），
签名内容 `<t>.<path>.<body>`，时间容差 5 分钟（沿用 `verifyInbound`）。

| 路由 | 用途 | 响应 |
|---|---|---|
| `GET /v1/mail-edge/config` | 边缘服务每 60 秒拉一次 | `{ version, mx_host, domains: [{ name, status, receiving_enabled, dkim_selector, dkim_private_key_pem }] }`，含共享域名；`ETag` / `If-None-Match` 未变时 `304` |
| `POST /v1/mail-edge/recipient-checks` | RCPT 阶段 | 请求 `{ recipient }`；`200 {exists: true}` 或 `200 {exists: false, reason: "no_mailbox"|"domain_suspended"}` |

已有 `/v1/inbound-mail-messages` 与 `/v1/mail-delivery-events` 不变，边缘服务按现有格式调用。

### 8.3 边缘服务对 API 暴露的发信接口

`POST /send`：请求和响应与 Cloudflare Email Sending 相同（原型已验证兼容），`Authorization: Bearer <MAILBOX_RELAY_TOKEN>`。
`GET /healthz`、`GET /metrics`（Prometheus）。只监听内网。

## 9. 授权矩阵

| 操作 | 所有者/管理员 | 成员 | 他组织 | 边缘服务 |
|---|---|---|---|---|
| 查看域名与记录 | 是 | 是 | 404 | — |
| 添加 / 检查 / 删除域名 | 是 | 403 | 404 | — |
| 在域名上建邮箱 | 是（仅 verified）| 403 | 404 | — |
| 拉取配置、查收件人、投递来信、上报事件 | — | — | — | 仅凭签名 |

recipient-checks 的回答不区分「域名不存在」与「域名属于别人」之外的信息，只回答该地址能否收信，不泄露组织信息。

## 10. 跨模块数据流

- `mailbox` 模块是域名、记录、邮箱、DKIM 密钥的唯一写入者。边缘服务只读配置、只调用投递与事件接口。
- `infra/mailtransport` 新增 `dns` 检查器（`net.Resolver`，可配 `MAILBOX_DNS_RESOLVERS=1.1.1.1:53,8.8.8.8:53`，
  默认用公共解析器而不是系统解析器，避免内网 DNS 缓存/劫持导致误判）。没有业务规则。
- 发信传输 `selfhosted`：沿用原型的做法，复用 Cloudflare 客户端，端点换成 `MAILBOX_RELAY_URL`。

## 11. 定时与异步任务

| 任务 | 触发 | 幂等与恢复 |
|---|---|---|
| 域名每日复检 | API 内定时器（复用现有 worker 命令进程，单实例运行，Postgres advisory lock 防并发）| 只读 DNS + 条件更新；重复执行无害 |
| 清理 30 天未验证的 pending | 同上 | 按创建时间删除 |
| 边缘配置同步（本期规模目标：≤ 1000 个域名、2 个边缘实例；全量拉取足够，增量同步与多实例共享队列留到以后）| 边缘服务每 60 秒 | `ETag`；拉取失败继续用内存中的上一份，超过 10 分钟仍失败则拒收（`451`）而不是盲收 |
| 出站队列投递与重试 | 边缘服务常驻 | 队列文件即状态；重启后从磁盘恢复 |
| 投递事件上报 | 边缘服务 | 失败重试；API 按事件 id 去重 |

## 12. 浏览器界面

域名页（`web/`）在 DNS 模式下：

```
example.com   仅发信   待验证                       [检查] [改为收发] [删除]
请在你的 DNS 服务商添加以下记录（阿里云 / 腾讯云 / GoDaddy 填写说明 ▸）
 用途    类型  主机记录             值                                  状态
 所有权  TXT   _zmail               zmail-verify=9f2c…   [复制]          ✓
 签名    TXT   zmail1._domainkey    v=DKIM1; k=rsa; p=…   [复制]         ✓
 退信    MX    zbounce              mx.zmail.example (10) [复制]        ✗ 未找到
 退信    TXT   zbounce              v=spf1 include:_spf…  [复制]        ✓
 DMARC   TXT   _dmarc               v=DMARC1; p=none      [复制]         — 建议添加
（收发域名多一行：收信 MX @ → mx.zmail.example，状态「未开启：当前 MX 指向 mxbiz1.qq.com」）
上次检查：2 分钟前。DNS 生效可能需要几分钟到几小时。
```

- 「主机记录」同时显示相对写法（`@`、`_zmail`），多数国内 DNS 控制台要求填相对名。
- 停用状态显示红色横幅和原因、恢复方法。
- 「仅发信」域名的邮箱页提示：「这个域名的回信会进入你现有的邮件系统，不会出现在 Zmail」。
- 收发域名在 MX 未指向我们时显示「发信已可用，收信未开启」及切换 MX 的影响说明。
- 发信暂停或预热中：域名页和发信表单显示原因、恢复时间、当前额度。
- 文案进 `web/src/i18n/modules/mailbox/{en-US,zh-Hans}.ts`；开发 mock BFF 在第一次检查时把记录全部置为 `ok`。

## 13. 安全、隐私与审计

- **不做开放转发**：只接收已验证域名的收件人；发信只允许本实例域名作为发件人。有针对两者的自动化测试。
- **域名抢占**：所有权 TXT 证明 + 部分唯一索引；持续复检 + 72 小时停用应对域名转手。
- **密钥**：DKIM 私钥加密存储；`/v1/mail-edge/config` 只在内网、需签名；边缘服务不落盘私钥。
  `MAILBOX_EDGE_SECRET` 泄露 = DKIM 私钥泄露，运维文档写明轮换步骤（换密钥 → 所有域名重新生成 DKIM → 客户更新 DNS），
  这也是下一期做 DKIM 轮换的动机。
- **收信安全**：消息大小上限 25 MB、单连接收件人 ≤ 100、每 IP 并发连接限制、SMTP 超时 1 分钟；
  入站 STARTTLS（证书文件由运营者提供，支持热加载）。
- **审计**：域名添加、验证、停用、恢复、删除写入现有审计记录；边缘日志不记录邮件正文与主题，只记录收发地址、
  消息 id、结果码。

## 14. 可观测性与运维

- 边缘服务指标：入站接受/拒绝（按原因）、出站投递结果（按收件域：gmail.com、outlook.com、qq.com 等聚合）、
  队列长度与最老一封的年龄、配置同步年龄。告警：队列最老 > 1 小时、对 Gmail 的 5xx 比例 > 2%、配置同步失败 > 5 分钟。
- **运营者上线前置条件**（部署文档逐条列出，缺一条发信会进垃圾箱或被拒）：
  1. 固定公网 IP，云厂商放开出站 25 端口（阿里云、腾讯云、AWS 默认封禁，需要工单申请）；
  2. PTR 反向解析指向发信主机名，且正向解析一致；
  3. `_spf` include 记录、MX 主机 A 记录、证书；
  4. 新 IP 预热：前 2 周按日递增发送量（与已有每日发信上限配合）；
  5. 注册 Google Postmaster Tools、Microsoft SNDS 监控信誉；
  6. 查询主要黑名单（Spamhaus 等）。
- 部署：`deploy/` 新增边缘服务镜像与 compose 示例（两个 MX 实例，MX 优先级 10/20，各自持久卷）。

## 15. 测试与验证

| 层 | 内容 |
|---|---|
| API 服务测试（假 DNS 解析器）| 两种能力的记录清单与验证条件；仅发信域名拒收、收发域名 MX 未就绪时只发不收；预热额度与组织额度取小；退信率/投诉率触发暂停、24 小时自动恢复、7 天内第二次需运营者恢复；投递令牌验签与伪造令牌被丢弃；边缘不可达时 `provider_unavailable` 排队与 1 小时后失败；记录生成；每类记录的 ok/missing/conflict（含主域名已有别家 MX、zbounce 上有多余记录、多条 DMARC）；验证与认领删除；72 小时停用与恢复；30 天清理；停用后发信 `422`；共享域名不可添加 |
| API 仓库测试（真 PostgreSQL）| 部分唯一索引下两个组织并发验证同名域名只有一个成功；迁移上下行 |
| 边缘服务测试 | 收信：RCPT 拒绝三种原因、API 4xx/5xx 映射、DKIM/SPF/DMARC 评估、DSN 识别；发信：签名可被验证、队列持久化后重启续投、退避时间表、5 天放弃、事件格式与签名；配置同步 `304` 与失败降级 |
| 契约 | OpenAPI 更新 + 两端生成类型；oasdiff 无 ERR |
| Web | 域名页 DNS 模式渲染、复制、检查、停用横幅；mock BFF |
| 端到端（本地）| 原型同款：两个本地边缘实例互为对方 MX，跑接入→收→发→退信全流程 |
| 上线前真实验证 | 一个非 Cloudflare 的测试域名，发往 Gmail / Outlook / QQ 并检查原始头部 `spf=pass dkim=pass dmarc=pass`；mail-tester.com 评分 ≥ 9/10 |

## 16. 交付切片

0. **准备发信服务器（运营者，与切片 1 并行）**：选云厂商并申请放开出站 25 端口；固定公网 IP（建议 2 个）；
   设置 PTR；准备 `mx.` / `_spf.` 记录与证书；注册 Google Postmaster Tools、Microsoft SNDS。
   当前还没有这台服务器〔用户确认〕，所以切片 1、2 先在本地和 staging 验收，真实送达验收放到上线闸门。
1. **边缘服务可上线（共享域名）**：`mailserver/` 正式实现（队列、重试、事件、STARTTLS、SPF/DMARC 评估、
   recipient-checks、配置同步、`zbounce` 退信处理）；API `selfhosted` 提供方、`provider_unavailable` 排队、
   `/v1/mail-edge/*`、共享域名 DKIM 命令；部署文件。
   验收：staging 上两个边缘实例互为对方 MX，跑通收、发、重试、退信；不用 Cloudflare。
2. **客户自助接入（DNS 模式）**：迁移、两种能力、记录生成与 DNS 检查、认领规则、每日复检与停用、预热与自动暂停、
   契约、Web 域名页。验收：staging 上用一个真实的非 Cloudflare 域名完成接入与内部收发。
3. **运营加固**：Rspamd 旁路、指标与告警、运维文档（前置条件、预热、密钥轮换步骤、`mail:sending:resume`）。

**上线闸门**（切片 0 完成后）：发往 Gmail / Outlook / QQ 的信头部 `spf=pass dkim=pass dmarc=pass` 且进收件箱；
mail-tester.com ≥ 9/10；第 1 节的成功标准全部达成。

每个切片单独 PR，按现有规则先部署 API 再部署 Web；边缘服务与 API 之间的新接口在切片 1 内同时上线。

## 17. 留给实现者的决定

- 边缘服务内部包结构、队列文件命名、日志格式。
- SPF/DMARC 评估用的库（如 `go-msgauth` 的 dmarc 包 + 一个 SPF 库），只要通过上面的测试。
- 国内主流 DNS 服务商填写说明的具体文案。
