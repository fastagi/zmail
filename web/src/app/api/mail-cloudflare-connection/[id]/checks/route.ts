import {
  cloudflareManagementRoute,
  privateMailboxResponse,
} from '@/features/mailbox/server/mailbox-route';

export async function POST(request: Request, { params }: { params: Promise<{ id: string }> }) {
  return privateMailboxResponse(
    await cloudflareManagementRoute(request, 'check', (await params).id)
  );
}
