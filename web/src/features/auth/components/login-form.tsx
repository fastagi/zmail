'use client';

import { useState } from 'react';
import Link from 'next/link';
import { useSearchParams } from 'next/navigation';
import { AlertCircle } from 'lucide-react';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { authConfig } from '@/config/auth';
import { useLogin } from '@/features/auth/hooks/use-auth';
import { getAuthErrorCode } from '@/features/auth/utils/auth-failure';
import { ApiErrorCode } from '@/http/codes';
import { hasAuthFieldError, resolveAuthErrorKey } from '@/features/auth/utils/auth-error';
import { withReturnUrl } from '@/features/auth/utils/return-url';
import { useT } from '@/i18n';

interface LoginFormProps {
  demoCredentials?: {
    email: string;
    password: string;
  };
  /** False when the API only creates accounts through invitations. */
  registrationEnabled?: boolean;
}

/** Login entry point with localized, contract-aware mutation feedback. */
export function LoginForm({ demoCredentials, registrationEnabled = true }: LoginFormProps) {
  const t = useT();
  const { mutate: login, isPending, error, reset } = useLogin();
  const returnUrl = useSearchParams()?.get('returnUrl') ?? null;
  const [email, setEmail] = useState(demoCredentials?.email ?? '');
  const [password, setPassword] = useState(demoCredentials?.password ?? '');
  const [totpCode, setTotpCode] = useState('');
  const errorCode = getAuthErrorCode(error);
  // The password was right and the account asks for its second factor.
  const [needsCode, setNeedsCode] = useState(false);
  if (
    !needsCode &&
    (errorCode === ApiErrorCode.AUTH_TOTP_REQUIRED || errorCode === ApiErrorCode.AUTH_TOTP_INVALID)
  ) {
    setNeedsCode(true);
  }
  const emailError = hasAuthFieldError(error, 'email') ? t.auth('emailInvalid') : undefined;
  const passwordError = hasAuthFieldError(error, 'password')
    ? t.auth('passwordInvalid')
    : undefined;

  const clearMutationError = () => {
    if (error) {
      reset();
    }
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    login(needsCode ? { email, password, totpCode: totpCode.trim() } : { email, password });
    setTotpCode('');
  };

  return (
    <Card className="border-none shadow-none bg-transparent">
      <CardHeader className="space-y-1 px-0">
        <CardTitle className="text-2xl font-bold tracking-tight">{t.auth('signIn')}</CardTitle>
        <CardDescription>{t.auth('signInToContinue')}</CardDescription>
      </CardHeader>
      <CardContent className="px-0 pt-4">
        <form onSubmit={handleSubmit} className="space-y-4" aria-busy={isPending}>
          {error && (
            <Alert variant="destructive">
              <AlertCircle aria-hidden="true" className="size-4" />
              <AlertDescription>{t(resolveAuthErrorKey(error, 'login'))}</AlertDescription>
            </Alert>
          )}

          <div className="space-y-2">
            <Label htmlFor="email">{t.auth('email')}</Label>
            <Input
              id="email"
              name="email"
              type="email"
              autoComplete="email"
              placeholder={t.auth('enterEmail')}
              required
              disabled={isPending}
              className="bg-bg-canvas"
              value={email}
              errorText={emailError}
              onChange={event => {
                setEmail(event.target.value);
                clearMutationError();
              }}
            />
          </div>
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <Label htmlFor="password">{t.auth('password')}</Label>
              {demoCredentials ? (
                <span className="text-sm text-text-muted">{t.auth('demoCredentialsHint')}</span>
              ) : (
                <Link
                  href={authConfig.routes.forgotPassword}
                  className="text-sm font-medium text-primary hover:underline"
                >
                  {t.auth('forgotPassword')}
                </Link>
              )}
            </div>
            <Input
              id="password"
              name="password"
              type="password"
              autoComplete="current-password"
              required
              disabled={isPending}
              className="bg-bg-canvas"
              value={password}
              errorText={passwordError}
              onChange={event => {
                setPassword(event.target.value);
                clearMutationError();
              }}
            />
          </div>
          {needsCode ? (
            <div className="space-y-2">
              <Label htmlFor="totp-code">{t.auth('totpCode')}</Label>
              <Input
                id="totp-code"
                name="totp-code"
                autoComplete="one-time-code"
                inputMode="numeric"
                autoFocus
                required
                maxLength={32}
                disabled={isPending}
                className="bg-bg-canvas font-mono"
                aria-describedby="totp-code-hint"
                value={totpCode}
                onChange={event => {
                  setTotpCode(event.target.value);
                  if (errorCode !== ApiErrorCode.AUTH_TOTP_REQUIRED) clearMutationError();
                }}
              />
              <p id="totp-code-hint" className="text-xs text-text-muted">
                {t.auth('totpCodeHint')}
              </p>
              <button
                type="button"
                className="text-sm font-medium text-primary hover:underline"
                onClick={() => {
                  setNeedsCode(false);
                  setTotpCode('');
                  setPassword('');
                  reset();
                }}
              >
                {t.auth('totpBack')}
              </button>
            </div>
          ) : null}
          <Button type="submit" className="w-full font-semibold" loading={isPending}>
            {t.auth('login')}
          </Button>
        </form>

        {demoCredentials && (
          <div className="mt-4 rounded-lg border border-border/60 bg-bg-surface/80 p-3 text-sm text-text-subtle">
            <p className="font-medium text-text-main">{t.auth('demoCredentials')}</p>
            <p>
              {demoCredentials.email} / {demoCredentials.password}
            </p>
          </div>
        )}
      </CardContent>
      <CardFooter className="px-0 flex flex-wrap items-center justify-center gap-1.5 text-sm text-muted-foreground mt-2">
        {registrationEnabled ? (
          <>
            {t.auth('noAccount')}
            <Link
              href={withReturnUrl(authConfig.routes.register, returnUrl)}
              className="font-medium text-primary hover:underline hover:text-primary-deep transition-colors"
            >
              {t.auth('signUp')}
            </Link>
          </>
        ) : (
          t.auth('inviteOnly')
        )}
      </CardFooter>
    </Card>
  );
}
