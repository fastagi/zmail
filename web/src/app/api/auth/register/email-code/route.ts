import { z } from 'zod';
import {
  apiJsonBodyErrorResponse,
  apiValidationErrorResponse,
} from '@/app/api/_shared/error-response';
import { readJsonBody } from '@/app/api/_shared/json-body';
import { resolveAuthRoute } from '@/app/api/_shared/auth-route';
import { guardSameOriginMutation } from '@/app/api/_shared/mock-bff';
import { apiSuccessResponse } from '@/app/api/_shared/success-response';
import { requestRegistrationEmailCodeWithGoApi } from '@/features/auth/server/auth-adapter-route';
import { privateAuthResponse } from '@/features/auth/server/auth-response';

const codeRequestSchema = z.object({ email: z.string().email().max(100) });

export const runtime = 'nodejs';

/** Asks for a registration code by email. The answer never tells whether the address is taken. */
export async function POST(request: Request) {
  return privateAuthResponse(await handleRegistrationCode(request));
}

async function handleRegistrationCode(request: Request) {
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
  const parsed = codeRequestSchema.safeParse(payload.data);
  if (!parsed.success) {
    return apiValidationErrorResponse('Invalid registration code payload', parsed.error);
  }
  if (resolution.backend === 'go-api') {
    return requestRegistrationEmailCodeWithGoApi(request, parsed.data.email);
  }
  // The development mock has no mail; it acknowledges like the API does and accepts any code.
  return apiSuccessResponse({ success: true });
}
