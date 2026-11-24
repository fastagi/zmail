'use client';

import { useRef, useState } from 'react';
import { useRouter } from 'next/navigation';
import { ChevronRight, Plus } from 'lucide-react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { ROUTES } from '@/constants/routes';
import { CloudflareConnectionForm, useCloudflareConnection } from './cloudflare-connection';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { ErrorAlert } from '@/features/mailbox/components/mail-shared';
import { useDomainReadyNotices } from '@/features/mailbox/hooks/use-domain-ready-notices';
import { mailOrganizationHref } from '@/features/mailbox/hooks/use-mail-organization';
import { useListMailZones, useMailDomains } from '@/features/mailbox/hooks/use-mailbox';
import { MAIL_DOMAIN_MAX_PER_ORGANIZATION } from '@/features/mailbox/schemas';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

import { ConnectDomainForm } from './domains/connect-domain-form';
import { DomainCard } from './domains/domain-card';
import { ZoneCard } from './domains/zone-card';

export { connectDomainErrorKey } from './domains/connect-domain-form';

/**
 * Domains the organization connected: connect one, see every DNS record it needs with its status,
 * run the setup or the check again, and remove a domain no mailbox uses. Members see the same
 * list without the actions.
 */
export function ConnectedDomains({ enabled, canManage }: { enabled: boolean; canManage: boolean }) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const router = useRouter();
  const domains = useMailDomains(organizationId, enabled);
  const zones = useListMailZones(organizationId, enabled);
  useDomainReadyNotices(domains.data, {
    onZoneActive: zone => toast.message(t('domain.zoneActive', { zone })),
    onReady: domain =>
      toast.success(t('domain.domainReady', { domain }), {
        action: canManage
          ? {
              label: t('domain.domainReadyAction'),
              onClick: () =>
                router.push(mailOrganizationHref(ROUTES.CONSOLE.MAILBOXES, organizationId)),
            }
          : undefined,
      }),
  });
  // A managed zone no domain lives in any more can be edited or removed on its own.
  const unusedZones =
    zones.data && domains.data
      ? zones.data.filter(zone => !domains.data.some(domain => domain.zone?.id === zone.id))
      : [];
  const connectedCount = domains.data?.length ?? 0;
  const atLimit = connectedCount >= MAIL_DOMAIN_MAX_PER_ORGANIZATION;
  const connectForm = useRef<HTMLFormElement>(null);
  const connectInput = useRef<HTMLInputElement>(null);
  const showConnectForm = () => {
    connectForm.current?.scrollIntoView({ behavior: 'smooth', block: 'center' });
    connectInput.current?.focus({ preventScroll: true });
  };

  return (
    <section aria-labelledby="mail-domains-connected" className="space-y-4 rounded-md border p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 space-y-1">
          <h2 id="mail-domains-connected" className="text-base font-semibold">
            {t('domain.connectedTitle')}
          </h2>
          <p className="max-w-2xl text-sm text-muted-foreground">
            {t(enabled ? 'domain.connectedDescription' : 'domain.connectedDisabled')}
          </p>
        </div>
        {enabled && domains.data ? (
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <span className="text-sm text-muted-foreground" data-testid="mail-domain-count">
              {t('domain.domainCount', {
                count: connectedCount,
                max: MAIL_DOMAIN_MAX_PER_ORGANIZATION,
              })}
            </span>
            {canManage ? (
              <Button
                type="button"
                size="sm"
                disabled={atLimit}
                aria-describedby={atLimit ? 'mail-domain-limit-hint' : undefined}
                icon={<Plus className="size-4" />}
                onClick={showConnectForm}
              >
                {t('domain.addDomainAction')}
              </Button>
            ) : null}
            {canManage && atLimit ? (
              <p id="mail-domain-limit-hint" className="basis-full text-xs text-muted-foreground">
                {t('domain.domainLimitReached', { max: MAIL_DOMAIN_MAX_PER_ORGANIZATION })}
              </p>
            ) : null}
          </div>
        ) : null}
      </div>
      {!enabled ? null : domains.isPending ? (
        <Skeleton className="h-24 w-full" aria-hidden="true" />
      ) : domains.error || !domains.data ? (
        <ErrorAlert
          message={t(resolveMailboxErrorKey(domains.error))}
          retryLabel={t('actions.retry')}
          onRetry={() => void domains.refetch()}
        />
      ) : (
        <>
          {domains.data.length === 0 && unusedZones.length === 0 ? (
            <p className="text-sm text-muted-foreground">{t('domain.connectedEmpty')}</p>
          ) : (
            <ul className="space-y-4">
              {domains.data.map(domain => (
                <li key={domain.id}>
                  <DomainCard domain={domain} canManage={canManage} />
                </li>
              ))}
              {unusedZones.map(zone => (
                <li key={`zone-${zone.id}`}>
                  <ZoneCard zone={zone} canManage={canManage} />
                </li>
              ))}
            </ul>
          )}
          {canManage ? (
            <>
              <ConnectDomainForm
                another={connectedCount > 0}
                formRef={connectForm}
                inputRef={connectInput}
              />
              <OwnCloudflareAccount key={organizationId} organizationId={organizationId} />
            </>
          ) : (
            <p className="text-xs text-muted-foreground">{t('domain.connectReadOnly')}</p>
          )}
        </>
      )}
    </section>
  );
}

/**
 * The way for a domain whose DNS stays in the organization's own Cloudflare account. Few need it,
 * so it stays folded unless the organization already connected an account.
 */
function OwnCloudflareAccount({ organizationId }: { organizationId: number }) {
  const t = useT('mailbox');
  const connection = useCloudflareConnection(organizationId);
  const [open, setOpen] = useState<boolean | null>(null);
  const connected = Boolean(connection.data?.connection);
  return (
    <details
      open={open ?? connected}
      onToggle={event => setOpen(event.currentTarget.open)}
      className="group rounded-md border"
    >
      <summary className="flex cursor-pointer list-none items-center gap-2 px-4 py-3 text-sm font-medium focus-ring [&::-webkit-details-marker]:hidden">
        <ChevronRight
          className="size-4 shrink-0 text-muted-foreground transition-transform group-open:rotate-90"
          aria-hidden="true"
        />
        {t('domain.cfSummary')}
      </summary>
      <div className="border-t p-3">
        <CloudflareConnectionForm organizationId={organizationId} />
      </div>
    </details>
  );
}
