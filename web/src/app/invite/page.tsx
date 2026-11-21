'use client';

import { useEffect } from 'react';
import { useRouter } from 'next/navigation';

import { ROUTES } from '@/constants/routes';
import {
  readInvitationFragment,
  stashPendingInvitation,
} from '@/features/organization/utils/pending-invitation';

/**
 * Where an invitation mail links: `/invite#<token>`. The token stays out of every URL a server
 * sees; it is kept in this tab while the invitee signs in or signs up, then the organization page
 * offers it in the accept dialog.
 */
export default function InvitePage() {
  const router = useRouter();

  useEffect(() => {
    const token = readInvitationFragment(window.location.hash);
    if (token !== null) stashPendingInvitation(token);
    // Drop the fragment from history before moving on.
    window.history.replaceState(null, '', window.location.pathname);
    router.replace(`${ROUTES.CONSOLE.ORGANIZATIONS}?accept=1`);
  }, [router]);

  return null;
}
