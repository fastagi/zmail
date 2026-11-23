export type NotificationFilter = 'all' | 'unread';

export interface NotificationItem {
  id: number;
  kind: string;
  title: string;
  body: string;
  action_url?: string;
  is_read: boolean;
  read_at: string | null;
  created_at: string;
}

export interface NotificationPage {
  items: NotificationItem[];
  meta: {
    current_page: number;
    per_page: number;
    total: number;
    last_page: number;
    from: number;
    to: number;
  };
  links: {
    first: string;
    last: string;
    prev: string | null;
    next: string | null;
  };
}

export interface NotificationStatus {
  unread_count: number;
}

export interface NotificationReadStateResult {
  updated_count: number;
  unread_count: number;
}

export interface NotificationPreference {
  in_app_enabled: boolean;
  email_enabled: boolean;
  web_push_enabled: boolean;
}

export interface ReplaceNotificationPreferenceInput {
  in_app_enabled: boolean;
  email_enabled: boolean;
  /** Omitted keeps the stored value. */
  web_push_enabled?: boolean;
}

export interface WebPushConfig {
  enabled: boolean;
  /** Base64url VAPID application server key; empty when push is not configured. */
  public_key: string;
}

export interface WebPushSubscription {
  id: number;
  device_label: string;
  created_at: string;
  last_success_at: string | null;
}

export interface RegisterWebPushSubscriptionInput {
  endpoint: string;
  keys: { p256dh: string; auth: string };
  device_label?: string;
}

export interface ReplaceNotificationReadStateInput {
  is_read: boolean;
}

export interface MarkNotificationsReadInput {
  through_id: number;
}
