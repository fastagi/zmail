'use client';

import { lazy, Suspense, useEffect, useId, useRef, useState } from 'react';
import { AlertCircle, Bot, History, PenLine, Plus, ShieldOff } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import {
  MAIL_AGENT_GRANT_MAX_PER_MAILBOX,
  type MailAgentDraft,
  type MailAgentGrant,
  type MailAgentGrantStatus,
} from '@/features/mailbox/agent-grant-schemas';
import { quotedReplyDraft, type ComposeDraft } from '@/features/mailbox/components/compose-drafts';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { useDateTimeFormat } from '@/features/mailbox/components/mail-shared';
import {
  useAgentDrafts,
  useAgentGrantActivity,
  useAgentGrants,
  useRevokeAgentGrant,
} from '@/features/mailbox/hooks/use-agent-grants';
import { useMailMessage } from '@/features/mailbox/hooks/use-mailbox';
import type { Mailbox, MailMessage } from '@/features/mailbox/types';
import { resolveAgentGrantErrorKey } from '@/features/mailbox/utils/agent-grant-error';
import { canComposeFrom } from '@/features/mailbox/utils/connected-mailbox';
import { useT } from '@/i18n';

// The form and the composer load when they first open.
const AgentGrantDialog = lazy(async () => ({
  default: (await import('@/features/mailbox/components/agent-grant-dialog')).AgentGrantDialog,
}));
const ComposeDialog = lazy(async () => ({
  default: (await import('@/features/mailbox/components/compose-dialog')).ComposeDialog,
}));

const statusVariant: Record<MailAgentGrantStatus, 'default' | 'secondary' | 'outline'> = {
  active: 'default',
  expired: 'secondary',
  revoked: 'outline',
};

/**
 * The Agent tab of a mailbox's access panel, shown to who may manage its agent grants: the grants
 * with their capabilities, filters, expiry, and status; revoking one after an inline confirmation;
 * a grant's recent activity; "Authorize an agent"; and, for holders of `send`, the drafts agents
 * wrote, each opened in the composer for a person to edit and send. Nothing here sends mail.
 */
export function MailboxAgentsTab({ mailbox }: { mailbox: Mailbox }) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const grants = useAgentGrants(organizationId, mailbox.id, true);
  const [authorizing, setAuthorizing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const titleId = useId();

  const items = grants.data?.items ?? [];
  const active = items.filter(grant => grant.status === 'active').length;
  const atLimit = active >= MAIL_AGENT_GRANT_MAX_PER_MAILBOX;

  return (
    <div className="space-y-6">
      <section className="space-y-2 text-sm">
        <p className="text-muted-foreground">{t('agents.intro')}</p>
        <p className="flex items-start gap-2 rounded-md bg-muted p-3 text-xs">
          <ShieldOff className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
          <span>{t('agents.neverSends')}</span>
        </p>
      </section>

      {error ? (
        <Alert variant="destructive" role="alert">
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      ) : null}

      <section className="space-y-3" aria-labelledby={titleId}>
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="min-w-0">
            <h3 id={titleId} className="text-sm font-semibold">
              {t('agents.grants.title')}
            </h3>
            {grants.data ? (
              <p className="text-xs text-muted-foreground">
                {t('agents.grants.count', { count: active, max: MAIL_AGENT_GRANT_MAX_PER_MAILBOX })}
              </p>
            ) : null}
          </div>
          <Button
            type="button"
            size="sm"
            icon={<Plus className="size-3.5" />}
            disabled={!grants.data || atLimit}
            aria-describedby={atLimit ? `${titleId}-limit` : undefined}
            onClick={() => setAuthorizing(true)}
          >
            {t('agents.form.open')}
          </Button>
        </div>
        {atLimit ? (
          <p id={`${titleId}-limit`} className="text-xs text-warning">
            {t('agents.form.limitReached', { max: MAIL_AGENT_GRANT_MAX_PER_MAILBOX })}
          </p>
        ) : null}
        {grants.isPending ? (
          <Skeleton className="h-20 w-full" />
        ) : grants.error ? (
          <p role="alert" className="text-sm text-error">
            {t(
              resolveAgentGrantErrorKey(grants.error) === 'agents.errors.permissionDenied'
                ? 'agents.errors.permissionDenied'
                : 'agents.grants.loadFailed'
            )}
          </p>
        ) : items.length === 0 ? (
          <p className="rounded-md border border-dashed p-4 text-center text-sm text-muted-foreground">
            {t('agents.grants.empty')}
          </p>
        ) : (
          <ul aria-labelledby={titleId} className="divide-y rounded-md border">
            {items.map(grant => (
              <GrantRow key={grant.id} mailboxId={mailbox.id} grant={grant} onError={setError} />
            ))}
          </ul>
        )}
      </section>

      {mailbox.access.send ? <DraftsSection mailbox={mailbox} grants={items} /> : null}

      {authorizing ? (
        <Suspense fallback={null}>
          <AgentGrantDialog
            mailbox={mailbox}
            grants={items}
            open={authorizing}
            onOpenChange={setAuthorizing}
          />
        </Suspense>
      ) : null}
    </div>
  );
}

function GrantRow({
  mailboxId,
  grant,
  onError,
}: {
  mailboxId: number;
  grant: MailAgentGrant;
  onError: (message: string | null) => void;
}) {
  const t = useT('mailbox');
  const formatter = useDateTimeFormat();
  const organizationId = useMailOrganizationId();
  const revoke = useRevokeAgentGrant(organizationId, mailboxId);
  const [confirming, setConfirming] = useState(false);
  const [showActivity, setShowActivity] = useState(false);
  const revokeRef = useRef<HTMLButtonElement>(null);
  const cancelRef = useRef<HTMLButtonElement>(null);
  const activityRef = useRef<HTMLButtonElement>(null);
  const activityId = useId();
  const confirmId = useId();
  const name = grant.api_key_name;
  const { senders, label_ids: labelIds } = grant.filters;
  const date = (iso: string) => formatter.format(new Date(iso));

  // The confirmation takes the focus and cancelling gives it back to Revoke. Once the grant is
  // revoked, Revoke is gone, so the row's Activity button takes it when the list shows the end.
  const { status } = grant;
  const wasConfirming = useRef(false);
  const revoked = useRef(false);
  useEffect(() => {
    if (confirming) cancelRef.current?.focus();
    else if (wasConfirming.current && !revoked.current) revokeRef.current?.focus();
    else if (revoked.current && status !== 'active') {
      // The list already shows the end: nothing further will change the status.
      revoked.current = false;
      activityRef.current?.focus();
    }
    wasConfirming.current = confirming;
    // Only a change of `confirming` moves the focus here; the status has its own effect.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [confirming]);
  useEffect(() => {
    if (!revoked.current || status === 'active') return;
    revoked.current = false;
    activityRef.current?.focus();
  }, [status]);

  const confirm = async () => {
    onError(null);
    try {
      await revoke.mutateAsync(grant.id);
      revoked.current = true;
      toast.success(t('agents.revoked'));
      setConfirming(false);
    } catch (error) {
      onError(t(resolveAgentGrantErrorKey(error)));
    }
  };

  return (
    <li className="space-y-2 px-3 py-3">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0 flex-1 space-y-1">
          <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
            <Bot className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
            <span className="min-w-0 break-words">
              {t('agents.grants.key', { name, prefix: grant.api_key_prefix })}
            </span>
            <Badge variant={statusVariant[grant.status]}>
              {t(`agents.status.${grant.status}`)}
            </Badge>
          </p>
          <ul className="flex flex-wrap gap-1.5" aria-label={t('agents.form.capabilities')}>
            {grant.capabilities.map(capability => (
              <li key={capability}>
                <Badge variant="secondary">{t(`agents.capabilities.${capability}`)}</Badge>
              </li>
            ))}
            {grant.reveal_secrets ? (
              <li>
                <Badge variant="outline" className="border-warning/50 text-warning">
                  {t('agents.capabilities.reveal')}
                </Badge>
              </li>
            ) : null}
          </ul>
          <p className="text-xs text-muted-foreground">
            {senders.length === 0 && labelIds.length === 0
              ? t('agents.grants.wholeMailbox')
              : [
                  senders.length > 0
                    ? `${t('agents.grants.senders', { count: senders.length })}: ${senders.join(', ')}`
                    : null,
                  labelIds.length > 0
                    ? t('agents.grants.labels', { count: labelIds.length })
                    : null,
                ]
                  .filter(Boolean)
                  .join(' · ')}
          </p>
          <p className="text-xs text-muted-foreground">
            {grant.status === 'revoked' && grant.revoked_at
              ? `${t('agents.grants.revokedAt', { date: date(grant.revoked_at) })}${
                  grant.revoke_reason
                    ? ` · ${t(`agents.revokeReasons.${grant.revoke_reason}`)}`
                    : ''
                }`
              : grant.status === 'expired'
                ? t('agents.grants.expired', { date: date(grant.expires_at) })
                : t('agents.grants.expires', { date: date(grant.expires_at) })}
          </p>
        </div>
        <div className="flex shrink-0 flex-wrap gap-2 sm:justify-end">
          <Button
            type="button"
            size="xs"
            variant="ghost"
            icon={<History className="size-3.5" />}
            aria-expanded={showActivity}
            ref={activityRef}
            aria-controls={activityId}
            aria-label={t('agents.grants.activityLabel', { name })}
            onClick={() => setShowActivity(open => !open)}
          >
            {t('agents.grants.activity')}
          </Button>
          {grant.status === 'active' && !confirming ? (
            <Button
              ref={revokeRef}
              type="button"
              size="xs"
              variant="outline"
              aria-label={t('agents.grants.revokeLabel', { name })}
              onClick={() => setConfirming(true)}
            >
              {t('agents.grants.revoke')}
            </Button>
          ) : null}
        </div>
      </div>

      {confirming ? (
        <div
          role="group"
          aria-labelledby={confirmId}
          className="space-y-2 rounded-md border border-destructive/40 p-3"
          onKeyDown={event => {
            if (event.key === 'Escape' && !revoke.isPending) {
              // Escape cancels the confirmation, not the whole panel.
              event.stopPropagation();
              setConfirming(false);
            }
          }}
        >
          <p id={confirmId} className="text-sm">
            {t('agents.grants.revokeConfirm', { name })}
          </p>
          <div className="flex flex-wrap gap-2">
            <Button
              ref={cancelRef}
              type="button"
              size="xs"
              variant="outline"
              disabled={revoke.isPending}
              onClick={() => setConfirming(false)}
            >
              {t('actions.cancel')}
            </Button>
            <Button
              type="button"
              size="xs"
              variant="destructive"
              loading={revoke.isPending}
              onClick={() => void confirm()}
            >
              {t('agents.grants.revokeAction')}
            </Button>
          </div>
        </div>
      ) : null}

      {showActivity ? (
        <div id={activityId}>
          <ActivityList mailboxId={mailboxId} grantId={grant.id} name={name} />
        </div>
      ) : null}
    </li>
  );
}

function ActivityList({
  mailboxId,
  grantId,
  name,
}: {
  mailboxId: number;
  grantId: number;
  name: string;
}) {
  const t = useT('mailbox');
  const formatter = useDateTimeFormat();
  const activity = useAgentGrantActivity(useMailOrganizationId(), mailboxId, grantId);
  const titleId = useId();

  return (
    <section aria-labelledby={titleId} className="space-y-2 rounded-md bg-muted/50 p-3">
      <h4 id={titleId} className="text-xs font-semibold">
        {t('agents.grants.activityLabel', { name })}
      </h4>
      <p className="text-xs text-muted-foreground">{t('agents.activity.description')}</p>
      {activity.isPending ? (
        <Skeleton className="h-10 w-full" />
      ) : activity.error ? (
        <p role="alert" className="text-xs text-error">
          {t(
            resolveAgentGrantErrorKey(activity.error) === 'agents.errors.notFound'
              ? 'agents.errors.notFound'
              : 'agents.activity.loadFailed'
          )}
        </p>
      ) : activity.data.length === 0 ? (
        <p className="text-xs text-muted-foreground">{t('agents.activity.empty')}</p>
      ) : (
        <ol className="space-y-1">
          {activity.data.map(entry => (
            <li key={entry.id} className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 text-xs">
              <time dateTime={entry.created_at} className="shrink-0 text-muted-foreground">
                {formatter.format(new Date(entry.created_at))}
              </time>
              <span
                className={
                  entry.action === 'read_revealed' ? 'font-medium text-warning' : undefined
                }
              >
                {t(`agents.activity.actions.${entry.action}`)}
              </span>
              <span className="text-muted-foreground">
                {t('agents.activity.message', { id: entry.message_id })}
              </span>
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}

function DraftsSection({ mailbox, grants }: { mailbox: Mailbox; grants: MailAgentGrant[] }) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const drafts = useAgentDrafts(organizationId, mailbox.id, mailbox.access.send);
  const formatter = useDateTimeFormat();
  const titleId = useId();
  const [composing, setComposing] = useState<ComposeDraft | null>(null);
  const opener = useRef<HTMLElement | null>(null);
  const composable = canComposeFrom(mailbox);

  // The composer has no trigger of its own; closing it gives the focus back to the draft's button.
  const wasComposing = useRef(false);
  useEffect(() => {
    if (composing) {
      wasComposing.current = true;
      return;
    }
    if (!wasComposing.current) return;
    wasComposing.current = false;
    const target = opener.current;
    const timer = window.setTimeout(() => {
      if (target?.isConnected) target.focus();
    }, 0);
    return () => window.clearTimeout(timer);
  }, [composing]);

  const replyCopy = {
    prefixSubject: (subject: string) => t('compose.replySubject', { subject }),
    quotedHeader: (params: { date: string; sender: string }) => t('compose.quotedHeader', params),
    formatDate: (iso: string) => formatter.format(new Date(iso)),
  };
  const agentName = (draft: MailAgentDraft) =>
    grants.find(grant => grant.id === draft.grant_id)?.api_key_name ??
    t('agents.drafts.unknownAgent');

  const open = (draft: MailAgentDraft, message: MailMessage, trigger: HTMLElement) => {
    opener.current = trigger;
    // The same hand-off as the AI triage draft: the agent's text above the quoted message.
    const reply = quotedReplyDraft(message, replyCopy);
    setComposing({ ...reply, body: `${draft.text}\n\n${reply.body ?? ''}` });
  };

  return (
    <section className="space-y-3" aria-labelledby={titleId}>
      <div>
        <h3 id={titleId} className="text-sm font-semibold">
          {t('agents.drafts.title')}
        </h3>
        <p className="text-xs text-muted-foreground">{t('agents.drafts.description')}</p>
      </div>
      {drafts.isPending ? (
        <Skeleton className="h-20 w-full" />
      ) : drafts.error ? (
        <p role="alert" className="text-sm text-error">
          {t('agents.drafts.loadFailed')}
        </p>
      ) : drafts.data.items.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t('agents.drafts.empty')}</p>
      ) : (
        <ul aria-labelledby={titleId} className="space-y-3">
          {drafts.data.items.map(draft => (
            <DraftCard
              key={draft.id}
              mailboxId={mailbox.id}
              draft={draft}
              agent={agentName(draft)}
              composable={composable}
              onOpen={open}
            />
          ))}
        </ul>
      )}
      {composing ? (
        <Suspense fallback={null}>
          <ComposeDialog
            mailbox={mailbox}
            draft={composing}
            onClose={() => setComposing(null)}
            onStored={() => setComposing(null)}
          />
        </Suspense>
      ) : null}
    </section>
  );
}

function DraftCard({
  mailboxId,
  draft,
  agent,
  composable,
  onOpen,
}: {
  mailboxId: number;
  draft: MailAgentDraft;
  agent: string;
  composable: boolean;
  onOpen: (draft: MailAgentDraft, message: MailMessage, trigger: HTMLElement) => void;
}) {
  const t = useT('mailbox');
  const formatter = useDateTimeFormat();
  const message = useMailMessage(useMailOrganizationId(), mailboxId, draft.message_id);
  const subject = message.data?.subject || t('agents.drafts.noSubject');

  return (
    <li className="space-y-3 rounded-md border p-3">
      <div className="grid gap-3 md:grid-cols-2">
        <div className="min-w-0 space-y-1">
          <p className="text-xs font-medium text-muted-foreground">
            {t('agents.drafts.inReplyTo')}
          </p>
          {message.isPending ? (
            <Skeleton className="h-10 w-full" />
          ) : message.data ? (
            <div className="space-y-0.5 text-sm">
              <p className="truncate font-medium">{subject}</p>
              <p className="truncate text-xs text-muted-foreground">
                {message.data.from.name
                  ? `${message.data.from.name} <${message.data.from.address}>`
                  : message.data.from.address}
              </p>
              <p className="line-clamp-3 text-xs whitespace-pre-line text-muted-foreground">
                {message.data.text_body}
              </p>
            </div>
          ) : (
            <p className="text-xs text-error">{t('agents.drafts.messageMissing')}</p>
          )}
        </div>
        <div className="min-w-0 space-y-1">
          <p className="flex flex-wrap items-center gap-x-2 text-xs font-medium text-muted-foreground">
            <span>{t('agents.drafts.by', { name: agent })}</span>
            <time dateTime={draft.updated_at}>{formatter.format(new Date(draft.updated_at))}</time>
          </p>
          <p className="max-h-40 overflow-y-auto rounded-md bg-muted/50 p-2 text-sm whitespace-pre-wrap break-words">
            {draft.text}
          </p>
        </div>
      </div>
      {composable ? (
        <div className="flex justify-end">
          <Button
            type="button"
            size="sm"
            variant="outline"
            icon={<PenLine className="size-3.5" />}
            disabled={!message.data}
            aria-label={t('agents.drafts.openLabel', { subject })}
            onClick={event => {
              if (message.data) onOpen(draft, message.data, event.currentTarget);
            }}
          >
            {t('agents.drafts.open')}
          </Button>
        </div>
      ) : null}
    </li>
  );
}
