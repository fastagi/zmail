'use client';

import { lazy, Suspense, useId, useState, type RefObject } from 'react';
import { useRouter } from 'next/navigation';
import { ArrowLeft, Eye, KeyRound, Lock, LogOut } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import {
  EXTERNAL_MAIL_BACKFILL_CHOICES,
  EXTERNAL_MAIL_DEFAULT_BACKFILL_DAYS,
  EXTERNAL_MAIL_PASSWORD_AUTH_KIND,
  type ExternalMailBackfillDays,
  type ExternalMailCompletion,
  type ExternalMailConnector,
} from '@/features/mailbox/external-mail-schemas';
import { ProviderMark } from '@/features/mailbox/components/provider-mark';
import { useDialogReturnFocus } from '@/features/mailbox/hooks/use-dialog-return-focus';
import { useBeginExternalAuthorization } from '@/features/mailbox/hooks/use-external-mail-actions';
import { connectedMailboxHref, providerName } from '@/features/mailbox/utils/external-mail';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/** The app password form loads only once a member picks a password connector. */
const ExternalPasswordForm = lazy(
  () => import('@/features/mailbox/components/external-password-form')
);

function isPasswordConnector(connector: ExternalMailConnector): boolean {
  return connector.auth_kind === EXTERNAL_MAIL_PASSWORD_AUTH_KIND;
}

/**
 * The authorization code flow and app passwords are offered; any other kind is listed as coming
 * soon, since new kinds arrive without a contract change.
 */
function isSupported(connector: ExternalMailConnector): boolean {
  return (
    (connector.auth_kind === 'oauth2' || isPasswordConnector(connector)) &&
    connector.capabilities.read
  );
}

/** Opens the dialog straight on a connector's form, to restore one of the member's accounts. */
export interface ExternalConnectPreset {
  provider: string;
  address: string;
  /** The connection to restore; the server checks the login is the same account. */
  connectionId: number;
}

/**
 * Add an external mailbox (roadmap §5.1): one button per connector the instance runs, in the
 * API's order, then what connecting means (read-only, private to the member, removed when they
 * leave) with the backfill choice. Continue starts the sign-in and sends the browser to the
 * provider; the callback page finishes it. A `password` connector (any IMAP server) shows the same
 * statement with what an app password is, then a form; success opens the new mailbox like the
 * callback page does.
 */
export function ExternalConnectDialog({
  open,
  onOpenChange,
  organizationId,
  connectors,
  preset = null,
  returnFocusRef,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  organizationId: number;
  connectors: readonly ExternalMailConnector[];
  /** Opens on a password connector's form to restore that address; null for a new binding. */
  preset?: ExternalConnectPreset | null;
  /** Shared with the dialogs that open this one, so focus goes back to where the flow began. */
  returnFocusRef?: RefObject<HTMLElement | null>;
}) {
  const t = useT('mailbox');
  const returnFocus = useDialogReturnFocus(open, returnFocusRef);
  const router = useRouter();
  // A restore opens on the form even while the connector list is unknown: the connection names
  // its own provider, and the server refuses a connector it no longer runs.
  const [presetConnector] = useState<ExternalMailConnector | null>(() =>
    preset
      ? (connectors.find(item => item.id === preset.provider && isPasswordConnector(item)) ?? {
          id: preset.provider,
          auth_kind: EXTERNAL_MAIL_PASSWORD_AUTH_KIND,
          capabilities: { read: true },
        })
      : null
  );
  const [chosen, setChosen] = useState<ExternalMailConnector | null>(presetConnector);
  // A password connector shows its statement first, then the form.
  const [formShown, setFormShown] = useState(presetConnector !== null);
  const [days, setDays] = useState<ExternalMailBackfillDays>(EXTERNAL_MAIL_DEFAULT_BACKFILL_DAYS);
  const authorization = useBeginExternalAuthorization(organizationId);
  const backfillName = useId();

  const reset = () => {
    setChosen(null);
    setFormShown(false);
    setDays(EXTERNAL_MAIL_DEFAULT_BACKFILL_DAYS);
    authorization.reset();
  };
  const connected = ({ outcome, connection }: ExternalMailCompletion, backfillDays: number) => {
    toast.success(
      outcome === 'created'
        ? t('external.callback.connected', { days: backfillDays })
        : t('external.callback.reconnected')
    );
    reset();
    onOpenChange(false);
    router.push(connectedMailboxHref(organizationId, connection.mailbox_id));
  };
  const password = chosen !== null && isPasswordConnector(chosen);
  const name = chosen ? providerName(chosen.id) : '';
  const unavailable =
    authorization.error instanceof ApiError &&
    authorization.error.errorCode === ApiErrorCode.MAILBOX_EXTERNAL_PROVIDER_UNAVAILABLE;

  return (
    <Dialog
      open={open}
      onOpenChange={next => {
        if (!next) reset();
        onOpenChange(next);
      }}
    >
      <DialogContent
        className="sm:max-w-[28rem]"
        onCloseAutoFocus={returnFocus}
        data-return-focus-chain=""
      >
        {chosen === null ? (
          <>
            <DialogHeader>
              <DialogTitle>{t('external.connect.title')}</DialogTitle>
              <DialogDescription>{t('external.connect.description')}</DialogDescription>
            </DialogHeader>
            <DialogBody>
              <p className="mb-2 text-xs text-text-muted">{t('external.connect.choose')}</p>
              <ul className="flex flex-col gap-2">
                {connectors.map(connector => {
                  const supported = isSupported(connector);
                  return (
                    <li key={connector.id}>
                      <button
                        type="button"
                        disabled={!supported}
                        data-connector={connector.id}
                        className={cn(
                          'flex min-h-11 w-full items-center gap-3 rounded-lg py-2 border border-border-main bg-bg-surface px-3 text-left text-sm text-text-main transition-colors focus-ring hover:bg-nav-hover motion-reduce:transition-none',
                          'disabled:cursor-not-allowed disabled:opacity-60 disabled:hover:bg-bg-surface'
                        )}
                        aria-describedby={
                          connector.id === 'imap' ? `${connector.id}-connector-hint` : undefined
                        }
                        onClick={() => setChosen(connector)}
                      >
                        <ProviderMark id={connector.id} />
                        <span className="min-w-0 flex-1">
                          <span className="block truncate font-medium">
                            {providerName(connector.id)}
                          </span>
                          {connector.id === 'imap' ? (
                            <span
                              id={`${connector.id}-connector-hint`}
                              aria-hidden="true"
                              className="block text-xs text-text-muted"
                            >
                              {t('external.connect.imapHint')}
                            </span>
                          ) : null}
                        </span>
                        {supported ? null : (
                          <Badge variant="outline" className="shrink-0 text-[0.6875rem]">
                            {t('external.connect.comingSoon')}
                          </Badge>
                        )}
                      </button>
                    </li>
                  );
                })}
              </ul>
            </DialogBody>
          </>
        ) : password && formShown ? (
          <>
            <DialogHeader>
              <DialogTitle>{t('external.password.title')}</DialogTitle>
              <DialogDescription>{t('external.password.description')}</DialogDescription>
            </DialogHeader>
            <Suspense fallback={<div className="h-80" aria-hidden="true" />}>
              <ExternalPasswordForm
                organizationId={organizationId}
                provider={chosen.id}
                providerLabel={name}
                reconnect={
                  preset && presetConnector === chosen
                    ? { address: preset.address, connectionId: preset.connectionId }
                    : null
                }
                onBack={() => setFormShown(false)}
                onConnected={connected}
              />
            </Suspense>
          </>
        ) : (
          <>
            <DialogHeader>
              <DialogTitle>{t('external.connect.consentTitle', { provider: name })}</DialogTitle>
              <DialogDescription>
                {password
                  ? t('external.password.consentDescription')
                  : t('external.connect.consentDescription', { provider: name })}
              </DialogDescription>
            </DialogHeader>
            <DialogBody className="space-y-4">
              <ul className="space-y-3 text-[13px] text-text-subtle">
                <ConsentPoint icon={<Eye className="size-4" />}>
                  {t('external.connect.readOnly', { provider: name })}
                </ConsentPoint>
                <ConsentPoint icon={<Lock className="size-4" />}>
                  {t('external.connect.private')}
                </ConsentPoint>
                <ConsentPoint icon={<LogOut className="size-4" />}>
                  {t('external.connect.leaving')}
                </ConsentPoint>
              </ul>
              {password ? (
                <AppPasswordExplainer />
              ) : (
              <fieldset className="space-y-2">
                <legend className="mb-2 text-xs text-text-muted">
                  {t('external.connect.backfill')}
                </legend>
                <div className="flex flex-wrap gap-2">
                  {EXTERNAL_MAIL_BACKFILL_CHOICES.map(choice => (
                    <label
                      key={choice}
                      className={cn(
                        'inline-flex h-8 cursor-pointer items-center rounded-md border px-3 text-[13px] transition-colors focus-within:ring-2 motion-reduce:transition-none',
                        choice === days
                          ? 'border-brand bg-nav-selected font-semibold text-text-main'
                          : 'border-border-main text-text-subtle hover:bg-nav-hover'
                      )}
                    >
                      <input
                        type="radio"
                        name={backfillName}
                        value={choice}
                        checked={choice === days}
                        className="sr-only"
                        onChange={() => setDays(choice)}
                      />
                      {t('external.connect.backfillOption', { days: choice })}
                    </label>
                  ))}
                </div>
              </fieldset>
              )}
              {authorization.error ? (
                <Alert variant="destructive">
                  <AlertDescription>
                    {t(unavailable ? 'external.connect.unavailable' : 'external.connect.failed')}
                  </AlertDescription>
                </Alert>
              ) : null}
            </DialogBody>
            <DialogFooter className="gap-2 sm:justify-between">
              <Button
                type="button"
                variant="ghost"
                icon={<ArrowLeft className="size-4" />}
                disabled={authorization.isPending}
                onClick={reset}
              >
                {t('external.connect.back')}
              </Button>
              {password ? (
                <Button type="button" onClick={() => setFormShown(true)}>
                  {t('external.password.continue')}
                </Button>
              ) : (
                <Button
                  type="button"
                  loading={authorization.isPending}
                  onClick={() =>
                    authorization.begin({ kind: 'new', provider: chosen.id, backfillDays: days })
                  }
                >
                  {authorization.isPending
                    ? t('external.connect.redirecting', { provider: name })
                    : t('external.connect.continue', { provider: name })}
                </Button>
              )}
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

/**
 * What an app password is and where providers keep it. The lines are guidance only: any server
 * with IMAP over TLS works, and nothing here depends on which provider the member uses.
 */
function AppPasswordExplainer() {
  const t = useT('mailbox');
  return (
    <div className="space-y-2 rounded-lg border border-border-main bg-bg-canvas p-3 text-[13px]">
      <p className="flex items-start gap-2.5 font-medium text-text-main">
        <KeyRound aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-text-muted" />
        <span>{t('external.password.appPasswordTitle')}</span>
      </p>
      <p className="text-text-subtle">{t('external.password.appPasswordBody')}</p>
      <details className="group text-text-subtle">
        <summary className="cursor-pointer rounded-sm text-xs font-medium text-text-main focus-ring">
          {t('external.password.whereTitle')}
        </summary>
        <ul className="mt-2 list-disc space-y-1 ps-5 text-xs">
          <li>{t('external.password.where.apple')}</li>
          <li>{t('external.password.where.yahoo')}</li>
          <li>{t('external.password.where.fastmail')}</li>
          <li>{t('external.password.where.chinese')}</li>
          <li>{t('external.password.where.other')}</li>
        </ul>
      </details>
    </div>
  );
}

function ConsentPoint({ icon, children }: { icon: React.ReactNode; children: React.ReactNode }) {
  return (
    <li className="flex items-start gap-2.5">
      <span aria-hidden="true" className="mt-0.5 shrink-0 text-text-muted">
        {icon}
      </span>
      <span>{children}</span>
    </li>
  );
}

export default ExternalConnectDialog;
