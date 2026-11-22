import {
  mailDraftListSchema,
  mailDraftSchema,
  type MailDraft,
  type MailDraftInput,
} from '@/features/mailbox/draft-schemas';
import { ClientErrorCode } from '@/http/codes';
import request, { ApiError } from '@/http/request';

/**
 * Browser service for drafts in `contracts/MAILBOXES.md` (section Drafts). A draft belongs to its
 * writer; the API decides whether they may send from its mailbox.
 */
export const draftService = {
  /** The caller's drafts in the organization, the latest change first. */
  async drafts(organizationId: number): Promise<MailDraft[]> {
    const parsed = mailDraftListSchema.safeParse(
      await request.get<unknown>('/mail-drafts', { headers: organizationHeaders(organizationId) })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  /** Saves a new draft, or replaces the draft `id`. */
  async save(
    organizationId: number,
    input: MailDraftInput,
    id: number | null = null
  ): Promise<MailDraft> {
    validId(input.mailbox_id);
    const headers = organizationHeaders(organizationId);
    let raw: unknown;
    if (id === null) {
      raw = await request.post<unknown, MailDraftInput>('/mail-drafts', input, { headers });
    } else {
      validId(id);
      raw = await request.put<unknown, MailDraftInput>(`/mail-drafts/${id}`, input, { headers });
    }
    const parsed = mailDraftSchema.safeParse(raw);
    if (!parsed.success || (id !== null && parsed.data.id !== id)) throw invalidResponse();
    return parsed.data;
  },

  /** Deletes one of the caller's drafts. */
  async remove(organizationId: number, id: number): Promise<void> {
    validId(id);
    await request.delete<unknown>(`/mail-drafts/${id}`, {
      headers: organizationHeaders(organizationId),
    });
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
    'Mail draft service returned an invalid response',
    ClientErrorCode.INVALID_RESPONSE
  );
}
