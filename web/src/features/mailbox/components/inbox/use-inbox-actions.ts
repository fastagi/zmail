'use client';

import { useCallback, useMemo, useRef, useState } from 'react';
import { type InfiniteData, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';

import {
  originOf,
  toastWithUndo,
  type MailOrigin,
} from '@/features/mailbox/components/message-actions';
import { useDateTimeFormat } from '@/features/mailbox/components/mail-shared';
import { rowMessageIds, rowUnread } from '@/features/mailbox/components/message-rows';
import { updateInChunks } from '@/features/mailbox/components/messages/message-list-helpers';
import { useUpdateInboxMessage, useUpdateInboxMessages } from '@/features/mailbox/hooks/use-inbox';
import { mailboxKeys } from '@/features/mailbox/hooks/use-mailbox';
import type {
  InboxItem,
  InboxPage,
  InboxView,
  UpdateMailMessagesInput,
} from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

export type InboxQuickAction = 'star' | 'toggleRead' | 'archive' | 'trash';
export type InboxBulkAction = 'markRead' | 'markUnread' | 'archive' | 'trash';

type InboxListing = InfiniteData<InboxPage, string | undefined>;

/** Whether a row can be archived from the unified inbox listing. */
export function canArchiveInboxItem(
  item: InboxItem,
  sendable: ReadonlySet<number>,
  view: InboxView = 'all'
): boolean {
  return (
    sendable.has(item.mailbox_id) &&
    item.placement !== 'archive' &&
    item.placement !== 'trash' &&
    view !== 'sent' &&
    !(item.direction === 'outbound' && !item.thread)
  );
}

/** Whether a row can be moved to the trash from the unified inbox listing. */
export function canDeleteInboxItem(item: InboxItem, sendable: ReadonlySet<number>): boolean {
  return sendable.has(item.mailbox_id) && item.placement !== 'trash';
}

/** Whether a row can be snoozed or unsnoozed from the unified inbox listing. */
export function canSnoozeInboxItem(
  item: InboxItem,
  sendable: ReadonlySet<number>,
  view: InboxView = 'all'
): boolean {
  return sendable.has(item.mailbox_id) && item.placement !== 'trash' && view !== 'sent';
}

function setRowReadState(item: InboxItem, read: boolean): InboxItem {
  return {
    ...item,
    read: item.direction === 'inbound' ? read : item.read,
    thread: item.thread && { ...item.thread, unread: !read },
  };
}

function groupMessageIdsByMailbox(items: readonly InboxItem[]): Map<number, number[]> {
  const byMailbox = new Map<number, Set<number>>();
  for (const item of items) {
    const ids = byMailbox.get(item.mailbox_id) ?? new Set<number>();
    for (const id of rowMessageIds(item)) ids.add(id);
    byMailbox.set(item.mailbox_id, ids);
  }
  return new Map(Array.from(byMailbox, ([mailboxId, ids]) => [mailboxId, [...ids]]));
}

interface MailboxOriginGroup {
  mailboxId: number;
  origin: MailOrigin;
  ids: number[];
}

function groupOriginsByMailbox(items: readonly InboxItem[]): MailboxOriginGroup[] {
  const groups = new Map<string, { mailboxId: number; origin: MailOrigin; ids: Set<number> }>();
  for (const item of items) {
    const origin = originOf(item);
    const key = `${item.mailbox_id}:${origin.placement}:${origin.folder_id ?? ''}`;
    const entry = groups.get(key) ?? { mailboxId: item.mailbox_id, origin, ids: new Set<number>() };
    for (const id of rowMessageIds(item)) entry.ids.add(id);
    groups.set(key, entry);
  }
  return Array.from(groups.values(), ({ mailboxId, origin, ids }) => ({
    mailboxId,
    origin,
    ids: [...ids],
  }));
}

/**
 * Row-level hover quick actions and multi-select bulk actions for the unified inbox.
 * Groups cross-mailbox updates by `mailbox_id`, applies optimistic cache updates with rollback on
 * failure, and shows undo toasts for archive and trash moves.
 */
export function useInboxActions({
  organizationId,
  rows,
  sendableIds,
  view,
  timeZone,
  scopeKey,
  openItemId,
  onLeaveOpenRow,
  onCloseOpenRow,
}: {
  organizationId: number;
  rows: readonly InboxItem[];
  sendableIds: ReadonlySet<number>;
  view: InboxView;
  timeZone?: string;
  scopeKey: string;
  openItemId: number | null;
  onLeaveOpenRow: () => void;
  onCloseOpenRow: () => void;
}) {
  const t = useT('mailbox');
  const formatDateTime = useDateTimeFormat(timeZone);
  const queryClient = useQueryClient();
  const updateInbox = useUpdateInboxMessages(organizationId);
  const updateStar = useUpdateInboxMessage(organizationId);

  const [selectionState, setSelectionState] = useState<{ scope: string; ids: ReadonlySet<number> }>(
    () => ({ scope: scopeKey, ids: new Set() })
  );
  // Reset checked rows automatically whenever the listing scope (view/filter/search) changes.
  const rawCheckedIds = useMemo(
    () => (selectionState.scope === scopeKey ? selectionState.ids : new Set<number>()),
    [selectionState, scopeKey]
  );

  const [bulkPending, setBulkPending] = useState(false);
  const movingIds = useRef(new Set<number>());

  const selectedItems = useMemo(
    () => rows.filter(row => rawCheckedIds.has(row.id)),
    [rows, rawCheckedIds]
  );
  const checkedIds = useMemo(
    () => new Set(selectedItems.map(item => item.id)),
    [selectedItems]
  );

  const setCheckedIds = useCallback(
    (updater: (prev: ReadonlySet<number>) => ReadonlySet<number>) => {
      setSelectionState(current => {
        const base = current.scope === scopeKey ? current.ids : new Set<number>();
        return { scope: scopeKey, ids: updater(base) };
      });
    },
    [scopeKey]
  );

  const toggleSelect = useCallback(
    (id: number) => {
      setCheckedIds(prev => {
        const next = new Set(prev);
        if (next.has(id)) next.delete(id);
        else next.add(id);
        return next;
      });
    },
    [setCheckedIds]
  );

  const toggleSelectAll = useCallback(() => {
    setCheckedIds(prev => {
      const allSelected = rows.length > 0 && rows.every(row => prev.has(row.id));
      return allSelected ? new Set() : new Set(rows.map(row => row.id));
    });
  }, [rows, setCheckedIds]);

  const clearSelection = useCallback(() => {
    setCheckedIds(() => new Set());
  }, [setCheckedIds]);

  const snapshotAndPatch = useCallback(
    (transform: (items: InboxItem[]) => InboxItem[]) => {
      const queryKey = mailboxKeys.inboxes(organizationId);
      const snapshot = queryClient.getQueriesData<InboxListing>({ queryKey });
      queryClient.setQueriesData<InboxListing>({ queryKey }, listing =>
        listing?.pages
          ? {
              ...listing,
              pages: listing.pages.map(page => ({ ...page, items: transform(page.items) })),
            }
          : listing
      );
      return () => {
        for (const [key, listing] of snapshot) queryClient.setQueryData(key, listing);
      };
    },
    [organizationId, queryClient]
  );

  const mutateMailbox = useCallback(
    (mailboxId: number, ids: number[], change: Omit<UpdateMailMessagesInput, 'message_ids'>) =>
      updateInChunks(
        input => updateInbox.mutateAsync({ mailboxId, ...input }),
        ids,
        change
      ),
    [updateInbox]
  );

  const runUndo = useCallback(
    async (originGroups: readonly MailboxOriginGroup[]) => {
      try {
        for (const group of originGroups) {
          await mutateMailbox(group.mailboxId, group.ids, group.origin);
        }
      } catch (failure) {
        toast.error(t(resolveMailboxErrorKey(failure)));
      }
    },
    [mutateMailbox, t]
  );

  const runQuickAction = useCallback(
    async (item: InboxItem, action: InboxQuickAction) => {
      if (action === 'star') {
        const starred = item.thread?.starred ?? item.starred;
        try {
          await updateStar.mutateAsync({ item, starred: !starred });
        } catch (failure) {
          toast.error(t(resolveMailboxErrorKey(failure)));
        }
        return;
      }

      if (action === 'toggleRead') {
        const nextRead = rowUnread(item);
        const rollback = snapshotAndPatch(items =>
          items.map(row => (row.id === item.id ? setRowReadState(row, nextRead) : row))
        );
        try {
          await mutateMailbox(item.mailbox_id, rowMessageIds(item), { read: nextRead });
        } catch (failure) {
          rollback();
          toast.error(t(resolveMailboxErrorKey(failure)));
        }
        return;
      }

      if (movingIds.current.has(item.id)) return;
      if (action === 'archive' && !canArchiveInboxItem(item, sendableIds, view)) return;
      if (action === 'trash' && !canDeleteInboxItem(item, sendableIds)) return;

      movingIds.current.add(item.id);
      const originGroups = groupOriginsByMailbox([item]);
      const rollback = snapshotAndPatch(items => items.filter(row => row.id !== item.id));
      if (openItemId === item.id) onLeaveOpenRow();
      setCheckedIds(prev => {
        if (!prev.has(item.id)) return prev;
        const next = new Set(prev);
        next.delete(item.id);
        return next;
      });
      try {
        await mutateMailbox(item.mailbox_id, rowMessageIds(item), { placement: action });
        toastWithUndo(
          t(action === 'archive' ? 'messages.archived' : 'messages.trashed'),
          t('actions.undo'),
          () => runUndo(originGroups)
        );
      } catch (failure) {
        rollback();
        toast.error(t(resolveMailboxErrorKey(failure)));
      } finally {
        movingIds.current.delete(item.id);
      }
    },
    [
      mutateMailbox,
      onLeaveOpenRow,
      openItemId,
      runUndo,
      sendableIds,
      setCheckedIds,
      snapshotAndPatch,
      t,
      updateStar,
      view,
    ]
  );

  const runBulkAction = useCallback(
    async (action: InboxBulkAction) => {
      if (bulkPending || selectedItems.length === 0) return;
      const targets =
        action === 'archive'
          ? selectedItems.filter(item => canArchiveInboxItem(item, sendableIds, view))
          : action === 'trash'
            ? selectedItems.filter(item => canDeleteInboxItem(item, sendableIds))
            : selectedItems;
      if (targets.length === 0) return;

      const targetIds = new Set(targets.map(item => item.id));
      const grouped = groupMessageIdsByMailbox(targets);
      const isMove = action === 'archive' || action === 'trash';
      const change: Omit<UpdateMailMessagesInput, 'message_ids'> =
        action === 'markRead'
          ? { read: true }
          : action === 'markUnread'
            ? { read: false }
            : { placement: action };
      const originGroups = isMove ? groupOriginsByMailbox(targets) : [];

      const rollback = isMove
        ? snapshotAndPatch(items => items.filter(row => !targetIds.has(row.id)))
        : snapshotAndPatch(items =>
            items.map(row =>
              targetIds.has(row.id) ? setRowReadState(row, action === 'markRead') : row
            )
          );

      if (isMove && openItemId !== null && targetIds.has(openItemId)) {
        onCloseOpenRow();
      }

      setBulkPending(true);
      try {
        for (const [mailboxId, messageIds] of grouped) {
          await mutateMailbox(mailboxId, messageIds, change);
        }
        clearSelection();
        if (isMove) {
          toastWithUndo(
            t(action === 'archive' ? 'messages.archived' : 'messages.trashed'),
            t('actions.undo'),
            () => runUndo(originGroups)
          );
        } else {
          toast.success(
            t(action === 'markRead' ? 'inbox.bulk.markedRead' : 'inbox.bulk.markedUnread', {
              count: targets.length,
            })
          );
        }
      } catch (failure) {
        rollback();
        toast.error(t(resolveMailboxErrorKey(failure)));
      } finally {
        setBulkPending(false);
      }
    },
    [
      bulkPending,
      clearSelection,
      mutateMailbox,
      onCloseOpenRow,
      openItemId,
      runUndo,
      selectedItems,
      sendableIds,
      snapshotAndPatch,
      t,
      view,
    ]
  );

  const runSnooze = useCallback(
    async (item: InboxItem, snoozedUntil: string | null) => {
      if (movingIds.current.has(item.id) || !canSnoozeInboxItem(item, sendableIds, view)) return;

      const ids = rowMessageIds(item);
      const previousSnooze = item.snoozed_until ?? null;
      const clearing = snoozedUntil === null;
      const removesFromView = clearing ? view === 'snoozed' : view !== 'snoozed';

      movingIds.current.add(item.id);
      const rollback = removesFromView
        ? snapshotAndPatch(items => items.filter(row => row.id !== item.id))
        : snapshotAndPatch(items =>
            items.map(row => (row.id === item.id ? { ...row, snoozed_until: snoozedUntil } : row))
          );
      if (removesFromView) {
        if (openItemId === item.id) onLeaveOpenRow();
        setCheckedIds(prev => {
          if (!prev.has(item.id)) return prev;
          const next = new Set(prev);
          next.delete(item.id);
          return next;
        });
      }

      const undoChange = (change: Omit<UpdateMailMessagesInput, 'message_ids'>) =>
        mutateMailbox(item.mailbox_id, ids, change).catch((failure: unknown) => {
          toast.error(t(resolveMailboxErrorKey(failure)));
        });

      try {
        await mutateMailbox(
          item.mailbox_id,
          ids,
          clearing ? { clear_snooze: true } : { snoozed_until: snoozedUntil }
        );
        if (clearing) {
          if (previousSnooze) {
            toastWithUndo(t('messages.unsnoozed'), t('actions.undo'), () =>
              undoChange({ snoozed_until: previousSnooze })
            );
          } else {
            toast.success(t('messages.unsnoozed'));
          }
        } else {
          const time = formatDateTime.format(new Date(snoozedUntil));
          toastWithUndo(t('messages.snoozed', { time }), t('actions.undo'), () =>
            undoChange(previousSnooze ? { snoozed_until: previousSnooze } : { clear_snooze: true })
          );
        }
      } catch (failure) {
        rollback();
        toast.error(t(resolveMailboxErrorKey(failure)));
      } finally {
        movingIds.current.delete(item.id);
      }
    },
    [
      formatDateTime,
      mutateMailbox,
      onLeaveOpenRow,
      openItemId,
      sendableIds,
      setCheckedIds,
      snapshotAndPatch,
      t,
      view,
    ]
  );

  return {
    checkedIds,
    selectedItems,
    bulkPending,
    toggleSelect,
    toggleSelectAll,
    clearSelection,
    runQuickAction,
    runBulkAction,
    runSnooze,
  };
}
