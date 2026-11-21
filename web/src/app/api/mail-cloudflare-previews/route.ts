import {
  changeCloudflareConnectionRoute,
  privateMailboxResponse,
} from '@/features/mailbox/server/mailbox-route';

export async function POST(request: Request) {
  return privateMailboxResponse(await changeCloudflareConnectionRoute(request, 'preview'));
}
