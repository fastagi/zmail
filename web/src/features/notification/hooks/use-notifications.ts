'use client';

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { LOCAL_ERROR_HANDLING_META } from '@/config/query-meta';
import { notificationService } from '@/features/notification/services/notification-service';
import type {
  RegisterWebPushSubscriptionInput,
  ReplaceNotificationPreferenceInput,
} from '@/features/notification/types';

export const notificationKeys = {
  all: ['notifications'] as const,
  list: () => [...notificationKeys.all, 'list'] as const,
  status: () => [...notificationKeys.all, 'status'] as const,
  preference: () => [...notificationKeys.all, 'preference'] as const,
  webPushConfig: () => [...notificationKeys.all, 'web-push-config'] as const,
  webPushSubscriptions: () => [...notificationKeys.all, 'web-push-subscriptions'] as const,
};

export function useNotificationStatus() {
  return useQuery({
    queryKey: notificationKeys.status(),
    queryFn: () => notificationService.status(),
    staleTime: 30_000,
    refetchInterval: 60_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useNotifications(enabled: boolean) {
  return useQuery({
    queryKey: notificationKeys.list(),
    queryFn: () => notificationService.list({ page: 1, perPage: 10, status: 'all' }),
    enabled,
    staleTime: 15_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useNotificationPreference(enabled: boolean) {
  return useQuery({
    queryKey: notificationKeys.preference(),
    queryFn: () => notificationService.preference(),
    enabled,
    staleTime: 60_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useReplaceNotificationReadState() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ notificationId, isRead }: { notificationId: number; isRead: boolean }) =>
      notificationService.replaceReadState(notificationId, { is_read: isRead }),
    meta: LOCAL_ERROR_HANDLING_META,
    onSuccess: () => refreshNotificationState(queryClient),
  });
}

export function useMarkNotificationsRead() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (throughId: number) => notificationService.markReadThrough(throughId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSuccess: () => refreshNotificationState(queryClient),
  });
}

export function useReplaceNotificationPreference() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: ReplaceNotificationPreferenceInput) =>
      notificationService.replacePreference(input),
    meta: LOCAL_ERROR_HANDLING_META,
    onSuccess: preference => {
      queryClient.setQueryData(notificationKeys.preference(), preference);
    },
  });
}

export function useWebPushConfig(enabled = true) {
  return useQuery({
    queryKey: notificationKeys.webPushConfig(),
    queryFn: () => notificationService.webPushConfig(),
    enabled,
    staleTime: 5 * 60_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useWebPushSubscriptions(enabled = true) {
  return useQuery({
    queryKey: notificationKeys.webPushSubscriptions(),
    queryFn: () => notificationService.listWebPushSubscriptions(),
    enabled,
    staleTime: 30_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useRegisterWebPushSubscription() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: RegisterWebPushSubscriptionInput) =>
      notificationService.registerWebPushSubscription(input),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () =>
      queryClient.invalidateQueries({ queryKey: notificationKeys.webPushSubscriptions() }),
  });
}

export function useRemoveWebPushSubscription() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (subscriptionId: number) =>
      notificationService.removeWebPushSubscription(subscriptionId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () =>
      queryClient.invalidateQueries({ queryKey: notificationKeys.webPushSubscriptions() }),
  });
}

function refreshNotificationState(queryClient: ReturnType<typeof useQueryClient>): void {
  queryClient.invalidateQueries({ queryKey: notificationKeys.list() });
  queryClient.invalidateQueries({ queryKey: notificationKeys.status() });
}
