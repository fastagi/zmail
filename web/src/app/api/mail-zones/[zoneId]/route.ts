import {
  deleteMailZoneRoute,
  getMailZoneRoute,
  privateMailboxResponse,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ zoneId: string }>;
}

export async function GET(request: Request, context: RouteContext) {
  const { zoneId } = await context.params;
  return privateMailboxResponse(await getMailZoneRoute(request, zoneId));
}

export async function DELETE(request: Request, context: RouteContext) {
  const { zoneId } = await context.params;
  return privateMailboxResponse(await deleteMailZoneRoute(request, zoneId));
}
