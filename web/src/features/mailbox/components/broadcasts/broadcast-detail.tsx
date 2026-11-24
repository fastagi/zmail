'use client';

import { useState } from 'react';
import { useLocale } from 'next-intl';
import { AlertCircle, Clock, Gauge, Pause, Play, Trash2, XCircle } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { RecipientTable } from '@/features/mailbox/components/broadcasts/broadcast-recipients';
import {
  BackButton,
  ConfirmDialog,
  knownPause,
  Progress,
  StatusBadge,
  useWhen,
} from '@/features/mailbox/components/broadcasts/broadcast-shared';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import {
  relativeTime,
  useDateTimeFormat,
  useRelativeTimeFormat,
} from '@/features/mailbox/components/mail-shared';
import {
  useCancelMailBroadcast,
  useDeleteMailBroadcast,
  usePauseMailBroadcast,
  useResumeMailBroadcast,
} from '@/features/mailbox/hooks/use-mail-broadcasts';
import type { MailBroadcast } from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

export function BroadcastDetail({
  broadcast,
  canManage,
  onBack,
}: {
  broadcast: MailBroadcast;
  canManage: boolean;
  onBack: () => void;
}) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const cancel = useCancelMailBroadcast(organizationId);
  const pause = usePauseMailBroadcast(organizationId);
  const resume = useResumeMailBroadcast(organizationId);
  const remove = useDeleteMailBroadcast(organizationId);
  const when = useWhen();
  const formatter = useDateTimeFormat();
  const [confirm, setConfirm] = useState<'cancel' | 'delete' | 'resume' | null>(null);
  const paused = broadcast.status === 'paused';
  const running = broadcast.status === 'sending' || broadcast.status === 'scheduled' || paused;
  const waiting = paused && broadcast.stop_reason === 'test_wave_complete';
  const healthStop =
    paused &&
    (broadcast.stop_reason === 'bounce_rate_high' ||
      broadcast.stop_reason === 'complaint_rate_high');
  const { counts, progress } = broadcast;

  const doPause = async () => {
    try {
      await pause.mutateAsync(broadcast.id);
      toast.success(t('broadcasts.pausedToast'));
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
  };

  const doResume = async () => {
    try {
      const resumed = await resume.mutateAsync(broadcast.id);
      // A test wave paused by hand during its wait goes back to that wait rather than sending.
      if (
        'stop_reason' in resumed &&
        resumed.status === 'paused' &&
        resumed.stop_reason === 'test_wave_complete' &&
        resumed.resume_at
      ) {
        toast.success(
          t('broadcasts.resumedWaitingToast', {
            time: formatter.format(new Date(resumed.resume_at)),
          })
        );
      } else {
        toast.success(t('broadcasts.resumedToast'));
      }
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
    setConfirm(null);
  };

  const doCancel = async () => {
    try {
      await cancel.mutateAsync(broadcast.id);
      toast.success(t('broadcasts.canceled'));
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
    setConfirm(null);
  };

  const doDelete = async () => {
    try {
      await remove.mutateAsync(broadcast.id);
      toast.success(t('broadcasts.deleted'));
      setConfirm(null);
      onBack();
    } catch (failure) {
      setConfirm(null);
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
  };

  const attempted = counts.sent + counts.failed;
  const percentage = (value: number, total: number) =>
    total > 0
      ? new Intl.NumberFormat(undefined, { style: 'percent', maximumFractionDigits: 2 }).format(
          value / total
        )
      : '—';
  // The core counts always show; the rest only once they are not zero (pending also while it
  // runs), so a clean finished broadcast is not a wall of zeros on a phone.
  const optionalStats: [string, number, boolean][] = [
    [t('broadcasts.failed'), counts.failed, false],
    [t('broadcasts.skipped'), counts.skipped, false],
    [t('broadcasts.pending'), counts.pending, running],
    [t('broadcasts.canceledCount'), counts.canceled, false],
    [t('broadcasts.bounced'), counts.bounced, false],
    [t('broadcasts.complained'), counts.complained, false],
  ];
  const stats: [string, number | string][] = [
    [t('broadcasts.queueProgress'), percentage(counts.total - counts.pending, counts.total)],
    [t('broadcasts.sendSuccessRate'), percentage(counts.sent, attempted)],
    [t('broadcasts.total'), counts.total],
    [t('broadcasts.sent'), counts.sent],
    ...optionalStats
      .filter(([, value, always]) => always || value > 0)
      .map(([label, value]): [string, number] => [label, value]),
  ];
  const clean = counts.sent > 0 && counts.failed + counts.bounced + counts.complained === 0;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <BackButton onBack={onBack} />
        <div className="flex flex-wrap gap-2">
          {running && canManage && (broadcast.status === 'sending' || waiting) ? (
            <Button
              type="button"
              size="sm"
              variant="outline"
              loading={pause.isPending}
              icon={<Pause className="size-4" />}
              onClick={() => void doPause()}
            >
              {t('broadcasts.pause')}
            </Button>
          ) : null}
          {paused && canManage ? (
            <Button
              type="button"
              size="sm"
              loading={resume.isPending}
              icon={<Play className="size-4" />}
              onClick={() => (healthStop ? setConfirm('resume') : void doResume())}
            >
              {t(waiting ? 'broadcasts.resumeNow' : 'broadcasts.resume')}
            </Button>
          ) : null}
          {running ? (
            canManage ? (
              <Button
                type="button"
                size="sm"
                variant="outline"
                icon={<XCircle className="size-4" />}
                onClick={() => setConfirm('cancel')}
              >
                {t('broadcasts.cancel')}
              </Button>
            ) : null
          ) : (
            <Button
              type="button"
              size="sm"
              variant="outline"
              icon={<Trash2 className="size-4" />}
              onClick={() => setConfirm('delete')}
            >
              {t('broadcasts.delete')}
            </Button>
          )}
        </div>
      </div>
      <div className="space-y-1">
        <div className="flex flex-wrap items-center gap-3">
          <h2 className="text-lg font-semibold break-words">{broadcast.name}</h2>
          <StatusBadge broadcast={broadcast} />
        </div>
        <p className="text-sm text-muted-foreground">{when(broadcast)}</p>
      </div>
      {broadcast.stop_reason === 'send_limit_reached' && running ? (
        <Alert data-testid="broadcast-limit">
          <Clock className="size-4" aria-hidden="true" />
          <AlertDescription>
            {progress?.send_limit_resumes_at
              ? t('broadcasts.limitPaused', {
                  pending: counts.pending,
                  time: formatter.format(new Date(progress.send_limit_resumes_at)),
                })
              : t('broadcasts.limitPausedNoTime', { pending: counts.pending })}
          </AlertDescription>
        </Alert>
      ) : progress?.send_limit_pauses && progress.send_limit_resumes_at && !paused ? (
        <Alert data-testid="broadcast-limit-ahead">
          <Clock className="size-4" aria-hidden="true" />
          <AlertDescription>
            {t('broadcasts.limitAhead', {
              time: formatter.format(new Date(progress.send_limit_resumes_at)),
            })}
          </AlertDescription>
        </Alert>
      ) : null}
      {paused && knownPause(broadcast.stop_reason) ? (
        <Alert variant={healthStop ? 'destructive' : 'default'} data-testid="broadcast-paused">
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertDescription>
            {broadcast.stop_reason === 'test_wave_complete'
              ? t('broadcasts.pausedTestWave', {
                  time: broadcast.resume_at ? formatter.format(new Date(broadcast.resume_at)) : '',
                })
              : broadcast.stop_reason === 'bounce_rate_high'
                ? t('broadcasts.pausedBounces', { bounced: counts.bounced })
                : broadcast.stop_reason === 'complaint_rate_high'
                  ? t('broadcasts.pausedComplaints', { complained: counts.complained })
                  : t('broadcasts.pausedByOwner')}
          </AlertDescription>
        </Alert>
      ) : null}
      {broadcast.test_percent > 0 ? (
        <p className="text-sm text-muted-foreground">
          {t('broadcasts.stagedSummary', {
            percent: broadcast.test_percent,
            minutes: broadcast.test_wait_minutes,
          })}{' '}
          {broadcast.status === 'sending'
            ? t(broadcast.current_wave === 1 ? 'broadcasts.waveTest' : 'broadcasts.waveRest')
            : null}
        </p>
      ) : null}
      {broadcast.safety_override ? (
        <p className="text-sm text-warning">{t('broadcasts.overrideNote')}</p>
      ) : null}
      <dl
        data-testid="broadcast-stats"
        className="grid grid-cols-2 gap-3 sm:grid-cols-4 lg:grid-cols-5"
      >
        {stats.map(([label, value]) => (
          <div key={label} className="rounded-md border px-3 py-2">
            <dt className="text-xs text-muted-foreground">{label}</dt>
            <dd className="text-xl font-semibold tabular-nums">{value}</dd>
          </div>
        ))}
      </dl>
      {clean ? (
        <p className="text-sm text-muted-foreground">{t('broadcasts.noProblems')}</p>
      ) : null}
      {running ? <ProgressSummary broadcast={broadcast} /> : null}
      <Progress broadcast={broadcast} />
      <RecipientTable broadcastId={broadcast.id} counts={counts} live={running} />
      {confirm === 'cancel' ? (
        <ConfirmDialog
          title={t('broadcasts.cancelTitle')}
          description={t('broadcasts.cancelDescription')}
          confirmLabel={t('broadcasts.cancel')}
          cancelLabel={t('broadcasts.keep')}
          destructive
          pending={cancel.isPending}
          onCancel={() => setConfirm(null)}
          onConfirm={() => void doCancel()}
        />
      ) : null}
      {confirm === 'resume' ? (
        <ConfirmDialog
          title={t('broadcasts.resumeTitle')}
          description={t('broadcasts.resumeDescription')}
          confirmLabel={t('broadcasts.resume')}
          destructive
          pending={resume.isPending}
          onCancel={() => setConfirm(null)}
          onConfirm={() => void doResume()}
        />
      ) : null}
      {confirm === 'delete' ? (
        <ConfirmDialog
          title={t('broadcasts.deleteTitle')}
          description={t('broadcasts.deleteDescription', { name: broadcast.name })}
          confirmLabel={t('actions.delete')}
          destructive
          pending={remove.isPending}
          onCancel={() => setConfirm(null)}
          onConfirm={() => void doDelete()}
        />
      ) : null}
    </div>
  );
}

/** Send rate and estimated finish of a running broadcast, as the API estimates them. */
function ProgressSummary({ broadcast }: { broadcast: MailBroadcast }) {
  const t = useT('mailbox');
  const locale = useLocale();
  const formatter = useDateTimeFormat();
  const relative = useRelativeTimeFormat();
  const { progress, status } = broadcast;
  const rate = progress?.rate_per_minute;
  const finish = progress?.estimated_finish_at;
  const waitingToStart =
    status === 'scheduled' || (status === 'paused' && broadcast.stop_reason === 'test_wave_complete');
  const eta = finish
    ? t('broadcasts.etaValue', {
        time: formatter.format(new Date(finish)),
        relative: relativeTime(
          relative,
          new Date(finish).getTime(),
          new Date(progress.computed_at).getTime()
        ),
      })
    : status === 'paused' && !waitingToStart
      ? t('broadcasts.etaPaused')
      : waitingToStart
        ? t('broadcasts.etaNotStarted')
        : t('broadcasts.etaMeasuring');
  return (
    <section className="space-y-2" aria-label={t('broadcasts.etaLabel')}>
      <dl className="grid gap-3 sm:grid-cols-2">
        <div className="rounded-md border px-3 py-2">
          <dt className="flex items-center gap-1.5 text-xs text-muted-foreground">
            <Gauge className="size-3.5" aria-hidden="true" />
            {t('broadcasts.rateLabel')}
          </dt>
          <dd className="text-base font-semibold tabular-nums" data-testid="broadcast-rate">
            {rate != null
              ? t('broadcasts.rateValue', {
                  rate: new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }).format(rate),
                })
              : status === 'sending'
                ? t('broadcasts.rateMeasuring')
                : '—'}
          </dd>
        </div>
        <div className="rounded-md border px-3 py-2">
          <dt className="flex items-center gap-1.5 text-xs text-muted-foreground">
            <Clock className="size-3.5" aria-hidden="true" />
            {t('broadcasts.etaLabel')}
          </dt>
          <dd className="text-base font-semibold tabular-nums" data-testid="broadcast-eta">
            {eta}
          </dd>
        </div>
      </dl>
      <p className="text-xs text-muted-foreground">
        {t('broadcasts.etaHint', { minutes: progress?.rate_window_minutes ?? 10 })}{' '}
        {t('broadcasts.noticeHint')}
      </p>
    </section>
  );
}
