import { z } from 'zod';
import { pageSchema } from '@/http/pagination';

const count = z.number().int().min(0);
const isoDay = z.string().regex(/^\d{4}-\d{2}-\d{2}$/);

export const outboundStatuses = ['sent', 'failed', 'queued'] as const;
export type OutboundStatus = (typeof outboundStatuses)[number];

export const mailMetricsSchema = z.object({
  date: isoDay,
  organizations: count,
  members: count,
  disabled_members: count,
  mailboxes: count,
  day: z.object({
    sent: count,
    failed: count,
    queued: count,
    delivered: count,
    bounced: count,
    complained: count,
  }),
  trend: z.array(z.object({ date: isoDay, sent: count, failed: count, queued: count })).max(31),
});

export const mailOrganizationSchema = z.object({
  id: z.number().int().positive(),
  name: z.string(),
  members: count,
  mailboxes: count,
  sent: count,
  failed: count,
  queued: count,
});

export const outboundMessageSchema = z.object({
  id: z.number().int().positive(),
  organization_id: z.number().int().positive(),
  organization_name: z.string(),
  mailbox_id: z.number().int().positive(),
  mailbox_address: z.string(),
  from_address: z.string(),
  subject: z.string(),
  status: z.enum(outboundStatuses),
  failure_code: z.string(),
  last_event: z.string(),
  created_at: z.string(),
  send_attempted_at: z.string().nullable(),
  // Set only for organization broadcast messages, the only ones whose body may be read.
  broadcast_id: z.number().int().positive().nullable(),
});

/** Longest reason the API accepts for reading a body, in characters. */
export const contentReasonMaxLength = 500;

export const broadcastContentSchema = z.object({
  access_id: z.number().int().positive(),
  organization_id: z.number().int().positive(),
  message_id: z.number().int().positive(),
  broadcast_id: z.number().int().positive(),
  subject: z.string(),
  from_address: z.string(),
  created_at: z.string(),
  text: z.string(),
  text_source: z.enum(['text', 'html']),
});

export const contentAccessSchema = z.object({
  id: z.number().int().positive(),
  viewer_user_id: z.number().int().positive(),
  organization_id: z.number().int().positive(),
  organization_name: z.string(),
  message_id: z.number().int().positive(),
  reason: z.string(),
  accessed_at: z.string(),
});

export const mailOrganizationPageSchema = pageSchema(mailOrganizationSchema);
export const outboundMessagePageSchema = pageSchema(outboundMessageSchema);
export const contentAccessPageSchema = pageSchema(contentAccessSchema);

export type MailMetrics = z.infer<typeof mailMetricsSchema>;
export type MailOrganization = z.infer<typeof mailOrganizationSchema>;
export type MailOrganizationPage = z.infer<typeof mailOrganizationPageSchema>;
export type OutboundMessage = z.infer<typeof outboundMessageSchema>;
export type OutboundMessagePage = z.infer<typeof outboundMessagePageSchema>;
export type BroadcastContent = z.infer<typeof broadcastContentSchema>;
export type ContentAccess = z.infer<typeof contentAccessSchema>;
export type ContentAccessPage = z.infer<typeof contentAccessPageSchema>;

/** Every Operations view carries the selected UTC day; absent means today. */
export interface MailDaySearch {
  date?: string | undefined;
}

export interface MailOrganizationSearch extends MailDaySearch {
  q?: string | undefined;
  page?: number | undefined;
}

export interface OutboundMessageSearch extends MailDaySearch {
  organization_id?: number | undefined;
  mailbox_id?: number | undefined;
  status?: OutboundStatus | undefined;
  page?: number | undefined;
}

/** Filters of the content access records; days are UTC and `to` is inclusive. */
export interface ContentAccessSearch {
  organization_id?: number | undefined;
  viewer_user_id?: number | undefined;
  from?: string | undefined;
  to?: string | undefined;
  page?: number | undefined;
}

/** The instant after the last moment of a UTC day. */
export function nextUtcDayStart(day: string): string {
  const next = new Date(`${day}T00:00:00Z`);
  next.setUTCDate(next.getUTCDate() + 1);
  return next.toISOString().replace('.000Z', 'Z');
}

/** Today's calendar day in UTC, the day the API uses by default. */
export function todayUtc(now: Date = new Date()): string {
  return now.toISOString().slice(0, 10);
}

/** Accepts a real `YYYY-MM-DD` calendar day that is not after today (UTC). */
export function isSelectableDay(value: string, now: Date = new Date()): boolean {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) {
    return false;
  }
  const parsed = new Date(`${value}T00:00:00Z`);
  return (
    !Number.isNaN(parsed.getTime()) &&
    parsed.toISOString().slice(0, 10) === value &&
    value <= todayUtc(now)
  );
}
