'use client';

import { useState } from 'react';
import { useLocale } from 'next-intl';

import { Badge } from '@/components/ui/badge';
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
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import {
  knownReason,
  reasonKey,
  recipientKey,
  recipientStatuses,
} from '@/features/mailbox/components/broadcasts/broadcast-shared';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { ErrorAlert, mailEventKey } from '@/features/mailbox/components/mail-shared';
import { useMailBroadcastRecipients } from '@/features/mailbox/hooks/use-mail-broadcasts';
import type { MailBroadcast, MailRecipientStatus } from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

export function RecipientTable({
  broadcastId,
  counts,
  live,
}: {
  broadcastId: number;
  counts: MailBroadcast['counts'];
  live: boolean;
}) {
  const t = useT('mailbox');
  const locale = useLocale();
  const [status, setStatus] = useState<MailRecipientStatus | undefined>(undefined);
  const [page, setPage] = useState(1);
  const recipients = useMailBroadcastRecipients(
    useMailOrganizationId(),
    broadcastId,
    status,
    page,
    live
  );
  return (
    <section className="space-y-3">
      <div className="space-y-2">
        <h3 className="font-medium">{t('broadcasts.recipients')}</h3>
        <Tabs
          value={status ?? 'all'}
          onValueChange={value => {
            setStatus(value === 'all' ? undefined : (value as MailRecipientStatus));
            setPage(1);
          }}
        >
          <div className="-mx-1 overflow-x-auto px-1 pb-1">
            <TabsList aria-label={t('broadcasts.recipientFilter')}>
              {(['all', ...recipientStatuses] as const)
                .filter(value => value === 'all' || value !== 'canceled' || counts.canceled > 0)
                .map(value => (
                  <TabsTrigger key={value} value={value} className="gap-1.5">
                    {t(value === 'all' ? 'broadcasts.filterAll' : recipientKey[value])}
                    <span className="text-xs text-muted-foreground tabular-nums">
                      {new Intl.NumberFormat(locale).format(
                        value === 'all' ? counts.total : counts[value]
                      )}
                    </span>
                  </TabsTrigger>
                ))}
            </TabsList>
          </div>
        </Tabs>
      </div>
      {recipients.isPending ? (
        <Skeleton className="h-32 w-full" aria-hidden="true" />
      ) : recipients.error || !recipients.data ? (
        <ErrorAlert
          message={t(resolveMailboxErrorKey(recipients.error))}
          retryLabel={t('actions.retry')}
          onRetry={() => void recipients.refetch()}
        />
      ) : recipients.data.items.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t('broadcasts.noRecipients')}</p>
      ) : (
        <>
          <div className="rounded-md border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('broadcasts.email')}</TableHead>
                  <TableHead>{t('broadcasts.status')}</TableHead>
                  <TableHead>{t('broadcasts.delivery')}</TableHead>
                  <TableHead>{t('broadcasts.reason')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {recipients.data.items.map(recipient => {
                  const code = recipient.failure_code;
                  return (
                    <TableRow key={recipient.id}>
                      <TableCell className="max-w-72 truncate">{recipient.email}</TableCell>
                      <TableCell>
                        <Badge variant={recipient.status === 'sent' ? 'secondary' : 'outline'}>
                          {t(recipientKey[recipient.status])}
                        </Badge>
                      </TableCell>
                      <TableCell className="text-sm">
                        <span className="flex flex-wrap gap-1">
                          {recipient.last_event ? (
                            <Badge
                              variant={
                                recipient.last_event === 'delivered' ? 'secondary' : 'outline'
                              }
                            >
                              {t(mailEventKey[recipient.last_event])}
                            </Badge>
                          ) : null}
                          {recipient.contact_status === 'unsubscribed' ? (
                            <Badge variant="outline">{t('broadcasts.contactUnsubscribed')}</Badge>
                          ) : recipient.contact_status === 'suppressed' ? (
                            <Badge variant="outline">{t('broadcasts.contactSuppressed')}</Badge>
                          ) : null}
                        </span>
                      </TableCell>
                      <TableCell className="text-sm text-muted-foreground">
                        {knownReason(code) ? (
                          t(reasonKey[code])
                        ) : code ? (
                          <span className="flex flex-wrap items-baseline gap-x-1.5">
                            {t('broadcasts.reasonOther')}
                            <code className="text-xs">{code}</code>
                          </span>
                        ) : null}
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </div>
          {recipients.data.meta.last_page > 1 ? (
            <div className="flex items-center justify-end gap-3">
              <Button
                type="button"
                size="sm"
                variant="outline"
                disabled={recipients.data.meta.current_page <= 1}
                onClick={() => setPage(recipients.data.meta.current_page - 1)}
              >
                {t('list.previous')}
              </Button>
              <span className="text-sm text-muted-foreground">
                {t('list.page', {
                  page: recipients.data.meta.current_page,
                  total: recipients.data.meta.last_page,
                })}
              </span>
              <Button
                type="button"
                size="sm"
                variant="outline"
                disabled={recipients.data.meta.current_page >= recipients.data.meta.last_page}
                onClick={() => setPage(recipients.data.meta.current_page + 1)}
              >
                {t('list.next')}
              </Button>
            </div>
          ) : null}
        </>
      )}
    </section>
  );
}
