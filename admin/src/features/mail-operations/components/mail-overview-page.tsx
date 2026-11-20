import { Building2, ChartColumn, Mailbox, Send, Users } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { SectionCard } from '@/components/layout/section-card';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { DayPicker } from '@/features/mail-operations/components/day-picker';
import { LoadFailure } from '@/features/mail-operations/components/load-failure';
import { useMailOverview } from '@/features/mail-operations/hooks/use-mail-operations';
import type { MailDaySearch, MailMetrics } from '@/features/mail-operations/types';
import { cn } from '@/lib/cn';

interface MailOverviewPageProps {
  search: MailDaySearch;
  onSearchChange: (search: MailDaySearch) => void;
  /** Opens the organization list for one day. */
  onOpenDay: (date: string) => void;
}

export function MailOverviewPage({ search, onSearchChange, onOpenDay }: MailOverviewPageProps) {
  const { t } = useTranslation();
  const overview = useMailOverview(search.date);

  return (
    <div className="page-stack">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
        <header className="page-header">
          <p className="page-eyebrow">{t('mailOperations.eyebrow')}</p>
          <h1>{t('mailOperations.overview.title')}</h1>
          <p>{t('mailOperations.overview.description')}</p>
        </header>
        <DayPicker value={search.date} onChange={(date) => onSearchChange({ date })} />
      </div>

      {overview.isPending ? (
        <div className="metric-grid" aria-busy="true">
          {Array.from({ length: 3 }, (_, index) => (
            <Skeleton key={index} className="h-20 w-full" />
          ))}
        </div>
      ) : overview.isError ? (
        <Card className="rounded-lg py-0 shadow-xs">
          <LoadFailure
            error={overview.error}
            onRetry={() => void overview.refetch()}
            retrying={overview.isFetching}
          />
        </Card>
      ) : (
        <OverviewContent metrics={overview.data} onOpenDay={onOpenDay} />
      )}
    </div>
  );
}

function OverviewContent({
  metrics,
  onOpenDay,
}: {
  metrics: MailMetrics;
  onOpenDay: (date: string) => void;
}) {
  const { t } = useTranslation();
  const totals = [
    {
      icon: Building2,
      label: t('mailOperations.overview.organizations'),
      value: metrics.organizations,
    },
    {
      icon: Users,
      label: t('mailOperations.overview.members'),
      value: metrics.members,
      hint: t('mailOperations.overview.disabledMembers', { count: metrics.disabled_members }),
    },
    { icon: Mailbox, label: t('mailOperations.overview.mailboxes'), value: metrics.mailboxes },
  ];
  const statuses = [
    { key: 'sent', value: metrics.day.sent, tone: 'text-foreground' },
    { key: 'failed', value: metrics.day.failed, tone: 'text-danger-strong' },
    { key: 'queued', value: metrics.day.queued, tone: 'text-foreground' },
  ] as const;
  const receipts = [
    { key: 'delivered', value: metrics.day.delivered },
    { key: 'bounced', value: metrics.day.bounced },
    { key: 'complained', value: metrics.day.complained },
  ] as const;
  const peak = Math.max(1, ...metrics.trend.map((day) => day.sent + day.failed + day.queued));

  return (
    <>
      <div className="metric-grid">
        {totals.map((total) => {
          const Icon = total.icon;
          return (
            <Card
              key={total.label}
              className="flex items-center gap-3 rounded-lg px-4 py-3.5 shadow-xs"
            >
              <span className="icon-surface" aria-hidden="true">
                <Icon className="size-4" />
              </span>
              <div className="min-w-0">
                <p className="text-xs font-medium text-subtle">{total.label}</p>
                <p className="mt-0.5 text-base font-semibold tabular-nums">{total.value}</p>
                {total.hint ? <p className="text-xs text-muted-foreground">{total.hint}</p> : null}
              </div>
            </Card>
          );
        })}
      </div>

      <div className="content-grid">
        <SectionCard
          icon={Send}
          titleId="mail-day-title"
          title={t('mailOperations.overview.dayTitle', { date: metrics.date })}
          description={t('mailOperations.overview.dayDescription')}
          actions={
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => onOpenDay(metrics.date)}
            >
              {t('mailOperations.overview.openDay')}
            </Button>
          }
        >
          <dl className="grid grid-cols-3 gap-3">
            {statuses.map((status) => (
              <div key={status.key}>
                <dt className="text-xs font-medium text-subtle">
                  {t(`mailOperations.status.${status.key}`)}
                </dt>
                <dd className={cn('text-xl font-semibold tabular-nums', status.tone)}>
                  {status.value}
                </dd>
              </div>
            ))}
          </dl>
          <p className="mt-4 text-xs font-medium text-subtle">
            {t('mailOperations.overview.receipts')}
          </p>
          <dl className="mt-1 grid grid-cols-3 gap-3">
            {receipts.map((receipt) => (
              <div key={receipt.key}>
                <dt className="text-xs text-muted-foreground">
                  {t(`mailOperations.receipt.${receipt.key}`)}
                </dt>
                <dd className="text-base font-semibold tabular-nums">{receipt.value}</dd>
              </div>
            ))}
          </dl>
        </SectionCard>

        <SectionCard
          icon={ChartColumn}
          titleId="mail-trend-title"
          title={t('mailOperations.overview.trendTitle')}
          description={t('mailOperations.overview.trendDescription')}
          contentClassName="p-0"
        >
          <ul className="divide-y divide-border">
            {metrics.trend.map((day) => {
              const total = day.sent + day.failed + day.queued;
              return (
                <li key={day.date}>
                  <button
                    type="button"
                    onClick={() => onOpenDay(day.date)}
                    aria-current={day.date === metrics.date ? 'date' : undefined}
                    className={cn(
                      'grid w-full grid-cols-[6.5rem_1fr_auto] items-center gap-3 px-4 py-2 text-left text-sm transition-colors hover:bg-muted/50 focus-visible:bg-muted/50 focus-visible:outline-none',
                      day.date === metrics.date && 'bg-muted/40 font-medium',
                    )}
                  >
                    <span className="tabular-nums">{day.date}</span>
                    <span aria-hidden="true" className="h-2 rounded-full bg-muted">
                      <span
                        className="block h-2 rounded-full bg-brand"
                        style={{ width: `${(total / peak) * 100}%` }}
                      />
                    </span>
                    <span className="whitespace-nowrap tabular-nums text-muted-foreground">
                      {t('mailOperations.overview.trendRow', {
                        sent: day.sent,
                        failed: day.failed,
                        queued: day.queued,
                      })}
                    </span>
                  </button>
                </li>
              );
            })}
          </ul>
        </SectionCard>
      </div>
    </>
  );
}
