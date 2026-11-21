'use client';

import { useEffect, useRef } from 'react';
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { LOCAL_ERROR_HANDLING_META } from '@/config/query-meta';
import { mailboxKeys } from '@/features/mailbox/hooks/use-mailbox';
import { mailboxService } from '@/features/mailbox/services/mailbox-service';
import type {
  MailBroadcast,
  MailBroadcastInput,
  MailBroadcastSendInput,
  MailRecipientStatus,
} from '@/features/mailbox/types';

export const MAIL_BROADCAST_PAGE_SIZE = 20;
export const MAIL_RECIPIENT_PAGE_SIZE = 25;
/** How often a running broadcast is refreshed. */
const runningRefreshMs = 3_000;

export const mailBroadcastKeys = {
  all: (organizationId: number) =>
    [...mailboxKeys.organization(organizationId), 'broadcasts'] as const,
  page: (organizationId: number, page: number) =>
    [...mailBroadcastKeys.all(organizationId), 'page', page] as const,
  detail: (organizationId: number, broadcastId: number) =>
    [...mailBroadcastKeys.all(organizationId), broadcastId] as const,
  recipients: (
    organizationId: number,
    broadcastId: number,
    status: MailRecipientStatus | undefined,
    page: number
  ) =>
    [
      ...mailBroadcastKeys.detail(organizationId, broadcastId),
      'recipients',
      status ?? '',
      page,
    ] as const,
};

function validId(value: number): boolean {
  return Number.isSafeInteger(value) && value > 0;
}

function running(broadcast: MailBroadcast | undefined): boolean {
  // A waiting test wave continues on its own, so it is refreshed like a running broadcast.
  return (
    broadcast?.status === 'sending' ||
    broadcast?.status === 'scheduled' ||
    (broadcast?.status === 'paused' && broadcast.stop_reason === 'test_wave_complete')
  );
}

export function useMailBroadcasts(organizationId: number, page: number) {
  return useQuery({
    queryKey: mailBroadcastKeys.page(organizationId, page),
    queryFn: () => mailboxService.broadcasts(organizationId, page, MAIL_BROADCAST_PAGE_SIZE),
    enabled: validId(organizationId),
    placeholderData: keepPreviousData,
    // Progress keeps moving while any broadcast on the page runs.
    refetchInterval: query =>
      query.state.data?.items.some(item => running(item)) ? runningRefreshMs : false,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useMailBroadcast(organizationId: number, broadcastId: number | null) {
  return useQuery({
    queryKey: mailBroadcastKeys.detail(organizationId, broadcastId ?? 0),
    queryFn: () => mailboxService.broadcast(organizationId, broadcastId ?? 0),
    enabled: validId(organizationId) && broadcastId !== null && validId(broadcastId),
    refetchInterval: query => (running(query.state.data) ? runningRefreshMs : false),
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useMailBroadcastRecipients(
  organizationId: number,
  broadcastId: number,
  status: MailRecipientStatus | undefined,
  page: number,
  live: boolean
) {
  const queryClient = useQueryClient();
  const wasLive = useRef(live);
  // Polling stops once the broadcast settles, so the last poll may predate the final recipient
  // states: refresh every cached recipient view of the broadcast when it stops being live.
  useEffect(() => {
    if (wasLive.current && !live && validId(organizationId) && validId(broadcastId)) {
      void queryClient.invalidateQueries({
        queryKey: [...mailBroadcastKeys.detail(organizationId, broadcastId), 'recipients'],
      });
    }
    wasLive.current = live;
  }, [broadcastId, live, organizationId, queryClient]);

  return useQuery({
    queryKey: mailBroadcastKeys.recipients(organizationId, broadcastId, status, page),
    queryFn: () =>
      mailboxService.broadcastRecipients(organizationId, broadcastId, {
        ...(status ? { status } : {}),
        page,
        perPage: MAIL_RECIPIENT_PAGE_SIZE,
      }),
    enabled: validId(organizationId) && validId(broadcastId),
    placeholderData: keepPreviousData,
    refetchInterval: live ? runningRefreshMs : false,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

function useBroadcastMutation<Variables, Result>(
  organizationId: number,
  mutationFn: (variables: Variables) => Promise<Result>
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn,
    meta: LOCAL_ERROR_HANDLING_META,
    // Sending changes contacts' messages, the log, and the counts of every view.
    onSettled: () =>
      queryClient.invalidateQueries({ queryKey: mailboxKeys.organization(organizationId) }),
  });
}

export function useSaveMailBroadcast(organizationId: number) {
  return useBroadcastMutation(
    organizationId,
    ({ input, broadcastId }: { input: MailBroadcastInput; broadcastId?: number }) =>
      mailboxService.saveBroadcast(organizationId, input, broadcastId)
  );
}

export function useSendMailBroadcast(organizationId: number) {
  return useBroadcastMutation(
    organizationId,
    ({ broadcastId, input }: { broadcastId: number; input: MailBroadcastSendInput }) =>
      mailboxService.sendBroadcast(organizationId, broadcastId, input)
  );
}

export function useCancelMailBroadcast(organizationId: number) {
  return useBroadcastMutation(organizationId, (broadcastId: number) =>
    mailboxService.cancelBroadcast(organizationId, broadcastId)
  );
}

export function usePauseMailBroadcast(organizationId: number) {
  return useBroadcastMutation(organizationId, (broadcastId: number) =>
    mailboxService.controlBroadcast(organizationId, broadcastId, 'pauses')
  );
}

export function useResumeMailBroadcast(organizationId: number) {
  return useBroadcastMutation(organizationId, (broadcastId: number) =>
    mailboxService.controlBroadcast(organizationId, broadcastId, 'resumptions')
  );
}

export function useDeleteMailBroadcast(organizationId: number) {
  return useBroadcastMutation(organizationId, (broadcastId: number) =>
    mailboxService.deleteBroadcast(organizationId, broadcastId)
  );
}
