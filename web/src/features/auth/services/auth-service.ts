import request from '@/http';
import type {
  AuthResponse,
  LoginRequest,
  LogoutResponse,
  RegisterRequest,
} from '@/features/auth/types';
import { isAuthResponse, isLogoutResponse } from '@/features/auth/utils/auth-response';
import { ClientErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';

async function validateResponse<T>(
  requestPromise: Promise<unknown>,
  isValid: (value: unknown) => value is T,
  contractName: string
): Promise<T> {
  const response = await requestPromise;

  if (!isValid(response)) {
    throw new ApiError(`Invalid ${contractName} response`, ClientErrorCode.INVALID_RESPONSE);
  }

  return response;
}

export const authService = {
  login: (data: LoginRequest) =>
    validateResponse<AuthResponse>(
      request.post<unknown, LoginRequest>('/auth/login', data),
      isAuthResponse,
      'login'
    ),

  register: (data: RegisterRequest) =>
    validateResponse<AuthResponse>(
      request.post<unknown, RegisterRequest>('/auth/register', data),
      isAuthResponse,
      'registration'
    ),

  me: () =>
    validateResponse<AuthResponse>(
      request.get<unknown>('/auth/me'),
      isAuthResponse,
      'current-session'
    ),

  logout: () =>
    validateResponse<LogoutResponse>(
      request.post<unknown, undefined>('/auth/logout'),
      isLogoutResponse,
      'logout'
    ),

  /** Mails a registration code when the address can register; the answer is the same either way. */
  requestRegistrationCode: (email: string) =>
    validateResponse<LogoutResponse>(
      request.post<unknown, { email: string }>('/auth/register/email-code', { email }),
      isLogoutResponse,
      'registration-code'
    ),

  /** Emails a reset token when the address has an account; the answer is the same either way. */
  requestPasswordReset: (email: string) =>
    validateResponse<LogoutResponse>(
      request.post<unknown, { email: string }>('/auth/password-reset', { email }),
      isLogoutResponse,
      'password-reset'
    ),

  confirmPasswordReset: (token: string, password: string) =>
    validateResponse<LogoutResponse>(
      request.post<unknown, { token: string; password: string }>('/auth/password-reset/confirm', {
        token,
        password,
      }),
      isLogoutResponse,
      'password-reset-confirm'
    ),

  /** Renames the signed-in user and returns the user as now stored. */
  updateProfile: (name: string) =>
    validateResponse<AuthResponse>(
      request.put<unknown, { name: string }>('/auth/me', { name }),
      isAuthResponse,
      'profile-update'
    ),

  /** Changes the signed-in user's password; every session of the account ends, this one included. */
  changePassword: (currentPassword: string, newPassword: string) =>
    validateResponse<LogoutResponse>(
      request.put<unknown, { currentPassword: string; newPassword: string }>('/auth/password', {
        currentPassword,
        newPassword,
      }),
      isLogoutResponse,
      'password-change'
    ),
};

export type AuthService = typeof authService;
