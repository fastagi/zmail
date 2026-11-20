'use client';

import { Sparkles } from 'lucide-react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Kbd } from '@/components/ui/kbd';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { useMailReplyDraft, useOverrideMailTriage } from '@/features/mailbox/hooks/use-mail-triage';
import {
  MAIL_TRIAGE_OVERRIDE_CATEGORIES,
  type MailTriageOverrideCategory,
} from '@/features/mailbox/triage-schemas';
import type {
  InboxItem,
  InboxTriageCounts,
  MailMessage,
  MailTriage,
  MailTriageCategory,
} from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/** The triage tabs of the unified inbox; `review` rides with `reply`, marked on its rows. */
export const INBOX_TRIAGE_TABS = ['reply', 'deadline', 'fyi'] as const;
export type InboxTriageTab = (typeof INBOX_TRIAGE_TABS)[number];

/** The categories a tab lists. */
export function triageTabCategories(tab: InboxTriageTab): MailTriageCategory[] {
  return tab === 'reply' ? ['reply', 'review'] : [tab];
}

/** The error copy of a triage request; a model or switch failure has its own words. */
function triageErrorKey(error: unknown) {
  if (error instanceof ApiError) {
    if (error.errorCode === ApiErrorCode.MAILBOX_TRIAGE_UNAVAILABLE) {
      return 'triage.errors.unavailable' as const;
    }
    if (error.errorCode === ApiErrorCode.MAILBOX_TRIAGE_MODEL_FAILED) {
      return 'triage.errors.modelFailed' as const;
    }
  }
  return resolveMailboxErrorKey(error);
}

/**
 * The hint and the triage tabs over the unified inbox, shown only while the organization's AI
 * triage is on (its counts are not null). Each tab shows the caller's unread mail in it; `null`
 * is every message.
 */
export function InboxTriageTabs({
  counts,
  active,
  onChange,
}: {
  counts: InboxTriageCounts;
  active: InboxTriageTab | null;
  onChange: (tab: InboxTriageTab | null) => void;
}) {
  const t = useT('mailbox');
  const unread: Record<InboxTriageTab, number> = {
    reply: counts.reply + counts.review,
    deadline: counts.deadline,
    fyi: counts.fyi,
  };
  const tab = (value: InboxTriageTab | null, label: string, count?: number) => (
    <button
      key={value ?? 'all'}
      type="button"
      role="tab"
      aria-selected={active === value}
      className={cn(
        'shrink-0 rounded-full px-2.5 py-1 text-xs transition-colors focus-ring motion-reduce:transition-none',
        active === value
          ? 'bg-row-selected font-semibold text-text-main'
          : 'text-text-muted hover:text-text-main'
      )}
      onClick={() => onChange(value)}
    >
      {label}
      {count ? (
        <>
          {' '}
          <span className="font-normal text-text-faint tabular-nums">{count}</span>
        </>
      ) : null}
    </button>
  );

  return (
    <div className="shrink-0 px-4 pb-2 sm:px-6" data-inbox-triage="">
      <p className="flex items-center gap-1.5 text-xs text-text-muted">
        <Sparkles className="size-3.5 shrink-0" aria-hidden="true" />
        {t('triage.hint')}
      </p>
      <div
        role="tablist"
        aria-label={t('triage.tabs.label')}
        className="mt-2 flex gap-1 overflow-x-auto"
      >
        {tab(null, t('triage.tabs.all'))}
        {INBOX_TRIAGE_TABS.map(value => tab(value, t(`triage.tabs.${value}`), unread[value]))}
      </div>
    </div>
  );
}

/** A row's triage line under the subject: the category, then why, or that a reader chose it. */
export function TriageReasonLine({ triage }: { triage: MailTriage }) {
  const t = useT('mailbox');
  const why = triage.source === 'manual' ? t('triage.manual') : triage.reason;
  return (
    <span className="mt-0.5 flex min-w-0 items-center gap-1.5 text-xs" data-triage-reason="">
      <span
        className={cn(
          'shrink-0 rounded-full px-1.5 font-medium',
          triage.category === 'review'
            ? 'bg-row-selected text-text-subtle'
            : 'bg-brand/10 text-text-main'
        )}
      >
        {t(`triage.categories.${triage.category}`)}
      </span>
      {why ? <span className="min-w-0 truncate text-text-muted">{why}</span> : null}
    </span>
  );
}

/**
 * The AI triage block of the reading pane, shown only while the organization's triage is on and
 * the open mail is inbound. It shows the row's result and the override menu (`F`, controlled by
 * the page so the key opens it), and, with the mailbox's `send`, drafts a reply on request: the
 * draft names what it relied on and only fills the reply window; nothing is sent.
 */
export function InboxTriagePanel({
  item,
  replyTarget,
  canSend,
  menuOpen,
  onMenuOpenChange,
  onUseDraft,
}: {
  item: InboxItem;
  replyTarget: MailMessage | undefined;
  canSend: boolean;
  menuOpen: boolean;
  onMenuOpenChange: (open: boolean) => void;
  onUseDraft: (message: MailMessage, body: string) => void;
}) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const override = useOverrideMailTriage(organizationId);
  const draft = useMailReplyDraft(organizationId);
  const triage = item.triage ?? null;

  const choose = (category: MailTriageOverrideCategory) =>
    override.mutate(
      { mailboxId: item.mailbox_id, messageId: item.id, category },
      {
        onSuccess: () => toast.success(t('triage.changed')),
        onError: failure => toast.error(t(triageErrorKey(failure))),
      }
    );
  const generate = () => {
    if (replyTarget) draft.mutate({ mailboxId: item.mailbox_id, messageId: replyTarget.id });
  };

  return (
    <section
      aria-label={t('triage.label')}
      className="mx-auto mb-6 w-full max-w-[640px] space-y-3 px-5 sm:px-7"
      data-triage-panel=""
    >
      <div className="flex min-w-0 flex-wrap items-center gap-2 text-xs">
        {triage ? (
          <TriageReasonLine triage={triage} />
        ) : (
          <span className="text-text-muted">{t('triage.pending')}</span>
        )}
        <span className="flex-1" />
        <DropdownMenu open={menuOpen} onOpenChange={onMenuOpenChange}>
          <DropdownMenuTrigger asChild>
            <Button type="button" size="xs" variant="ghost" disabled={override.isPending}>
              {t('triage.change')}
              <Kbd aria-hidden="true" className="max-md:hidden">
                ⇧F
              </Kbd>
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuLabel>{t('triage.changeTitle')}</DropdownMenuLabel>
            <DropdownMenuRadioGroup
              value={triage?.category ?? ''}
              onValueChange={value => choose(value as MailTriageOverrideCategory)}
            >
              {MAIL_TRIAGE_OVERRIDE_CATEGORIES.map(category => (
                <DropdownMenuRadioItem key={category} value={category}>
                  {t(`triage.categories.${category}`)}
                </DropdownMenuRadioItem>
              ))}
            </DropdownMenuRadioGroup>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      {canSend && replyTarget ? (
        draft.data ? (
          <div
            className="space-y-2 rounded-[10px] border border-border-subtle bg-bg-canvas p-3"
            data-reply-draft=""
          >
            <p className="flex items-center gap-1.5 text-xs font-medium text-text-subtle">
              <Sparkles className="size-3.5" aria-hidden="true" />
              {t('triage.draft.title')}
            </p>
            <p className="text-[13px] whitespace-pre-wrap text-text-main">{draft.data.body}</p>
            {draft.data.basis.length > 0 ? (
              <p className="text-xs text-text-muted">
                {t('triage.draft.basis', {
                  items: draft.data.basis.map(basis => basis.label || `#${basis.id}`).join(' · '),
                })}
              </p>
            ) : null}
            <div className="flex flex-wrap gap-2">
              <Button
                type="button"
                size="xs"
                onClick={() => onUseDraft(replyTarget, draft.data.body)}
              >
                {t('triage.draft.use')}
              </Button>
              <Button
                type="button"
                size="xs"
                variant="ghost"
                disabled={draft.isPending}
                onClick={generate}
              >
                {t('triage.draft.again')}
              </Button>
            </div>
          </div>
        ) : (
          <div className="space-y-1">
            <Button
              type="button"
              size="xs"
              variant="outline"
              icon={<Sparkles className="size-3.5" />}
              disabled={draft.isPending}
              onClick={generate}
            >
              {draft.isPending ? t('triage.draft.generating') : t('triage.draft.generate')}
            </Button>
            {draft.error ? (
              <p role="alert" className="text-xs text-error">
                {t(triageErrorKey(draft.error))}
              </p>
            ) : null}
          </div>
        )
      ) : null}
    </section>
  );
}
