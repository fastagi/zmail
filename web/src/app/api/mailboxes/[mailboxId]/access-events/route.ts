import {
  listMailboxAccessEventsRoute,
  privateMailboxResponse,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ mailboxId: string }>;
}

export async function GET(request: Request, context: RouteContext) {
  const { mailboxId } = await context.params;
  return privateMailboxResponse(await listMailboxAccessEventsRoute(request, mailboxId));
}
