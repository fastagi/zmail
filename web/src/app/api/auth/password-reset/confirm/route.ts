import { z } from 'zod';
import {
  apiErrorResponse,
  apiJsonBodyErrorResponse,
  apiValidationErrorResponse,
} from '@/app/api/_shared/error-response';
import { readJsonBody } from '@/app/api/_shared/json-body';
import { resolveAuthRoute } from '@/app/api/_shared/auth-route';
import { guardSameOriginMutation } from '@/app/api/_shared/mock-bff';
import { confirmPasswordResetWithGoApi } from '@/features/auth/server/auth-adapter-route';
import { privateAuthResponse } from '@/features/auth/server/auth-response';
import { ApiErrorCode } from '@/http/codes';

const resetConfirmSchema = z.object({
  token: z.string().trim().min(1).max(256),
  password: z.string().min(6).max(50),
});

export const runtime = 'nodejs';

/** Sets a new password with an emailed reset token; the account's sessions end. */
export async function POST(request: Request) {
  return privateAuthResponse(await handlePasswordResetConfirm(request));
}

async function handlePasswordResetConfirm(request: Request) {
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
  const parsed = resetConfirmSchema.safeParse(payload.data);
  if (!parsed.success) {
    return apiValidationErrorResponse('Invalid password reset payload', parsed.error);
  }
  if (resolution.backend === 'go-api') {
    return confirmPasswordResetWithGoApi(request, parsed.data);
  }
  // The development mock never issues reset tokens, so every token is unknown.
  return apiErrorResponse({
    status: 401,
    errorCode: ApiErrorCode.AUTH_PASSWORD_RESET_TOKEN_INVALID,
    message: 'Password reset token is invalid',
  });
}
