import { resolveAuthRoute } from '@/app/api/_shared/auth-route';
import { privateAuthResponse } from '@/features/auth/server/auth-response';
import {
  googleSignInFailureRedirect,
  startGoogleSignInWithGoApi,
} from '@/features/auth/server/google-sign-in';
import { ApiErrorCode } from '@/http/codes';

export const runtime = 'nodejs';

/**
 * Starts "Continue with Google": the API stores the pending sign-in, this route binds its state to
 * the browser in an HttpOnly cookie and redirects to Google. The development mock BFF has no Google
 * client, so it answers like an unconfigured API.
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
  return startGoogleSignInWithGoApi(request);
}
