import type {
  MailThreadAssignment,
  MailThreadAssignmentInput,
} from '@/features/mailbox/assignment-schemas';

/** Development-only thread assignments, keyed by organization, mailbox, and thread. */
class MockAssignmentStore {
  private readonly assignments = new Map<string, MailThreadAssignment>();

  reset(): void {
    this.assignments.clear();
  }

  read(organizationId: number, mailboxId: number, threadId: number, assignable: number[]) {
    const saved = this.assignments.get(key(organizationId, mailboxId, threadId));
    return {
      thread_id: threadId,
      assignee_user_id: saved?.assignee_user_id ?? null,
      status: saved?.status ?? 'open',
      updated_by_user_id: saved?.updated_by_user_id ?? null,
      updated_at: saved?.updated_at ?? null,
      assignable_user_ids: assignable,
    } satisfies MailThreadAssignment;
  }

  save(
    organizationId: number,
    mailboxId: number,
    threadId: number,
    input: MailThreadAssignmentInput,
    userId: number,
    assignable: number[]
  ): MailThreadAssignment {
    const saved: MailThreadAssignment = {
      thread_id: threadId,
      assignee_user_id: input.assignee_user_id,
      status: input.status,
      updated_by_user_id: userId,
      updated_at: new Date().toISOString(),
      assignable_user_ids: assignable,
    };
    this.assignments.set(key(organizationId, mailboxId, threadId), saved);
    return saved;
  }
}

function key(organizationId: number, mailboxId: number, threadId: number): string {
  return `${organizationId}:${mailboxId}:${threadId}`;
}

export const mockAssignmentStore = new MockAssignmentStore();
