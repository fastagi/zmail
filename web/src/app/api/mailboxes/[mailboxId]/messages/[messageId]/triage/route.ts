import {
  privateMailboxResponse,
  updateMailMessageTriageRoute,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ mailboxId: string; messageId: string }>;
}

export async function PUT(request: Request, context: RouteContext) {
  const { mailboxId, messageId } = await context.params;
  return privateMailboxResponse(await updateMailMessageTriageRoute(request, mailboxId, messageId));
}
