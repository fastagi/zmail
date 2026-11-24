# Zmail Desktop (`@zmail/desktop`)

Electron desktop client for Zmail following the monorepo architecture in `docs/plans/monorepo-and-mailbox-refactor.zh-CN.md`.

## Architecture Overview

- **Direct Go `/v1` API Access**: Unlike `web/` (which uses a same-origin Next.js BFF + HttpOnly cookies), the desktop client authenticates directly against the Go `/v1` API using revocable Bearer tokens stored in the OS keychain.
- **Hardened Main Process (`src/main/main.ts`)**:
  - `BrowserWindow` runs with `contextIsolation: true`, `nodeIntegration: false`, `sandbox: true`, and `webSecurity: true`.
  - Single-instance lock (`app.requestSingleInstanceLock()`).
  - `zmail://oauth/callback` deep-link handler for external OAuth sign-in flows.
  - System Tray with live unread count, quick Compose, Sync Now, and native OS notifications.
- **OS Keychain Store (`src/main/keychain.ts`)**:
  - Wraps Electron `safeStorage` and refuses to write plaintext tokens to disk if OS encryption is unavailable.
  - Persists encrypted envelopes atomically with `0600` owner-only permissions.
- **Offline Cache & Local Full-Text Search (`src/main/local-store.ts`)**:
  - Multi-account offline message cache (tested up to 30+ Personal/Work accounts) with inverted token indexing (ASCII, email fragments, and CJK unigrams/bigrams), relevance ranking, and per-account delta sync cursor persistence.
  - Designed with a `LocalStorePersistence` seam ready to back onto native SQLite/FTS5 in packaged Electron builds.
- **Typed Preload Bridge (`src/preload/index.ts` & `src/shared/ipc-channels.ts`)**:
  - Exposes a minimal, strictly typed `window.zmailDesktop` surface via `contextBridge.exposeInMainWorld`.
- **MailMaster 3-Pane Renderer (`src/renderer/`)**:
  - React 19 + Vite renderer with Personal/Work workspace filtering, up to 30 accounts in the left sidebar, instant local FTS search, `Sync Now`, and quick Compose.

## Development & Testing

```bash
# Run core unit tests (Node 22/24 native TypeScript test runner)
cd apps/desktop && pnpm test

# Type-check main, preload, shared, and renderer sources
cd apps/desktop && pnpm type-check

# Start Vite renderer dev server (http://127.0.0.1:5174)
cd apps/desktop && pnpm dev

# In a second terminal, compile main/preload and launch Electron against dev server
cd apps/desktop && pnpm build:main && ZMAIL_DESKTOP_DEV_URL=http://127.0.0.1:5174 pnpm dev:electron
```

## Packaging (`electron-builder`)

```bash
# Build renderer + main bundles
cd apps/desktop && pnpm build

# macOS (.dmg for arm64 + x64)
cd apps/desktop && pnpm dist:mac

# Windows (.exe NSIS installer for x64 + arm64)
cd apps/desktop && pnpm dist:win

# Linux (.AppImage for x64 + arm64)
cd apps/desktop && pnpm dist:linux
```
