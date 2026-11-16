'use client';

import { useEffect, useSyncExternalStore } from 'react';
import { usePathname, useRouter, useSearchParams } from 'next/navigation';

import { useOrganizations } from '@/features/organization/hooks/use-organizations';
import type { Organization } from '@/features/organization/types';

/** Query parameter of the mail pages that names the organization they show. */
export const MAIL_ORGANIZATION_PARAM = 'org';

const organizationIdPattern = /^[1-9]\d{0,15}$/u;

/** The organization the mail pages act in, with what the member may do there. */
export interface MailOrganization {
  id: number;
  name: string;
  /** Owners and admins create and delete mailboxes; the API remains the authority. */
  canManage: boolean;
}

export type MailOrganizationScope =
  | { status: 'loading' }
  | { status: 'error'; retry: () => void }
  /** The user belongs to no organization, so there is no mail to show. */
  | { status: 'empty' }
  | {
      status: 'ready';
      organization: MailOrganization;
      organizations: readonly Organization[];
      select: (organizationId: number) => void;
    };

/** A mail page path that keeps the organization, for links between the mail pages. */
export function mailOrganizationHref(path: string, organizationId: number): string {
  return `${path}?${MAIL_ORGANIZATION_PARAM}=${organizationId}`;
}

const subscribeToNothing = () => () => {};

/**
 * False on the server and while hydrating, true afterwards. The console header loads the
 * organization list too, so by the time a mail page hydrates the list can already be cached;
 * rendering it then would not match the server's loading markup.
 */
function useHydrated(): boolean {
  return useSyncExternalStore(
    subscribeToNothing,
    () => true,
    () => false
  );
}

/**
 * The organization of a mail page. The choice lives in the `org` query parameter, so a link or a
 * reload keeps it. A missing value, or one that is not an organization of the user, falls back
 * to the first organization and is written into the URL.
 */
export function useMailOrganization(): MailOrganizationScope {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const organizations = useOrganizations();
  const hydrated = useHydrated();

  const items = organizations.data?.items;
  const raw = searchParams.get(MAIL_ORGANIZATION_PARAM) ?? '';
  const requestedId = organizationIdPattern.test(raw) ? Number(raw) : null;
  const selected = items?.find(item => item.id === requestedId) ?? items?.[0];
  const selectedId = selected?.id;

  useEffect(() => {
    if (selectedId === undefined || selectedId === requestedId) return;
    // The live query, not the rendered one: the page may have written its own parameters, or
    // this organization, since the render. Everything else in the query is kept.
    const next = new URLSearchParams(window.location.search);
    if (next.get(MAIL_ORGANIZATION_PARAM) === String(selectedId)) return;
    next.set(MAIL_ORGANIZATION_PARAM, String(selectedId));
    router.replace(`${pathname}?${next.toString()}`, { scroll: false });
  }, [pathname, requestedId, router, selectedId]);

  if (!hydrated || organizations.isPending) return { status: 'loading' };
  if (organizations.error || !items) {
    return { status: 'error', retry: () => void organizations.refetch() };
  }
  if (!selected) return { status: 'empty' };
  return {
    status: 'ready',
    organization: {
      id: selected.id,
      name: selected.name,
      canManage: selected.role === 'owner' || selected.role === 'admin',
    },
    organizations: items,
    // Filters and pages belong to one organization, so switching starts from a clean query.
    select: organizationId =>
      router.replace(mailOrganizationHref(pathname, organizationId), { scroll: false }),
  };
}
