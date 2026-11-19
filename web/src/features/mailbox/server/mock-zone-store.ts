import 'server-only';

import { isIPv4, isIPv6 } from 'node:net';

import { MAIL_DNS_TXT_CONTENT_MAX_LENGTH } from '@/features/mailbox/schemas';
import type {
  MailDnsLockedReason,
  MailDnsRecord,
  MailDnsRecordInput,
  MailDnsRecordPatch,
  MailDomainCapability,
  MailZone,
} from '@/features/mailbox/types';

/**
 * Development-only managed zones and their DNS records. It mirrors the browser-facing rules of the
 * Go API without reaching Cloudflare: a new zone is pending with two Cloudflare name servers and the
 * records Cloudflare "imported" for the apex, the first domain check finds the delegation, and a
 * record Zmail mail relies on is locked. The registrar is never read, so an active zone always
 * counts as still delegated and only a pending zone can be removed.
 */

export const MOCK_ZONE_NAME_SERVERS = ['ada.ns.cloudflare.com', 'bob.ns.cloudflare.com'];
const originalNameServers = ['dns1.registrar.example', 'dns2.registrar.example'];
const editableTypes = new Set(['A', 'AAAA', 'CNAME', 'TXT', 'MX']);
const labelPattern = /^(?:\*|[a-z0-9_](?:[a-z0-9_-]{0,61}[a-z0-9_])?)$/u;
const hostPattern = /^(?:[a-z0-9_](?:[a-z0-9_-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$/u;
const routingHostSuffix = '.mx.cloudflare.net';

/** A mail domain living in a zone; its records decide which zone records are locked. */
export interface MockZoneDomain {
  name: string;
  capability: MailDomainCapability;
}

/** A record Zmail mail needs, as a domain setup writes it. */
export interface MockMailRecord {
  type: string;
  name: string;
  content: string;
  priority?: number;
}

export type MockZoneRemoveResult =
  { ok: true } | { ok: false; error: 'not_found' | 'in_use' | 'still_delegated' };

export type MockDnsRecordResult =
  | { ok: true; record: MailDnsRecord }
  | { ok: false; error: 'zone_not_found' | 'not_found' | 'locked' }
  | { ok: false; error: 'invalid'; field: keyof MailDnsRecordInput; message: string };

export type MockDnsRecordListResult =
  { ok: true; records: MailDnsRecord[] } | { ok: false; error: 'zone_not_found' };

export type MockDnsRecordRemoveResult =
  { ok: true } | { ok: false; error: 'zone_not_found' | 'not_found' | 'locked' };

type StoredRecord = Omit<MailDnsRecord, 'locked' | 'locked_reason'>;

/** A record to create; `proxied` defaults to false, as in the API. */
export type MockDnsRecordCreate = Omit<MailDnsRecordInput, 'proxied'> & { proxied?: boolean };

interface ZoneState {
  organizationId: number;
  zone: MailZone;
  records: StoredRecord[];
}

interface MockZoneStoreDependencies {
  now: () => Date;
}

export class MockZoneStore {
  private nextId = 1;
  private nextRecordId = 1;
  private zones = new Map<number, ZoneState>();

  constructor(private readonly dependencies: MockZoneStoreDependencies) {}

  /** By name, as the API lists them. */
  list(organizationId: number): MailZone[] {
    return [...this.zones.values()]
      .filter(state => state.organizationId === organizationId)
      .map(state => state.zone)
      .sort((left, right) => left.name.localeCompare(right.name));
  }

  get(organizationId: number, id: number): MailZone | null {
    const state = this.zones.get(id);
    return state && state.organizationId === organizationId ? state.zone : null;
  }

  /** The zone serving an apex, in any organization. */
  findByApex(apex: string): { organizationId: number; zone: MailZone } | undefined {
    const state = [...this.zones.values()].find(item => item.zone.name === apex);
    return state && { organizationId: state.organizationId, zone: state.zone };
  }

  /** A new pending zone with the records Cloudflare imported for the apex. */
  create(organizationId: number, apex: string): MailZone {
    const now = this.dependencies.now().toISOString();
    const zone: MailZone = {
      id: this.nextId++,
      name: apex,
      status: 'pending',
      name_servers: [...MOCK_ZONE_NAME_SERVERS],
      original_name_servers: [...originalNameServers],
      activated_at: null,
      last_checked_at: null,
      created_at: now,
    };
    this.zones.set(zone.id, {
      organizationId,
      zone,
      records: [
        this.stored({ type: 'A', name: apex, content: '192.0.2.10' }),
        this.stored({ type: 'CNAME', name: `www.${apex}`, content: 'example.com' }),
      ],
    });
    return zone;
  }

  /** A domain check looking at the zone: in the mock the delegation is always found. */
  check(organizationId: number, id: number): MailZone | null {
    const state = this.state(organizationId, id);
    if (!state) return null;
    const now = this.dependencies.now().toISOString();
    state.zone = {
      ...state.zone,
      status: 'active',
      activated_at: state.zone.activated_at ?? now,
      last_checked_at: now,
    };
    return state.zone;
  }

  remove(organizationId: number, id: number, domains: MockZoneDomain[]): MockZoneRemoveResult {
    const state = this.state(organizationId, id);
    if (!state) return { ok: false, error: 'not_found' };
    if (domains.length > 0) return { ok: false, error: 'in_use' };
    if (state.zone.status === 'active') return { ok: false, error: 'still_delegated' };
    this.zones.delete(id);
    return { ok: true };
  }

  /** What a domain setup writes: each record the zone lacks. */
  addMailRecords(zoneId: number, records: MockMailRecord[]): void {
    const state = this.zones.get(zoneId);
    if (!state) return;
    for (const record of records) {
      const present = state.records.some(
        item =>
          item.type === record.type && item.name === record.name && item.content === record.content
      );
      if (!present) state.records.push(this.stored(record));
    }
  }

  listRecords(
    organizationId: number,
    zoneId: number,
    domains: MockZoneDomain[]
  ): MockDnsRecordListResult {
    const state = this.state(organizationId, zoneId);
    if (!state) return { ok: false, error: 'zone_not_found' };
    const records = state.records
      .map(record => withLock(record, domains))
      .sort(
        (left, right) => left.name.localeCompare(right.name) || left.type.localeCompare(right.type)
      );
    return { ok: true, records };
  }

  createRecord(
    organizationId: number,
    zoneId: number,
    input: MockDnsRecordCreate,
    domains: MockZoneDomain[]
  ): MockDnsRecordResult {
    const state = this.state(organizationId, zoneId);
    if (!state) return { ok: false, error: 'zone_not_found' };
    const candidate = validated(state, null, {
      type: input.type,
      name: input.name,
      content: input.content,
      ttl: input.ttl ?? 1,
      priority: input.priority,
      proxied: input.proxied ?? false,
    });
    if (!candidate.ok) return candidate;
    if (targetLockReason(candidate.record, domains)) return { ok: false, error: 'locked' };
    const record = this.stored(candidate.record);
    state.records.push(record);
    return { ok: true, record: withLock(record, domains) };
  }

  updateRecord(
    organizationId: number,
    zoneId: number,
    recordId: string,
    patch: MailDnsRecordPatch,
    domains: MockZoneDomain[]
  ): MockDnsRecordResult {
    const state = this.state(organizationId, zoneId);
    if (!state) return { ok: false, error: 'zone_not_found' };
    const index = state.records.findIndex(record => record.id === recordId);
    const current = state.records[index];
    if (!current) return { ok: false, error: 'not_found' };
    if (!editableTypes.has(current.type)) {
      return { ok: false, error: 'invalid', field: 'type', message: 'The type cannot be edited' };
    }
    if (lockReason(current, domains)) return { ok: false, error: 'locked' };
    const candidate = validated(state, current.id, {
      type: patch.type ?? current.type,
      name: patch.name ?? current.name,
      content: patch.content ?? current.content,
      ttl: patch.ttl ?? current.ttl,
      priority: patch.priority ?? current.priority,
      proxied: patch.proxied ?? current.proxied,
    });
    if (!candidate.ok) return candidate;
    if (targetLockReason(candidate.record, domains)) return { ok: false, error: 'locked' };
    const record: StoredRecord = { ...candidate.record, id: current.id };
    state.records[index] = record;
    return { ok: true, record: withLock(record, domains) };
  }

  deleteRecord(
    organizationId: number,
    zoneId: number,
    recordId: string,
    domains: MockZoneDomain[]
  ): MockDnsRecordRemoveResult {
    const state = this.state(organizationId, zoneId);
    if (!state) return { ok: false, error: 'zone_not_found' };
    const current = state.records.find(record => record.id === recordId);
    if (!current) return { ok: false, error: 'not_found' };
    if (lockReason(current, domains)) return { ok: false, error: 'locked' };
    state.records = state.records.filter(record => record.id !== recordId);
    return { ok: true };
  }

  private state(organizationId: number, id: number): ZoneState | undefined {
    const state = this.zones.get(id);
    return state && state.organizationId === organizationId ? state : undefined;
  }

  private stored(record: Omit<StoredRecord, 'id' | 'ttl' | 'proxied'> & Partial<StoredRecord>) {
    const stored: StoredRecord = {
      id: (this.nextRecordId++).toString(16).padStart(32, '0'),
      type: record.type,
      name: record.name,
      content: record.content,
      ttl: record.ttl ?? 1,
      proxied: record.proxied ?? false,
    };
    if (record.type === 'MX') stored.priority = record.priority ?? 10;
    return stored;
  }
}

export const mockZoneStore = new MockZoneStore({ now: () => new Date() });

interface Candidate {
  type: string;
  name: string;
  content: string;
  ttl: number;
  priority: number | undefined;
  proxied: boolean;
}

type Validated =
  | { ok: true; record: Omit<StoredRecord, 'id'> }
  | { ok: false; error: 'invalid'; field: keyof MailDnsRecordInput; message: string };

/** The API's record rules, field by field; `selfId` is the record being changed. */
function validated(state: ZoneState, selfId: string | null, candidate: Candidate): Validated {
  const invalid = (field: keyof MailDnsRecordInput, message: string): Validated => ({
    ok: false,
    error: 'invalid',
    field,
    message,
  });
  const { type, content, ttl, priority, proxied } = candidate;
  if (!editableTypes.has(type)) return invalid('type', 'The type is not supported');
  const name = qualifiedName(candidate.name, state.zone.name);
  if (!name) return invalid('name', 'The name is not in the zone');
  if (type === 'A' && !isIPv4(content)) return invalid('content', 'Not an IPv4 address');
  if (type === 'AAAA' && !isIPv6(content)) return invalid('content', 'Not an IPv6 address');
  if ((type === 'CNAME' || type === 'MX') && !hostPattern.test(content.toLowerCase())) {
    return invalid('content', 'Not a host name');
  }
  if (type === 'TXT' && content.length > MAIL_DNS_TXT_CONTENT_MAX_LENGTH) {
    return invalid('content', 'The TXT content is too long');
  }
  const others = state.records.filter(record => record.id !== selfId && record.name === name);
  if (type === 'CNAME' && name === state.zone.name) {
    return invalid('name', 'A CNAME cannot be at the zone apex');
  }
  if ((type === 'CNAME' && others.length > 0) || others.some(item => item.type === 'CNAME')) {
    return invalid('name', 'A CNAME cannot share its name');
  }
  if (type === 'MX' && (priority === undefined || !isInRange(priority, 0, 65_535))) {
    return invalid('priority', 'MX needs a priority from 0 to 65535');
  }
  if (proxied && type !== 'A' && type !== 'AAAA' && type !== 'CNAME') {
    return invalid('proxied', 'Only A, AAAA, and CNAME records can be proxied');
  }
  if (ttl !== 1 && !isInRange(ttl, 60, 86_400)) return invalid('ttl', 'The TTL is invalid');
  return {
    ok: true,
    record: {
      type,
      name,
      content: type === 'CNAME' || type === 'MX' ? content.toLowerCase() : content,
      ttl,
      proxied,
      ...(type === 'MX' ? { priority } : {}),
    },
  };
}

/** `@`, a single label relative to the zone, or a name in the zone; null otherwise. */
function qualifiedName(raw: string, zone: string): string | null {
  const name = raw.trim().toLowerCase().replace(/\.$/u, '');
  if (name === '@' || name === zone) return zone;
  if (labelPattern.test(name)) return `${name}.${zone}`;
  if (!name.endsWith(`.${zone}`)) return null;
  const labels = name.slice(0, -(zone.length + 1)).split('.');
  return labels.every(label => labelPattern.test(label)) ? name : null;
}

function isInRange(value: number, min: number, max: number): boolean {
  return Number.isInteger(value) && value >= min && value <= max;
}

function withLock(record: StoredRecord, domains: MockZoneDomain[]): MailDnsRecord {
  const reason = lockReason(record, domains);
  return reason ? { ...record, locked: true, locked_reason: reason } : { ...record, locked: false };
}

/** What Zmail mail uses an existing record for, as `contracts/MAILBOXES.md` lists it. */
function lockReason(
  record: Pick<StoredRecord, 'type' | 'name' | 'content'>,
  domains: MockZoneDomain[]
): MailDnsLockedReason | null {
  const spf = record.type === 'TXT' && record.content.startsWith('v=spf1');
  for (const domain of domains) {
    const name = domain.name;
    if (
      record.type === 'MX' &&
      record.name === name &&
      record.content.toLowerCase().endsWith(routingHostSuffix)
    ) {
      return 'mail_receiving';
    }
    if (spf && record.name === name && domain.capability === 'send_receive') return 'mail_spf';
    if (record.name === `_dmarc.${name}`) return 'mail_dmarc';
    if (record.name.endsWith(`._domainkey.${name}`)) return 'mail_dkim';
    if (record.name === `cf-bounce.${name}` && (spf || record.type === 'MX')) {
      return 'mail_sending';
    }
  }
  return null;
}

/** A record created or changed into this would be locked, or is an MX Email Routing owns. */
function targetLockReason(
  record: Pick<StoredRecord, 'type' | 'name' | 'content'>,
  domains: MockZoneDomain[]
): MailDnsLockedReason | null {
  const reason = lockReason(record, domains);
  if (reason) return reason;
  const routed = domains.some(
    domain => domain.capability === 'send_receive' && domain.name === record.name
  );
  return record.type === 'MX' && routed ? 'mail_receiving' : null;
}
