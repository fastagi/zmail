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
  ReaderArticleSkeleton,
  StaleNotice,
  useDateTimeFormat,
} from '@/features/mailbox/components/mail-shared';
import { MessageActions } from '@/features/mailbox/components/message-actions';
import { SecretsPanel } from '@/features/mailbox/components/secrets-panel';
import {
  useMailMessage,
  useMailThread,
  useRevealMailMessage,
} from '@/features/mailbox/hooks/use-mailbox';
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
 * How a reader lays out: `card` in a mailbox's workspace (its own back and previous/next bar, each
 * message framed with its actions), `article` in the unified inbox (§11.0: the pane owns the
 * toolbar; a 640px column with `kicker` above a serif subject, each message under its sender's
 * avatar with only its star and mark-unread actions, replies and moves living in the toolbar).
 */
export interface ReaderLayout {
  variant?: 'card' | 'article';
  /** The line above the subject in the `article` layout. */
  kicker?: string;
}

/** The subject of an open message or thread, sized for the layout. */
function ReaderSubject({ subject, layout }: { subject: string; layout: ReaderLayout }) {
  const t = useT('mailbox');
  if (layout.variant !== 'article') {
    return <h3 className="text-lg font-semibold break-words">{subject || t('list.noSubject')}</h3>;
  }
  return (
    <header>
      {layout.kicker ? <p className="text-xs text-text-muted">{layout.kicker}</p> : null}
      <h2 className="mt-2 mb-[18px] font-serif text-[30px] leading-[1.25] font-semibold tracking-[-0.01em] break-words">
        {subject || t('list.noSubject')}
      </h2>
    </header>
  );
}

const articleClassName = 'mx-auto w-full max-w-[640px] space-y-4 px-5 sm:px-7';

/** The originals of the messages revealed in this reader, by message id. */
type RevealedMessages = ReadonlyMap<number, MailMessage>;

const noneRevealed: RevealedMessages = new Map();

/**
 * The originals a reader has revealed while it shows `owner` (a thread or a message), so the
 * subject above the messages and each message agree. Another row, or reopening this one, starts
 * masked again; the reader stays mounted, so focus on its controls survives moving through rows.
 */
function useRevealedMessages(owner: number) {
  const [state, setState] = useState<{ owner: number; messages: RevealedMessages }>(() => ({
    owner,
    messages: noneRevealed,
  }));
  // A new row drops what the last one revealed, during render, before it can show.
  if (state.owner !== owner) setState({ owner, messages: noneRevealed });
  const revealed = state.owner === owner ? state.messages : noneRevealed;
  const remember = (message: MailMessage) =>
    setState(current =>
      current.owner === owner
        ? { owner, messages: new Map(current.messages).set(message.id, message) }
        : current
    );
  return { revealed, remember };
}

/**
 * A thread opened from a placement view: every message of it outside the trash, oldest first, so
 * the replies in Sent show beside the mail they answer. The newest message is open; the others
 * open on request. The thread is polled with the list, so a new reply appears on its own.
 */
export function ThreadView({
  threadId,
  variant = 'card',
  kicker,
  ...reader
}: ReaderProps & ReaderLayout & { threadId: number }) {
  const t = useT('mailbox');
  const query = useMailThread(useMailOrganizationId(), reader.mailboxId, threadId);
  const [expanded, setExpanded] = useState<ReadonlySet<number>>(() => new Set());
  const { revealed, remember } = useRevealedMessages(threadId);
  const layout = { variant, kicker };

  return (
    <ReaderState query={query} reader={reader} layout={layout}>
      {(messages: MailMessage[]) => {
        const newest = messages.at(-1);
        if (!newest) return null;
        return (
          <article className={variant === 'article' ? articleClassName : 'space-y-4'}>
            {variant === 'article' ? null : <ReaderToolbar reader={reader} />}
            <ReaderSubject subject={(revealed.get(newest.id) ?? newest).subject} layout={layout} />
            <ol aria-label={t('thread.label')} className="space-y-3">
              {messages.map(message => (
                <li key={message.id}>
                  {message.id === newest.id || expanded.has(message.id) ? (
                    <MessageCard
                      message={message}
                      revealed={revealed.get(message.id) ?? null}
                      onRevealed={remember}
                      reader={reader}
                      variant={variant}
                    />
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
export function MessageDetail({
  messageId,
  variant = 'card',
  kicker,
  ...reader
}: ReaderProps & ReaderLayout & { messageId: number }) {
  const query = useMailMessage(useMailOrganizationId(), reader.mailboxId, messageId);
  const { revealed, remember } = useRevealedMessages(messageId);
  const layout = { variant, kicker };
  return (
    <ReaderState query={query} reader={reader} layout={layout}>
      {(message: MailMessage) => (
        <article className={variant === 'article' ? articleClassName : 'space-y-4'}>
          {variant === 'article' ? null : <ReaderToolbar reader={reader} />}
          <ReaderSubject subject={(revealed.get(message.id) ?? message).subject} layout={layout} />
          <MessageCard
            key={message.id}
            message={message}
            revealed={revealed.get(message.id) ?? null}
            onRevealed={remember}
            reader={reader}
            variant={variant}
          />
        </article>
      )}
    </ReaderState>
  );
}

function ReaderState<T>({
  query,
  reader,
  layout,
  children,
}: {
  query: { isPending: boolean; error: unknown; data: T | undefined; refetch: () => unknown };
  reader: ReaderProps;
  layout: ReaderLayout;
  children: (data: T) => ReactNode;
}) {
  const t = useT('mailbox');
  const article = layout.variant === 'article';
  if (query.isPending) {
    if (article) return <ReaderArticleSkeleton className={articleClassName} />;
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
      <div className={article ? articleClassName : 'space-y-3'}>
        {article ? null : <ReaderToolbar reader={reader} />}
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

/**
 * An open message: its headers, its actions, its files, and its content. `revealed` is its
 * original once the reader revealed it, kept by the reader so the subject above follows.
 */
function MessageCard({
  message: stored,
  revealed,
  onRevealed,
  reader,
  variant,
}: {
  message: MailMessage;
  revealed: MailMessage | null;
  onRevealed: (message: MailMessage) => void;
  reader: ReaderProps;
  variant: NonNullable<ReaderLayout['variant']>;
}) {
  const t = useT('mailbox');
  const [plainText, setPlainText] = useState(false);
  const reveal = useRevealMailMessage(useMailOrganizationId(), reader.mailboxId);
  const message = revealed?.id === stored.id ? revealed : stored;
  const showSecretsPanel = stored.secrets_masked === true;
  const onReveal = () => {
    reveal
      .mutateAsync(stored.id)
      .then(onRevealed)
      .catch(() => undefined);
  };
  const hasHtml = message.html_body !== '';
  const hasText = message.text_body !== '';
  const showHtml = hasHtml && !(plainText && hasText);
  const article = variant === 'article';

  return (
    <div
      className={cn(
        article ? 'space-y-4' : 'space-y-3 rounded-md border p-4',
        message.direction === 'outbound' && 'border-l-2 border-l-primary/60',
        message.direction === 'outbound' && article && 'pl-4'
      )}
    >
      {article ? (
        <SenderLine message={message} reader={reader} />
      ) : (
        <>
          {message.direction === 'outbound' ? <SentMark /> : null}
          <MessageHeaders message={message} reader={reader} />
        </>
      )}

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

      {showSecretsPanel ? (
        <SecretsPanel
          revealed={message !== stored}
          pending={reveal.isPending}
          failed={reveal.isError === true}
          onReveal={onReveal}
        />
      ) : null}

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
        <pre
          className={cn(
            'font-sans break-words whitespace-pre-wrap',
            article ? 'text-[15.5px] leading-[1.8] text-text-subtle' : 'text-sm'
          )}
        >
          {message.text_body}
        </pre>
      ) : (
        <p className="text-sm text-muted-foreground">{t('message.emptyBody')}</p>
      )}
    </div>
  );
}

/** A message's addresses and date, with every action on it: the workspace's card header. */
function MessageHeaders({ message, reader }: { message: MailMessage; reader: ReaderProps }) {
  const t = useT('mailbox');
  const formatter = useDateTimeFormat();
  return (
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
  );
}

/**
 * The sender above a message in the article layout: a 34px avatar with the initial, the name and
 * address, the time, then the actions that stay with one message (star, mark unread). Replying
 * and moving belong to the pane's toolbar, so `send` is not passed on.
 */
function SenderLine({ message, reader }: { message: MailMessage; reader: ReaderProps }) {
  const formatter = useDateTimeFormat();
  const name = senderName(message);
  return (
    <div className="flex flex-wrap items-center gap-3">
      <span
        aria-hidden="true"
        className="flex size-[34px] shrink-0 items-center justify-center rounded-full bg-avatar-bg font-semibold text-text-subtle"
      >
        {Array.from(name)[0]?.toUpperCase() ?? ''}
      </span>
      <div className="min-w-0 flex-1">
        <p className="flex min-w-0 items-center gap-2">
          <span className="truncate font-semibold">{name}</span>
          {message.direction === 'outbound' ? <SentMark /> : null}
        </p>
        <p className="truncate text-xs text-text-muted">{message.from.address}</p>
      </div>
      <time dateTime={message.created_at} className="shrink-0 text-xs text-text-faint">
        {formatter.format(new Date(message.created_at))}
      </time>
      <MessageActions
        message={message}
        mailboxAddress={reader.mailboxAddress}
        canSend={false}
        canOrganize={reader.canOrganize}
        onReply={reader.onReply}
        onClose={reader.onClose}
      />
    </div>
  );
}
