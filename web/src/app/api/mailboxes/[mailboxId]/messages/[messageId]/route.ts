import {
  deleteMailMessageRoute,
  getMailMessageRoute,
  privateMailboxResponse,
  updateMailMessageRoute,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ mailboxId: string; messageId: string }>;
}

export async function GET(request: Request, context: RouteContext) {
  const { mailboxId, messageId } = await context.params;
  return privateMailboxResponse(await getMailMessageRoute(request, mailboxId, messageId));
}

export async function PUT(request: Request, context: RouteContext) {
  const { mailboxId, messageId } = await context.params;
  return privateMailboxResponse(await updateMailMessageRoute(request, mailboxId, messageId));
}

export async function PATCH(request: Request, context: RouteContext) {
  const { mailboxId, messageId } = await context.params;
  return privateMailboxResponse(await updateMailMessageRoute(request, mailboxId, messageId));
}

export async function DELETE(request: Request, context: RouteContext) {
  const { mailboxId, messageId } = await context.params;
  return privateMailboxResponse(await deleteMailMessageRoute(request, mailboxId, messageId));
}
