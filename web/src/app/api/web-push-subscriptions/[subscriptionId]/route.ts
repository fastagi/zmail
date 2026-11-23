import {
  privateNotificationResponse,
  removeWebPushSubscriptionRoute,
} from '@/features/notification/server/notification-route';

export const runtime = 'nodejs';

interface RouteContext {
  params: Promise<{ subscriptionId: string }>;
}

export async function DELETE(request: Request, context: RouteContext) {
  const { subscriptionId } = await context.params;
  return privateNotificationResponse(
    await removeWebPushSubscriptionRoute(request, subscriptionId)
  );
}
