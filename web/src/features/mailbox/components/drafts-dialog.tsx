'use client';

import { Trash2 } from 'lucide-react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import type { MailDraftSummary } from '@/features/mailbox/draft-schemas';
import { useDeleteMailDraft, useMailDrafts } from '@/features/mailbox/hooks/use-mail-drafts';
import { useMailDateTimeFormat } from '@/features/mailbox/components/mail-time';
import type { Mailbox } from '@/features/mailbox/types';
import { useT } from '@/i18n';

/** The caller's saved drafts; choosing one continues it in the composer. */
export default function DraftsDialog({
  organizationId,
  mailboxes,
  open,
  onOpenChange,
  onOpenDraft,
}: {
  organizationId: number;
  mailboxes: readonly Pick<Mailbox, 'id' | 'address'>[];
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onOpenDraft: (draft: MailDraftSummary) => void;
}) {
  const t = useT('mailbox');
  const drafts = useMailDrafts(organizationId, open);
  const remove = useDeleteMailDraft(organizationId);
  const formatter = useMailDateTimeFormat();
  const addressOf = new Map(mailboxes.map(mailbox => [mailbox.id, mailbox.address]));

  const deleteDraft = (draft: MailDraftSummary) => {
    remove.mutate(draft.id, {
      onSuccess: () => toast.success(t('drafts.deleted')),
      onError: () => toast.error(t('drafts.deleteFailed')),
    });
  };

  let content;
  if (drafts.isPending) {
    content = <p className="py-6 text-center text-text-muted">{t('drafts.loading')}</p>;
  } else if (drafts.isError) {
    content = (
      <div className="flex flex-col items-center gap-3 py-6 text-center text-text-muted">
        <p role="alert">{t('drafts.loadFailed')}</p>
        <Button variant="outline" size="sm" onClick={() => void drafts.refetch()}>
          {t('drafts.retry')}
        </Button>
      </div>
    );
  } else if (drafts.data.length === 0) {
    content = <p className="py-6 text-center text-text-muted">{t('drafts.empty')}</p>;
  } else {
    content = (
      <ul className="-mx-2 flex flex-col">
        {drafts.data.map(draft => {
          const subject = draft.subject.trim() || t('drafts.noSubject');
          const preview = draft.preview;
          return (
            <li
              key={draft.id}
              className="group flex items-center gap-1 rounded-[9px] hover:bg-nav-hover"
            >
              <button
                type="button"
                className="min-w-0 flex-1 rounded-[9px] px-2 py-2 text-left focus-ring"
                onClick={() => onOpenDraft(draft)}
              >
                <span className="flex items-baseline gap-2">
                  <span className="min-w-0 flex-1 truncate font-medium text-text-main">
                    {subject}
                  </span>
                  <time
                    dateTime={draft.updated_at}
                    className="shrink-0 text-xs text-text-faint tabular-nums"
                  >
                    {formatter.format(new Date(draft.updated_at))}
                  </time>
                </span>
                <span className="block truncate text-xs text-text-muted">
                  {t('drafts.toLine', { to: draft.to || t('drafts.noRecipients') })}
                  {addressOf.has(draft.mailbox_id)
                    ? ` · ${t('drafts.fromLine', { from: addressOf.get(draft.mailbox_id) ?? '' })}`
                    : null}
                </span>
                {preview ? (
                  <span className="block truncate text-xs text-text-faint">{preview}</span>
                ) : null}
              </button>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                isIcon
                aria-label={t('drafts.delete', { subject })}
                title={t('drafts.delete', { subject })}
                disabled={remove.isPending && remove.variables === draft.id}
                onClick={() => deleteDraft(draft)}
              >
                <Trash2 className="size-4" aria-hidden="true" />
              </Button>
            </li>
          );
        })}
      </ul>
    );
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t('drafts.title')}</DialogTitle>
          <DialogDescription>{t('drafts.description')}</DialogDescription>
        </DialogHeader>
        <DialogBody className="max-h-[60vh] overflow-y-auto">{content}</DialogBody>
      </DialogContent>
    </Dialog>
  );
}
