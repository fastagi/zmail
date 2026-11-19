'use client';

import { useState } from 'react';
import { ShieldCheck, Trash2 } from 'lucide-react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { CreateMailboxForm } from '@/features/mailbox/components/create-mailbox-form';
import { hasAccessPanel } from '@/features/mailbox/components/mailbox-access-panel';
import { MailboxMarks } from '@/features/mailbox/components/mailbox-switcher';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { ConfirmDialog } from '@/features/mailbox/components/mail-shared';
import { useDeleteMailbox } from '@/features/mailbox/hooks/use-mailbox';
import type { Mailbox, MailboxDomain } from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

/**
 * Mailbox administration, kept out of the reading workspace: every mailbox of the organization
 * (also the ones the caller only runs and cannot read), its access, deleting it, and creating a
 * new one. Opened by owners and admins from the switcher, or from the empty workspace.
 */
export function ManageMailboxesDialog({
  open,
  mailboxes,
  domain,
  domains,
  onClose,
  onCreated,
  onOpenAccess,
}: {
  open: boolean;
  mailboxes: readonly Mailbox[];
  domain: MailboxDomain | null;
  domains: MailboxDomain[];
  onClose: () => void;
  onCreated: (mailbox: Mailbox) => void;
  onOpenAccess: (mailbox: Mailbox) => void;
}) {
  const t = useT('mailbox');
  const [deleteTarget, setDeleteTarget] = useState<Mailbox | null>(null);

  return (
    <Dialog open={open} onOpenChange={next => !next && onClose()}>
      <DialogContent size="lg" className="overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{t('manage.title')}</DialogTitle>
          <DialogDescription>{t('manage.description')}</DialogDescription>
        </DialogHeader>

        <section className="space-y-2" aria-labelledby="manage-mailboxes-list">
          <div className="flex items-baseline justify-between gap-3">
            <h3 id="manage-mailboxes-list" className="text-sm font-semibold">
              {t('mailboxes.title')}
            </h3>
            {domain ? (
              <span className="text-xs text-muted-foreground">
                {t('mailboxes.count', { count: mailboxes.length, max: domain.max_mailboxes })}
              </span>
            ) : null}
          </div>
          {mailboxes.length === 0 ? (
            <p className="text-sm text-muted-foreground">{t('mailboxes.empty')}</p>
          ) : (
            <ul className="divide-y rounded-md border" aria-labelledby="manage-mailboxes-list">
              {mailboxes.map(mailbox => (
                <li key={mailbox.id} className="flex items-center gap-2 px-3 py-2">
                  <span className="min-w-0 flex-1">
                    <span className="flex items-center gap-2">
                      <span className="truncate text-sm font-medium">{mailbox.address}</span>
                      <MailboxMarks mailbox={mailbox} />
                    </span>
                    <span className="block truncate text-xs text-muted-foreground">
                      {mailbox.display_name || t('mailboxes.noDisplayName')}
                    </span>
                  </span>
                  {hasAccessPanel(mailbox) ? (
                    <Button
                      type="button"
                      size="sm"
                      variant="outline"
                      icon={<ShieldCheck className="size-4" />}
                      aria-label={t('access.openLabel', { address: mailbox.address })}
                      onClick={() => onOpenAccess(mailbox)}
                    >
                      {t('access.open')}
                    </Button>
                  ) : null}
                  {mailbox.access.administer ? (
                    <Button
                      type="button"
                      isIcon
                      size="sm"
                      variant="ghost"
                      aria-label={t('deleteMailbox.label', { address: mailbox.address })}
                      title={t('deleteMailbox.label', { address: mailbox.address })}
                      onClick={() => setDeleteTarget(mailbox)}
                    >
                      <Trash2 className="size-4" />
                    </Button>
                  ) : null}
                </li>
              ))}
            </ul>
          )}
        </section>

        {domain ? (
          <CreateMailboxForm
            domain={domain}
            domains={domains}
            count={mailboxes.length}
            onCreated={onCreated}
          />
        ) : null}

        <DeleteMailboxDialog mailbox={deleteTarget} onClose={() => setDeleteTarget(null)} />
      </DialogContent>
    </Dialog>
  );
}

function DeleteMailboxDialog({
  mailbox,
  onClose,
}: {
  mailbox: Mailbox | null;
  onClose: () => void;
}) {
  const t = useT('mailbox');
  const remove = useDeleteMailbox(useMailOrganizationId());

  const confirm = async () => {
    if (!mailbox) return;
    try {
      await remove.mutateAsync(mailbox.id);
      toast.success(t('messages.mailboxDeleted'));
      onClose();
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
  };

  return (
    <ConfirmDialog
      open={mailbox !== null}
      title={t('deleteMailbox.title')}
      description={t('deleteMailbox.description', { address: mailbox?.address ?? '' })}
      confirmLabel={t('actions.delete')}
      cancelLabel={t('actions.cancel')}
      pending={remove.isPending}
      icon={<Trash2 className="size-4" />}
      onConfirm={() => void confirm()}
      onCancel={onClose}
    />
  );
}
