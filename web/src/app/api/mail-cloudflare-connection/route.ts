import {
  cloudflareManagementRoute,
  privateMailboxResponse,
} from '@/features/mailbox/server/mailbox-route';

export async function GET(request: Request) {
  return privateMailboxResponse(await cloudflareManagementRoute(request, 'status'));
}

export async function POST(request: Request) {
  return privateMailboxResponse(await cloudflareManagementRoute(request, 'bind'));
}
