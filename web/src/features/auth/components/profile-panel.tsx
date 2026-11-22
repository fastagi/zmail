'use client';

import { useState, type FormEvent } from 'react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { authService } from '@/features/auth/services/auth-service';
import { useAuthStore } from '@/features/auth/store/auth-store';
import { getAuthErrorCode } from '@/features/auth/utils/auth-failure';
import { ApiErrorCode } from '@/http/codes';
import { useT } from '@/i18n';

const maxNameLength = 50;

/** The signed-in user's profile: the name shown to teammates and the sign-in email. */
export function ProfilePanel() {
  const t = useT();
  const user = useAuthStore.use.user();
  const setUser = useAuthStore.use.setUser();
  const [name, setName] = useState(user?.name ?? '');
  const [error, setError] = useState<'required' | 'sessionEnded' | 'failed' | null>(null);
  const [pending, setPending] = useState(false);
  const trimmed = name.trim();

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (trimmed === '') {
      setError('required');
      return;
    }
    setError(null);
    setPending(true);
    try {
      const response = await authService.updateProfile(trimmed);
      setUser(response.user);
      setName(response.user.name);
      toast.success(t('settings.profile.saved'));
    } catch (caught) {
      setError(
        getAuthErrorCode(caught) === ApiErrorCode.AUTH_UNAUTHORIZED ? 'sessionEnded' : 'failed'
      );
    } finally {
      setPending(false);
    }
  };

  return (
    <section aria-labelledby="profile-title" className="max-w-xl space-y-5 py-2">
      <div className="space-y-1">
        <h2 id="profile-title" className="text-lg font-semibold">
          {t('settings.profile.title')}
        </h2>
        <p className="text-sm text-text-muted">{t('settings.profile.description')}</p>
      </div>

      <form onSubmit={submit} noValidate className="space-y-4" aria-busy={pending}>
        <div className="space-y-2">
          <Label htmlFor="profile-name">{t('settings.profile.name')}</Label>
          <Input
            id="profile-name"
            autoComplete="name"
            required
            root
            maxLength={maxNameLength}
            disabled={pending}
            value={name}
            onChange={event => setName(event.target.value)}
            aria-invalid={error === 'required' || undefined}
            aria-describedby={error ? 'profile-error' : undefined}
          />
          {error ? (
            <p id="profile-error" role="alert" className="text-sm text-error">
              {t(`settings.profile.errors.${error}`)}
            </p>
          ) : null}
        </div>
        <div className="space-y-2">
          <Label htmlFor="profile-email">{t('settings.profile.email')}</Label>
          <Input id="profile-email" root readOnly value={user?.email ?? ''} />
          <p className="text-xs text-text-muted">{t('settings.profile.emailHint')}</p>
        </div>
        <Button type="submit" loading={pending} disabled={trimmed === user?.name}>
          {t('settings.profile.submit')}
        </Button>
      </form>
    </section>
  );
}
