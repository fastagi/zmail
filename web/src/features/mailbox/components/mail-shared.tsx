'use client';

import { useMemo, type ReactNode } from 'react';
import { useLocale } from 'next-intl';
import { AlertCircle, RefreshCw } from 'lucide-react';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import type { MailStatus } from '@/features/mailbox/types';
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
 * its cookies.
 */
export function MailHtmlFrame({ html, title }: { html: string; title: string }) {
  return (
    <iframe
      sandbox=""
      srcDoc={frameContentPolicy + html}
      title={title}
      referrerPolicy="no-referrer"
      className="h-96 w-full rounded-md border bg-white"
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
    <Alert variant="destructive">
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

export function EmptyState({
  icon,
  title,
  description,
  action,
}: {
  icon: ReactNode;
  title: string;
  description: string;
  action?: ReactNode;
}) {
  return (
    <div className="flex flex-col items-center rounded-md border border-dashed px-6 py-10 text-center">
      <div className="flex size-10 items-center justify-center rounded-full bg-muted text-muted-foreground">
        {icon}
      </div>
      <p className="mt-3 text-sm font-medium">{title}</p>
      <p className="mt-1 max-w-sm text-sm text-muted-foreground">{description}</p>
      {action ? <div className="mt-4">{action}</div> : null}
    </div>
  );
}

export function useDateTimeFormat(): Intl.DateTimeFormat {
  const locale = useLocale();
  return useMemo(
    () => new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }),
    [locale]
  );
}

/** Native select styled like the text inputs; it keeps the platform's keyboard behavior. */
export const nativeSelectClassName =
  'h-9 min-w-0 rounded-md border border-input bg-transparent px-3 text-sm shadow-xs focus-border disabled:cursor-not-allowed disabled:opacity-50 dark:bg-input/30';

/** Translation key of each delivery status, in the `mailbox` namespace. */
export const mailStatusKey = {
  received: 'emails.statusReceived',
  queued: 'emails.statusQueued',
  sent: 'emails.statusSent',
  failed: 'emails.statusFailed',
} as const satisfies Record<MailStatus, string>;

const statusStyles: Record<MailStatus, string> = {
  received: 'bg-info-subtle text-info',
  sent: 'bg-success-subtle text-success',
  queued: 'bg-warning-subtle text-warning',
  failed: 'bg-error-subtle text-error',
};

/** Colored delivery status. The label comes from the caller so this file needs no namespace. */
export function MailStatusBadge({ status, label }: { status: MailStatus; label: string }) {
  return (
    <Badge
      variant="outline"
      data-status={status}
      className={cn('border-transparent', statusStyles[status])}
    >
      {label}
    </Badge>
  );
}
