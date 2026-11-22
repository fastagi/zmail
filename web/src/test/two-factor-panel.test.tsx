import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { TwoFactorPanel } from '@/features/auth/components/two-factor-panel';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { messages } from '@/i18n/modules';

const mocks = vi.hoisted(() => ({
  status: vi.fn(),
  startEnrollment: vi.fn(),
  confirm: vi.fn(),
  disable: vi.fn(),
}));

vi.mock('@/features/auth/services/totp-service', () => ({ totpService: mocks }));
vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

function renderPanel() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
        <TwoFactorPanel />
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
}

describe('TwoFactorPanel', () => {
  beforeEach(() => {
    Object.values(mocks).forEach(mock => mock.mockReset());
  });

  it('enrolls an authenticator, rejects a wrong code, and shows recovery codes once', async () => {
    mocks.status.mockResolvedValue({
      available: true,
      enabled: false,
      recovery_codes_remaining: 0,
    });
    mocks.startEnrollment.mockResolvedValue({
      secret: 'JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP',
      otpauth_uri: 'otpauth://totp/Zmail:ann@example.com?secret=JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP',
    });
    mocks.confirm
      .mockRejectedValueOnce(new ApiError('wrong', ApiErrorCode.AUTH_TOTP_INVALID, 422))
      .mockResolvedValueOnce(['aaaa-bbbb', 'cccc-dddd']);
    renderPanel();

    fireEvent.click(await screen.findByRole('button', { name: 'Set up authenticator app' }));
    expect(await screen.findByRole('link', { name: 'Open in authenticator app' })).toHaveAttribute(
      'href',
      expect.stringContaining('otpauth://totp/')
    );
    expect(screen.getByText('JBSW Y3DP EHPK 3PXP JBSW Y3DP EHPK 3PXP')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Turn on' }));
    expect(await screen.findByText('Enter a code.')).toBeInTheDocument();
    expect(mocks.confirm).not.toHaveBeenCalled();

    fireEvent.change(screen.getByLabelText('Authenticator code'), { target: { value: '111111' } });
    fireEvent.click(screen.getByRole('button', { name: 'Turn on' }));
    expect(
      await screen.findByText('That code is wrong or was already used. Try the next code.')
    ).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('Authenticator code'), { target: { value: '222222' } });
    mocks.status.mockResolvedValue({
      available: true,
      enabled: true,
      confirmed_at: '2026-11-22T00:00:00Z',
      recovery_codes_remaining: 2,
    });
    fireEvent.click(screen.getByRole('button', { name: 'Turn on' }));

    const list = await screen.findByRole('list', { name: 'Save your recovery codes' });
    expect(list).toHaveTextContent('aaaa-bbbb');
    expect(mocks.confirm).toHaveBeenLastCalledWith('222222', expect.anything());
    fireEvent.click(screen.getByRole('button', { name: 'I saved these codes' }));
    expect(await screen.findByText(/2 recovery codes left/)).toBeInTheDocument();
  });

  it('turns two-factor sign-in off with a recovery code', async () => {
    mocks.status.mockResolvedValue({
      available: true,
      enabled: true,
      confirmed_at: '2026-11-22T00:00:00Z',
      recovery_codes_remaining: 10,
    });
    mocks.disable.mockResolvedValue(undefined);
    renderPanel();

    fireEvent.change(await screen.findByLabelText('Authenticator or recovery code'), {
      target: { value: 'aaaa-bbbb' },
    });
    mocks.status.mockResolvedValue({
      available: true,
      enabled: false,
      recovery_codes_remaining: 0,
    });
    fireEvent.click(screen.getByRole('button', { name: 'Turn off' }));

    await waitFor(() => expect(mocks.disable).toHaveBeenCalledWith('aaaa-bbbb', expect.anything()));
    expect(
      await screen.findByRole('button', { name: 'Set up authenticator app' })
    ).toBeInTheDocument();
  });

  it('explains when the server cannot enroll authenticators', async () => {
    mocks.status.mockResolvedValue({
      available: false,
      enabled: false,
      recovery_codes_remaining: 0,
    });
    renderPanel();
    expect(
      await screen.findByText('Two-factor sign-in is not available on this server yet.')
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Set up authenticator app' })
    ).not.toBeInTheDocument();
  });
});
