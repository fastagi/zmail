'use client';

import { useMemo, useState, type ReactNode } from 'react';
import { useLocale } from 'next-intl';
import { AlertCircle, Check, Copy, RefreshCw } from 'lucide-react';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import type { MailEventSummary, MailEventType, MailStatus } from '@/features/mailbox/types';
import { cn } from '@/utils';

/**
 * Blocks every network load inside the message frame. The frame's empty `sandbox` already stops
 * scripts and gives the content an opaque origin; this also stops remote images and stylesheets,
 * so opening a message tells the sender nothing.
 */
const frameContentPolicy =
  '<meta http-equiv="Content-Security-Policy" content="default-src \'none\'; img-src data:; style-src \'unsafe-inline\'">';

/**
 * The only place sender HTML is rendered. Sender HTML is untrusted: the empty sandbox runs no
 * script and gives the content an opaque origin, so it can never act as this application or read
 * its cookies. The frame has a fixed height that follows the window (70% of it, at most 40rem) and
 * scrolls inside: fitting it to the content would need the sandbox loosened (decided 2026-11-20).
 */
export function MailHtmlFrame({ html, title }: { html: string; title: string }) {
  return (
    <iframe
      sandbox=""
      srcDoc={frameContentPolicy + html}
      title={title}
      referrerPolicy="no-referrer"
      className="animate-fade h-[min(70vh,40rem)] w-full rounded-md border bg-white"
    />
  );
}

export function ErrorAlert({
  message,
  retryLabel,
  onRetry,
}: {
  message: string;
  retryLabel: string;
  onRetry: () => void;
}) {
  return (
    <Alert variant="destructive" className="animate-pop">
      <AlertCircle className="size-4" aria-hidden="true" />
      <AlertDescription className="flex flex-wrap items-center justify-between gap-3">
        <span>{message}</span>
        <Button
          type="button"
          size="sm"
          variant="outline"
          icon={<RefreshCw className="size-3.5" />}
          onClick={onRetry}
        >
          {retryLabel}
        </Button>
      </AlertDescription>
    </Alert>
  );
}

/**
 * A background refetch failed while earlier data is still on screen: the data stays, and this
 * quiet line says it may be out of date until the next poll succeeds.
 */
export function StaleNotice({ message, className }: { message: string; className?: string }) {
  return (
    <p
      role="status"
      className={cn('flex items-center gap-1.5 text-xs text-muted-foreground', className)}
    >
      <AlertCircle className="size-3.5" aria-hidden="true" />
      {message}
    </p>
  );
}

/**
 * An article reader while its mail loads, at the finished sizes (§11.0): the line above the
 * subject, the serif subject, the sender's avatar line, then the first lines of the body, so the
 * mail takes the same place when it arrives.
 */
export function ReaderArticleSkeleton({ className }: { className?: string }) {
  return (
    <div aria-hidden="true" data-reader-skeleton="" className={className}>
      <div>
        <Skeleton className="h-3 w-44" />
        <Skeleton className="mt-2 mb-[18px] h-[37px] w-3/4" />
      </div>
      <div className="space-y-4">
        <div className="flex items-center gap-3">
          <Skeleton className="size-[34px] shrink-0 rounded-full" />
          <div className="flex-1 space-y-1.5">
            <Skeleton className="h-3.5 w-32" />
            <Skeleton className="h-3 w-48" />
          </div>
        </div>
        <div className="space-y-3">
          <Skeleton className="h-3.5 w-full" />
          <Skeleton className="h-3.5 w-full" />
          <Skeleton className="h-3.5 w-11/12" />
          <Skeleton className="h-3.5 w-2/3" />
        </div>
      </div>
    </div>
  );
}

export function EmptyState({
  icon,
  title,
  description,
  action,
}: {
  icon: ReactNode;
  title: string;
  description?: string;
  action?: ReactNode;
}) {
  return (
    <div className="animate-enter flex flex-col items-center rounded-md border border-dashed px-6 py-10 text-center">
      <div className="flex size-10 items-center justify-center rounded-full bg-muted text-muted-foreground transition-transform duration-200 hover:scale-105">
        {icon}
      </div>
      <p className="mt-3 text-sm font-medium">{title}</p>
      {description ? (
        <p className="mt-1 max-w-sm text-sm text-muted-foreground">{description}</p>
      ) : null}
      {action ? <div className="mt-4">{action}</div> : null}
    </div>
  );
}

/** `zone` when this browser knows it, else undefined (the browser's own zone). */
export function knownTimeZone(zone: string | undefined): string | undefined {
  if (!zone) return undefined;
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: zone });
    return zone;
  } catch {
    return undefined;
  }
}

/** Date and time in `timeZone` when given and known, else in the browser's zone. */
export function useDateTimeFormat(timeZone?: string): Intl.DateTimeFormat {
  const locale = useLocale();
  return useMemo(
    () =>
      new Intl.DateTimeFormat(locale, {
        dateStyle: 'medium',
        timeStyle: 'short',
        timeZone: knownTimeZone(timeZone),
      }),
    [locale, timeZone]
  );
}

/** Native select styled like the text inputs; it keeps the platform's keyboard behavior. */
export const nativeSelectClassName =
  'h-9 min-w-0 rounded-md border border-input bg-transparent px-3 text-sm shadow-xs focus-border disabled:cursor-not-allowed disabled:opacity-50 dark:bg-input/30';

export function useRelativeTimeFormat(): Intl.RelativeTimeFormat {
  const locale = useLocale();
  return useMemo(
    () => new Intl.RelativeTimeFormat(locale, { numeric: 'auto', style: 'short' }),
    [locale]
  );
}

const relativeUnits: readonly [Intl.RelativeTimeFormatUnit, number][] = [
  ['day', 86_400_000],
  ['hour', 3_600_000],
  ['minute', 60_000],
];

/** "5 min. ago", "2 days ago": the largest whole unit, down to minutes. */
export function relativeTime(format: Intl.RelativeTimeFormat, then: number, now: number): string {
  const elapsed = then - now;
  for (const [unit, size] of relativeUnits) {
    if (Math.abs(elapsed) >= size) return format.format(Math.trunc(elapsed / size), unit);
  }
  return format.format(0, 'minute');
}

/** Translation key of each message status, in the `mailbox` namespace. */
export const mailStatusKey = {
  received: 'emails.statusReceived',
  queued: 'emails.statusQueued',
  sent: 'emails.statusSent',
  failed: 'emails.statusFailed',
} as const satisfies Record<MailStatus, string>;

/** Translation key of each delivery event, in the `mailbox` namespace. */
export const mailEventKey = {
  accepted: 'emails.eventAccepted',
  delivered: 'emails.eventDelivered',
  deferred: 'emails.eventDeferred',
  bounced: 'emails.eventBounced',
  failed: 'emails.eventFailed',
  rejected: 'emails.eventRejected',
  complained: 'emails.eventComplained',
} as const satisfies Record<MailEventType, string>;

interface MailDeliveryState {
  status: MailStatus;
  last_event: MailEventSummary;
  queued_reason?: 'send_limit' | 'scheduled' | null;
}

/**
 * The delivery event a message's badge shows in place of its status, or null to show the status.
 * A message the provider never took stays `failed`, whatever events it carries.
 */
export function shownMailEvent(message: MailDeliveryState): MailEventType | null {
  if (message.status === 'failed' || message.last_event === '') return null;
  return message.last_event;
}

/** Translation key of the label `MailStatusBadge` needs for a message. */
export function mailBadgeKey(message: MailDeliveryState) {
  const event = shownMailEvent(message);
  if (event) return mailEventKey[event];
  // Waiting for the send limit or a scheduled time reads differently from an in-flight queue.
  if (message.status === 'queued' && message.queued_reason === 'send_limit')
    return 'emails.statusWaiting';
  if (message.status === 'queued' && message.queued_reason === 'scheduled')
    return 'emails.statusScheduled';
  return mailStatusKey[message.status];
}

const statusStyles: Record<MailStatus, string> = {
  received: 'bg-info-subtle text-info',
  sent: 'bg-success-subtle text-success',
  queued: 'bg-warning-subtle text-warning',
  failed: 'bg-error-subtle text-error',
};

const eventStyles: Record<MailEventType, string> = {
  accepted: 'bg-muted text-muted-foreground',
  delivered: 'bg-success-subtle text-success',
  deferred: 'bg-warning-subtle text-warning',
  bounced: 'bg-error-subtle text-error',
  failed: 'bg-error-subtle text-error',
  rejected: 'bg-error-subtle text-error',
  complained: 'bg-error-subtle text-error',
};

/**
 * Colored status of a message: its delivery summary once events are recorded, otherwise its
 * status. The label comes from the caller (`mailBadgeKey`) so this file needs no namespace.
 */
export function MailStatusBadge({ message, label }: { message: MailDeliveryState; label: string }) {
  const event = shownMailEvent(message);
  return (
    <Badge
      variant="outline"
      data-status={message.status}
      data-event={event ?? undefined}
      className={cn(
        'border-transparent',
        event ? eventStyles[event] : statusStyles[message.status]
      )}
    >
      {label}
    </Badge>
  );
}

/** One delivery event, colored like the badge that summarizes it. */
export function MailEventBadge({ type, label }: { type: MailEventType; label: string }) {
  return (
    <Badge
      variant="outline"
      data-event={type}
      className={cn('border-transparent', eventStyles[type])}
    >
      {label}
    </Badge>
  );
}

/** Copies a value such as a DNS record or a name server; the label names what it copies. */
export function CopyButton({
  value,
  label,
  copiedLabel,
}: {
  value: string;
  label: string;
  copiedLabel: string;
}) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1_500);
    } catch {
      setCopied(false);
    }
  };
  return (
    <Button
      type="button"
      isIcon
      size="sm"
      variant="ghost"
      className="size-6 shrink-0"
      aria-label={copied ? copiedLabel : label}
      title={copied ? copiedLabel : label}
      onClick={() => void copy()}
    >
      {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
    </Button>
  );
}
