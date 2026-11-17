'use client';

import type { ReactNode } from 'react';
import Link from 'next/link';
import { CheckCircle2, Circle, Globe, KeyRound, TriangleAlert } from 'lucide-react';

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
import { useMailboxDomains, useMailboxes } from '@/features/mailbox/hooks/use-mailbox';
import { useMailOrganization } from '@/features/mailbox/hooks/use-mail-organization';
import type { MailboxDomain } from '@/features/mailbox/types';
import { isOrganizationLost, resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/** `true` and `false` come from the API; `null` is a Cloudflare step this page cannot observe. */
type StepState = boolean | null;

const apiSettings = [
  'MAILBOX_PROVIDER=cloudflare',
  'MAILBOX_CLOUDFLARE_ACCOUNT_ID',
  'MAILBOX_CLOUDFLARE_API_TOKEN',
  'MAILBOX_INBOUND_SECRET',
];

/**
 * Domain console: what the API reports about the configured mail domain, a Cloudflare setup
 * checklist, and how to send with an API key. Everything here is read-only; the configuration
 * lives on the API and in Cloudflare. The limits are those of the chosen organization.
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
  const mailboxes = useMailboxes(organizationId);

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
  const sender = mailboxes.data?.[0]?.address ?? `hello@${domain.domain}`;

  return (
    <div className="space-y-6">
      <section aria-labelledby="mail-domain-name" className="rounded-md border">
        <div className="flex flex-wrap items-center gap-3 border-b p-4">
          <Globe className="size-5 text-muted-foreground" aria-hidden="true" />
          <h2 id="mail-domain-name" className="text-lg font-semibold break-all">
            {domain.domain}
          </h2>
          <Badge variant="outline" className="font-normal text-muted-foreground">
            {t('domain.sharedBadge')}
          </Badge>
        </div>
        <dl className="grid gap-px bg-border sm:grid-cols-2">
          <Fact
            label={t('domain.sending')}
            hint={t(delivering ? 'domain.providerHintCloudflare' : 'domain.providerHintLog')}
          >
            <StateBadge good={delivering} testId="mail-domain-provider">
              {t(delivering ? 'domain.providerCloudflare' : 'domain.providerLog')}
            </StateBadge>
          </Fact>
          <Fact
            label={t('domain.inbound')}
            hint={t(
              domain.inbound_enabled ? 'domain.inboundHintEnabled' : 'domain.inboundHintDisabled'
            )}
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

      <ConnectedDomains enabled={domain.custom_domains_enabled} canManage={canManage} />

      <SetupChecklist domain={domain} />

      <section aria-labelledby="mail-domain-api" className="space-y-3 rounded-md border p-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0 space-y-1">
            <h2 id="mail-domain-api" className="text-base font-semibold">
              {t('domain.apiTitle')}
            </h2>
            <p className="max-w-2xl text-sm text-muted-foreground">{t('domain.apiDescription')}</p>
          </div>
          <Button asChild size="sm" variant="outline">
            <Link href={ROUTES.CONSOLE.SETTINGS}>
              <KeyRound className="size-4" aria-hidden="true" />
              {t('domain.apiKeys')}
            </Link>
          </Button>
        </div>
        <pre
          aria-label={t('domain.apiExample')}
          tabIndex={0}
          className="overflow-x-auto rounded-md border bg-muted/40 p-3 font-mono text-xs focus-ring"
        >
          <code>{apiExample(sender, organizationId)}</code>
        </pre>
        <p className="text-xs text-muted-foreground">{t('domain.apiHostNote')}</p>
      </section>
    </div>
  );
}

function SetupChecklist({ domain }: { domain: MailboxDomain }) {
  const t = useT('mailbox');
  const name = { domain: domain.domain };
  const delivering = domain.provider === 'cloudflare';
  const steps: { key: string; state: StepState; title: string; body: ReactNode }[] = [
    {
      key: 'routing',
      state: null,
      title: t('domain.routingTitle', name),
      body: t('domain.routingBody'),
    },
    {
      key: 'rule',
      state: null,
      title: t('domain.ruleTitle'),
      body: t('domain.ruleBody', name),
    },
    {
      key: 'sending',
      state: delivering,
      title: t('domain.sendingTitle', name),
      body: t('domain.sendingBody'),
    },
    {
      key: 'inbound',
      state: domain.inbound_enabled,
      title: t('domain.inboundTitle'),
      body: t('domain.inboundBody'),
    },
    {
      key: 'config',
      state: delivering && domain.inbound_enabled ? true : null,
      title: t('domain.configTitle'),
      body: (
        <>
          {t('domain.configBody')}
          <span className="mt-1.5 flex flex-wrap gap-1.5">
            {apiSettings.map(setting => (
              <code key={setting} className="rounded-sm bg-muted px-1.5 py-0.5 text-xs">
                {setting}
              </code>
            ))}
          </span>
        </>
      ),
    },
  ];

  return (
    <section aria-labelledby="mail-domain-checklist" className="space-y-3 rounded-md border p-4">
      <div className="space-y-1">
        <h2 id="mail-domain-checklist" className="text-base font-semibold">
          {t('domain.checklistTitle')}
        </h2>
        <p className="max-w-2xl text-sm text-muted-foreground">
          {t('domain.checklistDescription')}
        </p>
      </div>
      <ol className="divide-y">
        {steps.map(step => {
          const Icon =
            step.state === true ? CheckCircle2 : step.state === false ? TriangleAlert : Circle;
          const stateLabel = t(
            step.state === true
              ? 'domain.stepDone'
              : step.state === false
                ? 'domain.stepPending'
                : 'domain.stepManual'
          );
          return (
            <li
              key={step.key}
              data-step={step.key}
              data-state={step.state === null ? 'manual' : step.state ? 'done' : 'pending'}
              className="flex gap-3 py-3"
            >
              <Icon
                className={cn(
                  'mt-0.5 size-4 shrink-0',
                  step.state === true
                    ? 'text-success'
                    : step.state === false
                      ? 'text-warning'
                      : 'text-muted-foreground'
                )}
                aria-hidden="true"
              />
              <div className="min-w-0 flex-1 space-y-0.5">
                <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
                  {step.title}
                  <Badge variant="outline" className="font-normal text-muted-foreground">
                    {stateLabel}
                  </Badge>
                </p>
                <div className="text-sm text-muted-foreground">{step.body}</div>
              </div>
            </li>
          );
        })}
      </ol>
    </section>
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

/** A copyable request; the sender is one of the organization's addresses on the real domain. */
function apiExample(sender: string, organizationId: number): string {
  const body = JSON.stringify(
    {
      from: sender,
      to: ['bob@example.com'],
      subject: 'Your receipt',
      text: 'Thanks for your order.',
    },
    null,
    2
  );
  return [
    'curl -X POST https://YOUR_API_HOST/v1/outbound-mail-messages \\',
    '  -H "X-API-Key: $FASTBUILD_API_KEY" \\',
    `  -H "Organization-Id: ${organizationId}" \\`,
    '  -H "Idempotency-Key: receipt-1042" \\',
    '  -H "Content-Type: application/json" \\',
    `  -d '${body}'`,
  ].join('\n');
}
