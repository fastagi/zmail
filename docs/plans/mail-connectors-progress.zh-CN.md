# 邮件连接器：自主推进日志

按 [mail-connectors-loop.md](mail-connectors-loop.md) 执行。每条记录：做了什么、怎么验证、接下来做什么、采用了哪些默认决定。

## 默认采用、待确认的决定

对应[路线图](mail-connectors-roadmap.zh-CN.md) §8。负责人要求不再逐项询问，先按建议执行，每项都保持容易回退。

| # | 决定 | 采用 |
|---|---|---|
| 1 | 管理员能否看到员工个人接入的地址 | 不能，只在"外部连接"页看遮罩地址和状态 |
| 2 | 是否做"来源"维度迁移 | 做 |
| 3 | 解绑或离职后副本保留多久 | 30 天后自动删除，本人可立即删除 |
| 4 | Agent 能否看到明文验证码 | 默认遮罩，授权时可单独勾选并单独记录 |
| 5 | Agent 的身份 | 某个人的 API key 加 Agent 标记 |
| 6 | 是否引入 IMAP 依赖库 | 优先用标准库实现；确需依赖时在 PR 里写明理由 |
| 7 | 首批试点账户类型 | 个人 Gmail、Google Workspace、Microsoft 365 |

## 2026-11-21 起点

- 已完成并开 PR：连接器底座（C0），见 fastagi/zmail#65。
- 分支 `feat/connector-adapters` 从 `feat/mail-connectors` 切出，后续任务逐个叠加 PR。

## 2026-11-21 T1 适配器接口缺口

- 做了什么：
  - `Fetch` 改为接收请求对象（账号地址、回溯天数、同步时间）。
  - 结果可以带"还有下一页"（立即再拉，长回填按页推进、重启后可续）。
  - 结果可以带轮换后的新令牌，与该页邮件在同一事务保存。
  - 新增"游标过期"错误：从空游标立即重新对齐，不产生重复邮件。
  - 真实连接器通过 `registerExternalMailConnector` 在各自文件里注册，新增服务商不改共享文件。
  - 配置接受 `gmail`、`outlook`、`imap`，并校验 OAuth 客户端和回调地址。
- 验证：新增 3 个测试（分页回填、令牌轮换、游标过期）和配置测试；mailbox 包与 config 包在 PostgreSQL 上全部通过。
- 未做：同步运行记录表、限流预算、附件元数据，留给 T9。
- 调度方式变更：负责人要求由本窗口负责调度，把后续任务并行派给多个 Opus 5.5 会话，并派验收会话。任务划分见下一条。

## 2026-11-21 第一波并行会话已派出

本窗口只负责调度、合并和验收。每个会话用 Opus 5.5（中等推理强度），各自一个工作树，从 `feat/connector-adapters` 切出，PR 的基准分支也是它。

| 会话 | 任务 | 主要文件 | 迁移时间戳 |
|---|---|---|---|
| gmail-adapter | Gmail 适配器（T2） | `external_mail_gmail.go` | 无 |
| outlook-adapter | Outlook 适配器（T3） | `external_mail_outlook.go` | 无 |
| imap-adapter | IMAP 适配器与密码绑定接口（T4） | `external_mail_imap.go`、绑定接口 | 无 |
| web-connect | Web 接入流程（T6） | 仅 `web/` | 无 |
| origin-model | 来源维度与管理员连接页接口（T5） | 邮箱模型、权限、契约 | 2026_12_04 |
| agent-feed | Agent 授权与收件流（T8） | `agent_grant_*.go` | 2026_12_05 |
| sync-ops | 同步运行记录、限流、副本清理（T9） | 同步服务、新表 | 2026_12_06 |

第一波只派 7 个而不是十几个：再多就会有两个会话改同一批文件。迁移、OpenAPI、生成的类型和 golden schema 是公共冲突点，由本窗口在合并时统一重新生成。

接下来：

1. 每个会话结束后，本窗口核对它的 diff 和测试结果，再合并进集成分支 `integration/connectors`（不合并到 `main`）。
2. 第二波（依赖第一波）：共享接入（T7）、管理员连接页和 Agent 页签的 Web 界面、租户接入（T10）、以原地址回复与推送（T11）。
3. 第三波：验收会话，在集成分支上按路线图的验收条目逐项实测。
