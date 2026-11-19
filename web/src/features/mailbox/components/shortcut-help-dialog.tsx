'use client';

import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Kbd } from '@/components/ui/kbd';
import type { MailShortcut } from '@/features/mailbox/hooks/use-mail-shortcuts';
import { useT } from '@/i18n';

/** Each listed shortcut with its key caps, in the order of §11.7. */
const rows: readonly (readonly [Exclude<MailShortcut, 'palette'>, readonly string[]])[] = [
  ['next', ['J', '↓']],
  ['previous', ['K', '↑']],
  ['open', ['↵']],
  ['archive', ['E']],
  ['trash', ['#']],
  ['star', ['S']],
  ['reply', ['R']],
  ['replyAll', ['A']],
  ['forward', ['F']],
  ['compose', ['C']],
  ['search', ['/']],
  ['help', ['?']],
  ['escape', ['Esc']],
];

/** The keys of the unified inbox (§11.7), opened by `?` and the sidebar's Shortcuts link. */
export function ShortcutHelpDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const t = useT('mailbox');
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('inbox.shortcuts.title')}</DialogTitle>
          <DialogDescription>{t('inbox.shortcuts.description')}</DialogDescription>
        </DialogHeader>
        <DialogBody>
          <dl className="divide-y divide-border-subtle text-[13px]">
            {rows.map(([shortcut, keys]) => (
              <div key={shortcut} className="flex items-center justify-between gap-4 py-2">
                <dt className="text-text-subtle">{t(`inbox.shortcuts.${shortcut}`)}</dt>
                <dd className="flex shrink-0 items-center gap-1.5 text-xs text-text-muted">
                  {keys.map((key, index) => (
                    <span key={key} className="flex items-center gap-1.5">
                      {index > 0 ? t('inbox.shortcuts.or') : null}
                      <Kbd>{key}</Kbd>
                    </span>
                  ))}
                </dd>
              </div>
            ))}
          </dl>
        </DialogBody>
      </DialogContent>
    </Dialog>
  );
}
