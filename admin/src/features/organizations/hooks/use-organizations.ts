import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { organizationsService } from '@/features/organizations/services/organizations-service';
import type { OperatorOrganization, OrganizationSearch } from '@/features/organizations/types';

export const organizationsQueryKey = ['operator', 'organizations'] as const;

export function useOrganizations(search: OrganizationSearch) {
  return useQuery({
    queryKey: [...organizationsQueryKey, 'list', search],
    queryFn: ({ signal }) => organizationsService.list(search, signal),
    placeholderData: keepPreviousData,
  });
}

export function useOrganization(organizationId: number) {
  return useQuery({
    queryKey: [...organizationsQueryKey, organizationId],
    queryFn: ({ signal }) => organizationsService.get(organizationId, signal),
  });
}

export function useOrganizationMembers(organizationId: number, page: number) {
  return useQuery({
    queryKey: [...organizationsQueryKey, organizationId, 'members', page],
    queryFn: ({ signal }) => organizationsService.members(organizationId, page, signal),
    placeholderData: keepPreviousData,
  });
}

export type OrganizationControl =
  | { action: 'suspend'; reason: string }
  | { action: 'restore' }
  | { action: 'setSendLimit'; dailySendLimit: number | null };

export function useOrganizationControl(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (control: OrganizationControl): Promise<OperatorOrganization> => {
      switch (control.action) {
        case 'suspend':
          return organizationsService.suspend(organizationId, control.reason);
        case 'restore':
          return organizationsService.restore(organizationId);
        case 'setSendLimit':
          return organizationsService.setSendLimit(organizationId, control.dailySendLimit);
      }
    },
    onSuccess: (organization) => {
      queryClient.setQueryData([...organizationsQueryKey, organizationId], organization);
    },
    onSettled: () => queryClient.invalidateQueries({ queryKey: organizationsQueryKey }),
  });
}
