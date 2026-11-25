import type { LocaleMessageShape } from '../../locale-message-shape';
import type { DocsMessages } from './en-US';

const messages = {
  meta: {
    title: '开发者文档与使用指南',
    description:
      '30 秒通过 POST /v1/emails 直接发信，支持模板与验证码自动脱敏、发送状态查询、幂等重试，以及自有域名企业邮箱快速上手。',
  },
  hero: {
    eyebrow: '开发者与产品文档',
    title: 'Zmail 使用文档与 API 指南',
    description:
      '免建模板，一行 HTTP 请求即可通过自有域名发送事务邮件；同时也提供完整的企业团队邮箱与客户端接入能力。',
    primaryCta: '前往开发者后台获取 API Key',
    secondaryCta: '进入企业邮箱控制台',
  },
  toc: {
    heading: '本页目录',
    quickstart: '30 秒快速发信',
    templates: '模板发信与脱敏',
    statusIdempotency: '状态查询与幂等重试',
    authPermissions: '认证与权限说明',
    enterpriseMail: '企业邮箱快速上手',
  },
  code: {
    copy: '复制',
    copied: '已复制',
    tabs: {
      node: 'Node.js / TS',
      curl: 'curl',
      python: 'Python',
      go: 'Go',
      http: 'HTTP',
      json: '响应示例 (202)',
      statusJson: '响应示例 (200)',
    },
    aria: {
      directSend: '代码直发邮件示例',
      directResponse: '发信接口响应示例',
      templateSend: '模板发信代码示例',
      statusQuery: '查询发送状态请求与响应示例',
    },
  },
  quickstart: {
    badge: '01 · 代码直发',
    heading: '30 秒快速发信（免建模板 / 代码直发）',
    summary:
      '只需一个接口即可直接发送 HTML 或纯文本邮件。无需预先创建邮箱账号，也无需在后台配置模板——只要域名完成验证即可发信。',
    highlights: {
      endpointLabel: '统一发信接口',
      endpointDesc: '同一地址同时支持代码直发 HTML/文本与模板发信。',
      authLabel: '组织绑定 Key 免传 Organization-Id',
      authDesc:
        '使用 Authorization: Bearer <ZMAIL_API_KEY> 认证，组织绑定 Key 会自动识别所属组织，无需额外传 Organization-Id。',
      domainLabel: '只需已验证域名',
      domainDesc:
        '可直接指定该域名下的任意发件地址，例如 "YourApp <noreply@yourdomain.com>"。',
    },
    responseHeading: '返回结果',
    responseNote:
      '首次接收返回 202 Accepted，并在 Location 响应头中给出 /v1/emails/{id}，可用于后续查询投递状态。',
  },
  templates: {
    badge: '02 · 模板发信（可选）',
    heading: '模板发信（支持敏感验证码自动脱敏）',
    summary:
      '在同一个 POST /v1/emails 接口上，将请求体切换为 template + variables，即可启用模板发信模式。',
    whenTitle: '什么时候推荐使用模板？',
    reasons: {
      nonEngineerTitle: '非研发人员随时改文案',
      nonEngineerDesc:
        '产品或运营可以直接在控制台修改邮件正文与标题，无需后端修改代码或重新发布。',
      maskingTitle: '敏感验证码自动脱敏（••••••）',
      maskingDesc:
        '将验证码等字段标记为敏感变量后，收件人仍会收到完整验证码，但发信日志与后台记录中会自动脱敏为 ••••••，避免泄露。',
    },
  },
  status: {
    badge: '03 · 状态与重试',
    heading: '查询发送状态 & 幂等重试',
    summary:
      '随时查询每封邮件的真实投递阶段，并在网络超时重试时借助幂等键避免重复发信。',
    queryHeading: '查询发送状态',
    queryDesc: '使用 POST /v1/emails 返回的 id，请求 GET /v1/emails/{id} 获取最新投递状态。',
    lifecycleHeading: '状态流转说明',
    statuses: {
      queued: {
        name: 'queued',
        desc: '请求已被 Zmail 受理，正在队列中等待出站投递。',
      },
      sent: {
        name: 'sent',
        desc: '已成功投递至上游发信通道或目标邮箱服务器。',
      },
      accepted: {
        name: 'accepted',
        desc: '目标邮件服务器已明确确认接收该邮件。',
      },
      bounced: {
        name: 'bounced',
        desc: '被对方服务器退信（例如地址不存在、邮箱已满或触发策略拒收）。',
      },
      failed: {
        name: 'failed',
        desc: '重试后仍未能完成投递，可在后台发送日志中查看具体原因。',
      },
    },
    idempotencyHeading: '可选请求头：Idempotency-Key',
    idempotencyPoints: {
      omit: '普通新发信可直接省略 Idempotency-Key，服务端会自动将其视为一次新发送。',
      reuse:
        '需要防重复时，传入稳定的业务幂等键（如 order-1042-receipt）；遇到网络超时重试时复用同一个 Key，Zmail 会直接返回 200 OK 与原记录，不会重复发送。',
      conflict:
        '如果使用同一个 Idempotency-Key 但修改了请求体内容，接口会返回 409 Conflict，防止误发。',
    },
  },
  auth: {
    badge: '04 · 认证与权限',
    heading: '认证与权限说明',
    summary: '所有 API 请求均通过限定权限的 API Key 认证，并支持按组织精细分配开发者权限。',
    headersHeading: '支持的认证请求头',
    bearerTitle: 'Authorization: Bearer <ZMAIL_API_KEY>',
    bearerBadge: '推荐',
    bearerDesc: '标准 Bearer Token 认证方式，与上方所有代码示例一致。',
    xApiKeyTitle: 'X-API-Key: <ZMAIL_API_KEY>',
    xApiKeyDesc: '同样受支持，便于兼容现有代理或旧版客户端。',
    orgScopeHeading: '组织上下文说明',
    orgBound:
      '组织绑定 Key（默认推荐）会自动推导所属组织，调用时无需再传 Organization-Id 请求头。',
    legacyMultiOrg: '旧版多组织 Key 需要在每次请求中显式传入 Organization-Id: <org_id>。',
    grantHeading: '管理员如何分配开发者权限',
    grantDesc:
      '组织管理员可前往「组织管理（/console/organizations）」，为成员配置「成员 + 开发者权限」及允许发信的域名白名单。开发者即可自助创建 API Key 并从授权域名发信，无需授予管理员权限。',
    grantCta: '前往组织管理配置权限',
  },
  enterprise: {
    badge: '05 · 企业邮箱',
    heading: '企业邮箱快速上手',
    summary: '除了开发者 API，三步即可用自有域名为团队开通完整的企业邮箱。',
    steps: {
      step1Num: '01',
      step1Title: '通过 Cloudflare 绑定域名',
      step1Desc:
        '在域名管理中接入自有域名，按需选择「仅发信」（适合应用通知/验证码）或「收发一体」（适合团队日常办公），自动配置 SPF、DKIM、DMARC 与 MX 记录。',
      step2Num: '02',
      step2Title: '邀请同事并分配企业邮箱',
      step2Desc:
        '在组织管理中邀请团队成员，为其分配专属企业邮箱（如 alice@yourdomain.com）或团队共享邮箱（如 support@yourdomain.com）。',
      step3Num: '03',
      step3Title: '连接 IMAP/SMTP 或同步存量邮箱',
      step3Desc:
        '可直接使用 Web 邮箱，也可通过标准 IMAP/SMTP 接入 Apple Mail、Outlook 等客户端，或一键同步飞书邮箱、Gmail、163、QQ 邮箱等现有邮件。',
    },
  },
  cta: {
    heading: '准备好发出第一封邮件了吗？',
    description: '前往开发者后台创建组织绑定 API Key，或立即为团队配置自有域名企业邮箱。',
    developers: '前往开发者后台获取 API Key',
    mailboxes: '进入企业邮箱控制台',
  },
} as const satisfies LocaleMessageShape<DocsMessages>;

export default messages;
