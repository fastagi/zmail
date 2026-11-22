'use client';

import { useEffect, useId, useRef, useState, type FormEvent } from 'react';
import { ArrowLeft } from 'lucide-react';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { DialogBody, DialogFooter } from '@/components/ui/dialog';
import { Input, PasswordInput } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  EXTERNAL_MAIL_BACKFILL_CHOICES,
  EXTERNAL_MAIL_DEFAULT_BACKFILL_DAYS,
  EXTERNAL_MAIL_HOST_MAX_LENGTH,
  EXTERNAL_MAIL_IMAP_PORT,
  EXTERNAL_MAIL_PASSWORD_MAX_LENGTH,
  EXTERNAL_MAIL_SMTP_PORTS,
  EXTERNAL_MAIL_DEFAULT_SMTP_PORT,
  EXTERNAL_MAIL_USERNAME_MAX_LENGTH,
  type ExternalMailBackfillDays,
  type ExternalMailCompletion,
  type ExternalMailSmtpPort,
} from '@/features/mailbox/external-mail-schemas';
import { useBindExternalMailCredentials } from '@/features/mailbox/hooks/use-external-mail-actions';
import { ApiErrorCode, ClientErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { useT } from '@/i18n';
import { cn } from '@/utils';

type FieldName = 'host' | 'username' | 'password' | 'smtpHost';
type FieldErrors = Partial<Record<FieldName, 'required' | 'host'>>;

/** Each refusal of the credentials route, in plain words. */
export type PasswordBindFailure =
  | { kind: 'unreachable' }
  | { kind: 'refused' }
  | { kind: 'taken' }
  | { kind: 'limit' }
  | { kind: 'rateLimited'; minutes: number | null }
  | { kind: 'timedOut' }
  | { kind: 'offline' }
  | { kind: 'mismatch' }
  | { kind: 'notFound' }
  | { kind: 'invalid' }
  | { kind: 'unavailable' }
  | { kind: 'generic' };

export function passwordBindFailure(error: unknown): PasswordBindFailure {
  if (!(error instanceof ApiError)) return { kind: 'generic' };
  switch (error.errorCode) {
    case ApiErrorCode.MAILBOX_EXTERNAL_SERVER_UNREACHABLE:
      return { kind: 'unreachable' };
    case ApiErrorCode.MAILBOX_EXTERNAL_AUTHORIZATION_FAILED:
      return { kind: 'refused' };
    case ApiErrorCode.MAILBOX_EXTERNAL_ACCOUNT_TAKEN:
      return { kind: 'taken' };
    case ApiErrorCode.MAILBOX_EXTERNAL_CONNECTION_LIMIT:
      return { kind: 'limit' };
    case ApiErrorCode.COMMON_RATE_LIMITED:
      return {
        kind: 'rateLimited',
        minutes:
          error.retryAfterSeconds === undefined ? null : Math.ceil(error.retryAfterSeconds / 60),
      };
    // The server may still finish the sign-in after the browser or the BFF stopped waiting.
    case ApiErrorCode.COMMON_TIMEOUT:
    case ClientErrorCode.TIMEOUT:
      return { kind: 'timedOut' };
    case ClientErrorCode.NETWORK_ERROR:
      return { kind: 'offline' };
    case ApiErrorCode.MAILBOX_EXTERNAL_ACCOUNT_MISMATCH:
      return { kind: 'mismatch' };
    case ApiErrorCode.MAILBOX_EXTERNAL_CONNECTION_NOT_FOUND:
      return { kind: 'notFound' };
    case ApiErrorCode.MAILBOX_EXTERNAL_AUTHORIZATION_INVALID:
    case ApiErrorCode.COMMON_VALIDATION_FAILED:
      return { kind: 'invalid' };
    case ApiErrorCode.MAILBOX_EXTERNAL_PROVIDER_UNAVAILABLE:
      return { kind: 'unavailable' };
    default:
      return { kind: 'generic' };
  }
}

/** After this long a sign-in says it is still working: a slow server may take 45 seconds. */
const SLOW_SIGN_IN_MS = 8_000;

/** A server name: letters, digits, dots, and hyphens, without a scheme, path, or port. */
const HOST_PATTERN = /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)*$/iu;

/** The usual outgoing server of an IMAP host: `imap.example.com` sends through `smtp.example.com`. */
export function suggestedSmtpHost(imapHost: string): string {
  const host = imapHost.trim().toLowerCase();
  return host.startsWith('imap.') ? `smtp.${host.slice('imap.'.length)}` : '';
}

/**
 * The app password step of a `password` connector: server, fixed port, login, app password, and
 * the backfill. The password lives in this component's state only until it is submitted, and is
 * cleared then, on Back, and when the dialog closes (which unmounts this form).
 */
export function ExternalPasswordForm({
  organizationId,
  provider,
  providerLabel,
  reconnect = null,
  onBack,
  onConnected,
}: {
  organizationId: number;
  provider: string;
  providerLabel: string;
  /** The connection being restored, when this binds an existing account again. */
  reconnect?: { address: string; connectionId: number } | null;
  onBack: (() => void) | null;
  onConnected: (completion: ExternalMailCompletion, backfillDays: number) => void;
}) {
  const t = useT('mailbox');
  const bind = useBindExternalMailCredentials(organizationId);
  const [host, setHost] = useState('');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [smtpHost, setSmtpHost] = useState('');
  // Once edited, the SMTP server no longer follows the IMAP server.
  const [smtpEdited, setSmtpEdited] = useState(false);
  const [smtpPort, setSmtpPort] = useState<ExternalMailSmtpPort>(EXTERNAL_MAIL_DEFAULT_SMTP_PORT);
  const smtpPortName = `${useId()}-smtp-port`;
  const [days, setDays] = useState<ExternalMailBackfillDays>(EXTERNAL_MAIL_DEFAULT_BACKFILL_DAYS);
  const [errors, setErrors] = useState<FieldErrors>({});
  // The first invalid field after a refused submit; focused once it has rendered its message.
  const [invalidField, setInvalidField] = useState<{ name: FieldName } | null>(null);
  const alertRef = useRef<HTMLDivElement | null>(null);
  const ids = useId();
  const backfillName = `${ids}-backfill`;
  // Read from the bind's rejection, which also covers a refusal before any request (offline).
  const [failure, setFailure] = useState<PasswordBindFailure | null>(null);
  const [slow, setSlow] = useState(false);
  useEffect(() => {
    if (!bind.isPending) {
      setSlow(false);
      return;
    }
    const timer = setTimeout(() => setSlow(true), SLOW_SIGN_IN_MS);
    return () => clearTimeout(timer);
  }, [bind.isPending]);

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (bind.isPending) return;
    const trimmedHost = host.trim();
    const trimmedUser = username.trim();
    const next: FieldErrors = {};
    if (trimmedHost === '') next.host = 'required';
    else if (!HOST_PATTERN.test(trimmedHost)) next.host = 'host';
    if (trimmedUser === '') next.username = 'required';
    if (password === '') next.password = 'required';
    const trimmedSmtp = smtpHost.trim();
    if (trimmedSmtp !== '' && !HOST_PATTERN.test(trimmedSmtp)) next.smtpHost = 'host';
    setErrors(next);
    const first = (['host', 'username', 'password', 'smtpHost'] as const).find(name => next[name]);
    if (first) {
      setInvalidField({ name: first });
      return;
    }
    const secret = password;
    // The password leaves component state before the request starts.
    setPassword('');
    setFailure(null);
    bind
      .bind({
        ...(reconnect ? { connection_id: reconnect.connectionId } : {}),
        provider,
        host: trimmedHost,
        port: EXTERNAL_MAIL_IMAP_PORT,
        username: trimmedUser,
        password: secret,
        ...(trimmedSmtp === '' ? {} : { smtp_host: trimmedSmtp, smtp_port: smtpPort }),
        backfill_days: days,
      })
      .then(
        completion => onConnected(completion, days),
        (error: unknown) => {
          setFailure(passwordBindFailure(error));
          // The error is announced; focus moves to it so the reason is read first.
          requestAnimationFrame(() => alertRef.current?.focus());
        }
      );
  };

  useEffect(() => {
    if (invalidField) document.getElementById(`${ids}-${invalidField.name}`)?.focus();
  }, [invalidField, ids]);

  const fieldError = (name: FieldName) => {
    const value = errors[name];
    if (!value) return undefined;
    if (name === 'smtpHost') return t('external.password.errors.smtpHostFormat');
    return value === 'host'
      ? t('external.password.errors.hostFormat')
      : t(`external.password.errors.${name as 'host' | 'username' | 'password'}Required`);
  };

  return (
    <form noValidate onSubmit={submit} aria-describedby={`${ids}-intro`}>
      <DialogBody className="space-y-4">
        <p id={`${ids}-intro`} className="text-[13px] text-text-subtle">
          {reconnect
            ? t('external.password.reconnectIntro', { address: reconnect.address })
            : t('external.password.intro', { provider: providerLabel })}
        </p>
        <div className="grid gap-3 sm:grid-cols-[1fr_6rem]">
          <div className="space-y-1.5">
            <Label htmlFor={`${ids}-host`}>{t('external.password.host')}</Label>
            <Input
              id={`${ids}-host`}
              name="host"
              inputMode="url"
              autoComplete="off"
              autoCapitalize="none"
              spellCheck={false}
              placeholder={t('external.password.hostPlaceholder')}
              maxLength={EXTERNAL_MAIL_HOST_MAX_LENGTH}
              value={host}
              disabled={bind.isPending}
              aria-invalid={errors.host ? true : undefined}
              errorText={fieldError('host')}
              onChange={event => {
                setHost(event.target.value);
                if (!smtpEdited) setSmtpHost(suggestedSmtpHost(event.target.value));
              }}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor={`${ids}-port`}>{t('external.password.port')}</Label>
            <Input
              id={`${ids}-port`}
              name="port"
              value={EXTERNAL_MAIL_IMAP_PORT}
              readOnly
              aria-describedby={`${ids}-port-hint`}
              className="bg-bg-canvas text-text-muted"
            />
          </div>
        </div>
        <p id={`${ids}-port-hint`} className="-mt-2 text-xs text-text-muted">
          {t('external.password.portHint', { port: EXTERNAL_MAIL_IMAP_PORT })}
        </p>
        <div className="space-y-1.5">
          <Label htmlFor={`${ids}-username`}>{t('external.password.username')}</Label>
          <Input
            id={`${ids}-username`}
            name="username"
            autoComplete="off"
            autoCapitalize="none"
            spellCheck={false}
            placeholder={t('external.password.usernamePlaceholder')}
            maxLength={EXTERNAL_MAIL_USERNAME_MAX_LENGTH}
            value={username}
            disabled={bind.isPending}
            aria-invalid={errors.username ? true : undefined}
            errorText={fieldError('username')}
            onChange={event => setUsername(event.target.value)}
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor={`${ids}-password`}>{t('external.password.password')}</Label>
          <PasswordInput
            id={`${ids}-password`}
            name="app-password"
            autoComplete="off"
            maxLength={EXTERNAL_MAIL_PASSWORD_MAX_LENGTH}
            value={password}
            disabled={bind.isPending}
            showPasswordLabel={t('external.password.show')}
            hidePasswordLabel={t('external.password.hide')}
            aria-invalid={errors.password ? true : undefined}
            aria-describedby={`${ids}-password-hint`}
            errorText={fieldError('password')}
            onChange={event => setPassword(event.target.value)}
          />
          <p id={`${ids}-password-hint`} className="text-xs text-text-muted">
            {t('external.password.passwordHint')}
          </p>
        </div>
        <div className="space-y-1.5">
          <Label htmlFor={`${ids}-smtpHost`}>{t('external.password.smtpHost')}</Label>
          <Input
            id={`${ids}-smtpHost`}
            name="smtp-host"
            inputMode="url"
            autoComplete="off"
            autoCapitalize="none"
            spellCheck={false}
            placeholder={t('external.password.smtpHostPlaceholder')}
            maxLength={EXTERNAL_MAIL_HOST_MAX_LENGTH}
            value={smtpHost}
            disabled={bind.isPending}
            aria-invalid={errors.smtpHost ? true : undefined}
            aria-describedby={`${ids}-smtp-hint`}
            errorText={fieldError('smtpHost')}
            onChange={event => {
              setSmtpEdited(true);
              setSmtpHost(event.target.value);
            }}
          />
          <p id={`${ids}-smtp-hint`} className="text-xs text-text-muted">
            {t('external.password.smtpHint')}
          </p>
        </div>
        {smtpHost.trim() !== '' ? (
          <fieldset className="space-y-2">
            <legend className="mb-2 text-xs text-text-muted">
              {t('external.password.smtpPort')}
            </legend>
            <div className="flex flex-wrap gap-2">
              {EXTERNAL_MAIL_SMTP_PORTS.map(choice => (
                <label
                  key={choice}
                  className={cn(
                    'inline-flex h-8 cursor-pointer items-center rounded-md border px-3 text-[13px] transition-colors focus-within:ring-2 motion-reduce:transition-none',
                    choice === smtpPort
                      ? 'border-brand bg-nav-selected font-semibold text-text-main'
                      : 'border-border-main text-text-subtle hover:bg-nav-hover'
                  )}
                >
                  <input
                    type="radio"
                    name={smtpPortName}
                    value={choice}
                    checked={choice === smtpPort}
                    disabled={bind.isPending}
                    className="sr-only"
                    onChange={() => setSmtpPort(choice)}
                  />
                  {t('external.password.smtpPortOption', { port: choice })}
                </label>
              ))}
            </div>
          </fieldset>
        ) : null}
        <fieldset className="space-y-2">
          <legend className="mb-2 text-xs text-text-muted">{t('external.connect.backfill')}</legend>
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
                  disabled={bind.isPending}
                  className="sr-only"
                  onChange={() => setDays(choice)}
                />
                {t('external.connect.backfillOption', { days: choice })}
              </label>
            ))}
          </div>
        </fieldset>
        {slow ? (
          <p role="status" className="text-xs text-text-muted">
            {t('external.password.slow')}
          </p>
        ) : null}
        {failure && !bind.isPending ? (
          <Alert variant="destructive" ref={alertRef} tabIndex={-1} className="outline-none">
            <AlertDescription data-failure={failure.kind}>
              {failure.kind === 'rateLimited'
                ? failure.minutes === null
                  ? t('external.password.errors.rateLimitedLater')
                  : t('external.password.errors.rateLimited', { minutes: failure.minutes })
                : t(`external.password.errors.${failure.kind}`)}
            </AlertDescription>
          </Alert>
        ) : null}
      </DialogBody>
      <DialogFooter className="gap-2 sm:justify-between">
        {onBack ? (
          <Button
            type="button"
            variant="ghost"
            icon={<ArrowLeft className="size-4" />}
            disabled={bind.isPending}
            onClick={() => {
              setPassword('');
              onBack();
            }}
          >
            {t('external.connect.back')}
          </Button>
        ) : (
          <span />
        )}
        <Button type="submit" loading={bind.isPending}>
          {bind.isPending ? t('external.password.connecting') : t('external.password.connect')}
        </Button>
      </DialogFooter>
    </form>
  );
}

export default ExternalPasswordForm;
