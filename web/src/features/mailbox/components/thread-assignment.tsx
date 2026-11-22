'use client';

import { CircleCheck, CircleDot, UserRound } from 'lucide-react';

import { Button } from '@/components/ui/button';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import {
  useSaveThreadAssignment,
  useThreadAssignment,
} from '@/features/mailbox/hooks/use-thread-assignment';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useOrganizationMembers } from '@/features/organization';
import { useT } from '@/i18n';

const unassigned = 'none';

/**
 * Who works a shared mailbox's conversation and whether it is done, so two members do not answer
 * the same mail. Members who may send change it; everyone who reads the thread sees it.
 */
export function ThreadAssignment({
  mailboxId,
  threadId,
  canSend,
}: {
  mailboxId: number;
  threadId: number;
  canSend: boolean;
}) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const query = useThreadAssignment(organizationId, mailboxId, threadId);
  const save = useSaveThreadAssignment(organizationId, mailboxId, threadId);
  const members = useOrganizationMembers(organizationId, query.isSuccess);
  const assignment = query.data;
  if (!assignment) return null;

  const names = new Map(
    (members.data?.items ?? []).map(member => [
      member.user_id,
      member.nickname?.trim() || member.username,
    ])
  );
  const nameOf = (userId: number) => names.get(userId) ?? t('assignment.member', { id: userId });
  const closed = assignment.status === 'closed';
  const options = assignment.assignable_user_ids;
  const current = assignment.assignee_user_id;
  const busy = save.isPending;

  return (
    <div
      role="group"
      aria-label={t('assignment.label')}
      className="flex flex-wrap items-center gap-2 rounded-[8px] border border-border bg-muted/40 px-3 py-2 text-sm"
    >
      <UserRound className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
      <Select
        value={current === null ? unassigned : String(current)}
        disabled={!canSend || busy}
        onValueChange={value =>
          save.mutate({
            assignee_user_id: value === unassigned ? null : Number(value),
            status: assignment.status,
          })
        }
      >
        <SelectTrigger size="sm" aria-label={t('assignment.assignee')} className="min-w-40">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value={unassigned}>{t('assignment.unassigned')}</SelectItem>
          {current !== null && !options.includes(current) ? (
            <SelectItem value={String(current)}>{nameOf(current)}</SelectItem>
          ) : null}
          {options.map(userId => (
            <SelectItem key={userId} value={String(userId)}>
              {nameOf(userId)}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <span className="inline-flex items-center gap-1 text-muted-foreground">
        {closed ? (
          <CircleCheck className="size-4" aria-hidden="true" />
        ) : (
          <CircleDot className="size-4" aria-hidden="true" />
        )}
        {t(closed ? 'assignment.closed' : 'assignment.open')}
      </span>
      {canSend ? (
        <Button
          type="button"
          size="sm"
          variant="outline"
          className="ml-auto"
          disabled={busy}
          onClick={() =>
            save.mutate({ assignee_user_id: current, status: closed ? 'open' : 'closed' })
          }
        >
          {t(closed ? 'assignment.reopen' : 'assignment.close')}
        </Button>
      ) : null}
      {save.isError ? (
        <p role="alert" className="w-full text-destructive">
          {t(resolveMailboxErrorKey(save.error))}
        </p>
      ) : null}
    </div>
  );
}
