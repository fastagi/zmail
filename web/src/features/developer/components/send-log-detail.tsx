'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import { Check, Clock, Copy, X } from 'lucide-react';
import { useLocale } from 'next-intl';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { useTransactionalLogEntry } from '@/features/developer/hooks/use-developer';
import type {
  TransactionalLogEntry,
  TransactionalMessageEvent,
} from '@/features/developer/types';
import { useDeveloperErrorMessage } from '@/features/developer/utils/developer-error';
import {
  LOG_STATUS_CLASSES,
  logStatus,
  type LogStatus,
} from '@/features/developer/utils/log-status';
import { ErrorAlert } from '@/features/mailbox/components/mail-shared';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/** How the API masks a secret value: six bullets, or `<host>/••••` for a URL. */
const maskedValuePattern = /^(?:••••••|[^\s/]+\/••••)$/u;

const EVENT_DOT_CLASSES: Record<TransactionalMessageEvent['type'], string> = {
  accepted: 'bg-primary',
  delivered: 'bg-success',
  deferred: 'bg-warning',
  bounced: 'bg-error',
  failed: 'bg-error',
  rejected: 'bg-error',
  complained: 'bg-error',
};

/**
 * Computes end-to-end delivery latency in milliseconds from `created_at` to `sent_at`, or to the
 * first `accepted` event when `sent_at` is absent.
 */
export function computeDeliveryLatencyMs(
  createdAt: string,
  sentAt: string | null,
  events?: readonly TransactionalMessageEvent[]
): number | null {
  const start = Date.parse(createdAt);
  if (Number.isNaN(start)) return null;
  const targetTimestamp =
    sentAt ?? events?.find(event => event.type === 'accepted')?.occurred_at ?? null;
  if (!targetTimestamp) return null;
  const end = Date.parse(targetTimestamp);
  if (Number.isNaN(end) || end < start) return null;
  return end - start;
}

/** Formats a non-negative latency in milliseconds for compact display (e.g. `420ms`, `0.88s`). */
export function formatDeliveryLatency(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)}ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(2)}s`;
  const minutes = Math.floor(ms / 60_000);
  const seconds = Math.round((ms % 60_000) / 1000);
  return `${minutes}m ${seconds}s`;
}

/**
 * Builds a plain-text diagnostic summary for a send-log entry. Strictly excludes template
 * variables and any unmasked secrets; recipients are always masked.
 */
export function buildDebugInfoText({
  entry,
  status,
  sender,
  latency,
  events,
}: {
  entry: TransactionalLogEntry;
  status: LogStatus;
  sender: string;
  latency: string | null;
  events: readonly TransactionalMessageEvent[];
}): string {
  const lines = [
    `Message ID: ${entry.id}`,
    `Mode: ${entry.mode}${entry.simulated ? ' (simulated)' : ''}`,
    `Status: ${status}`,
    `Template: ${entry.template || 'direct (POST /v1/emails)'}`,
  ];
  if (sender) lines.push(`From: ${sender}`);
  lines.push(`To: ${entry.to_masked}`);
  if (entry.idempotency_key) lines.push(`Idempotency-Key: ${entry.idempotency_key}`);
  if (latency) lines.push(`Latency: ${latency}`);
  if (entry.failure_code) lines.push(`Failure Code: ${entry.failure_code}`);
  lines.push(`Created At: ${entry.created_at}`);
  if (entry.sent_at) lines.push(`Sent At: ${entry.sent_at}`);
  if (events.length > 0) {
    lines.push('Timeline:');
    for (const event of events) {
      const detailText = event.detail.trim();
      lines.push(
        `  - ${event.occurred_at} [${event.type}]${detailText ? ` ${detailText}` : ''}`
      );
    }
  }
  return lines.join('\n');
}

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
  const [copied, setCopied] = useState(false);
  const copiedTimerRef = useRef<number | undefined>(undefined);
  const timeFormat = useMemo(
    () =>
      new Intl.DateTimeFormat(locale, { hour: '2-digit', minute: '2-digit', second: '2-digit' }),
    [locale]
  );

  useEffect(() => {
    setCopied(false);
    window.clearTimeout(copiedTimerRef.current);
    return () => window.clearTimeout(copiedTimerRef.current);
  }, [entry.id]);

  const data = detail.data?.id === entry.id ? detail.data : null;
  const hasSecret = data
    ? Object.values(data.variables).some(value => maskedValuePattern.test(value))
    : false;
  const sender = data
    ? data.from.name === ''
      ? data.from.address
      : `${data.from.name} <${data.from.address}>`
    : '';
  const templateLabel = entry.template || t('log.directEmail');
  const latencyMs = computeDeliveryLatencyMs(entry.created_at, entry.sent_at, data?.events);
  const latencyText = latencyMs !== null ? formatDeliveryLatency(latencyMs) : null;

  const copyDebugInfo = async () => {
    const text = buildDebugInfoText({
      entry,
      status,
      sender,
      latency: latencyText,
      events: data?.events ?? [],
    });
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      toast.success(t('detail.debugCopied'));
      window.clearTimeout(copiedTimerRef.current);
      copiedTimerRef.current = window.setTimeout(() => setCopied(false), 2_000);
    } catch {
      toast.error(t('detail.debugCopyFailed'));
    }
  };

  return (
    <section
      aria-label={t('detail.title', { template: templateLabel, to: entry.to_masked })}
      className="min-w-0 space-y-3 rounded-md border p-4"
    >
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1">
          <h3 className="flex min-w-0 items-center gap-1.5 text-sm font-semibold">
            {entry.template ? (
              <span className="truncate">
                {t('detail.title', { template: entry.template, to: entry.to_masked })}
              </span>
            ) : (
              <>
                <span className="inline-flex flex-none items-center rounded-md bg-muted px-1.5 py-0.5 text-xs font-medium text-muted-foreground">
                  {t('log.directEmail')}
                </span>
                <span className="truncate">→ {entry.to_masked}</span>
              </>
            )}
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

      <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 tabular-nums">
          <span className="font-mono">{t('detail.messageId', { id: entry.id })}</span>
          {latencyText ? (
            <span className="inline-flex items-center gap-1">
              <Clock className="size-3" aria-hidden="true" />
              {t('detail.latency', { value: latencyText })}
            </span>
          ) : null}
        </div>
        <Button
          type="button"
          size="xs"
          variant="outline"
          icon={
            copied ? (
              <Check className="size-3.5 text-success" aria-hidden="true" />
            ) : (
              <Copy className="size-3.5" aria-hidden="true" />
            )
          }
          onClick={() => void copyDebugInfo()}
        >
          {copied ? t('detail.debugCopiedLabel') : t('detail.copyDebug')}
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
              <ol
                className={cn(
                  'relative space-y-2.5 pl-4',
                  data.events.length > 1 &&
                    'before:absolute before:top-2 before:bottom-2 before:left-[4.5px] before:w-px before:bg-border'
                )}
              >
                {data.events.map((event, index) => {
                  const eventDetail = event.detail.trim();
                  return (
                    <li key={`${event.occurred_at}-${index}`} className="relative text-sm">
                      <span
                        className={cn(
                          'absolute top-1.5 -left-4 size-2.5 rounded-full ring-2 ring-background',
                          EVENT_DOT_CLASSES[event.type]
                        )}
                        aria-hidden="true"
                      />
                      <div className="flex gap-3">
                        <time
                          dateTime={event.occurred_at}
                          className="w-20 flex-none text-muted-foreground tabular-nums"
                        >
                          {timeFormat.format(new Date(event.occurred_at))}
                        </time>
                        <div className="min-w-0 flex-1">
                          <span className="break-words">{t(`events.${event.type}`)}</span>
                          {eventDetail ? (
                            <p className="mt-0.5 break-words font-mono text-xs text-muted-foreground">
                              {eventDetail}
                            </p>
                          ) : null}
                        </div>
                      </div>
                    </li>
                  );
                })}
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
