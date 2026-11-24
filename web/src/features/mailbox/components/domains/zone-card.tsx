'use client';

import { useState } from 'react';
import Link from 'next/link';
import { Globe, ListTree, Trash2 } from 'lucide-react';
import { toast } from 'sonner';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { getMailZoneDnsRoute } from '@/constants/routes';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { useDeleteMailZone } from '@/features/mailbox/hooks/use-mailbox';
import { mailOrganizationHref } from '@/features/mailbox/hooks/use-mail-organization';
import type { MailZone } from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/**
 * A managed zone no domain uses: its DNS can still be edited, and it can be removed from Zmail
 * once the registrar no longer points at Cloudflare.
 */
export function ZoneCard({ zone, canManage }: { zone: MailZone; canManage: boolean }) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const remove = useDeleteMailZone(organizationId);
  const [confirmRemove, setConfirmRemove] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const headingId = `mail-zone-${zone.id}`;
  const active = zone.status === 'active';

  const confirm = async () => {
    setError(null);
    try {
      await remove.mutateAsync(zone.id);
      setConfirmRemove(false);
      toast.success(t('domain.zoneRemoved'));
    } catch (failure) {
      setConfirmRemove(false);
      if (
        failure instanceof ApiError &&
        failure.errorCode === ApiErrorCode.MAILBOX_ZONE_STILL_DELEGATED
      ) {
        setError(
          zone.original_name_servers.length > 0
            ? t('domain.zoneStillDelegated', {
                zone: zone.name,
                servers: zone.original_name_servers.join(', '),
              })
            : t('domain.zoneStillDelegatedUnknown', { zone: zone.name })
        );
      } else {
        setError(t(resolveMailboxErrorKey(failure)));
      }
    }
  };

  return (
    <article aria-labelledby={headingId} className="rounded-md border" data-zone={zone.name}>
      <div className="flex flex-wrap items-center gap-3 border-b p-3">
        <Globe className="size-4 text-muted-foreground" aria-hidden="true" />
        <h3 id={headingId} className="min-w-0 font-semibold break-all">
          {zone.name}
        </h3>
        <Badge
          variant="outline"
          className={cn(
            'border-transparent',
            active ? 'bg-success-subtle text-success' : 'bg-warning-subtle text-warning'
          )}
        >
          {t(active ? 'domain.zoneStatusActive' : 'domain.zoneStatusPending')}
        </Badge>
        <div className="ml-auto flex flex-wrap gap-2">
          <Button asChild size="sm" variant="outline">
            <Link href={mailOrganizationHref(getMailZoneDnsRoute(zone.id), organizationId)}>
              <ListTree className="size-4" aria-hidden="true" />
              {t('domain.dnsRecords')}
            </Link>
          </Button>
          {canManage ? (
            <Button
              type="button"
              size="sm"
              variant="ghost"
              disabled={remove.isPending}
              icon={<Trash2 className="size-4" />}
              onClick={() => setConfirmRemove(true)}
            >
              {t('domain.zoneRemoveAction')}
            </Button>
          ) : null}
        </div>
      </div>
      <div className="space-y-3 p-3">
        <p className="text-sm text-muted-foreground">{t('domain.zoneUnused')}</p>
        {error ? (
          <p role="alert" className="text-sm break-words text-error">
            {error}
          </p>
        ) : null}
      </div>
      {confirmRemove ? (
        <Dialog open onOpenChange={open => !open && !remove.isPending && setConfirmRemove(false)}>
          <DialogContent size="sm">
            <DialogHeader>
              <DialogTitle>{t('domain.zoneRemoveTitle', { zone: zone.name })}</DialogTitle>
              <DialogDescription>
                {t('domain.zoneRemoveDescription', { zone: zone.name })}
              </DialogDescription>
            </DialogHeader>
            <DialogBody />
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                disabled={remove.isPending}
                onClick={() => setConfirmRemove(false)}
              >
                {t('actions.cancel')}
              </Button>
              <Button
                type="button"
                variant="destructive"
                loading={remove.isPending}
                onClick={() => void confirm()}
              >
                {t('domain.zoneRemoveAction')}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      ) : null}
    </article>
  );
}
