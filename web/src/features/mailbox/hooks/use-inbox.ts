'use client';

import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
  type InfiniteData,
  type QueryClient,
} from '@tanstack/react-query';

import { isWebFeatureEnabled } from '@/config/features';
import { LOCAL_ERROR_HANDLING_META } from '@/config/query-meta';
import {
  invalidateMail,
  liveRefresh,
  MAIL_PAGE_SIZE,
  mailboxKeys,
  validId,
} from '@/features/mailbox/hooks/use-mailbox';
import { mailboxService } from '@/features/mailbox/services/mailbox-service';
import { useUserSettings } from '@/features/setting/hooks/use-settings';
import type {
  InboxFilters,
  InboxItem,
  InboxPage,
  UpdateMailMessagesInput,
} from '@/features/mailbox/types';

/**
 * The unified inbox, one cursor page at a time: each next page continues after the last one's
 * `meta.next_cursor`, and the list stays fresh while the tab is visible.
 */
export function useInboxMessages(organizationId: number, filters: InboxFilters) {
  return useInfiniteQuery({
    queryKey: mailboxKeys.inbox(organizationId, filters),
    queryFn: ({ pageParam }) =>
      mailboxService.listInbox(organizationId, filters, pageParam, MAIL_PAGE_SIZE),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: lastPage => lastPage.meta.next_cursor ?? undefined,
    enabled: validId(organizationId),
    staleTime: 15_000,
    ...liveRefresh,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/**
 * The caller's own counts of the unified inbox, with `tz` deciding today; fresh while the tab is
 * visible, like the list.
 */
export function useInboxCounts(organizationId: number, tz: string) {
  return useQuery({
    queryKey: mailboxKeys.inboxCounts(organizationId, tz),
    queryFn: () => mailboxService.inboxCounts(organizationId, tz),
    enabled: validId(organizationId),
    staleTime: 15_000,
    ...liveRefresh,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

const fallbackTimeZone = 'UTC';
const userTimeZoneKey = 'localization.timezone';

/** The browser's IANA zone; UTC when it reports none. */
function browserTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || fallbackTimeZone;
  } catch {
    return fallbackTimeZone;
  }
}

/**
 * The zone whose day `today` is: the user's `localization.timezone` setting where the setting
 * feature runs, else (and until the settings load) the browser's zone.
 */
export function useInboxTimezone(): string {
  const settingsEnabled = isWebFeatureEnabled('setting');
  const settings = useUserSettings({ enabled: settingsEnabled });
  const setting = settingsEnabled
    ? settings.data?.find(item => item.key === userTimeZoneKey)
    : undefined;
  const zone = typeof setting?.value === 'string' ? setting.value : '';
  return zone || browserTimeZone();
}

/**
 * One change for 1 to 100 messages of one mailbox from the unified inbox, where each row belongs to
 * a different mailbox, so the mailbox is a variable of the mutation rather than of the hook.
 */
export function useUpdateInboxMessages(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ mailboxId, ...input }: { mailboxId: number } & UpdateMailMessagesInput) =>
      mailboxService.updateMessages(organizationId, mailboxId, input),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: (_result, _error, { mailboxId }) =>
      invalidateMail(queryClient, organizationId, mailboxId),
  });
}

type InboxListing = InfiniteData<InboxPage, string | undefined>;

/** A row as it reads once opened: an inbound message read, a thread with nothing unread. */
function readRow(item: InboxItem): InboxItem {
  return {
    ...item,
    read: item.direction === 'inbound' ? true : item.read,
    thread: item.thread && { ...item.thread, unread: false },
  };
}

/**
 * Shows the row read in every cached unified inbox listing of the organization before the server
 * answers. Answers the function that puts those listings back as they were, for a failed request.
 */
export function markInboxRowRead(
  queryClient: QueryClient,
  organizationId: number,
  rowId: number
): () => void {
  const queryKey = mailboxKeys.inboxes(organizationId);
  const snapshot = queryClient.getQueriesData<InboxListing>({ queryKey });
  queryClient.setQueriesData<InboxListing>({ queryKey }, listing =>
    listing?.pages
      ? {
          ...listing,
          pages: listing.pages.map(page => ({
            ...page,
            items: page.items.map(item => (item.id === rowId ? readRow(item) : item)),
          })),
        }
      : listing
  );
  return () => {
    for (const [key, listing] of snapshot) queryClient.setQueryData(key, listing);
  };
}
