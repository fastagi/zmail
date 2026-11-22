import {
  createMailDraftRoute,
  listMailDraftsRoute,
  privateMailboxResponse,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

export async function GET(request: Request) {
  return privateMailboxResponse(await listMailDraftsRoute(request));
}

export async function POST(request: Request) {
  return privateMailboxResponse(await createMailDraftRoute(request));
}
