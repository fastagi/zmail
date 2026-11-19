import {
  privateMailboxResponse,
  removeMailboxMemberRoute,
  setMailboxMemberRoute,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ mailboxId: string; userId: string }>;
}

export async function PUT(request: Request, context: RouteContext) {
  const { mailboxId, userId } = await context.params;
  return privateMailboxResponse(await setMailboxMemberRoute(request, mailboxId, userId));
}

export async function DELETE(request: Request, context: RouteContext) {
  const { mailboxId, userId } = await context.params;
  return privateMailboxResponse(await removeMailboxMemberRoute(request, mailboxId, userId));
}
