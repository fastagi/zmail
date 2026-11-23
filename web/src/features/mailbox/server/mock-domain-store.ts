import 'server-only';

import {
  MAIL_DOMAIN_MAX_PER_ORGANIZATION,
  MAIL_DOMAIN_NAME_MAX_LENGTH,
  MAIL_ZONE_MAX_PER_ORGANIZATION,
} from '@/features/mailbox/schemas';
import {
  MOCK_MAILBOX_DOMAIN,
  mockMailboxStore,
} from '@/features/mailbox/server/mock-mailbox-store';
import {
  mockZoneStore,
  type MockZoneDomain,
  type MockZoneStore,
} from '@/features/mailbox/server/mock-zone-store';
import type {
  MailDomain,
  MailDomainCapability,
  MailDomainPreview,
  MailDomainRecord,
} from '@/features/mailbox/types';

/**
 * Development-only connected domains. It mirrors the browser-facing rules of the Go API: one
 * organization per domain, the shared domain cannot be connected, at most ten domains per
 * organization, and a domain in use by a mailbox cannot be removed. Nothing reaches Cloudflare.
 * The operator manages only the shared domain's zone; any other apex gets a pending managed zone
 * (at most three per organization), where the domain waits without records until the first check
 * finds the delegation. Names under `.invalid` have no zone at all. A setup or check in an operator
 * zone or an active managed zone finds every record in place, and a new domain there starts pending
 * as if DNS were still propagating.
 */

const domainPattern = /^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$/u;
const operatorApexes = new Set([apexOf(MOCK_MAILBOX_DOMAIN)]);
/** The MX of another mail service that a preview finds on an apex. */
const foreignMx = ['mx1.provider.example', 'mx2.provider.example'];

export type MockDomainResult =
  | { ok: true; domain: MailDomain }
  | {
      ok: false;
      error:
        | 'invalid'
        | 'taken'
        | 'limit'
        | 'zone_not_found'
        | 'not_found'
        | 'zone_pending'
        | 'zone_limit'
        | 'not_verified';
    };

export type MockDomainRemoveResult = { ok: true } | { ok: false; error: 'not_found' | 'in_use' };

export type MockDomainResolution =
  { ok: true; name: string } | { ok: false; error: 'not_found' | 'not_verified' };

export type MockDomainPreviewResult =
  { ok: true; preview: MailDomainPreview } | { ok: false; error: 'invalid' | 'taken' };

interface MockDomainStoreDependencies {
  now: () => Date;
  /** Whether any live mailbox has an address on the domain. */
  inUse: (name: string) => boolean;
  /** The organization's live mailboxes on the domain. */
  mailboxCount: (organizationId: number, name: string) => number;
  zones: MockZoneStore;
}

/** The domain without its zone, which is read from the zone store whenever it is shown. */
type StoredDomain = Omit<MailDomain, 'zone' | 'default' | 'mailbox_count'>;

interface DomainState {
  organizationId: number;
  /** The managed zone; null in the operator's zone. */
  zoneId: number | null;
  domain: StoredDomain;
  /** Chosen as the organization's default by an owner or admin. */
  marked: boolean;
}

export class MockDomainStore {
  private nextId = 1;
  private domains = new Map<number, DomainState>();

  constructor(private readonly dependencies: MockDomainStoreDependencies) {}

  /** By name, as the API lists them. */
  list(organizationId: number): MailDomain[] {
    return [...this.domains.values()]
      .filter(state => state.organizationId === organizationId)
      .map(state => this.view(state))
      .sort((left, right) => left.name.localeCompare(right.name));
  }

  verifiedNames(organizationId: number): string[] {
    return this.list(organizationId)
      .filter(domain => domain.status === 'verified')
      .map(domain => domain.name);
  }

  /**
   * The domain new mailboxes use unless another is picked: the chosen one while verified, else the
   * verified domain connected first. Null while none is verified.
   */
  defaultName(organizationId: number): string | null {
    const verified = [...this.domains.values()].filter(
      state => state.organizationId === organizationId && state.domain.status === 'verified'
    );
    const chosen = verified.find(state => state.marked);
    if (chosen) return chosen.domain.name;
    const [oldest] = verified.sort(
      (left, right) =>
        left.domain.created_at.localeCompare(right.domain.created_at) ||
        left.domain.id - right.domain.id
    );
    return oldest ? oldest.domain.name : null;
  }

  /** Moves the default mark to a verified domain of the organization. */
  setDefault(organizationId: number, id: number): MockDomainResult {
    const state = this.state(organizationId, id);
    if (!state) return { ok: false, error: 'not_found' };
    if (state.domain.status !== 'verified') return { ok: false, error: 'not_verified' };
    for (const other of this.domains.values()) {
      if (other.organizationId === organizationId) other.marked = other === state;
    }
    return { ok: true, domain: this.view(state) };
  }

  get(organizationId: number, id: number): MailDomain | null {
    const state = this.state(organizationId, id);
    return state ? this.view(state) : null;
  }

  /** The domains living in a managed zone, which decide its locked records. */
  inZone(organizationId: number, zoneId: number): MockZoneDomain[] {
    return [...this.domains.values()]
      .filter(state => state.organizationId === organizationId && state.zoneId === zoneId)
      .map(({ domain }) => ({ name: domain.name, capability: domain.capability }));
  }

  preview(rawName: string): MockDomainPreviewResult {
    const name = normalized(rawName);
    if (!isDomainName(name)) return { ok: false, error: 'invalid' };
    if (name === MOCK_MAILBOX_DOMAIN) return { ok: false, error: 'taken' };
    const apex = apexOf(name);
    // An apex usually has mail elsewhere already; a fresh subdomain has none.
    const existing = name === apex ? [...foreignMx] : [];
    return {
      ok: true,
      preview: {
        name,
        apex,
        existing_mx: existing,
        suggested_capability: existing.length > 0 ? 'send_only' : 'send_receive',
      },
    };
  }

  connect(
    organizationId: number,
    rawName: string,
    capability: MailDomainCapability = 'send_receive'
  ): MockDomainResult {
    const name = normalized(rawName);
    if (!isDomainName(name)) return { ok: false, error: 'invalid' };
    if (name === MOCK_MAILBOX_DOMAIN || this.findByName(name)) return { ok: false, error: 'taken' };
    if (name.endsWith('.invalid')) return { ok: false, error: 'zone_not_found' };
    if (this.list(organizationId).length >= MAIL_DOMAIN_MAX_PER_ORGANIZATION) {
      return { ok: false, error: 'limit' };
    }
    const apex = apexOf(name);
    const zones = this.dependencies.zones;
    let zoneId: number | null = null;
    if (!operatorApexes.has(apex)) {
      const existing = zones.findByApex(apex);
      if (existing && existing.organizationId !== organizationId) {
        return { ok: false, error: 'taken' };
      }
      if (existing) {
        zoneId = existing.zone.id;
      } else if (zones.list(organizationId).length >= MAIL_ZONE_MAX_PER_ORGANIZATION) {
        return { ok: false, error: 'zone_limit' };
      } else {
        zoneId = zones.create(organizationId, apex).id;
      }
    }
    const zonePending = zoneId !== null && zones.get(organizationId, zoneId)?.status === 'pending';
    const now = this.dependencies.now().toISOString();
    const domain: StoredDomain = {
      id: this.nextId++,
      name,
      zone_name: apex,
      capability,
      status: 'pending',
      // No setup runs in a pending zone; elsewhere sending records still propagate.
      records: zonePending ? [] : requiredRecords(name, capability, false),
      setting_up: false,
      zone_check_limited: false,
      checked_at: now,
      verified_at: null,
      created_at: now,
    };
    const state: DomainState = { organizationId, zoneId, domain, marked: false };
    this.domains.set(domain.id, state);
    return { ok: true, domain: this.view(state) };
  }

  /**
   * A setup or a check. In a pending zone a setup is refused and a check finds the delegation; in
   * the mock both then find every record in place.
   */
  refresh(organizationId: number, id: number, action: 'setups' | 'checks'): MockDomainResult {
    const state = this.state(organizationId, id);
    if (!state) return { ok: false, error: 'not_found' };
    if (this.zonePending(state)) {
      if (action === 'setups') return { ok: false, error: 'zone_pending' };
      this.dependencies.zones.check(organizationId, state.zoneId ?? 0);
      // The zone is active now, which starts the setup of every pending domain in it.
      for (const sibling of this.domains.values()) {
        if (sibling !== state && sibling.zoneId === state.zoneId) {
          if (sibling.domain.status === 'pending') this.verify(sibling);
        }
      }
    }
    return { ok: true, domain: this.verify(state) };
  }

  /** Turns a send-only domain into one that also receives; one that receives is unchanged. */
  enableReceiving(organizationId: number, id: number): MockDomainResult {
    const state = this.state(organizationId, id);
    if (!state) return { ok: false, error: 'not_found' };
    if (this.zonePending(state)) return { ok: false, error: 'zone_pending' };
    if (state.domain.capability === 'send_receive') return { ok: true, domain: this.view(state) };
    state.domain = { ...state.domain, capability: 'send_receive' };
    return { ok: true, domain: this.verify(state) };
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
    const name = normalized(rawName);
    if (name === '' || name === MOCK_MAILBOX_DOMAIN) return { ok: true, name: MOCK_MAILBOX_DOMAIN };
    const state = this.findByName(name);
    if (!state || state.organizationId !== organizationId) return { ok: false, error: 'not_found' };
    if (state.domain.status !== 'verified') return { ok: false, error: 'not_verified' };
    return { ok: true, name };
  }

  /** The setup and check in one: every record is in place, and a managed zone holds them. */
  private verify(state: DomainState): MailDomain {
    const now = this.dependencies.now().toISOString();
    const records = requiredRecords(state.domain.name, state.domain.capability, true);
    state.domain = {
      ...state.domain,
      status: 'verified',
      records,
      checked_at: now,
      verified_at: state.domain.verified_at ?? now,
    };
    if (state.zoneId !== null) this.dependencies.zones.addMailRecords(state.zoneId, records);
    return this.view(state);
  }

  private zonePending(state: DomainState): boolean {
    if (state.zoneId === null) return false;
    return this.dependencies.zones.get(state.organizationId, state.zoneId)?.status === 'pending';
  }

  private view(state: DomainState): MailDomain {
    const zone =
      state.zoneId === null
        ? null
        : this.dependencies.zones.get(state.organizationId, state.zoneId);
    return {
      ...state.domain,
      zone,
      default: this.defaultName(state.organizationId) === state.domain.name,
      mailbox_count: this.dependencies.mailboxCount(state.organizationId, state.domain.name),
    };
  }

  private state(organizationId: number, id: number): DomainState | undefined {
    const state = this.domains.get(id);
    return state && state.organizationId === organizationId ? state : undefined;
  }

  private findByName(name: string): DomainState | undefined {
    return [...this.domains.values()].find(state => state.domain.name === name);
  }
}

export const mockDomainStore = new MockDomainStore({
  now: () => new Date(),
  inUse: name => mockMailboxStore.usesDomain(name),
  mailboxCount: (organizationId, name) => mockMailboxStore.countOnDomain(organizationId, name),
  zones: mockZoneStore,
});

function normalized(rawName: string): string {
  return rawName.trim().toLowerCase().replace(/\.$/u, '');
}

function isDomainName(name: string): boolean {
  return name.length <= MAIL_DOMAIN_NAME_MAX_LENGTH && domainPattern.test(name);
}

/** The mock has no public suffix list: the apex is the last two labels. */
function apexOf(name: string): string {
  return name.split('.').slice(-2).join('.');
}

function requiredRecords(
  name: string,
  capability: MailDomainCapability,
  sendingReady: boolean
): MailDomainRecord[] {
  const sending = sendingReady ? 'ok' : 'missing';
  const receiving: MailDomainRecord[] = [
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
  ];
  return [
    // A send-only domain leaves its MX with another mail service.
    ...(capability === 'send_receive' ? receiving : []),
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
