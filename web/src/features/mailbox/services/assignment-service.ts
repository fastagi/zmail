import {
  mailThreadAssignmentSchema,
  type MailThreadAssignment,
  type MailThreadAssignmentInput,
} from '@/features/mailbox/assignment-schemas';
import { ClientErrorCode } from '@/http/codes';
import request, { ApiError } from '@/http/request';

/**
 * Browser service for thread assignment in `contracts/MAILBOXES.md` (section Assignment). Only a
 * shared Zmail mailbox assigns its threads; the API decides who may read and change them.
 */
export const assignmentService = {
  /** Who works a thread and whether it is done; an untouched thread is open and unassigned. */
  async assignment(
    organizationId: number,
    mailboxId: number,
    threadId: number
  ): Promise<MailThreadAssignment> {
    return parse(
      await request.get<unknown>(path(mailboxId, threadId), {
        headers: organizationHeaders(organizationId),
      }),
      threadId
    );
  },

  /** Replaces who works a thread and whether it is done. */
  async saveAssignment(
    organizationId: number,
    mailboxId: number,
    threadId: number,
    input: MailThreadAssignmentInput
  ): Promise<MailThreadAssignment> {
    return parse(
      await request.put<unknown, MailThreadAssignmentInput>(path(mailboxId, threadId), input, {
        headers: organizationHeaders(organizationId),
      }),
      threadId
    );
  },
};

function path(mailboxId: number, threadId: number): string {
  validId(mailboxId);
  validId(threadId);
  return `/mailboxes/${mailboxId}/threads/${threadId}/assignment`;
}

function parse(raw: unknown, threadId: number): MailThreadAssignment {
  const parsed = mailThreadAssignmentSchema.safeParse(raw);
  if (!parsed.success || parsed.data.thread_id !== threadId) throw invalidResponse();
  return parsed.data;
}

function organizationHeaders(organizationId: number): Record<string, string> {
  validId(organizationId);
  return { 'Organization-Id': String(organizationId) };
}

function validId(value: number): void {
  if (!Number.isSafeInteger(value) || value < 1) throw invalidResponse();
}

function invalidResponse(): ApiError {
  return new ApiError(
    'Thread assignment service returned an invalid response',
    ClientErrorCode.INVALID_RESPONSE
  );
}
