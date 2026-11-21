import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { env } from '@/config/env';
import { RegisterForm } from '@/features/auth/components/register-form';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { messages } from '@/i18n/modules';

const mutation = vi.hoisted(() => ({
  error: null as unknown,
  isPending: false,
  mutate: vi.fn(),
  reset: vi.fn(),
}));
const service = vi.hoisted(() => ({ requestRegistrationCode: vi.fn() }));

vi.mock('@/config/env', async importOriginal => {
  const actual = await importOriginal<typeof import('@/config/env')>();
  return { ...actual, env: { ...actual.env } };
});
vi.mock('@/features/auth/hooks/use-auth', () => ({ useRegister: () => mutation }));
vi.mock('@/features/auth/services/auth-service', () => ({ authService: service }));
vi.mock('next/navigation', () => ({
  useSearchParams: () => new URLSearchParams(window.location.search),
}));

function renderForm() {
  return render(
    <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
      <RegisterForm />
    </NextIntlClientProvider>
  );
}

async function sendCode(email = 'ann@example.com') {
  fireEvent.change(screen.getByLabelText('Email'), { target: { value: ` ${email} ` } });
  fireEvent.click(screen.getByRole('button', { name: 'Send code' }));
  await screen.findByLabelText('Verification code');
}

describe('two-step registration form', () => {
  beforeEach(() => {
    mutation.error = null;
    mutation.isPending = false;
    mutation.mutate.mockReset();
    mutation.reset.mockReset();
    service.requestRegistrationCode.mockReset();
    service.requestRegistrationCode.mockResolvedValue({ success: true });
    env.NEXT_PUBLIC_TERMS_URL = 'https://legal.example.com/terms';
    env.NEXT_PUBLIC_PRIVACY_URL = 'https://legal.example.com/privacy';
  });

  afterEach(() => {
    vi.useRealTimers();
    window.history.replaceState(null, '', '/');
  });

  it('keeps the invitation page an invitee came from on the sign-in link', () => {
    window.history.replaceState(
      null,
      '',
      '/register?returnUrl=%2Fconsole%2Forganizations%3Finvitation%3Doinv_a'
    );
    renderForm();

    expect(screen.getByRole('link', { name: 'Sign In' })).toHaveAttribute(
      'href',
      '/login?returnUrl=%2Fconsole%2Forganizations%3Finvitation%3Doinv_a'
    );
  });

  it('asks for the email first and reveals the rest once a code is sent', async () => {
    renderForm();

    expect(screen.queryByLabelText('Verification code')).toBeNull();
    expect(screen.queryByLabelText('Full Name')).toBeNull();
    expect(screen.queryByLabelText('Password')).toBeNull();

    await sendCode();

    expect(service.requestRegistrationCode).toHaveBeenCalledWith('ann@example.com');
    expect(screen.getByText(/If ann@example.com can be registered/u)).toBeInTheDocument();
    expect(screen.getByLabelText('Verification code')).toHaveAttribute('autocomplete', 'one-time-code');
    expect(screen.getByLabelText('Full Name')).toBeInTheDocument();
    expect(screen.getByLabelText('Password')).toBeInTheDocument();
  });

  it('registers with the code, name, email, and password', async () => {
    renderForm();
    await sendCode();

    fireEvent.change(screen.getByLabelText('Verification code'), { target: { value: '04 29 17' } });
    fireEvent.change(screen.getByLabelText('Full Name'), { target: { value: 'Ann' } });
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'secret-123' } });
    fireEvent.click(screen.getByRole('checkbox'));
    fireEvent.click(screen.getByRole('button', { name: 'Sign Up' }));

    expect(mutation.mutate).toHaveBeenCalledWith({
      name: 'Ann',
      email: 'ann@example.com',
      password: 'secret-123',
      code: '042917',
    });
  });

  it('links the consent to the configured Terms of Service and Privacy Policy', async () => {
    renderForm();
    await sendCode();

    const terms = screen.getByRole('link', { name: 'Terms of Service' });
    const privacy = screen.getByRole('link', { name: 'Privacy Policy' });
    expect(terms).toHaveAttribute('href', 'https://legal.example.com/terms');
    expect(privacy).toHaveAttribute('href', 'https://legal.example.com/privacy');
    for (const link of [terms, privacy]) {
      expect(link).toHaveAttribute('target', '_blank');
      expect(link).toHaveAttribute('rel', 'noopener noreferrer');
    }
    expect(screen.getByRole('button', { name: 'Sign Up' })).toBeDisabled();
  });

  it('asks consent only for the one configured document', async () => {
    env.NEXT_PUBLIC_PRIVACY_URL = undefined;
    renderForm();
    await sendCode();

    expect(screen.getByRole('link', { name: 'Terms of Service' })).toBeInTheDocument();
    expect(screen.queryByText(/Privacy Policy/u)).toBeNull();
    expect(screen.getByRole('checkbox')).toBeInTheDocument();
  });

  it('asks no consent when no legal document is configured', async () => {
    env.NEXT_PUBLIC_TERMS_URL = undefined;
    env.NEXT_PUBLIC_PRIVACY_URL = undefined;
    renderForm();
    await sendCode();

    expect(screen.queryByRole('checkbox')).toBeNull();
    expect(screen.queryByText(/Terms of Service/u)).toBeNull();
    fireEvent.change(screen.getByLabelText('Verification code'), { target: { value: '042917' } });
    fireEvent.change(screen.getByLabelText('Full Name'), { target: { value: 'Ann' } });
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'secret-123' } });
    fireEvent.click(screen.getByRole('button', { name: 'Sign Up' }));

    expect(mutation.mutate).toHaveBeenCalledWith({
      name: 'Ann',
      email: 'ann@example.com',
      password: 'secret-123',
      code: '042917',
    });
  });

  it('counts down 60 seconds before the code can be sent again', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    renderForm();
    await sendCode();

    const resend = screen.getByRole('button', { name: 'Resend in 60s' });
    expect(resend).toBeDisabled();

    await act(async () => {
      vi.advanceTimersByTime(30_000);
    });
    expect(screen.getByRole('button', { name: 'Resend in 30s' })).toBeDisabled();

    await act(async () => {
      vi.advanceTimersByTime(30_000);
    });
    const again = screen.getByRole('button', { name: 'Resend code' });
    expect(again).toBeEnabled();
    fireEvent.click(again);
    await waitFor(() => expect(service.requestRegistrationCode).toHaveBeenCalledTimes(2));
  });

  it('shows a localized failure when the code cannot be sent', async () => {
    service.requestRegistrationCode.mockRejectedValueOnce(
      new ApiError('raw', ApiErrorCode.COMMON_SERVICE_UNAVAILABLE, 503)
    );
    renderForm();

    fireEvent.change(screen.getByLabelText('Email'), { target: { value: 'ann@example.com' } });
    fireEvent.click(screen.getByRole('button', { name: 'Send code' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('The code could not be sent');
    expect(screen.queryByLabelText('Verification code')).toBeNull();
    expect(screen.getByRole('button', { name: 'Send code' })).toBeEnabled();
  });

  it.each([
    [ApiErrorCode.AUTH_REGISTRATION_EMAIL_CODE_INVALID, 'This code is wrong or already used.'],
    [ApiErrorCode.AUTH_REGISTRATION_EMAIL_CODE_EXPIRED, 'This code has expired. Send a new one.'],
    [ApiErrorCode.AUTH_REGISTRATION_EMAIL_CODE_REQUIRED, 'Enter the code we emailed you.'],
  ])('marks the code field for %s', async (errorCode, copy) => {
    renderForm();
    await sendCode();

    mutation.error = new ApiError('raw backend detail', errorCode, 400);
    fireEvent.change(screen.getByLabelText('Full Name'), { target: { value: 'Ann' } });

    expect(await screen.findByRole('alert')).toHaveTextContent(copy);
    expect(screen.getByLabelText('Verification code')).toHaveAttribute('aria-invalid', 'true');
    expect(screen.queryByText('raw backend detail')).toBeNull();
  });
});
