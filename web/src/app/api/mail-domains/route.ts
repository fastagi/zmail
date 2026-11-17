import {
  connectMailDomainRoute,
  listMailDomainsRoute,
  privateMailboxResponse,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

export async function GET(request: Request) {
  return privateMailboxResponse(await listMailDomainsRoute(request));
}

export async function POST(request: Request) {
  return privateMailboxResponse(await connectMailDomainRoute(request));
}
