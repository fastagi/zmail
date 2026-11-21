'use client';

import { useId, useState } from 'react';
import { ArrowLeft, Eye, Lock, LogOut, Mail } from 'lucide-react';

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
  type ExternalMailBackfillDays,
  type ExternalMailConnector,
} from '@/features/mailbox/external-mail-schemas';
import { useBeginExternalAuthorization } from '@/features/mailbox/hooks/use-external-mail-actions';
import { providerName } from '@/features/mailbox/utils/external-mail';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/** Only the authorization code flow is offered; any other kind is listed as coming soon. */
function isSupported(connector: ExternalMailConnector): boolean {
  return connector.auth_kind === 'oauth2' && connector.capabilities.read;
}

/**
 * Add an external mailbox (roadmap §5.1): one button per connector the instance runs, in the
 * API's order, then what connecting means (read-only, private to the member, removed when they
 * leave) with the backfill choice. Continue starts the sign-in and sends the browser to the
 * provider; the callback page finishes it.
 */
export function ExternalConnectDialog({
  open,
  onOpenChange,
  organizationId,
  connectors,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  organizationId: number;
  connectors: readonly ExternalMailConnector[];
}) {
  const t = useT('mailbox');
  const [chosen, setChosen] = useState<ExternalMailConnector | null>(null);
  const [days, setDays] = useState<ExternalMailBackfillDays>(EXTERNAL_MAIL_DEFAULT_BACKFILL_DAYS);
  const authorization = useBeginExternalAuthorization(organizationId);
  const backfillName = useId();

  const reset = () => {
    setChosen(null);
    setDays(EXTERNAL_MAIL_DEFAULT_BACKFILL_DAYS);
    authorization.reset();
  };
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
      <DialogContent className="sm:max-w-md">
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
                          'flex h-11 w-full items-center gap-3 rounded-lg border border-border-main bg-bg-surface px-3 text-left text-sm text-text-main transition-colors focus-ring hover:bg-nav-hover motion-reduce:transition-none',
                          'disabled:cursor-not-allowed disabled:opacity-60 disabled:hover:bg-bg-surface'
                        )}
                        onClick={() => setChosen(connector)}
                      >
                        <ProviderMark id={connector.id} />
                        <span className="min-w-0 flex-1 truncate font-medium">
                          {providerName(connector.id)}
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
        ) : (
          <>
            <DialogHeader>
              <DialogTitle>{t('external.connect.consentTitle', { provider: name })}</DialogTitle>
              <DialogDescription>
                {t('external.connect.consentDescription', { provider: name })}
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
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
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

/** A neutral tile with the provider's initial; brand artwork stays out of the scaffold. */
export function ProviderMark({ id, className }: { id: string; className?: string }) {
  const initial = providerName(id).charAt(0);
  return (
    <span
      aria-hidden="true"
      className={cn(
        'grid size-6 shrink-0 place-items-center rounded-md bg-row-selected text-[11px] font-semibold text-text-muted',
        className
      )}
    >
      {initial || <Mail className="size-3.5" />}
    </span>
  );
}

export default ExternalConnectDialog;
