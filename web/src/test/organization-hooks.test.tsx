import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';

import {
  organizationKeys,
  useRemoveOrganizationMember,
} from '@/features/organization/hooks/use-organizations';
import { organizationService } from '@/features/organization/services/organization-service';

describe('useRemoveOrganizationMember', () => {
  // Issue #114: leaving refreshes the caller's own organization list and switcher.
  it('refreshes the organization list after a removal', async () => {
    vi.spyOn(organizationService, 'removeMember').mockResolvedValue(undefined);
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    client.setQueryData(organizationKeys.list(), { items: [], meta: {} });
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    );
    const { result } = renderHook(() => useRemoveOrganizationMember(7), { wrapper });

    await act(() => result.current.mutateAsync(3));

    expect(client.getQueryState(organizationKeys.list())?.isInvalidated).toBe(true);
  });
});
