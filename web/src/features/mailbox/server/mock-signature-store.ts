import 'server-only';

import type { MailSignature } from '@/features/mailbox/signature-schemas';

/**
 * Development mock of signatures (`contracts/MAILBOXES.md`, Signatures): one per member and
 * mailbox, removed when saved empty.
 */
class MockSignatureStore {
  private readonly signatures = new Map<string, { body: string; updatedAt: string }>();

  read(organizationId: number, mailboxId: number, userId: number): MailSignature {
    const saved = this.signatures.get(key(organizationId, mailboxId, userId));
    return {
      mailbox_id: mailboxId,
      body: saved?.body ?? '',
      updated_at: saved?.updatedAt ?? null,
    };
  }

  save(organizationId: number, mailboxId: number, userId: number, body: string): MailSignature {
    const id = key(organizationId, mailboxId, userId);
    if (body === '') {
      this.signatures.delete(id);
    } else {
      this.signatures.set(id, { body, updatedAt: new Date().toISOString() });
    }
    return this.read(organizationId, mailboxId, userId);
  }

  reset(): void {
    this.signatures.clear();
  }
}

function key(organizationId: number, mailboxId: number, userId: number): string {
  return `${organizationId}:${mailboxId}:${userId}`;
}

export const mockSignatureStore = new MockSignatureStore();
