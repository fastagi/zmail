import {
  listMemberWelcomeMailsRoute,
  privateMailboxResponse,
  resendMemberWelcomeMailRoute,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

export async function GET(request: Request) {
  return privateMailboxResponse(await listMemberWelcomeMailsRoute(request));
}

export async function POST(request: Request) {
  return privateMailboxResponse(await resendMemberWelcomeMailRoute(request));
}
