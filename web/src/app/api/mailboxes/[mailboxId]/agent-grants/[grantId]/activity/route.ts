import {
  listMailAgentGrantActivityRoute,
  privateMailboxResponse,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ mailboxId: string; grantId: string }>;
}

export async function GET(request: Request, context: RouteContext) {
  const { mailboxId, grantId } = await context.params;
  return privateMailboxResponse(await listMailAgentGrantActivityRoute(request, mailboxId, grantId));
}
