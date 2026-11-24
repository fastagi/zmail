import { agentTools } from '@ui-sdk/core/agent';

import {
  computeTotalUnread,
  computeUnreadByAccount,
  filterAccountsForMode,
  filterMessagesForMode,
} from '../selectors.ts';
import type { MailUiAccount, MailUiMessage, WorkspaceMode } from '../types.ts';
import { createAccountRailController, type AccountRailController } from './account-rail.ts';
import { createInboxListController, type InboxListController } from './inbox-behaviors.ts';

export interface MailUiAgentSnapshot {
  mode: WorkspaceMode;
  activeAccountId: string | null;
  selectedIds: string[];
  activeMessageId: string | null;
  openedMessageId: string | null;
  searchQuery: string;
  unreadTotal: number;
  visibleCount: number;
}

export type MailUiAgentIntent =
  | { type: 'select_mode'; mode: WorkspaceMode }
  | { type: 'select_account'; accountId: string | null }
  | { type: 'move_next' }
  | { type: 'move_prev' }
  | { type: 'toggle_select'; messageId?: string }
  | { type: 'archive_selected' }
  | { type: 'open_active' }
  | { type: 'search'; query: string };

export interface MailUiAgentActResult {
  ok: boolean;
  intent: MailUiAgentIntent['type'];
  snapshot: MailUiAgentSnapshot;
  error?: string;
}

export interface MailUiAgentOptions {
  accounts: readonly MailUiAccount[];
  messages: readonly MailUiMessage[];
  initialMode?: WorkspaceMode;
  initialAccountId?: string | null;
  onArchive?: (messageIds: string[]) => void;
  onOpen?: (message: MailUiMessage) => void;
}

export interface MailUiAgent {
  snapshot: () => MailUiAgentSnapshot;
  act: (intent: MailUiAgentIntent) => MailUiAgentActResult;
  list: InboxListController;
  accountRail: AccountRailController;
  tools: typeof agentTools;
  destroy: () => void;
}

export function createMailUiAgent(options: MailUiAgentOptions): MailUiAgent {
  let mode: WorkspaceMode = options.initialMode ?? 'personal';
  let searchQuery = '';
  let openedMessageId: string | null = null;
  const accounts: MailUiAccount[] = [...options.accounts];
  let messages: MailUiMessage[] = options.messages.map((m) => ({ ...m }));

  const accountRail = createAccountRailController({
    accounts: filterAccountsForMode(accounts, mode),
    initialAccountId: options.initialAccountId ?? null,
  });

  const visibleMessages = () =>
    filterMessagesForMode(messages, accounts, mode, {
      selectedAccountId: accountRail.getState().activeAccountId,
      searchQuery,
    });

  const list = createInboxListController({
    messages: visibleMessages(),
    onOpen: (msg) => {
      openedMessageId = msg.id;
      options.onOpen?.(msg);
    },
  });

  const syncViews = () => {
    accountRail.setAccounts(filterAccountsForMode(accounts, mode));
    list.setMessages(visibleMessages());
  };

  const snapshot = (): MailUiAgentSnapshot => {
    const modeAccounts = filterAccountsForMode(accounts, mode);
    const activeAccountId = accountRail.getState().activeAccountId;
    const scopedAccounts = activeAccountId
      ? modeAccounts.filter((a) => a.id === activeAccountId)
      : modeAccounts;
    const unreadMap = computeUnreadByAccount(messages, scopedAccounts);
    const listState = list.getState();
    return {
      mode,
      activeAccountId,
      selectedIds: listState.selectedIds,
      activeMessageId: listState.activeMessageId,
      openedMessageId,
      searchQuery,
      unreadTotal: computeTotalUnread(unreadMap, scopedAccounts),
      visibleCount: listState.totalCount,
    };
  };

  const act = (intent: MailUiAgentIntent): MailUiAgentActResult => {
    switch (intent.type) {
      case 'select_mode': {
        mode = intent.mode;
        accountRail.selectAccount(null);
        openedMessageId = null;
        syncViews();
        return { ok: true, intent: intent.type, snapshot: snapshot() };
      }
      case 'select_account': {
        const selected = accountRail.selectAccount(intent.accountId);
        if (!selected) {
          return {
            ok: false,
            intent: intent.type,
            snapshot: snapshot(),
            error: `Account ${intent.accountId} is not available in ${mode} mode`,
          };
        }
        list.setMessages(visibleMessages());
        return { ok: true, intent: intent.type, snapshot: snapshot() };
      }
      case 'move_next':
        list.moveNext();
        return { ok: true, intent: intent.type, snapshot: snapshot() };
      case 'move_prev':
        list.movePrev();
        return { ok: true, intent: intent.type, snapshot: snapshot() };
      case 'toggle_select':
        list.toggleSelect(intent.messageId);
        return { ok: true, intent: intent.type, snapshot: snapshot() };
      case 'archive_selected': {
        const state = list.getState();
        const targets =
          state.selectedIds.length > 0
            ? state.selectedIds
            : state.activeMessageId
              ? [state.activeMessageId]
              : [];
        if (targets.length === 0) {
          return {
            ok: false,
            intent: intent.type,
            snapshot: snapshot(),
            error: 'No messages selected to archive',
          };
        }
        const targetSet = new Set(targets);
        messages = messages.map((m) => (targetSet.has(m.id) ? { ...m, archived: true } : m));
        if (openedMessageId && targetSet.has(openedMessageId)) openedMessageId = null;
        options.onArchive?.(targets);
        list.setMessages(visibleMessages());
        return { ok: true, intent: intent.type, snapshot: snapshot() };
      }
      case 'open_active': {
        const currentId = list.getState().activeMessageId;
        const current = currentId ? list.getMessages().find((m) => m.id === currentId) : undefined;
        if (!current) {
          return {
            ok: false,
            intent: intent.type,
            snapshot: snapshot(),
            error: 'No active message to open',
          };
        }
        openedMessageId = current.id;
        messages = messages.map((m) => (m.id === current.id ? { ...m, read: true } : m));
        options.onOpen?.(current);
        list.setMessages(visibleMessages());
        return { ok: true, intent: intent.type, snapshot: snapshot() };
      }
      case 'search':
        searchQuery = intent.query;
        list.setMessages(visibleMessages());
        return { ok: true, intent: intent.type, snapshot: snapshot() };
    }
  };

  return {
    snapshot,
    act,
    list,
    accountRail,
    tools: agentTools,
    destroy() {
      list.destroy();
      accountRail.destroy();
    },
  };
}
