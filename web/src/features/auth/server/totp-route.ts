import 'server-only';

import { z } from 'zod';

import {
  apiErrorResponse,
  apiJsonBodyErrorResponse,
  apiValidationErrorResponse,
} from '@/app/api/_shared/error-response';
import type { AuthRouteBackend } from '@/app/api/_shared/auth-route';
import { readJsonBody } from '@/app/api/_shared/json-body';
import { apiSuccessResponse } from '@/app/api/_shared/success-response';
import { ApiErrorCode } from '@/http/codes';
import { forwardAuthenticatedGoApi } from '@/server/api-adapter/authenticated-route';

const codeSchema = z.object({ code: z.string().trim().min(1).max(32) });

type TotpMethod = 'GET' | 'POST' | 'PUT' | 'DELETE';

/**
 * Forwards the signed-in user's two-factor requests once the route has resolved its backend and
 * checked the origin of a mutation; the development mock has no factor store.
 */
export async function handleTotpRoute(
  request: Request,
  method: TotpMethod,
  path: string,
  backend: AuthRouteBackend
) {
  if (backend === 'mock') {
    return method === 'GET'
      ? apiSuccessResponse({ available: false, enabled: false, recovery_codes_remaining: 0 })
      : apiErrorResponse({
          status: 503,
          errorCode: ApiErrorCode.COMMON_SERVICE_UNAVAILABLE,
          message: 'Two-factor sign-in is unavailable',
        });
  }
  let body: z.infer<typeof codeSchema> | undefined;
  if (method === 'PUT' || method === 'DELETE') {
    const payload = await readJsonBody(request);
    if (!payload.ok) {
      return apiJsonBodyErrorResponse(payload.error);
    }
    const parsed = codeSchema.safeParse(payload.data);
    if (!parsed.success) {
      return apiValidationErrorResponse('Invalid two-factor code', parsed.error);
    }
    body = parsed.data;
  }
  return forwardAuthenticatedGoApi(request, { method, path, body });
}
