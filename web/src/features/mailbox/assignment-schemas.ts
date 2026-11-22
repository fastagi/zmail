import {
  array,
  literal,
  nullable,
  string,
  strictObject,
  union,
  type infer as Infer,
} from 'zod/mini';

import { safeIdSchema, timestampSchema } from '@/features/mailbox/schemas';

/** Whether a shared mailbox's conversation still needs work (`contracts/MAILBOXES.md`, Assignment). */
export const mailThreadStatusSchema = union([literal('open'), literal('closed')]);

export type MailThreadStatus = Infer<typeof mailThreadStatusSchema>;

/** Who works a thread of a shared mailbox, and the members it may go to. */
export const mailThreadAssignmentSchema = strictObject({
  thread_id: safeIdSchema,
  assignee_user_id: nullable(safeIdSchema),
  status: mailThreadStatusSchema,
  updated_by_user_id: nullable(safeIdSchema),
  updated_at: nullable(timestampSchema),
  assignable_user_ids: array(safeIdSchema),
});

export type MailThreadAssignment = Infer<typeof mailThreadAssignmentSchema>;

/** What replaces a thread's assignment; a null assignee unassigns it. */
export interface MailThreadAssignmentInput {
  assignee_user_id: number | null;
  status: MailThreadStatus;
}

/** An internal note on a shared mailbox's conversation (`contracts/MAILBOXES.md`, Thread Notes). */
export const mailThreadNoteSchema = strictObject({
  id: safeIdSchema,
  thread_id: safeIdSchema,
  author_user_id: nullable(safeIdSchema),
  body: string(),
  created_at: timestampSchema,
});

export type MailThreadNote = Infer<typeof mailThreadNoteSchema>;

export const mailThreadNotesSchema = strictObject({ items: array(mailThreadNoteSchema) });

/** Longest note body, in characters. */
export const MAIL_THREAD_NOTE_MAX_LENGTH = 2000;
