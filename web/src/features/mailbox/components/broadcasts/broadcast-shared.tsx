'use client';

import { ArrowLeft } from 'lucide-react';

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
import { useDateTimeFormat } from '@/features/mailbox/components/mail-shared';
import type {
  MailBroadcast,
  MailBroadcastStatus,
  MailRecipientStatus,
} from '@/features/mailbox/types';
import type { MailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

export const statusKey = {
  draft: 'broadcasts.statusDraft',
  scheduled: 'broadcasts.statusScheduled',
  sending: 'broadcasts.statusSending',
  paused: 'broadcasts.statusPaused',
  sent: 'broadcasts.statusSent',
  canceled: 'broadcasts.statusCanceled',
} as const satisfies Record<MailBroadcastStatus, string>;

export const recipientKey = {
  pending: 'broadcasts.pending',
  sent: 'broadcasts.sent',
  failed: 'broadcasts.failed',
  skipped: 'broadcasts.skipped',
  canceled: 'broadcasts.canceledCount',
} as const satisfies Record<MailRecipientStatus, string>;

export const recipientStatuses: readonly MailRecipientStatus[] = [
  'pending',
  'sent',
  'failed',
  'skipped',
  'canceled',
];

/**
 * Codes the API reports for failed and skipped recipients, in plain words. Any other code is a
 * provider's own refusal: it shows as "Not sent" with the code beside it for support.
 */
export const reasonKey = {
  contact_not_subscribed: 'broadcasts.reasonNotSubscribed',
  contact_deleted: 'broadcasts.reasonDeleted',
  contact_suppressed: 'broadcasts.reasonSuppressedList',
  'template.variables_missing': 'broadcasts.reasonVariables',
  mailbox_not_found: 'broadcasts.reasonMailbox',
  message_invalid: 'broadcasts.reasonMessage',
  'transport.permanent_bounce': 'broadcasts.reasonBounce',
  'transport.suppressed': 'broadcasts.reasonSuppressed',
  'transport.unreachable': 'broadcasts.reasonUnreachable',
  'transport.unknown_outcome': 'broadcasts.reasonUnreachable',
  'transport.invalid_response': 'broadcasts.reasonProviderResponse',
  'transport.response_too_large': 'broadcasts.reasonProviderResponse',
  'transport.not_configured': 'broadcasts.reasonUnavailable',
  'transport.unavailable': 'broadcasts.reasonUnavailable',
  'provider.unavailable': 'broadcasts.reasonUnavailable',
  'send.sender_unverified': 'broadcasts.reasonUnverified',
  'send.canceled': 'broadcasts.reasonCanceled',
  'send.expired': 'broadcasts.reasonExpired',
} as const;

export function knownReason(code: string): code is keyof typeof reasonKey {
  return Object.hasOwn(reasonKey, code);
}

/** Why a broadcast is paused, with readable copy. */
export const pauseKey = {
  test_wave_complete: 'broadcasts.pausedTestWave',
  bounce_rate_high: 'broadcasts.pausedBounces',
  complaint_rate_high: 'broadcasts.pausedComplaints',
  paused_by_owner: 'broadcasts.pausedByOwner',
} as const;

export function knownPause(code: string): code is keyof typeof pauseKey {
  return Object.hasOwn(pauseKey, code);
}

/** The domain part of an address, which groups mailboxes in the sender picker. */
export function domainOf(address: string): string {
  return address.slice(address.lastIndexOf('@') + 1);
}

/** The API schedules a broadcast at most a year (365 days) ahead (contracts/MAILBOXES.md). */
export const maxScheduleAheadMs = 365 * 24 * 60 * 60 * 1_000;

export type ScheduleErrorKey = 'broadcasts.schedulePast' | 'broadcasts.scheduleTooFar';
export type EditorErrorKey = MailboxErrorKey | ScheduleErrorKey;

/** A `datetime-local` value for an instant, to the minute, in the viewer's time zone. */
export function localDateTimeValue(timestamp: number): string {
  const at = new Date(timestamp);
  at.setSeconds(0, 0);
  const local = new Date(at.getTime() - at.getTimezoneOffset() * 60_000);
  return local.toISOString().slice(0, 16);
}

/** A `datetime-local` value for a time an hour from now, in the viewer's time zone. */
export function defaultScheduleValue(): string {
  return localDateTimeValue(Date.now() + 60 * 60 * 1_000);
}

/** Why a `datetime-local` value cannot be scheduled, or null when it can. */
export function scheduleProblem(value: string): ScheduleErrorKey | null {
  const timestamp = new Date(value).getTime();
  const now = Date.now();
  if (Number.isNaN(timestamp) || timestamp <= now) return 'broadcasts.schedulePast';
  if (timestamp - now > maxScheduleAheadMs) return 'broadcasts.scheduleTooFar';
  return null;
}

/** Viewer time zone name for scheduling copy, falling back safely to UTC. */
export function viewerTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
  } catch {
    return 'UTC';
  }
}

export function StatusBadge({
  broadcast,
}: {
  broadcast: Pick<MailBroadcast, 'status' | 'stop_reason'>;
}) {
  const t = useT('mailbox');
  const { status } = broadcast;
  // A test wave waiting out its observation window continues on its own; it is not a pause.
  const waiting = status === 'paused' && broadcast.stop_reason === 'test_wave_complete';
  return (
    <Badge
      variant={
        status === 'sending' || status === 'scheduled' || waiting
          ? 'default'
          : status === 'paused'
            ? 'outline'
            : 'secondary'
      }
      className="gap-1.5"
    >
      {status === 'sending' ? (
        <span className="size-1.5 rounded-full bg-current animate-pulse" aria-hidden="true" />
      ) : null}
      {t(waiting ? 'broadcasts.statusWaitingTestWave' : statusKey[status])}
    </Badge>
  );
}

export function Progress({ broadcast }: { broadcast: MailBroadcast }) {
  const t = useT('mailbox');
  const { counts } = broadcast;
  if (counts.total === 0) return <span className="text-muted-foreground">-</span>;
  const done = counts.total - counts.pending;
  return (
    <div className="min-w-32 space-y-1">
      <div
        className="h-1.5 overflow-hidden rounded-full bg-muted"
        role="progressbar"
        aria-valuemin={0}
        aria-valuemax={counts.total}
        aria-valuenow={done}
        aria-label={t('broadcasts.progress')}
      >
        <div
          className="h-full bg-primary transition-all duration-500 ease-out"
          style={{ width: `${(done / counts.total) * 100}%` }}
        />
      </div>
      <span className="text-xs text-muted-foreground">
        {t('broadcasts.progressValue', { sent: counts.sent, total: counts.total })}
        {counts.failed > 0 ? (
          <span className="text-error">
            {' · '}
            {t('broadcasts.progressFailed', { failed: counts.failed })}
          </span>
        ) : null}
      </span>
    </div>
  );
}

export function useWhen() {
  const t = useT('mailbox');
  const formatter = useDateTimeFormat();
  return (broadcast: MailBroadcast): string => {
    const time = (value: string) => formatter.format(new Date(value));
    if (broadcast.finished_at) {
      const ended =
        broadcast.status === 'canceled' ? 'broadcasts.canceledAt' : 'broadcasts.finishedAt';
      return t(ended, { time: time(broadcast.finished_at) });
    }
    if (broadcast.status === 'scheduled' && broadcast.scheduled_at) {
      return t('broadcasts.scheduledFor', { time: time(broadcast.scheduled_at) });
    }
    if (broadcast.started_at)
      return t('broadcasts.startedAt', { time: time(broadcast.started_at) });
    return t('broadcasts.createdAt', { time: time(broadcast.created_at) });
  };
}

export function BackButton({ onBack }: { onBack: () => void }) {
  const t = useT('mailbox');
  return (
    <Button
      type="button"
      size="sm"
      variant="ghost"
      icon={<ArrowLeft className="size-4" />}
      onClick={onBack}
    >
      {t('broadcasts.back')}
    </Button>
  );
}

export function ConfirmDialog({
  title,
  description,
  confirmLabel,
  cancelLabel,
  destructive = false,
  pending,
  onCancel,
  onConfirm,
}: {
  title: string;
  description: string;
  confirmLabel: string;
  cancelLabel?: string;
  destructive?: boolean;
  pending: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const t = useT('mailbox');
  return (
    <Dialog open onOpenChange={open => !open && !pending && onCancel()}>
      <DialogContent size="sm">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <DialogBody />
        <DialogFooter>
          <Button type="button" variant="outline" disabled={pending} onClick={onCancel}>
            {cancelLabel ?? t('actions.cancel')}
          </Button>
          <Button
            type="button"
            variant={destructive ? 'destructive' : 'default'}
            loading={pending}
            onClick={onConfirm}
          >
            {confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
