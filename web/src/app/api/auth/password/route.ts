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
import { changePasswordWithGoApi } from '@/features/auth/server/auth-adapter-route';
import { clearApiSessionCookie } from '@/features/auth/server/api-session';
import { privateAuthResponse } from '@/features/auth/server/auth-response';
import { authenticateMockIdentity } from '@/features/auth/server/mock-identity';
import { clearSessionCookie, getSessionUser } from '@/features/auth/server/session';
import { ApiErrorCode } from '@/http/codes';

/** The API's own bounds for `new_password` (6 to 50 characters). */
const changePasswordSchema = z
  .object({
    currentPassword: z.string().min(1).max(256),
    newPassword: z.string().min(6).max(50),
  })
  // The API accepts an unchanged password and still ends every session; refuse it here instead.
  .refine(value => value.newPassword !== value.currentPassword, {
    path: ['newPassword'],
    message: 'The new password must differ from the current one',
  });

export const runtime = 'nodejs';

/** Changes the signed-in user's password. Every session of the account ends, this one included. */
export async function PUT(request: Request) {
  return privateAuthResponse(await handleChangePassword(request));
}

async function handleChangePassword(request: Request) {
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
  const parsed = changePasswordSchema.safeParse(payload.data);
  if (!parsed.success) {
    return apiValidationErrorResponse('Invalid password change payload', parsed.error);
  }
  if (resolution.backend === 'go-api') {
    return changePasswordWithGoApi(request, parsed.data);
  }

  // The development mock checks the current password against its fixed demo identity. It has no
  // credential store, so the demo password stays the same; only the session ends, as it would.
  const user = await getSessionUser();
  if (!user) {
    await clearSessionCookie();
    return apiErrorResponse({
      status: 401,
      errorCode: ApiErrorCode.AUTH_UNAUTHORIZED,
      message: 'Session expired',
    });
  }
  if (!authenticateMockIdentity(user.email, parsed.data.currentPassword)) {
    return apiErrorResponse({
      status: 401,
      errorCode: ApiErrorCode.AUTH_INVALID_CREDENTIALS,
      message: 'Authentication failed',
    });
  }
  await clearApiSessionCookie();
  await clearSessionCookie();
  return apiSuccessResponse({ success: true });
}
