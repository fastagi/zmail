import {
  listMailBroadcastsRoute,
  privateMailboxResponse,
  saveMailBroadcastRoute,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

export async function GET(request: Request) {
  return privateMailboxResponse(await listMailBroadcastsRoute(request));
}

export async function POST(request: Request) {
  return privateMailboxResponse(await saveMailBroadcastRoute(request));
}
