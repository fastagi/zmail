import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ProfilePanel } from '@/features/auth/components/profile-panel';
import { ApiError } from '@/http/request';
import { messages } from '@/i18n/modules';

const mocks = vi.hoisted(() => ({
  updateProfile: vi.fn(),
  setUser: vi.fn(),
  toastSuccess: vi.fn(),
}));

vi.mock('@/features/auth/services/auth-service', () => ({
  authService: { updateProfile: mocks.updateProfile },
}));
vi.mock('@/features/auth/store/auth-store', () => ({
  useAuthStore: {
    use: {
      user: () => ({ id: '7', email: 'ada@example.com', name: 'Ada' }),
      setUser: () => mocks.setUser,
    },
  },
}));
vi.mock('sonner', () => ({ toast: { success: mocks.toastSuccess, error: vi.fn() } }));

function renderPanel() {
  return render(
    <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
      <ProfilePanel />
    </NextIntlClientProvider>
  );
}

describe('ProfilePanel', () => {
  beforeEach(() => {
    mocks.updateProfile.mockReset();
    mocks.setUser.mockReset();
    mocks.toastSuccess.mockReset();
  });

  it('shows the current name and sign-in email, and saves a new name', async () => {
    const renamed = { id: '7', email: 'ada@example.com', name: 'Ada Lovelace' };
    mocks.updateProfile.mockResolvedValue({ user: renamed });
    renderPanel();

    expect(screen.getByLabelText('Sign-in email')).toHaveValue('ada@example.com');
    const save = screen.getByRole('button', { name: 'Save profile' });
    expect(save).toBeDisabled();

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: ' Ada Lovelace ' } });
    fireEvent.click(save);

    await waitFor(() => expect(mocks.setUser).toHaveBeenCalledWith(renamed));
    expect(mocks.updateProfile).toHaveBeenCalledWith('Ada Lovelace');
    expect(mocks.toastSuccess).toHaveBeenCalledWith('Profile saved');
  });

  it('asks for a name instead of saving a blank one', () => {
    renderPanel();

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: '  ' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save profile' }));

    expect(screen.getByRole('alert')).toHaveTextContent('Enter your name.');
    expect(mocks.updateProfile).not.toHaveBeenCalled();
  });

  it('says when the session ended', async () => {
    mocks.updateProfile.mockRejectedValue(new ApiError('expired', 'AUTH.UNAUTHORIZED', 401));
    renderPanel();

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Ada L' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save profile' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('Your session has ended.');
  });
});
