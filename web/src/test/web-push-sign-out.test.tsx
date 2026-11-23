import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const calls = vi.hoisted(() => [] as string[]);
const mocks = vi.hoisted(() => ({
  logout: vi.fn(),
  remove: vi.fn(),
  unsubscribe: vi.fn(),
  local: null as null | { endpoint: string; unsubscribe: () => Promise<boolean> },
}));

vi.mock('@/features/auth/services/auth-service', () => ({
  authService: { logout: mocks.logout },
}));
vi.mock('@/features/auth/store/auth-store', () => ({
  useAuthStore: { use: { reset: () => vi.fn() } },
}));
vi.mock('@/i18n', () => ({ useT: () => (key: string) => key }));
vi.mock('next/navigation', () => ({
  useRouter: () => ({ replace: vi.fn() }),
  useSearchParams: () => new URLSearchParams(),
}));
vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }));
vi.mock('@/features/notification/services/notification-service', () => ({
  notificationService: { removeWebPushSubscription: mocks.remove },
}));
vi.mock('@/features/notification/web-push/browser', async importOriginal => {
  const original = await importOriginal<typeof import('@/features/notification/web-push/browser')>();
  return { ...original, getLocalSubscription: async () => mocks.local };
});

const endpoint = 'https://fcm.googleapis.com/fcm/send/shared-computer';
const originalFeatures = process.env.NEXT_PUBLIC_OPTIONAL_FEATURES;

describe('sign-out releases this device from web push', () => {
  beforeEach(() => {
    vi.resetModules();
    process.env.NEXT_PUBLIC_OPTIONAL_FEATURES = 'notification';
    calls.length = 0;
    window.localStorage.clear();
    window.localStorage.setItem('zmail.webPush.device', JSON.stringify({ id: 31, endpoint }));
    mocks.local = {
      endpoint,
      unsubscribe: mocks.unsubscribe.mockReset().mockImplementation(async () => {
        calls.push('unsubscribe');
        return true;
      }),
    };
    mocks.remove.mockReset().mockImplementation(async () => {
      calls.push('delete');
    });
    mocks.logout.mockReset().mockImplementation(async () => {
      calls.push('logout');
    });
  });

  afterEach(() => {
    vi.useRealTimers();
    if (originalFeatures === undefined) delete process.env.NEXT_PUBLIC_OPTIONAL_FEATURES;
    else process.env.NEXT_PUBLIC_OPTIONAL_FEATURES = originalFeatures;
  });

  it('deletes the server row and unsubscribes before the session ends', async () => {
    const { useLogout } = await import('@/features/auth/hooks/use-auth');
    const { result } = renderHook(() => useLogout(), { wrapper: wrapper() });

    act(() => result.current.mutate());

    await waitFor(() => expect(mocks.logout).toHaveBeenCalledTimes(1));
    expect(mocks.remove).toHaveBeenCalledWith(31);
    expect(mocks.unsubscribe).toHaveBeenCalled();
    expect(calls.indexOf('logout')).toBe(calls.length - 1);
    expect(window.localStorage.getItem('zmail.webPush.device')).toBeNull();
  });

  it('still signs out when releasing push fails', async () => {
    mocks.remove.mockRejectedValue(new Error('offline'));
    mocks.unsubscribe.mockRejectedValue(new Error('push service down'));
    const { useLogout } = await import('@/features/auth/hooks/use-auth');
    const { result } = renderHook(() => useLogout(), { wrapper: wrapper() });

    act(() => result.current.mutate());

    await waitFor(() => expect(mocks.logout).toHaveBeenCalledTimes(1));
  });

  it('gives up on a hanging release instead of blocking sign-out', async () => {
    vi.useFakeTimers();
    mocks.remove.mockImplementation(() => new Promise(() => {}));
    const { releaseWebPushOnSignOut } = await import('@/features/notification/web-push/sign-out');

    const released = releaseWebPushOnSignOut(3_000);
    await vi.advanceTimersByTimeAsync(3_000);

    await expect(released).resolves.toBeUndefined();
  });

  it('does nothing when the notification feature is off', async () => {
    process.env.NEXT_PUBLIC_OPTIONAL_FEATURES = '';
    const { useLogout } = await import('@/features/auth/hooks/use-auth');
    const { result } = renderHook(() => useLogout(), { wrapper: wrapper() });

    act(() => result.current.mutate());

    await waitFor(() => expect(mocks.logout).toHaveBeenCalledTimes(1));
    expect(mocks.remove).not.toHaveBeenCalled();
    expect(mocks.unsubscribe).not.toHaveBeenCalled();
  });
});

function wrapper() {
  const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
  };
}
