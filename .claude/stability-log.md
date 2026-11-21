# Zmail 稳定性守护日志

## 2026-11-20 02:00 UTC 第 1 轮（首轮）
- 生产巡检：全部通过。ready 200/0.52s，login 200，三个未登录接口 401，证书剩 87 天；4 容器 healthy/Up，重启 0（01:55 夜间循环刚部署 52fd321），近 30 分钟 error/panic 0；根分区剩 28%，内存可用约 54G；最新备份 01:53（cron.d/zmail-backup 存在）。
- 回归 @ origin/main 52fd321：API go test 58 包全绿；Web vitest 823/823、type-check 通过；Admin vitest 66/66。
- Kest：按说明的统一环境跑失败。原因是测试基础设施问题，不是生产 bug：run_local.sh 只用一套配置启动 API，而 mail_send_queue 需要发送上限 1，mailbox_access 需要 webhook starter（及其迁移）和 scheduled_at，各流程共用 run_id 时 team-<run>@ 冲突；另外 mail_send_queue Q-3 仍断言回收站功能上线前的旧行为（删除后 404）。
- 处理：分支 fix/kest-per-flow-env，每个流程用自己的 <flow>.kestenv 起一个刚迁移过的服务、各自的 run_id；Q-3 改为按合同断言（进回收站，failed/send.canceled）。修复后 9 个流程、299 步全绿。make check 进行中（同时算作今日完整检查）。
- make check 全绿（首次失败是 contracts/ 未安装依赖，安装后重跑通过，属本机环境）。PR https://github.com/fastagi/zmail/pull/37 已合并 → main edc3dd8。只涉及测试和文档，无需部署。
- 下一轮回归基线：edc3dd8（6 小时内无新提交则跳过回归）。

## 2026-11-20 10:11 UTC 第 2 轮（距上轮约 8 小时，期间会话因用量上限暂停）
- 巡检全通过：ready 200/0.60s，login 200，3 个 401；证书 87 天；4 容器镜像 52fd321、重启 0、30 分钟 error 0；根分区已用 71%，可用内存约 54G；最新备份 03:30；无部署锁。
- 回归 @ edc3dd8（距上次超过 6 小时）：API 58 包、Web 823、type-check、Admin 66、Kest 8 个流程 299 步全绿。（更正：上一轮记录写成 9 个流程，实为 8 个。）
- 生产运行 52fd321，main 的 edc3dd8 只多了测试和文档，无需部署。
- 2026-11-20 10:43 UTC 第 3 轮：巡检全通过（夜间循环 10:32 部署 341464d，容器全新启动、重启 0、error 0；备份 10:30）。main 前进到 a39e306（PR #38 交互打磨、#39 平台账户账本），回归全绿：API 58 包、Web 847、type-check、Admin 66、Kest 9 个流程（新的平台账户流程已采用 .kestenv 约定）。

## 2026-11-20 11:18 UTC 第 4 轮
- 巡检全通过（夜间循环 11:10 部署 07a84a2 共享邮箱管理，容器重启 0、error 0；备份 11:06）。巡检时部署锁仍在（夜间循环收尾中），11:21 复查已释放。
- 回归 @ 71b32f8：Web 875、Admin 66、Kest 10 个流程全绿；API 有 1 个包失败，Web type-check 失败。
  - Web type-check：本机 web/.next/types 是上次 make check 构建留下的旧产物（页面已移动），不是 bug。清掉后通过；本机回归脚本改为跑 type-check 前先清 .next/types。
  - API：TestProperty1_RepositoryGetRanOrdering 偶发失败。真正原因：迁移仓库按数据库排序规则（en_US.utf8，忽略下划线）排序，而测试和 Go 按字节序；同一批次内名字含下划线时顺序不同，回滚顺序也受排序规则影响。先写了必定复现的测试，再改为 COLLATE "C"。make check 全绿，PR https://github.com/fastagi/zmail/pull/44 已合并 → 79a7d09。实际迁移名都以定长时间戳开头，生产上基本不受影响；不涉及 schema，无需单独部署（随下次部署带上）。

## 2026-11-20 12:01 UTC 第 5 轮
- 巡检全通过（生产 a0235a2，11:57 部署，含 PR #44；重启 0、error 0；备份 11:53；无部署锁）。说明新增第七节"状态看板"，从本轮起更新看板；看板上没有已拍板的决定。
- 回归 @ a0235a2：API 58、type-check、Admin 66、Kest 11 个流程全绿；Web 5 个 inbox-ui 用例在全量并行时超时（约 1013ms），单独跑通过。原因：收件箱的对话框等用 React.lazy 按需加载，全量负载下冷启动的动态 import 超过 findBy 默认 1 秒。属不稳定测试，不是产品 bug。修复：测试在 beforeAll 预加载这些模块；全量连跑两次 897/897，make check 全绿。PR https://github.com/fastagi/zmail/pull/48 已合并 → e11ea26（只改测试）。
- 2026-11-20 12:41 UTC 第 6 轮：巡检全通过（API/worker cce1116 12:23 起、web 9df0898，开发循环部署中，部署锁由其持有，健康）；看板只有 public-launch 有答复，属开发循环且已执行。回归 @ b806e98（含 #49 邮箱验证码注册）全绿：API 58、Web 909、type-check、Admin 66、Kest 12 个流程。
- 12:55 UTC 插曲：开发循环问 cce1116 部署和 .deploy-lock 是否本循环所为。答复：不是。查到锁是 root 拥有的空普通文件（不是按约定 mkdir 的目录），mtime 12:23:11；cce1116 来自本机分支 feat/cloudflare-wire-message-id（12:21:56 提交，未推送、无 worktree）。生产健康。锁和分支都不是我建的，我不碰。
- 2026-11-20 13:15 UTC 第 7 轮：巡检全通过（API cce1116、web 9df0898，健康）；不明的 .deploy-lock 普通文件仍在，开发循环已在看板开 unknown-hotfix 待用户决定，部署暂停。回归 @ 52692a0（含 #50）全绿：API 58、Web 925、type-check、Admin 66、Kest 12。

## 2026-11-20 13:47 UTC 第 8 轮
- 13:25 用户确认 cce1116 是其热修，要求合入 main 后释放锁、随 b806e98 一起部署。我在热修提交上跑 make check 时因本机磁盘满（剩 1.7G）失败（属环境问题），曾问用户是否清 Go 缓存。随后开发循环清了可重新生成的缓存（剩约 20G）、合并 PR #51、删锁并部署 1629d63，看板 unknown-hotfix 已执行。我这边未删任何文件、未动锁。
- 巡检全通过：生产 1629d63（13:36 起，含 cce1116、#49、#50），重启 0、error 0，备份 13:33，无锁。
- 回归 @ 1629d63 全绿：API 58、Web 928、type-check、Admin 66、Kest 12。
- 2026-11-20 14:21 UTC 第 9 轮：巡检全通过（生产 da0855f，14:14 部署，重启 0、error 0，备份 14:11，无锁；本机磁盘剩 16G）。回归 @ da0855f（含 #53 运营邮件指标）全绿：API 58、Web 928、type-check、Admin 73、Kest 13。
- 2026-11-20 14:55 UTC 第 10 轮：巡检全通过（生产 74ebf05，14:53 部署；巡检时锁在，回归后复查已释放；备份 14:51）。看板 ops-dashboard-enable 属开发循环且已执行。回归 @ 74ebf05（含 #54 基准测试）全绿：API 58、Web 928、type-check、Admin 73、Kest 13。

## 2026-11-20 15:29 UTC 第 11 轮
- 巡检全通过（生产 4acca0a，15:15 部署，重启 0、error 0，备份 15:13，无锁）。看板没有新决定。
- 回归 @ 4acca0a：API 58、Web 928、type-check、Admin 84 全绿；Kest 新流程 operator_broadcast_content 在全量里失败（"broadcast worker sends the recipient once"），单独跑通过。
- 原因：一次 worker --once 只处理 25 条，按顺序先处理事务邮件、再处理等待发送的邮件、最后才是群发；共享测试库里积压着其他流程历次留下的事务邮件和等待发送的邮件（每次 transactional_mail 留约 2 条），这一轮处理了 20+6 条，没轮到群发。属于测试依赖执行顺序的不稳定，不是产品 bug（生产 worker 持续循环）。
- 处理：先跑 14 次 transactional_mail 造出积压，能稳定复现；把这一步改为最多跑 20 轮 worker，直到群发发出，仍断言只发一次。复现场景下通过，make check 全绿。PR https://github.com/fastagi/zmail/pull/56 已合并 → 5baf8dc（只改测试）。
- 2026-11-20 16:11 UTC 第 12 轮：巡检全通过（生产 4acca0a，重启 0、error 0，备份 15:13，无锁）。回归 @ 5baf8dc 全绿：API 58、Web 928、type-check、Admin 84、Kest 14 个流程（含修复后的群发正文流程）。
- 2026-11-20 16:43 UTC 第 13 轮：巡检全通过（生产 4acca0a，重启 0、error 0，备份 15:13，无锁）；main 仍为 5baf8dc，已回归，跳过。

## 2026-11-20 17:14 UTC 第 14 轮：生产短暂故障
- 现象：/health/ready 连续 3 次 503（{"status":"down"}），live 200、容器 healthy。API 日志：连接 zgi-shared-postgres 时 FATAL "remaining connection slots are reserved for roles with the SUPERUSER attribute"（SQLSTATE 53300），共 4 次，都在 17:14；一个 POST /v1/transactional-messages 和一个 POST /v1/mailboxes/:id/messages 因此失败，审计日志写入也失败。约 1 分钟后自愈，复查 ready 200。
- 原因（只读排查，未登录数据库）：共享实例 max_connections=100，当时已建立 95 个连接；Zmail 只占 2 个（两个 worker），API 当时 0 个。其余来自其他项目：lanhai-maas 18、picku 约 20、shudong、whale、zpeak、domainfast 等。Zmail 生产 DB_MAX_OPEN_CONNS=2、DB_MAX_IDLE_CONNS=1，空闲连接 15 分钟关闭，流量低时没有可用连接，高峰时抢不到。
- 处理：根本修复要改数据库实例配置或其他项目的连接池，属"停下等我"，已 PushNotification（手机推送未发出，Remote Control 未开），并在看板 shared-pg 补充现象和推荐。Zmail 侧可选缓解（延长空闲连接保留时间、备份失败自动重试）写进推荐，等用户拍板，没有动生产。巡检照常继续。
- 2026-11-20 17:37 UTC 第 15 轮：巡检全通过（ready 200）；17:14 之后没有新的连接满报错，但共享库已建立 97/100 个连接，随时可能再满。shared-pg 待用户拍板。main 仍为 5baf8dc，跳过回归。
- 2026-11-20 17:58 UTC 第 16 轮：巡检全通过；近 25 分钟无连接满报错，共享库 97/100。shared-pg 仍待用户决定。main 未变。
- 2026-11-20 18:19 UTC 第 17 轮：巡检全通过；1 小时无连接满报错，共享库 95/100，恢复 30 分钟节奏。shared-pg 仍待决定。main 未变。
2026-11-20 18:50 UTC 第 18 轮：巡检全通过；共享库 96/100，无连接满报错；shared-pg 仍待决定；main 未变。
2026-11-20 19:22 UTC 第 19 轮：共享库再次连接满，19:14:16/19:14:31/19:16:49 三个 GET 503（/v1/mailboxes×2、thread×1），已自愈；连接 96/100；其余全通过；已更新 shared-pg 决定并推送通知；Zmail 侧调参只能部分缓解（每小时换连接、并发需第二连接），仍等用户拍板。
2026-11-20 19:42 UTC 用户批准调大共享库连接上限：max_connections 100→200（备份 .bak-20261120-maxconn100），19:38:57 重启约 10s，期间 Zmail 8 个 503；19:39:30 后 Zmail 与 pg 0 报错；其他项目容器状态前后一致；锁已释放。开发循环同时把 Zmail 连接空闲/寿命调到 24h。
2026-11-20 20:11 UTC 第 21 轮：巡检全通过；共享库 88/200，调大后无连接满报错（pg 仅 2 条客户端断开）；main 9d1f7a6（PR #57）回归全绿：api 58 ok、web 929、type-check、admin 84、kest 14 流程；本工作树里有他人未提交的 web 改动（5 个文件，未动），改在临时工作树回归并已删除，已问开发循环。
2026-11-20 20:20 UTC 开发循环回复：本工作树里的未提交 web 改动不是它或它启动的会话做的，会与 main（PR #57）冲突；它已告知用户。我不动这些文件，继续在临时工作树回归 main，等用户决定。
2026-11-20 20:46 UTC 第 22 轮：巡检全通过（新版 api 77aef94 / web b3c5838 已上线，共享库 91/200，0 报错）；工作树的陌生改动已消失（干净）；main b3c5838 回归：web 932、type-check、admin 84、kest 14 全绿，api 4 包因本机磁盘满链接失败，删自己的 .next/api tmp 后重跑全过；本机磁盘仅剩约 2GB。
2026-11-20 21:23 UTC 第 23 轮：巡检全通过（web 79c1924 已上线，共享库 89/200，0 报错）；本机磁盘恢复到 70GB 可用；main 79c1924（PR #61）回归全绿：api 58 ok、web 932、type-check、admin 84、kest 14。
2026-11-20 21:57 UTC 第 24 轮：巡检全通过；共享库 90/200，0 报错；main 未变（79c1924 已回归）。
2026-11-20 22:28 UTC 第 25 轮：巡检全通过；共享库 90/200，0 报错；main 未变。
2026-11-20 22:59 UTC 第 26 轮：巡检全通过；共享库 91/200，0 报错；main 未变。
