'use client';

import { useState } from 'react';
import { MailOpen, Reply, Trash2 } from 'lucide-react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { useDeleteMailMessage, useUpdateMailMessage } from '@/features/mailbox/hooks/use-mailbox';
import type { MailMessage } from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

/**
 * Reply, mark unread, and delete for an open message. Reply and mark unread exist only for
 * inbound mail. Marking unread and deleting both leave the message, so they call `onClose`.
 */
export function MessageActions({
  message,
  onReply,
  onClose,
}: {
  message: MailMessage;
  onReply: (message: MailMessage) => void;
  onClose: () => void;
}) {
  const t = useT('mailbox');
  const update = useUpdateMailMessage(message.mailbox_id);
  const remove = useDeleteMailMessage(message.mailbox_id);
  const [confirmDelete, setConfirmDelete] = useState(false);

  const markUnread = async () => {
    try {
      await update.mutateAsync({ messageId: message.id, read: false });
      onClose();
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
  };

  const confirmRemove = async () => {
    try {
      await remove.mutateAsync(message.id);
      toast.success(t('messages.messageDeleted'));
      onClose();
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
  };

  return (
    <div className="flex flex-wrap gap-2">
      {message.direction === 'inbound' ? (
        <>
          <Button
            type="button"
            size="sm"
            variant="outline"
            icon={<Reply className="size-4" />}
            onClick={() => onReply(message)}
          >
            {t('actions.reply')}
          </Button>
          <Button
            type="button"
            size="sm"
            variant="outline"
            loading={update.isPending}
            icon={<MailOpen className="size-4" />}
            onClick={() => void markUnread()}
          >
            {t('actions.markUnread')}
          </Button>
        </>
      ) : null}
      <Button
        type="button"
        size="sm"
        variant="outline"
        icon={<Trash2 className="size-4" />}
        onClick={() => setConfirmDelete(true)}
      >
        {t('actions.delete')}
      </Button>

      <Dialog
        open={confirmDelete}
        onOpenChange={open => !open && !remove.isPending && setConfirmDelete(false)}
      >
        <DialogContent size="sm">
          <DialogHeader>
            <DialogTitle>{t('deleteMessage.title')}</DialogTitle>
            <DialogDescription>
              {t('deleteMessage.description', {
                subject: message.subject || t('list.noSubject'),
              })}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={remove.isPending}
              onClick={() => setConfirmDelete(false)}
            >
              {t('actions.cancel')}
            </Button>
            <Button
              type="button"
              variant="destructive"
              loading={remove.isPending}
              icon={<Trash2 className="size-4" />}
              onClick={() => void confirmRemove()}
            >
              {t('actions.delete')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
