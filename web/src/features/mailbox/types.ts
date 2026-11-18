import type { infer as Infer } from 'zod/mini';

import type {
  createMailboxSchema,
  mailboxAccessEventPageEnvelopeSchema,
  mailboxAccessEventSchema,
  mailboxAccessEventTypeSchema,
  mailboxAccessSchema,
  mailboxMemberInputSchema,
  mailboxMemberSchema,
  mailboxPermissionSchema,
  mailboxSealedDeletionSchema,
  mailboxTypeSchema,
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
  mailDomainInputSchema,
  mailDomainRecordSchema,
  mailDomainSchema,
  sendMailMessageSchema,
  updateMailMessageSchema,
  mailTemplateInputSchema,
  mailContactImportInputSchema,
  mailBroadcastInputSchema,
  mailBroadcastPageEnvelopeSchema,
  mailBroadcastRecipientPageEnvelopeSchema,
  mailBroadcastRecipientSchema,
  mailBroadcastSchema,
  mailBroadcastSendInputSchema,
  mailBroadcastStatusSchema,
  mailBroadcastStatusOnlySchema,
  mailRecipientStatusSchema,
  mailContactImportSchema,
  mailContactImportSkipReasonSchema,
  mailContactInputSchema,
  mailContactListInputSchema,
  mailContactListSchema,
  mailContactPageEnvelopeSchema,
  mailContactSchema,
  mailContactStatusSchema,
  mailTemplateSchema,
  mailTemplateSummarySchema,
  mailTemplateTestInputSchema,
} from './schemas';

export type MailboxDomain = Infer<typeof mailboxDomainSchema>;
export type MailDomain = Infer<typeof mailDomainSchema>;
export type MailDomainRecord = Infer<typeof mailDomainRecordSchema>;
export type MailDomainInput = Infer<typeof mailDomainInputSchema>;
export type Mailbox = Infer<typeof mailboxSchema>;
export type MailboxType = Infer<typeof mailboxTypeSchema>;
export type MailboxPermission = Infer<typeof mailboxPermissionSchema>;
export type MailboxAccess = Infer<typeof mailboxAccessSchema>;
export type MailboxMember = Infer<typeof mailboxMemberSchema>;
export type MailboxMemberInput = Infer<typeof mailboxMemberInputSchema>;
export type MailboxAccessEventType = Infer<typeof mailboxAccessEventTypeSchema>;
export type MailboxAccessEvent = Infer<typeof mailboxAccessEventSchema>;
export type MailboxSealedDeletion = Infer<typeof mailboxSealedDeletionSchema>;

type MailboxAccessEventPageEnvelope = Infer<typeof mailboxAccessEventPageEnvelopeSchema>;

export interface MailboxAccessEventPage {
  items: MailboxAccessEvent[];
  meta: MailboxAccessEventPageEnvelope['meta'];
  links: MailboxAccessEventPageEnvelope['links'];
}
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
export type MailContactStatus = Infer<typeof mailContactStatusSchema>;
export type MailContact = Infer<typeof mailContactSchema>;
export type MailContactList = Infer<typeof mailContactListSchema>;
export type MailContactImport = Infer<typeof mailContactImportSchema>;
export type MailContactImportSkipReason = Infer<typeof mailContactImportSkipReasonSchema>;
export type MailContactInput = Infer<typeof mailContactInputSchema>;
export type MailContactImportInput = Infer<typeof mailContactImportInputSchema>;
export type MailContactListInput = Infer<typeof mailContactListInputSchema>;

type MailContactPageEnvelope = Infer<typeof mailContactPageEnvelopeSchema>;

export interface MailContactPage {
  items: MailContact[];
  meta: MailContactPageEnvelope['meta'];
  links: MailContactPageEnvelope['links'];
}

/** Filters of the contact listing; an absent field does not narrow it. */
export interface MailContactFilters {
  q?: string;
  status?: MailContactStatus;
  listId?: number;
}

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

export type MailBroadcastStatus = Infer<typeof mailBroadcastStatusSchema>;
export type MailRecipientStatus = Infer<typeof mailRecipientStatusSchema>;
export type MailBroadcast = Infer<typeof mailBroadcastSchema>;
export type MailBroadcastStatusOnly = Infer<typeof mailBroadcastStatusOnlySchema>;
export type MailBroadcastRecipient = Infer<typeof mailBroadcastRecipientSchema>;
export type MailBroadcastInput = Infer<typeof mailBroadcastInputSchema>;
export type MailBroadcastSendInput = Infer<typeof mailBroadcastSendInputSchema>;

type MailBroadcastPageEnvelope = Infer<typeof mailBroadcastPageEnvelopeSchema>;
type MailBroadcastRecipientPageEnvelope = Infer<typeof mailBroadcastRecipientPageEnvelopeSchema>;

export interface MailBroadcastPage {
  items: MailBroadcast[];
  meta: MailBroadcastPageEnvelope['meta'];
  links: MailBroadcastPageEnvelope['links'];
}

export interface MailBroadcastRecipientPage {
  items: MailBroadcastRecipient[];
  meta: MailBroadcastRecipientPageEnvelope['meta'];
  links: MailBroadcastRecipientPageEnvelope['links'];
}
