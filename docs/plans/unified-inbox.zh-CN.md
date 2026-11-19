# 统一收件、邮件角色分组与 v2 外观（切片 ①）设计

状态：已实现（分支 `feature/unified-inbox`，验收流 `api/tests/kest/unified_inbox.flow.md`）。原状态：设计稿，待评审。分支 `docs/v2-architecture`（基于 main `0cd4191`，已含 #22 邮箱权限、#23 发送队列、#27 邮箱整理）。
上级需求：[产品需求文档](../PRODUCT_REQUIREMENTS.zh-CN.md) R2「每日统一收件」中不依赖外部邮箱的部分、R3 角色台账的第一步；[邮箱权限功能需求文档](../MAILBOX_ACCESS_PRD.zh-CN.md) §3.6、§3.7。
原型：[docs/design/v2/MainV2.dc.html](../design/v2/MainV2.dc.html)（收件箱 v2，1440×900 静态稿）。下文凡称"原型"均指此文件；它是 S4 与截图回归 UI-11 的视觉基准，§11 已把其中的布局数值抄录为文字，实施时以 §11 为准、以原型核对。本文按 [BUSINESS_IMPLEMENTATION_PLAN_STANDARD.md](../BUSINESS_IMPLEMENTATION_PLAN_STANDARD.md) 编写。

标注约定：

- 「设计决定（待确认）」：代码和已定需求都没有给出答案，由本文按现有模式做的选择，编号 D1–D31，汇总在 §17。评审不提异议即按此实现。
- 「需要产品确认的问题」：会改变要做什么、需要产品负责人拍板的事项，见 §18。

## 已默认的产品问题

以下四个产品问题在评审前已按默认做法定稿，均为设计决定（待确认）；完整的影响和备选见 §18，评审不提异议即按此实现。

1. 外发额度小组件降级：不画进度条、不显示"日常 / 群发 / 留给回信"分项，不新增组织用量接口（设计决定（待确认），D24、§11.9）。
2. "今日新到"包含今天到达后已被归档或移入文件夹的信（回收站除外），"今天"按查看者自己的时区计算（设计决定（待确认），D3、D4、§5.2）。
3. 成员能看到含其可见邮箱的角色的名称、颜色、描述和负责人（设计决定（待确认），D16、§8.4）。
4. 全站一次性换成 v2 外观，登录页、官网和控制台其它页面同步换肤并做截图回归（设计决定（待确认），D22、§11.10）。

## 1. 概述与范围（3.1）

### 1.1 要解决的问题

今天一个人每天早上要在邮箱切换器里逐个打开邮箱，才能知道哪里来了新信：`/console/mailboxes` 一次只显示一个邮箱（`web/src/features/mailbox/components/mailbox-workspace.tsx` 按 `?mailbox=` 或本地记住的邮箱选一个）。组织级日志 `GET /v1/mail-messages` 虽然跨邮箱，但它是投递记录：不分位置（归档、回收站都在里面）、不按线程、没有今日新到、没有按人的星标视图，也没有分页之外的计数。邮箱多了以后，用户也无法按"这个邮箱代表谁"来分组查看。

### 1.2 用户结果

一名成员打开邮件页，第一屏就是"全部收件"：他**有权阅读**的所有邮箱的收件箱合在一起，按收件时间倒序、按会话线程聚合，每行标出所属角色。侧栏可以切换"今日新到 / 待办 / 已发送"，可以按"角色 → 邮箱"收窄范围，看到每个视图、角色、邮箱的未读数。整个 Web 应用换成 v2 外观，常用操作都有快捷键和 ⌘K 命令面板。

### 1.3 本切片包含的流程

1. 统一收件列表：四个视图 `all` / `today` / `pending` / `sent`，按角色、邮箱、未读筛选，`q` 搜索，游标分页，`all` 与 `sent` 可按线程聚合。
2. 侧栏计数：每个视图、每个角色（含"未分组"）、每个邮箱的计数。
3. 邮件角色（R3 第一步）：组织所有者和管理员新建、修改、删除角色，把邮箱分到角色；成员看到自己可见邮箱所属的角色。
4. Web v2：全局主题令牌、统一收件为默认邮件页、角色与邮箱侧栏、阅读栏、快捷键、⌘K、外发额度小组件。

### 1.4 非目标（本切片不做，但留出位置）

| 不做 | 去向 | 本切片留的位置 |
|---|---|---|
| AI 分流标签页（需要回复 / 有期限 / 只需知道）、AI 回复草稿、"按 F 纠正" | 切片 ③ | 列表顶部的标签栏组件按"标签页定义数组"渲染；本切片只给出 全部 / 未读 两项（D25）。`pending` 视图的筛选在服务层是一个独立谓词，切片 ③ 可替换为"我的星标 ∪ AI 待办" |
| 验证码遮罩与"显示验证码"面板 | 切片 ② | 阅读栏正文区是单一组件 `InboxReadingPane`，本切片原样渲染正文（沿用现有沙箱 iframe） |
| 事务邮件 | 切片 ⑤，单独设计 | 无 |
| Gmail / Outlook 绑定与来源徽标（原型 `docs/design/v2/MainV2.dc.html` 侧栏邮箱行右侧的 `Gmail` 胶囊） | 切片 ④ | 侧栏邮箱行预留右侧徽标槽；列表项不含来源字段，切片 ④ 新增 `source` 时客户端忽略未知字段即可 |
| 平台账号台账、员工开通任务 | R3 后续切片 | `mail_roles` 只放名称、颜色、负责人、描述；台账表以后用 `role_id` 外键引用角色，不改本表 |
| 稍后提醒（原型 `docs/design/v2/MainV2.dc.html` 阅读栏工具栏的"稍后 H"）、草稿箱、收信规则 | 未排期 | 快捷键表不绑定 H |
| 组织级外发用量接口、"日常 / 群发 / 留给回信"分项 | 见 §18 问题 1 | 额度小组件按现有数据降级（D24） |
| 全局跨组织收件、Admin 控制台改动 | 不做 | `admin/` 不变 |

反目标（即使功能可用也算失败）：

- 统一收件、计数、角色、命令面板任一入口让人看到无"查看"权邮箱的主题、发件人、数量或存在（包括组织管理员看私人邮箱）。
- 封存历史出现在统一收件或计数中。
- 统一收件把两个邮箱的邮件并成一个线程。
- 新接口拖慢现有单邮箱列表或 `GET /v1/mailboxes`。

### 1.5 受影响的部署单元

- `contracts/`：`MAILBOXES.md` 新增"统一收件"和"邮件角色"两节，`openapi.yaml` 新增 6 个路径和 `role_id` 字段，`CONTEXT.md` 新增术语 *Mail role*。
- `api/`：`internal/modules/mailbox`（新文件 `inbox_*.go`、`role_*.go`），`internal/domain`（`mail_inbox.go`、`mail_role.go`），两个迁移。
- `web/`：`src/features/mailbox`（统一收件组件、角色管理、快捷键、命令面板、BFF 路由与开发 mock），`src/themes/`、`src/app/layout.tsx`（字体与令牌）。
- `admin/`：不变。

### 1.6 首次发布的成功信号

- 验收组织（管理员 A、员工 B、C；B、C 各有私人邮箱，共享邮箱 `team@` 中 B 为 send、C 为 view）里，B 打开邮件页一屏看到 `b@` 与 `team@` 的新信，C 看到 `c@` 与 `team@`，A 看不到任何私人邮箱的信，也看不到它们的数量。
- 10 个可读邮箱共 20 万封邮件时，统一收件首页 p95 < 200 ms（D28）。
- 角色分组后，侧栏角色未读数之和 + "未分组"未读数 = "全部收件"未读数。

## 2. 参与者与访问面（3.2）

| 参与者 | 目标 | 入口 | 读取范围 | 变更范围 | 审批 | 被拒绝时 |
|---|---|---|---|---|---|---|
| 组织成员（`member`） | 一处处理自己能读的所有信 | Web `/console/mailboxes`；API `GET /v1/inbox-messages`、`GET /v1/inbox-message-counts`、`GET /v1/mail-roles` | 自己有 `view` 的邮箱（私人邮箱本人、共享邮箱成员）中未封存的邮件；这些邮箱所属的角色 | 不新增变更；归档、回收站、星标、已读仍走现有单邮箱接口并按该邮箱权限检查 | 无 | 写角色接口 `403 PERMISSION.DENIED`；对不可读邮箱或不可见角色的筛选返回空结果（D8） |
| 组织所有者、管理员（`owner`、`admin`） | 与成员相同，另外按业务身份整理邮箱 | 成员的全部入口，另加 `/v1/mail-roles` 的写接口和 `/v1/mailboxes/:mailbox_id/role-assignment` | 读信范围与成员**完全相同**：只来自私人邮箱本人身份或共享邮箱成员身份，角色不额外授予；能看到全部角色与全部邮箱的元数据（地址、角色归属），这与 `GET /v1/mailboxes` 现状一致 | 新建、修改、删除角色；为任一邮箱设置或清除角色 | 无 | 无查看权的邮箱在统一收件中不存在；侧栏显示为锁定、无数字 |
| 邮箱成员中的 `manage` | 同成员 | 同成员 | 同成员 | 不能管理角色（角色是组织级整理，不是邮箱成员权限） | 无 | `403 PERMISSION.DENIED` |
| API key 调用方 | 不适用 | 新接口只挂在会话路由组（与 `/v1/mail-messages` 相同），不挂在 API key 路由组 | 无 | 无 | 无 | 路由不存在 |
| 组织成员离开事件（系统） | 清理角色负责人 | `domain.EventOrganizationMemberRemoved` 订阅（现有 `membership_events.go`） | 无 | 把该用户在该组织中作为负责人的角色 `owner_user_id` 置空 | 无 | 失败只记日志，读取时兜底（D17） |

继承关系的附加限制：所有者、管理员继承成员能力，但**不会因角色读到任何邮件**。这条直接复用 `domain.MailboxAccessFor`：`owner`/`admin` 只得到 `Administer`、`SeeHistory`、`ManageMembers`，`View` 只来自私人邮箱主人或共享邮箱成员行。统一收件只用 `View`。

## 3. 端到端流程（3.3）

### 3.1 流程 A：打开统一收件

```text
浏览器(InboxWorkspace) ──GET /api/inbox-messages?view=all&group=thread──▶ Web BFF
Web BFF ──白名单参数 + Organization-Id + 会话凭据──▶ Go API GET /v1/inbox-messages
Go API: 组织上下文中间件 → InboxHandler → InboxService
InboxService ──readableMailboxIDs(viewer)──▶ 访问规则（mailboxes + mailbox_members）
InboxService ──ListInbox(ids, filter, cursor)──▶ InboxRepository ──▶ PostgreSQL
InboxService ──fillViewerState（每人已读、星标）──▶ Repository
Go API ──游标页信封──▶ Web BFF ──private, no-store──▶ 浏览器
```

1. 发起者与前置条件：已登录、带 `Organization-Id`、是该组织成员；`organization` 和 `mailbox` 两个 starter 均启用。
2. 权威输入与校验：查询参数按 §8.2 校验；BFF 只转发白名单参数。
3. 记录变更：无。读取不改变已读状态（与现有列表一致：打开详情才标已读，且由客户端显式调用现有更新接口置 `read`）。
4. 状态迁移：无。
5. 跨模块调用：只读 `organization_memberships`（经已有组织上下文中间件）。无事件。
6. 用户可见结果：列表、行内角色标签、未读圆点；无可读邮箱时显示空状态"你还没有可阅读的邮箱"。
7. 拒绝与恢复：参数错误 `400 COMMON.INVALID_INPUT`（Web 回到默认视图）；搜索过宽 `422 MAILBOX.SEARCH_TOO_BROAD`（提示输入更长关键词）；`401` 走登录；组织丢失沿用 `OrganizationLostState`。
8. 超时、取消、重试：搜索语句 2 秒超时（沿用 `MailSearchTimeout`）；非搜索查询不设专门超时，依赖数据库默认与请求上下文取消；客户端 TanStack Query 重试一次，`4xx` 不重试。
9. 审计与通知：读请求不进写请求审计；记录结构化日志（§13.1），不含搜索词。

### 3.2 流程 B：在统一收件中对一封信或一个线程做动作

不新增写接口。每行带 `mailbox_id`，客户端按现有契约调用：

| 动作 | 调用（现有） | 所需权限 |
|---|---|---|
| 打开并标记已读 | 单邮箱详情 `GET .../messages/:message_id`，然后用现有单封更新接口 `.../messages/:message_id` 置 `read` | `view` |
| 星标 `s` | 同一 `PUT` 置 `starred` | `view` |
| 归档 `e`、移入回收站 `#`（线程行） | `/v1/mailboxes/:mailbox_id/message-updates`，`message_ids` 为该行 `thread.message_ids`，`placement` 为 `archive` 或 `trash` | `send` |
| 回复 `r`、全部回复 `a`、转发 `f` | 现有写信对话框，发件邮箱固定为该行邮箱 | `send` |

客户端按 `GET /v1/mailboxes` 返回的 `access.send` 隐藏或禁用 `e`、`#`、`r`、`a`、`f`；API 仍按现有顺序检查（404 → 403 → 404 → 422）。动作成功后失效 `inbox-messages`、`inbox-message-counts`、该邮箱的 `messages` 与 `message-counts` 查询。线程中超过 100 封时沿用 organize 的规则：只作用于列出的 id，刷新后该行仍在。

### 3.3 流程 C：管理员整理角色

```text
管理员 ──POST /api/mail-roles {name,color,description,owner_user_id}──▶ BFF ──▶ API
API: RoleService.Create → 校验 → 事务（组织级咨询锁 → 计数 ≤50 → 插入）→ 审计 → 201
管理员 ──PUT /api/mailboxes/:id/role-assignment {role_id}──▶ BFF ──▶ API
API: RoleService.Assign → authorizeMailbox(canAdminister) → 事务（UPDATE mailboxes SET role_id）→ 审计 → 200 邮箱
管理员 ──DELETE /api/mail-roles/:id──▶ API: 删除角色；外键 ON DELETE SET NULL 在同一语句内把邮箱放回"未分组" → 204
```

拒绝与恢复：成员写角色 `403 PERMISSION.DENIED`；重名 `409 MAILBOX.ROLE_NAME_TAKEN`（输入框内提示，保留输入）；第 51 个 `422 MAILBOX.ROLE_LIMIT_REACHED`；角色已被他人删除 `404 MAILBOX.ROLE_NOT_FOUND`（刷新角色列表）。没有异步部分，没有超时状态。每次写入记录请求审计（§12）。

### 3.4 流程 D：侧栏计数刷新

列表与计数分别查询，计数每 `MAIL_REFRESH_MS` 后台刷新（沿用 `liveRefresh`），任一写动作成功后立即失效。计数和列表在两次查询之间可能不一致（另一成员刚好读了信不会影响我的未读；新信到达会），下一次刷新收敛，不做跨查询一致性。

## 4. 模块拆分（3.4）

### 4.1 模块目录

| 模块 | 职责 | 输入 | 输出 | 数据归属（唯一写入者） | 依赖 | 消费者 | 优先级 |
|---|---|---|---|---|---|---|---|
| 邮箱访问（现有，`access_service.go`） | 计算调用者对每个邮箱的 `access`；`readableMailboxIDs` | 组织上下文 | 可读邮箱 id 集 | `mailbox_members`、`mailbox_access_events`（不变） | 组织成员资格 | 统一收件、角色、现有全部邮箱接口 | 已有，P0 依赖 |
| 统一收件读模型（新，`inbox_*.go`） | 跨可读邮箱的视图、筛选、搜索、线程聚合、游标、计数 | `GET /v1/inbox-messages`、`GET /v1/inbox-message-counts` | 游标页、计数 | **无**。只读 `mail_messages`、`mail_message_reads`、`mail_message_stars`、`mail_message_labels`、`mailboxes` | 邮箱访问、PostgreSQL | Web 统一收件 | P0：切片的主体 |
| 邮件角色（新，`role_*.go`） | 角色生命周期、名称唯一、上限、邮箱归属 | 角色 CRUD、角色分配、成员离开事件 | 角色、带 `role_id` 的邮箱 | `mail_roles`；`mailboxes.role_id` 列（唯一写入者是角色分配命令与外键动作） | 邮箱访问（`canAdminister`）、组织成员目录、请求审计 | Web 侧栏与管理对话框、统一收件（只读 `role_id`） | P1：分组依赖它，但统一收件不依赖 |
| 邮箱整理（现有，organize） | 位置、线程、星标、标签、搜索语义 | 现有接口 | 现有 | 现有 | 现有 | 统一收件复用其谓词与线程统计 | 已有 |
| Web 邮件工作区（`web/src/features/mailbox`） | 统一收件界面、角色管理、快捷键、命令面板、额度小组件、BFF 与 mock | 浏览器交互 | 浏览器路由 `/api/...` | 无持久化；`localStorage` 只存界面偏好 | 契约 | 用户 | P0 |
| Web 主题（`web/src/themes`、`src/app/layout.tsx`） | v2 令牌、字体、暗色 | 无 | CSS 变量 | 无 | 无 | 全部 Web 页面 | P1，可并行 |

### 4.2 模块：统一收件读模型

**目标**：让调用者在一次请求里拿到他有权阅读的所有邮箱中，某个视图的一页邮件或线程，以及侧栏需要的计数。

**职责**：视图定义（§5.2）；筛选（角色、邮箱、未读、`q`）；按 `(created_at, id)` 游标分页；`all`/`sent` 的线程聚合；摘要 `snippet`；计数。

**明确不做**：任何写入；邮件详情（继续用单邮箱详情接口）；投递记录（继续用 `/v1/mail-messages`）；API key 访问。

**入口**：公开 `GET /v1/inbox-messages`、`GET /v1/inbox-message-counts`；内部 `InboxService.List(ctx, viewer, InboxFilter)`、`InboxService.Counts(ctx, viewer, tz)`。

**服务层不变式**：

1. 结果只来自 `readableMailboxIDs(viewer)` 返回的邮箱，过滤先于任何读取；空集直接返回空页，不访问 `mail_messages`。
2. 每条结果满足 `unsealedMessage`。
3. `trash` 永不出现在任何视图（含 `q`）。
4. 线程聚合只在同一邮箱内（`thread_id` 等于起始邮件 id，id 全局唯一，故跨邮箱不可能同 id；查询仍以 `(mailbox_id, thread_id)` 为键）。
5. `read`、`starred` 是调用者自己的（按 `viewer.MembershipID`），出站邮件 `read` 恒为 `true`。

**拥有的数据 / 只读的数据**：不拥有；只读见目录。

**依赖**：同步依赖邮箱访问；无事件。新增两个仓储方法：

- `ListMailboxAccessRows`：列出组织邮箱（含 `role_id`）及调用者的成员行，**不计算未读数**；服务层用 `domain.MailboxAccessFor` 得出可读集。列表、计数、`GET /v1/mail-roles` 都从它取可读集或可见集。
- `CountUnreadByMailboxIDs(ctx context.Context, organizationID, readerMembershipID uint, mailboxIDs []uint) (map[uint]int, error)`：只对传入的邮箱 id 做"收件箱中我未读"的分组计数，谓词与现有 `ListMailboxes` 的分组查询相同（`direction='inbound'`、`placement='default'`、`unsealedMessage`、无该成员的 `mail_message_reads` 行），附加 `mailboxes.organization_id = ?` 条件，走现有索引 `idx_mail_messages_listing (mailbox_id, direction, id DESC)`；`mailboxIDs` 为空时直接返回空 map，不查库。

**不**复用 `visibleMailboxes`：它调用的 `repository.ListMailboxes`（`api/internal/modules/mailbox/repository.go:91-140`）对组织**全部**邮箱做分组未读计数，再由 `service.ListMailboxes` 把不可读邮箱的数字置 0。管理员非成员、或组织里有大邮箱而调用者不可读时，这会为不可读的邮件付出扫描代价，违背"不可读数据不拖慢查询"（§14.3）。`GET /v1/mailboxes` 本身不改。

**授权与范围**：组织上下文（中间件）→ 可读邮箱集 → 筛选与可读集求交。

**并发、幂等、事务**：只读；搜索在 `SET LOCAL statement_timeout` 的只读事务中（沿用 `repository.search`）。权限在两次查询之间被撤销时，本次结果按请求开始时的可读集返回，下一次请求生效，与 `/v1/mail-messages` 现状一致。

**失败与恢复**：数据库不可用 `503 COMMON.SERVICE_UNAVAILABLE`；搜索超时 `422 MAILBOX.SEARCH_TOO_BROAD`；无其它可恢复状态。

**浏览器面**：§11。

**可观测与审计**：§13；读不进写审计。

**验收**：

- Given C 是 `team@` 的 view 成员且拥有 `c@`，when 请求 `view=all`，then 只返回 `team@` 与 `c@` 收件箱中的邮件，`b@` 的 0 封，`meta` 不暴露其它邮箱。
- Given A 是管理员且不是任何邮箱成员，when 请求任一视图或计数，then 返回空页与全 0 计数，`mailboxes` 计数数组为空。
- Given `b@` 已交接给 C、封存边界之前有邮件，when C 请求 `view=today` 和计数，then 封存邮件不出现也不计数。
- Given 两封信分别在 `team@` 与 `c@`、`In-Reply-To` 相互引用，when `group=thread`，then 它们是两行。

**验证**：`cd api && go test ./internal/modules/mailbox/... -run 'Inbox'`；基准 `-run '^$' -bench 'Inbox'`（PostgreSQL）。

### 4.3 模块：邮件角色

**目标**：给邮箱一个"代表谁"的组织级分组，用于导航和行内标识。

**职责**：角色的新建、修改、删除；名称唯一与上限；把邮箱分到至多一个角色；成员离开时清空负责人；按调用者可见性返回角色。

**明确不做**：不授予任何邮箱权限（角色负责人对角色内邮箱没有任何访问权）；不做平台账号、开通任务、外部来源；不写邮箱访问历史（角色归属不是权限事实，D15）。

**入口**：公开 `GET /v1/mail-roles`、POST `/v1/mail-roles`、PUT/DELETE `/v1/mail-roles/:role_id`、PUT `/v1/mailboxes/:mailbox_id/role-assignment`；`GET /v1/mailboxes` 每项新增 `role_id`。内部 `RoleService.MemberRemoved`。

**服务层不变式**：

1. 角色属于且只属于一个组织；任何按 id 的操作都带 `organization_id` 条件，别组织的角色等同不存在。
2. 名称去首尾空白后 1–64 字符，组织内忽略大小写唯一（唯一索引兜底）。
3. 每组织至多 50 个角色（D14，等于每组织邮箱上限）。
4. 颜色取自固定色板 `gray, red, orange, yellow, green, teal, blue, purple, pink`（与标签相同，数据库 CHECK 兜底）。
5. 描述去首尾空白后 0–280 字符，不含控制字符。
6. `owner_user_id` 为空或为当前组织成员；写入时校验，读取时不是当前成员的负责人返回 `null`。
7. 一个邮箱至多一个角色（单列外键保证）；删除角色在同一语句内把其邮箱置为未分组。
8. 角色读写都不改变任何邮箱的 `access`。

设计决定（待确认）D31：在切片 ① 中，邮件角色**只用于展示**，任何情况下都不影响访问；`owner_user_id` 同样只用于展示，负责人离开组织时置为 `NULL`（D17）。PRD R3 中"负责人交接"和"离职后失去业务邮箱访问"推迟到切片 ④（平台账号台账），由切片 ④ 自行新增交接与回收行为，且不改变不变式 8（角色本身仍不授予、不撤销权限，访问变化仍只通过邮箱成员与交接命令发生）。判断错误的代价：若产品要求在切片 ① 就让负责人或离职影响访问，需要改 `owner_user_id` 列（例如改为成员关系外键或增加交接状态）的 schema，并改写 `EventOrganizationMemberRemoved` 处理器的离职行为，从"清空负责人"变为"触发交接或回收访问"。

**拥有的数据**：`mail_roles`；`mailboxes.role_id`。**只读**：`mailboxes`、`mailbox_members`、组织成员目录。

**依赖**：同步：邮箱访问（`authorizeMailbox(..., canAdminister)`）、`membershipDirectory.FindForUser`（已有接口，校验负责人）、请求审计 `auditstarter.RecordChange`。事件消费：`EventOrganizationMemberRemoved`（已订阅，在现有 `MemberRemoved` 内增加一步）。事件发布：无。

**授权与范围**：读：任何组织成员；写：`viewer.Role.CanManageOrganization()`（所有者、管理员）；分配：对该邮箱有 `administer`（只有所有者、管理员有）。

**并发、幂等、事务**：

- 新建：事务内 `pg_advisory_xact_lock(hashtext('mail_roles'), organization_id)` 后计数再插入，并发新建不会超过 50（与 `lockMailbox` 同一模式）；重名由唯一索引报 `23505` 映射为 `409`。
- 修改：整体替换，后写者胜（D18）；不需要版本号，因为字段之间没有跨字段不变式，覆盖不会产生非法状态。
- 删除：单条 `DELETE`；重复删除第二次 `404`。
- 分配：单条 `UPDATE mailboxes SET role_id = ? WHERE id = ? AND organization_id = ? AND deleted_at IS NULL`；同值重复调用幂等返回 `200`；与删除角色并发时外键违例 `23503` 映射为 `404 MAILBOX.ROLE_NOT_FOUND`。

**失败与恢复**：成员离开事件处理失败只记日志（与现有处理器相同），读取时的成员过滤保证不显示已离开的负责人；下一次修改该角色或该用户再次被移除时会再清理。

**浏览器面**：侧栏"角色与邮箱"分组、"管理角色"对话框、"管理邮箱"对话框中每个邮箱的角色选择。

**可观测与审计**：每个写操作 `RecordChange`（resource `mail_roles` 或 `mailboxes.role`）；结构化日志见 §13。

**验收**：

- Given 成员 B，when POST 角色，then `403 PERMISSION.DENIED` 且无记录。
- Given 管理员 A 删除含 2 个邮箱的角色，then 两个邮箱 `role_id` 变为 `null`，统一收件计数中它们的未读进入"未分组"。
- Given 角色"设计编辑部"只含 `b@`（私人），when C 请求 `GET /v1/mail-roles`，then 不返回该角色；B 请求时返回该角色且 `mailbox_ids` 为 `[b@ 的 id]`。
- Given 两个管理员同时新建第 50、第 51 个角色，then 恰好一个成功，另一个 `422 MAILBOX.ROLE_LIMIT_REACHED`。

**验证**：`cd api && go test ./internal/modules/mailbox/... -run 'MailRole'`；迁移测试 `go test ./database/migrations/... -run 'MailRoles'`。

### 4.4 模块：Web 邮件工作区与主题

见 §11。模块级验收：统一收件为 `/console/mailboxes` 默认内容；`?mailbox=ID` 仍打开原单邮箱工作区（文件夹、标签、回收站、权限面板全部可用）；快捷键与 ⌘K 可用键盘完成"打开下一封 → 归档 → 回复"。

## 5. 领域模型与术语（3.5）

### 5.1 术语

| 术语 | 定义 | 与易混词的区别 |
|---|---|---|
| 统一收件（Unified inbox） | 一个调用者有 `view` 的全部邮箱，在某个视图下合并的读模型 | 不是组织日志（`/v1/mail-messages` 是投递记录，不分位置） |
| 视图（Inbox view） | `all`、`today`、`pending`、`sent` 之一，定义见 §5.2 | 不是单邮箱的"位置"（placement）；视图由位置、方向、时间、星标组合而成 |
| 邮件角色（Mail role） | 组织级的业务身份分组（品牌、栏目、个人），一个邮箱至多属于一个 | **不是**组织成员角色（`owner`/`admin`/`member`），**不是**权限 starter 的 access role，也不授予任何权限。设计决定（待确认）D13：代码与契约一律用 `mail role` / `mail_roles`，界面文案用"角色"，并在 `CONTEXT.md` 增加此词条 |
| 未分组 | `role_id` 为空的邮箱集合 | 不是一个角色记录 |
| 今日 | 见 D3、D4 | 不是"未读" |

### 5.2 视图定义

| 视图 | 方向 | 位置 | 其它条件 | 可线程聚合 |
|---|---|---|---|---|
| `all` 全部收件 | `inbound` | `default` | — | 是（`group=thread`） |
| `today` 今日新到 | `inbound` | 除 `trash` 外全部 | `created_at ∈ [今日起点, 次日起点)` | 否 |
| `pending` 待办 | 两向 | 除 `archive`、`trash` 外全部 | 调用者已加星 | 否 |
| `sent` 已发送 | `outbound` | `default` | — | 是 |

- `all` 与 `sent` 正是单邮箱的"收件箱"和"已发送"（`placement=default` 加 `direction`），只是跨邮箱。
- 设计决定（待确认）D4：`today` 是"今天到达了什么"，与是否已读、是否已归档无关：今天到达后被归档或移入文件夹的信仍算今日新到，被移入回收站的不算。这样满足 PRD R2"邮箱新增记录单列，避免把未读旧信当作今日新信"：昨天以前的未读信不会出现在"今日新到"。
- 设计决定（待确认）D3（"今日"的边界）：请求带 `tz`（IANA 时区名，默认 `UTC`）。今日起点 = 在 `tz` 中 `now` 所在日期的 00:00，次日起点 = 该日期加一天的 00:00，二者换算为 UTC 瞬间后与 `created_at`（`timestamptz`）比较，左闭右开。夏令时切换日按 Go `time.Date(y, m, d, 0, 0, 0, 0, loc)` 求两端，当天长度可能是 23 或 25 小时；不存在的 00:00（极少数时区在午夜切换）按 Go 的规范化结果。Web 传入用户设置 `localization.timezone`（`setting` 功能启用时，见 `contracts/SETTINGS.md`），否则传浏览器 `Intl.DateTimeFormat().resolvedOptions().timeZone`。API 不读取 setting starter，因为 `mailbox` 只依赖 `organization`。响应回显 `today_starts_at`、`today_ends_at`，便于界面和支持排查。
- 设计决定（待确认）D5：视图值取 `pending`（界面文案"待办"），不用工作名里的英文 to-do 单词：仓库的计划检查脚本把该单词当作未完成标记，用作稳定枚举值会长期触发误报。`pending` 在本切片是"我加星、未归档、不在回收站的邮件"，收发两向都算（给自己发出的信加星当作跟进事项）。切片 ③ 将它扩展为"我的星标 ∪ 分配给我的 AI 待办"，接口参数不变。
- "收件时间"即 `mail_messages.created_at`：入站邮件在投递入库时写入，出站邮件在入库排队时写入。设计决定（待确认）D2：排序键为 `(created_at DESC, id DESC)`。现有单邮箱列表按 `id DESC`；两者在单个 API 实例上顺序一致，在多实例时钟偏差内可能交换相邻两封，可接受。

### 5.3 实体

**邮件角色 `MailRole`**（拥有者：邮件角色模块）

| 属性 | 可变 | 说明 |
|---|---|---|
| `id` | 否 | 自增 |
| `organization_id` | 否 | 所属组织 |
| `name` | 是 | 1–64 字符，组织内忽略大小写唯一 |
| `color` | 是 | 固定色板之一，默认 `gray` |
| `description` | 是 | 0–280 字符 |
| `owner_user_id` | 是 | 可空；当前组织成员；仅展示用，不授予权限 |
| `created_at`、`updated_at` | 系统维护 | UTC `timestamptz` |

生命周期：创建 → 存在 → 删除（硬删除，无回收）。关系：一个角色 0..N 个邮箱；一个邮箱 0..1 个角色。邮箱被删除（软删除）时 `role_id` 保留但该邮箱不再出现在任何列表，角色的 `mailbox_ids` 只含未删除邮箱。组织删除时级联删除角色。没有快照字段：列表项上的 `role_id` 读取时取自当前 `mailboxes.role_id`，移动邮箱到别的角色后历史邮件也随之显示新角色（角色是邮箱的当前归属，不是邮件的历史属性）。

**邮箱 `Mailbox`**（现有，新增一个属性）：`role_id`，可空，可变，唯一写入者为角色分配命令和外键 `ON DELETE SET NULL`。

**统一收件项 `InboxItem`**（派生，不持久化）：现有邮件摘要的全部字段，加 `mailbox_address`（与日志项同名同义）、`role_id`（派生自邮箱当前归属）、`snippet`（派生，D10）；线程行另有 `thread`，结构与单邮箱 `group=thread` 完全一致。

**统一收件计数 `InboxCounts`**（派生，实时计算，不缓存）：见 §8.3。

## 6. 持久化设计（3.6）

### 6.1 新表 `mail_roles`

迁移 `2026_11_22_090000_add_mail_roles`（冻结 SQL，`execStatements`，`UseTransaction: true`）：

```sql
CREATE TABLE mail_roles (
  id bigserial PRIMARY KEY,
  organization_id bigint NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  name varchar(64) NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 64),
  color varchar(16) NOT NULL DEFAULT 'gray'
    CHECK (color IN ('gray','red','orange','yellow','green','teal','blue','purple','pink')),
  description varchar(280) NOT NULL DEFAULT '',
  owner_user_id bigint REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL);
CREATE UNIQUE INDEX idx_mail_roles_name ON mail_roles (organization_id, lower(name));
CREATE INDEX idx_mail_roles_owner ON mail_roles (owner_user_id) WHERE owner_user_id IS NOT NULL;
ALTER TABLE mailboxes ADD COLUMN role_id bigint REFERENCES mail_roles(id) ON DELETE SET NULL;
CREATE INDEX idx_mailboxes_role ON mailboxes (role_id) WHERE role_id IS NOT NULL;
```

- 组织隔离：`mailboxes.role_id` 的外键只保证角色存在，不保证同组织；同组织由分配命令的 `WHERE` 条件保证（角色按 `(id, organization_id)` 查找），并有测试覆盖跨组织分配被拒。
- 时间：全部 `timestamptz`，应用写入 UTC。
- 软删除：不需要；删除是用户明确动作，邮箱随即回到未分组，无数据丢失。
- 锁：新建时用组织级咨询锁保护上限；其它写无锁（§4.3）。
- 保留与清理：随组织删除；无定期清理。
- 回滚 `Down`：`ALTER TABLE mailboxes DROP COLUMN IF EXISTS role_id`、`DROP TABLE IF EXISTS mail_roles`（索引随表删除）。回滚丢失角色数据，属于可接受的功能回退。
- 回填：无；现有邮箱全部为未分组。
- 种子数据：无（与环境无关）。`schema.golden.sql` 同步更新。

### 6.2 新索引

迁移 `2026_11_22_090100_add_mail_inbox_index`：

```sql
CREATE INDEX IF NOT EXISTS idx_mail_messages_inbox_time
  ON mail_messages (mailbox_id, direction, created_at DESC, id DESC);
```

- 设计决定（待确认）D11：一个索引服务三种访问：`all`（`inbound` + 过滤 `placement='default'`，按时间倒序取前 N）、`sent`（`outbound`）、`today`（`inbound` + 时间范围）。归档和文件夹占比低（基准数据中约 15%），在索引扫描中过滤即可，不把 `placement` 放进键里，从而让 `today` 能跨位置走范围扫描。
- 现有 `idx_mail_messages_listing (mailbox_id, direction, id DESC)` 保留，单邮箱列表不变。
- 大库可按 `UPGRADING.md` 中 organize 迁移的做法先 `CREATE INDEX CONCURRENTLY IF NOT EXISTS` 预建，迁移里的 `IF NOT EXISTS` 随即跳过，不长时间持有 `mail_messages` 写锁。`UPGRADING.md` 增加对应一段。
- `Down`：`DROP INDEX IF EXISTS idx_mail_messages_inbox_time`。

### 6.3 不新增的持久化

- 不新增 `snippet` 列。设计决定（待确认）D10：摘要在读取页内邮件时用 `substr(text_body, 1, 400)` 取出（PostgreSQL 对 TOAST 值做切片解压，不读完整正文），服务层把连续空白折叠为一个空格、去首尾空白后按 rune 截到 140 个字符。只有 HTML 正文的邮件 `snippet` 为空串。好处是六条入库路径都不用改，也无需回填 20 万行。
- 不新增"收件时间"列，`created_at` 即收件时间（§5.2）。
- 不缓存计数；计数实时计算（§8.4）。

## 7. 状态机（3.7）

### 7.1 邮件角色

| 当前状态 | 命令或触发 | 守卫 | 下一状态 | 参与者 | 副作用 | 失败 |
|---|---|---|---|---|---|---|
| （无） | 新建 | 调用者为所有者或管理员；名称、颜色、描述、负责人合法；名称未占用；组织角色数 < 50 | 存在 | 所有者、管理员 | 请求审计 | `403 PERMISSION.DENIED`、`422 COMMON.VALIDATION_FAILED`、`409 MAILBOX.ROLE_NAME_TAKEN`、`422 MAILBOX.ROLE_LIMIT_REACHED` |
| 存在 | 修改（整体替换） | 调用者为所有者或管理员；字段合法；名称唯一（排除自身） | 存在 | 所有者、管理员 | 请求审计 | `403 PERMISSION.DENIED`、`422 COMMON.VALIDATION_FAILED`、`409 MAILBOX.ROLE_NAME_TAKEN`、`404 MAILBOX.ROLE_NOT_FOUND` |
| 存在 | 删除 | 调用者为所有者或管理员 | （终态：不存在） | 所有者、管理员 | 同一语句内其邮箱 `role_id` 置空；请求审计 | `403`、`404 MAILBOX.ROLE_NOT_FOUND` |
| 存在 | 负责人离开组织 | 负责人是被移除的用户 | 存在（负责人为空） | 系统 | 无审计（系统维护） | 失败记日志，读时兜底 |
| （无）/ 终态 | 修改、删除 | — | 不变 | — | — | `404 MAILBOX.ROLE_NOT_FOUND` |

- 初始状态：创建即存在；终态：删除。没有自动迁移、没有超时。
- 重复命令：重复删除第二次 `404`；以相同内容重复修改返回 `200` 且 `updated_at` 仍更新（与标签一致）。
- 并发：两次修改后写者胜；修改与删除并发，修改要么在删除前提交（随后被删），要么找不到行返回 `404`。
- 副作用时机：审计在事务提交后由请求审计中间件写入（现有机制），不影响业务提交。

### 7.2 邮箱的角色归属

| 当前状态 | 命令或触发 | 守卫 | 下一状态 | 参与者 | 副作用 | 失败 |
|---|---|---|---|---|---|---|
| 未分组 | 分配 `role_id = R` | 调用者对邮箱有 `administer`；R 属于同组织 | 属于 R | 所有者、管理员 | 请求审计 | `404 MAILBOX.NOT_FOUND`（无任何访问）、`403 PERMISSION.DENIED`、`404 MAILBOX.ROLE_NOT_FOUND` |
| 属于 R | 分配 `role_id = R2` | 调用者对邮箱有 `administer`；R2 属于同组织 | 属于 R2 | 所有者、管理员 | 请求审计 | `404 MAILBOX.NOT_FOUND`、`403 PERMISSION.DENIED`、`404 MAILBOX.ROLE_NOT_FOUND` |
| 属于 R | 分配 `role_id = null` | 调用者对邮箱有 `administer` | 未分组 | 所有者、管理员 | 请求审计 | `404 MAILBOX.NOT_FOUND`、`403 PERMISSION.DENIED` |
| 属于 R | 删除角色 R | — | 未分组 | 外键动作 | 无 | 无 |
| 任意 | 分配为当前值 | 调用者对邮箱有 `administer` | 不变 | 所有者、管理员 | 审计记录"无变化" | 无 |

### 7.3 不适用的状态机

- 统一收件不改变任何邮件的状态。邮件的位置、已读、星标、发送状态机全部沿用 organize 与发送队列文档，未改动。

## 8. 接口契约（3.8）

### 8.1 路径总览

设计决定（待确认）D1：组织级跨邮箱读取沿用现有平铺连字符名词路径（`/v1/mail-messages`、`/v1/mail-message-stats`），因此统一收件为 `/v1/inbox-messages` 与 `/v1/inbox-message-counts`，而不是工作名 `/v1/inbox/messages`。

| 操作 | 浏览器路由 | Go API 路由 | 成功 |
|---|---|---|---|
| 统一收件列表 | `GET /api/inbox-messages` | `GET /v1/inbox-messages` | `200` 游标页 |
| 统一收件计数 | `GET /api/inbox-message-counts` | `GET /v1/inbox-message-counts` | `200` 信封 |
| 列出角色 | `GET /api/mail-roles` | `GET /v1/mail-roles` | `200` 信封，至多 50 项 |
| 新建角色 | 同方法 `/api/mail-roles` | `POST /v1/mail-roles` | `201` 信封 |
| 修改角色 | 同方法 `/api/mail-roles/:roleId` | `PUT /v1/mail-roles/:role_id` | `200` 信封 |
| 删除角色 | 同方法 `/api/mail-roles/:roleId` | `DELETE /v1/mail-roles/:role_id` | `204` |
| 设置邮箱角色 | 同方法 `/api/mailboxes/:id/role-assignment` | `PUT /v1/mailboxes/:mailbox_id/role-assignment` | `200` 信封（邮箱） |

全部挂在会话路由组 `auth`（与 `/v1/mail-messages` 同组），需要 `Organization-Id`；BFF 响应 `Cache-Control: private, no-store`，按 `Cookie`、`Organization-Id` 变化（沿用 `privateMailboxResponse`）。`GET /v1/mailboxes` 与邮箱详情每项新增 `role_id`（整数或 `null`），旧客户端忽略未知字段即可。新增错误码：`MAILBOX.ROLE_NOT_FOUND` 404、`MAILBOX.ROLE_NAME_TAKEN` 409、`MAILBOX.ROLE_LIMIT_REACHED` 422（D19，状态码与文件夹、标签的对应码一致）。

### 8.2 GET /v1/inbox-messages — 统一收件列表

- 归属：统一收件读模型。消费者：Web 统一收件、⌘K 邮件搜索。
- 认证：会话。授权与范围：组织成员；结果限于 `readableMailboxIDs`。
- 请求（查询参数，每个至多出现一次）：

| 参数 | 取值 | 默认 | 说明 |
|---|---|---|---|
| `view` | `all`、`today`、`pending`、`sent` | `all` | §5.2 |
| `role_id` | 正整数，或 `none` | 无 | `none` 为未分组（D9）。调用者不可见的角色匹配为空 |
| `mailbox_id` | 正整数 | 无 | 调用者不可读的邮箱匹配为空（D8，同日志） |
| `unread` | `true`、`false` | 无 | 语义同单邮箱：出站邮件恒为已读 |
| `q` | 同单邮箱搜索规则：原文至多 200 字符，去空白后非空，无控制字符 | 无 | 只允许与 `view=all` 组合（D6） |
| `group` | `thread` | 无 | 只允许 `view` 为 `all` 或 `sent`，且不与 `unread`、`q` 组合（D7，与单邮箱规则一致） |
| `tz` | IANA 时区名，至多 64 字符，`time.LoadLocation` 可解析 | `UTC` | 只影响 `today` |
| `cursor` | 上一页 `meta.next_cursor` 原样 | 无（最新一页） | 不透明 |
| `per_page` | 1–100 | 15 | 超出范围截断为边界值（与现有 `per_page` 一致） |

- 校验与限制：未知参数值、重复参数、`page` 参数、格式错误的游标、非法 `tz`、不允许的组合一律 `400 COMMON.INVALID_INPUT`。没有日期范围参数，结果由游标页界定。
- 前置条件：无。
- 搜索语义：`q` 时范围变为"可读邮箱中除回收站外的所有位置、收发两向"（与单邮箱"不带 placement 的 `q`"一致），再与 `role_id`、`mailbox_id`、`unread` 求交；匹配字段、转义、2 秒超时完全复用 `searchCondition`、`searchPattern`、`repository.search`。
- 伪代码：

  ```text
  QUERY ListInbox(actor, filters, cursor, per_page)

  BIND actor FROM authenticated_context            -- 会话 + Organization-Id 中间件已解析成员资格
  VALIDATE filters AGAINST 上表；REQUIRE 无 page 参数 ELSE COMMON.INVALID_INPUT
  REQUIRE q IS ABSENT OR view == all ELSE COMMON.INVALID_INPUT
  REQUIRE group IS ABSENT OR (view IN {all, sent} AND unread IS ABSENT AND q IS ABSENT)
    ELSE COMMON.INVALID_INPUT
  DERIVE position = decode(cursor) 或 +∞            -- 解码失败 COMMON.INVALID_INPUT
  DERIVE location = LoadLocation(tz)               -- 失败 COMMON.INVALID_INPUT

  LOAD access_rows FROM 邮箱访问 USING actor.organization_id, actor.membership_id
  DERIVE readable = { m.id | MailboxAccessFor(m, actor, member_row(m)).View }
  IF mailbox_id PRESENT: readable = readable ∩ {mailbox_id}
  IF role_id == none:    readable = { m ∈ readable | m.role_id IS NULL }
  IF role_id PRESENT:    readable = { m ∈ readable | m.role_id == role_id }
  IF readable 为空: RETURN 200 空页（has_more=false, next_cursor=null）

  DERIVE view_predicate:
    all   → direction=inbound  AND placement=default
    sent  → direction=outbound AND placement=default
    today → direction=inbound  AND placement<>trash AND created_at >= day_start(location) AND created_at < day_end(location)
    pending  → EXISTS star(actor.membership_id, message) AND placement NOT IN (archive, trash)
    q     → placement<>trash AND searchCondition(q)
  DERIVE unread_predicate FROM unread（复用 unreadByMember）

  IF group == thread:
    LOAD heads FROM mail_messages                  -- 见下方 SQL，按邮箱 LATERAL 取各自前 N+1 个线程头再归并
      WHERE mailbox_id IN readable AND view_predicate AND unsealed
        AND (created_at, id) < position
        AND 不存在同邮箱、同视图、同 thread_id 且 (created_at, id) 更大的邮件
      ORDER BY created_at DESC, id DESC LIMIT per_page + 1
    LOAD thread_stats FOR heads.thread_id          -- 复用 ListThreads 的统计：message_count、最新 100 个 message_ids、unread、starred、label_ids，范围限定同一视图
  ELSE IF q PRESENT:
    LOAD rows IN 只读事务(statement_timeout = 2s)  -- 超时 MAILBOX.SEARCH_TOO_BROAD
      WHERE mailbox_id IN readable AND view_predicate AND unread_predicate AND unsealed AND (created_at, id) < position
      ORDER BY created_at DESC, id DESC LIMIT per_page + 1
  ELSE IF view == pending:
    LOAD rows FROM mail_message_stars JOIN mail_messages   -- 从调用者星标出发，星标集小
      WHERE membership_id = actor.membership_id AND mailbox_id IN readable AND ... AND (created_at, id) < position
      ORDER BY created_at DESC, id DESC LIMIT per_page + 1
  ELSE:
    LOAD rows FROM mail_messages                   -- 按邮箱 LATERAL，走 idx_mail_messages_inbox_time
      WHERE ... ORDER BY created_at DESC, id DESC LIMIT per_page + 1

  DERIVE has_more = 行数 > per_page；截去第 N+1 行
  DERIVE next_cursor = has_more ? encode(最后一行.created_at, 最后一行.id) : null
  LOAD snippets FROM mail_messages USING 页内 id    -- substr(text_body, 1, 400)
  LOAD read, starred FOR 页内 id BY actor.membership_id（fillViewerState）
  DERIVE item.mailbox_address, item.role_id FROM 已加载的 access_rows
  RETURN 200 游标页 {data: InboxItem[], meta: {per_page, has_more, next_cursor}}
  ```

  非搜索、非星标路径的核心 SQL（数据库专有写法，故列出）：

  ```sql
  SELECT page.* FROM unnest(@mailbox_ids::bigint[]) AS box(id)
  CROSS JOIN LATERAL (
    SELECT m.id, m.mailbox_id, m.thread_id, m.created_at
    FROM mail_messages m
    WHERE m.mailbox_id = box.id AND m.direction = @direction AND m.placement = 'default'
      AND (m.created_at, m.id) < (@cursor_at, @cursor_id)
      AND NOT EXISTS (SELECT 1 FROM mailboxes seal WHERE seal.id = m.mailbox_id
                      AND seal.sealed_before IS NOT NULL AND m.created_at < seal.sealed_before)
      -- 仅 group=thread：本行必须是线程在视图内的最新一封
      AND NOT EXISTS (SELECT 1 FROM mail_messages newer
                      WHERE newer.mailbox_id = m.mailbox_id AND newer.placement = 'default'
                        AND newer.direction = m.direction AND newer.thread_id = m.thread_id
                        AND (newer.created_at, newer.id) > (m.created_at, m.id))
    ORDER BY m.created_at DESC, m.id DESC
    LIMIT @limit_plus_one) AS page
  ORDER BY page.created_at DESC, page.id DESC
  LIMIT @limit_plus_one;
  ```

  每个邮箱至多取 N+1 行，再在至多 50×(N+1) 行里归并，代价与组织邮件总量无关。线程头的 `NOT EXISTS` 走现有 `idx_mail_messages_threads (mailbox_id, placement, thread_id, id DESC)`，线程通常只有几封。`newer` 无需封存判断：封存邮件都早于封存边界，而 `m` 未封存，比 `m` 新的邮件也未封存。

- 事务边界：只读；仅搜索使用带 `SET LOCAL statement_timeout` 的事务。
- 幂等与并发：天然幂等。翻页期间新到的邮件出现在第一页之前，不会让后续页重复或遗漏；`group=thread` 时，翻页期间收到新回复的线程会跳到游标之上，本轮翻页中不再出现，刷新第一页即可看到。
- 副作用与事件：无。
- 成功响应：

  ```json
  {
    "code": 0,
    "message": "success",
    "data": [
      {
        "id": 9021, "mailbox_id": 12, "mailbox_address": "news@zgi.ai", "role_id": 3,
        "direction": "inbound", "status": "received", "queued_reason": null,
        "from": { "address": "ann@example.com", "name": "Ann Lee" },
        "to": ["news@zgi.ai"], "cc": [],
        "subject": "专业版能不能扩到 20 个成员？",
        "snippet": "Hi， 文件夹功能很好用。想问一下专业版能不能加到 20 个成员？",
        "read": false, "starred": false, "attachment_count": 0, "template_id": null,
        "placement": "default", "folder_id": null, "label_ids": [], "thread_id": 9001,
        "trashed_at": null, "failure_code": "", "last_event": "",
        "created_at": "2026-11-19T06:22:00Z",
        "thread": { "message_count": 3, "message_ids": [9001, 9010, 9021], "unread": true, "starred": false, "label_ids": [] }
      }
    ],
    "meta": { "per_page": 15, "has_more": true, "next_cursor": "eyJmIjoiY3JlYXRlZF9hdCIs..." }
  }
  ```

  字段来源：`mailbox_address`、`role_id` 取自请求时的邮箱当前状态（派生）；`read`、`starred` 为调用者自己的（派生）；`snippet` 派生；其余为邮件持久字段，与单邮箱列表项逐字段同义。`thread` 只在 `group=thread` 时出现。不含正文、`message_id`、`in_reply_to`、事件、附件列表。结果是已提交状态，立即可读。

- 稳定错误：

  | 条件 | HTTP | `error_code` | 可重试 |
  |---|---:|---|---|
  | 参数非法、组合不允许、`page`、坏游标、坏 `tz` | 400 | `COMMON.INVALID_INPUT` | 修正后 |
  | 缺 `Organization-Id` / 格式错 | 400 | `ORGANIZATION.CONTEXT_REQUIRED` / `ORGANIZATION.CONTEXT_INVALID` | 修正后 |
  | 未登录 | 401 | `AUTH.UNAUTHORIZED` | 登录后 |
  | 不是该组织成员 | 404 | `ORGANIZATION.NOT_FOUND` | 否 |
  | 搜索 2 秒内未完成 | 422 | `MAILBOX.SEARCH_TOO_BROAD` | 换更长关键词 |
  | 数据库不可用 | 503 | `COMMON.SERVICE_UNAVAILABLE` | 是 |

  不存在"邮箱不可读"的错误：不可读邮箱与不存在的邮箱一样匹配为空（不泄露存在性）。

- 审计与指标：§13。
- 兼容性：新增路径，无破坏；oasdiff 只应报新增。
- 测试：§14。

### 8.3 GET /v1/inbox-message-counts — 侧栏计数

- 归属：统一收件读模型。消费者：Web 侧栏、额度小组件。
- 认证与范围：同 §8.2。
- 请求：`tz`（规则与 §8.2 相同，默认 `UTC`）。其它参数、重复参数 `400 COMMON.INVALID_INPUT`。
- 查询规格（设计决定（待确认）D12）：

  ```text
  QUERY InboxCounts(actor, tz)

  BIND actor FROM authenticated_context
  VALIDATE tz
  LOAD rows FROM 邮箱访问 ListMailboxAccessRows(actor)   -- 不含未读数
  DERIVE readable = { m ∈ rows | MailboxAccessFor(m, actor, member(m)).view }
  IF readable 为空: RETURN 200 全 0、数组为空（不访问 mail_messages）
  LOAD unread FROM CountUnreadByMailboxIDs(actor.organization_id, actor.membership_id, readable.ids)
    -- 只对可读邮箱分组计数，走 idx_mail_messages_listing；不可读邮箱不进 IN 列表
  DERIVE mailboxes[] = { (m.id, unread[m.id] 缺省 0) | m ∈ readable }
  DERIVE all_unread = Σ mailboxes.unread
  DERIVE roles[] = 按 m.role_id 分组求和（仅 role_id 非空），ungrouped_unread = role_id 为空的和
  LOAD today, today_unread FROM mail_messages
    WHERE mailbox_id IN readable AND direction=inbound AND placement<>trash AND unsealed
      AND created_at >= day_start AND created_at < day_end
    -- COUNT(*) 与 COUNT(*) FILTER (WHERE unreadByMember)，走 idx_mail_messages_inbox_time 范围扫描
  LOAD pending FROM mail_message_stars JOIN mail_messages
    WHERE membership_id = actor.membership_id AND mailbox_id IN readable AND placement NOT IN (archive, trash) AND unsealed
  LOAD sent_waiting FROM mail_messages
    WHERE mailbox_id IN readable AND queued_reason = 'send_limit' AND unsealed   -- 走部分索引 idx_mail_messages_waiting
  RETURN 200 InboxCounts
  ```

  公式、时间与空输入：全部是调用者自己的计数（按 `membership_id`），实时计算，不缓存；`today` 边界同 §5.2；封存邮件与回收站永不计入；无可读邮箱时所有数为 0、数组为空。`mailboxes` 中每项与 `GET /v1/mailboxes` 对同一可读邮箱返回的 `unread_count` 相等（谓词相同，由一致性测试证明），`all_unread = Σ roles.unread + ungrouped_unread`。

- 成功响应：

  ```json
  {
    "code": 0,
    "message": "success",
    "data": {
      "views": { "all_unread": 9, "today": 14, "today_unread": 5, "pending": 3, "sent_waiting": 0 },
      "roles": [{ "role_id": 3, "unread": 4 }, { "role_id": 5, "unread": 3 }],
      "ungrouped_unread": 2,
      "mailboxes": [{ "mailbox_id": 12, "unread": 3 }, { "mailbox_id": 14, "unread": 1 }],
      "today_starts_at": "2026-11-18T16:00:00Z",
      "today_ends_at": "2026-11-19T16:00:00Z"
    }
  }
  ```

  `roles` 只含至少一个可读邮箱的角色，按 `role_id` 升序；`mailboxes` 按 `mailbox_id` 升序，只含可读邮箱。`sent_waiting` 是调用者可读邮箱中因额度排队的邮件数，不是组织用量（见 §12 与 D24）。

- 稳定错误：同 §8.2，去掉 `MAILBOX.SEARCH_TOO_BROAD`。
- 兼容性：新增。

### 8.4 GET /v1/mail-roles — 列出角色

- 归属：邮件角色。消费者：Web 侧栏、角色管理、⌘K。
- 授权与范围（设计决定（待确认）D16）：所有者、管理员看到组织全部角色，`mailbox_ids` 含组织全部未删除邮箱中属于该角色的；成员只看到**至少包含一个他可见邮箱**的角色，`mailbox_ids` 只含他可见的邮箱（"可见"= `ListMailboxAccessRows` 的行中 `MailboxAccessFor(...).Any()` 为真者，与 `GET /v1/mailboxes` 同一集合，但不计算未读数）。
- 查询规格：

  ```text
  QUERY ListMailRoles(actor)
  BIND actor FROM authenticated_context
  LOAD rows FROM 邮箱访问 ListMailboxAccessRows(actor)   -- 含 role_id，不计算未读数
  DERIVE visible = { m ∈ rows | MailboxAccessFor(m, actor, member(m)).any }   -- 管理员为全部邮箱
  LOAD roles FROM mail_roles WHERE organization_id = actor.organization_id ORDER BY lower(name), id LIMIT 50
  DERIVE role.mailbox_ids = { m.id | m ∈ visible AND m.role_id = role.id }，升序
  IF NOT actor.role.CanManageOrganization(): roles = { r | r.mailbox_ids 非空 }
  LOAD current_members FROM 组织成员目录 USING roles.owner_user_id
  DERIVE role.owner_user_id = 仍是成员 ? owner_user_id : null
  RETURN 200 { data: MailRole[] }（有界：至多 50 项）
  ```

- 成功响应项：

  ```json
  { "id": 3, "name": "设计编辑部", "color": "orange", "description": "Behance、Medium 等平台账号",
    "owner_user_id": 7, "mailbox_ids": [14, 15], "created_at": "2026-11-19T10:00:00Z", "updated_at": "2026-11-19T10:00:00Z" }
  ```

- 稳定错误：`400` 组织上下文、`401`、`404 ORGANIZATION.NOT_FOUND`、`503`。

### 8.5 POST /v1/mail-roles — 新建角色

- 归属：邮件角色。消费者：Web 角色管理对话框。
- 认证：会话。授权：`owner` 或 `admin`。
- 请求：`{ "name": "设计编辑部", "color": "orange", "description": "", "owner_user_id": 7 }`；`color` 缺省为 `gray`，`description` 缺省为空串，`owner_user_id` 可为 `null` 或缺省。请求体至多 4 KiB（同整理类请求）。
- 校验：§4.3 不变式 2、4、5、6；字段错误为 `422 COMMON.VALIDATION_FAILED`，`errors` 键为 `name`、`color`、`description`、`owner_user_id`。
- 伪代码：

  ```text
  COMMAND CreateMailRole(actor, request)

  BIND actor FROM authenticated_context
  VALIDATE request 为 JSON 对象，未知字段忽略，体积 ≤ 4 KiB ELSE COMMON.INVALID_INPUT
  REQUIRE actor.role IN {owner, admin} ELSE PERMISSION.DENIED
  DERIVE name = trim(request.name)；REQUIRE 1 ≤ len_runes(name) ≤ 64 ELSE COMMON.VALIDATION_FAILED(name)
  REQUIRE request.color IN 色板 ELSE COMMON.VALIDATION_FAILED(color)
  DERIVE description = trim(request.description)；REQUIRE len_runes ≤ 280 且无控制字符 ELSE COMMON.VALIDATION_FAILED(description)
  IF request.owner_user_id PRESENT:
    LOAD membership FROM 组织成员目录 USING (actor.organization_id, owner_user_id)
    REQUIRE membership 存在 ELSE COMMON.VALIDATION_FAILED(owner_user_id)

  BEGIN TRANSACTION 邮件角色
    pg_advisory_xact_lock(hashtext('mail_roles'), actor.organization_id)
    REQUIRE COUNT(mail_roles WHERE organization_id) < 50 ELSE MAILBOX.ROLE_LIMIT_REACHED
    INSERT mail_roles(organization_id, name, color, description, owner_user_id, created_at=now, updated_at=now)
      ON unique_violation(idx_mail_roles_name) → MAILBOX.ROLE_NAME_TAKEN
  COMMIT

  AFTER COMMIT 请求审计中间件写入 RecordChange{action: create, resource: mail_roles, target: role.id}
  RETURN 201 MailRole（mailbox_ids = []）
  ```

- 事务边界：单一事务，邮件角色模块拥有。幂等：无幂等键，重复提交第二次因重名得到 `409`，客户端据此刷新列表。
- 稳定错误：

  | 条件 | HTTP | `error_code` | 可重试 |
  |---|---:|---|---|
  | JSON 格式错 | 400 | `COMMON.INVALID_INPUT` | 修正后 |
  | 不是所有者或管理员 | 403 | `PERMISSION.DENIED` | 否 |
  | 名称、颜色、描述、负责人不合法 | 422 | `COMMON.VALIDATION_FAILED` | 修正后 |
  | 名称已占用 | 409 | `MAILBOX.ROLE_NAME_TAKEN` | 改名后 |
  | 已有 50 个角色 | 422 | `MAILBOX.ROLE_LIMIT_REACHED` | 删除后 |
  | 数据库不可用 | 503 | `COMMON.SERVICE_UNAVAILABLE` | 是 |

- 审计与指标：请求审计；日志 `mail.role.created`（组织 id、角色 id，不含名称）。
- 兼容性：新增。测试：§14。

### 8.6 PUT /v1/mail-roles/:role_id — 修改角色

- 请求：与新建相同的四个字段，整体替换（缺省字段取缺省值，与标签 `PUT` 替换两字段一致）。
- 伪代码：

  ```text
  COMMAND UpdateMailRole(actor, path.role_id, request)

  BIND actor FROM authenticated_context；BIND role_id 为正整数 ELSE COMMON.INVALID_INPUT
  VALIDATE request 同 CreateMailRole
  REQUIRE actor.role IN {owner, admin} ELSE PERMISSION.DENIED
  LOAD role FROM mail_roles USING (role_id, actor.organization_id) OR ELSE MAILBOX.ROLE_NOT_FOUND
  DERIVE name、color、description、owner_user_id 同新建规则（负责人须为当前成员）

  BEGIN TRANSACTION 邮件角色
    UPDATE mail_roles SET name, color, description, owner_user_id, updated_at = now
      WHERE id = role_id AND organization_id = actor.organization_id
      ON 0 行 → MAILBOX.ROLE_NOT_FOUND
      ON unique_violation → MAILBOX.ROLE_NAME_TAKEN
  COMMIT

  AFTER COMMIT RecordChange{action: update, resource: mail_roles, target: role_id, changes: 改变的字段名}
  RETURN 200 MailRole（按调用者可见性重新计算 mailbox_ids）
  ```

- 并发：后写者胜（D18）。错误表同新建，另加 `404 MAILBOX.ROLE_NOT_FOUND`（角色不存在或属于别的组织）。校验顺序：`400` → `403` → `404` → `422` → `409`，与现有消息路由"先权限再存在再校验"一致。

### 8.7 DELETE /v1/mail-roles/:role_id — 删除角色

- 伪代码：

  ```text
  COMMAND DeleteMailRole(actor, path.role_id)

  BIND actor FROM authenticated_context；BIND role_id ELSE COMMON.INVALID_INPUT
  REQUIRE actor.role IN {owner, admin} ELSE PERMISSION.DENIED

  BEGIN TRANSACTION 邮件角色
    DELETE FROM mail_roles WHERE id = role_id AND organization_id = actor.organization_id
      ON 0 行 → MAILBOX.ROLE_NOT_FOUND
    -- 外键 ON DELETE SET NULL 在同一语句内把 mailboxes.role_id 置空
  COMMIT

  AFTER COMMIT RecordChange{action: delete, resource: mail_roles, target: role_id}
  RETURN 204
  ```

- 错误：`400`、`403 PERMISSION.DENIED`、`404 MAILBOX.ROLE_NOT_FOUND`、`503`。重复删除第二次 `404`。

### 8.8 PUT /v1/mailboxes/:mailbox_id/role-assignment — 设置邮箱角色

设计决定（待确认）D15：沿用 `webhook-setting` 的"邮箱子资源 + PUT 整体设置"形状；需要 `administer`（只有所有者、管理员）；不写邮箱访问历史，只写请求审计。

- 请求：`{ "role_id": 3 }` 或 `{ "role_id": null }`；`role_id` 必须出现。
- 伪代码：

  ```text
  COMMAND AssignMailboxRole(actor, path.mailbox_id, request)

  BIND actor FROM authenticated_context；BIND mailbox_id ELSE COMMON.INVALID_INPUT
  VALIDATE request.role_id 存在且为正整数或 null ELSE COMMON.VALIDATION_FAILED(role_id)
  LOAD mailbox FROM 邮箱访问 authorizeMailbox(actor, mailbox_id, canAdminister)
    -- 无任何访问 MAILBOX.NOT_FOUND；有访问无 administer PERMISSION.DENIED
  IF request.role_id 非空:
    LOAD role FROM mail_roles USING (role_id, actor.organization_id) OR ELSE MAILBOX.ROLE_NOT_FOUND

  BEGIN TRANSACTION 邮件角色
    UPDATE mailboxes SET role_id = request.role_id, updated_at = now
      WHERE id = mailbox_id AND organization_id = actor.organization_id AND deleted_at IS NULL
      ON 0 行 → MAILBOX.NOT_FOUND
      ON foreign_key_violation（角色在加载后被删除）→ MAILBOX.ROLE_NOT_FOUND
  COMMIT

  AFTER COMMIT RecordChange{action: update, resource: mailboxes.role, target: mailbox_id, changes: {role_id: 旧 → 新}}
  RETURN 200 Mailbox（调用者视角，含 access、unread_count、role_id）
  ```

- 幂等：同值重复调用返回 `200`。错误：`400`、`403 PERMISSION.DENIED`、`404 MAILBOX.NOT_FOUND`、`404 MAILBOX.ROLE_NOT_FOUND`、`422 COMMON.VALIDATION_FAILED`、`503`。
- 兼容性：新增子资源；`Mailbox` 新增 `role_id` 字段。

### 8.9 契约文件改动清单

- `contracts/MAILBOXES.md`：路径总表加 7 行；"Mailboxes"响应示例加 `role_id`；新增"Unified Inbox"（视图、参数、组合规则、今日边界、游标、线程、计数）与"Mail Roles"两节；"Access"路由表加两行（统一收件与计数需要对至少一个邮箱的 `view`，结果按邮箱过滤；角色写需要 `owner`/`admin`，分配需要 `administer`）；错误表加 3 个码。
- `contracts/openapi.yaml`：6 个路径、`InboxItem`、`InboxCursorPageResponse`（复用 `CursorPageMeta`）、`InboxCounts`、`MailRole`、`MailRoleInput`、`MailboxRoleAssignment`、`Mailbox.role_id`；`cd contracts && corepack pnpm generate` 同步 `web/` 与 `admin/` 生成类型；`make contract-check` 与 oasdiff 只报新增。
- `CONTEXT.md`：新增 *Mail role* 词条（D13）。
- API 路由目录：`cd api && make route-catalog-check` 更新路由名 `inbox_messages.index`、`inbox_message_counts.index`、`mail_roles.index|store|update|destroy`、`mailboxes.role_assignment.update`。

## 9. 授权矩阵（3.9）

| 能力 | 成员 | 邮箱 `manage` 成员 | 所有者 / 管理员（非邮箱成员） | 所有者 / 管理员（同时是邮箱成员或私人邮箱主人） | 系统 |
|---|---|---|---|---|---|
| 统一收件列表、计数 | 限定范围：自己有 `view` 的邮箱 | 同左 | 限定范围：自己有 `view` 的邮箱（通常为空）；**不因角色扩大** | 限定范围：作为成员或主人可读的邮箱 | 禁止 |
| 在统一收件中归档、回收站、回复 | 限定范围：该邮箱有 `send` | 同左 | 禁止（无 `send`） | 限定范围：该邮箱有 `send` | 禁止 |
| 星标、标已读 | 仅本人状态，需该邮箱 `view` | 同左 | 禁止 | 仅本人状态 | 禁止 |
| 列出角色 | 限定范围：含自己可见邮箱的角色，`mailbox_ids` 只含可见邮箱 | 同左 | 组织内全部 | 组织内全部 | 禁止 |
| 新建、修改、删除角色 | 禁止 | 禁止 | 组织内全部 | 组织内全部 | 禁止 |
| 设置邮箱角色 | 禁止 | 禁止 | 组织内全部邮箱（`administer`） | 同左 | 禁止 |
| 清空离开者的角色负责人 | 禁止 | 禁止 | 禁止 | 禁止 | 组织内，事件触发 |

本矩阵没有"仅审批"类访问。

行级约束与不披露：

- 统一收件对不可读邮箱、不存在的邮箱、不可见的角色、别组织的 id 都返回空结果，不返回 `403`/`404`，因此无法用筛选参数探测邮箱或角色是否存在。这与 `/v1/mail-messages` 的 `mailbox_id` 规则一致。
- 角色列表对成员隐藏不含其可见邮箱的角色，避免通过角色名推断他看不到的业务（例如"高管私人"）。
- 角色负责人不因负责关系获得角色内任何邮箱的访问权；离开组织后不再显示为负责人。
- 管理员不能通过"设置邮箱角色"读到任何邮件；该接口的响应是邮箱元数据，`unread_count` 对无 `view` 的调用者为 0（现有规则）。
- Web 中锁定邮箱、隐藏按钮只是体验；API 每个入口独立检查。

## 10. 跨模块数据流、事件与异步任务（3.10、3.11）

| 流程 | 事务拥有者 | 同步参与者 | 事件 | 事实来源 | 幂等键 | 投递保证与重试 | 顺序 | 重复与乱序 | 失败可见性 | 对账 |
|---|---|---|---|---|---|---|---|---|---|---|
| 统一收件列表 / 计数 | 无（只读） | 邮箱访问、组织上下文中间件 | 无 | `mail_messages`、`mail_message_reads`、`mail_message_stars`、`mailboxes`、`mailbox_members` | 不适用 | 不适用 | 不适用 | 不适用 | 请求日志、`http_request_duration_seconds` | 不需要 |
| 角色写、分配 | 邮件角色 | 邮箱访问（分配时）、组织成员目录（负责人校验）、请求审计 | 无发布 | `mail_roles`、`mailboxes.role_id` | 无（自然键：名称唯一；分配同值幂等） | 同步 | 不适用 | 重名 `409`、并发删除 `404` | 审计记录、错误日志 | 不需要 |
| 成员离开 → 清空负责人 | 邮件角色（在 `MemberRemoved` 内单独一条 `UPDATE`） | 无 | 消费 `EventOrganizationMemberRemoved`（组织 starter 发布，同步、提交后） | 组织成员资格 | `(organization_id, user_id)`；`UPDATE ... SET owner_user_id = NULL WHERE organization_id = ? AND owner_user_id = ?` 天然幂等 | 进程内总线，至多一次；失败记日志不重试 | 无要求 | 重复事件无副作用；"离开后又加入"的乱序不影响：重新加入的用户不会自动恢复负责人，由管理员重新设置 | 结构化错误日志 `mail.role.owner_clear_failed` | 读取时按当前成员过滤，保证展示正确；数据最终由下一次角色修改覆盖 |

派生记录表：

| 触发 | 源记录 | 生成的记录或动作 | 拥有者 | 幂等键 | 失败恢复 |
|---|---|---|---|---|---|
| 删除角色 | `mail_roles` 行 | `mailboxes.role_id` 置空 | PostgreSQL 外键动作（同一语句） | 不适用（同一事务） | 与删除一起失败或一起成功 |
| 成员离开组织 | 组织成员资格 | `mail_roles.owner_user_id` 置空 | 邮件角色 | `(organization_id, user_id)` | 读时过滤兜底；记日志 |
| 列表请求 | 邮件、邮箱、每人状态 | 列表项的 `role_id`、`mailbox_address`、`snippet`、`read`、`starred` | 统一收件读模型（请求内计算，不落库） | 不适用 | 不适用 |

### 10.1 定时与异步任务（3.11）

本切片不新增定时任务、超时、提醒、周期生成或导出：

- 统一收件与计数是请求内实时计算，没有预计算或物化视图，因此没有需要后台维护的派生数据。
- 唯一的异步输入是上表的成员离开事件，由组织 starter 在成员变更提交后同步分发；它不是定时任务，没有租约、重试或积压。
- 回收站 30 天清理、额度排队发送沿用 organize 与发送队列已有的 `mail:broadcast:work` worker，本切片不改动它们的选择条件、批量和失败可见性。

## 11. 浏览器面（3.12）

### 11.0 视觉基准与布局数值

视觉基准是原型 `docs/design/v2/MainV2.dc.html`（画布 1440×900）。S4 的实现与截图回归 UI-11 都以它为准；下列数值抄自该文件，使本节无需打开原型即可实施。本切片不渲染的原型部分（AI 标签页与说明行、行内原因行、AI 草稿区、验证码锁定面板、"稍后 H"、来源徽标内容、额度分项、命令面板的"新建注册地址""问 AI"）见 §1.4、§11.5–§11.9。

整体：根容器 `flex`，背景 `--bg-canvas`，正文 Inter 13px、行高 1.5、`-webkit-font-smoothing: antialiased`。衬线类（标题、主题）`Newsreader, 'Noto Serif SC', Georgia, serif`，字距 `-0.01em`。

| 区域 | 尺寸与形状 | 内边距与间距 | 其它 |
|---|---|---|---|
| 侧栏 `aside` | 宽 248px，不伸缩；无背景（透出画布） | `18px 12px 14px 12px`，子项纵向间距 2px | — |
| 侧栏抬头 | 22px 圆角 6px 的标志 + 组织名（600，字距 -0.01em）+ 右侧 `⌘K` 键帽 | `0 8px 18px 8px`，间距 10px | — |
| 写邮件按钮 | 高 36px，圆角 9px，1px `--border-main`，背景 `--bg-surface`，阴影 `0 1px 0 rgba(22,21,15,.04)` | `0 12px`，外边距 `0 4px 14px 4px`，图标 15px，间距 8px | 右侧 `C` 键帽 |
| 视图 / 角色导航行 | 高 32px，圆角 8px，宽 100% | `0 12px`，间距 10px | 默认文字 `--text-subtle`；悬停底 `#F2F0EB`；选中底 `#EFEDE7`、600、`--text-main`；右侧数字 12px，`all` 用 `--brand-main` 600，其余 `--text-faint` |
| 分组标题"角色与邮箱" | 11px，字距 0.06em，`--text-faint` | `18px 12px 6px 12px` | — |
| 角色色点 | 7×7px，圆角 2px | 在名称前，间距 10px | 颜色取色板 |
| 角色下的邮箱行 | 高 30px，左内边距 29px，12.5px | 其它同导航行 | 地址单行省略；右侧来源徽标位：10.5px、`0 6px`、1px `--border-main`、圆角 999px、`--text-muted`（本切片留空不显示） |
| 额度卡片 | 圆角 10px，1px `--border-main`，背景 `--bg-surface`，12px、行高 1.6、`--text-muted` | `10px 12px`，外边距 `0 4px`；位于侧栏底部（其上方为弹性空白） | 首行两端对齐、`--text-subtle`；原型的 3px 进度条本切片不画（D24） |
| 侧栏底部导航 | 文字链接，`--text-muted` | `10px 8px 0 8px`，项间距 10px | 邮件、群发、联系人、管理 |
| 列表面板 `section` | 宽 470px，不伸缩；背景 `--bg-surface`；左右 1px `--border-main`；圆角 `14px 0 0 14px`；阴影 `0 1px 2px rgba(22,21,15,.04)` | 上下外边距 10px | — |
| 列表标题区 | 标题衬线 28px 600；副标题 `--text-muted`，与标题基线对齐，间距 10px | `22px 24px 10px 24px` | — |
| 标签栏 | 下边 1px `--border-subtle`，项间距 18px，距标题 14px | 每项 `0 0 10px 0`，`margin-bottom: -1px` | 选中：下边 2px `--text-main`、600；未选：2px 透明、`--text-muted`；数字 `--text-faint` |
| 列表滚动区 | — | `0 12px 12px 12px` | — |
| 列表行 | 圆角 10px | `12px 12px` | 悬停底 `#F7F5F0`，选中底 `#F3F1EC`；悬停时显示 `E`、`#` 键帽（`opacity` 过渡 .12s） |
| 行第一行 | 未读圆点 6×6px（未读 `--brand-main`，已读透明占位）、发件人 13.5px（未读 600，已读 `--text-subtle`）、角色标签、弹性空白、悬停键帽、时间 12px `--text-faint` 等宽数字 | 间距 8px | 角色标签 11px、`0 7px`、圆角 999px、底 `#F3F1EC`、`--text-muted`；收窄时隐藏 |
| 行第二、三行 | 主题距上 2px，单行省略（未读 600 `--text-main`，已读 `--text-subtle`）；摘要单行省略、`--text-muted` | — | — |
| 阅读栏 `main` | 自适应，`min-width: 0`；背景 `--bg-surface`；上、右、下 1px `--border-main`；圆角 `0 14px 14px 0` | 外边距 `10px 10px 10px 0` | 与列表面板拼成一张卡片 |
| 阅读栏工具栏 | 高 52px，下边 1px `--border-subtle`，12px、`--text-muted` | `0 28px`，间距 14px | 左组回复/全部回复/转发，弹性空白，右组归档/删除，均带键帽 |
| 阅读正文区 | 滚动区 `34px 0`；`article` 最大宽 640px 居中 | `0 28px` | "发到"行 12px `--text-muted`；主题衬线 30px 600、行高 1.25、外边距 `8px 0 18px` |
| 发件人行 | 头像 34px 圆形、底 `#F1EEE8`、首字 600 `--text-subtle` | 间距 12px，距正文 16px | 姓名 600；地址与时间 12px |
| 邮件正文 | 15.5px，行高 1.8，`--text-subtle` | — | 沙箱渲染沿用现有组件 |
| 命令面板 | 宽 600px，距顶 140px 水平居中；圆角 14px；1px `--border-main`；阴影 `0 30px 80px rgba(22,21,15,.22)`；遮罩 `rgba(22,21,15,.18)` + `blur(2px)` | 输入区 `14px 18px`、15px、下边 1px `--border-subtle`；结果区 `8px` | 组标题 11px 字距 0.06em `--text-faint`、`6px 10px`；结果项 `9px 10px`、圆角 8px，高亮底 `--brand-subtle`，右侧 `↵` 键帽 |
| 键帽 `kbd` | 最小宽 18px、高 18px，1px `--border-main`、下边 2px，圆角 5px，10.5px，`--text-muted`，底 `--bg-surface` | `0 5px` | — |

圆角汇总：面板与命令面板 14px、空状态与引用块 12px、额度卡片与列表行 10px、按钮 9px、导航行与命令结果 8px、标志 6px、键帽 5px、色点 2px、标签与徽标 999px。原型中出现的固定色（`#F2F0EB`、`#EFEDE7`、`#F1EEE8`）在 §11.10 映射为语义令牌，组件不直接写色值。

### 11.1 路由与 URL 状态

设计决定（待确认）D20：

| URL | 内容 |
|---|---|
| `/console/mailboxes` | 统一收件，`view=all` |
| `/console/mailboxes?view=today\|pending\|sent` | 对应视图 |
| `/console/mailboxes?role=3` / `?role=none` / `?box=12` | 统一收件收窄到角色、未分组、单个邮箱（可与 `view` 组合） |
| `/console/mailboxes?q=...` | 搜索（等价 `view=all&q=`） |
| `/console/mailboxes?mailbox=12` | **原单邮箱工作区，原样保留**（文件夹、标签、归档、回收站、批量操作、权限面板） |
| `/console/emails` | 组织日志，不变 |

- `?mailbox=` 优先于其它参数。`localStorage` 中记住的邮箱不再在无参数时自动跳转（否则永远到不了统一收件），只用于写信时预选发件邮箱。
- 收窄到 `box=12` 时，标题旁提供"在邮箱中打开"链接到 `?mailbox=12`，从而到达文件夹、标签、回收站。
- 列表的标签页与选中的邮件 id 用 `?tab=unread`、`?open=9021` 保存，刷新后保持；非法值回退默认，不报错。

### 11.2 组件与数据

在 `web/src/features/mailbox` 新增：

| 文件 | 内容 |
|---|---|
| `components/inbox-workspace.tsx` | 三栏布局：侧栏 248px、列表 470px、阅读栏自适应（数值见 §11.0） |
| `components/inbox-sidebar.tsx` | 写邮件按钮、四个视图、"角色与邮箱"分组、额度小组件、底部导航 |
| `components/inbox-list.tsx` | 标签栏、行（未读圆点、发件人、角色标签、悬停快捷键提示、时间、主题、摘要）、无限滚动 |
| `components/inbox-reading-pane.tsx` | 阅读栏：最大宽度 640px 的 `article`；复用 `thread-view.tsx`、`email-detail.tsx`、`mail-shared.tsx` 的沙箱正文 |
| `components/manage-roles-dialog.tsx` | 角色增删改与邮箱勾选（只对所有者、管理员渲染） |
| `components/command-palette.tsx` | ⌘K |
| `components/shortcut-help-dialog.tsx` | `?` 帮助 |
| `components/send-quota-card.tsx` | 额度小组件 |
| `hooks/use-inbox.ts` | `useInboxMessages`（`useInfiniteQuery`，游标）、`useInboxCounts`、`useMailRoles`、角色写 mutation |
| `hooks/use-mail-shortcuts.ts` | 键盘绑定 |
| `services/mailbox-service.ts` | 新增 DTO 与解析（在网络边界校验 JSON） |
| `server/mailbox-route.ts` | 新 BFF 路由函数，白名单转发 |
| `server/mock-mailbox-store.ts` | 统一收件、计数、角色的开发 mock，复用同一套访问规则 |

`src/app/api/` 新增 `inbox-messages/route.ts`、`inbox-message-counts/route.ts`、`mail-roles/route.ts`、`mail-roles/[roleId]/route.ts`、`mailboxes/[mailboxId]/role-assignment/route.ts`，与现有路由一样只调用 `mailbox-route.ts` 的函数并包 `privateMailboxResponse`。

查询键与失效：

- `mailboxKeys.inbox(orgId, filters)`、`mailboxKeys.inboxCounts(orgId, tz)`、`mailboxKeys.roles(orgId)`。
- 列表与计数使用现有 `liveRefresh`（`MAIL_REFRESH_MS`，后台标签页不刷新）。
- 单邮箱写动作（已读、星标、位置、发送）成功后，除现有失效外再失效 `inbox` 与 `inboxCounts`；角色写成功后失效 `roles`、`mailboxes`、`inboxCounts`。
- 星标与已读做乐观更新（在缓存中改该行），失败回滚并提示；归档、回收站不做乐观移除，成功后再刷新（避免与线程 `message_ids` 截断规则冲突）。

### 11.3 状态

| 状态 | 表现 |
|---|---|
| 加载 | 侧栏、列表、阅读栏骨架，尺寸与成品一致 |
| 空：无可读邮箱 | "你还没有可阅读的邮箱"，管理员另显示"管理邮箱"按钮 |
| 空：视图无内容 | 按视图文案："今天还没有新邮件"、"没有加星的待办"等 |
| 成功 | 列表与阅读栏 |
| 校验失败 | `400` 时清理 URL 参数回到 `view=all`；角色表单 `422` 在字段下显示 |
| 冲突 | `409 MAILBOX.ROLE_NAME_TAKEN` 在名称输入框下显示 |
| 禁止 | `403` 提示"无权限"，不暴露原因细节；锁定邮箱显示锁图标、无数字、不可点 |
| 不可用 | `503` 显示重试 |
| 搜索过宽 | `MAILBOX.SEARCH_TOO_BROAD` 提示"请输入更长的关键词" |

错误一律按 `error_code` 映射到 `utils/mailbox-error.ts` 的本地化键，不显示后端消息。

### 11.4 侧栏

- 视图：全部收件（数字为 `all_unread`，强调色）、今日新到（`today`，弱色）、待办（`pending`）、已发送（无数字；`sent_waiting > 0` 时显示"排队中"小标）。
- "角色与邮箱"分组：按 `GET /v1/mail-roles` 顺序列角色（色点、名称、未读数），点击角色既收窄列表也展开其邮箱；展开后列出该角色的邮箱地址，可读邮箱显示未读数、可点击收窄；不可读邮箱（管理员常见）显示锁图标、无数字、不可点（满足权限 PRD §4"无查看权的邮箱显示为锁定状态"）。最后一组是"未分组"。成员看不到不含其可见邮箱的角色。
- 所有者、管理员在分组标题右侧看到"管理角色"。
- 邮箱行右侧预留来源徽标位（切片 ④）。

### 11.5 列表

- 标题区：未收窄时标题"收件"（衬线体），副标题"N 个邮箱 · M 个角色"（客户端计数可读邮箱和可见角色）；收窄时标题为角色名或邮箱地址。
- 标签栏降级（设计决定（待确认）D25）：本切片为"全部 / 未读"两项，数字取计数接口；"未读"即 `unread=true`（不按线程聚合，符合 organize 规则）。原型 `docs/design/v2/MainV2.dc.html` 列表中的 AI 说明行（"AI 按……排好了"）和行内蓝色"原因"行不渲染。
- 行：未收窄时显示角色标签（角色名，色板色），收窄到角色或邮箱时隐藏；`role_id` 为空不显示标签。线程行在发件人后显示"（3）"。时间今天显示 `HH:mm`，今年显示"11 月 18 日"，更早显示年份；按 D3 的时区格式化。
- `today`、`pending`、`未读` 与搜索按单封显示。
- 无限滚动：接近底部时取下一页；`has_more=false` 停止。

### 11.6 阅读栏

- 正文列最大宽度 640px 居中；主题用衬线体 30px；"发到 news@zgi.ai · Zmail 官方"一行取自行的 `mailbox_address` 和角色名。
- 线程行打开时显示线程（复用 `GET /v1/mailboxes/:mailbox_id/threads/:thread_id`）；单封打开显示详情。打开即对未读入站邮件调用现有单封更新接口置 `read: true`（现有行为）。
- 工具栏：回复 R、全部回复 A、转发 F、归档 E、删除 #；无 `send` 时这几个按钮不渲染。原型 `docs/design/v2/MainV2.dc.html` 中的"稍后 H"、AI 草稿区、验证码锁定面板不渲染（§1.4）。

### 11.7 快捷键

| 键 | 动作 | 条件 |
|---|---|---|
| `j` / `k` | 下一封 / 上一封（移动选中并在阅读栏打开） | 列表非空 |
| `Enter` | 打开当前选中（焦点在列表时） | — |
| `e` | 归档当前行（线程行作用于 `thread.message_ids`） | 该邮箱 `send` |
| `#` | 移入回收站 | 该邮箱 `send` |
| `s` | 切换我的星标 | 该邮箱 `view` |
| `r` / `a` / `f` | 回复 / 全部回复 / 转发 | 该邮箱 `send` |
| `c` | 写邮件 | 至少一个有 `send` 的邮箱 |
| `/` | 聚焦搜索框 | — |
| `?` | 快捷键帮助 | — |
| `⌘K` / `Ctrl+K` | 命令面板 | — |
| `Esc` | 关闭面板或对话框；搜索框中清空 | — |

设计决定（待确认）D29：焦点在 `input`、`textarea`、`select`、`contenteditable` 内，输入法组合中（`event.isComposing`），或有模态对话框打开时，单键快捷键不触发（`Esc`、`⌘K` 除外）；带修饰键的组合不拦截浏览器默认行为（`⌘K` 例外）。条件不满足的动作不发请求，用 toast 提示"你在这个邮箱没有发送权限"。

设计决定（待确认）D26：在统一收件中按 `c` 写信时，写信对话框新增"发件邮箱"选择，列出有 `send` 的邮箱；默认值依次为当前收窄的邮箱、上次使用的邮箱（现有 `localStorage` 记忆）、列表第一个。回复、全部回复、转发的发件邮箱固定为原邮件所在邮箱（沿用 `replyAllDraft`、`forwardDraft`）。

### 11.8 ⌘K 命令面板

设计决定（待确认）D23：用现有 `components/ui/dialog.tsx` 加 combobox 列表实现（`role="combobox"` 输入框、`role="listbox"` 结果、`aria-activedescendant`），不引入 `cmdk` 新依赖，免去一次供应链评审。

结果分四组，每组至多 5 条：

1. 邮箱：可见邮箱地址的客户端前缀与子串匹配；可读的跳转到 `?box=`，不可读的显示锁定且不可选。
2. 角色：可见角色名匹配，跳转 `?role=`。
3. 邮件：输入停顿 250 ms 后调用 `GET /api/inbox-messages?q=...&per_page=5`；遇到 `MAILBOX.SEARCH_TOO_BROAD` 在该组显示"请输入更长的关键词"；选中后打开该邮件。
4. 操作：写邮件、四个视图、切换浅色/深色、快捷键帮助；所有者、管理员另有"管理邮箱""管理角色"。原型 `docs/design/v2/MainV2.dc.html` 命令面板中的"为 Medium 新建一个注册地址""问 AI"不在本切片。

### 11.9 外发额度小组件

现有数据（逐项核对）：

- `daily_send_limit`：`GET /v1/mailbox-domains` 每项都带，所有成员可见（滚动 24 小时、按组织、失败也计入）。
- `GET /v1/mail-message-stats?days=1`：**UTC 自然日**内、**仅调用者可读邮箱**的 `sent`、`failed`、`received`。
- `sent_waiting`：本设计计数接口新增，调用者可读邮箱中因额度排队的邮件数。

缺少的数据：组织级滚动 24 小时已用尝试次数（发送限额真正的分子，目前只在 `service.go` 内部计算，不对外）；直发与群发的拆分（群发邮件可经 `mail_broadcast_recipients.message_id` 区分，但没有接口）；"留给回信"的预留额度（不存在这一策略）。而且权限 PRD §3.7 规定组织用量的具体数字只给管理员看。

因此设计决定（待确认）D24：本切片不画进度条、不显示分项，不新增用量接口：

- 所有人：卡片标题"今日外发"，内容"你可见的邮箱：已发 N · 失败 M（UTC 日）"，数据取自 `stats days=1`。这是他本来就能在日志页看到的数字，不是组织用量。
- 所有者、管理员：另一行"组织上限 200 次 / 滚动 24 小时"。
- `sent_waiting > 0` 时：所有人看到"额度已满，排队中"，点击跳到 `view=sent`。不显示排队数量，符合"只提示额度已满、排队中"。
- 还原原型 `docs/design/v2/MainV2.dc.html` 中的 "108 / 200 · 日常 18 · 群发 90 · 留给回信 40" 需要的东西列在 §18 问题 1。

### 11.10 v2 主题（全站）

设计决定（待确认）D22：MainV2 的令牌成为 `web/` 全局主题，改 `src/themes/primitives.css`、`light.css`、`dark.css` 的映射，组件继续只用语义令牌（`web/AGENTS.md`"不硬编码平行设计系统"）。

| 语义 | 浅色 | 深色 |
|---|---|---|
| `--bg-canvas`（`--background`） | `#FBFAF7` | `#12110E` |
| `--bg-surface`（`--card`、`--popover`） | `#FFFFFF` | `#1A1915` |
| `--text-main`（`--foreground`、`--primary`） | `#16150F` | `#EDEBE5` |
| `--text-subtle` | `#3A3833` | `#C9C6BE` |
| `--text-muted`（`--muted-foreground`） | `#77736A` | `#9A968C` |
| `--text-faint`（新增） | `#A8A49B` | `#6F6B62` |
| `--border-main`（`--border`、`--input`） | `#ECE9E3` | `#2A2823` |
| `--border-subtle` | `#F3F1EC` | `#211F1B` |
| `--brand-main`（`--ring`、强调） | `#2F4BD8` | `#7D90F2` |
| `--brand-subtle`（`--accent` 选中底） | `#EEF1FD` | `#1E2443` |
| `--warning-main` | `#B4541F` | `#E08A57` |
| 行悬停 / 选中（`--row-hover` / `--row-selected`，新增） | `#F7F5F0` / `#F3F1EC` | `#1F1E1A` / `#25231F` |
| 导航悬停 / 选中（`--nav-hover` / `--nav-selected`，新增） | `#F2F0EB` / `#EFEDE7` | `#1F1E1A` / `#2A2823` |
| 头像底（`--avatar-bg`，新增） | `#F1EEE8` | `#25231F` |

- 发丝线：边框统一 1px `--border-main`，分隔线 `--border-subtle`；圆角数值见 §11.0。
- 深色值为本文选定，实施时用对比度脚本验证正文 ≥ 4.5:1、弱文字 ≥ 3:1，不达标只调亮度。
- 字体（设计决定（待确认）D21）：`next/font/google` 在构建时下载并自托管 `Inter`（400/500/600）与 `Newsreader`（500/600，`opsz`），运行时不向 Google 发请求；中文衬线 `Noto Serif SC` 600 用 `preload: false`，回退 `'Songti SC', 'STSong', serif`。`--font-sans` = Inter + `PingFang SC`、`Noto Sans SC`、系统字体；新增 `--font-serif`，用于页面标题、邮件主题、列表标题。
- 现有 `next-themes` 切换保持；`.dark` 类沿用。
- 影响面：登录、官网、控制台其它页面同步换肤；它们只用语义令牌，预计无需改组件，但需要逐页截图回归（§14）。是否接受全站一次性换肤见 §18 问题 4。

### 11.11 响应式与无障碍

- ≥ 1280px 三栏；1024–1279px 侧栏收为图标栏；< 1024px 列表与阅读栏切换显示（打开邮件时显示阅读栏和返回按钮）；< 768px 侧栏进抽屉。不做移动端专门设计（移动端原型不在本切片，仓库中也未收录）。
- 列表 `role="listbox"`，行 `role="option"`、`aria-selected`；`j`/`k` 与方向键同时可用；焦点环用 `--ring`。
- 未读用圆点加粗体两种方式表达，不只靠颜色；角色标签有文字。
- 快捷键帮助可由 `?` 和侧栏底部"快捷键"链接打开；`prefers-reduced-motion` 下关闭过渡。

### 11.12 本地化

新增 `web/src/i18n/modules/mailbox/{en-US,zh-Hans}.ts` 键：`inbox.views.*`、`inbox.tabs.*`、`inbox.empty.*`、`inbox.subtitle`、`inbox.openInMailbox`、`roles.*`（表单、色板名、删除确认"删除后其邮箱回到未分组"）、`shortcuts.*`、`palette.*`、`quota.*`、`errors.roleNotFound|roleNameTaken|roleLimitReached`。两种语言结构一致。

### 11.13 开发 mock 与生产替换边界

- `mock-mailbox-store.ts` 实现统一收件（视图、筛选、游标、线程、计数）与角色，复用 mock 内已有的访问判断，使 mock 与 API 在权限上同构。
- mock 路由按 `web/AGENTS.md`：`guardMockBffRoute()` 在前，写路由再 `guardSameOriginMutation(request)`；成功用 `apiSuccessResponse()`/游标页响应；失败用共享信封与稳定 `error_code`；生产禁用时 `503 COMMON.SERVICE_UNAVAILABLE`。
- 生产走 `forwardAuthenticatedGoApi`，只转发白名单参数（同 `listMailLogRoute` 的写法），不转发 `page`。

## 12. 安全、隐私与审计（3.13）

- 敏感数据：邮件主题、发件人、收件人、摘要（正文前 140 字符）、角色名（可能暴露业务结构）、角色负责人。
- 读取规则：§9。统一收件、计数、命令面板搜索全部经过 `readableMailboxIDs`；摘要与详情同等敏感，只给有 `view` 的人。
- 列表与日志暴露：列表不含正文全文、`Message-ID`、附件名。服务端日志不记 `q`、主题、地址、摘要，只记数量与耗时。角色名不进日志。
- 事件与通知：本切片不发布事件，不产生通知，不推送 webhook。
- 加密与密钥：无新增。
- 上传：无。
- 审计：角色新建、修改、删除和邮箱角色分配通过 `auditstarter.RecordChange` 写入现有请求审计（resource `mail_roles` / `mailboxes.role`，带目标 id 与变化的字段名，不带名称原文）；不写邮箱访问历史，因为它们不改变任何人的访问（D15）。读请求不审计，与权限 PRD"不对每次打开信件单独记日志"一致。
- 保留与删除：角色随组织删除；无新增个人数据保留期。
- 滥用与枚举：筛选参数无法探测邮箱或角色（§9）；`per_page` 上限 100；搜索 2 秒超时；角色上限 50；请求体上限 4 KiB。沿用全局限流中间件，不新增专门限流。
- 浏览器网关：写路由经 BFF 同源检查与会话凭据（HttpOnly）；响应 `private, no-store`，按 `Cookie`、`Organization-Id` 变化；阅读栏正文继续用 `default-src 'none'` 的沙箱 iframe。
- 字体自托管，避免每次页面加载向第三方泄露访问（D21）。

## 13. 可观测与运维（3.14）

### 13.1 日志

| 事件 | 字段（隐私安全） |
|---|---|
| `mail.inbox.list` | `request_id`、`organization_id`、`membership_id`、`view`、`grouped`、`has_query`（布尔）、`readable_mailboxes`、`result_count`、`duration_ms` |
| `mail.inbox.search_too_broad` | `request_id`、`organization_id`、`query_length`、`readable_mailboxes` |
| `mail.role.created` / `updated` / `deleted` / `assigned` | `request_id`、`organization_id`、`actor_user_id`、`role_id`、`mailbox_id` |
| `mail.role.owner_clear_failed` | `organization_id`、`user_id`、`error` |

### 13.2 指标与告警

- 现有 `http_request_duration_seconds`、`http_requests_total` 按路由名区分 `inbox_messages.index`、`inbox_message_counts.index`；`db_query_duration_seconds` 覆盖查询。
- 告警：`inbox_messages.index` p95 > 500 ms 持续 15 分钟；`inbox_message_counts.index` p95 > 500 ms 持续 15 分钟；`MAILBOX.SEARCH_TOO_BROAD` 占搜索请求 > 20% 持续 1 小时（提示索引失效或数据倾斜）。
- 追踪：沿用现有 HTTP 与数据库追踪边界，`request_id` 贯穿 BFF 与 API。

### 13.3 运维与回退

- 支持排查：用户报"某封信在统一收件里看不到"时，凭 `request_id` 查 `readable_mailboxes` 数量，再对照该邮箱 `access`；计数响应回显 `today_starts_at` 用于时区问题。
- 安全降级：统一收件接口失败时，Web 显示错误并提供"按邮箱查看"链接（`?mailbox=`，走现有接口）。
- 回退：API 新增路径与列，回退代码即可，旧 Web 不调用它们；Web 回退到上一版本即恢复单邮箱默认页；迁移 `Down` 可删除角色表、列与索引（会丢失角色数据）。不新增功能开关（D27，见 §15）。

## 14. 测试与验证（3.15）

### 14.1 不变式到证明

| 关注点 | 证明 | 位置 |
|---|---|---|
| 今日边界（UTC、`Asia/Shanghai`、`America/New_York` 夏令时切换日 23/25 小时） | 单元测试 | `inbox_service_test.go` |
| 视图与参数组合校验、游标编解码、坏游标 | 单元测试 | `inbox_service_test.go` |
| 摘要折叠与 140 rune 截断（中英混排、全空白、纯 HTML） | 单元测试 | `inbox_service_test.go` |
| 可读集求交：私人邮箱仅主人、共享需 view、管理员非成员为空、待交接无人；计数只以可读 id 调用 `CountUnreadByMailboxIDs`、空集不调用 | 服务测试（替身仓储） | `inbox_test.go` |
| `CountUnreadByMailboxIDs`：只计传入邮箱、别组织 id 不计、谓词与 `ListMailboxes` 分组计数一致 | PostgreSQL 测试 | `inbox_repository_test.go` |
| 角色可见性与负责人过滤 | 服务测试 | `role_test.go` |
| 视图谓词、封存排除、回收站排除、线程头 `NOT EXISTS`、LATERAL 归并顺序、`(created_at,id)` 游标无重复无遗漏 | PostgreSQL 测试 | `inbox_repository_test.go` |
| 角色唯一索引、色板 CHECK、外键 `SET NULL`、跨组织分配被拒、50 上限的并发新建 | PostgreSQL 测试（两个并发事务） | `role_repository_test.go` |
| 参数 `400`、写角色 `403`、`404` 顺序 | Handler 测试 | `handler_test.go` |
| 迁移：全新安装、从 `2026_11_21_090100` 升级、`Down` 后再 `Up`、`schema.golden.sql` | 迁移测试 | `database/migrations/mail_roles_test.go` |
| 契约：OpenAPI 与生成类型、BFF 白名单、mock 与契约同构 | 契约测试、Vitest | `make contract-check`；`web/src/test/mailbox-route.test.ts`、`mailbox-service.test.ts`、`mock-bff-route-contract.test.ts` 扩展 |
| 浏览器：视图切换、收窄、无限滚动、快捷键（含输入框内不触发、IME）、⌘K、角色管理、锁定邮箱 | 组件与路由测试 | `web/src/test/inbox-ui.test.tsx` |
| 性能 | 基准（PostgreSQL） | `inbox_bench_test.go` |

### 14.2 关键场景

1. 正常路径：四个视图各返回正确集合；`group=thread` 每线程一行且 `message_ids` 与单邮箱视图一致；计数满足 `all_unread = Σ roles + ungrouped`。
2. 越权（权限 PRD 验收组织）：C 的统一收件、计数、搜索、⌘K 邮件结果中 `b@` 的主题出现次数为 0；A（非成员）全部为空；`mailbox_id=<b@>`、`role_id=<仅含 b@ 的角色>` 返回空而非 `403`/`404`；交接后封存邮件不出现在 `today` 与计数中。
3. 跨邮箱线程：两个邮箱中互相 `In-Reply-To` 的邮件不合并。
4. 重复与并发：同时新建第 50、51 个角色恰好一个成功；分配与删除角色并发得到 `404 MAILBOX.ROLE_NOT_FOUND` 或未分组，不出现悬空引用；重复删除 `404`；重复分配同值 `200`。
5. 边界与时钟：`tz` 跨日（UTC 23:30 时上海已是次日）；`created_at` 恰为今日起点计入、恰为次日起点不计入；`per_page` 0 与 101 被截断；`q` 201 字符 `400`。
6. 部分依赖失败：成员离开事件处理失败后，角色列表不显示该负责人。
7. 分页与排序：插入同一 `created_at` 的多封，游标翻页按 `id` 打破平局；翻页期间插入新信不造成重复。
8. 迁移兼容与回退：升级后现有邮箱 `role_id` 为空，单邮箱列表与 `GET /v1/mailboxes` 行为不变；`Down` 成功。

### 14.3 性能目标

设计决定（待确认）D28，沿用 `listing_bench_test.go` 的做法（真实迁移建表、每个测试二进制种一次数据）：

- 数据：一个组织 11 个邮箱。10 个可读邮箱合计 20 万封（规模不均：一个 10 万、其余各约 1.1 万），三封一线程，70% 入站；5% 回收站、5% 文件夹、10% 归档；调用者已读 80%；今日到达 2,000 封；星标 300 封。另 1 个不可读邮箱 5 万封，用于证明不可读数据不拖慢查询。
- 目标：`view=all&group=thread` 首页与第 20 页 p95 < 200 ms；`today`、`pending`、`sent` 首页 p95 < 200 ms；`unread=true` 首页 p95 < 300 ms；计数 p95 < 300 ms（未读计数只对 10 个可读邮箱经 `CountUnreadByMailboxIDs` 计算；把不可读邮箱从 5 万封增到 50 万封，计数 p95 变化不超过 10%）；3 字符以上搜索 p95 < 500 ms。
- 不达标时的处理顺序：先看执行计划确认使用 `idx_mail_messages_inbox_time`；`unread=true` 若超标，改为按邮箱 LATERAL 内 `LIMIT` 加读表反连接的写法并复测；不引入缓存。

### 14.4 验证命令（按 `AGENTS.md` 验证矩阵）

```bash
cd api && go test ./internal/modules/mailbox/... -run 'Inbox|MailRole'
cd api && go test ./database/migrations/... -run 'MailRoles|SchemaGolden'
cd api && go test ./internal/modules/mailbox/... -run '^$' -bench 'Inbox' -benchtime 30x
cd api && bash ../.agents/skills/verification-before-completion/scripts/run-tiers.sh 1 ./internal/modules/mailbox/...
cd api && make route-catalog-check
make contract-check
cd web && corepack pnpm vitest run src/test/inbox-ui.test.tsx src/test/mailbox-route.test.ts src/test/mailbox-service.test.ts src/test/mock-bff-route-contract.test.ts
cd web && corepack pnpm type-check && corepack pnpm lint
```

验收流程新增 `api/tests/kest/unified_inbox.flow.md`：在本地 API 与一次性 PostgreSQL 上跑 §14.2 第 2 条全部越权断言和角色流程。合并前不跑 `make check`；发布前按发布规则跑一次。不发真信。

## 15. 交付计划（3.16）

关键路径：S0 契约 → S1 统一收件最小闭环 → S2 视图与计数 → S4 v2 交互整合。S3（角色）在 S0 后可与 S1、S2 并行；主题令牌（S4 的前半）是纯 Web，可在任意时刻并行。

| 切片 | 结果与流程 | 模块与部署单元 | 契约与迁移 | 开关与依赖 | 测试与验收证据 | 迁移与回填 | 回退 | 延后到后续切片 |
|---|---|---|---|---|---|---|---|---|
| S0 契约 | 评审通过的契约 | `contracts/`、`CONTEXT.md` | `MAILBOXES.md` 两节、`openapi.yaml` 6 路径、生成类型 | 无 | `make contract-check`、oasdiff 只报新增 | 无 | 撤销提交 | — |
| S1 全部收件闭环 | 打开邮件页看到跨邮箱"全部收件"线程列表，点开读信、`e` 归档 | API 统一收件读模型（`all`/`sent`、游标、线程）；Web BFF、`inbox-workspace`（旧外观即可）、路由 D20 | 迁移 `add_mail_inbox_index` | 依赖 S0 | PostgreSQL 仓储测试、越权服务测试、`all` 基准、组件测试；验收：C 看不到 `b@` | 无回填 | Web 回到上一版；API 路径无人调用 | 今日、待办、筛选、计数、角色、v2 |
| S2 视图、筛选、计数 | 今日新到、待办、已发送、未读、搜索、按邮箱收窄；侧栏数字 | API 计数（新仓储方法 `CountUnreadByMailboxIDs`，可读集取自 `ListMailboxAccessRows`，不经 `visibleMailboxes`）、`today`/`pending`/`q`/`unread`/`mailbox_id`；Web 侧栏视图与数字 | 无新增迁移 | 依赖 S1 | 时区单元测试；`CountUnreadByMailboxIDs` PostgreSQL 测试（只计传入 id、空集不查库、跨组织 id 不计）；计数与 `GET /v1/mailboxes` 每邮箱 `unread_count` 一致性测试；全部基准；验收：昨日未读不在今日新到 | 无 | 同 S1 | 角色 |
| S3 邮件角色 | 管理员建角色、分配邮箱；所有人在侧栏看到"角色与邮箱"，列表显示角色标签，计数含角色 | API 邮件角色模块、`GET /v1/mailboxes` 加 `role_id`、统一收件 `role_id` 筛选；Web 角色管理对话框、侧栏分组 | 迁移 `add_mail_roles` | 依赖 S0；与 S1、S2 并行，`role_id` 筛选在 S2 合入后接上 | 角色 PostgreSQL 并发测试、可见性服务测试、迁移测试、kest 流程 | 无回填（全部未分组） | 迁移 `Down`（丢失角色）或保留表仅回退 Web | 平台账号台账、开通任务 |
| S4 v2 外观与交互 | 全站 v2 主题与暗色；三栏布局、640px 阅读栏；快捷键、`?`、⌘K；额度小组件 | Web 主题、字体、`inbox-*` 组件重排、`command-palette`、`use-mail-shortcuts`、`send-quota-card` | 无 | 主题部分无依赖；交互依赖 S2、S3 | 逐页截图回归（登录、官网首页、控制台各页、邮件页浅深色；邮件页以原型 `docs/design/v2/MainV2.dc.html` 与 §11.0 数值为基准）、快捷键与 ⌘K 组件测试、对比度检查 | 无 | Web 回退 | AI 标签页、验证码遮罩、来源徽标、用量分项 |

估算假设与风险：不给日历数字。主要风险依次是：(1) `unread=true` 在高已读率下的扫描深度（S2 基准先行）；(2) 全站换肤引起其它页面视觉回归（S4 截图回归）；(3) 计数与列表分查导致的瞬时不一致（已接受）。

功能开关（设计决定（待确认）D27）：不新增环境开关。理由：API 只新增路径和列，对旧客户端透明；Web 的旧单邮箱工作区始终可通过 `?mailbox=` 访问，回退即重新部署上一版本 Web；新增 `NEXT_PUBLIC_*` 开关要走构建期配置流程，收益小于成本。

## 16. 验收标准

以权限 PRD §5 的组织为准（管理员 A，员工 B、C；`b@`、`c@` 私人；`team@` 共享，B 为 send，C 为 view），另建角色"设计编辑部"（含 `b@`）与"官方"（含 `team@`）。

| 编号 | 操作 | 预期 |
|---|---|---|
| UI-1 | B 打开 `/console/mailboxes` | 显示"全部收件"，含 `b@` 与 `team@` 的收件箱邮件，按收件时间倒序、按线程聚合，行内显示角色标签 |
| UI-2 | C 打开同页，并用搜索、⌘K、`?mailbox_id=<b@>`、`?role_id=<设计编辑部>` 尝试 | 任何结果中 `b@` 的邮件为 0；侧栏没有"设计编辑部"；筛选返回空列表，无错误提示泄露存在性 |
| UI-3 | A（非任何邮箱成员）打开同页 | 列表为空，侧栏全部 0；角色分组可见，`b@`、`team@` 显示锁定、无数字 |
| UI-4 | 昨天到达的一封未读信 + 今天到达后已归档的一封信 | "今日新到"只含后者；"全部收件"含前者不含后者 |
| UI-5 | B 给一封信加星，再归档另一封加星信 | "待办"只含前者；C 的"待办"不受影响 |
| UI-6 | B 在列表按 `j` `j` `e`，再按 `r` | 第二封被归档并从"全部收件"消失；回复对话框发件邮箱为该信所在邮箱 |
| UI-7 | C 在 `team@` 的信上按 `e` | 不发请求，提示无发送权限 |
| UI-8 | A 删除"官方"角色 | `team@` 回到"未分组"；B 的侧栏数字随之移动，总数不变 |
| UI-9 | 成员 B 调用新建角色接口 | `403 PERMISSION.DENIED` |
| UI-10 | 访问 `?mailbox=<team@ id>` | 原单邮箱工作区可用，文件夹、标签、回收站、权限面板不变 |
| UI-11 | 切换深色模式；在登录页、官网首页、控制台各页截图；邮件页在 1440×900 下与原型 `docs/design/v2/MainV2.dc.html` 对照 | 全部使用 v2 令牌，无硬编码旧色，文字对比度达标；邮件页浅色截图的栏宽、圆角、行结构、侧栏与额度卡片位置符合 §11.0（原型中本切片不渲染的部分除外） |
| UI-12 | 额度达到上限后 B 发信 | 已发送旁显示"排队中"，额度小组件显示"额度已满，排队中"，B 看不到组织用量数字；A 另外看到组织上限一行 |
| PERF-1 | 运行 §14.3 基准 | 全部达到目标；计数基准中不可读邮箱（5 万封）不进入未读计数查询，管理员非成员的计数请求不访问 `mail_messages` |

## 17. 设计决定（待确认）汇总

| 编号 | 决定 | 依据 |
|---|---|---|
| D1 | 路径为 `/v1/inbox-messages`、`/v1/inbox-message-counts` | 平铺连字符名词，同 `/v1/mail-messages`、`/v1/mail-message-stats` |
| D2 | 游标页，排序 `(created_at DESC, id DESC)`，`created_at` 即收件时间 | `contracts/README.md` Cursor Pages；`pkg/pagination/cursor.go` |
| D3 | "今日"按请求 `tz`（默认 UTC），左闭右开，Web 传用户时区设置或浏览器时区 | `mailbox` 不依赖 `setting` starter；`localization.timezone` 已有 |
| D4 | 今日新到 = 今天到达的入站信，除回收站外任何位置 | PRD R2"新增记录单列" |
| D5 | 待办视图值为 `pending`；内容 = 我加星且未归档、不在回收站，收发两向 | 用户已定"星标未归档未删除"；英文 to-do 单词会被计划检查当作未完成标记 |
| D6 | `q` 只与 `view=all` 组合，搜索除回收站外全部位置、两向 | 与单邮箱"无 placement 的 q"一致 |
| D7 | `all`、`sent` 可线程聚合，规则照 organize；`today`、`pending`、未读、搜索按单封 | organize §5.8 |
| D8 | 不可读邮箱或不可见角色的筛选返回空，不报错 | `/v1/mail-messages` 的 `mailbox_id` 规则 |
| D9 | `role_id=none` 表示未分组 | — |
| D10 | 列表项 `snippet` 读取时由 `substr(text_body,1,400)` 派生，截 140 字符，不加列 | 避免改六条入库路径与回填 |
| D11 | 新索引 `idx_mail_messages_inbox_time (mailbox_id, direction, created_at DESC, id DESC)`，按邮箱 LATERAL 取页 | 代价与组织总量无关 |
| D12 | 计数字段与公式见 §8.3；每邮箱未读由新仓储方法 `CountUnreadByMailboxIDs` 只对可读邮箱计算，服务层求和；不复用 `visibleMailboxes` | `repository.ListMailboxes` 对组织全部邮箱计数，再由服务层把不可读的置 0，代价随不可读数据增长 |
| D13 | 概念名 *Mail role*（`mail_roles`、`/v1/mail-roles`），写入 `CONTEXT.md` | 避免与组织角色、access role 混淆 |
| D14 | 角色字段与上限：名称 1–64 唯一、标签色板、描述 ≤ 280、负责人可空、每组织 ≤ 50 | 与标签、邮箱上限对齐 |
| D15 | 分配接口 PUT `/v1/mailboxes/:mailbox_id/role-assignment`，需 `administer`，只写请求审计 | 同 `webhook-setting` 形状；角色不是权限事实 |
| D16 | 成员只看到含其可见邮箱的角色，`mailbox_ids` 仅可见邮箱；可看到描述与负责人 | 用户已定"只对可见邮箱看到角色名" |
| D17 | 负责人离开组织时清空，读取时按当前成员过滤兜底 | 现有 `MemberRemoved` 处理器 |
| D18 | 角色修改整体替换，后写者胜 | 同标签 |
| D19 | 新错误码 `MAILBOX.ROLE_NOT_FOUND` 404、`ROLE_NAME_TAKEN` 409、`ROLE_LIMIT_REACHED` 422 | 同文件夹、标签 |
| D20 | `/console/mailboxes` 默认统一收件，`?view/role/box/q` 为其 URL 状态，`?mailbox=` 保留旧工作区 | 保持现有单邮箱路由 |
| D21 | `next/font/google` 自托管 Inter、Newsreader；Noto Serif SC 不预加载并有系统回退 | 无运行时第三方请求 |
| D22 | v2 令牌替换全站语义令牌，深色值见 §11.10 | `web/AGENTS.md` 主题规则 |
| D23 | ⌘K 用现有 Dialog 实现，不引入 `cmdk` | 免新依赖评审 |
| D24 | 额度小组件不画进度条与分项，按现有数据降级 | 缺组织用量接口；权限 PRD §3.7 |
| D25 | 列表标签页降级为"全部 / 未读" | AI 分流属切片 ③ |
| D26 | 统一收件写信增加发件邮箱选择 | 写信必须有发件邮箱 |
| D27 | 不新增功能开关 | 新增接口对旧客户端透明，旧工作区常驻 |
| D28 | 基准：10 个可读邮箱共 20 万封 + 1 个不可读 5 万封，目标见 §14.3 | `listing_bench_test.go` 的做法 |
| D29 | 单键快捷键在输入框、输入法组合、模态框中不触发；无权限动作不发请求 | 防误操作 |
| D30 | 列表与计数每 `MAIL_REFRESH_MS` 刷新，后台标签页不刷新 | 现有 `liveRefresh` |
| D31 | 切片 ① 中角色与 `owner_user_id` 只用于展示、不影响访问，负责人离开时置 `NULL`；PRD R3 的负责人交接与离职失去业务邮箱访问推迟到切片 ④，不改变不变式 8 | 角色不是权限事实（D15）；错判代价为 `owner_user_id` 列 schema 变更与离职事件行为改写 |

## 18. 需要产品确认的问题

以下问题都不阻塞 S0–S3；负责人为产品负责人，最晚在 S4 开工前决定。未决时按"默认做法"实施，不返工数据模型和接口。

1. **额度小组件要不要还原原型 `docs/design/v2/MainV2.dc.html` 的"108 / 200"和"日常 / 群发 / 留给回信"？** 需要一个新的、仅所有者和管理员可见的组织用量接口（滚动 24 小时已用尝试数，按直发与群发拆分），而"留给回信"是一项目前不存在的预留额度策略，需要先定义（预留多少、群发是否会因此提前暂停）。影响：S4 范围多一个接口和一条发送策略。默认：按 D24 降级，不做。
2. **"今日新到"的口径**：(a) 今天到达但已被归档或移入文件夹的信是否仍算（默认算，D4）；(b) "今天"按每个人自己的时区，还是全组织统一一个时区（默认按个人，D3；若要统一，需要新增一个组织级设置）。影响：计数和列表的结果。
3. **成员能看到角色的哪些信息？** 默认成员能看到含其可见邮箱的角色的名称、颜色、描述和负责人（D16）。如果描述或负责人也属于敏感信息，需要改为成员只见名称和颜色。影响：角色列表响应的字段可见性。
4. **是否接受全站一次性换成 v2 外观？** 主题令牌是全局的，登录页、官网和控制台其它页面会同时变化（D22）。备选是只在邮件页局部使用 v2 令牌，但这违背 `web/AGENTS.md`"不硬编码平行设计系统"，以后还要再迁一次。默认：全站换肤并做截图回归。

## 19. 就绪检查（§6 自检）

机械检查：`python3 .agents/skills/business-implementation-planning/scripts/validate_plan.py --strict docs/plans/unified-inbox.zh-CN.md`。

产品与领域

- [x] 结果、范围、非目标、参与者明确（§1、§2）。
- [x] 术语与 `CONTEXT.md` 一致；新增 *Mail role* 并与组织角色、access role 区分（§5.1、D13）。
- [x] 主流程、拒绝、恢复已定义；本切片无取消与超时流程，搜索超时有明确错误（§3）。
- [x] 状态机无缺失或矛盾的迁移（§7）。
- [x] 独立生命周期分开建模：角色生命周期与邮箱的角色归属分开（§7.1、§7.2）。
- [x] 时间与时区语义明确：`timestamptz`、今日边界、夏令时（§5.2、D3）；无金额。

归属与架构

- [x] 每条规则、记录、路由、事件处理有唯一归属（§4.1、§10）。
- [x] 模块职责与排除项明确（§4.2、§4.3）。
- [x] 部署单元只通过契约通信（§1.5、§8.9、§11.13）。
- [x] 同步与异步依赖已列出（§10）。
- [x] 事务边界不跨运行时边界（全部为单库单事务；事件处理器单独一条幂等 `UPDATE`）。

数据与契约

- [x] 表、约束、索引、迁移、回退、回填已定义（§6）。
- [x] 公开 HTTP 行为将写入 `contracts/`，改动清单见 §8.9（S0 先行）。
- [x] 授权包含组织与行级范围，不只看角色（§9）。
- [x] 分页、筛选、排序、上限、错误已定义（§8.2–§8.8）。
- [x] 每个写操作的幂等与并发已定义（§4.3、§8.5–§8.8）。

副作用与运维

- [x] 派生记录有唯一触发与幂等键（§10）。
- [x] 事件的投递、重试、重复、顺序、重放已定义（§10，唯一消费的事件）。
- [x] 本切片无新的定时任务，理由见 §10.1；现有 worker 未改动。
- [x] 审计、隐私、指标、告警、支持诊断已定义（§12、§13）。
- [x] 发布、回退、安全降级已定义（§13.3、§15）。

交付与证明

- [x] 垂直切片按依赖排序（§15）。
- [x] 每个切片有可观察的验收（§15、§16）。
- [x] 关键不变式对应到具体测试（§14.1）。
- [x] PostgreSQL 专有行为有 PostgreSQL 证明（LATERAL、`NOT EXISTS` 线程头、外键 `SET NULL`、咨询锁、唯一索引）。
- [x] 验证命令与受影响部署单元匹配（§14.4）。

未决事项：§18 的 4 个问题不改变数据模型、授权边界、状态机或切片顺序，已记录范围、负责人、决定时限和默认做法，符合 §8 Definition of Ready 对非阻塞问题的要求。

readiness: READY WITH RECORDED ASSUMPTIONS
