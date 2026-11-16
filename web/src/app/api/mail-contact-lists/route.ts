import {
  listMailContactListsRoute,
  privateMailboxResponse,
  saveMailContactListRoute,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

export async function GET(request: Request) {
  return privateMailboxResponse(await listMailContactListsRoute(request));
}

export async function POST(request: Request) {
  return privateMailboxResponse(await saveMailContactListRoute(request));
}
