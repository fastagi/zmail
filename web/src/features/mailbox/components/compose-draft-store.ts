import type { ComposeDraft } from '@/features/mailbox/components/compose-drafts';

/**
 * What a closed compose dialog keeps in this browser; template values are not kept. Attached files
 * themselves stay only in this page (see `readComposeDraftFiles`); their names are kept so a draft
 * reopened after a reload can say which files to attach again.
 */
export interface StoredComposeDraft {
  to: string;
  cc: string;
  subject: string;
  body: string;
  attachments?: string[];
}

const composeDraftPrefix = 'zmail.compose-draft';

// Files are too large for localStorage, so a draft's files live only as long as this page does.
const draftFiles = new Map<string, File[]>();

/**
 * The files attached to the draft at `key` earlier in this page: empty after a reload, and once the
 * kept draft no longer lists exactly these files (it was sent, discarded, or cleared on sign-out).
 */
export function readComposeDraftFiles(key: string): File[] {
  const files = draftFiles.get(key) ?? [];
  const names = readComposeDraft(key)?.attachments ?? [];
  if (files.length !== names.length || files.some((file, index) => file.name !== names[index])) {
    draftFiles.delete(key);
    return [];
  }
  return files;
}

/** Keeps `files` with the draft at `key` for the rest of this page; empty or null forgets them. */
export function writeComposeDraftFiles(key: string, files: readonly File[] | null): void {
  if (files && files.length > 0) draftFiles.set(key, [...files]);
  else draftFiles.delete(key);
}

/**
 * Where `userId`'s draft of `draft` is kept: one per organization for a new message, per answered
 * message for a reply, and per subject for a forward. Another account signed in later in the same
 * browser never sees it.
 */
export function composeDraftKey(
  userId: string,
  organizationId: number,
  draft: ComposeDraft
): string {
  const mode = draft.mode ?? (draft.reply ? 'reply' : 'compose');
  const subject = mode === 'forward' ? draft.subject : draft.inReplyTo;
  return `${composeDraftPrefix}.${userId}.${String(organizationId)}.${mode}.${subject || 'new'}`;
}

/** The kept draft, or null when there is none or this browser keeps nothing. */
export function readComposeDraft(key: string): StoredComposeDraft | null {
  try {
    const raw = window.localStorage.getItem(key);
    if (raw === null) return null;
    const value: unknown = JSON.parse(raw);
    if (typeof value !== 'object' || value === null) return null;
    const { to, cc, subject, body, attachments } = value as Record<string, unknown>;
    if (
      typeof to !== 'string' ||
      typeof cc !== 'string' ||
      typeof subject !== 'string' ||
      typeof body !== 'string'
    ) {
      return null;
    }
    const names =
      Array.isArray(attachments) && attachments.every(name => typeof name === 'string')
        ? (attachments as string[])
        : [];
    return { to, cc, subject, body, ...(names.length > 0 ? { attachments: names } : {}) };
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

/**
 * What of `written` is worth keeping as the draft of `draft`: null when nothing is written or only
 * the starting text is.
 */
export function draftToKeep(
  written: StoredComposeDraft,
  draft: ComposeDraft
): StoredComposeDraft | null {
  const hasFiles = (written.attachments?.length ?? 0) > 0;
  const empty =
    !hasFiles &&
    [written.to, written.cc, written.subject, written.body].every(value => value.trim() === '');
  const untouched =
    !hasFiles &&
    written.to === draft.to &&
    written.cc === (draft.cc ?? '') &&
    written.subject === draft.subject &&
    written.body === (draft.body ?? '');
  return empty || untouched ? null : written;
}
