import type { ComposeDraft } from '@/features/mailbox/components/compose-drafts';

/** What a closed compose dialog keeps in this browser; attachments and template values are not. */
export interface StoredComposeDraft {
  to: string;
  cc: string;
  subject: string;
  body: string;
}

const composeDraftPrefix = 'zmail.compose-draft';

/**
 * Where the draft of `draft` is kept: one per organization for a new message, per answered message
 * for a reply, and per subject for a forward.
 */
export function composeDraftKey(organizationId: number, draft: ComposeDraft): string {
  const mode = draft.mode ?? (draft.reply ? 'reply' : 'compose');
  const subject = mode === 'forward' ? draft.subject : draft.inReplyTo;
  return `${composeDraftPrefix}.${String(organizationId)}.${mode}.${subject || 'new'}`;
}

/** The kept draft, or null when there is none or this browser keeps nothing. */
export function readComposeDraft(key: string): StoredComposeDraft | null {
  try {
    const raw = window.localStorage.getItem(key);
    if (raw === null) return null;
    const value: unknown = JSON.parse(raw);
    if (typeof value !== 'object' || value === null) return null;
    const { to, cc, subject, body } = value as Record<string, unknown>;
    if (
      typeof to !== 'string' ||
      typeof cc !== 'string' ||
      typeof subject !== 'string' ||
      typeof body !== 'string'
    ) {
      return null;
    }
    return { to, cc, subject, body };
  } catch {
    return null;
  }
}

/** Keeps `value` as the draft, or forgets the draft when null; a browser that refuses keeps none. */
export function writeComposeDraft(key: string, value: StoredComposeDraft | null): void {
  try {
    if (value === null) window.localStorage.removeItem(key);
    else window.localStorage.setItem(key, JSON.stringify(value));
  } catch {
    // Private windows and full storage keep no draft; closing still works.
  }
}
