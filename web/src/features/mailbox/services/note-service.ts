import {
  mailThreadNoteSchema,
  mailThreadNotesSchema,
  type MailThreadNote,
} from '@/features/mailbox/assignment-schemas';
import { ClientErrorCode } from '@/http/codes';
import request, { ApiError } from '@/http/request';

/**
 * Browser service for internal thread notes in `contracts/MAILBOXES.md` (section Thread Notes).
 * Only a shared Zmail mailbox keeps notes; they are never sent to the correspondent.
 */
export const noteService = {
  /** A thread's notes, oldest first. */
  async notes(
    organizationId: number,
    mailboxId: number,
    threadId: number
  ): Promise<MailThreadNote[]> {
    const parsed = mailThreadNotesSchema.safeParse(
      await request.get<unknown>(path(mailboxId, threadId), { headers: headers(organizationId) })
    );
    if (!parsed.success || parsed.data.items.some(note => note.thread_id !== threadId)) {
      throw invalidResponse();
    }
    return parsed.data.items;
  },

  /** Leaves a note on a thread. */
  async addNote(
    organizationId: number,
    mailboxId: number,
    threadId: number,
    body: string
  ): Promise<MailThreadNote> {
    const parsed = mailThreadNoteSchema.safeParse(
      await request.post<unknown, { body: string }>(
        path(mailboxId, threadId),
        { body },
        { headers: headers(organizationId) }
      )
    );
    if (!parsed.success || parsed.data.thread_id !== threadId) throw invalidResponse();
    return parsed.data;
  },

  /** Removes one of the caller's own notes. */
  async deleteNote(
    organizationId: number,
    mailboxId: number,
    threadId: number,
    noteId: number
  ): Promise<void> {
    validId(noteId);
    await request.delete<unknown>(`${path(mailboxId, threadId)}/${noteId}`, {
      headers: headers(organizationId),
    });
  },
};

function path(mailboxId: number, threadId: number): string {
  validId(mailboxId);
  validId(threadId);
  return `/mailboxes/${mailboxId}/threads/${threadId}/notes`;
}

function headers(organizationId: number): Record<string, string> {
  validId(organizationId);
  return { 'Organization-Id': String(organizationId) };
}

function validId(value: number): void {
  if (!Number.isSafeInteger(value) || value < 1) throw invalidResponse();
}

function invalidResponse(): ApiError {
  return new ApiError(
    'Thread note service returned an invalid response',
    ClientErrorCode.INVALID_RESPONSE
  );
}
