import {
  deleteMailDomainRoute,
  getMailDomainRoute,
  privateMailboxResponse,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ domainId: string }>;
}

export async function GET(request: Request, context: RouteContext) {
  const { domainId } = await context.params;
  return privateMailboxResponse(await getMailDomainRoute(request, domainId));
}

export async function DELETE(request: Request, context: RouteContext) {
  const { domainId } = await context.params;
  return privateMailboxResponse(await deleteMailDomainRoute(request, domainId));
}
