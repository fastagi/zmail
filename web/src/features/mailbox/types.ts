import type { infer as Infer } from 'zod/mini';

import type {
  createMailboxSchema,
  mailDirectionSchema,
  mailMessagePageEnvelopeSchema,
  mailMessageSchema,
  mailMessageSummarySchema,
  mailboxDomainSchema,
  mailboxSchema,
  sendMailMessageSchema,
  updateMailMessageSchema,
} from './schemas';

export type MailboxDomain = Infer<typeof mailboxDomainSchema>;
export type Mailbox = Infer<typeof mailboxSchema>;
export type MailDirection = Infer<typeof mailDirectionSchema>;
export type MailMessageSummary = Infer<typeof mailMessageSummarySchema>;
export type MailMessage = Infer<typeof mailMessageSchema>;
export type CreateMailboxInput = Infer<typeof createMailboxSchema>;
export type SendMailMessageInput = Infer<typeof sendMailMessageSchema>;
export type UpdateMailMessageInput = Infer<typeof updateMailMessageSchema>;

type MailMessagePageEnvelope = Infer<typeof mailMessagePageEnvelopeSchema>;

export interface MailMessagePage {
  items: MailMessageSummary[];
  meta: MailMessagePageEnvelope['meta'];
  links: MailMessagePageEnvelope['links'];
}
