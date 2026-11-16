import {
  createMailboxRoute,
  listMailboxesRoute,
  privateMailboxResponse,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

export async function GET(request: Request) {
  return privateMailboxResponse(await listMailboxesRoute(request));
}

export async function POST(request: Request) {
  return privateMailboxResponse(await createMailboxRoute(request));
}
