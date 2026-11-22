import {
  deleteMailDraftRoute,
  privateMailboxResponse,
  updateMailDraftRoute,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ draftId: string }>;
}

export async function PUT(request: Request, context: RouteContext) {
  const { draftId } = await context.params;
  return privateMailboxResponse(await updateMailDraftRoute(request, draftId));
}

export async function DELETE(request: Request, context: RouteContext) {
  const { draftId } = await context.params;
  return privateMailboxResponse(await deleteMailDraftRoute(request, draftId));
}
