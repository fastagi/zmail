import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  organizationKeys,
  useForgetOrganization,
} from '@/features/organization/hooks/use-organizations';
import { ACTIVE_ORGANIZATION_STORAGE_KEY } from '@/features/organization/utils/active-organization';

const service = vi.hoisted(() => ({ list: vi.fn() }));
vi.mock('@/features/organization/services/organization-service', () => ({
  organizationService: service,
}));

function organization(id: number, name: string) {
  return { id, name, slug: name.toLowerCase(), role: 'member' };
}

describe('forgetting an organization the user left (#114)', () => {
  afterEach(() => {
    window.sessionStorage.clear();
  });

  it('drops it from the cached list, marks its pages stale, and forgets it as active', () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    client.setQueryData(organizationKeys.list(), {
      items: [organization(42, 'Left'), organization(7, 'Stays')],
      meta: { total: 2 },
      links: {},
    });
    client.setQueryData(organizationKeys.context(42), { organization_id: 42 });
    client.setQueryData(organizationKeys.context(7), { organization_id: 7 });
    window.sessionStorage.setItem(ACTIVE_ORGANIZATION_STORAGE_KEY, '42');
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    );
    const { result } = renderHook(() => useForgetOrganization(), { wrapper });

    act(() => result.current(42));

    const list = client.getQueryData<{ items: { id: number }[]; meta: { total: number } }>(
      organizationKeys.list()
    );
    expect(list?.items.map(({ id }) => id)).toEqual([7]);
    expect(list?.meta.total).toBe(1);
    expect(client.getQueryState(organizationKeys.list())?.isInvalidated).toBe(true);
    expect(client.getQueryState(organizationKeys.context(42))?.isInvalidated).toBe(true);
    expect(client.getQueryState(organizationKeys.context(7))?.isInvalidated).toBe(false);
    expect(window.sessionStorage.getItem(ACTIVE_ORGANIZATION_STORAGE_KEY)).toBeNull();
  });

  it('keeps a different remembered organization', () => {
    const client = new QueryClient();
    window.sessionStorage.setItem(ACTIVE_ORGANIZATION_STORAGE_KEY, '7');
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    );
    const { result } = renderHook(() => useForgetOrganization(), { wrapper });

    act(() => result.current(42));

    expect(window.sessionStorage.getItem(ACTIVE_ORGANIZATION_STORAGE_KEY)).toBe('7');
  });
});
