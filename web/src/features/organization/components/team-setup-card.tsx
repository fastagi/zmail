'use client';

import { lazy, Suspense, useContext, useId, useState } from 'react';
import { QueryClientContext } from '@tanstack/react-query';
import { useRouter } from 'next/navigation';
import { AlertCircle, Mail, Users } from 'lucide-react';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { ROUTES } from '@/constants/routes';
import { usePublicMailboxOptions } from '@/features/mailbox/hooks/use-public-mailbox';
import { useWorkspaceModeStore } from '@/features/mailbox/stores/workspace-mode';
import { useCreateOrganization } from '@/features/organization/hooks/use-organizations';
import { createOrganizationSchema } from '@/features/organization/schemas';
import type { Organization } from '@/features/organization/types';
import {
  hasOrganizationFieldError,
  resolveOrganizationErrorKey,
} from '@/features/organization/utils/organization-error';
import { useT } from '@/i18n';
import { cn } from '@/utils';

const ClaimPublicMailboxCard = lazy(async () => ({
  default: (await import('@/features/mailbox/components/claim-public-mailbox-card'))
    .ClaimPublicMailboxCard,
}));

function SimplePersonalModeHero({
  onStart,
  isPending,
}: {
  onStart: () => void;
  isPending: boolean;
}) {
  const t = useT('organization');
  return (
    <div className="rounded-lg border border-primary/25 bg-primary/5 p-4 sm:p-5">
      <div className="flex items-start gap-3">
        <span className="flex size-10 shrink-0 items-center justify-center rounded-md bg-primary/15 text-primary">
          <Mail className="size-5" aria-hidden="true" />
        </span>
        <div className="min-w-0 flex-1 space-y-1">
          <h2 className="text-base font-semibold text-foreground">{t('setup.personalTitle')}</h2>
          <p className="text-sm text-muted-foreground">{t('setup.personalDescription')}</p>
          <div className="pt-2">
            <Button type="button" onClick={onStart} loading={isPending}>
              {t('setup.personalSubmit')}
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}

function QueryAwarePersonalHero({
  onStart,
  isPending,
}: {
  onStart: () => void;
  isPending: boolean;
}) {
  const options = usePublicMailboxOptions();
  const canClaim =
    Boolean(options.data?.enabled) &&
    (options.data?.claimed_count ?? 0) < (options.data?.limit_per_user ?? 0);

  if (canClaim && options.data) {
    return (
      <Suspense fallback={<SimplePersonalModeHero onStart={onStart} isPending={isPending} />}>
        <ClaimPublicMailboxCard
          options={options.data}
          variant="hero"
          onSkip={onStart}
          skipPending={isPending}
        />
      </Suspense>
    );
  }

  return <SimplePersonalModeHero onStart={onStart} isPending={isPending} />;
}

/**
 * The first step for someone who belongs to no team: name it, and go straight on to create the
 * team's first mailbox. The slug is generated; invitees are pointed at their invitation mail.
 */
export function TeamSetupCard({ className }: { className?: string }) {
  const t = useT('organization');
  const titleId = useId();
  const router = useRouter();
  const queryClient = useContext(QueryClientContext);
  const setMode = useWorkspaceModeStore(state => state.setMode);
  const personalMutation = useCreateOrganization();

  const startPersonalMode = () => {
    if (personalMutation.isPending) return;
    personalMutation.mutate(
      { name: 'Personal' },
      {
        onSuccess: organization => {
          setMode('personal');
          router.push(`${ROUTES.CONSOLE.MAILBOXES}?org=${organization.id}`);
        },
      }
    );
  };

  return (
    <section
      aria-labelledby={titleId}
      className={cn('rounded-lg border bg-card p-6 text-left shadow-xs sm:p-8', className)}
    >
      {queryClient ? (
        <QueryAwarePersonalHero
          onStart={startPersonalMode}
          isPending={personalMutation.isPending}
        />
      ) : (
        <SimplePersonalModeHero
          onStart={startPersonalMode}
          isPending={personalMutation.isPending}
        />
      )}

      <div className="my-5 flex items-center gap-3 text-xs text-muted-foreground">
        <div className="h-px flex-1 bg-border" />
        <span>{t('setup.orTeam')}</span>
        <div className="h-px flex-1 bg-border" />
      </div>

      <div className="flex items-start gap-3">
        <span className="flex size-10 shrink-0 items-center justify-center rounded-md bg-muted">
          <Users className="size-5 text-muted-foreground" aria-hidden="true" />
        </span>
        <div className="min-w-0 space-y-1">
          <h2 id={titleId} className="text-lg font-semibold">
            {t('setup.title')}
          </h2>
          <p className="text-sm text-muted-foreground">{t('setup.description')}</p>
        </div>
      </div>
      <TeamSetupForm className="mt-5" />
    </section>
  );
}

/**
 * The team name form on its own. Without `onCreated` it goes on to the new team's first mailbox;
 * the console home's setup guide passes `onCreated` and stays where it is, on the next step.
 */
export function TeamSetupForm({
  className,
  onCreated,
}: {
  className?: string;
  onCreated?: (organization: Organization) => void;
}) {
  const t = useT('organization');
  const router = useRouter();
  const mutation = useCreateOrganization();
  const inputId = useId();
  const [name, setName] = useState('');
  const [invalid, setInvalid] = useState(false);

  const submit = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const parsed = createOrganizationSchema.safeParse({ name: name.trim() });
    if (!parsed.success) {
      setInvalid(true);
      return;
    }
    setInvalid(false);
    mutation.mutate(parsed.data, {
      onSuccess: organization => {
        if (onCreated) {
          onCreated(organization);
          return;
        }
        router.push(`${ROUTES.CONSOLE.MAILBOXES}?org=${organization.id}&new=mailbox`);
      },
    });
  };

  const nameError =
    invalid || hasOrganizationFieldError(mutation.error, 'name') ? t('nameInvalid') : undefined;

  return (
    <form onSubmit={submit} aria-busy={mutation.isPending} className={cn('space-y-3', className)}>
      {mutation.error && !hasOrganizationFieldError(mutation.error, 'name') ? (
        <Alert variant="destructive">
          <AlertCircle aria-hidden="true" className="size-4" />
          <AlertDescription>{t(resolveOrganizationErrorKey(mutation.error))}</AlertDescription>
        </Alert>
      ) : null}
      <div className="space-y-2">
        <Label htmlFor={inputId}>{t('setup.name')}</Label>
        <div className="flex flex-col gap-2 sm:flex-row sm:items-start">
          <Input
            id={inputId}
            name="name"
            autoComplete="organization"
            className="sm:flex-1"
            maxLength={200}
            value={name}
            errorText={nameError}
            disabled={mutation.isPending}
            placeholder={t('setup.namePlaceholder')}
            onChange={event => {
              setName(event.target.value);
              setInvalid(false);
              mutation.reset();
            }}
          />
          <Button type="submit" className="shrink-0" loading={mutation.isPending}>
            {t('setup.submit')}
          </Button>
        </div>
      </div>
      <p className="text-xs text-muted-foreground">{t('setup.invited')}</p>
    </form>
  );
}
