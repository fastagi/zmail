'use client';

import { useMemo, useState, type FormEvent } from 'react';
import { Search, ShieldCheck, Trash2 } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
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
  useMailSuppressions,
  useRemoveMailSuppression,
} from '@/features/developer/hooks/use-developer';
import { MAIL_SUPPRESSION_EMAIL_MAX_LENGTH } from '@/features/developer/schemas';
import type { MailSuppression, MailSuppressionFilters } from '@/features/developer/types';
import { useDeveloperErrorMessage } from '@/features/developer/utils/developer-error';
import { OrganizationLostState } from '@/features/mailbox/components/mail-organization';
import {
  ConfirmDialog,
  EmptyState,
  ErrorAlert,
  useDateTimeFormat,
} from '@/features/mailbox/components/mail-shared';
import { isOrganizationLost } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

/**
 * The organization's bounce and complaint list for owners and admins: an exact-address search, the
 * entries newest first with "Older" for the next keyset page, and a removal that asks first, since
 * mail to an address that still does not work bounces again and costs sending reputation.
 */
export function SuppressionList({ organizationId }: { organizationId: number }) {
  const t = useT('developer');
  const tm = useT('mailbox');
  const errorMessage = useDeveloperErrorMessage();
  const dateFormat = useDateTimeFormat();
  const [address, setAddress] = useState('');
  const [email, setEmail] = useState('');
  const [pending, setPending] = useState<MailSuppression | null>(null);

  const filters: MailSuppressionFilters = email ? { email } : {};
  const list = useMailSuppressions(organizationId, filters);
  const remove = useRemoveMailSuppression(organizationId);
  const entries = useMemo(() => list.data?.pages.flatMap(page => page.items) ?? [], [list.data]);

  const search = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    remove.reset();
    setEmail(address.trim().toLowerCase());
  };

  const confirm = () => {
    if (!pending) return;
    remove.mutate(pending.id, { onSettled: () => setPending(null) });
  };

  return (
    <section aria-labelledby="developer-suppressions-title" className="space-y-4">
      <div>
        <h2 id="developer-suppressions-title" className="text-base font-semibold">
          {t('limits.suppressions.title')}
        </h2>
        <p className="mt-1 text-sm text-muted-foreground">{t('limits.suppressions.description')}</p>
      </div>

      <form onSubmit={search} className="flex flex-wrap items-end gap-2" role="search">
        <div className="min-w-0 flex-1 space-y-1 sm:max-w-sm">
          <Label htmlFor="suppression-search" className="text-xs text-muted-foreground">
            {t('limits.suppressions.searchLabel')}
          </Label>
          <Input
            id="suppression-search"
            type="email"
            inputMode="email"
            autoComplete="off"
            spellCheck={false}
            maxLength={MAIL_SUPPRESSION_EMAIL_MAX_LENGTH}
            placeholder={t('limits.suppressions.searchPlaceholder')}
            value={address}
            onChange={event => setAddress(event.target.value)}
          />
        </div>
        <Button type="submit" variant="outline" icon={<Search className="size-4" />}>
          {t('limits.suppressions.search')}
        </Button>
        {email ? (
          <Button
            type="button"
            variant="ghost"
            onClick={() => {
              setAddress('');
              setEmail('');
            }}
          >
            {t('limits.suppressions.clearSearch')}
          </Button>
        ) : null}
      </form>

      {remove.error ? (
        <ErrorAlert
          message={errorMessage(remove.error)}
          retryLabel={tm('actions.retry')}
          onRetry={() => {
            remove.reset();
            void list.refetch();
          }}
        />
      ) : null}

      {list.isPending ? (
        <div className="space-y-2" aria-hidden="true">
          <Skeleton className="h-9 w-full" />
          <Skeleton className="h-9 w-full" />
        </div>
      ) : list.error && entries.length === 0 ? (
        isOrganizationLost(list.error) ? (
          <OrganizationLostState />
        ) : (
          <ErrorAlert
            message={errorMessage(list.error)}
            retryLabel={tm('actions.retry')}
            onRetry={() => void list.refetch()}
          />
        )
      ) : entries.length === 0 ? (
        <EmptyState
          icon={<ShieldCheck className="size-5" aria-hidden="true" />}
          title={
            email ? t('limits.suppressions.noMatchTitle') : t('limits.suppressions.emptyTitle')
          }
          description={
            email
              ? t('limits.suppressions.noMatchDescription')
              : t('limits.suppressions.emptyDescription')
          }
        />
      ) : (
        <div className="space-y-3">
          <div className="overflow-x-auto">
            <Table aria-label={t('limits.suppressions.table')}>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('limits.suppressions.columns.email')}</TableHead>
                  <TableHead>{t('limits.suppressions.columns.reason')}</TableHead>
                  <TableHead>{t('limits.suppressions.columns.source')}</TableHead>
                  <TableHead>{t('limits.suppressions.columns.createdAt')}</TableHead>
                  <TableHead>
                    <span className="sr-only">{t('limits.suppressions.columns.actions')}</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {entries.map(entry => (
                  <TableRow key={entry.id}>
                    <TableCell className="max-w-72 truncate font-mono text-xs">
                      {entry.email}
                    </TableCell>
                    <TableCell className="whitespace-nowrap">
                      {t(`limits.suppressions.reasons.${entry.reason}`)}
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-muted-foreground">
                      {t(`limits.suppressions.sources.${entry.source}`)}
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-muted-foreground tabular-nums">
                      {dateFormat.format(new Date(entry.created_at))}
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        type="button"
                        size="sm"
                        variant="ghost"
                        aria-label={t('limits.suppressions.removeLabel', { email: entry.email })}
                        icon={<Trash2 className="size-3.5" />}
                        onClick={() => {
                          remove.reset();
                          setPending(entry);
                        }}
                      >
                        {t('limits.suppressions.remove')}
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
          {list.hasNextPage ? (
            <Button
              type="button"
              variant="outline"
              loading={list.isFetchingNextPage}
              onClick={() => void list.fetchNextPage()}
            >
              {t('limits.suppressions.older')}
            </Button>
          ) : null}
        </div>
      )}

      <ConfirmDialog
        open={pending !== null}
        title={t('limits.suppressions.confirmTitle', { email: pending?.email ?? '' })}
        description={t('limits.suppressions.confirmDescription')}
        confirmLabel={t('limits.suppressions.remove')}
        cancelLabel={t('limits.suppressions.cancel')}
        pending={remove.isPending}
        icon={<Trash2 className="size-4" />}
        onConfirm={confirm}
        onCancel={() => setPending(null)}
      />
    </section>
  );
}
