import {
  deleteMailLabelRoute,
  privateMailboxResponse,
  updateMailLabelRoute,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ mailboxId: string; labelId: string }>;
}

export async function PUT(request: Request, context: RouteContext) {
  const { mailboxId, labelId } = await context.params;
  return privateMailboxResponse(await updateMailLabelRoute(request, mailboxId, labelId));
}

export async function DELETE(request: Request, context: RouteContext) {
  const { mailboxId, labelId } = await context.params;
  return privateMailboxResponse(await deleteMailLabelRoute(request, mailboxId, labelId));
}
