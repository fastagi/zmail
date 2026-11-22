import { privateAuthResponse } from '@/features/auth/server/auth-response';
import { handleTotpRoute } from '@/features/auth/server/totp-route';

export const runtime = 'nodejs';

export async function POST(request: Request) {
  return privateAuthResponse(await handleTotpRoute(request, 'POST', 'users/totp/enrollments'));
}
