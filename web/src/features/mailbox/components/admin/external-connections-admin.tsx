'use client';

import { Fragment, useEffect, useRef, useState } from 'react';
import { EyeOff, Link2 } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
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
import { ProviderMark } from '@/features/mailbox/components/external-connect-dialog';
import {
  MailOrganizationBoundary,
  MailPageHeader,
  OrganizationLostState,
  useMailOrganizationId,
} from '@/features/mailbox/components/mail-organization';
import {
  EmptyState,
  ErrorAlert,
  useDateTimeFormat,
} from '@/features/mailbox/components/mail-shared';
import type { ExternalMailDirectoryEntry } from '@/features/mailbox/external-mail-schemas';
import { useExternalMailDirectory } from '@/features/mailbox/hooks/use-external-mail';
import { useForceDisconnectExternalMail } from '@/features/mailbox/hooks/use-external-mail-actions';
import { useMailOrganization } from '@/features/mailbox/hooks/use-mail-organization';
import { providerName, statusDotClassName } from '@/features/mailbox/utils/external-mail';
import { isOrganizationLost, resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/**
 * Admin page of the outside accounts members connected (roadmap §5.2): who connected which
 * provider, with the address masked, its status and times, and a forced Disconnect. Message
 * contents and counts never reach this page; the API returns none. Owners and admins only.
 */
export function ExternalConnectionsAdmin() {
  const t = useT('mailbox');
  const scope = useMailOrganization();
  return (
    <div className="space-y-5">
      <MailPageHeader
        title={t('external.directory.title')}
        description={t('external.directory.description')}
        scope={scope}
      />
      <MailOrganizationBoundary scope={scope}>{() => <ExternalConnections />}</MailOrganizationBoundary>
    </div>
  );
}

function ExternalConnections() {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  // Cursors of the pages before the current one; the first page has none.
  const [cursors, setCursors] = useState<string[]>([]);
  const [confirming, setConfirming] = useState<number | null>(null);
  const directory = useExternalMailDirectory(organizationId, cursors.at(-1));
  const meta = directory.data?.meta;
  const items = directory.data?.items ?? [];

  if (isOrganizationLost(directory.error)) return <OrganizationLostState />;

  return (
    <div className="space-y-4">
      <Alert>
        <EyeOff className="size-4" aria-hidden="true" />
        <AlertDescription>{t('external.directory.privacy')}</AlertDescription>
      </Alert>

      {directory.isPending ? (
        <Skeleton className="h-48 w-full" aria-hidden="true" />
      ) : directory.error || !directory.data ? (
        <ErrorAlert
          message={t(resolveMailboxErrorKey(directory.error))}
          retryLabel={t('actions.retry')}
          onRetry={() => void directory.refetch()}
        />
      ) : items.length === 0 && cursors.length === 0 ? (
        <EmptyState
          icon={<Link2 className="size-5" aria-hidden="true" />}
          title={t('external.directory.emptyTitle')}
          description={t('external.directory.empty')}
        />
      ) : (
        <div className="rounded-md border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('external.directory.columns.account')}</TableHead>
                <TableHead>{t('external.directory.columns.provider')}</TableHead>
                <TableHead>{t('external.directory.columns.owner')}</TableHead>
                <TableHead>{t('external.directory.columns.status')}</TableHead>
                <TableHead>{t('external.directory.columns.lastSynced')}</TableHead>
                <TableHead>{t('external.directory.columns.created')}</TableHead>
                <TableHead className="text-right">
                  {t('external.directory.columns.actions')}
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map(entry => (
                <ConnectionRow
                  key={entry.id}
                  organizationId={organizationId}
                  entry={entry}
                  confirming={confirming === entry.id}
                  onConfirm={() => setConfirming(entry.id)}
                  onCancel={() => setConfirming(null)}
                />
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      {/* One page needs no pager. */}
      {cursors.length > 0 || meta?.has_more ? (
        <nav
          aria-label={t('external.directory.pages')}
          className="flex items-center justify-end gap-2"
        >
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={cursors.length === 0}
            onClick={() => {
              setConfirming(null);
              setCursors(previous => previous.slice(0, -1));
            }}
          >
            {t('admin.previous')}
          </Button>
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={!meta?.has_more || !meta.next_cursor || directory.isFetching}
            onClick={() => {
              const next = meta?.next_cursor;
              if (!next) return;
              setConfirming(null);
              setCursors(previous => [...previous, next]);
            }}
          >
            {t('admin.next')}
          </Button>
        </nav>
      ) : null}
    </div>
  );
}

function ConnectionRow({
  organizationId,
  entry,
  confirming,
  onConfirm,
  onCancel,
}: {
  organizationId: number;
  entry: ExternalMailDirectoryEntry;
  confirming: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const t = useT('mailbox');
  const formatter = useDateTimeFormat();
  const disconnect = useForceDisconnectExternalMail(organizationId);
  const confirmRef = useRef<HTMLDivElement | null>(null);
  const triggerRef = useRef<HTMLButtonElement | null>(null);
  const wasConfirming = useRef(false);
  const address = entry.masked_account_address;
  const owner =
    entry.owner_display_name ??
    (entry.owner_user_id === null
      ? t('external.directory.formerMember')
      : t('external.directory.memberId', { id: entry.owner_user_id }));

  useEffect(() => {
    // The confirmation takes focus when it opens; focus returns to Disconnect when it closes.
    if (confirming) confirmRef.current?.focus();
    else if (wasConfirming.current) triggerRef.current?.focus();
    wasConfirming.current = confirming;
  }, [confirming]);

  const run = () =>
    disconnect.mutate(entry.id, {
      onSuccess: () => {
        toast.success(t('external.directory.disconnected', { address }));
        onCancel();
      },
      onError: failure => {
        if (
          failure instanceof ApiError &&
          failure.errorCode === ApiErrorCode.MAILBOX_EXTERNAL_CONNECTION_NOT_FOUND
        ) {
          toast.error(t('external.directory.gone'));
          onCancel();
        }
      },
    });

  return (
    <Fragment>
      <TableRow data-connection={entry.id}>
        <TableCell className="font-medium">
          <span className="inline-flex items-center gap-2">
            <ProviderMark id={entry.provider} />
            <span className="whitespace-nowrap">{address}</span>
          </span>
        </TableCell>
        <TableCell className="whitespace-nowrap">{providerName(entry.provider)}</TableCell>
        <TableCell className="whitespace-nowrap">{owner}</TableCell>
        <TableCell>
          <span className="inline-flex items-center gap-1.5 whitespace-nowrap">
            <span
              aria-hidden="true"
              className={cn('size-1.5 rounded-full', statusDotClassName(entry.status))}
            />
            {t(`external.status.${entry.status}`)}
          </span>
        </TableCell>
        <TableCell className="whitespace-nowrap text-text-muted">
          {entry.last_synced_at
            ? formatter.format(new Date(entry.last_synced_at))
            : t('external.directory.never')}
        </TableCell>
        <TableCell className="whitespace-nowrap text-text-muted">
          {formatter.format(new Date(entry.created_at))}
        </TableCell>
        <TableCell className="text-right">
          {entry.status !== 'revoked' && !confirming ? (
            <Button
              ref={triggerRef}
              type="button"
              size="sm"
              variant="ghost"
              aria-label={t('external.directory.disconnectLabel', { address })}
              onClick={() => {
                disconnect.reset();
                onConfirm();
              }}
            >
              {t('external.directory.disconnect')}
            </Button>
          ) : null}
        </TableCell>
      </TableRow>
      {confirming ? (
        <TableRow className="hover:bg-transparent">
          <TableCell colSpan={7} className="whitespace-normal">
            <div
              ref={confirmRef}
              tabIndex={-1}
              role="group"
              aria-labelledby={`force-disconnect-${entry.id}`}
              aria-describedby={`force-disconnect-${entry.id}-detail`}
              className="space-y-3 rounded-lg border border-border-main bg-bg-canvas p-3 outline-none"
            >
              <p
                id={`force-disconnect-${entry.id}`}
                className="text-sm font-medium text-text-main"
              >
                {t('external.directory.confirmTitle', { address, owner })}
              </p>
              <p id={`force-disconnect-${entry.id}-detail`} className="text-xs text-text-muted">
                {t('external.directory.confirm')}
              </p>
              {disconnect.error ? (
                <p role="alert" className="text-xs text-error">
                  {t('external.directory.disconnectFailed')}
                </p>
              ) : null}
              <div className="flex flex-wrap justify-end gap-2">
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  disabled={disconnect.isPending}
                  onClick={onCancel}
                >
                  {t('external.manage.cancel')}
                </Button>
                <Button
                  type="button"
                  size="sm"
                  variant="destructive"
                  loading={disconnect.isPending}
                  onClick={run}
                >
                  {t('external.directory.confirmAction')}
                </Button>
              </div>
            </div>
          </TableCell>
        </TableRow>
      ) : null}
    </Fragment>
  );
}

export default ExternalConnectionsAdmin;
