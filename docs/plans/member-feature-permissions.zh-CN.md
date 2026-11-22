# 成员功能授权（群发 / 联系人 / 模板 / API 密钥）

状态：已实现（本 PR）。负责人：组织模块 + 邮件模块。日期：2026-11-22。

## 1. 要解决的问题

组织只有 owner / admin / member 三种角色。在 `origin/main 3f35e68` 上：

| 功能 | 现状 |
|---|---|
| 联系人 | 任何成员都能增删改、导入（`contact_service.go` 不看角色） |
| 模板 | 任何成员都能编辑普通模板；事务模板只有 owner/admin 能改 |
| 群发 | 对发件邮箱有 `send` 权限的成员能建、改、删草稿；发送、暂停、取消、恢复只有 owner/admin 能做 |
| API 密钥 | 任何登录用户都能创建，密钥可以代表本人在任何所属组织里发信 |

所以 owner 没法对某个成员说“你可以做营销群发”，也没法说“你不能动客户通讯录”或“你不能用 API 发信”。

## 2. 用户场景

1. **市场同事做群发**：公司邮箱管理员给市场部的小王打开「群发」。小王能建群发、选模板和联系人列表、自己点发送。其他同事看不到「群发」入口。
2. **客服不碰通讯录**：客服只需要收发邮件。管理员关掉他的「联系人」，他就看不到客户名单，也无法导出。
3. **研发用 API 发信**：管理员给后端工程师打开「API 密钥」。没有授权的成员即使自己建了密钥，拿它请求本组织接口也会被拒（403）。
4. **新同事入职**：默认能用「联系人」和「模板」（日常写信需要）。「群发」和「API 密钥」能大批量发信，要等管理员手动打开。
5. **误操作有反馈**：没有授权的成员直接打开 `/console/broadcasts` 这类地址，看到的是“请联系管理员开通”的友好提示，不会白屏，也不会看到报错码。

## 3. 同类产品怎么做（调研）

| 产品 | 授权粒度 | 新用户默认 | 管理员 |
|---|---|---|---|
| Google Workspace | 每个服务可以按全组织、按组织部门（OU）或按访问组开关。新服务默认开还是关，由一个组织级设置决定。[^gw-service] 联系人共享是全组织一个开关，默认开。[^gw-contacts] 第三方应用访问 API 按 OU 设为 Trusted / Limited / Blocked。[^gw-api] | 跟随所在 OU 或组 | 超级管理员不受服务开关限制 |
| Microsoft 365 | 分配许可证时可以单独关掉其中的服务（`DisabledPlans`），粒度是单个用户、一组用户或全部用户。[^ms-plans] 每个邮箱挂一个角色分配策略和一个 OWA 邮箱策略。[^ms-rbac] 每个用户每天最多发给 10,000 个收件人，官方建议群发不要走邮箱，改用 Azure Communication Services。[^ms-limits] | 许可证里的服务默认全开 | 管理员角色和许可证分开管理 |
| 飞书 / Lark | 每个应用有「可用范围」，可以按组织、用户组或个人设置。「能用这个应用」和「应用能读哪些通讯录数据」是两回事。[^fs-range][^fs-contacts] | 由可用范围决定 | 主管理员拥有全部权限，子管理员按角色限定[^lark-admin] |
| Zoho Mail / Campaigns | 邮件策略可以按用户或按组分配，新用户套用默认策略，管理员也受策略约束。[^zoho-policy] Campaigns 把「创建活动」「发送活动」「导入联系人」「管理模板」拆成可以单独授予的权限，Editor 角色能编辑活动和模板，但不能发送。[^zoho-roles] | 默认策略 | Admin 拥有全部权限 |

[^gw-service]: https://knowledge.workspace.google.com/admin/users/advanced/turn-a-service-on-or-off-for-google-workspace-users
[^gw-contacts]: https://knowledge.workspace.google.com/admin/users/turn-directory-on-or-off
[^gw-api]: https://knowledge.workspace.google.com/admin/apps/control-which-apps-access-google-workspace-data
[^ms-plans]: https://learn.microsoft.com/en-us/microsoft-365/enterprise/disable-access-to-services-with-microsoft-365-powershell
[^ms-rbac]: https://learn.microsoft.com/en-us/exchange/permissions-exo/role-assignment-policies
[^ms-limits]: https://learn.microsoft.com/en-us/office365/servicedescriptions/exchange-online-service-description/exchange-online-limits
[^fs-range]: https://www.feishu.cn/content/u6ojuobh
[^fs-contacts]: https://www.feishu.cn/content/788114687974
[^lark-admin]: https://larksuite.com/hc/en-US/articles/360043595213-admin-add-administrators-and-create-administrator-roles
[^zoho-policy]: https://www.zoho.com/mail/help/adminconsole/email-policy.html
[^zoho-roles]: https://help.zoho.com/portal/en/kb/campaigns-3-0/user-guide/settings/users-and-controls/articles/v3-roles-and-permissions

**结论**

- 大厂主要靠「策略 / 组」批量授权，但 Microsoft 的服务计划和 Zoho 的策略分配都能落到单个用户。Zmail 的目标客户是几人到几十人的团队，先做按人开关最直接，以后再加「按组授权」也不冲突。
- 普通功能对新用户默认开。能大规模发信的功能（Campaigns 的发送权限、Microsoft 的群发）要单独授予。
- 管理员拥有全部功能，这和飞书主管理员、Zoho Campaigns Admin 的做法一致。

## 4. 方案选择

### 4.1 选项

| 选项 | 优点 | 缺点 |
|---|---|---|
| A. 复用 `permission` starter（RBAC：角色 + 权限键） | 已有表和接口 | 生产环境 `OPTIONAL_STARTERS=organization,webhook,mailbox,operator` 没有启用它，要运维改配置才能用。目录里只有四个 `permission.*` 键，邮件代码从没调用过它。按人开关还得先建角色再分配，对“给小王开群发”来说太绕 |
| **B. 在成员关系表上加四个布尔列（选这个）** | 跟着 organization starter 走，生产已启用，不需要任何配置。组织上下文中间件原本就是一次查询拿到成员关系，多读四列就有了授权，不多一次 SQL。语义直观，迁移简单 | 以后要加新功能就得加列。四个功能都是产品级大功能，增长会很慢，可以接受 |
| C. 另建 `member_feature_grants(membership_id, feature)` 表 | 加功能不改表结构 | 每个请求要多一次 join 或查询，还要处理行的增删，代码比 B 多 |

**选 B**：不需要生产配置，不增加请求开销，代码最少。

### 4.2 模型

- 功能目录（固定顺序）：`broadcasts`（群发）、`contacts`（联系人）、`templates`（模板）、`api_keys`（API 密钥）。
- 存储：`organization_memberships.can_use_broadcasts / can_use_contacts / can_use_templates / can_use_api_keys`，类型 `boolean NOT NULL`。
- **owner / admin 永远拥有全部功能**。授权只对 member 角色生效。接口返回的是「有效权限」：owner/admin 四项都是 `true`。
- admin 降为 member 后，按该成员存储的值生效（新晋升的 admin 一般就是新成员默认值）。

### 4.3 每项授权具体放行什么

| 授权 | 放行 |
|---|---|
| 群发 | `/mail-broadcasts*` 全部接口：草稿增删改、发送、暂停、取消、恢复、收件人列表。成员发送和恢复时仍要对发件邮箱有 `send` 权限；暂停和取消只能操作自己能发送的群发。owner/admin 照旧可以停掉任何群发。另外放行只读的模板读取和联系人列表名读取（选模板、选列表要用） |
| 联系人 | `/mail-contacts*`、`/mail-contact-imports`、`/mail-contact-lists*` |
| 模板 | 模板的新建、修改、删除、测试发送。模板读取（列表、详情、预览）对持有模板、群发或 API 密钥任一授权的成员开放，因为开发者页面要读事务模板 |
| API 密钥 | 所有用 API 密钥认证的组织接口（`/outbound-mail-messages`、`/agent-mail-*`、`/transactional-messages`）。密钥本身属于个人，`/api-keys` 的增删查不在任何组织下面，所以闸门放在**使用**这一步：没有授权的成员拿密钥访问本组织，一律 403 |

被拒绝时统一返回 `403 ORGANIZATION.FEATURE_NOT_GRANTED`。客户端按错误码分支，不看提示文字。

**行为变化（需要知晓）**：以前成员即使能建群发草稿，也不能自己发送。现在有群发授权的成员可以自己发送（仍要求对发件邮箱有 `send` 权限）。“允许某人做群发”本来就该包括发出去，只能写草稿、等管理员代发的半套权限，满足不了「管理员决定谁可以做营销」。

### 4.4 默认值和存量迁移

- 迁移 `2026_12_18_090000_add_member_feature_grants`：先以 `DEFAULT true` 加列，**存量成员四项全开**，保持今天能做的事不变。然后把群发和 API 密钥两列的默认值改成 `false`。
- **新成员默认**：联系人、模板开；群发、API 密钥关，等管理员打开（参考第 3 节：能大规模发信的功能单独授予）。
- 回滚（Down）直接删掉四列，所有成员回到“角色允许什么就能做什么”。

> 注意：存量成员拿到群发授权后，也获得了上面说的“自己发送”能力。管理员可以在成员页一键关掉。如果产品希望存量成员默认不能群发，只要把迁移里 `can_use_broadcasts` 的回填改成 `false`，改一行即可。

## 5. 接口（契约见 `contracts/ORGANIZATIONS.md`、`contracts/MAILBOXES.md`、`contracts/openapi.yaml`）

- `GET /organizations/:id/members` 和 `GET /organization-context` 的响应里新增 `features: {broadcasts, contacts, templates, api_keys}`，值为有效权限。
- `PATCH /organizations/:id/members/:member_id/features`
  - 请求体：`{"broadcasts": true}`，只改出现的字段，至少要有一个字段，否则返回 422。
  - 只有 owner/admin 能调用，其他人返回 403 `PERMISSION.DENIED`。目标必须是 member 角色，否则返回 409 `ORGANIZATION.MEMBER_FEATURES_FIXED`。成员不存在返回 404。
  - 成功返回更新后的成员，并写一条审计 `organization_members.change_features`，只记录真正变化的功能。
- 新错误码：`ORGANIZATION.FEATURE_NOT_GRANTED`（403）、`ORGANIZATION.MEMBER_FEATURES_FIXED`（409）。

## 6. Web 控制台

- **成员页**（组织 → 成员）：owner/admin 在每个 member 的行里看到四个开关：群发、联系人、模板、API 密钥。点击立即保存：保存中开关禁用并显示加载状态，成功弹出提示，失败时开关回滚并提示原因。owner/admin 的行显示“管理员拥有全部功能”，不显示开关。
- **导航**：成员缺哪项授权，就隐藏对应入口（群发 / 联系人 / 模板 / API 密钥）。
- **直接打开地址**：显示“此功能尚未为你开通，请联系组织管理员”的空状态，附回到控制台首页的按钮。
- **写信**：没有模板授权的成员看不到「插入模板 / 存为模板」。
- 文案有中英两份（`web/src/i18n`）。在 1440×900 和 375×812 两种尺寸、浅色和深色主题下都验证过。

## 7. 验收标准

1. 存量组织迁移后，所有成员能做的事和以前一样（迁移测试覆盖）。
2. 新成员默认只有联系人和模板。
3. owner/admin 在成员页切换开关后，被授权成员下一次请求立即生效，不需要重新登录。
4. 没有授权的成员：看不到对应入口；直接打开地址看到友好提示；直接调 API 返回 `403 ORGANIZATION.FEATURE_NOT_GRANTED`（覆盖群发、联系人、模板写操作、API 密钥调用）。
5. owner/admin 不受任何授权限制，接口上也改不了他们的授权（返回 409）。
6. 生产环境不需要任何新配置。
7. 测试：domain 单测、PostgreSQL 仓储测试（读写授权、上下文解析）、迁移测试、路由级闸门测试（每条受控路由在无授权、单项授权、管理员三种情况下）。

## 8. 以后可以做

- 按组 / 按部门批量授权（Google OU、飞书用户组的做法）。
- 邀请成员时就勾选初始授权。
- 「群发需审批」模式：成员只能写草稿，由管理员发送（Zoho Campaigns 的 Editor 角色）。
