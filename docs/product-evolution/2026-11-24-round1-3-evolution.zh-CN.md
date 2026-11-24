# Zmail 3 轮自我进化日志（2026-11-24）

## Round 1：竞品微交互调研 + 真实浏览器找茬 → 并行修复

### 竞品洞察（Superhuman / Spark / 网易邮箱大师）
1. 统一收件箱行级必须展示具体来源厂商/账号徽章（如 `163`、`Gmail`、`QQ`），而非晦涩的底层协议 `imap`。
2. 移动端顶栏必须零遮挡、控件归位到抽屉，确保单手可切换个人/企业模式。
3. 多账号历史回溯除 14/30/90 天外，需支持 1 年与全部历史（3650 天），且解码所有 RFC 2047 中文发件人名称。

### Round 1 真实浏览器巡检发现（`https://zmail.im` @ `1c912badc1bf`）
- **P0-1**：`390×844` 移动端顶栏中 `WorkspaceModeToggle` 与主题/通知按钮重叠，导致 `Work` 按钮 `[occluded]` 无法点击。
- **P1-3**：个人模式下收件箱侧边栏底部的 `Mail pages`（群发/联系人/开发者/管理）未隐藏。
- **P1-4**：同步进来的 163 邮件发件人名存在未解码的 RFC 2047 字符串（如 `=?UTF-8?B?ODM0MVRva2Vu5bel5Y6C?=`）。
- **P1-5**：切换 `简体中文` 后，顶栏 `WorkspaceModeToggle` 仍硬编码显示英文 `Personal | Work`。
- **P2-1**：消息行来源徽章仅显示通用 `imap`，未识别 `163 / QQ / iCloud` 等具体厂商；首次回溯上限 90 天待扩展至全部历史。

### Round 1 并行改造任务
- `r1-backend-mime-backfill`：修复 RFC 2047 发件人/主题解码 + 扩展 `MaxExternalMailBackfillDays = 3650` + `-race` 回归测试。
- `r1-web-mobile-i18n-shell`：修复 `390px` 顶栏重叠（移入移动抽屉）+ 隐藏个人模式底部 B2B 链接 + `Personal/Work` 中英文 i18n。
- `r1-web-badges-backfill`：消息行智能厂商徽章（`163`/`QQ`/`iCloud`/`Gmail`）+ 回溯选项扩展（14/30/90/365/3650 天）。

---

## Round 2：Electron 桌面端接入共享 `@zmail/mail-ui` + 根治 P1-1「标为未读」竞态缺陷

### 交付成果
1. **`r2-desktop-mail-ui-wire`（`55976f3a`）**：
   - 将 `@zmail/mail-ui`（`packages/mail-ui/src/index.ts`）通过 Vite alias & TS path 正式接入 `apps/desktop`；
   - `apps/desktop/src/renderer/App.tsx` 直接挂载 `<MailShell />` 与 `QuickConnectModal.tsx`（13 厂商识别、30 账号隔离、回复自动匹配原账号），联动主进程本地 FTS 全文搜索（`window.zmailDesktop.searchLocal`）与系统未读角标（`window.zmailDesktop.setBadgeCount`）；
   - `packages/mail-ui` + `apps/desktop` 联合单测 **18/18 通过**（5 个套件，0 失败）。
2. **`r2-web-mark-unread-p1`（`45277b3c`）**：
   - 定位 P1-1 根因：`InboxReadingPane` 打开已读会话时未记录 `marked.current = item.id`，点击「标为未读」后乐观更新把 `rowUnread(item)` 置为 `true`，在关闭面板前触发自动已读 `useEffect` 反向把邮件重新标回已读；
   - 修复为在 `readable` 就绪时即锁定 `marked.current = item.id`，并补齐端到端回归测试 [inbox-mark-unread.test.tsx](file:///Users/stark/item/zmail/web/src/test/inbox-mark-unread.test.tsx)（113/113 通过）。

---

## Round 3：全端门禁验收、并发基准复核与最终生产部署

- **后端 `-race` 与架构守卫**：14/14 邮箱与迁移包 `-race` 全绿；`check-api-boundaries.sh` 0 违规；`check-code-size-guardrails.py` 全绿。
- **前端与跨端包**：`packages/mail-ui` + `apps/desktop` **18/18 通过**；`web/` `tsc --noEmit` 0 报错，Vitest 核心套件全绿。
- **合规材料**：`https://zmail.im/privacy` 已上线 Google API Services User Data Policy（Limited Use）声明，具备 Google OAuth / CASA Tier 2 审核前置条件。
