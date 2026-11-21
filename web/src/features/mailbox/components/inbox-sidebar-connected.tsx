'use client';

import { Plus, Settings2 } from 'lucide-react';

import { Button } from '@/components/ui/button';
import type { InboxSidebarExternal } from '@/features/mailbox/components/inbox-sidebar';
import type { Mailbox } from '@/features/mailbox/types';
import { providerName, statusDotClassName } from '@/features/mailbox/utils/external-mail';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/**
 * "Connected by me": the viewer's external mailboxes, each with a provider badge and a status dot
 * (green syncing, amber needs reconnecting, grey disconnected), then "Add external mailbox".
 */
export default function ConnectedMailboxes({
  external,
  mailboxes,
  unreadByMailbox,
  box,
  rail,
  onBox,
}: {
  external: InboxSidebarExternal;
  mailboxes: readonly Mailbox[];
  unreadByMailbox: Map<number, number>;
  box: number | null;
  rail: boolean;
  onBox: (mailboxId: number) => void;
}) {
  const t = useT('mailbox');
  const wide = rail ? 'hidden lg:flex' : 'flex';
  const readable = new Set(
    mailboxes.filter(mailbox => mailbox.access.view).map(mailbox => mailbox.id)
  );
  const label = t('external.sidebar.mine');
  return (
    <nav aria-label={label} className="flex flex-col gap-0.5" data-sidebar-section="connected">
      <div className={cn('items-center gap-2 pt-[18px] pr-1 pb-1.5 pl-3', wide)}>
        <p className="min-w-0 flex-1 truncate text-[11px] tracking-[0.06em] text-text-faint">
          {label}
        </p>
        {external.connections.length > 0 ? (
          <Button
            type="button"
            isIcon
            size="xs"
            variant="ghost"
            aria-label={t('external.sidebar.manage')}
            title={t('external.sidebar.manage')}
            className="text-text-faint hover:text-text-main"
            onClick={external.onManage}
          >
            <Settings2 className="size-3.5" />
          </Button>
        ) : null}
      </div>
      {rail ? <div className="h-4 lg:hidden" aria-hidden="true" /> : null}
      {external.connections.map(connection => {
        const provider = providerName(connection.provider);
        const status = t(`external.status.${connection.status}`);
        const unread = unreadByMailbox.get(connection.mailbox_id) ?? 0;
        const selectable = readable.has(connection.mailbox_id);
        const selected = selectable && connection.mailbox_id === box;
        const name =
          unread > 0
            ? t('external.sidebar.rowUnread', {
                address: connection.account_address,
                provider,
                status,
                count: unread,
              })
            : t('external.sidebar.row', { address: connection.account_address, provider, status });
        return (
          <button
            key={connection.id}
            type="button"
            aria-label={name}
            aria-current={selected ? 'page' : undefined}
            title={rail ? connection.account_address : undefined}
            disabled={!selectable}
            data-connection-status={connection.status}
            className={cn(
              'flex h-[30px] w-full min-w-0 shrink-0 items-center gap-2 rounded-[8px] pr-3 pl-3 text-left text-[12.5px] transition-colors focus-ring motion-reduce:transition-none',
              'disabled:cursor-default disabled:opacity-60',
              rail && 'justify-center px-0 lg:justify-start lg:px-3',
              selected
                ? 'bg-blue-50 font-semibold text-blue-800 dark:bg-blue-950/65 dark:text-blue-200'
                : 'text-text-subtle hover:bg-nav-hover disabled:hover:bg-transparent'
            )}
            onClick={() => onBox(connection.mailbox_id)}
          >
            <span aria-hidden="true" className="relative flex size-5 shrink-0 items-center justify-center">
              <span className="grid size-5 place-items-center rounded-md bg-row-selected text-[10px] font-semibold text-text-muted">
                {provider.charAt(0)}
              </span>
              <span
                data-status-dot={connection.status}
                className={cn(
                  'absolute -right-0.5 -bottom-0.5 size-2 rounded-full ring-2 ring-bg-canvas',
                  statusDotClassName(connection.status)
                )}
              />
            </span>
            <span
              aria-hidden="true"
              className={cn(
                'min-w-0 flex-1 truncate',
                rail && 'hidden lg:inline lg:min-w-0 lg:flex-1 lg:truncate'
              )}
            >
              {connection.account_address}
            </span>
            {unread > 0 ? (
              <span
                aria-hidden="true"
                className={cn(
                  'shrink-0 text-xs text-text-faint tabular-nums',
                  rail && 'hidden lg:inline'
                )}
              >
                {unread}
              </span>
            ) : null}
          </button>
        );
      })}
      {external.onAdd ? (
        <button
          type="button"
          aria-label={rail ? t('external.sidebar.add') : undefined}
          title={rail ? t('external.sidebar.add') : undefined}
          className={cn(
            'flex h-[30px] w-full min-w-0 shrink-0 items-center gap-2 rounded-[8px] px-3 text-left text-[12.5px] text-text-muted transition-colors focus-ring hover:bg-nav-hover hover:text-text-main motion-reduce:transition-none',
            rail && 'justify-center px-0 lg:justify-start lg:px-3'
          )}
          onClick={external.onAdd}
        >
          <Plus className="size-4 shrink-0" aria-hidden="true" />
          <span className={cn('min-w-0 flex-1 truncate', rail && 'sr-only lg:not-sr-only')}>
            {t('external.sidebar.add')}
          </span>
        </button>
      ) : null}
    </nav>
  );
}
