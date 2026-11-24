import { contextBridge, ipcRenderer, type IpcRendererEvent } from 'electron';
import {
  IPC_CHANNELS,
  type CachedMailMessage,
  type DeleteMessageRequest,
  type DeltaCursorState,
  type ListMessagesQuery,
  type LocalSearchQuery,
  type NativeNotificationPayload,
  type OAuthCallbackPayload,
  type OpenOAuthUrlRequest,
  type SetTokenRequest,
  type ZmailDesktopBridge,
} from '../shared/ipc-channels.ts';

const zmailDesktopBridge: ZmailDesktopBridge = {
  getAuthToken: (accountId: string) =>
    ipcRenderer.invoke(IPC_CHANNELS.AUTH_GET_TOKEN, accountId),

  setAuthToken: (request: SetTokenRequest) =>
    ipcRenderer.invoke(IPC_CHANNELS.AUTH_SET_TOKEN, request),

  deleteAuthToken: (accountId: string) =>
    ipcRenderer.invoke(IPC_CHANNELS.AUTH_DELETE_TOKEN, accountId),

  listAuthAccounts: () =>
    ipcRenderer.invoke(IPC_CHANNELS.AUTH_LIST_ACCOUNTS),

  openOAuthUrl: (request: OpenOAuthUrlRequest) =>
    ipcRenderer.invoke(IPC_CHANNELS.AUTH_OPEN_OAUTH_URL, request),

  upsertMessages: (messages: CachedMailMessage[]) =>
    ipcRenderer.invoke(IPC_CHANNELS.STORE_UPSERT_MESSAGES, messages),

  deleteMessage: (request: DeleteMessageRequest) =>
    ipcRenderer.invoke(IPC_CHANNELS.STORE_DELETE_MESSAGE, request),

  searchMessages: (query: LocalSearchQuery) =>
    ipcRenderer.invoke(IPC_CHANNELS.STORE_SEARCH_MESSAGES, query),

  listMessages: (query?: ListMessagesQuery) =>
    ipcRenderer.invoke(IPC_CHANNELS.STORE_LIST_MESSAGES, query),

  getStoreStats: () =>
    ipcRenderer.invoke(IPC_CHANNELS.STORE_GET_STATS),

  getDeltaCursor: (accountId: string) =>
    ipcRenderer.invoke(IPC_CHANNELS.SYNC_GET_CURSOR, accountId),

  setDeltaCursor: (cursor: DeltaCursorState) =>
    ipcRenderer.invoke(IPC_CHANNELS.SYNC_SET_CURSOR, cursor),

  resetAccountSync: (accountId: string) =>
    ipcRenderer.invoke(IPC_CHANNELS.SYNC_RESET_ACCOUNT, accountId),

  setBadgeCount: (count: number) =>
    ipcRenderer.invoke(IPC_CHANNELS.APP_SET_BADGE_COUNT, count),

  notify: (payload: NativeNotificationPayload) =>
    ipcRenderer.invoke(IPC_CHANNELS.APP_NOTIFY, payload),

  onOAuthCallback: (listener: (payload: OAuthCallbackPayload) => void) => {
    const handler = (_event: IpcRendererEvent, payload: OAuthCallbackPayload) => {
      listener(payload);
    };
    ipcRenderer.on(IPC_CHANNELS.EVENT_OAUTH_CALLBACK, handler);
    return () => {
      ipcRenderer.removeListener(IPC_CHANNELS.EVENT_OAUTH_CALLBACK, handler);
    };
  },

  onComposeRequested: (listener: () => void) => {
    const handler = () => listener();
    ipcRenderer.on(IPC_CHANNELS.EVENT_COMPOSE_REQUESTED, handler);
    return () => {
      ipcRenderer.removeListener(IPC_CHANNELS.EVENT_COMPOSE_REQUESTED, handler);
    };
  },

  onSyncRequested: (listener: () => void) => {
    const handler = () => listener();
    ipcRenderer.on(IPC_CHANNELS.EVENT_SYNC_REQUESTED, handler);
    return () => {
      ipcRenderer.removeListener(IPC_CHANNELS.EVENT_SYNC_REQUESTED, handler);
    };
  },
};

contextBridge.exposeInMainWorld('zmailDesktop', zmailDesktopBridge);
