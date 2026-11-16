import {
  deleteMailBroadcastRoute,
  getMailBroadcastRoute,
  privateMailboxResponse,
  saveMailBroadcastRoute,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ broadcastId: string }>;
}

export async function GET(request: Request, context: RouteContext) {
  const { broadcastId } = await context.params;
  return privateMailboxResponse(await getMailBroadcastRoute(request, broadcastId));
}

export async function PUT(request: Request, context: RouteContext) {
  const { broadcastId } = await context.params;
  return privateMailboxResponse(await saveMailBroadcastRoute(request, broadcastId));
}

export async function DELETE(request: Request, context: RouteContext) {
  const { broadcastId } = await context.params;
  return privateMailboxResponse(await deleteMailBroadcastRoute(request, broadcastId));
}
