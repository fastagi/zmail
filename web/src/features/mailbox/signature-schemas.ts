import { nullable, refine, strictObject, string, type infer as Infer } from 'zod/mini';

import { safeIdSchema, timestampSchema } from '@/features/mailbox/schemas';

/** Limit from `contracts/MAILBOXES.md`, section Signatures. */
export const MAIL_SIGNATURE_MAX_LENGTH = 2000;

/** The caller's own signature for a mailbox; `updated_at` is null until one is saved. */
export const mailSignatureSchema = strictObject({
  mailbox_id: safeIdSchema,
  // The limit counts characters as the API does, not UTF-16 units: an emoji is one.
  body: string().check(refine((body: string) => [...body].length <= MAIL_SIGNATURE_MAX_LENGTH)),
  updated_at: nullable(timestampSchema),
});

export type MailSignature = Infer<typeof mailSignatureSchema>;

/** The line that sets a signature apart from what is written above it. */
export const MAIL_SIGNATURE_SEPARATOR = '\n\n-- \n';

/** Normalizes a signature as the API stores it: `\n` line breaks, no trailing white space. */
export function normalizeMailSignature(body: string): string {
  return body.replace(/\r\n?/g, '\n').trimEnd();
}

/** The block a composer appends for a signature, or nothing without one. */
export function mailSignatureBlock(body: string | undefined): string {
  const normalized = normalizeMailSignature(body ?? '');
  return normalized ? `${MAIL_SIGNATURE_SEPARATOR}${normalized}` : '';
}
