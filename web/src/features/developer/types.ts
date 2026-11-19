import type { infer as Infer } from 'zod/mini';

import type {
  MAIL_SEND_LANES,
  mailSendLaneUsageListSchema,
  mailSendLaneUsageSchema,
  mailSuppressionPageMetaSchema,
  mailSuppressionReasonSchema,
  mailSuppressionSchema,
  TRANSACTIONAL_LOG_EVENTS,
  TRANSACTIONAL_OUTCOMES,
  transactionalLogEntryDetailSchema,
  transactionalLogEntrySchema,
  transactionalLogPageMetaSchema,
  transactionalMessageEventSchema,
  transactionalMessageSchema,
  transactionalModeSchema,
  transactionalStatusSchema,
} from '@/features/developer/schemas';

/** The sections of the developer console, in tab order. */
export const DEVELOPER_TABS = ['start', 'templates', 'logs', 'limits', 'webhooks'] as const;
export type DeveloperTab = (typeof DEVELOPER_TABS)[number];

/** Which API keys the console shows: live keys deliver, test keys only render and record. */
export const DEVELOPER_MODES = ['live', 'test'] as const;
export type DeveloperMode = (typeof DEVELOPER_MODES)[number];

export type TransactionalMode = Infer<typeof transactionalModeSchema>;
export type TransactionalStatus = Infer<typeof transactionalStatusSchema>;
export type TransactionalMessageEvent = Infer<typeof transactionalMessageEventSchema>;
export type TransactionalLogEntry = Infer<typeof transactionalLogEntrySchema>;
export type TransactionalLogEntryDetail = Infer<typeof transactionalLogEntryDetailSchema>;
export type TransactionalMessage = Infer<typeof transactionalMessageSchema>;
export type TransactionalLogPageMeta = Infer<typeof transactionalLogPageMetaSchema>;
export type TransactionalLogEvent = (typeof TRANSACTIONAL_LOG_EVENTS)[number];
export type TransactionalOutcome = (typeof TRANSACTIONAL_OUTCOMES)[number];

/** One keyset page of the send log, newest first. */
export interface TransactionalLogPage {
  items: TransactionalLogEntry[];
  meta: TransactionalLogPageMeta;
}

/**
 * Filters of the send log. `since` and `until` are RFC 3339 instants; without them the API shows
 * the last 7 days. `outcome` intersects with `status` and `lastEvent`.
 */
export interface TransactionalLogFilters {
  mode: TransactionalMode;
  status?: TransactionalStatus;
  lastEvent?: TransactionalLogEvent;
  outcome?: TransactionalOutcome;
  template?: string;
  to?: string;
  since?: string;
  until?: string;
}

/** A console test send: the recipient is always the caller's own account address. */
export interface TransactionalTestSendInput {
  template: string;
  mode: TransactionalMode;
  variables?: Record<string, string>;
}

export type MailSendLane = (typeof MAIL_SEND_LANES)[number];
export type MailSendLaneUsage = Infer<typeof mailSendLaneUsageSchema>;
/** How full each send lane is over the 24 hours before `computed_at`; owners and admins only. */
export type MailSendLaneUsageList = Infer<typeof mailSendLaneUsageListSchema>;
export type MailSuppressionReason = Infer<typeof mailSuppressionReasonSchema>;
export type MailSuppression = Infer<typeof mailSuppressionSchema>;
export type MailSuppressionPageMeta = Infer<typeof mailSuppressionPageMetaSchema>;

/** One keyset page of the suppression list, newest first. */
export interface MailSuppressionPage {
  items: MailSuppression[];
  meta: MailSuppressionPageMeta;
}

/** Filters of the suppression list; `email` is an exact address, compared lowercased. */
export interface MailSuppressionFilters {
  email?: string;
  reason?: MailSuppressionReason;
}
