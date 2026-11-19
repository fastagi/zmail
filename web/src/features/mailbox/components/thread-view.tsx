'use client';

import { useState, type ReactNode } from 'react';
import { AlertCircle, ArrowLeft, ChevronLeft, ChevronRight } from 'lucide-react';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { MailAttachmentList } from '@/features/mailbox/components/mail-attachments';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import {
  ErrorAlert,
  MailHtmlFrame,
  StaleNotice,
  useDateTimeFormat,
} from '@/features/mailbox/components/mail-shared';
import { MessageActions } from '@/features/mailbox/components/message-actions';
import { useMailMessage, useMailThread } from '@/features/mailbox/hooks/use-mailbox';
import type { MailMessage } from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/** What the reading pane needs to act on the messages it shows. */
export interface ReaderProps {
  mailboxId: number;
  mailboxAddress: string;
  canSend: boolean;
  canOrganize: boolean;
  onPrevious: (() => void) | null;
  onNext: (() => void) | null;
  onClose: () => void;
  onReply: (message: MailMessage) => void;
  onReplyAll: (message: MailMessage) => void;
  onForward: (message: MailMessage) => void;
}

/**
 * A thread opened from a placement view: every message of it outside the trash, oldest first, so
 * the replies in Sent show beside the mail they answer. The newest message is open; the others
 * open on request. The thread is polled with the list, so a new reply appears on its own.
 */
export function ThreadView({ threadId, ...reader }: ReaderProps & { threadId: number }) {
  const t = useT('mailbox');
  const query = useMailThread(useMailOrganizationId(), reader.mailboxId, threadId);
  const [expanded, setExpanded] = useState<ReadonlySet<number>>(() => new Set());

  return (
    <ReaderState query={query} reader={reader}>
      {(messages: MailMessage[]) => {
        const newest = messages.at(-1);
        if (!newest) return null;
        return (
          <article className="space-y-4">
            <ReaderToolbar reader={reader} />
            <h3 className="text-lg font-semibold break-words">
              {newest.subject || t('list.noSubject')}
            </h3>
            <ol aria-label={t('thread.label')} className="space-y-3">
              {messages.map(message => (
                <li key={message.id}>
                  {message.id === newest.id || expanded.has(message.id) ? (
                    <MessageCard message={message} reader={reader} />
                  ) : (
                    <CollapsedMessage
                      message={message}
                      onExpand={() => setExpanded(current => new Set(current).add(message.id))}
                    />
                  )}
                </li>
              ))}
            </ol>
          </article>
        );
      }}
    </ReaderState>
  );
}

/** One message opened from a view that lists single messages: stars, labels, search, the trash. */
export function MessageDetail({ messageId, ...reader }: ReaderProps & { messageId: number }) {
  const t = useT('mailbox');
  const query = useMailMessage(useMailOrganizationId(), reader.mailboxId, messageId);
  return (
    <ReaderState query={query} reader={reader}>
      {(message: MailMessage) => (
        <article className="space-y-4">
          <ReaderToolbar reader={reader} />
          <h3 className="text-lg font-semibold break-words">
            {message.subject || t('list.noSubject')}
          </h3>
          <MessageCard key={message.id} message={message} reader={reader} />
        </article>
      )}
    </ReaderState>
  );
}

function ReaderState<T>({
  query,
  reader,
  children,
}: {
  query: { isPending: boolean; error: unknown; data: T | undefined; refetch: () => unknown };
  reader: ReaderProps;
  children: (data: T) => ReactNode;
}) {
  const t = useT('mailbox');
  if (query.isPending) {
    return (
      <div className="space-y-3">
        <ReaderToolbar reader={reader} />
        <Skeleton className="h-48 w-full" />
      </div>
    );
  }
  // A failed poll keeps the open message on screen; only one that never loaded is an error.
  if (query.data === undefined) {
    return (
      <div className="space-y-3">
        <ReaderToolbar reader={reader} />
        <ErrorAlert
          message={t(resolveMailboxErrorKey(query.error))}
          retryLabel={t('actions.retry')}
          onRetry={() => void query.refetch()}
        />
      </div>
    );
  }
  // The notice takes its own slot, so the message below keeps its state when it comes and goes.
  return (
    <>
      {query.error ? <StaleNotice message={t('list.refreshFailed')} className="mb-3" /> : null}
      {children(query.data)}
    </>
  );
}

/** Back to the list, and the previous and next row of the list. */
function ReaderToolbar({ reader }: { reader: ReaderProps }) {
  const t = useT('mailbox');
  return (
    <div className="flex items-center gap-1">
      <Button
        type="button"
        size="sm"
        variant="ghost"
        icon={<ArrowLeft className="size-4" />}
        onClick={reader.onClose}
      >
        {t('actions.back')}
      </Button>
      <Button
        type="button"
        isIcon
        size="sm"
        variant="ghost"
        disabled={!reader.onPrevious}
        aria-label={t('message.previous')}
        title={t('message.previous')}
        onClick={reader.onPrevious ?? undefined}
      >
        <ChevronLeft className="size-4" />
      </Button>
      <Button
        type="button"
        isIcon
        size="sm"
        variant="ghost"
        disabled={!reader.onNext}
        aria-label={t('message.next')}
        title={t('message.next')}
        onClick={reader.onNext ?? undefined}
      >
        <ChevronRight className="size-4" />
      </Button>
    </div>
  );
}

/** Marks a message the mailbox sent, so replies stand out in a conversation. */
function SentMark() {
  const t = useT('mailbox');
  return (
    <Badge variant="secondary" className="shrink-0">
      {t('thread.sent')}
    </Badge>
  );
}

function senderName(message: MailMessage): string {
  return message.from.name || message.from.address;
}

/** A message of a thread that is not open: who, when, and the start of its text. */
function CollapsedMessage({ message, onExpand }: { message: MailMessage; onExpand: () => void }) {
  const t = useT('mailbox');
  const formatter = useDateTimeFormat();
  const snippet = message.text_body.replace(/\s+/gu, ' ').trim().slice(0, 160);
  return (
    <button
      type="button"
      aria-expanded={false}
      aria-label={t('thread.expand', { sender: senderName(message) })}
      className={cn(
        'flex w-full items-start gap-3 rounded-md border px-4 py-3 text-left transition-colors focus-ring hover:bg-accent/70',
        message.direction === 'outbound' && 'border-l-2 border-l-primary/60'
      )}
      onClick={onExpand}
    >
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-2">
          <span className="truncate text-sm font-medium">{senderName(message)}</span>
          {message.direction === 'outbound' ? <SentMark /> : null}
        </span>
        <span className="block truncate text-sm text-muted-foreground">{snippet}</span>
      </span>
      <time dateTime={message.created_at} className="shrink-0 text-xs text-muted-foreground">
        {formatter.format(new Date(message.created_at))}
      </time>
    </button>
  );
}

/** An open message: its headers, its actions, its files, and its content. */
function MessageCard({ message, reader }: { message: MailMessage; reader: ReaderProps }) {
  const t = useT('mailbox');
  const formatter = useDateTimeFormat();
  const [plainText, setPlainText] = useState(false);
  const hasHtml = message.html_body !== '';
  const hasText = message.text_body !== '';
  const showHtml = hasHtml && !(plainText && hasText);

  return (
    <div
      className={cn(
        'space-y-3 rounded-md border p-4',
        message.direction === 'outbound' && 'border-l-2 border-l-primary/60'
      )}
    >
      {message.direction === 'outbound' ? <SentMark /> : null}
      <div className="flex flex-wrap items-start justify-between gap-2">
        <dl className="grid min-w-0 flex-1 grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 text-sm">
          <dt className="text-muted-foreground">{t('message.from')}</dt>
          <dd className="break-words">
            {message.from.name
              ? `${message.from.name} <${message.from.address}>`
              : message.from.address}
          </dd>
          <dt className="text-muted-foreground">{t('message.to')}</dt>
          <dd className="break-words">{message.to.join(', ')}</dd>
          {message.cc.length > 0 ? (
            <>
              <dt className="text-muted-foreground">{t('message.cc')}</dt>
              <dd className="break-words">{message.cc.join(', ')}</dd>
            </>
          ) : null}
          <dt className="text-muted-foreground">{t('message.date')}</dt>
          <dd>
            <time dateTime={message.created_at}>
              {formatter.format(new Date(message.created_at))}
            </time>
          </dd>
        </dl>
        <MessageActions
          message={message}
          mailboxAddress={reader.mailboxAddress}
          canSend={reader.canSend}
          canOrganize={reader.canOrganize}
          onReply={reader.onReply}
          onReplyAll={reader.onReplyAll}
          onForward={reader.onForward}
          onClose={reader.onClose}
        />
      </div>

      {message.status === 'failed' ? (
        <Alert variant="destructive">
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertDescription>
            {message.failure_code
              ? t('message.failed', { code: message.failure_code })
              : t('message.failedUnknown')}
          </AlertDescription>
        </Alert>
      ) : null}

      <MailAttachmentList message={message} />

      {hasHtml && hasText ? (
        <div role="group" aria-label={t('message.viewLabel')} className="flex gap-2">
          <Button
            type="button"
            size="xs"
            variant={showHtml ? 'default' : 'outline'}
            aria-pressed={showHtml}
            onClick={() => setPlainText(false)}
          >
            {t('message.viewHtml')}
          </Button>
          <Button
            type="button"
            size="xs"
            variant={showHtml ? 'outline' : 'default'}
            aria-pressed={!showHtml}
            onClick={() => setPlainText(true)}
          >
            {t('message.viewText')}
          </Button>
        </div>
      ) : null}

      {showHtml ? (
        <MailHtmlFrame html={message.html_body} title={t('message.htmlTitle')} />
      ) : hasText ? (
        <pre className="font-sans text-sm break-words whitespace-pre-wrap">{message.text_body}</pre>
      ) : (
        <p className="text-sm text-muted-foreground">{t('message.emptyBody')}</p>
      )}
    </div>
  );
}
