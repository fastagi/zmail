import {
  privateMailboxResponse,
  purgeExternalMailCopiesRoute,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ connectionId: string }>;
}

export async function POST(request: Request, context: RouteContext) {
  const { connectionId } = await context.params;
  return privateMailboxResponse(await purgeExternalMailCopiesRoute(request, connectionId));
}
