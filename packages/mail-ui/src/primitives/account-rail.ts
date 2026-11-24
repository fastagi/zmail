import { createTypeahead } from '@ui-sdk/core';
import { fuzzyScore } from '@ui-sdk/core/text-match';

import { MAIL_UI_MAX_CONNECTED_ACCOUNTS } from '../providers.ts';
import type { MailUiAccount } from '../types.ts';

export interface KeyEventLike {
  key: string;
  shiftKey?: boolean;
  ctrlKey?: boolean;
  metaKey?: boolean;
  altKey?: boolean;
  preventDefault?: () => void;
}

export interface AccountRailState {
  accounts: readonly MailUiAccount[];
  filteredAccounts: readonly MailUiAccount[];
  activeAccountId: string | null;
  focusedIndex: number;
  filterQuery: string;
}

export interface AccountRailControllerOptions {
  accounts: readonly MailUiAccount[];
  maxAccounts?: number;
  initialAccountId?: string | null;
  typeaheadTimeoutMs?: number;
}

export interface AccountRailController {
  getState: () => AccountRailState;
  setAccounts: (next: readonly MailUiAccount[]) => void;
  selectAccount: (accountId: string | null) => boolean;
  setFilterQuery: (query: string) => readonly MailUiAccount[];
  typeahead: (char: string) => MailUiAccount | null;
  handleKeyDown: (event: KeyEventLike) => boolean;
  subscribe: (listener: () => void) => () => void;
  destroy: () => void;
}

function accountLabel(account: MailUiAccount): string {
  return account.displayName ? `${account.displayName} ${account.email}` : account.email;
}

export function createAccountRailController(
  options: AccountRailControllerOptions
): AccountRailController {
  const maxAccounts = Math.max(
    1,
    Math.min(MAIL_UI_MAX_CONNECTED_ACCOUNTS, options.maxAccounts ?? MAIL_UI_MAX_CONNECTED_ACCOUNTS)
  );
  let accounts: MailUiAccount[] = options.accounts.slice(0, maxAccounts);
  let filterQuery = '';
  let activeAccountId: string | null =
    options.initialAccountId && accounts.some((a) => a.id === options.initialAccountId)
      ? options.initialAccountId
      : null;
  let focusedIndex = activeAccountId
    ? accounts.findIndex((a) => a.id === activeAccountId)
    : accounts.length > 0
      ? 0
      : -1;
  const searchTypeahead = createTypeahead(options.typeaheadTimeoutMs ?? 500);
  const listeners = new Set<() => void>();

  const notify = () => {
    for (const listener of [...listeners]) listener();
  };

  const getFiltered = (): MailUiAccount[] => {
    const trimmed = filterQuery.trim();
    if (!trimmed) return accounts;
    return accounts
      .map((account) => ({
        account,
        score: Math.max(
          fuzzyScore(account.email, trimmed),
          fuzzyScore(account.displayName ?? '', trimmed),
          fuzzyScore(String(account.providerId), trimmed)
        ),
      }))
      .filter((entry) => entry.score > 0)
      .sort((a, b) => b.score - a.score)
      .map((entry) => entry.account);
  };

  const moveFocus = (nextIndex: number) => {
    const visible = getFiltered();
    focusedIndex = visible.length === 0 ? -1 : Math.max(0, Math.min(visible.length - 1, nextIndex));
    notify();
  };

  return {
    getState: () => ({
      accounts,
      filteredAccounts: getFiltered(),
      activeAccountId,
      focusedIndex,
      filterQuery,
    }),
    setAccounts(next) {
      accounts = next.slice(0, maxAccounts);
      if (activeAccountId && !accounts.some((a) => a.id === activeAccountId)) {
        activeAccountId = null;
      }
      const visible = getFiltered();
      focusedIndex = visible.length === 0 ? -1 : Math.min(Math.max(focusedIndex, 0), visible.length - 1);
      notify();
    },
    selectAccount(accountId) {
      if (accountId === null || accountId === 'all') {
        activeAccountId = null;
        notify();
        return true;
      }
      if (!accounts.some((a) => a.id === accountId)) return false;
      activeAccountId = accountId;
      const visibleIdx = getFiltered().findIndex((a) => a.id === accountId);
      if (visibleIdx !== -1) focusedIndex = visibleIdx;
      notify();
      return true;
    },
    setFilterQuery(query) {
      filterQuery = query;
      const visible = getFiltered();
      focusedIndex = visible.length > 0 ? 0 : -1;
      notify();
      return visible;
    },
    typeahead(char) {
      const visible = getFiltered();
      if (visible.length === 0) return null;
      const matchIdx = searchTypeahead(char, visible.map(accountLabel), focusedIndex);
      if (matchIdx === -1) return null;
      focusedIndex = matchIdx;
      const matched = visible[matchIdx] ?? null;
      if (matched) activeAccountId = matched.id;
      notify();
      return matched;
    },
    handleKeyDown(event) {
      const visible = getFiltered();
      if (event.key === 'ArrowDown' || event.key === 'j') {
        event.preventDefault?.();
        moveFocus(focusedIndex < 0 ? 0 : (focusedIndex + 1) % Math.max(1, visible.length));
        return true;
      }
      if (event.key === 'ArrowUp' || event.key === 'k') {
        event.preventDefault?.();
        moveFocus(focusedIndex <= 0 ? visible.length - 1 : focusedIndex - 1);
        return true;
      }
      if (event.key === 'Home') {
        event.preventDefault?.();
        moveFocus(0);
        return true;
      }
      if (event.key === 'End') {
        event.preventDefault?.();
        moveFocus(visible.length - 1);
        return true;
      }
      if (event.key === 'Enter' || event.key === ' ') {
        const target = focusedIndex >= 0 ? visible[focusedIndex] : undefined;
        if (target) {
          event.preventDefault?.();
          activeAccountId = target.id;
          notify();
          return true;
        }
      }
      if (event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) {
        const matched = this.typeahead(event.key);
        if (matched) {
          event.preventDefault?.();
          return true;
        }
      }
      return false;
    },
    subscribe(listener) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    destroy() {
      searchTypeahead.reset();
      listeners.clear();
    },
  };
}
