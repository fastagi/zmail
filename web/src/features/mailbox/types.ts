import type { infer as Infer } from 'zod/mini';

import type {
  createMailboxSchema,
  mailDayStatsSchema,
  mailLogEntrySchema,
  mailLogPageEnvelopeSchema,
  mailStatusSchema,
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
export type MailStatus = Infer<typeof mailStatusSchema>;
export type MailMessageSummary = Infer<typeof mailMessageSummarySchema>;
export type MailMessage = Infer<typeof mailMessageSchema>;
export type MailLogEntry = Infer<typeof mailLogEntrySchema>;
export type MailDayStats = Infer<typeof mailDayStatsSchema>;
export type CreateMailboxInput = Infer<typeof createMailboxSchema>;
export type SendMailMessageInput = Infer<typeof sendMailMessageSchema>;
export type UpdateMailMessageInput = Infer<typeof updateMailMessageSchema>;

type MailMessagePageEnvelope = Infer<typeof mailMessagePageEnvelopeSchema>;

export interface MailMessagePage {
  items: MailMessageSummary[];
  meta: MailMessagePageEnvelope['meta'];
  links: MailMessagePageEnvelope['links'];
}

type MailLogPageEnvelope = Infer<typeof mailLogPageEnvelopeSchema>;

export interface MailLogPage {
  items: MailLogEntry[];
  meta: MailLogPageEnvelope['meta'];
  links: MailLogPageEnvelope['links'];
}

/** Filters of the log; an absent field does not narrow the listing. */
export interface MailLogFilters {
  direction?: MailDirection;
  status?: MailStatus;
  mailboxId?: number;
  q?: string;
}
