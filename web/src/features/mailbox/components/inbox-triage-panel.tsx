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
import { TriageReasonLine } from '@/features/mailbox/components/inbox-triage-reason';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { useMailReplyDraft, useOverrideMailTriage } from '@/features/mailbox/hooks/use-mail-triage';
import {
  MAIL_TRIAGE_OVERRIDE_CATEGORIES,
  type MailTriageOverrideCategory,
} from '@/features/mailbox/triage-schemas';
import type { InboxItem, MailMessage } from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { useT } from '@/i18n';

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
