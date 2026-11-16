import {
  createMailTemplateRoute,
  listMailTemplatesRoute,
  privateMailboxResponse,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

export async function GET(request: Request) {
  return privateMailboxResponse(await listMailTemplatesRoute(request));
}

export async function POST(request: Request) {
  return privateMailboxResponse(await createMailTemplateRoute(request));
}
