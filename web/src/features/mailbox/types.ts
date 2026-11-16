import type { infer as Infer } from 'zod/mini';

import type {
  createMailboxSchema,
  mailDayStatsSchema,
  mailLogEntrySchema,
  mailLogPageEnvelopeSchema,
  mailStatusSchema,
  mailDirectionSchema,
  mailAttachmentSchema,
  mailEventSchema,
  mailEventSummarySchema,
  mailEventTypeSchema,
  mailMessagePageEnvelopeSchema,
  mailMessageSchema,
  mailMessageSummarySchema,
  mailboxDomainSchema,
  mailboxSchema,
  sendMailMessageSchema,
  updateMailMessageSchema,
  mailTemplateInputSchema,
  mailTemplateSchema,
  mailTemplateSummarySchema,
  mailTemplateTestInputSchema,
} from './schemas';

export type MailboxDomain = Infer<typeof mailboxDomainSchema>;
export type Mailbox = Infer<typeof mailboxSchema>;
export type MailDirection = Infer<typeof mailDirectionSchema>;
export type MailStatus = Infer<typeof mailStatusSchema>;
export type MailEventType = Infer<typeof mailEventTypeSchema>;
export type MailEventSummary = Infer<typeof mailEventSummarySchema>;
export type MailEvent = Infer<typeof mailEventSchema>;
export type MailAttachment = Infer<typeof mailAttachmentSchema>;
export type MailMessageSummary = Infer<typeof mailMessageSummarySchema>;
export type MailMessage = Infer<typeof mailMessageSchema>;
export type MailLogEntry = Infer<typeof mailLogEntrySchema>;
export type MailDayStats = Infer<typeof mailDayStatsSchema>;
export type CreateMailboxInput = Infer<typeof createMailboxSchema>;
export type SendMailMessageInput = Infer<typeof sendMailMessageSchema>;
export type UpdateMailMessageInput = Infer<typeof updateMailMessageSchema>;
export type MailTemplateSummary = Infer<typeof mailTemplateSummarySchema>;
export type MailTemplate = Infer<typeof mailTemplateSchema>;
export type MailTemplateInput = Infer<typeof mailTemplateInputSchema>;
export type MailTemplateTestInput = Infer<typeof mailTemplateTestInputSchema>;

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
