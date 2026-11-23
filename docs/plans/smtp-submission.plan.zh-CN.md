# IMAP/SMTP 网关 P1b（发信部分）实施计划：SMTP 提交

> 给执行 agent：按任务顺序逐个实现，每个任务先写失败测试、确认失败，再写实现；每个任务结束时单独提交一次。

**Goal**：员工在系统邮件 App 里用同一个客户端专用密码（勾选 `smtp` 范围）通过 `smtp.zmail.im` 的 465（隐式 TLS）或 587（STARTTLS）发信。发出的邮件走网页发信的同一条路径 `service.send`：权限、额度、排队、暂停、审计、Webhook 全部一致；bcc 只进投递信封，不进信头；内联图片保留 Content-ID；客户端重试同一封信只发一次。

**Architecture**：`fastbuild mail:gateway` 进程内新增 SMTP 提交服务，基于 `github.com/emersion/go-smtp v0.26.0`。协议适配层放在新包 `api/internal/modules/mailbox/smtpgateway`（只有这个包 import go-smtp），只依赖 `internal/domain` 新增的接口 `domain.MailSubmissionStore`；接口由 mailbox 模块的 `service` 实现，最终调用 `service.send(ctx, mailbox, input, queueAtLimit=true)`。IMAP 的登录限流和锁定从 `imapgateway` 挪到新的小包 `mailbox/mailclientauth`，两个协议共用**同一个** limiter 实例（同一个 IP 在 IMAP 和 SMTP 上的失败一起计数）。不新增进程，不新增数据库连接池。

**Tech Stack**：Go 1.26、go-smtp v0.26.0（MIT，连带已有的 go-sasl）、PostgreSQL、Next.js 16 / React 19（设置页）、Vitest。

**Spec**：[imap-smtp-gateway.zh-CN.md](imap-smtp-gateway.zh-CN.md) §4.1、§4.3、§5.7、§6、§8 的 P1b 行；P1a 计划 [imap-smtp-gateway.plan.zh-CN.md](imap-smtp-gateway.plan.zh-CN.md)。代码基线：`origin/main` `2fac1144`。

---

## 已定（不再讨论）

1. 依赖：`github.com/emersion/go-smtp v0.26.0`，精确版本，MIT 许可；go-sasl 已在 `go.sum` 中（go-imap 带入）。`go.mod` 不写范围版本；`make governance`/`make check` 覆盖供应链检查，OSV 扫描由独立工作流负责（`docs/DEPENDENCY_SECURITY.md`）。
2. 两个监听：`MAIL_GATEWAY_SMTPS_ADDR`（隐式 TLS，容器内 `:1465`，对外 465）和 `MAIL_GATEWAY_SUBMISSION_ADDR`（明文起步、必须 STARTTLS，容器内 `:1587`，对外 587）。为空即关闭。容器以 uid 65532、`cap_drop: all` 运行，所以只绑定非特权端口，由 compose 做 `465:1465`、`587:1587` 映射。
3. 证书与 IMAP 共用 `MAIL_GATEWAY_TLS_CERT_FILE`/`KEY_FILE` 和同一个热加载 reloader；证书需同时覆盖 `imap.zmail.im` 与 `smtp.zmail.im`（运维负责）。
4. 认证：`AUTH PLAIN` 和 `AUTH LOGIN`，只在 TLS 上提供和接受；需要密码带 `smtp` 范围；失败统一 `535 5.7.8`，固定延迟 1 秒；限流和锁定与 IMAP 共用。
5. 结果映射严格按 spec §5.7 表格（见 R6）。
6. 日志只写 ID、命令名、结果分类；**绝不写**主题、地址、正文、密码、用户名。go-smtp 的 `Debug` 永远为 nil，`ErrorLog` 只记录固定格式串。
7. 新迁移更新 golden schema，写 Up/Down 测试，在 `UPGRADING.md` 写明锁表影响。
8. 先改契约（`contracts/MAILBOXES.md`、`contracts/openapi.yaml`），再 `cd contracts && corepack pnpm generate`。本阶段没有新的浏览器错误码（SMTP 的拒绝只出现在协议里）。

## 本计划对 spec 空白处的裁定

| # | 问题 | 裁定 |
|---|---|---|
| R1 | MAIL FROM 与 `From:` 不同时以谁为准 | 两者都必须是该成员可发信的邮箱（个人邮箱，或权限 ≥ send 的共享邮箱）；**`From:` 头决定**用哪个邮箱发出。`From:` 缺失、多个地址、或 `MAIL FROM:<>` 都按 `550 5.7.1` 拒绝。`origin = connected` 的接入邮箱不允许经网关发信（与 IMAP 不暴露它们一致），返回 `550 5.7.1` |
| R2 | 收件人与 `To`/`Cc` 头的关系 | 投递集合 = 信封（RCPT）。`To`/`Cc` 取头中**同时出现在信封里**的地址（只在头里、不在信封里的地址丢弃，SMTP 语义下它们本来也不会收到）；信封里有、头里没有的就是 bcc。去重后上限 50（含 bcc），第 51 个不同地址 `452 4.5.3`；同一地址重复 RCPT 只算一次。`To` 为空（只有 Cc 或纯 bcc、"undisclosed-recipients"）时 `554 5.6.0` "Add at least one recipient in To"，因为发送路径要求 `to`；在 PR 中列为已知限制 |
| R3 | bcc 的存储与可见性 | `mail_messages` 新增 `bcc_addresses text NOT NULL DEFAULT '[]'` 和 `sent_by_membership_id bigint NULL`（不加外键：membership id 不复用，避免给 `mail_messages` 加外键校验锁）。消息详情只在 `viewer.MembershipID == sent_by_membership_id` 时返回 `bcc`，其他人（包括共享邮箱的其他成员、API key、IMAP）都看不到。网页 `SendMessage`/`SendFromAddress` 也记录 `sent_by_membership_id`。bcc 不进入 `cc_addresses`、不参与搜索、不进任何信头 |
| R4 | bcc 在发送通道中的形态 | `mailtransport.Message.Bcc`：Cloudflare 走请求体的 `bcc` 字段（官方文档：BCC 不写入信头）；接入邮箱的外部 SMTP 只加到 RCPT。`buildMIMEMessage` 永远不写 `Bcc:` 头（有测试） |
| R5 | 内联图片 | `mail_attachments` 新增 `content_id varchar(250) NOT NULL DEFAULT ''` 和 `inline boolean NOT NULL DEFAULT false`。解析时保留每个部分的 Content-ID；`inline` = `Content-Disposition: inline`，或没有 disposition 且位于 `multipart/related` 内（Apple Mail 会给普通附件也加 Content-ID，所以不能只看 Content-ID）。Cloudflare 走 `disposition: "inline"` + `contentId`；排队后由 worker 补发时从库里读回；IMAP 合成原文时写回 `Content-ID` 和 inline disposition |
| R6 | 结果映射（spec 表格的落地） | `sent` → `250 2.0.0 Ok <message id>`；排队（`queued_reason = send_limit`）→ `250 2.0.0 Queued`；`ErrMailSendLimitReached` → `452 4.3.1 Daily send limit reached, try later`；`ErrMailSendingPaused` → `550 5.7.1 Sending paused for this organization`；`ErrMailDeliveryFailed` → `451 4.4.1`；`ErrMailSenderNotConfigured` → `451 4.3.5`；`ErrMailMessageInvalid` → `554 5.6.0` + 原因；另外：幂等键仍在发送中（`ErrMailSendInProgress`）→ `451 4.4.1`（客户端稍后重试会拿到最终结果）；同一 Message-ID 内容不同（`ErrMailIdempotencyConflict`）→ `554 5.6.0 A different message was already sent with this Message-ID`；邮箱失权（`ErrPermissionDenied`/`ErrMailboxNotFound`）→ `550 5.7.1`；其他错误 → `451 4.3.0`（记日志，只含 ID） |
| R7 | 幂等键 | `"smtp:" + hex(sha256(decimal(mailbox_id) + ":" + Message-ID))`（共 68 个可见 ASCII 字符，满足 `validIdempotencyKey`）。没有 Message-ID 头时用 `"smtp:" + hex(sha256(mailbox_id + ":raw:" + hex(sha256(raw))))`，原文逐字节相同的重试同样只发一次。指纹 `sendFingerprint` 在 bcc 非空时追加 bcc，附件摘要在有 Content-ID/inline 时追加，二者为空时与旧指纹逐字节相同（已有 HTTP 幂等键不受影响） |
| R8 | 大小与逐部分限制 | `SIZE 8388608`（8 MiB，go-smtp 的 `MaxMessageBytes`，超出 `552 5.3.4 Maximum message size exceeded`）。解析采用**严格模式**（不同于入站的静默截断）：首个 text、首个 html 各 ≤256 KiB；附件（含内联图片）≤10 个、合计 ≤4 MiB；主题 ≤998 字符。超出返回 `552 5.3.4` 加可读原因，例如 `Attachments exceed 4 MiB in total`、`More than 10 attachments`、`The HTML body exceeds 256 KiB`、`The subject exceeds 998 characters` |
| R9 | 空主题、空正文 | 邮件 App 允许无主题、只有附件的邮件。提交路径空主题按 `(no subject)` 发出；text 和 html 都为空时正文为单个空格（`SendInput` 新增仅模块内可设的 `allowBlankBody`，HTTP 请求永远不能设置）。HTTP 接口的校验不变 |
| R10 | 是否存 SMTP 原文到 `mail_message_sources` | 不存。客户端原文可能带 `Bcc:` 头，共享邮箱的其他成员会经 IMAP 的 Sent 看到。IMAP 的 Sent 仍由存储字段合成（不含 bcc，含内联 Content-ID） |
| R11 | 撤销对 SMTP 会话的影响 | SMTP 会话短且只有在提交时才有副作用：每次 `MAIL FROM` 和 `DATA` 都调用 `RevalidateClients` 重新校验密码、组织开关、`mail_clients` 功能和 membership，失效即 `535 5.7.8` 并关闭连接；From 邮箱的 send 权限在提交时由 `service` 再查一次。另外已认证会话的空闲超时为 5 分钟（RFC 5321 §4.5.3.2 建议值）。因此不需要把 SMTP 会话放进 IMAP 的 sweep |
| R12 | 连接上限 | SMTP 与 IMAP 共用 `MAIL_GATEWAY_MAX_CONNECTIONS*` 的数值，但各自计数（每个协议一张 registry）：全局 1000、每 IP 100（IPv6 按 /64）、每 membership 20。超出时在问候前返回 `421 4.7.0` 并关闭；每用户超出时 `AUTH` 返回 `421 4.7.0` |
| R13 | 未认证超时 | 60 秒内没有成功 AUTH 就写 `421 4.7.0 Sign-in timed out` 并关闭，不管期间发了多少命令（与 IMAP 一致）。计时从 TCP 接受开始，STARTTLS 不重置 |
| R14 | `last_used_protocol` | `AuthenticateClient(…, MailClientProtocolSMTP, …)` 已经会写 `smtp`；列表里同一密码最近一次的协议就是它 |
| R15 | 设置页的“服务器设置”用户名一栏 | 显示该成员在当前组织可登录的个人邮箱地址（取自现有的邮箱列表 hook），每个地址一键复制；拿不到时退回通用提示。IMAP 和 SMTP 两段都显示 |
| R16 | `submitted_via` 列（spec §4.4） | 本 PR 不加；排查依靠 `sent_by_membership_id` + 网关日志里的 message id。留到 P1c 与 `mail_client_sessions` 一起做 |
| R17 | 失败后的重试 | 同一封信（同一幂等键、同一指纹）上一次以 `failed` 结束时，发送前把那条失败记录的 `idempotency_key` 置空（失败副本仍留在已发送），这次重新发送。邮件应用把 `451` 当作稍后重试，若一直重放失败就永远发不出去。代价：提供方应答丢失的模糊失败在重试时可能投递两次，与人在控制台点“重发”一致（至少一次） |

## Global Constraints

- 网关不写权限 SQL：From 授权调用 `service.checkAccess(..., canSend, ...)`，发信调用 `service.send`。
- `smtpgateway` 只依赖 `internal/domain`、`mailclientauth` 和 go-smtp，不 import `mailbox`；go-smtp 的类型不出现在 `domain`、`mailbox` 中。
- MIME 解析（含严格模式）在 `mailbox` 内完成，复用 `mimeWalker`；`smtpgateway` 只负责信封、限流、认证、读 DATA 和应答码映射。
- 资源有上限：DATA 8 MiB；RCPT 去重后 50，单会话 RCPT 命令最多 200 次（含重复）；命令行 2000 字节；已认证空闲 5 分钟；写超时 1 分钟。
- 不碰服务器、DNS、证书签发；不部署。

## Review Focus（最可能出错的 5 处）

1. **bcc 泄露**：bcc 绝不能出现在 `To`/`Cc` 存储、任何信头（Cloudflare headers、`buildMIMEMessage`、IMAP 合成原文）、消息列表、搜索、Webhook、或非发件人的详情里。测试：`TestSubmitBccStaysInEnvelope`（传输层收到 bcc、信头里没有）、`TestMessageDetailShowsBccOnlyToSender`、`TestBuildMIMEMessageNeverWritesBcc`。
2. **发件身份**：`From:` 必须由服务端按邮箱权限校验；显示名一律用邮箱的 `display_name`。共享邮箱只有 view 权限的成员必须被拒（`550 5.7.1`），接入邮箱一律拒。
3. **幂等**：同一封信（同一 Message-ID、同一内容）在 250 之前断网重试，只能产生一条 `mail_messages`、只调用一次传输层；同一 Message-ID 不同内容要拒绝而不是覆盖。
4. **锁定共用**：IMAP 与 SMTP 共用同一 limiter；SMTP 的失败也计入 IP 与用户名桶；锁定期间正确密码也被拒。
5. **TLS 边界**：587 上 STARTTLS 之前不广告 AUTH，直接发 AUTH 得 `523 5.7.10`；MAIL 在 AUTH 之前得 `530 5.7.0`。

---

## Task 1：迁移（bcc、发件 membership、附件 Content-ID）

**Files**：新建 `api/database/migrations/2026_12_25_090000_add_mail_submission_fields.go` 与 `mail_submission_fields_test.go`；`testdata/schema.golden.sql`；`api/internal/modules/mailbox/provider.go`（迁移名）；`UPGRADING.md`。

**Interfaces（表）**
- `mail_messages`：`bcc_addresses text NOT NULL DEFAULT '[]'`、`sent_by_membership_id bigint NULL`。
- `mail_attachments`：`content_id varchar(250) NOT NULL DEFAULT ''`、`inline boolean NOT NULL DEFAULT false`，约束 `chk_mail_attachments_content_id CHECK (content_id !~ '[<>[:space:]]')`。

**Steps**
1. 写失败测试 `TestMailSubmissionFieldsUpDown`：默认值生效；`content_id` 带尖括号或空白被拒；Down 后四列消失；Up 后与 golden 一致。
2. 实现（`UseTransaction: true`，Down 用 `DROP COLUMN IF EXISTS`）。
3. `FASTBUILD_UPDATE_GOLDEN_SCHEMA=1 go test ./database/migrations -run TestMigrationsProduceGoldenSchema`，再不带变量跑全包。
4. `UPGRADING.md`：PostgreSQL 11+ 对带常量默认值的 `ADD COLUMN` 只改目录、不重写表，但需要短暂的 `ACCESS EXCLUSIVE` 锁；在低峰期执行，并设置 `lock_timeout`。

**Commit**：`feat(api): store bcc, sending membership, and inline content ids`

## Task 2：发信路径支持 bcc、内联图片、提交专用校验

**Files**：`api/internal/domain/mailbox.go`（`MailMessage.Bcc`、`SentByMembershipID`；`MailAttachment.ContentID`、`Inline`）、`api/internal/infra/mailtransport/transport.go`、`cloudflare.go` 及测试、`api/internal/modules/mailbox/{model.go,service.go,attachment.go,queued_send_service.go,external_mail_smtp.go,message_source.go,dto.go,handler.go}` 及测试。

**Interfaces**
```go
// mailtransport
type Message struct { ...; Bcc []string }          // envelope only
type Attachment struct { ...; ContentID string; Inline bool }
// mailbox
type SendInput struct { ...; Bcc []string; SentByMembershipID uint; allowBlankBody bool }
type AttachmentInput struct { ...; ContentID string; Inline bool }
```
**Steps**
1. 写失败测试：
   - `TestCloudflareSendsBccAndInlineAttachments`：请求体有 `bcc`；内联附件为 `disposition: "inline"`、`contentId`；`headers` 里没有 Bcc。
   - `TestSendWithBccKeepsItOutOfCc`（fake repo）：存储的 `Cc` 不含 bcc，`Bcc` 单独保存；to+cc+bcc 超过 50 为 `ErrMailMessageInvalid`；bcc 与 to 重复时去重。
   - `TestSendFingerprintUnchangedWithoutBcc`：不带 bcc/内联时指纹与旧实现相同。
   - `TestQueuedSendKeepsBccAndInline`：排队后 worker 补发时传输层收到同样的 bcc 和 Content-ID。
   - `TestBuildMIMEMessageNeverWritesBcc`、`TestComposeMessageSourceKeepsContentID`。
   - `TestMessageDetailShowsBccOnlyToSender`（handler 级）。
2. 实现：PO 映射、`buildOutbound`、`deliver`、`storedFiles`、`createAttachments`、外部 SMTP 的 RCPT、`writeMIMEBody`、DTO（`bcc` 为 `omitempty`，只给发件人）。
3. `go test ./internal/infra/mailtransport/... ./internal/modules/mailbox -run 'Bcc|Inline|Fingerprint|Queued|MIME|ContentID'`。

**Commit**：`feat(api): bcc and inline images through the send path`

## Task 3：提交存储（MIME → SendInput、From 授权、结果分类）

**Files**：`api/internal/domain/mail_client.go`（`MailSubmissionStore`、`MailClientSubmission`、`MailClientSubmissionResult`、`MailClientSubmitError`）、`api/internal/modules/mailbox/mime.go`（严格模式、Content-ID）、新建 `mail_submission.go`、`mail_submission_test.go`、`app_password_service.go`（放开 `smtp` 范围）及测试、`api/internal/app/app.go`、`provider.go`（`wire.Bind`）、`make wire`。

**Interfaces**
```go
type MailSubmissionStore interface {
	AuthenticateClient(ctx, username, password string, protocol MailClientProtocol, ip string) (*MailClientPrincipal, error)
	RevalidateClients(ctx, principals []*MailClientPrincipal) (map[uint]bool, error)
	ClientSenderAllowed(ctx, p *MailClientPrincipal, address string) (bool, error)
	SubmitClientMessage(ctx, p *MailClientPrincipal, submission MailClientSubmission) (*MailClientSubmissionResult, error)
}
type MailClientSubmitErrorKind string // sender_not_allowed | too_large | invalid | conflict | limit_reached | paused | delivery_failed | in_progress | not_configured
type MailClientSubmitError struct { Kind MailClientSubmitErrorKind; Reason string } // Reason: English, never an address or subject
func parseSubmission(raw []byte) (*parsedMail, error) // strict: *MailClientSubmitError{too_large|invalid}
func submissionIdempotencyKey(mailboxID uint, messageID string, raw []byte) string
func classifySubmitError(err error) error
```
**Steps**
1. 写失败测试（单元 + disposable PostgreSQL 的 `mailClientFixture`）：
   - `TestParseSubmissionKeepsInlineContentID`：`multipart/related` 里的 `image/png` 带 `Content-ID: <logo@x>` → `ContentID = "logo@x"`、`Inline = true`；`multipart/mixed` 里带 Content-ID 的 PDF 附件仍是普通附件。
   - `TestParseSubmissionLimits`（表驱动）：11 个附件、附件合计 4 MiB+1、html 256 KiB+1、主题 999 字符各自返回 `too_large` 和对应原因；无法解析返回 `invalid`。
   - `TestSubmissionIdempotencyKey`：格式、长度、Message-ID 有无两种情况。
   - `TestClassifySubmitError`：spec 表格每一行加 R6 的补充行。
   - `TestClientSenderAllowed`：个人邮箱可；send 权限的共享邮箱可；view 权限的共享邮箱、别人的邮箱、接入邮箱、别的组织的地址都不可。
   - `TestSubmitClientMessageSendsOnce`：同一原文提交两次，只有一条消息、传输层只调用一次，第二次返回同样结果。
   - `TestSubmitBccStaysInEnvelope`：信封 3 人、`To` 1 人、`Cc` 1 人 → bcc 1 人；`sent_by_membership_id` 为发件人。
   - `TestSubmitQueuesAtLimit` / `TestSubmitLimitReached`：额度 1 时第二封 `queued`，排队也满时 `limit_reached`。
   - `TestSubmitUsesMailboxDisplayName`：`From: "Fake" <alice@…>` 发出的显示名是邮箱的 `display_name`。
   - `TestCreateAppPasswordAcceptsSMTP`（替换 `TestCreateAppPasswordRefusesSMTP`），以及 handler 测试里对应断言。
2. 实现 `mime.go` 的严格模式（`mimeWalker.strict`、`violation`，记录 `Content-ID` 与是否位于 `multipart/related`），`mail_submission.go`，放开 `smtp` 范围。
3. `cd api && make wire && go test ./internal/modules/mailbox -run 'Submission|Submit|SenderAllowed|AppPassword'`。

**Commit**：`feat(api): turn submitted MIME into sends with From checks and idempotency`

## Task 4：`mailclientauth` 共用限流 + `smtpgateway` 协议层

**Files**：新建 `api/internal/modules/mailbox/mailclientauth/{limiter.go,limiter_test.go,ip.go}`（从 imapgateway 挪过来，导出 `Limiter`、`NewLimiter`、`IPBucket`），`imapgateway/{server.go,registry.go,session.go}` 改用它；`go.mod`/`go.sum`；新建 `api/internal/modules/mailbox/smtpgateway/{server.go,session.go,auth.go,listener.go,replies.go,metrics.go}` 和测试；`api/internal/infra/metrics/mail_gateway.go`（`mail_gateway_smtp_submissions_total{outcome}`）。

**Interfaces**
```go
type Config struct { Domain string; MaxConnections, MaxPerUser, MaxPerIP int; FailureDelay, AuthTimeout, IdleTimeout time.Duration; Limiter *mailclientauth.Limiter; Now func() time.Time }
func NewServer(store domain.MailSubmissionStore, tlsConfig *tls.Config, cfg Config, logger *slog.Logger) *Server
func (s *Server) ServeImplicitTLS(ln net.Listener) error  // wraps ln with the caps, then tls.NewListener
func (s *Server) ServeSubmission(ln net.Listener) error   // STARTTLS required before AUTH
func (s *Server) Shutdown(ctx context.Context) error
func replyFor(err error) *smtp.SMTPError                   // the mapping table
```
**Steps**
1. 写失败测试：
   - `mailclientauth`：原有 limiter 测试原样搬过来；`imapgateway` 测试全绿。
   - `TestReplyForMapsEveryOutcome`（表驱动，对应 R6 全部行，含 `sent`/`queued` 的 250 文本）。
   - `TestLoginMechanism`：AUTH LOGIN 的两步质询、带初始响应、`*` 取消。
   - `TestSubmissionRefusesAuthBeforeSTARTTLS`：EHLO 不含 AUTH；AUTH 得 523。
   - `TestMailBeforeAuthRefused`（530）、`TestRcptDedupAndCap`（50 个不同地址后 452 4.5.3，重复不计）、`TestRcptInvalidAddress`（501 5.1.3）。
   - `TestAuthLockSharedWithIMAP`：同一 limiter 的 IP 被锁后 SMTP 正确密码也是 535。
   - `TestPreAuthTimeoutCloses`、`TestConnectionCaps`（每 IP、全局在问候前 421）。
   - `TestRevalidatedAtData`：认证后撤销，DATA 得 535 并断开。
   - `TestGatewayLogsContainNoSecrets`：日志中没有密码、用户名、地址、主题。
2. 实现。go-smtp 设置：`MaxMessageBytes = 8 MiB`、`MaxLineLength = 2000`、`MaxRecipients = 0`（会话内自己去重计数）、`AllowInsecureAuth = false`、`ReadTimeout = 5m`、`WriteTimeout = 1m`、`Debug = nil`、`ErrorLog` 只记格式串。
3. `go test ./internal/modules/mailbox/mailclientauth/... ./internal/modules/mailbox/imapgateway/... ./internal/modules/mailbox/smtpgateway/...`。

**Commit**：`feat(api): SMTP submission server on go-smtp with shared sign-in limits`

## Task 5：配置、`mail:gateway` 命令、端到端集成测试

**Files**：`api/internal/infra/config/{mail_gateway.go,config.go}` 及测试、`api/docs/CONFIGURATION.md`、`api/.env.example`、`api/internal/bootstrap/operatorcommands/mail_gateway.go` 及测试、`api/internal/modules/mailbox/smtp_gateway_test.go`、`UPGRADING.md`。

**Interfaces**
```go
type MailGatewayConfig struct { ...; SMTPSAddr string /* MAIL_GATEWAY_SMTPS_ADDR */; SubmissionAddr string /* MAIL_GATEWAY_SUBMISSION_ADDR */ }
func (c MailGatewayConfig) Enabled() bool // any of IMAP, SMTPS, submission
// MailboxConfig: ClientSMTPHost (MAIL_GATEWAY_PUBLIC_SMTP_HOST, smtp.zmail.im), ClientSMTPPort (MAIL_GATEWAY_PUBLIC_SMTP_PORT, 465), ClientSubmissionPort (MAIL_GATEWAY_PUBLIC_SUBMISSION_PORT, 587)
```
**Steps**
1. 写失败测试：
   - 配置：只开 SMTPS 也算启用；开了任一监听但缺证书/pepper 报错；地址格式；公开主机名与端口校验和默认值。
   - 命令：IMAP 为空、只开 SMTP 时也能启动（`runMailGateway` 用临时证书和 `127.0.0.1:0`）。
   - 集成（disposable PostgreSQL + 真实 `net/smtp` 客户端）：
     - `TestSMTPSubmissionImplicitTLS`：465 风格连接，AUTH PLAIN，发一封带 2 个附件、1 个内联图片、1 个 bcc 的邮件：传输层收到 bcc、附件、`contentId`；网页 `ListMessages` 的 Sent 里只有一份，`cc` 不含 bcc；发件人的详情有 `bcc`，Bob 的没有；应答 `250 2.0.0 Ok`。
     - `TestSMTPSubmissionSTARTTLS`：587 风格连接，STARTTLS 后 AUTH LOGIN 成功。
     - `TestSMTPSubmissionRetrySendsOnce`：同一原文提交两次，传输层只调用一次。
     - `TestSMTPSubmissionRefusesForeignFrom`：Alice 用 helpdesk@（只有 view）作 From → 550 5.7.1。
     - `TestSMTPSubmissionTooLarge`：5 MiB 附件 → 552 5.3.4，原因包含 `4 MiB`。
     - `TestSMTPSubmissionQueuedAtLimit`：额度用完 → `250 2.0.0 Queued`，消息 `queued_reason = send_limit`。
     - `TestSMTPRequiresSMTPScope`：只有 imap 范围的密码 → 535。
2. 实现配置、命令接线（同一个证书 reloader、同一个 limiter、同一个 health/metrics；关闭时三个服务都 Shutdown）。
3. `go test ./internal/infra/config/... ./internal/bootstrap/operatorcommands/... ./internal/modules/mailbox -run 'SMTP'`；`cd api && make route-catalog-check`。

**Commit**：`feat(api): serve SMTP submission from mail:gateway on 465 and 587`

## Task 6：契约、openapi、生成类型、设置页

**Files**：`contracts/MAILBOXES.md`（Mail Client Access 新增 “SMTP Submission” 小节，`scopes` 放开，`server` 增字段，消息详情 `bcc`）、`contracts/openapi.yaml`、生成的 `web/src/http/generated/openapi.ts`、`admin/src/http/generated/openapi.ts`；`api/internal/modules/mailbox/app_password_handler.go`（`server` 新字段）；web：`mail-client-schemas.ts`、`mail-clients-tab.tsx`、`mock-mail-app-password-store.ts`、消息 schema（`bcc` 可选）、i18n `settings` 中英文、测试 `web/src/test/mail-clients-tab-smtp.test.tsx`。

**界面行为**
- “服务器设置”卡片：收件（IMAP）一段不变；发件（SMTP）一段显示主机 `smtp.zmail.im`、端口 `465`（SSL/TLS）和 `587`（STARTTLS）、用户名、“同一个客户端专用密码”。
- 用户名一栏（R15）：列出成员在当前组织可登录的个人邮箱地址，每个带复制按钮；没有时显示通用提示。
- “添加设备”对话框：`smtp` 可勾选，默认 IMAP、SMTP 都选；提交时按 `['imap', 'smtp']` 顺序发送。
- 列表里的范围徽章已支持 `smtp`。

**Steps**
1. 写失败 Vitest：`shows SMTP servers with both ports`、`lists the member's own addresses as usernames with copy buttons`、`falls back to the generic username hint`、`sends the smtp scope when ticked`。
2. 改契约、`cd contracts && corepack pnpm generate`、实现。
3. `cd web && corepack pnpm vitest run <files> && corepack pnpm type-check && corepack pnpm lint`；`cd admin && corepack pnpm type-check`。

**Commit**：`feat(web): SMTP settings and the smtp scope for mail client passwords`（契约单独提交：`docs(contracts): SMTP submission and bcc in Mail Client Access`）

## Task 7：门禁与 PR

1. `bash <scratchpad>/gate.sh <worktree> <log>`，日志尾部必须是 `✅ All tiers (0+1+2) passed`；偶发失败先单独重跑确认，再整体重跑一次。
2. `git push -u origin feat/smtp-submission`，`gh pr create`（不合并），正文写运维事项：新环境变量、`465:1465`/`587:1587` 端口映射、`smtp.zmail.im` DNS（灰云）、证书需覆盖 `smtp.zmail.im`。

---

## 后续（不在本 PR）

- P1b 整理部分：IMAP MOVE、`\Deleted`/EXPUNGE、COPY 到 Trash、APPEND 到 Sent 去重。
- P1c：IDLE、`.mobileconfig`、autoconfig/autodiscover、Drafts、`mail_client_sessions`、`submitted_via`。
- 开放问题：Cloudflare Email Sending 是否接受原始 MIME 透传（可保留客户端自定义头和 Message-ID）。
