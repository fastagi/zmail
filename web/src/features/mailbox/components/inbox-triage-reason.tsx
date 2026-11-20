'use client';

import type { MailTriage } from '@/features/mailbox/types';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/** A row's triage line under the subject: the category, then why, or that a reader chose it. */
export function TriageReasonLine({ triage }: { triage: MailTriage }) {
  const t = useT('mailbox');
  const why = triage.source === 'manual' ? t('triage.manual') : triage.reason;
  return (
    <span className="mt-0.5 flex min-w-0 items-center gap-1.5 text-xs" data-triage-reason="">
      <span
        className={cn(
          'shrink-0 rounded-full px-1.5 font-medium',
          triage.category === 'review'
            ? 'bg-row-selected text-text-subtle'
            : 'bg-brand/10 text-text-main'
        )}
      >
        {t(`triage.categories.${triage.category}`)}
      </span>
      {why ? <span className="min-w-0 truncate text-text-muted">{why}</span> : null}
    </span>
  );
}
