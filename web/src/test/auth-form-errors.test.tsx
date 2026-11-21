import { fireEvent, render, screen } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { LoginForm } from '@/features/auth/components/login-form';
import { RegisterForm } from '@/features/auth/components/register-form';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { messages } from '@/i18n/modules';

const mutations = vi.hoisted(() => ({
  login: {
    error: null as unknown,
    isPending: false,
    mutate: vi.fn(),
    reset: vi.fn(),
  },
  register: {
    error: null as unknown,
    isPending: false,
    mutate: vi.fn(),
    reset: vi.fn(),
  },
}));

// The registration consent checkbox appears only when a legal document is configured.
vi.mock('@/config/env', async importOriginal => {
  const actual = await importOriginal<typeof import('@/config/env')>();
  return {
    ...actual,
    env: { ...actual.env, NEXT_PUBLIC_TERMS_URL: 'https://legal.example.com/terms' },
  };
});
vi.mock('@/features/auth/hooks/use-auth', () => ({
  useLogin: () => mutations.login,
  useRegister: () => mutations.register,
}));

vi.mock('@/features/auth/services/auth-service', () => ({
  authService: { requestRegistrationCode: vi.fn().mockResolvedValue({ success: true }) },
}));

/** Registration details appear once a code is sent; the error under test then renders with them. */
async function sendRegistrationCode(error: unknown) {
  fireEvent.change(screen.getByLabelText('Email'), { target: { value: 'ann@example.com' } });
  fireEvent.click(screen.getByRole('button', { name: 'Send code' }));
  await screen.findByLabelText('Full Name');
  mutations.register.error = error;
  fireEvent.change(screen.getByLabelText('Verification code'), { target: { value: '1' } });
}

function renderWithMessages(children: React.ReactNode) {
  return render(
    <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
      {children}
    </NextIntlClientProvider>
  );
}

describe('auth form error feedback', () => {
  beforeEach(() => {
    mutations.login.error = null;
    mutations.login.isPending = false;
    mutations.login.mutate.mockReset();
    mutations.login.reset.mockReset();
    mutations.register.error = null;
    mutations.register.isPending = false;
    mutations.register.mutate.mockReset();
    mutations.register.reset.mockReset();
  });

  it('shows localized invalid-credential feedback and clears it on edit', () => {
    mutations.login.error = new ApiError(
      'backend credential detail',
      ApiErrorCode.AUTH_INVALID_CREDENTIALS,
      401
    );

    renderWithMessages(<LoginForm />);

    expect(screen.getByRole('alert')).toHaveTextContent('Invalid email or password');
    expect(screen.queryByText('backend credential detail')).not.toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('Email'), {
      target: { value: 'new@example.com' },
    });
    expect(mutations.login.reset).toHaveBeenCalledTimes(1);
  });

  it('keeps real-backend login blank and free of mock credentials by default', () => {
    renderWithMessages(<LoginForm />);

    expect(screen.getByLabelText('Email')).toHaveValue('');
    expect(screen.getByLabelText('Password')).toHaveValue('');
    expect(screen.queryByText('Demo credentials')).not.toBeInTheDocument();
    expect(screen.queryByText('Use the preset login')).not.toBeInTheDocument();
  });

  it('offers sign-up only while registration is open and never a dead provider button', () => {
    const { unmount } = renderWithMessages(<LoginForm />);
    expect(screen.getByRole('link', { name: 'Sign Up' })).toHaveAttribute('href', '/register');
    expect(screen.queryByRole('button', { name: 'GitHub' })).toBeNull();
    unmount();

    renderWithMessages(<LoginForm registrationEnabled={false} />);
    expect(screen.queryByRole('link', { name: 'Sign Up' })).toBeNull();
    expect(screen.getByText(/Ask an organization admin to invite you/u)).toBeInTheDocument();
  });

  it('shows and prefills credentials only when the server enables the mock preset', () => {
    renderWithMessages(
      <LoginForm
        demoCredentials={{
          email: 'demo@example.test',
          password: 'demo-password',
        }}
      />
    );

    expect(screen.getByLabelText('Email')).toHaveValue('demo@example.test');
    expect(screen.getByLabelText('Password')).toHaveValue('demo-password');
    expect(screen.getByText('Demo credentials')).toBeInTheDocument();
    expect(screen.getByText('demo@example.test / demo-password')).toBeInTheDocument();
  });

  it('associates server field ownership with localized registration errors', async () => {
    const error = new ApiError(
      'backend validation detail',
      ApiErrorCode.COMMON_VALIDATION_FAILED,
      422,
      'request-1',
      {
        email: ['backend email detail'],
        name: ['backend name detail'],
        password: ['backend password detail'],
      }
    );

    renderWithMessages(<RegisterForm />);
    await sendRegistrationCode(error);

    expect(screen.getByRole('alert')).toHaveTextContent('Please review the highlighted fields');
    expect(screen.getByLabelText('Full Name')).toHaveAttribute('aria-invalid', 'true');
    expect(screen.getByLabelText('Email')).toHaveAttribute('aria-invalid', 'true');
    expect(screen.getByLabelText('Password')).toHaveAttribute('aria-invalid', 'true');
    expect(screen.getByText('Please enter a valid full name')).toBeInTheDocument();
    expect(screen.getByText('Please enter a valid email address')).toBeInTheDocument();
    expect(screen.getByText('Please enter a valid password')).toBeInTheDocument();
    expect(screen.queryByText('backend name detail')).not.toBeInTheDocument();
  });

  it('exposes login metadata and a stable pending state to the browser', () => {
    mutations.login.isPending = true;

    renderWithMessages(<LoginForm />);

    const email = screen.getByLabelText('Email');
    expect(email).toHaveAttribute('name', 'email');
    expect(email).toHaveAttribute('autocomplete', 'email');
    expect(email).toBeDisabled();
    expect(screen.getByLabelText('Password')).toHaveAttribute(
      'autocomplete',
      'current-password'
    );
    expect(email.closest('form')).toHaveAttribute('aria-busy', 'true');
  });

  it('exposes registration metadata and disables edits while pending', async () => {
    renderWithMessages(<RegisterForm />);
    await sendRegistrationCode(null);
    mutations.register.isPending = true;
    fireEvent.change(screen.getByLabelText('Verification code'), { target: { value: '12' } });

    const name = screen.getByLabelText('Full Name');
    expect(name).toHaveAttribute('name', 'name');
    expect(name).toHaveAttribute('autocomplete', 'name');
    expect(name).toBeDisabled();
    expect(screen.getByLabelText('Email')).toHaveAttribute(
      'autocomplete',
      'email'
    );
    expect(screen.getByLabelText('Password')).toHaveAttribute(
      'autocomplete',
      'new-password'
    );
    expect(screen.getByRole('checkbox')).toBeDisabled();
    expect(name.closest('form')).toHaveAttribute('aria-busy', 'true');
  });
});
