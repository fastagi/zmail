'use client';

import { Sparkles } from 'lucide-react';

import { INBOX_TRIAGE_TABS, type InboxTriageTab } from '@/features/mailbox/triage-tabs';
import type { InboxTriageCounts } from '@/features/mailbox/types';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/**
 * The hint and the triage tabs over the unified inbox, shown only while the organization's AI
 * triage is on (its counts are not null). Each tab shows the caller's unread mail in it; `null`
 * is every message.
 */
export function InboxTriageTabs({
  counts,
  active,
  onChange,
}: {
  counts: InboxTriageCounts;
  active: InboxTriageTab | null;
  onChange: (tab: InboxTriageTab | null) => void;
}) {
  const t = useT('mailbox');
  const unread: Record<InboxTriageTab, number> = {
    reply: counts.reply + counts.review,
    deadline: counts.deadline,
    fyi: counts.fyi,
  };
  const tab = (value: InboxTriageTab | null, label: string, count?: number) => (
    <button
      key={value ?? 'all'}
      type="button"
      role="tab"
      aria-selected={active === value}
      className={cn(
        'shrink-0 rounded-full px-2.5 py-1 text-xs transition-colors focus-ring motion-reduce:transition-none',
        active === value
          ? 'bg-row-selected font-semibold text-text-main'
          : 'text-text-muted hover:text-text-main'
      )}
      onClick={() => onChange(value)}
    >
      {label}
      {count ? (
        <>
          {' '}
          <span className="font-normal text-text-faint tabular-nums">{count}</span>
        </>
      ) : null}
    </button>
  );

  return (
    <div className="shrink-0 px-4 pb-2 sm:px-6" data-inbox-triage="">
      <p className="flex items-center gap-1.5 text-xs text-text-muted">
        <Sparkles className="size-3.5 shrink-0" aria-hidden="true" />
        {t('triage.hint')}
      </p>
      <div
        role="tablist"
        aria-label={t('triage.tabs.label')}
        className="mt-2 flex gap-1 overflow-x-auto"
      >
        {tab(null, t('triage.tabs.all'))}
        {INBOX_TRIAGE_TABS.map(value => tab(value, t(`triage.tabs.${value}`), unread[value]))}
      </div>
    </div>
  );
}
