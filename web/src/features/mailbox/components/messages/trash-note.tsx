'use client';

import { useState } from 'react';
import { Trash2 } from 'lucide-react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import { ConfirmDialog } from '@/features/mailbox/components/confirm-dialog';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { usePurgeMailTrash } from '@/features/mailbox/hooks/use-mailbox';
import type { Mailbox } from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

export function TrashNote({ mailbox }: { mailbox: Mailbox }) {
  const t = useT('mailbox');
  const purge = usePurgeMailTrash(useMailOrganizationId(), mailbox.id);
  const [confirm, setConfirm] = useState(false);
  return (
    <div className="flex flex-wrap items-center justify-between gap-2 rounded-md bg-muted/50 px-3 py-2">
      <p className="text-xs text-muted-foreground">{t('list.trashNote')}</p>
      {mailbox.access.organize ? (
        <>
          <Button
            type="button"
            size="xs"
            variant="outline"
            icon={<Trash2 className="size-3.5" />}
            onClick={() => setConfirm(true)}
          >
            {t('actions.emptyTrash')}
          </Button>
          <ConfirmDialog
            open={confirm}
            title={t('emptyTrash.title')}
            description={t('emptyTrash.description')}
            confirmLabel={t('actions.emptyTrash')}
            cancelLabel={t('actions.cancel')}
            pending={purge.isPending}
            icon={<Trash2 className="size-4" />}
            onCancel={() => setConfirm(false)}
            onConfirm={() =>
              void (async () => {
                try {
                  await purge.mutateAsync();
                  toast.success(t('messages.trashEmptied'));
                  setConfirm(false);
                } catch (failure) {
                  toast.error(t(resolveMailboxErrorKey(failure)));
                }
              })()
            }
          />
        </>
      ) : null}
    </div>
  );
}
