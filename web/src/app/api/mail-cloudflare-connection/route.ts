import {
  changeCloudflareConnectionRoute,
  getCloudflareConnectionRoute,
  privateMailboxResponse,
} from '@/features/mailbox/server/mailbox-route';

export async function GET(request: Request) {
  return privateMailboxResponse(await getCloudflareConnectionRoute(request));
}

export async function POST(request: Request) {
  return privateMailboxResponse(await changeCloudflareConnectionRoute(request, 'bind'));
}
