import { z } from 'zod';
import {
  apiErrorResponse,
  apiJsonBodyErrorResponse,
  apiValidationErrorResponse,
} from '@/app/api/_shared/error-response';
import { readJsonBody } from '@/app/api/_shared/json-body';
import { resolveAuthRoute } from '@/app/api/_shared/auth-route';
import { guardSameOriginMutation } from '@/app/api/_shared/mock-bff';
import { apiSuccessResponse } from '@/app/api/_shared/success-response';
import {
  getCurrentGoApiSession,
  updateProfileWithGoApi,
} from '@/features/auth/server/auth-adapter-route';
import { privateAuthResponse } from '@/features/auth/server/auth-response';
import {
  clearSessionCookie,
  getSessionUser,
  setSessionCookie,
} from '@/features/auth/server/session';
import { ApiErrorCode } from '@/http/codes';

/** The API keeps a nickname of at most 50 characters and ignores an empty one. */
const updateProfileSchema = z.object({
  name: z.string().trim().min(1).max(50),
});

export const runtime = 'nodejs';

export async function GET(request: Request) {
  return privateAuthResponse(await handleCurrentSession(request));
}

async function handleCurrentSession(request: Request) {
  const resolution = resolveAuthRoute();

  if (!resolution.available) {
    return resolution.response;
  }
  if (resolution.backend === 'go-api') {
    return getCurrentGoApiSession(request);
  }

  const user = await getSessionUser();

  if (!user) {
    await clearSessionCookie();

    return apiErrorResponse({
      status: 401,
      errorCode: ApiErrorCode.AUTH_UNAUTHORIZED,
      message: 'Session expired',
    });
  }

  return apiSuccessResponse({
    user,
  });
}

/** Renames the signed-in user. */
export async function PUT(request: Request) {
  return privateAuthResponse(await handleUpdateProfile(request));
}

async function handleUpdateProfile(request: Request) {
  const resolution = resolveAuthRoute();
  if (!resolution.available) {
    return resolution.response;
  }
  const sameOriginGuard = guardSameOriginMutation(request);
  if (sameOriginGuard) {
    return sameOriginGuard;
  }
  const payload = await readJsonBody(request);
  if (!payload.ok) {
    return apiJsonBodyErrorResponse(payload.error);
  }
  const parsed = updateProfileSchema.safeParse(payload.data);
  if (!parsed.success) {
    return apiValidationErrorResponse('Invalid profile payload', parsed.error);
  }
  if (resolution.backend === 'go-api') {
    return updateProfileWithGoApi(request, parsed.data);
  }

  const user = await getSessionUser();
  if (!user) {
    await clearSessionCookie();
    return apiErrorResponse({
      status: 401,
      errorCode: ApiErrorCode.AUTH_UNAUTHORIZED,
      message: 'Session expired',
    });
  }
  const renamed = { ...user, name: parsed.data.name };
  await setSessionCookie(renamed);
  return apiSuccessResponse({ user: renamed });
}
