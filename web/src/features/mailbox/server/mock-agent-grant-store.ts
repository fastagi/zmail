import 'server-only';

import type { ApiKey } from '@/features/api-key/types';
import {
  MAIL_AGENT_GRANT_MAX_DAYS,
  MAIL_AGENT_GRANT_MAX_PER_MAILBOX,
  type MailAgentAuditEntry,
  type MailAgentDraft,
  type MailAgentGrant,
  type MailAgentGrantInput,
} from '@/features/mailbox/agent-grant-schemas';
import type { Mailbox, MailMessageSummary } from '@/features/mailbox/types';
import { isUsableAgentKey } from '@/features/mailbox/utils/agent-grant-access';

const dayMilliseconds = 24 * 60 * 60 * 1_000;
const maxMockOrganizations = 1_000;
const maxAuditRowsPerGrant = 100;

/** A grant as stored; the status is computed from the clock when it is read. */
type StoredGrant = Omit<MailAgentGrant, 'status'>;

interface OrganizationState {
  nextGrantId: number;
  nextAuditId: number;
  nextDraftId: number;
  grants: StoredGrant[];
  audits: MailAgentAuditEntry[];
  drafts: MailAgentDraft[];
}

export type MockAgentGrantError = 'invalid' | 'exists' | 'limit' | 'not_found';
export type MockAgentGrantResult<T> =
  { ok: true; value: T } | { ok: false; error: MockAgentGrantError };

/** What the mock knows of the issuer when a grant is issued. */
export interface MockAgentGrantIssuer {
  userId: number;
  /** The issuer's own keys; another person's key is refused like an unknown one. */
  keys: readonly ApiKey[];
  /** The mailbox's label ids, which a label filter must name. */
  labelIds: ReadonlySet<number>;
  /**
   * The newest inbound message of the mailbox, if any. The mock agent "reads" it at once, and
   * drafts a reply to it when the grant may draft, so the tab has activity and a draft to show.
   */
  newestInbound: MailMessageSummary | null;
}

/**
 * Development stand-in for the agent grants of `contracts/MAILBOXES.md`. Who may call is decided
 * by the route from the mailbox's access; this store judges the key, the capabilities, the
 * filters, the expiry, and the limits, and keeps a little activity so the tab can be exercised.
 */
export class MockAgentGrantStore {
  private organizations = new Map<number, OrganizationState>();

  constructor(private readonly now: () => Date = () => new Date()) {}

  list(organizationId: number, mailboxId: number): MailAgentGrant[] {
    return this.state(organizationId)
      .grants.filter(grant => grant.mailbox_id === mailboxId)
      .sort((left, right) => right.id - left.id)
      .map(grant => this.withStatus(grant));
  }

  issue(
    organizationId: number,
    mailbox: Pick<Mailbox, 'id' | 'origin' | 'access'>,
    issuer: MockAgentGrantIssuer,
    input: MailAgentGrantInput
  ): MockAgentGrantResult<MailAgentGrant> {
    const state = this.state(organizationId);
    const now = this.now();
    const key = issuer.keys.find(candidate => candidate.id === input.api_key_id);
    if (!key || !isUsableAgentKey(key, now)) return { ok: false, error: 'invalid' };
    if (!input.capabilities.includes('read')) return { ok: false, error: 'invalid' };
    const drafts = input.capabilities.includes('draft');
    if (drafts && (mailbox.origin === 'connected' || !mailbox.access.send)) {
      return { ok: false, error: 'invalid' };
    }
    if (input.filters.label_ids.some(id => !issuer.labelIds.has(id))) {
      return { ok: false, error: 'invalid' };
    }
    const expires = Date.parse(input.expires_at);
    if (
      Number.isNaN(expires) ||
      expires <= now.getTime() ||
      expires > now.getTime() + MAIL_AGENT_GRANT_MAX_DAYS * dayMilliseconds
    ) {
      return { ok: false, error: 'invalid' };
    }
    const working = this.list(organizationId, mailbox.id).filter(
      grant => grant.status === 'active'
    );
    if (working.some(grant => grant.api_key_id === key.id)) return { ok: false, error: 'exists' };
    if (working.length >= MAIL_AGENT_GRANT_MAX_PER_MAILBOX) return { ok: false, error: 'limit' };

    const createdAt = now.toISOString();
    const grant: StoredGrant = {
      id: state.nextGrantId++,
      mailbox_id: mailbox.id,
      api_key_id: key.id,
      api_key_name: key.name,
      api_key_prefix: key.key_prefix,
      key_user_id: issuer.userId,
      granted_by: issuer.userId,
      capabilities: drafts ? ['read', 'draft'] : ['read'],
      reveal_secrets: input.reveal_secrets,
      filters: {
        senders: [...new Set(input.filters.senders.map(sender => sender.toLowerCase()))],
        label_ids: [...new Set(input.filters.label_ids)],
      },
      expires_at: new Date(expires).toISOString(),
      revoked_at: null,
      revoke_reason: null,
      created_at: createdAt,
    };
    state.grants.push(grant);
    this.simulateAgent(state, grant, issuer.newestInbound, createdAt);
    return { ok: true, value: this.withStatus(grant) };
  }

  /** Ends a grant at once; an ended one stays as it was. */
  revoke(organizationId: number, mailboxId: number, grantId: number): MockAgentGrantResult<null> {
    const grant = this.find(organizationId, mailboxId, grantId);
    if (!grant) return { ok: false, error: 'not_found' };
    if (this.withStatus(grant).status === 'active') {
      grant.revoked_at = this.now().toISOString();
      grant.revoke_reason = 'revoked';
    }
    return { ok: true, value: null };
  }

  activity(
    organizationId: number,
    mailboxId: number,
    grantId: number
  ): MockAgentGrantResult<MailAgentAuditEntry[]> {
    if (!this.find(organizationId, mailboxId, grantId)) return { ok: false, error: 'not_found' };
    return {
      ok: true,
      value: this.state(organizationId)
        .audits.filter(entry => entry.grant_id === grantId)
        .sort((left, right) => right.id - left.id)
        .slice(0, maxAuditRowsPerGrant),
    };
  }

  /** The drafts of the mailbox whose message still exists, newest first. */
  drafts(
    organizationId: number,
    mailboxId: number,
    messageExists: (messageId: number) => boolean
  ): MailAgentDraft[] {
    return this.state(organizationId)
      .drafts.filter(draft => draft.mailbox_id === mailboxId && messageExists(draft.message_id))
      .sort((left, right) => right.id - left.id);
  }

  reset(): void {
    this.organizations.clear();
  }

  private simulateAgent(
    state: OrganizationState,
    grant: StoredGrant,
    message: MailMessageSummary | null,
    at: string
  ): void {
    if (!message) return;
    const audit = (action: MailAgentAuditEntry['action']) =>
      state.audits.push({
        id: state.nextAuditId++,
        grant_id: grant.id,
        api_key_id: grant.api_key_id,
        message_id: message.id,
        action,
        created_at: at,
      });
    audit(grant.reveal_secrets ? 'read_revealed' : 'read');
    if (!grant.capabilities.includes('draft')) return;
    state.drafts.push({
      id: state.nextDraftId++,
      mailbox_id: grant.mailbox_id,
      grant_id: grant.id,
      message_id: message.id,
      api_key_id: grant.api_key_id,
      text: `Thanks for your message about "${message.subject}". We are looking into it and will reply shortly.`,
      html: '',
      status: 'pending',
      created_at: at,
      updated_at: at,
    });
    audit('draft');
  }

  private find(organizationId: number, mailboxId: number, grantId: number) {
    return this.state(organizationId).grants.find(
      grant => grant.id === grantId && grant.mailbox_id === mailboxId
    );
  }

  private withStatus(grant: StoredGrant): MailAgentGrant {
    const status =
      grant.revoked_at !== null
        ? 'revoked'
        : Date.parse(grant.expires_at) <= this.now().getTime()
          ? 'expired'
          : 'active';
    return { ...grant, filters: { ...grant.filters }, status };
  }

  private state(organizationId: number): OrganizationState {
    let state = this.organizations.get(organizationId);
    if (!state) {
      if (this.organizations.size >= maxMockOrganizations) this.organizations.clear();
      state = {
        nextGrantId: 1,
        nextAuditId: 1,
        nextDraftId: 1,
        grants: [],
        audits: [],
        drafts: [],
      };
      this.organizations.set(organizationId, state);
    }
    return state;
  }
}

export const mockAgentGrantStore = new MockAgentGrantStore();

/** One page of a mock listing, with the shared page meta and links. */
export function mockAgentPage<T>(all: readonly T[], page: number, perPage: number, path: string) {
  const total = all.length;
  const lastPage = Math.max(1, Math.ceil(total / perPage));
  const start = (page - 1) * perPage;
  const items = all.slice(start, start + perPage);
  const href = (target: number) => `${path}?page=${target}&per_page=${perPage}`;
  return {
    items,
    meta: {
      current_page: page,
      per_page: perPage,
      total,
      last_page: lastPage,
      from: items.length ? start + 1 : 0,
      to: items.length ? start + items.length : 0,
    },
    links: {
      first: href(1),
      last: href(lastPage),
      prev: page > 1 ? href(page - 1) : null,
      next: page < lastPage ? href(page + 1) : null,
    },
  };
}
