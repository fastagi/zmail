'use client';

import { NamedItemsDialog } from '@/features/mailbox/components/manage-folders-dialog';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import {
  useCreateMailLabel,
  useDeleteMailLabel,
  useUpdateMailLabel,
} from '@/features/mailbox/hooks/use-mailbox';
import type { MailLabel } from '@/features/mailbox/types';

/** The labels of a mailbox, each with a color of the fixed palette. */
export function ManageLabelsDialog({
  mailboxId,
  labels,
  open,
  onClose,
}: {
  mailboxId: number;
  labels: readonly MailLabel[];
  open: boolean;
  onClose: () => void;
}) {
  const organizationId = useMailOrganizationId();
  const create = useCreateMailLabel(organizationId, mailboxId);
  const update = useUpdateMailLabel(organizationId, mailboxId);
  const remove = useDeleteMailLabel(organizationId, mailboxId);
  return (
    <NamedItemsDialog
      open={open}
      items={labels}
      copy={{
        title: 'labelManager.title',
        description: 'labelManager.description',
        name: 'labelManager.name',
        newName: 'labelManager.newName',
        add: 'labelManager.add',
        empty: 'labelManager.empty',
        rename: 'labelManager.rename',
        delete: 'labelManager.delete',
        confirmDelete: 'labelManager.confirmDelete',
        saved: 'labelManager.saved',
        deleted: 'labelManager.deleted',
      }}
      withColor
      pending={create.isPending || update.isPending || remove.isPending}
      onCreate={input => create.mutateAsync({ name: input.name, color: input.color ?? 'blue' })}
      onUpdate={(labelId, input) =>
        update.mutateAsync({ labelId, name: input.name, color: input.color ?? 'blue' })
      }
      onDelete={labelId => remove.mutateAsync(labelId)}
      onClose={onClose}
    />
  );
}
