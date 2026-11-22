'use client';

import type { ReactNode } from 'react';
import Link from 'next/link';
import { Code2, Globe } from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { ROUTES } from '@/constants/routes';
import {
  MailOrganizationBoundary,
  MailPageHeader,
  OrganizationLostState,
  useMailOrganizationId,
} from '@/features/mailbox/components/mail-organization';
import { ConnectedDomains } from '@/features/mailbox/components/mail-domains';
import { EmptyState, ErrorAlert } from '@/features/mailbox/components/mail-shared';
import { useMailboxDomains } from '@/features/mailbox/hooks/use-mailbox';
import { useMailOrganization } from '@/features/mailbox/hooks/use-mail-organization';
import { isOrganizationLost, resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/**
 * Domain console for an organization: connect its own domain and follow it to verified, then the
 * shared domain its mailboxes use until then, with the organization's limits. Server settings and
 * Cloudflare account steps belong to the operator and never show here.
 */
export function MailDomainStatus() {
  const t = useT('mailbox');
  const scope = useMailOrganization();
  return (
    <div className="space-y-5">
      <MailPageHeader
        title={t('domain.title')}
        description={t('domain.description')}
        scope={scope}
      />
      <MailOrganizationBoundary scope={scope}>
        {organization => <DomainStatus canManage={organization.canManage} />}
      </MailOrganizationBoundary>
    </div>
  );
}

function DomainStatus({ canManage }: { canManage: boolean }) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const domains = useMailboxDomains(organizationId);

  if (domains.isPending) {
    return (
      <div className="space-y-4" aria-hidden="true">
        <Skeleton className="h-36 w-full" />
        <Skeleton className="h-48 w-full" />
      </div>
    );
  }
  if (isOrganizationLost(domains.error)) return <OrganizationLostState />;
  if (domains.error || !domains.data) {
    return (
      <ErrorAlert
        message={t(resolveMailboxErrorKey(domains.error))}
        retryLabel={t('actions.retry')}
        onRetry={() => void domains.refetch()}
      />
    );
  }
  const domain = domains.data.find(item => item.kind === 'shared') ?? domains.data[0];
  if (!domain) {
    return (
      <EmptyState
        icon={<Globe className="size-5" aria-hidden="true" />}
        title={t('domain.emptyTitle')}
        description={t('domain.empty')}
      />
    );
  }

  const delivering = domain.provider === 'cloudflare';

  return (
    <div className="space-y-6">
      <ConnectedDomains enabled={domain.custom_domains_enabled} canManage={canManage} />

      <section aria-labelledby="mail-domain-name" className="rounded-md border">
        <div className="space-y-1 border-b p-4">
          <div className="flex flex-wrap items-center gap-3">
            <Globe className="size-5 text-muted-foreground" aria-hidden="true" />
            <h2 id="mail-domain-name" className="text-base font-semibold break-all">
              {domain.domain}
            </h2>
            <Badge variant="outline" className="font-normal text-muted-foreground">
              {t('domain.sharedBadge')}
            </Badge>
          </div>
          <p className="text-sm text-muted-foreground">
            {t('domain.sharedDescription', { domain: domain.domain })}
          </p>
        </div>
        <dl className="grid gap-px bg-border sm:grid-cols-2">
          <Fact
            label={t('domain.sending')}
            hint={delivering ? undefined : t('domain.providerHintLog')}
          >
            <StateBadge good={delivering} testId="mail-domain-provider">
              {t(delivering ? 'domain.providerCloudflare' : 'domain.providerLog')}
            </StateBadge>
          </Fact>
          <Fact
            label={t('domain.inbound')}
            hint={domain.inbound_enabled ? undefined : t('domain.inboundHintDisabled')}
          >
            <StateBadge good={domain.inbound_enabled} testId="mail-domain-inbound">
              {t(domain.inbound_enabled ? 'domain.inboundEnabled' : 'domain.inboundDisabled')}
            </StateBadge>
          </Fact>
          <Fact label={t('domain.maxMailboxes')}>
            {t('domain.maxMailboxesValue', { count: domain.max_mailboxes })}
          </Fact>
          <Fact label={t('domain.dailySendLimit')}>
            {t('domain.dailySendLimitValue', { count: domain.daily_send_limit })}
          </Fact>
        </dl>
      </section>

      <section
        aria-labelledby="mail-domain-api"
        className="flex flex-wrap items-center justify-between gap-3 rounded-md border p-4"
      >
        <div className="min-w-0 space-y-1">
          <h2 id="mail-domain-api" className="text-base font-semibold">
            {t('domain.apiTitle')}
          </h2>
          <p className="max-w-2xl text-sm text-muted-foreground">{t('domain.apiDescription')}</p>
        </div>
        <Button asChild size="sm" variant="outline">
          <Link href={ROUTES.CONSOLE.DEVELOPERS}>
            <Code2 className="size-4" aria-hidden="true" />
            {t('domain.apiOpen')}
          </Link>
        </Button>
      </section>
    </div>
  );
}

function Fact({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <div className="space-y-1 bg-background p-4">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="text-sm font-medium">{children}</dd>
      {hint ? <dd className="text-xs text-muted-foreground">{hint}</dd> : null}
    </div>
  );
}

function StateBadge({
  good,
  testId,
  children,
}: {
  good: boolean;
  testId: string;
  children: ReactNode;
}) {
  return (
    <Badge
      variant="outline"
      data-testid={testId}
      data-state={good ? 'on' : 'off'}
      className={cn(
        'border-transparent whitespace-normal',
        good ? 'bg-success-subtle text-success' : 'bg-warning-subtle text-warning'
      )}
    >
      {children}
    </Badge>
  );
}
