import {
  cancelMailBroadcastRoute,
  privateMailboxResponse,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ broadcastId: string }>;
}

export async function POST(request: Request, context: RouteContext) {
  const { broadcastId } = await context.params;
  return privateMailboxResponse(await cancelMailBroadcastRoute(request, broadcastId));
}
