import {
  createMailReplyDraftRoute,
  privateMailboxResponse,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ mailboxId: string; messageId: string }>;
}

export async function POST(request: Request, context: RouteContext) {
  const { mailboxId, messageId } = await context.params;
  return privateMailboxResponse(await createMailReplyDraftRoute(request, mailboxId, messageId));
}
