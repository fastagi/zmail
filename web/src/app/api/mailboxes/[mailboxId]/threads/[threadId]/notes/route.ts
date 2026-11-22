import {
  createMailThreadNoteRoute,
  listMailThreadNotesRoute,
  privateMailboxResponse,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ mailboxId: string; threadId: string }>;
}

export async function GET(request: Request, context: RouteContext) {
  const { mailboxId, threadId } = await context.params;
  return privateMailboxResponse(await listMailThreadNotesRoute(request, mailboxId, threadId));
}

export async function POST(request: Request, context: RouteContext) {
  const { mailboxId, threadId } = await context.params;
  return privateMailboxResponse(await createMailThreadNoteRoute(request, mailboxId, threadId));
}
