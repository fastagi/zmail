import type { z } from 'zod/mini';

import {
  mailAgentActivitySchema,
  mailAgentDraftPageEnvelopeSchema,
  mailAgentGrantPageEnvelopeSchema,
  mailAgentGrantSchema,
  MAIL_AGENT_DRAFTS_PAGE_SIZE,
  MAIL_AGENT_GRANTS_MAX_PAGES,
  MAIL_AGENT_GRANTS_PAGE_SIZE,
  type MailAgentAuditEntry,
  type MailAgentDraftPage,
  type MailAgentGrant,
  type MailAgentGrantInput,
  type MailAgentGrantList,
  type MailAgentGrantPage,
} from '@/features/mailbox/agent-grant-schemas';
import { ClientErrorCode } from '@/http/codes';
import {
  assertContract,
  type Accepts,
  type ContractData,
  type ContractRequest,
  type ContractResponse,
  type Sends,
} from '@/http/contract';
import request, { ApiError } from '@/http/request';

/**
 * Browser service for agent grants in `contracts/MAILBOXES.md` (section Agent Grants). Issuing,
 * listing, revoking, and the activity belong to a personal mailbox's owner or a shared mailbox's
 * `manage` member; the drafts need `send`. The API decides; every call names the organization.
 */
export const agentGrantService = {
  /** One page of the mailbox's grants, newest first. */
  async list(organizationId: number, mailboxId: number, page = 1): Promise<MailAgentGrantPage> {
    validId(mailboxId);
    validId(page);
    const parsed = mailAgentGrantPageEnvelopeSchema.safeParse(
      await request.getEnvelope<unknown>(`/mailboxes/${mailboxId}/agent-grants`, {
        headers: organizationHeaders(organizationId),
        params: { page, per_page: MAIL_AGENT_GRANTS_PAGE_SIZE },
      })
    );
    if (!parsed.success || parsed.data.data.some(grant => grant.mailbox_id !== mailboxId)) {
      throw invalidResponse();
    }
    return { items: parsed.data.data, meta: parsed.data.meta };
  },

  /**
   * Every grant of the mailbox, newest first, read in pages of 100 until the last page (at most
   * 20 pages). A mailbox has at most 10 working grants; the rest is history.
   */
  async listAll(organizationId: number, mailboxId: number): Promise<MailAgentGrantList> {
    const items: MailAgentGrant[] = [];
    let page = 1;
    let lastPage = 1;
    let total = 0;
    do {
      const result = await this.list(organizationId, mailboxId, page);
      items.push(...result.items);
      lastPage = result.meta.last_page;
      total = result.meta.total;
      page += 1;
    } while (page <= lastPage && page <= MAIL_AGENT_GRANTS_MAX_PAGES);
    const unique = new Map(items.map(grant => [grant.id, grant]));
    return { items: [...unique.values()], total, complete: page > lastPage };
  },

  /** Issues a grant and answers it. */
  async issue(
    organizationId: number,
    mailboxId: number,
    input: MailAgentGrantInput
  ): Promise<MailAgentGrant> {
    validId(mailboxId);
    validId(input.api_key_id);
    const parsed = mailAgentGrantSchema.safeParse(
      await request.post<unknown, MailAgentGrantInput>(
        `/mailboxes/${mailboxId}/agent-grants`,
        input,
        { headers: organizationHeaders(organizationId) }
      )
    );
    if (!parsed.success || parsed.data.mailbox_id !== mailboxId) throw invalidResponse();
    return parsed.data;
  },

  /** Revokes at once; an ended grant answers `204` too. */
  async revoke(organizationId: number, mailboxId: number, grantId: number): Promise<void> {
    validId(mailboxId);
    validId(grantId);
    await request.delete<void>(`/mailboxes/${mailboxId}/agent-grants/${grantId}`, {
      headers: organizationHeaders(organizationId),
    });
  },

  /** The grant's newest audit rows, newest first; they never hold a subject or a body. */
  async activity(
    organizationId: number,
    mailboxId: number,
    grantId: number
  ): Promise<MailAgentAuditEntry[]> {
    validId(mailboxId);
    validId(grantId);
    const parsed = mailAgentActivitySchema.safeParse(
      await request.get<unknown>(`/mailboxes/${mailboxId}/agent-grants/${grantId}/activity`, {
        headers: organizationHeaders(organizationId),
      })
    );
    if (!parsed.success || parsed.data.some(entry => entry.grant_id !== grantId)) {
      throw invalidResponse();
    }
    return parsed.data;
  },

  /** One page of the drafts agents stored for the mailbox, newest first; needs `send`. */
  async drafts(organizationId: number, mailboxId: number, page = 1): Promise<MailAgentDraftPage> {
    validId(mailboxId);
    validId(page);
    const parsed = mailAgentDraftPageEnvelopeSchema.safeParse(
      await request.getEnvelope<unknown>(`/mailboxes/${mailboxId}/agent-drafts`, {
        headers: organizationHeaders(organizationId),
        params: { page, per_page: MAIL_AGENT_DRAFTS_PAGE_SIZE },
      })
    );
    if (!parsed.success || parsed.data.data.some(draft => draft.mailbox_id !== mailboxId)) {
      throw invalidResponse();
    }
    return { items: parsed.data.data, meta: parsed.data.meta };
  },
};

function organizationHeaders(organizationId: number): Record<string, string> {
  validId(organizationId);
  return { 'Organization-Id': String(organizationId) };
}

function validId(value: number): void {
  if (!Number.isSafeInteger(value) || value < 1) throw invalidResponse();
}

function invalidResponse(): ApiError {
  return new ApiError(
    'Agent grant service returned an invalid response',
    ClientErrorCode.INVALID_RESPONSE
  );
}

assertContract<
  Accepts<
    z.input<typeof mailAgentGrantPageEnvelopeSchema>,
    ContractResponse<'listMailboxAgentGrants', 200>
  >
>();
assertContract<
  Accepts<z.input<typeof mailAgentGrantSchema>, ContractData<'issueMailboxAgentGrant', 201>>
>();
assertContract<Sends<MailAgentGrantInput, ContractRequest<'issueMailboxAgentGrant'>>>();
assertContract<
  Accepts<
    z.input<typeof mailAgentActivitySchema>,
    ContractData<'listMailboxAgentGrantActivity', 200>
  >
>();
assertContract<
  Accepts<
    z.input<typeof mailAgentDraftPageEnvelopeSchema>,
    ContractResponse<'listMailboxAgentDrafts', 200>
  >
>();
