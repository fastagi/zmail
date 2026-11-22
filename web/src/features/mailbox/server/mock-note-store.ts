import type { MailThreadNote } from '@/features/mailbox/assignment-schemas';

/** Development-only thread notes, keyed by organization, mailbox, and thread. */
class MockNoteStore {
  private readonly notes = new Map<string, MailThreadNote[]>();
  private nextId = 1;

  reset(): void {
    this.notes.clear();
    this.nextId = 1;
  }

  list(organizationId: number, mailboxId: number, threadId: number): MailThreadNote[] {
    return [...(this.notes.get(key(organizationId, mailboxId, threadId)) ?? [])];
  }

  add(
    organizationId: number,
    mailboxId: number,
    threadId: number,
    authorUserId: number,
    body: string
  ): MailThreadNote | null {
    const notes = this.notes.get(key(organizationId, mailboxId, threadId)) ?? [];
    if (notes.length >= 200) return null;
    const note: MailThreadNote = {
      id: this.nextId++,
      thread_id: threadId,
      author_user_id: authorUserId,
      body,
      created_at: new Date().toISOString(),
    };
    this.notes.set(key(organizationId, mailboxId, threadId), [...notes, note]);
    return note;
  }

  remove(
    organizationId: number,
    mailboxId: number,
    threadId: number,
    noteId: number,
    authorUserId: number
  ): boolean {
    const notes = this.notes.get(key(organizationId, mailboxId, threadId)) ?? [];
    const kept = notes.filter(note => note.id !== noteId || note.author_user_id !== authorUserId);
    this.notes.set(key(organizationId, mailboxId, threadId), kept);
    return kept.length < notes.length;
  }
}

function key(organizationId: number, mailboxId: number, threadId: number): string {
  return `${organizationId}:${mailboxId}:${threadId}`;
}

export const mockNoteStore = new MockNoteStore();
