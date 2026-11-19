import type { TransactionalLogEntry } from '@/features/developer/types';

/** The status chips of the send log, each with its own text so color is never the only signal. */
export const LOG_STATUSES = [
  'queued',
  'sent',
  'delivered',
  'bounced',
  'blocked',
  'failed',
  'test',
] as const;
export type LogStatus = (typeof LOG_STATUSES)[number];

/**
 * The chip of one entry: test-mode messages are `test`; otherwise the delivery event, when one is
 * recorded, refines `sent`. A complaint counts with bounces as a problem, as the log's `outcome`
 * filter does.
 */
export function logStatus(
  entry: Pick<TransactionalLogEntry, 'mode' | 'status' | 'last_event'>
): LogStatus {
  if (entry.mode === 'test') return 'test';
  switch (entry.status) {
    case 'queued':
      return 'queued';
    case 'failed':
      return 'failed';
    case 'suppressed':
      return 'blocked';
    case 'sent':
      break;
  }
  switch (entry.last_event) {
    case 'delivered':
      return 'delivered';
    case 'bounced':
    case 'complained':
      return 'bounced';
    case 'rejected':
      return 'blocked';
    case 'failed':
      return 'failed';
    default:
      return 'sent';
  }
}

/** Chip colors from the theme tokens; the label carries the meaning. */
export const LOG_STATUS_CLASSES: Record<LogStatus, string> = {
  queued: 'bg-muted text-muted-foreground',
  sent: 'bg-muted text-foreground',
  delivered: 'bg-success-subtle text-success',
  bounced: 'bg-error-subtle text-error',
  blocked: 'bg-warning-subtle text-warning',
  failed: 'bg-error-subtle text-error',
  test: 'bg-primary/10 text-primary',
};
