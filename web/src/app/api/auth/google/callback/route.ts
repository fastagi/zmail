import { resolveAuthRoute } from '@/app/api/_shared/auth-route';
import { privateAuthResponse } from '@/features/auth/server/auth-response';
import {
  completeGoogleSignInWithGoApi,
  googleSignInFailureRedirect,
} from '@/features/auth/server/google-sign-in';
import { ApiErrorCode } from '@/http/codes';

export const runtime = 'nodejs';

/**
 * Google's redirect target, registered in Google Cloud Console as
 * `<NEXT_PUBLIC_APP_URL>/api/auth/google/callback`. It checks the state cookie, asks the API for a
 * session, sets the session cookie, and sends the browser to the stored return URL.
 */
export async function GET(request: Request) {
  return privateAuthResponse(await handle(request));
}

async function handle(request: Request) {
  const resolution = resolveAuthRoute();
  if (!resolution.available) {
    return resolution.response;
  }
  if (resolution.backend !== 'go-api') {
    return googleSignInFailureRedirect(ApiErrorCode.AUTH_GOOGLE_NOT_CONFIGURED);
  }
  return completeGoogleSignInWithGoApi(request);
}
