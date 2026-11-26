'use client';

import { useEffect, useId, useMemo, useState, type ReactNode } from 'react';
import { CornerDownLeft, Lock, Search } from 'lucide-react';
import { useTheme } from 'next-themes';

import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog';
import { Kbd } from '@/components/ui/kbd';
import { mailboxService } from '@/features/mailbox/services/mailbox-service';
import { MAIL_SEARCH_MAX_LENGTH } from '@/features/mailbox/schemas';
import type { InboxItem, InboxView, Mailbox, MailRole } from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/** What a chosen palette result asks the inbox to do; the theme switch runs in the palette. */
export type PaletteCommand =
  | { kind: 'mailbox'; mailboxId: number }
  | { kind: 'role'; roleId: number }
  | { kind: 'mail'; item: InboxItem; q: string }
  | { kind: 'compose' }
  | { kind: 'view'; view: InboxView }
  | { kind: 'help' }
  | { kind: 'manageMailboxes' }
  | { kind: 'manageRoles' };

/** Each searched group lists at most this many results (§11.8). */
const GROUP_LIMIT = 5;
/** The pause after the last keystroke before mail is searched (§11.8). */
export const MAIL_SEARCH_DELAY_MS = 250;
const PALETTE_VIEWS: readonly InboxView[] = ['all', 'today', 'pending', 'snoozed', 'sent'];

type MailSearch =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'done'; items: InboxItem[] }
  | { status: 'failed'; error: unknown };

interface PaletteEntry {
  key: string;
  label: string;
  detail?: string;
  icon?: ReactNode;
  disabled?: boolean;
  run: () => void;
}

interface PaletteGroup {
  key: 'mailboxes' | 'roles' | 'mail' | 'actions';
  entries: PaletteEntry[];
  notice?: string;
}

/** 0 when `text` starts with `query`, 1 when it contains it, null otherwise. */
function rank(text: string, query: string): number | null {
  const value = text.toLocaleLowerCase();
  if (value.startsWith(query)) return 0;
  return value.includes(query) ? 1 : null;
}

/** The items whose texts match, prefix matches first, at most `limit` when a query is typed. */
function matching<T>(
  items: readonly T[],
  texts: (item: T) => string[],
  query: string,
  limit: number
) {
  if (query === '') return items.slice(0, limit);
  return items
    .map(item => {
      const ranks = texts(item)
        .map(text => rank(text, query))
        .filter((value): value is number => value !== null);
      return { item, score: ranks.length === 0 ? null : Math.min(...ranks) };
    })
    .filter((entry): entry is { item: T; score: number } => entry.score !== null)
    .sort((a, b) => a.score - b.score)
    .slice(0, limit)
    .map(entry => entry.item);
}

/** Searches mail `MAIL_SEARCH_DELAY_MS` after `q` last changed; '' searches nothing. */
function useMailSearch(organizationId: number, timeZone: string, q: string): MailSearch {
  const [search, setSearch] = useState<{ q: string; result: MailSearch }>({
    q: '',
    result: { status: 'idle' },
  });
  useEffect(() => {
    if (q === '') return;
    let current = true;
    const timer = window.setTimeout(() => {
      mailboxService
        .listInbox(organizationId, { view: 'all', q, tz: timeZone }, undefined, GROUP_LIMIT)
        .then(
          page => {
            if (current) setSearch({ q, result: { status: 'done', items: page.items } });
          },
          (error: unknown) => {
            if (current) setSearch({ q, result: { status: 'failed', error } });
          }
        );
    }, MAIL_SEARCH_DELAY_MS);
    return () => {
      current = false;
      window.clearTimeout(timer);
    };
  }, [organizationId, timeZone, q]);
  if (q === '') return { status: 'idle' };
  return search.q === q ? search.result : { status: 'loading' };
}

interface CommandPaletteProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Puts focus back where the palette was opened from. */
  onCloseAutoFocus?: (event: Event) => void;
  organizationId: number;
  timeZone: string;
  /** Every listed mailbox; those without `view` show locked. */
  mailboxes: readonly Mailbox[];
  /** The roles the caller may see. */
  roles: readonly MailRole[];
  canManage: boolean;
  canCompose: boolean;
  onCommand: (command: PaletteCommand) => void;
}

/**
 * The ⌘K palette (§11.8, D23): mailboxes, roles, mail and actions in one combobox. Mailboxes and
 * roles match on the client; mail is searched on the server after a pause and only ever lists
 * what the caller reads.
 */
export function CommandPalette({
  open,
  onOpenChange,
  onCloseAutoFocus,
  ...props
}: CommandPaletteProps) {
  const t = useT('mailbox');
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        hideCloseButton
        aria-describedby={undefined}
        onCloseAutoFocus={onCloseAutoFocus}
        overlayClassName="bg-[oklch(from_var(--shadow-color)_l_c_h/0.18)] backdrop-blur-[2px]"
        className="top-[140px] max-h-[calc(100vh-180px)] max-w-[min(600px,calc(100vw-2rem))] translate-y-0 gap-0 overflow-hidden rounded-[14px] border-border-main bg-bg-surface p-0 shadow-[0_30px_80px_oklch(from_var(--shadow-color)_l_c_h/0.22)] data-[state=closed]:slide-out-to-top-0 data-[state=open]:slide-in-from-top-0 sm:rounded-[14px]"
      >
        <DialogTitle className="sr-only">{t('palette.title')}</DialogTitle>
        <DialogDescription className="sr-only">{t('palette.description')}</DialogDescription>
        {open ? <PaletteBody {...props} onClose={() => onOpenChange(false)} /> : null}
      </DialogContent>
    </Dialog>
  );
}

function PaletteBody({
  organizationId,
  timeZone,
  mailboxes,
  roles,
  canManage,
  canCompose,
  onCommand,
  onClose,
}: Omit<CommandPaletteProps, 'open' | 'onOpenChange'> & { onClose: () => void }) {
  const t = useT('mailbox');
  const listId = useId();
  const { resolvedTheme, setTheme } = useTheme();
  const [input, setInput] = useState('');
  const [active, setActive] = useState(0);
  const query = input.trim().toLocaleLowerCase();
  const mailQuery = input.trim();
  const searchable = mailQuery !== '' && Array.from(mailQuery).length <= MAIL_SEARCH_MAX_LENGTH;
  const mail = useMailSearch(organizationId, timeZone, searchable ? mailQuery : '');

  const groups = useMemo((): PaletteGroup[] => {
    const choose = (command: PaletteCommand) => () => {
      onClose();
      onCommand(command);
    };
    const result: PaletteGroup[] = [];
    const boxes = matching(
      mailboxes,
      mailbox => [mailbox.address, mailbox.display_name],
      query,
      GROUP_LIMIT
    );
    if (boxes.length > 0) {
      result.push({
        key: 'mailboxes',
        entries: boxes.map(mailbox => ({
          key: `mailbox-${mailbox.id}`,
          label: mailbox.address,
          detail: mailbox.access.view ? mailbox.display_name || undefined : t('palette.locked'),
          icon: mailbox.access.view ? undefined : <Lock className="size-3.5" aria-hidden="true" />,
          disabled: !mailbox.access.view,
          run: choose({ kind: 'mailbox', mailboxId: mailbox.id }),
        })),
      });
    }
    const matchedRoles = matching(roles, role => [role.name], query, GROUP_LIMIT);
    if (matchedRoles.length > 0) {
      result.push({
        key: 'roles',
        entries: matchedRoles.map(role => ({
          key: `role-${role.id}`,
          label: role.name,
          run: choose({ kind: 'role', roleId: role.id }),
        })),
      });
    }
    if (mailQuery !== '') {
      if (!searchable) {
        result.push({ key: 'mail', entries: [], notice: t('palette.mailTooLong') });
      } else if (mail.status === 'done') {
        result.push({
          key: 'mail',
          entries: mail.items.map(item => ({
            key: `mail-${item.id}`,
            label: item.subject || t('palette.noSubject'),
            detail: item.mailbox_address,
            run: choose({ kind: 'mail', item, q: mailQuery }),
          })),
          notice: mail.items.length === 0 ? t('palette.noMail') : undefined,
        });
      } else if (mail.status === 'failed') {
        result.push({ key: 'mail', entries: [], notice: t(resolveMailboxErrorKey(mail.error)) });
      } else {
        result.push({ key: 'mail', entries: [], notice: t('palette.searching') });
      }
    }
    const dark = resolvedTheme === 'dark';
    const actions: PaletteEntry[] = [
      ...(canCompose
        ? [{ key: 'compose', label: t('palette.compose'), run: choose({ kind: 'compose' }) }]
        : []),
      ...PALETTE_VIEWS.map(view => ({
        key: `view-${view}`,
        label: t('palette.goTo', { view: t(`inbox.views.${view}`) }),
        run: choose({ kind: 'view', view }),
      })),
      {
        key: 'theme',
        label: t(dark ? 'palette.lightTheme' : 'palette.darkTheme'),
        run: () => {
          onClose();
          setTheme(dark ? 'light' : 'dark');
        },
      },
      { key: 'help', label: t('palette.shortcuts'), run: choose({ kind: 'help' }) },
      ...(canManage
        ? [
            {
              key: 'manageMailboxes',
              label: t('palette.manageMailboxes'),
              run: choose({ kind: 'manageMailboxes' }),
            },
            {
              key: 'manageRoles',
              label: t('palette.manageRoles'),
              run: choose({ kind: 'manageRoles' }),
            },
          ]
        : []),
    ];
    // Actions are a short fixed list, so all that match show.
    const matchedActions = matching(actions, action => [action.label], query, actions.length);
    if (matchedActions.length > 0) result.push({ key: 'actions', entries: matchedActions });
    return result;
  }, [
    mailboxes,
    roles,
    query,
    mailQuery,
    searchable,
    mail,
    canCompose,
    canManage,
    resolvedTheme,
    setTheme,
    onClose,
    onCommand,
    t,
  ]);

  const selectable = groups.flatMap(group => group.entries).filter(entry => !entry.disabled);
  const activeIndex = selectable.length === 0 ? -1 : Math.min(active, selectable.length - 1);
  const activeEntry = activeIndex < 0 ? null : selectable[activeIndex];
  const optionId = (entry: PaletteEntry) => `${listId}-${entry.key}`;

  const onKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (event.nativeEvent.isComposing) return;
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      if (selectable.length === 0) return;
      const delta = event.key === 'ArrowDown' ? 1 : -1;
      setActive((activeIndex + delta + selectable.length) % selectable.length);
    } else if (event.key === 'Enter') {
      event.preventDefault();
      activeEntry?.run();
    }
  };

  return (
    <>
      <div className="flex items-center gap-2.5 border-b border-border-subtle px-[18px] py-3.5">
        <Search className="size-4 flex-none text-text-muted" aria-hidden="true" />
        <input
          role="combobox"
          aria-expanded="true"
          aria-controls={listId}
          aria-autocomplete="list"
          aria-activedescendant={activeEntry ? optionId(activeEntry) : undefined}
          aria-label={t('palette.label')}
          placeholder={t('palette.placeholder')}
          autoComplete="off"
          spellCheck={false}
          autoFocus
          value={input}
          onChange={event => {
            setInput(event.target.value);
            setActive(0);
          }}
          onKeyDown={onKeyDown}
          className="min-w-0 flex-1 bg-transparent text-[15px] text-text-main outline-none placeholder:text-text-faint"
        />
        <Kbd>Esc</Kbd>
      </div>
      <div
        id={listId}
        role="listbox"
        aria-label={t('palette.label')}
        className="overflow-y-auto p-2"
      >
        {groups.map(group => {
          const headingId = `${listId}-${group.key}`;
          return (
            <div key={group.key} role="group" aria-labelledby={headingId} className="pb-1">
              <div
                id={headingId}
                className="px-2.5 py-1.5 text-[11px] font-medium tracking-[0.06em] text-text-faint uppercase"
              >
                {t(`palette.groups.${group.key}`)}
              </div>
              {group.entries.map(entry => {
                const selected = entry === activeEntry;
                return (
                  <div
                    key={entry.key}
                    id={optionId(entry)}
                    role="option"
                    aria-selected={selected}
                    aria-disabled={entry.disabled || undefined}
                    onMouseMove={() => {
                      if (!entry.disabled) setActive(selectable.indexOf(entry));
                    }}
                    onClick={() => {
                      if (!entry.disabled) entry.run();
                    }}
                    className={cn(
                      'flex items-center gap-2.5 rounded-[8px] px-2.5 py-[9px] text-[13px]',
                      entry.disabled
                        ? 'cursor-not-allowed text-text-faint'
                        : 'cursor-pointer text-text-main',
                      selected && 'bg-brand-subtle'
                    )}
                  >
                    {entry.icon}
                    <span className="min-w-0 flex-1 truncate">{entry.label}</span>
                    {entry.detail ? (
                      <span className="max-w-[45%] truncate text-xs text-text-muted">
                        {entry.detail}
                      </span>
                    ) : null}
                    {selected ? (
                      <Kbd aria-hidden="true">
                        <CornerDownLeft className="size-3" />
                      </Kbd>
                    ) : null}
                  </div>
                );
              })}
              {group.notice ? (
                <p className="px-2.5 py-[9px] text-[13px] text-text-muted">{group.notice}</p>
              ) : null}
            </div>
          );
        })}
        {groups.length === 0 ? (
          <p className="px-2.5 py-[9px] text-[13px] text-text-muted">{t('palette.empty')}</p>
        ) : null}
      </div>
    </>
  );
}
