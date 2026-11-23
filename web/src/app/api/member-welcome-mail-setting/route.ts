import {
  getMemberWelcomeSettingRoute,
  privateMailboxResponse,
  updateMemberWelcomeSettingRoute,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

export async function GET(request: Request) {
  return privateMailboxResponse(await getMemberWelcomeSettingRoute(request));
}

export async function PUT(request: Request) {
  return privateMailboxResponse(await updateMemberWelcomeSettingRoute(request));
}
