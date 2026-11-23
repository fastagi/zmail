import { authConfig } from '@/config/auth';
import { resolveReturnUrl } from '@/features/auth/utils/return-url';
import { ApiErrorCode } from '@/http/codes';

/** The BFF route that starts a Google sign-in. */
export const GOOGLE_SIGN_IN_START_PATH = '/api/auth/google';
/** The BFF route Google sends the browser back to; register it in Google Cloud Console. */
export const GOOGLE_SIGN_IN_CALLBACK_PATH = '/api/auth/google/callback';
/** The sign-in page query parameter that carries why a Google sign-in failed. */
export const GOOGLE_SIGN_IN_ERROR_PARAM = 'googleError';
/** The person closed or declined Google's consent page. */
export const GOOGLE_SIGN_IN_CANCELLED = 'cancelled';

export type GoogleSignInErrorKey =
  | 'auth.googleCancelled'
  | 'auth.googleEmailUnverified'
  | 'auth.googleRegistrationClosed'
  | 'auth.googleAccountUnavailable'
  | 'auth.googleTotpRequired'
  | 'auth.googleStateInvalid'
  | 'auth.googleNotConfigured'
  | 'auth.googleFailed'
  | 'errors.rateLimited';

const errorKeys: Readonly<Record<string, GoogleSignInErrorKey>> = {
  [GOOGLE_SIGN_IN_CANCELLED]: 'auth.googleCancelled',
  [ApiErrorCode.AUTH_GOOGLE_EMAIL_UNVERIFIED]: 'auth.googleEmailUnverified',
  [ApiErrorCode.AUTH_GOOGLE_REGISTRATION_CLOSED]: 'auth.googleRegistrationClosed',
  [ApiErrorCode.AUTH_INVALID_CREDENTIALS]: 'auth.googleAccountUnavailable',
  [ApiErrorCode.AUTH_ACCOUNT_DISABLED]: 'auth.googleAccountUnavailable',
  [ApiErrorCode.AUTH_TOTP_REQUIRED]: 'auth.googleTotpRequired',
  [ApiErrorCode.AUTH_GOOGLE_STATE_INVALID]: 'auth.googleStateInvalid',
  [ApiErrorCode.AUTH_GOOGLE_NOT_CONFIGURED]: 'auth.googleNotConfigured',
  [ApiErrorCode.COMMON_RATE_LIMITED]: 'errors.rateLimited',
};

/**
 * Maps the `googleError` query value to copy. Only known codes are recognized; anything else,
 * including a hand-edited value, shows the generic failure, so the page never echoes the query.
 */
export function resolveGoogleSignInErrorKey(value: string | null): GoogleSignInErrorKey | null {
  if (!value) return null;
  return Object.hasOwn(errorKeys, value) ? errorKeys[value] : 'auth.googleFailed';
}

/** The start link, carrying a same-origin return URL only. */
export function googleSignInStartHref(returnUrl: string | null): string {
  const resolved = resolveReturnUrl(returnUrl);
  if (!returnUrl || resolved === authConfig.routes.afterLogin) return GOOGLE_SIGN_IN_START_PATH;
  return `${GOOGLE_SIGN_IN_START_PATH}?returnUrl=${encodeURIComponent(resolved)}`;
}
