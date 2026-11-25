import {
  privateMailboxResponse,
  setPendingMailboxMemberRoute,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ mailboxId: string }>;
}

export async function POST(request: Request, context: RouteContext) {
  const { mailboxId } = await context.params;
  return privateMailboxResponse(await setPendingMailboxMemberRoute(request, mailboxId));
}
