import 'server-only';

import type { AuthResponse, AuthUser, LoginRequest, RegisterRequest } from '@/features/auth/types';
import {
  authenticationSessionMaxAgeSeconds,
  isOpaqueAuthenticationCredential,
} from '@/features/auth/server/auth-credential';
import { ApiErrorCode, type ApiErrorCodeValue } from '@/http/codes';
import { GoApiClient, type GoApiError } from '@/server/api-adapter/go-api-client';

export interface GoApiAuthAdapterConfig {
  apiUrl: string;
  timeoutMs: number;
  maxResponseBytes?: number;
  clientIpHeader?: string;
}

interface AdapterDependencies {
  fetch: typeof fetch;
  randomUUID: () => string;
}

export interface AdapterError {
  status: number;
  errorCode: ApiErrorCodeValue;
  message: string;
  fieldErrors?: Record<string, string[]>;
  requestId?: string;
  responseHeaders?: Record<string, string>;
}

export type AdapterResult<T> = { ok: true; data: T } | { ok: false; error: AdapterError };

export interface AdapterSession extends AuthResponse {
  accessToken: string;
  maxAgeSeconds: number;
}

type AdapterOperation =
  | 'login'
  | 'logout'
  | 'register'
  | 'register-email-code'
  | 'session'
  | 'password-reset'
  | 'password-reset-confirm'
  | 'password-change'
  | 'profile-update'
  | 'providers'
  | 'google-start'
  | 'google-session';

/** Where to send the browser for Google sign-in and the state to bind to it. */
export interface GoogleAuthorization {
  authorizationUrl: string;
  state: string;
  expiresIn: number;
}

/** A Google sign-in session plus the return URL the API stored when it started. */
export interface GoogleAdapterSession extends AdapterSession {
  returnUrl: string;
}

const GOOGLE_STATE_PATTERN = /^[A-Za-z0-9_-]{16,128}$/;
const MAX_GOOGLE_AUTHORIZATION_URL_LENGTH = 4096;

interface GoApiUser {
  id: number;
  username: string;
  email: string;
  nickname?: string;
  status: number;
}

interface UpstreamSuccess {
  data: unknown;
  requestId: string;
}

export class GoApiAuthAdapter {
  private readonly dependencies: AdapterDependencies;
  private readonly client: GoApiClient;

  constructor(config: GoApiAuthAdapterConfig, dependencies: Partial<AdapterDependencies> = {}) {
    this.dependencies = {
      fetch: dependencies.fetch ?? fetch,
      randomUUID: dependencies.randomUUID ?? (() => crypto.randomUUID()),
    };
    this.client = new GoApiClient(
      {
        apiUrl: config.apiUrl,
        timeoutMs: config.timeoutMs,
        maxResponseBytes: config.maxResponseBytes ?? 1_048_576,
        clientIpHeader: config.clientIpHeader,
      },
      {
        fetch: this.dependencies.fetch,
        randomUUID: this.dependencies.randomUUID,
      }
    );
  }

  async login(
    input: LoginRequest,
    incomingHeaders: Headers,
    signal?: AbortSignal
  ): Promise<AdapterResult<AdapterSession>> {
    const result = await this.request(
      'login',
      'login',
      {
        method: 'POST',
        body: {
          username: input.email,
          password: input.password,
          ...(input.totpCode ? { totp_code: input.totpCode } : {}),
        },
      },
      incomingHeaders,
      signal
    );

    if (!result.ok) {
      return result;
    }

    return this.sessionFromLoginData(result.data.data, result.data.requestId);
  }

  async register(
    input: RegisterRequest,
    incomingHeaders: Headers,
    signal?: AbortSignal
  ): Promise<AdapterResult<AdapterSession>> {
    const registration = await this.request(
      'register',
      'register',
      {
        method: 'POST',
        body: {
          username: generatedUsername(this.dependencies.randomUUID()),
          password: input.password,
          email: input.email,
          nickname: input.name,
          email_code: input.code,
        },
      },
      incomingHeaders,
      signal
    );

    if (!registration.ok) {
      return registration;
    }

    if (!mapGoApiUser(registration.data.data)) {
      return adapterUnavailable(registration.data.requestId);
    }

    return this.login({ email: input.email, password: input.password }, incomingHeaders, signal);
  }

  async profile(
    accessToken: string,
    incomingHeaders: Headers,
    signal?: AbortSignal
  ): Promise<AdapterResult<AuthResponse>> {
    const result = await this.request(
      'session',
      'users/profile',
      {
        method: 'GET',
        accessToken,
      },
      incomingHeaders,
      signal
    );

    if (!result.ok) {
      return result;
    }

    const user = mapGoApiUser(result.data.data);
    if (!user) {
      return adapterUnavailable(result.data.requestId);
    }
    if (!isActiveGoApiUser(result.data.data)) {
      return accountDisabled(result.data.requestId);
    }

    return { ok: true, data: { user } };
  }

  async logout(
    accessToken: string,
    incomingHeaders: Headers,
    signal?: AbortSignal
  ): Promise<AdapterResult<{ success: true }>> {
    if (!isOpaqueAuthenticationCredential(accessToken)) {
      return adapterUnavailable(this.dependencies.randomUUID());
    }

    const result = await this.request(
      'logout',
      'logout',
      {
        method: 'POST',
        accessToken,
      },
      incomingHeaders,
      signal
    );

    if (!result.ok) {
      return result;
    }
    if (!isRecord(result.data.data) || result.data.data.success !== true) {
      return adapterUnavailable(result.data.requestId);
    }

    return { ok: true, data: { success: true } };
  }

  /** Asks the API to mail a registration code. The API answers the same whether the address is taken. */
  async requestRegistrationEmailCode(
    email: string,
    incomingHeaders: Headers,
    signal?: AbortSignal
  ): Promise<AdapterResult<{ success: true }>> {
    const result = await this.request(
      'register-email-code',
      'register/email-codes',
      { method: 'POST', body: { email } },
      incomingHeaders,
      signal
    );
    return result.ok ? { ok: true, data: { success: true } } : result;
  }

  /** Asks the API to email a reset token. The API answers the same whether the address exists. */
  async requestPasswordReset(
    email: string,
    incomingHeaders: Headers,
    signal?: AbortSignal
  ): Promise<AdapterResult<{ success: true }>> {
    const result = await this.request(
      'password-reset',
      'password/reset',
      { method: 'POST', body: { email } },
      incomingHeaders,
      signal
    );
    return result.ok ? { ok: true, data: { success: true } } : result;
  }

  /** Consumes a reset token and sets the new password; every session of the account ends. */
  async confirmPasswordReset(
    input: { token: string; password: string },
    incomingHeaders: Headers,
    signal?: AbortSignal
  ): Promise<AdapterResult<{ success: true }>> {
    const result = await this.request(
      'password-reset-confirm',
      'password/reset/confirm',
      { method: 'POST', body: { token: input.token, new_password: input.password } },
      incomingHeaders,
      signal
    );
    return result.ok ? { ok: true, data: { success: true } } : result;
  }

  /** Changes the signed-in user's password; the API then revokes every session of the account. */
  async changePassword(
    accessToken: string,
    input: { currentPassword: string; newPassword: string },
    incomingHeaders: Headers,
    signal?: AbortSignal
  ): Promise<AdapterResult<{ success: true }>> {
    if (!isOpaqueAuthenticationCredential(accessToken)) {
      return adapterUnavailable(this.dependencies.randomUUID());
    }
    const result = await this.request(
      'password-change',
      'users/password',
      {
        method: 'PUT',
        accessToken,
        body: { old_password: input.currentPassword, new_password: input.newPassword },
      },
      incomingHeaders,
      signal
    );
    return result.ok ? { ok: true, data: { success: true } } : result;
  }

  /** Renames the signed-in user; the API leaves the fields it is not sent unchanged. */
  async updateProfile(
    accessToken: string,
    input: { name: string },
    incomingHeaders: Headers,
    signal?: AbortSignal
  ): Promise<AdapterResult<AuthResponse>> {
    if (!isOpaqueAuthenticationCredential(accessToken)) {
      return adapterUnavailable(this.dependencies.randomUUID());
    }
    const result = await this.request(
      'profile-update',
      'users/profile',
      { method: 'PUT', accessToken, body: { nickname: input.name } },
      incomingHeaders,
      signal
    );
    if (!result.ok) {
      return result;
    }
    const user = mapGoApiUser(result.data.data);
    return user ? { ok: true, data: { user } } : adapterUnavailable(result.data.requestId);
  }

  /** Reports whether the API has a Google OAuth client configured. */
  async googleSignInEnabled(incomingHeaders: Headers, signal?: AbortSignal): Promise<boolean> {
    const result = await this.request('providers', 'auth/providers', { method: 'GET' }, incomingHeaders, signal);
    if (!result.ok || !isRecord(result.data.data) || !isRecord(result.data.data.google)) {
      return false;
    }
    return result.data.data.google.enabled === true;
  }

  /** Starts a Google sign-in; the API stores the state, nonce, PKCE verifier, and return URL. */
  async startGoogleSignIn(
    input: { redirectUri: string; returnUrl: string },
    incomingHeaders: Headers,
    signal?: AbortSignal
  ): Promise<AdapterResult<GoogleAuthorization>> {
    const result = await this.request(
      'google-start',
      'auth/google/authorizations',
      { method: 'POST', body: { redirect_uri: input.redirectUri, return_url: input.returnUrl } },
      incomingHeaders,
      signal
    );
    if (!result.ok) {
      return result;
    }
    const data = result.data.data;
    if (
      !isRecord(data) ||
      typeof data.state !== 'string' ||
      !GOOGLE_STATE_PATTERN.test(data.state) ||
      !isGoogleAuthorizationUrl(data.authorization_url) ||
      typeof data.expires_in !== 'number' ||
      !Number.isSafeInteger(data.expires_in) ||
      data.expires_in < 1
    ) {
      return adapterUnavailable(result.data.requestId);
    }
    return {
      ok: true,
      data: { authorizationUrl: data.authorization_url, state: data.state, expiresIn: data.expires_in },
    };
  }

  /** Completes a Google sign-in; the API issues a session exactly like a password login. */
  async completeGoogleSignIn(
    input: { state: string; code: string },
    incomingHeaders: Headers,
    signal?: AbortSignal
  ): Promise<AdapterResult<GoogleAdapterSession>> {
    const result = await this.request(
      'google-session',
      'auth/google/sessions',
      { method: 'POST', body: { state: input.state, code: input.code } },
      incomingHeaders,
      signal
    );
    if (!result.ok) {
      return result;
    }
    const session = this.sessionFromLoginData(result.data.data, result.data.requestId);
    if (!session.ok) {
      return session;
    }
    const returnUrl = isRecord(result.data.data) ? result.data.data.return_url : undefined;
    return {
      ok: true,
      data: { ...session.data, returnUrl: typeof returnUrl === 'string' ? returnUrl : '' },
    };
  }

  private sessionFromLoginData(value: unknown, requestId: string): AdapterResult<AdapterSession> {
    if (!isRecord(value)) {
      return adapterUnavailable(requestId);
    }

    const user = mapGoApiUser(value.user);
    const maxAgeSeconds = authenticationSessionMaxAgeSeconds(value.expires_in);

    if (
      !user ||
      !isOpaqueAuthenticationCredential(value.access_token) ||
      value.token_type !== 'Bearer' ||
      !maxAgeSeconds
    ) {
      return adapterUnavailable(requestId);
    }
    if (!isActiveGoApiUser(value.user)) {
      return accountDisabled(requestId);
    }

    return {
      ok: true,
      data: {
        user,
        accessToken: value.access_token,
        maxAgeSeconds,
      },
    };
  }

  private async request(
    operation: AdapterOperation,
    path: string,
    options: {
      method: 'GET' | 'POST' | 'PUT';
      body?: Record<string, string>;
      accessToken?: string;
    },
    incomingHeaders: Headers,
    signal?: AbortSignal
  ): Promise<AdapterResult<UpstreamSuccess>> {
    const result = await this.client.request({
      method: options.method,
      path,
      incomingHeaders,
      body: options.body,
      accessToken: options.accessToken,
      fieldMap: authFieldMap(operation),
      signal,
    });
    if (!result.ok) {
      return { ok: false, error: authenticationError(operation, result.error) };
    }
    if (!result.data.envelope) {
      return adapterUnavailable(result.data.requestId);
    }

    return {
      ok: true,
      data: {
        data: result.data.envelope.data,
        requestId: result.data.requestId,
      },
    };
  }
}

export function generatedUsername(randomId: string): string {
  const suffix = randomId
    .toLowerCase()
    .replace(/[^a-z0-9]/g, '')
    .slice(0, 32);

  if (suffix.length < 16) {
    throw new Error('Generated auth username requires at least 16 random characters');
  }

  return `user_${suffix}`;
}

export function mapGoApiUser(value: unknown): AuthUser | null {
  if (!isGoApiUser(value)) {
    return null;
  }

  return {
    id: String(value.id),
    email: value.email,
    name: value.nickname?.trim() || value.username,
  };
}

function isGoApiUser(value: unknown): value is GoApiUser {
  if (!isRecord(value)) {
    return false;
  }

  return (
    typeof value.id === 'number' &&
    Number.isSafeInteger(value.id) &&
    value.id > 0 &&
    isNonEmptyString(value.username) &&
    isNonEmptyString(value.email) &&
    (value.nickname === undefined || typeof value.nickname === 'string') &&
    typeof value.status === 'number'
  );
}

function isActiveGoApiUser(value: unknown): boolean {
  return isGoApiUser(value) && value.status === 1;
}

function authFieldMap(operation: AdapterOperation): Readonly<Record<string, string>> {
  switch (operation) {
    case 'register':
      return { email: 'email', nickname: 'name', password: 'password', email_code: 'code' };
    case 'register-email-code':
      return { email: 'email' };
    case 'login':
      return { username: 'email', password: 'password', totp_code: 'totpCode' };
    case 'password-reset':
      return { email: 'email' };
    case 'password-reset-confirm':
      return { token: 'token', new_password: 'password' };
    case 'password-change':
      return { old_password: 'currentPassword', new_password: 'newPassword' };
    case 'profile-update':
      return { nickname: 'name' };
    case 'google-session':
      return { state: 'state', code: 'code' };
    default:
      return {};
  }
}

function authenticationError(operation: AdapterOperation, error: GoApiError): AdapterError {
  const message =
    error.cause === 'timeout'
      ? 'Authentication service timed out'
      : error.cause === 'unavailable'
        ? 'Authentication service unavailable'
        : genericErrorMessage(operation, error.status);

  return {
    status: error.status,
    errorCode: error.errorCode,
    message,
    ...(error.fieldErrors ? { fieldErrors: error.fieldErrors } : {}),
    requestId: error.requestId,
    ...(error.responseHeaders ? { responseHeaders: error.responseHeaders } : {}),
  };
}

function genericErrorMessage(operation: AdapterOperation, status: number): string {
  if (status === 401) {
    return operation === 'session' ? 'Authentication required' : 'Authentication failed';
  }
  if (status === 403) {
    return 'Access forbidden';
  }
  if (status === 409) {
    return 'Registration conflict';
  }
  if (status === 400 || status === 422) {
    return 'Invalid authentication input';
  }
  if (status === 429) {
    return 'Too many authentication attempts';
  }
  return 'Authentication service unavailable';
}

/** Accepts only an absolute https URL of bounded length: Google's authorization page. */
function isGoogleAuthorizationUrl(value: unknown): value is string {
  if (typeof value !== 'string' || value.length > MAX_GOOGLE_AUTHORIZATION_URL_LENGTH) {
    return false;
  }
  try {
    const url = new URL(value);
    return url.protocol === 'https:';
  } catch {
    return false;
  }
}

function accountDisabled(requestId: string): AdapterResult<never> {
  return {
    ok: false,
    error: {
      status: 403,
      errorCode: ApiErrorCode.AUTH_ACCOUNT_DISABLED,
      message: 'Account access is disabled',
      requestId,
    },
  };
}

function adapterUnavailable(requestId: string): AdapterResult<never> {
  return {
    ok: false,
    error: {
      status: 503,
      errorCode: ApiErrorCode.COMMON_SERVICE_UNAVAILABLE,
      message: 'Authentication service unavailable',
      requestId,
    },
  };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

function isNonEmptyString(value: unknown): value is string {
  return typeof value === 'string' && value.trim().length > 0;
}
