/*
 * Zmail service worker: shows new-mail push notifications and opens the mail when one is clicked.
 * It deliberately has no fetch handler and caches nothing, so API responses and mail never land in
 * a cache.
 */

const FALLBACK_URL = '/console/mailboxes';
const ICON = '/icons/icon-192.png';
const BADGE = '/icons/badge-96.png';

self.addEventListener('install', () => {
  self.skipWaiting();
});

self.addEventListener('activate', event => {
  event.waitUntil(self.clients.claim());
});

self.addEventListener('push', event => {
  const message = readMessage(event);
  event.waitUntil(
    self.registration.showNotification(message.title, {
      body: message.body,
      tag: message.tag || undefined,
      data: { url: message.url },
      icon: ICON,
      badge: BADGE,
    })
  );
});

self.addEventListener('notificationclick', event => {
  event.notification.close();
  const url = safeUrl(event.notification.data && event.notification.data.url);
  event.waitUntil(openOrFocus(url));
});

function readMessage(event) {
  let payload = {};
  try {
    payload = event.data ? event.data.json() : {};
  } catch {
    payload = {};
  }
  return {
    title: text(payload.title, 160) || 'Zmail',
    body: text(payload.body, 400),
    tag: text(payload.tag, 128),
    url: safeUrl(payload.url),
  };
}

function text(value, max) {
  return typeof value === 'string' ? value.trim().slice(0, max) : '';
}

/** Only a same-origin relative path is opened; anything else falls back to the inbox. */
function safeUrl(value) {
  if (typeof value !== 'string' || !value.startsWith('/') || value.startsWith('//')) {
    return FALLBACK_URL;
  }
  if (value.includes('\\') || /[\u0000-\u001F\u007F]/.test(value)) return FALLBACK_URL;
  try {
    const resolved = new URL(value, self.location.origin);
    if (resolved.origin !== self.location.origin) return FALLBACK_URL;
    return resolved.pathname + resolved.search + resolved.hash;
  } catch {
    return FALLBACK_URL;
  }
}

async function openOrFocus(url) {
  const target = new URL(url, self.location.origin).href;
  const windows = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
  const sameOrigin = windows.filter(client => {
    try {
      return new URL(client.url).origin === self.location.origin;
    } catch {
      return false;
    }
  });
  const existing = sameOrigin.find(client => client.focused) || sameOrigin[0];
  if (existing) {
    const focused = await existing.focus();
    if ('navigate' in focused) {
      try {
        return await focused.navigate(target);
      } catch {
        // An uncontrolled window cannot be navigated by the worker; open a new one instead.
      }
    } else {
      return focused;
    }
  }
  return self.clients.openWindow(target);
}
