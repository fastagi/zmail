import { getTransactionalMailLogEntryRoute } from '@/features/developer/server/developer-route';
import { privateMailboxResponse } from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ messageId: string }>;
}

export async function GET(request: Request, context: RouteContext) {
  const { messageId } = await context.params;
  return privateMailboxResponse(await getTransactionalMailLogEntryRoute(request, messageId));
}
