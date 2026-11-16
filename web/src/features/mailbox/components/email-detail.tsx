'use client';

import { useState } from 'react';
import { ArrowLeft } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import {
  ErrorAlert,
  MailHtmlFrame,
  MailStatusBadge,
  mailStatusKey,
  useDateTimeFormat,
} from '@/features/mailbox/components/mail-shared';
import { MessageActions } from '@/features/mailbox/components/message-actions';
import { useMailMessage } from '@/features/mailbox/hooks/use-mailbox';
import type { MailMessage } from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

type ContentTab = 'preview' | 'text' | 'html';

/**
 * One message of the log: its headers, its delivery status, and its content as a sandboxed
 * preview, as plain text, and as the HTML source. The source is shown as text and never parsed.
 */
export function EmailDetail({
  mailboxId,
  mailboxAddress,
  messageId,
  onClose,
  onReply,
}: {
  mailboxId: number;
  mailboxAddress: string;
  messageId: number;
  onClose: () => void;
  onReply: (message: MailMessage) => void;
}) {
  const t = useT('mailbox');
  const formatter = useDateTimeFormat();
  const query = useMailMessage(mailboxId, messageId);
  const [chosenTab, setChosenTab] = useState<ContentTab | null>(null);

  const back = (
    <Button
      type="button"
      size="sm"
      variant="ghost"
      icon={<ArrowLeft className="size-4" />}
      onClick={onClose}
    >
      {t('actions.back')}
    </Button>
  );

  if (query.isPending) {
    return (
      <div className="space-y-3">
        {back}
        <Skeleton className="h-48 w-full" />
      </div>
    );
  }
  if (query.error || !query.data) {
    return (
      <div className="space-y-3">
        {back}
        <ErrorAlert
          message={t(resolveMailboxErrorKey(query.error))}
          retryLabel={t('actions.retry')}
          onRetry={() => void query.refetch()}
        />
      </div>
    );
  }

  const message = query.data;
  const hasHtml = message.html_body !== '';
  const hasText = message.text_body !== '';
  const tab = chosenTab ?? (hasHtml ? 'preview' : 'text');

  return (
    <article className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        {back}
        <MessageActions message={message} onReply={onReply} onClose={onClose} />
      </div>

      <div className="space-y-4 rounded-md border p-4">
        <h2 className="text-lg font-semibold break-words">
          {message.subject || t('list.noSubject')}
        </h2>
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-sm">
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
          <dt className="text-muted-foreground">{t('emails.mailbox')}</dt>
          <dd className="break-words">{mailboxAddress}</dd>
          <dt className="text-muted-foreground">{t('message.date')}</dt>
          <dd>
            <time dateTime={message.created_at}>
              {formatter.format(new Date(message.created_at))}
            </time>
          </dd>
          <dt className="text-muted-foreground">{t('emails.status')}</dt>
          <dd className="flex flex-wrap items-center gap-2">
            <MailStatusBadge status={message.status} label={t(mailStatusKey[message.status])} />
            {message.status === 'failed' && message.failure_code ? (
              <code className="text-xs break-all text-muted-foreground">
                {message.failure_code}
              </code>
            ) : null}
          </dd>
        </dl>

        <Tabs value={tab} onValueChange={value => setChosenTab(value as ContentTab)}>
          <TabsList aria-label={t('emails.tabs')}>
            <TabsTrigger value="preview" disabled={!hasHtml}>
              {t('emails.preview')}
            </TabsTrigger>
            <TabsTrigger value="text" disabled={!hasText}>
              {t('message.viewText')}
            </TabsTrigger>
            <TabsTrigger value="html" disabled={!hasHtml}>
              {t('emails.html')}
            </TabsTrigger>
          </TabsList>
          <TabsContent value="preview" className="pt-2">
            {hasHtml ? (
              <MailHtmlFrame html={message.html_body} title={t('message.htmlTitle')} />
            ) : null}
          </TabsContent>
          <TabsContent value="text" className="pt-2">
            {hasText ? (
              <pre className="font-sans text-sm break-words whitespace-pre-wrap">
                {message.text_body}
              </pre>
            ) : (
              <p className="text-sm text-muted-foreground">{t('message.emptyBody')}</p>
            )}
          </TabsContent>
          <TabsContent value="html" className="pt-2">
            {hasHtml ? (
              // The source is a text node: React escapes it, so nothing from the sender is parsed.
              <pre
                aria-label={t('emails.htmlSource')}
                tabIndex={0}
                className="max-h-96 overflow-auto rounded-md border bg-muted/40 p-3 font-mono text-xs break-all whitespace-pre-wrap focus-ring"
              >
                {message.html_body}
              </pre>
            ) : null}
          </TabsContent>
        </Tabs>
      </div>
    </article>
  );
}
