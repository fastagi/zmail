import {
  MAIL_AGENT_FILTER_MAX_ENTRIES,
  MAIL_AGENT_GRANT_MAX_DAYS,
  MAIL_AGENT_SENDER_MAX_LENGTH,
  type MailAgentGrantInput,
} from '@/features/mailbox/agent-grant-schemas';

const dayMilliseconds = 24 * 60 * 60 * 1_000;
/**
 * The expiry is computed in the browser and judged by the API a moment later against its own
 * clock; a minute off the longest expiry keeps a 90-day grant within the limit.
 */
const clockMarginMilliseconds = 60 * 1_000;

const addressPattern = /^[^\s@]+@(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/iu;
const domainPattern = /^@?(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/iu;

/** What the person filled in; `senders` is the raw text of the sender field. */
export interface AgentGrantFormValues {
  apiKeyId: number | null;
  scope: 'all' | 'filtered';
  senders: string;
  labelIds: number[];
  draft: boolean;
  revealSecrets: boolean;
  days: number;
}

export type AgentGrantFormError =
  | { field: 'key'; key: 'agents.form.errors.key' }
  | { field: 'key'; key: 'agents.form.errors.keyTaken' }
  | { field: 'filters'; key: 'agents.form.errors.filtersEmpty' }
  | { field: 'senders'; key: 'agents.form.errors.sendersTooMany'; max: number }
  | { field: 'senders'; key: 'agents.form.errors.senderInvalid'; value: string }
  | { field: 'labels'; key: 'agents.form.errors.labelsTooMany'; max: number }
  | { field: 'expiry'; key: 'agents.form.errors.expiry'; max: number }
  | { field: 'limit'; key: 'agents.form.limitReached'; max: number };

/** The sender entries: split on commas and line breaks, trimmed, lowercased, without repeats. */
export function parseSenders(text: string): string[] {
  const entries = text
    .split(/[\s,;]+/u)
    .map(entry => entry.trim().toLowerCase())
    .filter(entry => entry !== '');
  return [...new Set(entries)];
}

/** An exact address, or a domain with an optional leading `@`. */
export function isSenderFilter(value: string): boolean {
  if (value.length > MAIL_AGENT_SENDER_MAX_LENGTH) return false;
  return value.indexOf('@', 1) > 0 ? addressPattern.test(value) : domainPattern.test(value);
}

/**
 * The limits of `contracts/MAILBOXES.md` checked before submitting: a key, a non-empty filter when
 * filtering, at most 20 senders and 20 labels, at most 90 days, at most 10 active grants, and a key
 * without an active grant here. The API still judges every request.
 */
export function validateAgentGrantForm(
  values: AgentGrantFormValues,
  context: { activeGrants: number; activeKeyIds: ReadonlySet<number>; maxGrants: number }
): AgentGrantFormError[] {
  const errors: AgentGrantFormError[] = [];
  if (context.activeGrants >= context.maxGrants) {
    errors.push({ field: 'limit', key: 'agents.form.limitReached', max: context.maxGrants });
  }
  if (values.apiKeyId === null) {
    errors.push({ field: 'key', key: 'agents.form.errors.key' });
  } else if (context.activeKeyIds.has(values.apiKeyId)) {
    errors.push({ field: 'key', key: 'agents.form.errors.keyTaken' });
  }
  if (values.scope === 'filtered') {
    const senders = parseSenders(values.senders);
    if (senders.length === 0 && values.labelIds.length === 0) {
      errors.push({ field: 'filters', key: 'agents.form.errors.filtersEmpty' });
    }
    if (senders.length > MAIL_AGENT_FILTER_MAX_ENTRIES) {
      errors.push({
        field: 'senders',
        key: 'agents.form.errors.sendersTooMany',
        max: MAIL_AGENT_FILTER_MAX_ENTRIES,
      });
    } else {
      const invalid = senders.find(sender => !isSenderFilter(sender));
      if (invalid !== undefined) {
        errors.push({ field: 'senders', key: 'agents.form.errors.senderInvalid', value: invalid });
      }
    }
    if (values.labelIds.length > MAIL_AGENT_FILTER_MAX_ENTRIES) {
      errors.push({
        field: 'labels',
        key: 'agents.form.errors.labelsTooMany',
        max: MAIL_AGENT_FILTER_MAX_ENTRIES,
      });
    }
  }
  if (
    !Number.isInteger(values.days) ||
    values.days < 1 ||
    values.days > MAIL_AGENT_GRANT_MAX_DAYS
  ) {
    errors.push({
      field: 'expiry',
      key: 'agents.form.errors.expiry',
      max: MAIL_AGENT_GRANT_MAX_DAYS,
    });
  }
  return errors;
}

/** The request body for valid values; read is always included, filters only when filtering. */
export function agentGrantInput(values: AgentGrantFormValues, now: Date): MailAgentGrantInput {
  const filtered = values.scope === 'filtered';
  const expires = now.getTime() + values.days * dayMilliseconds - clockMarginMilliseconds;
  return {
    api_key_id: values.apiKeyId ?? 0,
    capabilities: values.draft ? ['read', 'draft'] : ['read'],
    reveal_secrets: values.revealSecrets,
    filters: {
      senders: filtered ? parseSenders(values.senders) : [],
      label_ids: filtered ? [...new Set(values.labelIds)] : [],
    },
    expires_at: new Date(expires).toISOString(),
  };
}
