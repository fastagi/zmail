import {
  deleteMailboxRoute,
  privateMailboxResponse,
  updateMailboxRoute,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ mailboxId: string }>;
}

export async function PUT(request: Request, context: RouteContext) {
  const { mailboxId } = await context.params;
  return privateMailboxResponse(await updateMailboxRoute(request, mailboxId));
}

export async function DELETE(request: Request, context: RouteContext) {
  const { mailboxId } = await context.params;
  return privateMailboxResponse(await deleteMailboxRoute(request, mailboxId));
}
