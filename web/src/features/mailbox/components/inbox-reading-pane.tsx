'use client';

import { useEffect, useRef, type ReactNode, type RefObject } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { ArrowLeft, ChevronLeft, ChevronRight, Info } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Kbd } from '@/components/ui/kbd';
import { InboxTriagePanel } from '@/features/mailbox/components/inbox-triage-panel';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { originOf, toastWithUndo } from '@/features/mailbox/components/message-actions';
import { rowMessageIds, rowUnread } from '@/features/mailbox/components/message-list';
import {
  MessageDetail,
  ThreadView,
  type ReaderProps,
} from '@/features/mailbox/components/thread-view';
import {
  markInboxRowRead,
  removeInboxRow,
  useUpdateInboxMessage,
  useUpdateInboxMessages,
} from '@/features/mailbox/hooks/use-inbox';
import {
  useMailMessage,
  useMailThread,
  useMarkMailMessageRead,
} from '@/features/mailbox/hooks/use-mailbox';
import type { InboxItem, MailMessage, Mailbox } from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/**
 * What the reading pane does besides the row's own mailbox: moving through the list, replying, and
 * `onLeave`, which opens the row after `item` (or goes back to the list) once it was archived or
 * deleted.
 */
export type InboxReaderNavigation = Omit<
  ReaderProps,
  'mailboxId' | 'mailboxAddress' | 'canSend' | 'canOrganize'
> & { onLeave: (item: InboxItem) => void };

/**
 * The AI triage block of the pane, given only while the organization's triage is on: the override
 * menu's state, which the `F` key opens, and what "use this draft" does with a reply draft.
 */
export interface InboxTriagePaneProps {
  menuOpen: boolean;
  onMenuOpenChange: (open: boolean) => void;
  onUseDraft: (message: MailMessage, body: string) => void;
}

/** What the keyboard asks of the open row; the caller checks the mailbox's access first. */
export interface InboxPaneActions {
  archive: () => void;
  trash: () => void;
  star: () => void;
  reply: () => void;
  replyAll: () => void;
  forward: () => void;
}

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

/** The newest inbound message, which Reply and Reply all answer; none when all were sent. */
function newestInbound(messages: readonly MailMessage[]): MailMessage | undefined {
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    if (messages[index].direction === 'inbound') return messages[index];
  }
  return undefined;
}

/**
 * The open row of the unified inbox, read in its own mailbox (§11.0, §11.6): a 52px bar, then a
 * 640px article. A thread row shows the thread, a single row the message. Opening unread mail
 * marks it read, shown at once in the list and put back if the request fails. The bar goes back
 * to the list below 1024px, where the list and the pane take turns, and steps to the previous or
 * next row; with the mailbox's `send` it holds Reply, Reply all, and Forward (answering the newest
 * inbound message, forwarding the newest) and Archive and Delete, which act on every message the
 * row stands for. When the mailbox stopped being readable since the list loaded, the pane says so
 * and `onUnavailable` refreshes what is listed, once per row.
 *
 * `access` is the row's mailbox access from the mailbox list, null when it is no longer listed.
 * `roleName` names the mailbox's role in the line above the subject. While the mail can be read,
 * `actionsRef` holds what the keyboard may ask of it; its star stars the row (every message of a
 * thread), shown in the list at once and put back if the request fails.
 */
export function InboxReadingPane({
  item,
  access,
  roleName = null,
  navigation,
  onUnavailable,
  actionsRef,
  triage = null,
}: {
  item: InboxItem;
  access: Mailbox['access'] | null;
  roleName?: string | null;
  navigation: InboxReaderNavigation;
  onUnavailable: () => void;
  actionsRef?: RefObject<InboxPaneActions | null>;
  triage?: InboxTriagePaneProps | null;
}) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const queryClient = useQueryClient();
  const markRead = useMarkMailMessageRead(organizationId);
  const updateInbox = useUpdateInboxMessages(organizationId);
  const updateStar = useUpdateInboxMessage(organizationId);
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
  // Either the mailbox list already knows the view is gone, or the API just said so.
  const unavailable = !readable || gone;
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
    if (!unavailable || reported.current === item.id) return;
    reported.current = item.id;
    onUnavailable();
  }, [unavailable, item.id, onUnavailable]);

  // Rows whose archive or delete is on its way; another row may be moved meanwhile.
  const moving = useRef(new Set<number>());
  /**
   * Archives or deletes the row: it leaves the list at once and the next row opens. A failed
   * request puts the row back; a done one offers for 5 seconds to put every message back where it
   * was.
   */
  const move = async (placement: 'archive' | 'trash') => {
    const target = item;
    if (moving.current.has(target.id)) return;
    moving.current.add(target.id);
    const messageIds = rowMessageIds(target);
    const origin = originOf(target);
    const rollback = removeInboxRow(queryClient, organizationId, target.id);
    navigation.onLeave(target);
    try {
      await updateInbox.mutateAsync({
        mailboxId: target.mailbox_id,
        message_ids: messageIds,
        placement,
      });
      toastWithUndo(
        t(placement === 'archive' ? 'messages.archived' : 'messages.trashed'),
        t('actions.undo'),
        () =>
          updateInbox
            .mutateAsync({ mailboxId: target.mailbox_id, message_ids: messageIds, ...origin })
            .catch((failure: unknown) => {
              toast.error(t(resolveMailboxErrorKey(failure)));
            })
      );
    } catch (failure) {
      rollback();
      toast.error(t(resolveMailboxErrorKey(failure)));
    } finally {
      moving.current.delete(target.id);
    }
  };
  const starred = item.thread?.starred ?? item.starred;
  const toggleStar = () =>
    updateStar.mutate(
      { item, starred: !starred },
      { onError: failure => toast.error(t(resolveMailboxErrorKey(failure))) }
    );
  const messages: readonly MailMessage[] =
    (item.thread ? thread.data : detail.data ? [detail.data] : undefined) ?? [];
  const replyTarget = newestInbound(messages);
  const newest = messages.at(-1);
  const reply = () => replyTarget && navigation.onReply(replyTarget);
  const replyAll = () => replyTarget && navigation.onReplyAll(replyTarget);
  const forward = () => newest && navigation.onForward(newest);

  useEffect(() => {
    if (!actionsRef || unavailable) return;
    actionsRef.current = {
      archive: () => void move('archive'),
      trash: () => void move('trash'),
      star: toggleStar,
      reply,
      replyAll,
      forward,
    };
    return () => {
      actionsRef.current = null;
    };
  });
  const kicker = roleName
    ? t('inbox.reader.toRole', { address: item.mailbox_address, role: roleName })
    : t('inbox.reader.to', { address: item.mailbox_address });

  let body: ReactNode;
  if (unavailable) {
    body = (
      <div className="mx-auto w-full max-w-[640px] px-5 sm:px-7">
        <Alert>
          <Info className="size-4" aria-hidden="true" />
          <AlertDescription>{t('inbox.unavailable')}</AlertDescription>
        </Alert>
      </div>
    );
  } else {
    const { onLeave: _onLeave, ...readerNavigation } = navigation;
    const reader: ReaderProps = {
      ...readerNavigation,
      mailboxId: item.mailbox_id,
      mailboxAddress: item.mailbox_address,
      canSend: access.send,
      canOrganize: access.organize,
    };
    body = item.thread ? (
      <ThreadView
        key={item.id}
        threadId={item.thread_id}
        variant="article"
        kicker={kicker}
        {...reader}
      />
    ) : (
      <MessageDetail
        key={item.id}
        messageId={item.id}
        variant="article"
        kicker={kicker}
        {...reader}
      />
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex h-[52px] shrink-0 items-center gap-3.5 border-b border-border-subtle px-3 text-xs text-text-muted sm:px-7">
        <Button
          type="button"
          size="sm"
          variant="ghost"
          icon={<ArrowLeft className="size-4" />}
          className="-ml-2 shrink-0 lg:hidden"
          onClick={navigation.onClose}
        >
          {t('actions.back')}
        </Button>
        {!unavailable && access.send ? (
          <div
            role="toolbar"
            aria-label={t('inbox.actions.label')}
            className="flex min-w-0 flex-1 items-center gap-3.5 overflow-x-auto"
          >
            <ToolbarAction
              label={t('actions.reply')}
              keyCap="R"
              disabled={!replyTarget}
              onClick={reply}
            />
            <ToolbarAction
              label={t('actions.replyAll')}
              keyCap="A"
              disabled={!replyTarget}
              onClick={replyAll}
            />
            <ToolbarAction
              label={t('actions.forward')}
              keyCap="F"
              disabled={!newest}
              onClick={forward}
            />
            <span className="flex-1" />
            <ToolbarAction
              label={t('inbox.actions.archive')}
              keyCap="E"
              onClick={() => void move('archive')}
            />
            <ToolbarAction
              label={t('inbox.actions.trash')}
              keyCap="#"
              onClick={() => void move('trash')}
            />
          </div>
        ) : (
          <span className="flex-1" />
        )}
        <div className="flex shrink-0 items-center">
          <Button
            type="button"
            isIcon
            size="sm"
            variant="ghost"
            disabled={!navigation.onPrevious}
            aria-label={t('message.previous')}
            title={t('message.previous')}
            onClick={navigation.onPrevious ?? undefined}
          >
            <ChevronLeft className="size-4" />
          </Button>
          <Button
            type="button"
            isIcon
            size="sm"
            variant="ghost"
            disabled={!navigation.onNext}
            aria-label={t('message.next')}
            title={t('message.next')}
            onClick={navigation.onNext ?? undefined}
          >
            <ChevronRight className="size-4" />
          </Button>
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto py-[34px]">
        {triage && !unavailable && item.direction === 'inbound' ? (
          <InboxTriagePanel
            item={item}
            replyTarget={replyTarget}
            canSend={access.send}
            menuOpen={triage.menuOpen}
            onMenuOpenChange={triage.onMenuOpenChange}
            onUseDraft={triage.onUseDraft}
          />
        ) : null}
        {body}
      </div>
    </div>
  );
}

/** A labelled toolbar action with its key cap; the cap is not part of its name. */
function ToolbarAction({
  label,
  keyCap,
  disabled = false,
  onClick,
}: {
  label: string;
  keyCap: string;
  disabled?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      disabled={disabled}
      className={cn(
        'inline-flex shrink-0 items-center gap-1.5 rounded-md whitespace-nowrap text-text-muted focus-ring hover:text-text-main motion-reduce:transition-none',
        'disabled:pointer-events-none disabled:opacity-50'
      )}
      onClick={onClick}
    >
      {label}
      <Kbd aria-hidden="true" className="max-md:hidden">
        {keyCap}
      </Kbd>
    </button>
  );
}
