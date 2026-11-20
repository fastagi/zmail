import {
  getMailTriageSettingRoute,
  privateMailboxResponse,
  updateMailTriageSettingRoute,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

export async function GET(request: Request) {
  return privateMailboxResponse(await getMailTriageSettingRoute(request));
}

export async function PUT(request: Request) {
  return privateMailboxResponse(await updateMailTriageSettingRoute(request));
}
