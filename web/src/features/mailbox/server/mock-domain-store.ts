import 'server-only';

import {
  MAIL_DOMAIN_MAX_PER_ORGANIZATION,
  MAIL_DOMAIN_NAME_MAX_LENGTH,
} from '@/features/mailbox/schemas';
import {
  MOCK_MAILBOX_DOMAIN,
  mockMailboxStore,
} from '@/features/mailbox/server/mock-mailbox-store';
import type { MailDomain, MailDomainRecord } from '@/features/mailbox/types';

/**
 * Development-only connected domains. It mirrors the browser-facing rules of the Go API: one
 * organization per domain, the shared domain cannot be connected, at most ten domains per
 * organization, and a domain in use by a mailbox cannot be removed. Nothing reaches Cloudflare:
 * every zone is "reachable" except names under `.invalid`, a new domain starts pending as if DNS
 * were still propagating, and the next setup or check verifies it.
 */

const domainPattern = /^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$/u;

export type MockDomainResult =
  | { ok: true; domain: MailDomain }
  | { ok: false; error: 'invalid' | 'taken' | 'limit' | 'zone_not_found' | 'not_found' };

export type MockDomainRemoveResult = { ok: true } | { ok: false; error: 'not_found' | 'in_use' };

export type MockDomainResolution =
  { ok: true; name: string } | { ok: false; error: 'not_found' | 'not_verified' };

interface MockDomainStoreDependencies {
  now: () => Date;
  /** Whether any live mailbox has an address on the domain. */
  inUse: (name: string) => boolean;
}

interface DomainState {
  organizationId: number;
  domain: MailDomain;
}

export class MockDomainStore {
  private nextId = 1;
  private domains = new Map<number, DomainState>();

  constructor(private readonly dependencies: MockDomainStoreDependencies) {}

  /** By name, as the API lists them. */
  list(organizationId: number): MailDomain[] {
    return [...this.domains.values()]
      .filter(state => state.organizationId === organizationId)
      .map(state => state.domain)
      .sort((left, right) => left.name.localeCompare(right.name));
  }

  verifiedNames(organizationId: number): string[] {
    return this.list(organizationId)
      .filter(domain => domain.status === 'verified')
      .map(domain => domain.name);
  }

  get(organizationId: number, id: number): MailDomain | null {
    const state = this.domains.get(id);
    return state && state.organizationId === organizationId ? state.domain : null;
  }

  connect(organizationId: number, rawName: string): MockDomainResult {
    const name = rawName.trim().toLowerCase().replace(/\.$/u, '');
    if (name.length > MAIL_DOMAIN_NAME_MAX_LENGTH || !domainPattern.test(name)) {
      return { ok: false, error: 'invalid' };
    }
    if (name === MOCK_MAILBOX_DOMAIN || this.findByName(name)) return { ok: false, error: 'taken' };
    if (name.endsWith('.invalid')) return { ok: false, error: 'zone_not_found' };
    if (this.list(organizationId).length >= MAIL_DOMAIN_MAX_PER_ORGANIZATION) {
      return { ok: false, error: 'limit' };
    }
    const now = this.dependencies.now().toISOString();
    const domain: MailDomain = {
      id: this.nextId++,
      name,
      zone_name: name.split('.').slice(-2).join('.'),
      status: 'pending',
      // Sending records are still propagating right after the setup.
      records: requiredRecords(name, false),
      checked_at: now,
      verified_at: null,
      created_at: now,
    };
    this.domains.set(domain.id, { organizationId, domain });
    return { ok: true, domain };
  }

  /** A setup or a check: in the mock both find every record in place. */
  refresh(organizationId: number, id: number): MockDomainResult {
    const current = this.get(organizationId, id);
    if (!current) return { ok: false, error: 'not_found' };
    const now = this.dependencies.now().toISOString();
    const domain: MailDomain = {
      ...current,
      status: 'verified',
      records: requiredRecords(current.name, true),
      checked_at: now,
      verified_at: current.verified_at ?? now,
    };
    this.domains.set(id, { organizationId, domain });
    return { ok: true, domain };
  }

  remove(organizationId: number, id: number): MockDomainRemoveResult {
    const current = this.get(organizationId, id);
    if (!current) return { ok: false, error: 'not_found' };
    if (this.dependencies.inUse(current.name)) return { ok: false, error: 'in_use' };
    this.domains.delete(id);
    return { ok: true };
  }

  /** Where a new mailbox may go: an empty name is the shared domain. */
  resolve(organizationId: number, rawName: string): MockDomainResolution {
    const name = rawName.trim().toLowerCase().replace(/\.$/u, '');
    if (name === '' || name === MOCK_MAILBOX_DOMAIN) return { ok: true, name: MOCK_MAILBOX_DOMAIN };
    const state = this.findByName(name);
    if (!state || state.organizationId !== organizationId) return { ok: false, error: 'not_found' };
    if (state.domain.status !== 'verified') return { ok: false, error: 'not_verified' };
    return { ok: true, name };
  }

  private findByName(name: string): DomainState | undefined {
    return [...this.domains.values()].find(state => state.domain.name === name);
  }
}

export const mockDomainStore = new MockDomainStore({
  now: () => new Date(),
  inUse: name => mockMailboxStore.usesDomain(name),
});

function requiredRecords(name: string, sendingReady: boolean): MailDomainRecord[] {
  const sending = sendingReady ? 'ok' : 'missing';
  return [
    ...['route1', 'route2', 'route3'].map((host, index): MailDomainRecord => ({
      purpose: 'receiving',
      type: 'MX',
      name,
      content: `${host}.mx.cloudflare.net`,
      priority: 10 + index,
      status: 'ok',
    })),
    {
      purpose: 'receiving',
      type: 'TXT',
      name,
      content: 'v=spf1 include:_spf.mx.cloudflare.net ~all',
      status: 'ok',
    },
    {
      purpose: 'sending',
      type: 'TXT',
      name: `cf-bounce.${name}`,
      content: 'v=spf1 include:_spf.mx.cloudflare.net ~all',
      status: sending,
    },
    {
      purpose: 'sending',
      type: 'TXT',
      name: `cf2024-1._domainkey.${name}`,
      content: 'v=DKIM1; h=sha256; k=rsa; p=MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA',
      status: sending,
    },
    {
      purpose: 'dmarc',
      type: 'TXT',
      name: `_dmarc.${name}`,
      content: 'v=DMARC1; p=none',
      status: 'ok',
    },
  ];
}
