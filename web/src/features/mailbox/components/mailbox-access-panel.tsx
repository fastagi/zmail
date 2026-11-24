'use client';

import { lazy, Suspense, useId, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { AlertCircle, RefreshCw, UserX } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import { Switch } from '@/components/ui/switch';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { nativeSelectClassName } from '@/features/mailbox/components/mail-shared';
import {
  HandoverSection,
  SealedSection,
} from '@/features/mailbox/components/mailbox-access-admin-sections';
import { HistorySection } from '@/features/mailbox/components/mailbox-access-history';
import {
  MailboxPermissionSelect,
  memberName,
  useOrganizationPeople,
  useUserNames,
  type MailboxPerson,
} from '@/features/mailbox/components/mailbox-people';
import { MailboxSignatureEditor } from '@/features/mailbox/components/mailbox-signature-editor';
import { MailboxTriageSwitch } from '@/features/mailbox/components/mailbox-triage-switch';
import {
  useMailboxAccessEvents,
  useMailboxMembers,
  useRemoveMailboxMember,
  useSetMailboxMember,
  useSetMailboxWebhooks,
} from '@/features/mailbox/hooks/use-mailbox';
import type { Mailbox, MailboxMember, MailboxPermission } from '@/features/mailbox/types';
import {
  isAccessConflict,
  isPermissionDenied,
  resolveMailboxErrorKey,
  type MailboxErrorKey,
} from '@/features/mailbox/utils/mailbox-error';
import {
  canManageAgentGrants,
  canReviewAgentDrafts,
  needsOwnMembership,
} from '@/features/mailbox/utils/agent-grant-access';
import { mailboxKeys } from '@/features/mailbox/hooks/mailbox-keys';
import { organizationService } from '@/features/organization/services/organization-service';
import { useT } from '@/i18n';

// The Agent tab and everything it loads arrive when it is first opened.
const MailboxAgentsTab = lazy(async () => ({
  default: (await import('@/features/mailbox/components/mailbox-agents-tab')).MailboxAgentsTab,
}));

/** Whether the access panel has anything to show the caller. */
export function hasAccessPanel(mailbox: Mailbox): boolean {
  const { access } = mailbox;
  return (
    access.manage_members ||
    access.see_history ||
    access.administer ||
    canManageAgentGrants(mailbox) ||
    needsOwnMembership(mailbox) ||
    canReviewAgentDrafts(mailbox)
  );
}

/**
 * A failed change. `stale` marks a concurrent change (`MAILBOX.ACCESS_CONFLICT`) or access lost
 * meanwhile (`PERMISSION.DENIED`): the panel offers a refresh for both.
 */
type PanelError = { key: MailboxErrorKey; stale: boolean } | null;

/**
 * Access of one mailbox: its type and owner or members, the members with their permissions
 * (`manage_members`), the webhook switch (shared, `manage_members`), the AI triage switch (the
 * personal owner or `manage_members`, while the organization has triage on), handover of a personal
 * mailbox and deletion of sealed history (`administer`), and the access history (`see_history`).
 * Controls follow `mailbox.access`; the API remains the authority. A concurrent change answers
 * `MAILBOX.ACCESS_CONFLICT` and lost access answers `PERMISSION.DENIED`; both ask for a refresh.
 */
export function MailboxAccessPanel({
  mailbox,
  onClose,
  onRefresh,
}: {
  mailbox: Mailbox | null;
  onClose: () => void;
  /** Reloads the mailbox list, which carries the type, seal, switch, and access. */
  onRefresh: () => void;
}) {
  const t = useT('mailbox');
  return (
    <Dialog open={mailbox !== null} onOpenChange={open => !open && onClose()}>
      <DialogContent size="lg" className="max-h-[90vh] overflow-y-auto">
        {mailbox ? (
          <>
            <DialogHeader>
              <DialogTitle>{t('access.title', { address: mailbox.address })}</DialogTitle>
              <DialogDescription>{t('access.description')}</DialogDescription>
            </DialogHeader>
            <PanelContent key={mailbox.id} mailbox={mailbox} onRefresh={onRefresh} />
          </>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

/**
 * The access sections alone, or with the Agent tab for who may manage the mailbox's agent grants
 * or review its agent drafts (`send`).
 */
function PanelContent({ mailbox, onRefresh }: { mailbox: Mailbox; onRefresh: () => void }) {
  if (needsOwnMembership(mailbox)) {
    return <ConnectedSharedContent mailbox={mailbox} onRefresh={onRefresh} />;
  }
  return (
    <AgentAwareContent
      mailbox={mailbox}
      canManage={canManageAgentGrants(mailbox)}
      onRefresh={onRefresh}
    />
  );
}

function AgentAwareContent({
  mailbox,
  canManage,
  onRefresh,
}: {
  mailbox: Mailbox;
  canManage: boolean;
  onRefresh: () => void;
}) {
  if (!canManage && !canReviewAgentDrafts(mailbox)) {
    return <PanelBody mailbox={mailbox} onRefresh={onRefresh} />;
  }
  return <PanelTabs mailbox={mailbox} canManage={canManage} onRefresh={onRefresh} />;
}

/**
 * A connected shared mailbox, where only the caller's own membership tells a `manage` member
 * from an owner or admin with a lesser one (see `canManageAgentGrants`): the caller's id from the
 * organization context, looked up in the member list. Until both load, grants count as not
 * manageable, so nobody is offered an action the API would refuse.
 */
function ConnectedSharedContent({
  mailbox,
  onRefresh,
}: {
  mailbox: Mailbox;
  onRefresh: () => void;
}) {
  const organizationId = useMailOrganizationId();
  const members = useMailboxMembers(organizationId, mailbox.id, true);
  const context = useQuery({
    // The caller's own organization context; only this check reads it here.
    queryKey: [...mailboxKeys.organization(organizationId), 'own-context'],
    queryFn: () => organizationService.resolveContext(organizationId),
    staleTime: 15_000,
  });
  const userId = context.data?.user_id;
  const ownPermission: MailboxPermission | null | undefined =
    members.data && userId !== undefined
      ? (members.data.find(member => member.user_id === userId)?.permission ?? null)
      : undefined;
  return (
    <AgentAwareContent
      mailbox={mailbox}
      canManage={canManageAgentGrants(mailbox, ownPermission)}
      onRefresh={onRefresh}
    />
  );
}

/** The access sections and the Agent tab; the Agent tab's content loads when first chosen. */
function PanelTabs({
  mailbox,
  canManage,
  onRefresh,
}: {
  mailbox: Mailbox;
  canManage: boolean;
  onRefresh: () => void;
}) {
  const t = useT('mailbox');
  const [tab, setTab] = useState<'access' | 'agents'>('access');
  return (
    <Tabs value={tab} onValueChange={value => setTab(value === 'agents' ? 'agents' : 'access')}>
      <TabsList aria-label={t('agents.tabs.label')} className="w-full sm:w-fit">
        <TabsTrigger value="access">{t('agents.tabs.access')}</TabsTrigger>
        <TabsTrigger value="agents">{t('agents.tabs.agents')}</TabsTrigger>
      </TabsList>
      <TabsContent value="access" className="pt-4">
        <PanelBody mailbox={mailbox} onRefresh={onRefresh} />
      </TabsContent>
      <TabsContent value="agents" className="pt-4">
        {tab === 'agents' ? (
          <Suspense fallback={<AgentsTabFallback label={t('agents.loading')} />}>
            <MailboxAgentsTab mailbox={mailbox} canManage={canManage} />
          </Suspense>
        ) : null}
      </TabsContent>
    </Tabs>
  );
}

function AgentsTabFallback({ label }: { label: string }) {
  return (
    <div role="status" aria-label={label} className="space-y-3">
      <Skeleton className="h-12 w-full" />
      <Skeleton className="h-24 w-full" />
    </div>
  );
}

function PanelBody({ mailbox, onRefresh }: { mailbox: Mailbox; onRefresh: () => void }) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const { access } = mailbox;
  const shared = mailbox.type === 'shared';
  const people = useOrganizationPeople(organizationId, true);
  const nameOf = useUserNames(people.items);
  const showMembers = shared && access.manage_members;
  const members = useMailboxMembers(organizationId, mailbox.id, showMembers);
  const [historyPage, setHistoryPage] = useState(1);
  const events = useMailboxAccessEvents(
    organizationId,
    mailbox.id,
    historyPage,
    access.see_history
  );
  const [error, setError] = useState<PanelError>(null);

  /** Runs one change; a failure is shown at the top of the panel and returned to the caller. */
  const run: Run = async (change, success) => {
    setError(null);
    try {
      await change();
      if (success) toast.success(success);
      return { ok: true };
    } catch (failure) {
      const failed = {
        key: resolveMailboxErrorKey(failure),
        stale: isAccessConflict(failure) || isPermissionDenied(failure),
      };
      setError(failed);
      return { ok: false, ...failed };
    }
  };

  // `refetch` ignores `enabled`, so only the sections this mailbox shows are refetched.
  const refresh = () => {
    setError(null);
    onRefresh();
    if (showMembers) void members.refetch();
    if (access.see_history) void events.refetch();
  };

  return (
    <div className="space-y-6">
      {error ? (
        <Alert variant="destructive" role="alert">
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
            <span>{t(error.key === 'errors.accessConflict' ? 'access.conflict' : error.key)}</span>
            {error.stale ? (
              <Button
                type="button"
                size="xs"
                variant="outline"
                icon={<RefreshCw className="size-3.5" />}
                onClick={refresh}
              >
                {t('access.refresh')}
              </Button>
            ) : null}
          </AlertDescription>
        </Alert>
      ) : null}

      <section className="space-y-1 text-sm">
        <p className="font-medium">
          {t(
            mailbox.awaiting_handover
              ? 'mailboxes.awaitingHandover'
              : shared
                ? 'mailboxes.shared'
                : 'mailboxes.personal'
          )}
        </p>
        {!shared ? (
          <p className="text-muted-foreground">
            {mailbox.owner_user_id === null || mailbox.awaiting_handover
              ? t('access.ownerLeft')
              : t('access.owner', { name: nameOf(mailbox.owner_user_id) })}
          </p>
        ) : null}
        {mailbox.awaiting_handover ? (
          <p className="text-muted-foreground">{t('access.awaiting')}</p>
        ) : null}
      </section>

      {shared && access.manage_members ? (
        <MembersSection
          mailbox={mailbox}
          members={members}
          people={people.items}
          nameOf={nameOf}
          run={run}
        />
      ) : null}

      {shared && access.manage_members ? <WebhookSwitch mailbox={mailbox} run={run} /> : null}

      {access.send ? (
        <MailboxSignatureEditor organizationId={organizationId} mailboxId={mailbox.id} run={run} />
      ) : null}

      <MailboxTriageSwitch organizationId={organizationId} mailbox={mailbox} run={run} />

      {!shared && access.administer ? (
        <HandoverSection mailbox={mailbox} people={people.items} run={run} />
      ) : null}

      {mailbox.sealed_before !== null && access.administer ? (
        <SealedSection mailbox={mailbox} run={run} />
      ) : null}

      {access.see_history ? (
        <HistorySection
          events={events}
          page={historyPage}
          onPageChange={setHistoryPage}
          nameOf={nameOf}
        />
      ) : null}
    </div>
  );
}

type RunResult = { ok: true } | { ok: false; key: MailboxErrorKey; stale: boolean };
export type Run = (change: () => Promise<unknown>, success?: string) => Promise<RunResult>;

/** The members of a shared mailbox with their permissions; needs `manage_members`. */
export function MembersSection({
  mailbox,
  members,
  people,
  nameOf,
  run,
}: {
  mailbox: Pick<Mailbox, 'id'>;
  members: ReturnType<typeof useMailboxMembers>;
  people: MailboxPerson[];
  nameOf: (userId: number | null) => string;
  run: Run;
}) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const setMember = useSetMailboxMember(organizationId, mailbox.id);
  const removeMember = useRemoveMailboxMember(organizationId, mailbox.id);
  const ids = { title: useId(), candidate: useId() };
  const [candidate, setCandidate] = useState('');
  const [permission, setPermission] = useState<MailboxPermission>('send');
  const pending = setMember.isPending || removeMember.isPending;

  const list: MailboxMember[] = members.data ?? [];
  const current = new Set(list.map(member => member.user_id));
  const candidates = people.filter(person => !current.has(person.user_id));

  const add = async () => {
    if (candidate === '') return;
    const added = await run(
      () => setMember.mutateAsync({ userId: Number(candidate), permission }),
      t('messages.memberSaved')
    );
    if (added.ok) setCandidate('');
  };

  return (
    <section className="space-y-3" aria-labelledby={ids.title}>
      <h3 id={ids.title} className="text-sm font-semibold">
        {t('access.members.title')}
      </h3>
      {members.isPending ? (
        <Skeleton className="h-16 w-full" />
      ) : members.error ? (
        <p className="text-sm text-error">{t('access.members.loadFailed')}</p>
      ) : list.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t('access.members.empty')}</p>
      ) : (
        <ul className="divide-y rounded-md border">
          {list.map(member => {
            const name = nameOf(member.user_id);
            return (
              <li key={member.user_id} className="flex flex-wrap items-center gap-2 px-3 py-2">
                <span className="min-w-0 flex-1 truncate text-sm">{name}</span>
                <MailboxPermissionSelect
                  label={t('access.members.permission', { name })}
                  value={member.permission}
                  disabled={pending}
                  onChange={next =>
                    void run(
                      () => setMember.mutateAsync({ userId: member.user_id, permission: next }),
                      t('messages.memberSaved')
                    )
                  }
                />
                <Button
                  type="button"
                  isIcon
                  size="sm"
                  variant="ghost"
                  aria-label={t('access.members.remove', { name })}
                  title={t('access.members.remove', { name })}
                  disabled={pending}
                  onClick={() =>
                    void run(
                      () => removeMember.mutateAsync(member.user_id),
                      t('messages.memberRemoved')
                    )
                  }
                >
                  <UserX className="size-4" />
                </Button>
              </li>
            );
          })}
        </ul>
      )}
      {!members.isPending && !members.error ? (
        candidates.length === 0 ? (
          <p className="text-xs text-muted-foreground">{t('access.members.noCandidates')}</p>
        ) : (
          <div className="flex flex-wrap items-center gap-2">
            <select
              id={ids.candidate}
              aria-label={t('access.members.candidate')}
              className={`${nativeSelectClassName} min-w-0 flex-1`}
              value={candidate}
              disabled={pending}
              onChange={event => setCandidate(event.target.value)}
            >
              <option value="">{t('create.chooseMember')}</option>
              {candidates.map(person => (
                <option key={person.user_id} value={String(person.user_id)}>
                  {memberName(person)}
                </option>
              ))}
            </select>
            <MailboxPermissionSelect
              label={t('access.members.candidatePermission')}
              value={permission}
              disabled={pending}
              onChange={setPermission}
            />
            <Button
              type="button"
              size="sm"
              variant="outline"
              disabled={candidate === '' || pending}
              onClick={() => void add()}
            >
              {t('access.members.add')}
            </Button>
          </div>
        )
      ) : null}
      <p className="text-xs text-muted-foreground">{t('access.permissionHint')}</p>
    </section>
  );
}

function WebhookSwitch({ mailbox, run }: { mailbox: Mailbox; run: Run }) {
  const t = useT('mailbox');
  const setWebhooks = useSetMailboxWebhooks(useMailOrganizationId(), mailbox.id);
  const ids = { label: useId(), hint: useId() };
  return (
    <section className="flex items-start justify-between gap-4 rounded-md border p-3">
      <div className="min-w-0 space-y-1">
        <Label id={ids.label} htmlFor={`${ids.label}-switch`}>
          {t('access.webhooks.label')}
        </Label>
        <p id={ids.hint} className="text-xs text-muted-foreground">
          {t('access.webhooks.hint')}
        </p>
      </div>
      <Switch
        id={`${ids.label}-switch`}
        aria-describedby={ids.hint}
        checked={mailbox.webhooks_enabled}
        disabled={setWebhooks.isPending}
        onCheckedChange={enabled =>
          void run(
            () => setWebhooks.mutateAsync(enabled),
            t(enabled ? 'messages.webhooksOn' : 'messages.webhooksOff')
          )
        }
      />
    </section>
  );
}
