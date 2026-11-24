'use client';

import { lazy, Suspense, useState, type ReactNode } from 'react';
import {
  ArrowDownWideNarrow,
  CalendarDays,
  FileText,
  Inbox,
  Lock,
  PenLine,
  Send,
  Settings2,
  Star,
} from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Kbd } from '@/components/ui/kbd';
import { LabelDot } from '@/features/mailbox/components/mailbox-sidebar';
import { WorkspaceModeToggle } from '@/features/mailbox/components/workspace-mode-toggle';
import type { ExternalMailConnection } from '@/features/mailbox/external-mail-schemas';
import { useIsPersonalMode } from '@/features/mailbox/stores/workspace-mode';
import type { InboxCounts, InboxView, MailRole, Mailbox } from '@/features/mailbox/types';
import { isOwnConnectedMailbox } from '@/features/mailbox/utils/connected-mailbox';
import { useT } from '@/i18n';
import { cn } from '@/utils';

const InboxSidebarFooter = lazy(() => import('./inbox-sidebar-footer'));
// "Connected by me" loads only when the viewer has a connected mailbox or may add one.
const ConnectedMailboxes = lazy(() => import('./inbox-sidebar-connected'));

const viewIcons = {
  all: Inbox,
  today: CalendarDays,
  pending: Star,
  sent: Send,
} as const satisfies Record<InboxView, unknown>;

const viewOrder: readonly InboxView[] = ['all', 'today', 'pending', 'sent'];

/** A role narrowing of the list: a role id, or `none` for the mailboxes without one. */
export type InboxRoleFilter = number | 'none';

/**
 * The external mailboxes section: the viewer's own connections when known (they decorate the
 * connected mailboxes with their provider and status), the "Add external mailbox" entry (null when
 * the instance offers no connector), and their management.
 */
export interface InboxSidebarExternal {
  connections: readonly ExternalMailConnection[] | undefined;
  onAdd: (() => void) | null;
  onManage: () => void;
}

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
 * The unified inbox sidebar (§11.0, §11.4): the organization with the `⌘K` cap, Compose, the four
 * views with the caller's counts (`all_unread` emphasized, Sent without a number but with a
 * "queued" badge while mail waits for the send limit), then "Roles & mailboxes": each mail role
 * in the API's order with its color and unread count, and Ungrouped last. A role narrows the list
 * and unfolds its mailboxes; a readable mailbox shows its unread count and narrows to itself, an
 * unreadable one (an admin's) is locked, without a number, and cannot be chosen. Members see only
 * roles holding a mailbox they can see. Owners and admins get "Manage roles" beside the heading.
 * At the bottom: today's sending, links to the other mail pages, and the shortcut help.
 *
 * With external mail on, the list splits in two (roadmap §5.1): "Team mailboxes" above, and
 * "Connected by me" below with the viewer's own external mailboxes, each with its provider and a
 * status dot, and the "Add external mailbox" entry. External mailboxes never show among the team's.
 *
 * `rail` folds it to an icon rail between 768 and 1023px wide (§11.11); the full sidebar, with
 * the mailboxes, the quota card, and the links, shows from 1024px and in the narrow-screen sheet.
 */
export function InboxSidebar({
  organization,
  sort,
  onSort,
  counts,
  mailboxes,
  roles,
  canManage,
  view,
  box = null,
  role = null,
  rail = false,
  onView,
  onBox,
  onRole,
  onManageRoles,
  onCompose,
  onShortcuts,
  external = null,
  drafts = null,
}: {
  /** The organization's name, or its picker. */
  organization: ReactNode;
  sort: string;
  onSort: (value: string) => void;
  counts: InboxCounts | undefined;
  /** Every mailbox the caller has any access to. */
  mailboxes: readonly Mailbox[];
  roles: readonly MailRole[] | undefined;
  canManage: boolean;
  view: InboxView;
  box?: number | null;
  role?: InboxRoleFilter | null;
  rail?: boolean;
  onView: (view: InboxView) => void;
  onBox: (mailboxId: number) => void;
  onRole: (role: InboxRoleFilter) => void;
  onManageRoles: () => void;
  /** Writes a new message; null when no mailbox may send. */
  onCompose: (() => void) | null;
  /** Opens the keyboard shortcut help. */
  onShortcuts: () => void;
  /** The external mailboxes section; it shows while the viewer has one or may add one. */
  external?: InboxSidebarExternal | null;
  /** The caller's saved drafts: how many, and opening their list; null when none may send. */
  drafts?: { count: number | undefined; onOpen: () => void } | null;
}) {
  const t = useT('mailbox');
  const isPersonalMode = useIsPersonalMode();
  const views = counts?.views;
  const viewNumber: Record<InboxView, number | undefined> = {
    all: views?.all_unread,
    today: views?.today,
    pending: views?.pending,
    sent: undefined,
  };
  // All mail narrowed to a role or mailbox is that role or mailbox; other views stay marked.
  const narrowed = box !== null || role !== null;
  const unreadByMailbox = new Map(counts?.mailboxes.map(item => [item.mailbox_id, item.unread]));
  const unreadByRole = new Map(counts?.roles.map(item => [item.role_id, item.unread]));
  // In the rail, labels stay for assistive technology and come back from 1024px.
  const label = rail
    ? 'sr-only lg:not-sr-only lg:min-w-0 lg:flex-1 lg:truncate'
    : 'min-w-0 flex-1 truncate';
  const wide = rail ? 'hidden lg:flex' : 'flex';
  const railIcon = rail ? 'size-4 shrink-0 lg:hidden' : 'hidden';

  // A member's connected accounts are private copies: their owner finds them under "Connected by
  // me", and nobody else lists them among the team's. A connected shared mailbox is the team's.
  const teamMailboxes = mailboxes.filter(mailbox => !isOwnConnectedMailbox(mailbox));
  const mine = mailboxes.filter(isOwnConnectedMailbox);
  const showConnected = external !== null && (mine.length > 0 || external.onAdd !== null);
  const ordered =
    sort === 'default'
      ? teamMailboxes
      : [...teamMailboxes].sort((a, b) => {
          if (sort === 'unread') {
            const difference = (unreadByMailbox.get(b.id) ?? 0) - (unreadByMailbox.get(a.id) ?? 0);
            if (difference) return difference;
          }
          return a.address.localeCompare(b.address);
        });
  const groups = sidebarGroups(ordered, roles ?? [], canManage, {
    ungroupedName: t('inbox.groups.ungrouped'),
    unreadByRole,
    ungroupedUnread: counts?.ungrouped_unread ?? 0,
  });
  // Without roles there is nothing to group: the mailboxes are listed themselves, as Gmail lists
  // its labels, instead of behind a lone Ungrouped folder.
  const flat = groups.length === 1;
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
    <div className="flex min-h-full flex-col gap-0.5">
      <div
        className={cn(
          'flex items-center gap-2.5 pb-[18px]',
          rail ? 'justify-center lg:justify-start lg:px-2' : 'px-2'
        )}
      >
        <ZmailMark />
        <div className={cn('min-w-0 flex-1', rail && 'hidden lg:block')}>{organization}</div>
        <Kbd aria-hidden="true" className={cn(rail && 'hidden lg:inline-flex')}>
          ⌘K
        </Kbd>
      </div>

      <div className={cn('px-1 pb-2.5', rail && 'hidden lg:block')}>
        <WorkspaceModeToggle className="w-full" />
      </div>

      {onCompose ? (
        <button
          type="button"
          data-compose-trigger=""
          aria-label={rail ? t('actions.compose') : undefined}
          title={rail ? t('actions.compose') : undefined}
          className={cn(
            'mx-1 mb-3.5 flex h-9 shrink-0 items-center gap-2 rounded-[9px] border border-border-main bg-bg-surface px-3 text-left text-text-main shadow-xs transition-colors focus-ring hover:bg-nav-hover motion-reduce:transition-none',
            rail && 'justify-center px-0 lg:justify-start lg:px-3'
          )}
          onClick={onCompose}
        >
          <PenLine className="size-[15px] shrink-0" aria-hidden="true" />
          <span className={label}>{t('actions.compose')}</span>
          <Kbd aria-hidden="true" className={cn(rail && 'hidden lg:inline-flex')}>
            C
          </Kbd>
        </button>
      ) : null}

      <nav aria-label={t('inbox.views.label')} className="flex flex-col gap-0.5">
        {viewOrder.map(item => {
          const Icon = viewIcons[item];
          const name = t(`inbox.views.${item}`);
          const count = viewNumber[item];
          const queued = item === 'sent' && (views?.sent_waiting ?? 0) > 0;
          let accessibleName: string | undefined;
          if (queued) accessibleName = t('inbox.views.sentQueued', { name });
          else if (item === 'all' && count) {
            accessibleName = t('inbox.views.unreadCount', { name, count });
          } else if (count) accessibleName = t('inbox.views.count', { name, count });
          return (
            <button
              key={item}
              type="button"
              aria-label={accessibleName}
              aria-current={item === view && !(narrowed && item === 'all') ? 'page' : undefined}
              title={rail ? name : undefined}
              className={navRowClass(item === view && !(narrowed && item === 'all'), rail)}
              onClick={() => onView(item)}
            >
              <Icon className={railIcon} aria-hidden="true" />
              <span className={label}>{name}</span>
              {queued ? (
                <Badge
                  variant="outline"
                  className={cn(
                    'shrink-0 px-1.5 text-[0.6875rem]',
                    rail && 'hidden lg:inline-flex'
                  )}
                >
                  {t('inbox.views.queued')}
                </Badge>
              ) : null}
              {count ? (
                <span
                  aria-hidden="true"
                  className={cn(
                    'shrink-0 text-xs tabular-nums',
                    item === 'all' ? 'font-semibold text-brand' : 'text-text-faint',
                    rail && 'hidden lg:inline'
                  )}
                >
                  {count}
                </span>
              ) : null}
            </button>
          );
        })}
        {drafts ? (
          <button
            type="button"
            aria-label={
              drafts.count ? t('drafts.sidebarCount', { count: drafts.count }) : undefined
            }
            title={rail ? t('drafts.sidebar') : undefined}
            className={navRowClass(false, rail)}
            onClick={drafts.onOpen}
          >
            <FileText className={railIcon} aria-hidden="true" />
            <span className={label}>{t('drafts.sidebar')}</span>
            {drafts.count ? (
              <span
                aria-hidden="true"
                className={cn(
                  'shrink-0 text-xs text-text-faint tabular-nums',
                  rail && 'hidden lg:inline'
                )}
              >
                {drafts.count}
              </span>
            ) : null}
          </button>
        ) : null}
      </nav>

      {!isPersonalMode ? (
      <nav aria-label={t('inbox.groups.label')} className="flex flex-col gap-0.5">
        <div className={cn('items-center gap-2 pt-[18px] pr-1 pb-1.5 pl-3', wide)}>
          <p className="min-w-0 flex-1 truncate text-[11px] tracking-[0.06em] text-text-faint">
            {showConnected
              ? t('external.sidebar.team')
              : flat
                ? t('inbox.groups.mailboxes')
                : t('inbox.groups.label')}
          </p>
          <label
            title={t('inbox.sort.label')}
            className="relative grid size-7 shrink-0 place-items-center rounded-md text-text-muted hover:bg-nav-hover focus-within:ring-2"
          >
            <ArrowDownWideNarrow aria-hidden="true" className="size-4" />
            <select
              aria-label={t('inbox.sort.label')}
              className="absolute inset-0 size-full cursor-pointer opacity-0"
              value={sort}
              onChange={event => onSort(event.target.value)}
            >
              <option value="default">{t('inbox.sort.default')}</option>
              <option value="address">{t('inbox.sort.address')}</option>
              <option value="unread">{t('inbox.sort.unread')}</option>
            </select>
          </label>
          {canManage ? (
            <Button
              type="button"
              isIcon
              size="xs"
              variant="ghost"
              aria-label={t('inbox.groups.manage')}
              title={t('inbox.groups.manage')}
              className="text-text-faint hover:text-text-main"
              onClick={onManageRoles}
            >
              <Settings2 className="size-3.5" />
            </Button>
          ) : null}
        </div>
        {rail ? <div className="h-4 lg:hidden" aria-hidden="true" /> : null}
        {flat
          ? ordered.map(mailbox =>
              mailbox.access.view ? (
                <MailboxRow
                  key={mailbox.id}
                  mailbox={mailbox}
                  unread={unreadByMailbox.get(mailbox.id) ?? 0}
                  selected={mailbox.id === box}
                  flat
                  rail={rail}
                  onSelect={() => onBox(mailbox.id)}
                />
              ) : (
                <LockedMailboxRow key={mailbox.id} mailbox={mailbox} flat className={wide} />
              )
            )
          : null}
        {(flat ? [] : groups).map(group => {
          const open = isOpen(group);
          const selected = role !== null && String(role) === group.key;
          return (
            <div key={group.key} className="flex flex-col gap-0.5">
              <button
                type="button"
                aria-expanded={open}
                aria-current={selected ? 'page' : undefined}
                aria-label={
                  group.unread > 0
                    ? t('inbox.groups.unread', { name: group.name, count: group.unread })
                    : undefined
                }
                title={rail ? group.name : undefined}
                className={navRowClass(selected, rail)}
                onClick={() => chooseGroup(group)}
              >
                {group.role ? (
                  <LabelDot color={group.role.color} className="size-[7px] rounded-[2px]" />
                ) : (
                  <span
                    aria-hidden="true"
                    className="size-[7px] shrink-0 rounded-[2px] border border-text-faint"
                  />
                )}
                <span className={label}>{group.name}</span>
                {group.unread > 0 ? (
                  <span
                    aria-hidden="true"
                    className={cn(
                      'shrink-0 text-xs text-text-faint tabular-nums',
                      rail && 'hidden lg:inline'
                    )}
                  >
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
                        className={wide}
                        onSelect={() => onBox(mailbox.id)}
                      />
                    ) : (
                      <LockedMailboxRow key={mailbox.id} mailbox={mailbox} className={wide} />
                    )
                  )
                : null}
            </div>
          );
        })}
      </nav>
      ) : null}

      {isPersonalMode && external && external.onAdd && mine.length === 0 ? (
        <div
          className={cn(
            'mx-1 mt-3 rounded-xl border border-dashed border-border-main bg-bg-surface/60 p-3 text-left',
            rail && 'hidden lg:block'
          )}
        >
          <p className="text-xs font-medium text-text-main">Personal Mailboxes</p>
          <p className="mt-1 text-[11px] leading-relaxed text-text-muted">
            Connect Gmail, Outlook, iCloud, QQ, 163, or any IMAP mailbox in one click.
          </p>
          <Button
            type="button"
            size="sm"
            variant="secondary"
            className="mt-2.5 h-7 w-full text-xs"
            onClick={() => external.onAdd?.()}
          >
            Connect personal email
          </Button>
        </div>
      ) : null}

      {external && showConnected ? (
        <Suspense fallback={null}>
          <ConnectedMailboxes
            external={external}
            mailboxes={mine}
            unreadByMailbox={unreadByMailbox}
            box={box}
            rail={rail}
            onBox={onBox}
          />
        </Suspense>
      ) : null}

      <div className="min-h-4 flex-1" aria-hidden="true" />
      <div className={cn('flex-col', wide)}>
        <Suspense fallback={null}>
          <InboxSidebarFooter
            canManage={canManage}
            sentWaiting={views?.sent_waiting ?? 0}
            onShortcuts={onShortcuts}
          />
        </Suspense>
      </div>
    </div>
  );
}

/** A view or role row: 32px, radius 8px; the rail centers its icon or dot. */
function navRowClass(selected: boolean, rail: boolean): string {
  return cn(
    'flex h-8 w-full min-w-0 shrink-0 items-center gap-2.5 rounded-[8px] px-3 text-left transition-colors focus-ring motion-reduce:transition-none',
    selected
      ? 'bg-nav-selected font-semibold text-text-main'
      : 'text-text-subtle hover:bg-nav-hover',
    rail && 'justify-center px-0 lg:justify-start lg:px-3'
  );
}

/** The product mark: a 22px tile with radius 6px. */
function ZmailMark() {
  return (
    <svg
      width="22"
      height="22"
      viewBox="0 0 24 24"
      aria-hidden="true"
      className="shrink-0 text-text-main"
    >
      <rect width="24" height="24" rx="6" fill="currentColor" />
      <path
        d="M7 8h10L7 16h10"
        className="stroke-bg-canvas"
        strokeWidth="2"
        fill="none"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

/**
 * A mailbox of the sidebar: under its role, or, `flat`, at the top level when there are no roles.
 * In the rail a flat mailbox is the first letter of its address, its address on hover.
 */
function MailboxRow({
  mailbox,
  unread,
  selected,
  flat = false,
  rail = false,
  className,
  onSelect,
}: {
  mailbox: Mailbox;
  unread: number;
  selected: boolean;
  flat?: boolean;
  rail?: boolean;
  className?: string;
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
      title={rail ? mailbox.address : undefined}
      className={cn(
        'flex h-[30px] w-full min-w-0 shrink-0 items-center gap-2.5 rounded-[8px] pr-3 text-left text-[12.5px] transition-colors focus-ring motion-reduce:transition-none',
        flat ? 'pl-3' : 'pl-[29px]',
        rail && 'justify-center px-0 lg:justify-start lg:px-3',
        selected
          ? 'bg-blue-50 font-semibold text-blue-800 dark:bg-blue-950/65 dark:text-blue-200'
          : 'text-text-subtle hover:bg-nav-hover',
        className
      )}
      onClick={onSelect}
    >
      {rail ? (
        <span
          aria-hidden="true"
          className="flex size-5 shrink-0 items-center justify-center rounded-full bg-row-selected text-[11px] font-semibold text-text-muted uppercase lg:hidden"
        >
          {mailbox.address.charAt(0)}
        </span>
      ) : null}
      <span
        className={cn(
          'min-w-0 flex-1 truncate',
          rail && 'sr-only lg:not-sr-only lg:min-w-0 lg:flex-1 lg:truncate'
        )}
      >
        {mailbox.address}
      </span>
      {/* The source badge slot (slice ④), empty in this slice. */}
      <span data-slot="mailbox-source" className="empty:hidden" />
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
}

/** A mailbox the caller administers but cannot read: its address and a lock, never chosen. */
function LockedMailboxRow({
  mailbox,
  flat = false,
  className,
}: {
  mailbox: Mailbox;
  flat?: boolean;
  className?: string;
}) {
  const t = useT('mailbox');
  return (
    <div
      className={cn(
        'h-[30px] min-w-0 items-center gap-2.5 rounded-[8px] pr-3 text-[12.5px] text-text-faint',
        flat ? 'pl-3' : 'pl-[29px]',
        className
      )}
    >
      <span className="min-w-0 flex-1 truncate">{mailbox.address}</span>
      <Lock className="size-3.5 shrink-0" aria-hidden="true" />
      <span className="sr-only">{t('inbox.groups.locked')}</span>
    </div>
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
  const shown = visibleRoles(mailboxes, roles, canManage);
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

/** The roles the sidebar lists: all for owners and admins, else those holding a listed mailbox. */
export function visibleRoles(
  mailboxes: readonly Mailbox[],
  roles: readonly MailRole[],
  canManage: boolean
): MailRole[] {
  return roles.filter(role => canManage || mailboxes.some(mailbox => mailbox.role_id === role.id));
}
