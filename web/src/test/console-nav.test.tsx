import { render, screen } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import ConsoleLayout from '@/app/(protected)/(console)/console/layout';
import { messages } from '@/i18n/modules';

const state = vi.hoisted(() => ({ pathname: '/console/mailboxes' }));

vi.mock('next/navigation', () => ({
  useRouter: () => ({ replace: vi.fn(), push: vi.fn() }),
  usePathname: () => state.pathname,
  useSearchParams: () => new URLSearchParams(),
}));
vi.mock('@/features/auth/hooks/use-auth', () => ({
  useLogout: () => ({ mutate: vi.fn(), isPending: false }),
}));
vi.mock('@/features/auth/store/auth-store', () => ({
  useAuthStore: { use: { user: () => ({ name: 'Ada', email: 'ada@example.com' }) } },
}));
vi.mock('@/config/features', () => ({
  isWebFeatureEnabled: (feature: string) => feature === 'mailbox',
}));
vi.mock('@/components/theme-toggle', () => ({ ThemeToggle: () => null }));
vi.mock('@/components/common', () => ({ LanguageSwitcher: () => null }));

function renderLayout() {
  return render(
    <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
      <ConsoleLayout>
        <p>page</p>
      </ConsoleLayout>
    </NextIntlClientProvider>
  );
}

describe('console navigation', () => {
  beforeEach(() => {
    state.pathname = '/console/mailboxes';
  });

  it('folds to an icon rail on the mailboxes page, like Gmail keeps one navigation', () => {
    renderLayout();

    const mailboxes = screen.getByRole('link', { name: 'Mailboxes' });
    expect(mailboxes).toHaveAttribute('title', 'Mailboxes');
    expect(mailboxes).toHaveAttribute('aria-current', 'page');
    expect(screen.getByRole('complementary')).toHaveAttribute('data-compact', 'true');
  });

  it('keeps the labelled sidebar on the other console pages', () => {
    state.pathname = '/console/emails';
    renderLayout();

    expect(screen.getByRole('link', { name: 'Mailboxes' })).not.toHaveAttribute('title');
    expect(screen.getByRole('complementary')).toHaveAttribute('data-compact', 'false');
  });
});
