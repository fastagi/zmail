'use client';

import { useId, useState } from 'react';
import { AlertCircle, RefreshCw, Trash2, UserX } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import { Switch } from '@/components/ui/switch';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import {
  nativeSelectClassName,
  useDateTimeFormat,
} from '@/features/mailbox/components/mail-shared';
import {
  MailboxPermissionSelect,
  memberName,
  useOrganizationPeople,
  useUserNames,
  type MailboxPerson,
} from '@/features/mailbox/components/mailbox-people';
import { MailboxTriageSwitch } from '@/features/mailbox/components/mailbox-triage-switch';
import {
  useDeleteMailboxSealedHistory,
  useHandOverMailbox,
  useMailboxAccessEvents,
  useMailboxMembers,
  useRemoveMailboxMember,
  useSetMailboxMember,
  useSetMailboxWebhooks,
} from '@/features/mailbox/hooks/use-mailbox';
import type {
  Mailbox,
  MailboxAccessEvent,
  MailboxMember,
  MailboxPermission,
} from '@/features/mailbox/types';
import {
  isAccessConflict,
  isPermissionDenied,
  resolveMailboxErrorKey,
  type MailboxErrorKey,
} from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

/** Whether the access panel has anything to show the caller. */
export function hasAccessPanel(mailbox: Mailbox): boolean {
  const { access } = mailbox;
  return access.manage_members || access.see_history || access.administer;
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
            <PanelBody key={mailbox.id} mailbox={mailbox} onRefresh={onRefresh} />
          </>
        ) : null}
      </DialogContent>
    </Dialog>
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

function HandoverSection({
  mailbox,
  people,
  run,
}: {
  mailbox: Mailbox;
  people: MailboxPerson[];
  run: Run;
}) {
  const t = useT('mailbox');
  const handOver = useHandOverMailbox(useMailOrganizationId(), mailbox.id);
  const ids = { title: useId(), successor: useId() };
  const [successor, setSuccessor] = useState('');
  const [confirming, setConfirming] = useState(false);
  const candidates = people.filter(person => person.user_id !== mailbox.owner_user_id);

  return (
    <section className="space-y-3 rounded-md border p-3" aria-labelledby={ids.title}>
      <h3 id={ids.title} className="text-sm font-semibold">
        {t('access.handover.title')}
      </h3>
      <p className="text-sm text-muted-foreground">{t('access.handover.description')}</p>
      <p className="text-sm text-muted-foreground">{t('access.handover.seal')}</p>
      <div className="space-y-2">
        <Label htmlFor={ids.successor}>{t('access.handover.successor')}</Label>
        <select
          id={ids.successor}
          className={nativeSelectClassName}
          value={successor}
          disabled={handOver.isPending}
          onChange={event => {
            setSuccessor(event.target.value);
            setConfirming(false);
          }}
        >
          <option value="">{t('create.chooseMember')}</option>
          {candidates.map(person => (
            <option key={person.user_id} value={String(person.user_id)}>
              {memberName(person)}
            </option>
          ))}
        </select>
      </div>
      {confirming ? (
        <div className="space-y-2">
          <p className="text-sm font-medium">{t('access.handover.confirmHint')}</p>
          <div className="flex flex-wrap gap-2">
            <Button
              type="button"
              size="sm"
              variant="outline"
              disabled={handOver.isPending}
              onClick={() => setConfirming(false)}
            >
              {t('actions.cancel')}
            </Button>
            <Button
              type="button"
              size="sm"
              variant="destructive"
              loading={handOver.isPending}
              onClick={async () => {
                const done = await run(
                  () => handOver.mutateAsync(Number(successor)),
                  t('messages.handedOver')
                );
                if (done.ok) setConfirming(false);
              }}
            >
              {t('access.handover.confirm')}
            </Button>
          </div>
        </div>
      ) : (
        <Button
          type="button"
          size="sm"
          disabled={successor === ''}
          onClick={() => setConfirming(true)}
        >
          {t('access.handover.submit')}
        </Button>
      )}
    </section>
  );
}

function SealedSection({ mailbox, run }: { mailbox: Mailbox; run: Run }) {
  const t = useT('mailbox');
  const formatter = useDateTimeFormat();
  const remove = useDeleteMailboxSealedHistory(useMailOrganizationId(), mailbox.id);
  const inputId = useId();
  const [open, setOpen] = useState(false);
  const [typed, setTyped] = useState('');
  const [failure, setFailure] = useState<MailboxErrorKey | null>(null);
  const matches = typed.trim().toLowerCase() === mailbox.address.toLowerCase();
  const sealedBefore = mailbox.sealed_before ?? '';

  const confirm = async () => {
    const address = typed.trim();
    let counts = { deleted_messages: 0, deleted_broadcasts: 0 };
    setFailure(null);
    const result = await run(async () => {
      counts = await remove.mutateAsync(address);
    });
    if (result.ok) {
      toast.success(
        t('messages.sealedDeleted', {
          messages: counts.deleted_messages,
          broadcasts: counts.deleted_broadcasts,
        })
      );
    } else if (!result.stale) {
      // A wrong address or a passing outage: the dialog stays open to correct or retry.
      setFailure(result.key);
      return;
    }
    close();
  };

  const close = () => {
    setOpen(false);
    setTyped('');
    setFailure(null);
  };

  return (
    <section className="space-y-2 rounded-md border p-3">
      <h3 className="text-sm font-semibold">{t('access.sealed.title')}</h3>
      <p className="text-sm text-muted-foreground">
        {t('access.sealed.description', { date: formatter.format(new Date(sealedBefore)) })}
      </p>
      <Button
        type="button"
        size="sm"
        variant="outline"
        icon={<Trash2 className="size-4" />}
        onClick={() => setOpen(true)}
      >
        {t('access.sealed.delete')}
      </Button>
      <Dialog
        open={open}
        onOpenChange={next => {
          if (next || remove.isPending) return;
          close();
        }}
      >
        <DialogContent size="sm" role="alertdialog">
          <DialogHeader>
            <DialogTitle>{t('access.sealed.confirmTitle')}</DialogTitle>
            <DialogDescription>
              {t('access.sealed.confirmDescription', { address: mailbox.address })}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor={inputId}>{t('access.sealed.confirmLabel')}</Label>
            <Input
              id={inputId}
              value={typed}
              autoComplete="off"
              autoCapitalize="none"
              spellCheck={false}
              placeholder={mailbox.address}
              disabled={remove.isPending}
              onChange={event => setTyped(event.target.value)}
            />
            {failure ? (
              <p role="alert" className="text-sm text-error">
                {t(failure)}
              </p>
            ) : null}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" disabled={remove.isPending} onClick={close}>
              {t('actions.cancel')}
            </Button>
            <Button
              type="button"
              variant="destructive"
              disabled={!matches}
              loading={remove.isPending}
              icon={<Trash2 className="size-4" />}
              onClick={() => void confirm()}
            >
              {t('access.sealed.confirmAction')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  );
}

function HistorySection({
  events,
  page,
  onPageChange,
  nameOf,
}: {
  events: ReturnType<typeof useMailboxAccessEvents>;
  page: number;
  onPageChange: (page: number) => void;
  nameOf: (userId: number | null) => string;
}) {
  const t = useT('mailbox');
  const formatter = useDateTimeFormat();
  const titleId = useId();
  const describe = useEventText(nameOf);

  return (
    <section className="space-y-3">
      <h3 id={titleId} className="text-sm font-semibold">
        {t('access.history.title')}
      </h3>
      {events.isPending ? (
        <Skeleton className="h-24 w-full" />
      ) : events.error || !events.data ? (
        <p className="text-sm text-error">{t('access.history.loadFailed')}</p>
      ) : events.data.items.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t('access.history.empty')}</p>
      ) : (
        <>
          <ul aria-labelledby={titleId} className="divide-y rounded-md border">
            {events.data.items.map(event => (
              <li key={event.id} className="flex flex-wrap justify-between gap-2 px-3 py-2 text-sm">
                <span className="min-w-0">{describe(event)}</span>
                <time
                  dateTime={event.created_at}
                  className="shrink-0 text-xs text-muted-foreground"
                >
                  {formatter.format(new Date(event.created_at))}
                </time>
              </li>
            ))}
          </ul>
          {events.data.meta.last_page > 1 ? (
            <div className="flex items-center justify-between gap-3">
              <Button
                type="button"
                size="xs"
                variant="outline"
                disabled={page <= 1}
                onClick={() => onPageChange(page - 1)}
              >
                {t('access.history.newer')}
              </Button>
              <span className="text-xs text-muted-foreground">
                {t('list.page', { page, total: events.data.meta.last_page })}
              </span>
              <Button
                type="button"
                size="xs"
                variant="outline"
                disabled={page >= events.data.meta.last_page}
                onClick={() => onPageChange(page + 1)}
              >
                {t('access.history.older')}
              </Button>
            </div>
          ) : null}
        </>
      )}
    </section>
  );
}

/** One sentence per event; unknown `detail` keys are ignored. */
function useEventText(nameOf: (userId: number | null) => string) {
  const t = useT('mailbox');
  const count = (value: unknown) => (typeof value === 'number' ? value : 0);
  const permissionOf = (value: unknown) =>
    value === 'view' || value === 'send' || value === 'manage'
      ? t(`access.permissions.${value}`)
      : String(value ?? '');

  return (event: MailboxAccessEvent): string => {
    const actor = nameOf(event.actor_user_id);
    const subject = nameOf(event.subject_user_id);
    const { detail } = event;
    switch (event.type) {
      case 'created':
        return detail.type === 'personal' && event.subject_user_id !== null
          ? t('access.history.createdPersonal', { actor, subject })
          : t('access.history.createdShared', { actor });
      case 'member_set':
        return t('access.history.memberSet', {
          actor,
          subject,
          permission: permissionOf(detail.permission),
        });
      case 'member_removed':
        return detail.reason === 'left_organization'
          ? t('access.history.memberLeft', { subject })
          : t('access.history.memberRemoved', { actor, subject });
      case 'owner_left':
        return t('access.history.ownerLeft', { subject });
      case 'owner_returned':
        return t('access.history.ownerReturned', { subject });
      case 'handed_over':
        return t('access.history.handedOver', { actor, subject });
      case 'sealed_deleted':
        return t('access.history.sealedDeleted', {
          actor,
          messages: count(detail.deleted_messages),
          broadcasts: count(detail.deleted_broadcasts),
        });
      case 'webhooks_changed':
        return t(
          detail.enabled === true ? 'access.history.webhooksOn' : 'access.history.webhooksOff',
          { actor }
        );
      case 'renamed':
        return typeof detail.to === 'string' && detail.to !== ''
          ? t('access.history.renamed', { actor, name: detail.to })
          : t('access.history.renamedCleared', { actor });
      case 'connection_bound':
        return t('access.history.connectionBound', { actor });
      case 'connection_reconnected':
        return t('access.history.connectionReconnected', { actor });
      case 'connection_revoked':
        return t('access.history.connectionRevoked', { actor });
      case 'connection_force_disconnected':
        return t('access.history.connectionForceDisconnected', { actor });
      case 'connection_revoked_on_leave':
        return t('access.history.connectionRevokedOnLeave', { subject });
    }
  };
}
