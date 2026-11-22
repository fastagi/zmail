import { mailSignatureSchema, type MailSignature } from '@/features/mailbox/signature-schemas';
import { ClientErrorCode } from '@/http/codes';
import request, { ApiError } from '@/http/request';

/**
 * Browser service for signatures in `contracts/MAILBOXES.md` (section Signatures). A signature
 * belongs to the caller; the API decides whether they may send from the mailbox.
 */
export const signatureService = {
  /** The caller's signature for a mailbox; an empty body when none is saved. */
  async signature(organizationId: number, mailboxId: number): Promise<MailSignature> {
    validId(mailboxId);
    const parsed = mailSignatureSchema.safeParse(
      await request.get<unknown>(`/mailboxes/${mailboxId}/signature`, {
        headers: organizationHeaders(organizationId),
      })
    );
    if (!parsed.success || parsed.data.mailbox_id !== mailboxId) throw invalidResponse();
    return parsed.data;
  },

  /** Replaces the caller's signature; an empty body removes it. */
  async saveSignature(
    organizationId: number,
    mailboxId: number,
    body: string
  ): Promise<MailSignature> {
    validId(mailboxId);
    const parsed = mailSignatureSchema.safeParse(
      await request.put<unknown, { body: string }>(
        `/mailboxes/${mailboxId}/signature`,
        { body },
        { headers: organizationHeaders(organizationId) }
      )
    );
    if (!parsed.success || parsed.data.mailbox_id !== mailboxId) throw invalidResponse();
    return parsed.data;
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
    'Mail signature service returned an invalid response',
    ClientErrorCode.INVALID_RESPONSE
  );
}
