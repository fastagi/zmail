'use client';

import { useMemo } from 'react';
import { X } from 'lucide-react';
import { useLocale } from 'next-intl';

import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { useTransactionalLogEntry } from '@/features/developer/hooks/use-developer';
import type { TransactionalLogEntry } from '@/features/developer/types';
import { useDeveloperErrorMessage } from '@/features/developer/utils/developer-error';
import { LOG_STATUS_CLASSES, logStatus } from '@/features/developer/utils/log-status';
import { ErrorAlert } from '@/features/mailbox/components/mail-shared';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/** How the API masks a secret value: six bullets, or `<host>/••••` for a URL. */
const maskedValuePattern = /^(?:••••••|[^\s/]+\/••••)$/u;

/**
 * One send-log entry: who it went to (masked), its status, the delivery timeline oldest first, and
 * why secrets cannot be read. Nothing here holds the full recipient.
 */
export function SendLogDetail({
  organizationId,
  entry,
  onClose,
}: {
  organizationId: number;
  entry: TransactionalLogEntry;
  onClose: () => void;
}) {
  const t = useT('developer');
  const tm = useT('mailbox');
  const locale = useLocale();
  const errorMessage = useDeveloperErrorMessage();
  const detail = useTransactionalLogEntry(organizationId, entry.id);
  const status = logStatus(entry);
  const timeFormat = useMemo(
    () =>
      new Intl.DateTimeFormat(locale, { hour: '2-digit', minute: '2-digit', second: '2-digit' }),
    [locale]
  );

  const data = detail.data?.id === entry.id ? detail.data : null;
  const hasSecret = data
    ? Object.values(data.variables).some(value => maskedValuePattern.test(value))
    : false;
  const sender = data
    ? data.from.name === ''
      ? data.from.address
      : `${data.from.name} <${data.from.address}>`
    : '';

  return (
    <section
      aria-label={t('detail.title', { template: entry.template, to: entry.to_masked })}
      className="min-w-0 space-y-3 rounded-md border p-4"
    >
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1">
          <h3 className="truncate text-sm font-semibold">
            {t('detail.title', { template: entry.template, to: entry.to_masked })}
          </h3>
          <p className="mt-0.5 text-sm break-words text-muted-foreground">{entry.subject}</p>
        </div>
        <span
          className={cn(
            'inline-flex h-6 flex-none items-center rounded-full px-2.5 text-xs',
            LOG_STATUS_CLASSES[status]
          )}
        >
          {t(`status.${status}`)}
        </span>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          isIcon
          aria-label={t('detail.close')}
          title={t('detail.close')}
          onClick={onClose}
        >
          <X className="size-4" />
        </Button>
      </div>

      {entry.simulated ? (
        <p className="text-xs text-muted-foreground">{t('detail.simulatedNote')}</p>
      ) : null}
      {entry.status === 'queued' && entry.queued_reason ? (
        <p className="text-xs text-muted-foreground">
          {t(entry.queued_reason === 'send_limit' ? 'detail.queuedLimit' : 'detail.queuedRetry')}
        </p>
      ) : null}
      {entry.failure_code ? (
        <p className="font-mono text-xs text-error">
          {t('detail.failure', { code: entry.failure_code })}
        </p>
      ) : null}

      {detail.isPending ? (
        <Skeleton className="h-24 w-full" aria-hidden="true" />
      ) : !data ? (
        <ErrorAlert
          message={errorMessage(detail.error)}
          retryLabel={tm('actions.retry')}
          onRetry={() => void detail.refetch()}
        />
      ) : (
        <>
          {sender ? (
            <p className="truncate text-xs text-muted-foreground">{t('detail.from', { sender })}</p>
          ) : null}
          <div className="space-y-1.5">
            <h4 className="text-xs text-muted-foreground">{t('detail.timeline')}</h4>
            {data.events.length === 0 ? (
              <p className="text-sm text-muted-foreground">{t('detail.noEvents')}</p>
            ) : (
              <ol className="space-y-1">
                {data.events.map((event, index) => (
                  <li key={`${event.occurred_at}-${index}`} className="flex gap-3 text-sm">
                    <time
                      dateTime={event.occurred_at}
                      className="w-20 flex-none text-muted-foreground tabular-nums"
                    >
                      {timeFormat.format(new Date(event.occurred_at))}
                    </time>
                    <span className="min-w-0 break-words">{t(`events.${event.type}`)}</span>
                  </li>
                ))}
              </ol>
            )}
          </div>
          {Object.keys(data.variables).length > 0 ? (
            <div className="space-y-1.5">
              <h4 className="text-xs text-muted-foreground">{t('detail.variables')}</h4>
              <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 font-mono text-xs">
                {Object.entries(data.variables).map(([name, value]) => (
                  <div key={name} className="contents">
                    <dt className="text-muted-foreground">{name}</dt>
                    <dd className="truncate">{value}</dd>
                  </div>
                ))}
              </dl>
            </div>
          ) : null}
          {hasSecret ? (
            <p className="rounded-md bg-warning-subtle px-3 py-2 text-xs leading-relaxed text-warning">
              {t('detail.secretNote')}
            </p>
          ) : null}
        </>
      )}
    </section>
  );
}
