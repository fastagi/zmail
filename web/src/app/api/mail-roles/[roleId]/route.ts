import {
  deleteMailRoleRoute,
  privateMailboxResponse,
  updateMailRoleRoute,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ roleId: string }>;
}

export async function PUT(request: Request, context: RouteContext) {
  const { roleId } = await context.params;
  return privateMailboxResponse(await updateMailRoleRoute(request, roleId));
}

export async function DELETE(request: Request, context: RouteContext) {
  const { roleId } = await context.params;
  return privateMailboxResponse(await deleteMailRoleRoute(request, roleId));
}
