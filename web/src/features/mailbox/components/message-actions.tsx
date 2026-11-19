'use client';

import { useState } from 'react';
import { Forward, MailOpen, Reply, ReplyAll, Trash2 } from 'lucide-react';
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
import { replyAllRecipients } from '@/features/mailbox/components/compose-dialog';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { useDeleteMailMessage, useUpdateMailMessage } from '@/features/mailbox/hooks/use-mailbox';
import type { MailMessage } from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

/**
 * Reply, reply all, forward, mark unread, and delete for an open message. Reply, reply all, and
 * mark unread exist only for inbound mail; forward exists for any message when `onForward` is
 * given. Reply, reply all, forward, and delete need `send` on the mailbox, marking unread only
 * `view`. Marking unread and deleting both leave the message, so they call `onClose`.
 */
export function MessageActions({
  message,
  mailboxAddress,
  canSend,
  onReply,
  onReplyAll,
  onForward,
  onClose,
}: {
  message: MailMessage;
  mailboxAddress?: string;
  /** The mailbox's `access.send`; without it there is no reply, forward, or delete. */
  canSend: boolean;
  onReply: (message: MailMessage) => void;
  onReplyAll?: (message: MailMessage) => void;
  onForward?: (message: MailMessage) => void;
  onClose: () => void;
}) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const update = useUpdateMailMessage(organizationId, message.mailbox_id);
  const remove = useDeleteMailMessage(organizationId, message.mailbox_id);
  const [confirmDelete, setConfirmDelete] = useState(false);

  const recipients = replyAllRecipients(message, mailboxAddress);
  const canReplyAll =
    canSend &&
    message.direction === 'inbound' &&
    onReplyAll !== undefined &&
    message.to.length + message.cc.length > 1 &&
    (recipients.cc.length > 0 || recipients.to[0] !== message.from.address.trim().toLowerCase());

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
      {canSend && message.direction === 'inbound' ? (
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
          {canReplyAll ? (
            <Button
              type="button"
              size="sm"
              variant="outline"
              icon={<ReplyAll className="size-4" />}
              onClick={() => onReplyAll(message)}
            >
              {t('actions.replyAll')}
            </Button>
          ) : null}
        </>
      ) : null}
      {canSend && onForward ? (
        <Button
          type="button"
          size="sm"
          variant="outline"
          icon={<Forward className="size-4" />}
          onClick={() => onForward(message)}
        >
          {t('actions.forward')}
        </Button>
      ) : null}
      {message.direction === 'inbound' ? (
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
      ) : null}
      {canSend ? (
        <Button
          type="button"
          size="sm"
          variant="outline"
          icon={<Trash2 className="size-4" />}
          onClick={() => setConfirmDelete(true)}
        >
          {t('actions.delete')}
        </Button>
      ) : null}

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
