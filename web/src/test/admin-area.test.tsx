import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { render, screen } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { AdminEntryLink } from '@/features/mailbox/components/admin/admin-entry-link';
import { AdminShell } from '@/features/mailbox/components/admin/admin-shell';
import { messages } from '@/i18n/modules';
import nextConfig from '../../next.config';

const state = vi.hoisted(() => ({
  role: 'owner',
  pathname: '/console/admin/mailboxes',
  notFound: vi.fn(),
  replace: vi.fn(),
}));

vi.mock('next/navigation', () => ({
  useRouter: () => ({ replace: state.replace }),
  usePathname: () => state.pathname,
  useSearchParams: () => new URLSearchParams('org=1'),
  notFound: () => state.notFound(),
}));
vi.mock('@/features/organization/hooks/use-organizations', () => {
  const now = '2026-11-16T10:00:00Z';
  return {
    useOrganizations: () => ({
      data: {
        items: [
          { id: 1, name: 'Acme', slug: 'acme', role: state.role, created_at: now, updated_at: now },
        ],
        meta: { current_page: 1, per_page: 25, total: 1, last_page: 1, from: 1, to: 1 },
        links: { first: '', last: '', prev: null, next: null },
      },
      isPending: false,
      error: null,
      refetch: vi.fn(),
    }),
  };
});

function renderWithMessages(node: React.ReactNode) {
  return render(
    <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
      {node}
    </NextIntlClientProvider>
  );
}

describe('organization admin area', () => {
  beforeEach(() => {
    state.role = 'owner';
    state.pathname = '/console/admin/mailboxes';
    state.notFound.mockReset();
  });

  it('a member gets the console 404', () => {
    state.role = 'member';
    renderWithMessages(
      <AdminShell>
        <p>admin content</p>
      </AdminShell>
    );
    expect(state.notFound).toHaveBeenCalled();
  });

  it('owners and admins see the admin navigation', () => {
    for (const role of ['owner', 'admin']) {
      state.role = role;
      const { unmount } = renderWithMessages(
        <AdminShell>
          <p>admin content</p>
        </AdminShell>
      );
      const nav = screen.getByRole('navigation', { name: 'Admin console' });
      const domains = screen.getByRole('link', { name: 'Domains' });
      const mailboxes = screen.getByRole('link', { name: 'Shared mailboxes' });
      expect(nav).toContainElement(domains);
      expect(domains).toHaveAttribute('href', '/console/admin/domains?org=1');
      expect(mailboxes).toHaveAttribute('href', '/console/admin/mailboxes?org=1');
      expect(mailboxes).toHaveAttribute('aria-current', 'page');
      expect(domains).not.toHaveAttribute('aria-current');
      expect(screen.getByText('admin content')).toBeInTheDocument();
      unmount();
    }
    expect(state.notFound).not.toHaveBeenCalled();
  });

  it('lists the platform account page', () => {
    state.pathname = '/console/admin/platform-accounts';
    renderWithMessages(<AdminShell>content</AdminShell>);
    const link = screen.getByRole('link', { name: 'Platform accounts' });
    expect(link).toHaveAttribute('href', '/console/admin/platform-accounts?org=1');
    expect(link).toHaveAttribute('aria-current', 'page');
  });

  it('marks Domains current on a zone DNS page', () => {
    state.pathname = '/console/admin/domains/9/dns';
    renderWithMessages(<AdminShell>content</AdminShell>);
    expect(screen.getByRole('link', { name: 'Domains' })).toHaveAttribute('aria-current', 'page');
  });

  it('the entry link shows only for owners and admins', () => {
    const { unmount } = renderWithMessages(<AdminEntryLink />);
    expect(screen.getByRole('link', { name: 'Admin console' })).toHaveAttribute(
      'href',
      '/console/admin?org=1'
    );
    unmount();

    state.role = 'member';
    renderWithMessages(<AdminEntryLink />);
    expect(screen.queryByRole('link', { name: 'Admin console' })).toBeNull();
    // The link reads the organization and never changes the URL.
    expect(state.replace).not.toHaveBeenCalled();
  });

  it('old domain paths redirect', async () => {
    const redirects = await nextConfig.redirects?.();
    expect(redirects).toEqual(
      expect.arrayContaining([
        {
          source: '/console/mail-domain',
          destination: '/console/admin/domains',
          permanent: false,
        },
        {
          source: '/console/mail-domain/:zoneId/dns',
          destination: '/console/admin/domains/:zoneId/dns',
          permanent: false,
        },
      ])
    );
  });

  it('the console sidebar no longer lists Domain', () => {
    const layout = readFileSync(
      resolve(process.cwd(), 'src/app/(protected)/(console)/console/layout.tsx'),
      'utf8'
    );
    const home = readFileSync(
      resolve(process.cwd(), 'src/app/(protected)/(console)/console/page.tsx'),
      'utf8'
    );
    expect(layout).not.toContain('nav.mailDomain');
    expect(layout).toContain('<AdminEntryLink />');
    expect(home).not.toContain('home.quickLinks.domain');
    expect(messages.nav).not.toHaveProperty('mailDomain');
  });
});
