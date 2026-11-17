import {
  privateMailboxResponse,
  refreshMailDomainRoute,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ domainId: string }>;
}

export async function POST(request: Request, context: RouteContext) {
  const { domainId } = await context.params;
  return privateMailboxResponse(await refreshMailDomainRoute(request, domainId, 'setups'));
}
