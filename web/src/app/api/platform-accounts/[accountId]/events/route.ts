import {
  listPlatformAccountEventsRoute,
  privateMailboxResponse,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ accountId: string }>;
}

export async function GET(request: Request, context: RouteContext) {
  const { accountId } = await context.params;
  return privateMailboxResponse(await listPlatformAccountEventsRoute(request, accountId));
}
