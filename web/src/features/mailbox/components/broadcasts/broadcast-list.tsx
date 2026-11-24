'use client';

import { useState } from 'react';
import { Megaphone, Plus } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import {
  Progress,
  StatusBadge,
  useWhen,
} from '@/features/mailbox/components/broadcasts/broadcast-shared';
import {
  OrganizationLostState,
  useMailOrganizationId,
} from '@/features/mailbox/components/mail-organization';
import { EmptyState, ErrorAlert } from '@/features/mailbox/components/mail-shared';
import { useMailBroadcasts } from '@/features/mailbox/hooks/use-mail-broadcasts';
import { isOrganizationLost, resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

export function BroadcastList({
  enabled,
  onCreate,
  onOpen,
}: {
  enabled: boolean;
  onCreate: () => void;
  onOpen: (broadcastId: number) => void;
}) {
  const t = useT('mailbox');
  const [page, setPage] = useState(1);
  const broadcasts = useMailBroadcasts(useMailOrganizationId(), page);
  const when = useWhen();
  const create = (
    <Button type="button" icon={<Plus className="size-4" />} disabled={!enabled} onClick={onCreate}>
      {t('broadcasts.new')}
    </Button>
  );

  if (broadcasts.isPending) return <Skeleton className="h-48 w-full" aria-hidden="true" />;
  if (broadcasts.error || !broadcasts.data) {
    if (isOrganizationLost(broadcasts.error)) return <OrganizationLostState />;
    return (
      <ErrorAlert
        message={t(resolveMailboxErrorKey(broadcasts.error))}
        retryLabel={t('actions.retry')}
        onRetry={() => void broadcasts.refetch()}
      />
    );
  }
  const { items, meta } = broadcasts.data;
  if (items.length === 0) {
    return (
      <EmptyState
        icon={<Megaphone className="size-5" />}
        title={t('broadcasts.emptyTitle')}
        description={t('broadcasts.emptyDescription')}
        action={create}
      />
    );
  }

  return (
    <div className="space-y-3">
      <div className="flex justify-end">{create}</div>
      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('broadcasts.name')}</TableHead>
              <TableHead>{t('broadcasts.status')}</TableHead>
              <TableHead>{t('broadcasts.progress')}</TableHead>
              <TableHead className="text-right">{t('broadcasts.when')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map(broadcast => (
              <TableRow key={broadcast.id}>
                <TableCell className="max-w-64 truncate font-medium">
                  <button
                    type="button"
                    className="max-w-full truncate text-left hover:underline focus-ring"
                    aria-label={t('broadcasts.open', { name: broadcast.name })}
                    onClick={() => onOpen(broadcast.id)}
                  >
                    {broadcast.name}
                  </button>
                </TableCell>
                <TableCell>
                  <StatusBadge broadcast={broadcast} />
                </TableCell>
                <TableCell>
                  <Progress broadcast={broadcast} />
                </TableCell>
                <TableCell className="text-right text-sm whitespace-nowrap text-muted-foreground">
                  {when(broadcast)}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      {meta.last_page > 1 ? (
        <div className="flex items-center justify-end gap-3">
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={meta.current_page <= 1}
            onClick={() => setPage(meta.current_page - 1)}
          >
            {t('list.previous')}
          </Button>
          <span className="text-sm text-muted-foreground">
            {t('list.page', { page: meta.current_page, total: meta.last_page })}
          </span>
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={meta.current_page >= meta.last_page}
            onClick={() => setPage(meta.current_page + 1)}
          >
            {t('list.next')}
          </Button>
        </div>
      ) : null}
    </div>
  );
}
