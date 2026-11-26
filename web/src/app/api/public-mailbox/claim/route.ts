import { privateMailboxResponse } from '@/features/mailbox/server/mailbox-route';
import { claimPublicMailboxRoute } from '@/features/mailbox/server/public-mailbox-route';

export const dynamic = 'force-dynamic';

export async function POST(request: Request) {
  return privateMailboxResponse(await claimPublicMailboxRoute(request));
}
