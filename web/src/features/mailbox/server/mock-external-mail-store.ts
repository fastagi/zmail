import 'server-only';

import { createHash, randomBytes } from 'node:crypto';

import type {
  ExternalMailConnection,
  ExternalMailConnector,
  ExternalMailDirectoryEntry,
  ExternalMailSyncRun,
} from '@/features/mailbox/external-mail-schemas';

/**
 * Development-only external mailboxes (`contracts/MAILBOXES.md`, External Mailboxes). The mock
 * offers two fake connectors, `gmail` and `outlook`, that never leave the process: the
 * "provider page" a sign-in answers is the app's own callback page with a `state` and a fake
 * `code`, so the whole connect flow runs in the default mock mode. The signed-in account is the
 * member's own address at the provider's domain, so connecting the same connector twice restores
 * the same connection (`200`) and a reconnect never mismatches. Everything else follows the
 * contract: single-use states valid for 10 minutes, kept only as a SHA-256; at most 10 live
 * connections per member; revoked connections kept so a rebind restores the same mailbox.
 *
 * The `imap` connector binds with an app password and never stores or logs it. Its fake server
 * answers by what it is sent, so every outcome can be tried without a real account: a host that
 * starts with `unreachable` cannot be reached, the password `wrong-password` is refused, and
 * anything else signs in. More than 10 attempts by a member in 15 minutes are rate limited.
 */
export const MOCK_EXTERNAL_MAIL_CONNECTORS: readonly ExternalMailConnector[] = [
  { id: 'gmail', auth_kind: 'oauth2', capabilities: { read: true } },
  { id: 'outlook', auth_kind: 'oauth2', capabilities: { read: true } },
  { id: 'imap', auth_kind: 'password', capabilities: { read: true } },
];

export const MOCK_IMAP_UNREACHABLE_HOST_PREFIX = 'unreachable';
export const MOCK_IMAP_REFUSED_PASSWORD = 'wrong-password';
const credentialAttemptWindowMs = 15 * 60 * 1_000;
const maxCredentialAttempts = 10;
const maxSyncRuns = 50;
/** A first sync reports `more` until this long after the binding, then finishes its backfill. */
const mockBackfillMs = 30 * 1_000;

const providerDomains: Record<string, string> = {
  gmail: 'gmail.com',
  outlook: 'outlook.com',
};

export const MOCK_EXTERNAL_MAIL_CALLBACK_PATH = '/console/mailboxes/connected/callback';
const authorizationTtlMs = 10 * 60 * 1_000;
const maxLiveConnections = 10;
const maxPendingAuthorizations = 1_000;
const defaultBackfillDays = 14;

interface PendingAuthorization {
  organizationId: number;
  userId: number;
  provider: string;
  backfillDays: number;
  connectionId: number | null;
  expiresAt: number;
}

interface StoredConnection extends ExternalMailConnection {
  organizationId: number;
  userId: number;
}

export type MockStartResult =
  | { ok: true; value: { authorization_url: string; expires_at: string } }
  | { ok: false; error: 'provider_unavailable' | 'connection_not_found' | 'invalid' };

export type MockBindCredentialsResult =
  | { ok: true; status: 200 | 201; value: ExternalMailConnection }
  | { ok: false; error: 'rate_limited'; retryAfterSeconds: number }
  | {
      ok: false;
      error:
        | 'provider_unavailable'
        | 'server_unreachable'
        | 'authorization_failed'
        | 'connection_not_found'
        | 'account_mismatch'
        | 'account_taken'
        | 'connection_limit'
        | 'invalid';
    };

export type MockPurgeResult =
  | { ok: true; value: { deleted_messages: number; completed: boolean } }
  | { ok: false; error: 'not_found' | 'not_revoked' };

export interface MockDirectoryPage {
  items: ExternalMailDirectoryEntry[];
  nextBeforeId: number | null;
}

export type MockCompleteResult =
  | { ok: true; status: 200 | 201; value: ExternalMailConnection }
  | {
      ok: false;
      error:
        | 'authorization_not_found'
        | 'provider_unavailable'
        | 'account_mismatch'
        | 'account_taken'
        | 'connection_limit'
        | 'authorization_failed';
    };

/** What the store needs from the mailbox mock to create and fill an external mailbox. */
export interface MockExternalMailboxes {
  create: (
    organizationId: number,
    userId: number,
    address: string,
    provider: string,
    backfillDays: number
  ) => number;
  sync: (organizationId: number, mailboxId: number, provider: string, backfillDays: number) => void;
  /** Records whether the mailbox sends through the account's SMTP server (password bindings). */
  setSend?: (organizationId: number, mailboxId: number, sends: boolean) => void;
}

interface MockExternalMailDependencies {
  now: () => Date;
  randomState: () => string;
}

const defaultDependencies: MockExternalMailDependencies = {
  now: () => new Date(),
  randomState: () => randomBytes(24).toString('base64url'),
};

export class MockExternalMailStore {
  private pending = new Map<string, PendingAuthorization>();
  private connections = new Map<number, StoredConnection>();
  private nextConnectionId = 1;
  private runs = new Map<number, ExternalMailSyncRun[]>();
  private nextRunId = 1;
  /** Attempt times per member of an organization, for the credential rate limit. */
  private attempts = new Map<string, number[]>();

  constructor(private readonly dependencies: MockExternalMailDependencies = defaultDependencies) {}

  connectors(): ExternalMailConnector[] {
    return MOCK_EXTERNAL_MAIL_CONNECTORS.map(connector => ({ ...connector }));
  }

  /** The member's connections in the organization, live ones first, then id ascending. */
  list(organizationId: number, userId: number): ExternalMailConnection[] {
    return [...this.connections.values()]
      .filter(item => item.organizationId === organizationId && item.userId === userId)
      .sort((left, right) => {
        const leftLive = left.status === 'revoked' ? 1 : 0;
        const rightLive = right.status === 'revoked' ? 1 : 0;
        return leftLive - rightLive || left.id - right.id;
      })
      .map(publicConnection);
  }

  start(
    organizationId: number,
    userId: number,
    origin: string,
    input: { provider?: string; backfill_days?: number; connection_id?: number }
  ): MockStartResult {
    let provider = input.provider;
    let connectionId: number | null = null;
    if (input.connection_id !== undefined) {
      const connection = this.connections.get(input.connection_id);
      if (!connection || connection.organizationId !== organizationId || connection.userId !== userId) {
        return { ok: false, error: 'connection_not_found' };
      }
      if (provider !== undefined && provider !== connection.provider) {
        return { ok: false, error: 'invalid' };
      }
      provider = connection.provider;
      connectionId = connection.id;
    }
    if (provider === undefined) return { ok: false, error: 'invalid' };
    if (!MOCK_EXTERNAL_MAIL_CONNECTORS.some(connector => connector.id === provider)) {
      return { ok: false, error: 'provider_unavailable' };
    }
    this.prune();
    const state = this.dependencies.randomState();
    const expiresAt = this.dependencies.now().getTime() + authorizationTtlMs;
    this.pending.set(hash(state), {
      organizationId,
      userId,
      provider,
      backfillDays: input.backfill_days ?? defaultBackfillDays,
      connectionId,
      expiresAt,
    });
    const url = new URL(MOCK_EXTERNAL_MAIL_CALLBACK_PATH, origin);
    url.searchParams.set('state', state);
    url.searchParams.set('code', `mock-${provider}`);
    return {
      ok: true,
      value: { authorization_url: url.toString(), expires_at: new Date(expiresAt).toISOString() },
    };
  }

  /**
   * Uses the pending sign-in up whatever the outcome. The fake code `mock-<provider>` signs in to
   * `memberEmail`'s local part at the provider; `mock-refuse` is refused like a bad code.
   */
  complete(
    organizationId: number,
    userId: number,
    memberEmail: string,
    input: { state: string; code: string },
    mailboxes: MockExternalMailboxes
  ): MockCompleteResult {
    const key = hash(input.state);
    const pending = this.pending.get(key);
    this.pending.delete(key);
    if (
      !pending ||
      pending.organizationId !== organizationId ||
      pending.userId !== userId ||
      pending.expiresAt <= this.dependencies.now().getTime()
    ) {
      return { ok: false, error: 'authorization_not_found' };
    }
    if (!MOCK_EXTERNAL_MAIL_CONNECTORS.some(connector => connector.id === pending.provider)) {
      return { ok: false, error: 'provider_unavailable' };
    }
    if (input.code !== `mock-${pending.provider}`) {
      return { ok: false, error: 'authorization_failed' };
    }
    const localPart = memberEmail.split('@')[0] || `member${userId}`;
    const address = `${localPart}@${providerDomains[pending.provider] ?? `${pending.provider}.example`}`;

    if (pending.connectionId !== null) {
      const own = this.connections.get(pending.connectionId);
      if (!own) return { ok: false, error: 'authorization_not_found' };
      if (own.account_address !== address) return { ok: false, error: 'account_mismatch' };
    }
    return this.bindAccount(
      organizationId,
      userId,
      pending.provider,
      address,
      pending.backfillDays,
      mailboxes
    );
  }

  /**
   * Binds an account with an app password. The password is compared with the fake server's one
   * refused value and dropped; it is never stored, logged, or returned.
   */
  bindCredentials(
    organizationId: number,
    userId: number,
    input: {
      provider: string;
      host: string;
      username: string;
      password: string;
      smtp_host?: string;
      backfill_days?: number;
      connection_id?: number;
    },
    mailboxes: MockExternalMailboxes
  ): MockBindCredentialsResult {
    const connector = MOCK_EXTERNAL_MAIL_CONNECTORS.find(item => item.id === input.provider);
    if (!connector || connector.auth_kind !== 'password') {
      return { ok: false, error: 'provider_unavailable' };
    }
    const own =
      input.connection_id === undefined ? null : this.connections.get(input.connection_id);
    if (
      own === undefined ||
      (own !== null &&
        (own.organizationId !== organizationId ||
          own.userId !== userId ||
          own.provider !== input.provider))
    ) {
      return { ok: false, error: 'connection_not_found' };
    }
    const limited = this.recordAttempt(organizationId, userId);
    if (limited !== null) return { ok: false, error: 'rate_limited', retryAfterSeconds: limited };
    const host = input.host.trim().toLowerCase();
    if (host === '' || host.startsWith(MOCK_IMAP_UNREACHABLE_HOST_PREFIX)) {
      return { ok: false, error: 'server_unreachable' };
    }
    if (input.password === MOCK_IMAP_REFUSED_PASSWORD) {
      return { ok: false, error: 'authorization_failed' };
    }
    const username = input.username.trim().toLowerCase();
    const address = username.includes('@') ? username : `${username}@${host}`;
    if (own && own.account_address !== address) return { ok: false, error: 'account_mismatch' };
    const result = this.bindAccount(
      organizationId,
      userId,
      input.provider,
      address,
      input.backfill_days ?? defaultBackfillDays,
      mailboxes
    );
    if (result.ok) {
      mailboxes.setSend?.(organizationId, result.value.mailbox_id, Boolean(input.smtp_host));
    }
    return result;
  }

  private bindAccount(
    organizationId: number,
    userId: number,
    provider: string,
    address: string,
    backfillDays: number,
    mailboxes: MockExternalMailboxes
  ):
    | { ok: true; status: 200 | 201; value: ExternalMailConnection }
    | { ok: false; error: 'account_taken' | 'connection_limit' } {
    const now = this.dependencies.now().toISOString();
    const existing = [...this.connections.values()].find(
      item =>
        item.organizationId === organizationId &&
        item.provider === provider &&
        item.account_address === address
    );
    if (existing && existing.userId !== userId) return { ok: false, error: 'account_taken' };
    const live = [...this.connections.values()].filter(
      item =>
        item.organizationId === organizationId && item.userId === userId && item.status !== 'revoked'
    );
    if (existing) {
      const backfill = existing.status === 'revoked';
      if (backfill && live.length >= maxLiveConnections) {
        return { ok: false, error: 'connection_limit' };
      }
      Object.assign(existing, {
        status: 'active',
        last_error_code: null,
        last_synced_at: now,
      } satisfies Partial<StoredConnection>);
      if (backfill) {
        mailboxes.sync(organizationId, existing.mailbox_id, existing.provider, backfillDays);
      }
      this.recordRun(existing.id, { outcome: 'ok', fetched: 3, stored: backfill ? 3 : 0, more: backfill });
      return { ok: true, status: 200, value: publicConnection(existing) };
    }
    if (live.length >= maxLiveConnections) return { ok: false, error: 'connection_limit' };
    const mailboxId = mailboxes.create(organizationId, userId, address, provider, backfillDays);
    const connection: StoredConnection = {
      id: this.nextConnectionId++,
      organizationId,
      userId,
      provider,
      account_address: address,
      mailbox_id: mailboxId,
      status: 'active',
      last_synced_at: now,
      last_error_code: null,
      created_at: now,
    };
    this.connections.set(connection.id, connection);
    this.recordRun(connection.id, { outcome: 'ok', fetched: 3, stored: 3, more: true });
    return { ok: true, status: 201, value: publicConnection(connection) };
  }

  /**
   * The latest runs of the member's own connection, newest first; null for anyone else. A first
   * backfill reports `more` for a short while, then the mock records the page that finishes it.
   */
  syncRuns(organizationId: number, userId: number, connectionId: number): ExternalMailSyncRun[] | null {
    const connection = this.connections.get(connectionId);
    if (!connection || connection.organizationId !== organizationId || connection.userId !== userId) {
      return null;
    }
    const runs = this.runs.get(connectionId) ?? [];
    const latest = runs[0];
    if (
      connection.status === 'active' &&
      latest?.outcome === 'ok' &&
      latest.more &&
      this.dependencies.now().getTime() - Date.parse(latest.finished_at) >= mockBackfillMs
    ) {
      this.recordRun(connectionId, { outcome: 'ok', fetched: 0, stored: 0, more: false });
      connection.last_synced_at = this.dependencies.now().toISOString();
    }
    return (this.runs.get(connectionId) ?? []).map(run => ({ ...run }));
  }

  /** Deletes the copies, mailbox, connection, and runs of the member's own revoked connection. */
  purge(
    organizationId: number,
    userId: number,
    connectionId: number,
    removeMailbox: (organizationId: number, mailboxId: number) => number | null
  ): MockPurgeResult {
    const connection = this.connections.get(connectionId);
    if (!connection || connection.organizationId !== organizationId || connection.userId !== userId) {
      return { ok: false, error: 'not_found' };
    }
    if (connection.status !== 'revoked') return { ok: false, error: 'not_revoked' };
    const deleted = removeMailbox(organizationId, connection.mailbox_id) ?? 0;
    this.connections.delete(connectionId);
    this.runs.delete(connectionId);
    return { ok: true, value: { deleted_messages: deleted, completed: true } };
  }

  /** Every connection of the organization, newest first, with the address masked. */
  directory(
    organizationId: number,
    query: { beforeId?: number; perPage: number; ownerName: (userId: number) => string | null }
  ): MockDirectoryPage {
    const matching = [...this.connections.values()]
      .filter(
        item =>
          item.organizationId === organizationId &&
          (query.beforeId === undefined || item.id < query.beforeId)
      )
      .sort((left, right) => right.id - left.id);
    const page = matching.slice(0, query.perPage);
    const last = page.at(-1);
    return {
      items: page.map(item => ({
        id: item.id,
        provider: item.provider,
        status: item.status,
        masked_account_address: maskAddress(item.account_address),
        owner_user_id: item.userId,
        owner_display_name: query.ownerName(item.userId),
        last_synced_at: item.last_synced_at,
        created_at: item.created_at,
      })),
      nextBeforeId: matching.length > page.length && last ? last.id : null,
    };
  }

  /** Unbinds; the member who bound it, or an owner or admin, may. Revoked again answers ok. */
  revoke(
    organizationId: number,
    viewer: { userId: number; manages: boolean },
    connectionId: number
  ): boolean {
    const connection = this.connections.get(connectionId);
    if (
      !connection ||
      connection.organizationId !== organizationId ||
      (connection.userId !== viewer.userId && !viewer.manages)
    ) {
      return false;
    }
    connection.status = 'revoked';
    return true;
  }

  /** The mailbox a connection of the organization fills, or null for an unknown connection. */
  mailboxIdOf(organizationId: number, connectionId: number): number | null {
    const connection = this.connections.get(connectionId);
    return connection && connection.organizationId === organizationId
      ? connection.mailbox_id
      : null;
  }

  /** Development aid: the provider refused the grant, as a failed sync records it. */
  markNeedsReconnect(connectionId: number): void {
    const connection = this.connections.get(connectionId);
    if (!connection || connection.status === 'revoked') return;
    connection.status = 'needs_reconnect';
    connection.last_error_code = 'external.auth_failed';
    this.recordRun(connectionId, { outcome: 'auth_failed', fetched: 0, stored: 0, more: false });
  }

  reset(): void {
    this.pending.clear();
    this.connections.clear();
    this.nextConnectionId = 1;
    this.runs.clear();
    this.nextRunId = 1;
    this.attempts.clear();
  }

  /** Seconds until the member may try again, or null when this attempt is allowed. */
  private recordAttempt(organizationId: number, userId: number): number | null {
    const key = `${organizationId}:${userId}`;
    const now = this.dependencies.now().getTime();
    const recent = (this.attempts.get(key) ?? []).filter(
      time => now - time < credentialAttemptWindowMs
    );
    if (recent.length >= maxCredentialAttempts) {
      this.attempts.set(key, recent);
      const oldest = recent[0] ?? now;
      return Math.max(1, Math.ceil((oldest + credentialAttemptWindowMs - now) / 1_000));
    }
    recent.push(now);
    this.attempts.set(key, recent);
    return null;
  }

  private recordRun(
    connectionId: number,
    run: { outcome: ExternalMailSyncRun['outcome']; fetched: number; stored: number; more: boolean }
  ): void {
    const time = this.dependencies.now().toISOString();
    const runs = this.runs.get(connectionId) ?? [];
    runs.unshift({
      id: this.nextRunId++,
      started_at: time,
      finished_at: time,
      outcome: run.outcome,
      fetched_count: run.fetched,
      stored_count: run.stored,
      more: run.more,
      error_code: run.outcome === 'auth_failed' ? 'external.auth_failed' : null,
      timed_out: false,
      throttled: false,
    });
    this.runs.set(connectionId, runs.slice(0, maxSyncRuns));
  }

  private prune(): void {
    const now = this.dependencies.now().getTime();
    for (const [key, item] of this.pending) {
      if (item.expiresAt <= now) this.pending.delete(key);
    }
    while (this.pending.size >= maxPendingAuthorizations) {
      const oldest = this.pending.keys().next().value;
      if (oldest === undefined) break;
      this.pending.delete(oldest);
    }
  }
}

/** The first two characters of the local part (one when it is that short), `***`, the domain. */
export function maskAddress(address: string): string {
  const at = address.lastIndexOf('@');
  const local = at < 0 ? address : address.slice(0, at);
  const domain = at < 0 ? '' : address.slice(at);
  const keep = Array.from(local).slice(0, local.length <= 2 ? 1 : 2).join('');
  return `${keep}***${domain}`;
}

function hash(state: string): string {
  return createHash('sha256').update(state).digest('hex');
}

function publicConnection(connection: StoredConnection): ExternalMailConnection {
  return {
    id: connection.id,
    provider: connection.provider,
    account_address: connection.account_address,
    mailbox_id: connection.mailbox_id,
    status: connection.status,
    last_synced_at: connection.last_synced_at,
    last_error_code: connection.last_error_code,
    created_at: connection.created_at,
  };
}

export const mockExternalMailStore = new MockExternalMailStore();
