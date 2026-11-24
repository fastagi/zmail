import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  MAIL_UI_MAX_CONNECTED_ACCOUNTS,
  MAIL_UI_PROVIDER_PRESETS,
  MailShell,
  buildReplyPayload,
  computeTotalUnread,
  computeUnreadByAccount,
  detectProviderFromEmail,
  resolveReplySenderAccount,
  type MailUiAccount,
  type MailUiComposePayload,
  type MailUiMessage,
  type WorkspaceMode,
} from '@zmail/mail-ui';
import type {
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
import { ComposeModal, QuickConnectModal, type QuickConnectSubmit } from './QuickConnectModal.tsx';

declare global {
  interface Window {
    zmailDesktop?: ZmailDesktopBridge;
  }
}

function toMailUiAccount(acct: DesktopAccountConfig, syncing: boolean, activeId: string | null): MailUiAccount {
  const detected = detectProviderFromEmail(acct.email);
  return {
    id: acct.id,
    email: acct.email,
    displayName: acct.displayName,
    providerId: acct.providerId ?? detected?.preset.id ?? 'custom',
    kind: acct.workspace === 'work' ? 'hosted' : 'external',
    mode: acct.workspace,
    unreadCount: acct.unreadCount,
    syncStatus: syncing && (!activeId || activeId === acct.id) ? 'syncing' : 'synced',
    canSend: true,
  };
}

function toMailUiMessage(msg: CachedMailMessage, emailByAccountId: ReadonlyMap<string, string>): MailUiMessage {
  return {
    id: String(msg.id),
    threadId: String(msg.threadId),
    accountId: msg.accountId,
    accountEmail: emailByAccountId.get(msg.accountId),
    from: msg.from,
    to: msg.to,
    cc: msg.cc,
    subject: msg.subject,
    snippet: msg.snippet,
    bodyText: msg.bodyText,
    receivedAt: msg.createdAt,
    read: msg.read,
    starred: msg.starred,
    archived: msg.placement === 'archive',
    attachmentCount: msg.attachmentCount,
  };
}

export function App() {
  const bridge = window.zmailDesktop;
  const [mode, setMode] = useState<WorkspaceMode>('work');
  const [accounts, setAccounts] = useState<DesktopAccountConfig[]>(INITIAL_ACCOUNTS);
  const [selectedAccountId, setSelectedAccountId] = useState<string | null>(null);
  const [searchText, setSearchText] = useState('');
  const [searchElapsedMs, setSearchElapsedMs] = useState<number | null>(null);
  const [messages, setMessages] = useState<CachedMailMessage[]>(SEED_MESSAGES);
  const [selectedMessageId, setSelectedMessageId] = useState<string | null>(
    SEED_MESSAGES[0] ? String(SEED_MESSAGES[0].id) : null
  );
  const [syncStatus, setSyncStatus] = useState('Offline cache ready');
  const [isSyncing, setIsSyncing] = useState(false);
  const [composeDraft, setComposeDraft] = useState<MailUiComposePayload | null>(null);
  const [composeToText, setComposeToText] = useState('');
  const [connectOpen, setConnectOpen] = useState(false);

  const emailByAccountId = useMemo(() => new Map(accounts.map((a) => [a.id, a.email])), [accounts]);
  const mailUiAccounts = useMemo(
    () => accounts.map((a) => toMailUiAccount(a, isSyncing, selectedAccountId)),
    [accounts, isSyncing, selectedAccountId]
  );
  const mailUiMessages = useMemo(
    () => messages.map((m) => toMailUiMessage(m, emailByAccountId)),
    [messages, emailByAccountId]
  );
  const activeAccount = useMemo(
    () =>
      accounts.find((a) => a.id === selectedAccountId) ??
      accounts.find((a) => a.workspace === mode) ??
      accounts[0] ??
      null,
    [accounts, mode, selectedAccountId]
  );
  const totalUnread = useMemo(
    () => computeTotalUnread(computeUnreadByAccount(mailUiMessages, mailUiAccounts), mailUiAccounts),
    [mailUiAccounts, mailUiMessages]
  );

  const loadLocalMessages = useCallback(async (accountId: string | null, queryText: string) => {
    const trimmed = queryText.trim();
    if (bridge) {
      if (trimmed) {
        const result = await bridge.searchLocal({ query: trimmed, accountId: accountId ?? undefined, limit: 80 });
        setMessages(result.hits.map((hit) => hit.message));
        setSearchElapsedMs(result.elapsedMs);
      } else {
        setMessages(await bridge.listMessages({ accountId: accountId ?? undefined, limit: 80 }));
        setSearchElapsedMs(null);
      }
      return;
    }
    const lower = trimmed.toLowerCase();
    setMessages(
      SEED_MESSAGES.filter((msg) => {
        if (accountId && msg.accountId !== accountId) return false;
        if (!lower) return true;
        return (
          msg.subject.toLowerCase().includes(lower) ||
          msg.from.toLowerCase().includes(lower) ||
          msg.bodyText.toLowerCase().includes(lower)
        );
      })
    );
    setSearchElapsedMs(lower ? 0.2 : null);
  }, [bridge]);

  const handleSyncNow = useCallback(async () => {
    if (!activeAccount || isSyncing) return;
    setIsSyncing(true);
    setSyncStatus(`Syncing ${activeAccount.email} via /v1...`);
    try {
      const token = bridge ? await bridge.getAuthToken(activeAccount.id) : null;
      if (!token) {
        setSyncStatus('No keychain token yet. Connect a mailbox to sign in.');
        return;
      }
      const cursor = bridge
        ? await bridge.getDeltaCursor(activeAccount.id)
        : { accountId: activeAccount.id, token: null, updatedAt: null };
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
      await loadLocalMessages(selectedAccountId, searchText);
      setSyncStatus(`Synced ${synced.messages.length} messages at ${new Date().toLocaleTimeString()}`);
    } catch (err) {
      setSyncStatus(err instanceof Error ? err.message : 'Sync failed');
    } finally {
      setIsSyncing(false);
    }
  }, [activeAccount, bridge, isSyncing, loadLocalMessages, searchText, selectedAccountId]);

  const openNewCompose = useCallback(() => {
    setComposeDraft({
      mode: 'new',
      fromAccountId: activeAccount?.id ?? '',
      fromEmail: activeAccount?.email ?? '',
      to: [],
      subject: '',
      bodyText: '',
    });
    setComposeToText('');
  }, [activeAccount]);

  useEffect(() => {
    if (!bridge) return;
    void bridge.upsertMessages(SEED_MESSAGES);
    const unsubCompose = bridge.onComposeRequested(() => openNewCompose());
    const unsubSync = bridge.onSyncRequested(() => void handleSyncNow());
    const unsubOAuth = bridge.onOAuthCallback((payload) => {
      if (payload.error) {
        setSyncStatus(`OAuth error: ${payload.error}`);
      } else if (payload.code) {
        setSyncStatus(`OAuth connected for ${payload.account ?? 'mailbox'}`);
      }
    });
    return () => {
      unsubCompose();
      unsubSync();
      unsubOAuth();
    };
  }, [bridge, handleSyncNow, openNewCompose]);

  useEffect(() => {
    void loadLocalMessages(selectedAccountId, searchText);
  }, [loadLocalMessages, searchText, selectedAccountId]);

  useEffect(() => {
    if (!bridge) return;
    void bridge.setBadgeCount(totalUnread);
  }, [bridge, totalUnread]);

  const patchCachedMessage = useCallback(
    async (messageId: string, patch: { read?: boolean; starred?: boolean; placement?: MailPlacement }) => {
      const target = messages.find((item) => String(item.id) === messageId);
      if (!target) return;
      const updated: CachedMailMessage = { ...target, ...patch };
      setMessages((prev) => prev.map((item) => (item.id === target.id ? updated : item)));
      if (!bridge) return;
      await bridge.upsertMessages([updated]);
      const token = await bridge.getAuthToken(target.accountId);
      const acct = accounts.find((item) => item.id === target.accountId);
      if (token && acct) {
        await patchMessageOnV1({
          apiBaseUrl: acct.apiBaseUrl,
          bearerToken: token,
          messageId: target.id,
          patch,
        }).catch(() => undefined);
      }
    },
    [accounts, bridge, messages]
  );

  const handleSelectMessage = useCallback((messageId: string) => {
    setSelectedMessageId(messageId);
    const found = messages.find((item) => String(item.id) === messageId);
    if (found && !found.read) void patchCachedMessage(messageId, { read: true });
  }, [messages, patchCachedMessage]);

  const openReplyOrForward = useCallback((message: MailUiMessage, replyMode: 'reply' | 'forward') => {
    const sender = resolveReplySenderAccount(message, mailUiAccounts);
    const payload = buildReplyPayload(message, mailUiAccounts, replyMode);
    if (!payload) return;
    setComposeDraft({
      ...payload,
      fromAccountId: sender?.id ?? payload.fromAccountId,
      fromEmail: sender?.email ?? payload.fromEmail,
    });
    setComposeToText(payload.to.join(', '));
  }, [mailUiAccounts]);

  const handleQuickConnect = async ({ email: rawEmail, secret, mode: nextMode, preset }: QuickConnectSubmit) => {
    if (accounts.length >= MAIL_UI_MAX_CONNECTED_ACCOUNTS) {
      setSyncStatus(`Limit reached (${MAIL_UI_MAX_CONNECTED_ACCOUNTS} accounts max).`);
      return;
    }
    const email = rawEmail.trim().toLowerCase();
    if (!email || !email.includes('@')) {
      setSyncStatus('Enter a valid email address to connect.');
      return;
    }
    const accountId = `acct-${preset.id}-${Date.now()}`;
    const nextAccount: DesktopAccountConfig = {
      id: accountId,
      email,
      displayName: `${email.split('@')[0]} (${preset.shortLabel})`,
      workspace: nextMode,
      providerId: preset.id,
      apiBaseUrl: DEFAULT_API_BASE,
      unreadCount: 0,
    };
    setAccounts((prev) => [...prev, nextAccount]);
    setMode(nextMode);
    setSelectedAccountId(accountId);
    setConnectOpen(false);

    if (bridge && secret.trim()) {
      await bridge.setAuthToken({ accountId, token: secret.trim() });
    }
    if (bridge && preset.authStrategy === 'oauth2') {
      await bridge.openOAuthUrl({
        url: `${DEFAULT_API_BASE}/v1/auth/oauth/start?provider=${encodeURIComponent(preset.id)}&redirect_uri=${encodeURIComponent('zmail://oauth/callback')}&account=${encodeURIComponent(accountId)}`,
      });
    }
    setSyncStatus(`Connected ${email} via ${preset.label} (${preset.imapHost}:${preset.imapPort})`);
  };

  const handleSendCompose = async () => {
    if (!composeDraft || !composeDraft.subject.trim()) return;
    const recipients = composeToText.split(',').map((item) => item.trim()).filter(Boolean);
    if (recipients.length === 0) return;
    const senderAcct =
      accounts.find((acct) => acct.id === composeDraft.fromAccountId) ?? activeAccount;
    if (senderAcct && bridge) {
      const token = await bridge.getAuthToken(senderAcct.id);
      if (token) {
        await sendMessageOnV1({
          apiBaseUrl: senderAcct.apiBaseUrl,
          bearerToken: token,
          to: recipients,
          subject: composeDraft.subject.trim(),
          bodyText: composeDraft.bodyText,
        });
      }
    }
    setSyncStatus(`Sent "${composeDraft.subject.trim()}" from ${composeDraft.fromEmail}`);
    setComposeDraft(null);
    setComposeToText('');
  };

  return (
    <div className="desktop-root">
      <header className="desktop-topbar">
        <div className="desktop-brand">
          <span className="brand-title">Zmail Desktop</span>
          <span className="status-pill">{syncStatus}</span>
          {searchElapsedMs !== null && (
            <span className="status-pill">Local FTS: {searchElapsedMs.toFixed(1)} ms</span>
          )}
        </div>
        <div className="desktop-actions">
          <span className="status-pill">Unread: {totalUnread}</span>
          <button type="button" className="btn-secondary" onClick={() => setConnectOpen(true)}>
            + Quick Connect
          </button>
          <button type="button" className="btn-primary" onClick={openNewCompose}>
            Compose
          </button>
        </div>
      </header>

      <div className="desktop-shell-host">
        <MailShell
          mode={mode}
          onModeChange={(nextMode) => {
            setMode(nextMode);
            setSelectedAccountId(null);
          }}
          accounts={mailUiAccounts}
          messages={mailUiMessages}
          selectedAccountId={selectedAccountId}
          onSelectAccount={(accountId) => setSelectedAccountId(accountId)}
          selectedMessageId={selectedMessageId}
          onSelectMessage={handleSelectMessage}
          searchQuery={searchText}
          onSearchChange={setSearchText}
          isSyncing={isSyncing}
          onSyncNow={() => void handleSyncNow()}
          onConnectAccount={() => setConnectOpen(true)}
          onReply={(message) => openReplyOrForward(message, 'reply')}
          onForward={(message) => openReplyOrForward(message, 'forward')}
          onArchive={(message) => void patchCachedMessage(message.id, { placement: 'archive' })}
          onToggleStar={(message) => void patchCachedMessage(message.id, { starred: !message.starred })}
          maxConnectedAccounts={MAIL_UI_MAX_CONNECTED_ACCOUNTS}
        />
      </div>

      {connectOpen && (
        <QuickConnectModal
          presets={MAIL_UI_PROVIDER_PRESETS}
          accountCount={accounts.length}
          maxAccounts={MAIL_UI_MAX_CONNECTED_ACCOUNTS}
          onClose={() => setConnectOpen(false)}
          onSubmit={(payload) => void handleQuickConnect(payload)}
        />
      )}

      {composeDraft && (
        <ComposeModal
          draft={composeDraft}
          toText={composeToText}
          onToTextChange={setComposeToText}
          onChangeDraft={setComposeDraft}
          onClose={() => setComposeDraft(null)}
          onSend={() => void handleSendCompose()}
        />
      )}
    </div>
  );
}
