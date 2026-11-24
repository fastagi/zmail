import path from 'node:path';
import { fileURLToPath } from 'node:url';
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
  type OpenOAuthUrlResult,
  type SetTokenRequest,
} from '../shared/ipc-channels.ts';
import { KeychainStore, createFileKeychainBackend } from './keychain.ts';
import { LocalMailStore, createFileStorePersistence } from './local-store.ts';

export const ZMAIL_PROTOCOL = 'zmail';

export interface HardenedWindowOptions {
  width: number;
  height: number;
  minWidth: number;
  minHeight: number;
  title: string;
  titleBarStyle: 'hiddenInset';
  show: boolean;
  webPreferences: {
    preload: string;
    contextIsolation: true;
    nodeIntegration: false;
    sandbox: true;
    webSecurity: true;
    allowRunningInsecureContent: false;
  };
}

/**
 * Builds the hardened Electron BrowserWindow options required by Zmail desktop
 * security boundaries (isolated context, sandboxed renderer, no Node globals).
 */
export function createHardenedWindowOptions(preloadPath: string): HardenedWindowOptions {
  return {
    width: 1320,
    height: 860,
    minWidth: 960,
    minHeight: 620,
    title: 'Zmail',
    titleBarStyle: 'hiddenInset',
    show: false,
    webPreferences: {
      preload: preloadPath,
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
      webSecurity: true,
      allowRunningInsecureContent: false,
    },
  };
}

/**
 * Parses `zmail://oauth/callback?code=...&state=...&account=...` deep links.
 * Returns null for unrecognized schemes or paths.
 */
export function parseOAuthDeepLink(rawUrl: string): OAuthCallbackPayload | null {
  try {
    const parsed = new URL(rawUrl);
    if (parsed.protocol !== `${ZMAIL_PROTOCOL}:`) {
      return null;
    }
    const route = `${parsed.hostname}${parsed.pathname}`.replace(/\/+$/, '');
    if (route !== 'oauth/callback') {
      return null;
    }
    return {
      code: parsed.searchParams.get('code'),
      state: parsed.searchParams.get('state'),
      account: parsed.searchParams.get('account'),
      error: parsed.searchParams.get('error'),
      rawUrl,
    };
  } catch {
    return null;
  }
}

/**
 * Validates that an external OAuth URL is safe to hand off to the OS browser.
 */
export function isAllowedExternalOAuthUrl(rawUrl: string): boolean {
  try {
    const parsed = new URL(rawUrl);
    if (parsed.protocol === 'https:') {
      return true;
    }
    if (parsed.protocol === 'http:' && (parsed.hostname === 'localhost' || parsed.hostname === '127.0.0.1')) {
      return true;
    }
    return false;
  } catch {
    return false;
  }
}

function extractDeepLinkArg(argv: readonly string[]): string | null {
  for (const arg of argv) {
    if (arg.startsWith(`${ZMAIL_PROTOCOL}://`)) {
      return arg;
    }
  }
  return null;
}

export async function bootstrapDesktopApp(): Promise<void> {
  const electron = await import('electron');
  const { app, BrowserWindow, ipcMain, Menu, nativeImage, Notification, safeStorage, shell, Tray } = electron;

  const gotSingleInstanceLock = app.requestSingleInstanceLock();
  if (!gotSingleInstanceLock) {
    app.quit();
    return;
  }

  const currentDir = path.dirname(fileURLToPath(import.meta.url));
  const preloadPath = path.resolve(currentDir, '../preload/index.js');
  const rendererIndexHtml = path.resolve(currentDir, '../renderer/index.html');
  const devServerUrl = process.env.ZMAIL_DESKTOP_DEV_URL;

  const userDataDir = app.getPath('userData');
  const keychain = new KeychainStore(
    safeStorage,
    createFileKeychainBackend(path.join(userDataDir, 'keychain.enc.json'))
  );
  const localStore = new LocalMailStore(
    createFileStorePersistence(path.join(userDataDir, 'mail-cache.json'))
  );

  let mainWindow: InstanceType<typeof BrowserWindow> | null = null;
  let tray: InstanceType<typeof Tray> | null = null;
  let unreadCount = localStore.getUnreadCount();
  let pendingDeepLink: OAuthCallbackPayload | null = null;

  const updateTrayAndBadge = (nextUnread: number): void => {
    unreadCount = Math.max(0, Math.floor(nextUnread));
    app.setBadgeCount(unreadCount);
    if (!tray) return;
    const label = unreadCount > 0 ? `Zmail (${unreadCount} unread)` : 'Zmail';
    tray.setToolTip(label);
    if (process.platform === 'darwin') {
      tray.setTitle(unreadCount > 0 ? ` ${unreadCount}` : '');
    }
    tray.setContextMenu(
      Menu.buildFromTemplate([
        { label, enabled: false },
        { type: 'separator' },
        {
          label: 'Quick Compose',
          click: () => {
            showMainWindow();
            mainWindow?.webContents.send(IPC_CHANNELS.EVENT_COMPOSE_REQUESTED);
          },
        },
        {
          label: 'Sync Now',
          click: () => {
            mainWindow?.webContents.send(IPC_CHANNELS.EVENT_SYNC_REQUESTED);
          },
        },
        {
          label: 'Open Zmail',
          click: () => showMainWindow(),
        },
        { type: 'separator' },
        { label: 'Quit Zmail', role: 'quit' },
      ])
    );
  };

  const showMainWindow = (): void => {
    if (!mainWindow) return;
    if (mainWindow.isMinimized()) {
      mainWindow.restore();
    }
    mainWindow.show();
    mainWindow.focus();
  };

  const dispatchDeepLink = (rawUrl: string): void => {
    const payload = parseOAuthDeepLink(rawUrl);
    if (!payload) return;
    if (mainWindow && !mainWindow.isDestroyed()) {
      showMainWindow();
      mainWindow.webContents.send(IPC_CHANNELS.EVENT_OAUTH_CALLBACK, payload);
    } else {
      pendingDeepLink = payload;
    }
  };

  const createMainWindow = async (): Promise<void> => {
    mainWindow = new BrowserWindow(createHardenedWindowOptions(preloadPath));

    mainWindow.webContents.setWindowOpenHandler(({ url }) => {
      if (isAllowedExternalOAuthUrl(url)) {
        void shell.openExternal(url);
      }
      return { action: 'deny' };
    });

    mainWindow.webContents.on('will-navigate', (event, targetUrl) => {
      const allowedDevOrigin = devServerUrl ? new URL(devServerUrl).origin : null;
      const isFileLoad = targetUrl.startsWith('file://');
      const isAllowedDev = allowedDevOrigin !== null && targetUrl.startsWith(allowedDevOrigin);
      if (!isFileLoad && !isAllowedDev) {
        event.preventDefault();
      }
    });

    mainWindow.once('ready-to-show', () => {
      mainWindow?.show();
      if (pendingDeepLink) {
        mainWindow?.webContents.send(IPC_CHANNELS.EVENT_OAUTH_CALLBACK, pendingDeepLink);
        pendingDeepLink = null;
      }
    });

    mainWindow.on('closed', () => {
      mainWindow = null;
    });

    if (devServerUrl) {
      await mainWindow.loadURL(devServerUrl);
    } else {
      await mainWindow.loadFile(rendererIndexHtml);
    }
  };

  const registerIpcHandlers = (): void => {
    ipcMain.handle(IPC_CHANNELS.AUTH_GET_TOKEN, (_event, accountId: string) => keychain.getToken(accountId));
    ipcMain.handle(IPC_CHANNELS.AUTH_SET_TOKEN, (_event, req: SetTokenRequest) => {
      keychain.setToken(req.accountId, req.token);
      return { saved: true };
    });
    ipcMain.handle(IPC_CHANNELS.AUTH_DELETE_TOKEN, (_event, accountId: string) => ({
      deleted: keychain.deleteToken(accountId),
    }));
    ipcMain.handle(IPC_CHANNELS.AUTH_LIST_ACCOUNTS, () => keychain.listAccountIds());
    ipcMain.handle(IPC_CHANNELS.AUTH_OPEN_OAUTH_URL, async (_event, req: OpenOAuthUrlRequest): Promise<OpenOAuthUrlResult> => {
      if (!isAllowedExternalOAuthUrl(req.url)) {
        return { opened: false, reason: 'Blocked disallowed OAuth URL protocol.' };
      }
      await shell.openExternal(req.url);
      return { opened: true };
    });

    ipcMain.handle(IPC_CHANNELS.STORE_UPSERT_MESSAGES, (_event, messages: CachedMailMessage[]) => {
      const count = localStore.upsertMessages(messages);
      updateTrayAndBadge(localStore.getUnreadCount());
      return { count };
    });
    ipcMain.handle(IPC_CHANNELS.STORE_DELETE_MESSAGE, (_event, req: DeleteMessageRequest) => {
      const deleted = localStore.deleteMessage(req.accountId, req.messageId);
      updateTrayAndBadge(localStore.getUnreadCount());
      return { deleted };
    });
    ipcMain.handle(IPC_CHANNELS.STORE_SEARCH_MESSAGES, (_event, query: LocalSearchQuery) => localStore.search(query));
    ipcMain.handle(IPC_CHANNELS.STORE_LIST_MESSAGES, (_event, query?: ListMessagesQuery) => localStore.listMessages(query));
    ipcMain.handle(IPC_CHANNELS.STORE_GET_STATS, () => localStore.getStats());

    ipcMain.handle(IPC_CHANNELS.SYNC_GET_CURSOR, (_event, accountId: string) => localStore.getDeltaCursor(accountId));
    ipcMain.handle(IPC_CHANNELS.SYNC_SET_CURSOR, (_event, cursor: DeltaCursorState) => localStore.setDeltaCursor(cursor));
    ipcMain.handle(IPC_CHANNELS.SYNC_RESET_ACCOUNT, (_event, accountId: string) => {
      localStore.resetAccount(accountId);
      updateTrayAndBadge(localStore.getUnreadCount());
      return { reset: true };
    });

    ipcMain.handle(IPC_CHANNELS.APP_SET_BADGE_COUNT, (_event, count: number) => {
      updateTrayAndBadge(count);
      return { count: unreadCount };
    });
    ipcMain.handle(IPC_CHANNELS.APP_NOTIFY, (_event, payload: NativeNotificationPayload) => {
      if (!Notification.isSupported()) {
        return { shown: false };
      }
      const notification = new Notification({
        title: payload.title,
        body: payload.body,
        silent: payload.silent ?? false,
      });
      notification.on('click', () => showMainWindow());
      notification.show();
      return { shown: true };
    });
  };

  if (process.defaultApp && process.argv.length >= 2 && process.argv[1]) {
    app.setAsDefaultProtocolClient(ZMAIL_PROTOCOL, process.execPath, [path.resolve(process.argv[1])]);
  } else {
    app.setAsDefaultProtocolClient(ZMAIL_PROTOCOL);
  }

  app.on('second-instance', (_event, commandLine) => {
    showMainWindow();
    const deepLink = extractDeepLinkArg(commandLine);
    if (deepLink) {
      dispatchDeepLink(deepLink);
    }
  });

  app.on('open-url', (event, url) => {
    event.preventDefault();
    dispatchDeepLink(url);
  });

  await app.whenReady();
  registerIpcHandlers();
  tray = new Tray(nativeImage.createEmpty());
  updateTrayAndBadge(unreadCount);
  await createMainWindow();

  const initialDeepLink = extractDeepLinkArg(process.argv);
  if (initialDeepLink) {
    dispatchDeepLink(initialDeepLink);
  }

  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) {
      void createMainWindow();
    } else {
      showMainWindow();
    }
  });

  app.on('window-all-closed', () => {
    if (process.platform !== 'darwin') {
      app.quit();
    }
  });
}

if (typeof process !== 'undefined' && Boolean(process.versions?.electron)) {
  void bootstrapDesktopApp();
}
