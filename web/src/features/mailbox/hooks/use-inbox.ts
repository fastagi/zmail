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
  MailRoleInput,
  UpdateMailMessagesInput,
} from '@/features/mailbox/types';

/** `enabled: false` holds a query back, such as until the inbox's zone is known. */
interface InboxQueryOptions {
  enabled?: boolean;
}

/**
 * The unified inbox, one cursor page at a time: each next page continues after the last one's
 * `meta.next_cursor`, and the list stays fresh while the tab is visible.
 */
export function useInboxMessages(
  organizationId: number,
  filters: InboxFilters,
  { enabled = true }: InboxQueryOptions = {}
) {
  return useInfiniteQuery({
    queryKey: mailboxKeys.inbox(organizationId, filters),
    queryFn: ({ pageParam }) =>
      mailboxService.listInbox(organizationId, filters, pageParam, MAIL_PAGE_SIZE),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: lastPage => lastPage.meta.next_cursor ?? undefined,
    enabled: enabled && validId(organizationId),
    staleTime: 15_000,
    ...liveRefresh,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/**
 * The caller's own counts of the unified inbox, with `tz` deciding today; fresh while the tab is
 * visible, like the list.
 */
export function useInboxCounts(
  organizationId: number,
  tz: string,
  { enabled = true }: InboxQueryOptions = {}
) {
  return useQuery({
    queryKey: mailboxKeys.inboxCounts(organizationId, tz),
    queryFn: () => mailboxService.inboxCounts(organizationId, tz),
    enabled: enabled && validId(organizationId),
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
 * feature runs, else the browser's zone. `ready` is false while the settings load, so the inbox
 * is not read once in the browser's zone and again in the user's; a failed load is not reported
 * globally and leaves the browser's zone.
 */
export function useInboxTimezone(): { zone: string; ready: boolean } {
  const settingsEnabled = isWebFeatureEnabled('setting');
  const settings = useUserSettings({ enabled: settingsEnabled, localErrors: true });
  if (!settingsEnabled) return { zone: browserTimeZone(), ready: true };
  const setting = settings.data?.find(item => item.key === userTimeZoneKey);
  const zone = typeof setting?.value === 'string' ? setting.value : '';
  return { zone: zone || browserTimeZone(), ready: !settings.isPending };
}

/** The organization's mail roles, as the caller may see them; they change seldom. */
export function useMailRoles(organizationId: number) {
  return useQuery({
    queryKey: mailboxKeys.roles(organizationId),
    queryFn: () => mailboxService.listRoles(organizationId),
    enabled: validId(organizationId),
    staleTime: 60_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/**
 * After a role write: the roles, the mailbox list (each mailbox's `role_id`), the unified inbox
 * counts, and the inbox rows, whose role tags and role filter follow the mailbox's role.
 */
function invalidateRoles(queryClient: QueryClient, organizationId: number) {
  return Promise.all([
    queryClient.invalidateQueries({ queryKey: mailboxKeys.roles(organizationId) }),
    queryClient.invalidateQueries({ queryKey: mailboxKeys.list(organizationId) }),
    queryClient.invalidateQueries({ queryKey: mailboxKeys.allInboxCounts(organizationId) }),
    queryClient.invalidateQueries({ queryKey: mailboxKeys.inboxes(organizationId) }),
  ]);
}

export function useCreateMailRole(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: MailRoleInput) => mailboxService.createRole(organizationId, input),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () => invalidateRoles(queryClient, organizationId),
  });
}

export function useUpdateMailRole(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ roleId, ...input }: { roleId: number } & MailRoleInput) =>
      mailboxService.updateRole(organizationId, roleId, input),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () => invalidateRoles(queryClient, organizationId),
  });
}

export function useDeleteMailRole(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (roleId: number) => mailboxService.deleteRole(organizationId, roleId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () => invalidateRoles(queryClient, organizationId),
  });
}

/** Puts a mailbox in a role, or takes it out with `roleId: null`. */
export function useAssignMailboxRole(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ mailboxId, roleId }: { mailboxId: number; roleId: number | null }) =>
      mailboxService.assignRole(organizationId, mailboxId, roleId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () => invalidateRoles(queryClient, organizationId),
  });
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
 * Changes the row in every cached unified inbox listing of the organization before the server
 * answers. Answers the function that puts those listings back as they were, for a failed request.
 */
function patchInboxRow(
  queryClient: QueryClient,
  organizationId: number,
  rowId: number,
  change: (item: InboxItem) => InboxItem
): () => void {
  const queryKey = mailboxKeys.inboxes(organizationId);
  const snapshot = queryClient.getQueriesData<InboxListing>({ queryKey });
  queryClient.setQueriesData<InboxListing>({ queryKey }, listing =>
    listing?.pages
      ? {
          ...listing,
          pages: listing.pages.map(page => ({
            ...page,
            items: page.items.map(item => (item.id === rowId ? change(item) : item)),
          })),
        }
      : listing
  );
  return () => {
    for (const [key, listing] of snapshot) queryClient.setQueryData(key, listing);
  };
}

/** Shows the row read in every cached listing at once; answers the rollback. */
export function markInboxRowRead(
  queryClient: QueryClient,
  organizationId: number,
  rowId: number
): () => void {
  return patchInboxRow(queryClient, organizationId, rowId, readRow);
}

/**
 * Stars or unstars a row of the unified inbox: the message, or every message of a thread row. The
 * star shows in the cached listings at once and is put back if the request fails; the caller
 * says why.
 */
export function useUpdateInboxMessage(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ item, starred }: { item: InboxItem; starred: boolean }): Promise<unknown> => {
      const ids = item.thread?.message_ids ?? [item.id];
      return ids.length === 1
        ? mailboxService.updateMessage(organizationId, item.mailbox_id, ids[0], { starred })
        : mailboxService.updateMessages(organizationId, item.mailbox_id, {
            message_ids: ids,
            starred,
          });
    },
    meta: LOCAL_ERROR_HANDLING_META,
    onMutate: ({ item, starred }) => ({
      rollback: patchInboxRow(queryClient, organizationId, item.id, row => ({
        ...row,
        starred,
        thread: row.thread && { ...row.thread, starred },
      })),
    }),
    onError: (_error, _variables, context) => context?.rollback(),
    onSettled: (_result, _error, { item }) =>
      invalidateMail(queryClient, organizationId, item.mailbox_id),
  });
}
