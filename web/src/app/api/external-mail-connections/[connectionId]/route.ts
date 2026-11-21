import {
  privateMailboxResponse,
  revokeExternalMailConnectionRoute,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ connectionId: string }>;
}

export async function DELETE(request: Request, context: RouteContext) {
  const { connectionId } = await context.params;
  return privateMailboxResponse(await revokeExternalMailConnectionRoute(request, connectionId));
}
