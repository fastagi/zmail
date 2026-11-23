import 'server-only';

import type { AuthUser } from '@/features/auth/types';
import type {
  NotificationFilter,
  NotificationItem,
  NotificationPage,
  NotificationPreference,
  NotificationReadStateResult,
  NotificationStatus,
  RegisterWebPushSubscriptionInput,
  ReplaceNotificationPreferenceInput,
  WebPushConfig,
  WebPushSubscription,
} from '@/features/notification/types';

interface MockWebPushSubscription extends WebPushSubscription {
  endpoint: string;
}

interface MockNotificationState {
  items: NotificationItem[];
  preference: NotificationPreference;
  webPushSubscriptions: MockWebPushSubscription[];
}

export type MockWebPushRegistration =
  | { ok: true; subscription: WebPushSubscription }
  | { ok: false; error: 'disabled' | 'endpoint' | 'keys' };

// The mock BFF has no VAPID key and sends nothing, so it reports push as not configured by
// default; tests opt in with `configureWebPush`. A mock subscription never records a delivery.
const disabledWebPushConfig: WebPushConfig = { enabled: false, public_key: '' };
const maxWebPushSubscriptions = 20;
const pushServiceHosts = [
  'fcm.googleapis.com',
  'push.services.mozilla.com',
  'push.apple.com',
  'notify.windows.com',
];

let nextNotificationId = 1;
let nextWebPushSubscriptionId = 1;
let webPushConfig: WebPushConfig = disabledWebPushConfig;
const states = new Map<string, MockNotificationState>();

export const mockNotificationStore = {
  list(
    user: AuthUser,
    page: number,
    perPage: number,
    status: NotificationFilter
  ): NotificationPage {
    const state = stateFor(user);
    const matching = state.items
      .filter(item => status === 'all' || !item.is_read)
      .sort((left, right) => right.id - left.id);
    const start = (page - 1) * perPage;
    const items = matching.slice(start, start + perPage).map(cloneNotification);
    return {
      items,
      meta: pageMeta(matching.length, page, perPage, items.length),
      links: pageLinks(matching.length, page, perPage, status),
    };
  },

  status(user: AuthUser): NotificationStatus {
    return {
      unread_count: stateFor(user).items.filter(item => !item.is_read).length,
    };
  },

  replaceReadState(
    user: AuthUser,
    notificationId: number,
    isRead: boolean
  ): NotificationItem | null {
    const item = stateFor(user).items.find(candidate => candidate.id === notificationId);
    if (!item) return null;
    item.is_read = isRead;
    item.read_at = isRead ? new Date().toISOString() : null;
    return cloneNotification(item);
  },

  markReadThrough(user: AuthUser, throughId: number): NotificationReadStateResult {
    const state = stateFor(user);
    let updatedCount = 0;
    const readAt = new Date().toISOString();
    for (const item of state.items) {
      if (item.id <= throughId && !item.is_read) {
        item.is_read = true;
        item.read_at = readAt;
        updatedCount += 1;
      }
    }
    return {
      updated_count: updatedCount,
      unread_count: state.items.filter(item => !item.is_read).length,
    };
  },

  preference(user: AuthUser): NotificationPreference {
    return { ...stateFor(user).preference };
  },

  replacePreference(
    user: AuthUser,
    input: ReplaceNotificationPreferenceInput
  ): NotificationPreference {
    const state = stateFor(user);
    state.preference = {
      in_app_enabled: input.in_app_enabled,
      email_enabled: input.email_enabled,
      web_push_enabled: input.web_push_enabled ?? state.preference.web_push_enabled,
    };
    return { ...state.preference };
  },

  webPushConfig(): WebPushConfig {
    return { ...webPushConfig };
  },

  /** Test seam: the mock answers as an instance with this VAPID public key. */
  configureWebPush(publicKey: string | null): void {
    webPushConfig = publicKey ? { enabled: true, public_key: publicKey } : disabledWebPushConfig;
  },

  listWebPushSubscriptions(user: AuthUser): WebPushSubscription[] {
    return [...stateFor(user).webPushSubscriptions]
      .sort((left, right) => right.id - left.id)
      .map(publicSubscription);
  },

  registerWebPushSubscription(
    user: AuthUser,
    input: RegisterWebPushSubscriptionInput
  ): MockWebPushRegistration {
    if (!webPushConfig.enabled) return { ok: false, error: 'disabled' };
    const endpoint = input.endpoint.trim();
    if (!isAllowedPushEndpoint(endpoint)) return { ok: false, error: 'endpoint' };
    if (decodedLength(input.keys.p256dh) !== 65 || decodedLength(input.keys.auth) !== 16) {
      return { ok: false, error: 'keys' };
    }
    const state = stateFor(user);
    const deviceLabel = (input.device_label ?? '').trim().slice(0, 120);
    const existing = state.webPushSubscriptions.find(item => item.endpoint === endpoint);
    if (existing) {
      existing.device_label = deviceLabel;
      return { ok: true, subscription: publicSubscription(existing) };
    }
    const created: MockWebPushSubscription = {
      id: nextWebPushSubscriptionId++,
      device_label: deviceLabel,
      created_at: new Date().toISOString(),
      last_success_at: null,
      endpoint,
    };
    state.webPushSubscriptions.push(created);
    if (state.webPushSubscriptions.length > maxWebPushSubscriptions) {
      state.webPushSubscriptions.sort((left, right) => left.id - right.id).shift();
    }
    return { ok: true, subscription: publicSubscription(created) };
  },

  removeWebPushSubscription(user: AuthUser, subscriptionId: number): boolean {
    const state = stateFor(user);
    const index = state.webPushSubscriptions.findIndex(item => item.id === subscriptionId);
    if (index < 0) return false;
    state.webPushSubscriptions.splice(index, 1);
    return true;
  },

  reset(): void {
    states.clear();
    nextNotificationId = 1;
    nextWebPushSubscriptionId = 1;
    webPushConfig = disabledWebPushConfig;
  },
};

function publicSubscription(item: MockWebPushSubscription): WebPushSubscription {
  return {
    id: item.id,
    device_label: item.device_label,
    created_at: item.created_at,
    last_success_at: item.last_success_at,
  };
}

function isAllowedPushEndpoint(value: string): boolean {
  try {
    const url = new URL(value);
    if (url.protocol !== 'https:' || url.username || url.password || url.port) return false;
    return pushServiceHosts.some(
      host => url.hostname === host || url.hostname.endsWith(`.${host}`)
    );
  } catch {
    return false;
  }
}

function decodedLength(value: string): number {
  const trimmed = value.trim().replace(/=+$/, '');
  if (!/^[A-Za-z0-9_\-+/]+$/.test(trimmed)) return -1;
  return Math.floor((trimmed.length * 3) / 4);
}

function stateFor(user: AuthUser): MockNotificationState {
  const existing = states.get(user.id);
  if (existing) return existing;

  const state: MockNotificationState = {
    items: [
      {
        id: nextNotificationId++,
        kind: 'starter.workspace_ready',
        title: 'Your Zmail workspace is ready',
        body: 'Replace this development notification with your first downstream workflow.',
        action_url: '/console',
        is_read: true,
        read_at: '2026-07-15T14:10:00Z',
        created_at: '2026-07-15T14:00:00Z',
      },
      {
        id: nextNotificationId++,
        kind: 'starter.notification_ready',
        title: 'Notification starter ready',
        body: 'In-app records, preferences, and durable email delivery are connected.',
        action_url: '/console/settings',
        is_read: false,
        read_at: null,
        created_at: '2026-07-15T14:20:00Z',
      },
    ],
    preference: {
      in_app_enabled: true,
      email_enabled: true,
      web_push_enabled: true,
    },
    webPushSubscriptions: [],
  };
  states.set(user.id, state);
  return state;
}

function cloneNotification(item: NotificationItem): NotificationItem {
  return { ...item };
}

function pageMeta(total: number, page: number, perPage: number, itemCount: number) {
  const lastPage = Math.max(1, Math.ceil(total / perPage));
  const from = total === 0 ? 0 : (page - 1) * perPage + 1;
  return {
    current_page: page,
    per_page: perPage,
    total,
    last_page: lastPage,
    from,
    to: itemCount === 0 ? 0 : from + itemCount - 1,
  };
}

function pageLinks(
  total: number,
  page: number,
  perPage: number,
  status: NotificationFilter
) {
  const lastPage = Math.max(1, Math.ceil(total / perPage));
  const path = (targetPage: number) =>
    `/api/notifications?page=${targetPage}&per_page=${perPage}&status=${status}`;
  return {
    first: path(1),
    last: path(lastPage),
    prev: page > 1 ? path(page - 1) : null,
    next: page < lastPage ? path(page + 1) : null,
  };
}
