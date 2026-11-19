'use client';

import { useState } from 'react';
import { Check, ChevronsUpDown, Lock, Search, Settings2, User, UserX, Users } from 'lucide-react';

import { Avatar, AvatarFallback } from '@/components/ui/avatar';
import { Input } from '@/components/ui/input';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import type { Mailbox } from '@/features/mailbox/types';
import { useT } from '@/i18n';
import { cn } from '@/utils';

function cappedCount(count: number): string {
  return count > 99 ? '99+' : String(count);
}

/** Initials of a mailbox for its avatar: the display name's words, else the address. */
function initials(mailbox: Mailbox): string {
  const words = mailbox.display_name.split(/\s+/u).filter(Boolean);
  const letters =
    words.length > 0 ? words.slice(0, 2).map(word => Array.from(word)[0]) : [mailbox.address[0]];
  return letters.join('').toUpperCase();
}

function MailboxAvatar({ mailbox, className }: { mailbox: Mailbox; className?: string }) {
  return (
    <Avatar aria-hidden="true" className={cn('size-8', className)}>
      <AvatarFallback className="bg-primary/10 text-xs font-semibold text-primary">
        {initials(mailbox)}
      </AvatarFallback>
    </Avatar>
  );
}

/** The caller's permission on a shared mailbox, as the access panel names it. */
function roleKey(mailbox: Mailbox) {
  if (mailbox.access.organize) return 'access.permissions.manage' as const;
  return mailbox.access.send
    ? ('access.permissions.send' as const)
    : ('access.permissions.view' as const);
}

/**
 * The global mailbox switcher at the top of the left rail, as in a desktop mail client. It lists
 * the mailboxes the caller reads, the caller's own first and then the shared ones with the
 * caller's role, each with its Inbox unread count. The trigger shows the unread mail waiting in
 * the other mailboxes. Owners and admins find mailbox administration at the bottom.
 */
export function MailboxSwitcher({
  mailboxes,
  selected,
  canManage,
  onSelect,
  onManage,
}: {
  /** Only mailboxes the caller reads (`access.view`). */
  mailboxes: readonly Mailbox[];
  selected: Mailbox | null;
  canManage: boolean;
  onSelect: (mailbox: Mailbox) => void;
  onManage: () => void;
}) {
  const t = useT('mailbox');
  const [open, setOpen] = useState(false);
  const [filter, setFilter] = useState('');
  const otherUnread = mailboxes
    .filter(mailbox => mailbox.id !== selected?.id)
    .reduce((total, mailbox) => total + mailbox.unread_count, 0);
  const needle = filter.trim().toLowerCase();
  const shown = mailboxes.filter(
    mailbox =>
      needle === '' ||
      mailbox.address.toLowerCase().includes(needle) ||
      mailbox.display_name.toLowerCase().includes(needle)
  );
  const groups = [
    {
      key: 'mine',
      title: t('switcher.mine'),
      items: shown.filter(item => item.type === 'personal'),
    },
    {
      key: 'shared',
      title: t('switcher.shared'),
      items: shown.filter(item => item.type !== 'personal'),
    },
  ].filter(group => group.items.length > 0);

  return (
    <Popover
      open={open}
      onOpenChange={next => {
        setOpen(next);
        if (!next) setFilter('');
      }}
    >
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label={
            selected ? t('switcher.label', { address: selected.address }) : t('switcher.none')
          }
          className="flex w-full items-center gap-2.5 rounded-lg border bg-background px-2.5 py-2 text-left shadow-xs transition-colors focus-ring hover:bg-accent"
        >
          {selected ? <MailboxAvatar mailbox={selected} /> : null}
          <span className="min-w-0 flex-1">
            <span className="block truncate text-sm font-medium">
              {selected ? selected.display_name || selected.address : t('switcher.none')}
            </span>
            {selected?.display_name ? (
              <span className="block truncate text-xs text-muted-foreground">
                {selected.address}
              </span>
            ) : null}
          </span>
          {otherUnread > 0 ? (
            <span
              aria-label={t('switcher.otherUnread', { count: otherUnread })}
              className="shrink-0 rounded-full bg-primary px-1.5 py-0.5 text-[11px] leading-none font-medium text-primary-foreground"
            >
              {cappedCount(otherUnread)}
            </span>
          ) : null}
          <ChevronsUpDown className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-(--radix-popover-trigger-width) min-w-72 p-1.5">
        <div className="relative mb-1">
          <Search
            className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground"
            aria-hidden="true"
          />
          <Input
            type="search"
            value={filter}
            aria-label={t('switcher.filter')}
            placeholder={t('switcher.filter')}
            className="h-8 pl-8 text-sm"
            onChange={event => setFilter(event.target.value)}
          />
        </div>
        <div className="max-h-80 space-y-2 overflow-y-auto">
          {groups.length === 0 ? (
            <p className="px-2.5 py-2 text-sm text-muted-foreground">{t('switcher.noMatch')}</p>
          ) : null}
          {groups.map(group => (
            <section key={group.key}>
              <h3 className="px-2.5 pt-1 pb-0.5 text-xs font-medium text-muted-foreground">
                {group.title}
              </h3>
              <ul aria-label={group.title} className="space-y-0.5">
                {group.items.map(mailbox => {
                  const current = mailbox.id === selected?.id;
                  return (
                    <li key={mailbox.id}>
                      <button
                        type="button"
                        aria-current={current ? 'true' : undefined}
                        className={cn(
                          'flex w-full items-center gap-2.5 rounded-md px-2.5 py-1.5 text-left focus-ring hover:bg-accent',
                          current && 'bg-accent'
                        )}
                        onClick={() => {
                          setOpen(false);
                          setFilter('');
                          onSelect(mailbox);
                        }}
                      >
                        <MailboxAvatar mailbox={mailbox} className="size-7" />
                        <span className="min-w-0 flex-1">
                          <span className="block truncate text-sm font-medium">
                            {mailbox.address}
                          </span>
                          <span className="block truncate text-xs text-muted-foreground">
                            {mailbox.display_name || t('mailboxes.noDisplayName')}
                            {mailbox.type === 'personal' ? null : (
                              <>
                                {' · '}
                                {t(roleKey(mailbox))}
                              </>
                            )}
                          </span>
                        </span>
                        {mailbox.unread_count > 0 ? (
                          <span
                            aria-label={t('mailboxes.unreadCount', { count: mailbox.unread_count })}
                            className="shrink-0 rounded-full bg-primary px-2 py-0.5 text-xs font-medium text-primary-foreground"
                          >
                            {cappedCount(mailbox.unread_count)}
                          </span>
                        ) : null}
                        <Check
                          className={cn('size-4 shrink-0', current ? 'opacity-100' : 'opacity-0')}
                          aria-hidden="true"
                        />
                      </button>
                    </li>
                  );
                })}
              </ul>
            </section>
          ))}
        </div>
        {canManage ? (
          <div className="mt-1.5 border-t pt-1.5">
            <button
              type="button"
              className="flex w-full items-center gap-2 rounded-md px-2.5 py-2 text-left text-sm focus-ring hover:bg-accent"
              onClick={() => {
                setOpen(false);
                onManage();
              }}
            >
              <Settings2 className="size-4 text-muted-foreground" aria-hidden="true" />
              {t('switcher.manage')}
            </button>
          </div>
        ) : null}
      </PopoverContent>
    </Popover>
  );
}

/** Type, handover, and lock marks of a mailbox, with their names for assistive technology. */
export function MailboxMarks({ mailbox }: { mailbox: Mailbox }) {
  const t = useT('mailbox');
  const typeLabel = t(
    mailbox.awaiting_handover
      ? 'mailboxes.awaitingHandover'
      : mailbox.type === 'personal'
        ? 'mailboxes.personal'
        : 'mailboxes.shared'
  );
  const TypeIcon = mailbox.awaiting_handover ? UserX : mailbox.type === 'personal' ? User : Users;
  return (
    <span className="flex shrink-0 items-center gap-1 text-muted-foreground">
      <span title={typeLabel} className="flex items-center">
        <TypeIcon className="size-3.5" aria-hidden="true" />
        <span className="sr-only">{typeLabel}</span>
      </span>
      {mailbox.access.view ? null : (
        <span title={t('mailboxes.locked')} className="flex items-center">
          <Lock className="size-3.5" aria-hidden="true" />
          <span className="sr-only">{t('mailboxes.locked')}</span>
        </span>
      )}
    </span>
  );
}
