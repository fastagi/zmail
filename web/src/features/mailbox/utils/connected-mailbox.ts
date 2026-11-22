import type { Mailbox } from '@/features/mailbox/types';

/** A mailbox holding read-only copies of a bound outside account (`origin: connected`). */
export function isConnectedMailbox(mailbox: Pick<Mailbox, 'origin'>): boolean {
  return mailbox.origin === 'connected';
}

/**
 * The viewer's own connected account: a connected personal mailbox, which the API lists for its
 * owner alone, so `view` on it means the viewer owns it.
 */
export function isOwnConnectedMailbox(
  mailbox: Pick<Mailbox, 'type' | 'origin' | 'access'>
): boolean {
  return isConnectedMailbox(mailbox) && mailbox.type === 'personal' && mailbox.access.view;
}

/**
 * Whether mail may be written from `mailbox` in Zmail: its `send` access. The API grants it on a
 * connected mailbox only when its account was bound with an SMTP server, which then sends it.
 */
export function canComposeFrom(mailbox: Pick<Mailbox, 'origin' | 'access'>): boolean {
  return mailbox.access.send;
}
