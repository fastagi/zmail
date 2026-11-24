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
