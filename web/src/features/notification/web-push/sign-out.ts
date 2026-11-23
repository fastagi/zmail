import { isWebFeatureEnabled } from '@/config/features';
import { notificationService } from '@/features/notification/services/notification-service';
import {
  getLocalSubscription,
  readStoredDevice,
  writeStoredDevice,
} from '@/features/notification/web-push/browser';

const releaseTimeoutMs = 3_000;

/**
 * Stops this browser from receiving the signed-out account's pushes (a shared computer must not
 * keep showing someone's mail): deletes this device's server subscription while the session still
 * works and unsubscribes the browser. It never throws and gives up after a few seconds, so it can
 * never block sign-out.
 */
export async function releaseWebPushOnSignOut(timeoutMs = releaseTimeoutMs): Promise<void> {
  if (!isWebFeatureEnabled('notification') || typeof window === 'undefined') return;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const timeout = new Promise<void>(resolve => {
    timer = setTimeout(resolve, timeoutMs);
  });
  try {
    await Promise.race([release(), timeout]);
  } catch {
    // Sign-out continues; an unsubscribed endpoint is pruned by the API on its next push.
  } finally {
    clearTimeout(timer);
  }
}

async function release(): Promise<void> {
  const stored = readStoredDevice();
  writeStoredDevice(null);
  const subscription = await getLocalSubscription();
  await Promise.allSettled([
    stored ? notificationService.removeWebPushSubscription(stored.id) : Promise.resolve(),
    subscription ? subscription.unsubscribe() : Promise.resolve(false),
  ]);
}
