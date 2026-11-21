'use client';

import Link from 'next/link';
import { ArrowUpRight, Check, Rocket } from 'lucide-react';

import { getOrganizationRoute, ROUTES } from '@/constants/routes';
import { useMailOrganization } from '@/features/mailbox/hooks/use-mail-organization';
import { useMailboxDomains, useMailboxes } from '@/features/mailbox/hooks/use-mailbox';
import { TeamSetupCard } from '@/features/organization/components/team-setup-card';
import { useOrganizationMembers } from '@/features/organization/hooks/use-organizations';
import { useT } from '@/i18n';
import { cn } from '@/utils';

type StepKey = 'team' | 'mailbox' | 'invite' | 'domain';

/**
 * The console home's getting-started list for the team in view, ticked from what the team has
 * done: name the team, create its first mailbox, invite colleagues, connect its own domain. A user
 * without a team names one right here. Members, who cannot set the team up, and teams with every
 * step done do not see it.
 */
export function OnboardingChecklist() {
  const scope = useMailOrganization();
  if (scope.status === 'empty') return <TeamSetupCard className="mt-10" />;
  if (scope.status !== 'ready' || !scope.organization.canManage) return null;
  return <TeamChecklist organizationId={scope.organization.id} />;
}

function TeamChecklist({ organizationId }: { organizationId: number }) {
  const t = useT('console');
  const mailboxes = useMailboxes(organizationId);
  const members = useOrganizationMembers(organizationId);
  const domains = useMailboxDomains(organizationId);
  // Until a list loads its step reads as not done, so nothing is ticked by mistake.
  const done: Record<StepKey, boolean> = {
    team: true,
    mailbox: (mailboxes.data?.length ?? 0) > 0,
    invite: (members.data?.items.length ?? 0) > 1,
    domain: domains.data?.some(domain => domain.kind === 'organization') ?? false,
  };
  if (Object.values(done).every(Boolean)) return null;
  const hrefs: Record<StepKey, string> = {
    team: getOrganizationRoute(organizationId),
    mailbox: `${ROUTES.CONSOLE.MAILBOXES}?org=${organizationId}&new=mailbox`,
    invite: `${getOrganizationRoute(organizationId)}?tab=invitations`,
    domain: ROUTES.CONSOLE.MAIL_DOMAIN,
  };
  const steps: StepKey[] = ['team', 'mailbox', 'invite', 'domain'];
  const titleId = 'onboarding-checklist-title';

  return (
    <section className="animate-enter mt-10 rounded-xl border bg-bg-surface p-6">
      <div className="flex items-start gap-4">
        <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
          <Rocket className="h-5 w-5" aria-hidden="true" />
        </div>
        <div className="min-w-0 flex-1">
          <h2 id={titleId} className="text-base font-semibold">
            {t('home.gettingStartedTitle')}
          </h2>
          <ol aria-labelledby={titleId} className="mt-3 space-y-2.5">
            {steps.map((step, index) => {
              const label = t(`home.onboarding.${step}.title`);
              return (
                <li
                  key={step}
                  aria-label={label}
                  data-done={String(done[step])}
                  className="flex items-start gap-3 text-sm"
                >
                  <span
                    className={cn(
                      'mt-0.5 flex size-5 shrink-0 items-center justify-center rounded-full border text-[11px]',
                      done[step]
                        ? 'border-primary bg-primary text-primary-foreground'
                        : 'text-text-muted'
                    )}
                    aria-hidden="true"
                  >
                    {done[step] ? <Check className="size-3" /> : index + 1}
                  </span>
                  <div className="min-w-0">
                    {done[step] ? (
                      <span className="text-text-muted line-through">{label}</span>
                    ) : (
                      <Link
                        href={hrefs[step]}
                        className="inline-flex items-center gap-1 font-medium hover:underline"
                      >
                        {label}
                        <ArrowUpRight className="size-3.5 opacity-60" aria-hidden="true" />
                      </Link>
                    )}
                    {done[step] ? null : (
                      <p className="mt-0.5 text-text-muted">
                        {t(`home.onboarding.${step}.hint`)}
                      </p>
                    )}
                  </div>
                </li>
              );
            })}
          </ol>
          <p className="mt-4 text-xs text-text-muted">
            {t('home.onboarding.more')}{' '}
            <Link href={ROUTES.CONSOLE.MAIL_BROADCASTS} className="hover:underline">
              {t('home.onboarding.moreLink')}
            </Link>
          </p>
        </div>
      </div>
    </section>
  );
}
