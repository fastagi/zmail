import {
  createSelection,
  createVirtualizer,
  type SelectionModel,
  type Store,
  type Virtualizer,
} from '@ui-sdk/core';

import type { MailUiMessage } from '../types.ts';
import type { KeyEventLike } from './account-rail.ts';

export {
  createAccountRailController,
  type AccountRailController,
  type AccountRailControllerOptions,
  type AccountRailState,
  type KeyEventLike,
} from './account-rail.ts';
export {
  createMailUiAgent,
  type MailUiAgent,
  type MailUiAgentActResult,
  type MailUiAgentIntent,
  type MailUiAgentOptions,
  type MailUiAgentSnapshot,
} from './mail-agent.ts';

type SelectionReason = 'keyboard' | 'pointer' | 'agent' | 'imperative';

interface SelectionState {
  selectedIds: string[];
}

export interface VirtualWindow {
  startIndex: number;
  endIndex: number;
  totalHeight: number;
  offsetTop: number;
  visibleItems: readonly MailUiMessage[];
}

export interface InboxListState {
  activeIndex: number;
  activeMessageId: string | null;
  selectedIds: string[];
  totalCount: number;
}

export interface InboxListControllerOptions {
  messages: readonly MailUiMessage[];
  itemHeight?: number;
  overscan?: number;
  initialActiveId?: string | null;
  onOpen?: (message: MailUiMessage) => void;
}

export interface InboxListController {
  getState: () => InboxListState;
  getMessages: () => readonly MailUiMessage[];
  setMessages: (next: readonly MailUiMessage[]) => void;
  moveNext: (options?: { extend?: boolean }) => string | null;
  movePrev: (options?: { extend?: boolean }) => string | null;
  moveTo: (index: number, options?: { extend?: boolean }) => string | null;
  setActiveById: (messageId: string | null) => void;
  toggleSelect: (messageId?: string) => string[];
  selectRange: (targetId: string, options?: { additive?: boolean }) => string[];
  toggleSelectAll: () => string[];
  clearSelection: () => void;
  isSelected: (messageId: string) => boolean;
  handleRowClick: (
    messageId: string,
    modifiers?: { shiftKey?: boolean; ctrlKey?: boolean; metaKey?: boolean }
  ) => void;
  handleKeyDown: (event: KeyEventLike) => boolean;
  getVirtualWindow: (scrollTop: number, viewportHeight: number) => VirtualWindow;
  virtualizer: Virtualizer;
  selection: SelectionModel;
  subscribe: (listener: () => void) => () => void;
  destroy: () => void;
}

function createHeadlessSelectionContext(initial: string[] = []) {
  let state: SelectionState = { selectedIds: [...initial] };
  const listeners = new Set<() => void>();
  const cleanups: Array<() => void> = [];

  const store = {
    get: () => state,
    set(patch: Partial<SelectionState>) {
      state = { ...state, ...patch };
      for (const listener of [...listeners]) listener();
    },
    subscribe(listener: () => void) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
  } as unknown as Store<SelectionState>;

  return {
    store,
    options: () => ({ multiple: true }),
    onDestroy: (fn: () => void) => {
      cleanups.push(fn);
    },
    dispose: () => {
      for (const fn of cleanups.splice(0)) fn();
      listeners.clear();
    },
  };
}

export function createInboxListController(
  options: InboxListControllerOptions
): InboxListController {
  const itemHeight = Math.max(1, options.itemHeight ?? 72);
  const overscan = Math.max(0, options.overscan ?? 4);
  let messages: MailUiMessage[] = [...options.messages];
  const listeners = new Set<() => void>();

  const notify = () => {
    for (const listener of [...listeners]) listener();
  };

  let activeIndex = (() => {
    if (messages.length === 0) return -1;
    if (options.initialActiveId) {
      const idx = messages.findIndex((m) => m.id === options.initialActiveId);
      if (idx !== -1) return idx;
    }
    return 0;
  })();

  const selCtx = createHeadlessSelectionContext();
  const selection = createSelection<
    { multiple: boolean },
    SelectionState,
    'selectedIds',
    SelectionReason
  >(selCtx, {
    key: 'selectedIds',
    multiple: () => true,
    order: () => messages.map((m) => m.id),
    set: (next) => {
      selCtx.store.set({ selectedIds: [...next] });
      return true;
    },
  });

  const virtualizer = createVirtualizer({
    count: () => messages.length,
    estimate: () => itemHeight,
    key: (index) => messages[index]?.id ?? String(index),
    overscan,
  });

  const activeMessageId = (): string | null =>
    activeIndex >= 0 && activeIndex < messages.length ? (messages[activeIndex]?.id ?? null) : null;

  const moveTo = (index: number, moveOptions?: { extend?: boolean }): string | null => {
    if (messages.length === 0) {
      activeIndex = -1;
      notify();
      return null;
    }
    const prevId = activeMessageId();
    activeIndex = Math.max(0, Math.min(messages.length - 1, index));
    const nextId = activeMessageId();
    if (nextId) {
      if (moveOptions?.extend) {
        selection.extend(nextId, { reason: 'keyboard', from: prevId, additive: true });
      } else {
        selection.setAnchor(nextId);
      }
    }
    notify();
    return nextId;
  };

  const toggleSelect = (messageId?: string): string[] => {
    const targetId = messageId ?? activeMessageId();
    if (targetId && messages.some((m) => m.id === targetId)) {
      selection.toggle(targetId, { reason: 'keyboard' });
      notify();
    }
    return [...selCtx.store.get().selectedIds];
  };

  const selectRange = (targetId: string, rangeOptions?: { additive?: boolean }): string[] => {
    const targetIdx = messages.findIndex((m) => m.id === targetId);
    if (targetIdx === -1) return [...selCtx.store.get().selectedIds];
    selection.extend(targetId, {
      reason: 'pointer',
      from: activeMessageId(),
      additive: rangeOptions?.additive ?? true,
    });
    activeIndex = targetIdx;
    notify();
    return [...selCtx.store.get().selectedIds];
  };

  return {
    getState: () => ({
      activeIndex,
      activeMessageId: activeMessageId(),
      selectedIds: [...selCtx.store.get().selectedIds],
      totalCount: messages.length,
    }),
    getMessages: () => messages,
    setMessages(next) {
      const prevActiveId = activeMessageId();
      messages = [...next];
      const validIds = new Set(messages.map((m) => m.id));
      const kept = selCtx.store.get().selectedIds.filter((id) => validIds.has(id));
      if (kept.length !== selCtx.store.get().selectedIds.length) {
        selCtx.store.set({ selectedIds: kept });
      }
      if (messages.length === 0) {
        activeIndex = -1;
      } else if (prevActiveId) {
        const idx = messages.findIndex((m) => m.id === prevActiveId);
        activeIndex = idx !== -1 ? idx : Math.min(Math.max(activeIndex, 0), messages.length - 1);
      } else {
        activeIndex = 0;
      }
      virtualizer.refresh();
      notify();
    },
    moveNext: (opts) => moveTo(activeIndex < 0 ? 0 : activeIndex + 1, opts),
    movePrev: (opts) => moveTo(activeIndex <= 0 ? 0 : activeIndex - 1, opts),
    moveTo,
    setActiveById(messageId) {
      if (!messageId) {
        activeIndex = -1;
        notify();
        return;
      }
      const idx = messages.findIndex((m) => m.id === messageId);
      if (idx !== -1) moveTo(idx);
    },
    toggleSelect,
    selectRange,
    toggleSelectAll() {
      selection.toggleAll({ reason: 'keyboard' });
      notify();
      return [...selCtx.store.get().selectedIds];
    },
    clearSelection() {
      selCtx.store.set({ selectedIds: [] });
      selection.setAnchor(activeMessageId());
      notify();
    },
    isSelected: (messageId) => selection.isSelected(messageId),
    handleRowClick(messageId, modifiers = {}) {
      const idx = messages.findIndex((m) => m.id === messageId);
      if (idx === -1) return;
      if (modifiers.shiftKey) {
        selectRange(messageId, { additive: false });
        return;
      }
      if (modifiers.ctrlKey || modifiers.metaKey) {
        activeIndex = idx;
        selection.toggle(messageId, { reason: 'pointer' });
        notify();
        return;
      }
      activeIndex = idx;
      selection.setAnchor(messageId);
      notify();
    },
    handleKeyDown(event) {
      const { key } = event;
      if (key === 'j' || key === 'ArrowDown') {
        event.preventDefault?.();
        moveTo(activeIndex < 0 ? 0 : activeIndex + 1, { extend: Boolean(event.shiftKey) });
        return true;
      }
      if (key === 'k' || key === 'ArrowUp') {
        event.preventDefault?.();
        moveTo(activeIndex <= 0 ? 0 : activeIndex - 1, { extend: Boolean(event.shiftKey) });
        return true;
      }
      if (key === 'x' || key === 'X') {
        event.preventDefault?.();
        toggleSelect();
        return true;
      }
      if (key === 'Home') {
        event.preventDefault?.();
        moveTo(0, { extend: Boolean(event.shiftKey) });
        return true;
      }
      if (key === 'End') {
        event.preventDefault?.();
        moveTo(messages.length - 1, { extend: Boolean(event.shiftKey) });
        return true;
      }
      if ((key === 'a' || key === 'A') && (event.ctrlKey || event.metaKey)) {
        event.preventDefault?.();
        selection.toggleAll({ reason: 'keyboard' });
        notify();
        return true;
      }
      if (key === 'Enter') {
        const current = activeIndex >= 0 ? messages[activeIndex] : undefined;
        if (current) {
          event.preventDefault?.();
          options.onOpen?.(current);
          return true;
        }
      }
      return false;
    },
    getVirtualWindow(scrollTop, viewportHeight) {
      const totalCount = messages.length;
      const totalHeight = virtualizer.size();
      if (totalCount === 0) {
        return { startIndex: 0, endIndex: -1, totalHeight: 0, offsetTop: 0, visibleItems: [] };
      }
      const safeScroll = Math.max(0, scrollTop);
      const safeViewport = Math.max(itemHeight, viewportHeight);
      const rawStart = Math.floor(safeScroll / itemHeight);
      const rawEnd = Math.min(totalCount - 1, Math.ceil((safeScroll + safeViewport) / itemHeight) - 1);
      const startIndex = Math.max(0, rawStart - overscan);
      const endIndex = Math.min(totalCount - 1, rawEnd + overscan);
      return {
        startIndex,
        endIndex,
        totalHeight,
        offsetTop: startIndex * itemHeight,
        visibleItems: messages.slice(startIndex, endIndex + 1),
      };
    },
    virtualizer,
    selection,
    subscribe(listener) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    destroy() {
      selCtx.dispose();
      listeners.clear();
    },
  };
}
