import {
  MAIL_AGENT_FILTER_MAX_ENTRIES,
  MAIL_AGENT_SENDER_MAX_LENGTH,
  type MailAgentCapability,
  type MailAgentGrantInput,
} from '@/features/mailbox/agent-grant-schemas';

/**
 * Shape checks of an agent grant request (`contracts/MAILBOXES.md`, Agent Grants) for the browser
 * route. The rules on the key, the capabilities, the labels, and the expiry belong to the API and,
 * in development, to the mock store.
 */

const capabilities: readonly MailAgentCapability[] = ['read', 'draft'];

/** Only the canonical fields reach the API; an unknown field is dropped. */
export function mailAgentGrantInput(
  body: Record<string, unknown>
): { ok: true; value: MailAgentGrantInput } | { ok: false; errors: Record<string, string[]> } {
  const errors: Record<string, string[]> = {};
  const { api_key_id: apiKeyId, capabilities: rawCapabilities, expires_at: expiresAt } = body;
  const revealSecrets = body.reveal_secrets ?? false;
  const filters = body.filters ?? {};

  if (typeof apiKeyId !== 'number' || !Number.isSafeInteger(apiKeyId) || apiKeyId < 1) {
    errors.api_key_id = ['The api_key_id field is invalid'];
  }
  if (
    !Array.isArray(rawCapabilities) ||
    rawCapabilities.length === 0 ||
    rawCapabilities.length > capabilities.length ||
    rawCapabilities.some(item => !capabilities.includes(item as MailAgentCapability)) ||
    new Set(rawCapabilities).size !== rawCapabilities.length
  ) {
    errors.capabilities = ['The capabilities field is invalid'];
  }
  if (typeof revealSecrets !== 'boolean') {
    errors.reveal_secrets = ['The reveal_secrets field must be a boolean'];
  }
  if (typeof expiresAt !== 'string' || Number.isNaN(Date.parse(expiresAt))) {
    errors.expires_at = ['The expires_at field must be a date-time'];
  }
  let senders: string[] = [];
  let labelIds: number[] = [];
  if (typeof filters !== 'object' || filters === null || Array.isArray(filters)) {
    errors.filters = ['The filters field is invalid'];
  } else {
    const raw = filters as Record<string, unknown>;
    const rawSenders = raw.senders ?? [];
    const rawLabels = raw.label_ids ?? [];
    if (
      !Array.isArray(rawSenders) ||
      rawSenders.length > MAIL_AGENT_FILTER_MAX_ENTRIES ||
      rawSenders.some(
        item =>
          typeof item !== 'string' ||
          item.trim() === '' ||
          item.trim().length > MAIL_AGENT_SENDER_MAX_LENGTH
      )
    ) {
      errors['filters.senders'] = ['The sender filters are invalid'];
    } else {
      senders = (rawSenders as string[]).map(item => item.trim());
    }
    if (
      !Array.isArray(rawLabels) ||
      rawLabels.length > MAIL_AGENT_FILTER_MAX_ENTRIES ||
      rawLabels.some(item => typeof item !== 'number' || !Number.isSafeInteger(item) || item < 1)
    ) {
      errors['filters.label_ids'] = ['The label filters are invalid'];
    } else {
      labelIds = rawLabels as number[];
    }
  }
  if (Object.keys(errors).length > 0) return { ok: false, errors };
  return {
    ok: true,
    value: {
      api_key_id: apiKeyId as number,
      capabilities: rawCapabilities as MailAgentCapability[],
      reveal_secrets: revealSecrets as boolean,
      filters: { senders, label_ids: labelIds },
      expires_at: expiresAt as string,
    },
  };
}
