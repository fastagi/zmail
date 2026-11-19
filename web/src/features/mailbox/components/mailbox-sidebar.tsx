'use client';

import { Fragment, type ReactNode } from 'react';
import { Archive, Folder, Inbox, Send, Settings2, Star, Trash2 } from 'lucide-react';

import { Button } from '@/components/ui/button';
import type {
  MailFolder,
  MailLabel,
  MailLabelColor,
  MailMessageCounts,
  MailMessageFilters,
} from '@/features/mailbox/types';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/** One view of a mailbox: a placement, a folder, a label, or the caller's stars. */
export type MailView =
  | { kind: 'inbox' }
  | { kind: 'starred' }
  | { kind: 'sent' }
  | { kind: 'archive' }
  | { kind: 'trash' }
  | { kind: 'folder'; id: number }
  | { kind: 'label'; id: number };

export const inboxView: MailView = { kind: 'inbox' };

/**
 * The listing filters of a view. Placement views (Inbox, Sent, Archive, a folder) list one row per
 * thread; stars, labels, and the trash list single messages. See `contracts/MAILBOXES.md`.
 */
export function viewFilters(view: MailView): MailMessageFilters {
  switch (view.kind) {
    case 'inbox':
      return { placement: 'default', direction: 'inbound', group: 'thread' };
    case 'sent':
      return { placement: 'default', direction: 'outbound', group: 'thread' };
    case 'archive':
      return { placement: 'archive', group: 'thread' };
    case 'folder':
      return { placement: 'folder', folderId: view.id, group: 'thread' };
    case 'trash':
      return { placement: 'trash' };
    case 'starred':
      return { starred: true };
    case 'label':
      return { labelId: view.id };
  }
}

export function sameView(left: MailView, right: MailView): boolean {
  if (left.kind !== right.kind) return false;
  if (left.kind === 'folder' && right.kind === 'folder') return left.id === right.id;
  if (left.kind === 'label' && right.kind === 'label') return left.id === right.id;
  return true;
}

/** Fill classes of the fixed label palette. */
export const labelColorClass: Record<MailLabelColor, string> = {
  gray: 'bg-gray-500',
  red: 'bg-red-500',
  orange: 'bg-orange-500',
  yellow: 'bg-yellow-500',
  green: 'bg-green-600',
  teal: 'bg-teal-500',
  blue: 'bg-blue-500',
  purple: 'bg-purple-500',
  pink: 'bg-pink-500',
};

export function LabelDot({ color, className }: { color: MailLabelColor; className?: string }) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        'inline-block size-2.5 shrink-0 rounded-full',
        labelColorClass[color],
        className
      )}
    />
  );
}

/**
 * The left rail below the mailbox switcher: Inbox, Starred, Sent, Archive, Trash, then the
 * mailbox's folders and labels. Counts are the caller's own. Only `organize` arranges folders and
 * labels; everyone who reads the mailbox sees and opens them.
 */
export function MailboxSidebar({
  view,
  counts,
  folders,
  labels,
  canOrganize,
  onSelect,
  onManageFolders,
  onManageLabels,
}: {
  view: MailView | null;
  counts: MailMessageCounts | undefined;
  folders: readonly MailFolder[];
  labels: readonly MailLabel[];
  canOrganize: boolean;
  onSelect: (view: MailView) => void;
  onManageFolders: () => void;
  onManageLabels: () => void;
}) {
  const t = useT('mailbox');
  const folderUnread = new Map(counts?.folders.map(entry => [entry.folder_id, entry.unread]));
  const item = (
    target: MailView,
    icon: ReactNode,
    name: string,
    count?: number,
    countLabel?: string
  ) => {
    const current = view !== null && sameView(view, target);
    return (
      <li>
        <button
          type="button"
          aria-current={current ? 'page' : undefined}
          className={cn(
            'flex w-full items-center gap-2.5 rounded-md px-2.5 py-1.5 text-left text-sm transition-colors focus-ring hover:bg-accent',
            current ? 'bg-accent font-medium text-foreground' : 'text-muted-foreground'
          )}
          onClick={() => onSelect(target)}
        >
          {icon}
          <span className="min-w-0 flex-1 truncate">{name}</span>
          {count && count > 0 ? (
            <span
              aria-label={countLabel}
              className={cn(
                'shrink-0 text-xs tabular-nums',
                current ? '' : 'font-semibold text-foreground'
              )}
            >
              {count > 999 ? '999+' : count}
            </span>
          ) : null}
        </button>
      </li>
    );
  };
  const iconClass = 'size-4 shrink-0';
  const unread = (name: string, count: number | undefined) =>
    count === undefined ? undefined : t('views.unread', { name, count });

  return (
    <nav aria-label={t('views.label')} className="space-y-4">
      <ul className="space-y-0.5">
        {item(
          inboxView,
          <Inbox className={iconClass} aria-hidden="true" />,
          t('views.inbox'),
          counts?.placements.default_inbound_unread,
          unread(t('views.inbox'), counts?.placements.default_inbound_unread)
        )}
        {item(
          { kind: 'starred' },
          <Star className={iconClass} aria-hidden="true" />,
          t('views.starred'),
          counts?.starred,
          counts ? t('views.starredCount', { count: counts.starred }) : undefined
        )}
        {item({ kind: 'sent' }, <Send className={iconClass} aria-hidden="true" />, t('views.sent'))}
        {item(
          { kind: 'archive' },
          <Archive className={iconClass} aria-hidden="true" />,
          t('views.archive'),
          counts?.placements.archive_unread,
          unread(t('views.archive'), counts?.placements.archive_unread)
        )}
        {item(
          { kind: 'trash' },
          <Trash2 className={iconClass} aria-hidden="true" />,
          t('views.trash')
        )}
      </ul>

      {folders.length > 0 || canOrganize ? (
        <SidebarSection
          title={t('views.folders')}
          manageLabel={canOrganize ? t('views.manageFolders') : null}
          onManage={onManageFolders}
        >
          {folders.map(folder => (
            <Fragment key={folder.id}>
              {item(
                { kind: 'folder', id: folder.id },
                <Folder className={iconClass} aria-hidden="true" />,
                folder.name,
                folderUnread.get(folder.id),
                unread(folder.name, folderUnread.get(folder.id))
              )}
            </Fragment>
          ))}
        </SidebarSection>
      ) : null}

      {labels.length > 0 || canOrganize ? (
        <SidebarSection
          title={t('views.labels')}
          manageLabel={canOrganize ? t('views.manageLabels') : null}
          onManage={onManageLabels}
        >
          {labels.map(label => (
            <Fragment key={label.id}>
              {item(
                { kind: 'label', id: label.id },
                <span className="flex size-4 shrink-0 items-center justify-center">
                  <LabelDot color={label.color} />
                </span>,
                label.name
              )}
            </Fragment>
          ))}
        </SidebarSection>
      ) : null}
    </nav>
  );
}

function SidebarSection({
  title,
  manageLabel,
  onManage,
  children,
}: {
  title: string;
  manageLabel: string | null;
  onManage: () => void;
  children: ReactNode;
}) {
  return (
    <section className="space-y-1">
      <div className="flex items-center justify-between gap-2 px-2.5">
        <h3 className="flex items-center gap-1.5 text-xs font-medium tracking-wide text-muted-foreground uppercase">
          {title}
        </h3>
        {manageLabel ? (
          <Button
            type="button"
            isIcon
            size="xs"
            variant="ghost"
            aria-label={manageLabel}
            title={manageLabel}
            onClick={onManage}
          >
            <Settings2 className="size-3.5" />
          </Button>
        ) : null}
      </div>
      <ul className="space-y-0.5">{children}</ul>
    </section>
  );
}
