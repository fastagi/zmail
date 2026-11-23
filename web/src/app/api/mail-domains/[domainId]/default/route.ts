import {
  privateMailboxResponse,
  setDefaultMailDomainRoute,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ domainId: string }>;
}

export async function PUT(request: Request, context: RouteContext) {
  const { domainId } = await context.params;
  return privateMailboxResponse(await setDefaultMailDomainRoute(request, domainId));
}
