import {
  createMailDnsRecordRoute,
  listMailDnsRecordsRoute,
  privateMailboxResponse,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ zoneId: string }>;
}

export async function GET(request: Request, context: RouteContext) {
  const { zoneId } = await context.params;
  return privateMailboxResponse(await listMailDnsRecordsRoute(request, zoneId));
}

export async function POST(request: Request, context: RouteContext) {
  const { zoneId } = await context.params;
  return privateMailboxResponse(await createMailDnsRecordRoute(request, zoneId));
}
