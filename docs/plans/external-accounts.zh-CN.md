# 外部邮箱绑定、来源徽标、平台账号台账与负责人交接（v2 切片 ④）设计

状态：已定稿（夜间自主推进，自审后定稿；未决处记为"设计决定（待确认）"）。**真实接入 Gmail / Outlook 需要你在 Google Cloud 和 Microsoft Entra 创建 OAuth 应用并通过审核，见 §9"待我确认"。**
上级需求：[产品需求文档](../PRODUCT_REQUIREMENTS.zh-CN.md) R2"外部邮箱绑定与每日统一收件"（路线图阶段 C）和 R3"角色、平台账号与员工开通任务"（阶段 D）。
原型：[docs/design/v2/MainV2.dc.html](../design/v2/MainV2.dc.html) 中列表行的来源徽标位置。§11 中切片 ① 只渲染了占位。
前置：切片 ① 的邮件角色（`mail_roles`）是 R3 角色台账的第一步；邮箱权限模块的离职交接（`handovers`）。

## 1. 两块独立的工作

| 部分 | 依赖外部服务 | 夜间可做 |
|---|---|---|
| A. 外部邮箱绑定（Gmail / Outlook 只读同步）与来源徽标 | 是：两家的 OAuth 应用、受限权限范围审核 | 数据模型、同步接口抽象、假实现、来源徽标、绑定页骨架（开关默认关） |
| B. 平台账号台账与负责人交接 | 否 | 全部 |

两部分分两个 PR，B 先做。

## 2. B：平台账号台账与负责人交接

### 2.1 问题

角色（`mail_roles`）已经能把邮箱分组，但"这个平台账号用哪个邮箱注册、负责人是谁、交接过几次"仍然散落各处。R3 的验收要求：从一封平台注册邀请定位到角色；负责人离职后由新负责人接手。

### 2.2 数据模型

新表 `platform_accounts`：

| 列 | 说明 |
|---|---|
| `id` | 主键 |
| `organization_id` | 所属组织 |
| `platform` | 平台名，自由文本，最多 64 字符，例如 Medium、Behance |
| `handle` | 账号名或主页，最多 200 字符 |
| `profile_url` | 可空，只接受 http(s) |
| `role_id` | 可空，外键指向 `mail_roles`，删除时置空 |
| `mailbox_id` | 注册邮箱，可空，外键指向 `mailboxes`，删除时置空 |
| `owner_user_id` | 负责人，可空 |
| `status` | `invited`、`awaiting_confirmation`、`active`、`timed_out`、`needs_review`、`retired` |
| `notes` | 最多 2 000 字符 |
| `created_at`、`updated_at` | 时间戳 |

- 唯一约束：`(organization_id, lower(platform), lower(handle))`。
- 新表 `platform_account_events`：账号的交接与状态历史。列为 `account_id`、`kind`（`created`、`status_changed`、`owner_changed`、`mailbox_changed`）、`actor_user_id`、`from`、`to`（短文本）、`created_at`。

### 2.3 权限

- 组织所有者和管理员：增删改。
- 成员：只能查看注册邮箱是自己能读的邮箱的账号，或自己是负责人的账号。这样不会通过台账推断出别人的私人邮箱。

设计决定（待确认）D1：成员也能只读看到自己相关的台账条目。

### 2.4 接口

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/v1/platform-accounts` | 支持 `q`（平台、账号、注册地址）、`role_id`、`mailbox_id`、`owner_user_id`、`status` 筛选；分页 |
| POST | `/v1/platform-accounts` | 新建 |
| PUT | `/v1/platform-accounts/{id}` | 修改 |
| DELETE | `/v1/platform-accounts/{id}` | 删除，记一条事件后硬删 |
| GET | `/v1/platform-accounts/{id}/events` | 交接与状态历史 |
| POST | `/v1/platform-account-handovers` | 批量交接 |

批量交接的请求体是 `{from_user_id, to_user_id}`：把 from 负责的所有账号转给 to，并逐条记 `owner_changed` 事件。它与邮箱的离职交接各自独立。

设计决定（待确认）D2：邮箱离职交接时不自动交接台账，而是在交接结果里提示"该员工还负责 N 个平台账号"，附一键交接入口。

### 2.5 界面

- 组织管理后台新增"平台账号"页，复用公共邮箱管理第一期建立的后台骨架：
  - 表格列为平台、账号、角色、注册邮箱、负责人、状态；
  - 支持搜索、筛选、编辑抽屉和事件时间线。
- 角色管理对话框里，每个角色显示"名下账号 N 个"。

## 3. A：外部邮箱绑定与来源徽标

### 3.1 数据模型

- 新表 `external_mail_connections`：
  - `id`、`organization_id`、`mailbox_id`（同步进来的信落在哪个 Zmail 邮箱，类型为新的 `external`）；
  - `provider`（`gmail`、`outlook`）、`account_address`、`owner_user_id`；
  - `status`（`active`、`needs_reconnect`、`revoked`）、`last_synced_at`、`last_error_code`；
  - `encrypted_refresh_token`：用现有 `MAILBOX_SECRET_ENCRYPTION_KEY` 的 AEAD 加密；
  - `sync_cursor`：Gmail 的 historyId 或 Graph 的 deltaLink；
  - `backfill_days`，默认 14。
- `mail_messages` 新增 `source` 列：`zmail` 为默认，另有 `gmail`、`outlook`。还新增可空的 `external_id`，并加唯一约束 `(mailbox_id, external_id)`，保证重连、重复通知都不会多出副本（R2 验收）。
- `mailboxes.type` 新增枚举值 `external`。外部邮箱不能发信（只读），成员规则与私人邮箱相同：默认仅绑定人可见。

### 3.2 同步接口

```go
type ExternalMailSource interface {
    AuthURL(state string) string
    Exchange(ctx context.Context, code string) (Token, Account, error)
    Fetch(ctx context.Context, token Token, cursor string) (FetchResult, error) // 新信 + 新游标
    Revoke(ctx context.Context, token Token) error
}
```

- 实现：`disabled`（默认）、`fake`（测试用，从内存脚本吐出邮件），以及真实的 Gmail（`gmail.readonly`）和 Outlook（Graph `Mail.Read`）。真实实现要等你确认后才写。
- 同步 worker 每 2 分钟轮询一次 active 连接，限速，按 `external_id` 幂等写入。返回 401 时置为 `needs_reconnect`。
- 解绑：调用 `Revoke`，状态置为 `revoked`，立即停止拉取。已同步的历史按 §9 的决定处理。

### 3.3 来源徽标

- 统一收件和列表行的摘要新增 `source` 字段。
- 前端在角色标签旁显示小徽标，区分 Gmail、Outlook 和自建；自建默认不显示。
- 本切片可以先只做徽标：`source` 恒为 `zmail` 时界面没有变化，接口字段先就位。

### 3.4 开关

`EXTERNAL_MAIL_PROVIDERS` 默认为空。为空时：

- 绑定入口不显示；
- 绑定接口返回 404；
- worker 不启动。

## 4. 测试

- **B（台账与交接）：**
  - 增删改查；
  - 成员可见范围（越权回归）；
  - 批量交接和事件记录；
  - 唯一约束；
  - 迁移的 Up/Down 测试与 golden schema；
  - kest 验收流：从注册邮箱定位账号，并完成一次交接。
- **A（外部邮箱同步）：** 用 fake 源测试以下场景：
  - 幂等，重复拉取不多出副本；
  - 游标推进；
  - 401 后进入待重连；
  - 撤权后停止拉取；
  - 私人可见性。

## 5. 设计决定汇总

| 编号 | 决定 |
|---|---|
| D1 | 成员只读可见与自己相关的台账条目 |
| D2 | 邮箱离职交接不自动交接台账，只提示并给入口 |
| D3 | 外部来源落在新的 `external` 类型邮箱里，只读，默认仅绑定人可见 |
| D4 | 首次回溯 14 天 |

## 9. 待我确认（需要你决定或提供）

1. **Google Cloud OAuth 应用**：在 Google Cloud 控制台创建，`gmail.readonly` 属于受限范围，需要通过 Google 审核，第三方安全评估可能收费。
2. **Microsoft Entra 应用注册**：多租户，申请 `Mail.Read` 和 `offline_access` 权限。企业租户可能需要管理员同意。
3. 回调地址使用 `https://zmail.im/api/external-mail/callback`，还是另用子域名。
4. **解绑后历史副本的处理**：立即删除，还是保留 N 天后删除（PRD 开放问题 2）。我默认采用"保留 30 天、可手动立即删除"，待你确认。
5. 试点支持哪些账户类型：个人 Gmail、Google Workspace、个人 Outlook、Microsoft 365。

在你确认前，夜间只做 B 的全部内容，以及 A 中不需要外部服务的部分（数据模型、fake 源、来源徽标字段）。A 的开关默认关闭。
