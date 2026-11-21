import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { useMailBroadcastRecipients } from '@/features/mailbox/hooks/use-mail-broadcasts';

const service = vi.hoisted(() => ({ broadcastRecipients: vi.fn() }));

vi.mock('@/features/mailbox/services/mailbox-service', () => ({ mailboxService: service }));

function recipientsPage(status: 'pending' | 'sent') {
  return {
    items: [{ id: 1, email: 'ann@example.com', status }],
    total: 1,
    page: 1,
    per_page: 25,
  };
}

function setup(initialLive: boolean) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  return renderHook(
    ({ live }: { live: boolean }) => useMailBroadcastRecipients(7, 11, undefined, 1, live),
    { wrapper, initialProps: { live: initialLive } }
  );
}

describe('broadcast recipient refresh (#87)', () => {
  beforeEach(() => {
    service.broadcastRecipients.mockReset();
  });

  it('refetches the recipients once the broadcast stops being live', async () => {
    service.broadcastRecipients.mockResolvedValueOnce(recipientsPage('pending'));
    const { result, rerender } = setup(true);
    await waitFor(() => expect(result.current.data?.items[0]?.status).toBe('pending'));

    // The detail poll saw the terminal status: polling stops, so the rows must refresh now.
    service.broadcastRecipients.mockResolvedValueOnce(recipientsPage('sent'));
    rerender({ live: false });

    await waitFor(() => expect(result.current.data?.items[0]?.status).toBe('sent'));
    expect(service.broadcastRecipients).toHaveBeenCalledTimes(2);
  });

  it('does not refetch a broadcast that was never live', async () => {
    service.broadcastRecipients.mockResolvedValue(recipientsPage('sent'));
    const { result, rerender } = setup(false);
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    rerender({ live: false });

    expect(service.broadcastRecipients).toHaveBeenCalledTimes(1);
  });
});
