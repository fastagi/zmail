# 开发者 · 事务邮件（v2 切片 ⑤）设计

状态：设计稿，待评审（分支 `docs/v2-architecture`）
依据：[PRODUCT_REQUIREMENTS.zh-CN.md](../PRODUCT_REQUIREMENTS.zh-CN.md) R4（验证码与恢复链接按权限保护、创建与发送权限分离）、R6（回执与可追溯历史）；原型 `Developer.dc.html`（快速上手、事务模板、发送日志、额度与保护、Webhook 五个页签）。
编写规范：[BUSINESS_IMPLEMENTATION_PLAN_STANDARD.md](../BUSINESS_IMPLEMENTATION_PLAN_STANDARD.md)。
前置：[send-quota-queue.md](send-quota-queue.md)（发送队列）、[mail-template-editor.md](mail-template-editor.md)、[broadcast-variables.md](broadcast-variables.md)、[mailbox-organize.zh-CN.md](mailbox-organize.zh-CN.md)（`rfc_message_id`、标签）。

本文中"设计决定（待确认）"标出的是代码和既定决定都没有给出答案、由本文作出的取舍，评审时逐条确认；未标注的是既定决定或现有代码的事实。

## 1. 概述与范围（§3.1）

### 1.1 要解决的问题

开发者自己的网站或 App 需要从后端发注册验证码、欢迎信、重置密码、账单提醒。今天 Zmail 只有 `POST /v1/outbound-mail-messages`，它不适合这件事：

- `from` 必须是某个邮箱的地址，发出的信进该邮箱的"已发送"，组织成员按邮箱权限能读到完整正文，验证码和重置链接因此对人可见，违反 R4"通知列表不暴露秘密"。
- 调用方传原始主题和正文；模板虽可用（`template.id` + `variables`），但没有"秘密变量"的概念，渲染结果原样入库。
- 额度是组织级滚动 24 小时 200 次，个人邮件、模板试发和群发共用（`CountOutboundSince` 统计所有外发）。营销群发用满后，注册验证码会排进 `queued_reason=send_limit` 的队列，几小时后才发出，等于失效。
- 幂等键按邮箱作用域、无时间窗；没有按收件人或按密钥的限频；没有测试模式。

### 1.2 本切片交付的结果

开发者在组织控制台的"开发者"区域：建一把只能发事务邮件的密钥（只显示一次），建事务模板并把 `code`、`reset_url` 之类的变量标成秘密，用一行 HTTP 调用"模板 + 变量 + 幂等键"发信，在发送日志里看到遮罩后的记录和投递时间线，并通过 Webhook 收到 `email.*` 投递结果。事务邮件有独立的额度通道，营销用满不影响验证码。

### 1.3 范围内的流程

1. 建事务模板（声明变量、标记秘密、选发件地址）。
2. 建事务密钥（正式或测试）。
3. API 发送：校验 → 幂等 → 限频 → 抑制检查 → 入库（秘密加密）→ 派发 → 投递事件 → Webhook。
4. 测试模式与模拟地址。
5. 控制台发送日志、试发、额度与保护、抑制名单、Webhook 订阅。
6. 额度通道拆分：事务、营销群发、个人邮件分开计数（现有语义迁移）。

### 1.4 非目标

- 多发信厂商（R7）、短信、AI（含 AI 读取事务记录）。
- SDK 包（`@zmail/node`、Python SDK）；代码片段用原生 HTTP。
- 打开与点击追踪（现有契约不追踪）。
- 附件、抄送、多个收件人（见 D5）。
- 组织拥有的服务密钥（见第 19 节默认 1、D31）。
- 回复路由（回信进入指定邮箱并打标签，原 D26）：推迟到后续，见第 18 节"后续"与 D32；本次的事务模板没有回复路由字段，发出的信不带自定义 `Reply-To`。
- 任何会发送真实邮件的自动化步骤（包括代理执行的验证）：本次一律不做（D32）。
- 平台运维控制台（`admin/`）里的事务邮件视图；按组织单独调整通道额度的运维界面。
- 营销邮件的退订规则变化。

### 1.5 反目标（功能能用但算失败）

- 任何组织成员、日志、Webhook、审计或数据库备份之外的读取路径能看到秘密变量的明文。
- 营销群发用满后，事务邮件被延迟或拒绝。
- 一把事务密钥能群发、能读任何邮箱、能调用 `POST /v1/outbound-mail-messages`。
- 同一幂等键在 24 小时内发出两封。
- 现有组织升级后个人邮件或群发可用额度变少。

### 1.6 涉及的部署单元

- `api/`：`mailbox` 模块（事务邮件、模板、额度通道、抑制名单、投递事件、群发派发的抑制检查、worker）、`infra/mailtransport`（`Error.Responded`，D33）、`apikey` 模块（`mode`）、`webhook` 目录扩展（定义仍由 mailbox 提供）。
- `web/`：新"开发者"控制台、BFF 路由、mock BFF。
- `contracts/`：`openapi.yaml`、`MAILBOXES.md`、`API_KEYS.md`。
- `admin/`：不变。

### 1.7 首个版本的成功信号

- 一个试点组织用事务密钥从自己的后端发出注册验证码，在 Zmail 发送日志里只能看到 `••••••`，Webhook 收到 `email.delivered`。
- 同一组织营销通道满时，验证码仍在 10 秒内被服务商接受（p95）。
- 验收算例（第 15 节）全部通过，越权与重复发送为零。

## 2. 方案取舍

### D1 新资源而不是扩展 `outbound-mail-messages`（设计决定（待确认））

| 方案 | 做法 | 取舍 |
|---|---|---|
| **新资源 `/v1/transactional-messages`（采用）** | 新路径、新 scope、新响应结构、独立存储 | oasdiff 视新增路径为非破坏性；响应可以不带 `mailbox_id`；scope 与 `mail:send` 互不包含 |
| 扩展 `SendOutboundMailMessageRequest` | 加 `transactional: true`，`from` 改为可选 | 请求侧放宽在 oasdiff 下可接受，但 `MailMessageResponse` 的 `mailbox_id`、`placement`、`thread_id` 等必填字段对没有邮箱的消息无意义；把它们改为可空或可缺省属于响应侧破坏性变更（`response-property-became-nullable` / 删除必填属性）。同一路由用两种 scope 也违背"scope 精确匹配路由"的现有约定 |

现有约定也支持新资源：会话日志 `GET /v1/mail-messages` 与密钥路由 `/v1/outbound-mail-messages/{id}` 本来就是两条不同路径；本切片照此分为密钥路由 `/v1/transactional-messages` 与会话路由 `/v1/transactional-mail-log`。

### D29 事务邮件放在 `mailbox` 模块内（设计决定（待确认））

事务邮件依赖模板、已验证域名、发信传输、投递事件接收、抑制名单和 worker，全部由 `mailbox` 模块拥有。另起一个模块会让两个模块同时写 `mail_message_events` 的同类事实或互相调用私有仓储。所以作为 `mailbox` 内的子领域（文件 `transactional_*.go`），随 `mailbox` 启用；它拥有自己的表，不写 `mail_messages`。

## 3. 角色与访问面（§3.2）

| 角色 | 目标 | 入口 | 可读范围 | 可变更范围 | 审批 | 拒绝表现 |
|---|---|---|---|---|---|---|
| 事务密钥（正式） | 后端发事务邮件 | `X-API-Key` + `Organization-Id` 调 `/v1/transactional-messages` | 本组织的事务消息（收件人完整、主题与变量遮罩） | 新建事务消息 | 无 | 缺 scope `403 PERMISSION.DENIED`；持有人非成员 `404 ORGANIZATION.NOT_FOUND` |
| 事务密钥（测试） | 联调，不投递 | `X-API-Key`（测试密钥）+ `Organization-Id` 调 `/v1/transactional-messages` | 本组织 `mode=test` 的事务消息 | 新建测试消息 | 无 | 调用其他任何路由 `403 PERMISSION.DENIED` |
| 组织成员 `member` | 接入、排查 | 控制台"开发者" | 事务模板（只读）、发送日志（收件人遮罩）、额度上限（无用量）、Webhook 端点元数据（按 webhook 模块现有规则） | 给自己建密钥、向自己的账号邮箱试发 | 无 | 改事务模板 `403 PERMISSION.DENIED` |
| 组织 `owner`/`admin` | 管理发送 | 控制台"开发者" | 成员可读的全部 + 通道用量 + 抑制名单 | 事务模板增删改、解除抑制、Webhook 端点（按现有规则） | 无 | — |
| 邮件 worker（系统） | 派发、重试、清理 | `fastbuild mail:broadcast:work` | 全部事务消息 | 状态迁移、清空密文、清理 | 无 | — |
| 邮件边缘（系统） | 回报投递事件 | `POST /v1/mail-delivery-events`（签名） | 无 | 追加事件 | 无 | 签名错 `401 MAILBOX.INBOUND_SIGNATURE_INVALID` |
| Webhook 接收方（外部） | 同步投递结果 | 组织配置的 HTTPS 端点 | 收到的事件 | 无 | 无 | — |
| 收件人（外部） | 收信、回信 | 邮件客户端 | 自己的邮件 | 回信 | 无 | — |
| 平台运维 | — | 无新入口 | 无新增 | 无新增 | — | — |

附加限制：

- 密钥属于用户（现有模型），以持有人在 `Organization-Id` 所指组织中的当前成员身份行事；持有人离开组织后下一次请求即失效（`API_KEYS.md`、`MAILBOXES.md` Programmatic Sending）。
- 事务密钥不需要任何邮箱的访问权：发件地址是已验证域名上的身份，不是邮箱（决定 4）。
- `owner`/`admin` 不因角色读到秘密明文：任何读路径都只返回遮罩值。

## 4. 端到端流程（§3.3）

### 4.1 API 发送一封事务邮件

参与者：调用方后端、API（mailbox 事务子领域）、PostgreSQL、限频存储、发信服务商（Cloudflare）、邮件边缘、webhook 模块。

```text
后端 ──POST /v1/transactional-messages──▶ API
API: 鉴权(scope, mode) → 校验 → 按密钥限频 → 幂等查找 ─(命中)─▶ 200 原结果
                                       └(未命中)→ 按收件人限频 → 加载模板/域名/抑制名单
API: BEGIN TX mailbox
       INSERT transactional_messages(status=queued 或 suppressed, payload 密文)
       [suppressed] ENQUEUE webhook email.suppressed
     COMMIT ──▶ 202 {id, status}
AFTER COMMIT: 后台快速派发(lease 该条) ──▶ 服务商
   服务商接受 → TX: status=sent, 清空密文, 事件 accepted, ENQUEUE email.sent
   收到 HTTP 状态的非永久失败(Permanent=false 且 Responded=true) → TX: queued_reason=retry, next_attempt_at, attempts+1
   永久失败 → TX: status=failed, 清空密文, ENQUEUE email.failed
   其他(未收到 HTTP 状态, 结果未知) → TX: status=failed(transport.unknown_outcome), 清空密文, ENQUEUE email.failed, 不重发
worker 每轮兜底：未被快速派发、租约过期、重试到期、等额度的消息
邮件边缘 ──POST /v1/mail-delivery-events──▶ API: 按服务商 message id 找到事务消息
   TX: 追加事件, 更新 last_event, [永久退信/投诉] INSERT mail_suppressions, ENQUEUE email.*
webhook worker ──签名 POST──▶ 接收方（现有重试）
```

逐项说明：

1. **发起与前置**：密钥有效、含 `mail:transactional`（或 `*`），`Organization-Id` 校验成员身份；功能已启用（`MAILBOX_TRANSACTIONAL_ENABLED=true` 且加密密钥可用）。
2. **权威输入与校验**：请求体 `template`（模板 key）、`to`（单个地址）、`variables`；请求头 `Idempotency-Key`（必填，D12）。变量必须是模板声明过的，必填变量必须有值。
3. **创建的记录**：一行 `transactional_messages`；被抑制时不存密文。
4. **状态迁移**：`queued` →（`sent` | `failed`）或直接 `suppressed`；见第 8 节。
5. **跨模块**：webhook 发布者（同事务入队）；限频存储（事务外，先于写入）。
6. **用户可见结果**：`202` 与消息；之后 `GET` 或 Webhook 看结果。
7. **拒绝与恢复**：校验失败不存任何记录；被抑制的地址存一条 `suppressed` 记录以便追溯；服务商失败按 D14/D33 重试或终止：只有服务商返回了 HTTP 状态的非永久失败才重试，其余非永久失败视为结果未知，置 `failed`，不重发。
8. **超时、取消、重试**：请求只等到提交；派发失败由 worker 重试；排队超过 1 小时置 `failed`（`send.expired`，D15）；租约 2 分钟。
9. **审计与通知**：发送不写审计（量大、无特权操作，与现有 `outbound-mail-messages` 一致）；结构化日志与指标见第 13 节。

### 4.2 测试模式与模拟地址

- 测试密钥（`mode=test`）发送的消息记 `mode=test`，渲染并校验全部变量，**从不调用服务商，不计入任何额度**。
- 模拟地址（D21）：`@test.zmail.im` 域上的 `delivered`、`bounced`、`soft-bounced`、`complained`、`suppressed`，可带 `+标签`，正式密钥和测试密钥都可以用，同样不调用服务商、不计额度。
- 测试密钥发往普通地址时按 `delivered@` 模拟。
- 模拟结果写入同一张事件表（`detail` 为 `simulated`），照常发布 Webhook（`mode`、`simulated` 字段区分），**从不写抑制名单**。

### 4.3 控制台试发

成员在"快速上手"点"发一封试试"：Web 调 `POST /v1/transactional-test-sends`，用所选事务模板（变量取请求值，缺省取示例值）发往**调用者自己的账号邮箱**；页面选"正式模式"时计入事务通道，选"测试模式"时按模拟处理。其余流程与 4.1 相同，`api_key_id` 为空，`created_by` 为调用者。

### 4.4 回复路由（推迟）

本次不做。原设计（模板指定回复邮箱与标签，按 `In-Reply-To`/`References` 给回信打标签，原 D26）移到第 18 节"后续"。本次的事务模板没有回复路由字段，事务消息不带自定义 `Reply-To`，收信流程不改（D32）。

### 4.5 额度通道（现有语义迁移）

见第 6.6 节和第 10 节。迁移后：个人邮件、营销群发、事务邮件各自按组织滚动 24 小时计数，互不占用；worker 每轮先派发事务邮件。

### 4.6 拒绝、恢复一览

| 情形 | 结果 | 是否存记录 | 是否计额度 |
|---|---|---|---|
| 校验失败、模板不存在或不是事务模板、变量缺失或未声明 | `422`/`404` | 否 | 否 |
| 按密钥超频 | `429 COMMON.RATE_LIMITED` + `Retry-After` | 否 | 否 |
| 按收件人超频 | `429 MAILBOX.RECIPIENT_RATE_LIMITED` + `Retry-After` | 否 | 否 |
| 收件人在抑制名单 | `202`，`status=suppressed`，`email.suppressed` | 是（无密文） | 否 |
| 事务通道满、等待数未达上限 | `202`，`queued_reason=send_limit` | 是 | 发出时计 |
| 事务通道满且等待数达上限 | `429 MAILBOX.SEND_LIMIT_REACHED` | 否 | 否 |
| 服务商返回 HTTP 状态的非永久失败（`Responded=true`） | 自动重试，最多 3 次 | 是 | 每次尝试计 1 |
| 服务商永久拒绝 | `failed`，`email.failed` | 是 | 计 |
| 未收到 HTTP 状态的失败（结果未知，`Responded=false`） | `failed`（`transport.unknown_outcome`），不重发 | 是 | 计 |
| 排队超过 1 小时 | `failed`（`send.expired`） | 是 | 否 |

## 5. 模块拆分（§3.4）

### 5.1 模块目录

| 模块 | 职责 | 输入 | 输出 | 独占数据 | 依赖 | 消费方 | 优先级 |
|---|---|---|---|---|---|---|---|
| 事务消息（`mailbox` 子领域） | 事务发送、幂等、限频、秘密加密与清除、派发、重试、投递状态、30 天保留 | 密钥请求、会话试发、worker、投递事件 | 事务消息、事件、Webhook | `transactional_messages`、`transactional_message_events` | 模板、额度通道、抑制名单、`crypto`、`ratelimit`（经 Wire 新注入，D34）、`mailtransport`、webhook 发布者 | 后端、Web 开发者控制台 | P0：核心 |
| 邮件模板（`mailbox`，变更） | 增加事务类型、key、声明变量与秘密标记、发件地址 | 会话 CRUD | 模板 | `mail_templates` | 域名 | 事务消息、Web | P0：事务发送的前提 |
| 发送通道（`mailbox`，变更） | 三条通道分开计数、优先级、上限配置 | 各发送路径 | 允许/排队/拒绝判定、用量查询 | `mail_messages.send_lane` | 配置 | 个人发送、群发、事务消息、Web | P0：先行交付，其他切片依赖 |
| 抑制名单（`mailbox`，新增） | 组织级唯一的永久退信与投诉地址名单；事务发送与群发派发都检查；管理员解除 | 投递事件（`bounced`、`complained`）、管理员解除 | 是否抑制、名单 | `mail_suppressions` | 无 | 事务消息、群发、Web | P1 |
| API 密钥（`apikey`，变更） | `mode` 与测试前缀；`RequireScopes` 内默认拒绝测试密钥 | 会话建密钥、请求鉴权 | 密钥、请求上下文 | `api_keys.mode` | 无 | 所有密钥路由 | P0 |
| Webhook（`webhook`，不改代码结构） | 签名、投递、重试（现有） | `PublishWebhook` | 投递 | 现有表 | — | 事务消息 | P1 |
| Web 开发者控制台 | 五个页签、BFF、mock | 浏览器 | 页面 | 无 | 契约 | 成员 | 随各切片 |

### 5.2 模块：事务消息

#### Objective
后端用模板、变量和幂等键安全地发出事务邮件，秘密值只在投递所需的时间内以密文存在，结果可追溯 30 天。

#### Responsibilities
- 校验请求、渲染模板、计算遮罩副本与密文。
- 幂等（24 小时窗口）、按密钥和按收件人限频。
- 抑制检查（入库时和派发时各一次）。
- 派发、重试、超时失败、终态清除密文。
- 接收服务商投递事件（经现有接收路由分流）、维护 `last_event`。
- 发布 `email.*` Webhook。
- 30 天清理。

#### Explicit exclusions
- 不写 `mail_messages`，不出现在任何邮箱、`GET /v1/mail-messages`、`GET /v1/mail-message-stats` 中。
- 不做附件、抄送、多收件人、定时发送。
- 不追踪打开与点击。

#### Actors and access
- 事务密钥：创建本组织消息；按 id 读本组织消息（收件人完整，主题与变量遮罩）。
- 成员：读日志（收件人也遮罩）；向自己试发。
- worker：全部消息的派发与清理。

#### Domain objects and invariants
- 事务消息：见第 6.1 节。不变量：`status <> 'queued'` 时 `payload_ciphertext IS NULL`；任何对外 DTO 不含密文和秘密明文；`mode='test'` 或 `simulated=true` 时 `send_attempted_at IS NULL` 且从不调用服务商。

#### Owned persistence
- `transactional_messages`、`transactional_message_events`（第 7 节）。

#### Public contract
- `/v1/transactional-messages`（POST 新建；GET `{message_id}` 读取）、`/v1/transactional-mail-log`（GET 列表与 `{message_id}` 详情）、`/v1/transactional-test-sends`（POST）。

#### Internal dependencies
- Synchronous：模板、发送通道、抑制名单、`ratelimit.Stores`、`crypto.AESEncryptor`、`mailtransport.Transport`。`mailbox` 模块今天没有注入 `ratelimit`（`api/internal/infra/ratelimit` 只被 `bootstrap`、`app` 和 `user` 模块使用），S3 须在 Wire 中把 `*ratelimit.Stores` 注入 `mailbox`（D34）。
- Events consumed：投递事件（经 `RecordDeliveryEvent` 分流）。
- Events published：`email.sent`、`email.delivered`、`email.bounced`、`email.complained`、`email.suppressed`、`email.failed`。
- Infrastructure capabilities：`crypto`、`ratelimit`、`mailtransport`、`domain.WebhookPublisher`。

#### Transactions, concurrency, and idempotency
- 写消息与入队 Webhook 在同一事务（`s.apply` 现有模式）。
- 幂等唯一索引 `(organization_id, mode, idempotency_key)`；并发同键靠 `INSERT … ON CONFLICT DO NOTHING` 后重读。
- 派发用 `FOR UPDATE SKIP LOCKED` 租约；按组织的通道余量计算加 `pg_advisory_xact_lock`（沿用 `LeaseWaitingSends`）。

#### Failure and recovery
- 服务商调用永远在事务外；结果记录用 `context.WithoutCancel` + 超时（沿用 `deliver`）。
- 服务商调用返回非永久错误但 `Responded=false`（`transport.unreachable`、`transport.invalid_response`、`transport.response_too_large`：服务商可能已经接受）：置 `failed`（`transport.unknown_outcome`），不重发（D33）。
- 进程在服务商调用后、记录前崩溃：租约过期后 worker 发现 `send_attempted_at` 已设置但状态仍 `queued`，置 `failed`（`transport.unknown_outcome`），**不重发**，与 R5"结果未知不盲目重试"一致。
- 密文无法解密（密钥轮换或损坏）：置 `failed`（`send.payload_unavailable`），清空密文。

#### Browser surfaces
- 快速上手、发送日志页签。

#### Observability and audit
- 见第 13、14 节。

#### Acceptance criteria
- Given 一个含秘密变量 `code` 的模板，when 发送 `code=482913`，then 库中 `subject_masked`、`variables_masked`、日志接口、Webhook 均不含 `482913`，且 `sent` 后 `payload_ciphertext` 为空。
- Given 同一幂等键 10 秒内请求两次，then 只调用服务商一次，第二次 `200` 返回同一 id。
- Given 营销通道已满，when 发验证码，then 立即派发。

#### Verification
- `cd api && go test ./internal/modules/mailbox/... -run Transactional`，PostgreSQL 仓储测试，Kest 流程（第 15 节）。

### 5.3 模块：邮件模板（变更）

#### Objective
同一套模板支持事务类型：声明变量、标记秘密、绑定发件地址，并保证事务模板只能走事务通道。

#### Responsibilities
- `kind`：`standard`（默认，现有行为）或 `transactional`，创建后不可改（D6）。
- 事务模板必填 `key`、`from_address`、`variable_definitions`；可选 `from_name`。本次没有回复路由字段（D32）。
- 校验：模板里出现的每个占位符都已声明，声明的每个变量都被使用；秘密变量不得带 fallback；`from_address` 的域名是本组织已验证的自有域名（D8）。

#### Explicit exclusions
- 不做模板版本历史；事务消息入库时渲染并加密，之后改模板不影响已入库消息。
- 不改可视化编辑器的设计文档格式。

#### Actors and access
- 成员：读全部模板，增删改 `standard` 模板（现有）。
- `owner`/`admin`：另可增删改 `transactional` 模板（D7）。

#### Domain objects and invariants
- 见第 6.2 节。

#### Owned persistence
- `mail_templates` 新列（第 7.2 节）。

#### Public contract
- 现有 `/v1/mail-templates`、`/v1/mail-templates/{template_id}`、`/v1/mail-template-previews`、`/v1/mail-templates/{template_id}/tests` 的请求与响应增加字段；列表增加 `kind` 查询参数。

#### Internal dependencies
- Synchronous：域名仓储（验证状态）。
- Events：无。

#### Transactions, concurrency, and idempotency
- 单表写入；名称与 key 唯一由数据库约束保证，冲突转 `409`。替换是整行覆盖（现有语义）。

#### Failure and recovery
- 发件域名后来失去验证：模板保留；发送时 `422 MAILBOX.DOMAIN_NOT_VERIFIED`，排队中的消息派发时置 `failed`（`send.sender_unverified`）。

#### Browser surfaces
- 事务模板页签；现有"模板"页面按 `kind=standard` 过滤。

#### Observability and audit
- 走现有写请求审计。

#### Acceptance criteria
- Given 一个 `transactional` 模板，when 在邮箱发信或群发中引用，then `422 MAILBOX.TEMPLATE_KIND_MISMATCH`。
- Given 成员身份，when 修改事务模板，then `403 PERMISSION.DENIED`。

#### Verification
- `go test ./internal/modules/mailbox/... -run Template`；Web 模板表单测试。

### 5.4 模块：发送通道（变更）

#### Objective
事务、营销群发、个人邮件分开计数，营销用满不影响另外两条，现有组织的额度不变少。

#### Responsibilities
- `mail_messages.send_lane` 标记个人（`personal`）与群发（`broadcast`）；事务通道统计 `transactional_messages`。
- 每条通道一个上限（实例配置），滚动 24 小时，按"发起服务商调用的时刻"计数，失败的尝试也计（现有口径）。
- 个人通道沿用等待队列（`queued_reason=send_limit`）；群发通道沿用暂停（`stop_reason=send_limit_reached`）；事务通道排队（D16）。
- 用量查询（仅 `owner`/`admin`）。

#### Explicit exclusions
- 不做按组织单独设置上限（第 19 节默认 3、D31）。
- 不做跨通道的组织总上限；服务商侧的账号上限由运维按通道配置控制。

#### Actors and access
- 所有发送路径读；`owner`/`admin` 读用量。

#### Domain objects and invariants
- 一封邮件恰属于一条通道；通道在入库时确定，之后不变。

#### Owned persistence
- `mail_messages.send_lane` 列（第 7.4 节）。

#### Public contract
- `GET /v1/mail-send-lanes`（新）；`GET /v1/mailbox-domains` 增加上限字段；`MAILBOXES.md` Send 一节改写额度说明。

#### Internal dependencies
- 配置：`MAILBOX_DAILY_SEND_LIMIT`（现有，个人通道）、`MAILBOX_DAILY_BROADCAST_LIMIT`、`MAILBOX_DAILY_TRANSACTIONAL_LIMIT`（新）。

#### Transactions, concurrency, and idempotency
- 计数是软上限（现有契约："concurrent requests may exceed it by the number of requests in flight"），worker 端用按组织的咨询锁精确控制余量。

#### Failure and recovery
- 迁移回填失败则迁移整体回滚，旧代码继续以共享额度运行。

#### Browser surfaces
- 额度与保护页签。

#### Observability and audit
- 每通道用量指标（第 14 节）。

#### Acceptance criteria
- Given 群发通道 200/200，when 成员手写发信，then `201` 立即发出（今天会排队）。
- Given 升级前 24 小时内有 150 封个人邮件、50 封群发，when 升级完成，then 个人通道用量 150、群发 50。

#### Verification
- PostgreSQL 计数与回填测试；`TestBroadcastLaneDoesNotConsumePersonalLane`。

### 5.5 模块：抑制名单（新增）

#### Objective
永久退信和投诉过的地址在组织内只记一处（`mail_suppressions` 是组织唯一的抑制名单），事务发送与群发派发共用（D35）。

#### Responsibilities
- 记录：群发或事务消息的投递事件 `bounced`、`complained`（非模拟）在同一事务里写入，与现有 `RecordDeliveryEvent` 只对这两类抑制联系人的规则一致；`rejected`（含发送时服务商报告的 `transport.suppressed`）不写入。群发现有的联系人抑制逻辑保持不变。
- 查询：事务发送入库时和派发时检查；群发派发每个收件人前也检查，命中则跳过该收件人（在现有联系人状态检查之外）。
- 解除：`owner`/`admin` 删除一行，写审计。

#### Explicit exclusions
- 个人邮件发送不检查（今天也不检查，行为不变）。
- 写入名单不改变联系人状态，解除也不改变联系人状态（联系人状态仍按现有规则由群发投递事件与成员操作维护）。
- 退订（`unsubscribed`）不进入本名单，事务邮件不受退订影响（见 9.3）。

#### Actors and access
- `owner`/`admin`：列出、按地址精确查找、解除。成员：无。

#### Domain objects and invariants
- `(organization_id, email)` 唯一；地址小写。

#### Owned persistence
- `mail_suppressions`（第 7.5 节）。

#### Public contract
- `GET /v1/mail-suppressions`、`DELETE /v1/mail-suppressions/{suppression_id}`。

#### Internal dependencies
- 投递事件接收；群发派发（读取）。

#### Transactions, concurrency, and idempotency
- 写入 `ON CONFLICT (organization_id, email) DO NOTHING`；解除重复执行返回 `204`。

#### Failure and recovery
- 解除与新退信并发：解除先提交则新退信再次写入（正确）；新退信先提交则解除删除最新行（管理员显式意图）。

#### Browser surfaces
- 额度与保护页签下的"退信与投诉名单"。

#### Observability and audit
- 审计 `mail_suppression.removed`（操作者、组织、行 id、原因，不含地址明文以外的信息；地址本就是管理员输入的查询条件）。

#### Acceptance criteria
- Given 一次事务邮件永久退信，when 再向该地址发事务邮件，then `202`、`status=suppressed`、不计额度；之后群发派发到该地址的收件人被跳过（群发派发检查 `mail_suppressions`），而该联系人的状态保持不变。
- Given 服务商在发送时报告 `transport.suppressed`（`rejected`），then 该消息为 `suppressed`，但 `mail_suppressions` 不新增行。

#### Verification
- PostgreSQL 回填测试、`TestPermanentBounceSuppressesAcrossLanes`。

### 5.6 模块：API 密钥（变更）

#### Objective
支持测试密钥，前缀与正式密钥不同，测试密钥只能用于明确声明支持测试模式的路由。

#### Responsibilities
- `mode`：`live`（默认）或 `test`，创建后不可改。
- 前缀：正式密钥沿用 `fastbuild_<短 id>`；测试密钥 `fastbuild_test_<短 id>`（D3）。
- 中间件把 `apiKeyMode` 放入请求上下文；默认拒绝测试密钥的判断放在 `RequireScopes` 内部（gin 中间件按顺序执行，后挂的 `AllowTestKeys()` 无法撤销前面已经发生的拒绝），路由用 `RequireScopes` 的选项声明允许测试密钥，例如 `apikey.RequireScopes(apikey.AllowTestKeys, "mail:transactional")` 或等价的 `RequireScopesAllowingTest(...)`；未声明的路由对测试密钥返回 `403 PERMISSION.DENIED`（D36）。

#### Explicit exclusions
- 不新增组织拥有的密钥；不改 scope 语法。

#### Actors and access
- 用户管理自己的密钥（现有）。

#### Domain objects and invariants
- 已有密钥迁移为 `live`；`mode` 不可变。

#### Owned persistence
- `api_keys.mode`。

#### Public contract
- `CreateAPIKeyRequest.mode`（可选）、`APIKey.mode`（响应新增）。

#### Internal dependencies
- 无。

#### Transactions, concurrency, and idempotency
- 不变。

#### Failure and recovery
- 不变。

#### Browser surfaces
- 快速上手的建密钥流程；现有 API 密钥页面显示 `mode`。

#### Observability and audit
- 现有建密钥审计增加 `mode` 字段。

#### Acceptance criteria
- Given 一把测试密钥含 `mail:send`，when 调 `POST /v1/outbound-mail-messages`，then `403 PERMISSION.DENIED`。

#### Verification
- `go test ./internal/modules/apikey/...`；`openapi_contract_test.go`。

### 5.7 模块：Web 开发者控制台

见第 12 节。

## 6. 领域模型与术语（§3.5）

术语与 `CONTEXT.md` 一致：API key scope 仍是对用户密钥的削减；Webhook event / delivery 沿用；"事务消息"是本切片新增的业务术语，加入 `MAILBOXES.md`，不进入全局 `CONTEXT.md`（只属于 mailbox starter）。

| 术语 | 含义 |
|---|---|
| 事务消息（transactional message） | 由应用后端为单个收件人触发的系统邮件，按模板渲染，独立存储 30 天，不属于任何邮箱 |
| 事务模板 | `kind=transactional` 的邮件模板 |
| 秘密变量 | 事务模板中声明 `secret: true` 的变量；值只以密文短暂存在 |
| 遮罩值 | 秘密变量对外展示的替代文本 |
| 发送通道（send lane） | 计数额度的分类：`personal`、`broadcast`、`transactional` |
| 抑制名单 | 组织级"不再发送"地址集合 |
| 模拟地址 | 模拟域上用于测试投递结果的保留地址 |
| 测试密钥 | `mode=test` 的 API 密钥 |

### 6.1 事务消息

- 身份：自增 `id`（与其他资源一致，正整数）；生命周期见第 8 节。
- 所属：事务消息子领域。
- 不可变（入库后）：`organization_id`、`mode`、`simulated`、`api_key_id`、`created_by`、模板快照（`template_id`、`template_key`）、发件人快照（`from_address`、`from_name`）、`to_address`、`subject_masked`、`variables_masked`、`idempotency_key`、`idempotency_fingerprint`、`rfc_message_id`、`created_at`。
- 可变：`status`、`queued_reason`、`attempts`、`next_attempt_at`、`lease_until`、`send_attempted_at`、`provider_message_id`、`failure_code`、`last_event`、`sent_at`、`payload_ciphertext`（只能从有值变为空）、`idempotency_key`（24 小时后可被释放为空）。
- 值对象：
  - 遮罩规则（设计决定（待确认））：值能解析为 `http`/`https` URL 时显示 `主机名/••••`；否则一律 `••••••`（固定 6 个点，不泄露长度）。主题里的秘密值同样替换。
  - 收件人遮罩：本地部分前 2 个字符加 `••`（本地部分不超过 2 个字符时只保留 1 个），域名完整，例：`li••@example.com`。
  - 密文：`payload_ciphertext` = AES-256-GCM（`crypto.NewAESEncryptorFromString(MAILBOX_SECRET_ENCRYPTION_KEY)`，随机 nonce 前置，沿用 webhook 签名密钥的做法）加密的 JSON `{subject, html, text}`，即渲染后的完整内容。
  - 幂等指纹：HMAC-SHA256（键由加密密钥派生，作用域串 `transactional-idempotency`），输入为规范化的 `template_key`、`to`、按名排序的全部变量（含秘密值）。不用裸 SHA-256，避免 6 位验证码可被离线穷举。
- 关系：属于 1 个组织；引用 0 或 1 个模板（模板删除后置空）；有 0..n 个事件。
- 权威字段与快照：模板、发件人、回复地址均为入库时快照；`last_event` 由事件派生（规则同 `MAILBOXES.md` Delivery Events，单收件人时即该收件人当前状态）。
- 保留：创建 30 天后由 worker 删除（含事件）；终态时清空密文；组织删除时级联删除。

### 6.2 事务模板（`mail_templates` 的扩展）

- `kind` 不可变；`key`：`^[a-z][a-z0-9-]{0,63}$`，组织内唯一，用于 API 引用（原型 `signup-code`）。
- `variable_definitions`：数组，每项 `{name, secret, required}`；`name` 同现有变量名规则；最多 100 项。`required=false` 的变量必须在模板中带 fallback（`{{name|fallback}}`），`required=true` 的不得带；秘密变量必须 `required=true`。
- `from_address` 小写裸地址，域名须为本组织 `status=verified` 的 `mail_domains`（`send_receive` 或 `send_only` 都可）。实例使用 `log` 服务商时也允许实例自身的 `MAILBOX_DOMAIN`，便于本地开发（设计决定（待确认））。
- `sample_variables` 对秘密变量也允许示例值（仅用于预览和试发，界面标明"示例，不是真实秘密"）。
- 不变量：`standard` 模板不能有上述事务字段；事务模板不能用于邮箱发送、模板试发（`/v1/mail-templates/{id}/tests`）和群发。

### 6.3 发送通道

| 通道 | 来源 | 上限配置 | 满时行为 |
|---|---|---|---|
| `personal` | 控制台发信、回复、`POST /v1/outbound-mail-messages`、模板试发 | `MAILBOX_DAILY_SEND_LIMIT`（现有，默认 200） | 排队（现有） |
| `broadcast` | 群发 worker 发出的每个收件人 | `MAILBOX_DAILY_BROADCAST_LIMIT`，未设置时取 `MAILBOX_DAILY_SEND_LIMIT` 的值 | 群发暂停（现有） |
| `transactional` | `mode=live` 且非模拟的事务消息 | `MAILBOX_DAILY_TRANSACTIONAL_LIMIT`，未设置时取 `MAILBOX_DAILY_SEND_LIMIT` 的值 | 排队，等待数上限等于通道上限，超出 `429` |

### 6.4 抑制项

`(organization_id, email, reason ∈ {bounced, complained}, source ∈ {broadcast, transactional, backfill}, created_at)`。存在即抑制；删除即解除；无其他状态。与联系人状态相互独立（D35）。

### 6.5 API 密钥模式

`mode ∈ {live, test}`，不可变。

### 6.6 时间语义

所有时间为 `timestamptz`，按 UTC 瞬时存储和比较。滚动 24 小时 = `[now - 24h, now)`；幂等窗口 = 首条消息 `created_at` 起 24 小时；保留 = `created_at` 起 30 天；限频是固定窗口（`ratelimit` 基础设施的语义）：按收件人 60 秒窗口最多 3 封，按密钥 1 秒窗口最多 20 次。无本地时间规则。

## 7. 持久化设计（§3.6）

全部为 PostgreSQL 冻结 SQL 迁移（沿用 `api/database/migrations/` 约定），同时更新 schema golden 测试。时间列均为 `timestamptz`。

### 7.1 `api_keys`（迁移 `2026_11_23_090000_add_api_key_mode`）

- `ADD COLUMN mode varchar(8) NOT NULL DEFAULT 'live' CHECK (mode IN ('live','test'))`。
- 回填：默认值即回填。回滚：删除列（测试密钥随之变为正式密钥，所以回滚前须先吊销测试密钥；`UPGRADING.md` 写明）。

### 7.2 `mail_templates`（迁移 `2026_11_23_100000_add_transactional_templates`）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| `kind` | `varchar(16)` | 否 | `'standard'` | `CHECK (kind IN ('standard','transactional'))` |
| `template_key` | `varchar(64)` | 是 | — | 事务模板必填 |
| `from_address` | `varchar(320)` | 是 | — | 事务模板必填 |
| `from_name` | `varchar(100)` | 否 | `''` | |
| `variable_definitions` | `jsonb` | 否 | `'[]'` | |

- 唯一索引 `mail_templates_key_idx ON mail_templates (organization_id, template_key) WHERE template_key IS NOT NULL`。
- 检查约束：`kind = 'transactional'` 时 `template_key`、`from_address` 非空；`kind = 'standard'` 时二者为空且 `variable_definitions = '[]'`。回复路由列推迟到后续（D32）。
- 已有模板全部为 `standard`，无回填。回滚：先删除事务模板（`UPGRADING.md` 给出 SQL 由迁移 down 执行：`DELETE FROM mail_templates WHERE kind = 'transactional'`），再删列。

### 7.3 `transactional_messages` 与 `transactional_message_events`（迁移 `2026_11_23_110000_create_transactional_messages`）

`transactional_messages`：

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| `id` | `bigserial` PK | 否 | |
| `organization_id` | `bigint` | 否 | `REFERENCES organizations(id) ON DELETE CASCADE` |
| `mode` | `varchar(8)` | 否 | `CHECK (mode IN ('live','test'))` |
| `simulated` | `boolean` | 否 | 默认 `false` |
| `api_key_id` | `bigint` | 是 | 控制台试发为空；不设外键（密钥软删除，记录保留来源） |
| `created_by` | `bigint` | 否 | 密钥持有人或试发者的用户 id |
| `template_id` | `bigint` | 是 | `REFERENCES mail_templates(id) ON DELETE SET NULL` |
| `template_key` | `varchar(64)` | 否 | 快照 |
| `from_address` / `from_name` | `varchar(320)` / `varchar(100)` | 否 | 快照 |
| `to_address` | `varchar(320)` | 否 | 小写完整地址（投递事件、抑制、精确筛选需要） |
| `subject_masked` | `varchar(998)` | 否 | |
| `variables_masked` | `jsonb` | 否 | 秘密值已替换 |
| `payload_ciphertext` | `bytea` | 是 | |
| `idempotency_key` | `varchar(255)` | 是 | 24 小时后可释放为空 |
| `idempotency_fingerprint` | `bytea` | 否 | 32 字节 |
| `status` | `varchar(16)` | 否 | `CHECK (status IN ('queued','sent','failed','suppressed'))` |
| `queued_reason` | `varchar(16)` | 是 | `CHECK (queued_reason IN ('send_limit','retry'))` |
| `attempts` | `smallint` | 否 | 默认 0 |
| `next_attempt_at` | `timestamptz` | 是 | |
| `lease_until` | `timestamptz` | 是 | |
| `send_attempted_at` | `timestamptz` | 是 | 最近一次服务商调用开始时刻；通道计数用 |
| `provider_message_id` | `varchar(255)` | 是 | |
| `rfc_message_id` | `varchar(998)` | 否 | Zmail 生成，`<随机串@发件域名>` 去掉尖括号 |
| `failure_code` | `varchar(120)` | 否 | 默认 `''` |
| `last_event` | `varchar(16)` | 是 | |
| `sent_at` | `timestamptz` | 是 | |
| `created_at` / `updated_at` | `timestamptz` | 否 | |

约束：

- `CHECK (status = 'queued' OR payload_ciphertext IS NULL)`（终态无密文，由数据库兜底）。
- `CHECK (status = 'queued' OR (queued_reason IS NULL AND next_attempt_at IS NULL AND lease_until IS NULL))`：终态不再带排队、重试与租约字段。
- `CHECK (mode = 'live' AND NOT simulated OR send_attempted_at IS NULL)`：测试和模拟消息从不记尝试时刻。

索引：

- `UNIQUE (organization_id, mode, idempotency_key) WHERE idempotency_key IS NOT NULL`。
- `UNIQUE (provider_message_id) WHERE provider_message_id IS NOT NULL`（投递事件匹配）。
- `UNIQUE (rfc_message_id)`（发出的 `Message-ID` 唯一；后续回复路由可直接按它匹配）。
- `(organization_id, id DESC)`（日志列表）；`(organization_id, to_address, id DESC)`（按地址筛选）；`(organization_id, template_key, id DESC)`。
- `(organization_id, send_attempted_at) WHERE send_attempted_at IS NOT NULL`（通道计数）。
- `(next_attempt_at, id) WHERE status = 'queued'`（派发选择，`next_attempt_at` 入库时等于 `created_at`）。
- `(created_at)`（清理）。

`transactional_message_events`：`id bigserial`、`message_id bigint NOT NULL REFERENCES transactional_messages(id) ON DELETE CASCADE`、`type varchar(16)`（与 `mail_message_events` 同一枚举）、`detail varchar(255) DEFAULT ''`、`provider_event_id varchar(128) NULL`（`UNIQUE WHERE NOT NULL`）、`occurred_at`、`created_at`；索引 `(message_id, occurred_at)`。不存 `recipient`（单收件人，即消息的 `to_address`）。

锁与并发：派发租约用 `FOR UPDATE SKIP LOCKED` + `lease_until`；状态变更用条件更新 `WHERE id = ? AND status = 'queued' AND lease_until = ?`（作为 CAS，防止过期租约的旧持有者覆盖）。

保留：worker 删除 `created_at < now() - 30 days` 的行（批量，事件级联）。新表，无回填；回滚删表（30 天内的事务记录随之丢失，`UPGRADING.md` 写明）。

### 7.4 `mail_messages.send_lane`（迁移 `2026_11_23_080000_add_send_lanes`，最先执行）

- `ADD COLUMN send_lane varchar(16) NOT NULL DEFAULT 'personal' CHECK (send_lane IN ('personal','broadcast'))`。入站邮件也取默认值，但计数只看 `direction='outbound'`。
- 回填：`UPDATE mail_messages m SET send_lane = 'broadcast' FROM mail_broadcast_recipients r WHERE r.message_id = m.id`（一次性，在迁移事务内；群发收件人表已有 `message_id`）。
- 索引：新建 `mail_messages_lane_attempted_idx ON mail_messages (send_lane, send_attempted_at) WHERE direction = 'outbound'`，删除 `mail_messages_send_attempted_idx`（被新索引覆盖）。
- 回滚：down 删除新索引和列、恢复旧索引；旧代码按共享额度工作，不丢数据。

### 7.5 `mail_suppressions`（迁移 `2026_11_23_120000_create_mail_suppressions`）

- `id bigserial`、`organization_id bigint NOT NULL REFERENCES organizations(id) ON DELETE CASCADE`、`email varchar(320) NOT NULL`、`reason varchar(16) NOT NULL CHECK (reason IN ('bounced','complained'))`、`source varchar(16) NOT NULL CHECK (source IN ('broadcast','transactional','backfill'))`、`created_at timestamptz NOT NULL`。
- `UNIQUE (organization_id, email)`；`(organization_id, created_at DESC, id DESC)`（列表）。
- 回填：`INSERT … SELECT organization_id, lower(email), status_reason, 'backfill', updated_at FROM mail_contacts WHERE status = 'suppressed' AND status_reason IN ('bounced','complained') ON CONFLICT DO NOTHING`。因发送时 `rejected` 而被抑制的联系人不回填，仍由联系人状态挡住群发（D35）。
- 保留：无自动过期，直到管理员解除或组织删除（发信信誉需要长期记住）。回滚删表。

### 7.6 种子数据

无。模拟域、上限、限频值来自配置，不入库。

## 8. 状态机（§3.7）

### 8.1 事务消息的发送状态

初始状态：`queued` 或 `suppressed`。终态：`sent`、`failed`、`suppressed`。`last_event` 是独立的投递结果生命周期（8.2）。

| 当前状态 | 命令或触发 | 守卫 | 下一状态 | 角色 | 副作用 | 失败 |
|---|---|---|---|---|---|---|
| （无） | SendTransactionalMessage | 校验、幂等、限频通过；收件人不在抑制名单 | `queued`（`queued_reason` 为空，或通道满时 `send_limit`） | 密钥 / 试发者 | 写密文；提交后快速派发 | 见 9.1 错误表 |
| （无） | SendTransactionalMessage | 收件人在抑制名单 | `suppressed` | 密钥 / 试发者 | 不写密文；同事务入队 `email.suppressed` | — |
| `queued` | 租约（快速派发或 worker） | 未被他人持有；`next_attempt_at <= now`；`live` 非模拟时通道有余量 | `queued`（持有租约） | 系统 | `lease_until = now + 2min` | 无余量则跳过 |
| `queued`（持租约） | 派发前复查 | 收件人新进抑制名单 | `suppressed` | 系统 | 清密文；`email.suppressed` | — |
| `queued`（持租约） | 派发前复查 | 发件域名不再验证 | `failed`（`send.sender_unverified`） | 系统 | 清密文；`email.failed` | — |
| `queued`（持租约） | 派发前复查 | 密文无法解密 | `failed`（`send.payload_unavailable`） | 系统 | 清密文；`email.failed` | — |
| `queued`（持租约，`live` 非模拟） | 开始尝试 | — | `queued`，设 `send_attempted_at`、`attempts+1` | 系统 | 事务外调用服务商 | — |
| `queued`（已尝试） | 服务商接受 | — | `sent` | 系统 | 清密文；事件 `accepted`；`email.sent` | — |
| `queued`（已尝试） | 服务商报告收件人被其抑制（`transport.suppressed`） | — | `suppressed` | 系统 | 清密文；不写抑制名单（D35）；`email.suppressed` | — |
| `queued`（已尝试） | 非永久失败且收到 HTTP 状态（`Permanent=false && Responded=true`） | `attempts < 3` | `queued`（`queued_reason=retry`，`next_attempt_at` 按 30 秒、2 分钟、10 分钟） | 系统 | 无 Webhook | — |
| `queued`（已尝试） | 非永久失败且收到 HTTP 状态（`Permanent=false && Responded=true`） | `attempts = 3` | `failed`（服务商失败码） | 系统 | 清密文；`email.failed` | — |
| `queued`（已尝试） | 永久拒绝（`Permanent=true`） | — | `failed`（服务商失败码） | 系统 | 清密文；`email.failed` | — |
| `queued`（已尝试） | 非永久但未收到 HTTP 状态（`Responded=false`：`transport.unreachable`、`transport.invalid_response`、`transport.response_too_large`）或非 `mailtransport.Error` 的错误 | — | `failed`（`transport.unknown_outcome`） | 系统 | 清密文；`email.failed` | 不重发（D33） |
| `queued`（测试或模拟） | 派发 | — | `sent`（`suppressed@` 模拟为 `suppressed`） | 系统 | 清密文；按模拟地址写事件；对应 Webhook | — |
| `queued` | 租约过期且 `send_attempted_at` 晚于上次结果 | 服务商调用后崩溃 | `failed`（`transport.unknown_outcome`） | worker | 清密文；`email.failed` | 不重发 |
| `queued` | 超时 | `created_at < now - 1h` | `failed`（`send.expired`） | worker | 清密文；`email.failed` | — |
| 任意 | 清理 | `created_at < now - 30d` | （删除） | worker | 事件级联删除 | — |

拒绝的迁移：终态不再变化；重复的派发因 CAS 条件不匹配而成为空操作；重复请求按幂等返回原记录（`200`）。没有对外的取消命令（设计决定（待确认）：验证码类邮件取消意义不大，排队上限 1 小时已兜底）。

副作用时机：密文写入、状态变更、Webhook 入队在同一数据库事务内；服务商调用在事务外，之后用独立事务记录结果（沿用 `deliver`）。

### 8.2 投递结果（`last_event`）

`null` →（`accepted`）→ `delivered` | `deferred` | `bounced` | `failed` | `rejected` | `complained`，规则同 `MAILBOXES.md`：单收件人时为最新事件，`complained` 优先于一切。重复的服务商事件按 `provider_event_id` 去重，不重复发布 Webhook；乱序事件按 `occurred_at` 存储，`last_event` 按严重度与时间重新计算（现有 `AddMessageEvents` 逻辑）。

### 8.3 其他

- 事务模板：无生命周期（存在或删除）；`kind` 不可变，改变请求返回 `409 MAILBOX.TEMPLATE_KIND_IMMUTABLE`。
- 抑制项：存在 → 删除；重复删除 `204`。
- API 密钥 `mode`：不可变（现有密钥无更新接口）。

## 9. HTTP 契约（§3.8）

契约先行：先改 `contracts/openapi.yaml`、`contracts/MAILBOXES.md`、`contracts/API_KEYS.md`，用 oasdiff 检查；本切片所有变更均为新增路径、新增可选请求字段、新增响应字段或新增错误码，没有破坏性变更。所有响应用 `contracts/README.md` 的统一信封。

### 9.1 POST /v1/transactional-messages — 发送一封事务邮件

- Owner：mailbox（事务消息）。
- Consumers：开发者后端（机器调用，无浏览器路由）。
- Authentication：`X-API-Key`（现有方案）；`Organization-Id` 头。不采用原型里的 `Authorization: Bearer zk_live_…`（D4，设计决定（待确认））。
- Authorization and resource scope：scope `mail:transactional`（D2，设计决定（待确认）；`*` 依现有规则也通过）；路由用带"允许测试密钥"选项的 `RequireScopes`（D36）；持有人须为组织成员；模板须属本组织。
- Request：

  ```http
  POST /v1/transactional-messages
  X-API-Key: fastbuild_xxxxxxxx.yyyyyyyy
  Organization-Id: 42
  Idempotency-Key: signup-u_8812
  Content-Type: application/json

  {
    "template": "signup-code",
    "to": "lilei@example.com",
    "variables": { "code": "482913", "app": "Acme" }
  }
  ```

- Validation and limits：
  - `Idempotency-Key` 必填（D12，设计决定（待确认）），1–255 个可打印 ASCII；缺失或不合法 `422 MAILBOX.IDEMPOTENCY_KEY_INVALID`。
  - `template`：`^[a-z][a-z0-9-]{0,63}$`。
  - `to`：一个裸地址（D5，设计决定（待确认）），3–320 字符，小写化；显示名形式、多个地址拒绝。
  - `variables`：同 `MailTemplateVariables`（最多 100 个，值最多 2000 字符）；含未声明的变量 `422 MAILBOX.MESSAGE_INVALID`（字段错误 `variables.<name>`，D13）；缺少必填变量 `422 MAILBOX.TEMPLATE_VARIABLES_MISSING`。
  - 请求体上限 64 KiB（路由级，设计决定（待确认））。
- Preconditions：事务功能已启用；模板存在且 `kind=transactional`；发件域名已验证。
- Pseudocode：

  ```text
  COMMAND SendTransactionalMessage(key_actor, organization_header, idempotency_header, request)

  BIND key_actor FROM api_key middleware (key_id, user_id, scopes, mode)
  BIND organization FROM organization_context middleware ELSE ORGANIZATION.CONTEXT_REQUIRED | ORGANIZATION.CONTEXT_INVALID | ORGANIZATION.NOT_FOUND
  REQUIRE key_actor HAS SCOPE mail:transactional ELSE PERMISSION.DENIED   -- RequireScopes; route allows test keys
  REQUIRE settings.transactional_enabled ELSE MAILBOX.TRANSACTIONAL_DISABLED
  VALIDATE request AGAINST TransactionalMessageRequest, body <= 64 KiB ELSE COMMON.VALIDATION_FAILED | COMMON.REQUEST_TOO_LARGE
  REQUIRE idempotency_header IS PRESENT AND printable ASCII 1..255 ELSE MAILBOX.IDEMPOTENCY_KEY_INVALID
  REQUIRE ratelimit.Take("mail.transactional.key", key_actor.key_id) ELSE COMMON.RATE_LIMITED WITH Retry-After

  LOAD template FROM mailbox templates USING (organization.id, request.template)
    OR ELSE MAILBOX.TEMPLATE_NOT_FOUND
  REQUIRE template.kind == transactional ELSE MAILBOX.TEMPLATE_KIND_MISMATCH
  DERIVE to = lowercase(trim(request.to))
  REQUIRE to IS one bare address ELSE MAILBOX.MESSAGE_INVALID
  REQUIRE every key of request.variables IS DECLARED BY template ELSE MAILBOX.MESSAGE_INVALID
  DERIVE simulated = domain_of(to) == settings.simulator_domain
  REQUIRE NOT simulated OR local_part_of(to) IN simulator outcomes ELSE MAILBOX.MESSAGE_INVALID
  DERIVE fingerprint = HMAC(idempotency_scope_key, template.key, to, sorted variables)

  LOAD existing FROM transactional_messages USING (organization.id, key_actor.mode, idempotency_header)
  IF existing IS PRESENT AND existing.created_at >= now - 24h
    REQUIRE existing.idempotency_fingerprint == fingerprint ELSE MAILBOX.IDEMPOTENCY_CONFLICT
    RETURN 200 TransactionalMessageResponse(existing)

  REQUIRE ratelimit.Take("mail.transactional.recipient", organization.id + mode + template.id + to)
    ELSE MAILBOX.RECIPIENT_RATE_LIMITED WITH Retry-After
  LOAD sender_domain FROM mailbox domains USING (organization.id, domain_of(template.from_address))
  REQUIRE sender_domain.status == verified OR (provider == log AND domain == settings.domain)
    ELSE MAILBOX.DOMAIN_NOT_VERIFIED
  DERIVE rendered = render(template, request.variables) ELSE MAILBOX.TEMPLATE_VARIABLES_MISSING
  DERIVE subject_masked = render(template.subject, variables with secret values masked)
  DERIVE variables_masked = request.variables with secret values masked
  DERIVE suppressed = NOT simulated AND mail_suppressions CONTAINS (organization.id, to)
  DERIVE counts_in_lane = key_actor.mode == live AND NOT simulated
  DERIVE rfc_message_id = random + "@" + domain_of(template.from_address)

  BEGIN TRANSACTION mailbox
    IF existing IS PRESENT AND existing.created_at < now - 24h
      UPDATE existing SET idempotency_key = NULL          -- release the expired key
    IF counts_in_lane AND NOT suppressed
      LOAD lane_used = COUNT transactional attempts of organization since now - 24h
      IF lane_used >= settings.transactional_limit
        LOAD waiting = COUNT queued transactional messages of organization WITH queued_reason send_limit
        REQUIRE waiting < settings.transactional_limit ELSE MAILBOX.SEND_LIMIT_REACHED
        DERIVE queued_reason = send_limit
    IF suppressed
      INSERT transactional_message status suppressed, payload_ciphertext NULL, snapshots, masked copies
        ON CONFLICT (organization_id, mode, idempotency_key) DO NOTHING
      ENQUEUE_OUTBOX webhook email.suppressed v1 KEY "t<id>:suppressed"
    ELSE
      INSERT transactional_message status queued, queued_reason, next_attempt_at = now,
        payload_ciphertext = encrypt(rendered), snapshots, masked copies
        ON CONFLICT (organization_id, mode, idempotency_key) DO NOTHING
    IF insert did nothing
      LOAD winner FROM transactional_messages USING (organization.id, mode, idempotency_header)
      REQUIRE winner.idempotency_fingerprint == fingerprint ELSE MAILBOX.IDEMPOTENCY_CONFLICT
      RETURN 200 TransactionalMessageResponse(winner)
  COMMIT

  AFTER COMMIT
    IF status == queued AND queued_reason IS NULL
      background DispatchTransactionalMessage(id)   -- best effort; worker reconciles
  RETURN 202 TransactionalMessageResponse(created)
  ```

- Transaction boundary：通道计数、插入、Webhook 入队在 mailbox 事务内；限频在事务前（限频存储不是业务记录，`CONTEXT.md` 规定其独立）；服务商调用在提交后。
- Idempotency/concurrency：同键并发由唯一索引决出一个；输家重读并比较指纹。通道计数是软上限（与现有一致），worker 的租约按咨询锁精确控制实际发送数。
- Side effects/events：被抑制时 `email.suppressed`；其余事件由派发与投递事件产生。
- Success response：`202`（新建）或 `200`（幂等重放），`data` 为 `TransactionalMessage`：

  ```json
  {
    "id": 7012,
    "mode": "live",
    "simulated": false,
    "template": "signup-code",
    "from": { "address": "no-reply@notify.acme-app.com", "name": "Acme" },
    "to": "lilei@example.com",
    "to_masked": "li••@example.com",
    "subject": "Acme 验证码：••••••",
    "variables": { "code": "••••••", "app": "Acme" },
    "status": "queued",
    "queued_reason": null,
    "failure_code": null,
    "last_event": null,
    "attempts": 0,
    "idempotency_key": "signup-u_8812",
    "created_at": "2026-11-19T14:22:08Z",
    "sent_at": null,
    "events": []
  }
  ```

  `subject`、`variables` 是遮罩副本（快照）；`status` 等为已提交的权威值，提交后立即可读。`to` 在密钥路由中完整返回（调用方自己提供的数据），在会话日志中不返回（9.3）。响应头 `Location: /v1/transactional-messages/7012`。
- Stable errors：

  | Condition | HTTP status | `error_code` | Retryable |
  |---|---:|---|---|
  | 缺少或错误的组织头 | 400 | `ORGANIZATION.CONTEXT_REQUIRED` / `ORGANIZATION.CONTEXT_INVALID` | 修正后 |
  | 密钥无效 | 401 | `AUTH.UNAUTHORIZED` | 否 |
  | 缺 scope；测试密钥用于不支持的路由 | 403 | `PERMISSION.DENIED` | 否 |
  | 非成员或组织不存在 | 404 | `ORGANIZATION.NOT_FOUND` | 否 |
  | 模板 key 不存在或属其他组织 | 404 | `MAILBOX.TEMPLATE_NOT_FOUND` | 否 |
  | 幂等键内容不一致 | 409 | `MAILBOX.IDEMPOTENCY_CONFLICT` | 换新键 |
  | 请求体过大 | 413 | `COMMON.REQUEST_TOO_LARGE` | 修正后 |
  | 字段校验失败 | 422 | `COMMON.VALIDATION_FAILED` | 修正后 |
  | 地址、未声明变量、模拟地址不可用 | 422 | `MAILBOX.MESSAGE_INVALID` | 修正后 |
  | 幂等键缺失或格式错 | 422 | `MAILBOX.IDEMPOTENCY_KEY_INVALID` | 修正后 |
  | 不是事务模板 | 422 | `MAILBOX.TEMPLATE_KIND_MISMATCH` | 否 |
  | 缺必填变量 | 422 | `MAILBOX.TEMPLATE_VARIABLES_MISSING` | 修正后 |
  | 发件域名未验证 | 422 | `MAILBOX.DOMAIN_NOT_VERIFIED` | 域名验证后 |
  | 按密钥超频 | 429 | `COMMON.RATE_LIMITED`（`Retry-After` 秒） | 是 |
  | 按收件人超频 | 429 | `MAILBOX.RECIPIENT_RATE_LIMITED`（`Retry-After` 秒） | 是 |
  | 通道满且等待数已达上限 | 429 | `MAILBOX.SEND_LIMIT_REACHED` | 稍后 |
  | 功能未启用 | 503 | `MAILBOX.TRANSACTIONAL_DISABLED` | 否 |
  | 数据库或依赖不可用 | 503 | `COMMON.SERVICE_UNAVAILABLE` | 是 |

  授权先于资源查找：scope、成员身份失败时不透露模板是否存在。
- Audit/metrics：不写审计；日志 `transactional_message.accepted`（组织、模板 id、mode、消息 id、是否抑制，无地址无变量）；计数器见第 14 节。
- Compatibility notes：新增路径，oasdiff 非破坏；`POST /v1/outbound-mail-messages` 行为不变。
- Tests：handler（scope、测试密钥、头、错误码映射）、service（幂等窗口、指纹、未声明变量、遮罩）、PostgreSQL（唯一索引并发、通道计数、键释放）、Kest。

#### 校验顺序说明

先按密钥限频（最便宜，保护数据库），再查幂等（重放不消耗收件人额度，也不受模板后来变更影响），再按收件人限频，最后读模板之外的依赖。模板与变量校验在幂等查找之前，因为指纹依赖规范化后的变量；这也意味着模板被删除后，同键重放返回 `404 MAILBOX.TEMPLATE_NOT_FOUND`（设计决定（待确认），与现有"模板删除不影响已发消息"不冲突：已发消息仍可按 id 读取）。

### 9.2 GET /v1/transactional-messages/{message_id} — 按 id 读一封

- Owner：mailbox。Consumers：开发者后端。
- Authentication：`X-API-Key` + `Organization-Id`；scope `mail:transactional`；允许测试密钥，但测试密钥只能读 `mode=test` 的消息，正式密钥只能读 `mode=live` 的（不匹配按不存在处理）。
- Pseudocode：

  ```text
  QUERY GetTransactionalMessage(key_actor, organization, message_id)
  BIND key_actor, organization FROM middleware
  REQUIRE key_actor HAS SCOPE mail:transactional ELSE PERMISSION.DENIED
  LOAD message FROM transactional_messages
    WHERE id = message_id AND organization_id = organization.id AND mode = key_actor.mode
    OR ELSE MAILBOX.TRANSACTIONAL_MESSAGE_NOT_FOUND
  LOAD events FROM transactional_message_events WHERE message_id ORDER BY occurred_at, id LIMIT 50
  RETURN 200 TransactionalMessageResponse(message, events)
  ```

- Success response：`200`，同 9.1 的 DTO，`events` 为 `[{type, detail, occurred_at}]`，最多 50 条，按时间升序。
- Stable errors：`401 AUTH.UNAUTHORIZED`、`403 PERMISSION.DENIED`、`404 ORGANIZATION.NOT_FOUND`、`404 MAILBOX.TRANSACTIONAL_MESSAGE_NOT_FOUND`、`503 COMMON.SERVICE_UNAVAILABLE`。
- Tests：跨组织、跨 mode 均 404。

### 9.3 GET /v1/transactional-mail-log — 发送日志（控制台）

- Owner：mailbox。Consumers：Web BFF `/api/transactional-mail-log`。
- Authentication：会话（`bearerSession`）+ `Organization-Id`；任何成员（D24，设计决定（待确认））。
- Query：`mode`（`live` 默认 / `test`）、`status`（`queued|sent|failed|suppressed`）、`last_event`（`delivered|deferred|bounced|failed|rejected|complained`）、`template`（key）、`to`（精确地址，小写比较）、`since`/`until`（默认最近 7 天，最长 30 天）、`cursor`、`per_page`（默认 50，最大 100）。另有 `outcome`（`delivered` = `last_event=delivered`；`problem` = `last_event IN (bounced, complained, failed, rejected)` 或 `status IN (failed, suppressed)`），对应原型的"已投递"与"退信 / 拦截"芯片；`outcome` 与 `status`、`last_event` 同时给出时取交集（设计决定（待确认））。
- Pseudocode：

  ```text
  QUERY ListTransactionalLog(member, organization, filters, cursor)
  BIND member, organization FROM session and organization_context
  VALIDATE filters: enums, to <= 320, range <= 30 days, per_page 1..100 ELSE COMMON.INVALID_INPUT
  LOAD page FROM transactional_messages
    WHERE organization_id = organization.id AND mode = filters.mode
      AND created_at IN [since, until) AND optional filters
      AND id < cursor
    ORDER BY id DESC
    LIMIT per_page + 1
  RETURN 200 TransactionalLogPage(items with to_masked only, meta.next_cursor)
  ```

- Success response：键集分页，`meta.next_cursor` 为最后一项 id；每项：`id`、`mode`、`simulated`、`template`、`to_masked`、`subject`（遮罩）、`status`、`queued_reason`、`failure_code`、`last_event`、`idempotency_key`、`created_at`、`sent_at`。**不含**完整收件人和变量。
- 计数：导航上的"1.2k"取 `GET /v1/transactional-mail-log?…&per_page=1` 的 `meta.total_24h`（近 24 小时该 mode 的条数，由 `(organization_id, id DESC)` 范围计数，上限 10000 后显示"1 万+"）。
- Stable errors：`400 COMMON.INVALID_INPUT`、`400 ORGANIZATION.CONTEXT_*`、`401 AUTH.UNAUTHORIZED`、`404 ORGANIZATION.NOT_FOUND`、`503 COMMON.SERVICE_UNAVAILABLE`。

### 9.4 GET /v1/transactional-mail-log/{message_id} — 日志详情与时间线

- 会话、任何成员；返回 9.3 的字段加 `from`、`variables`（遮罩）、`events`。不返回完整收件人。
- 不存在或其他组织：`404 MAILBOX.TRANSACTIONAL_MESSAGE_NOT_FOUND`。

```text
QUERY GetTransactionalLogEntry(member, organization, message_id)
BIND member, organization FROM session and organization_context
LOAD message FROM transactional_messages WHERE id = message_id AND organization_id = organization.id
  OR ELSE MAILBOX.TRANSACTIONAL_MESSAGE_NOT_FOUND
LOAD events ORDER BY occurred_at, id LIMIT 50
RETURN 200 TransactionalLogEntryResponse(masked fields, events)
```

### 9.5 POST /v1/transactional-test-sends — 控制台试发（D28，设计决定（待确认））

- Owner：mailbox。Consumers：Web BFF `/api/transactional-test-sends`。
- Authentication：会话 + `Organization-Id`；任何成员。
- Request：`{ "template": "signup-code", "mode": "live" | "test", "variables": {…} }`。收件人固定为调用者账号邮箱，请求不能指定。
- 限制：按用户每分钟 5 次（`ratelimit` 规则 `mail.transactional.test_send`），超出 `429 COMMON.RATE_LIMITED`。不需要幂等键（服务端生成 `test-send:<user_id>:<随机>`）。

```text
COMMAND SendTransactionalTest(member, organization, request)
BIND member, organization FROM session and organization_context
REQUIRE settings.transactional_enabled ELSE MAILBOX.TRANSACTIONAL_DISABLED
VALIDATE request ELSE COMMON.VALIDATION_FAILED
REQUIRE ratelimit.Take("mail.transactional.test_send", member.user_id) ELSE COMMON.RATE_LIMITED
LOAD account_email FROM user module USING member.user_id
LOAD template FROM mailbox templates USING (organization.id, request.template) OR ELSE MAILBOX.TEMPLATE_NOT_FOUND
REQUIRE template.kind == transactional ELSE MAILBOX.TEMPLATE_KIND_MISMATCH
DERIVE variables = template.sample_variables overlaid with request.variables
DERIVE idempotency_key = "test-send:" + member.user_id + ":" + random
CONTINUE AS SendTransactionalMessage FROM the recipient rate limit
  WITH to = account_email, mode = request.mode, api_key_id = NULL, created_by = member.user_id
RETURN 202 TransactionalMessageResponse(created)
```

错误：同 9.1 中适用的部分，另 `403` 不适用（任何成员可用）。

### 9.6 模板接口变更（现有路由）

`MailTemplateRequest` 增加可选字段：`kind`（默认 `standard`）、`key`、`from_address`、`from_name`、`variable_definitions`；`MailTemplate` 与 `MailTemplateSummary` 响应增加 `kind`、`key`（可空）、`from`（可空对象）、`variable_definitions`。本次没有回复路由字段（D32）。列表增加查询参数 `kind`。新增字段只出现在响应中或为可选请求字段，oasdiff 非破坏。

```text
COMMAND SaveTransactionalTemplate(member, organization, template_id?, request)
BIND member, organization FROM session and organization_context
VALIDATE request AGAINST MailTemplateRequest ELSE COMMON.VALIDATION_FAILED
IF request.kind == transactional OR (template_id IS PRESENT AND stored.kind == transactional)
  REQUIRE member.role IN {owner, admin} ELSE PERMISSION.DENIED
IF template_id IS PRESENT
  LOAD stored FROM mail_templates USING (organization.id, template_id) OR ELSE MAILBOX.TEMPLATE_NOT_FOUND
  REQUIRE request.kind == stored.kind ELSE MAILBOX.TEMPLATE_KIND_IMMUTABLE
REQUIRE placeholders(subject, html, text) == declared variable names ELSE MAILBOX.TEMPLATE_INVALID
REQUIRE no secret variable has a fallback; required flags match fallbacks ELSE MAILBOX.TEMPLATE_INVALID
LOAD domain FROM mail_domains USING (organization.id, domain_of(request.from_address))
REQUIRE domain.status == verified (or log-provider instance domain) ELSE MAILBOX.DOMAIN_NOT_VERIFIED
BEGIN TRANSACTION mailbox
  INSERT or replace mail_templates row
    ON unique name violation -> MAILBOX.TEMPLATE_NAME_TAKEN
    ON unique key violation -> MAILBOX.TEMPLATE_KEY_TAKEN
    ON organization at 200 templates -> MAILBOX.TEMPLATE_LIMIT_REACHED
COMMIT
RETURN 201 (create) | 200 (replace) MailTemplateResponse
```

| Condition | HTTP status | `error_code` | Retryable |
|---|---:|---|---|
| 成员改事务模板 | 403 | `PERMISSION.DENIED` | 否 |
| 模板不存在 | 404 | `MAILBOX.TEMPLATE_NOT_FOUND` | 否 |
| 名称被占用 | 409 | `MAILBOX.TEMPLATE_NAME_TAKEN` | 否 |
| key 被占用 | 409 | `MAILBOX.TEMPLATE_KEY_TAKEN` | 否 |
| 改 `kind` | 409 | `MAILBOX.TEMPLATE_KIND_IMMUTABLE` | 否 |
| 已有 200 个模板 | 409 | `MAILBOX.TEMPLATE_LIMIT_REACHED` | 否 |
| 占位符与声明不符、秘密变量带 fallback 等 | 422 | `MAILBOX.TEMPLATE_INVALID` | 修正后 |
| 发件域名未验证 | 422 | `MAILBOX.DOMAIN_NOT_VERIFIED` | 验证后 |

删除模板：

```text
COMMAND DeleteMailTemplate(member, organization, template_id)
BIND member, organization FROM session and organization_context
LOAD template FROM mail_templates USING (organization.id, template_id) OR ELSE MAILBOX.TEMPLATE_NOT_FOUND
IF template.kind == transactional
  REQUIRE member.role IN {owner, admin} ELSE PERMISSION.DENIED
BEGIN TRANSACTION mailbox
  DELETE template   -- transactional_messages.template_id becomes NULL; key snapshot stays
COMMIT
RETURN 204
```

邮箱发送、模板试发（`/v1/mail-templates/{id}/tests`）与群发在加载模板后增加 `REQUIRE template.kind == standard ELSE MAILBOX.TEMPLATE_KIND_MISMATCH`。群发在创建和启动时检查（`422`）；worker 发送前不再检查，因为启动时模板已复制进群发。

### 9.7 POST /v1/api-keys 变更 — 建密钥时选择模式

`CreateAPIKeyRequest.mode`（可选，`live`/`test`，默认 `live`）；`APIKey.mode` 响应必有。

```text
COMMAND CreateAPIKey(user, request)
BIND user FROM session
VALIDATE request (name, scopes, expires_at, mode IN {live, test}) ELSE COMMON.VALIDATION_FAILED | COMMON.INVALID_INPUT
DERIVE prefix = mode == test ? "fastbuild_test_" + short_id : "fastbuild_" + short_id
DERIVE plaintext = prefix + "." + secret; hash = sha256(plaintext)
BEGIN TRANSACTION apikey
  INSERT api_keys (user_id, name, prefix, hash, scopes, mode, expires_at)
  INSERT audit event api_key.created WITH mode, scopes
COMMIT
RETURN 201 CreateAPIKeyResponse(api_key, plaintext_key once)
```

中间件：校验通过后上下文增加 `apiKeyMode`；`RequireScopes` 在检查 scope 的同一处检查 mode，路由未用"允许测试密钥"选项时遇测试密钥返回 `403 PERMISSION.DENIED`（D36）。拒绝必须在 `RequireScopes` 内部完成：gin 中间件按挂载顺序执行，若另挂一个 `AllowTestKeys()`，它无法撤销前面已 `Abort` 的拒绝，放在前面又会让"默认拒绝"依赖每条路由记得挂它。今天不存在测试密钥，所以对现有调用方无行为变化。

```text
MIDDLEWARE RequireScopes(options, required_scopes...)
BIND scopes, mode FROM api_key middleware context ELSE AUTH.UNAUTHORIZED
REQUIRE mode == live OR options.allow_test_keys ELSE PERMISSION.DENIED
REQUIRE scopes COVER required_scopes ELSE PERMISSION.DENIED
CONTINUE
```

### 9.8 GET /v1/mail-send-lanes — 通道用量（D25，设计决定（待确认））

- 会话 + `Organization-Id`；仅 `owner`/`admin`，成员 `403 PERMISSION.DENIED`（延续"用量数字只给管理员"的现有规则）。

```text
QUERY ListSendLanes(admin, organization)
BIND admin, organization FROM session and organization_context
REQUIRE admin.role IN {owner, admin} ELSE PERMISSION.DENIED
FOR lane IN [transactional, broadcast, personal]
  DERIVE used = COUNT attempts in lane for organization WHERE send_attempted_at >= now - 24h
  DERIVE waiting = COUNT messages in lane WAITING for the limit (personal, transactional) or
                   recipients of broadcasts with stop_reason send_limit_reached (broadcast)
  DERIVE limit = settings limit of lane
RETURN 200 { lanes: [{lane, limit, used, waiting}], window_hours: 24, computed_at }
```

实时计算，不缓存；结果固定 3 项。

### 9.9 GET /v1/mailbox-domains 变更

每项增加：`daily_broadcast_limit`、`daily_transactional_limit`、`transactional_enabled`、`transactional_recipient_limit_per_minute`、`transactional_key_limit_per_second`、`simulator_domain`。`daily_send_limit` 含义改为"个人通道上限"，数值不变。成员可读（今天已可读 `daily_send_limit`）。

### 9.10 GET /v1/mail-suppressions 与 DELETE /v1/mail-suppressions/{suppression_id}（D18，设计决定（待确认））

- 会话 + `Organization-Id`；仅 `owner`/`admin`。
- 列表：`email`（精确）、`reason`、`cursor`、`per_page`（最大 100），按 `created_at DESC, id DESC`；每项 `{id, email, reason, source, created_at}`。

```text
COMMAND RemoveMailSuppression(admin, organization, suppression_id)
BIND admin, organization FROM session and organization_context
REQUIRE admin.role IN {owner, admin} ELSE PERMISSION.DENIED
BEGIN TRANSACTION mailbox
  DELETE mail_suppressions WHERE id = suppression_id AND organization_id = organization.id
  IF a row was deleted
    INSERT audit event mail_suppression.removed (actor, organization, suppression id, reason)
COMMIT
RETURN 204   -- also when the row did not exist (idempotent), so nothing is disclosed
```

错误：`403 PERMISSION.DENIED`、`404 ORGANIZATION.NOT_FOUND`、`503 COMMON.SERVICE_UNAVAILABLE`。不存在的行返回 `204`，不使用 `MAILBOX.SUPPRESSION_NOT_FOUND`。

### 9.11 POST /v1/mail-delivery-events 变更 — 事件分流

请求与签名不变。按服务商 message id 先查 `mail_messages`，未命中再查 `transactional_messages`；响应增加 `message_kind`（`mailbox`/`transactional`），`message_id` 是对应表的 id。

```text
COMMAND RecordDeliveryEvent(edge_request)
VERIFY signature ELSE MAILBOX.INBOUND_SIGNATURE_INVALID
PARSE event ELSE MAILBOX.DELIVERY_EVENT_INVALID
LOAD target FROM mail_messages USING provider message id
  OR ELSE LOAD FROM transactional_messages USING provider message id
  OR ELSE MAILBOX.MESSAGE_NOT_FOUND
IF target IS transactional
  BEGIN TRANSACTION mailbox
    INSERT transactional_message_events ON CONFLICT (provider_event_id) DO NOTHING
    IF inserted
      UPDATE last_event BY severity rules
      IF event.type IN {bounced, complained}          -- rejected never suppresses (matches existing rule)
        INSERT mail_suppressions (organization, to_address, event.type, transactional) ON CONFLICT DO NOTHING
      ENQUEUE_OUTBOX webhook email.<mapped> v1 KEY "t<id>:event:" + hash(provider_event_id)
  COMMIT
  RETURN 201 {message_id, message_kind: transactional, type, duplicate: false} | 200 duplicate
ELSE
  existing mailbox branch (contact suppression unchanged);
  additionally, for a broadcast message with bounced/complained:
    INSERT mail_suppressions (organization, recipient, event.type, broadcast) ON CONFLICT DO NOTHING
```

错误码不变。

### 9.12 POST /v1/inbound-mail-messages — 本次不变

回复打标签随回复路由推迟（D32，第 18 节"后续"）。收信的请求、响应、错误与存储事务均不改。

```text
COMMAND ReceiveInbound(edge_request)
EXISTING verification, parsing, mailbox lookup, insert and thread resolution   -- unchanged in this rollout
RETURN 201 | 200 (existing)
```

### 9.13 DELETE /v1/mail-domains/{domain_id} 变更

```text
COMMAND RemoveMailDomain(admin, organization, domain_id)
EXISTING authorization and lookup
REQUIRE no mailbox uses the domain ELSE MAILBOX.DOMAIN_IN_USE        -- existing
REQUIRE no transactional template sends from the domain ELSE MAILBOX.DOMAIN_IN_USE   -- new
EXISTING removal transaction and provider cleanup
RETURN 204
```

### 9.14 额度相关的现有发送命令变更

```text
COMMAND SendMessage(viewer, mailbox_id, input)        -- console, API outbound, template test
EXISTING access, validation, idempotency
LOAD used = COUNT outbound with send_lane = personal of organization since now - 24h
IF used >= settings.personal_limit OR personal waiting > 0
  EXISTING queue-or-429 behaviour against settings.personal_limit
BEGIN TRANSACTION mailbox
  INSERT mail_messages send_lane = personal (existing fields)
COMMIT
AFTER COMMIT existing provider call and record step
RETURN 201 | 202 | 429 | 502 | 503
```

```text
COMMAND SendBroadcastRecipient(worker, broadcast, recipient)
EXISTING contact status check (unsubscribed / suppressed contacts are skipped)
IF mail_suppressions CONTAINS (organization.id, lowercase(recipient.email))     -- new (D35)
  FINISH recipient skipped (failure_code contact_suppressed); RETURN   -- contact status is not changed
LOAD used = COUNT outbound with send_lane = broadcast of organization since now - 24h
IF used >= settings.broadcast_limit
  UPDATE broadcast stop_reason = send_limit_reached; RETURN (existing pause)
BEGIN TRANSACTION mailbox
  INSERT mail_messages send_lane = broadcast (existing fields)
COMMIT
AFTER COMMIT existing provider call and record step
RETURN recipient outcome
```

### 9.15 新增错误码

| Code | Status |
|---|---|
| `MAILBOX.TRANSACTIONAL_MESSAGE_NOT_FOUND` | 404 |
| `MAILBOX.TEMPLATE_KEY_TAKEN` | 409 |
| `MAILBOX.TEMPLATE_KIND_IMMUTABLE` | 409 |
| `MAILBOX.TEMPLATE_KIND_MISMATCH` | 422 |
| `MAILBOX.RECIPIENT_RATE_LIMITED` | 429 |
| `MAILBOX.TRANSACTIONAL_DISABLED` | 503 |

### 9.16 Webhook 事件（D22，设计决定（待确认））

加入 mailbox 的 webhook 目录（`NewWebhookCatalog`），`GET /v1/webhook-event-types` 随之列出。签名、重试、投递记录全部沿用 `WEBHOOKS.md`（重试按 1 分钟、5 分钟、15 分钟、1 小时……最多 10 次，带 ±20% 抖动；原型里"1、5、30 分钟"的文案按实际计划改）。

| 事件类型 | 发布时机 |
|---|---|
| `email.sent` | 服务商接受（`status → sent`），或测试/模拟消息完成渲染 |
| `email.delivered` | 投递事件 `delivered` |
| `email.bounced` | 投递事件 `bounced`（`bounce_type: "permanent"`）或 `deferred`（`bounce_type: "temporary"`） |
| `email.complained` | 投递事件 `complained` |
| `email.suppressed` | 入库或派发时命中抑制名单；服务商报告收件人在其抑制名单（发送时或事件 `rejected`） |
| `email.failed` | `status → failed`（服务商永久拒绝、结果未知、重试用尽、排队超时、发件域名失效），或投递事件 `failed`（服务商放弃且无退信） |

`data`（每个字段始终存在）：

| 字段 | 值 |
|---|---|
| `message_id` | 事务消息 id |
| `template` | 模板 key |
| `to` | 收件人**完整**地址 |
| `mode` | `live` / `test` |
| `simulated` | 布尔 |
| `bounce_type` | `permanent` / `temporary` / `""` |
| `reason` | 简短机器码（如 `5.1.1 permanent_failure`、`suppressed`、失败码），无则 `""` |

不含主题、正文、任何变量。收件人给完整地址的理由：接收方是组织自己配置并验证签名的后端，地址本就是开发者提供的用户数据，只有完整地址才能把退信对回用户、标记"邮箱无效"；现有 `mail.message.*` 也给完整地址；遮罩的目标是人（控制台），不是组织自己的系统。事件 id：`t<消息 id>:<类型后缀>[:<键哈希>]`，与 `mailWebhookEventID` 同样的稳定规则，重复发布被 webhook 模块按 `(organization_id, source, event_id)` 去重（唯一索引 `idx_webhook_events_identity`）。

事务邮件的 Webhook 不受邮箱 Webhook 开关约束（它不属于任何邮箱），只要组织有订阅了这些类型的已启用端点即投递。测试与模拟消息照常发布，接收方用 `mode`、`simulated` 区分。

## 10. 授权矩阵（§3.9）

| 能力 | 事务密钥 live | 事务密钥 test | member | owner/admin | worker | 平台运维 |
|---|---|---|---|---|---|---|
| 发送事务消息 | 本组织范围 | 本组织范围（仅 test） | 禁止（只能试发给自己） | 禁止（只能试发给自己） | — | 禁止 |
| 按 id 读事务消息（收件人完整） | 本组织 live | 本组织 test | 禁止 | 禁止 | 全部 | 禁止 |
| 读发送日志（收件人遮罩） | 禁止 | 禁止 | 本组织 | 本组织 | — | 禁止 |
| 试发给自己 | 禁止 | 禁止 | 仅本人邮箱 | 仅本人邮箱 | — | 禁止 |
| 读事务模板 | 禁止 | 禁止 | 本组织 | 本组织 | — | 禁止 |
| 增删改事务模板 | 禁止 | 禁止 | 禁止 | 本组织 | — | 禁止 |
| 通道用量 | 禁止 | 禁止 | 禁止（只见上限） | 本组织 | — | 禁止 |
| 抑制名单读与解除 | 禁止 | 禁止 | 禁止 | 本组织 | 写入（事件） | 禁止 |
| 建密钥 | — | — | 仅自己的密钥 | 仅自己的密钥 | — | — |
| 邮箱、群发、`outbound-mail-messages` | 禁止（无 scope） | 禁止 | 按现有规则 | 按现有规则 | — | — |
| 读任何秘密明文 | 禁止 | 禁止 | 禁止 | 禁止 | 仅派发时解密 | 禁止 |

行级约束与不透露：

- 一切查找带 `organization_id`；其他组织的资源与不存在同样返回 404。
- 密钥 mode 与消息 mode 不一致按不存在处理。
- 试发收件人只能是调用者账号邮箱，防止控制台成为向任意地址发信的通道。
- 解除抑制对不存在的行也返回 `204`。
- Web 的路由守卫和按钮隐藏只是体验；API 是权威。

## 11. 跨模块数据流（§3.10）

| 流程 | 事务所有者 | 同步参与者 | 事件生产者 → 消费者 | 权威来源 | 幂等键 | 投递保证与重试 | 顺序 | 重复与乱序 | 失败可见性 | 对账与重放 |
|---|---|---|---|---|---|---|---|---|---|---|
| 发送 | mailbox | 限频存储、加密 | mailbox → webhook | `transactional_messages` | `(org, mode, Idempotency-Key)` | 本地事务；派发失败由 worker 兜底 | 无跨消息顺序要求；单条按状态机 | 唯一索引 + 指纹 | 日志、指标、`status`/`failure_code` | worker 扫描 `queued` |
| 派发 | mailbox | 服务商 | mailbox → webhook | `transactional_messages` | 消息 id + 租约 | 服务商调用至多一次每次尝试；只对收到 HTTP 状态的非永久失败重试（D33） | 每组织先到先发（id 升序） | CAS 条件更新 | `failure_code`、告警 | 租约过期处理（不重发） |
| 投递事件 | mailbox | 无 | 邮件边缘 → mailbox → webhook | 事件表 | `provider_event_id` | 边缘按响应重试 | 可乱序，按 `occurred_at` | 唯一索引去重 | 接收路由日志 | 边缘重发 |
| 抑制写入 | mailbox | 无 | 投递事件 → 抑制名单 | `mail_suppressions` | `(org, email)` | 与事件同事务 | — | `ON CONFLICT DO NOTHING` | 名单页 | 回填迁移 |
| Webhook | webhook | 无 | mailbox → 接收方 | `webhook_events`/投递 | `(organization_id, source=mailbox, event_id)` | 现有重试与重放 | 不保证 | `webhook-id` 去重 | 投递历史 | 运维重放 |

Webhook 入队失败的处理沿用现有：状态照常记录，失败写日志，该条 Webhook 丢失（`MAILBOXES.md` Webhooks 一节的既有约定）。

### 派生记录表

| Trigger | Source record | Generated record/action | Owner | Idempotency key | Failure recovery |
|---|---|---|---|---|---|
| 发送请求 | 请求 | 事务消息 | mailbox | `(org, mode, Idempotency-Key)` | 重放返回原结果 |
| 入库命中抑制 | 事务消息 | `email.suppressed` | mailbox | `t<id>:suppressed` | 同事务 |
| 服务商接受 | 派发结果 | 事件 `accepted`、`email.sent`、清空密文 | mailbox | `t<id>:sent` | 记录用独立超时上下文 |
| 终止失败 | 派发结果 | `email.failed`、清空密文 | mailbox | `t<id>:failed` | 记录用独立超时上下文 |
| 投递事件 | 服务商事件 | 事件行、`last_event`、`email.*` | mailbox | `provider_event_id` | 边缘重试 |
| 永久退信/投诉（`bounced`、`complained`） | 投递事件 | 抑制项 | mailbox | `(org, email)` | 同事务 |
| 模拟派发 | 事务消息 | 模拟事件与 Webhook | mailbox | `t<id>:sim:<序号>` | 同事务 |
| 30 天到期 | 事务消息 | 删除 | mailbox worker | 消息 id | 下一轮继续 |

## 12. 计划与异步任务（§3.11）

全部挂在现有 `fastbuild mail:broadcast:work`（批量、轮询、出错退避均沿用 `runWebhookWorker`），每轮顺序改为：

1. `DispatchTransactional(limit)`（新，优先）。
2. `DispatchQueuedSends`（个人通道等待队列，现有）。
3. `DispatchBroadcasts`（现有）。
4. `PurgeExpiredTrash`（现有）。
5. `PurgeExpiredTransactional`（新）。

| 任务 | 触发 | 时区 | 选择与批量 | 并发控制 | 幂等与重试 | 最大尝试与退避 | 失败可见性 | 人工处理 | 保留 |
|---|---|---|---|---|---|---|---|---|---|
| 快速派发 | 发送提交后 `s.background` | UTC | 单条 id | 条件租约（`lease_until IS NULL OR <= now`） | CAS | — | 日志 | worker 兜底 | — |
| 事务派发 | 每轮 | UTC | 每组织按 `next_attempt_at, id` 取 `min(余量, 批量)` | `pg_advisory_xact_lock(hashtext('zmail.transactional_send'), org)` + `SKIP LOCKED` + 2 分钟租约 | CAS；只在 `Permanent=false && Responded=true` 时重试 | 3 次；30 秒、2 分钟、10 分钟 | `failure_code`、指标、告警 | 无重放命令（换新幂等键重发） | — |
| 超时失败 | 每轮（派发前） | UTC | `status='queued' AND created_at < now-1h`，每轮 ≤ 批量 | 条件更新 | 幂等 | — | `send.expired` | — | — |
| 结果未知处理 | 每轮 | UTC | `queued`、租约过期、`send_attempted_at` 晚于最近结果 | 条件更新 | 幂等 | 不重发 | `transport.unknown_outcome` | 开发者换新键重发 | — |
| 清理 | 每轮最后，用余下批量 | UTC | `created_at < now-30d` 按 id，批量 ≤ 剩余额度 | 普通删除 | 幂等 | 下轮继续 | 日志 `transactional_message.purged` | — | 30 天 |

排队超时 1 小时（D15）是设计决定（待确认）：验证码有效期通常 10–30 分钟，1 小时后再送达有害无益；"通道满时排队"已按默认定稿（第 19 节默认 2、D31）。

快速派发与 worker 共用同一个 `DispatchTransactionalMessage`：

```text
COMMAND DispatchTransactionalMessage(system, message_id)
BEGIN TRANSACTION mailbox
  LOCK organization lane via advisory lock
  LOAD_FOR_UPDATE message WHERE id AND status = queued AND (lease_until IS NULL OR lease_until <= now)
    AND next_attempt_at <= now SKIP LOCKED     -- concurrency: one dispatcher per message
  IF none RETURN
  IF message counts in lane AND lane used >= limit
    UPDATE message SET queued_reason = send_limit; RETURN   -- waits for room
  UPDATE message SET lease_until = now + 2min
COMMIT
IF recipient now suppressed -> TX: status suppressed, wipe payload, ENQUEUE email.suppressed; RETURN
IF sender domain not verified -> TX: status failed send.sender_unverified, wipe, ENQUEUE email.failed; RETURN
DERIVE payload = decrypt(message.payload_ciphertext) ELSE TX: failed send.payload_unavailable, wipe; RETURN
IF message.mode == test OR message.simulated
  BEGIN TRANSACTION mailbox
    CAS_UPDATE message EXPECT lease SET status per simulator outcome, payload NULL, lease NULL
    INSERT simulated events; ENQUEUE_OUTBOX matching email.* KEY "t<id>:sim:<n>"
  COMMIT
  RETURN
BEGIN TRANSACTION mailbox
  CAS_UPDATE message EXPECT lease SET send_attempted_at = now, attempts = attempts + 1, queued_reason = NULL
COMMIT
AFTER COMMIT
  CALL provider (from, to, payload, Message-ID = rfc_message_id)     -- no custom Reply-To (D32)
  BEGIN TRANSACTION mailbox      -- context.WithoutCancel + record timeout
    IF accepted: CAS_UPDATE SET status sent, provider_message_id, sent_at, payload NULL, lease NULL
                 INSERT event accepted (+ any per-recipient outcome); ENQUEUE email.sent KEY "t<id>:sent"
    ELSE IF provider suppressed: SET status suppressed, payload NULL; ENQUEUE email.suppressed   -- no mail_suppressions row (D35)
    ELSE IF err IS mailtransport.Error AND NOT err.Permanent AND err.Responded AND attempts < 3:
                 SET queued_reason retry, next_attempt_at = now + backoff(attempts), lease NULL, failure_code
    ELSE IF err IS mailtransport.Error AND (err.Permanent OR err.Responded):
                 SET status failed, failure_code = err.Code, payload NULL, lease NULL; ENQUEUE email.failed KEY "t<id>:failed"
    ELSE:        -- no HTTP status received: the provider may have accepted it
                 SET status failed, failure_code = transport.unknown_outcome, payload NULL, lease NULL
                 ENQUEUE email.failed KEY "t<id>:failed"        -- never resend (D33)
  COMMIT
RETURN
```

```text
COMMAND PurgeExpiredTransactional(system, now, limit)
BEGIN TRANSACTION mailbox
  DELETE FROM transactional_messages
    WHERE id IN (SELECT id WHERE created_at < now - 30 days ORDER BY id LIMIT limit)
  -- events cascade
COMMIT
RETURN number deleted
```

## 13. 浏览器界面（§3.12）

### 13.1 路由与归属

- 路由：`/console/developers`（`web/src/app/(protected)/(console)/console/developers/page.tsx`），归属新 feature `web/src/features/developer/`；页签与模式用 URL 状态 `?tab=start|templates|logs|limits|webhooks&mode=live|test`（D27，设计决定（待确认）），刷新与分享保持。组织选择沿用现有邮件页面（如 `/console/mail-templates`）的活动组织机制。
- 导航：v2 外壳的底部分区"邮件 · 群发 · 开发者 · 管理"中的"开发者"；外壳与主题由 v2 主题切片负责，本切片只提供页面与 i18n。
- 只在 `NEXT_PUBLIC_OPTIONAL_FEATURES` 含 `organization` 与 `mailbox` 且 `GET /api/mailbox-domains` 报 `transactional_enabled` 时显示；关闭时显示"未启用"空状态。

### 13.2 BFF（固定白名单，`web/src/app/api/…`）

新增：`transactional-mail-log`（GET）、`transactional-mail-log/[messageId]`（GET）、`transactional-test-sends`（POST）、`mail-send-lanes`（GET）、`mail-suppressions`（GET）、`mail-suppressions/[suppressionId]`（DELETE）。复用：`api-keys`、`mail-templates`、`mail-template-previews`、`mail-domains`、`mailbox-domains`、`mailboxes`、`webhook-endpoints`、`webhook-event-types`、`webhook-deliveries`。密钥路由 `/v1/transactional-messages` 不进入 BFF（浏览器永远不持有 API 密钥去发信）。mock BFF 为每条新路由提供同信封、同错误码的实现。

### 13.3 页签

**快速上手**

- 三步状态：① 已验证发信域名（`GET /api/mail-domains` 有 `verified`）；② 已有本人的事务密钥（`GET /api/api-keys` 中 scopes 恰为 `["mail:transactional"]`，按当前 mode 匹配）；③ 当前 mode 下组织已有一封事务消息（日志 `per_page=1`）。
- 建密钥：调用 `POST /api/api-keys`，`scopes: ["mail:transactional"]`、`mode` 取页面模式；密钥属于创建者个人，页面在持有人旁提示"持有人离开组织后这把钥匙立即失效，生产环境的验证码会停发"（D31）；明文只在创建响应中显示一次，"显示并复制"后不再可取回（关闭后显示前缀 + 尾 4 位，来自 `key_prefix`）。文案："这把钥匙只能发事务邮件，不能群发、不能读收件箱。"
- 代码片段（curl / Node.js / Python，原生 HTTP，D27）：Node 用全局 `fetch`（Node 18+），Python 用标准库 `urllib.request`，均不依赖 SDK；片段里带 `X-API-Key`、`Organization-Id`（当前组织 id）、`Idempotency-Key`、模板 key 和示例变量。API 地址取新的公开配置 `NEXT_PUBLIC_DEVELOPER_API_ORIGIN`（设计决定（待确认）；未配置时显示 `https://api.example.com` 并提示运维配置）。
- "发一封试试"：`POST /api/transactional-test-sends`；成功显示"已发往 <本人邮箱>，状态 排队中/已发送"，并轮询日志详情至多 30 秒显示投递事件；测试模式显示"测试模式：渲染成功，未真实发出"。

**事务模板**

- 左列：`GET /api/mail-templates?kind=transactional`，每项显示名称、key、"含秘密"标记、近 24 小时发送数（来自日志按模板 key 的 `meta.total_24h`，只在选中时请求，避免 N 次调用）。
- 右侧：主题、正文（纯文本视图）、变量芯片（秘密为警示色"· 秘密"）、说明框、预览（`POST /api/mail-template-previews`，在沙箱 iframe 中渲染 HTML，沿用现有预览组件）。
- 编辑（仅 owner/admin 可见入口）：在现有模板编辑器上增加"事务"字段区：key、发件地址（下拉：已验证域名 + 自填本地部分）、显示名、变量声明表（名称、必填、秘密）。秘密变量的 fallback 输入框禁用。本次没有回复邮箱与回复标签（D32）。
- 冲突：`409 MAILBOX.TEMPLATE_KEY_TAKEN` 标在 key 字段；`MAILBOX.TEMPLATE_INVALID` 显示具体字段错误。

**发送日志**

- 筛选芯片：全部 / 已投递 / 退信·拦截 / 测试（测试 = 切换 `mode=test`）；另有按收件人精确地址搜索、按模板筛选；URL 保存筛选。
- 表格：时间、模板、收件人（遮罩）、内容（遮罩主题）、状态芯片（排队中、已发送、已投递、退信、已拦截、失败、测试）、幂等键；"更早"按钮加载下一页（键集分页）。
- 选中行：详情面板显示时间线（事件）与说明文案；秘密行说明"验证码已遮住，只能看到是否发出、何时投递"。
- 顶部固定提示"只保留 30 天 · 不进任何邮箱的'已发送'"。
- 刷新：页面可见时每 10 秒刷新第一页（沿用邮箱工作区的定时刷新做法）。

**额度与保护**

- 三条通道：owner/admin 显示 `用量 / 上限` 与进度条（`GET /api/mail-send-lanes`）；成员只显示上限与说明"用量仅管理员可见"（来自 `mailbox-domains`）。
- 保护规则（只读，值来自 `mailbox-domains`）：同一收件人限频、单个密钥限速、幂等键 24 小时、退信与投诉名单、秘密变量。
- 退信与投诉名单（owner/admin）：按地址精确查找、列表、"解除"二次确认（文案说明解除后可能再次退信影响信誉）。

**Webhook**

- 复用 `features/webhook` 的端点列表、创建（签名密钥只显示一次）、启停、测试、投递历史组件，事件选择区默认只展示 `email.*` 六类并附中文说明；右侧示例载荷按 9.16 的真实字段生成（不再是原型里的扁平结构）。权限沿用 webhook 模块（端点管理按其现有规则）。

### 13.4 状态与错误映射

| 状态 | 表现 |
|---|---|
| 加载 | 骨架屏 |
| 空 | 快速上手引导；日志"还没有事务邮件" |
| 校验失败 | 字段级错误（`COMMON.VALIDATION_FAILED`、`MAILBOX.TEMPLATE_INVALID`） |
| 冲突 | `409` 就地提示并保留输入 |
| 禁止 | `403` 隐藏编辑入口；直接访问显示"需要管理员" |
| 不可用 | `503 MAILBOX.TRANSACTIONAL_DISABLED` 显示"实例未启用事务邮件"；`COMMON.SERVICE_UNAVAILABLE` 显示重试 |
| 限频 | 试发 `429` 显示"请稍后再试（N 秒）" |

界面只按 `error_code` 分支，不读后端 message。

### 13.5 其他

- 响应式：≥1024px 两栏；更窄时列表与详情改为上下堆叠，代码块横向滚动不撑破页面。
- 可访问性：页签用 `role="tablist"`；状态芯片带文字不只靠颜色；复制按钮有 `aria-live` 反馈。
- i18n：`web/src/i18n/modules/developer/{en-US,zh-Hans}.ts`（新模块），错误文案进 `errors` 模块。
- 缓存失效：建密钥后失效 `api-keys` 查询；模板保存后失效模板列表与预览；解除抑制后失效名单；试发后失效日志第一页。
- mock 与生产一致：mock BFF 生成遮罩数据和模拟事件，契约测试保证两者同 schema。

## 14. 安全、隐私与审计（§3.13）

### 14.1 敏感数据

| 数据 | 存放 | 谁能读 | 处理 |
|---|---|---|---|
| 秘密变量值、含秘密的渲染内容 | `payload_ciphertext`（AES-256-GCM） | 仅派发代码在内存中解密 | 终态立即清空；数据库约束兜底；最长存活 = 排队上限 1 小时 + 重试 |
| 遮罩主题、遮罩变量 | 明文列 | 本组织成员、该组织的事务密钥 | 30 天 |
| 收件人地址 | `to_address` | 密钥路由与 Webhook 完整；控制台遮罩 | 30 天 |
| 抑制地址 | `mail_suppressions` | owner/admin | 直到解除 |
| API 密钥明文 | 不存（仅哈希） | 创建时一次 | 现有 |
| 加密密钥 `MAILBOX_SECRET_ENCRYPTION_KEY` | 环境变量 | 进程 | ≥32 字符，启用事务功能时必填，否则启动失败 |

- 日志：任何 `slog` 字段不得含变量、主题、地址、密文；只记 id、组织、模板 id、mode、状态、失败码。新增一个单元测试扫描事务代码的日志调用参数名白名单。
- 错误：模板渲染错误只报变量名，不回显值。
- 幂等指纹用 HMAC 而不是裸哈希（6.1）。
- 数据库备份中只有密文；备份与加密密钥分开保管（`DEPLOYMENT.md` 写明）。
- AI：本切片无 AI 读取路径；未来任何 AI 读取只能经由日志接口，天然只得到遮罩值（原型说明"AI 只看到 ••••••"由此保证）。
- 审计：`api_key.created`（含 mode）、模板写请求（现有写审计）、`mail_suppression.removed`。审计不记录请求体（`AUDIT.md`）。

### 14.2 不带退订链接但仍检查抑制的理由

- 法律：CAN-SPAM 对"主要目的为交易或关系"的邮件豁免退订要求；我国《互联网电子邮件服务管理办法》对未经同意发送的"商业性电子邮件广告"设限，注册验证码、密码重置、账单通知不是广告；Gmail 与 Yahoo 的批量发件人一键退订要求针对营销邮件。让用户"退订"验证码会使其无法登录，产品上也不合理。
- 抑制仍适用：永久退信说明地址不存在，继续发只会损害共享发信账号的信誉并拖累所有组织；投诉说明收件人明确不想要，继续发同样伤信誉（投诉率是 Gmail 的硬指标）。服务商自身的抑制名单本来就会拒发。
- 代价：投诉过的用户收不到重置密码邮件，所以提供 owner/admin 手动解除并留审计（第 19 节默认 4、D31）。

### 14.3 滥用与枚举

- 按密钥 20 次/秒、按收件人同模板 3 次/分钟、试发按用户 5 次/分钟。
- 限频照用实例配置的 `RATE_LIMIT_STORE` 与现有失败策略（`RATE_LIMIT_STORE_FAILURE`，默认 `local`），不新增配置校验（D34）。`memory` 时每个副本各自计数；多副本部署必须设 `RATE_LIMIT_STORE=postgres` 才能共享计数，`DEPLOYMENT.md` 写明。生产 zmail.im 目前只运行一个 API 副本，`memory` 即为准确计数。
- 不透露：跨组织、跨 mode 一律 404；解除抑制对不存在行返回 204；回复邮箱无权与不存在同为 404。
- 模拟地址不触达服务商，不能被用来探测外部地址。

### 14.4 浏览器网关

所有会话路由经 Web 生产 API 适配器（HttpOnly 会话、固定路径、`Organization-Id` 转发、`Cache-Control: private, no-store`）；不新增任意代理。密钥路由不经浏览器。

## 15. 可观测与运维（§3.14）

- 结构化日志：`transactional_message.accepted`、`.dispatched`（尝试次数、耗时）、`.sent`、`.retry_scheduled`、`.failed`（失败码）、`.suppressed`、`.expired`、`.unknown_outcome`、`.purged`（数量）、`transactional_payload.decrypt_failed`。字段：`request_id`、`organization_id`、`message_id`、`template_id`、`mode`。
- 指标：`zmail_transactional_messages_total{mode,outcome}`、`zmail_transactional_dispatch_seconds`（入库到服务商接受）、`zmail_transactional_queue_oldest_seconds`、`zmail_send_lane_attempts_total{lane}`、`zmail_transactional_rate_limited_total{rule}`。
- 追踪：请求 span 覆盖到提交；派发单独 span，以消息 id 关联；`request_id` 写入消息日志事件。
- 告警：最老的未发事务消息 > 2 分钟（live，非 `send_limit`）；解密失败 > 0；5 分钟内失败率 > 5%；`transport.unknown_outcome` 出现即通知。
- 运维视图：控制台日志 + 通道用量；平台运维控制台不变（非目标）。
- 安全降级与关闭：`MAILBOX_TRANSACTIONAL_ENABLED=false` 后新请求 `503 MAILBOX.TRANSACTIONAL_DISABLED`，worker 继续发完已入库消息（加密密钥保留）；轮换加密密钥前先关闭功能并等队列清空，否则排队消息以 `send.payload_unavailable` 失败（`UPGRADING.md` 写步骤）。
- 升级说明（`UPGRADING.md`）：额度从共享改为三条通道；默认值下个人与群发额度不少于今天，组织理论最大外发量变为今天的三倍，运维需对照 Cloudflare 账号上限设置 `MAILBOX_DAILY_BROADCAST_LIMIT`、`MAILBOX_DAILY_TRANSACTIONAL_LIMIT`。

## 16. 测试与验证（§3.15）

| 关注点 | 证明 |
|---|---|
| 遮罩规则、指纹、模拟地址解析、退避计算 | 单元测试 |
| 变量声明校验、秘密不带 fallback、kind 互斥 | 服务测试 |
| 幂等唯一索引并发、键释放、通道计数、`SKIP LOCKED` 租约、终态无密文约束 | 一次性 PostgreSQL 测试 |
| scope、测试密钥拒绝、组织头、错误码、`Retry-After` | handler 测试 |
| OpenAPI 新路径与字段 | `openapi_contract_test.go`、`make route-catalog-check`、oasdiff |
| 五个页签、错误映射、URL 状态 | Vitest 组件与 hook 测试 |
| 投递事件分流、Webhook 去重 | 生产者、消费者与幂等测试 |
| 两个 worker 同时派发 | 确定性并发测试 |
| 迁移与回填 | 干净安装与升级路径（含 down） |

关键场景：

1. 秘密不落地：发送后查询所有列与日志输出，`482913` 只出现在 `payload_ciphertext` 解密结果里；`sent` 后密文为空；DB 约束拒绝在 `sent` 行写密文。
2. 主题含秘密：`subject_masked` 为 `Acme 验证码：••••••`；URL 秘密显示主机名。
3. 幂等：同键同内容重放 `200` 同 id；同键不同内容 `409`；25 小时后同键 `202` 新 id 且旧行 `idempotency_key` 为空；两个并发同键请求只产生一行、只调用一次服务商。
4. 限频：同一收件人同模板 60 秒内第 4 次 `429 MAILBOX.RECIPIENT_RATE_LIMITED` 带 `Retry-After`；同键重放不消耗收件人额度；第 21 次/秒 `429 COMMON.RATE_LIMITED`。
5. 通道：群发通道满时事务与个人发信均立即发出；事务通道满时排队，worker 有余量后发出；等待达上限 `429`；排队 1 小时置 `send.expired`。
6. 迁移：升级前 24 小时内的群发收件人消息回填为 `broadcast`；升级后个人与群发用量与回填一致；down 后旧代码按共享额度工作。
7. 抑制：永久退信或投诉后同地址 `202 suppressed`、不计额度、`email.suppressed`；临时退信（`deferred`）与 `rejected`（含 `transport.suppressed`）不进名单；名单中的地址被群发派发跳过且联系人状态不变；回填只含因 `bounced`、`complained` 抑制的联系人；解除后事务邮件可再发，联系人状态仍不变（`TestPermanentBounceSuppressesAcrossLanes`）。
8. 测试模式：测试密钥发往普通地址不调用服务商（传输替身断言 0 次）、不计额度；`bounced@test.zmail.im` 产生 `email.bounced permanent` 且不写名单；测试密钥调 `outbound-mail-messages` `403`。
9. 派发失败：服务商返回 `503`（`Responded=true`）重试 3 次后 `failed`；`transport.unreachable`、`transport.invalid_response`、`transport.response_too_large`（`Responded=false`）立即置 `failed`（`transport.unknown_outcome`），传输替身断言只被调用 1 次；调用后崩溃（租约过期）置 `unknown_outcome` 不重发。
10. 授权：成员改事务模板 `403`；跨组织读消息 `404`；成员读通道用量 `403`；事务模板用于群发 `422`。
11. 既有路径不变：`mailtransport.Error` 增加 `Responded` 后，个人发信与群发对 `transport.*` 与 `provider.http_*` 的处理、重试与契约错误码与升级前一致（现有发送与群发测试不改断言即通过）；`cloudflare.go` 只在收到 HTTP 状态时设 `Responded=true`（`mailtransport` 单元测试）。
12. 分页：日志键集分页稳定（并发插入不重复不遗漏）；30 天范围上限。
13. 清理：31 天前的消息与事件被删除，批量受限。

最终验证命令（按 `AGENTS.md` 验证矩阵）：

```bash
cd api && go test ./internal/infra/mailtransport/... ./internal/modules/mailbox/... ./internal/modules/apikey/... ./internal/modules/webhook/... ./database/...
cd api && make wire && make route-catalog-check
cd web && corepack pnpm type-check && corepack pnpm lint
cd web && corepack pnpm vitest run src/features/developer
python3 .agents/skills/business-implementation-planning/scripts/validate_plan.py --strict docs/plans/transactional-mail.zh-CN.md
```

契约变更另跑契约守卫与 oasdiff（CI 已包含）；发布前跑一次 `make check`。Kest 流程 `api/tests/kest/transactional_mail.flow.md` 在本地 API 与一次性 PostgreSQL 上覆盖场景 1、3、4、5、7、8（`log` 服务商 + 模拟地址，不发真信）。本次所有自动化工作（测试、Kest、代理执行的验证）一律不发送真实邮件；真实投递验收不属于本次交付，也不是任何切片的完成条件（D32）。

## 17. 交付计划（§3.16）

关键路径：S1 → S3 → S4。S2 与 S1 可并行；S5 在 S3 之后与 S4 并行。估算只给相对风险，不给日历天数。

### S1 额度通道拆分

- 结果：个人邮件、群发、事务三条通道分开计数；营销用满不再挤占员工手写邮件。
- 模块：发送通道；`api/`、`contracts/`、`web/`（额度提示文案不变，仅 `mailbox-domains` 新字段的 schema）。
- 契约与迁移：`MAILBOXES.md` Send 一节、`MailboxDomain` 新字段；迁移 `add_send_lanes`。
- 配置：`MAILBOX_DAILY_BROADCAST_LIMIT`、`MAILBOX_DAILY_TRANSACTIONAL_LIMIT`（默认取现有上限）。
- 测试与验收：场景 5（个人/群发部分）、6；`TestBroadcastLaneDoesNotConsumePersonalLane`。
- 回滚：down 迁移 + 回退代码。
- 排除：事务通道的实际使用（S3）。
- 风险：低；回填一次性 UPDATE 的耗时需在 10 万级群发收件人数据上测一次。

### S2 事务模板

- 结果：owner/admin 能建事务模板、声明秘密变量、选发件地址，并预览；事务模板不能被误用于邮箱发信或群发。
- 模块：模板；`api/`、`contracts/`、`web/`（事务模板页签只读 + 编辑）。
- 迁移：`add_transactional_templates`。
- 测试：场景 10（模板部分）、模板服务与 handler 测试、Web 表单测试。
- 回滚：down 删除事务模板与列。
- 排除：发送（S3）。

### S3 事务发送核心（首个端到端可用切片）

- 结果：开发者在快速上手建密钥、复制片段、从后端发出验证码；发送日志可查、秘密遮罩；测试模式与模拟地址可用；抑制生效。
- 模块：API 密钥（mode）、事务消息、抑制名单、投递事件分流、群发派发的抑制检查、`mailtransport`、worker；`api/`、`contracts/`、`web/`（快速上手、发送日志）。
- 任务（除事务消息本身外）：
  - `api/internal/infra/mailtransport/transport.go`：`Error` 增加 `Responded bool`（服务商返回了明确的 HTTP 状态）；`cloudflare.go` 只在拿到 HTTP 响应状态的分支（`provider.http_*`、`transport.permanent_bounce`、`transport.suppressed`）设为 `true`，`transport.unreachable`、`transport.invalid_response`、`transport.response_too_large` 保持 `false`；`log` 等其他传输按同一语义设置。个人发信与群发不读该字段，行为与契约错误码不变（D33）。
  - Wire：把 `*ratelimit.Stores` 注入 `mailbox` 模块（今天未注入），新增规则 `mail.transactional.key`、`mail.transactional.recipient`、`mail.transactional.test_send`；照用 `RATE_LIMIT_STORE` 与现有失败策略（D34）。
  - `apikey.RequireScopes` 内部按 mode 默认拒绝测试密钥，并提供允许测试密钥的选项（D36）。
  - 群发派发在现有联系人状态检查之外检查 `mail_suppressions`；投递事件只对 `bounced`、`complained` 写名单（D35）。
- 迁移：`add_api_key_mode`、`create_transactional_messages`、`create_mail_suppressions`。
- 配置：`MAILBOX_TRANSACTIONAL_ENABLED`、`MAILBOX_SECRET_ENCRYPTION_KEY`、`MAILBOX_SIMULATOR_DOMAIN`、`MAILBOX_TRANSACTIONAL_RECIPIENT_PER_MINUTE`、`MAILBOX_TRANSACTIONAL_KEY_PER_SECOND`。
- 测试与验收：场景 1–4、5（事务部分）、7–9、11–13；Kest 流程。不发送真实邮件（D32）。
- 回滚：关闭开关；必要时 down 迁移（丢失 30 天内事务记录）。
- 排除：Webhook（S4）、额度页与抑制管理（S5）、回复路由（推迟，D32）。
- 风险：中高——加密、幂等、租约与重试的组合；评审重点放在第 12 节伪代码。

### S4 Webhook

- 结果：接收方收到签名的 `email.*` 事件，载荷无秘密。
- 模块：事务消息（发布）、webhook 目录；`api/`、`contracts/`、`web/`（Webhook 页签）。
- 迁移：无。
- 测试：生产者与去重测试、载荷 schema 测试、Web 组件测试。
- 回滚：从目录移除类型（已排队的投递按 webhook 模块规则处理）。
- 排除：无。

### S5 额度与保护页、抑制管理

- 结果：管理员看到三条通道用量、管理抑制名单。
- 模块：发送通道（用量查询）、抑制名单（解除）；`api/`、`contracts/`、`web/`。
- 测试：场景 7（解除）、10（用量权限）。
- 回滚：隐藏页签；路由下线。
- 排除：按组织配置额度（第 19 节默认 3）、回复路由（D32）。

## 18. 设计决定汇总

以下均为"设计决定（待确认）"，正文对应位置已标注：

- D1 新资源 `/v1/transactional-messages`，不扩展 `outbound-mail-messages`（oasdiff 响应侧破坏性）。
- D2 scope 名 `mail:transactional`，与 `mail:send` 互不包含。
- D3 API 密钥增加不可变 `mode`；测试密钥前缀 `fastbuild_test_`，正式密钥前缀不变；未声明支持测试的路由拒绝测试密钥（拒绝放在 `RequireScopes` 内，见 D36）。
- D4 沿用用户拥有的密钥、`X-API-Key` 与 `Organization-Id` 头；不采用原型的 Bearer 与 `zk_live_` 前缀。
- D5 每次请求只有一个收件人，无抄送、无附件。
- D6 事务模板用 `key` 引用；`kind` 不可改；事务模板不能用于邮箱发信、模板试发与群发。
- D7 事务模板的增删改限 owner/admin，成员只读。
- D8 发件身份 = 模板上的 `from_address`，域名须为本组织已验证域名（`log` 服务商时可用实例域名）；不新建身份表；域名被事务模板使用时不可移除。
- D9 秘密变量不得带 fallback；渲染内容整体 AES-256-GCM 加密，终态清空；遮罩规则 `••••••` / URL 留主机名；指纹用 HMAC。
- D10 新环境变量 `MAILBOX_TRANSACTIONAL_ENABLED`、`MAILBOX_SECRET_ENCRYPTION_KEY`（≥32 字符）。
- D11 异步发送：新建 `202`、重放 `200`；提交后后台快速派发，worker 兜底。
- D12 `Idempotency-Key` 必填，作用域 `(组织, mode, 键)`，24 小时后释放。
- D13 未声明的变量拒绝。
- D14 只对服务商返回了 HTTP 状态的非永久失败（`Permanent=false && Responded=true`）自动重试，最多 3 次（30 秒、2 分钟、10 分钟）；结果未知不重发（判定方式见 D33）。
- D15 排队最长 1 小时，之后 `failed`（`send.expired`）。
- D16 三条通道各自的环境变量上限，默认取 `MAILBOX_DAILY_SEND_LIMIT`；事务通道满时排队，等待数上限等于通道上限。
- D17 worker 每轮先派发事务邮件。
- D18 新增组织级 `mail_suppressions`，回填自因 `bounced`、`complained` 抑制的联系人，群发派发与事务共用；owner/admin 可查看与解除（共享与写入规则见 D35）。
- D19 命中抑制时存一条 `suppressed` 记录、`202`、不计额度、发 `email.suppressed`。
- D20 限频用 `ratelimit` 基础设施的固定窗口；按密钥 `COMMON.RATE_LIMITED`，按收件人 `MAILBOX.RECIPIENT_RATE_LIMITED`，都带 `Retry-After`；先查幂等再按收件人限频。
- D21 模拟域可配置（默认 `test.zmail.im`），五个本地部分；正式密钥也可用；从不写抑制名单；测试密钥发普通地址按 `delivered` 模拟。
- D22 Webhook 增加 `email.failed`；载荷含完整收件人；测试消息也发布；不受邮箱 Webhook 开关约束。
- D23 投递结果沿用 `last_event` 的枚举与计算规则。
- D24 所有成员可读发送日志，控制台中收件人遮罩、可按完整地址精确筛选。
- D25 通道用量仅 owner/admin 可读。
- D26 已移出本次，见下方"后续"与 D32。
- D27 控制台路由 `/console/developers`，页签与模式进 URL；片段用 Node `fetch` 与 Python `urllib.request`；API 地址取 `NEXT_PUBLIC_DEVELOPER_API_ORIGIN`。
- D28 控制台试发只发往调用者账号邮箱，新路由 `/v1/transactional-test-sends`。
- D29 事务邮件作为 `mailbox` 模块的子领域，不另建 starter。
- D30 事务消息保留 30 天，由 `mail:broadcast:work` 清理；不提供取消命令。
- D31 第 19 节五个产品问题按本文默认定稿（设计决定（待确认））：① 沿用个人密钥，控制台在持有人旁提示离组即失效；② 事务通道满时排队，最长 1 小时后 `failed`（`send.expired`）；③ 三条通道都默认取 `MAILBOX_DAILY_SEND_LIMIT`（200），不做按组织设置；④ 永久退信与投诉都抑制事务邮件，owner/admin 可解除并留审计；⑤ 增加 `email.failed`。代价：① 若产品要组织服务密钥，需改 `apikey` 模型、契约与迁移，已发出的个人事务密钥要迁移或重建；② 若改为立即拒绝，要改 9.1 的错误表与 worker 的等待逻辑，已接入的调用方要处理新 `429`；③ 若要按组织配置，需要新表、运维界面与计数改造；④ 若投诉不该拦截，要拆分抑制原因并迁移已有行；⑤ 若撤掉 `email.failed`，已订阅的接收方会突然收不到该事件。
- D32 回复路由（原 D26）移出本次；本次的事务模板没有回复路由字段，事务消息不带自定义 `Reply-To`，收信流程不改；任何自动化工作（测试、Kest、代理执行的验证）都不发送真实邮件，原 S3"真实发信确认 Reply-To"一步删除（设计决定（待确认））。代价：试点组织无法把事务邮件的回信自动归到 support@ 并打标签，回信会发往发件地址（通常是不收信的 no-reply）；之后补做时要加模板与消息列、改收信事务，并由人完成一次 Cloudflare `Reply-To` 真实发信验证。
- D33 `mailtransport.Error` 增加 `Responded bool`，只在服务商返回了明确的 HTTP 状态时为 `true`；事务通道只在 `Permanent=false && Responded=true` 时重试，其余非永久失败（`transport.unreachable`、`transport.invalid_response`、`transport.response_too_large` 等，服务商可能已接受）一律视为结果未知，置 `failed`（`transport.unknown_outcome`），不重发；个人发信与群发忽略该字段，行为与契约错误码不变（设计决定（待确认））。代价：若服务商在 5xx 时其实已接受邮件，重试会产生重复（与今天个人发信相同的风险）；若网络中断时服务商其实未接受，开发者要换新幂等键重发，验证码会延迟。
- D34 `ratelimit.Stores` 经 Wire 注入 `mailbox` 模块；照用实例的 `RATE_LIMIT_STORE` 与现有失败策略，不新增配置校验；多副本部署必须设 `RATE_LIMIT_STORE=postgres` 才能共享计数（生产 zmail.im 现为单副本）（设计决定（待确认））。代价：多副本却用 `memory` 时，按收件人与按密钥的实际上限会放大到副本数倍，验证码轰炸防护变弱；若之后要强制校验，需要新增启动检查并通知已部署的实例。
- D35 `mail_suppressions` 是组织唯一的抑制名单：事务发送与群发派发都检查（群发在现有联系人状态检查之外再查它，命中跳过）；只在投递事件 `bounced`、`complained` 时写入（与现有 `RecordDeliveryEvent` 一致，`rejected` 与发送时的 `transport.suppressed` 不写）；写入与解除都不改变联系人状态（设计决定（待确认））。代价：解除后联系人若仍为 `suppressed`，群发仍跳过它，管理员需要另改联系人，两处状态可能让人困惑；`rejected` 不入名单意味着服务商已拒的地址仍会被事务邮件重试一次（服务商会再拒，只损失一次额度）。
- D36 默认拒绝测试密钥放在 `apikey.RequireScopes` 内部，路由以 `RequireScopes` 的选项（如 `AllowTestKeys`）或 `RequireScopesAllowingTest` 声明允许测试密钥；不使用单独排在后面的 `AllowTestKeys()` 中间件（gin 按顺序执行，后挂的中间件无法撤销已发生的拒绝）（设计决定（待确认））。代价：`RequireScopes` 的签名或变体增加，所有挂载点的写法要统一；若有路由只鉴权不调 `RequireScopes`，测试密钥会被放行，需要路由目录检查覆盖。

### 后续（不在本次）

- D26 回复路由：模板上设回复邮箱与可选标签，发信带 `Reply-To`，收信按 `In-Reply-To`/`References` 匹配 `transactional_messages.rfc_message_id` 给回信打标签。重新启动前须由人完成一次 Cloudflare 接受自定义 `Reply-To` 的真实发信验证（方法同 `docs/runbooks/mail-message-id-check.md`），自动化工作不做这一步（D32）。

## 19. 已默认的产品问题

以下五个问题原本需要产品确认，现按本文默认做法定稿（D31，设计决定（待确认）），不再阻塞任何切片；若产品负责人推翻某条，按 D31 中列出的代价调整对应切片。

1. **事务密钥属于个人还是组织？** 默认：沿用个人密钥；控制台在密钥持有人旁提示"持有人离开组织后这把钥匙立即失效"。组织服务密钥不在本次范围。
2. **事务通道满时排队还是立即拒绝？** 默认：排队，最长 1 小时后 `failed`（`send.expired`，D15）；等待数达通道上限时 `429 MAILBOX.SEND_LIMIT_REACHED`。
3. **三条通道的默认额度和是否按组织设置？** 默认：三条通道都取实例配置，未单独设置时都等于 `MAILBOX_DAILY_SEND_LIMIT`（200）；不做按组织设置。
4. **投诉过的地址是否也拦截事务邮件，管理员能否解除？** 默认：永久退信与投诉都写入抑制名单并拦截事务邮件；owner/admin 可解除，写审计 `mail_suppression.removed`（D18、D35）。
5. **是否增加 `email.failed` 事件？** 默认：增加（D22），覆盖服务商永久拒绝、结果未知、重试用尽、排队超时、发件域名失效与投递事件 `failed`。

## 20. 验收

- T-1：只含 `mail:transactional` 的密钥能发事务邮件；调用群发、邮箱发信或读任何邮箱都被拒（`403`）。
- T-2：秘密变量的明文不出现在日志接口、Webhook、控制台、应用日志中，发送完成后库中也没有密文。
- T-3：群发通道用满时，验证码在 10 秒内被服务商接受（p95，本地 `log` 服务商下以派发延迟衡量）。
- T-4：同一幂等键 24 小时内只发一封，重放返回第一次的结果。
- T-5：同一收件人同模板 1 分钟第 4 封、同一密钥 1 秒第 21 次返回 `429` 和 `Retry-After`。
- T-6：永久退信或投诉过的地址不再收到事务邮件与群发，不计额度，并推送 `email.suppressed`；联系人状态不因名单而改变。
- T-7：测试密钥不投递、不计额度；模拟地址产生对应的投递事件和 Webhook。
- T-8：事务邮件不出现在任何邮箱的"已发送"、组织邮件日志和统计中；30 天后被清理。
- T-9：测试密钥调用任何未声明支持测试的路由（如 `POST /v1/outbound-mail-messages`）返回 `403 PERMISSION.DENIED`；服务商未返回 HTTP 状态的失败不重发。
- T-10：升级后现有组织的个人邮件和群发可用额度不少于升级前。

## 21. 就绪检查（§6）

### 产品与领域

- [x] 结果、范围、非目标、角色明确（第 1、3 节）。
- [x] 术语与 `CONTEXT.md` 一致：scope 仍是削减，webhook 术语沿用，新术语限于 mailbox 契约（第 6 节）。
- [x] 主流程、拒绝、取消（不提供，已说明）、超时、恢复已定义（第 4、8、12 节）。
- [x] 状态机无缺失或矛盾的迁移；发送状态与投递结果是两个独立生命周期（第 8 节）。
- [x] 数量、时间、时区语义明确：UTC 瞬时、滚动 24 小时、固定窗口限频（6.6）；无金额。

### 归属与架构

- [x] 每条规则、记录、路由、事件、任务都有唯一所有者（第 5 节目录）。
- [x] 模块职责与排除明确。
- [x] 部署单元只经契约通信；密钥路由不经浏览器。
- [x] 同步与异步依赖列出（第 11 节）。
- [x] 事务边界不跨进程；服务商调用在事务外。

### 数据与契约

- [x] 表、约束、索引、迁移、回滚、回填已定义（第 7 节）。
- [ ] 公共 HTTP 行为写入 `contracts/`——本文定义了内容，契约文件随各切片先行修改（第 17 节），实现前完成。
- [x] 授权包含组织与行级范围（第 10 节）。
- [x] 分页、筛选、排序、上限、错误码已定义（第 9 节）。
- [x] 每个重要写操作的幂等与并发行为已定义。

### 副作用与运维

- [x] 派生记录各有唯一触发与幂等键（第 11 节表）。
- [x] 事件的投递、重试、去重、顺序、重放已定义。
- [x] 定时任务有可执行的所有者（`mail:broadcast:work`）与失败可见性。
- [x] 审计、隐私、指标、告警、排障信息已定义（第 14、15 节）。
- [x] 上线、回滚、安全关闭已定义。

### 交付与证明

- [x] 切片按依赖排序，S3 为首个端到端可用切片。
- [x] 每个切片有可观察的验收。
- [x] 关键不变量映射到具体测试（第 16 节）。
- [x] PostgreSQL 行为有 PostgreSQL 证明。
- [x] 验证命令与受影响的部署单元一致。

未勾选的一项是流程性前置（契约先行），不是未决设计。第 19 节的五个问题已按默认定稿（D31），不阻塞任何切片；回复路由推迟（D32）。

readiness: READY WITH RECORDED ASSUMPTIONS

记录的假设：生产部署 `mail:broadcast:work` 常驻运行（现有部署已如此）；生产 API 为单副本，或多副本时已设 `RATE_LIMIT_STORE=postgres`（D34）；产品负责人接受第 19 节的默认做法（D31）；评审接受第 18 节的设计决定。
