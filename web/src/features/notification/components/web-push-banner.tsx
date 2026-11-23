'use client';

import { useState, useSyncExternalStore } from 'react';
import { BellRing, X } from 'lucide-react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import {
  useWebPushDevice,
  type WebPushDeviceStatus,
} from '@/features/notification/web-push/use-web-push-device';
import { useT } from '@/i18n';

const DISMISSED_KEY = 'zmail.webPushBannerDismissed';
const subscribeToNothing = () => () => {};

function readDismissed(): boolean {
  try {
    return window.localStorage.getItem(DISMISSED_KEY) === '1';
  } catch {
    return false;
  }
}

function writeDismissed(): void {
  try {
    window.localStorage.setItem(DISMISSED_KEY, '1');
  } catch {
    // The dismissal lasts for this page only.
  }
}

/**
 * Whether the inbox should offer alerts: only where they can actually be turned on — a supported
 * browser, push configured on the server, permission not denied, this device not yet subscribed,
 * and the offer not dismissed on this device before.
 */
export function shouldOfferWebPush(status: WebPushDeviceStatus, dismissed: boolean): boolean {
  return status === 'off' && !dismissed;
}

/** A one-line inbox offer to turn on new-mail alerts on this device; dismissible per device. */
export function WebPushBanner() {
  const t = useT('notification');
  const device = useWebPushDevice();
  const storedDismissal = useSyncExternalStore(subscribeToNothing, readDismissed, () => true);
  const [dismissedNow, setDismissedNow] = useState(false);
  const dismissed = dismissedNow || storedDismissal;

  if (!shouldOfferWebPush(device.status, dismissed) && !device.pending) return null;

  async function enable(): Promise<void> {
    const result = await device.enable();
    if (result === 'enabled') toast.success(t('push.enabled'));
    else if (result === 'denied') toast.error(t('push.deniedToast'));
    else if (result === 'unavailable') toast.error(t('push.unavailable'));
    else toast.error(t('push.enableFailed'));
  }

  return (
    <div
      role="status"
      className="mx-3 mb-2 flex flex-wrap items-center gap-x-3 gap-y-2 rounded-lg border border-border-main bg-bg-subtle px-3 py-2.5 text-[13px] text-text-main"
    >
      <BellRing className="size-4 shrink-0 text-primary" aria-hidden="true" />
      <div className="min-w-0 flex-1 basis-40">
        <p className="font-medium">{t('push.bannerTitle')}</p>
        <p className="text-xs text-text-muted">{t('push.bannerDescription')}</p>
      </div>
      <div className="flex shrink-0 items-center gap-1">
        <Button
          type="button"
          size="sm"
          variant="outline"
          loading={device.pending}
          onClick={() => void enable()}
        >
          {t('push.bannerAction')}
        </Button>
        <Button
          type="button"
          isIcon
          size="sm"
          variant="ghost"
          aria-label={t('push.bannerDismiss')}
          title={t('push.bannerDismiss')}
          onClick={() => {
            writeDismissed();
            setDismissedNow(true);
          }}
        >
          <X className="size-4" />
        </Button>
      </div>
    </div>
  );
}
