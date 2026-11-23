# 群发进度、预计完成时间与完成通知

状态：已实现（本 PR）。负责人：邮件模块（API + Web）。日期：2026-11-23。

## 1. 要解决的问题

产品原则：任何排队或异步任务（群发、导入、域名设置、同步）都要随时告诉用户总数、已完成、失败（附原因）、剩余、预计完成时间，说明可以暂停或重试，并在完成时通知。

在 `main d464d965` 上，群发详情已经显示 已发/总数、失败、跳过、待发和暂停原因，但缺四样：

| 缺口 | 现状 |
|---|---|
| 速度和预计完成时间 | 没有。组织 24 小时群发上限（`daily_broadcast_limit`，未设置时取 `MAILBOX_DAILY_SEND_LIMIT`）卡住时，只提示“额度空出后会自动继续”，不说具体时间 |
| 完成或暂停通知 | 没有。用户得一直开着页面盯 |
| 实时收件人列表 | 有状态下拉筛选，但看不到每个状态有多少人。失败原因只翻译了 5 个代码，其余显示原始代码 |
| 错误文案 | `disabledDescription` 和 `errors.broadcastDisabled` 直接把 `MAILBOX_INBOUND_SECRET`、`MAILBOX_PUBLIC_URL` 写给终端用户 |

## 2. 用户场景

1. **市场同事发 5 万封**：点「立即发送」后，详情页显示“每分钟 42 封，预计明天 08:39 完成（明天）”。她关掉页面去开会。
2. **碰到每日上限**：发到第 2 万封时组织额度用完。页面写明“正在等待每日发送上限，还有 3 万位收件人在等待，不会丢失，大约 11 月 24 日 08:00 自动继续”。她同时收到站内通知和邮件，内容一样。
3. **退信过多被自动暂停**：健康检查发现退信率达到 5%，暂停了群发。发起人收到通知：“20 封里有 3 封退信……请检查名单里过期或拼错的地址，然后恢复或取消。”
4. **发完了**：通知写“3 位收件人中 2 位已发送，1 位失败。打开群发查看谁失败了、为什么”。点链接直接打开这次群发，组织也自动切对。
5. **查失败原因**：收件人列表顶部的标签带着数量：全部 3 · 待发 0 · 已发 1 · 失败 1 · 跳过 1。失败一栏写“地址不存在或拒收邮件”，不再是 `transport.permanent_bounce`。
6. **普通成员遇到未配置的环境**：看到的是“群发暂不可用，每封群发邮件都必须带退订链接，这项功能还需要 Zmail 管理员开启”，而不是环境变量名。

## 3. 同类产品怎么做（调研）

| 产品 | 进度展示 | 限额 / 节流的说明 | 完成通知 | 失败原因 |
|---|---|---|---|---|
| Mailchimp 营销活动 | 活动名旁显示投递百分比和 Sending 标记，报告页有动画进度。文档没写速率或 ETA。投递卡住（比如停在 97%）时，解释为收件方服务器的临时错误，系统会重试最多 4 天[^mc-delivery] | 付费套餐超额按量计费，“不会中断服务”[^mc-charges] | 只在应用内：报告显示 “Your campaign has been delivered.”，标记由 Sending 变为 Sent。文档没写邮件通知[^mc-delivery] | 用平白的话解释收件方服务器的问题，退信明细在报告里 |
| Resend Broadcasts | 状态有 draft / scheduled / queued / sent / canceled，可按状态和受众筛选。发完后看送达、退订、打开率、点击率。文档没写实时计数或逐个收件人列表[^resend-manage][^resend-perf] | 文档未写。取消排队中的群发“只停止发给剩余收件人”[^resend-manage] | 状态变为 sent。文档没写邮件或 webhook 通知[^resend-intro] | 文档未写 |
| Postmark Bulk API | 四家里最完整。`GET /email/bulk/{id}` 返回 `TotalMessages`、`PercentageCompleted`、`ReleasedCount`、`FailedCount`，并保证 已放行 + 失败 ≤ 总数，完成时两者之和等于总数。进度到 100% 只表示处理完，不等于全部送达[^postmark-bulk] | 由 Postmark 自己“以最有利于投递的速度”放量，文档没写明确上限[^postmark-bulk] | 只能轮询状态接口，没有完成 webhook。逐个收件人的结果走投递和退信 webhook[^postmark-bulk] | `FailedCount` 包括被屏蔽的地址和“无法构建”的邮件 |
| SendGrid Single Sends | 状态只有 Draft / Scheduled / Triggered，没有“发送中”，也没有进度条。统计项有 Triggered、Delivered、Bounces、Bounce Drops、Spam Report Drops、Invalid Emails 等[^sg-single][^sg-stats] | 官方计费页没写（第三方文章说超额会部分丢弃或按量计费，未核实） | 文档未写 | 按类别（drops、bounces、invalid）配术语表解释[^sg-stats] |
| 飞书邮箱（分别发送） | 每位收件人收到单独一封，每封最多 500 人。文档没写进度视图或逐个收件人状态[^feishu-limit] | 按套餐设每日人均和组织总额度，每天 0:00（UTC+0）重置，不能提前恢复，只能升级或等重置。超额的邮件不发，发件人会收到超额退信，管理员会收到邮件机器人告警[^feishu-limit][^feishu-quota]。Lark 由 Mail Assistant 机器人通知[^lark-limit] | 只有超额告警，文档没写完成通知 | 平白的退信邮件 |
| 腾讯企业邮（群发单显 / 个性群发） | 「已发送」里每封邮件有「发送状态 → 查看详情」，共六种状态：投递中、已投递到对方邮箱、已投递到对方服务器、投递失败已退信、投递被系统拦截、数据异常状态未知。每种都附处理建议。个性群发最多 1000 人。文档没写汇总进度或计数[^exmail-status][^exmail-mass] | 全域名共享每日额度，发给内部成员和回复过的联系人不计入，额度随发信质量调整。按分钟、小时、天的频率限制会拦截相应时长[^exmail-limit] | 文档未写 | 平白的状态描述，附退信内容 |

飞书 / Lark 和部分腾讯页面的正文靠 JavaScript 加载，抓取工具只拿到标题。这些结论依据的是搜索引擎索引里的官方正文，脚注里标注“索引”。

**可以借鉴的做法：**

- 计数永远对得上（Postmark）：总数 = 已发 + 失败 + 跳过 + 待发 + 已取消，并把“处理完”和“已送达”分开显示。
- 当场说明节流：上面几家都不会在发送中显示“被额度暂停，几点恢复”，这是我们可以补上的空白。飞书的“不能提前恢复，到点重置”最接近。我们的上限是滚动窗口，能精确算出下一批额度空出的时间。
- 每个收件人状态用平白的话说清楚（腾讯）：说明发生了什么、该做什么，原始代码留给支持排查。
- 通知发件人（飞书 / Lark）：被上限或健康检查停下时通知。再加上一条明确的“发完了：成功 X、失败 Y”，上面几家都没有文档化这一条。
- 取消只影响剩余收件人（Mailchimp / Resend）：确认框直接写明“尚未发送的收件人不会再发送”。

## 4. 方案

### 4.1 速度和预计完成时间：服务端用 API 自己的数据计算

`GET /v1/mail-broadcasts/{id}` 对 `scheduled`、`sending`、`paused` 的群发返回 `progress`。列表和写操作的返回里为 `null`。每次请求现算，不缓存：

| 字段 | 计算方式 |
|---|---|
| `rate_per_minute` | 最近 10 分钟里变成 sent / failed / skipped 的收件人数，按“从开始发送起、最多 10 分钟、至少 1 分钟”折算成每分钟。只有 `sending` 才有，保留两位小数 |
| `estimated_finish_at` | 见下面的算法。被人工或健康检查暂停时为 `null` |
| `send_limit_pauses` | 发完前会不会（或已经）被 24 小时群发上限卡住 |
| `send_limit_resumes_at` | 卡住后什么时候有额度 |

**算法（`estimateProgress`，纯函数，单元测试覆盖）**：

- 输入：待发数 P、速度 R、本组织当前适用的群发上限 L、窗口内已用 U、以及窗口内第 n 早一次发送的时间（与上限计数口径一致，已删除邮件的发送记录也算）。
- 剩余额度 room = max(0, L − U)。并发请求可能让 U 超过 L，超出部分 overshoot = max(0, U − L)，要先等它们滑出窗口。
- P ≤ room：完成时间 = 开始时间 + P / R。没有速度时不估算。
- P > room：发完 room 后暂停。第 overshoot 早的那次发送滑出 24 小时窗口时恢复。超出 room 的第 k 个收件人，要等第 overshoot + k 早的那次发送滑出窗口才能发。超过当前窗口内的历史发送后，每多一天多发 L 封。
- 开始时间：立即发送是现在；定时群发是 `scheduled_at`；测试组等待中是 `resume_at`。
- 已知局限：假设同组织没有其他群发同时占用群发通道，所以是估算，不是承诺。页面和合同都写明了这一点。

新增两个只读查询：`CountRecipientsFinishedSince`（按 `broadcast_id` + `updated_at`），以及 `NthBroadcastAttemptSince`（与上限计数同一组 UNION，`ORDER BY … OFFSET n LIMIT 1`）。每次详情请求最多 2 次 OFFSET 查询。

成员能看到 `send_limit_resumes_at`，但看不到通道的原始用量，`GET /v1/mail-send-lanes` 仍然只给 owner/admin。

### 4.2 完成 / 暂停通知

- **何时通知**：群发结束为 `sent`；或以 `all_recipients_failed` 结束为 `canceled`；或被健康检查以 `bounce_rate_high` / `complaint_rate_high` 暂停；或开始因 `send_limit_reached` 等待。人工暂停、人工取消、测试组等待不通知，因为那是用户自己的操作或预期中的停顿。
- **通知谁**：发起人，即点击发送或定时的人。新增 `mail_broadcasts.started_by`。已经离开组织的发起人不通知。
- **怎么通知**：
  - 站内：通知 starter 启用时，用已有的 `domain.NotificationPublisher` 发 `in_app` 通知，`kind` 为 `mail.broadcast_sent` / `mail.broadcast_failed` / `mail.broadcast_paused`，`action_url` 是控制台路径。
  - 邮件：通过已有的系统发信器（`MAIL_PROVIDER` / `MAIL_FROM`，与密码重置相同）发到发起人的账号邮箱，链接是绝对地址 `{MAILBOX_PUBLIC_URL}/console/mail-broadcasts?org={org}&open={id}`。
  - 邮件不走通知 starter 的 email 通道，有两个原因：那个通道的“查看详情”链接是相对路径，在邮件里打不开；而且 starter 未启用时（例如 self-host compose 默认不启用）就不会有邮件。
- **只通知一次**：新增 `notice_state` 和 `notice_count`。worker 每轮把“当前状态”与“上次已通知状态”不同的群发找出来（只看 7 天内结束的），发出通知后用 CAS 更新。幂等键 `mail-broadcast.{id}.{n}` 同时用于站内通知和邮件 provider，重试不会重复投递。离开某个状态时也会记录，所以第二天再次被上限卡住会再通知一次。邮件暂时失败时下一轮重试；永久失败（地址无效、发信器未配置）记录日志后不再重试。通知失败不会影响发送。
- **部署不补发**：迁移 `2026_12_20_090000_add_broadcast_notices` 把所有现有群发的当前状态记为“已通知”，并用 `created_by` 回填非草稿群发的 `started_by`。
- **文案**：英文源码（服务端产生，规则要求英文），平白、可操作。每条都说清发生了什么（计数）、Zmail 接下来做什么（上限场景给出 UTC 恢复时间）、用户可以做什么。

### 4.3 实时收件人列表

- 状态筛选从下拉框改为带数量的标签：全部 / 待发 / 已发 / 失败 / 跳过；有取消的收件人时才显示「已取消」。列表已有 3 秒轮询，群发停下后会再刷新一次。
- 失败原因：翻译所有已知代码，共 18 个，中英双语，见 `reasonKey`。未知的服务商代码显示“未发送”，并保留代码以便支持排查。
- 通知链接用已有的 `open` 参数直接打开那次群发。切换组织时这个参数会被丢弃，所以旧链接不会在别的组织里误打开。

### 4.4 错误文案

`broadcasts.disabledTitle`、`broadcasts.disabledDescription`、`errors.broadcastDisabled` 改为面向用户的说法，不再出现环境变量名。环境变量仍然写在 `contracts/MAILBOXES.md` 和部署文档里，那是管理员看的。

## 5. 验收标准

| # | 标准 | 证明 |
|---|---|---|
| A1 | 详情显示每分钟速度和预计完成时间；被人工或健康检查暂停时不估算，并说明原因 | `TestEstimate*`、`TestMeasuredRate*`、`TestBroadcastProgressIsEmptyForPausedAndFinishedBroadcasts`、web “shows the send rate…”、“has no estimate while a person holds…” |
| A2 | 会被每日上限卡住时提前提示；已被卡住时写明大约几点自动继续 | `TestEstimateTellsWhenTheLimitPausesAndResumes`、`TestEstimateHeldByAFullLimitNeedsNoRate`、`TestBroadcastProgressReportsTheLimitFromStoredAttempts`、web “says when a broadcast held by the limit continues” |
| A3 | `progress` 只出现在读取单个群发的响应里，列表中为 `null`；合同和 OpenAPI 一致，类型已重新生成 | `TestReadingOneBroadcastIncludesItsProgressButTheListingDoesNot`、`contracts check` |
| A4 | 群发完成、全部失败、退信或投诉暂停、被上限卡住时，发起人各收到一次站内通知和邮件，内容可操作，链接直达 | `TestTheStarterIsTold*` |
| A5 | 人工暂停或取消不通知；通知失败不影响发送并会重试；离开组织的人不通知；部署不补发旧通知 | `TestStoppingABroadcastByHandSendsNoNotice`、`TestANoticeThatCouldNotBeSentIsTriedAgain`、`TestAStarterWhoLeftTheOrganizationIsNotTold`、`TestAddBroadcastNoticesBackfillsSoTheDeployTellsNoOne` |
| A6 | 收件人标签带数量，可按状态筛选，失败原因中英文都是平白说法 | web “shows progress, a paused send, recipients…” |
| A7 | 终端用户界面不出现环境变量名 | web “explains missing configuration…”（断言页面文本里没有 `MAILBOX_` 等） |
| A8 | `make check` 通过；1440×900 亮色和暗色、375×812 下页面正常 | PR 测试小结 |

## 6. 部署

- **先部署 Web，再部署 API**：旧版 Web 用 `strictObject` 校验群发响应，遇到新增的 `progress` 字段会报错。新版 Web 把 `progress` 设为可选，可以先上线。
- 迁移只加列，回滚时 `Down` 删除这三列，不丢其他数据。
- 不需要新的生产配置。要发通知邮件，需要已配置系统发信器（`MAIL_PROVIDER`、`MAIL_FROM`）；站内通知需要启用 `notification` starter，没启用时只发邮件。

## 7. 后续（不在本 PR）

- 同组织多个群发同时进行时，按 worker 的处理顺序扣除排在前面的群发占用的额度，让预计时间更准。
- 通知偏好：让用户可以关掉群发通知邮件。目前因为通知关乎用户自己发出的邮件，总是发送。
- 导入、域名设置、外部邮箱同步等其他异步任务套用同一套“总数 / 完成 / 失败 / 剩余 / 预计时间 / 通知”模式。

[^mc-delivery]: https://mailchimp.com/help/about-delivery-insights/
[^mc-charges]: https://mailchimp.com/help/about-additional-charges/
[^resend-manage]: https://resend.com/docs/dashboard/broadcasts/manage-broadcasts
[^resend-intro]: https://resend.com/docs/dashboard/broadcasts/introduction
[^resend-perf]: https://resend.com/docs/dashboard/broadcasts/performance-tracking
[^postmark-bulk]: https://postmarkapp.com/developer/api/bulk-email
[^sg-single]: https://www.twilio.com/docs/sendgrid/ui/sending-email/single-sends
[^sg-stats]: https://www.twilio.com/docs/sendgrid/ui/analytics-and-reporting/marketing-campaigns-stats
[^feishu-limit]: https://www.feishu.cn/hc/zh-CN/articles/441089771295（索引）
[^feishu-quota]: https://www.feishu.cn/hc/zh-CN/articles/329462350714（索引）
[^lark-limit]: https://www.larksuite.com/hc/en-US/articles/360048488497-limitations-on-sending-emails（索引）
[^exmail-status]: https://open.work.weixin.qq.com/help2/pc/19855?person_id=1
[^exmail-mass]: https://work.weixin.qq.com/help?doc_id=438&helpType=exmail（索引）
[^exmail-limit]: https://open.work.weixin.qq.com/help?doc_id=308&helpType=exmail（索引）
