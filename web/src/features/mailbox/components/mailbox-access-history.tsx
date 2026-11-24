'use client';

import { useId } from 'react';

import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { useDateTimeFormat } from '@/features/mailbox/components/mail-shared';
import type { useMailboxAccessEvents } from '@/features/mailbox/hooks/use-mailbox';
import type { MailboxAccessEvent } from '@/features/mailbox/types';
import { useT } from '@/i18n';

export function HistorySection({
  events,
  page,
  onPageChange,
  nameOf,
}: {
  events: ReturnType<typeof useMailboxAccessEvents>;
  page: number;
  onPageChange: (page: number) => void;
  nameOf: (userId: number | null) => string;
}) {
  const t = useT('mailbox');
  const formatter = useDateTimeFormat();
  const titleId = useId();
  const describe = useEventText(nameOf);

  return (
    <section className="space-y-3">
      <h3 id={titleId} className="text-sm font-semibold">
        {t('access.history.title')}
      </h3>
      {events.isPending ? (
        <Skeleton className="h-24 w-full" />
      ) : events.error || !events.data ? (
        <p className="text-sm text-error">{t('access.history.loadFailed')}</p>
      ) : events.data.items.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t('access.history.empty')}</p>
      ) : (
        <>
          <ul aria-labelledby={titleId} className="divide-y rounded-md border">
            {events.data.items.map(event => (
              <li key={event.id} className="flex flex-wrap justify-between gap-2 px-3 py-2 text-sm">
                <span className="min-w-0">{describe(event)}</span>
                <time
                  dateTime={event.created_at}
                  className="shrink-0 text-xs text-muted-foreground"
                >
                  {formatter.format(new Date(event.created_at))}
                </time>
              </li>
            ))}
          </ul>
          {events.data.meta.last_page > 1 ? (
            <div className="flex items-center justify-between gap-3">
              <Button
                type="button"
                size="xs"
                variant="outline"
                disabled={page <= 1}
                onClick={() => onPageChange(page - 1)}
              >
                {t('access.history.newer')}
              </Button>
              <span className="text-xs text-muted-foreground">
                {t('list.page', { page, total: events.data.meta.last_page })}
              </span>
              <Button
                type="button"
                size="xs"
                variant="outline"
                disabled={page >= events.data.meta.last_page}
                onClick={() => onPageChange(page + 1)}
              >
                {t('access.history.older')}
              </Button>
            </div>
          ) : null}
        </>
      )}
    </section>
  );
}

/** One sentence per event; unknown `detail` keys are ignored. */
function useEventText(nameOf: (userId: number | null) => string) {
  const t = useT('mailbox');
  const count = (value: unknown) => (typeof value === 'number' ? value : 0);
  const permissionOf = (value: unknown) =>
    value === 'view' || value === 'send' || value === 'manage'
      ? t(`access.permissions.${value}`)
      : String(value ?? '');

  return (event: MailboxAccessEvent): string => {
    const actor = nameOf(event.actor_user_id);
    const subject = nameOf(event.subject_user_id);
    const { detail } = event;
    switch (event.type) {
      case 'created':
        return detail.type === 'personal' && event.subject_user_id !== null
          ? t('access.history.createdPersonal', { actor, subject })
          : t('access.history.createdShared', { actor });
      case 'member_set':
        return t('access.history.memberSet', {
          actor,
          subject,
          permission: permissionOf(detail.permission),
        });
      case 'member_removed':
        return detail.reason === 'left_organization'
          ? t('access.history.memberLeft', { subject })
          : t('access.history.memberRemoved', { actor, subject });
      case 'owner_left':
        return t('access.history.ownerLeft', { subject });
      case 'owner_returned':
        return t('access.history.ownerReturned', { subject });
      case 'handed_over':
        return t('access.history.handedOver', { actor, subject });
      case 'sealed_deleted':
        return t('access.history.sealedDeleted', {
          actor,
          messages: count(detail.deleted_messages),
          broadcasts: count(detail.deleted_broadcasts),
        });
      case 'webhooks_changed':
        return t(
          detail.enabled === true ? 'access.history.webhooksOn' : 'access.history.webhooksOff',
          { actor }
        );
      case 'renamed':
        return typeof detail.to === 'string' && detail.to !== ''
          ? t('access.history.renamed', { actor, name: detail.to })
          : t('access.history.renamedCleared', { actor });
      case 'connection_bound':
        return t('access.history.connectionBound', { actor });
      case 'connection_reconnected':
        return t('access.history.connectionReconnected', { actor });
      case 'connection_revoked':
        return t('access.history.connectionRevoked', { actor });
      case 'connection_force_disconnected':
        return t('access.history.connectionForceDisconnected', { actor });
      case 'connection_revoked_on_leave':
        return t('access.history.connectionRevokedOnLeave', { subject });
      case 'agent_granted':
        return t('access.history.agentGranted', { actor, subject });
      case 'agent_revoked':
        return event.actor_user_id === null
          ? t('access.history.agentEnded', { subject })
          : t('access.history.agentRevoked', { actor, subject });
      case 'owner_assigned':
        return t('access.history.ownerAssigned', { subject });
    }
  };
}
