import { privateAuthResponse } from '@/features/auth/server/auth-response';
import { handleTotpRoute } from '@/features/auth/server/totp-route';

export const runtime = 'nodejs';

export async function GET(request: Request) {
  return privateAuthResponse(await handleTotpRoute(request, 'GET', 'users/totp'));
}

export async function PUT(request: Request) {
  return privateAuthResponse(await handleTotpRoute(request, 'PUT', 'users/totp'));
}

export async function DELETE(request: Request) {
  return privateAuthResponse(await handleTotpRoute(request, 'DELETE', 'users/totp'));
}
