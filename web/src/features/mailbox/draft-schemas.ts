import { array, refine, strictObject, string, type infer as Infer } from 'zod/mini';

import { safeIdSchema, timestampSchema } from '@/features/mailbox/schemas';

/** Limits from `contracts/MAILBOXES.md`, section Drafts, in characters. */
export const MAIL_DRAFT_LIMITS = {
  to: 4000,
  cc: 4000,
  subject: 998,
  body: 100_000,
  in_reply_to: 998,
} as const;

/** How many drafts one member keeps in an organization. */
export const MAIL_DRAFTS_PER_MEMBER = 200;

const bounded = (max: number) =>
  string().check(refine((value: string) => [...value].length <= max));

/** One of the caller's saved, unsent messages. */
export const mailDraftSchema = strictObject({
  id: safeIdSchema,
  mailbox_id: safeIdSchema,
  to: bounded(MAIL_DRAFT_LIMITS.to),
  cc: bounded(MAIL_DRAFT_LIMITS.cc),
  subject: bounded(MAIL_DRAFT_LIMITS.subject),
  body: bounded(MAIL_DRAFT_LIMITS.body),
  in_reply_to: bounded(MAIL_DRAFT_LIMITS.in_reply_to),
  created_at: timestampSchema,
  updated_at: timestampSchema,
});

export const mailDraftListSchema = array(mailDraftSchema).check(
  refine(list => list.length <= MAIL_DRAFTS_PER_MEMBER)
);

export type MailDraft = Infer<typeof mailDraftSchema>;

/** What a draft holds; saving replaces all of it. */
export interface MailDraftInput {
  mailbox_id: number;
  to: string;
  cc: string;
  subject: string;
  body: string;
  in_reply_to: string;
}
