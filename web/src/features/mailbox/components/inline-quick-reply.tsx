'use client';

import { useEffect, useRef, useState, type KeyboardEvent } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Forward, Maximize2, Reply, ReplyAll, Send, X } from 'lucide-react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import { Textarea } from '@/components/ui/textarea';
import {
  replyAllDraft,
  replyAllRecipients,
  replyDraft,
} from '@/features/mailbox/components/compose-drafts';
import { invalidateMail, useSendMailMessage } from '@/features/mailbox/hooks/use-mailbox';
import { splitRecipients } from '@/features/mailbox/schemas';
import type { MailMessage, SendMailMessageInput } from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

type QuickReplyMode = 'reply' | 'replyAll';

function formatSender(from: MailMessage['from']): string {
  return from.name ? `${from.name} <${from.address}>` : from.address;
}

/**
 * Gmail-style bottom action row and expandable inline reply box for the unified inbox reading pane.
 * Collapsed, it offers pill buttons for Reply, Reply all (only when there are multiple recipients),
 * and Forward. Clicking Reply or Reply all expands an inline reply composer right below the thread
 * without opening the floating compose window, with a Pop out button to hand off to the composer.
 */
export function InlineQuickReply({
  organizationId,
  mailboxId,
  mailboxAddress,
  replyTarget,
  forwardTarget,
  onPopOutReply,
  onPopOutReplyAll,
  onForward,
}: {
  organizationId: number;
  mailboxId: number;
  mailboxAddress: string;
  replyTarget: MailMessage | undefined;
  forwardTarget: MailMessage | undefined;
  onPopOutReply: (message: MailMessage) => void;
  onPopOutReplyAll: (message: MailMessage) => void;
  onForward: (message: MailMessage) => void;
}) {
  const t = useT('mailbox');
  const queryClient = useQueryClient();
  const sendMail = useSendMailMessage(organizationId, mailboxId);
  const [mode, setMode] = useState<QuickReplyMode | null>(null);
  const [body, setBody] = useState('');
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);

  const allRecipients = replyTarget
    ? replyAllRecipients(replyTarget, mailboxAddress)
    : { to: [], cc: [] };
  const canReplyAll =
    replyTarget !== undefined && allRecipients.to.length + allRecipients.cc.length > 1;

  useEffect(() => {
    if (mode !== null) {
      textareaRef.current?.focus();
    }
  }, [mode]);

  if (!replyTarget && !forwardTarget) return null;

  const collapse = () => {
    setMode(null);
    setBody('');
  };

  const handlePopOut = () => {
    if (!replyTarget || mode === null) return;
    const currentMode = mode;
    collapse();
    if (currentMode === 'replyAll') onPopOutReplyAll(replyTarget);
    else onPopOutReply(replyTarget);
  };

  const handleSend = async () => {
    const trimmedBody = body.trim();
    if (!replyTarget || mode === null || trimmedBody === '' || sendMail.isPending) return;

    const prefixSubject = (subject: string) => t('compose.replySubject', { subject });
    const draft =
      mode === 'replyAll'
        ? replyAllDraft(replyTarget, mailboxAddress, {
            prefixSubject,
            quotedHeader: params => t('compose.quotedHeader', params),
            formatDate: iso => iso,
          })
        : replyDraft(replyTarget, prefixSubject);

    const to = splitRecipients(draft.to);
    const cc = splitRecipients(draft.cc ?? '');
    const subject = draft.subject.trim() || prefixSubject(t('list.noSubject'));

    const payload: SendMailMessageInput = {
      to,
      ...(cc.length > 0 ? { cc } : {}),
      subject,
      text: trimmedBody,
      ...(draft.inReplyTo ? { in_reply_to: draft.inReplyTo } : {}),
    };

    try {
      const sent = await sendMail.mutateAsync(payload);
      collapse();
      toast.success(
        t(sent?.queued_reason === 'send_limit' ? 'messages.queued' : 'messages.sent')
      );
      void invalidateMail(queryClient, organizationId, mailboxId);
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') {
      event.preventDefault();
      void handleSend();
      return;
    }
    if (event.key === 'Escape') {
      event.preventDefault();
      if (body.trim() === '') setMode(null);
    }
  };

  if (mode !== null && replyTarget) {
    const senderLabel = formatSender(replyTarget.from);
    const headerRecipient =
      mode === 'replyAll'
        ? allRecipients.to
            .map(addr =>
              addr === replyTarget.from.address.trim().toLowerCase() ? senderLabel : addr
            )
            .join(', ')
        : senderLabel;

    return (
      <div
        data-inline-quick-reply="expanded"
        className="mx-auto mt-6 w-full max-w-[640px] px-5 sm:px-7"
      >
        <div className="rounded-2xl border border-border-main bg-bg-surface shadow-sm transition-all motion-safe:animate-in motion-safe:fade-in motion-safe:slide-in-from-bottom-1 motion-safe:duration-150">
          <div className="flex items-start justify-between gap-2 border-b border-border-subtle px-4 py-2.5">
            <div className="min-w-0 flex-1 text-xs text-text-muted">
              <div className="flex items-center gap-1.5">
                {mode === 'replyAll' ? (
                  <ReplyAll className="size-3.5 shrink-0" aria-hidden="true" />
                ) : (
                  <Reply className="size-3.5 shrink-0" aria-hidden="true" />
                )}
                <span className="truncate font-medium text-text-main">
                  {mode === 'replyAll'
                    ? t('quickReply.replyAllTo', { recipients: headerRecipient })
                    : t('quickReply.replyTo', { recipient: headerRecipient })}
                </span>
              </div>
              {mode === 'replyAll' && allRecipients.cc.length > 0 ? (
                <p className="mt-0.5 truncate pl-5 text-[11px] text-text-faint">
                  {t('quickReply.cc', { recipients: allRecipients.cc.join(', ') })}
                </p>
              ) : null}
            </div>
            <div className="flex shrink-0 items-center gap-1">
              <Button
                type="button"
                size="xs"
                variant="ghost"
                className="gap-1 text-text-muted hover:text-text-main"
                onClick={handlePopOut}
              >
                <Maximize2 className="size-3" aria-hidden="true" />
                <span>{t('quickReply.popOut')}</span>
              </Button>
              <Button
                type="button"
                isIcon
                size="xs"
                variant="ghost"
                aria-label={t('quickReply.discard')}
                title={t('quickReply.discard')}
                className="text-text-muted hover:text-text-main"
                onClick={collapse}
              >
                <X className="size-3.5" aria-hidden="true" />
              </Button>
            </div>
          </div>
          <div className="p-3">
            <Textarea
              ref={textareaRef}
              value={body}
              rows={4}
              aria-label={t('quickReply.placeholder')}
              placeholder={t('quickReply.placeholder')}
              disabled={sendMail.isPending}
              className="min-h-[96px] border-0 bg-transparent px-1 py-1 shadow-none focus-visible:ring-0"
              onChange={event => setBody(event.target.value)}
              onKeyDown={handleKeyDown}
            />
          </div>
          <div className="flex items-center justify-between gap-2 border-t border-border-subtle px-4 py-2.5">
            <div className="flex items-center gap-2.5">
              <Button
                type="button"
                size="sm"
                className="rounded-full px-4"
                disabled={body.trim() === '' || sendMail.isPending}
                onClick={() => void handleSend()}
              >
                <Send className="size-3.5" aria-hidden="true" />
                <span>{sendMail.isPending ? t('quickReply.sending') : t('quickReply.send')}</span>
              </Button>
              <span className="hidden text-[11px] text-text-faint sm:inline">
                {t('quickReply.shortcutHint')}
              </span>
            </div>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              disabled={sendMail.isPending}
              onClick={collapse}
            >
              {t('actions.cancel')}
            </Button>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div
      data-inline-quick-reply="collapsed"
      className="mx-auto mt-6 w-full max-w-[640px] px-5 sm:px-7"
    >
      <div className="flex flex-wrap items-center gap-2 border-t border-border-subtle pt-5">
        {replyTarget ? (
          <Button
            type="button"
            size="sm"
            variant="outline"
            aria-label={t('quickReply.replyAria')}
            className="gap-1.5 rounded-full px-4"
            onClick={() => setMode('reply')}
          >
            <Reply className="size-3.5" aria-hidden="true" />
            <span>{t('quickReply.reply')}</span>
          </Button>
        ) : null}
        {canReplyAll ? (
          <Button
            type="button"
            size="sm"
            variant="outline"
            aria-label={t('quickReply.replyAllAria')}
            className="gap-1.5 rounded-full px-4"
            onClick={() => setMode('replyAll')}
          >
            <ReplyAll className="size-3.5" aria-hidden="true" />
            <span>{t('quickReply.replyAll')}</span>
          </Button>
        ) : null}
        {forwardTarget ? (
          <Button
            type="button"
            size="sm"
            variant="outline"
            aria-label={t('quickReply.forwardAria')}
            className="gap-1.5 rounded-full px-4"
            onClick={() => onForward(forwardTarget)}
          >
            <Forward className="size-3.5" aria-hidden="true" />
            <span>{t('quickReply.forward')}</span>
          </Button>
        ) : null}
      </div>
    </div>
  );
}
