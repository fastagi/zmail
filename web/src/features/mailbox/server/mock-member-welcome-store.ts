import 'server-only';

import type {
  MemberWelcomeLanguage,
  MemberWelcomeMailStatus,
  MemberWelcomePreview,
  MemberWelcomeSetting,
  UpdateMemberWelcomeSettingInput,
} from '@/features/mailbox/member-welcome-schemas';

/**
 * The standard text of each language, as the API has it. Chinese copy is written as Unicode escapes
 * because source files stay English-only.
 */
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
    subject: '\u6b22\u8fce\u52a0\u5165 {{organization_name}}',
    body:
      '{{member_name}}\uff0c\u4f60\u597d\uff1a\n\n' +
      '{{organization_name}} \u5df2\u4e3a\u4f60\u5f00\u901a Zmail \u90ae\u7bb1\uff1a{{mailbox_address}}\n\n' +
      '\u8bf7\u7528\u6536\u5230\u8fd9\u5c01\u90ae\u4ef6\u7684\u90ae\u7bb1\u5730\u5740\u767b\u5f55\uff1a{{sign_in_url}}\n' +
      '\u5982\u679c\u4f60\u8fd8\u6536\u5230\u4e86\u52a0\u5165\u9080\u8bf7\uff0c\u8bf7\u5148\u63a5\u53d7\u9080\u8bf7\uff0c\u52a0\u5165\u540e\u5373\u53ef\u4f7f\u7528\u90ae\u7bb1\u3002',
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
      member_name: input.language === 'zh-Hans' ? '\u9648\u6653\u660e' : 'Alex Chen',
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
    : `\u8bbe\u7f6e\u90ae\u4ef6\u5ba2\u6237\u7aef\n\n\u7528\u6237\u540d\uff1a${address}\n\u6536\u4fe1\u670d\u52a1\u5668\uff08IMAP\uff09\uff1a127.0.0.1\uff0c\u7aef\u53e3 1993\uff0cSSL/TLS\n\u53d1\u4fe1\u670d\u52a1\u5668\uff08SMTP\uff09\uff1a127.0.0.1\uff0c\u7aef\u53e3 1465\uff08SSL/TLS\uff09\u6216 1587\uff08STARTTLS\uff09`;
}

function escapeHtml(text: string): string {
  return text
    .replace(/&/gu, '&amp;')
    .replace(/</gu, '&lt;')
    .replace(/>/gu, '&gt;')
    .replace(/"/gu, '&quot;');
}

export const mockMemberWelcomeStore = new MockMemberWelcomeStore();
