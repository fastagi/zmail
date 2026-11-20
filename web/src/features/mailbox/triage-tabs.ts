import type { MailTriageCategory } from '@/features/mailbox/types';

/** The triage tabs of the unified inbox; `review` rides with `reply`, marked on its rows. */
export const INBOX_TRIAGE_TABS = ['reply', 'deadline', 'fyi'] as const;
export type InboxTriageTab = (typeof INBOX_TRIAGE_TABS)[number];

/** The categories a tab lists. */
export function triageTabCategories(tab: InboxTriageTab): MailTriageCategory[] {
  return tab === 'reply' ? ['reply', 'review'] : [tab];
}
