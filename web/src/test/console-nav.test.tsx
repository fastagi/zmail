import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import ConsoleLayout from '@/app/(protected)/(console)/console/layout';
import {
  DEFAULT_WORKSPACE_MODE,
  useWorkspaceModeStore,
} from '@/features/mailbox/stores/workspace-mode';
import { messages } from '@/i18n/modules';

const state = vi.hoisted(() => ({
  pathname: '/console/mailboxes',
  features: undefined as
    | { broadcasts: boolean; contacts: boolean; templates: boolean; api_keys: boolean }
    | undefined,
}));

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
vi.mock('@/features/mailbox/hooks/use-mail-organization', () => ({
  useChosenMemberFeatures: () => state.features,
}));
vi.mock('@/components/common', () => ({ LanguageSwitcher: () => null }));

function layoutTree() {
  return (
    <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
      <ConsoleLayout>
        <p>page</p>
      </ConsoleLayout>
    </NextIntlClientProvider>
  );
}

function renderLayout() {
  return render(layoutTree());
}

describe('console navigation', () => {
  beforeEach(() => {
    state.pathname = '/console/mailboxes';
    state.features = undefined;
    window.localStorage.clear();
    useWorkspaceModeStore.setState({
      mode: DEFAULT_WORKSPACE_MODE,
      isPersonal: false,
    });
  });

  it('hides the entries a member has not been granted in the active organization', () => {
    state.pathname = '/console/emails';
    state.features = { broadcasts: false, contacts: true, templates: false, api_keys: false };
    renderLayout();

    expect(screen.getByRole('link', { name: 'Contacts' })).toBeInTheDocument();
    for (const name of ['Broadcasts', 'Templates', 'Developers']) {
      expect(screen.queryByRole('link', { name })).not.toBeInTheDocument();
    }
    expect(screen.getByRole('link', { name: 'Mailboxes' })).toBeInTheDocument();
  });

  it('shows every entry until the grants are known', () => {
    state.pathname = '/console/emails';
    renderLayout();

    for (const name of ['Broadcasts', 'Contacts', 'Templates', 'Developers']) {
      expect(screen.getByRole('link', { name })).toBeInTheDocument();
    }
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

  // #80: a mouse click leaves DOM focus on the rail link; `focus-within` kept the
  // rail expanded over the Mailboxes compose button until the user clicked elsewhere.
  it('expands the compact rail for hover or keyboard focus, not for a stale click focus', () => {
    renderLayout();

    const rail = screen.getByRole('complementary');
    expect(rail.className).not.toMatch(/focus-within/);
    expect(rail.className).toContain('has-[:focus-visible]:w-[220px]');
    expect(rail.className).toContain('hover:w-[220px]');
    const label = within(rail).getByText('Mailboxes');
    expect(label.className).not.toMatch(/focus-within/);
    expect(label.className).toContain('group-has-[:focus-visible]/rail:opacity-100');
  });

  // #79: below the desktop breakpoint the rail is hidden; a header menu replaces it.
  it('offers the same destinations in a mobile menu that closes on navigation', async () => {
    const view = renderLayout();

    const menuButton = screen.getByRole('button', { name: 'Open navigation menu' });
    expect(menuButton).toHaveClass('md:hidden');
    expect(menuButton).toHaveAttribute('aria-expanded', 'false');

    menuButton.focus();
    fireEvent.click(menuButton);
    const sheet = await screen.findByRole('dialog', { name: 'Console navigation' });
    expect(menuButton).toHaveAttribute('aria-expanded', 'true');
    const sheetNav = within(sheet).getByRole('navigation', { name: 'Console navigation' });
    for (const name of [
      'Console',
      'Emails',
      'Mailboxes',
      'Broadcasts',
      'Contacts',
      'Templates',
      'Developers',
      'Settings',
      'Return to site',
    ]) {
      expect(within(sheetNav).getByRole('link', { name })).toBeInTheDocument();
    }
    expect(within(sheetNav).getByRole('link', { name: 'Mailboxes' })).toHaveAttribute(
      'aria-current',
      'page'
    );

    fireEvent.keyDown(sheet, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(menuButton).toHaveFocus();

    fireEvent.click(menuButton);
    await screen.findByRole('dialog', { name: 'Console navigation' });
    state.pathname = '/console/emails';
    view.rerender(layoutTree());
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
  });

  it('hides B2B-only nav items in Personal Mode while keeping Mailboxes, Settings, and the mode toggle', () => {
    state.pathname = '/console/mailboxes';
    useWorkspaceModeStore.getState().setMode('personal');
    renderLayout();

    expect(screen.getByRole('link', { name: 'Mailboxes' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Settings' })).toBeInTheDocument();
    expect(screen.getByRole('radiogroup', { name: 'Workspace mode' })).toHaveAttribute(
      'data-mode',
      'personal'
    );
    for (const name of [
      'Console',
      'Organizations',
      'Emails',
      'Broadcasts',
      'Contacts',
      'Templates',
      'Developers',
    ]) {
      expect(screen.queryByRole('link', { name })).not.toBeInTheDocument();
    }
  });
});
