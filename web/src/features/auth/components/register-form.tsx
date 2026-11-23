'use client';

import { useEffect, useState } from 'react';
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
import { Checkbox } from '@/components/ui/checkbox';
import { useRegister } from '@/features/auth/hooks/use-auth';
import { authService } from '@/features/auth/services/auth-service';
import {
  hasAuthFieldError,
  registrationCodeErrorKey,
  resolveAuthErrorKey,
} from '@/features/auth/utils/auth-error';
import { getAuthErrorCode } from '@/features/auth/utils/auth-failure';
import { withReturnUrl } from '@/features/auth/utils/return-url';
import { authConfig } from '@/config/auth';
import { env } from '@/config/env';
import { ApiErrorCode } from '@/http/codes';
import { useT } from '@/i18n';
import { GoogleSignInButton } from '@/features/auth/components/google-sign-in-button';

/** Seconds before another code can be requested; the API refuses sooner requests silently. */
const resendCooldownSeconds = 60;

/**
 * Two-step registration on one page: mail a 6-digit code to the address, then register with the
 * code, a name, and a password. The code request never reveals whether the address is taken.
 */
interface RegisterFormProps {
  /** True when the API has a Google OAuth client, so "Continue with Google" works. */
  googleEnabled?: boolean;
}

export function RegisterForm({ googleEnabled = false }: RegisterFormProps = {}) {
  const t = useT();
  // Consent is asked only for the legal documents the deployment links to (#83): asking users to
  // accept documents they cannot open is not meaningful consent.
  const termsUrl = env.NEXT_PUBLIC_TERMS_URL;
  const privacyUrl = env.NEXT_PUBLIC_PRIVACY_URL;
  const consentRequired = Boolean(termsUrl || privacyUrl);
  const { mutate: register, isPending, error, reset } = useRegister();
  const returnUrl = useSearchParams()?.get('returnUrl') ?? null;
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [code, setCode] = useState('');
  const [acceptedTerms, setAcceptedTerms] = useState(false);
  const [codeSentTo, setCodeSentTo] = useState<string | null>(null);
  const [sendingCode, setSendingCode] = useState(false);
  const [codeRequestFailed, setCodeRequestFailed] = useState<'rate' | 'other' | null>(null);
  const [resendAt, setResendAt] = useState(0);
  const [now, setNow] = useState(0);
  const cooldown = Math.max(0, Math.ceil((resendAt - now) / 1000));

  useEffect(() => {
    if (!resendAt) {
      return;
    }
    const timer = setInterval(() => {
      const current = Date.now();
      setNow(current);
      if (current >= resendAt) {
        clearInterval(timer);
      }
    }, 1000);
    return () => clearInterval(timer);
  }, [resendAt]);

  const codeError =
    hasAuthFieldError(error, 'code') || registrationCodeErrorKey(getAuthErrorCode(error))
      ? t.auth('enterVerificationCode')
      : undefined;
  const nameError = hasAuthFieldError(error, 'name') ? t.auth('nameInvalid') : undefined;
  const emailError = hasAuthFieldError(error, 'email') ? t.auth('emailInvalid') : undefined;
  const passwordError = hasAuthFieldError(error, 'password')
    ? t.auth('passwordInvalid')
    : undefined;

  const clearMutationError = () => {
    if (error) {
      reset();
    }
  };

  const sendCode = async () => {
    const address = email.trim();
    if (!address || sendingCode || cooldown > 0) {
      return;
    }
    setCodeRequestFailed(null);
    setSendingCode(true);
    try {
      await authService.requestRegistrationCode(address);
      const sentAt = Date.now();
      setCodeSentTo(address);
      setNow(sentAt);
      setResendAt(sentAt + resendCooldownSeconds * 1000);
    } catch (failure) {
      setCodeRequestFailed(
        getAuthErrorCode(failure) === ApiErrorCode.COMMON_RATE_LIMITED ? 'rate' : 'other'
      );
    } finally {
      setSendingCode(false);
    }
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!codeSentTo) {
      void sendCode();
      return;
    }
    if (consentRequired && !acceptedTerms) {
      return;
    }

    register({ name, email: email.trim(), password, code });
  };

  return (
    <Card className="border-none shadow-none bg-transparent">
      <CardHeader className="space-y-1 px-0">
        <CardTitle className="text-2xl font-bold tracking-tight">
          {t.auth('createAccount')}
        </CardTitle>
        <CardDescription>{t.auth('getStarted')}</CardDescription>
      </CardHeader>
      <CardContent className="px-0 pt-4">
        <form onSubmit={handleSubmit} className="space-y-4" aria-busy={isPending}>
          {error && (
            <Alert variant="destructive">
              <AlertCircle aria-hidden="true" className="size-4" />
              <AlertDescription>{t(resolveAuthErrorKey(error, 'register'))}</AlertDescription>
            </Alert>
          )}
          {codeRequestFailed && (
            <Alert variant="destructive">
              <AlertCircle aria-hidden="true" className="size-4" />
              <AlertDescription>
                {codeRequestFailed === 'rate'
                  ? t('errors.rateLimited')
                  : t.auth('registrationCodeRequestFailed')}
              </AlertDescription>
            </Alert>
          )}

          <div className="space-y-2">
            <Label htmlFor="email">{t.auth('email')}</Label>
            <div className="flex gap-2">
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
              <Button
                type="button"
                variant="outline"
                className="shrink-0"
                disabled={isPending || cooldown > 0 || !email.trim()}
                loading={sendingCode}
                onClick={() => void sendCode()}
              >
                {cooldown > 0
                  ? t.auth('resendCodeIn', { seconds: cooldown })
                  : codeSentTo
                    ? t.auth('resendCode')
                    : t.auth('sendCode')}
              </Button>
            </div>
            {codeSentTo && (
              <p role="status" className="text-xs text-muted-foreground">
                {t.auth('registrationCodeSent', { email: codeSentTo })}
              </p>
            )}
          </div>

          {codeSentTo && (
            <>
              <div className="space-y-2">
                <Label htmlFor="code">{t.auth('verificationCode')}</Label>
                <Input
                  id="code"
                  name="code"
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  placeholder={t.auth('enterVerificationCode')}
                  required
                  disabled={isPending}
                  className="bg-bg-canvas font-mono tracking-widest"
                  value={code}
                  errorText={codeError}
                  onChange={event => {
                    setCode(event.target.value.replace(/\D/gu, '').slice(0, 6));
                    clearMutationError();
                  }}
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="name">{t.auth('fullName')}</Label>
                <Input
                  id="name"
                  name="name"
                  autoComplete="name"
                  placeholder={t.auth('enterFullName')}
                  required
                  disabled={isPending}
                  className="bg-bg-canvas"
                  value={name}
                  errorText={nameError}
                  onChange={event => {
                    setName(event.target.value);
                    clearMutationError();
                  }}
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="password">{t.auth('password')}</Label>
                <Input
                  id="password"
                  name="password"
                  type="password"
                  autoComplete="new-password"
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

              {consentRequired ? (
                <div className="flex items-start space-x-2 pt-2">
                  <Checkbox
                    id="terms"
                    name="terms"
                    checked={acceptedTerms}
                    disabled={isPending}
                    onCheckedChange={checked => {
                      setAcceptedTerms(Boolean(checked));
                      clearMutationError();
                    }}
                    required
                  />
                  <div className="grid gap-1.5 leading-none">
                    <label
                      htmlFor="terms"
                      className="text-xs font-medium leading-none peer-disabled:cursor-not-allowed peer-disabled:opacity-70"
                    >
                      {t.auth('agreeToTerms')}{' '}
                      {termsUrl ? (
                        <LegalLink href={termsUrl}>{t.auth('termsOfService')}</LegalLink>
                      ) : null}
                      {termsUrl && privacyUrl ? ` ${t.auth('and')} ` : null}
                      {privacyUrl ? (
                        <LegalLink href={privacyUrl}>{t.auth('privacyPolicy')}</LegalLink>
                      ) : null}
                    </label>
                  </div>
                </div>
              ) : null}

              <Button
                type="submit"
                className="w-full font-semibold"
                disabled={consentRequired && !acceptedTerms}
                loading={isPending}
              >
                {t.auth('signUp')}
              </Button>
            </>
          )}
        </form>

        {googleEnabled && (
          <div className="mt-4">
            <GoogleSignInButton returnUrl={returnUrl} />
          </div>
        )}
      </CardContent>
      <CardFooter className="px-0 flex flex-wrap items-center justify-center gap-1.5 text-sm text-muted-foreground mt-2">
        {t.auth('hasAccount')}
        <Link
          href={withReturnUrl(authConfig.routes.login, returnUrl)}
          className="font-medium text-primary hover:underline hover:text-primary-deep transition-colors"
        >
          {t.auth('signIn')}
        </Link>
      </CardFooter>
    </Card>
  );
}

/** A legal document opened in a new tab so the half-filled registration form is kept. */
function LegalLink({ href, children }: { href: string; children: React.ReactNode }) {
  return (
    <a
      href={href}
      target="_blank"
      rel="noopener noreferrer"
      className="font-semibold text-foreground underline underline-offset-2 hover:text-primary"
    >
      {children}
    </a>
  );
}
