'use client';

import { useState } from 'react';
import {
  Archive,
  ArchiveRestore,
  Forward,
  MailOpen,
  Reply,
  ReplyAll,
  Star,
  Trash2,
} from 'lucide-react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import { replyAllRecipients } from '@/features/mailbox/components/compose-drafts';
import { ConfirmDialog } from '@/features/mailbox/components/mail-shared';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { useDeleteMailMessage, useUpdateMailMessage } from '@/features/mailbox/hooks/use-mailbox';
import type { MailMessage, MailPlacement } from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/** Where a message was, so a move can be undone. */
export interface MailOrigin {
  placement: MailPlacement;
  folder_id?: number;
}

export function originOf(message: {
  placement: MailPlacement;
  folder_id: number | null;
}): MailOrigin {
  return message.placement === 'folder' && message.folder_id !== null
    ? { placement: 'folder', folder_id: message.folder_id }
    : { placement: message.placement };
}

/** How long a move can be undone from its toast. */
export const UNDO_TOAST_MS = 5000;

/** A success toast for a move, with an Undo action that runs `undo` for 5 seconds. */
export function toastWithUndo(text: string, undoLabel: string, undo: () => Promise<unknown>) {
  toast.success(text, {
    duration: UNDO_TOAST_MS,
    action: { label: undoLabel, onClick: () => void undo() },
  });
}

/**
 * Actions on one open message.
 *
 * - Reply, reply all, and mark unread exist only for inbound mail; forward for any message when
 *   `onForward` is given. Reply, reply all, and forward need `send`.
 * - Star needs only `view`: a star is the caller's own.
 * - Outside the trash, `send` archives the message and deletes it, which moves it to the trash
 *   with a placement update (never `DELETE`, which would delete for good a message another member
 *   already trashed).
 * - In the trash, `send` restores it to the Inbox or Sent, and `organize` deletes it for good after
 *   a confirmation.
 *
 * Every action that takes the message out of the open view calls `onClose`.
 */
export function MessageActions({
  message,
  mailboxAddress,
  canSend,
  canOrganize = false,
  onReply,
  onReplyAll,
  onForward,
  onClose,
}: {
  message: MailMessage;
  mailboxAddress?: string;
  /** The mailbox's `access.send`; without it there is no reply, forward, move, or delete. */
  canSend: boolean;
  /** The mailbox's `access.organize`; needed to delete a message in the trash for good. */
  canOrganize?: boolean;
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
  const inTrash = message.placement === 'trash';

  const recipients = replyAllRecipients(message, mailboxAddress);
  const canReplyAll =
    canSend &&
    message.direction === 'inbound' &&
    onReplyAll !== undefined &&
    message.to.length + message.cc.length > 1 &&
    (recipients.cc.length > 0 || recipients.to[0] !== message.from.address.trim().toLowerCase());

  /** Runs a change; on success shows `done` (when given) and leaves the message when `leave`. */
  const run = async (action: () => Promise<unknown>, done: string | null, leave: boolean) => {
    try {
      await action();
      if (done) toast.success(done);
      if (leave) onClose();
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
  };

  const iconButton = 'text-muted-foreground hover:text-foreground';
  const origin = originOf(message);
  const undo = () =>
    update.mutateAsync({ messageId: message.id, ...origin }).catch((failure: unknown) => {
      toast.error(t(resolveMailboxErrorKey(failure)));
    });
  /** Archives or trashes the message, then offers to put it back where it was. */
  const move = async (action: () => Promise<unknown>, done: string) => {
    try {
      await action();
      toastWithUndo(done, t('actions.undo'), undo);
      onClose();
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
  };

  return (
    <div className="flex flex-wrap items-center gap-1.5">
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
          variant="ghost"
          // Only marking unread spins here; a star shows at once on its own button.
          loading={update.isPending && update.variables?.read !== undefined}
          icon={<MailOpen className="size-4" />}
          onClick={() =>
            void run(() => update.mutateAsync({ messageId: message.id, read: false }), null, true)
          }
        >
          {t('actions.markUnread')}
        </Button>
      ) : null}
      <Button
        type="button"
        isIcon
        size="sm"
        variant="ghost"
        aria-pressed={message.starred}
        aria-label={t(message.starred ? 'list.unstar' : 'list.star')}
        title={t(message.starred ? 'list.unstar' : 'list.star')}
        className={cn(message.starred ? 'text-warning hover:text-warning' : iconButton)}
        onClick={() =>
          void run(
            () => update.mutateAsync({ messageId: message.id, starred: !message.starred }),
            null,
            false
          )
        }
      >
        <Star className={cn('size-4', message.starred && 'fill-current')} />
      </Button>
      {canSend && !inTrash && message.placement !== 'archive' ? (
        <Button
          type="button"
          isIcon
          size="sm"
          variant="ghost"
          aria-label={t('actions.archive')}
          title={t('actions.archive')}
          className={iconButton}
          onClick={() =>
            void move(
              () => update.mutateAsync({ messageId: message.id, placement: 'archive' }),
              t('messages.archived')
            )
          }
        >
          <Archive className="size-4" />
        </Button>
      ) : null}
      {canSend && !inTrash ? (
        <Button
          type="button"
          isIcon
          size="sm"
          variant="ghost"
          aria-label={t('actions.delete')}
          title={t('actions.delete')}
          className={iconButton}
          onClick={() =>
            void move(
              // Always a move: DELETE would delete for good a message someone already trashed.
              () => update.mutateAsync({ messageId: message.id, placement: 'trash' }),
              t('messages.trashed')
            )
          }
        >
          <Trash2 className="size-4" />
        </Button>
      ) : null}
      {canSend && inTrash ? (
        <Button
          type="button"
          size="sm"
          variant="outline"
          icon={<ArchiveRestore className="size-4" />}
          onClick={() =>
            void run(
              () => update.mutateAsync({ messageId: message.id, placement: 'default' }),
              t('messages.restored'),
              true
            )
          }
        >
          {t('actions.restore')}
        </Button>
      ) : null}
      {canOrganize && inTrash ? (
        <Button
          type="button"
          size="sm"
          variant="outline"
          icon={<Trash2 className="size-4" />}
          onClick={() => setConfirmDelete(true)}
        >
          {t('actions.deleteForever')}
        </Button>
      ) : null}

      <ConfirmDialog
        open={confirmDelete}
        title={t('deleteForever.title')}
        description={t('deleteForever.description', { count: 1 })}
        confirmLabel={t('actions.deleteForever')}
        cancelLabel={t('actions.cancel')}
        pending={remove.isPending}
        icon={<Trash2 className="size-4" />}
        onCancel={() => setConfirmDelete(false)}
        onConfirm={() =>
          void run(
            async () => {
              await remove.mutateAsync(message.id);
              setConfirmDelete(false);
            },
            t('messages.deletedForever'),
            true
          )
        }
      />
    </div>
  );
}
