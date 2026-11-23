'use client';

import { useCallback, useState, useSyncExternalStore, type ReactNode } from 'react';
import Link from 'next/link';
import { ArrowRight, Check, Clock, Loader2, Rocket } from 'lucide-react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import { getOrganizationRoute, ROUTES } from '@/constants/routes';
import { useCurrentUser } from '@/features/auth/store/auth-store';
import { useMailOrganization } from '@/features/mailbox/hooks/use-mail-organization';
import {
  useMailboxDomains,
  useMailboxes,
  useMailDomains,
} from '@/features/mailbox/hooks/use-mailbox';
import type { MailDomain, Mailbox } from '@/features/mailbox/types';
import { TeamSetupForm } from '@/features/organization/components/team-setup-card';
import {
  useOrganizationInvitations,
  useOrganizationMembers,
} from '@/features/organization/hooks/use-organizations';
import type {
  OrganizationInvitation,
  OrganizationMember,
} from '@/features/organization/types';
import { useT } from '@/i18n';
import { cn } from '@/utils';

type StepKey = 'team' | 'domain' | 'mailbox' | 'invite';
/** `waiting`: started and moving on its own (DNS, setup, invitations out); `current`: do this next. */
type StepState = 'done' | 'skipped' | 'current' | 'waiting' | 'upcoming';

interface DomainProgress {
  /** The organization's own domain furthest along, if any. */
  domain: MailDomain | undefined;
  phase: 'none' | 'zone' | 'setting_up' | 'records' | 'verified';
}

/**
 * The console home's setup guide: team, company domain, own mailbox, colleagues. It sits at the
 * top of the home page until the team is set up, shows each step's live state (a zone waiting for
 * its name servers, a setup running, who joined), and puts the one next action in front. Someone
 * without a team names it right here; members, who cannot set the team up, do not see it.
 */
export function OnboardingChecklist() {
  const scope = useMailOrganization();
  if (scope.status === 'empty') return <NoTeamGuide />;
  if (scope.status !== 'ready' || !scope.organization.canManage) return null;
  return <TeamGuide organizationId={scope.organization.id} />;
}

function NoTeamGuide() {
  const t = useT('console');
  const steps: { key: StepKey; state: StepState }[] = [
    { key: 'team', state: 'current' },
    { key: 'domain', state: 'upcoming' },
    { key: 'mailbox', state: 'upcoming' },
    { key: 'invite', state: 'upcoming' },
  ];
  return (
    <GuideFrame title={t('home.setup.titleNoTeam')} done={0} total={steps.length}>
      {steps.map((step, index) => (
        <GuideStep key={step.key} step={step.key} state={step.state} index={index}>
          {step.key === 'team' ? (
            <TeamSetupForm
              className="mt-3"
              onCreated={organization =>
                toast.success(t('home.setup.team.created', { name: organization.name }))
              }
            />
          ) : null}
        </GuideStep>
      ))}
    </GuideFrame>
  );
}

function TeamGuide({ organizationId }: { organizationId: number }) {
  const t = useT('console');
  const user = useCurrentUser();
  const mailboxes = useMailboxes(organizationId);
  const members = useOrganizationMembers(organizationId);
  const invitations = useOrganizationInvitations(organizationId);
  const baseDomains = useMailboxDomains(organizationId);
  const shared = baseDomains.data?.find(item => item.kind === 'shared') ?? baseDomains.data?.[0];
  const domainsEnabled = shared?.custom_domains_enabled ?? false;
  const connected = useMailDomains(organizationId, domainsEnabled);
  const [skipped, setSkipped] = useDomainSkip(organizationId);

  // Nothing shows until every list has loaded: a team that is set up never sees the guide flash.
  const loaded =
    mailboxes.data !== undefined &&
    members.data !== undefined &&
    invitations.data !== undefined &&
    baseDomains.data !== undefined &&
    (!domainsEnabled || connected.data !== undefined);
  const ownMailboxes = (mailboxes.data ?? []).filter(mailbox => mailbox.origin !== 'connected');
  const progress = domainProgress(connected.data ?? []);
  const people = colleagues(
    members.data?.items ?? [],
    invitations.data?.items ?? [],
    ownMailboxes,
    user?.id
  );

  const done: Record<StepKey, boolean> = {
    team: true,
    domain: progress.phase === 'verified',
    mailbox: ownMailboxes.length > 0,
    invite: people.joined > 0 && people.withoutMailbox === 0,
  };
  const keys: StepKey[] = domainsEnabled
    ? ['team', 'domain', 'mailbox', 'invite']
    : ['team', 'mailbox', 'invite'];
  const settled = (key: StepKey) => done[key] || (key === 'domain' && skipped);
  if (!loaded || keys.every(settled)) return null;

  const currentKey = keys.find(key => !settled(key));
  const stateOf = (key: StepKey): StepState => {
    if (done[key]) return 'done';
    if (key === 'domain' && skipped) return 'skipped';
    const moving =
      (key === 'domain' && progress.phase !== 'none') ||
      (key === 'invite' && people.pending > 0 && people.withoutMailbox === 0);
    if (key === currentKey) return moving ? 'waiting' : 'current';
    return moving ? 'waiting' : 'upcoming';
  };
  const sharedName = shared?.domain ?? '';
  const verifiedName = progress.phase === 'verified' ? progress.domain?.name : undefined;
  const newMailbox = `${ROUTES.CONSOLE.MAILBOXES}?org=${organizationId}&new=mailbox&type=personal`;
  const invitationsHref = `${getOrganizationRoute(organizationId)}?tab=invitations`;

  const body: Record<StepKey, (state: StepState) => ReactNode> = {
    team: () => null,
    domain: state => {
      if (state === 'skipped') {
        return (
          <StepNote>
            {t('home.setup.domain.skipped', { shared: sharedName })}{' '}
            <button
              type="button"
              className="font-medium text-foreground underline-offset-4 hover:underline focus-ring"
              onClick={() => setSkipped(false)}
            >
              {t('home.setup.domain.undoSkip')}
            </button>
          </StepNote>
        );
      }
      if (state === 'done') {
        return <StepNote>{t('home.setup.domain.verified', { domain: verifiedName ?? '' })}</StepNote>;
      }
      const name = progress.domain?.name ?? '';
      const zone = progress.domain?.zone?.name ?? name;
      return (
        <>
          {progress.phase === 'none' ? (
            <StepHint>{t('home.setup.domain.hint')}</StepHint>
          ) : (
            <StepStatus busy={progress.phase === 'setting_up'}>
              {progress.phase === 'zone'
                ? t('home.setup.domain.pendingZone', { zone })
                : progress.phase === 'setting_up'
                  ? t('home.setup.domain.settingUp', { domain: name })
                  : t('home.setup.domain.pendingRecords', { domain: name })}
            </StepStatus>
          )}
          {state === 'current' || state === 'waiting' ? (
            <StepActions>
              <Button asChild size="sm" variant={progress.phase === 'none' ? 'default' : 'outline'}>
                <Link href={ROUTES.CONSOLE.MAIL_DOMAIN}>
                  {progress.phase === 'none'
                    ? t('home.setup.domain.action')
                    : t('home.setup.domain.view')}
                  <ArrowRight className="size-4" aria-hidden="true" />
                </Link>
              </Button>
              {progress.phase === 'none' && sharedName ? (
                <Button type="button" size="sm" variant="ghost" onClick={() => setSkipped(true)}>
                  {t('home.setup.domain.skip', { shared: sharedName })}
                </Button>
              ) : null}
            </StepActions>
          ) : null}
        </>
      );
    },
    mailbox: state => {
      if (state === 'done') {
        return <StepNote>{t('home.setup.mailbox.done', { count: ownMailboxes.length })}</StepNote>;
      }
      return (
        <>
          <StepHint>{t('home.setup.mailbox.hint')}</StepHint>
          {state === 'current' ? (
            <StepActions>
              <Button asChild size="sm">
                <Link href={newMailbox}>
                  {verifiedName
                    ? t('home.setup.mailbox.actionDomain', { domain: verifiedName })
                    : t('home.setup.mailbox.action')}
                  <ArrowRight className="size-4" aria-hidden="true" />
                </Link>
              </Button>
            </StepActions>
          ) : null}
        </>
      );
    },
    invite: state => {
      const started = people.joined > 0 || people.pending > 0;
      return (
        <>
          {started ? (
            <StepStatus busy={false}>
              {people.pending > 0
                ? t('home.setup.invite.status', { joined: people.joined, pending: people.pending })
                : t('home.setup.invite.statusJoined', { joined: people.joined })}
            </StepStatus>
          ) : (
            <StepHint>{t('home.setup.invite.hint')}</StepHint>
          )}
          {people.withoutMailbox > 0 ? (
            <StepNote>{t('home.setup.invite.noMailbox', { count: people.withoutMailbox })}</StepNote>
          ) : null}
          {state === 'current' || state === 'waiting' ? (
            <StepActions>
              {people.withoutMailbox > 0 ? (
                <Button asChild size="sm">
                  <Link href={newMailbox}>
                    {t('home.setup.invite.giveMailbox')}
                    <ArrowRight className="size-4" aria-hidden="true" />
                  </Link>
                </Button>
              ) : null}
              <Button
                asChild
                size="sm"
                variant={started || people.withoutMailbox > 0 ? 'outline' : 'default'}
              >
                <Link href={invitationsHref}>
                  {started ? t('home.setup.invite.manage') : t('home.setup.invite.action')}
                </Link>
              </Button>
            </StepActions>
          ) : null}
        </>
      );
    },
  };

  return (
    <GuideFrame
      title={t('home.setup.title')}
      done={keys.filter(settled).length}
      total={keys.length}
    >
      {keys.map((key, index) => {
        const state = stateOf(key);
        return (
          <GuideStep key={key} step={key} state={state} index={index}>
            {body[key](state)}
          </GuideStep>
        );
      })}
    </GuideFrame>
  );
}

function GuideFrame({
  title,
  done,
  total,
  children,
}: {
  title: string;
  done: number;
  total: number;
  children: ReactNode;
}) {
  const t = useT('console');
  const titleId = 'setup-guide-title';
  return (
    <section
      aria-labelledby={titleId}
      className="animate-enter mb-8 rounded-xl border bg-bg-surface p-5 sm:p-6"
    >
      <div className="flex items-start gap-4">
        <div className="hidden size-10 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary sm:flex">
          <Rocket className="size-5" aria-hidden="true" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
            <h2 id={titleId} className="text-lg font-semibold">
              {title}
            </h2>
            <p className="text-sm text-text-muted">
              {t('home.setup.progress', { done, total })}
            </p>
          </div>
          <p className="mt-1 text-sm text-text-muted">{t('home.setup.description')}</p>
          <div
            className="mt-3 h-1.5 overflow-hidden rounded-full bg-muted"
            role="progressbar"
            aria-labelledby={titleId}
            aria-valuemin={0}
            aria-valuemax={total}
            aria-valuenow={done}
          >
            <div
              className="h-full rounded-full bg-primary transition-[width] duration-500"
              style={{ width: `${(done / total) * 100}%` }}
            />
          </div>
          <ol aria-labelledby={titleId} className="mt-5 space-y-1">
            {children}
          </ol>
        </div>
      </div>
    </section>
  );
}

function GuideStep({
  step,
  state,
  index,
  children,
}: {
  step: StepKey;
  state: StepState;
  index: number;
  children: ReactNode;
}) {
  const t = useT('console');
  const label = t(`home.setup.${step}.title`);
  const open = state === 'current' || state === 'waiting';
  return (
    <li
      aria-label={label}
      aria-current={state === 'current' ? 'step' : undefined}
      data-step={step}
      data-state={state}
      className={cn(
        'flex gap-3 rounded-lg px-3 py-3 text-sm',
        state === 'current' && 'bg-primary/5 ring-1 ring-primary/20',
        state === 'waiting' && 'bg-muted/50'
      )}
    >
      <span
        aria-hidden="true"
        className={cn(
          'mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full border text-xs',
          state === 'done' && 'border-transparent bg-primary text-primary-foreground',
          state === 'skipped' && 'border-dashed text-text-muted',
          state === 'current' && 'border-primary text-primary',
          state === 'waiting' && 'border-warning text-warning',
          state === 'upcoming' && 'text-text-muted'
        )}
      >
        {state === 'done' ? (
          <Check className="size-3.5" />
        ) : state === 'waiting' ? (
          <Clock className="size-3.5" />
        ) : (
          index + 1
        )}
      </span>
      <div className="min-w-0 flex-1">
        <p className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
          <span
            className={cn(
              'font-medium',
              (state === 'done' || state === 'skipped') && 'text-text-muted',
              state === 'upcoming' && 'text-foreground/80'
            )}
          >
            {label}
          </span>
          {state === 'done' || state === 'skipped' || state === 'waiting' ? (
            <span className="text-xs text-text-muted">{t(`home.setup.state.${state}`)}</span>
          ) : null}
        </p>
        {open || state === 'done' || state === 'skipped' ? children : null}
        {state === 'upcoming' ? <UpcomingHint step={step} /> : null}
      </div>
    </li>
  );
}

/** A short line under a step that is not up yet, so the whole way ahead reads at a glance. */
function UpcomingHint({ step }: { step: StepKey }) {
  const t = useT('console');
  return <p className="mt-0.5 text-text-muted">{t(`home.setup.${step}.short`)}</p>;
}

function StepHint({ children }: { children: ReactNode }) {
  return <p className="mt-1 max-w-2xl text-text-muted">{children}</p>;
}

function StepNote({ children }: { children: ReactNode }) {
  return <p className="mt-0.5 text-text-muted">{children}</p>;
}

function StepStatus({ busy, children }: { busy: boolean; children: ReactNode }) {
  return (
    <p role="status" className="mt-1 flex max-w-2xl items-start gap-1.5 text-foreground/90">
      {busy ? (
        <Loader2 className="mt-0.5 size-3.5 shrink-0 animate-spin" aria-hidden="true" />
      ) : null}
      <span>{children}</span>
    </p>
  );
}

function StepActions({ children }: { children: ReactNode }) {
  return <div className="mt-3 flex flex-wrap items-center gap-2">{children}</div>;
}

/** The organization's own domain furthest along the way to verified, and where it stands. */
export function domainProgress(domains: MailDomain[]): DomainProgress {
  const rank = (domain: MailDomain) =>
    domain.status === 'verified' && !domain.setting_up
      ? 4
      : domain.setting_up
        ? 3
        : domain.zone?.status === 'pending'
          ? 1
          : 2;
  const best = [...domains].sort((a, b) => rank(b) - rank(a))[0];
  if (!best) return { domain: undefined, phase: 'none' };
  const phases = { 1: 'zone', 2: 'records', 3: 'setting_up', 4: 'verified' } as const;
  return { domain: best, phase: phases[rank(best) as 1 | 2 | 3 | 4] };
}

/**
 * Colleagues who joined (everyone but the signed-in manager), invitations still out, and joined
 * colleagues without a personal mailbox of their own.
 */
export function colleagues(
  members: Pick<OrganizationMember, 'user_id'>[],
  invitations: Pick<OrganizationInvitation, 'status'>[],
  mailboxes: Pick<Mailbox, 'type' | 'owner_user_id'>[],
  currentUserId: string | undefined
) {
  const others = members.filter(member => String(member.user_id) !== currentUserId);
  const owners = new Set(
    mailboxes
      .filter(mailbox => mailbox.type === 'personal' && mailbox.owner_user_id !== null)
      .map(mailbox => mailbox.owner_user_id)
  );
  return {
    joined: others.length,
    pending: invitations.filter(invitation => invitation.status === 'pending').length,
    withoutMailbox: others.filter(member => !owners.has(member.user_id)).length,
  };
}

const skipKey = (organizationId: number) => `zmail.setup.domain-skipped.${organizationId}`;
const skipListeners = new Set<() => void>();

function readSkip(organizationId: number): boolean {
  try {
    return window.localStorage.getItem(skipKey(organizationId)) === '1';
  } catch {
    return false;
  }
}

/**
 * Whether this browser chose to go on without an own domain for the team: an individual or a team
 * that is fine with the shared domain. Only a hint for the guide, so browser storage is enough.
 */
function useDomainSkip(organizationId: number): [boolean, (value: boolean) => void] {
  const subscribe = useCallback((listener: () => void) => {
    skipListeners.add(listener);
    return () => skipListeners.delete(listener);
  }, []);
  const stored = useSyncExternalStore(
    subscribe,
    () => readSkip(organizationId),
    () => false
  );
  // Keeps the choice in view where storage is unavailable.
  const [fallback, setFallback] = useState<boolean | null>(null);
  const set = useCallback(
    (value: boolean) => {
      setFallback(value);
      try {
        if (value) window.localStorage.setItem(skipKey(organizationId), '1');
        else window.localStorage.removeItem(skipKey(organizationId));
      } catch {
        // Storage refused: the in-memory choice above still applies until reload.
      }
      for (const listener of skipListeners) listener();
    },
    [organizationId]
  );
  return [fallback ?? stored, set];
}
