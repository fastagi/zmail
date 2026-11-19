'use client';

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { LOCAL_ERROR_HANDLING_META } from '@/config/query-meta';
import { mailboxKeys } from '@/features/mailbox/hooks/use-mailbox';
import { mailboxService } from '@/features/mailbox/services/mailbox-service';
import type {
  MailTemplateInput,
  MailTemplateKind,
  MailTemplateTestInput,
} from '@/features/mailbox/types';

/** Template keys live under the organization, like every other mailbox key. */
export const mailTemplateKeys = {
  all: (organizationId: number) =>
    [...mailboxKeys.organization(organizationId), 'templates'] as const,
  lists: (organizationId: number) => [...mailTemplateKeys.all(organizationId), 'list'] as const,
  list: (organizationId: number, kind?: MailTemplateKind) =>
    [...mailTemplateKeys.lists(organizationId), kind ?? 'all'] as const,
  detail: (organizationId: number, templateId: number) =>
    [...mailTemplateKeys.all(organizationId), templateId] as const,
};

function validId(value: number): boolean {
  return Number.isSafeInteger(value) && value > 0;
}

/** The organization's templates; only those of `kind` when it is given. */
export function useMailTemplates(organizationId: number, kind?: MailTemplateKind) {
  return useQuery({
    queryKey: mailTemplateKeys.list(organizationId, kind),
    queryFn: () => mailboxService.templates(organizationId, kind),
    enabled: validId(organizationId),
    staleTime: 30_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useMailTemplate(organizationId: number, templateId: number | null) {
  return useQuery({
    queryKey: mailTemplateKeys.detail(organizationId, templateId ?? 0),
    queryFn: () => mailboxService.template(organizationId, templateId ?? 0),
    enabled: validId(organizationId) && templateId !== null && validId(templateId),
    staleTime: 30_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useSaveMailTemplate(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ input, templateId }: { input: MailTemplateInput; templateId?: number }) =>
      mailboxService.saveTemplate(organizationId, input, templateId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSuccess: template => {
      queryClient.setQueryData(mailTemplateKeys.detail(organizationId, template.id), template);
    },
    onSettled: () =>
      queryClient.invalidateQueries({ queryKey: mailTemplateKeys.lists(organizationId) }),
  });
}

export function useDeleteMailTemplate(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (templateId: number) => mailboxService.deleteTemplate(organizationId, templateId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () =>
      queryClient.invalidateQueries({ queryKey: mailTemplateKeys.all(organizationId) }),
  });
}

export function useSendMailTemplateTest(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ templateId, input }: { templateId: number; input: MailTemplateTestInput }) =>
      mailboxService.sendTemplateTest(organizationId, templateId, input),
    meta: LOCAL_ERROR_HANDLING_META,
    // A test is a real send: it appears in the sent folder and the log.
    onSettled: () =>
      queryClient.invalidateQueries({ queryKey: mailboxKeys.organization(organizationId) }),
  });
}
