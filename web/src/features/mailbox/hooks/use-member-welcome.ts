'use client';

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { LOCAL_ERROR_HANDLING_META } from '@/config/query-meta';
import type {
  MemberWelcomePreviewInput,
  UpdateMemberWelcomeSettingInput,
} from '@/features/mailbox/member-welcome-schemas';
import { memberWelcomeService } from '@/features/mailbox/services/member-welcome-service';

export const memberWelcomeKeys = {
  all: ['member-welcome'] as const,
  setting: (organizationId: number) =>
    [...memberWelcomeKeys.all, organizationId, 'setting'] as const,
};

function validId(value: number): boolean {
  return Number.isSafeInteger(value) && value > 0;
}

/** The organization's welcome mail setting; only owners and admins read it. */
export function useMemberWelcomeSetting(organizationId: number) {
  return useQuery({
    queryKey: memberWelcomeKeys.setting(organizationId),
    queryFn: () => memberWelcomeService.setting(organizationId),
    enabled: validId(organizationId),
    staleTime: 30_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useUpdateMemberWelcomeSetting(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: UpdateMemberWelcomeSettingInput) =>
      memberWelcomeService.update(organizationId, input),
    meta: LOCAL_ERROR_HANDLING_META,
    onSuccess: setting => {
      queryClient.setQueryData(memberWelcomeKeys.setting(organizationId), setting);
    },
  });
}

/** A preview is a read with a body: never cached, so each one shows the copy as typed. */
export function usePreviewMemberWelcome(organizationId: number) {
  return useMutation({
    mutationFn: (input: MemberWelcomePreviewInput) =>
      memberWelcomeService.preview(organizationId, input),
    gcTime: 0,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}
