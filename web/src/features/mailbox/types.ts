import type { infer as Infer } from 'zod/mini';

import type {
  createMailboxSchema,
  inboxCountsSchema,
  inboxTriageCountsSchema,
  mailTriageCategorySchema,
  mailTriageSchema,
  inboxItemSchema,
  inboxPageEnvelopeSchema,
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
  mailDomainCapabilitySchema,
  mailDomainInputSchema,
  mailDomainPreviewInputSchema,
  mailDomainPreviewSchema,
  mailDomainRecordSchema,
  mailDomainSchema,
  mailDnsLockedReasonSchema,
  mailDnsRecordInputSchema,
  mailDnsRecordPatchSchema,
  mailDnsRecordSchema,
  mailDnsRecordTypeSchema,
  mailZoneSchema,
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
  mailTemplateKindSchema,
  mailTemplateSchema,
  mailTemplateSummarySchema,
  mailTemplateTestInputSchema,
  mailFolderInputSchema,
  mailFolderSchema,
  mailLabelColorSchema,
  mailLabelInputSchema,
  mailLabelSchema,
  mailMessageCountsSchema,
  mailMessageUpdatesResultSchema,
  mailPlacementSchema,
  mailRoleInputSchema,
  mailRoleSchema,
  mailThreadSummarySchema,
  mailTrashPurgeSchema,
  updateMailMessagesSchema,
} from './schemas';

export type MailboxDomain = Infer<typeof mailboxDomainSchema>;
export type MailDomain = Infer<typeof mailDomainSchema>;
export type MailDomainRecord = Infer<typeof mailDomainRecordSchema>;
export type MailDomainInput = Infer<typeof mailDomainInputSchema>;
export type MailDomainCapability = Infer<typeof mailDomainCapabilitySchema>;
export type MailDomainPreviewInput = Infer<typeof mailDomainPreviewInputSchema>;
export type MailDomainPreview = Infer<typeof mailDomainPreviewSchema>;
export type MailZone = Infer<typeof mailZoneSchema>;
export type MailDnsRecordType = Infer<typeof mailDnsRecordTypeSchema>;
export type MailDnsLockedReason = Infer<typeof mailDnsLockedReasonSchema>;
export type MailDnsRecord = Infer<typeof mailDnsRecordSchema>;
export type MailDnsRecordInput = Infer<typeof mailDnsRecordInputSchema>;
export type MailDnsRecordPatch = Infer<typeof mailDnsRecordPatchSchema>;
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
export type UpdateMailMessagesInput = Infer<typeof updateMailMessagesSchema>;
export type MailPlacement = Infer<typeof mailPlacementSchema>;
export type MailLabelColor = Infer<typeof mailLabelColorSchema>;
export type MailThreadSummary = Infer<typeof mailThreadSummarySchema>;
export type MailFolder = Infer<typeof mailFolderSchema>;
export type MailFolderInput = Infer<typeof mailFolderInputSchema>;
export type MailLabel = Infer<typeof mailLabelSchema>;
export type MailLabelInput = Infer<typeof mailLabelInputSchema>;
export type MailRole = Infer<typeof mailRoleSchema>;
export type MailRoleInput = Infer<typeof mailRoleInputSchema>;
export type MailMessageCounts = Infer<typeof mailMessageCountsSchema>;
export type MailMessageUpdatesResult = Infer<typeof mailMessageUpdatesResultSchema>;
export type MailTrashPurge = Infer<typeof mailTrashPurgeSchema>;

/**
 * Filters of a mailbox listing, combined with AND; an absent field does not narrow it. Without
 * `placement` every placement except the trash is listed. `group: 'thread'` is for placement views
 * only; see `contracts/MAILBOXES.md`.
 */
export interface MailMessageFilters {
  direction?: MailDirection;
  placement?: MailPlacement;
  folderId?: number;
  labelId?: number;
  starred?: boolean;
  unread?: boolean;
  hasAttachments?: boolean;
  q?: string;
  group?: 'thread';
}
export type MailTemplateKind = Infer<typeof mailTemplateKindSchema>;
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

export type InboxView = 'all' | 'today' | 'pending' | 'sent';

/**
 * Filters of the unified inbox, combined with AND; an absent field does not narrow it. `roleId`
 * `'none'` keeps mailboxes without a role. `q` is only for `all`, and `group` only for `all` and
 * `sent` without `unread` or `q`; see `contracts/MAILBOXES.md`. `tz` is the IANA zone of today.
 */
export interface InboxFilters {
  view: InboxView;
  roleId?: number | 'none';
  mailboxId?: number;
  unread?: boolean;
  q?: string;
  group?: 'thread';
  /** AI triage categories; only with `all` or `today`, never with `group` or `q`. */
  triage?: MailTriageCategory[];
  tz: string;
}

export type InboxItem = Infer<typeof inboxItemSchema>;
export type InboxCounts = Infer<typeof inboxCountsSchema>;
export type MailTriageCategory = Infer<typeof mailTriageCategorySchema>;
export type MailTriage = Infer<typeof mailTriageSchema>;
export type InboxTriageCounts = Infer<typeof inboxTriageCountsSchema>;

export interface InboxPage {
  items: InboxItem[];
  meta: Infer<typeof inboxPageEnvelopeSchema>['meta'];
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
