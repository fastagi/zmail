import {
  privateMailboxResponse,
  revokeMailAppPasswordRoute,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ passwordId: string }>;
}

export async function DELETE(request: Request, context: RouteContext) {
  const { passwordId } = await context.params;
  return privateMailboxResponse(await revokeMailAppPasswordRoute(request, passwordId));
}
