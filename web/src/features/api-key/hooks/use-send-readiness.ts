'use client';

import { isWebFeatureEnabled } from '@/config/features';
import { useCurrentUser } from '@/features/auth/store/auth-store';
import { useChosenMailOrganization } from '@/features/mailbox/hooks/use-mail-organization';
import { useMailboxes } from '@/features/mailbox/hooks/use-mailbox';
import type { Mailbox } from '@/features/mailbox/types';
import { useOrganizations } from '@/features/organization/hooks/use-organizations';

export interface SendReadinessOrganization {
  id: number;
  name: string;
  /** Owners and admins create mailboxes; members ask them. */
  canManage: boolean;
}

/** What a developer still needs before a key can send mail. */
export type SendReadiness =
  | { status: 'unavailable' }
  | { status: 'loading' }
  | { status: 'error' }
  | { status: 'no-organization' }
  | { status: 'no-mailbox'; organization: SendReadinessOrganization }
  | {
      status: 'ready';
      organization: SendReadinessOrganization;
      /** Addresses the user may send from in the organization, the first being the default. */
      senders: string[];
      /** The user's own address: the first message lands in their own inbox. */
      recipient: string;
    };

/** A mailbox `POST /v1/outbound-mail-messages` can send from as this user. */
export function canSendFrom(mailbox: Mailbox): boolean {
  return mailbox.access.send && mailbox.origin !== 'connected' && !mailbox.awaiting_handover;
}

/**
 * Whether the user has an organization and a mailbox there they may send from, which a key needs
 * before it can send anything. It uses the organization the mail pages last showed, or
 * `organizationId` for a key bound to one organization.
 */
export function useSendReadiness(organizationId?: number): SendReadiness {
  const enabled = isWebFeatureEnabled('mailbox');
  const organizations = useOrganizations(enabled);
  const lastShown = useChosenMailOrganization();
  const bound = organizationId
    ? organizations.data?.items.find(item => item.id === organizationId)
    : undefined;
  const chosen = organizationId ? bound : lastShown;
  const mailboxes = useMailboxes(chosen?.id ?? 0);
  const user = useCurrentUser();

  if (!enabled) return { status: 'unavailable' };
  if (organizations.isError) return { status: 'error' };
  if (organizations.isPending) return { status: 'loading' };
  if (organizations.data.items.length === 0) return { status: 'no-organization' };
  // A key bound to an organization the user has left has nothing to show.
  if (organizationId && !bound) return { status: 'error' };
  if (!chosen) return { status: 'loading' };

  const organization = {
    id: chosen.id,
    name: chosen.name,
    canManage: chosen.role === 'owner' || chosen.role === 'admin',
  };
  if (mailboxes.isError) return { status: 'error' };
  if (mailboxes.isPending) return { status: 'loading' };
  const senders = mailboxes.data.filter(canSendFrom).map(mailbox => mailbox.address);
  if (senders.length === 0) return { status: 'no-mailbox', organization };
  return { status: 'ready', organization, senders, recipient: user?.email ?? senders[0] };
}
