import { z } from 'zod';
import {
  apiJsonBodyErrorResponse,
  apiValidationErrorResponse,
} from '@/app/api/_shared/error-response';
import { readJsonBody } from '@/app/api/_shared/json-body';
import { resolveAuthRoute } from '@/app/api/_shared/auth-route';
import { guardSameOriginMutation } from '@/app/api/_shared/mock-bff';
import { apiSuccessResponse } from '@/app/api/_shared/success-response';
import { requestPasswordResetWithGoApi } from '@/features/auth/server/auth-adapter-route';
import { privateAuthResponse } from '@/features/auth/server/auth-response';

const resetRequestSchema = z.object({ email: z.string().email().max(320) });

export const runtime = 'nodejs';

/** Asks for a reset token by email. The answer never tells whether the address has an account. */
export async function POST(request: Request) {
  return privateAuthResponse(await handlePasswordReset(request));
}

async function handlePasswordReset(request: Request) {
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
  const parsed = resetRequestSchema.safeParse(payload.data);
  if (!parsed.success) {
    return apiValidationErrorResponse('Invalid password reset payload', parsed.error);
  }
  if (resolution.backend === 'go-api') {
    return requestPasswordResetWithGoApi(request, parsed.data.email);
  }
  // The development mock has no mail; it acknowledges like the API does.
  return apiSuccessResponse({ success: true });
}
