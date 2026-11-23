import 'server-only';

import { createHash, randomInt } from 'node:crypto';

import type {
  MailAppPassword,
  MailAppPasswordCreated,
  MailAppPasswordList,
  MailClientAccessReason,
  MailClientProtocol,
  MailClientSettings,
} from '@/features/mailbox/mail-client-schemas';

/** Limits from `contracts/MAILBOXES.md`, section Mail Client Access. */
export const MAIL_APP_PASSWORD_LIMIT = 20;
const ATTEMPT_LIMIT = 5;
const ATTEMPT_WINDOW_MS = 15 * 60 * 1000;
/** The development gateway listens on 127.0.0.1:1993, :1465, and :1587 (`MAIL_GATEWAY_PUBLIC_*` in `.env.example`). */
const MOCK_SERVER = {
  imap_host: '127.0.0.1',
  imap_port: 1993,
  imap_security: 'ssl_tls',
  smtp_host: '127.0.0.1',
  smtp_port: 1465,
  smtp_security: 'ssl_tls',
  smtp_starttls_port: 1587,
} as const;

interface MockPassword extends MailAppPassword {
  organizationId: number;
  userId: number;
  secretHash: string;
}

export interface MockMailAppPasswordCaller {
  organizationId: number;
  userId: number;
  /** Whether the membership may use mail apps; owners and admins always may. */
  granted: boolean;
}

export type MockCreateResult =
  | { ok: true; value: MailAppPasswordCreated }
  | { ok: false; error: 'feature_not_granted' | 'disabled' | 'limit_reached' }
  | { ok: false; error: 'rate_limited'; retryAfterSeconds: number }
  | { ok: false; error: 'validation'; fields: Record<string, string[]> };

/**
 * Development mock of mail client passwords and the organization switch. Like the API it keeps
 * only a hash of each secret; the secret leaves the store once, in the creation result.
 */
class MockMailAppPasswordStore {
  private nextId = 1;
  private passwords: MockPassword[] = [];
  private readonly disabled = new Set<number>();
  private readonly settingsUpdatedAt = new Map<number, string>();
  private readonly failures = new Map<number, number[]>();

  list(caller: MockMailAppPasswordCaller): MailAppPasswordList {
    const items = this.passwords
      .filter(item => item.organizationId === caller.organizationId && item.userId === caller.userId)
      .sort((left, right) => right.id - left.id)
      .map(publicPassword);
    const reason = this.accessReason(caller);
    return {
      items,
      limit: MAIL_APP_PASSWORD_LIMIT,
      access: { enabled: reason === null, reason },
      server: { ...MOCK_SERVER },
    };
  }

  /**
   * Creates a password after the feature, the switch, the fields, and the credentials, in the
   * API's order. `verifyPassword` stands in for the account check; the mock has no second factor.
   */
  create(
    caller: MockMailAppPasswordCaller,
    input: { name: string; scopes: MailClientProtocol[]; currentPassword: string },
    verifyPassword: (password: string) => boolean
  ): MockCreateResult {
    if (!caller.granted) return { ok: false, error: 'feature_not_granted' };
    if (this.disabled.has(caller.organizationId)) return { ok: false, error: 'disabled' };
    const now = Date.now();
    const recent = (this.failures.get(caller.userId) ?? []).filter(
      at => now - at < ATTEMPT_WINDOW_MS
    );
    if (recent.length >= ATTEMPT_LIMIT) {
      const retryAfterSeconds = Math.max(1, Math.ceil((recent[0] + ATTEMPT_WINDOW_MS - now) / 1000));
      return { ok: false, error: 'rate_limited', retryAfterSeconds };
    }
    if (!verifyPassword(input.currentPassword)) {
      this.failures.set(caller.userId, [...recent, now]);
      return { ok: false, error: 'validation', fields: { current_password: ['is incorrect'] } };
    }
    this.failures.delete(caller.userId);
    const active = this.passwords.filter(
      item => item.organizationId === caller.organizationId && item.userId === caller.userId
    );
    if (active.length >= MAIL_APP_PASSWORD_LIMIT) return { ok: false, error: 'limit_reached' };

    const letters = Array.from({ length: 16 }, () => String.fromCharCode(97 + randomInt(26))).join('');
    const record: MockPassword = {
      id: this.nextId++,
      name: input.name,
      scopes: (['imap', 'smtp'] as const).filter(scope => input.scopes.includes(scope)),
      hint: letters.slice(12),
      created_at: new Date(now).toISOString(),
      last_used_at: null,
      last_used_protocol: null,
      organizationId: caller.organizationId,
      userId: caller.userId,
      secretHash: createHash('sha256').update(letters).digest('hex'),
    };
    this.passwords.push(record);
    return {
      ok: true,
      value: { ...publicPassword(record), secret: letters.match(/.{4}/gu)?.join(' ') ?? letters },
    };
  }

  /** Revokes one of the caller's own passwords; false when there is none with that id. */
  revoke(caller: MockMailAppPasswordCaller, id: number): boolean {
    const before = this.passwords.length;
    this.passwords = this.passwords.filter(
      item =>
        !(item.id === id && item.organizationId === caller.organizationId && item.userId === caller.userId)
    );
    return this.passwords.length < before;
  }

  settings(organizationId: number): MailClientSettings {
    return {
      access_enabled: !this.disabled.has(organizationId),
      updated_at: this.settingsUpdatedAt.get(organizationId) ?? null,
    };
  }

  updateSettings(organizationId: number, accessEnabled: boolean): MailClientSettings {
    if (accessEnabled) this.disabled.delete(organizationId);
    else this.disabled.add(organizationId);
    this.settingsUpdatedAt.set(organizationId, new Date().toISOString());
    return this.settings(organizationId);
  }

  reset(): void {
    this.nextId = 1;
    this.passwords = [];
    this.disabled.clear();
    this.settingsUpdatedAt.clear();
    this.failures.clear();
  }

  private accessReason(caller: MockMailAppPasswordCaller): MailClientAccessReason | null {
    if (this.disabled.has(caller.organizationId)) return 'organization_disabled';
    if (!caller.granted) return 'feature_not_granted';
    return null;
  }
}

function publicPassword(record: MockPassword): MailAppPassword {
  return {
    id: record.id,
    name: record.name,
    scopes: record.scopes,
    hint: record.hint,
    created_at: record.created_at,
    last_used_at: record.last_used_at,
    last_used_protocol: record.last_used_protocol,
  };
}

export const mockMailAppPasswordStore = new MockMailAppPasswordStore();
