import type { z } from 'zod/mini';

import {
  MAILBOX_ACCESS_EVENTS_MAX_PAGE_SIZE,
  MAILBOX_LIST_SCOPES,
  mailboxAccessEventPageEnvelopeSchema,
  mailboxListSchema,
  mailboxMemberListSchema,
  mailboxMemberSchema,
  mailboxSchema,
  mailboxSealedDeletionSchema,
  mailRoleListSchema,
  mailRoleSchema,
} from '@/features/mailbox/schemas';
import type {
  CreateMailboxInput,
  Mailbox,
  MailboxAccessEventPage,
  MailboxListScope,
  MailboxMember,
  MailboxPermission,
  MailboxSealedDeletion,
  MailRole,
  MailRoleInput,
} from '@/features/mailbox/types';
import {
  assertContract,
  type Accepts,
  type ContractData,
  type ContractRequest,
  type ContractResponse,
  type Sends,
} from '@/http/contract';
import request from '@/http/request';

import {
  invalidResponse,
  organizationHeaders,
  parseOrganizeItem,
  validId,
} from './mailbox-service-shared';

/** Mailbox lifecycle, members, access history, handover, roles, and webhook settings. */
export const mailboxManagementService = {
  /** Every mailbox the caller has any access to; `usable` keeps only the caller's own. */
  async list(organizationId: number, scope?: MailboxListScope): Promise<Mailbox[]> {
    const headers = organizationHeaders(organizationId);
    if (scope !== undefined && !(MAILBOX_LIST_SCOPES as readonly string[]).includes(scope)) {
      throw invalidResponse();
    }
    const parsed = mailboxListSchema.safeParse(
      await request.get<unknown>(
        '/mailboxes',
        scope === undefined ? { headers } : { headers, params: { scope } }
      )
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  async create(organizationId: number, input: CreateMailboxInput): Promise<Mailbox> {
    const value = await request.post<unknown, CreateMailboxInput>('/mailboxes', input, {
      headers: organizationHeaders(organizationId),
    });
    const parsed = mailboxSchema.safeParse(value);
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  async remove(organizationId: number, mailboxId: number): Promise<void> {
    validId(mailboxId);
    await request.delete<void>(`/mailboxes/${mailboxId}`, {
      headers: organizationHeaders(organizationId),
    });
  },

  /** Members of a shared mailbox, ordered by user id; needs `manage_members`. */
  async members(organizationId: number, mailboxId: number): Promise<MailboxMember[]> {
    const headers = organizationHeaders(organizationId);
    validId(mailboxId);
    const parsed = mailboxMemberListSchema.safeParse(
      await request.get<unknown>(`/mailboxes/${mailboxId}/members`, { headers })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  /** Adds the user or changes their permission. */
  async setMember(
    organizationId: number,
    mailboxId: number,
    userId: number,
    permission: MailboxPermission
  ): Promise<MailboxMember> {
    const headers = organizationHeaders(organizationId);
    validId(mailboxId);
    validId(userId);
    const parsed = mailboxMemberSchema.safeParse(
      await request.put<unknown, { permission: MailboxPermission }>(
        `/mailboxes/${mailboxId}/members/${userId}`,
        { permission },
        { headers }
      )
    );
    if (!parsed.success || parsed.data.user_id !== userId) throw invalidResponse();
    return parsed.data;
  },

  async removeMember(organizationId: number, mailboxId: number, userId: number): Promise<void> {
    const headers = organizationHeaders(organizationId);
    validId(mailboxId);
    validId(userId);
    await request.delete<void>(`/mailboxes/${mailboxId}/members/${userId}`, { headers });
  },

  /** One page of the access history, newest first; needs `see_history`. */
  async accessEvents(
    organizationId: number,
    mailboxId: number,
    { page = 1, perPage = 20 }: { page?: number; perPage?: number } = {}
  ): Promise<MailboxAccessEventPage> {
    const headers = organizationHeaders(organizationId);
    validId(mailboxId);
    validId(page);
    if (
      !Number.isSafeInteger(perPage) ||
      perPage < 1 ||
      perPage > MAILBOX_ACCESS_EVENTS_MAX_PAGE_SIZE
    ) {
      throw invalidResponse();
    }
    const parsed = mailboxAccessEventPageEnvelopeSchema.safeParse(
      await request.getEnvelope<unknown>(`/mailboxes/${mailboxId}/access-events`, {
        headers,
        params: { page, per_page: perPage },
      })
    );
    if (!parsed.success) throw invalidResponse();
    return { items: parsed.data.data, meta: parsed.data.meta, links: parsed.data.links };
  },

  /** Turns a personal mailbox into a shared one the successor works; answers the mailbox. */
  async handOver(
    organizationId: number,
    mailboxId: number,
    successorUserId: number
  ): Promise<Mailbox> {
    const headers = organizationHeaders(organizationId);
    validId(mailboxId);
    validId(successorUserId);
    const parsed = mailboxSchema.safeParse(
      await request.post<unknown, { successor_user_id: number }>(
        `/mailboxes/${mailboxId}/handovers`,
        { successor_user_id: successorUserId },
        { headers }
      )
    );
    if (!parsed.success || parsed.data.id !== mailboxId) throw invalidResponse();
    return parsed.data;
  },

  /** Deletes the sealed history for good; `confirmAddress` must be the mailbox address. */
  async deleteSealedHistory(
    organizationId: number,
    mailboxId: number,
    confirmAddress: string
  ): Promise<MailboxSealedDeletion> {
    const headers = organizationHeaders(organizationId);
    validId(mailboxId);
    const parsed = mailboxSealedDeletionSchema.safeParse(
      await request.delete<unknown>(`/mailboxes/${mailboxId}/sealed-messages`, {
        headers,
        data: { confirm_address: confirmAddress },
      })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  /**
   * The organization's mail roles in the API's order. Owners and admins get every role; members
   * only those holding a mailbox they can see, with only those mailboxes.
   */
  async listRoles(organizationId: number): Promise<MailRole[]> {
    const parsed = mailRoleListSchema.safeParse(
      await request.get<unknown>('/mail-roles', { headers: organizationHeaders(organizationId) })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  /** Owners and admins only. */
  async createRole(organizationId: number, input: MailRoleInput): Promise<MailRole> {
    const headers = organizationHeaders(organizationId);
    return parseOrganizeItem(
      mailRoleSchema,
      await request.post<unknown, MailRoleInput>('/mail-roles', input, { headers })
    );
  },

  /** Replaces every field of the role; an omitted field takes its default. */
  async updateRole(
    organizationId: number,
    roleId: number,
    input: MailRoleInput
  ): Promise<MailRole> {
    const headers = organizationHeaders(organizationId);
    validId(roleId);
    return parseOrganizeItem(
      mailRoleSchema,
      await request.put<unknown, MailRoleInput>(`/mail-roles/${roleId}`, input, { headers }),
      roleId
    );
  },

  /** Its mailboxes become ungrouped; no access changes. */
  async deleteRole(organizationId: number, roleId: number): Promise<void> {
    const headers = organizationHeaders(organizationId);
    validId(roleId);
    await request.delete<void>(`/mail-roles/${roleId}`, { headers });
  },

  /** Puts the mailbox in a role, or takes it out with null; needs `administer`. */
  async assignRole(
    organizationId: number,
    mailboxId: number,
    roleId: number | null
  ): Promise<Mailbox> {
    const headers = organizationHeaders(organizationId);
    validId(mailboxId);
    if (roleId !== null) validId(roleId);
    const parsed = mailboxSchema.safeParse(
      await request.put<unknown, { role_id: number | null }>(
        `/mailboxes/${mailboxId}/role-assignment`,
        { role_id: roleId },
        { headers }
      )
    );
    if (!parsed.success || parsed.data.id !== mailboxId) throw invalidResponse();
    return parsed.data;
  },

  /** Turns organization webhooks for a shared mailbox on or off; answers the mailbox. */
  async setWebhooks(organizationId: number, mailboxId: number, enabled: boolean): Promise<Mailbox> {
    const headers = organizationHeaders(organizationId);
    validId(mailboxId);
    const parsed = mailboxSchema.safeParse(
      await request.put<unknown, { enabled: boolean }>(
        `/mailboxes/${mailboxId}/webhook-setting`,
        { enabled },
        { headers }
      )
    );
    if (!parsed.success || parsed.data.id !== mailboxId) throw invalidResponse();
    return parsed.data;
  },
};

assertContract<Accepts<z.input<typeof mailboxListSchema>, ContractData<'listMailboxes', 200>>>();
assertContract<Accepts<z.input<typeof mailboxSchema>, ContractData<'createMailbox', 201>>>();
assertContract<Sends<CreateMailboxInput, ContractRequest<'createMailbox'>>>();
assertContract<
  Accepts<z.input<typeof mailboxMemberListSchema>, ContractData<'listMailboxMembers', 200>>
>();
assertContract<
  Accepts<z.input<typeof mailboxMemberSchema>, ContractData<'setMailboxMember', 200>>
>();
assertContract<Sends<{ permission: MailboxPermission }, ContractRequest<'setMailboxMember'>>>();
assertContract<
  Accepts<
    z.input<typeof mailboxAccessEventPageEnvelopeSchema>,
    ContractResponse<'listMailboxAccessEvents', 200>
  >
>();
assertContract<Accepts<z.input<typeof mailboxSchema>, ContractData<'handOverMailbox', 201>>>();
assertContract<Sends<{ successor_user_id: number }, ContractRequest<'handOverMailbox'>>>();
assertContract<
  Accepts<
    z.input<typeof mailboxSealedDeletionSchema>,
    ContractData<'deleteMailboxSealedMessages', 200>
  >
>();
assertContract<
  Sends<{ confirm_address: string }, ContractRequest<'deleteMailboxSealedMessages'>>
>();
assertContract<
  Accepts<z.input<typeof mailboxSchema>, ContractData<'setMailboxWebhookSetting', 200>>
>();
assertContract<Sends<{ enabled: boolean }, ContractRequest<'setMailboxWebhookSetting'>>>();
assertContract<Accepts<z.input<typeof mailRoleListSchema>, ContractData<'listMailRoles', 200>>>();
assertContract<Accepts<z.input<typeof mailRoleSchema>, ContractData<'createMailRole', 201>>>();
assertContract<Accepts<z.input<typeof mailRoleSchema>, ContractData<'updateMailRole', 200>>>();
assertContract<Sends<MailRoleInput, ContractRequest<'createMailRole'>>>();
assertContract<Sends<MailRoleInput, ContractRequest<'updateMailRole'>>>();
assertContract<
  Accepts<z.input<typeof mailboxSchema>, ContractData<'setMailboxRoleAssignment', 200>>
>();
assertContract<Sends<{ role_id: number | null }, ContractRequest<'setMailboxRoleAssignment'>>>();
