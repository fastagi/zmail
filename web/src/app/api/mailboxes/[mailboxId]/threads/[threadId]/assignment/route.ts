import {
  getMailThreadAssignmentRoute,
  privateMailboxResponse,
  updateMailThreadAssignmentRoute,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ mailboxId: string; threadId: string }>;
}

export async function GET(request: Request, context: RouteContext) {
  const { mailboxId, threadId } = await context.params;
  return privateMailboxResponse(await getMailThreadAssignmentRoute(request, mailboxId, threadId));
}

export async function PUT(request: Request, context: RouteContext) {
  const { mailboxId, threadId } = await context.params;
  return privateMailboxResponse(
    await updateMailThreadAssignmentRoute(request, mailboxId, threadId)
  );
}
