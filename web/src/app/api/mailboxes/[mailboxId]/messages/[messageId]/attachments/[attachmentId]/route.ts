import {
  downloadMailAttachmentRoute,
  privateMailboxResponse,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ mailboxId: string; messageId: string; attachmentId: string }>;
}

export async function GET(request: Request, context: RouteContext) {
  const { mailboxId, messageId, attachmentId } = await context.params;
  return privateMailboxResponse(
    await downloadMailAttachmentRoute(request, mailboxId, messageId, attachmentId)
  );
}
