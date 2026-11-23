import type { ComposeDraft } from '@/features/mailbox/components/compose-drafts';

/**
 * What a closed compose dialog keeps in this browser; template values are not kept. Attached files
 * themselves are kept apart (see `readComposeDraftFiles` and `loadComposeDraftFiles`); their names
 * are kept here so a draft whose files this browser could not keep can say which to attach again.
 */
export interface StoredComposeDraft {
  to: string;
  cc: string;
  subject: string;
  body: string;
  attachments?: string[];
  /** The API draft this browser copy also saved as, so the next close updates it. */
  savedId?: number;
  /** Closed, but the API has not taken this text yet; it outranks the API's copy on reopen. */
  pending?: true;
}

const composeDraftPrefix = 'zmail.compose-draft';

/**
 * Where a draft's files outlive the page. The API keeps no draft attachments
 * (`contracts/MAILBOXES.md`, section Drafts: attachments stay in the browser), so this browser
 * keeps them itself.
 */
export interface ComposeDraftFileStore {
  get(key: string): Promise<File[] | null>;
  /** Resolves false when the browser refused to keep them. */
  put(key: string, files: readonly File[]): Promise<boolean>;
  remove(key: string): Promise<void>;
}

// Files are too large for localStorage: this page holds them, and IndexedDB keeps them for reloads.
const draftFiles = new Map<string, File[]>();
// The latest write of each draft's files, so a close can say whether they outlive the page.
const draftFileWrites = new Map<string, Promise<boolean>>();
let fileStore: ComposeDraftFileStore = indexedDbFileStore();

/** Replaces where draft files outlive the page, returning the one before; tests stand in for IndexedDB. */
export function setComposeDraftFileStore(store: ComposeDraftFileStore): ComposeDraftFileStore {
  const previous = fileStore;
  fileStore = store;
  return previous;
}

/** Forgets the files this page holds, as a reload does; files kept across reloads stay. */
export function forgetComposeDraftPageFiles(): void {
  draftFiles.clear();
  draftFileWrites.clear();
}

function sameNames(files: readonly File[], names: readonly string[]): boolean {
  return files.length === names.length && files.every((file, index) => file.name === names[index]);
}

/**
 * The files attached to the draft at `key` earlier in this page: empty after a reload, and once the
 * kept draft no longer lists exactly these files (it was sent, discarded, or cleared on sign-out).
 */
export function readComposeDraftFiles(key: string): File[] {
  const files = draftFiles.get(key) ?? [];
  if (!sameNames(files, readComposeDraft(key)?.attachments ?? [])) {
    draftFiles.delete(key);
    return [];
  }
  return files;
}

/**
 * The files of the draft at `key`, from this page or, after a reload, from where this browser kept
 * them; empty when it kept none or they are not exactly the files the kept draft lists.
 */
export async function loadComposeDraftFiles(key: string): Promise<File[]> {
  const page = readComposeDraftFiles(key);
  if (page.length > 0) return page;
  if ((readComposeDraft(key)?.attachments ?? []).length === 0) return [];
  let files: File[] | null;
  try {
    files = await fileStore.get(key);
  } catch {
    files = null;
  }
  // The draft may have changed while the files were read.
  if (!files || !sameNames(files, readComposeDraft(key)?.attachments ?? [])) return [];
  draftFiles.set(key, [...files]);
  return [...files];
}

/**
 * Keeps `files` with the draft at `key`, for this page and, where the browser allows, across
 * reloads; empty or null forgets them.
 */
export function writeComposeDraftFiles(key: string, files: readonly File[] | null): void {
  if (files && files.length > 0) {
    const previous = draftFiles.get(key);
    const unchanged =
      previous?.length === files.length && previous.every((file, index) => file === files[index]);
    draftFiles.set(key, [...files]);
    if (unchanged && draftFileWrites.has(key)) return;
    draftFileWrites.set(
      key,
      fileStore.put(key, files).catch(() => false)
    );
    return;
  }
  draftFiles.delete(key);
  draftFileWrites.delete(key);
  void fileStore.remove(key).catch(() => undefined);
}

/** Whether the files last written for `key` outlive this page: false when the browser refused. */
export function composeDraftFilesKept(key: string): Promise<boolean> {
  return draftFileWrites.get(key) ?? Promise.resolve(false);
}

const fileDatabase = 'zmail-compose-draft-files';
const fileTable = 'files';

/** IndexedDB, or a store that keeps nothing where the browser offers none. */
function indexedDbFileStore(): ComposeDraftFileStore {
  let opened: Promise<IDBDatabase> | null = null;
  const open = (): Promise<IDBDatabase> => {
    if (typeof indexedDB === 'undefined') return Promise.reject(new Error('no IndexedDB'));
    opened ??= new Promise<IDBDatabase>((resolve, reject) => {
      const request = indexedDB.open(fileDatabase, 1);
      request.onupgradeneeded = () => request.result.createObjectStore(fileTable);
      request.onsuccess = () => resolve(request.result);
      request.onerror = () => reject(request.error ?? new Error('IndexedDB open failed'));
    }).catch((error: unknown) => {
      opened = null;
      throw error;
    });
    return opened;
  };
  const run = async <T>(
    mode: IDBTransactionMode,
    act: (table: IDBObjectStore) => IDBRequest<T>
  ): Promise<T> => {
    const database = await open();
    return new Promise<T>((resolve, reject) => {
      const transaction = database.transaction(fileTable, mode);
      const request = act(transaction.objectStore(fileTable));
      transaction.oncomplete = () => resolve(request.result);
      transaction.onerror = () => reject(transaction.error ?? new Error('IndexedDB failed'));
      transaction.onabort = () => reject(transaction.error ?? new Error('IndexedDB aborted'));
    });
  };
  return {
    async get(key) {
      const value: unknown = await run('readonly', table => table.get(key));
      return Array.isArray(value) && value.every(file => file instanceof File)
        ? (value as File[])
        : null;
    },
    async put(key, files) {
      try {
        await run('readwrite', table => table.put([...files], key));
        return true;
      } catch {
        return false;
      }
    },
    async remove(key) {
      await run('readwrite', table => table.delete(key));
    },
  };
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
  if (draft.savedId !== undefined) {
    return `${composeDraftPrefix}.${userId}.${String(organizationId)}.saved.${String(draft.savedId)}`;
  }
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
    const { to, cc, subject, body, attachments, savedId, pending } = value as Record<
      string,
      unknown
    >;
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
    return {
      to,
      cc,
      subject,
      body,
      ...(names.length > 0 ? { attachments: names } : {}),
      ...(typeof savedId === 'number' && Number.isSafeInteger(savedId) && savedId > 0
        ? { savedId }
        : {}),
      ...(pending === true ? { pending } : {}),
    };
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
