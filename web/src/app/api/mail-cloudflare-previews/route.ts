import {
  cloudflareManagementRoute,
  privateMailboxResponse,
} from '@/features/mailbox/server/mailbox-route';

export async function POST(request: Request) {
  return privateMailboxResponse(await cloudflareManagementRoute(request, 'preview'));
}
