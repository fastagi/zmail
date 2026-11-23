import {
  getMailClientSettingsRoute,
  privateMailboxResponse,
  updateMailClientSettingsRoute,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

export async function GET(request: Request) {
  return privateMailboxResponse(await getMailClientSettingsRoute(request));
}

export async function PUT(request: Request) {
  return privateMailboxResponse(await updateMailClientSettingsRoute(request));
}
