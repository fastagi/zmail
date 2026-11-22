'use client';

import { MessageSquareText, Trash2 } from 'lucide-react';
import { useFormatter } from 'next-intl';
import { useContext, useState, type FormEvent } from 'react';

import { Button } from '@/components/ui/button';
import { Textarea } from '@/components/ui/textarea';
import { MAIL_THREAD_NOTE_MAX_LENGTH } from '@/features/mailbox/assignment-schemas';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import {
  useAddThreadNote,
  useDeleteThreadNote,
  useThreadNotes,
} from '@/features/mailbox/hooks/use-thread-notes';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { AuthStoreContext } from '@/features/auth/store/auth-store';
import { useOrganizationMembers } from '@/features/organization';
import { useT } from '@/i18n';

/**
 * Internal notes on a shared mailbox's conversation: members leave context for each other without
 * mailing the correspondent. Members who may send add notes; each author deletes their own.
 */
export function ThreadNotes({
  mailboxId,
  threadId,
  canSend,
}: {
  mailboxId: number;
  threadId: number;
  canSend: boolean;
}) {
  const t = useT('mailbox');
  const format = useFormatter();
  const organizationId = useMailOrganizationId();
  const viewerId = useContext(AuthStoreContext)?.getState().user?.id;
  const query = useThreadNotes(organizationId, mailboxId, threadId);
  const add = useAddThreadNote(organizationId, mailboxId, threadId);
  const remove = useDeleteThreadNote(organizationId, mailboxId, threadId);
  const members = useOrganizationMembers(organizationId, query.isSuccess);
  const [draft, setDraft] = useState('');
  const notes = query.data;
  if (!notes) return null;

  const names = new Map(
    (members.data?.items ?? []).map(member => [
      member.user_id,
      member.nickname?.trim() || member.username,
    ])
  );
  const authorOf = (userId: number | null) =>
    userId === null
      ? t('notes.deletedAuthor')
      : (names.get(userId) ?? t('assignment.member', { id: userId }));
  const body = draft.trim();
  const tooLong = [...body].length > MAIL_THREAD_NOTE_MAX_LENGTH;
  const failed = add.isError ? add.error : remove.isError ? remove.error : null;

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (!body || tooLong || add.isPending) return;
    add.mutate(body, { onSuccess: () => setDraft('') });
  };

  return (
    <section
      aria-label={t('notes.label')}
      className="space-y-3 rounded-[8px] border border-dashed border-border bg-muted/30 p-3 text-sm"
    >
      <header className="flex items-center gap-2">
        <MessageSquareText className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
        <h3 className="font-medium">{t('notes.label')}</h3>
        {notes.length > 0 ? (
          <span className="text-muted-foreground">{t('notes.count', { count: notes.length })}</span>
        ) : null}
      </header>
      {notes.length === 0 ? (
        <p className="text-muted-foreground">{t('notes.empty')}</p>
      ) : (
        <ol className="space-y-2">
          {notes.map(note => (
            <li key={note.id} className="group rounded-[6px] bg-background px-3 py-2">
              <div className="flex items-center gap-2 text-xs text-muted-foreground">
                <span className="font-medium text-foreground">{authorOf(note.author_user_id)}</span>
                <time dateTime={note.created_at}>
                  {format.dateTime(new Date(note.created_at), {
                    dateStyle: 'medium',
                    timeStyle: 'short',
                  })}
                </time>
                {viewerId !== undefined && String(note.author_user_id) === viewerId ? (
                  <Button
                    type="button"
                    size="xs"
                    isIcon
                    variant="ghost"
                    className="ml-auto"
                    aria-label={t('notes.delete')}
                    title={t('notes.delete')}
                    disabled={remove.isPending}
                    onClick={() => remove.mutate(note.id)}
                  >
                    <Trash2 className="size-3.5" aria-hidden="true" />
                  </Button>
                ) : null}
              </div>
              <p className="mt-1 whitespace-pre-wrap break-words">{note.body}</p>
            </li>
          ))}
        </ol>
      )}
      {canSend ? (
        <form onSubmit={submit} className="space-y-2">
          <Textarea
            value={draft}
            rows={2}
            aria-label={t('notes.placeholder')}
            placeholder={t('notes.placeholder')}
            aria-invalid={tooLong || undefined}
            onChange={event => setDraft(event.target.value)}
            onKeyDown={event => {
              if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) submit(event);
            }}
          />
          <div className="flex items-center gap-2">
            <p className="text-xs text-muted-foreground">{t('notes.hint')}</p>
            <Button
              type="submit"
              size="sm"
              variant="outline"
              className="ml-auto"
              disabled={!body || tooLong || add.isPending}
            >
              {t('notes.add')}
            </Button>
          </div>
        </form>
      ) : (
        <p className="text-xs text-muted-foreground">{t('notes.hint')}</p>
      )}
      {failed ? (
        <p role="alert" className="text-error">
          {t(resolveMailboxErrorKey(failed))}
        </p>
      ) : null}
    </section>
  );
}
