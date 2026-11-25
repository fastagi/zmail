'use client';

import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { MAILBOX_PERMISSIONS } from '@/features/mailbox/schemas';
import type { MailboxPermission } from '@/features/mailbox/types';
import { useOrganizationMembers } from '@/features/organization/hooks/use-organizations';
import type { OrganizationMember } from '@/features/organization/types';
import { useT } from '@/i18n';
import { cn } from '@/utils';

export type MailboxPerson = Pick<OrganizationMember, 'user_id' | 'username' | 'nickname'>;

/** The organization's member directory, as mailbox owners and members are picked from it. */
export function useOrganizationPeople(organizationId: number, enabled: boolean) {
  const query = useOrganizationMembers(organizationId, enabled);
  const items: MailboxPerson[] = query.data?.items ?? [];
  const status: 'loading' | 'error' | 'ready' =
    query.isPending && enabled ? 'loading' : query.error ? 'error' : 'ready';
  return { items, status, refetch: query.refetch };
}

export function memberName(person: MailboxPerson): string {
  return person.nickname || person.username;
}

/**
 * Names users by their id. Someone no longer in the organization, such as a former owner, is
 * named by id only.
 */
export function useUserNames(people: readonly MailboxPerson[]) {
  const t = useT('mailbox');
  const names = new Map(people.map(person => [person.user_id, memberName(person)]));
  return (userId: number | null): string =>
    userId === null
      ? t('access.system')
      : (names.get(userId) ?? t('access.unknownUser', { id: userId }));
}

export function MailboxPermissionSelect({
  label,
  value,
  disabled,
  onChange,
  className,
}: {
  label: string;
  value: MailboxPermission;
  disabled?: boolean;
  onChange: (permission: MailboxPermission) => void;
  className?: string;
}) {
  const t = useT('mailbox');
  return (
    <Select
      value={value}
      disabled={disabled}
      onValueChange={next => onChange(next as MailboxPermission)}
    >
      <SelectTrigger aria-label={label} className={cn('w-auto', className)}>
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {MAILBOX_PERMISSIONS.map(permission => (
          <SelectItem key={permission} value={permission}>
            {t(`access.permissions.${permission}`)}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
