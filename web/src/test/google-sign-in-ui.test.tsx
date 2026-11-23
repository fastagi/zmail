import { render, screen } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { LoginForm } from '@/features/auth/components/login-form';
import { RegisterForm } from '@/features/auth/components/register-form';
import {
  googleSignInStartHref,
  resolveGoogleSignInErrorKey,
} from '@/features/auth/utils/google-sign-in';
import { messages } from '@/i18n/modules';
import zhHansAuth from '@/i18n/modules/auth/zh-Hans';

const mutation = vi.hoisted(() => ({
  error: null as unknown,
  isPending: false,
  mutate: vi.fn(),
  reset: vi.fn(),
}));

vi.mock('@/features/auth/hooks/use-auth', () => ({
  useLogin: () => mutation,
  useRegister: () => mutation,
}));
vi.mock('@/features/auth/services/auth-service', () => ({
  authService: { requestRegistrationCode: vi.fn() },
}));
vi.mock('next/navigation', () => ({
  useSearchParams: () => new URLSearchParams(window.location.search),
}));

function renderWith(ui: React.ReactNode, locale: 'en-US' | 'zh-Hans' = 'en-US') {
  const localized =
    locale === 'en-US' ? messages : { ...messages, auth: { ...messages.auth, ...zhHansAuth } };
  return render(
    <NextIntlClientProvider locale={locale} messages={localized as unknown as AbstractIntlMessages}>
      {ui}
    </NextIntlClientProvider>
  );
}

describe('Continue with Google', () => {
  afterEach(() => {
    window.history.replaceState(null, '', '/');
  });

  it('is hidden on both pages when the API has no Google client', () => {
    renderWith(<LoginForm />);
    expect(screen.queryByRole('link', { name: 'Continue with Google' })).toBeNull();
    renderWith(<RegisterForm />);
    expect(screen.queryByRole('link', { name: 'Continue with Google' })).toBeNull();
  });

  it('starts from the sign-in page with the return URL', () => {
    window.history.replaceState(null, '', '/login?returnUrl=%2Fconsole%2Fadmin%2Fdomains');
    renderWith(<LoginForm googleEnabled />);
    expect(screen.getByRole('link', { name: 'Continue with Google' })).toHaveAttribute(
      'href',
      '/api/auth/google?returnUrl=%2Fconsole%2Fadmin%2Fdomains'
    );
  });

  it('starts from the sign-up page too', () => {
    renderWith(<RegisterForm googleEnabled />);
    expect(screen.getByRole('link', { name: 'Continue with Google' })).toHaveAttribute(
      'href',
      '/api/auth/google'
    );
  });

  it('drops an off-site return URL from the start link', () => {
    window.history.replaceState(null, '', '/login?returnUrl=%2F%2Fevil.example%2Fsteal');
    renderWith(<LoginForm googleEnabled />);
    expect(screen.getByRole('link', { name: 'Continue with Google' })).toHaveAttribute(
      'href',
      '/api/auth/google'
    );
  });

  it('reads in Simplified Chinese', () => {
    renderWith(<LoginForm googleEnabled />, 'zh-Hans');
    expect(screen.getByRole('link', { name: '使用 Google 登录' })).toBeInTheDocument();
    expect(zhHansAuth.continueWithGoogle).toBe('使用 Google 登录');
    expect(messages.auth.continueWithGoogle).toBe('Continue with Google');
  });

  it('explains a failed Google sign-in without echoing the query', () => {
    window.history.replaceState(null, '', '/login?googleError=AUTH.GOOGLE_REGISTRATION_CLOSED');
    const { unmount } = renderWith(<LoginForm googleEnabled />);
    expect(screen.getByText(messages.auth.googleRegistrationClosed)).toBeInTheDocument();
    unmount();

    window.history.replaceState(null, '', '/login?googleError=%3Cscript%3Ealert(1)%3C%2Fscript%3E');
    renderWith(<LoginForm googleEnabled />);
    expect(screen.getByText(messages.auth.googleFailed)).toBeInTheDocument();
    expect(screen.queryByText(/script/)).toBeNull();
  });
});

describe('Google sign-in helpers', () => {
  it('maps known failure codes to copy', () => {
    expect(resolveGoogleSignInErrorKey(null)).toBeNull();
    expect(resolveGoogleSignInErrorKey('cancelled')).toBe('auth.googleCancelled');
    expect(resolveGoogleSignInErrorKey('AUTH.GOOGLE_EMAIL_UNVERIFIED')).toBe(
      'auth.googleEmailUnverified'
    );
    expect(resolveGoogleSignInErrorKey('AUTH.INVALID_CREDENTIALS')).toBe(
      'auth.googleAccountUnavailable'
    );
    expect(resolveGoogleSignInErrorKey('AUTH.TOTP_REQUIRED')).toBe('auth.googleTotpRequired');
    expect(resolveGoogleSignInErrorKey('toString')).toBe('auth.googleFailed');
  });

  it('keeps only same-origin return URLs on the start link', () => {
    expect(googleSignInStartHref('/console/admin/domains')).toBe(
      '/api/auth/google?returnUrl=%2Fconsole%2Fadmin%2Fdomains'
    );
    expect(googleSignInStartHref('https://evil.example')).toBe('/api/auth/google');
    expect(googleSignInStartHref(null)).toBe('/api/auth/google');
  });
});
