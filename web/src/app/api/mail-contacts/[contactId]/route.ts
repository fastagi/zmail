import {
  deleteMailContactRoute,
  privateMailboxResponse,
  updateMailContactRoute,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ contactId: string }>;
}

export async function PUT(request: Request, context: RouteContext) {
  const { contactId } = await context.params;
  return privateMailboxResponse(await updateMailContactRoute(request, contactId));
}

export async function DELETE(request: Request, context: RouteContext) {
  const { contactId } = await context.params;
  return privateMailboxResponse(await deleteMailContactRoute(request, contactId));
}
