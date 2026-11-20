import {
  exportMailboxesRoute,
  privateMailboxResponse,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

export async function GET(request: Request) {
  return privateMailboxResponse(await exportMailboxesRoute(request));
}
