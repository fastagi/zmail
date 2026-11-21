'use client';

import { createContext, useContext } from 'react';

import { useDateTimeFormat } from '@/features/mailbox/components/mail-shared';

/** The zone the surrounding mail list shows times in; undefined is the browser's zone. */
export const MailTimeZoneContext = createContext<string | undefined>(undefined);

/**
 * Date and time of mail in the zone of the list around it, so a message's row and its reader show
 * one time. The unified inbox lists mail in the user's `localization.timezone`, else the browser's.
 */
export function useMailDateTimeFormat(): Intl.DateTimeFormat {
  return useDateTimeFormat(useContext(MailTimeZoneContext));
}
