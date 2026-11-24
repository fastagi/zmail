import type {
  MailUiProviderDetection,
  MailUiProviderId,
  MailUiProviderPreset,
} from './types.ts';

export const MAIL_UI_MAX_CONNECTED_ACCOUNTS = 30;

interface DomainServerOverride {
  imapHost: string;
  smtpHost: string;
}

const NETEASE_DOMAIN_OVERRIDES: Readonly<Record<string, DomainServerOverride>> = {
  '126.com': { imapHost: 'imap.126.com', smtpHost: 'smtp.126.com' },
  'yeah.net': { imapHost: 'imap.yeah.net', smtpHost: 'smtp.yeah.net' },
};

export const MAIL_UI_PROVIDER_PRESETS: readonly MailUiProviderPreset[] = [
  {
    id: 'gmail',
    label: 'Gmail',
    shortLabel: 'G',
    region: 'global',
    enterprise: false,
    authStrategy: 'oauth2',
    imapHost: 'imap.gmail.com',
    imapPort: 993,
    imapTls: 'tls',
    smtpHost: 'smtp.gmail.com',
    smtpPort: 465,
    smtpTls: 'tls',
    domains: ['gmail.com', 'googlemail.com'],
    helpUrl: 'https://support.google.com/mail/answer/7126229',
    badgeColor: '#EA4335',
    guidance: 'Connect Gmail in one click with Google OAuth 2.0.',
  },
  {
    id: 'outlook',
    label: 'Outlook / Microsoft 365',
    shortLabel: 'O',
    region: 'global',
    enterprise: false,
    authStrategy: 'oauth2',
    imapHost: 'outlook.office365.com',
    imapPort: 993,
    imapTls: 'tls',
    smtpHost: 'smtp.office365.com',
    smtpPort: 587,
    smtpTls: 'starttls',
    domains: ['outlook.com', 'hotmail.com', 'live.com', 'msn.com', 'outlook.cn'],
    helpUrl:
      'https://support.microsoft.com/en-us/office/pop-imap-and-smtp-settings-for-outlook-com-d088b986-291d-42b8-9564-9c414e2aa040',
    badgeColor: '#0078D4',
    guidance: 'Connect Outlook, Hotmail, or Live with Microsoft OAuth 2.0.',
  },
  {
    id: 'icloud',
    label: 'iCloud Mail',
    shortLabel: 'iC',
    region: 'global',
    enterprise: false,
    authStrategy: 'app_password',
    imapHost: 'imap.mail.me.com',
    imapPort: 993,
    imapTls: 'tls',
    smtpHost: 'smtp.mail.me.com',
    smtpPort: 587,
    smtpTls: 'starttls',
    domains: ['icloud.com', 'me.com', 'mac.com'],
    helpUrl: 'https://support.apple.com/en-us/102654',
    badgeColor: '#0EA5E9',
    guidance: 'Generate an Apple app-specific password; IMAP & SMTP are pre-filled.',
  },
  {
    id: 'qq',
    label: 'QQ Mail',
    shortLabel: 'QQ',
    region: 'cn',
    enterprise: false,
    authStrategy: 'app_password',
    imapHost: 'imap.qq.com',
    imapPort: 993,
    imapTls: 'tls',
    smtpHost: 'smtp.qq.com',
    smtpPort: 465,
    smtpTls: 'tls',
    domains: ['qq.com', 'foxmail.com', 'vip.qq.com'],
    helpUrl: 'https://service.mail.qq.com/detail/0/75',
    badgeColor: '#F59E0B',
    guidance: 'Enable IMAP/SMTP in QQ Mail settings and paste your 16-character authorization code.',
  },
  {
    id: 'netease163',
    label: 'NetEase 163 / 126',
    shortLabel: '163',
    region: 'cn',
    enterprise: false,
    authStrategy: 'app_password',
    imapHost: 'imap.163.com',
    imapPort: 993,
    imapTls: 'tls',
    smtpHost: 'smtp.163.com',
    smtpPort: 465,
    smtpTls: 'tls',
    domains: ['163.com', 'vip.163.com', '126.com', 'yeah.net'],
    helpUrl: 'https://help.mail.163.com/',
    badgeColor: '#10B981',
    guidance: 'Enable IMAP/SMTP in NetEase Mail settings and sign in with your authorization code.',
  },
  {
    id: 'fastmail',
    label: 'Fastmail',
    shortLabel: 'FM',
    region: 'global',
    enterprise: false,
    authStrategy: 'app_password',
    imapHost: 'imap.fastmail.com',
    imapPort: 993,
    imapTls: 'tls',
    smtpHost: 'smtp.fastmail.com',
    smtpPort: 465,
    smtpTls: 'tls',
    domains: ['fastmail.com', 'fastmail.fm'],
    helpUrl: 'https://www.fastmail.help/hc/en-us/articles/1500000278342-Server-names-and-ports',
    badgeColor: '#4F46E5',
    guidance: 'Create a Fastmail app password with IMAP & SMTP access.',
  },
  {
    id: 'yahoo',
    label: 'Yahoo Mail',
    shortLabel: 'Y!',
    region: 'global',
    enterprise: false,
    authStrategy: 'app_password',
    imapHost: 'imap.mail.yahoo.com',
    imapPort: 993,
    imapTls: 'tls',
    smtpHost: 'smtp.mail.yahoo.com',
    smtpPort: 465,
    smtpTls: 'tls',
    domains: ['yahoo.com', 'ymail.com', 'yahoo.co.jp', 'yahoo.co.uk'],
    helpUrl: 'https://help.yahoo.com/kb/SLN15241.html',
    badgeColor: '#7C3AED',
    guidance: 'Generate a Yahoo app password from Account Security.',
  },
  {
    id: 'zoho',
    label: 'Zoho Mail',
    shortLabel: 'ZH',
    region: 'global',
    enterprise: true,
    authStrategy: 'app_password',
    imapHost: 'imap.zoho.com',
    imapPort: 993,
    imapTls: 'tls',
    smtpHost: 'smtp.zoho.com',
    smtpPort: 465,
    smtpTls: 'tls',
    domains: ['zoho.com', 'zohomail.com'],
    helpUrl: 'https://www.zoho.com/mail/help/imap-access.html',
    badgeColor: '#E11D48',
    guidance: 'Enable IMAP access in Zoho Mail settings and use an app-specific password.',
  },
  {
    id: 'exmail',
    label: 'Tencent Exmail',
    shortLabel: 'EX',
    region: 'cn',
    enterprise: true,
    authStrategy: 'app_password',
    imapHost: 'imap.exmail.qq.com',
    imapPort: 993,
    imapTls: 'tls',
    smtpHost: 'smtp.exmail.qq.com',
    smtpPort: 465,
    smtpTls: 'tls',
    domains: ['exmail.qq.com'],
    helpUrl: 'https://service.exmail.qq.com/cgi-bin/help?subtype=1&id=28&no=1000585',
    badgeColor: '#2563EB',
    guidance: 'Generate a client password in Tencent Exmail security settings.',
  },
  {
    id: 'feishu',
    label: 'Feishu Mail',
    shortLabel: 'FS',
    region: 'cn',
    enterprise: true,
    authStrategy: 'app_password',
    imapHost: 'imap.feishu.cn',
    imapPort: 993,
    imapTls: 'tls',
    smtpHost: 'smtp.feishu.cn',
    smtpPort: 465,
    smtpTls: 'tls',
    domains: ['feishu.cn', 'larksuite.com'],
    helpUrl: 'https://www.feishu.cn/hc/en-US/articles/360049067887',
    badgeColor: '#00B96B',
    guidance: 'Create a third-party client password in Feishu Mail settings.',
  },
  {
    id: 'aliyun',
    label: 'Alibaba Mail',
    shortLabel: 'AL',
    region: 'cn',
    enterprise: true,
    authStrategy: 'app_password',
    imapHost: 'imap.aliyun.com',
    imapPort: 993,
    imapTls: 'tls',
    smtpHost: 'smtp.aliyun.com',
    smtpPort: 465,
    smtpTls: 'tls',
    domains: ['aliyun.com'],
    helpUrl: 'https://help.aliyun.com/document_detail/36576.html',
    badgeColor: '#F97316',
    guidance: 'Enable IMAP/SMTP in Alibaba Mail settings and use your third-party client password.',
  },
  {
    id: 'sina',
    label: 'Sina Mail',
    shortLabel: 'SN',
    region: 'cn',
    enterprise: false,
    authStrategy: 'app_password',
    imapHost: 'imap.sina.com',
    imapPort: 993,
    imapTls: 'tls',
    smtpHost: 'smtp.sina.com',
    smtpPort: 465,
    smtpTls: 'tls',
    domains: ['sina.com', 'sina.cn'],
    helpUrl: 'https://help.sina.com.cn/',
    badgeColor: '#DC2626',
    guidance: 'Enable IMAP/SMTP in Sina Mail settings and sign in with your authorization code.',
  },
  {
    id: 'custom',
    label: 'Custom IMAP / SMTP',
    shortLabel: 'IMAP',
    region: 'global',
    enterprise: true,
    authStrategy: 'password',
    imapHost: '',
    imapPort: 993,
    imapTls: 'tls',
    smtpHost: '',
    smtpPort: 465,
    smtpTls: 'tls',
    domains: [],
    helpUrl: 'https://datatracker.ietf.org/doc/html/rfc9051',
    badgeColor: '#64748B',
    guidance: 'Connect any standard mailbox using its IMAP (TLS 993) and SMTP credentials.',
  },
];

const PRESET_BY_ID: ReadonlyMap<MailUiProviderId, MailUiProviderPreset> = new Map(
  MAIL_UI_PROVIDER_PRESETS.map((preset) => [preset.id, preset])
);

const PRESET_BY_DOMAIN: ReadonlyMap<string, MailUiProviderPreset> = new Map(
  MAIL_UI_PROVIDER_PRESETS.flatMap((preset) =>
    preset.domains.map((domain) => [domain.toLowerCase(), preset] as const)
  )
);

const CUSTOM_IMAP_PRESET = PRESET_BY_ID.get('custom') as MailUiProviderPreset;

const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]{2,}$/u;

export function getProviderPreset(providerId: string): MailUiProviderPreset {
  const normalized = providerId.trim().toLowerCase() as MailUiProviderId;
  return PRESET_BY_ID.get(normalized) ?? CUSTOM_IMAP_PRESET;
}

/**
 * Auto-detects the mail provider preset from an email address.
 * Returns `null` when the input is not a valid email address; otherwise returns
 * the matching preset (with domain-specific IMAP/SMTP overrides) or a Custom IMAP fallback.
 */
export function detectProviderFromEmail(email: string): MailUiProviderDetection | null {
  const normalized = email.trim().toLowerCase();
  if (!EMAIL_PATTERN.test(normalized)) {
    return null;
  }

  const atIndex = normalized.lastIndexOf('@');
  const local = normalized.slice(0, atIndex);
  const domain = normalized.slice(atIndex + 1);
  if (
    local.length === 0 ||
    local.length > 64 ||
    local.startsWith('.') ||
    local.endsWith('.') ||
    local.includes('..') ||
    domain.startsWith('.') ||
    domain.endsWith('.') ||
    domain.includes('..')
  ) {
    return null;
  }

  const matched = PRESET_BY_DOMAIN.get(domain);
  if (matched) {
    const override = matched.id === 'netease163' ? NETEASE_DOMAIN_OVERRIDES[domain] : undefined;
    const preset: MailUiProviderPreset = override
      ? { ...matched, imapHost: override.imapHost, smtpHost: override.smtpHost }
      : matched;
    return {
      email: normalized,
      domain,
      valid: true,
      matched: true,
      preset,
    };
  }

  return {
    email: normalized,
    domain,
    valid: true,
    matched: false,
    preset: {
      ...CUSTOM_IMAP_PRESET,
      imapHost: `imap.${domain}`,
      smtpHost: `smtp.${domain}`,
    },
  };
}
