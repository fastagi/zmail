import {
  deleteMailThreadNoteRoute,
  privateMailboxResponse,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ mailboxId: string; threadId: string; noteId: string }>;
}

export async function DELETE(request: Request, context: RouteContext) {
  const { mailboxId, threadId, noteId } = await context.params;
  return privateMailboxResponse(
    await deleteMailThreadNoteRoute(request, mailboxId, threadId, noteId)
  );
}
