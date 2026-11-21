import 'server-only';

import { createHash, randomBytes } from 'node:crypto';

import type {
  ExternalMailConnection,
  ExternalMailConnector,
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
 */
export const MOCK_EXTERNAL_MAIL_CONNECTORS: readonly ExternalMailConnector[] = [
  { id: 'gmail', auth_kind: 'oauth2', capabilities: { read: true } },
  { id: 'outlook', auth_kind: 'oauth2', capabilities: { read: true } },
];

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
    const now = this.dependencies.now().toISOString();

    if (pending.connectionId !== null) {
      const own = this.connections.get(pending.connectionId);
      if (!own) return { ok: false, error: 'authorization_not_found' };
      if (own.account_address !== address) return { ok: false, error: 'account_mismatch' };
    }
    const existing = [...this.connections.values()].find(
      item =>
        item.organizationId === organizationId &&
        item.provider === pending.provider &&
        item.account_address === address
    );
    if (existing && existing.userId !== userId) return { ok: false, error: 'account_taken' };
    if (existing) {
      const backfill = existing.status === 'revoked';
      Object.assign(existing, {
        status: 'active',
        last_error_code: null,
        last_synced_at: now,
      } satisfies Partial<StoredConnection>);
      if (backfill) {
        mailboxes.sync(organizationId, existing.mailbox_id, existing.provider, pending.backfillDays);
      }
      return { ok: true, status: 200, value: publicConnection(existing) };
    }
    const live = [...this.connections.values()].filter(
      item =>
        item.organizationId === organizationId && item.userId === userId && item.status !== 'revoked'
    );
    if (live.length >= maxLiveConnections) return { ok: false, error: 'connection_limit' };
    const mailboxId = mailboxes.create(
      organizationId,
      userId,
      address,
      pending.provider,
      pending.backfillDays
    );
    const connection: StoredConnection = {
      id: this.nextConnectionId++,
      organizationId,
      userId,
      provider: pending.provider,
      account_address: address,
      mailbox_id: mailboxId,
      status: 'active',
      last_synced_at: now,
      last_error_code: null,
      created_at: now,
    };
    this.connections.set(connection.id, connection);
    return { ok: true, status: 201, value: publicConnection(connection) };
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

  /** Development aid: the provider refused the grant, as a failed sync records it. */
  markNeedsReconnect(connectionId: number): void {
    const connection = this.connections.get(connectionId);
    if (!connection || connection.status === 'revoked') return;
    connection.status = 'needs_reconnect';
    connection.last_error_code = 'external.auth_failed';
  }

  reset(): void {
    this.pending.clear();
    this.connections.clear();
    this.nextConnectionId = 1;
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
