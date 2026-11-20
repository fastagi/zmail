'use client';

import { useOrganizationPeople, useUserNames } from '@/features/mailbox/components/mailbox-people';
import { useMailRoles } from '@/features/mailbox/hooks/use-inbox';
import { useMailboxes } from '@/features/mailbox/hooks/use-mailbox';
import type { Mailbox, MailRole } from '@/features/mailbox/types';

/**
 * What the ledger's ids point at: the organization's mail roles, the mailboxes the caller sees, and
 * its members. Each list may still be loading; a reference outside them is named by its id.
 */
export function usePlatformAccountLookups(organizationId: number) {
  const roles = useMailRoles(organizationId);
  const mailboxes = useMailboxes(organizationId);
  const people = useOrganizationPeople(organizationId, true);
  const nameOf = useUserNames(people.items);
  const roleList: MailRole[] = roles.data ?? [];
  const mailboxList: Mailbox[] = mailboxes.data ?? [];
  const roleNames = new Map(roleList.map(role => [role.id, role.name]));
  const mailboxAddresses = new Map(mailboxList.map(mailbox => [mailbox.id, mailbox.address]));
  return {
    roles: roleList,
    mailboxes: mailboxList,
    people: people.items,
    nameOf,
    roleName: (roleId: number) => roleNames.get(roleId),
    mailboxAddress: (mailboxId: number) => mailboxAddresses.get(mailboxId),
  };
}

export type PlatformAccountLookups = ReturnType<typeof usePlatformAccountLookups>;
