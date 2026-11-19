import {
  getMailThreadRoute,
  privateMailboxResponse,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ mailboxId: string; threadId: string }>;
}

export async function GET(request: Request, context: RouteContext) {
  const { mailboxId, threadId } = await context.params;
  return privateMailboxResponse(await getMailThreadRoute(request, mailboxId, threadId));
}
