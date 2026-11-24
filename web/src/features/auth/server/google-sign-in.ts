import 'server-only';

import { timingSafeEqual } from 'node:crypto';

import { cookies, headers } from 'next/headers';
import { NextResponse } from 'next/server';

import { authConfig } from '@/config/auth';
import { env } from '@/config/env';
import { serverEnv } from '@/config/server-env';
import { getAuthSessionCookieName } from '@/config/auth-session';
import { setApiSessionCookie } from '@/features/auth/server/api-session';
import { configuredAdapter } from '@/features/auth/server/auth-adapter-route';
import { privateAuthResponse } from '@/features/auth/server/auth-response';
import { clearSessionCookie } from '@/features/auth/server/session';
import { resolveReturnUrl } from '@/features/auth/utils/return-url';
import {
  GOOGLE_SIGN_IN_CALLBACK_PATH,
  GOOGLE_SIGN_IN_CANCELLED,
  GOOGLE_SIGN_IN_ERROR_PARAM,
} from '@/features/auth/utils/google-sign-in';
import { EXTERNAL_MAIL_CALLBACK_PATH } from '@/features/mailbox/utils/external-mail';
import { ApiErrorCode } from '@/http/codes';

type NodeEnvironment = typeof env.NODE_ENV;

/** The longest a started Google sign-in is kept in the browser; the API keeps it as long. */
const GOOGLE_STATE_MAX_AGE_SECONDS = 600;
const GOOGLE_STATE_PATTERN = /^[A-Za-z0-9_-]{16,128}$/;
const GOOGLE_CODE_MAX_LENGTH = 2048;
const PROVIDERS_CACHE_MS = 60_000;
const PROVIDERS_FAILURE_CACHE_MS = 10_000;

let providersCache: { enabled: boolean; expiresAt: number } | undefined;

export function getGoogleStateCookieName(environment: NodeEnvironment = env.NODE_ENV): string {
  return environment === 'production' ? '__Host-zmail_google_state' : 'zmail_google_state';
}

/**
 * The state cookie binds a started Google sign-in to this browser. It is `SameSite=Lax` because
 * Google sends the browser back with a cross-site top-level GET, which Lax cookies accompany.
 */
export function createGoogleStateCookie(
  state: string,
  maxAgeSeconds: number,
  environment: NodeEnvironment = env.NODE_ENV
) {
  return {
    name: getGoogleStateCookieName(environment),
    value: state,
    httpOnly: true,
    sameSite: 'lax' as const,
    secure: environment === 'production',
    path: '/',
    maxAge: Math.max(1, Math.min(Math.floor(maxAgeSeconds), GOOGLE_STATE_MAX_AGE_SECONDS)),
  };
}

export function createExpiredGoogleStateCookie(environment: NodeEnvironment = env.NODE_ENV) {
  return { ...createGoogleStateCookie('', 1, environment), maxAge: 0, expires: new Date(0) };
}

/** The callback registered in Google Cloud Console: `<NEXT_PUBLIC_APP_URL>/api/auth/google/callback`. */
export function googleSignInCallbackUrl(): string {
  return new URL(GOOGLE_SIGN_IN_CALLBACK_PATH, env.NEXT_PUBLIC_APP_URL).toString();
}

/**
 * Whether the login and register pages show "Continue with Google". Only the production adapter can
 * offer it, and only when the API reports a configured OAuth client; any failure hides the button.
 * The answer is cached briefly so rendering the sign-in page does not call the API every time.
 */
export async function isGoogleSignInAvailable(now: number = Date.now()): Promise<boolean> {
  if (!serverEnv.API_ADAPTER_ENABLED || !serverEnv.API_UPSTREAM_URL) {
    return false;
  }
  if (providersCache && providersCache.expiresAt > now) {
    return providersCache.enabled;
  }
  let enabled = false;
  let failed = false;
  try {
    enabled = await configuredAdapter().googleSignInEnabled(new Headers(await headers()));
  } catch {
    failed = true;
  }
  providersCache = {
    enabled,
    expiresAt: now + (failed || !enabled ? PROVIDERS_FAILURE_CACHE_MS : PROVIDERS_CACHE_MS),
  };
  return enabled;
}

/** Test seam: forget the cached provider answer. */
export function resetGoogleSignInAvailabilityCache(): void {
  providersCache = undefined;
}

/** Sends the browser back to the sign-in page with a code the page maps to localized copy. */
export function googleSignInFailureRedirect(code: string, returnUrl?: string | null): NextResponse {
  const target = new URL(authConfig.routes.login, env.NEXT_PUBLIC_APP_URL);
  target.searchParams.set(GOOGLE_SIGN_IN_ERROR_PARAM, code);
  const resolved = returnUrl ? resolveReturnUrl(returnUrl) : authConfig.routes.afterLogin;
  if (resolved !== authConfig.routes.afterLogin) {
    target.searchParams.set('returnUrl', resolved);
  }
  return privateAuthResponse(NextResponse.redirect(target, 303));
}

/** Starts a Google sign-in through the API and sends the browser to Google. */
export async function startGoogleSignInWithGoApi(request: Request): Promise<NextResponse> {
  const rawReturnUrl = new URL(request.url).searchParams.get('returnUrl');
  const returnUrl = resolveReturnUrl(rawReturnUrl);
  const result = await configuredAdapter().startGoogleSignIn(
    { redirectUri: googleSignInCallbackUrl(), returnUrl },
    request.headers,
    request.signal
  );
  if (!result.ok) {
    return googleSignInFailureRedirect(result.error.errorCode, rawReturnUrl);
  }
  const response = privateAuthResponse(NextResponse.redirect(result.data.authorizationUrl, 303));
  response.cookies.set(createGoogleStateCookie(result.data.state, result.data.expiresIn));
  return response;
}

/**
 * Completes a Google sign-in. The state in the query must equal this browser's state cookie before
 * the API is asked to consume it; the cookie is dropped whatever the outcome.
 */
export async function completeGoogleSignInWithGoApi(request: Request): Promise<NextResponse> {
  const query = new URL(request.url).searchParams;
  const cookieStore = await cookies();
  const cookieState = cookieStore.get(getGoogleStateCookieName())?.value ?? '';
  const hasAuthSession = Boolean(cookieStore.get(getAuthSessionCookieName())?.value);
  const state = query.get('state') ?? '';
  const code = query.get('code') ?? '';
  const providerError = query.get('error');

  // When an already-authenticated user returns from Google without a login state cookie,
  // forward to the external-mail callback page so Gmail binding reuses the registered redirect URI.
  if (!cookieState && hasAuthSession && state && (code || providerError)) {
    const target = new URL(EXTERNAL_MAIL_CALLBACK_PATH, env.NEXT_PUBLIC_APP_URL);
    target.searchParams.set('state', state);
    if (code) target.searchParams.set('code', code);
    if (providerError) target.searchParams.set('error', providerError);
    return privateAuthResponse(NextResponse.redirect(target, 303));
  }

  cookieStore.set(createExpiredGoogleStateCookie());

  if (providerError) {
    return googleSignInFailureRedirect(GOOGLE_SIGN_IN_CANCELLED);
  }
  if (
    !GOOGLE_STATE_PATTERN.test(state) ||
    !sameState(state, cookieState) ||
    !code ||
    code.length > GOOGLE_CODE_MAX_LENGTH
  ) {
    return googleSignInFailureRedirect(ApiErrorCode.AUTH_GOOGLE_STATE_INVALID);
  }

  const result = await configuredAdapter().completeGoogleSignIn(
    { state, code },
    request.headers,
    request.signal
  );
  if (!result.ok) {
    return googleSignInFailureRedirect(result.error.errorCode);
  }

  await clearSessionCookie();
  await setApiSessionCookie(result.data.accessToken, result.data.maxAgeSeconds);
  const target = new URL(resolveReturnUrl(result.data.returnUrl), env.NEXT_PUBLIC_APP_URL);
  return privateAuthResponse(NextResponse.redirect(target, 303));
}

function sameState(left: string, right: string): boolean {
  const a = Buffer.from(left);
  const b = Buffer.from(right);
  return a.length === b.length && a.length > 0 && timingSafeEqual(a, b);
}
