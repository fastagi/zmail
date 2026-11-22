'use client';

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { LOCAL_ERROR_HANDLING_META } from '@/config/query-meta';
import type { MailThreadNote } from '@/features/mailbox/assignment-schemas';
import { mailboxKeys, validId } from '@/features/mailbox/hooks/mailbox-keys';
import { noteService } from '@/features/mailbox/services/note-service';

/** Cache key of a thread's notes. */
export function threadNotesKey(organizationId: number, mailboxId: number, threadId: number) {
  return [...mailboxKeys.mailbox(organizationId, mailboxId), 'notes', threadId] as const;
}

/** A shared mailbox thread's internal notes; polled so a colleague's note shows up on its own. */
export function useThreadNotes(organizationId: number, mailboxId: number, threadId: number) {
  return useQuery({
    queryKey: threadNotesKey(organizationId, mailboxId, threadId),
    queryFn: () => noteService.notes(organizationId, mailboxId, threadId),
    enabled: validId(organizationId) && validId(mailboxId) && validId(threadId),
    staleTime: 15_000,
    refetchInterval: 30_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/** Leaves a note; the saved note joins the cached list. */
export function useAddThreadNote(organizationId: number, mailboxId: number, threadId: number) {
  const queryClient = useQueryClient();
  const key = threadNotesKey(organizationId, mailboxId, threadId);
  return useMutation({
    mutationFn: (body: string) => noteService.addNote(organizationId, mailboxId, threadId, body),
    onSuccess: note => {
      queryClient.setQueryData<MailThreadNote[]>(key, notes =>
        notes?.some(item => item.id === note.id) ? notes : [...(notes ?? []), note]
      );
    },
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/** Removes one of the caller's own notes from the cached list once the API agrees. */
export function useDeleteThreadNote(organizationId: number, mailboxId: number, threadId: number) {
  const queryClient = useQueryClient();
  const key = threadNotesKey(organizationId, mailboxId, threadId);
  return useMutation({
    mutationFn: (noteId: number) =>
      noteService.deleteNote(organizationId, mailboxId, threadId, noteId),
    onSuccess: (_, noteId) => {
      queryClient.setQueryData<MailThreadNote[]>(key, notes =>
        notes?.filter(note => note.id !== noteId)
      );
    },
    meta: LOCAL_ERROR_HANDLING_META,
  });
}
