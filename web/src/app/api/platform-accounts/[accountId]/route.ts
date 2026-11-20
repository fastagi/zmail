import {
  deletePlatformAccountRoute,
  privateMailboxResponse,
  updatePlatformAccountRoute,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ accountId: string }>;
}

export async function PUT(request: Request, context: RouteContext) {
  const { accountId } = await context.params;
  return privateMailboxResponse(await updatePlatformAccountRoute(request, accountId));
}

export async function DELETE(request: Request, context: RouteContext) {
  const { accountId } = await context.params;
  return privateMailboxResponse(await deletePlatformAccountRoute(request, accountId));
}
