import { describe, expect, it } from 'vitest';

import {
  notificationPreferenceSchema,
  replaceNotificationPreferenceSchema,
  webPushConfigSchema,
  webPushSubscriptionSchema,
} from '@/features/notification/schemas';
import {
  base64UrlToUint8Array,
  deviceLabelFromUserAgent,
} from '@/features/notification/web-push/browser';

describe('web push schemas', () => {
  it('accepts preferences with web_push_enabled', () => {
    expect(
      notificationPreferenceSchema.parse({
        in_app_enabled: true,
        email_enabled: false,
        web_push_enabled: false,
      })
    ).toEqual({ in_app_enabled: true, email_enabled: false, web_push_enabled: false });
  });

  it('defaults web_push_enabled to true for an API that predates web push', () => {
    expect(
      notificationPreferenceSchema.parse({ in_app_enabled: false, email_enabled: true })
    ).toEqual({ in_app_enabled: false, email_enabled: true, web_push_enabled: true });
  });

  it('keeps web_push_enabled optional on replacement so omission keeps the stored value', () => {
    expect(
      replaceNotificationPreferenceSchema.parse({ in_app_enabled: true, email_enabled: true })
    ).toEqual({ in_app_enabled: true, email_enabled: true });
    expect(
      replaceNotificationPreferenceSchema.safeParse({
        in_app_enabled: true,
        email_enabled: true,
        web_push_enabled: 'yes',
      }).success
    ).toBe(false);
  });

  it('validates the push config and subscription responses strictly', () => {
    expect(webPushConfigSchema.parse({ enabled: false, public_key: '' })).toEqual({
      enabled: false,
      public_key: '',
    });
    expect(webPushConfigSchema.safeParse({ enabled: true, public_key: 'a b' }).success).toBe(
      false
    );
    expect(
      webPushSubscriptionSchema.parse({
        id: 12,
        device_label: 'Chrome on macOS',
        created_at: '2026-12-23T09:00:00Z',
        last_success_at: null,
      }).id
    ).toBe(12);
    expect(
      webPushSubscriptionSchema.safeParse({
        id: 12,
        device_label: '',
        created_at: '2026-12-23T09:00:00Z',
        last_success_at: null,
        endpoint: 'https://fcm.googleapis.com/x',
      }).success
    ).toBe(false);
  });
});

describe('web push browser helpers', () => {
  it('decodes a base64url VAPID key', () => {
    expect(Array.from(base64UrlToUint8Array('AQID_-8'))).toEqual([1, 2, 3, 255, 239]);
  });

  it.each([
    [
      'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Safari/537.36',
      'Chrome · macOS',
    ],
    [
      'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Safari/537.36 Edg/131.0',
      'Edge · Windows',
    ],
    ['Mozilla/5.0 (X11; Linux x86_64; rv:133.0) Gecko/20100101 Firefox/133.0', 'Firefox · Linux'],
    [
      'Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1',
      'Safari · iPhone',
    ],
    [
      'Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Mobile Safari/537.36',
      'Chrome · Android',
    ],
  ])('labels %s', (userAgent, label) => {
    expect(deviceLabelFromUserAgent(userAgent)).toBe(label);
  });
});
