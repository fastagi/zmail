'use client';

import { useEffect, useId, useMemo, useState, type FormEvent } from 'react';
import {
  AlertCircle,
  ArrowLeft,
  Clock,
  Megaphone,
  Pause,
  Play,
  Plus,
  Save,
  Send,
  Trash2,
  XCircle,
} from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import {
  MailOrganizationBoundary,
  MailPageHeader,
  OrganizationLostState,
  useMailOrganizationId,
} from '@/features/mailbox/components/mail-organization';
import {
  EmptyState,
  ErrorAlert,
  MailHtmlFrame,
  nativeSelectClassName,
  useDateTimeFormat,
} from '@/features/mailbox/components/mail-shared';
import {
  useCancelMailBroadcast,
  useDeleteMailBroadcast,
  useMailBroadcast,
  useMailBroadcastRecipients,
  useMailBroadcasts,
  usePauseMailBroadcast,
  useResumeMailBroadcast,
  useSaveMailBroadcast,
  useSendMailBroadcast,
} from '@/features/mailbox/hooks/use-mail-broadcasts';
import { useMailContactLists } from '@/features/mailbox/hooks/use-mail-contacts';
import { useMailOrganization } from '@/features/mailbox/hooks/use-mail-organization';
import { useMailTemplate, useMailTemplates } from '@/features/mailbox/hooks/use-mail-templates';
import { useMailboxDomains, useMailboxes } from '@/features/mailbox/hooks/use-mailbox';
import {
  MAIL_BROADCAST_CONTACT_VARIABLES,
  MAIL_BROADCAST_LABEL_MAX_LENGTH,
  MAIL_BROADCAST_NAME_MAX_LENGTH,
  MAIL_BROADCAST_TEST_MAX_PERCENT,
  MAIL_BROADCAST_TEST_WAIT_DEFAULT_MINUTES,
  MAIL_BROADCAST_TEST_WAIT_MAX_MINUTES,
  MAIL_BROADCAST_TEST_WAIT_MIN_MINUTES,
  MAIL_TEMPLATE_VALUE_MAX_LENGTH,
} from '@/features/mailbox/schemas';
import type {
  MailBroadcast,
  MailBroadcastStatus,
  MailRecipientStatus,
  MailTemplate,
} from '@/features/mailbox/types';
import {
  isOrganizationLost,
  resolveMailboxErrorKey,
  type MailboxErrorKey,
} from '@/features/mailbox/utils/mailbox-error';
import { renderTemplate, templateFallbacks } from '@/features/mailbox/utils/template-render';
import { useT } from '@/i18n';
import { cn } from '@/utils';

const statusKey = {
  draft: 'broadcasts.statusDraft',
  scheduled: 'broadcasts.statusScheduled',
  sending: 'broadcasts.statusSending',
  paused: 'broadcasts.statusPaused',
  sent: 'broadcasts.statusSent',
  canceled: 'broadcasts.statusCanceled',
} as const satisfies Record<MailBroadcastStatus, string>;

const recipientKey = {
  pending: 'broadcasts.pending',
  sent: 'broadcasts.sent',
  failed: 'broadcasts.failed',
  skipped: 'broadcasts.skipped',
  canceled: 'broadcasts.canceledCount',
} as const satisfies Record<MailRecipientStatus, string>;

const recipientStatuses: readonly MailRecipientStatus[] = [
  'pending',
  'sent',
  'failed',
  'skipped',
  'canceled',
];

/** Short codes the API reports for recipients, with readable copy; others are shown as codes. */
const reasonKey = {
  contact_not_subscribed: 'broadcasts.reasonNotSubscribed',
  contact_deleted: 'broadcasts.reasonDeleted',
  'template.variables_missing': 'broadcasts.reasonVariables',
  'transport.permanent_bounce': 'broadcasts.reasonBounce',
  'transport.suppressed': 'broadcasts.reasonSuppressed',
} as const;

function knownReason(code: string): code is keyof typeof reasonKey {
  return Object.hasOwn(reasonKey, code);
}

/** Why a broadcast is paused, with readable copy. */
const pauseKey = {
  test_wave_complete: 'broadcasts.pausedTestWave',
  bounce_rate_high: 'broadcasts.pausedBounces',
  complaint_rate_high: 'broadcasts.pausedComplaints',
  paused_by_owner: 'broadcasts.pausedByOwner',
} as const;

function knownPause(code: string): code is keyof typeof pauseKey {
  return Object.hasOwn(pauseKey, code);
}

/** The domain part of an address, which groups mailboxes in the sender picker. */
function domainOf(address: string): string {
  return address.slice(address.lastIndexOf('@') + 1);
}

/**
 * Broadcasts console: drafts, sending, scheduling, progress, and per-recipient outcomes. The
 * worker sends in the background; running broadcasts refresh on their own.
 */
export function MailBroadcasts() {
  const t = useT('mailbox');
  const scope = useMailOrganization();
  return (
    <div className="space-y-5">
      <MailPageHeader
        title={t('broadcasts.title')}
        description={t('broadcasts.description')}
        scope={scope}
      />
      <MailOrganizationBoundary scope={scope}>
        {organization => <BroadcastWorkspace canManage={organization.canManage} />}
      </MailOrganizationBoundary>
    </div>
  );
}

type View = { kind: 'list' } | { kind: 'new' } | { kind: 'open'; broadcastId: number };

/** Members draft broadcasts; only owners and admins start, stop, pause, and resume them. */
function BroadcastWorkspace({ canManage }: { canManage: boolean }) {
  const t = useT('mailbox');
  const domains = useMailboxDomains(useMailOrganizationId());
  const [view, setView] = useState<View>({ kind: 'list' });
  const enabled = domains.data?.[0]?.broadcasts_enabled ?? true;
  const back = () => setView({ kind: 'list' });

  return (
    <div className="space-y-4">
      {!enabled ? (
        <Alert>
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertTitle>{t('broadcasts.disabledTitle')}</AlertTitle>
          <AlertDescription>{t('broadcasts.disabledDescription')}</AlertDescription>
        </Alert>
      ) : null}
      {view.kind === 'new' ? (
        <BroadcastEditor
          broadcast={null}
          enabled={enabled}
          canManage={canManage}
          onBack={back}
          onSaved={broadcastId => setView({ kind: 'open', broadcastId })}
        />
      ) : view.kind === 'open' ? (
        <OpenBroadcast
          broadcastId={view.broadcastId}
          enabled={enabled}
          canManage={canManage}
          onBack={back}
        />
      ) : (
        <BroadcastList
          enabled={enabled}
          onCreate={() => setView({ kind: 'new' })}
          onOpen={broadcastId => setView({ kind: 'open', broadcastId })}
        />
      )}
    </div>
  );
}

function StatusBadge({ broadcast }: { broadcast: Pick<MailBroadcast, 'status' | 'stop_reason'> }) {
  const t = useT('mailbox');
  const { status } = broadcast;
  // A test wave waiting out its observation window continues on its own; it is not a pause.
  const waiting = status === 'paused' && broadcast.stop_reason === 'test_wave_complete';
  return (
    <Badge
      variant={
        status === 'sending' || status === 'scheduled' || waiting
          ? 'default'
          : status === 'paused'
            ? 'outline'
            : 'secondary'
      }
      className="gap-1.5"
    >
      {status === 'sending' ? (
        <span className="size-1.5 rounded-full bg-current animate-pulse" aria-hidden="true" />
      ) : null}
      {t(waiting ? 'broadcasts.statusWaitingTestWave' : statusKey[status])}
    </Badge>
  );
}

function Progress({ broadcast }: { broadcast: MailBroadcast }) {
  const t = useT('mailbox');
  const { counts } = broadcast;
  if (counts.total === 0) return <span className="text-muted-foreground">-</span>;
  const done = counts.total - counts.pending;
  return (
    <div className="min-w-32 space-y-1">
      <div
        className="h-1.5 overflow-hidden rounded-full bg-muted"
        role="progressbar"
        aria-valuemin={0}
        aria-valuemax={counts.total}
        aria-valuenow={done}
        aria-label={t('broadcasts.progress')}
      >
        <div
          className="h-full bg-primary transition-all duration-500 ease-out"
          style={{ width: `${(done / counts.total) * 100}%` }}
        />
      </div>
      <span className="text-xs text-muted-foreground">
        {t('broadcasts.progressValue', { sent: counts.sent, total: counts.total })}
        {counts.failed > 0 ? (
          <span className="text-error">
            {' · '}
            {t('broadcasts.progressFailed', { failed: counts.failed })}
          </span>
        ) : null}
      </span>
    </div>
  );
}

function useWhen() {
  const t = useT('mailbox');
  const formatter = useDateTimeFormat();
  return (broadcast: MailBroadcast): string => {
    const time = (value: string) => formatter.format(new Date(value));
    if (broadcast.finished_at)
      return t('broadcasts.finishedAt', { time: time(broadcast.finished_at) });
    if (broadcast.status === 'scheduled' && broadcast.scheduled_at) {
      return t('broadcasts.scheduledFor', { time: time(broadcast.scheduled_at) });
    }
    if (broadcast.started_at)
      return t('broadcasts.startedAt', { time: time(broadcast.started_at) });
    return t('broadcasts.createdAt', { time: time(broadcast.created_at) });
  };
}

function BroadcastList({
  enabled,
  onCreate,
  onOpen,
}: {
  enabled: boolean;
  onCreate: () => void;
  onOpen: (broadcastId: number) => void;
}) {
  const t = useT('mailbox');
  const [page, setPage] = useState(1);
  const broadcasts = useMailBroadcasts(useMailOrganizationId(), page);
  const when = useWhen();
  const create = (
    <Button type="button" icon={<Plus className="size-4" />} disabled={!enabled} onClick={onCreate}>
      {t('broadcasts.new')}
    </Button>
  );

  if (broadcasts.isPending) return <Skeleton className="h-48 w-full" aria-hidden="true" />;
  if (broadcasts.error || !broadcasts.data) {
    if (isOrganizationLost(broadcasts.error)) return <OrganizationLostState />;
    return (
      <ErrorAlert
        message={t(resolveMailboxErrorKey(broadcasts.error))}
        retryLabel={t('actions.retry')}
        onRetry={() => void broadcasts.refetch()}
      />
    );
  }
  const { items, meta } = broadcasts.data;
  if (items.length === 0) {
    return (
      <EmptyState
        icon={<Megaphone className="size-5" />}
        title={t('broadcasts.emptyTitle')}
        description={t('broadcasts.emptyDescription')}
        action={create}
      />
    );
  }

  return (
    <div className="space-y-3">
      <div className="flex justify-end">{create}</div>
      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('broadcasts.name')}</TableHead>
              <TableHead>{t('broadcasts.status')}</TableHead>
              <TableHead>{t('broadcasts.progress')}</TableHead>
              <TableHead className="text-right">{t('broadcasts.when')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map(broadcast => (
              <TableRow key={broadcast.id}>
                <TableCell className="max-w-64 truncate font-medium">
                  <button
                    type="button"
                    className="max-w-full truncate text-left hover:underline focus-ring"
                    aria-label={t('broadcasts.open', { name: broadcast.name })}
                    onClick={() => onOpen(broadcast.id)}
                  >
                    {broadcast.name}
                  </button>
                </TableCell>
                <TableCell>
                  <StatusBadge broadcast={broadcast} />
                </TableCell>
                <TableCell>
                  <Progress broadcast={broadcast} />
                </TableCell>
                <TableCell className="text-right text-sm whitespace-nowrap text-muted-foreground">
                  {when(broadcast)}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      {meta.last_page > 1 ? (
        <div className="flex items-center justify-end gap-3">
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={meta.current_page <= 1}
            onClick={() => setPage(meta.current_page - 1)}
          >
            {t('list.previous')}
          </Button>
          <span className="text-sm text-muted-foreground">
            {t('list.page', { page: meta.current_page, total: meta.last_page })}
          </span>
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={meta.current_page >= meta.last_page}
            onClick={() => setPage(meta.current_page + 1)}
          >
            {t('list.next')}
          </Button>
        </div>
      ) : null}
    </div>
  );
}

function OpenBroadcast({
  broadcastId,
  enabled,
  canManage,
  onBack,
}: {
  broadcastId: number;
  enabled: boolean;
  canManage: boolean;
  onBack: () => void;
}) {
  const t = useT('mailbox');
  const broadcast = useMailBroadcast(useMailOrganizationId(), broadcastId);
  if (broadcast.isPending) return <Skeleton className="h-64 w-full" aria-hidden="true" />;
  if (broadcast.error || !broadcast.data) {
    return (
      <div className="space-y-3">
        <BackButton onBack={onBack} />
        <ErrorAlert
          message={t(resolveMailboxErrorKey(broadcast.error))}
          retryLabel={t('actions.retry')}
          onRetry={() => void broadcast.refetch()}
        />
      </div>
    );
  }
  if (broadcast.data.status === 'draft') {
    return (
      <BroadcastEditor
        key={broadcast.data.id}
        broadcast={broadcast.data}
        enabled={enabled}
        canManage={canManage}
        onBack={onBack}
        onSaved={() => undefined}
      />
    );
  }
  return <BroadcastDetail broadcast={broadcast.data} canManage={canManage} onBack={onBack} />;
}

function BackButton({ onBack }: { onBack: () => void }) {
  const t = useT('mailbox');
  return (
    <Button
      type="button"
      size="sm"
      variant="ghost"
      icon={<ArrowLeft className="size-4" />}
      onClick={onBack}
    >
      {t('broadcasts.back')}
    </Button>
  );
}

/** The API schedules a broadcast at most a year (365 days) ahead (contracts/MAILBOXES.md). */
const maxScheduleAheadMs = 365 * 24 * 60 * 60 * 1_000;

/** A `datetime-local` value for an instant, to the minute, in the viewer's time zone. */
function localDateTimeValue(timestamp: number): string {
  const at = new Date(timestamp);
  at.setSeconds(0, 0);
  const local = new Date(at.getTime() - at.getTimezoneOffset() * 60_000);
  return local.toISOString().slice(0, 16);
}

/** A `datetime-local` value for a time an hour from now, in the viewer's time zone. */
function defaultScheduleValue(): string {
  return localDateTimeValue(Date.now() + 60 * 60 * 1_000);
}

/** Why a `datetime-local` value cannot be scheduled, or null when it can. */
function scheduleProblem(value: string): ScheduleErrorKey | null {
  const timestamp = new Date(value).getTime();
  const now = Date.now();
  if (Number.isNaN(timestamp) || timestamp <= now) return 'broadcasts.schedulePast';
  if (timestamp - now > maxScheduleAheadMs) return 'broadcasts.scheduleTooFar';
  return null;
}

/** Viewer time zone name for scheduling copy, falling back safely to UTC. */
function viewerTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
  } catch {
    return 'UTC';
  }
}

type ScheduleErrorKey = 'broadcasts.schedulePast' | 'broadcasts.scheduleTooFar';
type EditorErrorKey = MailboxErrorKey | ScheduleErrorKey;

function BroadcastEditor({
  broadcast,
  enabled,
  canManage,
  onBack,
  onSaved,
}: {
  broadcast: MailBroadcast | null;
  enabled: boolean;
  canManage: boolean;
  onBack: () => void;
  onSaved: (broadcastId: number) => void;
}) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const mailboxes = useMailboxes(organizationId);
  const templates = useMailTemplates(organizationId, 'standard');
  const lists = useMailContactLists(organizationId);
  const save = useSaveMailBroadcast(organizationId);
  const send = useSendMailBroadcast(organizationId);
  const remove = useDeleteMailBroadcast(organizationId);
  const [name, setName] = useState(broadcast?.name ?? '');
  const [mailboxId, setMailboxId] = useState<number | null>(broadcast?.mailbox_id ?? null);
  const [templateId, setTemplateId] = useState<number | null>(broadcast?.template_id ?? null);
  const selectedTemplate = useMailTemplate(organizationId, templateId);
  const [listId, setListId] = useState<number | null>(broadcast?.list_id ?? null);
  const [label, setLabel] = useState(broadcast?.unsubscribe_label ?? '');
  const [variables, setVariables] = useState<Record<string, string>>(broadcast?.variables ?? {});
  const [testPercent, setTestPercent] = useState(broadcast?.test_percent ?? 0);
  const [testWait, setTestWait] = useState(
    broadcast?.test_wait_minutes || MAIL_BROADCAST_TEST_WAIT_DEFAULT_MINUTES
  );
  const [fromDomain, setFromDomain] = useState<string | null>(null);
  const [savedId, setSavedId] = useState<number | null>(broadcast?.id ?? null);
  const [dirty, setDirty] = useState(false);
  const [errorKey, setErrorKey] = useState<EditorErrorKey | null>(null);
  const [confirm, setConfirm] = useState<'send' | 'schedule' | 'delete' | 'discard' | null>(null);
  const [scheduleAt, setScheduleAt] = useState(defaultScheduleValue);
  const zone = useMemo(viewerTimeZone, []);
  const ids = {
    name: useId(),
    from: useId(),
    template: useId(),
    list: useId(),
    label: useId(),
    labelHint: useId(),
    at: useId(),
    atHint: useId(),
    domain: useId(),
    staged: useId(),
    percent: useId(),
    wait: useId(),
    stagedHint: useId(),
  };

  useEffect(() => {
    if (!dirty) return undefined;
    const handleBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', handleBeforeUnload);
    return () => window.removeEventListener('beforeunload', handleBeforeUnload);
  }, [dirty]);

  // Senders are picked by domain first: the shared domain and every domain the organization uses.
  // A broadcast needs `send` on its sender mailbox.
  const senderMailboxes = (mailboxes.data ?? []).filter(mailbox => mailbox.access.send);
  const senderDomains = [...new Set(senderMailboxes.map(mailbox => domainOf(mailbox.address)))];
  const currentDomain =
    fromDomain ??
    senderMailboxes.find(mailbox => mailbox.id === mailboxId)?.address.split('@')[1] ??
    senderDomains[0] ??
    null;
  const ready =
    senderMailboxes.length > 0 &&
    (templates.data?.length ?? 0) > 0 &&
    (lists.data?.length ?? 0) > 0;
  const loading = mailboxes.isPending || templates.isPending || lists.isPending;
  const pending = save.isPending || send.isPending || remove.isPending;
  const activeTemplate = templateId !== null ? (selectedTemplate.data ?? null) : null;

  const change =
    <T,>(setter: (value: T) => void) =>
    (value: T) => {
      setErrorKey(null);
      setDirty(true);
      setter(value);
    };

  const requestBack = () => {
    if (dirty) {
      setConfirm('discard');
      return;
    }
    onBack();
  };

  // Only values for variables the chosen template uses, and only filled ones, are saved.
  const templateNames = new Set(
    activeTemplate?.variables ??
      templates.data?.find(template => template.id === templateId)?.variables ??
      []
  );
  const sharedValues = Object.fromEntries(
    Object.entries(variables).filter(
      ([variableName, value]) => templateNames.has(variableName) && value !== ''
    )
  );

  const hasValidDraftFields = (): boolean =>
    name.trim() !== '' && mailboxId !== null && templateId !== null && listId !== null;

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!hasValidDraftFields() || mailboxId === null || templateId === null || listId === null) {
      setErrorKey('errors.broadcastInvalid');
      return;
    }
    try {
      const stored = await save.mutateAsync({
        input: {
          name,
          mailbox_id: mailboxId,
          template_id: templateId,
          list_id: listId,
          unsubscribe_label: label,
          test_percent: testPercent,
          test_wait_minutes: testWait,
          variables: sharedValues,
        },
        ...(savedId === null ? {} : { broadcastId: savedId }),
      });
      toast.success(t(savedId === null ? 'broadcasts.created' : 'broadcasts.saved'));
      setSavedId(stored.id);
      setDirty(false);
      onSaved(stored.id);
    } catch (failure) {
      setErrorKey(resolveMailboxErrorKey(failure));
    }
  };

  const requestSend = () => {
    setErrorKey(null);
    if (!hasValidDraftFields()) {
      setErrorKey('errors.broadcastInvalid');
      return;
    }
    setConfirm('send');
  };

  const requestSchedule = () => {
    setErrorKey(null);
    if (!hasValidDraftFields()) {
      setErrorKey('errors.broadcastInvalid');
      return;
    }
    const problem = scheduleProblem(scheduleAt);
    if (problem) {
      setErrorKey(problem);
      return;
    }
    setConfirm('schedule');
  };

  const start = async (scheduled: boolean) => {
    if (scheduled) {
      const problem = scheduleProblem(scheduleAt);
      if (problem) {
        setConfirm(null);
        setErrorKey(problem);
        return;
      }
    }
    let sending = false;
    try {
      let targetId = savedId;
      if (targetId === null || dirty) {
        if (
          !hasValidDraftFields() ||
          mailboxId === null ||
          templateId === null ||
          listId === null
        ) {
          setConfirm(null);
          setErrorKey('errors.broadcastInvalid');
          return;
        }
        const stored = await save.mutateAsync({
          input: {
            name,
            mailbox_id: mailboxId,
            template_id: templateId,
            list_id: listId,
            unsubscribe_label: label,
            test_percent: testPercent,
            test_wait_minutes: testWait,
            variables: sharedValues,
          },
          ...(targetId === null ? {} : { broadcastId: targetId }),
        });
        targetId = stored.id;
        setSavedId(stored.id);
        setDirty(false);
      }
      sending = true;
      await send.mutateAsync({
        broadcastId: targetId,
        input: scheduled ? { scheduled_at: new Date(scheduleAt).toISOString() } : {},
      });
      toast.success(t(scheduled ? 'broadcasts.scheduled' : 'broadcasts.started'));
      setConfirm(null);
      onSaved(targetId);
    } catch (failure) {
      setConfirm(null);
      const key = resolveMailboxErrorKey(failure);
      // A scheduled send's only own field is scheduled_at, so BROADCAST_INVALID there names the
      // date (for example when the viewer's clock is behind), not the draft.
      setErrorKey(
        scheduled && sending && key === 'errors.broadcastInvalid'
          ? 'broadcasts.scheduleTooFar'
          : key
      );
    }
  };

  const confirmDelete = async () => {
    if (savedId === null) return;
    try {
      await remove.mutateAsync(savedId);
      toast.success(t('broadcasts.deleted'));
      setConfirm(null);
      onBack();
    } catch (failure) {
      setConfirm(null);
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
  };

  const select = (
    id: string,
    labelText: string,
    value: number | null,
    onChange: (value: number) => void,
    options: { id: number; label: string }[]
  ) => (
    <div className="space-y-2">
      <Label htmlFor={id}>{labelText}</Label>
      <select
        id={id}
        className={cn(nativeSelectClassName, 'w-full')}
        value={value === null ? '' : String(value)}
        disabled={pending}
        onChange={event => onChange(Number(event.target.value))}
      >
        <option value="" disabled>
          {t('broadcasts.choose')}
        </option>
        {options.map(option => (
          <option key={option.id} value={String(option.id)}>
            {option.label}
          </option>
        ))}
      </select>
    </div>
  );

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <BackButton onBack={requestBack} />
        {savedId !== null ? (
          <Button
            type="button"
            size="sm"
            variant="outline"
            icon={<Trash2 className="size-4" />}
            disabled={pending}
            onClick={() => setConfirm('delete')}
          >
            {t('broadcasts.delete')}
          </Button>
        ) : null}
      </div>
      {!loading && !ready ? (
        <Alert>
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertDescription>{t('broadcasts.needsSetup')}</AlertDescription>
        </Alert>
      ) : null}
      <div className={cn('grid gap-6', activeTemplate !== null && 'lg:grid-cols-2')}>
        <form onSubmit={submit} className="min-w-0 max-w-2xl space-y-4">
          <div className="space-y-2">
            <Label htmlFor={ids.name}>{t('broadcasts.name')}</Label>
            <Input
              id={ids.name}
              value={name}
              maxLength={MAIL_BROADCAST_NAME_MAX_LENGTH}
              placeholder={t('broadcasts.namePlaceholder')}
              disabled={pending}
              onChange={event => change(setName)(event.target.value)}
            />
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label htmlFor={ids.domain}>{t('broadcasts.fromDomain')}</Label>
              <select
                id={ids.domain}
                className={cn(nativeSelectClassName, 'w-full')}
                value={currentDomain ?? ''}
                disabled={pending || senderDomains.length === 0}
                onChange={event => {
                  const next = event.target.value;
                  change(setFromDomain)(next);
                  // Keep the sender only when it is on the chosen domain.
                  const current = senderMailboxes.find(mailbox => mailbox.id === mailboxId);
                  if (!current || domainOf(current.address) !== next) setMailboxId(null);
                }}
              >
                {senderDomains.map(domain => (
                  <option key={domain} value={domain}>
                    {domain}
                  </option>
                ))}
              </select>
            </div>
            {select(
              ids.from,
              t('broadcasts.from'),
              mailboxId,
              change(setMailboxId),
              senderMailboxes
                .filter(
                  mailbox => currentDomain === null || domainOf(mailbox.address) === currentDomain
                )
                .map(mailbox => ({ id: mailbox.id, label: mailbox.address }))
            )}
          </div>
          {select(
            ids.template,
            t('broadcasts.template'),
            templateId,
            change(setTemplateId),
            (templates.data ?? []).map(template => ({ id: template.id, label: template.name }))
          )}
          {select(
            ids.list,
            t('broadcasts.list'),
            listId,
            change(setListId),
            (lists.data ?? []).map(list => ({
              id: list.id,
              label: t('broadcasts.listOption', { name: list.name, count: list.contact_count }),
            }))
          )}
          {activeTemplate === null ? null : (
            <SharedValues
              template={activeTemplate}
              values={variables}
              disabled={pending}
              onChange={(variableName, value) =>
                change(setVariables)({ ...variables, [variableName]: value })
              }
            />
          )}
          <div className="space-y-2">
            <Label htmlFor={ids.label}>{t('broadcasts.label')}</Label>
            <Input
              id={ids.label}
              value={label}
              maxLength={MAIL_BROADCAST_LABEL_MAX_LENGTH}
              placeholder={t('broadcasts.labelPlaceholder')}
              aria-describedby={ids.labelHint}
              disabled={pending}
              onChange={event => change(setLabel)(event.target.value)}
            />
            <p id={ids.labelHint} className="text-xs text-muted-foreground">
              {t('broadcasts.labelHint')}
            </p>
          </div>
          <fieldset className="space-y-3 rounded-md border p-3">
            <div className="flex items-center gap-2">
              <input
                id={ids.staged}
                type="checkbox"
                className="size-4"
                checked={testPercent > 0}
                disabled={pending}
                aria-describedby={ids.stagedHint}
                onChange={event => change(setTestPercent)(event.target.checked ? 10 : 0)}
              />
              <Label htmlFor={ids.staged}>{t('broadcasts.staged')}</Label>
            </div>
            <p id={ids.stagedHint} className="text-xs text-muted-foreground">
              {t('broadcasts.stagedHint')}
            </p>
            {testPercent > 0 ? (
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="space-y-2">
                  <Label htmlFor={ids.percent}>{t('broadcasts.testPercent')}</Label>
                  <Input
                    id={ids.percent}
                    type="number"
                    min={1}
                    max={MAIL_BROADCAST_TEST_MAX_PERCENT}
                    value={testPercent}
                    disabled={pending}
                    onChange={event =>
                      change(setTestPercent)(
                        Math.min(
                          MAIL_BROADCAST_TEST_MAX_PERCENT,
                          Math.max(1, Number(event.target.value) || 1)
                        )
                      )
                    }
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor={ids.wait}>{t('broadcasts.testWait')}</Label>
                  <Input
                    id={ids.wait}
                    type="number"
                    min={MAIL_BROADCAST_TEST_WAIT_MIN_MINUTES}
                    max={MAIL_BROADCAST_TEST_WAIT_MAX_MINUTES}
                    value={testWait}
                    disabled={pending}
                    onChange={event =>
                      change(setTestWait)(
                        Math.min(
                          MAIL_BROADCAST_TEST_WAIT_MAX_MINUTES,
                          Math.max(
                            MAIL_BROADCAST_TEST_WAIT_MIN_MINUTES,
                            Number(event.target.value) || 0
                          )
                        )
                      )
                    }
                  />
                </div>
              </div>
            ) : null}
          </fieldset>
          <p className="text-sm text-muted-foreground">{t('broadcasts.safetyHint')}</p>
          {errorKey ? (
            <Alert variant="destructive">
              <AlertCircle className="size-4" aria-hidden="true" />
              <AlertDescription>{t(errorKey)}</AlertDescription>
            </Alert>
          ) : null}
          <div className="flex flex-wrap items-end gap-3">
            <Button
              type="submit"
              variant={savedId === null ? 'default' : 'outline'}
              loading={save.isPending && confirm === null}
              disabled={(!dirty && savedId !== null) || !ready || pending}
              icon={<Save className="size-4" />}
            >
              {t('broadcasts.save')}
            </Button>
            {canManage ? (
              <>
                <Button
                  type="button"
                  loading={(save.isPending || send.isPending) && confirm === 'send'}
                  disabled={!ready || !enabled || pending}
                  icon={<Send className="size-4" />}
                  onClick={requestSend}
                >
                  {t('broadcasts.sendNow')}
                </Button>
                <div className="flex flex-wrap items-end gap-2">
                  <div className="space-y-2">
                    <div className="flex items-baseline justify-between gap-2">
                      <Label htmlFor={ids.at}>{t('broadcasts.scheduleAt')}</Label>
                      <span id={ids.atHint} className="text-xs text-muted-foreground">
                        {t('broadcasts.timezoneHint', { zone })}
                      </span>
                    </div>
                    <Input
                      id={ids.at}
                      type="datetime-local"
                      value={scheduleAt}
                      max={localDateTimeValue(Date.now() + maxScheduleAheadMs)}
                      aria-describedby={ids.atHint}
                      disabled={pending}
                      onChange={event => {
                        setErrorKey(null);
                        setScheduleAt(event.target.value);
                      }}
                    />
                  </div>
                  <Button
                    type="button"
                    variant="outline"
                    loading={(save.isPending || send.isPending) && confirm === 'schedule'}
                    disabled={!ready || !enabled || pending || scheduleAt === ''}
                    icon={<Clock className="size-4" />}
                    onClick={requestSchedule}
                  >
                    {t('broadcasts.schedule')}
                  </Button>
                </div>
              </>
            ) : null}
          </div>
          {!canManage ? (
            <p className="text-sm text-muted-foreground">{t('broadcasts.managersSend')}</p>
          ) : null}
        </form>
        {activeTemplate !== null ? (
          <BroadcastPreview key={activeTemplate.id} template={activeTemplate} values={variables} />
        ) : null}
      </div>
      {confirm === 'send' || confirm === 'schedule' ? (
        <ConfirmDialog
          title={t(confirm === 'send' ? 'broadcasts.sendTitle' : 'broadcasts.scheduleTitle')}
          description={t(
            confirm === 'send' ? 'broadcasts.sendDescription' : 'broadcasts.scheduleDescription'
          )}
          confirmLabel={t(confirm === 'send' ? 'broadcasts.sendNow' : 'broadcasts.schedule')}
          pending={save.isPending || send.isPending}
          onCancel={() => setConfirm(null)}
          onConfirm={() => void start(confirm === 'schedule')}
        />
      ) : null}
      {confirm === 'delete' ? (
        <ConfirmDialog
          title={t('broadcasts.deleteTitle')}
          description={t('broadcasts.deleteDescription', { name: broadcast?.name ?? name })}
          confirmLabel={t('actions.delete')}
          destructive
          pending={remove.isPending}
          onCancel={() => setConfirm(null)}
          onConfirm={() => void confirmDelete()}
        />
      ) : null}
      {confirm === 'discard' ? (
        <ConfirmDialog
          title={t('broadcasts.unsavedTitle')}
          description={t('broadcasts.unsavedDescription')}
          confirmLabel={t('broadcasts.discard')}
          cancelLabel={t('broadcasts.keep')}
          destructive
          pending={false}
          onCancel={() => setConfirm(null)}
          onConfirm={() => {
            setConfirm(null);
            onBack();
          }}
        />
      ) : null}
    </div>
  );
}

/** Live preview of the selected template with sample contact values and shared broadcast values. */
function BroadcastPreview({
  template,
  values,
}: {
  template: MailTemplate;
  values: Record<string, string>;
}) {
  const t = useT('mailbox');
  const [chosenTab, setChosenTab] = useState<'html' | 'text' | null>(null);
  const rendered = useMemo(() => {
    const merged: Record<string, string> = {
      ...template.sample_variables,
      unsubscribe_url: template.sample_variables.unsubscribe_url || '#unsubscribe',
      ...Object.fromEntries(Object.entries(values).filter(([, value]) => value !== '')),
    };
    return renderTemplate(
      { subject: template.subject, html: template.html_body, text: template.text_body },
      merged
    );
  }, [template, values]);

  if (!rendered) return null;
  const hasHtml = template.html_body !== '';
  const tab = chosenTab ?? (hasHtml ? 'html' : 'text');

  return (
    <section
      aria-label={t('broadcasts.previewTitle')}
      className="min-w-0 space-y-3 lg:sticky lg:top-4 lg:self-start"
    >
      <div className="space-y-1">
        <h3 className="text-sm font-medium">{t('broadcasts.previewTitle')}</h3>
        <p className="text-xs text-muted-foreground">{t('broadcasts.previewHint')}</p>
      </div>
      <p className="rounded-md border px-3 py-2 text-sm font-medium break-words">
        {rendered.subject || '\u00A0'}
      </p>
      <Tabs value={tab} onValueChange={value => setChosenTab(value as 'html' | 'text')}>
        <TabsList>
          <TabsTrigger value="html">{t('templates.previewHtml')}</TabsTrigger>
          <TabsTrigger value="text">{t('templates.previewText')}</TabsTrigger>
        </TabsList>
        <TabsContent value="html" className="pt-2">
          <MailHtmlFrame html={rendered.html} title={t('broadcasts.previewTitle')} />
        </TabsContent>
        <TabsContent value="text" className="pt-2">
          {rendered.text ? (
            <pre className="max-h-96 overflow-auto rounded-md border p-3 font-sans text-sm break-words whitespace-pre-wrap">
              {rendered.text}
            </pre>
          ) : (
            <p className="text-sm text-muted-foreground">{t('templates.noText')}</p>
          )}
        </TabsContent>
      </Tabs>
    </section>
  );
}

const contactVariables: readonly string[] = MAIL_BROADCAST_CONTACT_VARIABLES;

/** The template's variables: contact fields are filled per message, the rest can be shared. */
function SharedValues({
  template,
  values,
  disabled,
  onChange,
}: {
  template: MailTemplate;
  values: Record<string, string>;
  disabled: boolean;
  onChange: (name: string, value: string) => void;
}) {
  const t = useT('mailbox');
  const baseId = useId();
  const fields =
    templateFallbacks({
      subject: template.subject,
      html: template.html_body,
      text: template.text_body,
    }) ?? [];
  const fromContact = fields.filter(field => contactVariables.includes(field.name));
  const shared = fields.filter(field => !contactVariables.includes(field.name));
  return (
    <fieldset className="space-y-3 rounded-md border p-3">
      <legend className="px-1 text-sm font-medium">{t('broadcasts.values.title')}</legend>
      <p className="text-xs text-muted-foreground">
        {fromContact.length > 0
          ? t('broadcasts.values.fromContact', {
              names: fromContact.map(field => field.name).join(', '),
            })
          : t('broadcasts.values.fromContactNone')}
      </p>
      {shared.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t('broadcasts.values.none')}</p>
      ) : (
        <>
          <p className="text-xs text-muted-foreground">{t('broadcasts.values.hint')}</p>
          {shared.map(field => {
            const id = `${baseId}-${field.name}`;
            const hintId = `${id}-hint`;
            const value = values[field.name] ?? '';
            const atRisk = value === '' && field.fallback === null;
            return (
              <div key={field.name} className="space-y-1">
                <Label htmlFor={id} className="font-mono text-xs">
                  {field.name}
                </Label>
                <Input
                  id={id}
                  value={value}
                  maxLength={MAIL_TEMPLATE_VALUE_MAX_LENGTH}
                  placeholder={
                    field.fallback === null
                      ? t('broadcasts.values.placeholder')
                      : t('broadcasts.values.fallback', { value: field.fallback })
                  }
                  aria-describedby={atRisk ? hintId : undefined}
                  disabled={disabled}
                  onChange={event => onChange(field.name, event.target.value)}
                />
                {atRisk ? (
                  <p id={hintId} className="text-xs text-amber-700 dark:text-amber-400">
                    {t('broadcasts.values.required', { name: field.name })}
                  </p>
                ) : null}
              </div>
            );
          })}
        </>
      )}
    </fieldset>
  );
}

function BroadcastDetail({
  broadcast,
  canManage,
  onBack,
}: {
  broadcast: MailBroadcast;
  canManage: boolean;
  onBack: () => void;
}) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const cancel = useCancelMailBroadcast(organizationId);
  const pause = usePauseMailBroadcast(organizationId);
  const resume = useResumeMailBroadcast(organizationId);
  const remove = useDeleteMailBroadcast(organizationId);
  const when = useWhen();
  const formatter = useDateTimeFormat();
  const [confirm, setConfirm] = useState<'cancel' | 'delete' | 'resume' | null>(null);
  const paused = broadcast.status === 'paused';
  const running = broadcast.status === 'sending' || broadcast.status === 'scheduled' || paused;
  const waiting = paused && broadcast.stop_reason === 'test_wave_complete';
  const healthStop =
    paused &&
    (broadcast.stop_reason === 'bounce_rate_high' ||
      broadcast.stop_reason === 'complaint_rate_high');
  const { counts } = broadcast;

  const doPause = async () => {
    try {
      await pause.mutateAsync(broadcast.id);
      toast.success(t('broadcasts.pausedToast'));
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
  };

  const doResume = async () => {
    try {
      await resume.mutateAsync(broadcast.id);
      toast.success(t('broadcasts.resumedToast'));
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
    setConfirm(null);
  };

  const doCancel = async () => {
    try {
      await cancel.mutateAsync(broadcast.id);
      toast.success(t('broadcasts.canceled'));
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
    setConfirm(null);
  };

  const doDelete = async () => {
    try {
      await remove.mutateAsync(broadcast.id);
      toast.success(t('broadcasts.deleted'));
      setConfirm(null);
      onBack();
    } catch (failure) {
      setConfirm(null);
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
  };

  const attempted = counts.sent + counts.failed;
  const percentage = (value: number, total: number) =>
    total > 0
      ? new Intl.NumberFormat(undefined, { style: 'percent', maximumFractionDigits: 2 }).format(
          value / total
        )
      : '—';
  const stats: [string, number | string][] = [
    [t('broadcasts.queueProgress'), percentage(counts.total - counts.pending, counts.total)],
    [t('broadcasts.sendSuccessRate'), percentage(counts.sent, attempted)],
    [t('broadcasts.total'), counts.total],
    [t('broadcasts.sent'), counts.sent],
    [t('broadcasts.failed'), counts.failed],
    [t('broadcasts.skipped'), counts.skipped],
    [t('broadcasts.pending'), counts.pending],
    [t('broadcasts.canceledCount'), counts.canceled],
    [t('broadcasts.bounced'), counts.bounced],
    [t('broadcasts.complained'), counts.complained],
  ];

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <BackButton onBack={onBack} />
        <div className="flex flex-wrap gap-2">
          {running && canManage && (broadcast.status === 'sending' || waiting) ? (
            <Button
              type="button"
              size="sm"
              variant="outline"
              loading={pause.isPending}
              icon={<Pause className="size-4" />}
              onClick={() => void doPause()}
            >
              {t('broadcasts.pause')}
            </Button>
          ) : null}
          {paused && canManage ? (
            <Button
              type="button"
              size="sm"
              loading={resume.isPending}
              icon={<Play className="size-4" />}
              onClick={() => (healthStop ? setConfirm('resume') : void doResume())}
            >
              {t(waiting ? 'broadcasts.resumeNow' : 'broadcasts.resume')}
            </Button>
          ) : null}
          {running ? (
            canManage ? (
              <Button
                type="button"
                size="sm"
                variant="outline"
                icon={<XCircle className="size-4" />}
                onClick={() => setConfirm('cancel')}
              >
                {t('broadcasts.cancel')}
              </Button>
            ) : null
          ) : (
            <Button
              type="button"
              size="sm"
              variant="outline"
              icon={<Trash2 className="size-4" />}
              onClick={() => setConfirm('delete')}
            >
              {t('broadcasts.delete')}
            </Button>
          )}
        </div>
      </div>
      <div className="space-y-1">
        <div className="flex flex-wrap items-center gap-3">
          <h2 className="text-lg font-semibold break-words">{broadcast.name}</h2>
          <StatusBadge broadcast={broadcast} />
        </div>
        <p className="text-sm text-muted-foreground">{when(broadcast)}</p>
      </div>
      {broadcast.stop_reason === 'send_limit_reached' && running ? (
        <Alert>
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertDescription>{t('broadcasts.limitPaused')}</AlertDescription>
        </Alert>
      ) : null}
      {paused && knownPause(broadcast.stop_reason) ? (
        <Alert variant={healthStop ? 'destructive' : 'default'} data-testid="broadcast-paused">
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertDescription>
            {broadcast.stop_reason === 'test_wave_complete'
              ? t('broadcasts.pausedTestWave', {
                  time: broadcast.resume_at ? formatter.format(new Date(broadcast.resume_at)) : '',
                })
              : broadcast.stop_reason === 'bounce_rate_high'
                ? t('broadcasts.pausedBounces', { bounced: counts.bounced })
                : broadcast.stop_reason === 'complaint_rate_high'
                  ? t('broadcasts.pausedComplaints', { complained: counts.complained })
                  : t('broadcasts.pausedByOwner')}
          </AlertDescription>
        </Alert>
      ) : null}
      {broadcast.test_percent > 0 ? (
        <p className="text-sm text-muted-foreground">
          {t('broadcasts.stagedSummary', {
            percent: broadcast.test_percent,
            minutes: broadcast.test_wait_minutes,
          })}{' '}
          {broadcast.status === 'sending'
            ? t(broadcast.current_wave === 1 ? 'broadcasts.waveTest' : 'broadcasts.waveRest')
            : null}
        </p>
      ) : null}
      {broadcast.safety_override ? (
        <p className="text-sm text-warning">{t('broadcasts.overrideNote')}</p>
      ) : null}
      <dl className="grid grid-cols-2 gap-3 sm:grid-cols-4 lg:grid-cols-5">
        {stats.map(([label, value]) => (
          <div key={label} className="rounded-md border px-3 py-2">
            <dt className="text-xs text-muted-foreground">{label}</dt>
            <dd className="text-xl font-semibold tabular-nums">{value}</dd>
          </div>
        ))}
      </dl>
      <Progress broadcast={broadcast} />
      <RecipientTable broadcastId={broadcast.id} live={running} />
      {confirm === 'cancel' ? (
        <ConfirmDialog
          title={t('broadcasts.cancelTitle')}
          description={t('broadcasts.cancelDescription')}
          confirmLabel={t('broadcasts.cancel')}
          cancelLabel={t('broadcasts.keep')}
          destructive
          pending={cancel.isPending}
          onCancel={() => setConfirm(null)}
          onConfirm={() => void doCancel()}
        />
      ) : null}
      {confirm === 'resume' ? (
        <ConfirmDialog
          title={t('broadcasts.resumeTitle')}
          description={t('broadcasts.resumeDescription')}
          confirmLabel={t('broadcasts.resume')}
          destructive
          pending={resume.isPending}
          onCancel={() => setConfirm(null)}
          onConfirm={() => void doResume()}
        />
      ) : null}
      {confirm === 'delete' ? (
        <ConfirmDialog
          title={t('broadcasts.deleteTitle')}
          description={t('broadcasts.deleteDescription', { name: broadcast.name })}
          confirmLabel={t('actions.delete')}
          destructive
          pending={remove.isPending}
          onCancel={() => setConfirm(null)}
          onConfirm={() => void doDelete()}
        />
      ) : null}
    </div>
  );
}

function RecipientTable({ broadcastId, live }: { broadcastId: number; live: boolean }) {
  const t = useT('mailbox');
  const [status, setStatus] = useState<MailRecipientStatus | undefined>(undefined);
  const [page, setPage] = useState(1);
  const recipients = useMailBroadcastRecipients(
    useMailOrganizationId(),
    broadcastId,
    status,
    page,
    live
  );
  const filterId = useId();

  return (
    <section className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h3 className="font-medium">{t('broadcasts.recipients')}</h3>
        <div>
          <Label htmlFor={filterId} className="sr-only">
            {t('broadcasts.recipientFilter')}
          </Label>
          <select
            id={filterId}
            className={nativeSelectClassName}
            value={status ?? ''}
            onChange={event => {
              setStatus(
                event.target.value === '' ? undefined : (event.target.value as MailRecipientStatus)
              );
              setPage(1);
            }}
          >
            <option value="">{t('broadcasts.allRecipients')}</option>
            {recipientStatuses.map(value => (
              <option key={value} value={value}>
                {t(recipientKey[value])}
              </option>
            ))}
          </select>
        </div>
      </div>
      {recipients.isPending ? (
        <Skeleton className="h-32 w-full" aria-hidden="true" />
      ) : recipients.error || !recipients.data ? (
        <ErrorAlert
          message={t(resolveMailboxErrorKey(recipients.error))}
          retryLabel={t('actions.retry')}
          onRetry={() => void recipients.refetch()}
        />
      ) : recipients.data.items.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t('broadcasts.noRecipients')}</p>
      ) : (
        <>
          <div className="rounded-md border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('broadcasts.email')}</TableHead>
                  <TableHead>{t('broadcasts.status')}</TableHead>
                  <TableHead>{t('broadcasts.reason')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {recipients.data.items.map(recipient => {
                  const code = recipient.failure_code;
                  return (
                    <TableRow key={recipient.id}>
                      <TableCell className="max-w-72 truncate">{recipient.email}</TableCell>
                      <TableCell>
                        <Badge variant={recipient.status === 'sent' ? 'secondary' : 'outline'}>
                          {t(recipientKey[recipient.status])}
                        </Badge>
                      </TableCell>
                      <TableCell className="text-sm text-muted-foreground">
                        {knownReason(code) ? (
                          t(reasonKey[code])
                        ) : code ? (
                          <code className="text-xs">{code}</code>
                        ) : null}
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </div>
          {recipients.data.meta.last_page > 1 ? (
            <div className="flex items-center justify-end gap-3">
              <Button
                type="button"
                size="sm"
                variant="outline"
                disabled={recipients.data.meta.current_page <= 1}
                onClick={() => setPage(recipients.data.meta.current_page - 1)}
              >
                {t('list.previous')}
              </Button>
              <span className="text-sm text-muted-foreground">
                {t('list.page', {
                  page: recipients.data.meta.current_page,
                  total: recipients.data.meta.last_page,
                })}
              </span>
              <Button
                type="button"
                size="sm"
                variant="outline"
                disabled={recipients.data.meta.current_page >= recipients.data.meta.last_page}
                onClick={() => setPage(recipients.data.meta.current_page + 1)}
              >
                {t('list.next')}
              </Button>
            </div>
          ) : null}
        </>
      )}
    </section>
  );
}

function ConfirmDialog({
  title,
  description,
  confirmLabel,
  cancelLabel,
  destructive = false,
  pending,
  onCancel,
  onConfirm,
}: {
  title: string;
  description: string;
  confirmLabel: string;
  cancelLabel?: string;
  destructive?: boolean;
  pending: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const t = useT('mailbox');
  return (
    <Dialog open onOpenChange={open => !open && !pending && onCancel()}>
      <DialogContent size="sm">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <DialogBody />
        <DialogFooter>
          <Button type="button" variant="outline" disabled={pending} onClick={onCancel}>
            {cancelLabel ?? t('actions.cancel')}
          </Button>
          <Button
            type="button"
            variant={destructive ? 'destructive' : 'default'}
            loading={pending}
            onClick={onConfirm}
          >
            {confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
