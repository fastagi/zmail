import { resolveAuthRoute } from '@/app/api/_shared/auth-route';
import { guardSameOriginMutation } from '@/app/api/_shared/mock-bff';
import { privateAuthResponse } from '@/features/auth/server/auth-response';
import { handleTotpRoute } from '@/features/auth/server/totp-route';

export const runtime = 'nodejs';

export async function GET(request: Request) {
  const resolution = resolveAuthRoute();
  if (!resolution.available) {
    return privateAuthResponse(resolution.response);
  }
  return privateAuthResponse(
    await handleTotpRoute(request, 'GET', 'users/totp', resolution.backend)
  );
}

export async function PUT(request: Request) {
  const resolution = resolveAuthRoute();
  if (!resolution.available) {
    return privateAuthResponse(resolution.response);
  }
  const sameOriginGuard = guardSameOriginMutation(request);
  if (sameOriginGuard) {
    return privateAuthResponse(sameOriginGuard);
  }
  return privateAuthResponse(
    await handleTotpRoute(request, 'PUT', 'users/totp', resolution.backend)
  );
}

export async function DELETE(request: Request) {
  const resolution = resolveAuthRoute();
  if (!resolution.available) {
    return privateAuthResponse(resolution.response);
  }
  const sameOriginGuard = guardSameOriginMutation(request);
  if (sameOriginGuard) {
    return privateAuthResponse(sameOriginGuard);
  }
  return privateAuthResponse(
    await handleTotpRoute(request, 'DELETE', 'users/totp', resolution.backend)
  );
}
