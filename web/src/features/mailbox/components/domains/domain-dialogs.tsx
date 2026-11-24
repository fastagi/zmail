'use client';

import { useEffect, useState } from 'react';
import { toast } from 'sonner';

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
import { Skeleton } from '@/components/ui/skeleton';
import { CreateMailboxForm } from '@/features/mailbox/components/create-mailbox-form';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { ErrorAlert } from '@/features/mailbox/components/mail-shared';
import {
  useEnableReceiving,
  useMailboxDomains,
  usePreviewMailDomain,
} from '@/features/mailbox/hooks/use-mailbox';
import type { MailDomain } from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

/** The mailbox form with the verified domain already chosen as the address domain. */
export function DomainMailboxForm({
  domain,
  onCreated,
}: {
  domain: string;
  onCreated: () => void;
}) {
  const t = useT('mailbox');
  const domains = useMailboxDomains(useMailOrganizationId());
  const list = domains.data ?? [];
  // The domain list is cached for minutes; a domain verified meanwhile is not in it yet, and the
  // form would quietly fall back to the shared domain.
  const missing = domains.data !== undefined && !list.some(item => item.domain === domain);
  const { refetch } = domains;
  useEffect(() => {
    if (missing) void refetch();
  }, [missing, refetch]);
  if (domains.isPending || (missing && domains.isFetching)) {
    return <Skeleton className="h-48 w-full" aria-hidden="true" />;
  }
  const base = list.find(item => item.kind === 'shared') ?? list[0];
  if (missing) {
    return (
      <ErrorAlert
        message={t('domain.stillPending')}
        retryLabel={t('actions.retry')}
        onRetry={() => void refetch()}
      />
    );
  }
  if (domains.error || !base) {
    return (
      <ErrorAlert
        message={t(resolveMailboxErrorKey(domains.error))}
        retryLabel={t('actions.retry')}
        onRetry={() => void domains.refetch()}
      />
    );
  }
  return (
    <CreateMailboxForm domain={base} domains={list} initialDomain={domain} onCreated={onCreated} />
  );
}

/**
 * Confirms turning a send-only domain into one that receives in Zmail, listing the MX records of
 * the current mail service that Email Routing replaces.
 */
export function EnableReceivingDialog({
  domain,
  onClose,
}: {
  domain: MailDomain;
  onClose: () => void;
}) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const preview = usePreviewMailDomain(organizationId);
  const receiving = useEnableReceiving(organizationId);
  const [mx, setMx] = useState<string[] | null>(null);
  const [lookupFailed, setLookupFailed] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const { mutateAsync: lookUp } = preview;
  const name = { domain: domain.name };

  useEffect(() => {
    let active = true;
    lookUp(domain.name).then(
      result => active && setMx(result.existing_mx),
      () => active && setLookupFailed(true)
    );
    return () => {
      active = false;
    };
  }, [domain.name, lookUp]);

  const confirm = async () => {
    setError(null);
    try {
      await receiving.mutateAsync(domain.id);
      toast.success(t('domain.receivingStarted'));
      onClose();
    } catch (failure) {
      setError(t(resolveMailboxErrorKey(failure)));
    }
  };

  return (
    <Dialog open onOpenChange={open => !open && !receiving.isPending && onClose()}>
      <DialogContent size="sm">
        <DialogHeader>
          <DialogTitle>{t('domain.receivingTitle', name)}</DialogTitle>
          <DialogDescription>{t('domain.receivingDescription', name)}</DialogDescription>
        </DialogHeader>
        <DialogBody className="space-y-3 text-sm">
          {lookupFailed ? (
            <p className="text-muted-foreground">{t('domain.receivingLookupFailed')}</p>
          ) : mx === null ? (
            <p className="text-muted-foreground" role="status">
              {t('domain.receivingLoading')}
            </p>
          ) : mx.length === 0 ? (
            <p className="text-muted-foreground">{t('domain.receivingNoMx', name)}</p>
          ) : (
            <div className="space-y-1">
              <p className="font-medium">{t('domain.receivingCurrentMx')}</p>
              <ul className="space-y-1 font-mono text-xs">
                {mx.map(host => (
                  <li key={host} className="rounded-sm bg-muted px-2 py-1 break-all">
                    {host}
                  </li>
                ))}
              </ul>
            </div>
          )}
          <p className="text-muted-foreground">{t('domain.receivingDeleteHint')}</p>
          {error ? (
            <p role="alert" className="text-error">
              {error}
            </p>
          ) : null}
        </DialogBody>
        <DialogFooter>
          <Button type="button" variant="outline" disabled={receiving.isPending} onClick={onClose}>
            {t('actions.cancel')}
          </Button>
          <Button type="button" loading={receiving.isPending} onClick={() => void confirm()}>
            {t('domain.receivingConfirm')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
