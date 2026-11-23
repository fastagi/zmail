# 使用 Google 登录（OAuth 2.0 / OpenID Connect）

状态：已实现，待评审，2026-11-23。分支 `feat/google-sign-in`。涉及 `api/`（user starter）、`web/`（登录、注册页和 BFF）、`contracts/`。

## 1. 场景

| 谁 | 想做什么 | 结果 |
|---|---|---|
| 没有 Zmail 账号的人 | 在登录页或注册页点“使用 Google 登录” | 首次登录自动建账号，邮箱取 Google 返回的已验证邮箱，直接进入控制台 |
| 已用密码注册的人 | 用同一邮箱的 Google 账号登录 | 关联到原账号，不重复建号；邮箱已验证的原密码和会话继续可用，未验证的清空密码并撤销全部会话 |
| 已关联过的人，后来改了 Google 邮箱 | 再次用 Google 登录 | 按 Google `sub` 找到同一个账号 |
| 只用 Google 登录过的人 | 想要一个密码 | 走现有“忘记密码”设置密码 |
| 从 `/console/admin/domains` 被踢到登录页的人 | 用 Google 登录 | 回到 `/console/admin/domains` |

## 2. 已定决策

- 授权码流程，带 `state`、`nonce`、PKCE（S256）。ID token 校验签名（Google JWKS，RS256）、`iss`、`aud`、`azp`、`exp`、`iat`、`nonce`。
- 依赖：`go.mod` 里没有 `golang.org/x/oauth2` 和 `go-oidc`；仓库已有只用标准库的 Google OIDC 校验（Gmail 推送）和授权码换取（Gmail 连接器）。本功能照同样方式用标准库实现，不引入新依赖。
- 只接受 `email_verified=true`。关联存新表 `user_identities`（`provider, subject` 唯一）。匹配顺序：先 Google `sub`，再已验证邮箱（不区分大小写，同名时精确匹配优先）。按邮箱关联会把账号的 `users.email_verified_at` 标为已验证。若该账号此前邮箱未验证（可能被他人抢注），在同一事务里清空密码、撤销全部会话（原因 `identity_linked`），并写审计；之后只能用 Google 登录，或通过忘记密码重设密码。邮箱已验证的账号照常关联，不撤销任何东西。注销账号时同一事务删除其 `user_identities`，Google `sub` 既不能复活旧账号，也不会挡住新账号。
- 被禁用的账号返回和密码登录相同的 `401 AUTH.INVALID_CREDENTIALS`。`AUTH_REGISTRATION_ENABLED=false` 时，没有账号的 Google 用户收到 `403 AUTH.GOOGLE_REGISTRATION_CLOSED`；已有或已关联的账号照常登录。
- `returnUrl` 只接受同源相对路径（单个 `/` 开头，不是 `//`，没有协议），否则回到默认落地页。它在发起时存进服务端的 state 记录，回调只用记录里的值。
- 环境变量：API 端 `GOOGLE_OAUTH_CLIENT_ID`、`GOOGLE_OAUTH_CLIENT_SECRET`，必须同时设置或同时为空。未配置时按钮隐藏，接口返回 `503 AUTH.GOOGLE_NOT_CONFIGURED`。
- 回调地址由 Web 公网源推出：`${NEXT_PUBLIC_APP_URL}/api/auth/google/callback`，在 Google Cloud Console 的“已获授权的重定向 URI”里登记。
- 会话签发复用密码登录的同一段代码（`SessionService.Issue` + 记录最后登录），Cookie、刷新、审计行为一致。
- 文案：zh-Hans “使用 Google 登录”，en-US “Continue with Google”，使用 Google 标准彩色 “G” 标志。

## 3. 本次裁定（实现中新增）

- **与 Gmail 连接器的 OAuth 客户端分开配置**：连接器用 `EXTERNAL_MAIL_GMAIL_CLIENT_*`，申请受限权限 `gmail.readonly`，回调是控制台页面；登录只要 `openid email profile`，回调是 BFF 路由。两者权限、同意屏幕审核和启停都不同，所以用两组变量。运营者可以填同一个 Google 客户端，只要把两个回调地址都登记上。
- **开启了两步验证的账号**：Google 登录不能绕过验证器。这类账号用 Google 登录时返回 `401 AUTH.TOTP_REQUIRED`，登录页提示改用密码 + 验证码登录。完整的“Google 之后再输验证码”不在本次范围。
- **发起和回调的浏览器绑定**：BFF 发起时把 `state` 写进 10 分钟的 HttpOnly Cookie（`SameSite=Lax`，因为 Google 回跳是跨站顶级 GET），回调时与查询参数比对，再交给 API 一次性消费。
- **state 记录**：新表 `oauth_sign_in_states` 只存 `state` 和 `nonce` 的 SHA-256、PKCE verifier、回调地址和 returnUrl，10 分钟过期，消费即删除；每次发起顺带清理最多 100 条过期记录。
- **Google 新建账号**：用户名为生成的 `user_<随机>`，昵称取 Google `name`（截断到 50 字符），密码为空（任何密码都登录不了），之后可走忘记密码设置。同样发 `UserCreated` 事件（欢迎邮件）。
- **限流**：两个 Google 接口共用登录的每 IP 配额。
- **`email_verified_at`**：`users` 新增可空列。Google 新建和按邮箱关联时写入；验证码注册成功时也写入。旧账号保持空值。API 用户 JSON 不输出它（契约里 API 用户仍没有该字段）。

## 4. 接口

API（`/v1`，公开，每 IP 限流）：

| 操作 | 请求 | 成功 |
|---|---|---|
| `GET /v1/auth/providers` | 无 | `{ google: { enabled } }` |
| `POST /v1/auth/google/authorizations` | `{ redirect_uri, return_url? }` | `201 { authorization_url, state, expires_in }` |
| `POST /v1/auth/google/sessions` | `{ state, code }` | 与 `POST /v1/login` 相同的 `{ access_token, token_type, expires_in, user }`，外加 `return_url` |

Web BFF：

| 路由 | 行为 |
|---|---|
| `GET /api/auth/google?returnUrl=...` | 调 API 发起，写 state Cookie，302 到 Google |
| `GET /api/auth/google/callback?state&code` | 比对 Cookie，调 API 换会话，写会话 Cookie，302 到 returnUrl；失败 302 到 `/login?error=<code>` |

失败码：`AUTH.GOOGLE_NOT_CONFIGURED`(503)、`AUTH.GOOGLE_STATE_INVALID`(400)、`AUTH.GOOGLE_EMAIL_UNVERIFIED`(403)、`AUTH.GOOGLE_REGISTRATION_CLOSED`(403)、`AUTH.GOOGLE_SIGN_IN_FAILED`(502)，以及沿用的 `AUTH.INVALID_CREDENTIALS`、`AUTH.TOTP_REQUIRED`、`COMMON.RATE_LIMITED`。

## 5. 验收标准

- [x] 新 Google 用户首次登录建出账号，邮箱为 Google 已验证邮箱，并有 `user_identities` 记录。
- [x] 同邮箱已有账号时只关联不新建，账号标为邮箱已验证。
- [x] 关联邮箱未验证的账号时清空密码、撤销旧会话、写审计；已验证账号保留密码和会话。
- [x] 注销账号删除其 Google 关联，同一 `sub` 不复活旧账号也不挡新账号。
- [x] 关联后 Google 邮箱变化，仍按 `sub` 登进同一账号，身份记录里的邮箱更新。
- [x] `email_verified=false` 被拒；错误或重放的 state、nonce 不符、Cookie 与 state 不符都被拒。
- [x] returnUrl 保留（如 `/console/admin/domains`）；`//evil.com`、`https://evil.com` 回落到默认页。
- [x] 禁用账号被拒且与密码登录同错；关闭注册时新用户被拒、老用户可登录。
- [x] 未配置时按钮隐藏、接口返回未配置；登录页、注册页都有按钮，中英文案正确。
- [x] 测试只用假 OIDC 提供方，不访问真实 Google。

## 6. 不做

- 其他身份提供方（GitHub、Microsoft 等）。
- 账号关联管理界面（查看、解除关联）。
- Google 登录后的两步验证输入流程。
- Admin Console 的 Google 登录。
