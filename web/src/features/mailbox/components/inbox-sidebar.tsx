'use client';

import { useState } from 'react';
import {
  CalendarDays,
  ChevronDown,
  ChevronRight,
  Inbox,
  Lock,
  Send,
  Settings2,
  Star,
} from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { LabelDot } from '@/features/mailbox/components/mailbox-sidebar';
import type { InboxCounts, InboxView, MailRole, Mailbox } from '@/features/mailbox/types';
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

/** A role narrowing of the list: a role id, or `none` for the mailboxes without one. */
export type InboxRoleFilter = number | 'none';

/** The key of a sidebar group: a role id, or `none` for Ungrouped. */
type GroupKey = string;
const ungroupedKey: GroupKey = 'none';

interface SidebarGroup {
  key: GroupKey;
  filter: InboxRoleFilter;
  name: string;
  role: MailRole | null;
  mailboxes: Mailbox[];
  unread: number;
}

/**
 * The unified inbox sidebar: the four views with the caller's counts (`all_unread` emphasized,
 * Sent without a number but with a "queued" badge while mail waits for the send limit), then
 * "Roles & mailboxes": each mail role in the API's order with its color and unread count, and
 * Ungrouped last. A role narrows the list and unfolds its mailboxes; a readable mailbox shows its
 * unread count and narrows to itself, an unreadable one (an admin's) is locked, without a number,
 * and cannot be chosen. Members see only roles holding a mailbox they can see. Owners and admins
 * get "Manage roles" beside the heading. Numbers appear once `counts` has loaded.
 */
export function InboxSidebar({
  counts,
  mailboxes,
  roles,
  canManage,
  view,
  box = null,
  role = null,
  onView,
  onBox,
  onRole,
  onManageRoles,
}: {
  counts: InboxCounts | undefined;
  /** Every mailbox the caller has any access to. */
  mailboxes: readonly Mailbox[];
  roles: readonly MailRole[] | undefined;
  canManage: boolean;
  view: InboxView;
  box?: number | null;
  role?: InboxRoleFilter | null;
  onView: (view: InboxView) => void;
  onBox: (mailboxId: number) => void;
  onRole: (role: InboxRoleFilter) => void;
  onManageRoles: () => void;
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
  const unreadByRole = new Map(counts?.roles.map(item => [item.role_id, item.unread]));

  const groups = sidebarGroups(mailboxes, roles ?? [], canManage, {
    ungroupedName: t('inbox.groups.ungrouped'),
    unreadByRole,
    ungroupedUnread: counts?.ungrouped_unread ?? 0,
  });
  // Unfolded by default: the narrowed group, the group of the narrowed mailbox, and Ungrouped
  // when there is no role. A click on the narrowed group folds or unfolds it.
  const activeKey =
    role !== null
      ? String(role)
      : box !== null
        ? (groups.find(group => group.mailboxes.some(mailbox => mailbox.id === box))?.key ?? null)
        : null;
  const [folded, setFolded] = useState<Record<GroupKey, boolean>>({});
  const isOpen = (group: SidebarGroup) =>
    folded[group.key] === undefined
      ? group.key === activeKey || (group.key === ungroupedKey && groups.length === 1)
      : !folded[group.key];

  const chooseGroup = (group: SidebarGroup) => {
    if (role !== null && String(role) === group.key) {
      setFolded(current => ({ ...current, [group.key]: isOpen(group) }));
      return;
    }
    setFolded(current => ({ ...current, [group.key]: false }));
    onRole(group.filter);
  };

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
      <nav aria-label={t('inbox.groups.label')} className="space-y-1">
        <div className="flex min-h-7 items-center gap-2 px-3">
          <p className="min-w-0 flex-1 truncate text-xs font-medium text-muted-foreground">
            {t('inbox.groups.label')}
          </p>
          {canManage ? (
            <Button
              type="button"
              isIcon
              size="xs"
              variant="ghost"
              aria-label={t('inbox.groups.manage')}
              title={t('inbox.groups.manage')}
              onClick={onManageRoles}
            >
              <Settings2 className="size-3.5" />
            </Button>
          ) : null}
        </div>
        {groups.map(group => {
          const open = isOpen(group);
          const selected = role !== null && String(role) === group.key;
          const Chevron = open ? ChevronDown : ChevronRight;
          return (
            <div key={group.key} className="space-y-0.5">
              <button
                type="button"
                aria-expanded={open}
                aria-current={selected ? 'page' : undefined}
                aria-label={
                  group.unread > 0
                    ? t('inbox.groups.unread', { name: group.name, count: group.unread })
                    : undefined
                }
                className={cn(
                  rowClass,
                  selected ? 'bg-accent font-medium text-foreground' : 'text-muted-foreground'
                )}
                onClick={() => chooseGroup(group)}
              >
                <Chevron className="size-3.5 shrink-0" aria-hidden="true" />
                {group.role ? <LabelDot color={group.role.color} className="size-2" /> : null}
                <span className="min-w-0 flex-1 truncate">{group.name}</span>
                {group.unread > 0 ? (
                  <span aria-hidden="true" className="shrink-0 text-xs tabular-nums">
                    {group.unread}
                  </span>
                ) : null}
              </button>
              {open
                ? group.mailboxes.map(mailbox =>
                    mailbox.access.view ? (
                      <MailboxRow
                        key={mailbox.id}
                        mailbox={mailbox}
                        unread={unreadByMailbox.get(mailbox.id) ?? 0}
                        selected={mailbox.id === box}
                        onSelect={() => onBox(mailbox.id)}
                      />
                    ) : (
                      <div
                        key={mailbox.id}
                        className="flex min-w-0 items-center gap-2 py-1.5 pr-3 pl-9 text-sm text-muted-foreground"
                      >
                        <Lock className="size-3.5 shrink-0" aria-hidden="true" />
                        <span className="min-w-0 flex-1 truncate">{mailbox.address}</span>
                        <span className="sr-only">{t('inbox.groups.locked')}</span>
                      </div>
                    )
                  )
                : null}
            </div>
          );
        })}
      </nav>
    </>
  );
}

function MailboxRow({
  mailbox,
  unread,
  selected,
  onSelect,
}: {
  mailbox: Mailbox;
  unread: number;
  selected: boolean;
  onSelect: () => void;
}) {
  const t = useT('mailbox');
  return (
    <button
      type="button"
      aria-label={
        unread > 0 ? t('views.unread', { name: mailbox.address, count: unread }) : undefined
      }
      aria-current={selected ? 'page' : undefined}
      className={cn(
        rowClass,
        'pl-9',
        selected ? 'bg-accent font-medium text-foreground' : 'text-muted-foreground'
      )}
      onClick={onSelect}
    >
      <span className="min-w-0 flex-1 truncate">{mailbox.address}</span>
      {unread > 0 ? (
        <span aria-hidden="true" className="shrink-0 text-xs tabular-nums">
          {unread}
        </span>
      ) : null}
    </button>
  );
}

/**
 * The roles in the API's order, then Ungrouped. A mailbox belongs to the group of its `role_id`;
 * one whose role is not listed is ungrouped, so no mailbox is hidden. Members see only roles with
 * a mailbox in their list, as the API answers them.
 */
function sidebarGroups(
  mailboxes: readonly Mailbox[],
  roles: readonly MailRole[],
  canManage: boolean,
  {
    ungroupedName,
    unreadByRole,
    ungroupedUnread,
  }: { ungroupedName: string; unreadByRole: Map<number, number>; ungroupedUnread: number }
): SidebarGroup[] {
  const shown = roles.filter(
    role => canManage || mailboxes.some(mailbox => mailbox.role_id === role.id)
  );
  const shownIds = new Set(shown.map(role => role.id));
  return [
    ...shown.map(role => ({
      key: String(role.id),
      filter: role.id,
      name: role.name,
      role,
      mailboxes: mailboxes.filter(mailbox => mailbox.role_id === role.id),
      unread: unreadByRole.get(role.id) ?? 0,
    })),
    {
      key: ungroupedKey,
      filter: 'none' as const,
      name: ungroupedName,
      role: null,
      mailboxes: mailboxes.filter(
        mailbox =>
          mailbox.role_id === undefined ||
          mailbox.role_id === null ||
          !shownIds.has(mailbox.role_id)
      ),
      unread: ungroupedUnread,
    },
  ];
}
