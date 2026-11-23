import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { runInNewContext } from 'node:vm';
import { describe, expect, it, vi } from 'vitest';

type Listener = (event: Record<string, unknown>) => void;

function loadWorker(windows: Array<Record<string, unknown>> = []) {
  const listeners = new Map<string, Listener>();
  const showNotification = vi.fn<(title: string, options: unknown) => Promise<void>>(
    async () => undefined
  );
  const openWindow = vi.fn(async () => null);
  const self = {
    location: { origin: 'https://app.example.com' },
    registration: { showNotification },
    clients: { claim: vi.fn(), matchAll: vi.fn(async () => windows), openWindow },
    skipWaiting: vi.fn(),
    addEventListener: (type: string, listener: Listener) => listeners.set(type, listener),
  };
  const source = readFileSync(resolve(process.cwd(), 'public/sw.js'), 'utf8');
  runInNewContext(source, { self, URL });
  return { listeners, showNotification, openWindow, self };
}

async function dispatch(listener: Listener | undefined, event: Record<string, unknown>) {
  let pending: Promise<unknown> = Promise.resolve();
  listener?.({ ...event, waitUntil: (promise: Promise<unknown>) => (pending = promise) });
  await pending;
}

describe('push service worker', () => {
  it('registers no fetch handler, so nothing is cached', () => {
    const { listeners } = loadWorker();
    expect([...listeners.keys()].sort()).toEqual(['activate', 'install', 'notificationclick', 'push']);
  });

  it('shows the pushed title, body, tag, and url', async () => {
    const { listeners, showNotification } = loadWorker();
    await dispatch(listeners.get('push'), {
      data: {
        json: () => ({
          title: 'Ada Lovelace',
          body: 'Quarterly numbers',
          url: '/console/mailboxes?org=1&mailbox=2&open=3',
          tag: 'mail-3',
        }),
      },
    });

    expect(showNotification).toHaveBeenCalledWith('Ada Lovelace', {
      body: 'Quarterly numbers',
      tag: 'mail-3',
      data: { url: '/console/mailboxes?org=1&mailbox=2&open=3' },
      icon: '/icons/icon-192.png',
      badge: '/icons/badge-96.png',
    });
  });

  it('falls back to the inbox for a missing payload or a foreign url', async () => {
    const { listeners, showNotification } = loadWorker();
    await dispatch(listeners.get('push'), {
      data: { json: () => ({ title: 'x', url: 'https://evil.example/' }) },
    });
    await dispatch(listeners.get('push'), { data: null });

    expect(showNotification.mock.calls[0][1]).toMatchObject({
      data: { url: '/console/mailboxes' },
    });
    expect(showNotification.mock.calls[1][0]).toBe('Zmail');
  });

  it('focuses and navigates an open Zmail window on click', async () => {
    const navigate = vi.fn(async () => null);
    const client = {
      url: 'https://app.example.com/console',
      focused: false,
      focus: vi.fn(async () => ({ navigate })),
    };
    const { listeners, openWindow } = loadWorker([client]);
    const close = vi.fn();
    await dispatch(listeners.get('notificationclick'), {
      notification: { close, data: { url: '/console/mailboxes?open=3' } },
    });

    expect(close).toHaveBeenCalled();
    expect(navigate).toHaveBeenCalledWith('https://app.example.com/console/mailboxes?open=3');
    expect(openWindow).not.toHaveBeenCalled();
  });

  it('opens a new window when none is open, never to another origin', async () => {
    const { listeners, openWindow } = loadWorker([]);
    await dispatch(listeners.get('notificationclick'), {
      notification: { close: vi.fn(), data: { url: '//evil.example/x' } },
    });

    expect(openWindow).toHaveBeenCalledWith('https://app.example.com/console/mailboxes');
  });
});
