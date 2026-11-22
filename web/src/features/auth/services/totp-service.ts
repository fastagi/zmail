import request from '@/http';
import { ClientErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';

export interface TotpStatus {
  available: boolean;
  enabled: boolean;
  confirmed_at?: string;
  recovery_codes_remaining: number;
}

export interface TotpEnrollment {
  secret: string;
  otpauth_uri: string;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

function expect<T>(value: unknown, valid: (value: Record<string, unknown>) => boolean, name: string): T {
  if (!isRecord(value) || !valid(value)) {
    throw new ApiError(`Invalid ${name} response`, ClientErrorCode.INVALID_RESPONSE);
  }
  return value as T;
}

/** Two-factor sign-in for the signed-in user, through the same-origin auth routes. */
export const totpService = {
  status: async () =>
    expect<TotpStatus>(
      await request.get<unknown>('/auth/totp'),
      value =>
        typeof value.available === 'boolean' &&
        typeof value.enabled === 'boolean' &&
        typeof value.recovery_codes_remaining === 'number',
      'two-factor status'
    ),

  startEnrollment: async () =>
    expect<TotpEnrollment>(
      await request.post<unknown>('/auth/totp/enrollments'),
      value =>
        typeof value.secret === 'string' &&
        typeof value.otpauth_uri === 'string' &&
        value.otpauth_uri.startsWith('otpauth://totp/'),
      'two-factor enrollment'
    ),

  confirm: async (code: string) =>
    expect<{ recovery_codes: string[] }>(
      await request.put<unknown, { code: string }>('/auth/totp', { code }),
      value =>
        Array.isArray(value.recovery_codes) &&
        value.recovery_codes.every(item => typeof item === 'string'),
      'two-factor confirmation'
    ).recovery_codes,

  disable: (code: string) => request.delete<unknown>('/auth/totp', { data: { code } }),
};
