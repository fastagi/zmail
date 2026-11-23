import {
  listWebPushSubscriptionsRoute,
  privateNotificationResponse,
  registerWebPushSubscriptionRoute,
} from '@/features/notification/server/notification-route';

export const runtime = 'nodejs';

export async function GET(request: Request) {
  return privateNotificationResponse(await listWebPushSubscriptionsRoute(request));
}

export async function PUT(request: Request) {
  return privateNotificationResponse(await registerWebPushSubscriptionRoute(request));
}
