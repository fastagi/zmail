'use client';

import { useEffect, useRef, useState, type FormEvent } from 'react';
import { useRouter } from 'next/navigation';
import { AlertCircle } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { authConfig } from '@/config/auth';
import { authService } from '@/features/auth/services/auth-service';
import { useAuthStore } from '@/features/auth/store/auth-store';
import {
  getAuthErrorCode,
  getAuthErrorStatus,
  hasAuthFieldError,
} from '@/features/auth/utils/auth-failure';
import { ApiErrorCode } from '@/http/codes';
import { ROUTES } from '@/constants/routes';
import { useT, type AllTranslationKeys } from '@/i18n';

const minPasswordLength = 6;
const maxPasswordLength = 50;

type SecurityKey = Extract<AllTranslationKeys, `settings.security.${string}`>;

interface FieldErrors {
  currentPassword?: SecurityKey;
  newPassword?: SecurityKey;
  confirmPassword?: SecurityKey;
}

/**
 * Account security for the signed-in user: change the password. The API ends every session of the
 * account when the password changes, so success signs this browser out as well.
 */
export function AccountSecurityPanel() {
  const t = useT();
  const router = useRouter();
  const resetAuth = useAuthStore.use.reset();
  const currentRef = useRef<HTMLInputElement>(null);
  const newRef = useRef<HTMLInputElement>(null);
  const confirmRef = useRef<HTMLInputElement>(null);
  const [currentPassword, setCurrentPassword] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({});
  const [formError, setFormError] = useState<SecurityKey | null>(null);
  const [pending, setPending] = useState(false);
  // The field to focus once the form re-renders enabled with its error showing.
  const focusTarget = useRef<keyof FieldErrors | null>(null);

  useEffect(() => {
    if (pending || !focusTarget.current) return;
    const refs = { currentPassword: currentRef, newPassword: newRef, confirmPassword: confirmRef };
    refs[focusTarget.current].current?.focus();
    focusTarget.current = null;
  });

  const showFieldErrors = (errors: FieldErrors) => {
    setFieldErrors(errors);
    focusTarget.current = errors.currentPassword
      ? 'currentPassword'
      : errors.newPassword
        ? 'newPassword'
        : errors.confirmPassword
          ? 'confirmPassword'
          : null;
  };

  const clearPasswords = () => {
    setCurrentPassword('');
    setNewPassword('');
    setConfirmPassword('');
  };

  const signInAgain = () => {
    resetAuth();
    const returnUrl = encodeURIComponent(ROUTES.CONSOLE.SETTINGS);
    router.replace(`${authConfig.routes.login}?returnUrl=${returnUrl}`);
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setFormError(null);
    const errors = validate(currentPassword, newPassword, confirmPassword);
    showFieldErrors(errors);
    if (focusTarget.current) return;

    setPending(true);
    try {
      await authService.changePassword(currentPassword, newPassword);
      clearPasswords();
      toast.success(t('settings.security.changed'));
      signInAgain();
    } catch (error) {
      // Passwords never stay in memory after a request, whatever its outcome.
      clearPasswords();
      const code = getAuthErrorCode(error);
      if (code === ApiErrorCode.AUTH_INVALID_CREDENTIALS) {
        // A wrong current password is a 401 too, but the session is still valid.
        showFieldErrors({ currentPassword: 'settings.security.errors.currentIncorrect' });
      } else if (code === ApiErrorCode.AUTH_UNAUTHORIZED) {
        toast.error(t('settings.security.errors.sessionEnded'));
        signInAgain();
      } else if (hasAuthFieldError(error, 'newPassword')) {
        showFieldErrors({ newPassword: 'settings.security.errors.newInvalid' });
      } else if (
        code === ApiErrorCode.COMMON_RATE_LIMITED ||
        getAuthErrorStatus(error) === 429
      ) {
        setFormError('settings.security.errors.rateLimited');
      } else {
        setFormError('settings.security.errors.failed');
      }
    } finally {
      setPending(false);
    }
  };

  return (
    <section aria-labelledby="security-password-title" className="max-w-xl space-y-5 py-2">
      <div className="space-y-1">
        <h2 id="security-password-title" className="text-lg font-semibold">
          {t('settings.security.passwordTitle')}
        </h2>
        <p className="text-sm text-text-muted">{t('settings.security.passwordDescription')}</p>
      </div>

      {formError ? (
        <Alert variant="destructive" role="alert">
          <AlertCircle aria-hidden="true" className="size-4" />
          <AlertDescription>{t(formError)}</AlertDescription>
        </Alert>
      ) : null}

      <form onSubmit={submit} noValidate className="space-y-4" aria-busy={pending}>
        <div className="space-y-2">
          <Label htmlFor="security-current-password">{t('settings.security.currentPassword')}</Label>
          <Input
            ref={currentRef}
            id="security-current-password"
            type="password"
            autoComplete="current-password"
            required
            root
            disabled={pending}
            value={currentPassword}
            onChange={event => setCurrentPassword(event.target.value)}
            errorText={fieldErrors.currentPassword ? t(fieldErrors.currentPassword) : undefined}
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="security-new-password">{t('settings.security.newPassword')}</Label>
          <Input
            ref={newRef}
            id="security-new-password"
            type="password"
            autoComplete="new-password"
            required
            minLength={minPasswordLength}
            maxLength={maxPasswordLength}
            root
            aria-describedby={fieldErrors.newPassword ? undefined : 'security-new-password-hint'}
            disabled={pending}
            value={newPassword}
            onChange={event => setNewPassword(event.target.value)}
            errorText={fieldErrors.newPassword ? t(fieldErrors.newPassword) : undefined}
          />
          {fieldErrors.newPassword ? null : (
            <p id="security-new-password-hint" className="text-xs text-text-muted">
              {t('settings.security.newPasswordHint')}
            </p>
          )}
        </div>
        <div className="space-y-2">
          <Label htmlFor="security-confirm-password">{t('settings.security.confirmPassword')}</Label>
          <Input
            ref={confirmRef}
            id="security-confirm-password"
            type="password"
            autoComplete="new-password"
            required
            root
            disabled={pending}
            value={confirmPassword}
            onChange={event => setConfirmPassword(event.target.value)}
            errorText={fieldErrors.confirmPassword ? t(fieldErrors.confirmPassword) : undefined}
          />
        </div>
        <Button type="submit" loading={pending}>
          {t('settings.security.submit')}
        </Button>
      </form>
    </section>
  );
}

function validate(current: string, next: string, confirmation: string): FieldErrors {
  const errors: FieldErrors = {};
  if (!current) {
    errors.currentPassword = 'settings.security.errors.currentRequired';
  }
  if (next.length < minPasswordLength || next.length > maxPasswordLength) {
    errors.newPassword = 'settings.security.errors.newLength';
  } else if (next === current) {
    errors.newPassword = 'settings.security.errors.newUnchanged';
  }
  if (!errors.newPassword && next !== confirmation) {
    errors.confirmPassword = 'settings.security.errors.mismatch';
  }
  return errors;
}
