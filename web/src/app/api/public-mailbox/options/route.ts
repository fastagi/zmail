import {
  getPublicMailboxOptionsRoute,
  privatePublicMailboxResponse,
} from '@/features/mailbox/server/public-mailbox-route';

export const dynamic = 'force-dynamic';

export async function GET(request: Request) {
  return privatePublicMailboxResponse(await getPublicMailboxOptionsRoute(request));
}
