'use client';

import { isWebFeatureEnabled } from '@/config/features';
import { useMailboxDomains } from '@/features/mailbox/hooks/use-mailbox';
import { useOrganizations } from '@/features/organization/hooks/use-organizations';

export interface RestrictionOrganization {
  id: number;
  name: string;
}

/** Organizations a new key may be limited to: the ones the user belongs to. */
export type RestrictionOrganizations =
  | { status: 'unavailable' | 'loading' | 'error' }
  | { status: 'ready'; organizations: RestrictionOrganization[] };

/** Domains a key limited to one organization may send from. */
export type RestrictionDomains =
  { status: 'unavailable' | 'loading' | 'error' } | { status: 'ready'; domains: string[] };

export function useRestrictionOrganizations(): RestrictionOrganizations {
  const enabled = isWebFeatureEnabled('organization');
  const organizations = useOrganizations(enabled);
  if (!enabled) return { status: 'unavailable' };
  if (organizations.isError) return { status: 'error' };
  if (organizations.isPending) return { status: 'loading' };
  return {
    status: 'ready',
    organizations: organizations.data.items.map(item => ({ id: item.id, name: item.name })),
  };
}

/**
 * The shared domain and the organization's verified domains, which the API accepts as sending
 * domains of a key limited to that organization. Nothing loads until an organization is chosen.
 */
export function useRestrictionDomains(organizationId: number): RestrictionDomains {
  const enabled = isWebFeatureEnabled('mailbox');
  const domains = useMailboxDomains(enabled ? organizationId : 0);
  if (!enabled || organizationId <= 0) return { status: 'unavailable' };
  if (domains.isError) return { status: 'error' };
  if (domains.isPending) return { status: 'loading' };
  return {
    status: 'ready',
    domains: [...new Set(domains.data.map(item => item.domain.toLowerCase()))],
  };
}
