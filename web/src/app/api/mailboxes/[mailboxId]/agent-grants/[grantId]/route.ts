import {
  privateMailboxResponse,
  revokeMailAgentGrantRoute,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ mailboxId: string; grantId: string }>;
}

export async function DELETE(request: Request, context: RouteContext) {
  const { mailboxId, grantId } = await context.params;
  return privateMailboxResponse(await revokeMailAgentGrantRoute(request, mailboxId, grantId));
}
