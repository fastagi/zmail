'use client';

import { useLocale } from 'next-intl';

import { Skeleton } from '@/components/ui/skeleton';
import { SuppressionList } from '@/features/developer/components/suppression-list';
import { useSendLanes } from '@/features/developer/hooks/use-developer';
import { MAIL_SEND_LANES } from '@/features/developer/schemas';
import type { MailSendLane, MailSendLaneUsage } from '@/features/developer/types';
import { useDeveloperErrorMessage } from '@/features/developer/utils/developer-error';
import { ErrorAlert } from '@/features/mailbox/components/mail-shared';
import type { MailOrganization } from '@/features/mailbox/hooks/use-mail-organization';
import type { MailboxDomain } from '@/features/mailbox/types';
import { useT } from '@/i18n';
import { cn } from '@/utils';

const PROTECTION_RULES = ['recipient', 'key', 'idempotency', 'suppression', 'secrets'] as const;

/**
 * Limits and protection: the three send lanes, the read-only protection rules, and (for owners and
 * admins) the suppression list. Owners and admins see each lane's usage from `mail-send-lanes`;
 * members see only the limits, which every member reads from `mailbox-domains`.
 */
export function LimitsTab({
  organization,
  domains,
}: {
  organization: MailOrganization;
  domains: readonly MailboxDomain[];
}) {
  const t = useT('developer');
  const locale = useLocale();
  // The limits and rules are instance settings, the same on every domain.
  const settings = domains.find(domain => domain.transactional_enabled) ?? domains[0];
  const number = (value: number) => value.toLocaleString(locale);

  const ruleValues: Record<(typeof PROTECTION_RULES)[number], string> = {
    recipient: t('limits.rules.recipient.value', {
      count: number(settings?.transactional_recipient_limit_per_minute ?? 0),
    }),
    key: t('limits.rules.key.value', {
      count: number(settings?.transactional_key_limit_per_second ?? 0),
    }),
    idempotency: t('limits.rules.idempotency.value'),
    suppression: t('limits.rules.suppression.value'),
    secrets: t('limits.rules.secrets.value'),
  };

  return (
    <div className="space-y-8">
      <div className="grid gap-8 lg:grid-cols-2">
        <section aria-labelledby="developer-lanes-title" className="min-w-0">
          <h2 id="developer-lanes-title" className="text-base font-semibold">
            {t('limits.lanesTitle')}
          </h2>
          <p className="mt-1 text-sm text-muted-foreground">{t('limits.lanesDescription')}</p>
          <SendLanes organization={organization} settings={settings} />
        </section>
        <section aria-labelledby="developer-rules-title" className="min-w-0">
          <h2 id="developer-rules-title" className="text-base font-semibold">
            {t('limits.rulesTitle')}
          </h2>
          <ul aria-label={t('limits.rulesTitle')} className="mt-4 space-y-2.5">
            {PROTECTION_RULES.map(rule => (
              <li key={rule} className="flex items-start gap-3 rounded-md border px-3.5 py-3">
                <span className="min-w-0 flex-1">
                  <span className="block text-sm font-medium">
                    {t(`limits.rules.${rule}.name`)}
                  </span>
                  <span className="block text-xs text-muted-foreground">
                    {t(`limits.rules.${rule}.description`, {
                      count: number(settings?.transactional_recipient_limit_per_minute ?? 0),
                    })}
                  </span>
                </span>
                <span
                  data-rule-value
                  className="shrink-0 font-mono text-sm whitespace-nowrap text-muted-foreground"
                >
                  {ruleValues[rule]}
                </span>
              </li>
            ))}
          </ul>
        </section>
      </div>
      {organization.canManage ? <SuppressionList organizationId={organization.id} /> : null}
    </div>
  );
}

function SendLanes({
  organization,
  settings,
}: {
  organization: MailOrganization;
  settings: MailboxDomain | undefined;
}) {
  const t = useT('developer');
  const tm = useT('mailbox');
  const locale = useLocale();
  const errorMessage = useDeveloperErrorMessage();
  const usage = useSendLanes(organization.id, { canManage: organization.canManage });
  const number = (value: number) => value.toLocaleString(locale);

  const configured: Record<MailSendLane, number> = {
    transactional: settings?.daily_transactional_limit ?? 0,
    broadcast: settings?.daily_broadcast_limit ?? 0,
    personal: settings?.daily_send_limit ?? 0,
  };
  const lanes = usage.data?.lanes;

  return (
    <div className="mt-4 space-y-4">
      {organization.canManage && usage.error ? (
        <ErrorAlert
          message={errorMessage(usage.error)}
          retryLabel={tm('actions.retry')}
          onRetry={() => void usage.refetch()}
        />
      ) : null}
      <ul aria-label={t('limits.lanesLabel')} className="space-y-4">
        {MAIL_SEND_LANES.map(lane => {
          const measured = lanes?.find(item => item.lane === lane);
          return (
            <li key={lane}>
              <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
                <span className="text-sm font-medium">{t(`limits.lanes.${lane}.name`)}</span>
                {measured ? (
                  <span className="text-sm text-muted-foreground tabular-nums">
                    {t('limits.usage', {
                      used: number(measured.used),
                      limit: number(measured.limit),
                    })}
                  </span>
                ) : organization.canManage && usage.isPending ? (
                  <Skeleton className="h-4 w-20" aria-hidden="true" />
                ) : (
                  <span className="text-sm text-muted-foreground tabular-nums">
                    {t('limits.limitOnly', { limit: number(configured[lane]) })}
                  </span>
                )}
              </div>
              {measured ? (
                <LaneBar usage={measured} label={t(`limits.lanes.${lane}.name`)} />
              ) : null}
              <p className="mt-1 text-xs text-muted-foreground">
                {t(`limits.lanes.${lane}.note`)}
                {measured && measured.waiting > 0 ? (
                  <span className="ml-1 font-medium text-warning">
                    {t(`limits.waiting.${lane}`, { count: measured.waiting })}
                  </span>
                ) : null}
              </p>
            </li>
          );
        })}
      </ul>
      <p className="text-xs text-muted-foreground">
        {organization.canManage ? t('limits.window') : t('limits.usageAdminsOnly')}
      </p>
    </div>
  );
}

/** Usage may exceed the limit by the requests in flight; the bar stops at full. */
function LaneBar({ usage, label }: { usage: MailSendLaneUsage; label: string }) {
  const shown = Math.min(usage.used, usage.limit);
  const full = usage.used >= usage.limit;
  return (
    <div
      role="progressbar"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={usage.limit}
      aria-valuenow={shown}
      className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-muted"
    >
      <span
        className={cn(
          'block h-full rounded-full transition-[width] duration-300',
          full ? 'bg-warning' : 'bg-primary'
        )}
        style={{ width: `${(shown / usage.limit) * 100}%` }}
      />
    </div>
  );
}
