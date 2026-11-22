import { resolveAuthRoute } from '@/app/api/_shared/auth-route';
import { guardSameOriginMutation } from '@/app/api/_shared/mock-bff';
import { privateAuthResponse } from '@/features/auth/server/auth-response';
import { handleTotpRoute } from '@/features/auth/server/totp-route';

export const runtime = 'nodejs';

export async function POST(request: Request) {
  const resolution = resolveAuthRoute();
  if (!resolution.available) {
    return privateAuthResponse(resolution.response);
  }
  const sameOriginGuard = guardSameOriginMutation(request);
  if (sameOriginGuard) {
    return privateAuthResponse(sameOriginGuard);
  }
  return privateAuthResponse(
    await handleTotpRoute(request, 'POST', 'users/totp/enrollments', resolution.backend)
  );
}
