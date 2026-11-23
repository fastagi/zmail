# 企业开通体验打磨（控制台首页 · 域名 · 邀请）

状态：已实现（Web），2026-11-22。范围只在 `web/`，没有 API 或契约变更。

## 1. 场景

| 谁 | 想做什么 | 卡在哪里（origin/main 3f35e68 实测） |
|---|---|---|
| 公司管理员，刚注册 | 从“账号建好”走到“公司域名验证通过、同事都已邀请、每人一个邮箱” | 首页先看到 8 张功能卡和脚手架内容（设计规范、国际化测试、“后续步骤：把 NEXT_PUBLIC_API_URL 指向…”）；“给你的团队起个名字”在最下面；问候语是“晚上好，朋友”，没用注册时填的名字 |
| 同上，正在连域名 | 知道要改什么、改到哪、要等多久、什么时候好 | 域名页把运营者内容给客户看：“Cloudflare 配置”清单、`MAILBOX_*` 环境变量、带 `YOUR_API_HOST` 的 curl；改 NS 后没有“接下来会发生什么”和完成提示 |
| 同上，邀请之后 | 看到谁加入了、谁没有，没加入的能再发 | 邀请页只有一张历史表，过期的邀请没有“重新邀请” |
| 个人用户 | 不想买域名，直接用 | 清单把域名列为“可选”放在最后，公司反而会漏掉这一步 |

## 2. 竞品怎么做（官方帮助文档）

- **Google Workspace**：设置向导顺序是加用户 → 验证域名 → 付款；首选 TXT 验证，生效后确认最多 1 小时；MX 页给 12 家注册商教程；入口就分 “Just you” 和团队。[FAQ](https://knowledge.workspace.google.com/admin/getting-started/google-workspace-setup-faq) · [验证域名](https://knowledge.workspace.google.com/admin/domains/verify-your-domain-for-google-workspace) · [MX](https://knowledge.workspace.google.com/admin/domains/set-up-mx-records-for-google-workspace)
- **Microsoft 365 Business**：向导是 添加并验证域名 → 添加用户 → 连接域名（DNS）；自动识别注册商，GoDaddy/Cloudflare 等支持 Domain Connect 一键写记录；强调“先建好邮箱再改 MX”；时间说“最多 10 分钟，部分注册商 48 小时”。[Business Standard 设置](https://learn.microsoft.com/microsoft-365/admin/setup/setup-business-standard) · [添加域名](https://learn.microsoft.com/en-us/microsoft-365/admin/setup/add-domain) · [任意 DNS 服务商](https://learn.microsoft.com/en-us/microsoft-365/admin/get-help-with-domains/create-dns-records-at-any-dns-hosting-provider)
- **飞书 / Lark**：注册即选“企业/团队”，当场邀请成员；域名管理里每条记录有“状态”列，全部通过后域名变为“已启用”；成员分“未加入 / 未激活”，可单个或批量“发送提醒”，24 小时一次；有阿里云、华为云、DNSPod、GoDaddy 图文教程。[开通邮箱](https://www.larksuite.com/hc/en-US/articles/808258782596) · [配置 DNS](https://www.larksuite.com/hc/en-US/articles/360048488073) · [提醒激活](https://www.feishu.cn/hc/zh-CN/articles/360043261094)
- **Zoho Mail**：注册分 Personal（免域名）和 Business；域名列表按阶段显示 “Yet to prove ownership → Yet to point MX → Yet to configure SPF/DKIM → Completed”；Invitees 页列出每个人的状态，可单个或批量重发；SPF/DKIM 说 4–48 小时。[添加域名](https://www.zoho.com/mail/help/adminconsole/add-domains.html) · [添加用户](https://www.zoho.com/mail/help/adminconsole/adding-users.html) · [个人邮箱](https://www.zoho.com/mail/how-to/create-an-email-account.html)

共性：先分个人/公司；域名紧跟在组织之后；每一步有自己的状态和时间预估；等待期可以离开，回来看得到进度；邀请列表分“已加入 / 未加入”并能重发。四家文档都没写“验证通过后发邮件通知”，这是我们以后可以做的差异点。

## 3. 选定的流程

首页顶部的**设置向导**（团队管理员可见，全部完成后自动消失）：

1. **创建团队**：没有团队时，向导第一步就是团队名表单，“创建团队”按钮一开始就在；创建后留在首页，提示下一步。
2. **连接公司域名**（服务端开启自有域名时才出现）：公司推荐；个人用户可点“个人使用，先用 @共享域名”跳过（记在本浏览器，可撤销）。等待中显示实际阶段：等改 DNS 服务器（“每 10 分钟自动检查”）/ 正在配置记录（约一分钟）/ 记录未就绪。
3. **创建你的邮箱**：域名已验证时按钮直接写“创建 @acme.com 邮箱”。
4. **邀请同事并为他们开邮箱**：显示“N 人已加入 · M 人未加入”和“K 位同事还没有邮箱”，给出“为他们开邮箱”和“查看谁已加入”。

为什么是 团队 → 域名 → 邮箱 → 邀请：邮箱地址取决于域名，先连域名，邮箱就不会先开在共享域名上再迁移；邀请不依赖域名，所以域名在等 DNS 时，其余步骤照样可点。

**域名页**：
- 先“连接你的域名”（无需 Token，适合大多数域名）；“DNS 已在你自己的 Cloudflare 账号里”折叠在下面（已连接时自动展开），并说明在 Cloudflare 注册的域名只能走这条路。
- 去掉运营者内容：Cloudflare 配置清单、环境变量、curl 示例、`MAILBOX_CUSTOM_DOMAINS_ENABLED` 提示。共享域名只讲“连接前地址以 @共享域名 结尾”和组织限额；测试环境才提示“不投递”。用 API 发信改为指向开发者中心。
- 每张域名卡在记录表下用一句话解释每类记录（MX 收信、SPF/DKIM 证明是你发的、DMARC 防冒充），并说明记录由 Zmail 自动写入。
- 等改 NS 时：两条 NS 可复制；“接下来会发生什么”三步（Cloudflare 发现：几分钟到几小时，最长 48 小时 → Zmail 写记录约一分钟 → 可以建邮箱；每 10 分钟自动检查，可关闭页面）；注册商教程增加 Cloudflare 和“其他注册商”，按原 NS 自动展开对应一家。
- 页面开着时，NS 生效、域名就绪都会弹出提示（就绪提示带“创建邮箱”按钮）。设置中每 3 秒、等 NS 时每分钟刷新列表（只读数据库，不调 Cloudflare）。

**邀请页**：顶部概况“N 人已加入 · M 人未加入 · K 份已过期”（按每人最新一次邀请计算）；未加入的排最前；过期或已撤销的邀请可一键“重新邀请”（再次邀请过的旧记录不再显示按钮）。

**其他**：问候语用注册时填写的名字（取自认证状态，任何认证模式都有），没有名字时用邮箱 @ 前的部分；控制台导航和首页不再链接设计规范、国际化测试（开发环境仍可直接访问）；“返回站点”降为次要按钮。

## 4. 验收标准

- [x] 首页没有“设计规范”“国际化测试”“后续步骤”；导航里也没有设计规范。
- [x] 新用户登录后，设置向导在首页最上方，团队名输入框和“创建团队”按钮立即可见；创建后向导前进到“连接公司域名”，有成功提示。
- [x] 问候语显示注册时的名字。
- [x] 向导顺序：团队 → 域名 → 邮箱 → 邀请；个人可跳过域名，跳过可撤销；服务端未开启自有域名时只有三步。
- [x] 域名在等 NS / 配置中 / 记录未就绪时，向导和域名页都显示对应阶段和剩余步骤；状态变化时弹出提示。
- [x] 域名页对组织用户不出现 `MAILBOX_*`、`YOUR_API_HOST`、Email Worker、Cloudflare 配置清单。
- [x] 每类 DNS 记录都有一句人话解释；NS 等待期有时间预估和阿里云、腾讯云、华为云、GoDaddy、Namecheap、Cloudflare、其他注册商的指引。
- [x] 邀请页显示已加入/未加入/已过期人数，未加入的排最前，可重发待接受的邀请、重新邀请过期的邀请，每个操作都有提示。
- [x] 中英文文案齐全；1440×900 和 375×812、浅色和深色下检查过首次使用流程，对话框边距正常。

## 5. 后续（不在本次范围）

- 域名就绪后给管理员发邮件 / 站内通知（需要 API：`mail:zone:work` 激活区域、域名验证通过时发通知）。
- 注册时直接问“团队/公司”还是“个人”，把跳过域名的选择存到服务端（现在只存在浏览器里）。
- 邀请时可直接为对方预建 @公司域名 邮箱，对方加入即可用（需要 API 支持“待加入成员的个人邮箱”）。
- 自有 Cloudflare 账号一键授权（类似 Domain Connect），省去粘贴 Token。
