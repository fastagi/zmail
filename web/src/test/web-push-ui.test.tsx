import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { NotificationSettingsPanel } from '@/features/notification/components/notification-settings-panel';
import { WebPushBanner } from '@/features/notification/components/web-push-banner';
import { messages } from '@/i18n/modules';

const browser = vi.hoisted(() => ({
  support: 'supported' as 'supported' | 'unsupported' | 'ios-needs-install',
  permission: 'default' as 'default' | 'granted' | 'denied',
  local: null as null | { endpoint: string; unsubscribe: ReturnType<typeof vi.fn> },
  requestPermission: vi.fn(),
  subscribeThisBrowser: vi.fn(),
}));

const service = vi.hoisted(() => ({
  webPushConfig: vi.fn(),
  listWebPushSubscriptions: vi.fn(),
  registerWebPushSubscription: vi.fn(),
  removeWebPushSubscription: vi.fn(),
  preference: vi.fn(),
  replacePreference: vi.fn(),
}));

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));

vi.mock('sonner', () => ({ toast }));
vi.mock('@/features/notification/services/notification-service', () => ({
  notificationService: service,
}));
vi.mock('@/features/notification/web-push/browser', async importOriginal => {
  const original = await importOriginal<typeof import('@/features/notification/web-push/browser')>();
  return {
    ...original,
    detectWebPushSupport: () => browser.support,
    currentPermission: () => browser.permission,
    requestPermission: browser.requestPermission,
    getLocalSubscription: async () => browser.local,
    subscribeThisBrowser: browser.subscribeThisBrowser,
    currentDeviceLabel: () => 'Chrome · macOS',
  };
});

const endpoint = 'https://fcm.googleapis.com/fcm/send/this-device';

function localSubscription() {
  return {
    endpoint,
    unsubscribe: vi.fn(async () => true),
    toJSON: () => ({ endpoint, keys: { p256dh: 'BKEY', auth: 'AUTH' } }),
  };
}

const devices = [
  {
    id: 9,
    device_label: 'Firefox · Windows',
    created_at: '2026-11-01T10:00:00Z',
    last_success_at: '2026-11-20T08:30:00Z',
  },
  {
    id: 4,
    device_label: 'Chrome · macOS',
    created_at: '2026-10-01T10:00:00Z',
    last_success_at: null,
  },
];

describe('web push settings', () => {
  beforeEach(() => {
    window.localStorage.clear();
    browser.support = 'supported';
    browser.permission = 'default';
    browser.local = null;
    browser.requestPermission.mockReset().mockResolvedValue('granted');
    browser.subscribeThisBrowser.mockReset().mockImplementation(async () => localSubscription());
    for (const fn of Object.values(service)) fn.mockReset();
    service.webPushConfig.mockResolvedValue({ enabled: true, public_key: 'BPublic' });
    service.listWebPushSubscriptions.mockResolvedValue([]);
    service.preference.mockResolvedValue({
      in_app_enabled: true,
      email_enabled: true,
      web_push_enabled: true,
    });
    service.replacePreference.mockImplementation(async input => ({
      web_push_enabled: true,
      ...input,
    }));
    service.removeWebPushSubscription.mockResolvedValue(undefined);
    toast.success.mockReset();
    toast.error.mockReset();
  });

  it('explains an unsupported browser and keeps the switch off', async () => {
    browser.support = 'unsupported';
    renderWithProviders(<NotificationSettingsPanel />);

    expect(await screen.findByText(/This browser cannot show Zmail alerts/)).toBeInTheDocument();
    expect(deviceSwitch()).toBeDisabled();
    expect(service.webPushConfig).not.toHaveBeenCalled();
  });

  it('asks iOS Safari users to add Zmail to the Home Screen first', async () => {
    browser.support = 'ios-needs-install';
    renderWithProviders(<NotificationSettingsPanel />);

    expect(await screen.findByText('Add Zmail to your Home Screen first')).toBeInTheDocument();
    expect(screen.getByText(/Add to Home Screen/)).toBeInTheDocument();
    expect(deviceSwitch()).toBeDisabled();
  });

  it('shows a calm note when push is not configured on the server', async () => {
    service.webPushConfig.mockResolvedValue({ enabled: false, public_key: '' });
    renderWithProviders(<NotificationSettingsPanel />);

    expect(await screen.findByText(/not available on this Zmail server yet/)).toBeInTheDocument();
    expect(deviceSwitch()).toBeDisabled();
    expect(service.listWebPushSubscriptions).not.toHaveBeenCalled();
  });

  it('explains how to re-allow notifications after a denial', async () => {
    browser.permission = 'denied';
    renderWithProviders(<NotificationSettingsPanel />);

    expect(await screen.findByText('Notifications are blocked for Zmail')).toBeInTheDocument();
    expect(deviceSwitch()).toBeDisabled();
  });

  it('subscribes this device with its label and marks it in the device list', async () => {
    service.registerWebPushSubscription.mockResolvedValue(devices[1]);
    renderWithProviders(<NotificationSettingsPanel />);
    await waitFor(() => expect(deviceSwitch()).toBeEnabled());

    service.listWebPushSubscriptions.mockResolvedValue(devices);
    fireEvent.click(deviceSwitch());

    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith('New-mail alerts are on for this device')
    );
    expect(browser.requestPermission).toHaveBeenCalled();
    expect(browser.subscribeThisBrowser).toHaveBeenCalledWith('BPublic');
    expect(service.registerWebPushSubscription).toHaveBeenCalledWith({
      endpoint,
      keys: { p256dh: 'BKEY', auth: 'AUTH' },
      device_label: 'Chrome · macOS',
    });
    await waitFor(() => expect(deviceSwitch()).toBeChecked());
    const current = screen.getByText('This device').closest('li');
    expect(current).not.toBeNull();
    expect(within(current as HTMLElement).getByText('Chrome · macOS')).toBeInTheDocument();
    expect(screen.getByText(/No alerts yet/)).toBeInTheDocument();
  });

  it('reports a denied permission request without subscribing', async () => {
    browser.requestPermission.mockResolvedValue('denied');
    renderWithProviders(<NotificationSettingsPanel />);
    await waitFor(() => expect(deviceSwitch()).toBeEnabled());

    fireEvent.click(deviceSwitch());

    await waitFor(() => expect(toast.error).toHaveBeenCalled());
    expect(browser.subscribeThisBrowser).not.toHaveBeenCalled();
    expect(await screen.findByText('Notifications are blocked for Zmail')).toBeInTheDocument();
  });

  it('turns this device off by deleting its server row and unsubscribing', async () => {
    const subscription = localSubscription();
    browser.local = subscription;
    browser.permission = 'granted';
    window.localStorage.setItem('zmail.webPush.device', JSON.stringify({ id: 4, endpoint }));
    service.listWebPushSubscriptions.mockResolvedValue(devices);
    renderWithProviders(<NotificationSettingsPanel />);
    await waitFor(() => expect(deviceSwitch()).toBeChecked());

    fireEvent.click(deviceSwitch());

    await waitFor(() => expect(service.removeWebPushSubscription).toHaveBeenCalledWith(4));
    expect(subscription.unsubscribe).toHaveBeenCalled();
    expect(window.localStorage.getItem('zmail.webPush.device')).toBeNull();
  });

  it('removes another device only after confirmation', async () => {
    browser.permission = 'granted';
    service.listWebPushSubscriptions.mockResolvedValue(devices);
    renderWithProviders(<NotificationSettingsPanel />);

    fireEvent.click(await screen.findByRole('button', { name: 'Remove Firefox · Windows' }));
    expect(service.removeWebPushSubscription).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Remove device' }));

    await waitFor(() => expect(service.removeWebPushSubscription).toHaveBeenCalledWith(9));
    expect(toast.success).toHaveBeenCalledWith('Device removed');
  });

  it('binds the push alerts toggle to web_push_enabled', async () => {
    renderWithProviders(<NotificationSettingsPanel />);

    fireEvent.click(await screen.findByRole('switch', { name: 'Push alerts' }));

    await waitFor(() =>
      expect(service.replacePreference).toHaveBeenCalledWith({
        in_app_enabled: true,
        email_enabled: true,
        web_push_enabled: false,
      })
    );
  });
});

describe('web push inbox banner', () => {
  beforeEach(() => {
    window.localStorage.clear();
    browser.support = 'supported';
    browser.permission = 'default';
    browser.local = null;
    for (const fn of Object.values(service)) fn.mockReset();
    service.webPushConfig.mockResolvedValue({ enabled: true, public_key: 'BPublic' });
    service.listWebPushSubscriptions.mockResolvedValue([]);
  });

  it('offers alerts on a supported, unsubscribed device', async () => {
    renderWithProviders(<WebPushBanner />);
    expect(await screen.findByText('Get notified about new mail')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Turn on alerts' })).toBeInTheDocument();
  });

  it.each([
    ['an unsupported browser', () => (browser.support = 'unsupported')],
    ['iOS Safari outside the Home Screen', () => (browser.support = 'ios-needs-install')],
    ['denied permission', () => (browser.permission = 'denied')],
    [
      'push not configured on the server',
      () => service.webPushConfig.mockResolvedValue({ enabled: false, public_key: '' }),
    ],
    [
      'a subscribed device',
      () => {
        browser.local = localSubscription();
        window.localStorage.setItem('zmail.webPush.device', JSON.stringify({ id: 4, endpoint }));
        service.listWebPushSubscriptions.mockResolvedValue(devices);
      },
    ],
    ['an earlier dismissal', () => window.localStorage.setItem('zmail.webPushBannerDismissed', '1')],
  ])('stays hidden for %s', async (_case, arrange) => {
    arrange();
    renderWithProviders(<WebPushBanner />);
    await new Promise(resolve => setTimeout(resolve, 20));
    expect(screen.queryByText('Get notified about new mail')).not.toBeInTheDocument();
  });

  it('remembers a dismissal on this device', async () => {
    renderWithProviders(<WebPushBanner />);
    fireEvent.click(await screen.findByRole('button', { name: 'Dismiss' }));

    expect(screen.queryByText('Get notified about new mail')).not.toBeInTheDocument();
    expect(window.localStorage.getItem('zmail.webPushBannerDismissed')).toBe('1');
  });
});

function deviceSwitch(): HTMLElement {
  return screen.getByRole('switch', { name: 'New-mail alerts on this device' });
}

function renderWithProviders(children: ReactNode) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
        {children}
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
}
