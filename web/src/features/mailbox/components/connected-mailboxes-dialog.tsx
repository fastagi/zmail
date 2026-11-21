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
import { ProviderMark } from '@/features/mailbox/components/external-connect-dialog';
import { useDateTimeFormat } from '@/features/mailbox/components/mail-shared';
import type { ExternalMailConnection } from '@/features/mailbox/external-mail-schemas';
import { useDialogReturnFocus } from '@/features/mailbox/hooks/use-dialog-return-focus';
import {
  useBeginExternalAuthorization,
  useDisconnectExternalMail,
} from '@/features/mailbox/hooks/use-external-mail-actions';
import { providerName, statusDotClassName } from '@/features/mailbox/utils/external-mail';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/**
 * The member's connected mailboxes (roadmap §5.1, items 6 and 7): each with its provider, status,
 * and last sync; Reconnect for one that lost its grant or was disconnected, and Disconnect with an
 * inline confirmation that says what stops and what is kept.
 */
export function ConnectedMailboxesDialog({
  open,
  onOpenChange,
  organizationId,
  connections,
  error,
  onAdd,
  returnFocusRef,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  organizationId: number;
  connections: readonly ExternalMailConnection[] | undefined;
  error: boolean;
  /** Opens the connect dialog; null when the instance offers no connector. */
  onAdd: (() => void) | null;
  /** Shared with the connect dialog, so focus goes back to where the flow began. */
  returnFocusRef?: RefObject<HTMLElement | null>;
}) {
  const t = useT('mailbox');
  const [confirming, setConfirming] = useState<number | null>(null);
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
        className="sm:max-w-lg"
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
                  confirming={confirming === connection.id}
                  onConfirm={() => setConfirming(connection.id)}
                  onCancel={() => setConfirming(null)}
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
  confirming,
  onConfirm,
  onCancel,
}: {
  organizationId: number;
  connection: ExternalMailConnection;
  confirming: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const t = useT('mailbox');
  const formatter = useDateTimeFormat();
  const reconnect = useBeginExternalAuthorization(organizationId);
  const disconnect = useDisconnectExternalMail(organizationId);
  const confirmRef = useRef<HTMLDivElement | null>(null);
  const disconnectRef = useRef<HTMLButtonElement | null>(null);
  const wasConfirming = useRef(false);
  const provider = providerName(connection.provider);
  const address = connection.account_address;

  useEffect(() => {
    // The confirmation takes focus when it opens; focus returns to Disconnect when it closes.
    if (confirming) confirmRef.current?.focus();
    else if (wasConfirming.current) disconnectRef.current?.focus();
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
            <span aria-hidden="true">·</span>
            <span>{lastSync}</span>
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          {connection.status !== 'active' ? (
            <Button
              type="button"
              size="sm"
              variant="outline"
              loading={reconnect.isPending}
              onClick={() =>
                reconnect.begin({
                  kind: 'reconnect',
                  provider: connection.provider,
                  connectionId: connection.id,
                })
              }
            >
              {t('external.manage.reconnect')}
            </Button>
          ) : null}
          {connection.status !== 'revoked' && !confirming ? (
            <Button
              ref={disconnectRef}
              type="button"
              size="sm"
              variant="ghost"
              onClick={onConfirm}
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
      {confirming ? (
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
                disconnect.mutate(connection.id, {
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

export default ConnectedMailboxesDialog;
