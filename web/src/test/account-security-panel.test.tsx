import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { AccountSecurityPanel } from '@/features/auth/components/account-security-panel';
import { ApiError } from '@/http/request';
import { messages } from '@/i18n/modules';

const mocks = vi.hoisted(() => ({
  changePassword: vi.fn(),
  replace: vi.fn(),
  reset: vi.fn(),
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
}));

vi.mock('next/navigation', () => ({
  useRouter: () => ({ replace: mocks.replace, push: vi.fn() }),
}));
vi.mock('@/features/auth/services/auth-service', () => ({
  authService: { changePassword: mocks.changePassword },
}));
vi.mock('@/features/auth/store/auth-store', () => ({
  useAuthStore: { use: { reset: () => mocks.reset } },
}));
vi.mock('sonner', () => ({
  toast: { success: mocks.toastSuccess, error: mocks.toastError },
}));

function renderPanel() {
  return render(
    <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
      <AccountSecurityPanel />
    </NextIntlClientProvider>
  );
}

function fields() {
  return {
    current: screen.getByLabelText('Current password') as HTMLInputElement,
    next: screen.getByLabelText('New password') as HTMLInputElement,
    confirm: screen.getByLabelText('Confirm new password') as HTMLInputElement,
  };
}

function fill(current: string, next: string, confirm = next) {
  const inputs = fields();
  fireEvent.change(inputs.current, { target: { value: current } });
  fireEvent.change(inputs.next, { target: { value: next } });
  fireEvent.change(inputs.confirm, { target: { value: confirm } });
  return inputs;
}

function submit() {
  fireEvent.click(screen.getByRole('button', { name: 'Change password' }));
}

function expectCleared(inputs: ReturnType<typeof fields>) {
  expect(inputs.current).toHaveValue('');
  expect(inputs.next).toHaveValue('');
  expect(inputs.confirm).toHaveValue('');
}

describe('AccountSecurityPanel', () => {
  beforeEach(() => {
    for (const mock of Object.values(mocks)) mock.mockReset();
  });

  it('changes the password, clears the fields, and sends the user to sign in again', async () => {
    mocks.changePassword.mockResolvedValue({ success: true });
    renderPanel();
    const inputs = fill('old-secret', 'new-secret');
    submit();

    await waitFor(() => expect(mocks.replace).toHaveBeenCalled());
    expect(mocks.changePassword).toHaveBeenCalledWith('old-secret', 'new-secret');
    expectCleared(inputs);
    expect(mocks.reset).toHaveBeenCalled();
    expect(mocks.replace).toHaveBeenCalledWith('/login?returnUrl=%2Fconsole%2Fsettings');
    expect(mocks.toastSuccess).toHaveBeenCalledWith(
      expect.stringContaining('Every session has been signed out')
    );
  });

  it.each([
    ['', 'new-secret', 'new-secret', 'Current password', 'Enter your current password.'],
    ['old-secret', '12345', '12345', 'New password', 'Use 6 to 50 characters.'],
    [
      'same-secret',
      'same-secret',
      'same-secret',
      'New password',
      'Choose a password different from the current one.',
    ],
    ['old-secret', 'new-secret', 'other-secret', 'Confirm new password', 'The passwords do not match.'],
  ])(
    'validates locally before sending (%#)',
    async (current, next, confirm, label, message) => {
      renderPanel();
      fill(current, next, confirm);
      submit();

      const field = screen.getByLabelText(label);
      expect(field).toHaveAttribute('aria-invalid', 'true');
      expect(field).toHaveAccessibleDescription(message);
      expect(field).toHaveFocus();
      expect(mocks.changePassword).not.toHaveBeenCalled();
    }
  );

  it('shows a wrong current password on its field and keeps the user signed in', async () => {
    mocks.changePassword.mockRejectedValue(
      new ApiError('Authentication failed', 'AUTH.INVALID_CREDENTIALS', 401)
    );
    renderPanel();
    const inputs = fill('wrong-secret', 'new-secret');
    submit();

    await waitFor(() =>
      expect(inputs.current).toHaveAccessibleDescription('The current password is incorrect.')
    );
    expect(inputs.current).toHaveAttribute('aria-invalid', 'true');
    expect(inputs.current).toHaveFocus();
    expectCleared(inputs);
    expect(mocks.reset).not.toHaveBeenCalled();
    expect(mocks.replace).not.toHaveBeenCalled();
  });

  it('sends the user to sign in when the session has ended', async () => {
    mocks.changePassword.mockRejectedValue(
      new ApiError('Authentication required', 'AUTH.UNAUTHORIZED', 401)
    );
    renderPanel();
    const inputs = fill('old-secret', 'new-secret');
    submit();

    await waitFor(() => expect(mocks.replace).toHaveBeenCalled());
    expect(mocks.reset).toHaveBeenCalled();
    expect(mocks.toastError).toHaveBeenCalled();
    expectCleared(inputs);
  });

  it('puts a server-side new password rejection on the new password field', async () => {
    mocks.changePassword.mockRejectedValue(
      new ApiError('Invalid', 'COMMON.VALIDATION_FAILED', 422, undefined, {
        newPassword: ['rejected'],
      })
    );
    renderPanel();
    const inputs = fill('old-secret', 'new-secret');
    submit();

    await waitFor(() => expect(inputs.next).toHaveAttribute('aria-invalid', 'true'));
    expect(inputs.next).toHaveAccessibleDescription(/This password cannot be used/);
    expectCleared(inputs);
  });

  it.each([
    [new ApiError('Too many', 'COMMON.RATE_LIMITED', 429), 'Too many attempts'],
    [new ApiError('Down', 'COMMON.SERVICE_UNAVAILABLE', 503), 'could not be changed'],
  ])('reports other failures in an alert (%#)', async (error, text) => {
    mocks.changePassword.mockRejectedValue(error);
    renderPanel();
    const inputs = fill('old-secret', 'new-secret');
    submit();

    expect(await screen.findByRole('alert')).toHaveTextContent(text);
    expectCleared(inputs);
    expect(mocks.replace).not.toHaveBeenCalled();
  });
});
