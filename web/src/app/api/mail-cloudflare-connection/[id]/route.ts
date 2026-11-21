import {
  changeCloudflareConnectionRoute,
  privateMailboxResponse,
} from '@/features/mailbox/server/mailbox-route';

export async function DELETE(request: Request, { params }: { params: Promise<{ id: string }> }) {
  return privateMailboxResponse(
    await changeCloudflareConnectionRoute(request, 'disconnect', (await params).id)
  );
}
