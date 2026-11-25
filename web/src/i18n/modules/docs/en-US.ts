const messages = {
  meta: {
    title: 'Documentation & API Guide',
    description:
      'Send transactional emails in 30 seconds with POST /v1/emails, use templates with automatic OTP masking, check delivery status, and set up Zmail enterprise mailboxes.',
  },
  hero: {
    eyebrow: 'Documentation',
    title: 'Zmail API & Quickstart Guide',
    description:
      'Send transactional emails over a single HTTP endpoint without pre-created templates, or set up enterprise team mailboxes on your own domain.',
    primaryCta: 'Get API Key in Developer Console',
    secondaryCta: 'Open Mailbox Console',
  },
  toc: {
    heading: 'On this page',
    quickstart: '30-Second Quickstart',
    templates: 'Template Sending',
    statusIdempotency: 'Status & Idempotency',
    authPermissions: 'Auth & Permissions',
    enterpriseMail: 'Enterprise Mailboxes',
  },
  code: {
    copy: 'Copy',
    copied: 'Copied',
    tabs: {
      node: 'Node.js / TS',
      curl: 'curl',
      python: 'Python',
      go: 'Go',
      http: 'HTTP',
      json: 'Response (202)',
      statusJson: 'Response (200)',
    },
    aria: {
      directSend: 'Direct email send code examples',
      directResponse: 'Direct email send JSON response',
      templateSend: 'Template email send code examples',
      statusQuery: 'Query email status request and response',
    },
  },
  quickstart: {
    badge: '01 · Direct Send',
    heading: '30-Second Quickstart (No Template Required)',
    summary:
      'Send HTML or plain-text email directly from your code using a single endpoint. No pre-created mailbox or template is needed — only a verified sending domain.',
    highlights: {
      endpointLabel: 'Endpoint',
      endpointDesc: 'Accepts direct HTML/text or template payloads on the same URL.',
      authLabel: 'Organization-bound key',
      authDesc:
        'Pass Authorization: Bearer <ZMAIL_API_KEY>. No Organization-Id header is needed for org-bound keys.',
      domainLabel: 'Verified domain only',
      domainDesc:
        'Send from any address on your verified domain, such as "YourApp <noreply@yourdomain.com>".',
    },
    responseHeading: 'Response',
    responseNote:
      'Returns 202 Accepted with a Location header pointing to /v1/emails/{id} so you can poll delivery status.',
  },
  templates: {
    badge: '02 · Templates (Optional)',
    heading: 'Template Sending & Automatic OTP Masking',
    summary:
      'Switch to template mode on the exact same POST /v1/emails endpoint by passing template and variables instead of from, subject, and html.',
    whenTitle: 'When to use templates',
    reasons: {
      nonEngineerTitle: 'Non-engineer copy edits',
      nonEngineerDesc:
        'Product and operations teams can update email copy and subjects in the console without redeploying your backend.',
      maskingTitle: 'Automatic OTP masking (••••••)',
      maskingDesc:
        'Variables marked as sensitive (such as login verification codes) are delivered to the recipient in full, while automatically redacted as •••••• in send logs and console history.',
    },
  },
  status: {
    badge: '03 · Delivery & Retries',
    heading: 'Delivery Status & Idempotent Retries',
    summary:
      'Track every message through its delivery lifecycle and safely retry timed-out requests without sending duplicate emails.',
    queryHeading: 'Query email status',
    queryDesc: 'Poll GET /v1/emails/{id} using the id returned by POST /v1/emails.',
    lifecycleHeading: 'Status lifecycle',
    statuses: {
      queued: {
        name: 'queued',
        desc: 'Accepted by Zmail and queued for outbound SMTP/provider delivery.',
      },
      sent: {
        name: 'sent',
        desc: 'Handed off to the upstream mail relay or recipient MX server.',
      },
      accepted: {
        name: 'accepted',
        desc: 'Confirmed accepted by the receiving mail server.',
      },
      bounced: {
        name: 'bounced',
        desc: 'Rejected by the recipient server (invalid address, full inbox, or policy block).',
      },
      failed: {
        name: 'failed',
        desc: 'Delivery could not complete after retries; inspect the error details in logs.',
      },
    },
    idempotencyHeading: 'Idempotency-Key header (optional)',
    idempotencyPoints: {
      omit: 'Omit Idempotency-Key for regular new sends — Zmail automatically treats each request as a new message.',
      reuse:
        'Set a stable Idempotency-Key (for example, order-1042-receipt) and reuse the exact same key when retrying after a network timeout. Zmail returns 200 OK with the original message instead of sending twice.',
      conflict:
        'Reusing an existing Idempotency-Key with a different request body returns 409 Conflict to protect against accidental payload mismatches.',
    },
  },
  auth: {
    badge: '04 · Security & Access',
    heading: 'Authentication & Permissions',
    summary:
      'Authenticate every API call with a scoped API key and delegate developer access cleanly per organization.',
    headersHeading: 'Authentication headers',
    bearerTitle: 'Authorization: Bearer <ZMAIL_API_KEY>',
    bearerBadge: 'Recommended',
    bearerDesc: 'Standard HTTP bearer token header used across all examples above.',
    xApiKeyTitle: 'X-API-Key: <ZMAIL_API_KEY>',
    xApiKeyDesc: 'Alternative header supported for legacy clients and proxies.',
    orgScopeHeading: 'Organization scoping',
    orgBound:
      'Organization-bound keys (default for new keys) automatically infer your organization — omit the Organization-Id header.',
    legacyMultiOrg:
      'Legacy multi-organization keys must include Organization-Id: <org_id> on every request.',
    grantHeading: 'Granting Developer access',
    grantDesc:
      'Organization admins can open Organization Management (/console/organizations) to assign a teammate Member + Developer permissions with a domain allowlist. Developers can then create API keys and send from approved domains without full admin access.',
    grantCta: 'Manage organization members',
  },
  enterprise: {
    badge: '05 · Team Mail',
    heading: 'Enterprise Mailbox Quickstart',
    summary:
      'Beyond transactional APIs, Zmail runs full team email on your own domain in three steps.',
    steps: {
      step1Num: '01',
      step1Title: 'Bind your domain via Cloudflare',
      step1Desc:
        'Connect your domain in Domain Settings and choose Send-only (for transactional API mail) or Full Send & Receive (for team mailboxes). DNS records (SPF, DKIM, DMARC, MX) are provisioned automatically.',
      step2Num: '02',
      step2Title: 'Invite colleagues & assign mailboxes',
      step2Desc:
        'Invite teammates in Organization Settings and assign company addresses (such as alice@yourdomain.com or shared support@yourdomain.com) in minutes.',
      step3Num: '03',
      step3Title: 'Connect IMAP/SMTP or sync existing mailboxes',
      step3Desc:
        'Use Zmail directly on the web, connect Apple Mail / Outlook / Thunderbird via IMAP & SMTP, or sync existing mailboxes from Feishu, Gmail, 163, and QQ Mail.',
    },
  },
  cta: {
    heading: 'Ready to send your first email?',
    description:
      'Generate an organization-bound API key in the developer console or configure your team mailboxes now.',
    developers: 'Get API Key in Developer Console',
    mailboxes: 'Open Mailbox Console',
  },
} as const;

export default messages;
export type DocsMessages = typeof messages;
