'use client';

import { useDateTimeFormat } from '@/features/mailbox/components/mail-shared';
import { useInboxTimezone } from '@/features/mailbox/hooks/use-inbox';

/**
 * Date and time of mail in the zone the inbox lists it in (the user's `localization.timezone`,
 * else the browser's), so a message's row and its reader show one time.
 */
export function useMailDateTimeFormat(): Intl.DateTimeFormat {
  return useDateTimeFormat(useInboxTimezone().zone);
}
