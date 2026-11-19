import {
  deleteMailFolderRoute,
  privateMailboxResponse,
  renameMailFolderRoute,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ mailboxId: string; folderId: string }>;
}

export async function PUT(request: Request, context: RouteContext) {
  const { mailboxId, folderId } = await context.params;
  return privateMailboxResponse(await renameMailFolderRoute(request, mailboxId, folderId));
}

export async function DELETE(request: Request, context: RouteContext) {
  const { mailboxId, folderId } = await context.params;
  return privateMailboxResponse(await deleteMailFolderRoute(request, mailboxId, folderId));
}
