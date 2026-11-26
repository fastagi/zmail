import 'server-only';

import { normalizeLocalPart } from '@/features/mailbox/schemas';
import { mockMailboxStore } from '@/features/mailbox/server/mock-mailbox-store';
import type {
  PublicMailboxAvailability,
  PublicMailboxOptions,
} from '@/features/mailbox/services/public-mailbox-service';
import type { Mailbox } from '@/features/mailbox/types';
import type { MailboxViewer } from '@/features/mailbox/server/mock-store/types';

export const MOCK_PUBLIC_MAILBOX_DOMAINS = ['zmail.im'] as const;
export const MOCK_PUBLIC_MIN_LOCAL_LENGTH = 4;
export const MOCK_PUBLIC_LIMIT_PER_USER = 1;
export const MOCK_PUBLIC_DAILY_SEND_LIMIT_NEW = 10;
export const MOCK_PUBLIC_DAILY_SEND_LIMIT = 30;
export const MOCK_PUBLIC_MAX_RECIPIENTS_NEW = 5;
export const MOCK_PUBLIC_MAX_RECIPIENTS = 20;

const extraReservedLocalParts = new Set([
  'api',
  'auth',
  'billing',
  'contact',
  'dns',
  'ftp',
  'hello',
  'help',
  'info',
  'login',
  'mail',
  'official',
  'sales',
  'signup',
  'ssl',
  'staff',
  'system',
  'team',
  'verify',
  'www',
  'zmail',
]);

interface ClaimRecord {
  address: string;
  organizationId: number;
  mailboxId: number;
}

export type MockPublicMailboxClaimResult =
  | { ok: true; mailbox: Mailbox }
  | { ok: false; error: 'disabled' | 'domain_invalid' | 'invalid' | 'too_short' | 'reserved' | 'taken' | 'limit' };

/**
 * Development-only in-memory store for self-serve free public `@zmail.im` mailbox claiming.
 * Mirrors the API contract so Vitest and local development run cleanly without the Go API.
 */
export class MockPublicMailboxStore {
  private enabled = true;
  private domains: string[] = [...MOCK_PUBLIC_MAILBOX_DOMAINS];
  private readonly claimsByUser = new Map<string, ClaimRecord>();
  private readonly claimedAddresses = new Set<string>();

  reset(): void {
    this.enabled = true;
    this.domains = [...MOCK_PUBLIC_MAILBOX_DOMAINS];
    this.claimsByUser.clear();
    this.claimedAddresses.clear();
  }

  configure(settings: { enabled?: boolean; domains?: string[] }): void {
    if (settings.enabled !== undefined) this.enabled = settings.enabled;
    if (settings.domains !== undefined) this.domains = [...settings.domains];
  }

  seedTakenAddress(address: string): void {
    this.claimedAddresses.add(address.trim().toLowerCase());
  }

  options(userKey: string): PublicMailboxOptions {
    const claim = this.claimsByUser.get(userKey) ?? null;
    return {
      enabled: this.enabled,
      domains: [...this.domains],
      min_local_length: MOCK_PUBLIC_MIN_LOCAL_LENGTH,
      limit_per_user: MOCK_PUBLIC_LIMIT_PER_USER,
      claimed_count: claim ? 1 : 0,
      claimed_address: claim?.address ?? null,
      claimed_organization_id: claim?.organizationId ?? null,
      claimed_mailbox_id: claim?.mailboxId ?? null,
      daily_send_limit_new: MOCK_PUBLIC_DAILY_SEND_LIMIT_NEW,
      daily_send_limit: MOCK_PUBLIC_DAILY_SEND_LIMIT,
      max_recipients_new: MOCK_PUBLIC_MAX_RECIPIENTS_NEW,
      max_recipients: MOCK_PUBLIC_MAX_RECIPIENTS,
    };
  }

  resolveDomain(requested?: string): string | null {
    const target = (requested ?? '').trim().toLowerCase();
    if (target === '') return this.domains[0] ?? null;
    return this.domains.includes(target) ? target : null;
  }

  availability(rawLocalPart: string, rawDomain?: string): PublicMailboxAvailability {
    const domain = this.resolveDomain(rawDomain) ?? this.domains[0] ?? MOCK_PUBLIC_MAILBOX_DOMAINS[0];
    const trimmed = rawLocalPart.trim().toLowerCase();
    const address = trimmed ? `${trimmed}@${domain}` : '';

    const normalized = normalizeLocalPart(trimmed, { sharedDomain: true });
    if (!normalized.ok) {
      const status = normalized.reason === 'reserved' ? 'reserved' : 'invalid';
      return {
        address,
        local_part: trimmed,
        domain,
        status,
        suggestions: this.buildSuggestions(trimmed, domain),
      };
    }

    if (normalized.value.length < MOCK_PUBLIC_MIN_LOCAL_LENGTH) {
      return {
        address: `${normalized.value}@${domain}`,
        local_part: normalized.value,
        domain,
        status: 'too_short',
        suggestions: this.buildSuggestions(normalized.value, domain),
      };
    }

    if (extraReservedLocalParts.has(normalized.value)) {
      return {
        address: `${normalized.value}@${domain}`,
        local_part: normalized.value,
        domain,
        status: 'reserved',
        suggestions: this.buildSuggestions(normalized.value, domain),
      };
    }

    const fullAddress = `${normalized.value}@${domain}`;
    if (this.isTaken(fullAddress)) {
      return {
        address: fullAddress,
        local_part: normalized.value,
        domain,
        status: 'taken',
        suggestions: this.buildSuggestions(normalized.value, domain),
      };
    }

    return {
      address: fullAddress,
      local_part: normalized.value,
      domain,
      status: 'available',
      suggestions: [],
    };
  }

  claim(
    organizationId: number,
    viewer: MailboxViewer,
    userKey: string,
    input: { local_part: string; domain?: string; display_name?: string }
  ): MockPublicMailboxClaimResult {
    if (!this.enabled) return { ok: false, error: 'disabled' };
    if (this.claimsByUser.has(userKey)) return { ok: false, error: 'limit' };

    const domain = this.resolveDomain(input.domain);
    if (!domain) return { ok: false, error: 'domain_invalid' };

    const check = this.availability(input.local_part, domain);
    if (check.status !== 'available') {
      return { ok: false, error: check.status };
    }

    const created = mockMailboxStore.create(
      organizationId,
      viewer,
      {
        local_part: check.local_part,
        display_name: input.display_name ?? '',
        type: 'personal',
        owner_user_id: viewer.userId,
      },
      domain
    );

    if (!created.ok) {
      if (created.error === 'taken') return { ok: false, error: 'taken' };
      if (created.error === 'reserved') return { ok: false, error: 'reserved' };
      if (created.error === 'limit') return { ok: false, error: 'limit' };
      return { ok: false, error: 'invalid' };
    }

    this.claimedAddresses.add(created.mailbox.address.toLowerCase());
    this.claimsByUser.set(userKey, {
      address: created.mailbox.address,
      organizationId,
      mailboxId: created.mailbox.id,
    });
    return { ok: true, mailbox: created.mailbox };
  }

  private isTaken(address: string): boolean {
    return this.claimedAddresses.has(address.toLowerCase());
  }

  private buildSuggestions(raw: string, domain: string): string[] {
    const base = raw
      .toLowerCase()
      .replace(/[^a-z0-9._-]+/gu, '')
      .replace(/\.{2,}/gu, '.')
      .replace(/^[._-]+|[._-]+$/gu, '');
    if (!base) return [];

    const candidates = [
      `${base}.mail`,
      `${base}01`,
      `${base}26`,
      `${base}.im`,
      `${base}88`,
    ];
    const suggestions: string[] = [];
    for (const candidate of candidates) {
      if (suggestions.length >= 3) break;
      const check = normalizeLocalPart(candidate, { sharedDomain: true });
      if (
        check.ok &&
        check.value.length >= MOCK_PUBLIC_MIN_LOCAL_LENGTH &&
        !extraReservedLocalParts.has(check.value) &&
        !this.isTaken(`${check.value}@${domain}`)
      ) {
        suggestions.push(check.value);
      }
    }
    return suggestions;
  }
}

export const mockPublicMailboxStore = new MockPublicMailboxStore();
