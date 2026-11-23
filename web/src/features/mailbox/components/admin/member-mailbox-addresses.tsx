'use client';

import { AtSign } from 'lucide-react';

import { useMailboxes } from '@/features/mailbox/hooks/use-mailbox';
import type { Mailbox } from '@/features/mailbox/types';
import { useT } from '@/i18n';

/**
 * The personal Zmail mailbox addresses a member owns in the organization, for a row of the Members
 * tab. Reads the mailbox list the caller already loads; shows nothing while it loads, when it
 * fails, or when the member owns none.
 */
export function MemberMailboxAddresses({
  organizationId,
  userId,
}: {
  organizationId: number;
  userId: number;
}) {
  const t = useT('mailbox');
  const query = useMailboxes(organizationId);
  const addresses = ownedPersonalAddresses(query.data ?? [], userId);
  const [first, ...rest] = addresses;
  if (!first) return null;
  return (
    <p
      className="flex min-w-0 items-center gap-1 text-xs text-muted-foreground"
      title={addresses.join(', ')}
    >
      <AtSign className="size-3 shrink-0" aria-hidden="true" />
      <span className="sr-only">{t('admin.memberMailboxes.label')}: </span>
      <span className="min-w-0 truncate">{first}</span>
      {rest.length > 0 ? (
        <span className="shrink-0">{t('admin.memberMailboxes.more', { count: rest.length })}</span>
      ) : null}
    </p>
  );
}

/** A member's own Zmail personal mailboxes, oldest first; connected accounts stay private. */
export function ownedPersonalAddresses(mailboxes: Mailbox[], userId: number): string[] {
  return mailboxes
    .filter(
      mailbox =>
        mailbox.type === 'personal' &&
        mailbox.owner_user_id === userId &&
        mailbox.origin !== 'connected' &&
        !mailbox.awaiting_handover
    )
    .map(mailbox => mailbox.address);
}
