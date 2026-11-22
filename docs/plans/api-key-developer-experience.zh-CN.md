# API 密钥开发者体验方案

目标：开发者注册 Zmail 后，一分钟内创建 API 密钥，并用它发出第一封邮件，全程不用猜。

## 现状问题（origin/main 3f35e68，本地实测）

1. 作用域（scope）是自由文本框，占位符还是脚手架留下的 `models:read` / `models:invoke`；空列表也能创建成功，得到一把什么都做不了的密钥。
2. 密钥前缀是 `fastbuild_`，不是 Zmail 品牌。
3. 「保存密钥」对话框在窄屏下贴着窗口边缘，375 px 宽时没有外边距、也没有圆角。
4. 设置页创建完密钥就结束了，没有调用示例。`/console/developers` 只讲事务邮件，而且只有开启事务邮件后才可用。
5. 用户还没有组织或可发信邮箱时，页面不提示，开发者要拿着密钥自己撞 404 才知道。

## 场景

- **S1 首次接入**：新用户已有组织和自己的邮箱，打开「设置 → API」，创建一把可发信的密钥，复制代码运行，在邮件日志里看到这封信。
- **S2 缺少前置条件**：用户没有组织，或者组织里没有自己可发信的邮箱。页面直接说明缺什么，并给出下一步的链接。
- **S3 最小权限**：只读同步服务只勾选 `mail:read`；AI 代理只勾选 `mail:agent`。
- **S4 老密钥**：已经发出去的 `fastbuild_` 密钥照常可用，不需要轮换。

## 同类产品怎么做（2026-11 查阅）

| 产品 | 密钥权限 | 只显示一次 | 前缀 | 发信前置条件 | 发完去哪看 |
|---|---|---|---|---|---|
| Resend | 两档：`full_access`（完全访问）和 `sending_access`（只能发信），后者可以限定到一个域名 | 是 | `re_` | 验证域名；测试可以用 `onboarding@resend.dev`，但只能发给自己 | Emails / Logs 页面，每把密钥可以跳到它的日志 |
| Postmark | Server Token 只管一个 server 的发信和查询；Account Token 做管理 | — | — | From 地址必须是已确认的发件签名；账号审核通过前只能发给已验证的域名 | Activity |
| SendGrid | 四种：Full / Custom（Restricted）/ Billing / 邮箱校验，权限不能超过创建者本人 | 是 | `SG.` | 验证发件身份，推荐做域名认证 | Email Activity Feed |

来源：
- Resend：[创建 API key](https://resend.com/docs/api-reference/api-keys/create-api-key)、[密钥管理](https://resend.com/docs/dashboard/api-keys/introduction)、[Node 快速开始](https://resend.com/docs/send-with-nodejs)、[测试域名限制](https://resend.com/docs/knowledge-base/403-error-resend-dev-domain)、[Emails 页](https://resend.com/docs/dashboard/emails/introduction)
- Postmark：[Account 与 Server Token](https://postmarkapp.com/support/article/1008-what-are-the-account-and-server-api-tokens)、[用 API 发信](https://postmarkapp.com/developer/user-guide/send-email-with-api)、[账号审核](https://postmarkapp.com/support/article/1084-how-does-the-account-approval-process-work)
- SendGrid：[API Keys](https://www.twilio.com/docs/sendgrid/ui/account-and-settings/api-keys)、[Node 快速开始](https://www.twilio.com/docs/sendgrid/for-developers/sending-email/quickstart-nodejs)、[Email Activity Feed](https://www.twilio.com/docs/sendgrid/ui/analytics-and-reporting/email-activity-feed)

共同点：

1. 权限是少数几个命名好的档位，用户勾选，不手写字符串。
2. 密钥带品牌前缀，一眼就能认出来，也方便密钥扫描工具识别。
3. 密钥只显示一次。
4. 先讲清楚发信前提（验证过的发件身份），再给出可以直接运行的示例。
5. 发完告诉用户去哪里看结果。

## 设计

**作用域改成复选框**：提供四个选项，各配一句白话说明，默认勾选「发送邮件」。

| 作用域 | 说明 |
|---|---|
| `mail:send` | 发送邮件：以你可发信的邮箱发出邮件 |
| `mail:read` | 读取已发邮件：查询这把密钥发出的邮件和投递状态 |
| `mail:transactional` | 事务邮件：按模板发送验证码、通知等系统邮件 |
| `mail:agent` | AI 代理：配合邮箱代理授权读取新邮件、起草回复 |

至少要选一个，前端和 API 都校验。API 只接受这四个作用域和 `*`：空列表或未知值一律返回 `422 COMMON.INVALID_INPUT`。已经存在的密钥保留原来的作用域。

**前缀**：新密钥使用 `zmail_` / `zmail_test_`。系统按整把密钥的哈希查找密钥，所以老的 `fastbuild_` 密钥不受影响。契约 `contracts/API_KEYS.md` 和 `openapi.yaml` 同步更新。

**对话框**：所有尺寸的对话框四周至少留 16 px，始终圆角居中；内容太长时在对话框内部滚动。直接修共享的 `DialogContent`，所有对话框一起受益。

**快速开始（设置 → API）**：

- 页面上常驻一张「发送第一封邮件」卡片，提供 curl / Node.js / Python 三种示例：
  - 地址是真实的 API 地址：配置了 `NEXT_PUBLIC_DEVELOPER_API_ORIGIN` 就用它，否则用当前站点，生产环境 `zmail.im/v1` 同源可用。
  - 带上真实的组织 ID。
  - `from` 用用户自己可发信的邮箱，有多个时可以下拉切换。
  - 收件人默认是用户自己。
- 卡片里解释 `Organization-Id` 请求头：密钥属于个人，这个请求头说明以哪个组织的身份发信。
- 卡片链接到「收发记录」`/console/emails`。
- 创建出带 `mail:send` 的密钥后，「保存密钥」对话框里直接展示填好真实密钥的同一套示例，复制就能运行。对话框关闭后不再保留明文。

**空状态引导**：

- 没有组织：提示「先创建组织」，链接到 `/console/organizations`。
- 有组织，但没有自己可发信的邮箱：管理员看到「创建邮箱」并链接到 `/console/mailboxes`；普通成员看到「请管理员为你分配邮箱」。

**开发者控制台**：事务邮件未开启时显示的「未启用」空状态，增加一个跳转到「设置 → API」普通发信快速开始的链接。

**不做**：管理员查看、吊销成员的密钥。这需要新增组织级的列表和吊销路由，还要定权限规则，和并行进行的成员权限工作重叠，另立任务。

## 验收标准

1. 创建对话框里没有自由文本作用域；默认勾选 `mail:send`；全部取消勾选后提交，前端提示错误；直接调用 API 传空列表或 `models:read`，返回 422。
2. 新密钥以 `zmail_` 开头；`fastbuild_` 老密钥仍能通过 `X-API-Key` 发信（有单元测试）。
3. 1440×900 和 375×812、浅色和深色下，创建和保存密钥的对话框四周都有留白、居中、不溢出。
4. 有组织和邮箱时，创建密钥后对话框里的 curl 示例原样复制到终端，能在本地 API 上发信成功（201），并且能在「收发记录」里看到这封信。
5. 没有组织、没有可发信邮箱这两种情况，各自显示对应的提示和链接。
6. 中英文文案齐全；`make check` 通过。
