import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';

import {
  useCreateOrganizationInvitation,
  useResendOrganizationInvitation,
} from '@/features/organization/hooks/use-organizations';

const link = 'https://app.example.com/invite#oinv_abc.0123456789abcdef';
const result = {
  invitation: {
    id: 73,
    organization_id: 42,
    email: 'member@example.com',
    role: 'member',
    status: 'pending',
    expires_at: '2026-07-22T10:00:00Z',
    email_send_status: 'accepted_by_provider',
    email_attempted_at: '2026-07-15T10:00:01Z',
    created_at: '2026-07-15T10:00:00Z',
    updated_at: '2026-07-15T10:00:00Z',
  },
  email_send_status: 'accepted_by_provider',
  invitation_url: link,
};
const service = vi.hoisted(() => ({ invite: vi.fn(), resendInvitation: vi.fn() }));
vi.mock('@/features/organization/services/organization-service', () => ({
  organizationService: service,
}));

function setup<T>(hook: () => T) {
  const client = new QueryClient();
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  return { client, ...renderHook(hook, { wrapper }) };
}

function cachedLink(client: QueryClient): boolean {
  return client
    .getMutationCache()
    .getAll()
    .some((mutation) => JSON.stringify(mutation.state.data ?? null).includes(link));
}

describe('the one-time invitation link leaves no trace in the mutation cache (#195)', () => {
  it('forgets the created link once the confirmation resets the mutation', async () => {
    service.invite.mockResolvedValue(result);
    const { client, result: hook } = setup(() => useCreateOrganizationInvitation(42));

    await act(() => hook.current.mutateAsync({ email: 'member@example.com', role: 'member' }));
    expect(cachedLink(client)).toBe(true);

    act(() => hook.current.reset());
    await waitFor(() => expect(cachedLink(client)).toBe(false));
    expect(hook.current.data).toBeUndefined();
  });

  it('forgets the resent link once the confirmation resets the mutation', async () => {
    service.resendInvitation.mockResolvedValue(result);
    const { client, result: hook } = setup(() => useResendOrganizationInvitation(42));

    await act(() => hook.current.mutateAsync(73));
    expect(cachedLink(client)).toBe(true);

    act(() => hook.current.reset());
    await waitFor(() => expect(cachedLink(client)).toBe(false));
    expect(hook.current.data).toBeUndefined();
  });
});
