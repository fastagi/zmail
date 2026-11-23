'use client';

import { useEffect, useRef, useState, type RefObject } from 'react';
import { Plus } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import type { ExternalConnectPreset } from '@/features/mailbox/components/external-connect-dialog';
import { ProviderMark } from '@/features/mailbox/components/provider-mark';
import { useDateTimeFormat } from '@/features/mailbox/components/mail-shared';
import {
  type ExternalMailConnection,
  type ExternalMailConnector,
  type ExternalMailSyncRun,
} from '@/features/mailbox/external-mail-schemas';
import { useDialogReturnFocus } from '@/features/mailbox/hooks/use-dialog-return-focus';
import { useExternalMailSyncRuns } from '@/features/mailbox/hooks/use-external-mail';
import {
  useBeginExternalAuthorization,
  useDisconnectExternalMail,
  usePurgeExternalMail,
} from '@/features/mailbox/hooks/use-external-mail-actions';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import {
  providerName,
  reconnectKind,
  statusDotClassName,
} from '@/features/mailbox/utils/external-mail';
import { useT } from '@/i18n';
import { cn } from '@/utils';

type Confirming = { id: number; kind: 'disconnect' | 'purge' } | null;

/**
 * The member's connected mailboxes (roadmap §5.1, items 6 and 7): each with its provider, status,
 * and the last sync result read from its sync runs; Reconnect for one that lost its grant or was
 * disconnected (an app password connection reopens the connect form), Disconnect with an inline
 * confirmation that says what stops and what is kept, and for a disconnected one, deleting its
 * synced mail now with a confirmation that says it cannot be undone.
 */
export function ConnectedMailboxesDialog({
  open,
  onOpenChange,
  organizationId,
  connections,
  connectors,
  error,
  onAdd,
  onReconnectWithPassword = null,
  returnFocusRef,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  organizationId: number;
  connections: readonly ExternalMailConnection[] | undefined;
  /**
   * The instance's connectors, to tell an app password connection from an OAuth one and to know
   * which still run; undefined while the list is unknown.
   */
  connectors?: readonly ExternalMailConnector[];
  error: boolean;
  /** Opens the connect dialog; null when the instance offers no connector. */
  onAdd: (() => void) | null;
  /** Opens the connect form for an app password connection; null hides its Reconnect. */
  onReconnectWithPassword?: ((preset: ExternalConnectPreset) => void) | null;
  /** Shared with the connect dialog, so focus goes back to where the flow began. */
  returnFocusRef?: RefObject<HTMLElement | null>;
}) {
  const t = useT('mailbox');
  const [confirming, setConfirming] = useState<Confirming>(null);

  const returnFocus = useDialogReturnFocus(open, returnFocusRef);

  return (
    <Dialog
      open={open}
      onOpenChange={next => {
        if (!next) setConfirming(null);
        onOpenChange(next);
      }}
    >
      <DialogContent
        className="sm:max-w-[32rem]"
        onCloseAutoFocus={returnFocus}
        data-return-focus-chain=""
      >
        <DialogHeader>
          <DialogTitle>{t('external.manage.title')}</DialogTitle>
          <DialogDescription>{t('external.manage.description')}</DialogDescription>
        </DialogHeader>
        <DialogBody className="space-y-3">
          {error ? (
            <Alert variant="destructive">
              <AlertDescription>{t('external.manage.loadFailed')}</AlertDescription>
            </Alert>
          ) : connections && connections.length === 0 ? (
            <p className="text-sm text-text-muted">{t('external.manage.empty')}</p>
          ) : (
            <ul className="divide-y divide-border-subtle">
              {(connections ?? []).map(connection => (
                <ConnectionRow
                  key={connection.id}
                  organizationId={organizationId}
                  connection={connection}
                  watching={open}
                  confirming={confirming?.id === connection.id ? confirming.kind : null}
                  onConfirm={kind => setConfirming({ id: connection.id, kind })}
                  onCancel={() => setConfirming(null)}
                  reconnectKind={reconnectKind(connection.provider, connectors)}
                  onPasswordReconnect={
                    onReconnectWithPassword
                      ? () =>
                          onReconnectWithPassword({
                            provider: connection.provider,
                            address: connection.account_address,
                            connectionId: connection.id,
                          })
                      : null
                  }
                />
              ))}
            </ul>
          )}
          {onAdd ? (
            <Button
              type="button"
              variant="outline"
              size="sm"
              icon={<Plus className="size-4" />}
              onClick={onAdd}
            >
              {t('external.manage.add')}
            </Button>
          ) : null}
        </DialogBody>
      </DialogContent>
    </Dialog>
  );
}

function ConnectionRow({
  organizationId,
  connection,
  watching,
  confirming,
  onConfirm,
  onCancel,
  reconnectKind: kind,
  onPasswordReconnect,
}: {
  organizationId: number;
  connection: ExternalMailConnection;
  /** The dialog is open, so the sync runs are read and kept fresh. */
  watching: boolean;
  confirming: 'disconnect' | 'purge' | null;
  onConfirm: (kind: 'disconnect' | 'purge') => void;
  onCancel: () => void;
  /** How this connection is reconnected, decided from its provider and the connector list. */
  reconnectKind: 'oauth' | 'password' | 'unavailable';
  /** Opens the connect form for an app password connection; null when it cannot open here. */
  onPasswordReconnect: (() => void) | null;
}) {
  const t = useT('mailbox');
  const formatter = useDateTimeFormat();
  const reconnect = useBeginExternalAuthorization(organizationId);
  const disconnect = useDisconnectExternalMail(organizationId);
  const purge = usePurgeExternalMail(organizationId);
  const runs = useExternalMailSyncRuns(organizationId, connection.id, { enabled: watching });
  const confirmRef = useRef<HTMLDivElement | null>(null);
  const disconnectRef = useRef<HTMLButtonElement | null>(null);
  const purgeRef = useRef<HTMLButtonElement | null>(null);
  const wasConfirming = useRef<'disconnect' | 'purge' | null>(null);
  const provider = providerName(connection.provider);
  const address = connection.account_address;
  const purgeConflict =
    purge.error instanceof ApiError &&
    purge.error.errorCode === ApiErrorCode.MAILBOX_EXTERNAL_CONNECTION_NOT_REVOKED;

  useEffect(() => {
    // The confirmation takes focus when it opens; focus returns to its button when it closes.
    if (confirming) confirmRef.current?.focus();
    else if (wasConfirming.current === 'disconnect') disconnectRef.current?.focus();
    else if (wasConfirming.current === 'purge') purgeRef.current?.focus();
    wasConfirming.current = confirming;
  }, [confirming]);

  const lastSync = connection.last_synced_at
    ? t('external.manage.lastSync', { time: formatter.format(new Date(connection.last_synced_at)) })
    : t('external.manage.neverSynced');

  return (
    <li className="py-3" data-connection={connection.id}>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <ProviderMark id={connection.provider} />
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-medium text-text-main">{address}</p>
          <p className="flex flex-wrap items-center gap-x-1.5 text-xs text-text-muted">
            <span>{provider}</span>
            <span aria-hidden="true">·</span>
            <span className="inline-flex items-center gap-1">
              <span
                aria-hidden="true"
                className={cn('size-1.5 rounded-full', statusDotClassName(connection.status))}
              />
              {t(`external.status.${connection.status}`)}
            </span>
            {runs.data ? null : (
              <>
                <span aria-hidden="true">·</span>
                <span>{lastSync}</span>
              </>
            )}
          </p>
          <SyncSummary
            runs={runs.data}
            failed={!runs.data && runs.isError}
            format={time => formatter.format(new Date(time))}
          />
        </div>
        <div className="flex shrink-0 items-center gap-2">
          {connection.status !== 'active' &&
          (kind === 'oauth' || (kind === 'password' && onPasswordReconnect)) ? (
            <Button
              type="button"
              size="sm"
              variant="outline"
              loading={reconnect.isPending}
              onClick={() =>
                kind === 'password' && onPasswordReconnect
                  ? onPasswordReconnect()
                  : reconnect.begin({
                      kind: 'reconnect',
                      provider: connection.provider,
                      connectionId: connection.id,
                    })
              }
            >
              {t('external.manage.reconnect')}
            </Button>
          ) : null}
          {connection.status === 'revoked' && !confirming ? (
            <Button
              ref={purgeRef}
              type="button"
              size="sm"
              variant="ghost"
              className="text-error"
              onClick={() => {
                purge.reset();
                onConfirm('purge');
              }}
            >
              {t('external.purge.action')}
            </Button>
          ) : null}
          {connection.status !== 'revoked' && !confirming ? (
            <Button
              ref={disconnectRef}
              type="button"
              size="sm"
              variant="ghost"
              onClick={() => onConfirm('disconnect')}
            >
              {t('external.manage.disconnect')}
            </Button>
          ) : null}
        </div>
      </div>
      {reconnect.error ? (
        <p role="alert" className="mt-2 text-xs text-error">
          {t('external.connect.failed')}
        </p>
      ) : null}
      {connection.status !== 'active' && kind === 'unavailable' ? (
        <p className="mt-2 text-xs text-text-muted">{t('external.manage.connectorOff')}</p>
      ) : null}
      {purgeConflict && !confirming ? (
        <p role="alert" className="mt-2 text-xs text-error">
          {t('external.purge.notRevoked')}
        </p>
      ) : null}
      {confirming === 'purge' ? (
        <div
          ref={confirmRef}
          tabIndex={-1}
          role="group"
          aria-labelledby={`purge-${connection.id}`}
          aria-describedby={`purge-${connection.id}-detail`}
          className="mt-3 space-y-3 rounded-lg border border-error/40 bg-bg-canvas p-3 outline-none"
        >
          <p id={`purge-${connection.id}`} className="text-sm font-medium text-text-main">
            {t('external.purge.confirmTitle', { address })}
          </p>
          <p id={`purge-${connection.id}-detail`} className="text-xs text-text-muted">
            {t('external.purge.confirm')}
          </p>
          {purge.error && !purgeConflict ? (
            <p role="alert" className="text-xs text-error">
              {t('external.purge.failed')}
            </p>
          ) : null}
          <div className="flex flex-wrap justify-end gap-2">
            <Button
              type="button"
              size="sm"
              variant="ghost"
              disabled={purge.isPending}
              onClick={onCancel}
            >
              {t('external.manage.cancel')}
            </Button>
            <Button
              type="button"
              size="sm"
              variant="destructive"
              loading={purge.isPending}
              onClick={() =>
                purge.mutate({ connectionId: connection.id, mailboxId: connection.mailbox_id }, {
                  onSuccess: result => {
                    toast.success(
                      result.completed
                        ? t('external.purge.done', { count: result.deleted_messages, address })
                        : t('external.purge.continuing', {
                            count: result.deleted_messages,
                            address,
                          })
                    );
                    onCancel();
                  },
                  onError: failure => {
                    // A reconnected account keeps its mail: close the confirmation and say why.
                    if (
                      failure instanceof ApiError &&
                      failure.errorCode === ApiErrorCode.MAILBOX_EXTERNAL_CONNECTION_NOT_REVOKED
                    ) {
                      onCancel();
                    }
                  },
                })
              }
            >
              {t('external.purge.confirmAction')}
            </Button>
          </div>
        </div>
      ) : null}
      {confirming === 'disconnect' ? (
        <div
          ref={confirmRef}
          tabIndex={-1}
          role="group"
          aria-labelledby={`disconnect-${connection.id}`}
          className="mt-3 space-y-3 rounded-lg border border-border-main bg-bg-canvas p-3 outline-none"
        >
          <p id={`disconnect-${connection.id}`} className="text-sm font-medium text-text-main">
            {t('external.manage.confirmTitle', { address })}
          </p>
          <p className="text-xs text-text-muted">{t('external.manage.confirm')}</p>
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
              onClick={() =>
                disconnect.mutate({ connectionId: connection.id, mailboxId: connection.mailbox_id }, {
                  onSuccess: () => {
                    toast.success(t('external.manage.disconnected', { address }));
                    onCancel();
                  },
                  onError: () => toast.error(t('external.manage.disconnectFailed')),
                })
              }
            >
              {t('external.manage.confirmAction')}
            </Button>
          </div>
        </div>
      ) : null}
    </li>
  );
}

/** What the newest runs say, in plain words: the last success, the latest outcome, backfill. */
export function syncSummaryOf(runs: readonly ExternalMailSyncRun[]): {
  lastSuccess: string | null;
  latest: ExternalMailSyncRun | null;
  backfilling: boolean;
} {
  const latest = runs[0] ?? null;
  const lastSuccess = runs.find(run => run.outcome === 'ok')?.finished_at ?? null;
  const backfilling =
    latest !== null &&
    ((latest.outcome === 'ok' && latest.more) || latest.outcome === 'cursor_expired');
  return { lastSuccess, latest, backfilling };
}

function SyncSummary({
  runs,
  failed,
  format,
}: {
  runs: readonly ExternalMailSyncRun[] | undefined;
  failed: boolean;
  format: (time: string) => string;
}) {
  const t = useT('mailbox');
  if (failed) {
    return <p className="mt-1 text-xs text-text-muted">{t('external.sync.loadFailed')}</p>;
  }
  if (!runs) return null;
  const { lastSuccess, latest, backfilling } = syncSummaryOf(runs);
  const outcome =
    latest === null
      ? t('external.sync.none')
      : latest.outcome === 'fetch_failed' && latest.timed_out
        ? t('external.sync.outcome.timed_out')
        : t(`external.sync.outcome.${latest.outcome}`);
  const problem =
    latest !== null &&
    latest.outcome !== 'ok' &&
    latest.outcome !== 'cursor_expired' &&
    latest.outcome !== 'dropped';
  return (
    <div className="mt-1 space-y-0.5 text-xs" data-sync-outcome={latest?.outcome ?? 'none'}>
      <p className={cn(problem ? 'text-warning' : 'text-text-muted')}>
        {latest === null
          ? outcome
          : t('external.sync.latest', { outcome, time: format(latest.finished_at) })}
      </p>
      {latest !== null ? (
        <p className="text-text-muted">
          {lastSuccess
            ? t('external.sync.lastSuccess', { time: format(lastSuccess) })
            : t('external.sync.noSuccess')}
          {backfilling ? (
            <>
              <span aria-hidden="true"> · </span>
              <span>{t('external.sync.backfilling')}</span>
            </>
          ) : null}
        </p>
      ) : null}
    </div>
  );
}

export default ConnectedMailboxesDialog;
