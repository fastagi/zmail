import type { Mailbox } from '@/features/mailbox/types';

/**
 * A mailbox holding read-only copies of a member's bound outside account: `origin: connected`, or
 * `type: external` from an API that predates the origin field.
 */
export function isConnectedMailbox(mailbox: Pick<Mailbox, 'type' | 'origin'>): boolean {
  return mailbox.origin === 'connected' || mailbox.type === 'external';
}
