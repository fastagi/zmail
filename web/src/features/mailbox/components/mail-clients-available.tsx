'use client';

import type { ReactNode } from 'react';

import { useMailAppPasswords } from '@/features/mailbox/hooks/use-mail-app-passwords';
import { useChosenMailOrganization } from '@/features/mailbox/hooks/use-mail-organization';

/**
 * Shows its children only on a server that runs the mail gateway. Until then the settings would
 * point members at a server they cannot reach, so the tab stays hidden instead. Other reasons
 * (switched off for the organization, not granted) still show it, because the tab explains them.
 */
export function MailClientsAvailable({ children }: { children: ReactNode }) {
  const organization = useChosenMailOrganization();
  const passwords = useMailAppPasswords(organization?.id);
  const access = passwords.data?.access;
  if (access === undefined || access.reason === 'unavailable') return null;
  return <>{children}</>;
}
