'use client';

import { useEffect, useRef } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { ArrowLeft, Archive, Info, Trash2 } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { rowMessageIds, rowUnread } from '@/features/mailbox/components/message-list';
import {
  MessageDetail,
  ThreadView,
  type ReaderProps,
} from '@/features/mailbox/components/thread-view';
import { markInboxRowRead, useUpdateInboxMessages } from '@/features/mailbox/hooks/use-inbox';
import {
  useMailMessage,
  useMailThread,
  useMarkMailMessageRead,
} from '@/features/mailbox/hooks/use-mailbox';
import type { InboxItem, Mailbox } from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { useT } from '@/i18n';

/** What the reading pane does besides the row's own mailbox: moving through the list, replying. */
export type InboxReaderNavigation = Omit<
  ReaderProps,
  'mailboxId' | 'mailboxAddress' | 'canSend' | 'canOrganize'
>;

/**
 * The open mail is gone for the caller: its mailbox is no longer readable (the API answers a
 * mailbox it cannot read as not found), or the message itself left the mailbox.
 */
function isMailGone(error: unknown): boolean {
  return (
    error instanceof ApiError &&
    (error.errorCode === ApiErrorCode.MAILBOX_NOT_FOUND ||
      error.errorCode === ApiErrorCode.MAILBOX_MESSAGE_NOT_FOUND)
  );
}

/**
 * The open row of the unified inbox, read in its own mailbox: a thread row shows the thread, a
 * single row the message. Opening unread mail marks it read, shown at once in the list and put
 * back if the request fails. Archive and Trash act on every message the row stands for and need
 * the mailbox's `send`. When the mailbox stopped being readable since the list loaded, the pane
 * says so and `onUnavailable` refreshes what is listed, once per row.
 *
 * `access` is the row's mailbox access from the mailbox list, null when it is no longer listed.
 */
export function InboxReadingPane({
  item,
  access,
  navigation,
  onUnavailable,
}: {
  item: InboxItem;
  access: Mailbox['access'] | null;
  navigation: InboxReaderNavigation;
  onUnavailable: () => void;
}) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const queryClient = useQueryClient();
  const markRead = useMarkMailMessageRead(organizationId);
  const updateInbox = useUpdateInboxMessages(organizationId);
  const readable = access?.view === true;
  const thread = useMailThread(
    organizationId,
    item.mailbox_id,
    readable && item.thread ? item.thread_id : null
  );
  const detail = useMailMessage(
    organizationId,
    item.mailbox_id,
    readable && !item.thread ? item.id : null
  );
  const gone = isMailGone(item.thread ? thread.error : detail.error);
  const marked = useRef<number | null>(null);
  const reported = useRef<number | null>(null);

  useEffect(() => {
    if (!readable || marked.current === item.id || !rowUnread(item)) return;
    marked.current = item.id;
    const rollback = markInboxRowRead(queryClient, organizationId, item.id);
    const onError = (failure: unknown) => {
      rollback();
      // Mail that is gone has its own notice in the pane.
      if (!isMailGone(failure)) toast.error(t(resolveMailboxErrorKey(failure)));
    };
    const ids = rowMessageIds(item);
    if (ids.length === 1) {
      markRead.mutate({ mailboxId: item.mailbox_id, messageId: ids[0] }, { onError });
    } else {
      updateInbox.mutate({ mailboxId: item.mailbox_id, message_ids: ids, read: true }, { onError });
    }
  }, [item, readable, queryClient, organizationId, markRead, updateInbox, t]);

  useEffect(() => {
    if (!gone || reported.current === item.id) return;
    reported.current = item.id;
    onUnavailable();
  }, [gone, item.id, onUnavailable]);

  if (!readable || gone) {
    return (
      <div className="space-y-3">
        <Button
          type="button"
          size="sm"
          variant="ghost"
          icon={<ArrowLeft className="size-4" />}
          onClick={navigation.onClose}
        >
          {t('actions.back')}
        </Button>
        <Alert>
          <Info className="size-4" aria-hidden="true" />
          <AlertDescription>{t('inbox.unavailable')}</AlertDescription>
        </Alert>
      </div>
    );
  }

  const move = async (placement: 'archive' | 'trash') => {
    try {
      await updateInbox.mutateAsync({
        mailboxId: item.mailbox_id,
        message_ids: rowMessageIds(item),
        placement,
      });
      toast.success(t(placement === 'archive' ? 'messages.archived' : 'messages.trashed'));
      navigation.onClose();
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
  };
  const reader: ReaderProps = {
    ...navigation,
    mailboxId: item.mailbox_id,
    mailboxAddress: item.mailbox_address,
    canSend: access.send,
    canOrganize: access.organize,
  };

  return (
    <div className="space-y-3">
      {access.send ? (
        <div
          role="toolbar"
          aria-label={t('inbox.actions.label')}
          className="flex flex-wrap items-center gap-2"
        >
          <Button
            type="button"
            size="sm"
            variant="outline"
            icon={<Archive className="size-4" />}
            disabled={updateInbox.isPending}
            onClick={() => void move('archive')}
          >
            {t('inbox.actions.archive')}
          </Button>
          <Button
            type="button"
            size="sm"
            variant="outline"
            icon={<Trash2 className="size-4" />}
            disabled={updateInbox.isPending}
            onClick={() => void move('trash')}
          >
            {t('inbox.actions.trash')}
          </Button>
        </div>
      ) : null}
      {item.thread ? (
        <ThreadView key={item.id} threadId={item.thread_id} {...reader} />
      ) : (
        <MessageDetail key={item.id} messageId={item.id} {...reader} />
      )}
    </div>
  );
}
