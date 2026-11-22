'use client';

import { useEffect, useSyncExternalStore } from 'react';
import { usePathname, useRouter, useSearchParams } from 'next/navigation';
import { toast } from 'sonner';

import { useOrganizations } from '@/features/organization/hooks/use-organizations';
import type { Organization } from '@/features/organization/types';
import { useT } from '@/i18n';

import {
  MAIL_ORGANIZATION_PARAM,
  ORGANIZATION_SCOPED_PARAMS,
} from '@/features/mailbox/components/mailbox-params';

export { MAIL_ORGANIZATION_PARAM };

const ACTIVE_ORGANIZATION_STORAGE_KEY = 'zmail.activeOrganizationId';

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

function readStoredOrganizationId(): number | null {
  try {
    const raw = window.sessionStorage.getItem(ACTIVE_ORGANIZATION_STORAGE_KEY) ?? '';
    return organizationIdPattern.test(raw) ? Number(raw) : null;
  } catch {
    return null;
  }
}

function writeStoredOrganizationId(organizationId: number): void {
  try {
    window.sessionStorage.setItem(ACTIVE_ORGANIZATION_STORAGE_KEY, String(organizationId));
  } catch {
    // Storage may be unavailable in restricted contexts; URL remains authoritative.
  }
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

/** Which organization the `org` parameter, or else the session's last choice, names. */
function useOrganizationChoice() {
  const searchParams = useSearchParams();
  const organizations = useOrganizations();
  const hydrated = useHydrated();

  const items = organizations.data?.items;
  const hasExplicitOrg = searchParams.has(MAIL_ORGANIZATION_PARAM);
  const raw = searchParams.get(MAIL_ORGANIZATION_PARAM) ?? '';
  const requestedId = organizationIdPattern.test(raw) ? Number(raw) : null;
  const storedId = !hasExplicitOrg && hydrated ? readStoredOrganizationId() : null;
  const candidateId = hasExplicitOrg ? requestedId : storedId;
  const selected = items?.find(item => item.id === candidateId) ?? items?.[0];
  return { organizations, hydrated, items, requestedId, selected };
}

/**
 * The organization a mail page would show, chosen like `useMailOrganization` but read only: it
 * never changes the URL or the stored choice, so pages outside the mail area can use it. Undefined
 * while loading or when the user belongs to no organization.
 */
export function useChosenMailOrganization(): Organization | undefined {
  const { hydrated, selected } = useOrganizationChoice();
  return hydrated ? selected : undefined;
}

/**
 * The organization of a mail page. The choice lives in the `org` query parameter, so a link or a
 * reload keeps it. Navigating via the sidebar (no `org` parameter) restores the last chosen
 * organization of the session; an unknown explicit `org` parameter falls back to the first one.
 * When that unknown organization is a real id (one the user left or was removed from), the
 * switch is said aloud and the ids of the old organization's mail (`open`, `box`, ...) are
 * dropped, so a stale link never opens anything in the new one.
 */
export function useMailOrganization(): MailOrganizationScope {
  const t = useT('mailbox');
  const router = useRouter();
  const pathname = usePathname();
  const { organizations, hydrated, items, requestedId, selected } = useOrganizationChoice();
  const selectedId = selected?.id;
  const selectedName = selected?.name ?? '';
  const lost =
    requestedId !== null && items !== undefined && !items.some(item => item.id === requestedId);

  useEffect(() => {
    if (selectedId === undefined) return;
    writeStoredOrganizationId(selectedId);
    if (selectedId === requestedId) return;
    const next = new URLSearchParams(window.location.search);
    if (next.get(MAIL_ORGANIZATION_PARAM) === String(selectedId)) return;
    next.set(MAIL_ORGANIZATION_PARAM, String(selectedId));
    if (lost) {
      for (const param of ORGANIZATION_SCOPED_PARAMS) next.delete(param);
      toast.error(t('organization.unavailable', { name: selectedName }), {
        id: 'mail-organization-unavailable',
      });
    }
    router.replace(`${pathname}?${next.toString()}`, { scroll: false });
  }, [lost, pathname, requestedId, router, selectedId, selectedName, t]);

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
    select: organizationId => {
      writeStoredOrganizationId(organizationId);
      router.replace(mailOrganizationHref(pathname, organizationId), { scroll: false });
    },
  };
}
