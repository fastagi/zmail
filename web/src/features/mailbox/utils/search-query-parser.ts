import type { INBOX_DATE_RANGES } from '@/features/mailbox/schemas';

export type SearchDateRange = (typeof INBOX_DATE_RANGES)[number];

export interface ParsedSearchQuery {
  cleanQuery: string;
  unread?: boolean;
  attachments?: boolean;
  dateRange?: SearchDateRange;
}

/**
 * Extracts supported Gmail-style search operators (`is:unread`, `has:attachment`, `after:7d`,
 * `after:30d`) from a raw search string and returns the remaining clean query alongside the
 * matched filter flags.
 */
export function parseSearchOperators(raw: string): ParsedSearchQuery {
  const trimmed = raw.trim();
  if (trimmed === '') return { cleanQuery: '' };

  const tokens = trimmed.split(/\s+/u);
  const remaining: string[] = [];
  let unread: boolean | undefined;
  let attachments: boolean | undefined;
  let dateRange: SearchDateRange | undefined;

  for (const token of tokens) {
    const normalized = token.toLowerCase();
    if (normalized === 'is:unread') {
      unread = true;
    } else if (normalized === 'has:attachment' || normalized === 'has:attachments') {
      attachments = true;
    } else if (normalized === 'after:7d') {
      dateRange = 'week';
    } else if (normalized === 'after:30d') {
      dateRange = 'month';
    } else {
      remaining.push(token);
    }
  }

  return {
    cleanQuery: remaining.join(' '),
    ...(unread !== undefined ? { unread } : {}),
    ...(attachments !== undefined ? { attachments } : {}),
    ...(dateRange !== undefined ? { dateRange } : {}),
  };
}
