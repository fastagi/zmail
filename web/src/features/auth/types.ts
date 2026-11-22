export interface AuthUser {
  id: string;
  email: string;
  name: string;
}

/**
 * Serializable result passed from a protected Server Component to its
 * request-scoped client auth store.
 */
export type AuthBootstrap =
  | { status: 'authenticated'; user: AuthUser }
  | { status: 'unauthenticated' }
  | { status: 'forbidden' }
  | { status: 'unavailable' }
  | { status: 'client-required' };

export type AuthStatus =
  'idle' | 'loading' | 'authenticated' | 'unauthenticated' | 'forbidden' | 'unavailable';

export interface LoginRequest {
  email: string;
  password: string;
  /** Authenticator or recovery code for an account with two-factor sign-in. */
  totpCode?: string;
}

export interface RegisterRequest {
  name: string;
  email: string;
  password: string;
  /** The 6-digit code mailed to `email` by the registration-code request. */
  code: string;
}

export interface AuthResponse {
  user: AuthUser;
}

export interface LogoutResponse {
  success: true;
}
