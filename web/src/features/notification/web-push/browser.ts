/**
 * Browser seams for web push: feature detection, the service worker, the local PushSubscription,
 * and the per-device record of which server subscription belongs to this browser. Every function
 * is safe to call during SSR or in a browser without push support; none throws for a missing API.
 */

export type WebPushSupport = 'supported' | 'unsupported' | 'ios-needs-install';
export type WebPushPermission = 'default' | 'granted' | 'denied';

export const SERVICE_WORKER_URL = '/sw.js';
const DEVICE_STORAGE_KEY = 'zmail.webPush.device';

export interface StoredWebPushDevice {
  id: number;
  endpoint: string;
}

export interface LocalPushSubscription {
  endpoint: string;
  toJSON(): PushSubscriptionJSON;
  unsubscribe(): Promise<boolean>;
}

export function detectWebPushSupport(): WebPushSupport {
  if (typeof window === 'undefined' || typeof navigator === 'undefined') return 'unsupported';
  const ios = isIos(navigator);
  if (ios && !isStandalone()) return 'ios-needs-install';
  const capable =
    'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window;
  return capable ? 'supported' : 'unsupported';
}

export function currentPermission(): WebPushPermission {
  if (typeof window === 'undefined' || !('Notification' in window)) return 'default';
  const value = window.Notification.permission;
  return value === 'granted' || value === 'denied' ? value : 'default';
}

export async function requestPermission(): Promise<WebPushPermission> {
  if (typeof window === 'undefined' || !('Notification' in window)) return 'denied';
  const value = await window.Notification.requestPermission();
  return value === 'granted' || value === 'denied' ? value : 'default';
}

/** The local subscription of this browser, without registering a service worker. */
export async function getLocalSubscription(): Promise<LocalPushSubscription | null> {
  if (typeof navigator === 'undefined' || !('serviceWorker' in navigator)) return null;
  try {
    const registration = await navigator.serviceWorker.getRegistration('/');
    if (!registration || !('pushManager' in registration)) return null;
    return await registration.pushManager.getSubscription();
  } catch {
    return null;
  }
}

/** Registers (or refreshes) the service worker and subscribes this browser with the VAPID key. */
export async function subscribeThisBrowser(publicKey: string): Promise<LocalPushSubscription> {
  await navigator.serviceWorker.register(SERVICE_WORKER_URL, { scope: '/' });
  const registration = await navigator.serviceWorker.ready;
  const applicationServerKey = base64UrlToUint8Array(publicKey);
  const existing = await registration.pushManager.getSubscription();
  if (existing) {
    if (sameKey(existing.options?.applicationServerKey ?? null, applicationServerKey)) {
      return existing;
    }
    // The instance rotated its VAPID key; the old subscription can no longer be used.
    await existing.unsubscribe().catch(() => false);
  }
  return registration.pushManager.subscribe({
    userVisibleOnly: true,
    applicationServerKey: applicationServerKey as BufferSource,
  });
}

export function subscriptionKeys(
  subscription: LocalPushSubscription
): { endpoint: string; keys: { p256dh: string; auth: string } } | null {
  const json = subscription.toJSON();
  const p256dh = json.keys?.p256dh;
  const auth = json.keys?.auth;
  if (!json.endpoint || !p256dh || !auth) return null;
  return { endpoint: json.endpoint, keys: { p256dh, auth } };
}

export function base64UrlToUint8Array(value: string): Uint8Array {
  const padded = `${value}${'='.repeat((4 - (value.length % 4)) % 4)}`
    .replace(/-/g, '+')
    .replace(/_/g, '/');
  const binary = atob(padded);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
  return bytes;
}

/** A short, readable name for this browser such as "Chrome · macOS". */
export function deviceLabelFromUserAgent(userAgent: string, platformHint = ''): string {
  const browser = /Edg\//.test(userAgent)
    ? 'Edge'
    : /OPR\/|Opera/.test(userAgent)
      ? 'Opera'
      : /Firefox\/|FxiOS\//.test(userAgent)
        ? 'Firefox'
        : /CriOS\/|Chrome\//.test(userAgent)
          ? 'Chrome'
          : /Safari\//.test(userAgent)
            ? 'Safari'
            : 'Browser';
  const os = /iPhone/.test(userAgent)
    ? 'iPhone'
    : /iPad/.test(userAgent) || (/Macintosh/.test(userAgent) && platformHint === 'touch')
      ? 'iPad'
      : /Android/.test(userAgent)
        ? 'Android'
        : /Windows/.test(userAgent)
          ? 'Windows'
          : /Mac OS X|Macintosh/.test(userAgent)
            ? 'macOS'
            : /CrOS/.test(userAgent)
              ? 'ChromeOS'
              : /Linux/.test(userAgent)
                ? 'Linux'
                : '';
  return os ? `${browser} · ${os}` : browser;
}

export function currentDeviceLabel(): string {
  if (typeof navigator === 'undefined') return '';
  const touchMac = navigator.maxTouchPoints > 1 ? 'touch' : '';
  return deviceLabelFromUserAgent(navigator.userAgent, touchMac);
}

export function readStoredDevice(): StoredWebPushDevice | null {
  try {
    const raw = window.localStorage.getItem(DEVICE_STORAGE_KEY);
    if (!raw) return null;
    const value: unknown = JSON.parse(raw);
    if (
      typeof value === 'object' &&
      value !== null &&
      Number.isSafeInteger((value as StoredWebPushDevice).id) &&
      (value as StoredWebPushDevice).id > 0 &&
      typeof (value as StoredWebPushDevice).endpoint === 'string'
    ) {
      return { id: (value as StoredWebPushDevice).id, endpoint: (value as StoredWebPushDevice).endpoint };
    }
    return null;
  } catch {
    return null;
  }
}

export function writeStoredDevice(device: StoredWebPushDevice | null): void {
  try {
    if (device) window.localStorage.setItem(DEVICE_STORAGE_KEY, JSON.stringify(device));
    else window.localStorage.removeItem(DEVICE_STORAGE_KEY);
  } catch {
    // Storage is unavailable; this device is recognized again the next time it subscribes.
  }
}

/** The server subscription id of this browser, when the stored record matches its subscription. */
export function thisDeviceId(subscription: LocalPushSubscription | null): number | null {
  if (!subscription) return null;
  const stored = readStoredDevice();
  return stored && stored.endpoint === subscription.endpoint ? stored.id : null;
}

function isIos(nav: Navigator): boolean {
  const userAgent = nav.userAgent ?? '';
  return (
    /iPhone|iPad|iPod/.test(userAgent) ||
    (/Macintosh/.test(userAgent) && typeof nav.maxTouchPoints === 'number' && nav.maxTouchPoints > 1)
  );
}

function isStandalone(): boolean {
  const legacy = (navigator as Navigator & { standalone?: boolean }).standalone === true;
  try {
    return legacy || window.matchMedia('(display-mode: standalone)').matches;
  } catch {
    return legacy;
  }
}

function sameKey(current: ArrayBuffer | null, expected: Uint8Array): boolean {
  if (!current) return false;
  const bytes = new Uint8Array(current);
  return bytes.length === expected.length && bytes.every((value, index) => value === expected[index]);
}
