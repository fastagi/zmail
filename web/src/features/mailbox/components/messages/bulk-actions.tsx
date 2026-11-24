'use client';

import { useState, type ReactNode } from 'react';
import {
  Archive,
  ArchiveRestore,
  FolderInput,
  Inbox,
  MailCheck,
  MailOpen,
  Tag,
  Trash2,
} from 'lucide-react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { ConfirmDialog } from '@/features/mailbox/components/confirm-dialog';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { LabelDot, type MailView } from '@/features/mailbox/components/mailbox-sidebar';
import { toastWithUndo } from '@/features/mailbox/components/message-actions';
import { rowMessageIds } from '@/features/mailbox/components/message-rows';
import {
  origins,
  rowLabelIds,
  updateInChunks,
  useUndoMove,
} from '@/features/mailbox/components/messages/message-list-helpers';
import { useDeleteMailMessage, useUpdateMailMessages } from '@/features/mailbox/hooks/use-mailbox';
import type {
  MailFolder,
  MailLabel,
  MailMessageSummary,
  Mailbox,
  UpdateMailMessagesInput,
} from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/**
 * What may be done to the selected rows. `view` marks read and unread; `send` moves and labels;
 * in the trash `send` restores and `organize` deletes for good.
 */
export function BulkActions({
  mailbox,
  view,
  q,
  rows,
  folders,
  labels,
  onDone,
}: {
  mailbox: Mailbox;
  view: MailView;
  q: string;
  rows: MailMessageSummary[];
  folders: readonly MailFolder[];
  labels: readonly MailLabel[];
  onDone: (moved: boolean) => void;
}) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const updateMany = useUpdateMailMessages(organizationId, mailbox.id);
  const remove = useDeleteMailMessage(organizationId, mailbox.id);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const ids = [...new Set(rows.flatMap(rowMessageIds))];
  const canSend = mailbox.access.send;
  const inTrash = view.kind === 'trash' && !q;

  const undoMove = useUndoMove(mailbox.id);
  const apply = async (
    change: Omit<UpdateMailMessagesInput, 'message_ids'>,
    done: string | null,
    moved: boolean
  ) => {
    // Taken before the change: the rows still say where they are.
    const before = origins(view, rows);
    try {
      await updateInChunks(updateMany.mutateAsync, ids, change);
      // A move out of the view can be undone; restoring from the trash is itself the undo.
      if (done && moved && !inTrash) toastWithUndo(done, t('actions.undo'), () => undoMove(before));
      else if (done) toast.success(done);
      onDone(moved);
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
  };

  const deleteForever = async () => {
    setDeleting(true);
    try {
      for (const id of ids) await remove.mutateAsync(id);
      toast.success(t('messages.deletedForever'));
      setConfirmDelete(false);
      onDone(true);
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    } finally {
      setDeleting(false);
    }
  };

  const busy = updateMany.isPending || deleting;
  const action = (label: string, icon: ReactNode, onClick: () => void) => (
    <Button type="button" size="xs" variant="outline" disabled={busy} icon={icon} onClick={onClick}>
      {label}
    </Button>
  );
  const iconClass = 'size-3.5';
  const otherFolders = folders.filter(folder => !(view.kind === 'folder' && view.id === folder.id));

  return (
    <div
      className="flex flex-wrap items-center gap-1.5"
      role="toolbar"
      aria-label={t('list.selected', { count: rows.length })}
    >
      <span className="mr-1 text-xs text-muted-foreground">
        {t('list.selected', { count: rows.length })}
      </span>
      {action(
        t('actions.markRead'),
        <MailCheck className={iconClass} />,
        () => void apply({ read: true }, null, false)
      )}
      {action(
        t('actions.markUnread'),
        <MailOpen className={iconClass} />,
        () => void apply({ read: false }, null, false)
      )}
      {canSend && !inTrash ? (
        <>
          {(view.kind === 'archive' || view.kind === 'folder') && !q
            ? action(
                t('actions.moveToInbox'),
                <Inbox className={iconClass} />,
                () => void apply({ placement: 'default' }, t('messages.movedToInbox'), true)
              )
            : action(
                t('actions.archive'),
                <Archive className={iconClass} />,
                () => void apply({ placement: 'archive' }, t('messages.archived'), true)
              )}
          {otherFolders.length > 0 ? (
            <ChoiceMenu
              label={t('actions.moveToFolder')}
              icon={<FolderInput className={iconClass} />}
              disabled={busy}
            >
              {close =>
                otherFolders.map(folder => (
                  <MenuButton
                    key={folder.id}
                    onClick={() => {
                      close();
                      void apply(
                        { placement: 'folder', folder_id: folder.id },
                        t('messages.movedToFolder', { folder: folder.name }),
                        true
                      );
                    }}
                  >
                    {folder.name}
                  </MenuButton>
                ))
              }
            </ChoiceMenu>
          ) : null}
          {labels.length > 0 ? (
            <ChoiceMenu
              label={t('actions.label')}
              icon={<Tag className={iconClass} />}
              disabled={busy}
            >
              {close =>
                labels.map(label => {
                  const onAll = rows.every(row => rowLabelIds(row).includes(label.id));
                  return (
                    <MenuButton
                      key={label.id}
                      pressed={onAll}
                      onClick={() => {
                        close();
                        void apply(
                          onAll ? { remove_label_ids: [label.id] } : { add_label_ids: [label.id] },
                          t('messages.labelsUpdated'),
                          false
                        );
                      }}
                    >
                      <LabelDot color={label.color} />
                      {label.name}
                    </MenuButton>
                  );
                })
              }
            </ChoiceMenu>
          ) : null}
          {action(
            t('actions.delete'),
            <Trash2 className={iconClass} />,
            () => void apply({ placement: 'trash' }, t('messages.trashed'), true)
          )}
        </>
      ) : null}
      {canSend && inTrash
        ? action(
            t('actions.restore'),
            <ArchiveRestore className={iconClass} />,
            () => void apply({ placement: 'default' }, t('messages.restored'), true)
          )
        : null}
      {mailbox.access.organize && inTrash
        ? action(t('actions.deleteForever'), <Trash2 className={iconClass} />, () =>
            setConfirmDelete(true)
          )
        : null}
      <ConfirmDialog
        open={confirmDelete}
        title={t('deleteForever.title')}
        description={t('deleteForever.description', { count: ids.length })}
        confirmLabel={t('actions.deleteForever')}
        cancelLabel={t('actions.cancel')}
        pending={deleting}
        icon={<Trash2 className="size-4" />}
        onCancel={() => setConfirmDelete(false)}
        onConfirm={() => void deleteForever()}
      />
    </div>
  );
}

function ChoiceMenu({
  label,
  icon,
  disabled,
  children,
}: {
  label: string;
  icon: ReactNode;
  disabled: boolean;
  children: (close: () => void) => ReactNode;
}) {
  const [open, setOpen] = useState(false);
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button type="button" size="xs" variant="outline" disabled={disabled} icon={icon}>
          {label}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-56 p-1">
        <div role="group" aria-label={label} className="flex max-h-72 flex-col overflow-y-auto">
          {children(() => setOpen(false))}
        </div>
      </PopoverContent>
    </Popover>
  );
}

function MenuButton({
  pressed,
  onClick,
  children,
}: {
  pressed?: boolean;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      aria-pressed={pressed}
      className={cn(
        'flex items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-sm focus-ring hover:bg-accent',
        pressed && 'font-medium'
      )}
      onClick={onClick}
    >
      {children}
    </button>
  );
}
