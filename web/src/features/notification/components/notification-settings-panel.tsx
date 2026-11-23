'use client';

import { useState } from 'react';
import { useLocale } from 'next-intl';
import { BellOff, BellRing, Info, Loader2, Smartphone, Trash2 } from 'lucide-react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Skeleton } from '@/components/ui/skeleton';
import { Switch } from '@/components/ui/switch';
import {
  useNotificationPreference,
  useReplaceNotificationPreference,
} from '@/features/notification/hooks/use-notifications';
import type {
  NotificationPreference,
  WebPushSubscription,
} from '@/features/notification/types';
import {
  useWebPushDevice,
  type WebPushDeviceStatus,
} from '@/features/notification/web-push/use-web-push-device';
import { useT } from '@/i18n';
import { cn } from '@/utils';

type PreferenceField = keyof NotificationPreference;

/**
 * The settings "Notifications" tab: new-mail alerts on this device, the account-wide channel
 * switches (the same preference the notification center edits), and the registered devices.
 */
export function NotificationSettingsPanel() {
  const t = useT('notification');
  const device = useWebPushDevice();

  return (
    <section aria-labelledby="notification-settings-title" className="max-w-2xl space-y-8 py-2">
      <div className="space-y-1">
        <h2 id="notification-settings-title" className="text-lg font-semibold">
          {t('push.sectionTitle')}
        </h2>
        <p className="text-sm text-text-muted">{t('push.sectionDescription')}</p>
      </div>

      <ThisDeviceSection device={device} />
      <ChannelSection />
      {device.status === 'on' || device.status === 'off' || device.status === 'denied' ? (
        <DeviceListSection device={device} />
      ) : null}
    </section>
  );
}

type Device = ReturnType<typeof useWebPushDevice>;

function ThisDeviceSection({ device }: { device: Device }) {
  const t = useT('notification');
  const toggleable = device.status === 'on' || device.status === 'off';

  async function toggle(next: boolean): Promise<void> {
    if (next) {
      const result = await device.enable();
      if (result === 'enabled') toast.success(t('push.enabled'));
      else if (result === 'denied') toast.error(t('push.deniedToast'));
      else if (result === 'unavailable') toast.error(t('push.unavailable'));
      else toast.error(t('push.enableFailed'));
      return;
    }
    if (await device.disable()) toast.success(t('push.disabled'));
    else toast.error(t('push.disableFailed'));
  }

  return (
    <div className="space-y-3">
      <div className="flex items-start gap-4">
        <span className="min-w-0 flex-1">
          <span id="web-push-device-label" className="block text-sm font-medium">
            {t('push.deviceTitle')}
          </span>
          <span className="mt-1 block text-xs leading-5 text-text-muted">
            {t('push.deviceDescription')}
          </span>
        </span>
        <div className="flex shrink-0 items-center gap-2 pt-0.5">
          {device.pending ? (
            <Loader2 className="size-4 animate-spin text-text-muted" aria-hidden="true" />
          ) : null}
          <Switch
            aria-labelledby="web-push-device-label"
            checked={device.status === 'on'}
            disabled={!toggleable || device.pending}
            onCheckedChange={checked => void toggle(checked)}
          />
        </div>
      </div>
      {device.pending ? (
        <p role="status" className="text-xs text-text-muted">
          {t('push.enabling')}
        </p>
      ) : (
        <DeviceStatusNote status={device.status} onRetry={() => void device.config.refetch()} />
      )}
    </div>
  );
}

function DeviceStatusNote({
  status,
  onRetry,
}: {
  status: WebPushDeviceStatus;
  onRetry: () => void;
}) {
  const t = useT('notification');
  if (status === 'checking') {
    return (
      <p role="status" className="text-xs text-text-muted">
        {t('push.checking')}
      </p>
    );
  }
  if (status === 'unsupported') return <Note icon={BellOff} text={t('push.unsupported')} />;
  if (status === 'unavailable') return <Note icon={Info} text={t('push.unavailable')} />;
  if (status === 'error') {
    return (
      <div className="flex flex-wrap items-center gap-3">
        <Note icon={Info} text={t('push.configError')} />
        <Button type="button" variant="outline" size="sm" onClick={onRetry}>
          {t('retry')}
        </Button>
      </div>
    );
  }
  if (status === 'ios-needs-install') {
    return (
      <div className="rounded-lg border border-border-main bg-bg-subtle px-4 py-3 text-sm">
        <p className="flex items-center gap-2 font-medium">
          <Smartphone className="size-4 shrink-0" aria-hidden="true" />
          {t('push.iosTitle')}
        </p>
        <p className="mt-1 text-xs text-text-muted">{t('push.iosDescription')}</p>
        <ol className="mt-2 list-decimal space-y-1 pl-5 text-xs">
          <li>{t('push.iosStep1')}</li>
          <li>{t('push.iosStep2')}</li>
          <li>{t('push.iosStep3')}</li>
        </ol>
      </div>
    );
  }
  if (status === 'denied') {
    return (
      <div className="rounded-lg border border-border-main bg-bg-subtle px-4 py-3 text-sm">
        <p className="flex items-center gap-2 font-medium">
          <BellOff className="size-4 shrink-0" aria-hidden="true" />
          {t('push.deniedTitle')}
        </p>
        <p className="mt-1 text-xs text-text-muted">{t('push.deniedDescription')}</p>
      </div>
    );
  }
  return null;
}

function Note({ icon: Icon, text }: { icon: typeof Info; text: string }) {
  return (
    <p className="flex items-start gap-2 text-xs leading-5 text-text-muted">
      <Icon className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
      <span>{text}</span>
    </p>
  );
}

function ChannelSection() {
  const t = useT('notification');
  const preference = useNotificationPreference(true);
  const replace = useReplaceNotificationPreference();

  async function update(field: PreferenceField, value: boolean): Promise<void> {
    if (!preference.data) return;
    try {
      await replace.mutateAsync({ ...preference.data, [field]: value });
      toast.success(t('push.preferenceSaved'));
    } catch {
      toast.error(t('saveError'));
    }
  }

  const rows: { field: PreferenceField; label: string; description: string }[] = [
    {
      field: 'web_push_enabled',
      label: t('push.pushPreference'),
      description: t('push.pushPreferenceDescription'),
    },
    { field: 'in_app_enabled', label: t('inApp'), description: t('inAppDescription') },
    { field: 'email_enabled', label: t('email'), description: t('emailDescription') },
  ];

  return (
    <div className="space-y-2">
      <h3 className="text-sm font-semibold">{t('push.channelsTitle')}</h3>
      {preference.isPending ? (
        <div className="space-y-2" aria-hidden="true">
          <Skeleton className="h-14 w-full" />
          <Skeleton className="h-14 w-full" />
        </div>
      ) : preference.isError || !preference.data ? (
        <div className="flex flex-wrap items-center gap-3">
          <p className="text-sm text-text-muted">{t('loadError')}</p>
          <Button type="button" variant="outline" size="sm" onClick={() => preference.refetch()}>
            {t('retry')}
          </Button>
        </div>
      ) : (
        <div className="rounded-lg border border-border-main px-4">
          {rows.map(row => (
            <label
              key={row.field}
              className="flex min-h-16 cursor-pointer items-center gap-4 border-b py-3 last:border-b-0"
            >
              <span className="min-w-0 flex-1">
                <span className="block text-sm font-medium">{row.label}</span>
                <span className="mt-1 block text-xs leading-5 text-text-muted">
                  {row.description}
                </span>
              </span>
              <Switch
                aria-label={row.label}
                checked={preference.data[row.field]}
                disabled={replace.isPending}
                onCheckedChange={checked => void update(row.field, checked)}
              />
            </label>
          ))}
        </div>
      )}
    </div>
  );
}

function DeviceListSection({ device }: { device: Device }) {
  const t = useT('notification');
  const locale = useLocale();
  const [confirming, setConfirming] = useState<WebPushSubscription | null>(null);
  const formatter = new Intl.DateTimeFormat(locale, { dateStyle: 'medium' });
  const dateTime = new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' });
  const list = device.devices;

  async function confirmRemove(): Promise<void> {
    if (!confirming) return;
    if (await device.removeDevice(confirming.id)) {
      toast.success(t('push.removed'));
      setConfirming(null);
    } else {
      toast.error(t('push.removeFailed'));
    }
  }

  const label = (item: WebPushSubscription) => item.device_label || t('push.unnamedDevice');

  return (
    <div className="space-y-2">
      <h3 className="text-sm font-semibold">{t('push.devicesTitle')}</h3>
      {list.isPending ? (
        <Skeleton className="h-14 w-full" aria-hidden="true" />
      ) : list.isError ? (
        <div className="flex flex-wrap items-center gap-3">
          <p className="text-sm text-text-muted">{t('push.devicesError')}</p>
          <Button type="button" variant="outline" size="sm" onClick={() => list.refetch()}>
            {t('retry')}
          </Button>
        </div>
      ) : !list.data || list.data.length === 0 ? (
        <p className="text-sm text-text-muted">{t('push.devicesEmpty')}</p>
      ) : (
        <ul className="divide-y rounded-lg border border-border-main">
          {list.data.map(item => {
            const current = item.id === device.deviceId;
            return (
              <li key={item.id} className="flex items-center gap-3 px-4 py-3">
                <BellRing
                  className={cn('size-4 shrink-0', current ? 'text-primary' : 'text-text-muted')}
                  aria-hidden="true"
                />
                <div className="min-w-0 flex-1">
                  <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
                    <span className="truncate">{label(item)}</span>
                    {current ? (
                      <span className="rounded-full bg-primary/10 px-2 py-0.5 text-[11px] font-medium text-primary">
                        {t('push.thisDevice')}
                      </span>
                    ) : null}
                  </p>
                  <p className="mt-0.5 text-xs text-text-muted">
                    {t('push.addedOn', { date: formatter.format(new Date(item.created_at)) })}
                    {' · '}
                    {item.last_success_at
                      ? t('push.lastDelivered', {
                          date: dateTime.format(new Date(item.last_success_at)),
                        })
                      : t('push.neverDelivered')}
                  </p>
                </div>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  aria-label={t('push.removeDevice', { device: label(item) })}
                  onClick={() => setConfirming(item)}
                >
                  <Trash2 className="size-4" aria-hidden="true" />
                  <span className="max-sm:sr-only">{t('push.remove')}</span>
                </Button>
              </li>
            );
          })}
        </ul>
      )}

      <Dialog open={confirming !== null} onOpenChange={open => !open && setConfirming(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('push.removeTitle')}</DialogTitle>
            <DialogDescription>
              {confirming ? t('push.removeDescription', { device: label(confirming) }) : null}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setConfirming(null)}>
              {t('cancel')}
            </Button>
            <Button
              type="button"
              variant="destructive"
              loading={device.removing || device.pending}
              onClick={() => void confirmRemove()}
            >
              {t('push.removeConfirm')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
