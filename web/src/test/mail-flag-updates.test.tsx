import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  mailboxKeys,
  useUpdateMailMessage,
  useUpdateMailMessages,
} from '@/features/mailbox/hooks/use-mailbox';
import { mailboxService } from '@/features/mailbox/services/mailbox-service';
import type { InboxFilters } from '@/features/mailbox/types';

const organizationId = 1;
const mailboxId = 2;
const inboxFilters: InboxFilters = { view: 'all', group: 'thread', tz: 'UTC' };
const listFilters = { direction: 'inbound' as const };

let queryClient: QueryClient;

function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((done, fail) => {
    resolve = done;
    reject = fail;
  });
  return { promise, resolve, reject };
}

function summary(overrides: Record<string, unknown> = {}) {
  return {
    id: 11,
    mailbox_id: mailboxId,
    direction: 'inbound',
    read: false,
    starred: false,
    subject: 'Hello',
    ...overrides,
  };
}

function message(overrides: Record<string, unknown> = {}) {
  return { ...summary(), text_body: 'Hi', html_body: '', ...overrides };
}

function seed() {
  queryClient.setQueryData(mailboxKeys.message(organizationId, mailboxId, 11), message());
  queryClient.setQueryData(mailboxKeys.thread(organizationId, mailboxId, 10), [
    message({ id: 10, read: true }),
    message(),
  ]);
  queryClient.setQueryData(mailboxKeys.filteredPage(organizationId, mailboxId, listFilters, 1), {
    items: [
      summary({
        thread: {
          message_count: 2,
          message_ids: [10, 11],
          unread: true,
          starred: false,
          label_ids: [],
        },
      }),
    ],
    meta: {},
    links: {},
  });
  queryClient.setQueryData(mailboxKeys.inbox(organizationId, inboxFilters), {
    pages: [
      {
        items: [
          summary({
            thread: {
              message_count: 2,
              message_ids: [10, 11],
              unread: true,
              starred: false,
              label_ids: [],
            },
          }),
          // Another mailbox's message with the same id is left alone.
          summary({ mailbox_id: 9 }),
        ],
        meta: {},
      },
    ],
    pageParams: [undefined],
  });
}

function cached() {
  const detail = queryClient.getQueryData<{ starred: boolean; read: boolean }>(
    mailboxKeys.message(organizationId, mailboxId, 11)
  );
  const thread = queryClient.getQueryData<{ id: number; starred: boolean }[]>(
    mailboxKeys.thread(organizationId, mailboxId, 10)
  );
  const list = queryClient.getQueryData<{
    items: { starred: boolean; read: boolean; thread: { starred: boolean; unread: boolean } }[];
  }>(mailboxKeys.filteredPage(organizationId, mailboxId, listFilters, 1));
  const inbox = queryClient.getQueryData<{
    pages: { items: { starred: boolean; thread?: { starred: boolean } }[] }[];
  }>(mailboxKeys.inbox(organizationId, inboxFilters));
  return {
    detail,
    threadNewest: thread?.find(item => item.id === 11),
    listRow: list?.items[0],
    inboxRow: inbox?.pages[0].items[0],
    otherMailboxRow: inbox?.pages[0].items[1],
  };
}

describe('mail flag updates', () => {
  beforeEach(() => {
    queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    seed();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('stars a message in every cached view before the server answers', async () => {
    const answer = deferred<never>();
    vi.spyOn(mailboxService, 'updateMessage').mockReturnValue(answer.promise);
    const { result } = renderHook(() => useUpdateMailMessage(organizationId, mailboxId), {
      wrapper,
    });

    act(() => result.current.mutate({ messageId: 11, starred: true }));

    await waitFor(() => expect(cached().detail?.starred).toBe(true));
    const now = cached();
    expect(now.threadNewest?.starred).toBe(true);
    expect(now.listRow?.starred).toBe(true);
    expect(now.listRow?.thread.starred).toBe(true);
    expect(now.inboxRow?.thread?.starred).toBe(true);
    expect(now.otherMailboxRow?.starred).toBe(false);
  });

  it('puts the star back everywhere when the request fails', async () => {
    const answer = deferred<never>();
    vi.spyOn(mailboxService, 'updateMessage').mockReturnValue(answer.promise);
    const { result } = renderHook(() => useUpdateMailMessage(organizationId, mailboxId), {
      wrapper,
    });

    act(() => result.current.mutate({ messageId: 11, starred: true }));
    await waitFor(() => expect(cached().detail?.starred).toBe(true));
    await act(async () => {
      answer.reject(new Error('down'));
      await answer.promise.catch(() => undefined);
    });

    await waitFor(() => expect(cached().detail?.starred).toBe(false));
    const now = cached();
    expect(now.threadNewest?.starred).toBe(false);
    expect(now.listRow?.thread.starred).toBe(false);
    expect(now.inboxRow?.thread?.starred).toBe(false);
  });

  it('marks a thread read only once the change covers all of its messages', async () => {
    vi.spyOn(mailboxService, 'updateMessages').mockReturnValue(deferred<never>().promise);
    const { result } = renderHook(() => useUpdateMailMessages(organizationId, mailboxId), {
      wrapper,
    });

    act(() => result.current.mutate({ message_ids: [11], read: true }));
    await waitFor(() => expect(cached().listRow?.read).toBe(true));
    expect(cached().listRow?.thread.unread).toBe(true);

    act(() => result.current.mutate({ message_ids: [10, 11], read: true }));
    await waitFor(() => expect(cached().listRow?.thread.unread).toBe(false));
  });

  it('leaves the caches to the server for a move', async () => {
    vi.spyOn(mailboxService, 'updateMessage').mockReturnValue(deferred<never>().promise);
    const { result } = renderHook(() => useUpdateMailMessage(organizationId, mailboxId), {
      wrapper,
    });

    act(() => result.current.mutate({ messageId: 11, placement: 'archive', starred: true }));

    await waitFor(() => expect(result.current.isPending).toBe(true));
    expect(cached().detail?.starred).toBe(false);
  });
});
