import {
  privateMailboxResponse,
  sendMailTemplateTestRoute,
} from '@/features/mailbox/server/mailbox-route';

export const dynamic = 'force-dynamic';

interface RouteContext {
  params: Promise<{ templateId: string }>;
}

export async function POST(request: Request, context: RouteContext) {
  const { templateId } = await context.params;
  return privateMailboxResponse(await sendMailTemplateTestRoute(request, templateId));
}
