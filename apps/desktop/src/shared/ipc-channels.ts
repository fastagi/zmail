/**
 * Strictly typed IPC channel names and request/response contracts shared
 * between the Electron main process, preload bridge, and renderer.
 */

export const IPC_CHANNELS = {
  AUTH_GET_TOKEN: 'zmail:auth:get-token',
  AUTH_SET_TOKEN: 'zmail:auth:set-token',
  AUTH_DELETE_TOKEN: 'zmail:auth:delete-token',
  AUTH_LIST_ACCOUNTS: 'zmail:auth:list-accounts',
  AUTH_OPEN_OAUTH_URL: 'zmail:auth:open-oauth-url',

  STORE_UPSERT_MESSAGES: 'zmail:store:upsert-messages',
  STORE_DELETE_MESSAGE: 'zmail:store:delete-message',
  STORE_SEARCH_MESSAGES: 'zmail:store:search-messages',
  STORE_LIST_MESSAGES: 'zmail:store:list-messages',
  STORE_GET_STATS: 'zmail:store:get-stats',

  SYNC_GET_CURSOR: 'zmail:sync:get-cursor',
  SYNC_SET_CURSOR: 'zmail:sync:set-cursor',
  SYNC_RESET_ACCOUNT: 'zmail:sync:reset-account',

  APP_SET_BADGE_COUNT: 'zmail:app:set-badge-count',
  APP_NOTIFY: 'zmail:app:notify',

  EVENT_OAUTH_CALLBACK: 'zmail:event:oauth-callback',
  EVENT_COMPOSE_REQUESTED: 'zmail:event:compose-requested',
  EVENT_SYNC_REQUESTED: 'zmail:event:sync-requested',
} as const;

export type IpcChannelName = (typeof IPC_CHANNELS)[keyof typeof IPC_CHANNELS];

export type MailPlacement = 'default' | 'archive' | 'folder' | 'trash';
export type AccountWorkspaceKind = 'personal' | 'work';

export interface CachedMailMessage {
  id: number;
  accountId: string;
  mailboxId: number;
  threadId: number;
  from: string;
  to: string[];
  cc: string[];
  subject: string;
  snippet: string;
  bodyText: string;
  read: boolean;
  starred: boolean;
  placement: MailPlacement;
  attachmentCount: number;
  createdAt: string;
}

export interface LocalSearchQuery {
  query: string;
  accountId?: string;
  placement?: MailPlacement;
  unreadOnly?: boolean;
  starredOnly?: boolean;
  limit?: number;
}

export interface LocalSearchHit {
  message: CachedMailMessage;
  score: number;
  matchedTerms: string[];
  highlight: string;
}

export interface LocalSearchResult {
  query: string;
  total: number;
  elapsedMs: number;
  hits: LocalSearchHit[];
}

export interface ListMessagesQuery {
  accountId?: string;
  placement?: MailPlacement;
  unreadOnly?: boolean;
  starredOnly?: boolean;
  limit?: number;
}

export interface LocalStoreStats {
  totalMessages: number;
  unreadMessages: number;
  indexedTerms: number;
  accountCount: number;
}

export interface DeltaCursorState {
  accountId: string;
  token: string | null;
  updatedAt: string | null;
}

export interface NativeNotificationPayload {
  title: string;
  body: string;
  accountId?: string;
  messageId?: number;
  silent?: boolean;
}

export interface OAuthCallbackPayload {
  code: string | null;
  state: string | null;
  account: string | null;
  error: string | null;
  rawUrl: string;
}

export interface SetTokenRequest {
  accountId: string;
  token: string;
}

export interface DeleteMessageRequest {
  accountId: string;
  messageId: number;
}

export interface OpenOAuthUrlRequest {
  url: string;
}

export interface OpenOAuthUrlResult {
  opened: boolean;
  reason?: string;
}

/**
 * Typed bridge exposed via `contextBridge.exposeInMainWorld('zmailDesktop', ...)`.
 */
export interface ZmailDesktopBridge {
  getAuthToken(accountId: string): Promise<string | null>;
  setAuthToken(request: SetTokenRequest): Promise<{ saved: boolean }>;
  deleteAuthToken(accountId: string): Promise<{ deleted: boolean }>;
  listAuthAccounts(): Promise<string[]>;
  openOAuthUrl(request: OpenOAuthUrlRequest): Promise<OpenOAuthUrlResult>;

  upsertMessages(messages: CachedMailMessage[]): Promise<{ count: number }>;
  deleteMessage(request: DeleteMessageRequest): Promise<{ deleted: boolean }>;
  searchMessages(query: LocalSearchQuery): Promise<LocalSearchResult>;
  searchLocal(query: LocalSearchQuery): Promise<LocalSearchResult>;
  listMessages(query?: ListMessagesQuery): Promise<CachedMailMessage[]>;
  getStoreStats(): Promise<LocalStoreStats>;

  getDeltaCursor(accountId: string): Promise<DeltaCursorState>;
  setDeltaCursor(cursor: DeltaCursorState): Promise<DeltaCursorState>;
  resetAccountSync(accountId: string): Promise<{ reset: boolean }>;

  setBadgeCount(count: number): Promise<{ count: number }>;
  notify(payload: NativeNotificationPayload): Promise<{ shown: boolean }>;

  onOAuthCallback(listener: (payload: OAuthCallbackPayload) => void): () => void;
  onComposeRequested(listener: () => void): () => void;
  onSyncRequested(listener: () => void): () => void;
}
