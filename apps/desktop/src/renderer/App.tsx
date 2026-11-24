import { useCallback, useEffect, useMemo, useState } from 'react';
import type {
  AccountWorkspaceKind,
  CachedMailMessage,
  MailPlacement,
  ZmailDesktopBridge,
} from '../shared/ipc-channels.ts';
import {
  DEFAULT_API_BASE,
  INITIAL_ACCOUNTS,
  SEED_MESSAGES,
  patchMessageOnV1,
  sendMessageOnV1,
  syncInboxFromV1,
  type DesktopAccountConfig,
} from './api.ts';

declare global {
  interface Window {
    zmailDesktop?: ZmailDesktopBridge;
  }
}

const MAX_ACCOUNTS = 30;
type FolderFilter = 'inbox' | 'starred' | 'archive' | 'trash';

export function App() {
  const bridge = window.zmailDesktop;
  const [workspaceFilter, setWorkspaceFilter] = useState<'all' | AccountWorkspaceKind>('all');
  const [accounts, setAccounts] = useState<DesktopAccountConfig[]>(INITIAL_ACCOUNTS);
  const [activeAccountId, setActiveAccountId] = useState<string>(INITIAL_ACCOUNTS[0]?.id ?? '');
  const [folder, setFolder] = useState<FolderFilter>('inbox');
  const [searchText, setSearchText] = useState('');
  const [searchElapsedMs, setSearchElapsedMs] = useState<number | null>(null);
  const [messages, setMessages] = useState<CachedMailMessage[]>(SEED_MESSAGES);
  const [selectedId, setSelectedId] = useState<number | null>(SEED_MESSAGES[0]?.id ?? null);
  const [syncStatus, setSyncStatus] = useState<string>('Offline cache ready');
  const [isSyncing, setIsSyncing] = useState(false);
  const [composeOpen, setComposeOpen] = useState(false);
  const [composeTo, setComposeTo] = useState('');
  const [composeSubject, setComposeSubject] = useState('');
  const [composeBody, setComposeBody] = useState('');

  const visibleAccounts = useMemo(
    () => accounts.filter((acct) => workspaceFilter === 'all' || acct.workspace === workspaceFilter),
    [accounts, workspaceFilter]
  );

  const activeAccount = useMemo(
    () => accounts.find((acct) => acct.id === activeAccountId) ?? visibleAccounts[0] ?? null,
    [accounts, activeAccountId, visibleAccounts]
  );

  const loadLocalMessages = useCallback(
    async (accountId: string, queryText: string, nextFolder: FolderFilter) => {
      const placement: MailPlacement | undefined =
        nextFolder === 'inbox'
          ? 'default'
          : nextFolder === 'archive'
            ? 'archive'
            : nextFolder === 'trash'
              ? 'trash'
              : undefined;
      const starredOnly = nextFolder === 'starred';

      if (bridge) {
        if (queryText.trim()) {
          const result = await bridge.searchMessages({
            query: queryText,
            accountId,
            placement,
            starredOnly,
            limit: 80,
          });
          setMessages(result.hits.map((hit) => hit.message));
          setSearchElapsedMs(result.elapsedMs);
        } else {
          const list = await bridge.listMessages({
            accountId,
            placement,
            starredOnly,
            limit: 80,
          });
          setMessages(list);
          setSearchElapsedMs(null);
        }
        return;
      }

      const lower = queryText.trim().toLowerCase();
      const filtered = SEED_MESSAGES.filter((msg) => {
        if (msg.accountId !== accountId) return false;
        if (placement && msg.placement !== placement) return false;
        if (starredOnly && !msg.starred) return false;
        if (!lower) return true;
        return (
          msg.subject.toLowerCase().includes(lower) ||
          msg.from.toLowerCase().includes(lower) ||
          msg.bodyText.toLowerCase().includes(lower)
        );
      });
      setMessages(filtered);
      setSearchElapsedMs(lower ? 0.2 : null);
    },
    [bridge]
  );

  const handleSyncNow = useCallback(async () => {
    if (!activeAccount || isSyncing) return;
    setIsSyncing(true);
    setSyncStatus(`Syncing ${activeAccount.email} via /v1...`);
    try {
      const token = bridge ? await bridge.getAuthToken(activeAccount.id) : null;
      if (!token) {
        setSyncStatus('No keychain Bearer token for account. Click + Account to sign in via OAuth.');
        return;
      }
      const cursor = bridge ? await bridge.getDeltaCursor(activeAccount.id) : { accountId: activeAccount.id, token: null, updatedAt: null };
      const synced = await syncInboxFromV1({
        accountId: activeAccount.id,
        apiBaseUrl: activeAccount.apiBaseUrl,
        bearerToken: token,
        cursor: cursor.token,
      });

      if (bridge && synced.messages.length > 0) {
        await bridge.upsertMessages(synced.messages);
        await bridge.setDeltaCursor({
          accountId: activeAccount.id,
          token: synced.nextCursor,
          updatedAt: new Date().toISOString(),
        });
      }
      await loadLocalMessages(activeAccount.id, searchText, folder);
      setSyncStatus(`Synced ${synced.messages.length} messages at ${new Date().toLocaleTimeString()}`);
    } catch (err) {
      setSyncStatus(err instanceof Error ? err.message : 'Sync failed');
    } finally {
      setIsSyncing(false);
    }
  }, [activeAccount, bridge, folder, isSyncing, loadLocalMessages, searchText]);

  useEffect(() => {
    if (!bridge) return;
    void bridge.upsertMessages(SEED_MESSAGES);
    const unsubCompose = bridge.onComposeRequested(() => setComposeOpen(true));
    const unsubSync = bridge.onSyncRequested(() => void handleSyncNow());
    const unsubOAuth = bridge.onOAuthCallback((payload) => {
      if (payload.error) {
        setSyncStatus(`OAuth error: ${payload.error}`);
      } else if (payload.code) {
        setSyncStatus(`OAuth callback received for ${payload.account ?? 'new account'}`);
      }
    });
    return () => {
      unsubCompose();
      unsubSync();
      unsubOAuth();
    };
  }, [bridge, handleSyncNow]);

  useEffect(() => {
    if (!activeAccount) return;
    void loadLocalMessages(activeAccount.id, searchText, folder);
  }, [activeAccount, folder, loadLocalMessages, searchText]);

  const selectedMessage = useMemo(
    () => messages.find((msg) => msg.id === selectedId) ?? messages[0] ?? null,
    [messages, selectedId]
  );

  const handleToggleFlag = async (message: CachedMailMessage, patch: { read?: boolean; starred?: boolean; placement?: MailPlacement }) => {
    const updated: CachedMailMessage = { ...message, ...patch };
    setMessages((prev) => prev.map((item) => (item.id === message.id ? updated : item)));
    if (bridge) {
      await bridge.upsertMessages([updated]);
      const token = await bridge.getAuthToken(message.accountId);
      if (token && activeAccount) {
        await patchMessageOnV1({
          apiBaseUrl: activeAccount.apiBaseUrl,
          bearerToken: token,
          messageId: message.id,
          patch,
        }).catch(() => undefined);
      }
    }
  };

  const handleAddAccount = async () => {
    if (accounts.length >= MAX_ACCOUNTS) {
      setSyncStatus(`Account limit reached (${MAX_ACCOUNTS} accounts max).`);
      return;
    }
    const nextIndex = accounts.length + 1;
    const workspace: AccountWorkspaceKind = nextIndex % 2 === 0 ? 'personal' : 'work';
    const nextAccount: DesktopAccountConfig = {
      id: `acct-${workspace}-${nextIndex}`,
      email: `account${nextIndex}@zmail.dev`,
      displayName: `Account ${nextIndex} (${workspace === 'work' ? 'Work' : 'Personal'})`,
      workspace,
      apiBaseUrl: DEFAULT_API_BASE,
      unreadCount: 0,
    };
    setAccounts((prev) => [...prev, nextAccount]);
    setActiveAccountId(nextAccount.id);
    if (bridge) {
      await bridge.openOAuthUrl({
        url: `${DEFAULT_API_BASE}/v1/auth/oauth/start?redirect_uri=${encodeURIComponent('zmail://oauth/callback')}&account=${encodeURIComponent(nextAccount.id)}`,
      });
    }
  };

  const handleSendCompose = async () => {
    if (!activeAccount || !composeTo.trim() || !composeSubject.trim()) return;
    const token = bridge ? await bridge.getAuthToken(activeAccount.id) : null;
    if (token) {
      await sendMessageOnV1({
        apiBaseUrl: activeAccount.apiBaseUrl,
        bearerToken: token,
        to: composeTo.split(',').map((item) => item.trim()).filter(Boolean),
        subject: composeSubject.trim(),
        bodyText: composeBody,
      });
    }
    setComposeOpen(false);
    setComposeTo('');
    setComposeSubject('');
    setComposeBody('');
    setSyncStatus(`Sent "${composeSubject.trim()}"`);
  };

  return (
    <div className="desktop-shell">
      <aside className="sidebar-pane">
        <div className="sidebar-header">
          <div className="brand-title">Zmail</div>
          <button type="button" className="btn-primary" onClick={() => setComposeOpen(true)}>
            Compose
          </button>
        </div>

        <div className="workspace-tabs" role="tablist" aria-label="Workspace mode">
          {(['all', 'work', 'personal'] as const).map((mode) => (
            <button
              key={mode}
              type="button"
              className={workspaceFilter === mode ? 'tab active' : 'tab'}
              onClick={() => setWorkspaceFilter(mode)}
            >
              {mode === 'all' ? 'All' : mode === 'work' ? 'Work' : 'Personal'}
            </button>
          ))}
        </div>

        <nav className="folder-list" aria-label="Folders">
          {(['inbox', 'starred', 'archive', 'trash'] as const).map((item) => (
            <button
              key={item}
              type="button"
              className={folder === item ? 'nav-item active' : 'nav-item'}
              onClick={() => setFolder(item)}
            >
              {item[0]?.toUpperCase() + item.slice(1)}
            </button>
          ))}
        </nav>

        <div className="accounts-section">
          <div className="section-header">
            <span>Accounts ({accounts.length}/{MAX_ACCOUNTS})</span>
            <button type="button" className="btn-ghost" onClick={() => void handleAddAccount()}>
              + Add
            </button>
          </div>
          <div className="account-list">
            {visibleAccounts.map((acct) => (
              <button
                key={acct.id}
                type="button"
                className={acct.id === activeAccount?.id ? 'account-item active' : 'account-item'}
                onClick={() => setActiveAccountId(acct.id)}
              >
                <span className="account-name">{acct.displayName}</span>
                <span className="account-email">{acct.email}</span>
              </button>
            ))}
          </div>
        </div>
      </aside>

      <section className="list-pane">
        <div className="list-toolbar">
          <input
            type="search"
            className="search-input"
            placeholder="Instant local search..."
            value={searchText}
            onChange={(event) => setSearchText(event.target.value)}
          />
          <button type="button" className="btn-secondary" disabled={isSyncing} onClick={() => void handleSyncNow()}>
            {isSyncing ? 'Syncing...' : 'Sync Now'}
          </button>
        </div>
        <div className="status-strip">
          <span>{syncStatus}</span>
          {searchElapsedMs !== null && <span>Local FTS: {searchElapsedMs} ms</span>}
        </div>
        <div className="message-list">
          {messages.map((msg) => (
            <button
              key={`${msg.accountId}:${msg.id}`}
              type="button"
              className={`message-row ${selectedMessage?.id === msg.id ? 'selected' : ''} ${msg.read ? '' : 'unread'}`}
              onClick={() => {
                setSelectedId(msg.id);
                if (!msg.read) {
                  void handleToggleFlag(msg, { read: true });
                }
              }}
            >
              <div className="row-top">
                <span className="row-from">{msg.from}</span>
                <span className="row-date">{new Date(msg.createdAt).toLocaleDateString()}</span>
              </div>
              <div className="row-subject">{msg.subject}</div>
              <div className="row-snippet">{msg.snippet}</div>
            </button>
          ))}
          {messages.length === 0 && <div className="empty-state">No messages match the current filter.</div>}
        </div>
      </section>

      <main className="reader-pane">
        {selectedMessage ? (
          <article className="reader-content">
            <header className="reader-header">
              <h1>{selectedMessage.subject}</h1>
              <div className="reader-actions">
                <button type="button" className="btn-ghost" onClick={() => void handleToggleFlag(selectedMessage, { starred: !selectedMessage.starred })}>
                  {selectedMessage.starred ? 'Unstar' : 'Star'}
                </button>
                <button type="button" className="btn-ghost" onClick={() => void handleToggleFlag(selectedMessage, { read: !selectedMessage.read })}>
                  {selectedMessage.read ? 'Mark Unread' : 'Mark Read'}
                </button>
                <button type="button" className="btn-ghost" onClick={() => void handleToggleFlag(selectedMessage, { placement: 'archive' })}>
                  Archive
                </button>
              </div>
            </header>
            <div className="reader-meta">
              <div><strong>From:</strong> {selectedMessage.from}</div>
              <div><strong>To:</strong> {selectedMessage.to.join(', ')}</div>
              <div><strong>Date:</strong> {new Date(selectedMessage.createdAt).toLocaleString()}</div>
            </div>
            <div className="reader-body">{selectedMessage.bodyText}</div>
          </article>
        ) : (
          <div className="empty-state">Select a message to read.</div>
        )}
      </main>

      {composeOpen && (
        <div className="compose-modal" role="dialog" aria-label="Compose message">
          <div className="compose-card">
            <div className="compose-header">
              <strong>New Message ({activeAccount?.email})</strong>
              <button type="button" className="btn-ghost" onClick={() => setComposeOpen(false)}>Close</button>
            </div>
            <input type="text" placeholder="To (comma separated)" value={composeTo} onChange={(e) => setComposeTo(e.target.value)} />
            <input type="text" placeholder="Subject" value={composeSubject} onChange={(e) => setComposeSubject(e.target.value)} />
            <textarea rows={8} placeholder="Write your message..." value={composeBody} onChange={(e) => setComposeBody(e.target.value)} />
            <div className="compose-footer">
              <button type="button" className="btn-primary" onClick={() => void handleSendCompose()}>Send via /v1</button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
