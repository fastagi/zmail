import {
  importMembersRoute,
  privateMailboxResponse,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

export async function POST(request: Request) {
  return privateMailboxResponse(await importMembersRoute(request));
}
