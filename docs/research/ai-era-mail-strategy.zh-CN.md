# AI 时代的邮箱怎么做：Zmail 的竞争力与"啊哈时刻"

2026 年 11 月 23 日 · 调研报告 · 草案，待产品评审

依据：竞品官网、定价页、更新日志，以及 Hacker News、Product Hunt、Trustpilot、G2 上的用户评价（价格均为 2026-11-23 当天所见）；本仓库的 `CONTEXT.md`、`contracts/MAILBOXES.md`、`docs/plans/mail-connectors-roadmap.zh-CN.md`、`docs/plans/ai-triage.zh-CN.md`、`docs/plans/mail-agent-grants.plan.md`、`docs/PRODUCT_REQUIREMENTS.zh-CN.md`，以及 issue #98。

**调研局限**：调研工具访问不了 Reddit，用户声音主要来自 HN、Product Hunt、Trustpilot、G2 和媒体评测。只有一个来源、或只有厂商自述的信息，都标为"未核实"。用户原话一律不超过 15 个词。

---

## 0. 一页结论

1. **市场在发生的变化**：价值正从"更好的收件箱界面"转向"替人处理邮件的 Agent"。证据有三条：
   - Notion 在 2026-09-22 关停了 Notion Mail，理由是超过一半用户不打开收件箱就处理邮件，所以改为 all in on agents。
   - Superhuman、Spark、Bloop、Missive、HEY 在 2026 年都把"让外部 Agent 操作邮箱"（MCP 或 CLI）做成了卖点。
   - Gmail 和 Outlook 的官方 MCP 都还在预览阶段。Gmail 的 MCP 只能存草稿，不能发信。
2. **没有人做全的组合**：没有一家同时具备以下五项：托管自有域名邮箱、接入现有 Gmail/Outlook/IMAP、人用的团队共享邮箱、Agent 独立授权、事务邮件。最接近的两家：
   - Resend：2026-10-22 发布 Inboxes，缺"接入现有邮箱"，且仍是私测。
   - Nylas：缺人用界面，也不是发信服务（ESP）。
   Zmail 五项都已有雏形。
3. **三类用户共同的最大痛点是信任，不是 AI 写得好不好。** 用户想要的是：Agent 能读但不能发，能起草但要人放行，每一步可审计、可撤回。现有的 OAuth 权限给不出这种粒度。团队的第一痛点也不是 AI，而是"这封信归谁、谁回了"。
4. **推荐的啊哈时刻**："几分钟内，你自己的 Agent（Claude Code、Codex 等）在团队邮箱里替你起草了第一封回复，你点一下就用公司地址发出去。它看不到验证码，也碰不到别的邮箱。"
5. **关键的策略转向**：D2 之所以卡住，是因为缺 AI 服务商密钥。改为**先支持用户自带 Agent（BYO Agent）**：Zmail 提供 MCP 和"待审草稿"放行，模型由用户的 Agent 自己出。这样不需要我们的模型密钥，啊哈时刻就能上线。内置 AI 分流放到第二步。
6. **未来两个季度的前三个优先级**：
   - (1) Zmail MCP 服务器 + 待审草稿一键放行（建在 D1 之上，提前交付 D2 的"确认→发送"半段）；
   - (2) 从注册到"Agent 第一封草稿"的引导路径，目标 10 分钟以内；
   - (3) 信任层：入站 SPF/DKIM/DMARC 判定、Agent 发信策略（收件人白名单、限速、审批）、审计导出。

---

## 1. 竞品调研

### 1.1 总览

| 类别 | 产品 | 一句话定位 | Agent/MCP 开放程度 | 价格（2026-11-23） |
|---|---|---|---|---|
| AI 客户端 | Superhuman | 高端键盘流客户端，转向团队和销售 | Mail MCP（Claude、ChatGPT），仅 Business 及以上；发信默认逐次批准 | Starter $25、Business $33/人/月（年付） |
| AI 客户端 | Shortwave + Tasklet | 只支持 Gmail 的 AI 客户端；Tasklet 做无人值守的自动化 | 可经 MCP 连接外部工具；Tasklet 是独立产品 | Business $30、Premier $45、Max $120/席/月 |
| AI 客户端 | Spark（Readdle） | 大众多账号客户端，主打"Agent-ready inbox" | Spark CLI + 开源 skills；每个账号可设关闭/只读/分拣/发送；只读免费，分拣要 Pro；**必须开着桌面 App** | Plus €10、Pro €22/月 |
| AI 客户端 | Bloop.email | macOS 原生、零服务器、多账号 | 内置 MCP；按账号授权；默认不能发信；对 AI 隐藏回收站和登录验证码 | 一次性 $4.99，BYOK |
| AI 客户端 | Notion Mail | Notion 生态的 Gmail 客户端 | **已于 2026-09-22 关停**，转向 Notion Agent + Gmail 连接器 | — |
| AI 客户端 | Inbox Zero | 开源 AI 邮件助理 | 可自托管；仓库里有 MCP 插件和 CLI | $20–40/人/月 |
| AI 客户端 | Fyxer | AI 执行助理（邮件、会议、知识库），按积分计费 | 官网列出 MCP；完整 API 仅 Enterprise | Pro $37、Team $64/人/月（年付） |
| 平台内置 | Gmail + Gemini | AI Inbox、AI 概览、Help Me Write | 官方 Gmail MCP 处于 Developer Preview，**没有发信工具**；AI Inbox 没有管理员开关；仅面向美国英文用户 | 基础功能免费，AI Inbox 需付费套餐 |
| 平台内置 | Outlook + Copilot | Frontier 预览：批量分拣、代为执行 | Mail MCP 能发信，但处于预览，且已标为只做向后兼容；分拣只支持英语 | Copilot Business $18–21/人/月 |
| Agent 基础设施 | AgentMail（YC） | 给 Agent 开新邮箱的 API | API、SDK、MCP、WebSocket；**不能接入已有邮箱，也没有人用界面** | 免费 3 个；$20/10 个；$200/150 个 |
| Agent 基础设施 | Nylas Agent Accounts | 老牌统一邮件 API，加上 Agent 自有邮箱（2026-06 正式发布） | API、CLI、托管 MCP；同时能接入现有邮箱；**没有人用的共享邮箱** | Pro $49/月含 50 个 Agent 账号 |
| Agent 基础设施 | Cloudflare Email Service | 发信 public beta（2026-04），入站路由免费 | Workers binding、MCP、`onEmail`；**不托管邮箱**，开源的 Agentic Inbox 要部署在自己账号里 | 3,000 封/月，之后 $0.35/千封 |
| Agent 基础设施 | Resend（含 Inboxes） | 开发者事务邮件服务；Inboxes 是人与 Agent 共用的托管收件箱 | API、CLI、MCP、webhook，内置 Agent；支持分配会话；**没有接入现有邮箱**；私测 | 额外收件箱 $1/个；事务邮件 $20 起 |
| Agent 基础设施 | Hostinger Agentic Mail | 低价托管企业邮箱 + Agent 接口 | API、webhook、MCP | 约 €0.39/邮箱/月（厂商自述） |
| 共享邮箱 | Front | 客户运营协作平台 | 官方 MCP（以用户身份操作）；Autopilot 按会话计费 | $25–105/座；Autopilot 每会话 $0.39（据单一来源，一周内从 $0.05 涨上来） |
| 共享邮箱 | Missive | 中小团队协作邮件客户端 | 官方托管 MCP，能发送回复；支持 BYOK | $14–36/座 |
| 共享邮箱 | Zoho Mail + TeamInbox | 低价托管邮箱 + 单独售卖的共享收件箱 | Zoho Mail MCP；能力分散在多个产品里，没有 Agent 身份的概念 | 邮箱 $1–6/用户 |
| 共享邮箱 | HEY for Domains | 强主张的邮件服务 | HEY CLI 和 MCP，**只存草稿，从不发信** | 首个用户 $10/月，之后每人 $12/月 |

### 1.2 AI 邮件客户端

**Superhuman**
- 定位：被 Grammarly 收购后成为 Superhuman Suite 的一部分，主打速度，产品方向向销售团队倾斜。
- 目标用户：高收入个人、创始人、BD 团队。
- 核心 AI：Auto Drafts 2.0（2026-07）。官方自报 60% 的草稿原样发出。另有 Ask AI 和自动标签。
- Agent 开放：Mail MCP 可以搜索、总结、起草和发送，每次发送都要用户批准；仅 Business 及以上可用。
- 用户夸：速度快，AI 草稿好用。TechCrunch 评价它"almost makes me like AI replies"。
- 用户骂：贵，而且越来越贵；功能被移到更高档；最强的 AI 锁在 Business 档。
- 来源：[定价](https://superhuman.com/plans/mail)、[MCP](https://superhuman.com/products/mail/mcp)、[TechCrunch](https://techcrunch.com/2026/07/14/superhumans-new-auto-draft-feature-almost-makes-me-like-ai-replies/)、[PH 评价](https://www.producthunt.com/products/superhuman/reviews)、[HN](https://news.ycombinator.com/item?id=23614167)

**Shortwave + Tasklet**
- 定位：只支持 Gmail，AI 搜索和团队协作做得最深。官网公开写明用的是 Claude Sonnet 和 Opus 两档模型。
- Tasklet 是同一团队的独立产品，有人称它为"Agent 时代的 IFTTT"，支持定时、邮件、webhook 三种触发。Sacra 称它的增速已超过 Shortwave（未核实）。
- 用户夸：长邮件摘要好用；AI 嵌在原有操作里，而不是一个聊天框。
- 用户骂：智能标签过度激进；AI 会承诺它做不到的事；只支持 Gmail。
- 来源：[定价](https://www.shortwave.com/pricing)、[Tasklet](https://www.shortwave.com/blog/introducing-tasklet-ai-automation/)、[PH 评价](https://www.producthunt.com/products/shortwave-6/reviews)

**Spark**
- 定位：大众多账号客户端。2026-05 发布 Spark CLI，Agent 通过它读取本地数据。
- 权限：每个账号分四级（关闭、只读、分拣、发送）。
- MacStories 认为它比 Google 官方的 workspace CLI 好上手得多，因为不需要 OAuth 或 GCP 项目。
- 用户骂：桌面 App 必须一直开着；分拣功能锁在 Pro；用户长期担心凭据存在 Spark 的服务器上。
- 来源：[Spark CLI](https://sparkmailapp.com/blog/introducing-spark-cli)、[MacStories](https://www.macstories.net/reviews/spark-mail-adds-a-mac-cli-and-agent-skills/)、[HN](https://news.ycombinator.com/item?id=38960650)

**Bloop.email**
- 定位：macOS 原生，官网写的是"No Bloop server … not even a sign-up"。一次性买断 $4.99，自带模型 Key 或用本机的 Apple Intelligence。
- MCP：按账号授权，默认不能发信，对 AI 永远隐藏回收站和登录验证码。
- 疑点：官网前后矛盾。MCP 段落说授权后能发信，FAQ 又说 AI 不能发信、不能移动邮件。
- 用户评价：没有找到（未核实）。`bloop` CLI 也没有核实到。
- 来源：[官网](https://bloop.email)

**Notion Mail（已关停）**
- 2025-04 发布，2026-06-25 宣布关停，2026-09-22 正式关闭。官方理由是超过一半用户不打开收件箱就处理邮件。
- 关停前的用户抱怨：只支持 Gmail；"只是 Gmail 的漂亮外壳"。
- **启示**：大厂主动放弃了"做收件箱界面"这条路。
- 来源：[Notion 帮助中心](https://www.notion.com/help/notion-mail-inbox-is-going-away-what-to-do-next)、[Android Authority](https://www.androidauthority.com/notion-mail-is-shutting-down-3681674/)、[HN](https://news.ycombinator.com/item?id=43694310)

**Inbox Zero**
- 定位：开源（约 1.3 万 star），能力包括用自然语言写规则、拦截冷邮件、批量退订、回复跟踪，可在 Slack 或 Telegram 里审草稿。
- 用户夸：几分钟清掉多年积压；可以免费自托管。
- 用户骂：退订列表不准；自托管依赖太多。
- 来源：[GitHub](https://github.com/elie222/inbox-zero)、[PH](https://www.producthunt.com/products/inbox-zero-2/reviews)

**Fyxer**
- 定位：AI 执行助理，改成按积分计费（分拣每封 0.2 积分）。
- 用户夸：草稿"right on point"。
- 用户骂：Trustpilot 上集中投诉计费，包括未经同意自动加人扣费、退款难、客服是机器人；草稿大约只有一半能直接用。
- 来源：[定价](https://www.fyxer.com/pricing)、[Trustpilot](https://www.trustpilot.com/review/fyxer.com?page=2)、[G2](https://www.g2.com/products/fyxer/reviews)

**Gmail + Gemini**
- 定位：2026-01 宣布"进入 Gemini 时代"，AI Inbox 只向美国英文的付费用户推送。
- 用户骂：没有管理员开关，关不掉；不信任隐私和训练政策；Gemini 搜邮件被评为"incredible how bad it is"。
- 来源：[Google 博客](https://blog.google/products-and-platforms/products/gmail/gmail-is-entering-the-gemini-era/)、[Workspace 更新](https://workspaceupdates.googleblog.com/2026/10/gmails-ai-inbox-is-rolling-out-to-paid-Workspace-users.html)、[HN 1](https://news.ycombinator.com/item?id=45004736)、[HN 2](https://news.ycombinator.com/item?id=50429426)

**Outlook + Copilot**
- 定位：Frontier 预览阶段的代为执行能力，默认开启"Allow actions"。
- 用户骂：关掉之后会自己回来；2026-02 出现过无视 DLP、摘要机密邮件的事件；分拣只支持英语。
- 来源：[微软博客](https://techcommunity.microsoft.com/blog/outlook/copilot-in-outlook-new-agentic-experiences-for-email-and-calendar/4514601)、[The Register](https://forums.theregister.com/forum/all/2026/02/18/microsoft_copilot_data_loss_prevention/)、[HN](https://news.ycombinator.com/item?id=47060202)

**其他新玩家**
- Cora（Every）：收件箱幕僚长，每天两次简报，草稿只暂存不发，约 $15/月（未核实）。
- Jace：改为积分制后，年付用户觉得额度被缩水。
- Zero（0.email，YC）：开源 MIT 协议，有 MCP，商业化情况未核实。
- Postage、Sickle、bytemail：Mac 本地 MCP 客户端已经形成一个小赛道（未核实）。

### 1.3 面向 Agent 和开发者的邮件基础设施

**AgentMail（YC，2026-03 种子轮 600 万美元）**
- 定位：给 Agent 一个有状态的收件箱，有线程、标签、草稿。
- 开放程度：全套 API、MCP 和 WebSocket。
- 缺口：不能接入已有邮箱；也没有人用的界面，只有控制台。
- HN 上有人质疑一键开收件箱会助长垃圾邮件，也有人问它怎么防提示注入。
- 来源：[定价](https://www.agentmail.to/pricing.md)、[Show HN](https://news.ycombinator.com/item?id=44745820)、[TechCrunch](https://techcrunch.com/2026/03/10/agentmail-raises-6m-to-build-an-email-service-for-ai-agents/)

**Nylas Agent Accounts**
- 定位：2026-06 正式发布，口号是"Not a borrowed one"，也就是 Agent 拥有自己的邮箱和日历。
- 能力：可托管自有域名的 MX 和 SPF/DKIM/DMARC；有限速、白名单、发送配额等策略控制。
- 缺口：没有人用的共享收件箱。
- 用户骂：客服是机器人；合同条款严。
- 来源：[定价](https://nylas.com/pricing/)、[GA 更新日志](https://developer.nylas.com/docs/changelogs/2026-06-17-agent-accounts-ga/)、[G2](https://www.g2.com/products/nylas/reviews)

**Cloudflare Email Service**
- 能力：发信 public beta；入站路由免费；Agents SDK 提供 `onEmail`；开源了 Agentic Inbox 参考应用（约 7.8k star，未核实）。
- 缺口：不托管邮箱，存储要自己做。
- 对 Zmail 的意义：Zmail 本身就跑在 Cloudflare 投递上，这是我们的供应商，不是竞争对手。
- 来源：[博客](https://blog.cloudflare.com/email-for-agents)、[定价](https://developers.cloudflare.com/email-service/platform/pricing/)、[Agentic Inbox](https://github.com/cloudflare/agentic-inbox)

**Resend Inboxes（最直接的威胁）**
- 发布时间：2026-10-22。
- 能力：在客户自己的域名上提供人与 Agent 共用的收件箱，支持分配会话和备注，内置可配置的 Agent，有 MCP 和 skills，还保留原有的事务邮件业务。
- 定价：额外收件箱每个 $1。
- 状态：文档写明仍是私测、只对少数用户开放。
- 缺口：没有接入现有 Gmail/Outlook，也没有个人与共享之间的隐私隔离。
- 来源：[发布博文](https://resend.com/blog/announcing-inboxes)、[产品页](https://resend.com/inboxes)、[定价](https://resend.com/pricing.md)

**Postmark、Mailgun**
- 都是传统 ESP，MCP 只覆盖发信和诊断。
- 2025 年 npm 上出现过冒充 postmark-mcp 的恶意包，会把邮件密送给外部服务器。这对"邮件 MCP"整个品类都是供应链警示。
- 来源：[Postmark 定价](https://postmarkapp.com/pricing)

**官方 MCP**
- Gmail MCP：Developer Preview，共 10 个工具，没有发送工具，只能建草稿。Google 自己也提醒，不要让 MCP 客户端处理来源不明的邮件。
- Microsoft Mail MCP：能发信，但处于预览，已被标为只做向后兼容，新项目要改用 Work IQ。
- 来源：[Gmail MCP](https://developers.google.com/workspace/gmail/api/guides/configure-mcp-server)、[Microsoft Mail MCP](https://learn.microsoft.com/en-us/microsoft-agent-365/mcp-server-reference/mail)

### 1.4 团队共享邮箱

**Front**
- 定价：$25–105/座。
- Autopilot 价格：据价格追踪器，2026-11 第二周从每会话 $0.05 涨到 $0.39（单一来源）。
- 用户评价：Trustpilot 1.5 分，主要抱怨涨价、必须年付、Outlook 不再双向同步。G2 上对共享收件箱本身的评价很高（4.7）。
- 来源：[定价](https://front.com/pricing)、[MCP](https://dev.frontapp.com/docs/mcp-server)、[Trustpilot](https://www.trustpilot.com/review/frontapp.com)、[价格变动](https://pricingsaas.com/companies/front/diffs/2026W46)

**Missive**
- 定价：$14–36/座。
- 能力：支持自带 Key（BYOK）；官方托管 MCP 能发送回复；不托管邮箱。
- 用户夸：分配会话、线程内聊天。
- 用户骂：通知太吵；涨过价。
- 来源：[定价](https://missiveapp.com/pricing)、[MCP](https://missiveapp.com/docs/ai/mcp/server)、[Capterra](https://www.capterra.com/p/152884/Missive/reviews)

**Zoho**
- 名义上最接近五项全有，但能力分散在 Mail、TeamInbox、ZeptoMail 和 MCP 控制台几个产品里，没有 Agent 身份的概念。
- 来源：[Zoho Mail MCP](https://www.zoho.com/mail/help/mcp/getting-started.html)、[TeamInbox 定价](https://www.zoho.com/teaminbox/pricing.html)

**HEY for Domains**
- MCP 只存草稿、从不发信。
- 不能接入 Gmail 或 Outlook，只能转发进来。
- 用户骂：开始取消后仍被扣了全年费用。
- 来源：[HEY Agents](https://hey.com/agents)、[Trustpilot](https://www.trustpilot.com/review/hey.com)

### 1.5 五项能力对照：没人做全

| | 托管自有域名 | 接入现有 Gmail/Outlook/IMAP | 人用共享邮箱 | Agent 独立授权 | 事务邮件 |
|---|---|---|---|---|---|
| Resend（Inboxes） | 有 | 无 | 有（私测） | 部分：内置 Agent，权限与人的权限不挂钩 | 有 |
| Nylas | 有（Agent 账号） | 有 | 无 | 有（策略控制） | 部分 |
| AgentMail | 有（只给 Agent） | 无 | 无 | 有 | 部分 |
| Front / Missive | 无 | 有 | 有 | 无（Agent 以人的身份操作） | 无 |
| Zoho | 有 | 部分 | 有（另一个产品） | 无 | 有（另购） |
| Superhuman / Spark / Bloop | 无 | 有 | Spark 部分有 | MCP 或 CLI 绑定个人 | 无 |
| **Zmail（现状）** | **有**（Cloudflare，多域名） | **有**（Gmail、Outlook、IMAP、租户接入、推送） | **有**（三级权限、分配、内部备注、交接） | **有**（D1：按邮箱授权，权限不超过授权人，验证码默认遮罩，读取审计，草稿待审） | **有**（事务邮件 API、模板、webhook） |

**价格锚点**：Agent 收件箱已被压到每个 $0.2–2/月，事务邮件每千封 $0.35–1.80。单卖其中一块没有溢价空间。**能收溢价的是这五块组合起来之后的治理能力**：谁的 Agent、能碰哪个邮箱、能做什么、由谁放行、留下什么记录。

---

## 2. 痛点：三类用户，按频次 × 强度排序

频次指在不同来源中被独立提起的次数，强度指情绪激烈程度或金钱、时间成本。来源中看不到 Reddit，结论偏向 HN 和英文用户。

### 2.1 个人

| # | 痛点 | 频次 | 强度 | 原始来源 |
|---|---|---|---|---|
| A1 | 不敢把整个收件箱交给 AI：怕被训练、被人工标注，而且授权后能读通常也能发 | 高 | 高 | [HN 45015230](https://news.ycombinator.com/item?id=45015230)、[HN 50429426](https://news.ycombinator.com/item?id=50429426)、[HN 47060202](https://news.ycombinator.com/item?id=47060202) |
| A2 | AI 草稿"不像我"，最后还得重写 | 高 | 中 | [HN 45015230](https://news.ycombinator.com/item?id=45015230)（"always end up significantly editing"）、[TechCrunch](https://techcrunch.com/2026/07/14/superhumans-new-auto-draft-feature-almost-makes-me-like-ai-replies/) |
| A3 | 摘要被邮件里的隐藏指令操纵，助手变成钓鱼的帮凶 | 中 | 高 | [HN 44528382](https://news.ycombinator.com/item?id=44528382)、[KnowBe4](https://blog.knowbe4.com/are-your-ai-assistants-the-next-attack-surface)、[Malwarebytes](https://www.malwarebytes.com/blog/news/2026/10/phishing-emails-are-baiting-your-ai-assistant) |
| A4 | AI 只会分拣和摘要，不能真正把邮件处理完 | 中 | 中 | [Cal Newport](https://calnewport.com/why-cant-ai-empty-my-inbox/) |
| A5 | 多账号聚合越来越难：Gmail 停止 POP 抓取，Gmailify 下线 | 中 | 中 | [The Register](https://www.theregister.com/2026/01/05/gmail_dropping_pop3/)、[TidBITS](https://tidbits.com/2026/01/12/gmail-to-end-pop-fetching-and-gmailify-features/)、[HN 46840144](https://news.ycombinator.com/item?id=46840144) |
| A6 | AI 客户端太贵（$16–50/月），积分制计费不透明 | 中 | 中 | [HN 26959449](https://news.ycombinator.com/item?id=26959449)、[Fyxer Trustpilot](https://www.trustpilot.com/review/fyxer.com?page=2) |
| A7 | 通讯和营销邮件淹没了真正要回的信 | 中 | 低到中 | [HN 42898051](https://news.ycombinator.com/item?id=42898051) |
| A8 | 验证码和魔法链接：慢、会过期、要跨设备找 | 中 | 低到中 | [HN 42627453](https://news.ycombinator.com/item?id=42627453)、[HN 47336889](https://news.ycombinator.com/item?id=47336889) |

### 2.2 团队和小型组织

| # | 痛点 | 频次 | 强度 | 原始来源 |
|---|---|---|---|---|
| B1 | 共享邮箱没人认领，不知道谁回了什么。原话："no one knew who was going to answer what" | 高 | 高 | [HN 42119042](https://news.ycombinator.com/item?id=42119042)、[Missive 博客](https://missiveapp.com/blog/google-groups-collaborative-inbox) |
| B2 | 自有域名发信进垃圾箱；SPF/DKIM/DMARC 难配，配齐了也不保证进收件箱 | 高 | 高 | [HN 50488230](https://news.ycombinator.com/item?id=50488230)、[HN 28194414](https://news.ycombinator.com/item?id=28194414) |
| B3 | 按席位计费贵：Workspace 捆绑 AI 涨价，Front 这类工具"get expensive, fast" | 高 | 中到高 | [HN 42710978](https://news.ycombinator.com/item?id=42710978)、[PCWorld](https://www.pcworld.com/article/2578255/google-couldnt-sell-ai-for-workspace-so-it-upped-the-price-and-bundled.html)、[HN 42119042](https://news.ycombinator.com/item?id=42119042) |
| B4 | Google 原生的共享收件箱和群组功能残缺，被评为"delegation with a new coat of paint" | 中 | 中 | [HN 45955559](https://news.ycombinator.com/item?id=45955559)、[HN 35070618](https://news.ycombinator.com/item?id=35070618) |
| B5 | 员工离职：邮箱转共享、存档、许可证、数据外泄 | 中 | 中到高 | [Microsoft 文档](https://learn.microsoft.com/en-us/microsoft-365/admin/add-users/remove-former-employee-step-4)、[Tech Community](https://techcommunity.microsoft.com/discussions/microsoft-365/whats-the-best-way-to-handle-when-an-employee-leaves-the-company-onedrive-and-em/310254)、[HN 31738423](https://news.ycombinator.com/item?id=31738423) |
| B6 | 客户绕过团队邮箱，直接给员工的个人地址发信 | 低 | 中 | [HN 42119042](https://news.ycombinator.com/item?id=42119042) |

### 2.3 开发者和 AI Agent

| # | 痛点 | 频次 | 强度 | 原始来源 |
|---|---|---|---|---|
| C1 | 邮件是提示注入的载体，Agent 被诱导去越权操作 | 高 | 高 | [HN 44205881](https://news.ycombinator.com/item?id=44205881)、[HN 44673613](https://news.ycombinator.com/item?id=44673613)、[Barracuda](https://blog.barracuda.com/2026/10/23/indirect-prompt-injection-email-ai-assistants) |
| C2 | Gmail 受限权限要过每年一次的 CASA 安全评估：贵、慢、不透明 | 高 | 高 | [HN 46812608](https://news.ycombinator.com/item?id=46812608)、[HN 41664281](https://news.ycombinator.com/item?id=41664281)、[Google 论坛](https://groups.google.com/g/google-apps-script-community/c/XgsHT2Fr7u8) |
| C3 | Agent 不经人批准就发信或删信（Meta 的 AI 安全总监遇到 Agent 批量删邮件） | 高 | 高 | [404 Media](https://www.404media.co/meta-director-of-ai-safety-allows-ai-agent-to-accidentally-delete-her-inbox)、[HN 48100227](https://news.ycombinator.com/item?id=48100227) |
| C4 | Agent 邮箱变成垃圾邮件机器，域名信誉崩掉 | 高 | 中到高 | [HN 48225596](https://news.ycombinator.com/item?id=48225596)、[HN 44745820](https://news.ycombinator.com/item?id=44745820) |
| C5 | 权限粒度太粗：能读就能发，没有"只起草"，也没有审计 | 中 | 高 | [HN 44205881](https://news.ycombinator.com/item?id=44205881)（"full access to act on my behalf"）、[HN 48896628](https://news.ycombinator.com/item?id=48896628)、[Drag](https://www.dragapp.com/blog/gmail-mcp-server/) |
| C6 | 用程序收信很难：SES 太底层，解析、线程、搜索都要自建 | 中 | 中 | [HN 44745820](https://news.ycombinator.com/item?id=44745820)、[HN 41614976](https://news.ycombinator.com/item?id=41614976) |
| C7 | Agent 注册服务时要收验证码，几个 Agent 共用一个邮箱会串码 | 中 | 中 | [AgentMail 博客](https://www.agentmail.to/blog/when-your-ai-agent-hits-2fa-or-an-email-otp)、[dev.to](https://dev.to/francofuji/why-every-ai-agent-stack-needs-an-email-verification-layer-1pai)、[HN 47572153](https://news.ycombinator.com/item?id=47572153) |

### 2.4 归纳

1. **信任是三类用户的最大公约数。** A1、A3、C1、C3、C5 本质上是同一个问题：用户想要"读、起草、发送"分级，人来放行，每一步都可审计、可撤回。行业的默认做法正在收敛到"Agent 起草，人发送"：Gmail MCP 不能发信，HEY 只存草稿，Superhuman 逐次批准。这已经是底线，不再是差异点。**真正的差异点在于把这条底线做到团队和组织层面**：别人的 Agent 能不能碰共享邮箱、谁来放行、员工离职后 Agent 怎么处理。
2. **团队的刚需是归属，而不是 AI。** B1 和 B5 排在 AI 草稿前面。
3. **Google 既是瓶颈也是机会。** CASA 年审、Gmailify 下线、Workspace 涨价，都在把用户推向一个不依赖 Gmail 的邮箱和域名层。
4. **投递率是硬门槛**，不管发信的是人还是 Agent。

---

## 3. Zmail 现在有什么（只读核对仓库，截至 main @2404f4de）

| 能力 | 现状 | 依据 |
|---|---|---|
| 托管自有域名 | Cloudflare 收发，多域名，组织默认域名，自助接入域名并解释失败步骤 | PR #202、#207、#225；`docs/plans/cloudflare-self-serve-domains.zh-CN.md` |
| 组织、成员、邮箱权限 | 私人和共享两类邮箱；共享邮箱三级权限（查看、回复、管理成员）；每人各自的已读状态；离职交接与封存；花名册批量导入；开发者角色 | `CONTEXT.md`、PR #210、#227 |
| 团队协作 | 会话分配和完成、内部备注、按分配筛选、签名、撤回发送、服务端草稿 | PR #156、#157、#161–#164 |
| 接入外部邮箱 | 访问方式与来源两个维度分开；个人、共享、租户三种接入方式；Gmail、Outlook、IMAP（QQ、163 预设）；推送订阅；接入的 IMAP 邮箱可经其 SMTP 回复 | 路线图 §3、PR #153、#155、#178、#215 |
| 统一收件 | 多来源同页显示，可按来源、日期、附件、分配筛选 | `unified-inbox.zh-CN.md`、PR #165 |
| 验证码遮罩 | 所有列表、摘要、详情默认遮罩，点"显示"才能看到原文，每次显示都留痕；送往 AI 的内容一律用遮罩后的版本 | `verification-mask.zh-CN.md` |
| AI 分流 | 数据表、开关、worker、改判、草稿接口都已就位，目前只有 `fake` 和 `disabled` 两种实现，**缺真实模型密钥** | `ai-triage.zh-CN.md` §9 |
| Agent 授权（D1） | 按邮箱授权，Agent 是"某个人的 API key"，权限不超过这个人；可按发件人、标签过滤；最长 90 天；游标拉取，可重放；读取审计；验证码默认遮罩；Agent 只能存待审草稿，永不发信；离职、交接、移出成员时自动撤销；Web 有 Agent 权限页和待审草稿接口 | `mail-agent-grants.plan.md`、`contracts/MAILBOXES.md` "Agent Grants"、`web/src/features/mailbox/*agent-grant*` |
| 事务邮件与群发 | 事务邮件 API、模板（Agent 可用 `templates:create` 创建）、webhook、联系人、退订、群发进度 | PR #203、#208、#222 |
| 客户端接入 | IMAP 只读网关、SMTP 提交、按设备的应用专用密码、Web Push | PR #214、#216、#218、#221 |
| 未做 | D2 Agent 任务；**MCP 服务器**；稍后提醒；单封定时发送；入站 SPF/DKIM/DMARC 判定（契约明文写着"目前不存"）；Google 登录（即将上线） | issue #98、契约 |

**关键判断**：Zmail 在"Agent 治理"这一层已经比所有竞品走得深：
- 按邮箱授权；
- 权限不超过授权人；
- 个人和共享的隐私隔离；
- 离职自动撤权；
- 验证码默认遮罩；
- 读取审计；
- 草稿待审。

**缺的是让用户在几分钟内摸到这一层的入口**：没有 MCP，没有"连上我的 Agent"引导，待审草稿也还没变成一键放行的主界面。

---

## 4. 结论

### 4.1 啊哈时刻：三个候选

| 候选 | 一句话（用户第一次几分钟内感受到的价值） | 目标用户 | 优点 | 代价和风险 |
|---|---|---|---|---|
| **A. "Agent 起草，我来放行"**（推荐） | "我的 Claude Code 在团队邮箱里替我起草了第一封回复，我点一下就用公司地址发出去了；它看不到验证码，也碰不到别的邮箱。" | 已经在用 Agent 的创始人、开发者和小团队（Zmail 现有用户画像中的"受托 Agent"和"运营"） | 一次体验就用上了五项组合（托管地址、团队邮箱、接入、Agent 授权、审计）；**不需要我们的 AI 密钥**（用户自带 Agent）；正好命中 A1、C1、C3、C5 这组最强的信任痛点；Resend 和 Nylas 做不到"接入现有邮箱 + 人用团队界面 + 权限与人挂钩" | 依赖用户已经有 Agent，人群较窄；要先做 MCP；第一封信从哪来需要设计（见 §4.3） |
| B. "一页看完所有邮箱，AI 已经告诉你今天要回哪 3 封" | 统一收件（自建 + Gmail + QQ/163）加上 AI 分流的三个标签页 | 多账号运营、个人重度用户 | 人群最大；接入和分流的骨架已经有了；非英语和中国邮箱是 Gmail、Copilot 的空白 | 正面撞上 Gmail Gemini（免费）、Superhuman、Spark；卡在 AI 密钥和 Gmail CASA 审核上；"分拣不能处理完"（A4）仍未解决；差异化最弱 |
| C. "support@ 来了第一封客户信：自动有负责人，同事看得到，没人会重复回" | 团队共享邮箱的认领、分配和冲突提示 | 客服、运营小团队 | 命中 B1 这个最强的团队痛点；分配和备注已经上线 | 这是 Front 和 Missive 的主场，也是 Resend Inboxes 的方向；"几分钟"内很难体验到，要先有两个人和真实客户来信；没有 AI 时代的新意 |

**推荐 A，并把 C 作为 A 的场景载体**：A 发生在团队邮箱里，被分配的"负责人"可以是一个人，也可以是一个 Agent，放行的是人。B 作为留存功能，放到第二个季度，有模型密钥之后再上。

理由：
- A 是唯一一个**只有 Zmail 这套组合才讲得通**的体验；
- 它不依赖当前最大的外部阻塞（AI 密钥、Google 审核）；
- 它把我们已经在 D1 上投入的治理能力，变成用户看得见的价值。

### 4.2 它解决哪个痛点，为什么竞品做不到或做不好

**解决的痛点**："我想让 Agent 帮我处理邮件，但不敢把整个邮箱交给它"（A1、C1、C3、C5），以及在团队里"Agent 的产出由谁负责、谁放行"（B1 的 Agent 版本）。

各家为什么做不到：
- **AI 客户端（Superhuman、Spark、Bloop）**：MCP 和 CLI 绑定在个人和本机上。Spark 和 Bloop 要求桌面 App 开着，Superhuman 要 $33/人/月。三家都没有组织概念：同事的 Agent 能不能碰 support@、员工离职后它的 Agent 怎么办，都无从谈起。
- **平台（Gmail、Outlook）**：官方 MCP 处于预览、只覆盖本家邮箱、只支持英语；Gmail 那边连发信都做不到。用户对平台 AI 的隐私信任本来就低（A1）。
- **Agent 基础设施（AgentMail、Nylas、Cloudflare）**：给的是 API，没有人用界面。待审草稿要开发者自己做，放行流程也要自己做。AgentMail 和 Cloudflare 还接不了已有邮箱，而真正要处理的信都在已有邮箱里。
- **Resend Inboxes**：最像我们，但有三点不同。不能接入现有 Gmail/Outlook/IMAP；Agent 是"收件箱内置的"，权限不跟某个人挂钩；没有私人邮箱的隐私隔离、离职交接和验证码遮罩。而且它还在私测。
- **共享邮箱（Front、Missive）**：不托管邮箱，没有事务邮件；Agent 以人的身份操作，没有独立授权和配额。Front 的 AI 按会话计费又刚刚暴涨。

**Zmail 的组合论证**：
1. **托管**：Agent 可以拥有一个公司域名上的新地址，用来注册、收验证码、发事务邮件，并且受遮罩保护（C7）。
2. **接入**：Agent 也能处理人在 Gmail、Outlook、QQ 里本来就有的信（A5）。
3. **团队**：草稿由有"回复"权限的同事放行，责任清楚（B1）。
4. **Agent 授权**：按邮箱、按发件人和标签、最长 90 天、权限不超过授权人、离职自动撤权（C5、B5）。
5. **事务邮件**：同一套域名信誉和审计也覆盖 Agent 发出的系统邮件（B2、C4）。

一句话定位：**Zmail 是组织给人和 Agent 共用的邮箱层：一样的地址，一样的权限，人来放行。**

### 4.3 首次使用路径：从注册到啊哈时刻

目标是 **10 分钟以内**，并且不需要配置 DNS。DNS 放在啊哈之后再做。

| 步 | 用户动作 | 系统做什么 | 现状和缺口 | 时间预算 |
|---|---|---|---|---|
| 1 | Google 一键登录 | 自动创建个人组织 | Google 登录即将上线；生产自助注册目前关闭，需要决定是否开放 | 30 秒 |
| 2 | 选择"我想让 Agent 帮我处理邮件" | 根据目的分流引导：Agent 路径或团队路径 | **缺**：控制台首页已有新团队引导（PR #204），需要加"目的"分支 | 10 秒 |
| 3 | 拿到一个立即可用的地址 | 在 Zmail 共享域名下创建一个共享邮箱，比如 `xxx@<共享域名>`；同时提示"之后可换成你自己的域名" | **待确认**：共享试发域名 `send.zmail.im` 只用于发信，收信要有一个可以分给用户的共享收件域名。否则就走下一步的"接入现有邮箱" | 30 秒 |
| 3' | （备选）接入现有邮箱 | IMAP 应用专用密码（QQ、163、iCloud），或 Gmail、Outlook 授权 | 已有。Gmail 公共应用审核前，个人 Gmail 走测试用户模式或应用专用密码 | 1–2 分钟 |
| 4 | 点"连接我的 Agent" | 一次完成三件事：生成一个 `mail:agent` 范围的 key；默认授权 read + draft、30 天、遮罩验证码；给出一段 MCP 配置，可直接粘贴到 Claude Code、Codex、Cursor | D1 后端和 Web 权限页已有；**缺 MCP 服务器和"一键三合一"向导** | 1 分钟 |
| 5 | 让第一封信到达 | 引导页提供"给自己发一封测试来信"，模板是一封真实的客户询问；或者直接用接入邮箱里的最新一封 | **缺**：测试来信模板（可复用事务邮件发送能力） | 30 秒 |
| 6 | 在 Agent 里说"帮我处理新邮件" | Agent 通过 MCP 拉取事件、读信、写入待审草稿 | 后端已有（feed、read、draft） | 1–2 分钟 |
| 7 | **啊哈**：Zmail 顶部亮起"1 封 Agent 草稿待你放行"，右侧并排显示原信和草稿，标明依据，下面有三个按钮："发送 / 改一下 / 拒绝" | 点发送后，以邮箱地址发出；审计记录"哪个 Agent 起草、谁放行" | **缺**：待审草稿的主界面和一键放行（D2 的后半段） | 10 秒 |
| 8 | （啊哈之后）邀请同事；换成自有域名；打开 AI 分流 | 现有的团队路径和域名向导 | 已有 | — |

**北极星指标**：从注册到"第一封 Agent 草稿被放行"的时间，以及完成这一步的用户比例。辅助指标：草稿原样发出率、修改后发出率、拒绝率；七天内仍在放行草稿的用户比例。

### 4.4 未来一到两个季度的产品优先级

**Q1（约 6–8 周）：让啊哈时刻上线**

| 优先级 | 做什么 | 对应路线图 | 说明 |
|---|---|---|---|
| **P1** | **Zmail MCP 服务器**（远程 MCP，以 API key 或 OAuth 鉴权）。工具包括：`list_mailboxes`（只列已授权的）、`poll_events`、`get_message`（默认遮罩）、`create_draft`、`list_drafts`。**不提供发送工具**。同时提供 `zmail` CLI 和 Claude Code、Codex 的 skill 包 | issue #98 迭代 4 的"MCP / Agent 接入"；D1 的上层 | 薄薄一层，只调用 D1 已有的接口和权限检查，不新增权限规则 |
| **P1** | **待审草稿收件箱和一键放行**：全组织的"待放行"视图；原信和草稿并排；发送、编辑、拒绝；拒绝理由回流给 Agent；放行记录进入审计 | D2 的"人工确认 → 发送"半段提前交付 | 发送沿用现有发送接口和额度；Agent 永远不直接发信，这条不变 |
| **P2** | **"连接我的 Agent"向导**和 §4.3 的引导路径：一键生成 key 和授权、给出 MCP 配置、发测试来信；按目的分流的首页 | PR #204、#220 的延续 | 埋点统计北极星指标 |
| **P2** | **信任层第一步**：入站存储 SPF/DKIM/DMARC 判定，并在授权过滤和界面里显示（契约里已经写明目前缺这一项）；MCP 返回的正文明确标为"不可信数据"；收件人白名单（Agent 起草的回信只能发给原线程参与人）；草稿放行速率限制 | D1 评审遗留；R4"不凭邮件里的指令扩大访问" | 直接回应 C1、C4 |
| P3 | 共享收件域名或 Agent 专属地址（如 `agent-xxx@` 子地址），让 Agent 用自己的地址去注册服务、收验证码，验证码只对授权人可见 | R1 别名；C7 | 先确认共享收件域名的滥用风险和 Cloudflare 配额 |

**Q2：从"自带 Agent"走向"内置 AI"，把团队场景做深**

| 优先级 | 做什么 | 对应路线图 |
|---|---|---|
| P1 | **AI 分流接入真实模型**，并支持组织自带模型 Key（BYOK）。先 BYOK，避免我们承担模型费用和数据出境决策；之后再提供托管的 Haiku 级分流。重点打磨中文邮件和中国平台通知的分流质量 | `ai-triage.zh-CN.md` §9；R4 |
| P1 | **D2 全量**：分流 → 待办（同一封邀请只生成一条）→ 内置起草 → 放行。内置起草和 BYO Agent 共用同一个待审队列 | D2 |
| P2 | **Agent 作为会话负责人**：共享邮箱的会话可以分配给某个 Agent 授权，处理结果进入待审；"同事正在回复"的冲突提示同时覆盖 Agent | issue #98 迭代 3 + D1 |
| P2 | Agent 活动审计导出、按组织的 Agent 总览页（哪些 Agent、在哪些邮箱、最近做了什么），满足企业 IT 的需要 | 路线图决定 5 之后的"组织拥有的 Agent" |
| P3 | 稍后提醒、单封定时发送（个人效率的基础补齐，工作量小，放在空档里做） | issue #98 迭代 2 |

**明确不做（或推迟）**：
- 原生桌面或手机客户端。我们已有 IMAP 网关和 SMTP 提交，可以复用现成的客户端。
- 自己训练模型；按积分计费（Fyxer、Jace 的计费投诉是前车之鉴）。
- 和 Superhuman 拼速度和快捷键；做通用 AI 写作助手。
- 营销自动化的深度功能（A/B 测试、序列、CRM）。群发维持现状，只修可解释性。
- **让 Agent 直接发信**。至少两个季度内不做；以后如果要做，也只开放给"白名单收件人 + 事务模板"。
- 租户接入（C4）的大规模推广和 Gmail 公共应用的 CASA 审核，等有企业客户需要时再做。在那之前用组织自带 OAuth 应用和 IMAP 应用专用密码。

### 4.5 风险与待验证的假设

| # | 假设或风险 | 如果错了会怎样 | 低成本验证方法 |
|---|---|---|---|
| H1 | 已经在用 Agent 的人，愿意让它处理**团队或组织**的邮件，而不只是个人邮件 | 啊哈时刻 A 的人群太窄，应改为 B | 做一个落地页加 MCP 候补名单（一周）；访谈 10 位用 Claude Code 或 Codex 的创始人或开发者，问"你现在用 Agent 处理过哪些邮件？卡在哪？" |
| H2 | "Agent 起草，人放行"真的比自己回更省事 | 用户觉得放行也是负担，留存会差 | 内部和 3–5 个试点团队连续用两周，统计放行耗时、原样发出率、拒绝率；目标是超过一半的草稿原样或小改后发出 |
| H3 | 自带 Agent 的用户足够多，可以先不做内置 AI | 激活率低，必须更早接入模型 | 引导页同时给"连接我的 Agent"和"让 Zmail AI 试试"（先用规则版 `fake` 演示），比较两条路径的点击和完成率 |
| H4 | Resend Inboxes 短期内不会补上"接入现有邮箱"和人级权限 | 差异化收窄 | 每月跟踪 Resend 的更新日志和文档；我们的护城河放在接入（含 QQ/163）、私人与共享隔离、离职交接、中文市场 |
| H5 | 中文和中国邮箱（QQ、163）是被 Gmail、Copilot 忽略、且有付费意愿的市场 | 本地化投入回报低 | 在现有 IMAP 预设用户中统计接入占比；向 5 个中国团队试点收费意向 |
| R1 | 提示注入：Agent 读到恶意邮件后，起草的内容泄露别的信息 | 信任事故，对我们是致命的 | 草稿只能回给原线程参与人；正文作为数据并带不可信标记；放行界面高亮外链和收件人变化；用公开的注入样本做回归测试 |
| R2 | 共享收件域名被滥用来批量注册或发垃圾邮件，拖累 `zmail.im` 的信誉 | 投递率下降（B2、C4） | 共享域名只收不发，或者只回复原线程；新组织低额度；单独的子域名隔离信誉 |
| R3 | 数据出境和合规：内置 AI 要把（遮罩后的）邮件送到境外模型 | 中国企业客户不敢开 | 先做 BYOK 和自带 Agent（数据流向由客户决定）；开关处加告知文案（`ai-triage.zh-CN.md` §9 第 3 条） |
| R4 | 价格被压低：Agent 收件箱已经 $0.2–2/个 | 单卖能力没有利润 | 按"组织 + 席位"定价，Agent 授权不单独收费；对比 Front、Missive 的 $14–65/座，定在 $8–15/座区间做定价访谈 |
| R5 | MCP 供应链风险：有人冒充我们的 MCP 包 | 品牌和用户数据受损 | 只提供官方远程 MCP（不发 npm 本地包），或者发签名包，并在文档里写明唯一的官方地址 |

---

## 5. 下一步（建议）

1. 产品评审确认：推荐的啊哈时刻（A）和"先自带 Agent、后内置 AI"的顺序。
2. 确认是否开放生产自助注册，以及是否有可以分给用户的共享收件域名（§4.3 第 3 步）。
3. 按 `docs/BUSINESS_IMPLEMENTATION_PLAN_STANDARD.md` 为 MCP 服务器和待审草稿放行写实施计划。两者都只建在 D1 已有的权限之上。
4. 同时启动 H1 的落地页和访谈，以及 H2 的内部试用。

## 附：主要来源

- 产品官网和定价：[Superhuman](https://superhuman.com/plans/mail)、[Shortwave](https://www.shortwave.com/pricing)、[Spark CLI](https://sparkmailapp.com/blog/introducing-spark-cli)、[Bloop](https://bloop.email)、[Notion Mail 关停](https://www.notion.com/help/notion-mail-inbox-is-going-away-what-to-do-next)、[Inbox Zero](https://github.com/elie222/inbox-zero)、[Fyxer](https://www.fyxer.com/pricing)、[Gmail Gemini](https://blog.google/products-and-platforms/products/gmail/gmail-is-entering-the-gemini-era/)、[Outlook Copilot](https://techcommunity.microsoft.com/blog/outlook/copilot-in-outlook-new-agentic-experiences-for-email-and-calendar/4514601)、[AgentMail](https://www.agentmail.to/pricing.md)、[Nylas](https://nylas.com/pricing/)、[Cloudflare](https://blog.cloudflare.com/email-for-agents)、[Resend Inboxes](https://resend.com/blog/announcing-inboxes)、[Front](https://front.com/pricing)、[Missive](https://missiveapp.com/pricing)、[Zoho Mail MCP](https://www.zoho.com/mail/help/mcp/getting-started.html)、[HEY Agents](https://hey.com/agents)、[Gmail MCP](https://developers.google.com/workspace/gmail/api/guides/configure-mcp-server)、[Microsoft Mail MCP](https://learn.microsoft.com/en-us/microsoft-agent-365/mcp-server-reference/mail)
- 用户声音：正文 §1 和 §2 中各条链接。
