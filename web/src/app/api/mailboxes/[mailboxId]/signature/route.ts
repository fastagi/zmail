import {
  getMailSignatureRoute,
  privateMailboxResponse,
  updateMailSignatureRoute,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ mailboxId: string }>;
}

export async function GET(request: Request, context: RouteContext) {
  const { mailboxId } = await context.params;
  return privateMailboxResponse(await getMailSignatureRoute(request, mailboxId));
}

export async function PUT(request: Request, context: RouteContext) {
  const { mailboxId } = await context.params;
  return privateMailboxResponse(await updateMailSignatureRoute(request, mailboxId));
}
