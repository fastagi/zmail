'use client';

import { useCallback, useEffect, useState, useSyncExternalStore } from 'react';

import {
  useRegisterWebPushSubscription,
  useRemoveWebPushSubscription,
  useWebPushConfig,
  useWebPushSubscriptions,
} from '@/features/notification/hooks/use-notifications';
import {
  currentDeviceLabel,
  currentPermission,
  detectWebPushSupport,
  getLocalSubscription,
  requestPermission,
  subscribeThisBrowser,
  subscriptionKeys,
  thisDeviceId,
  writeStoredDevice,
  type LocalPushSubscription,
  type WebPushPermission,
  type WebPushSupport,
} from '@/features/notification/web-push/browser';
import { ApiError } from '@/http/request';
import { ApiErrorCode } from '@/http/codes';

export type WebPushDeviceStatus =
  | 'checking'
  | 'unsupported'
  | 'ios-needs-install'
  | 'unavailable'
  | 'error'
  | 'denied'
  | 'off'
  | 'on';

export type EnableWebPushResult = 'enabled' | 'denied' | 'unavailable' | 'failed';

const subscribeToNothing = () => () => {};

/**
 * This browser's new-mail alert state and the flows that turn it on and off. Browser APIs are read
 * only after hydration, so the server render and the first client render agree.
 */
export function useWebPushDevice({ enabled = true }: { enabled?: boolean } = {}) {
  const support = useSyncExternalStore<WebPushSupport | null>(
    subscribeToNothing,
    detectWebPushSupport,
    () => null
  );
  const initialPermission = useSyncExternalStore<WebPushPermission | null>(
    subscribeToNothing,
    currentPermission,
    () => null
  );
  const [permissionOverride, setPermissionOverride] = useState<WebPushPermission | null>(null);
  const permission = permissionOverride ?? initialPermission;
  const [local, setLocal] = useState<LocalPushSubscription | null | undefined>(undefined);
  const [pending, setPending] = useState(false);
  const supported = enabled && support === 'supported';

  useEffect(() => {
    if (!supported) return;
    let active = true;
    void getLocalSubscription().then(subscription => {
      if (active) setLocal(subscription);
    });
    return () => {
      active = false;
    };
  }, [supported]);

  const config = useWebPushConfig(supported);
  const serverEnabled = config.data?.enabled === true;
  const subscriptions = useWebPushSubscriptions(supported && serverEnabled);
  const register = useRegisterWebPushSubscription();
  const remove = useRemoveWebPushSubscription();

  const deviceId = local === undefined ? null : thisDeviceId(local);
  const subscribedHere =
    local != null &&
    deviceId !== null &&
    (subscriptions.data?.some(item => item.id === deviceId) ?? false);

  let status: WebPushDeviceStatus;
  if (!enabled || support === null) status = 'checking';
  else if (support !== 'supported') status = support;
  else if (config.isPending) status = 'checking';
  else if (config.isError || !config.data) status = 'error';
  else if (!config.data.enabled) status = 'unavailable';
  else if (permission === 'denied') status = 'denied';
  else if (local === undefined || subscriptions.isPending) status = 'checking';
  else status = subscribedHere ? 'on' : 'off';

  const { mutateAsync: registerAsync } = register;
  const { mutateAsync: removeAsync } = remove;
  const publicKey = config.data?.public_key ?? '';

  const enable = useCallback(async (): Promise<EnableWebPushResult> => {
    if (!serverEnabled || !publicKey) return 'unavailable';
    setPending(true);
    try {
      const granted =
        currentPermission() === 'granted' ? 'granted' : await requestPermission();
      setPermissionOverride(granted);
      if (granted !== 'granted') return granted === 'denied' ? 'denied' : 'failed';
      const subscription = await subscribeThisBrowser(publicKey);
      const keys = subscriptionKeys(subscription);
      if (!keys) return 'failed';
      const label = currentDeviceLabel();
      const saved = await registerAsync({ ...keys, ...(label ? { device_label: label } : {}) });
      writeStoredDevice({ id: saved.id, endpoint: keys.endpoint });
      setLocal(subscription);
      return 'enabled';
    } catch (error) {
      if (error instanceof ApiError && error.errorCode === ApiErrorCode.NOTIFICATION_WEB_PUSH_DISABLED) {
        return 'unavailable';
      }
      return 'failed';
    } finally {
      setPending(false);
    }
  }, [publicKey, registerAsync, serverEnabled]);

  const disable = useCallback(async (): Promise<boolean> => {
    setPending(true);
    try {
      const subscription = local ?? (await getLocalSubscription());
      const id = thisDeviceId(subscription);
      if (id !== null) await removeIgnoringMissing(removeAsync, id);
      await subscription?.unsubscribe();
      writeStoredDevice(null);
      setLocal(null);
      return true;
    } catch {
      return false;
    } finally {
      setPending(false);
    }
  }, [local, removeAsync]);

  /** Removes any registered device; removing this one also unsubscribes the browser. */
  const removeDevice = useCallback(
    async (subscriptionId: number): Promise<boolean> => {
      if (subscriptionId === deviceId) return disable();
      try {
        await removeIgnoringMissing(removeAsync, subscriptionId);
        return true;
      } catch {
        return false;
      }
    },
    [deviceId, disable, removeAsync]
  );

  return {
    status,
    pending,
    deviceId: subscribedHere ? deviceId : null,
    devices: subscriptions,
    config,
    enable,
    disable,
    removeDevice,
    removing: remove.isPending,
  };
}

async function removeIgnoringMissing(
  remove: (id: number) => Promise<void>,
  id: number
): Promise<void> {
  try {
    await remove(id);
  } catch (error) {
    // Already gone on the server is the outcome the caller wanted.
    if (
      error instanceof ApiError &&
      error.errorCode === ApiErrorCode.NOTIFICATION_WEB_PUSH_SUBSCRIPTION_NOT_FOUND
    ) {
      return;
    }
    throw error;
  }
}
