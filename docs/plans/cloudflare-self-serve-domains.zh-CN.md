# 客户自助接入域名（Cloudflare 托管 + 修改 NS）— 设计

状态：已批准（2026-11-19，选择「先用 Cloudflare 改 NS」；第 3.2、3.3 节借鉴飞书邮箱的接入设计）。是 [mail-domains.md](mail-domains.md) 的扩展：那份计划已定的「Zmail 自动在 Cloudflare 写入
收发信记录、逐条显示状态、`pending/verified`、邮箱选择域名」全部沿用，这里只写新增部分。
自建收发信方案 [self-hosted-mail-domains.zh-CN.md](self-hosted-mail-domains.zh-CN.md) 暂停，作为以后不想迁 DNS 的客户的长期方案。

## 1. 目标与取舍

**目标。** DNS 在任何服务商（阿里云、腾讯云、GoDaddy……）的公司，都能在 Zmail 控制台自助接入域名：
输入域名 → 到域名注册商把 NS 改成页面给出的两条 Cloudflare 地址 → 生效后 Zmail 自动配置收发信 → 创建邮箱收发。
发信与送达继续由 Cloudflare 负责，现在就能发到 Gmail、QQ 等真实邮箱。

**为什么这样做（用户已决定）。** 现有流程要求域名已经在实例的 Cloudflare 账号里，客户自己做不到。
让 Zmail 通过 Cloudflare API 把客户域名加进我们的账号、客户只改 NS，就能复用全部现有收发信能力，不用自建邮件服务器。

**代价（用户已接受）。**
- 客户整个域名的 DNS（网站、其他服务）都搬进 Zmail 的 Cloudflare 账号；客户没有 Cloudflare 登录，
  所以 Zmail 必须提供 DNS 记录编辑页（第 5 节）。
- Cloudflare 免费套餐只能添加主域名（`example.com`），不能单独添加子域名。客户改的是主域名的 NS；
  收发信可以用主域名或它的子域名（如 `mail.example.com`）。
- 与 Cloudflare 绑定更深；Cloudflare 账号的配额与稳定性成为所有客户的共同依赖。

**非目标。** 客户自带 Cloudflare 账号；部分（CNAME）接入（需 Business 套餐）；DNSSEC 管理；
批量导入/导出 DNS；Cloudflare 代理（橙色云）以外的 Cloudflare 功能设置。

## 2. 名词

- **托管区域（mail zone）**：Zmail 在自己的 Cloudflare 账号里为某组织创建的 Cloudflare zone，对应一个主域名。
  状态 `pending`（等待客户改 NS）→ `active`（NS 已指向 Cloudflare）。一个托管区域属于一个组织。
- **邮件域名（mail domain）**：沿用现有概念，是托管区域的主域名或其子域名，新增能力字段：
  `send_receive`（收发）或 `send_only`（仅发信，不开启 Email Routing，不动 MX）。
- **运营者区域**：运营者事先放进账号、不是 Zmail 创建的 zone。行为不变（沿用 `mail-domains.md`，token 范围即信任边界）。

## 3. 流程

### 3.1 接入

1. 所有者/管理员输入 `mail.example.com`（或 `example.com`）并选择能力。默认 `send_receive`；
   页面先查该名字现有 MX，若指向别家邮箱（如 `mxbiz1.qq.com`），默认改为 `send_only` 并提示
   「想在 Zmail 收信又不影响现有邮箱，请用子域名，如 mail.example.com」。
2. Zmail 用公共后缀表（`golang.org/x/net/publicsuffix`）求出主域名 `example.com`，然后：
   - 账号里已有该 zone，且是本组织的托管区域 → 直接用它，走第 6 步。
   - 账号里已有该 zone，且是运营者区域 → 沿用现有行为（任何组织可认领）。
   - 账号里已有该 zone，且是别的组织的托管区域 → `409 MAILBOX.DOMAIN_TAKEN`。
   - 账号里没有 → 创建 zone（`type: full`），并让 Cloudflare 扫描导入现有 DNS 记录，
     保存托管区域（`pending`，含 Cloudflare 分配的两条 NS 与域名原来的 NS），保存邮件域名（`pending`）。
3. 页面显示：「到域名注册商把 example.com 的 DNS 服务器改为：`xxx.ns.cloudflare.com`、`yyy.ns.cloudflare.com`」，
   附阿里云 / 腾讯云 / GoDaddy 的修改指引，以及**导入的现有记录清单，请客户核对网站等记录是否齐全**（缺的可先在 DNS 编辑页补上，改 NS 前补好不会影响网站）。
4. 客户改 NS。Zmail 每 10 分钟查一次所有 `pending` 托管区域的状态；客户点「检查」时额外请求 Cloudflare 立即复查
   （Cloudflare 对复查限频，失败只提示稍后再试）。
5. 区域变为 `active` → 自动对该区域下所有 `pending` 邮件域名运行现有的「设置」（`send_only` 跳过 Email Routing）。
6. 区域已是 `active` 时直接运行设置。设置与检查完全沿用 `mail-domains.md`，验证通过即可建邮箱。

### 3.2 先发信、后收信（借鉴飞书「邮箱搬家后启用」）

飞书允许先只加一条 TXT 验证归属、旧邮箱照常收信，最后才切 MX。Zmail 的对应做法：

- `send_only` 域名验证通过后，页面提供「开始在 Zmail 收信」：`POST /v1/mail-domains/{id}/receiving`
  把能力改为 `send_receive` 并重新运行设置（开启 Email Routing）。确认框列出将被取代的现有 MX，并提示先备份。
- 严格检查沿用现有规则：名字上还有别家的 MX 时，该行显示 `conflict`，域名不会通过验证。
  客户可以在 DNS 编辑页删掉旧 MX（旧 MX 不是 Zmail 使用的记录，不锁定）。

### 3.3 页面细节（借鉴飞书）

- 按注册商给出改 NS 的分步指引（阿里云、腾讯云 DNSPod、华为云、GoDaddy、Namecheap），默认展开客户原 NS 所属的那一家
  （根据 `original_name_servers` 的后缀判断，判断不出就全部折叠）。
- 「立即检查」按钮：即第 4 步的复查。
- 域名验证通过后显示收发测试清单：同域互发、发往外部（如 Gmail）、外部发来、回复；只是提示，不自动发信。

### 3.4 超时与删除

- `pending` 托管区域 14 天未生效：删除 Cloudflare zone 和对应行（NS 从未指过来，删除不影响客户）。
- 删除邮件域名：沿用现有规则（有邮箱时 `409`），**从不删除 `active` 的托管区域**。
- 删除托管区域（「移出 Zmail」）：仅当区域下没有邮件域名，且公网查询该域名的 NS 已不再指向 Cloudflare 时允许；
  否则 `409 MAILBOX.ZONE_STILL_DELEGATED`，提示先把 NS 改回原来的服务商（页面显示原 NS）。这样不会误删客户整站 DNS。

## 4. 持久化

新表 `mail_zones`（`mailbox` 模块唯一写入者）：

| 列 | 说明 |
|---|---|
| `id` bigserial | |
| `organization_id` bigint，外键级联 | 所属组织 |
| `name` varchar(253) 唯一 | 主域名 |
| `cloudflare_zone_id` varchar(64) 唯一 | |
| `status` varchar(20) CHECK `pending`/`active` | |
| `name_servers` jsonb | Cloudflare 分配的 NS |
| `original_name_servers` jsonb | 创建时公网查到的原 NS，用于「移出」指引 |
| `activated_at`、`last_checked_at` timestamptz | |
| `created_by`、`created_at`、`updated_at` | |

`mail_domains` 新增 `mail_zone_id`（可空，外键，运营者区域为空）、`capability`（默认 `send_receive`）。
每组织最多 3 个托管区域（Cloudflare 账号对新增 zone 有频率与数量限制，运营者可调 `MAILBOX_MAX_ZONES_PER_ORGANIZATION`）。

## 5. DNS 记录编辑

- 范围：本组织的 `active` 或 `pending` 托管区域；类型 A、AAAA、CNAME、TXT、MX；字段：名称、内容、TTL、MX 优先级、
  A/AAAA/CNAME 的「Cloudflare 代理」开关。
- 读取与写入直接调用 Cloudflare API，Zmail 不存 DNS 记录副本（Cloudflare 是唯一来源）。
- **邮件相关记录锁定**：Email Routing 的 MX/SPF、发信记录、DKIM、`_dmarc`，以及邮件域名上的 MX，显示「由 Zmail 邮件使用」，
  不能在编辑页修改或删除（否则收发信会坏）；要改需先删除对应邮件域名。
- 权限：成员可读；所有者、管理员可写。每次写入记审计（记录类型、名称，不记内容）。
- 输入校验在 Zmail 侧先做（名称属于本区域、IP 格式、TXT 长度、CNAME 不与其他记录同名），Cloudflare 的拒绝原因转成稳定错误码返回。

## 6. HTTP 契约（新增/修改）

| 路由 | 说明 |
|---|---|
| `POST /v1/mail-domains` | 请求新增 `capability`；不再因「账号里没有 zone」返回 `422 DOMAIN_ZONE_NOT_FOUND`，改为创建托管区域；响应新增 `capability`、`zone`（`id`、`name`、`status`、`name_servers`、`original_name_servers`）|
| `POST /v1/mail-domains/{id}/checks` | 区域 `pending` 时同时请求 Cloudflare 复查 |
| `POST /v1/mail-domains/{id}/receiving` | `send_only` → `send_receive` 并重新设置；已是收发时 `200` 原样返回；区域未生效 `409 MAILBOX.ZONE_PENDING` |
| `POST /v1/mail-domain-previews` | 只读：输入名字，返回主域名、公网现有 MX、建议能力（MX 指向别家时为 `send_only`），供第 3.1 步提示 |
| `GET /v1/mail-zones`、`GET /v1/mail-zones/{id}` | 本组织的托管区域 |
| `DELETE /v1/mail-zones/{id}` | 移出 Zmail；`409 MAILBOX.ZONE_IN_USE` / `409 MAILBOX.ZONE_STILL_DELEGATED` |
| `GET /v1/mail-zones/{id}/dns-records` | 全部记录，每条带 `locked` 与锁定原因 |
| `POST /v1/mail-zones/{id}/dns-records`、`PATCH …/{record_id}`、`DELETE …/{record_id}` | 增改删；锁定记录 `409 MAILBOX.DNS_RECORD_LOCKED`；Cloudflare 拒绝 `422 MAILBOX.DNS_RECORD_REJECTED` |

Web（Next.js BFF）对应新增适配路由；开发 mock BFF 内存实现，第一次检查即 `active`。

## 7. 运营者前置条件

- Cloudflare API token 需要新增权限：**Account → Zone: Edit**（创建/删除 zone），以及现有的 DNS Edit、Zone Settings Edit、
  Email Routing Rules Edit、Email Sending Edit。现在的 `zmail-fe5-mail-domains` token 需要在 Cloudflare 后台加上
  创建 zone 的权限（这一步需要你在 Cloudflare 控制台操作）。
- 功能开关沿用 `MAILBOX_CUSTOM_DOMAINS_ENABLED=true`。
- 确认 Cloudflare 账号可新增 zone 的数量上限，必要时升级账号。

## 8. 测试

- 服务测试（假 Cloudflare 客户端）：主域名/子域名求解（含 `example.com.cn` 等多级后缀）；四种 zone 归属分支；
  创建后 `pending`、轮询变 `active` 自动设置；`send_only` 不调 Email Routing；14 天清理；删除域名不删区域；
  「移出」两种 `409`；DNS 编辑的锁定规则与权限。
- Cloudflare 客户端测试（httptest）：创建 zone、扫描导入、复查、zone 状态、DNS 记录增删改查。
- 仓库测试（PostgreSQL）：唯一约束、并发两个组织添加同一主域名只有一个成功。
- Web：接入页 NS 指引与导入记录核对、DNS 编辑页、锁定记录不可编辑。
- 真实验收（fe5 / zmail.im）：用一个非 Cloudflare 的测试域名改 NS，接入后发信到 `xishudev@gmail.com`，
  原始头部 `spf=pass dkim=pass dmarc=pass`。

## 9. 交付切片

1. **改 NS 接入**：托管区域、创建 zone + 导入记录、NS 指引页、状态轮询、自动设置、`send_only`、清理。
   验收：真实测试域名改 NS 后收发信。
2. **DNS 编辑页**：记录增删改查与锁定。没有它客户改 NS 后无法维护网站记录，所以两个切片一起上线（同一功能开关）。
