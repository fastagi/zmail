# 花名册批量导入（一次邀请、开邮箱、配功能）

状态：已实现（本 PR）。负责人：邮件模块（入口）+ 组织模块（邀请）。日期：2026-11-23。

## 1. 用户场景

一家 30 人的公司刚把 `company.com` 通过 Cloudflare 接入 Zmail。管理员手里有一份 HR 导出的花名册（Excel / CSV）。今天要做的事：

1. 给每个同事发邀请；
2. 给每个人开一个自己的邮箱，比如 `alice@company.com`；
3. 决定哪些人能用群发、联系人、模板、API 密钥。

以前只能一人一人地点「邀请成员」，再去「邮箱」页一个个建个人邮箱，最后逐个改功能开关。30 个人要 90 次操作，还容易漏、容易建错地址。

花名册导入把这三件事合成一次：上传 CSV → 预览（每行标出问题）→ 确认 → 看结果 → 只重试失败的行。

## 2. 同类产品怎么做（调研）

| 产品 | 入口与模板 | 关键字段 | 结果与重试 |
|---|---|---|---|
| Google Workspace | 管理控制台「目录 > 用户 > 批量更新用户」，先「下载空白 CSV 模板」，填好后「附加 CSV 文件」上传。单文件最多 15 万行、35 MB；不能自己加列；非 ASCII 需 UTF-8。CSV 创建的账号不发欢迎邮件，凭据要管理员自己发。[^gw] | First Name、Last Name、Email Address、Password（必填），其余可选 | 出错时改 CSV 再传一次 |
| Microsoft 365 | 管理中心「用户 > 活跃用户 > 添加多个用户」，可下载空模板或带示例数据的模板；列名不能改，User Name 必须是完整邮箱地址；之后一步分配许可证，最后给出结果报告。[^ms][^ms-sample] | User Name（完整地址）、First/Last/Display Name 等 | 导入后显示结果，可下载 |
| 飞书 / 飞书邮箱 | 管理后台「组织架构 > 成员与部门 > 成员 > 批量导入/导出」，下载空模板并按「填写须知」填写，上传后创建账号、设置部门；也支持「导出 → 修改 → 导入」做批量修改。[^fs-bulk][^fs-import] 邮箱地址在「成员与部门」里分配。[^fs-mail] | 姓名、手机号/邮箱、部门、企业邮箱地址等 | 导入后给出失败明细 |
| 腾讯企业邮 | 「通讯录 > 批量导入」，下载批量导入模板，按指引填写并导入到指定部门；导入成功后选择「邀请」，系统通知成员登录。[^qq-help][^qq-guide] | 帐号（邮箱前缀）、姓名、手机、部门、职务等 | 结果在「系统日志 > 批量任务」里看 |

[^gw]: https://knowledge.workspace.google.com/admin/users/add-or-update-multiple-users-from-a-csv-file
[^ms]: https://support.office.com/article/1f5767ed-e717-4f24-969c-6ea9d412ca88
[^ms-sample]: https://www.microsoft.com/download/details.aspx?id=45485
[^fs-bulk]: https://www.feishu.cn/hc/zh-CN/articles/360042714814
[^fs-import]: https://www.feishu.cn/hc/zh-CN/articles/360046543134
[^fs-mail]: https://www.feishu.cn/content/how-to-set-up-company-email
[^qq-help]: https://service.exmail.qq.com/cgi-bin/help?id=20012&no=1000683&subtype=1
[^qq-guide]: https://main.qcloudimg.com/raw/document/product/pdf/613_46019_cn.pdf

**共同点，Zmail 照做：**

- 都先给一个**可下载的模板**，固定列名。
- 都是**导入 = 开账号 + 分配邮箱地址**一步完成，地址默认用前缀 + 企业域名。
- 都在导入后给**逐行结果**；出错的行改完再传。

**Zmail 的不同：**

- Zmail 不替用户设密码（Google、腾讯要填密码或发短信）。每个人收到邀请邮件，用自己的账号接受，邮箱在接受时自动归他。这样管理员永远不经手同事的密码。
- 在提交前就**预览校验**（浏览器先查格式，服务端再用 `dry_run` 查域名、地址占用、是否已是成员），比「传了再看报错」少一轮。
- **只重试失败的行**：同一份文件再传一次也安全（幂等），已处理的行会显示「已跳过」。

## 3. 方案

### 3.1 CSV 格式

```csv
email,name,role,mailbox
alice@gmail.com,Alice Wang,member,alice
bob@outlook.com,Bob Li,admin,
```

| 列 | 必填 | 说明 |
|---|---|---|
| `email` | 是 | 受邀人的登录邮箱（他们用这个地址注册 / 登录 Zmail 并接受邀请） |
| `name` | 否 | 邮箱显示名，最多 120 字符 |
| `role` | 否 | `member`（默认）或 `admin`。不能导入 `owner` |
| `mailbox` | 否 | 邮箱前缀（`alice`）或完整地址（`alice@company.com`，必须是本组织已验证域名）。留空时取 `email` 的前缀 |

一次最多 500 行（和共享邮箱导入一致）。

### 3.2 接口

`POST /v1/member-imports`（需要组织上下文，只有 owner / admin 能调用）：

```json
{
  "dry_run": false,
  "domain": "company.com",
  "features": { "broadcasts": false, "contacts": true, "templates": true, "api_keys": false },
  "rows": [{ "email": "alice@gmail.com", "name": "Alice Wang", "role": "member", "mailbox": "alice" }]
}
```

- `domain`：邮箱建在哪个已验证的组织域名上（行里写完整地址时以行为准）。
- `features`：这批成员的功能授权；不传就用 `domain.DefaultMemberFeatures()`（联系人 + 模板）。admin 永远拥有全部功能。
- 每行返回一个结果：

| status | 含义 |
|---|---|
| `invited` | 新发出邀请，邮箱已为他预留 |
| `created` | 他已经是成员但还没有个人邮箱，直接建好并归他 |
| `skipped` | 无需处理：`already_member`（已是成员且已有个人邮箱）、`already_invited`（已有待接受邀请且邮箱已预留）、`mailbox_taken`（地址已被别人或共享邮箱占用） |
| `failed` | 失败并给出 `error_code`，例如地址格式错、域名未验证、重复行、邮箱数量到上限、角色不合法 |
| `valid` | 仅 `dry_run`：这一行提交后会被处理 |

### 3.3 邮箱预留与归属（不放松隔离）

- 导入时直接建一个**个人邮箱**，`owner_user_id` 为空，`pending_owner_email` 记受邀邮箱（小写）。
- 按现有 `MailboxAccessFor`：个人邮箱只有 owner 能读写；owner 为空时**没人能读**，owner / admin 只能管理（删除、看访问记录），看不到邮件内容。这条规则不改。
- 地址立刻占住、Cloudflare 路由规则立刻建好：在对方接受之前寄来的信也不会丢，接受后就能看到。
- 对方用**同一个邮箱**接受邀请（接受时本来就校验邮箱一致）后，组织模块发出 `member_joined` 事件，邮件模块把 `pending_owner_email` 等于该账号邮箱的预留邮箱归给他，清空预留，并写一条 `owner_assigned` 访问记录。
- 邀请被撤销或过期：邮箱保留在「待认领」状态，管理员可以删除，或重新导入同一行再次邀请（会复用这只邮箱）。

### 3.4 功能授权跟着邀请走

`organization_invitations` 加四列 `can_use_broadcasts / contacts / templates / api_keys`（已有邀请默认为联系人 + 模板，和新成员默认一致）。接受邀请创建成员关系时，把这四个值写进成员关系。普通「邀请成员」接口不变，仍用默认值。

### 3.5 幂等

服务端按状态对账，而不是按「这一行是否传过」：

1. 已是成员 → 有个人邮箱则跳过，没有则给他建；
2. 不是成员 → 有待接受邀请则复用，没有就新建；预留邮箱已存在且属于这个人则复用，不存在就建；
3. 两样都已经在了 → `skipped / already_invited`。

所以同一份文件再传一次，结果全是「已跳过」，不会重复发邀请、不会多建邮箱。中途某行因为 Cloudflare 报错失败，重试那一行会补上缺的那一半。

### 3.6 界面（组织页「成员」标签）

1. 「批量导入」按钮 → 对话框；
2. 第一步：下载 CSV 模板 / 选择文件 / 直接粘贴；选择域名，勾选功能授权（默认联系人 + 模板）；
3. 第二步：预览表格，服务端 `dry_run` 的结果逐行显示，有问题的行标红并写明原因；
4. 第三步：提交后显示进度和汇总（共 N 行：成功、跳过、失败及原因）；
5. 「重试失败的行」只把失败行再提交一次。
6. 没有已验证域名时，对话框说明原因并给出「去绑定域名」的入口。

## 4. 验收标准

- [ ] owner / admin 上传 `email,name,role,mailbox` CSV，预览能看到每行状态；member 调用接口返回 403。
- [ ] 一次提交后：每个新人收到邀请；每人在已验证域名上有一个预留的个人邮箱；邀请带着选定的功能授权。
- [ ] 被邀请人用同一邮箱接受后，成员列表显示所选功能；他在邮箱列表里只看到自己的邮箱。
- [ ] 接受前，owner / admin 看不到预留邮箱里的邮件内容；其他成员看不到这个邮箱。
- [ ] 同一文件传第二次：全部 `skipped`，不重复发邀请、不多建邮箱。
- [ ] 地址被占用、域名未验证、格式错误、重复行都会在对应行显示原因；只重试失败行可用。
- [ ] 中英文界面都没有遗漏的英文 / 中文；手机宽度（375px）下对话框不贴边。
- [ ] Go 单测、PostgreSQL 仓储测试、Vitest 全部通过；`make check` 通过。
