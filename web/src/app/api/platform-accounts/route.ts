import {
  createPlatformAccountRoute,
  listPlatformAccountsRoute,
  privateMailboxResponse,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

export async function GET(request: Request) {
  return privateMailboxResponse(await listPlatformAccountsRoute(request));
}

export async function POST(request: Request) {
  return privateMailboxResponse(await createPlatformAccountRoute(request));
}
