import {
  deleteMailDnsRecordRoute,
  privateMailboxResponse,
  updateMailDnsRecordRoute,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ zoneId: string; recordId: string }>;
}

export async function PATCH(request: Request, context: RouteContext) {
  const { zoneId, recordId } = await context.params;
  return privateMailboxResponse(await updateMailDnsRecordRoute(request, zoneId, recordId));
}

export async function DELETE(request: Request, context: RouteContext) {
  const { zoneId, recordId } = await context.params;
  return privateMailboxResponse(await deleteMailDnsRecordRoute(request, zoneId, recordId));
}
