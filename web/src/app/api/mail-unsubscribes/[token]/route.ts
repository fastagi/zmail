import { unsubscribeRoute } from '@/features/mailbox/server/unsubscribe-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ token: string }>;
}

/**
 * One-click unsubscribe (RFC 8058) and the unsubscribe page post here. The signed token is the
 * credential, so no session or same-origin check applies.
 */
export async function POST(request: Request, context: RouteContext) {
  const { token } = await context.params;
  return unsubscribeRoute(request, token);
}
