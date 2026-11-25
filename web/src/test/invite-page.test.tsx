import { render, screen, waitFor } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import InvitePage from '@/app/invite/page';
import { takePendingInvitation } from '@/features/organization/utils/pending-invitation';
import { messages } from '@/i18n/modules';
import zhHansOrganization from '@/i18n/modules/organization/zh-Hans';

const nav = vi.hoisted(() => ({
  replace: vi.fn(),
}));

vi.mock('next/navigation', () => ({
  useRouter: () => ({ replace: nav.replace }),
}));

function renderInvitePage(locale: 'en-US' | 'zh-Hans' = 'en-US') {
  const localized =
    locale === 'en-US' ? messages : { ...messages, organization: zhHansOrganization };
  return render(
    <NextIntlClientProvider locale={locale} messages={localized as unknown as AbstractIntlMessages}>
      <InvitePage />
    </NextIntlClientProvider>
  );
}

describe('/invite landing page', () => {
  const originalFetch = global.fetch;
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    nav.replace.mockReset();
    window.sessionStorage.clear();
    window.history.replaceState(null, '', '/invite');
    fetchMock = vi.fn();
    global.fetch = fetchMock as unknown as typeof fetch;
  });

  afterEach(() => {
    global.fetch = originalFetch;
  });

  it('stashes the fragment token, clears the hash, and redirects signed-in users directly to accept', async () => {
    window.history.replaceState(null, '', '/invite#oinv_secret.token');
    fetchMock.mockResolvedValue({ ok: true, status: 200 });

    renderInvitePage();

    await waitFor(() => {
      expect(nav.replace).toHaveBeenCalledWith('/console/organizations?accept=1');
    });
    expect(takePendingInvitation()).toBe('oinv_secret.token');
    expect(window.location.hash).toBe('');
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/auth/me',
      expect.objectContaining({ credentials: 'include' })
    );
  });

  it('shows a welcoming onboarding card with register and login returnUrl links for logged-out users', async () => {
    window.history.replaceState(null, '', '/invite#oinv_new_colleague');
    fetchMock.mockResolvedValue({ ok: false, status: 401 });

    renderInvitePage();

    expect(
      await screen.findByText("You're invited to join an organization")
    ).toBeInTheDocument();
    expect(takePendingInvitation()).toBe('oinv_new_colleague');
    expect(window.location.hash).toBe('');
    expect(nav.replace).not.toHaveBeenCalled();

    const registerLink = screen.getByRole('link', { name: 'Create account & join' });
    const loginLink = screen.getByRole('link', { name: 'Sign in to join' });
    expect(registerLink).toHaveAttribute(
      'href',
      '/register?returnUrl=%2Fconsole%2Forganizations%3Faccept%3D1'
    );
    expect(loginLink).toHaveAttribute(
      'href',
      '/login?returnUrl=%2Fconsole%2Forganizations%3Faccept%3D1'
    );
  });

  it('renders the zh-Hans onboarding card copy for logged-out visitors', async () => {
    window.history.replaceState(null, '', '/invite#oinv_zh_token');
    fetchMock.mockResolvedValue({ ok: false, status: 401 });

    renderInvitePage('zh-Hans');

    expect(await screen.findByText('你收到了一份组织邀请')).toBeInTheDocument();
    expect(
      screen.getByText(
        '还没有 Zmail 账号？请使用收到邀请的邮箱完成注册，注册后将自动引导你加入组织并进入企业邮箱。'
      )
    ).toBeInTheDocument();
    expect(screen.getByRole('link', { name: '注册账号并加入' })).toHaveAttribute(
      'href',
      '/register?returnUrl=%2Fconsole%2Forganizations%3Faccept%3D1'
    );
    expect(screen.getByRole('link', { name: '已有账号，登录并加入' })).toHaveAttribute(
      'href',
      '/login?returnUrl=%2Fconsole%2Forganizations%3Faccept%3D1'
    );
  });

  it('shows a friendly invalid-link notice with a login link when no token fragment is present', async () => {
    window.history.replaceState(null, '', '/invite');

    renderInvitePage();

    expect(
      await screen.findByText('Invitation link is invalid or expired')
    ).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Go to sign in' })).toHaveAttribute('href', '/login');
    expect(fetchMock).not.toHaveBeenCalled();
    expect(nav.replace).not.toHaveBeenCalled();
  });
});
