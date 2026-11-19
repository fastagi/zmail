import 'server-only';

import { randomBytes } from 'node:crypto';

import {
  TRANSACTIONAL_LOG_DEFAULT_PAGE_SIZE,
  TRANSACTIONAL_TEST_SENDS_PER_MINUTE,
} from '@/features/developer/schemas';
import type {
  TransactionalLogEntry,
  TransactionalLogEntryDetail,
  TransactionalLogEvent,
  TransactionalLogPage,
  TransactionalMessage,
  TransactionalMessageEvent,
  TransactionalMode,
  TransactionalOutcome,
  TransactionalStatus,
} from '@/features/developer/types';
import type { MailTemplate } from '@/features/mailbox/types';
import { renderTemplate } from '@/features/mailbox/utils/template-render';

/**
 * Development mock of transactional mail for the developer console: console test sends and the
 * send log. It reproduces the contract's masking, simulation, and the per-user test-send limit;
 * it never calls a provider, so a live send is recorded as sent without delivery events.
 */

const SIMULATOR_DOMAIN = 'test.zmail.im';
const SECRET_MASK = '••••••';
const minuteMs = 60_000;
const dayMs = 24 * 60 * minuteMs;
const totalCap = 10_000;
const problemEvents: ReadonlySet<string> = new Set(['bounced', 'complained', 'failed', 'rejected']);

/** The simulator's outcome for each local part, as `contracts/MAILBOXES.md` lists them. */
const simulatorOutcomes: Record<
  string,
  { status: TransactionalStatus; events: TransactionalMessageEvent['type'][] }
> = {
  delivered: { status: 'sent', events: ['accepted', 'delivered'] },
  bounced: { status: 'sent', events: ['accepted', 'bounced'] },
  'soft-bounced': { status: 'sent', events: ['accepted', 'deferred'] },
  complained: { status: 'sent', events: ['accepted', 'delivered', 'complained'] },
  suppressed: { status: 'suppressed', events: ['rejected'] },
};

interface StoredMessage extends TransactionalMessage {
  organizationId: number;
}

export interface MockTransactionalLogQuery {
  mode: TransactionalMode;
  status?: TransactionalStatus;
  lastEvent?: TransactionalLogEvent;
  outcome?: TransactionalOutcome;
  template?: string;
  /** Already trimmed and lowercased. */
  to?: string;
  since: Date;
  /** Open at the top when absent. */
  until?: Date;
  cursor?: number;
  perPage?: number;
}

export interface MockTestSendInput {
  template: MailTemplate;
  mode: TransactionalMode;
  variables: Readonly<Record<string, string>>;
  /** The caller's account address, the only recipient a console test send has. */
  to: string;
  userId: string;
}

export type MockTestSendResult =
  | { ok: true; message: TransactionalMessage }
  | { ok: false; error: 'disabled' }
  | { ok: false; error: 'rate_limited'; retryAfterSeconds: number }
  | { ok: false; error: 'undeclared'; names: string[] }
  | { ok: false; error: 'missing'; names: string[] }
  | { ok: false; error: 'recipient_invalid' };

class MockTransactionalStore {
  private enabled = true;
  private nextId = 1;
  private messages: StoredMessage[] = [];
  /** Test sends by user and fixed one-minute window. */
  private testSends = new Map<string, { windowStart: number; count: number }>();

  reset(): void {
    this.enabled = true;
    this.nextId = 1;
    this.messages = [];
    this.testSends.clear();
  }

  /** The instance switch `MAILBOX_TRANSACTIONAL_ENABLED`; on by default in development. */
  setEnabled(enabled: boolean): void {
    this.enabled = enabled;
  }

  isEnabled(): boolean {
    return this.enabled;
  }

  /**
   * A console test send, checked in the API's order: the switch (the route checks it before the
   * body), the per-user limit, the variable names, the required values, then the simulator.
   */
  testSend(organizationId: number, input: MockTestSendInput): MockTestSendResult {
    if (!this.enabled) return { ok: false, error: 'disabled' };
    const limited = this.takeTestSend(input.userId);
    if (limited !== null) return { ok: false, error: 'rate_limited', retryAfterSeconds: limited };

    const { template } = input;
    const declared = new Set(template.variable_definitions.map(definition => definition.name));
    const undeclared = Object.keys(input.variables).filter(name => !declared.has(name));
    if (undeclared.length > 0) return { ok: false, error: 'undeclared', names: undeclared };
    const values: Record<string, string> = {};
    for (const definition of template.variable_definitions) {
      const sample = template.sample_variables[definition.name];
      if (sample !== undefined) values[definition.name] = sample;
    }
    Object.assign(values, input.variables);
    const missing = template.variable_definitions
      .filter(definition => definition.required && !values[definition.name])
      .map(definition => definition.name);
    if (missing.length > 0) return { ok: false, error: 'missing', names: missing };

    const to = normalizeAddress(input.to);
    const outcome = simulate(to, input.mode);
    if (!outcome) return { ok: false, error: 'recipient_invalid' };

    const masked: Record<string, string> = {};
    for (const [name, value] of Object.entries(values)) {
      const secret = template.variable_definitions.some(
        definition => definition.name === name && definition.secret
      );
      masked[name] = secret ? maskSecret(value) : value;
    }
    const rendered = renderTemplate(
      { subject: template.subject, html: template.html_body, text: template.text_body },
      masked
    );
    const now = new Date().toISOString();
    const events: TransactionalMessageEvent[] = outcome.events.map(type => ({
      type,
      detail: 'simulated',
      occurred_at: now,
    }));
    const message: StoredMessage = {
      organizationId,
      id: this.nextId++,
      mode: input.mode,
      simulated: outcome.simulated,
      template: template.key ?? '',
      from: template.from ?? { address: '', name: '' },
      to,
      to_masked: maskRecipient(to),
      subject: rendered?.subject ?? template.subject,
      variables: masked,
      status: outcome.status,
      queued_reason: null,
      failure_code: null,
      last_event: lastEvent(events),
      attempts: outcome.simulated ? 0 : 1,
      // The API generates a fresh key for every console test send.
      idempotency_key: `test-send:${input.userId}:${randomBytes(16).toString('hex')}`,
      created_at: now,
      sent_at: outcome.status === 'sent' ? now : null,
      events,
    };
    this.messages.push(message);
    return { ok: true, message: publicMessage(message) };
  }

  /** One keyset page of the organization's log, newest first, with the mode's 24-hour count. */
  list(organizationId: number, query: MockTransactionalLogQuery): TransactionalLogPage {
    const perPage = query.perPage ?? TRANSACTIONAL_LOG_DEFAULT_PAGE_SIZE;
    const matching = this.messages
      .filter(message => message.organizationId === organizationId)
      .filter(message => matches(message, query))
      .sort((a, b) => b.id - a.id);
    const items = matching.slice(0, perPage);
    const dayAgo = Date.now() - dayMs;
    const total = this.messages.filter(
      message =>
        message.organizationId === organizationId &&
        message.mode === query.mode &&
        Date.parse(message.created_at) >= dayAgo
    ).length;
    return {
      items: items.map(logEntry),
      meta: {
        next_cursor: matching.length > perPage ? String(items[items.length - 1].id) : null,
        total_24h: Math.min(total, totalCap),
      },
    };
  }

  /** One message of the organization, of either mode; null when it has none with the id. */
  get(organizationId: number, id: number): TransactionalLogEntryDetail | null {
    const message = this.messages.find(
      candidate => candidate.organizationId === organizationId && candidate.id === id
    );
    if (!message) return null;
    return {
      ...logEntry(message),
      from: { ...message.from },
      variables: { ...message.variables },
      events: [...message.events],
    };
  }

  /** Counts one test send; the seconds until the window has room when it is full. */
  private takeTestSend(userId: string): number | null {
    const now = Date.now();
    const windowStart = Math.floor(now / minuteMs) * minuteMs;
    const current = this.testSends.get(userId);
    const window =
      current && current.windowStart === windowStart ? current : { windowStart, count: 0 };
    if (window.count >= TRANSACTIONAL_TEST_SENDS_PER_MINUTE) {
      return Math.max(1, Math.ceil((windowStart + minuteMs - now) / 1_000));
    }
    window.count += 1;
    this.testSends.set(userId, window);
    return null;
  }
}

function matches(message: StoredMessage, query: MockTransactionalLogQuery): boolean {
  const created = Date.parse(message.created_at);
  if (message.mode !== query.mode) return false;
  if (query.status && message.status !== query.status) return false;
  if (query.lastEvent && message.last_event !== query.lastEvent) return false;
  if (query.outcome === 'delivered' && message.last_event !== 'delivered') return false;
  if (query.outcome === 'problem' && !isProblem(message)) return false;
  if (query.template && message.template !== query.template) return false;
  if (query.to && message.to !== query.to) return false;
  if (created < query.since.getTime()) return false;
  if (query.until && created >= query.until.getTime()) return false;
  if (query.cursor !== undefined && message.id >= query.cursor) return false;
  return true;
}

function isProblem(message: StoredMessage): boolean {
  return (
    message.status === 'failed' ||
    message.status === 'suppressed' ||
    (message.last_event !== null && problemEvents.has(message.last_event))
  );
}

function publicMessage(message: StoredMessage): TransactionalMessage {
  return {
    ...logEntry(message),
    from: { ...message.from },
    to: message.to,
    variables: { ...message.variables },
    attempts: message.attempts,
    events: [...message.events],
  };
}

function logEntry(message: StoredMessage): TransactionalLogEntry {
  return {
    id: message.id,
    mode: message.mode,
    simulated: message.simulated,
    template: message.template,
    to_masked: message.to_masked,
    subject: message.subject,
    status: message.status,
    queued_reason: message.queued_reason,
    failure_code: message.failure_code,
    last_event: message.last_event,
    idempotency_key: message.idempotency_key,
    created_at: message.created_at,
    sent_at: message.sent_at,
  };
}

/**
 * `last_event` of a one-recipient message: its latest event, where `accepted` counts only while no
 * other event is recorded (see Delivery Events); null without events.
 */
function lastEvent(
  events: readonly TransactionalMessageEvent[]
): TransactionalMessage['last_event'] {
  const latest = events.filter(event => event.type !== 'accepted').at(-1);
  return latest?.type ?? events.at(-1)?.type ?? null;
}

/**
 * The simulator decides addresses on its domain in both modes; a test message to any other
 * address is simulated as delivered; a live one is sent (by the mock's log provider). Null for an
 * unknown local part on the simulator domain.
 */
function simulate(
  to: string,
  mode: TransactionalMode
): {
  simulated: boolean;
  status: TransactionalStatus;
  events: TransactionalMessageEvent['type'][];
} | null {
  const at = to.lastIndexOf('@');
  const local = to.slice(0, at);
  const domain = to.slice(at + 1);
  if (domain === SIMULATOR_DOMAIN) {
    const plus = local.indexOf('+');
    const outcome = simulatorOutcomes[plus < 0 ? local : local.slice(0, plus)];
    return outcome ? { simulated: true, ...outcome } : null;
  }
  if (mode === 'test') return { simulated: true, ...simulatorOutcomes.delivered };
  return { simulated: false, status: 'sent', events: [] };
}

export function normalizeAddress(value: string): string {
  return value.trim().toLowerCase();
}

/** The first two characters of the local part (one when it has at most two), then `••`. */
export function maskRecipient(address: string): string {
  const at = address.lastIndexOf('@');
  const local = Array.from(address.slice(0, at));
  const keep = local.length <= 2 ? 1 : 2;
  return `${local.slice(0, keep).join('')}••${address.slice(at)}`;
}

/** `<host>/••••` for an `http(s)` URL with a host, otherwise six bullets. */
export function maskSecret(value: string): string {
  try {
    const url = new URL(value);
    if ((url.protocol === 'http:' || url.protocol === 'https:') && url.hostname !== '') {
      return `${url.hostname}/••••`;
    }
  } catch {
    // Not a URL: masked as an opaque secret.
  }
  return SECRET_MASK;
}

export const mockTransactionalStore = new MockTransactionalStore();
