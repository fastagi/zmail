# Zmail Monorepo 目录规划与 `mailbox` 拆解实施蓝图

> 本文档是 Zmail 面向**多端演进（Web / Admin / Electron 桌面端 / Mobile）**与**未来开源发布**的架构重构权威蓝图与任务优先级清单。所有后续重构 PR 与长任务（`/goal`）应严格遵循本文档的目录边界与依赖规则。

---

## 一、背景与核心目标

当前仓库基于 FastBuild 脚手架构建，基础设施与架构守卫（`Makefile` 中 25+ 项 governance 检查）非常扎实，但在业务高速迭代后出现三大结构性瓶颈：

1. **`mailbox` 上帝模块膨胀**：
   - 后端 `api/internal/modules/mailbox/` 达 174 文件 / 52,014 LOC，`mailbox.Service` 接口高达 **140 个方法**，`service` 结构体包含 **62+ 个字段**；
   - 前端 `web/src/features/mailbox/` 达 169 文件 / 53,817 LOC，单文件 `mailbox-route.ts` 达 **6,502 行**，`mock-mailbox-store.ts` 达 **3,088 行**，多个组件超过 1,000 行；
   - 契约 `contracts/MAILBOXES.md` 达 272 KB，`openapi.yaml` 达 775 KB。
2. **多端扩展准备不足**：
   - `web/` 与 `admin/` 各自维护独立的 `pnpm-workspace.yaml` 与 20,080 行完全重复的 `openapi.ts`。
   - 后续引入 Electron 桌面端与 Mobile 客户端时，必须抽离平台无关的核心业务层与客户端轻量 SDK，避免 4 端重复维护。
3. **开源可读性与贡献者体验**：
   - 开源前需确立清晰的 `api/` + `contracts/` + `apps/*` + `packages/*` 顶层布局，拆解巨型模块，降低社区贡献与 AI Agent 上下文成本。

---

## 二、终态目录蓝图

### 1. 顶层仓库结构（Go 与 TS 领地清晰分离）

```text
zmail/
├── api/                          # Go 后端（独立 go.mod，保持顶层不动，不进 pnpm workspace）
├── contracts/                    # 跨语言权威契约（OpenAPI + 分域文档 + 校验脚本）
├── apps/                         # TS/JS 可部署应用壳（路由、鉴权适配、原生壳、打包）
│   ├── web/                      # Next.js（官网 + 客户控制台 + 同源 BFF）
│   ├── admin/                    # Vite SPA（纯静态运营后台）
│   ├── desktop/                  # Electron（Node 主进程 SQLite/FTS5/Keychain/托盘 + 渲染进程复用 mail-ui）
│   └── mobile/                   # Expo / React Native（手机端原生交互壳，复用 mail-core）
├── packages/                     # TS/JS 跨端共享内部包（严格单向依赖，零平台专属代码）
│   ├── api-client/               # 按 OpenAPI Tag 拆入口生成的轻量 SDK + 类型 + Zod
│   ├── mail-core/                # 平台无关纯 TS：TanStack Query hooks、收件箱状态机、Delta Sync、乐观更新、i18n
│   ├── ui/                       # 设计系统原语：Tailwind 4 tokens + shadcn/ui 基础组件
│   ├── mail-ui/                  # 邮箱业务大组件：收件箱、安全 HTML 读信器、写信器、AI Triage（Web + Desktop 共用）
│   └── config/                   # 共享 eslint / tsconfig / tailwind 预设
├── docs/                         # 架构文档、ADR、实施计划
├── scripts/                      # 运维、发版、治理脚本
├── .agents/                      # AI Agent 技能与规则
├── pnpm-workspace.yaml           # 根级统一依赖锁定与安全 overrides
├── turbo.json                    # 并行任务编排 + 增量缓存 + Docker turbo prune
└── Makefile
```

### 2. 后端 `api/` 目标结构

```text
api/internal/
├── domain/
│   ├── mail/                     # 收拢散落的 mail_*.go / mailbox_*.go 领域模型与窄接口
│   │   ├── inbox.go              # 邮箱、消息、线程、草稿、签名、文件夹/标签
│   │   ├── domain.go             # 自定义域名、DNS、Zone
│   │   ├── delivery.go           # 群发、模板、联系人、事务邮件、抑制列表
│   │   ├── external.go           # 外部邮箱连接、同步、Push、释放
│   │   ├── triage.go             # AI 分类、草稿、Agent 授权
│   │   └── gateway.go            # IMAP/SMTP App Password 与客户端配置
│   └── ...
├── infra/
│   ├── config/                   # 拆分 config.go，移除 GlobalConfig 全局变量
│   │   ├── config.go
│   │   ├── server.go
│   │   ├── database.go
│   │   ├── auth.go
│   │   └── mail.go
│   └── mcp/                      # 收敛为 5~8 个高内聚意图工具
└── modules/
    └── mailbox/                  # 门面装配层（路由汇总、Starter Manifest、事件桥接）
        ├── core/                 # 高频收发信核心（邮箱、收件箱、草稿、签名、附件、搜索、Delta Sync）
        ├── domainsetup/          # 自定义域名、DNS、Cloudflare、托管 Zone
        ├── broadcast/            # 群发、联系人、名单、模板、退订
        ├── transactional/        # 事务性邮件、抑制列表、发送通道、测试发信
        ├── externalmail/         # Gmail / Outlook / IMAP 同步、凭据、Push 订阅
        ├── triage/               # AI Triage 分类/草稿与 Worker
        ├── agentgrant/           # Agent 授权与 Feed
        ├── roster/               # 成员花名册导入、欢迎信、平台账号账本
        ├── imapgateway/          # （已有）IMAP 网关
        ├── smtpgateway/          # （已有）SMTP 网关
        └── mailclientauth/       # （已有）客户端密码与鉴权
```

### 3. 契约 `contracts/` 目标结构

```text
contracts/
├── openapi.yaml                  # 统一入口（带清晰子域 Tags，供 api-client 按 Tag 生成）
├── mail/
│   ├── INBOX.md                  # 客户端核心（Web / Desktop / Mobile 共用）
│   ├── DOMAINS.md                # 域名 / DNS / Zone / Cloudflare
│   ├── DELIVERY.md               # 群发 / 联系人 / 模板 / 事务性邮件
│   ├── EXTERNAL_MAIL.md          # 外部邮箱同步与 Push
│   ├── TRIAGE_AND_AGENTS.md      # AI Triage 与 Agent Grant
│   └── GATEWAY.md                # IMAP / SMTP 客户端接入与 App Password
└── ...
```

---

## 三、架构铁律（防腐化边界）

1. **后端单向依赖（DAG）**：外围子包（`broadcast`、`externalmail`、`triage`、`imapgateway` 等）可单向依赖 `core/` 与 `domain/mail`；**`core/` 严禁反向依赖外围子包**，收信后副作用一律通过 `infra/events` 发布内部事件解耦。
2. **前端严格分层**：
   ```text
   apps/* ──▶ packages/mail-ui ──▶ packages/ui
                    │
                    ▼
             packages/mail-core ──▶ packages/api-client ──▶ contracts/
   ```
   - `packages/` 严禁 import `apps/`；
   - `packages/mail-core` 保持 **100% 平台无关纯 TS**（不引用 `window`/`document`/`next/*`/`electron`），通过 `MailTransport` 抽象注入不同端的网络与缓存实现。
3. **多端鉴权原则**：`apps/web` 走同源 BFF + HttpOnly Cookie；`apps/desktop` 与 `apps/mobile` 通过系统 Keychain/Keystore 保存可吊销 Bearer Token，**直连 Go `/v1` API**，不经过 Next.js BFF。
4. **开源前完成目录归位**：内部可按“先建 `packages/`，再归位 `apps/`”平滑推进，但**必须在开源发布前完成 `apps/web`、`apps/admin` 归位与敏感文件脱敏**。

---

## 四、分阶段任务清单与验收标准

### P0：清场、环境统一与架构护栏
- [x] **T0.1** 清理 61 个已合并干净 worktree，核查并收口 `feat/google-sign-in` 分支（已 cherry-pick `cbd633a1` 安全修复至 `main` 并清理该 worktree）。
- [x] **T0.2** 固化 Node 22/24 LTS 环境说明（新增 `.nvmrc`）；在 `Makefile` 的 `governance` 中新增 `check-code-size-guardrails.py`（单文件 800 行新文件上限 + 遗留文件防膨胀棘轮 + `mailbox.Service` 直接方法数棘轮）。
- [x] **T0.3** 落盘本蓝图文档 `docs/plans/monorepo-and-mailbox-refactor.md`。
- [x] **T0.4** 拆分 `api/internal/infra/config/config.go`（从 1,961 行降至 1,256 行，拆出 `server.go`、`auth.go`、`mailbox.go`，保持全部 8 个配置边界守卫 100% 通过）。
  - **验收**：`make governance` 相关检查 + `cd api && go test -count=1 ./...` 全部通过。

### P1：后端 `mailbox` 巨石拆解
- [x] **T1.1** 接口隔离（零破坏）：将 `mailbox.Service`（140 方法）拆为 7 个子接口（`CoreService`、`DomainService`、`BroadcastService`、`TransactionalService`、`ClientAccessService`、`RosterService`、`ExternalMailAllService`，加上既有的 `TriageService`、`AgentGrantService`），原 `Service` 退化为 0 直接方法的组合 Facade（已在护栏中锁定上限 `0`），20 个子域 Handler 全部切换为依赖窄接口并提供独立窄构造函数。
- [x] **T1.2** 逐个抽离独立子包（7/7 全部完成并验证）：
  1. [x] `triage/`（AI 分类模型、仓储与 Worker 已抽离至 `api/internal/modules/mailbox/triage`）
  2. [x] `agentgrant/`（Agent 授权模型与仓储已抽离至 `api/internal/modules/mailbox/agentgrant`）
  3. [x] `transactional/`（事务邮件脱敏、模型与仓储已抽离至 `api/internal/modules/mailbox/transactional`）
  4. [x] `broadcast/`（群发、联系人、模板与渲染引擎已抽离至 `api/internal/modules/mailbox/broadcast`）
  5. [x] `domainsetup/`（自定义域名、Zone 仓储与错误分类已抽离至 `api/internal/modules/mailbox/domainsetup`）
  6. [x] `externalmail/`（外部邮箱连接、Push、释放、运维与 Tenant 仓储已抽离至 `api/internal/modules/mailbox/externalmail`）
  7. [x] `roster/`（花名册、目录、CSV 导出、欢迎信与平台账本已抽离至 `api/internal/modules/mailbox/roster`）
- [ ] **T1.3** 收信副作用改为 `infra/events` 领域事件（`MailReceivedEvent`），切断 `core/` 对外围子包的硬引用。
- [ ] **T1.4** 收拢 `api/internal/domain/mail/`，拆分 `contracts/MAILBOXES.md` 为 `contracts/mail/*.md` 并在 `openapi.yaml` 补齐子域 Tags。
  - **验收**：`cd api && go test -count=1 ./...` + `make contract-check` + `make governance` 全绿。

### P2：TS Monorepo 底座 + Web `mailbox` 拆解与目录归位
- [ ] **T2.1** 根目录引入 `pnpm-workspace.yaml`（统一安全 `overrides`）与 `turbo.json`，建立 `packages/api-client`（按 Tag 多入口生成，消灭 `web` 与 `admin` 两份重复的 20,080 行 `openapi.ts`）。
- [x] **T2.2** 将 6,502 行 `mailbox-route.ts` 拆分为 16 个 ≤ 729 行的子域路由模块 + 171 行 barrel；将 3,088 行 `mock-mailbox-store.ts` 拆分为 9 个子域 Mock Store；将 `mailbox-service.ts`（1,570→17 行）与 `schemas.ts`（1,309→21 行）拆分为子域模块（全部从大文件遗留白名单永久移除）。
- [ ] **T2.3** 抽离 `packages/mail-core`（纯 TS Hooks、乐观更新、i18n、`MailTransport` 接口）与 `packages/mail-ui`（收件箱、安全 HTML 读信器、写信器、AI Triage）。Web 端全部 6 个超 1,000 行巨型组件已完成拆解（`mail-broadcasts.tsx`、`inbox-workspace.tsx`、`mail-domains.tsx`、`mail-contacts.tsx`、`mail-templates.tsx`、`message-list.tsx` 均已降至 ≤ 311 行并永久移除出大文件白名单）。
- [ ] **T2.4** 目录正式归位：将 `web/`、`admin/` 移入 `apps/web/`、`apps/admin/`，同步更新 `Makefile`、CI、治理脚本与 Docker 构建（接入 `turbo prune`）。
  - **验收**：`make check`（`governance` + `contract-check` + `api-check` + `web-check` + `admin-check`）全绿。

### P3：多端后端能力、客户端交付与开源准备
- [ ] **T3.1** 后端多端 3 件套：`Delta Sync` 增量同步接口（复用 IMAP UID 体系）+ 前台 `SSE` 实时推送 + `notification` 新增 `mobile_push`（APNs/FCM）。
- [ ] **T3.2** 桌面端落地 `apps/desktop`（Electron）：主进程本地 SQLite + FTS5 全文搜索 + Keychain + 托盘 + 自动更新；渲染进程挂载 `packages/mail-ui`。
- [ ] **T3.3** 手机端落地 `apps/mobile`（Expo / React Native）：复用 `packages/mail-core`，聚焦收信、读信、搜索、回复、AI Triage、原生推送 6 个高频场景。
- [ ] **T3.4** AI/MCP 收敛与开源发布准备：`api/internal/infra/mcp` 收敛为 5~8 个意图工具 + `zmail` CLI；为高频 `.agents/skills/` 建立真实失败 Trace 回归评测集；脱敏清理未跟踪内部文件与历史截图，补齐中英文开源 README 与一键 `docker compose`。
