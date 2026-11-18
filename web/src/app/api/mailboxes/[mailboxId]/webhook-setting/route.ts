import {
  setMailboxWebhookSettingRoute,
  privateMailboxResponse,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ mailboxId: string }>;
}

export async function PUT(request: Request, context: RouteContext) {
  const { mailboxId } = await context.params;
  return privateMailboxResponse(await setMailboxWebhookSettingRoute(request, mailboxId));
}
