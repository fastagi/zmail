import { MAIL_LOG_QUERY_MAX_LENGTH } from '@/features/mailbox/schemas';
import type { MailDirection, MailLogFilters, MailStatus } from '@/features/mailbox/types';

/** What the Emails page shows: the log filters and the page, mirrored in the URL query. */
export interface EmailLogView extends MailLogFilters {
  page: number;
}

type SearchParams = Record<string, string | string[] | undefined>;

const directions: readonly MailDirection[] = ['inbound', 'outbound'];
const statuses: readonly MailStatus[] = ['received', 'queued', 'sent', 'failed'];
const positiveIntegerPattern = /^[1-9]\d{0,8}$/u;

/** Reads the page's query string. Anything unknown or malformed is ignored, never an error. */
export function parseEmailLogView(params: SearchParams): EmailLogView {
  const direction = directions.find(value => value === single(params.direction));
  const status = statuses.find(value => value === single(params.status));
  const mailbox = single(params.mailbox);
  const page = single(params.page);
  const q = Array.from(single(params.q).trim()).slice(0, MAIL_LOG_QUERY_MAX_LENGTH).join('');
  return {
    ...(direction ? { direction } : {}),
    ...(status ? { status } : {}),
    ...(positiveIntegerPattern.test(mailbox) ? { mailboxId: Number(mailbox) } : {}),
    ...(q === '' ? {} : { q }),
    page: positiveIntegerPattern.test(page) ? Number(page) : 1,
  };
}

/**
 * The query string for a view, with its leading `?`, or '' for the unfiltered first page. The
 * Emails page passes its organization, which the query then names first as `org`.
 */
export function emailLogSearch(view: EmailLogView, organizationId?: number): string {
  const query = new URLSearchParams();
  if (organizationId !== undefined) query.set('org', String(organizationId));
  if (view.direction) query.set('direction', view.direction);
  if (view.status) query.set('status', view.status);
  if (view.mailboxId !== undefined) query.set('mailbox', String(view.mailboxId));
  if (view.q) query.set('q', view.q);
  if (view.page > 1) query.set('page', String(view.page));
  const search = query.toString();
  return search === '' ? '' : `?${search}`;
}

export function hasEmailLogFilters(view: EmailLogView): boolean {
  return Boolean(view.direction || view.status || view.mailboxId !== undefined || view.q);
}

function single(value: string | string[] | undefined): string {
  return (Array.isArray(value) ? value[0] : value) ?? '';
}
