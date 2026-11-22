'use client';

import { useState, type FormEvent } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useLocale } from 'next-intl';
import { AlertCircle, Copy, ShieldCheck } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import { totpService, type TotpEnrollment } from '@/features/auth/services/totp-service';
import { getAuthErrorCode, getAuthErrorStatus } from '@/features/auth/utils/auth-failure';
import { ApiErrorCode } from '@/http/codes';
import { useT, type AllTranslationKeys } from '@/i18n';

type TwoFactorKey = Extract<AllTranslationKeys, `settings.security.twoFactor.${string}`>;

const totpQueryKey = ['auth', 'totp'] as const;

function errorKey(error: unknown): TwoFactorKey {
  const code = getAuthErrorCode(error);
  if (code === ApiErrorCode.AUTH_TOTP_INVALID) return 'settings.security.twoFactor.errors.codeInvalid';
  if (code === ApiErrorCode.AUTH_TOTP_ALREADY_ENABLED) {
    return 'settings.security.twoFactor.errors.alreadyEnabled';
  }
  if (code === ApiErrorCode.AUTH_UNAUTHORIZED) return 'settings.security.twoFactor.errors.sessionEnded';
  if (code === ApiErrorCode.COMMON_RATE_LIMITED || getAuthErrorStatus(error) === 429) {
    return 'settings.security.twoFactor.errors.rateLimited';
  }
  return 'settings.security.twoFactor.errors.failed';
}

async function copy(text: string, done: string) {
  await navigator.clipboard.writeText(text);
  toast.success(done);
}

/** Turns authenticator-app two-factor sign-in on or off for the signed-in user. */
export function TwoFactorPanel() {
  const t = useT();
  const locale = useLocale();
  const queryClient = useQueryClient();
  const status = useQuery({ queryKey: totpQueryKey, queryFn: totpService.status });
  const [enrollment, setEnrollment] = useState<TotpEnrollment | null>(null);
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null);
  const [code, setCode] = useState('');
  const [codeError, setCodeError] = useState<TwoFactorKey | null>(null);

  const refresh = () => queryClient.invalidateQueries({ queryKey: totpQueryKey });
  const fail = (error: unknown) => {
    setCode('');
    setCodeError(errorKey(error));
  };

  const start = useMutation({
    mutationFn: totpService.startEnrollment,
    retry: false,
    onSuccess: value => {
      setCodeError(null);
      setEnrollment(value);
    },
    onError: fail,
  });
  const confirm = useMutation({
    mutationFn: totpService.confirm,
    retry: false,
    onSuccess: codes => {
      setEnrollment(null);
      setCode('');
      setRecoveryCodes(codes);
      toast.success(t('settings.security.twoFactor.enabled'));
      void refresh();
    },
    onError: fail,
  });
  const disable = useMutation({
    mutationFn: totpService.disable,
    retry: false,
    onSuccess: () => {
      setCode('');
      toast.success(t('settings.security.twoFactor.disabled'));
      void refresh();
    },
    onError: fail,
  });

  const submitCode = (event: FormEvent, action: (value: string) => void) => {
    event.preventDefault();
    const value = code.trim();
    if (!value) {
      setCodeError('settings.security.twoFactor.errors.codeRequired');
      return;
    }
    setCodeError(null);
    action(value);
  };

  const codeField = (id: string, label: TwoFactorKey, pending: boolean, numeric: boolean) => (
    <div className="space-y-2">
      <Label htmlFor={id}>{t(label)}</Label>
      <Input
        id={id}
        autoComplete="one-time-code"
        inputMode={numeric ? 'numeric' : 'text'}
        maxLength={32}
        root
        className="max-w-48 font-mono"
        disabled={pending}
        value={code}
        onChange={event => {
          setCode(event.target.value);
          setCodeError(null);
        }}
        errorText={codeError ? t(codeError) : undefined}
      />
    </div>
  );

  let body: React.ReactNode;
  if (status.isPending) {
    body = <Skeleton className="h-16 w-full" aria-hidden="true" />;
  } else if (status.isError) {
    body = (
      <Alert variant="destructive" role="alert">
        <AlertCircle aria-hidden="true" className="size-4" />
        <AlertDescription>{t('settings.security.twoFactor.loadError')}</AlertDescription>
      </Alert>
    );
  } else if (recoveryCodes) {
    body = (
      <div className="space-y-3 rounded-lg border border-border p-4">
        <h3 className="font-medium">{t('settings.security.twoFactor.recoveryTitle')}</h3>
        <p className="text-sm text-text-muted">{t('settings.security.twoFactor.recoveryDescription')}</p>
        <ul aria-label={t('settings.security.twoFactor.recoveryTitle')} className="grid grid-cols-2 gap-1 font-mono text-sm">
          {recoveryCodes.map(item => (
            <li key={item}>{item}</li>
          ))}
        </ul>
        <div className="flex flex-wrap gap-2">
          <Button
            type="button"
            variant="outline"
            icon={<Copy className="size-4" />}
            onClick={() => void copy(recoveryCodes.join('\n'), t('settings.security.twoFactor.recoveryCopied'))}
          >
            {t('settings.security.twoFactor.copyRecovery')}
          </Button>
          <Button type="button" onClick={() => setRecoveryCodes(null)}>
            {t('settings.security.twoFactor.recoveryDone')}
          </Button>
        </div>
      </div>
    );
  } else if (status.data.enabled) {
    body = (
      <div className="space-y-4">
        <p className="text-sm text-text-muted">
          {t('settings.security.twoFactor.enabledSince', {
            date: status.data.confirmed_at
              ? new Date(status.data.confirmed_at).toLocaleDateString(locale)
              : '',
            count: status.data.recovery_codes_remaining,
          })}
        </p>
        <form
          onSubmit={event => submitCode(event, value => disable.mutate(value))}
          noValidate
          className="space-y-3"
          aria-label={t('settings.security.twoFactor.disableTitle')}
        >
          {codeField('two-factor-disable-code', 'settings.security.twoFactor.disableCode', disable.isPending, false)}
          <Button type="submit" variant="outline" loading={disable.isPending}>
            {t('settings.security.twoFactor.disable')}
          </Button>
        </form>
      </div>
    );
  } else if (!status.data.available) {
    body = <p className="text-sm text-text-muted">{t('settings.security.twoFactor.unavailable')}</p>;
  } else if (enrollment) {
    body = (
      <div className="space-y-4">
        <p className="text-sm">{t('settings.security.twoFactor.stepAdd')}</p>
        <div className="space-y-2">
          <a href={enrollment.otpauth_uri} className="text-sm font-medium text-primary hover:underline">
            {t('settings.security.twoFactor.openApp')}
          </a>
          <p className="text-xs text-text-muted">{t('settings.security.twoFactor.secretLabel')}</p>
          <div className="flex flex-wrap items-center gap-2">
            <code className="rounded bg-bg-surface px-2 py-1 font-mono text-sm break-all">
              {enrollment.secret.replace(/(.{4})/g, '$1 ').trim()}
            </code>
            <Button
              type="button"
              size="sm"
              variant="outline"
              icon={<Copy className="size-4" />}
              onClick={() => void copy(enrollment.secret, t('settings.security.twoFactor.copied'))}
            >
              {t('settings.security.twoFactor.copySecret')}
            </Button>
          </div>
        </div>
        <form onSubmit={event => submitCode(event, value => confirm.mutate(value))} noValidate className="space-y-3">
          <p className="text-sm">{t('settings.security.twoFactor.stepConfirm')}</p>
          {codeField('two-factor-confirm-code', 'settings.security.twoFactor.code', confirm.isPending, true)}
          <div className="flex gap-2">
            <Button type="submit" loading={confirm.isPending}>
              {t('settings.security.twoFactor.confirm')}
            </Button>
            <Button
              type="button"
              variant="ghost"
              disabled={confirm.isPending}
              onClick={() => {
                setEnrollment(null);
                setCode('');
                setCodeError(null);
              }}
            >
              {t('settings.security.twoFactor.cancel')}
            </Button>
          </div>
        </form>
      </div>
    );
  } else {
    body = (
      <div className="space-y-2">
        <Button
          type="button"
          icon={<ShieldCheck className="size-4" />}
          loading={start.isPending}
          onClick={() => start.mutate()}
        >
          {t('settings.security.twoFactor.start')}
        </Button>
        {codeError ? <p role="alert" className="text-sm text-destructive">{t(codeError)}</p> : null}
      </div>
    );
  }

  return (
    <section aria-labelledby="security-two-factor-title" className="max-w-xl space-y-4 border-t border-border py-6">
      <div className="space-y-1">
        <div className="flex items-center gap-2">
          <h2 id="security-two-factor-title" className="text-lg font-semibold">
            {t('settings.security.twoFactor.title')}
          </h2>
          {status.data?.available || status.data?.enabled ? (
            <Badge variant={status.data.enabled ? 'default' : 'secondary'}>
              {t(status.data.enabled ? 'settings.security.twoFactor.on' : 'settings.security.twoFactor.off')}
            </Badge>
          ) : null}
        </div>
        <p className="text-sm text-text-muted">{t('settings.security.twoFactor.description')}</p>
      </div>
      {body}
    </section>
  );
}
