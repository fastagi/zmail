'use client';

import { Trash2 } from 'lucide-react';
import { toast } from 'sonner';

import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { ConfirmDialog } from '@/features/mailbox/components/confirm-dialog';
import { useDeleteMailbox } from '@/features/mailbox/hooks/use-mailbox';
import type { MailboxDirectoryEntry } from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

/** Deletes a mailbox and its mail after confirmation; the address is never reused. */
export function DeleteMailboxDialog({
  mailbox,
  onClose,
  onDeleted,
}: {
  mailbox: Pick<MailboxDirectoryEntry, 'id' | 'address'> | null;
  onClose: () => void;
  onDeleted?: () => void;
}) {
  const t = useT('mailbox');
  const remove = useDeleteMailbox(useMailOrganizationId());

  const confirm = async () => {
    if (!mailbox) return;
    try {
      await remove.mutateAsync(mailbox.id);
      toast.success(t('messages.mailboxDeleted'));
      onDeleted?.();
      onClose();
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
  };

  return (
    <ConfirmDialog
      open={mailbox !== null}
      title={t('admin.deleteDialog.title', { address: mailbox?.address ?? '' })}
      description={t('admin.deleteDialog.description')}
      confirmLabel={t('actions.delete')}
      cancelLabel={t('actions.cancel')}
      pending={remove.isPending}
      icon={<Trash2 className="size-4" />}
      onConfirm={() => void confirm()}
      onCancel={onClose}
    />
  );
}
