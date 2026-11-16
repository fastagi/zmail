import {
  listMailBroadcastRecipientsRoute,
  privateMailboxResponse,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ broadcastId: string }>;
}

export async function GET(request: Request, context: RouteContext) {
  const { broadcastId } = await context.params;
  return privateMailboxResponse(await listMailBroadcastRecipientsRoute(request, broadcastId));
}
