import {
  array,
  boolean,
  literal,
  maxLength,
  nullable,
  string,
  strictObject,
  union,
  type infer as Infer,
} from 'zod/mini';

import {
  pageLinksSchema,
  pageMetaSchema,
  safeIdSchema,
  timestampSchema,
} from '@/features/mailbox/schemas';

/** Limits from `contracts/MAILBOXES.md`, section Agent Grants. */
export const MAIL_AGENT_GRANT_MAX_PER_MAILBOX = 10;
export const MAIL_AGENT_GRANT_MAX_DAYS = 90;
export const MAIL_AGENT_GRANT_EXPIRY_DAYS = [7, 30, 90] as const;
export const MAIL_AGENT_GRANT_DEFAULT_DAYS = 30;
export const MAIL_AGENT_FILTER_MAX_ENTRIES = 20;
/** The longest sender entry: an address (320) or a domain (253). */
export const MAIL_AGENT_SENDER_MAX_LENGTH = 320;
/** The API's largest page; every page of grants is read, at most this many pages. */
export const MAIL_AGENT_GRANTS_PAGE_SIZE = 100;
export const MAIL_AGENT_GRANTS_MAX_PAGES = 20;
export const MAIL_AGENT_DRAFTS_PAGE_SIZE = 20;
/** The activity route answers the newest 100 rows of a grant. */
export const MAIL_AGENT_ACTIVITY_MAX_ROWS = 100;

export const mailAgentCapabilitySchema = union([literal('read'), literal('draft')]);
export const mailAgentGrantStatusSchema = union([
  literal('active'),
  literal('expired'),
  literal('revoked'),
]);
export const mailAgentRevokeReasonSchema = union([
  literal('revoked'),
  literal('left_organization'),
  literal('handed_over'),
  literal('member_removed'),
  literal('connection_revoked'),
]);

export const mailAgentGrantSchema = strictObject({
  id: safeIdSchema,
  mailbox_id: safeIdSchema,
  api_key_id: safeIdSchema,
  api_key_name: string(),
  api_key_prefix: string(),
  key_user_id: safeIdSchema,
  granted_by: safeIdSchema,
  capabilities: array(mailAgentCapabilitySchema).check(maxLength(2)),
  reveal_secrets: boolean(),
  filters: strictObject({
    senders: array(string()).check(maxLength(MAIL_AGENT_FILTER_MAX_ENTRIES)),
    label_ids: array(safeIdSchema).check(maxLength(MAIL_AGENT_FILTER_MAX_ENTRIES)),
  }),
  status: mailAgentGrantStatusSchema,
  expires_at: timestampSchema,
  revoked_at: nullable(timestampSchema),
  revoke_reason: nullable(mailAgentRevokeReasonSchema),
  created_at: timestampSchema,
});

export const mailAgentGrantPageEnvelopeSchema = strictObject({
  code: literal(0),
  message: string(),
  data: array(mailAgentGrantSchema),
  meta: pageMetaSchema,
  links: pageLinksSchema,
});

export const mailAgentAuditEntrySchema = strictObject({
  id: safeIdSchema,
  grant_id: safeIdSchema,
  api_key_id: safeIdSchema,
  message_id: safeIdSchema,
  action: union([literal('read'), literal('read_revealed'), literal('draft')]),
  created_at: timestampSchema,
});

export const mailAgentActivitySchema = array(mailAgentAuditEntrySchema).check(
  maxLength(MAIL_AGENT_ACTIVITY_MAX_ROWS)
);

export const mailAgentDraftSchema = strictObject({
  id: safeIdSchema,
  mailbox_id: safeIdSchema,
  grant_id: safeIdSchema,
  message_id: safeIdSchema,
  api_key_id: safeIdSchema,
  text: string(),
  html: string(),
  status: literal('pending'),
  created_at: timestampSchema,
  updated_at: timestampSchema,
});

export const mailAgentDraftPageEnvelopeSchema = strictObject({
  code: literal(0),
  message: string(),
  data: array(mailAgentDraftSchema),
  meta: pageMetaSchema,
  links: pageLinksSchema,
});

export type MailAgentCapability = Infer<typeof mailAgentCapabilitySchema>;
export type MailAgentGrant = Infer<typeof mailAgentGrantSchema>;
export type MailAgentGrantStatus = Infer<typeof mailAgentGrantStatusSchema>;
export type MailAgentRevokeReason = Infer<typeof mailAgentRevokeReasonSchema>;
export type MailAgentAuditEntry = Infer<typeof mailAgentAuditEntrySchema>;
export type MailAgentDraft = Infer<typeof mailAgentDraftSchema>;

export interface MailAgentGrantPage {
  items: MailAgentGrant[];
  meta: Infer<typeof pageMetaSchema>;
}

/** Every grant read; `complete` is false when more pages exist than were read. */
export interface MailAgentGrantList {
  items: MailAgentGrant[];
  total: number;
  complete: boolean;
}

export interface MailAgentDraftPage {
  items: MailAgentDraft[];
  meta: Infer<typeof pageMetaSchema>;
}

/** The body of `POST /v1/mailboxes/:mailbox_id/agent-grants`. */
export interface MailAgentGrantInput {
  api_key_id: number;
  capabilities: MailAgentCapability[];
  reveal_secrets: boolean;
  filters: { senders: string[]; label_ids: number[] };
  expires_at: string;
}
