import {
  listExternalMailSyncRunsRoute,
  privateMailboxResponse,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ connectionId: string }>;
}

export async function GET(request: Request, context: RouteContext) {
  const { connectionId } = await context.params;
  return privateMailboxResponse(await listExternalMailSyncRunsRoute(request, connectionId));
}
