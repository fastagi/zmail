import type { z } from 'zod/mini';
import {
  assertContract,
  type Accepts,
  type ContractData,
  type ContractRequest,
  type ContractResponse,
  type Sends,
} from '@/http/contract';
import request, { ApiError } from '@/http/request';
import { ClientErrorCode } from '@/http/codes';
import {
  notificationPageEnvelopeSchema,
  notificationPreferenceSchema,
  notificationReadStateResultSchema,
  notificationSchema,
  notificationStatusSchema,
  webPushConfigSchema,
  webPushSubscriptionListSchema,
  webPushSubscriptionSchema,
} from '@/features/notification/schemas';
import type {
  MarkNotificationsReadInput,
  NotificationFilter,
  NotificationItem,
  NotificationPage,
  NotificationPreference,
  NotificationReadStateResult,
  NotificationStatus,
  RegisterWebPushSubscriptionInput,
  ReplaceNotificationPreferenceInput,
  ReplaceNotificationReadStateInput,
  WebPushConfig,
  WebPushSubscription,
} from '@/features/notification/types';

interface ListOptions {
  page?: number;
  perPage?: number;
  status?: NotificationFilter;
}

export const notificationService = {
  async list({ page = 1, perPage = 10, status = 'all' }: ListOptions = {}): Promise<NotificationPage> {
    const value = await request.getEnvelope<unknown>('/notifications', {
      params: { page, per_page: perPage, status },
    });
    return parseNotificationPage(value);
  },

  async status(): Promise<NotificationStatus> {
    return parseNotificationStatus(await request.get<unknown>('/notification-status'));
  },

  async replaceReadState(
    notificationId: number,
    input: ReplaceNotificationReadStateInput
  ): Promise<NotificationItem> {
    const value = await request.patch<unknown, ReplaceNotificationReadStateInput>(
      `/notifications/${notificationId}`,
      input
    );
    return parseNotification(value);
  },

  async markReadThrough(throughId: number): Promise<NotificationReadStateResult> {
    const input: MarkNotificationsReadInput = { through_id: throughId };
    return parseNotificationReadStateResult(
      await request.put<unknown, MarkNotificationsReadInput>('/notification-read-state', input)
    );
  },

  async preference(): Promise<NotificationPreference> {
    return parseNotificationPreference(
      await request.get<unknown>('/notification-preferences')
    );
  },

  async replacePreference(
    input: ReplaceNotificationPreferenceInput
  ): Promise<NotificationPreference> {
    return parseNotificationPreference(
      await request.put<unknown, ReplaceNotificationPreferenceInput>(
        '/notification-preferences',
        input
      )
    );
  },

  async webPushConfig(): Promise<WebPushConfig> {
    return parseWebPushConfig(await request.get<unknown>('/web-push-config'));
  },

  async listWebPushSubscriptions(): Promise<WebPushSubscription[]> {
    return parseWebPushSubscriptionList(await request.get<unknown>('/web-push-subscriptions'));
  },

  async registerWebPushSubscription(
    input: RegisterWebPushSubscriptionInput
  ): Promise<WebPushSubscription> {
    return parseWebPushSubscription(
      await request.put<unknown, RegisterWebPushSubscriptionInput>(
        '/web-push-subscriptions',
        input
      )
    );
  },

  async removeWebPushSubscription(subscriptionId: number): Promise<void> {
    await request.delete<unknown>(`/web-push-subscriptions/${subscriptionId}`);
  },
};

export function parseWebPushConfig(value: unknown): WebPushConfig {
  const parsed = webPushConfigSchema.safeParse(value);
  if (!parsed.success) throw invalidResponse();
  return parsed.data;
}

export function parseWebPushSubscription(value: unknown): WebPushSubscription {
  const parsed = webPushSubscriptionSchema.safeParse(value);
  if (!parsed.success) throw invalidResponse();
  return parsed.data;
}

export function parseWebPushSubscriptionList(value: unknown): WebPushSubscription[] {
  const parsed = webPushSubscriptionListSchema.safeParse(value);
  if (!parsed.success) throw invalidResponse();
  return parsed.data;
}

export function parseNotificationPage(value: unknown): NotificationPage {
  const parsed = notificationPageEnvelopeSchema.safeParse(value);
  if (!parsed.success) throw invalidResponse();
  return {
    items: parsed.data.data,
    meta: parsed.data.meta,
    links: parsed.data.links,
  };
}

export function parseNotification(value: unknown): NotificationItem {
  const parsed = notificationSchema.safeParse(value);
  if (!parsed.success) throw invalidResponse();
  return parsed.data;
}

export function parseNotificationStatus(value: unknown): NotificationStatus {
  const parsed = notificationStatusSchema.safeParse(value);
  if (!parsed.success) throw invalidResponse();
  return parsed.data;
}

export function parseNotificationReadStateResult(value: unknown): NotificationReadStateResult {
  const parsed = notificationReadStateResultSchema.safeParse(value);
  if (!parsed.success) throw invalidResponse();
  return parsed.data;
}

export function parseNotificationPreference(value: unknown): NotificationPreference {
  const parsed = notificationPreferenceSchema.safeParse(value);
  if (!parsed.success) throw invalidResponse();
  return parsed.data;
}

function invalidResponse(): ApiError {
  return new ApiError(
    'Notification service returned an invalid response',
    ClientErrorCode.INVALID_RESPONSE
  );
}

assertContract<
  Accepts<
    z.input<typeof notificationPageEnvelopeSchema>,
    ContractResponse<'listNotifications', 200>
  >
>();
assertContract<
  Accepts<z.input<typeof notificationStatusSchema>, ContractData<'getNotificationStatus', 200>>
>();
assertContract<
  Accepts<z.input<typeof notificationSchema>, ContractData<'updateNotificationReadState', 200>>
>();
assertContract<
  Sends<ReplaceNotificationReadStateInput, ContractRequest<'updateNotificationReadState'>>
>();
assertContract<
  Accepts<
    z.input<typeof notificationReadStateResultSchema>,
    ContractData<'markNotificationsReadThrough', 200>
  >
>();
assertContract<
  Sends<MarkNotificationsReadInput, ContractRequest<'markNotificationsReadThrough'>>
>();
assertContract<
  Accepts<
    z.input<typeof notificationPreferenceSchema>,
    ContractData<'getNotificationPreferences', 200>
  >
>();
assertContract<
  Accepts<
    z.input<typeof notificationPreferenceSchema>,
    ContractData<'replaceNotificationPreferences', 200>
  >
>();
assertContract<
  Sends<ReplaceNotificationPreferenceInput, ContractRequest<'replaceNotificationPreferences'>>
>();
assertContract<Accepts<z.input<typeof webPushConfigSchema>, ContractData<'getWebPushConfig', 200>>>();
assertContract<
  Accepts<
    z.input<typeof webPushSubscriptionListSchema>,
    ContractData<'listWebPushSubscriptions', 200>
  >
>();
assertContract<
  Accepts<z.input<typeof webPushSubscriptionSchema>, ContractData<'putWebPushSubscription', 200>>
>();
assertContract<
  Sends<RegisterWebPushSubscriptionInput, ContractRequest<'putWebPushSubscription'>>
>();
