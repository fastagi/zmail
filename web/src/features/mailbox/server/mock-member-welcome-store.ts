import 'server-only';

import type {
  MemberWelcomeLanguage,
  MemberWelcomeMailStatus,
  MemberWelcomePreview,
  MemberWelcomeSetting,
  UpdateMemberWelcomeSettingInput,
} from '@/features/mailbox/member-welcome-schemas';

/** The standard text of each language, as `contracts/MAILBOXES.md` (Member Welcome Mail) has it. */
const DEFAULTS: MemberWelcomeSetting['defaults'] = {
  'en-US': {
    subject: 'Welcome to {{organization_name}} on Zmail',
    body:
      'Hi {{member_name}},\n\n' +
      '{{organization_name}} has set up a Zmail mailbox for you: {{mailbox_address}}\n\n' +
      'Sign in with the email address this message was sent to: {{sign_in_url}}\n' +
      'If you also received an invitation, accept it first. Your mailbox is ready as soon as you join.',
  },
  'zh-Hans': {
    subject: '欢迎加入 {{organization_name}}',
    body:
      '{{member_name}}，你好：\n\n' +
      '{{organization_name}} 已为你开通 Zmail 邮箱：{{mailbox_address}}\n\n' +
      '请用收到这封邮件的邮箱地址登录：{{sign_in_url}}\n' +
      '如果你还收到了加入邀请，请先接受邀请，加入后即可使用邮箱。',
  },
};
const VARIABLES = ['member_name', 'mailbox_address', 'organization_name', 'sign_in_url'] as const;
const SUBJECT_MAX = 200;
const BODY_MAX = 5000;
const SIGN_IN_URL = 'http://localhost:3000/login';

interface StoredSetting {
  enabled: boolean;
  language: MemberWelcomeLanguage;
  subject: string;
  body: string;
  updatedAt: string;
}

export type MockMemberWelcomeResult =
  { ok: true; value: MemberWelcomeSetting } | { ok: false; fields: Record<string, string[]> };

/**
 * Development mock of the member welcome mail: the organization setting, previews, and the one
 * welcome per mailbox a roster import sends. Nothing is delivered.
 */
class MockMemberWelcomeStore {
  private readonly settings = new Map<number, StoredSetting>();
  private readonly welcomed = new Set<string>();

  setting(organizationId: number): MemberWelcomeSetting {
    const stored = this.settings.get(organizationId);
    const language = stored?.language ?? 'zh-Hans';
    return {
      enabled: stored?.enabled ?? true,
      language,
      subject: stored?.subject || DEFAULTS[language].subject,
      body: stored?.body || DEFAULTS[language].body,
      customized: Boolean(stored?.subject || stored?.body),
      variables: [...VARIABLES],
      defaults: DEFAULTS,
      updated_at: stored?.updatedAt ?? null,
    };
  }

  update(organizationId: number, input: UpdateMemberWelcomeSettingInput): MockMemberWelcomeResult {
    const subject = input.subject.trim();
    const body = input.body.replace(/\r\n/gu, '\n').trim();
    const fields = validate(subject, body);
    if (fields) return { ok: false, fields };
    const standard = DEFAULTS[input.language];
    this.settings.set(organizationId, {
      enabled: input.enabled,
      language: input.language,
      subject: subject === standard.subject ? '' : subject,
      body: body === standard.body ? '' : body,
      updatedAt: new Date().toISOString(),
    });
    return { ok: true, value: this.setting(organizationId) };
  }

  preview(
    organizationName: string,
    input: { language: MemberWelcomeLanguage; subject: string; body: string }
  ): { ok: true; value: MemberWelcomePreview } | { ok: false; fields: Record<string, string[]> } {
    const subject = input.subject.trim() || DEFAULTS[input.language].subject;
    const body = input.body.trim() || DEFAULTS[input.language].body;
    const fields = validate(subject, body);
    if (fields) return { ok: false, fields };
    const values: Record<string, string> = {
      member_name: input.language === 'zh-Hans' ? '陈晓明' : 'Alex Chen',
      mailbox_address: input.language === 'zh-Hans' ? 'xiaoming@example.com' : 'alex@example.com',
      organization_name: organizationName,
      sign_in_url: SIGN_IN_URL,
    };
    const fill = (text: string) =>
      text.replace(
        /\{\{\s*([a-z_]+)\s*(?:\|[^}]*)?\}\}/gu,
        (_, name: string) => values[name] ?? ''
      );
    const text = `${fill(body)}\n\n${setupSection(input.language, values.mailbox_address)}`;
    return {
      ok: true,
      value: { subject: fill(subject), text, html: `<pre>${escapeHtml(text)}</pre>` },
    };
  }

  /** The welcome outcome of a provisioned roster row: once per mailbox, unless turned off. */
  welcome(organizationId: number, mailboxId: number): MemberWelcomeMailStatus {
    if (!this.setting(organizationId).enabled) return 'disabled';
    const key = `${organizationId}:${mailboxId}`;
    if (this.welcomed.has(key)) return 'already_sent';
    this.welcomed.add(key);
    return 'queued';
  }

  reset(): void {
    this.settings.clear();
    this.welcomed.clear();
  }
}

function validate(subject: string, body: string): Record<string, string[]> | null {
  const fields: Record<string, string[]> = {};
  const check = (name: 'subject' | 'body', value: string, limit: number) => {
    if (value.length > limit) {
      fields[name] = [`is longer than ${limit} characters`];
      return;
    }
    if (name === 'subject' && /[\r\n]/u.test(value)) {
      fields[name] = ['must be one line'];
      return;
    }
    const opened = value.split('{{').length - 1;
    const tokens = [...value.matchAll(/\{\{\s*([^}|]*?)\s*(?:\|[^}]*)?\}\}/gu)];
    if (tokens.length !== opened) {
      fields[name] = ['has a malformed {{variable}}'];
      return;
    }
    const unknown = tokens.find(
      token => !VARIABLES.includes(token[1] as (typeof VARIABLES)[number])
    );
    if (unknown) fields[name] = [`uses the unknown variable ${unknown[1]}`];
  };
  check('subject', subject, SUBJECT_MAX);
  check('body', body, BODY_MAX);
  return Object.keys(fields).length > 0 ? fields : null;
}

function setupSection(language: MemberWelcomeLanguage, address: string): string {
  return language === 'en-US'
    ? `Set up a mail app\n\nUser name: ${address}\nIncoming mail (IMAP): 127.0.0.1, port 1993, SSL/TLS\nOutgoing mail (SMTP): 127.0.0.1, port 1465 (SSL/TLS) or 1587 (STARTTLS)`
    : `设置邮件客户端\n\n用户名：${address}\n收信服务器（IMAP）：127.0.0.1，端口 1993，SSL/TLS\n发信服务器（SMTP）：127.0.0.1，端口 1465（SSL/TLS）或 1587（STARTTLS）`;
}

function escapeHtml(text: string): string {
  return text
    .replace(/&/gu, '&amp;')
    .replace(/</gu, '&lt;')
    .replace(/>/gu, '&gt;')
    .replace(/"/gu, '&quot;');
}

export const mockMemberWelcomeStore = new MockMemberWelcomeStore();
