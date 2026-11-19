import { removeMailSuppressionRoute } from '@/features/developer/server/developer-route';
import { privateMailboxResponse } from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ suppressionId: string }>;
}

export async function DELETE(request: Request, context: RouteContext) {
  const { suppressionId } = await context.params;
  return privateMailboxResponse(await removeMailSuppressionRoute(request, suppressionId));
}
