'use client';

import { useState, type FormEvent } from 'react';
import Link from 'next/link';
import { AlertCircle, CheckCircle2 } from 'lucide-react';
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
import { authService } from '@/features/auth/services/auth-service';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { useT } from '@/i18n';

type Step = 'request' | 'confirm' | 'done';

const minPasswordLength = 6;

/**
 * Two-step password reset: ask for an emailed token, then set a new password with it. The first
 * step never reveals whether the address has an account.
 */
export function ForgotPasswordForm() {
  const t = useT();
  const [step, setStep] = useState<Step>('request');
  const [email, setEmail] = useState('');
  const [token, setToken] = useState('');
  const [password, setPassword] = useState('');
  const [confirmation, setConfirmation] = useState('');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const requestToken = async (event: FormEvent) => {
    event.preventDefault();
    setError(null);
    setPending(true);
    try {
      await authService.requestPasswordReset(email.trim());
      setStep('confirm');
    } catch {
      setError(t.auth('resetRequestFailed'));
    } finally {
      setPending(false);
    }
  };

  const confirm = async (event: FormEvent) => {
    event.preventDefault();
    setError(null);
    if (password.length < minPasswordLength) {
      setError(t.auth('resetPasswordTooShort'));
      return;
    }
    if (password !== confirmation) {
      setError(t.auth('resetPasswordMismatch'));
      return;
    }
    setPending(true);
    try {
      await authService.confirmPasswordReset(token.trim(), password);
      setStep('done');
    } catch (failure) {
      const code = failure instanceof ApiError ? failure.errorCode : '';
      setError(
        t.auth(
          code === ApiErrorCode.AUTH_PASSWORD_RESET_TOKEN_INVALID ||
            code === ApiErrorCode.AUTH_PASSWORD_RESET_TOKEN_EXPIRED
            ? 'resetTokenInvalid'
            : 'resetConfirmFailed'
        )
      );
    } finally {
      setPending(false);
    }
  };

  return (
    <Card className="border-none shadow-none bg-transparent">
      <CardHeader className="space-y-1 px-0">
        <CardTitle className="text-2xl font-bold tracking-tight">{t.auth('resetTitle')}</CardTitle>
        <CardDescription>
          {t.auth(
            step === 'request'
              ? 'resetRequestDescription'
              : step === 'confirm'
                ? 'resetConfirmDescription'
                : 'resetDoneDescription'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className="px-0 pt-4 space-y-4">
        {error ? (
          <Alert variant="destructive">
            <AlertCircle aria-hidden="true" className="size-4" />
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        ) : null}
        {step === 'request' ? (
          <form onSubmit={requestToken} className="space-y-4" aria-busy={pending}>
            <div className="space-y-2">
              <Label htmlFor="reset-email">{t.auth('email')}</Label>
              <Input
                id="reset-email"
                type="email"
                autoComplete="email"
                required
                disabled={pending}
                className="bg-bg-canvas"
                placeholder={t.auth('enterEmail')}
                value={email}
                onChange={event => setEmail(event.target.value)}
              />
            </div>
            <Button type="submit" className="w-full font-semibold" loading={pending}>
              {t.auth('resetSendCode')}
            </Button>
          </form>
        ) : step === 'confirm' ? (
          <form onSubmit={confirm} className="space-y-4" aria-busy={pending}>
            <Alert>
              <CheckCircle2 aria-hidden="true" className="size-4" />
              <AlertDescription>
                {t.auth('resetCodeSent', { email: email.trim() })}
              </AlertDescription>
            </Alert>
            <div className="space-y-2">
              <Label htmlFor="reset-token">{t.auth('resetCode')}</Label>
              <Input
                id="reset-token"
                required
                autoComplete="one-time-code"
                disabled={pending}
                className="bg-bg-canvas font-mono"
                value={token}
                onChange={event => setToken(event.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="reset-password">{t.auth('resetNewPassword')}</Label>
              <Input
                id="reset-password"
                type="password"
                autoComplete="new-password"
                required
                disabled={pending}
                className="bg-bg-canvas"
                value={password}
                onChange={event => setPassword(event.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="reset-confirmation">{t.auth('confirmPassword')}</Label>
              <Input
                id="reset-confirmation"
                type="password"
                autoComplete="new-password"
                required
                disabled={pending}
                className="bg-bg-canvas"
                value={confirmation}
                onChange={event => setConfirmation(event.target.value)}
              />
            </div>
            <Button type="submit" className="w-full font-semibold" loading={pending}>
              {t.auth('resetSetPassword')}
            </Button>
            <Button
              type="button"
              variant="ghost"
              className="w-full"
              disabled={pending}
              onClick={() => {
                setStep('request');
                setError(null);
              }}
            >
              {t.auth('resetSendAgain')}
            </Button>
          </form>
        ) : (
          <Button asChild className="w-full font-semibold">
            <Link href={authConfig.routes.login}>{t.auth('signIn')}</Link>
          </Button>
        )}
      </CardContent>
      <CardFooter className="px-0 flex justify-center text-sm mt-2">
        <Link
          href={authConfig.routes.login}
          className="font-medium text-primary hover:underline hover:text-primary-deep transition-colors"
        >
          {t.auth('backToSignIn')}
        </Link>
      </CardFooter>
    </Card>
  );
}
