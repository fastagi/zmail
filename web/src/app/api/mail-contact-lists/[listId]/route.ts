import {
  deleteMailContactListRoute,
  privateMailboxResponse,
  saveMailContactListRoute,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ listId: string }>;
}

export async function PUT(request: Request, context: RouteContext) {
  const { listId } = await context.params;
  return privateMailboxResponse(await saveMailContactListRoute(request, listId));
}

export async function DELETE(request: Request, context: RouteContext) {
  const { listId } = await context.params;
  return privateMailboxResponse(await deleteMailContactListRoute(request, listId));
}
