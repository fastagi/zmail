# Zmail 个人 C 端模式与多服务商第三方邮箱聚合架构规划

> 目标：让 Zmail 同时具备 **个人极致邮件客户端（Personal Mode，对标 Superhuman / Spark / Fastmail）** 与 **企业团队邮箱平台（Work Mode）** 双重形态；个人用户可一键接入 Gmail、Outlook、iCloud、QQ、163、Fastmail 及任意 IMAP/SMTP 邮箱，享受毫秒级响应的实时 Runtime 体验，且个人数据与企业组织彻底隔离。

---

## 一、个人模式（Personal）与企业模式（Work）的关系设计

### 1.1 核心原则：个人资产归个人，企业资产归组织
- **零摩擦切换**：每个注册用户天生拥有一个私有的 **个人空间（Personal Workspace）**；同时可加入 0~N 个 **企业组织（Work Organizations）**。
- **数据严格隔离**：
  - 个人空间绑定的 Gmail / Outlook / iCloud / QQ / 163 仅归属个人，**离职或被移出企业组织时个人邮箱与邮件 100% 保留**，企业管理员永远无法看到个人空间的任何元数据或正文。
  - 企业组织下的自定义域名邮箱、共享邮箱、群发、事务邮件、Agent 授权归属组织，离职即自动收回。
- **界面自动降噪（Progressive Disclosure）**：
  - 切到 **个人模式** 时，左侧导航自动隐藏 B2B 管理菜单（企业域名、群发活动、联系人、开发者 API、组织成员、运营后台），只保留：**统一收件箱、星标/稍后处理、已发送、草稿、已接入外部邮箱、AI 分拣、快捷键与设置**。
  - 切到 **企业模式** 时，完整展示团队共享邮箱、多域名管理、群发与事务邮件能力。

### 1.2 演进路线（兼容现有架构，零破坏）
- **Phase 1（前端模式切换 + 个人账号聚合层）**：通过 `workspace-mode`（`personal | work`）立即提供个人聚焦视图 + 一键接入主流个人邮箱向导，复用现有 `externalmail` 与 `mailbox` API，零数据库破坏。
- **Phase 2（后端 `personal` Workspace 原生模型）**：注册时自动创建 `organizations.kind = 'personal'`（不可转让、单成员、免成员管理），使个人空间与企业空间共用同一套权限与隔离底座。

---

## 二、多服务商第三方邮箱接入底层架构与设计模式

### 2.1 核心设计模式
1. **Strategy + Registry Pattern（服务商策略注册表）**
   - `api/internal/modules/externalmail/providers.go` 统一定义 `ProviderSpec`：
     - 认证策略：`oauth2_pkce`（Gmail / Outlook） vs `app_password`（QQ / 163 / iCloud / Fastmail / Yahoo / Exmail / Feishu / 自定义 IMAP+SMTP）。
     - 能力声明：`CanRead`、`CanSend`、`SupportsPush`、`SupportsIDLE`。
     - 智能识别：`DetectProvider(email)` 根据邮箱后缀自动识别服务商，预填 IMAP/SMTP 主机与端口，并给出直达授权码/应用密码设置页链接。
2. **Ports & Adapters（六边形同步引擎）**
   - 统一 `SyncCursor` + 增量拉取协议，上层收件箱与 SSE 推送完全屏蔽 Gmail HistoryID、MS Graph DeltaLink、IMAP UIDVALIDITY 差异。
3. **Real-Time Runtime Hub（SSE Fan-out + Backpressure）**
   - 基于 `api/internal/modules/mailbox/syncfeed/` 构建多订阅者 `Hub`：
     - 有界环形/丢弃最旧缓冲（Drop-Oldest Backpressure），慢客户端绝不拖垮整个进程；
     - 断线重连携带 `cursor` 自动增量补齐，保证零丢信；
     - `MailReceivedEvent` → `EventBus` → `syncfeed.Hub` → 浏览器 SSE，端到端延迟 < 100ms。
4. **Optimistic UI + 5s Undo Queue（即时响应 Runtime 体验）**
   - 标已读、星标、归档、删除、发送全部走乐观更新（0ms 视觉反馈），失败自动回滚，底部提供 5 秒可撤销 Toast；
   - 全局键盘流（`J/K`、`C`、`R`、`E`、`S`、`/`、`?` 快捷键浮层）让高频处理邮件像 Linear / Superhuman 一样顺手。

---

## 三、分波次长任务拆解（Swarm 并行执行）

### Wave 1（本次并行落地）
- **T1 (`provider-engine`)**：`api/internal/modules/externalmail/providers.go` + `providers_test.go` — 12+ 主流国内外邮箱服务商注册表与智能邮箱自动识别引擎（含 `-race` 单测）。
- **T2 (`sync-runtime`)**：`api/internal/modules/mailbox/syncfeed/hub.go` + `hub_test.go` — 高并发 SSE 实时同步 Hub（心跳保活、慢消费者背压、断线游标补齐、50 并发 `-race` 压测）。
- **T3 (`personal-mode-ui`)**：`web/` 个人/企业模式切换器（`WorkspaceModeToggle`）、个人模式聚焦导航、主流邮箱一键接入预设条 + Vitest UI 测试。
- **T4 (`inbox-runtime-ux`)**：`web/` 快捷键速查浮层（`?` 呼出 `KeyboardShortcutsDialog`）+ 乐观更新与 5 秒撤销 Hook（`useOptimisticUndo`）+ Vitest 测试。

### Wave 2（后续迭代）
- **T5**：Gmail API & MS Graph OAuth 发信打通（不仅只读，支持原地址发信与草稿同步）+ 生产 OAuth App 接入。
- **T6**：IMAP IDLE 长连接实时收信 Worker（从 2 分钟轮询升级到秒级推送）。
- **T7**：`GET /v1/inbox/stream` SSE 路由与前端 `EventSource` 自动重连接入。
- **T8**：桌面端（Electron）与移动端（Expo/RN）共享 `@zmail/mail-core` + APNs/FCM 原生推送。
