import {
  deleteMailTemplateRoute,
  getMailTemplateRoute,
  privateMailboxResponse,
  updateMailTemplateRoute,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ templateId: string }>;
}

export async function GET(request: Request, context: RouteContext) {
  const { templateId } = await context.params;
  return privateMailboxResponse(await getMailTemplateRoute(request, templateId));
}

export async function PUT(request: Request, context: RouteContext) {
  const { templateId } = await context.params;
  return privateMailboxResponse(await updateMailTemplateRoute(request, templateId));
}

export async function DELETE(request: Request, context: RouteContext) {
  const { templateId } = await context.params;
  return privateMailboxResponse(await deleteMailTemplateRoute(request, templateId));
}
