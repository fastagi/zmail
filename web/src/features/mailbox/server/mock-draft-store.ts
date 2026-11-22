import 'server-only';

import type { MailDraft, MailDraftInput } from '@/features/mailbox/draft-schemas';
import { MAIL_DRAFTS_PER_MEMBER } from '@/features/mailbox/draft-schemas';

/** Development mock of drafts (`contracts/MAILBOXES.md`, Drafts): each visible to its writer. */
class MockDraftStore {
  private readonly drafts = new Map<number, MailDraft & { owner: string }>();
  private nextId = 1;

  list(organizationId: number, userId: number): MailDraft[] {
    const owner = key(organizationId, userId);
    return [...this.drafts.values()]
      .filter(draft => draft.owner === owner)
      .sort((a, b) => b.updated_at.localeCompare(a.updated_at) || b.id - a.id)
      .map(strip);
  }

  /** The saved draft; null when it is another's or gone, 'limit' at the per-member cap. */
  save(
    organizationId: number,
    userId: number,
    input: MailDraftInput,
    id: number | null
  ): MailDraft | null | 'limit' {
    const owner = key(organizationId, userId);
    const now = new Date().toISOString();
    if (id === null) {
      if (this.list(organizationId, userId).length >= MAIL_DRAFTS_PER_MEMBER) return 'limit';
      const created = { ...input, id: this.nextId++, created_at: now, updated_at: now, owner };
      this.drafts.set(created.id, created);
      return strip(created);
    }
    const current = this.drafts.get(id);
    if (current?.owner !== owner) return null;
    const updated = { ...current, ...input, updated_at: now };
    this.drafts.set(id, updated);
    return strip(updated);
  }

  remove(organizationId: number, userId: number, id: number): boolean {
    if (this.drafts.get(id)?.owner !== key(organizationId, userId)) return false;
    return this.drafts.delete(id);
  }

  reset(): void {
    this.drafts.clear();
    this.nextId = 1;
  }
}

function key(organizationId: number, userId: number): string {
  return `${organizationId}:${userId}`;
}

function strip(draft: MailDraft & { owner: string }): MailDraft {
  const { id, mailbox_id, to, cc, subject, body, in_reply_to, created_at, updated_at } = draft;
  return { id, mailbox_id, to, cc, subject, body, in_reply_to, created_at, updated_at };
}

export const mockDraftStore = new MockDraftStore();
