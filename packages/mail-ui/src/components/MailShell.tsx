import type { ReactNode } from 'react';

import { getProviderPreset, MAIL_UI_MAX_CONNECTED_ACCOUNTS } from '../providers.ts';
import {
  computeTotalUnread,
  computeUnreadByAccount,
  filterAccountsForMode,
  filterMessagesForMode,
  resolveReplySenderAccount,
} from '../selectors.ts';
import type {
  MailUiAccount,
  MailUiMessage,
  MailUiSyncStatus,
  WorkspaceMode,
} from '../types.ts';

export interface MailShellProps {
  mode: WorkspaceMode;
  onModeChange: (mode: WorkspaceMode) => void;
  accounts: readonly MailUiAccount[];
  messages: readonly MailUiMessage[];
  /** Selected account ID, or `'all'` / `null` for All Inboxes. */
  selectedAccountId?: string | null;
  onSelectAccount: (accountId: string | null) => void;
  selectedMessageId?: string | null;
  onSelectMessage: (messageId: string) => void;
  searchQuery?: string;
  onSearchChange: (query: string) => void;
  isSyncing?: boolean;
  onSyncNow: () => void;
  onConnectAccount: () => void;
  onReply: (message: MailUiMessage, senderAccount: MailUiAccount | null) => void;
  onForward: (message: MailUiMessage, senderAccount: MailUiAccount | null) => void;
  onArchive: (message: MailUiMessage) => void;
  onToggleStar: (message: MailUiMessage) => void;
  maxConnectedAccounts?: number;
  renderMessageBody?: (message: MailUiMessage) => ReactNode;
  className?: string;
}

const SYNC_DOTS: Readonly<Record<MailUiSyncStatus, { dot: string; label: string }>> = {
  synced: { dot: 'bg-emerald-500', label: 'Synced' },
  syncing: { dot: 'bg-sky-500 animate-pulse', label: 'Syncing' },
  error: { dot: 'bg-rose-500', label: 'Sync error' },
  paused: { dot: 'bg-amber-500', label: 'Paused' },
  idle: { dot: 'bg-slate-400', label: 'Idle' },
};

function formatTime(iso: string): string {
  const ms = Date.parse(iso);
  return Number.isNaN(ms)
    ? iso
    : new Date(ms).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

export function MailShell({
  mode,
  onModeChange,
  accounts,
  messages,
  selectedAccountId = null,
  onSelectAccount,
  selectedMessageId = null,
  onSelectMessage,
  searchQuery = '',
  onSearchChange,
  isSyncing = false,
  onSyncNow,
  onConnectAccount,
  onReply,
  onForward,
  onArchive,
  onToggleStar,
  maxConnectedAccounts = MAIL_UI_MAX_CONNECTED_ACCOUNTS,
  renderMessageBody,
  className = '',
}: MailShellProps) {
  const visibleAccounts = filterAccountsForMode(accounts, mode, maxConnectedAccounts);
  const accountById = new Map(accounts.map((account) => [account.id, account]));
  const computedUnread = computeUnreadByAccount(messages, visibleAccounts);
  const unreadByAccount: Record<string, number> = {};
  for (const account of visibleAccounts) {
    const derived = computedUnread[account.id] ?? 0;
    unreadByAccount[account.id] = derived > 0 ? derived : (account.unreadCount ?? 0);
  }
  const totalUnread = computeTotalUnread(unreadByAccount, visibleAccounts);
  const activeAccountId = selectedAccountId && selectedAccountId !== 'all' ? selectedAccountId : null;
  const filteredMessages = filterMessagesForMode(messages, visibleAccounts, mode, {
    selectedAccountId: activeAccountId,
    searchQuery,
  });
  const selectedMessage =
    filteredMessages.find((msg) => msg.id === selectedMessageId) ??
    messages.find((msg) => msg.id === selectedMessageId) ??
    null;
  const replySender = selectedMessage ? resolveReplySenderAccount(selectedMessage, accounts) : null;
  const canConnectMore = visibleAccounts.length < maxConnectedAccounts;

  return (
    <div
      data-slot="mail-shell"
      data-workspace-mode={mode}
      className={`grid h-full min-h-[600px] w-full grid-cols-1 overflow-hidden rounded-xl border border-slate-200 bg-white text-slate-900 md:grid-cols-[260px_minmax(280px,360px)_1fr] dark:border-slate-800 dark:bg-slate-950 dark:text-slate-100 ${className}`.trim()}
    >
      {/* Left Sidebar */}
      <aside
        aria-label="Mailboxes"
        data-slot="mail-shell-sidebar"
        className="flex min-h-0 flex-col border-b border-slate-200 bg-slate-50/70 md:border-r md:border-b-0 dark:border-slate-800 dark:bg-slate-900/50"
      >
        <div className="border-b border-slate-200 p-3 dark:border-slate-800">
          <div
            role="tablist"
            aria-label="Workspace mode"
            className="grid grid-cols-2 gap-1 rounded-lg bg-slate-200/70 p-1 dark:bg-slate-800"
          >
            {(['work', 'personal'] as const).map((tab) => (
              <button
                key={tab}
                type="button"
                role="tab"
                aria-selected={mode === tab}
                onClick={() => onModeChange(tab)}
                className={`rounded-md px-2.5 py-1.5 text-xs font-medium capitalize transition ${
                  mode === tab
                    ? 'bg-white text-slate-900 shadow-xs dark:bg-slate-950 dark:text-slate-100'
                    : 'text-slate-600 hover:text-slate-900 dark:text-slate-400 dark:hover:text-slate-100'
                }`}
              >
                {tab === 'work' ? 'Work' : 'Personal'}
              </button>
            ))}
          </div>
        </div>

        <nav className="flex-1 space-y-1 overflow-y-auto p-2" aria-label="Account list">
          <button
            type="button"
            data-account-item="all"
            onClick={() => onSelectAccount(null)}
            className={`flex w-full items-center justify-between rounded-lg px-2.5 py-2 text-left text-sm font-medium transition ${
              activeAccountId === null
                ? 'bg-slate-900 text-white dark:bg-slate-100 dark:text-slate-900'
                : 'text-slate-700 hover:bg-slate-200/60 dark:text-slate-300 dark:hover:bg-slate-800/70'
            }`}
          >
            <span>All Inboxes</span>
            {totalUnread > 0 ? (
              <span className="rounded-full bg-sky-500/20 px-2 py-0.5 text-xs font-semibold">
                {totalUnread}
              </span>
            ) : null}
          </button>

          <div className="px-2 pt-2 pb-1 text-[11px] font-semibold tracking-wide text-slate-500 uppercase">
            Accounts ({visibleAccounts.length}/{maxConnectedAccounts})
          </div>

          {visibleAccounts.map((account) => {
            const preset = getProviderPreset(account.providerId);
            const unread = unreadByAccount[account.id] ?? 0;
            const sync = SYNC_DOTS[account.syncStatus];
            const isSelected = activeAccountId === account.id;
            return (
              <button
                key={account.id}
                type="button"
                data-account-item={account.id}
                onClick={() => onSelectAccount(account.id)}
                className={`flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-sm transition ${
                  isSelected
                    ? 'bg-slate-200/90 font-medium text-slate-950 dark:bg-slate-800 dark:text-white'
                    : 'text-slate-700 hover:bg-slate-200/50 dark:text-slate-300 dark:hover:bg-slate-800/60'
                }`}
              >
                <span
                  aria-hidden="true"
                  style={{ backgroundColor: preset.badgeColor }}
                  className="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-md text-[10px] font-bold text-white"
                >
                  {preset.shortLabel}
                </span>
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-xs font-medium">
                    {account.displayName || account.email}
                  </span>
                  {account.displayName ? (
                    <span className="block truncate text-[11px] text-slate-500">{account.email}</span>
                  ) : null}
                </span>
                <span
                  title={account.lastError ? `${sync.label}: ${account.lastError}` : sync.label}
                  aria-label={sync.label}
                  className={`h-2 w-2 shrink-0 rounded-full ${sync.dot}`}
                />
                {unread > 0 ? (
                  <span className="shrink-0 rounded-full bg-slate-200 px-1.5 py-0.5 text-[11px] font-semibold text-slate-700 dark:bg-slate-700 dark:text-slate-100">
                    {unread}
                  </span>
                ) : null}
              </button>
            );
          })}
        </nav>

        <div className="border-t border-slate-200 p-3 dark:border-slate-800">
          <button
            type="button"
            data-slot="connect-account-button"
            disabled={!canConnectMore}
            onClick={onConnectAccount}
            className="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-xs font-semibold text-slate-800 transition hover:bg-slate-100 disabled:cursor-not-allowed disabled:opacity-50 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100 dark:hover:bg-slate-800"
          >
            {canConnectMore ? '+ Connect Mailbox' : `Limit Reached (${maxConnectedAccounts})`}
          </button>
        </div>
      </aside>

      {/* Middle Message List */}
      <section
        aria-label="Message list"
        data-slot="mail-shell-list"
        className="flex min-h-0 flex-col border-b border-slate-200 md:border-r md:border-b-0 dark:border-slate-800"
      >
        <div className="flex items-center gap-2 border-b border-slate-200 p-3 dark:border-slate-800">
          <input
            type="search"
            aria-label="Search messages"
            placeholder="Search mail..."
            value={searchQuery}
            onChange={(event) => onSearchChange(event.target.value)}
            className="min-w-0 flex-1 rounded-lg border border-slate-300 bg-slate-50 px-3 py-1.5 text-sm outline-none focus:border-sky-500 dark:border-slate-700 dark:bg-slate-900"
          />
          <button
            type="button"
            data-slot="sync-now-button"
            disabled={isSyncing}
            onClick={onSyncNow}
            className="shrink-0 rounded-lg border border-slate-300 px-3 py-1.5 text-xs font-medium text-slate-700 transition hover:bg-slate-100 disabled:opacity-60 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
          >
            {isSyncing ? 'Syncing…' : 'Sync Now'}
          </button>
        </div>

        <div className="flex-1 divide-y divide-slate-100 overflow-y-auto dark:divide-slate-800/70">
          {filteredMessages.length === 0 ? (
            <div className="flex h-full min-h-[200px] items-center justify-center p-6 text-center text-sm text-slate-500">
              No messages match the current view.
            </div>
          ) : (
            filteredMessages.map((msg) => {
              const isSelected = msg.id === selectedMessage?.id;
              const acc = accountById.get(msg.accountId);
              const preset = acc ? getProviderPreset(acc.providerId) : null;
              return (
                <button
                  key={msg.id}
                  type="button"
                  data-message-id={msg.id}
                  onClick={() => onSelectMessage(msg.id)}
                  className={`flex w-full flex-col gap-1 px-3.5 py-3 text-left transition ${
                    isSelected
                      ? 'bg-sky-50/90 dark:bg-sky-950/40'
                      : 'hover:bg-slate-50 dark:hover:bg-slate-900/60'
                  }`}
                >
                  <div className="flex items-center justify-between gap-2">
                    <div className="flex min-w-0 items-center gap-1.5">
                      {!msg.read ? <span aria-label="Unread" className="h-2 w-2 shrink-0 rounded-full bg-sky-500" /> : null}
                      <span className={`truncate text-sm ${msg.read ? 'text-slate-700 dark:text-slate-300' : 'font-semibold'}`}>
                        {msg.fromName || msg.from}
                      </span>
                    </div>
                    <span className="shrink-0 text-[11px] text-slate-500">{formatTime(msg.receivedAt)}</span>
                  </div>
                  <div className="flex items-center justify-between gap-2">
                    <span className={`truncate text-xs ${msg.read ? 'text-slate-600 dark:text-slate-400' : 'font-medium'}`}>
                      {msg.subject || '(No subject)'}
                    </span>
                    <div className="flex shrink-0 items-center gap-1.5">
                      {activeAccountId === null && preset ? (
                        <span
                          style={{ backgroundColor: preset.badgeColor }}
                          className="rounded px-1 py-0.5 text-[9px] font-bold text-white"
                        >
                          {preset.shortLabel}
                        </span>
                      ) : null}
                      {msg.starred ? <span aria-label="Starred" className="text-xs text-amber-500">★</span> : null}
                    </div>
                  </div>
                  {msg.snippet ? <p className="line-clamp-2 text-xs text-slate-500 dark:text-slate-400">{msg.snippet}</p> : null}
                </button>
              );
            })
          )}
        </div>
      </section>

      {/* Right Reading Pane */}
      <main
        aria-label="Reading pane"
        data-slot="mail-shell-reader"
        className="flex min-h-0 flex-col bg-white dark:bg-slate-950"
      >
        {selectedMessage ? (
          <>
            <header className="flex flex-col gap-3 border-b border-slate-200 p-4 dark:border-slate-800">
              <div className="flex flex-wrap items-start justify-between gap-2">
                <h1 className="text-base font-semibold">{selectedMessage.subject || '(No subject)'}</h1>
                <div className="flex flex-wrap items-center gap-1.5">
                  <button
                    type="button"
                    onClick={() => onReply(selectedMessage, replySender)}
                    className="rounded-md bg-slate-900 px-2.5 py-1 text-xs font-medium text-white hover:bg-slate-800 dark:bg-slate-100 dark:text-slate-900"
                  >
                    Reply
                  </button>
                  <button
                    type="button"
                    onClick={() => onForward(selectedMessage, replySender)}
                    className="rounded-md border border-slate-300 px-2.5 py-1 text-xs font-medium hover:bg-slate-100 dark:border-slate-700 dark:hover:bg-slate-800"
                  >
                    Forward
                  </button>
                  <button
                    type="button"
                    onClick={() => onArchive(selectedMessage)}
                    className="rounded-md border border-slate-300 px-2.5 py-1 text-xs font-medium hover:bg-slate-100 dark:border-slate-700 dark:hover:bg-slate-800"
                  >
                    Archive
                  </button>
                  <button
                    type="button"
                    aria-pressed={selectedMessage.starred}
                    onClick={() => onToggleStar(selectedMessage)}
                    className="rounded-md border border-slate-300 px-2.5 py-1 text-xs font-medium hover:bg-slate-100 dark:border-slate-700 dark:hover:bg-slate-800"
                  >
                    {selectedMessage.starred ? '★ Starred' : '☆ Star'}
                  </button>
                </div>
              </div>
              <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-slate-600 dark:text-slate-400">
                <div className="space-y-0.5">
                  <div>
                    <span className="font-medium text-slate-800 dark:text-slate-200">From:</span>{' '}
                    {selectedMessage.fromName ? `${selectedMessage.fromName} <${selectedMessage.from}>` : selectedMessage.from}
                  </div>
                  <div>
                    <span className="font-medium text-slate-800 dark:text-slate-200">To:</span>{' '}
                    {selectedMessage.to.join(', ')}
                  </div>
                  {replySender ? (
                    <div className="text-[11px] text-slate-500">
                      Reply via: <span className="font-mono">{replySender.email}</span>
                    </div>
                  ) : null}
                </div>
                <time dateTime={selectedMessage.receivedAt} className="text-[11px] text-slate-500">
                  {selectedMessage.receivedAt}
                </time>
              </div>
            </header>
            <div className="flex-1 overflow-y-auto p-5 text-sm leading-relaxed text-slate-800 dark:text-slate-200">
              {renderMessageBody ? (
                renderMessageBody(selectedMessage)
              ) : (
                <div className="whitespace-pre-wrap">{selectedMessage.bodyText ?? selectedMessage.snippet ?? ''}</div>
              )}
            </div>
          </>
        ) : (
          <div className="flex h-full min-h-[240px] items-center justify-center p-8 text-sm text-slate-500">
            Select a message to read.
          </div>
        )}
      </main>
    </div>
  );
}
