'use client';

import { CalendarDays, Inbox, Send, Star } from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import type { InboxCounts, InboxView, Mailbox } from '@/features/mailbox/types';
import { useT } from '@/i18n';
import { cn } from '@/utils';

const viewIcons = {
  all: Inbox,
  today: CalendarDays,
  pending: Star,
  sent: Send,
} as const satisfies Record<InboxView, unknown>;

const viewOrder: readonly InboxView[] = ['all', 'today', 'pending', 'sent'];

const rowClass =
  'flex w-full min-w-0 items-center gap-2 rounded-md px-3 py-1.5 text-left text-sm transition-colors focus-ring hover:bg-accent/70 hover:text-foreground';

/**
 * The unified inbox sidebar: the four views with the caller's counts (`all_unread` emphasized,
 * Sent without a number but with a "queued" badge while mail waits for the send limit), then the
 * readable mailboxes with their unread counts. Numbers appear once `counts` has loaded.
 */
export function InboxSidebar({
  counts,
  mailboxes,
  view,
  box = null,
  onView,
  onBox,
}: {
  counts: InboxCounts | undefined;
  mailboxes: readonly Mailbox[];
  view: InboxView;
  box?: number | null;
  onView: (view: InboxView) => void;
  onBox: (mailboxId: number) => void;
}) {
  const t = useT('mailbox');
  const views = counts?.views;
  const viewNumber: Record<InboxView, number | undefined> = {
    all: views?.all_unread,
    today: views?.today,
    pending: views?.pending,
    sent: undefined,
  };
  const unreadByMailbox = new Map(counts?.mailboxes.map(item => [item.mailbox_id, item.unread]));

  return (
    <>
      <nav aria-label={t('inbox.views.label')} className="space-y-1">
        {viewOrder.map(item => {
          const Icon = viewIcons[item];
          const name = t(`inbox.views.${item}`);
          const count = viewNumber[item];
          const queued = item === 'sent' && (views?.sent_waiting ?? 0) > 0;
          let label: string | undefined;
          if (queued) label = t('inbox.views.sentQueued', { name });
          else if (item === 'all' && count) label = t('inbox.views.unreadCount', { name, count });
          else if (count) label = t('inbox.views.count', { name, count });
          return (
            <button
              key={item}
              type="button"
              aria-label={label}
              aria-current={item === view ? 'page' : undefined}
              className={cn(
                rowClass,
                item === view ? 'bg-accent font-medium text-foreground' : 'text-muted-foreground'
              )}
              onClick={() => onView(item)}
            >
              <Icon className="size-4 shrink-0" aria-hidden="true" />
              <span className="min-w-0 flex-1 truncate">{name}</span>
              {queued ? (
                <Badge variant="outline" className="shrink-0 px-1.5 text-[0.6875rem]">
                  {t('inbox.views.queued')}
                </Badge>
              ) : null}
              {count ? (
                <span
                  aria-hidden="true"
                  className={cn(
                    'shrink-0 text-xs tabular-nums',
                    item === 'all' ? 'font-semibold text-primary' : 'text-muted-foreground'
                  )}
                >
                  {count}
                </span>
              ) : null}
            </button>
          );
        })}
      </nav>
      <nav aria-label={t('inbox.mailboxesLabel')} className="space-y-1">
        <p className="px-3 text-xs font-medium text-muted-foreground">
          {t('inbox.mailboxesLabel')}
        </p>
        {mailboxes.map(mailbox => {
          const unread = unreadByMailbox.get(mailbox.id) ?? 0;
          const selected = mailbox.id === box;
          return (
            <button
              key={mailbox.id}
              type="button"
              aria-label={
                unread > 0 ? t('views.unread', { name: mailbox.address, count: unread }) : undefined
              }
              aria-current={selected ? 'page' : undefined}
              className={cn(
                rowClass,
                selected ? 'bg-accent font-medium text-foreground' : 'text-muted-foreground'
              )}
              onClick={() => onBox(mailbox.id)}
            >
              <span className="min-w-0 flex-1 truncate">{mailbox.address}</span>
              {unread > 0 ? (
                <span aria-hidden="true" className="shrink-0 text-xs tabular-nums">
                  {unread}
                </span>
              ) : null}
            </button>
          );
        })}
      </nav>
    </>
  );
}
